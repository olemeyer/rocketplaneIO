'use client';

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import Link from 'next/link';
import { useParams, useRouter } from 'next/navigation';
import { Spinner } from '@/components/ui';
import { useMe } from '@/components/app/me-context';
import { useClusterEvents } from '@/lib/hooks/use-cluster-events';
import { approveAction, cancelTransaction, getTransaction, listTransactionEvents, rejectAction, revertAction, revertTransaction } from '@/lib/api/controlplane';
import { LEVEL_META, actionLevelOf, levelColor } from '@/lib/approval';
import { deadlineCountdown, txnChip } from '@/lib/transactions';
import { StepTimeline } from '@/components/actions/step-timeline';
import { timeAgo } from '@/lib/format';
import type { ClusterAction, MCPTransaction, MCPTransactionEvent, OrgRole } from '@/lib/api/types';

// Transaction detail — the full audit timeline of ONE external-agent session:
// every read, every action run (embedded as a compact run card), every
// approval decision and the lifecycle (opened → committed / rolled back).
// awaiting_approval runs carry inline Approve/Reject.
// committed transactions carry a "Revert transaction" button (admin only).
// succeeded+revertible runs carry an inline Revert button (admin only).
// System transactions (no tokenName + no requestedBy) render actor as "system".

const POLL_MS = 5000;

/* ── event spine presentation ────────────────────────────────────────────── */
function eventMeta(type: string, payload?: Record<string, unknown>): { glyph: string; fg: string; label: string } {
  switch (type) {
    case 'opened': {
      // Distinguish revert-transaction opens from normal opens.
      const revertsId = payload?.revertsTransaction as string | undefined;
      return {
        glyph: '●',
        fg: 'var(--rp-ink)',
        label: revertsId ? 'revert transaction opened' : 'transaction opened',
      };
    }
    case 'read': return { glyph: '◎', fg: 'var(--rp-ink-muted)', label: 'read' };
    case 'action_created': return { glyph: '⚙', fg: 'var(--rp-ink-mid)', label: 'action' };
    case 'action_result': return { glyph: '›', fg: 'var(--rp-ink-muted)', label: 'action result' };
    case 'approval_requested': return { glyph: '◆', fg: 'var(--rp-tone-yellow-fg)', label: 'approval requested' };
    case 'approval_decided': return { glyph: '●', fg: 'var(--rp-ink-mid)', label: 'approval decided' };
    case 'committed': return { glyph: '✓', fg: 'var(--rp-ink)', label: 'committed — changes kept' };
    case 'cancel_requested': return { glyph: '⊘', fg: 'var(--rp-tone-yellow-fg)', label: 'cancel requested' };
    case 'rollback_started': return { glyph: '↺', fg: 'var(--rp-tone-yellow-fg)', label: 'rollback started' };
    case 'rollback_done': return { glyph: '↺', fg: 'var(--rp-ink-muted)', label: 'rollback done — all changes restored' };
    case 'rollback_failed': return { glyph: '▲', fg: 'var(--rp-tone-red-fg)', label: 'rollback failed' };
    case 'expired': return { glyph: '◆', fg: 'var(--rp-tone-yellow-fg)', label: 'deadline expired' };
    case 'reverted_by': return { glyph: '↺', fg: 'var(--rp-tone-yellow-fg)', label: 'reverted by' };
    default: return { glyph: '·', fg: 'var(--rp-ink-faint)', label: type.replace(/_/g, ' ') };
  }
}

// payloadSummary renders a compact, mono one-liner of the event payload,
// omitting fields rendered as dedicated UI (revertsTransaction, revertTransaction).
function payloadSummary(payload?: Record<string, unknown>): string {
  if (!payload || Object.keys(payload).length === 0) return '';
  const skip = new Set(['revertsTransaction', 'revertTransaction']);
  const entries = Object.entries(payload).filter(([k]) => !skip.has(k));
  if (entries.length === 0) return '';
  const s = entries
    .map(([k, v]) => `${k}=${typeof v === 'string' ? v : JSON.stringify(v)}`)
    .join(' · ');
  return s.length > 160 ? `${s.slice(0, 157)}…` : s;
}

