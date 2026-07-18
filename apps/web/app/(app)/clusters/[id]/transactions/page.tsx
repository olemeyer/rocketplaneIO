'use client';

import { useEffect, useMemo, useRef, useState } from 'react';
import Link from 'next/link';
import { useParams } from 'next/navigation';
import { cn } from '@/lib/cn';
import { Spinner } from '@/components/ui';
import { useMe } from '@/components/app/me-context';
import { PageHeader } from '@/components/app/page-header';
import { useClusterEvents } from '@/lib/hooks/use-cluster-events';
import { approveAction, getTransaction, listTransactions, rejectAction } from '@/lib/api/controlplane';
import { LEVEL_META, actionLevelOf, levelColor } from '@/lib/approval';
import { deadlineCountdown, txnChip } from '@/lib/transactions';
import type { ClusterAction, MCPTransaction, OrgRole } from '@/lib/api/types';

// Transactions — the audit surface for EXTERNAL AI agents (MCP): every agent
// session that may mutate the cluster is one transaction. The page leads with
// the single most important interaction: parked runs awaiting a human
// approve/reject decision. Below it, the ledger of all transactions.

const POLL_MS = 5000;
const DETAIL_CAP = 40; // list rows we enrich with member actions (count + approvals)

function relTime(iso: string): string {
  const t = Date.parse(iso);
  if (!t) return '';
  const s = Math.max(0, (Date.now() - t) / 1000);
  if (s < 60) return `${Math.floor(s)}s ago`;
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86400)}d ago`;
}

type PendingApproval = { action: ClusterAction; txn: MCPTransaction };

/* ── pending approvals — the reason an SRE lands here from a notification ── */
function ApprovalBanner({
  pending,
  isAdmin,
  clusterId,
  onDecide,
}: {
  pending: PendingApproval[];
  isAdmin: boolean;
  clusterId: string;
  onDecide: (actionId: string, approve: boolean) => Promise<void>;
}) {
  const [busy, setBusy] = useState<Set<string>>(new Set());
  const [errs, setErrs] = useState<Record<string, string>>({});
  if (pending.length === 0) return null;

  const decide = async (id: string, approve: boolean) => {
    setBusy((b) => new Set(b).add(id));
    setErrs((e) => ({ ...e, [id]: '' }));
    try {
      await onDecide(id, approve);
    } catch (e) {
      setErrs((prev) => ({ ...prev, [id]: e instanceof Error ? e.message : 'decision failed' }));
    } finally {
      setBusy((b) => { const n = new Set(b); n.delete(id); return n; });
    }
  };

  return (
    <section
      className="shrink-0 rounded-skin border bg-raised"
      style={{ borderColor: 'color-mix(in oklab, var(--rp-tone-yellow-fg) 45%, var(--rp-line))', boxShadow: 'var(--rp-rim)' }}
    >
      <div className="flex items-center gap-2 border-b border-line px-3 py-2">
        <span aria-hidden style={{ color: 'var(--rp-tone-yellow-fg)' }}>◆</span>
        <span className="font-mono text-[11px] font-semibold text-ink">
          {pending.length === 1 ? '1 action awaits your approval' : `${pending.length} actions await your approval`}
        </span>
        <span className="ml-auto font-mono text-[9.5px] text-faint">an external agent proposed these — nothing runs until a human decides</span>
      </div>
      <div className="divide-y divide-line">
        {pending.map(({ action: a, txn }) => {
          const lvl = actionLevelOf(a.kind, (a.params as Record<string, unknown>) ?? {});
          const isBusy = busy.has(a.id);
          return (
            <div key={a.id} className="flex flex-wrap items-center gap-x-3 gap-y-1.5 px-3 py-2 font-mono text-[11px]">
              <span className="flex w-[96px] shrink-0 items-center gap-1 text-[9.5px] uppercase tracking-[0.05em]" style={{ color: levelColor(lvl) }} title={LEVEL_META[lvl].hint}>
                {LEVEL_META[lvl].glyph} {LEVEL_META[lvl].label}
              </span>
              <span className="min-w-0 flex-1 basis-[220px] truncate">
                <span className="font-semibold text-ink">{a.kind.replace(/_/g, ' ')}</span>
                <span className="text-faint"> · </span>
                <span className="text-mid">{a.targetNamespace && a.targetNamespace !== '-' ? `${a.targetNamespace}/` : ''}{a.targetKind}/{a.targetName}</span>
              </span>
              <Link href={`/clusters/${clusterId}/transactions/${txn.id}`} className="rp-focus hidden max-w-[220px] shrink-0 truncate text-[10px] text-muted transition-colors hover:text-ink md:block" title={txn.title}>
                {txn.title}
              </Link>
              <span className="hidden shrink-0 text-[10px] text-faint lg:block">{txn.tokenName ? `token ${txn.tokenName}` : txn.requestedBy}</span>
              {errs[a.id] ? <span className="basis-full text-[10px]" style={{ color: 'var(--rp-tone-red-fg)' }}>{errs[a.id]}</span> : null}
              {isAdmin ? (
                <span className="ml-auto flex shrink-0 items-center gap-1.5">
                  <button
                    type="button"
                    disabled={isBusy}
                    onClick={() => decide(a.id, false)}
                    className="rp-focus rounded-skin-sm border px-2.5 py-1 font-mono text-[10.5px] transition-colors hover:bg-hover disabled:opacity-40"
                    style={{ borderColor: 'color-mix(in oklab, var(--rp-tone-red-fg) 45%, var(--rp-line))', color: 'var(--rp-tone-red-fg)' }}
                    title="Reject — the run is cancelled, nothing executes"
                  >
                    Reject
                  </button>
                  <button
                    type="button"
                    disabled={isBusy}
                    onClick={() => decide(a.id, true)}
                    className="rp-focus rounded-skin-sm px-2.5 py-1 font-mono text-[10.5px] font-semibold transition-opacity hover:opacity-90 disabled:opacity-40"
                    style={{ background: 'var(--rp-btn-bg)', color: 'var(--rp-btn-fg)' }}
                    title="Approve — the run executes inside the transaction (still rolls back on cancel/expiry)"
                  >
                    {isBusy ? 'deciding…' : 'Approve'}
                  </button>
                </span>
              ) : (
                <span className="ml-auto shrink-0 text-[10px] text-faint">admin approval required</span>
              )}
            </div>
          );
        })}
      </div>
    </section>
  );
}

/* ── one transaction row ─────────────────────────────────────────────────── */
function TxnRow({ t, clusterId, now }: { t: MCPTransaction; clusterId: string; now: number }) {
  const chip = txnChip(t.status);
  const liveish = t.status === 'open' || t.status === 'cancelling';
  const count = t.actions?.length;
  const awaiting = (t.actions ?? []).filter((a) => a.status === 'awaiting_approval').length;
  return (
    <Link
      href={`/clusters/${clusterId}/transactions/${t.id}`}
      className="rp-focus flex items-center gap-2.5 rounded-skin-sm border border-line bg-raised px-2.5 py-2 font-mono text-[11px] transition-colors hover:border-line-strong"
    >
      <span className="w-[3px] self-stretch rounded-full" style={{ background: chip.fg, opacity: liveish ? 0.9 : 0.5 }} aria-hidden />
      <span className="flex w-[118px] shrink-0 items-center gap-1.5">
        <span className={chip.pulse ? 'rp-breath' : ''} style={{ color: chip.fg }} aria-hidden>{chip.glyph}</span>
        <span className="text-[9px] uppercase tracking-[0.06em]" style={{ color: chip.fg }}>{chip.label}</span>
      </span>
      <span className="min-w-0 flex-1">
        <span className="block truncate font-semibold text-ink">{t.title}</span>
        <span className="block truncate text-[9.5px] text-faint">
          {t.closeReason ? `closed · ${t.closeReason}` : t.status === 'open' ? 'agent session active' : ''}
          {awaiting > 0 ? <span style={{ color: 'var(--rp-tone-yellow-fg)' }}>{t.closeReason || t.status === 'open' ? ' · ' : ''}◆ {awaiting} awaiting approval</span> : null}
        </span>
      </span>
      <span className="hidden w-[110px] shrink-0 truncate text-[10px] text-muted md:block" title={t.tokenName ? `token: ${t.tokenName}` : undefined}>
        {t.tokenName || ((!t.requestedBy) ? (
          <span className="rounded-skin-chip bg-inset px-1.5 py-0.5 text-[9px] uppercase tracking-[0.05em] text-faint">system</span>
        ) : '—')}
      </span>
      <span className="hidden w-[150px] shrink-0 truncate text-[10px] text-faint lg:block" title={t.requestedBy}>{t.requestedBy || '—'}</span>
      <span className="w-[64px] shrink-0 text-right text-[10px] text-muted tnum">
        {count == null ? '—' : `${count} ${count === 1 ? 'run' : 'runs'}`}
      </span>
      <span className="hidden w-[84px] shrink-0 text-right text-[10px] tnum sm:block" style={{ color: t.status === 'open' ? 'var(--rp-ink)' : 'var(--rp-ink-faint)' }}>
        {t.status === 'open' ? deadlineCountdown(t.deadline, now) : ''}
      </span>
      <span className="w-[58px] shrink-0 text-right text-[10px] text-faint tnum">{relTime(t.createdAt)}</span>
      <span className="shrink-0 text-faint" aria-hidden>›</span>
    </Link>
  );
}

/* ── page ────────────────────────────────────────────────────────────────── */
export default function TransactionsPage() {
  const params = useParams<{ id: string }>();
  const clusterId = params.id;
  const { currentOrg } = useMe();
  const orgId = currentOrg?.id;
  const role = (currentOrg?.role ?? 'member') as OrgRole;
  const isAdmin = role === 'admin' || role === 'owner';

  const [txns, setTxns] = useState<MCPTransaction[] | null>(null);
  const [now, setNow] = useState(() => Date.now());

  // SSE-driven (transactions/actions signal → refetch), poll as fallback.
  const pollRef = useRef<() => void>(() => {});
  const { live } = useClusterEvents(orgId, clusterId, {
    transactions: () => pollRef.current(),
    actions: () => pollRef.current(),
  });

  useEffect(() => {
    if (!orgId) return;
    let alive = true;
    const poll = async () => {
      try {
        const list = await listTransactions(orgId, clusterId, 100);
        // Enrich the newest rows with their member runs (action count + parked
        // approvals). MCP transactions are low-volume; a bounded fan-out is fine.
        const detailed = await Promise.all(
          list.slice(0, DETAIL_CAP).map((t) =>
            getTransaction(orgId, clusterId, t.id).catch(() => t),
          ),
        );
        if (alive) setTxns([...detailed, ...list.slice(DETAIL_CAP)]);
      } catch {
        /* keep the last state */
      }
    };
    pollRef.current = () => void poll();
    void poll();
    const t = setInterval(poll, live ? 20_000 : POLL_MS);
    return () => { alive = false; clearInterval(t); };
  }, [orgId, clusterId, live]);

  // 1s tick only while an open transaction shows a deadline countdown.
  const hasOpen = useMemo(() => (txns ?? []).some((t) => t.status === 'open' || t.status === 'cancelling'), [txns]);
  useEffect(() => {
    if (!hasOpen) return;
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, [hasOpen]);

  const pending = useMemo<PendingApproval[]>(() => {
    const out: PendingApproval[] = [];
    for (const t of txns ?? []) {
      if (t.status !== 'open') continue;
      for (const a of t.actions ?? []) {
        if (a.status === 'awaiting_approval') out.push({ action: a, txn: t });
      }
    }
    return out;
  }, [txns]);

  const openCount = useMemo(() => (txns ?? []).filter((t) => t.status === 'open').length, [txns]);

  return (
    <div className="flex h-[calc(100dvh-52px)] flex-col px-4 pt-4 sm:px-5">
      <PageHeader kicker="act / external agents" title="Transactions" meta={txns ? `${txns.length} transactions` : undefined}>
        {openCount > 0 ? (
          <span className="flex items-center gap-1.5 font-mono text-[11px] text-mid">
            <span className="rp-breath inline-block h-1.5 w-1.5 rounded-full" style={{ background: 'var(--rp-green)', color: 'var(--rp-green)' }} />
            {openCount} open
          </span>
        ) : null}
      </PageHeader>

      <div className="mt-3 flex min-h-0 flex-1 flex-col gap-3 pb-3">
        <ApprovalBanner
          pending={pending}
          isAdmin={isAdmin}
          clusterId={clusterId}
          onDecide={async (actionId, approve) => {
            if (!orgId) return;
            await (approve ? approveAction(orgId, clusterId, actionId) : rejectAction(orgId, clusterId, actionId));
            pollRef.current();
          }}
        />

        <div className="min-h-0 flex-1 space-y-1.5 overflow-y-auto pr-1">
          {txns === null ? (
            <div className="flex h-24 items-center justify-center gap-2 text-muted"><Spinner /> <span className="font-mono text-[11px]">loading…</span></div>
          ) : txns.length === 0 ? (
            <div className={cn('mx-auto mt-6 max-w-[560px] rounded-skin border border-dashed p-5 text-center')} style={{ borderColor: 'var(--rp-line-strong)' }}>
              <p className="font-mono text-[11.5px] text-muted">No agent transactions yet.</p>
              <p className="mt-1.5 font-mono text-[10.5px] leading-relaxed text-faint">
                Connect an external AI agent (Claude Code, Cursor …) to this cluster&apos;s MCP endpoint — see <Link href="/settings" className="rp-focus text-mid underline decoration-dotted underline-offset-2 hover:text-ink">Settings → MCP</Link>. Every mutating session appears here as a transaction with a full audit timeline.
              </p>
            </div>
          ) : (
            txns.map((t) => <TxnRow key={t.id} t={t} clusterId={clusterId} now={now} />)
          )}
        </div>
      </div>
    </div>
  );
}