/* ── actor chip ──────────────────────────────────────────────────────────── */
function ActorChip({ tokenName, requestedBy }: { tokenName?: string; requestedBy: string }) {
  const isSystem = !tokenName && !requestedBy;
  if (isSystem) {
    return (
      <span
        className="rounded-skin-chip px-1.5 py-0.5 font-mono text-[9px] uppercase tracking-[0.05em]"
        style={{ color: 'var(--rp-ink-faint)', background: 'var(--rp-tone-neutral-bg)' }}
        title="Triggered automatically by the system (alert auto-remediation or revert)"
      >
        system
      </span>
    );
  }
  return (
    <>
      {tokenName ? (
        <span>token <code className="rounded-skin-chip bg-inset px-1 py-px text-[9.5px] text-mid">{tokenName}</code></span>
      ) : null}
      {requestedBy ? <span>{requestedBy}</span> : null}
    </>
  );
}

/* ── embedded run card (compact; approve/reject inline when parked) ──────── */
type ActionTone = { fg: string; glyph: string };
const ACTION_TONE: Record<string, ActionTone> = {
  pending: { fg: 'var(--rp-ink-muted)', glyph: '◌' },
  awaiting_approval: { fg: 'var(--rp-tone-yellow-fg)', glyph: '◆' },
  running: { fg: 'var(--rp-ink-mid)', glyph: '◌' },
  succeeded: { fg: 'var(--rp-tone-green-fg)', glyph: '●' },
  failed: { fg: 'var(--rp-tone-red-fg)', glyph: '▲' },
  cancelled: { fg: 'var(--rp-tone-yellow-fg)', glyph: '⊘' },
};

function TxnActionCard({
  a,
  clusterId,
  isAdmin,
  onDecide,
  onRevert,
}: {
  a: ClusterAction;
  clusterId: string;
  isAdmin: boolean;
  onDecide: (actionId: string, approve: boolean) => Promise<void>;
  onRevert: (actionId: string) => Promise<void>;
}) {
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');
  const [confirmRevert, setConfirmRevert] = useState(false);
  const tone = ACTION_TONE[a.status] ?? { fg: 'var(--rp-ink-muted)', glyph: '○' };
  const lvl = actionLevelOf(a.kind, (a.params as Record<string, unknown>) ?? {});
  const awaiting = a.status === 'awaiting_approval';
  const running = a.status === 'pending' || a.status === 'running';
  const canRevert = isAdmin && a.status === 'succeeded' && !!a.revertible;
  const steps = a.steps ?? [];

  const decide = async (approve: boolean) => {
    setBusy(true); setErr('');
    try {
      await onDecide(a.id, approve);
    } catch (e) {
      setErr(e instanceof Error ? e.message : 'decision failed');
    } finally {
      setBusy(false);
    }
  };

  const doRevert = async () => {
    setBusy(true); setErr('');
    try {
      await onRevert(a.id);
      setConfirmRevert(false);
    } catch (e) {
      setErr(e instanceof Error ? e.message : 'revert failed');
    } finally {
      setBusy(false);
    }
  };

  return (
    <div
      className="mt-1.5 overflow-hidden rounded-skin-sm border bg-raised"
      style={{
        borderColor: awaiting ? 'color-mix(in oklab, var(--rp-tone-yellow-fg) 45%, var(--rp-line))' : 'var(--rp-line)',
        boxShadow: 'var(--rp-rim)',
      }}
    >
      <div className="flex flex-wrap items-center gap-x-2.5 gap-y-1 px-2.5 py-1.5 font-mono text-[10.5px]">
        <span className="flex shrink-0 items-center gap-1.5">
          <span className={running ? 'rp-breath' : ''} style={{ color: tone.fg }} aria-hidden>{tone.glyph}</span>
          <span className="text-[9px] uppercase tracking-[0.06em]" style={{ color: tone.fg }}>{a.status.replace(/_/g, ' ')}</span>
        </span>
        <span className="min-w-0 flex-1 truncate">
          <span className="font-semibold text-ink">{a.kind.replace(/_/g, ' ')}</span>
          <span className="text-faint"> · </span>
          <span className="text-mid">{a.targetNamespace && a.targetNamespace !== '-' ? `${a.targetNamespace}/` : ''}{a.targetKind}/{a.targetName}</span>
        </span>
        <span className="flex shrink-0 items-center gap-1 text-[9px] uppercase tracking-[0.05em]" style={{ color: levelColor(lvl) }} title={LEVEL_META[lvl].hint}>
          {LEVEL_META[lvl].glyph} {LEVEL_META[lvl].label}
        </span>
        {canRevert ? (
          confirmRevert ? (
            <span className="flex items-center gap-1">
              <span className="text-[9.5px]" style={{ color: 'var(--rp-tone-yellow-fg)' }}>undo this run?</span>
              <button
                type="button"
                disabled={busy}
                onClick={doRevert}
                className="rp-focus rounded-skin-chip px-2 py-0.5 font-mono text-[9.5px] font-semibold transition-opacity hover:opacity-90 disabled:opacity-40"
                style={{ background: 'var(--rp-node-warn)', color: 'var(--rp-btn-fg)' }}
              >
                {busy ? '…' : 'Revert'}
              </button>
              <button
                type="button"
                disabled={busy}
                onClick={() => setConfirmRevert(false)}
                className="rp-focus rounded-skin-chip border border-line px-2 py-0.5 font-mono text-[9.5px] text-mid transition-colors hover:bg-hover"
              >
                Keep
              </button>
            </span>
          ) : (
            <button
              type="button"
              onClick={() => setConfirmRevert(true)}
              className="rp-focus shrink-0 rounded-skin-chip border px-2 py-0.5 font-mono text-[9.5px] transition-colors hover:bg-hover"
              style={{ borderColor: 'var(--rp-line)', color: 'var(--rp-ink-muted)' }}
              title="Undo this run via snapshot restore"
            >
              ↺ Revert run
            </button>
          )
        ) : null}
      </div>

      {Object.keys(a.params ?? {}).length ? (
        <div className="break-all border-t border-line bg-inset px-2.5 py-1 font-mono text-[9.5px] text-muted">
          {JSON.stringify(a.params)}
        </div>
      ) : null}

      {steps.length > 0 ? (
        <div className="border-t border-line px-2.5 py-1.5">
          <StepTimeline steps={steps} className="font-mono text-[10px]" />
        </div>
      ) : null}

      {a.result ? (
        <div className="break-all border-t border-line px-2.5 py-1.5 font-mono text-[10px] leading-snug" style={{ color: a.status === 'failed' ? 'var(--rp-tone-red-fg)' : 'var(--rp-ink-muted)' }}>
          {a.result}
        </div>
      ) : null}

      {err ? (
        <div className="border-t border-line px-2.5 py-1 font-mono text-[10px]" style={{ color: 'var(--rp-tone-red-fg)' }}>{err}</div>
      ) : null}

      {awaiting ? (
        <div className="flex flex-wrap items-center gap-2 border-t border-line px-2.5 py-1.5">
          <span className="font-mono text-[10px]" style={{ color: 'var(--rp-tone-yellow-fg)' }}>
            parked — a human must decide before this runs
          </span>
          {isAdmin ? (
            <span className="ml-auto flex items-center gap-1.5">
              <button
                type="button"
                disabled={busy}
                onClick={() => decide(false)}
                className="rp-focus rounded-skin-sm border px-2.5 py-1 font-mono text-[10.5px] transition-colors hover:bg-hover disabled:opacity-40"
                style={{ borderColor: 'color-mix(in oklab, var(--rp-tone-red-fg) 45%, var(--rp-line))', color: 'var(--rp-tone-red-fg)' }}
              >
                Reject
              </button>
              <button
                type="button"
                disabled={busy}
                onClick={() => decide(true)}
                className="rp-focus rounded-skin-sm px-2.5 py-1 font-mono text-[10.5px] font-semibold transition-opacity hover:opacity-90 disabled:opacity-40"
                style={{ background: 'var(--rp-btn-bg)', color: 'var(--rp-btn-fg)' }}
              >
                {busy ? 'deciding…' : 'Approve'}
              </button>
            </span>
          ) : (
            <span className="ml-auto font-mono text-[10px] text-faint">admin approval required</span>
          )}
        </div>
      ) : null}
    </div>
  );
}

/* ── rollback state banner ───────────────────────────────────────────────── */
function RollbackBanner({ t, clusterId }: { t: MCPTransaction; clusterId: string }) {
  if (t.status === 'cancelling') {
    return (
      <div className="flex items-center gap-2 rounded-skin-sm border px-3 py-2 font-mono text-[11px]" style={{ borderColor: 'color-mix(in oklab, var(--rp-tone-yellow-fg) 45%, var(--rp-line))', color: 'var(--rp-tone-yellow-fg)', background: 'var(--rp-tone-yellow-bg)' }}>
        <span className="rp-breath" aria-hidden>◆</span>
        rolling back — every change this agent made restores from its snapshot…
      </div>
    );
  }
  if (t.status === 'rolled_back') {
    return (
      <div className="flex items-center gap-2 rounded-skin-sm border border-line px-3 py-2 font-mono text-[11px] text-muted" style={{ background: 'var(--rp-tone-neutral-bg)' }}>
        <span aria-hidden>↺</span>
        all changes restored — the cluster is back to its pre-transaction state{t.closeReason === 'expired' ? ' (deadline expired)' : ''}.
      </div>
    );
  }
  if (t.status === 'rollback_failed') {
    return (
      <div className="flex flex-wrap items-center gap-2 rounded-skin-sm border px-3 py-2 font-mono text-[11px]" style={{ borderColor: 'color-mix(in oklab, var(--rp-tone-red-fg) 45%, var(--rp-line))', color: 'var(--rp-tone-red-fg)', background: 'var(--rp-tone-red-bg)' }}>
        <span aria-hidden>▲</span>
        rollback failed — some changes may still be live. Inspect the restore run in the
        <Link href={`/clusters/${clusterId}/transactions`} className="rp-focus underline decoration-dotted underline-offset-2 transition-opacity hover:opacity-80">
          transactions list ›
        </Link>
      </div>
    );
  }
  return null;
}

/* ── page ────────────────────────────────────────────────────────────────── */
export default function TransactionDetailPage() {
  const params = useParams<{ id: string; txnId: string }>();
  const clusterId = params.id;
  const txnId = params.txnId;
  const router = useRouter();
  const { currentOrg } = useMe();
  const orgId = currentOrg?.id;
  const role = (currentOrg?.role ?? 'member') as OrgRole;
  const isAdmin = role === 'admin' || role === 'owner';

  const [txn, setTxn] = useState<MCPTransaction | null>(null);
  const [events, setEvents] = useState<MCPTransactionEvent[]>([]);
  const [notFound, setNotFound] = useState(false);
  const [now, setNow] = useState(() => Date.now());
  const [confirmCancel, setConfirmCancel] = useState(false);
  const [cancelBusy, setCancelBusy] = useState(false);
  const [confirmRevertTxn, setConfirmRevertTxn] = useState(false);
  const [revertTxnBusy, setRevertTxnBusy] = useState(false);
  const [revertTxnErr, setRevertTxnErr] = useState('');
  const lastSeqRef = useRef(0);

  const load = useCallback(async () => {
    if (!orgId) return;
    try {
      const [t, fresh] = await Promise.all([
        getTransaction(orgId, clusterId, txnId),
        // Resume the timeline from the last seen seq (exclusive).
        listTransactionEvents(orgId, clusterId, txnId, lastSeqRef.current),
      ]);
      setTxn(t);
      if (fresh.length > 0) {
        lastSeqRef.current = Math.max(lastSeqRef.current, fresh[fresh.length - 1]!.seq);
        // Dedupe by seq — overlapping refetches (SSE burst + poll) must not
        // append the same event twice.
        setEvents((cur) => {
          const seen = new Set(cur.map((e) => e.seq));
          const add = fresh.filter((e) => !seen.has(e.seq));
          return add.length ? [...cur, ...add] : cur;
        });
      }
    } catch {
      setNotFound(true);
    }
  }, [orgId, clusterId, txnId]);

  const loadRef = useRef(load);
  loadRef.current = load;
  const { live } = useClusterEvents(orgId, clusterId, {
    transactions: () => void loadRef.current(),
    actions: () => void loadRef.current(),
  });

  useEffect(() => { void load(); }, [load]);

  // Poll while the transaction can still change (open/cancelling); rare
  // fallback poll otherwise when the SSE stream is down.
  const activeStatus = txn?.status === 'open' || txn?.status === 'cancelling';
  useEffect(() => {
    if (!activeStatus && live) return;
    const t = setInterval(() => void loadRef.current(), activeStatus ? POLL_MS : 20_000);
    return () => clearInterval(t);
  }, [activeStatus, live]);

  // 1s deadline tick while open.
  useEffect(() => {
    if (txn?.status !== 'open') return;
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, [txn?.status]);

  const actionsById = useMemo(() => {
    const m = new Map<string, ClusterAction>();
    for (const a of txn?.actions ?? []) m.set(a.id, a);
    return m;
  }, [txn?.actions]);

  const decide = useCallback(async (actionId: string, approve: boolean) => {
    if (!orgId) return;
    await (approve ? approveAction(orgId, clusterId, actionId) : rejectAction(orgId, clusterId, actionId));
    await loadRef.current();
  }, [orgId, clusterId]);

  const revertRun = useCallback(async (actionId: string) => {
    if (!orgId) return;
    await revertAction(orgId, clusterId, actionId);
    await loadRef.current();
  }, [orgId, clusterId]);

  const doRevertTransaction = async () => {
    if (!orgId) return;
    setRevertTxnBusy(true);
    setRevertTxnErr('');
    try {
      const revertTxn = await revertTransaction(orgId, clusterId, txnId);
      router.push(`/clusters/${clusterId}/transactions/${revertTxn.id}`);
    } catch (e) {
      setRevertTxnErr(e instanceof Error ? e.message : 'revert failed');
      setRevertTxnBusy(false);
      setConfirmRevertTxn(false);
    }
  };

  if (notFound) {
    return (
      <div className="flex h-[calc(100dvh-52px)] flex-col items-center justify-center gap-3">
        <span className="font-mono text-[12px] text-muted">Transaction not found.</span>
        <button type="button" onClick={() => router.push(`/clusters/${clusterId}/transactions`)} className="rp-focus h-9 rounded-skin-sm border border-line px-4 font-mono text-[11.5px] text-ink transition-colors hover:bg-hover">
          Back to transactions
        </button>
      </div>
    );
  }
  if (!txn) {
    return <div className="flex h-64 items-center justify-center text-muted"><Spinner /></div>;
  }

  const chip = txnChip(txn.status);
  const isSystem = !txn.tokenName && !txn.requestedBy;

  return (
    <div className="flex h-[calc(100dvh-52px)] flex-col px-4 pt-4 sm:px-5">
      {/* Header */}
      <header className="shrink-0">
        <Link href={`/clusters/${clusterId}/transactions`} className="rp-focus font-mono text-[10.5px] text-faint transition-colors hover:text-ink">
          ← transactions
        </Link>
        <div className="rp-keyline mt-2 flex flex-wrap items-end justify-between gap-x-4 gap-y-2 pb-3">
          <div className="min-w-0">
            <h1 className="break-words font-display text-[21px] font-bold leading-tight tracking-tightest text-ink sm:truncate sm:text-[24px] sm:leading-none">
              {txn.title}
            </h1>
            <div className="mt-1.5 flex flex-wrap items-center gap-x-2.5 gap-y-1 font-mono text-[10px] text-muted">
              <ActorChip tokenName={txn.tokenName} requestedBy={txn.requestedBy} />
              {!isSystem ? <span className="text-faint">·</span> : null}
              <span className="tnum">opened {timeAgo(txn.createdAt)}</span>
              {txn.closedAt ? (
                <>
                  <span className="text-faint">·</span>
                  <span className="tnum">closed {timeAgo(txn.closedAt)}{txn.closeReason ? ` (${txn.closeReason})` : ''}</span>
                </>
              ) : null}
              {txn.status === 'open' ? (
                <>
                  <span className="text-faint">·</span>
                  <span>expires in <span className="text-ink tnum">{deadlineCountdown(txn.deadline, now)}</span> — then everything rolls back</span>
                </>
              ) : null}
              {txn.incidentId ? (
                <Link href={`/clusters/${clusterId}/incidents/${txn.incidentId}`} className="rp-focus text-mid underline decoration-dotted underline-offset-2 transition-colors hover:text-ink">
                  linked incident ›
                </Link>
              ) : null}
            </div>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <span className="inline-flex items-center gap-1 rounded-skin-chip px-1.5 py-0.5 font-mono text-[10px] uppercase tracking-[0.04em]" style={{ color: chip.fg, background: chip.bg }}>
              <span className={chip.pulse ? 'rp-breath' : ''} aria-hidden>{chip.glyph}</span>
              {chip.label}
            </span>
            {isAdmin && txn.status === 'open' ? (
              confirmCancel ? (
                <span className="flex items-center gap-1.5">
                  <span className="font-mono text-[10px]" style={{ color: 'var(--rp-tone-red-fg)' }}>roll back everything?</span>
                  <button
                    type="button"
                    disabled={cancelBusy}
                    onClick={async () => {
                      if (!orgId) return;
                      setCancelBusy(true);
                      try { await cancelTransaction(orgId, clusterId, txnId); await loadRef.current(); } catch { /* surfaced via refetch */ }
                      setCancelBusy(false);
                      setConfirmCancel(false);
                    }}
                    className="rp-focus h-8 rounded-skin-sm px-3 font-mono text-[11px] font-semibold transition-opacity hover:opacity-90 disabled:opacity-40"
                    style={{ background: 'var(--rp-node-crit)', color: 'var(--rp-btn-fg)' }}
                  >
                    {cancelBusy ? 'cancelling…' : 'Yes, roll back'}
                  </button>
                  <button type="button" disabled={cancelBusy} onClick={() => setConfirmCancel(false)} className="rp-focus h-8 rounded-skin-sm border border-line px-3 font-mono text-[11px] text-mid transition-colors hover:bg-hover">
                    Keep open
                  </button>
                </span>
              ) : (
                <button
                  type="button"
                  onClick={() => setConfirmCancel(true)}
                  className="rp-focus h-8 rounded-skin-sm border px-3 font-mono text-[11px] transition-colors hover:bg-hover"
                  style={{ borderColor: 'color-mix(in oklab, var(--rp-tone-red-fg) 45%, var(--rp-line))', color: 'var(--rp-tone-red-fg)' }}
                  title="Kill switch: close the agent's transaction and restore every change from its snapshot"
                >
                  Cancel &amp; roll back
                </button>
              )
            ) : null}
            {isAdmin && txn.status === 'committed' ? (
              confirmRevertTxn ? (
                <span className="flex items-center gap-1.5">
                  <span className="font-mono text-[10px]" style={{ color: 'var(--rp-tone-yellow-fg)' }}>undo all changes?</span>
                  {revertTxnErr ? <span className="font-mono text-[10px]" style={{ color: 'var(--rp-tone-red-fg)' }}>{revertTxnErr}</span> : null}
                  <button
                    type="button"
                    disabled={revertTxnBusy}
                    onClick={doRevertTransaction}
                    className="rp-focus h-8 rounded-skin-sm px-3 font-mono text-[11px] font-semibold transition-opacity hover:opacity-90 disabled:opacity-40"
                    style={{ background: 'var(--rp-node-warn)', color: 'var(--rp-btn-fg)' }}
                  >
                    {revertTxnBusy ? 'reverting…' : 'Yes, revert'}
                  </button>
                  <button type="button" disabled={revertTxnBusy} onClick={() => { setConfirmRevertTxn(false); setRevertTxnErr(''); }} className="rp-focus h-8 rounded-skin-sm border border-line px-3 font-mono text-[11px] text-mid transition-colors hover:bg-hover">
                    Keep
                  </button>
                </span>
              ) : (
                <button
                  type="button"
                  onClick={() => setConfirmRevertTxn(true)}
                  className="rp-focus h-8 rounded-skin-sm border px-3 font-mono text-[11px] transition-colors hover:bg-hover"
                  style={{ borderColor: 'color-mix(in oklab, var(--rp-tone-yellow-fg) 45%, var(--rp-line))', color: 'var(--rp-tone-yellow-fg)' }}
                  title="Undo all committed changes via snapshot restore — opens a new revert transaction"
                >
                  ↺ Revert transaction
                </button>
              )
            ) : null}
          </div>
        </div>
      </header>

      <div className="mt-3 min-h-0 flex-1 space-y-4 overflow-y-auto pb-8">
        <div className="max-w-[760px]">
          <RollbackBanner t={txn} clusterId={clusterId} />
        </div>

        {/* Timeline: events are the spine; action_created embeds the run card */}
        <section className="max-w-[760px]">
          <div className="rp-micro pb-2 text-faint">timeline · {events.length} events</div>
          {events.length === 0 ? (
            <div className="font-mono text-[11px] text-faint">No events yet.</div>
          ) : (
            <ol className="relative">
              {events.map((e, i) => {
                const m = eventMeta(e.type, e.payload);
                const last = i === events.length - 1;
                const action = e.type === 'action_created' && e.actionId ? actionsById.get(e.actionId) : undefined;
                const summary = e.type === 'action_created' ? '' : payloadSummary(e.payload);

                // For reverted_by events: link to the revert transaction.
                const revertTxnId = e.type === 'reverted_by' ? (e.payload?.revertTransaction as string | undefined) : undefined;
                // For opened events from tool=revert_transaction: link back to original.
                const revertsId = e.type === 'opened' ? (e.payload?.revertsTransaction as string | undefined) : undefined;

                return (
                  <li key={e.seq} className="relative flex gap-3 pb-3">
                    {!last ? <span className="absolute left-[9px] top-5 h-full w-px bg-line" aria-hidden /> : null}
                    <span className="relative z-10 mt-0.5 grid h-[19px] w-[19px] shrink-0 place-items-center rounded-full border border-line bg-raised font-mono text-[10px]" style={{ color: m.fg }} aria-hidden>
                      {m.glyph}
                    </span>
                    <div className="min-w-0 flex-1">
                      <div className="flex items-baseline justify-between gap-2">
                        <span className="min-w-0 truncate font-mono text-[11.5px] text-ink">
                          {m.label}
                          {e.tool && e.type !== 'opened' ? <span className="text-faint"> · </span> : null}
                          {e.tool && e.type !== 'opened' ? <code className="rounded-skin-chip bg-inset px-1 py-px text-[10px] text-mid">{e.tool}</code> : null}
                        </span>
                        <span className="shrink-0 font-mono text-[10px] tabular-nums text-faint">{timeAgo(e.at)}</span>
                      </div>
                      {/* revert_transaction opened: link back to the original transaction */}
                      {revertsId ? (
                        <div className="mt-0.5 font-mono text-[10px] text-muted">
                          reverting{' '}
                          <Link href={`/clusters/${clusterId}/transactions/${revertsId}`} className="rp-focus text-mid underline decoration-dotted underline-offset-2 transition-colors hover:text-ink">
                            transaction {revertsId.slice(0, 8)}… ›
                          </Link>
                        </div>
                      ) : null}
                      {/* reverted_by: link to the revert transaction */}
                      {revertTxnId ? (
                        <div className="mt-0.5 font-mono text-[10px] text-muted">
                          <Link href={`/clusters/${clusterId}/transactions/${revertTxnId}`} className="rp-focus text-mid underline decoration-dotted underline-offset-2 transition-colors hover:text-ink">
                            view revert transaction {revertTxnId.slice(0, 8)}… ›
                          </Link>
                        </div>
                      ) : null}
                      {summary ? (
                        <div className="mt-0.5 break-all font-mono text-[10px] leading-snug text-muted" title={JSON.stringify(e.payload)}>
                          {summary}
                        </div>
                      ) : null}
                      {action ? (
                        <TxnActionCard a={action} clusterId={clusterId} isAdmin={isAdmin} onDecide={decide} onRevert={revertRun} />
                      ) : e.type === 'action_created' && e.actionId ? (
                        <div className="mt-0.5 font-mono text-[10px] text-faint">run {e.actionId.slice(0, 8)}… (no longer part of this transaction)</div>
                      ) : null}
                    </div>
                  </li>
                );
              })}
            </ol>
          )}
        </section>
      </div>
    </div>
  );
}
