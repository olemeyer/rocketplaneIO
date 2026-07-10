'use client';

import { useEffect, useMemo, useRef, useState } from 'react';
import { useParams } from 'next/navigation';
import { cn } from '@/lib/cn';
import { Spinner } from '@/components/ui';
import { useMe } from '@/components/app/me-context';
import { PageHeader } from '@/components/app/page-header';
import { useClusterEvents } from '@/lib/hooks/use-cluster-events';
import { cancelAction, createAction, getActions } from '@/lib/api/controlplane';
import { CATEGORY_LABEL, LEVEL_META, RISK_LEVELS, actionCategoryOf, actionLevelOf, levelColor, type ActionCategory, type RiskLevel } from '@/lib/approval';
import type { ClusterAction } from '@/lib/api/types';

// Runs — der Audit-Trail aller Action-Ausführungen. Trace-Stil: eine Zeile pro
// Lauf (Status · Kind · Ziel · Level · Dauer), Klick klappt die volle Step-
// Timeline + Params + Ergebnis auf. Oben der Aktivitäts-Graph (Läufe über Zeit,
// gestapelt nach Ausgang) + Status-/Level-Filter.

const POLL_MS = 2500;

type Tone = { fg: string; glyph: string; rail: string };
const FALLBACK: Tone = { fg: 'var(--rp-ink-muted)', glyph: '○', rail: 'var(--rp-line-strong)' };
const STATUS: Record<string, Tone> = {
  pending: { ...FALLBACK, glyph: '◌' },
  running: { fg: 'var(--rp-ink-mid)', glyph: '◌', rail: 'var(--rp-green)' },
  succeeded: { fg: 'var(--rp-tone-green-fg)', glyph: '●', rail: 'var(--rp-green)' },
  failed: { fg: 'var(--rp-tone-red-fg)', glyph: '▲', rail: 'var(--rp-red)' },
  cancelled: { fg: 'var(--rp-tone-yellow-fg)', glyph: '⊘', rail: 'var(--rp-yellow)' },
};
const STATUS_FILTERS = ['all', 'running', 'succeeded', 'failed', 'cancelled'] as const;

function relTime(iso: string): string {
  const t = Date.parse(iso);
  if (!t) return '';
  const s = Math.max(0, (Date.now() - t) / 1000);
  if (s < 60) return `${Math.floor(s)}s ago`;
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86400)}d ago`;
}
function durOf(a: ClusterAction): string {
  const ms = Math.max(0, new Date(a.updatedAt).getTime() - new Date(a.createdAt).getTime());
  return ms < 1000 ? `${ms}ms` : ms < 60_000 ? `${(ms / 1000).toFixed(1)}s` : `${Math.floor(ms / 60000)}m ${Math.floor((ms % 60000) / 1000)}s`;
}
function stepColor(status: string): string {
  return status === 'ok' ? 'var(--rp-tone-green-fg)' : status === 'failed' ? 'var(--rp-tone-red-fg)' : status === 'running' ? 'var(--rp-ink-mid)' : 'var(--rp-ink-faint)';
}
function levelOf(a: ClusterAction): RiskLevel {
  return actionLevelOf(a.kind, (a.params as Record<string, unknown>) ?? {});
}

/* ── Aktivitäts-Graph: Läufe über Zeit, gestapelt nach Ausgang ───────────── */
function ActivityGraph({ runs }: { runs: ClusterAction[] }) {
  const BUCKETS = 36;
  const data = useMemo(() => {
    if (!runs.length) return null;
    const times = runs.map((r) => new Date(r.createdAt).getTime());
    const max = Date.now();
    const min = Math.min(...times);
    const span = Math.max(max - min, 60_000);
    const buckets = Array.from({ length: BUCKETS }, () => ({ ok: 0, fail: 0, other: 0 }));
    for (const r of runs) {
      const t = new Date(r.createdAt).getTime();
      const i = Math.min(BUCKETS - 1, Math.max(0, Math.floor(((t - min) / span) * BUCKETS)));
      if (r.status === 'succeeded') buckets[i]!.ok++;
      else if (r.status === 'failed') buckets[i]!.fail++;
      else buckets[i]!.other++;
    }
    const peak = Math.max(1, ...buckets.map((b) => b.ok + b.fail + b.other));
    return { buckets, peak, from: min };
  }, [runs]);
  if (!data) return null;
  return (
    <div>
      <div className="flex h-12 items-end gap-px">
        {data.buckets.map((b, i) => {
          const total = b.ok + b.fail + b.other;
          const h = (total / data.peak) * 100;
          return (
            <div key={i} className="relative flex-1" style={{ height: '100%' }} title={total ? `${total} runs` : ''}>
              {total > 0 ? (
                <div className="absolute bottom-0 flex w-full flex-col overflow-hidden rounded-t-[1px]" style={{ height: `${Math.max(6, h)}%` }}>
                  {b.fail > 0 ? <div style={{ flex: b.fail, background: 'var(--rp-tone-red-fg)' }} /> : null}
                  {b.other > 0 ? <div style={{ flex: b.other, background: 'var(--rp-line-strong)' }} /> : null}
                  {b.ok > 0 ? <div style={{ flex: b.ok, background: 'color-mix(in oklab, var(--rp-green) 55%, var(--rp-line-strong))' }} /> : null}
                </div>
              ) : (
                <div className="absolute bottom-0 h-px w-full" style={{ background: 'var(--rp-line)' }} />
              )}
            </div>
          );
        })}
      </div>
      <div className="mt-1 flex justify-between font-mono text-[8.5px] text-faint">
        <span>{relTime(new Date(data.from).toISOString())}</span>
        <span>now</span>
      </div>
    </div>
  );
}

/* ── eine Run-Zeile (Trace-Stil, aufklappbar) ────────────────────────────── */
function RunRow({ a, open, onToggle, onCancel, onForceCancel, onRevert }: { a: ClusterAction; open: boolean; onToggle: () => void; onCancel: (id: string) => void; onForceCancel: (id: string) => void; onRevert: (a: ClusterAction) => void }) {
  const tone = STATUS[a.status] ?? FALLBACK;
  const running = a.status === 'pending' || a.status === 'running';
  const lvl = levelOf(a);
  const isScript = a.kind === 'script';
  const steps = a.steps ?? [];
  const okCount = steps.filter((s) => s.status === 'ok').length;

  return (
    <div className={cn('overflow-hidden rounded-skin-sm border transition-colors', open ? 'border-line-strong bg-raised' : 'border-line bg-raised hover:border-line-strong')} style={{ boxShadow: open ? 'var(--rp-rim)' : undefined }}>
      {/* Zeile */}
      <button type="button" onClick={onToggle} className="rp-focus flex w-full items-center gap-2.5 px-2.5 py-2 text-left font-mono text-[11px]">
        <span className="w-[3px] self-stretch rounded-full" style={{ background: tone.rail, opacity: running ? 0.9 : 0.7 }} aria-hidden />
        <span className="flex w-[92px] shrink-0 items-center gap-1.5">
          <span className={running ? 'rp-breath' : ''} style={{ color: tone.fg }}>{tone.glyph}</span>
          <span className="text-[9px] uppercase tracking-[0.06em]" style={{ color: tone.fg }}>{a.status}</span>
        </span>
        <span className="w-[150px] shrink-0">
          <span className="block truncate font-semibold text-ink">{isScript ? 'workflow' : a.kind.replace(/_/g, ' ')}</span>
          <span className="block truncate text-[9px] text-faint">{CATEGORY_LABEL[actionCategoryOf(a.kind)]}</span>
        </span>
        <span className="min-w-0 flex-1 truncate text-mid">
          {a.targetNamespace !== '-' && a.targetNamespace ? <span className="text-faint">{a.targetNamespace} / </span> : null}
          {a.targetName}
          {running && a.progress ? <span className="text-faint"> — {a.progress}</span> : null}
        </span>
        <span className="hidden w-[96px] shrink-0 items-center gap-1 text-[9px] uppercase tracking-[0.05em] md:flex" style={{ color: levelColor(lvl) }}>
          {LEVEL_META[lvl].glyph} {LEVEL_META[lvl].label}
        </span>
        <span className="hidden w-[110px] shrink-0 truncate text-[10px] text-faint lg:block" title={a.requestedBy}>{a.requestedBy || '—'}</span>
        <span className="w-[52px] shrink-0 text-right text-[10px] text-muted tnum">{durOf(a)}</span>
        <span className="w-[58px] shrink-0 text-right text-[10px] text-faint tnum">{relTime(a.createdAt)}</span>
        <span className={cn('shrink-0 text-faint transition-transform', open && 'rotate-90')} aria-hidden>›</span>
      </button>

      {/* aufgeklappt: Steps links · Details rechts */}
      {open ? (
        <div className="grid grid-cols-1 gap-3 border-t border-line px-3 py-2.5 md:grid-cols-[minmax(0,3fr)_minmax(0,2fr)]">
          <div>
            <p className="rp-micro !text-[9.5px] mb-1.5">pipeline · {okCount}/{steps.length} steps</p>
            {steps.length === 0 ? (
              <p className="font-mono text-[10.5px] text-faint">{running ? 'waiting for the agent…' : 'no steps recorded'}</p>
            ) : (
              <ol>
                {steps.map((s, i) => {
                  const c = stepColor(s.status);
                  const last = i === steps.length - 1;
                  return (
                    <li key={`${s.name}-${i}`} className="relative flex gap-2.5 pb-1.5 last:pb-0">
                      {!last ? <span className="absolute bottom-0 left-[4.5px] top-[11px] w-px" style={{ background: 'var(--rp-line-strong)' }} aria-hidden /> : null}
                      <span className="relative z-10 mt-[3px] shrink-0">
                        {s.status === 'ok' ? (
                          <span className="flex h-2.5 w-2.5 items-center justify-center rounded-full" style={{ background: c }}>
                            <svg width="6" height="6" viewBox="0 0 8 8" aria-hidden><path d="M1.5 4.2 3 5.7 6.5 2.2" stroke="var(--rp-bg-base)" strokeWidth="1.4" fill="none" strokeLinecap="round" strokeLinejoin="round" /></svg>
                          </span>
                        ) : s.status === 'running' ? (
                          <span className="animate-ping-slow flex h-2.5 w-2.5 rounded-full" style={{ background: c, color: c }} />
                        ) : s.status === 'failed' ? (
                          <span className="text-[10px] leading-none" style={{ color: c }}>▲</span>
                        ) : (
                          <span className="block h-2.5 w-2.5 rounded-full border" style={{ borderColor: 'var(--rp-line-strong)' }} />
                        )}
                      </span>
                      <div className="min-w-0 flex-1 font-mono text-[10.5px] leading-snug">
                        <span style={{ color: s.status === 'pending' ? 'var(--rp-ink-faint)' : 'var(--rp-ink)' }}>{s.name}</span>
                        {s.detail ? <span className="text-faint"> · {s.detail}</span> : null}
                      </div>
                    </li>
                  );
                })}
              </ol>
            )}
            {a.result ? (
              <div className="mt-2 break-all rounded-skin-sm border border-line bg-inset px-2 py-1.5 font-mono text-[10.5px] leading-snug" style={{ color: a.status === 'failed' ? 'var(--rp-tone-red-fg)' : 'var(--rp-ink-muted)' }}>
                {a.result}
              </div>
            ) : null}
          </div>
          <div className="space-y-2">
            <div>
              <p className="rp-micro !text-[9.5px] mb-1">request</p>
              <div className="divide-y divide-line overflow-hidden rounded-skin-sm border border-line font-mono text-[10.5px]">
                <div className="flex bg-inset px-2 py-1"><span className="w-24 shrink-0 text-faint">kind</span><span className="text-mid">{a.kind}</span></div>
                <div className="flex bg-inset px-2 py-1"><span className="w-24 shrink-0 text-faint">target</span><span className="min-w-0 break-all text-mid">{a.targetKind}/{a.targetName}{a.targetNamespace && a.targetNamespace !== '-' ? ` · ns ${a.targetNamespace}` : ''}</span></div>
                {Object.keys(a.params ?? {}).length ? (
                  <div className="flex bg-inset px-2 py-1"><span className="w-24 shrink-0 text-faint">params</span><span className="min-w-0 break-all text-mid">{JSON.stringify(a.params)}</span></div>
                ) : null}
                <div className="flex bg-inset px-2 py-1"><span className="w-24 shrink-0 text-faint">by</span><span className="text-mid">{a.requestedBy || '—'}</span></div>
                <div className="flex bg-inset px-2 py-1"><span className="w-24 shrink-0 text-faint">started</span><span className="text-mid">{new Date(a.createdAt).toLocaleString()}</span></div>
                <div className="flex bg-inset px-2 py-1"><span className="w-24 shrink-0 text-faint">run id</span><span className="min-w-0 break-all text-faint">{a.id}</span></div>
              </div>
            </div>
            {running ? (
              a.cancelRequested ? (
                <div className="flex items-center gap-2">
                  <span className="font-mono text-[10.5px]" style={{ color: 'var(--rp-tone-yellow-fg)' }}>cancelling — the engine rolls back…</span>
                  <button type="button" onClick={() => onForceCancel(a.id)} className="rp-focus rounded-skin-sm border px-2.5 py-1.5 font-mono text-[10.5px] transition-colors hover:opacity-90" style={{ borderColor: 'var(--rp-node-crit)', color: 'var(--rp-node-crit)' }} title="Finalize immediately — do not wait for the agent. Nothing stays stuck.">
                    force cancel
                  </button>
                </div>
              ) : (
                <button type="button" onClick={() => onCancel(a.id)} className="rp-focus rounded-skin-sm border border-line px-2.5 py-1.5 font-mono text-[10.5px] text-mid transition-colors hover:bg-hover hover:text-ink" title="Cancel — the engine rolls back automatically">
                  cancel &amp; roll back
                </button>
              )
            ) : null}
            {a.snapshot && Object.keys(a.snapshot).length ? (
              <details className="rounded-skin-sm border border-line bg-inset p-2">
                <summary className="rp-micro !text-[9.5px] cursor-pointer select-none">before-snapshot — the object as it was</summary>
                <pre className="mt-1.5 max-h-[220px] overflow-auto whitespace-pre-wrap break-all font-mono text-[9.5px] leading-relaxed text-muted">{JSON.stringify(a.snapshot, null, 2)}</pre>
              </details>
            ) : null}
            {a.status === 'succeeded' && a.revert ? (
              <div className="rounded-skin-sm border border-line bg-inset p-2">
                <p className="rp-micro !text-[9.5px] mb-1">revert</p>
                <code className="block break-all font-mono text-[10px] leading-snug text-muted">
                  {a.revert.kind} {a.revert.targetKind}/{a.revert.targetName}
                  {Object.keys(a.revert.params ?? {}).length ? ' ' + JSON.stringify(a.revert.params) : ''}
                </code>
                <button type="button" onClick={() => onRevert(a)} className="rp-focus mt-1.5 rounded-skin-sm border border-line px-2.5 py-1 font-mono text-[10.5px] text-ink transition-colors hover:bg-hover" title="Dispatch the inverse action with the values captured BEFORE this run">
                  ↺ revert this change
                </button>
              </div>
            ) : null}
          </div>
        </div>
      ) : null}
    </div>
  );
}

/* ── Seite ───────────────────────────────────────────────────────────────── */
export default function RunsPage() {
  const params = useParams<{ id: string }>();
  const clusterId = params.id;
  const { currentOrg } = useMe();
  const orgId = currentOrg?.id;

  const [runs, setRuns] = useState<ClusterAction[] | null>(null);
  const [statusF, setStatusF] = useState<(typeof STATUS_FILTERS)[number]>('all');
  const [levelF, setLevelF] = useState<RiskLevel | 'all'>('all');
  const [catF, setCatF] = useState<ActionCategory | 'all'>('all');
  const [q, setQ] = useState('');
  const [open, setOpen] = useState<Set<string>>(new Set());

  // SSE-getrieben (actions-Signal → refetch), Poll als Fallback.
  const pollRef = useRef<() => void>(() => {});
  const { live } = useClusterEvents(orgId, clusterId, { actions: () => pollRef.current() });
  useEffect(() => {
    if (!orgId) return;
    let alive = true;
    const poll = () => getActions(orgId, clusterId, '', '', 200).then((r) => { if (alive) setRuns(r.actions); }).catch(() => {});
    pollRef.current = () => void poll();
    void poll();
    const t = setInterval(poll, live ? 20_000 : POLL_MS);
    return () => { alive = false; clearInterval(t); };
  }, [orgId, clusterId, live]);

  const counts = useMemo(() => {
    const c: Record<string, number> = { all: runs?.length ?? 0 };
    for (const s of STATUS_FILTERS.slice(1)) c[s] = 0;
    for (const r of runs ?? []) {
      const k = r.status === 'pending' ? 'running' : r.status;
      c[k] = (c[k] ?? 0) + 1;
    }
    return c;
  }, [runs]);

  const filtered = useMemo(() => {
    const needle = q.trim().toLowerCase();
    return (runs ?? []).filter((r) => {
      if (statusF !== 'all' && (r.status === 'pending' ? 'running' : r.status) !== statusF) return false;
      if (levelF !== 'all' && levelOf(r) !== levelF) return false;
      if (catF !== 'all' && actionCategoryOf(r.kind) !== catF) return false;
      if (needle && !`${r.kind} ${r.targetName} ${r.targetNamespace} ${r.requestedBy}`.toLowerCase().includes(needle)) return false;
      return true;
    });
  }, [runs, statusF, levelF, catF, q]);

  // Kategorie-Chips nur für Kategorien zeigen, die im Feed vorkommen — bei ~45
  // Kinds bleibt die Leiste sonst nicht lesbar.
  const presentCats = useMemo(() => {
    const c = new Map<ActionCategory, number>();
    for (const r of runs ?? []) {
      const k = actionCategoryOf(r.kind);
      c.set(k, (c.get(k) ?? 0) + 1);
    }
    return [...c.entries()].sort((x, y) => y[1] - x[1]);
  }, [runs]);

  const toggle = (id: string) => setOpen((cur) => { const n = new Set(cur); if (n.has(id)) n.delete(id); else n.add(id); return n; });

  return (
    <div className="flex h-[calc(100dvh-52px)] flex-col px-4 pt-4 sm:px-5">
      <PageHeader kicker="act / audit trail" title="Runs" meta={runs ? `${runs.length} runs` : undefined}>
        {counts.running ? (
          <span className="flex items-center gap-1.5 font-mono text-[11px] text-mid">
            <span className="rp-breath inline-block h-1.5 w-1.5 rounded-full" style={{ background: 'var(--rp-green)', color: 'var(--rp-green)' }} />
            {counts.running} running
          </span>
        ) : null}
      </PageHeader>

      <div className="mt-3 flex min-h-0 flex-1 flex-col gap-3 pb-3">
        {/* Graph + Filter */}
        <div className="shrink-0 rounded-skin border border-line bg-raised px-3 pb-2 pt-3" style={{ boxShadow: 'var(--rp-rim)' }}>
          <ActivityGraph runs={runs ?? []} />
          <div className="mt-2 flex flex-wrap items-center gap-x-1.5 gap-y-2 border-t border-line pt-2 sm:gap-y-1.5">
            {STATUS_FILTERS.map((s) => {
              const on = statusF === s;
              const tone = s === 'all' ? undefined : (STATUS[s] ?? FALLBACK);
              return (
                <button key={s} type="button" onClick={() => setStatusF(s)} className={cn('rp-focus flex items-center gap-1 rounded-skin-chip border px-2 py-1.5 font-mono text-[10px] transition-colors sm:py-0.5', on ? 'border-line-strong bg-hover text-ink' : 'border-line text-muted hover:text-ink')}>
                  {tone ? <span style={{ color: tone.fg }}>{tone.glyph}</span> : null}
                  {s}
                  <span className="text-[9px] text-faint tnum">{counts[s] ?? 0}</span>
                </button>
              );
            })}
            <span className="mx-1 h-4 w-px bg-line" aria-hidden />
            {(['all', ...RISK_LEVELS] as const).map((l) => {
              const on = levelF === l;
              return (
                <button key={l} type="button" onClick={() => setLevelF(l as RiskLevel | 'all')} className={cn('rp-focus flex items-center gap-1 rounded-skin-chip border px-2 py-1.5 font-mono text-[10px] transition-colors sm:py-0.5', on ? 'border-line-strong bg-hover text-ink' : 'border-line text-muted hover:text-ink')}>
                  {l === 'all' ? 'any level' : <><span style={{ color: levelColor(l) }}>{LEVEL_META[l as RiskLevel].glyph}</span>{LEVEL_META[l as RiskLevel].label}</>}
                </button>
              );
            })}
            <input value={q} onChange={(e) => setQ(e.target.value)} spellCheck={false} placeholder="filter kind / target / user…" className="rp-focus h-7 w-full rounded-skin-sm border border-line bg-inset pl-2 pr-2.5 font-mono text-[10.5px] text-ink placeholder:text-faint md:ml-auto md:w-56" />
          </div>
          {presentCats.length > 1 ? (
            <div className="mt-1.5 flex flex-wrap items-center gap-1.5">
              <button type="button" onClick={() => setCatF('all')} className={cn('rp-focus rounded-skin-chip border px-2 py-0.5 font-mono text-[10px] transition-colors', catF === 'all' ? 'border-line-strong bg-hover text-ink' : 'border-line text-muted hover:text-ink')}>
                any category
              </button>
              {presentCats.map(([c, n]) => (
                <button key={c} type="button" onClick={() => setCatF(c)} className={cn('rp-focus flex items-center gap-1 rounded-skin-chip border px-2 py-0.5 font-mono text-[10px] transition-colors', catF === c ? 'border-line-strong bg-hover text-ink' : 'border-line text-muted hover:text-ink')}>
                  {CATEGORY_LABEL[c]}
                  <span className="text-[9px] text-faint tnum">{n}</span>
                </button>
              ))}
            </div>
          ) : null}
        </div>

        {/* Zeilen */}
        <div className="min-h-0 flex-1 space-y-1.5 overflow-y-auto pr-1">
          {runs === null ? (
            <div className="flex h-24 items-center justify-center gap-2 text-muted"><Spinner /> <span className="font-mono text-[11px]">loading…</span></div>
          ) : filtered.length === 0 ? (
            <div className="flex h-24 items-center justify-center font-mono text-[11px] text-faint">no runs match</div>
          ) : (
            (() => {
              // Safe Actions v2: render actions that were triggered together (one
              // Copilot turn / manual batch) as a single grouped TRACE. A lone
              // action (group of one) renders as a plain row, exactly as before.
              const renderRow = (a: ClusterAction) => (
                <RunRow
                  key={a.id}
                  a={a}
                  open={open.has(a.id)}
                  onToggle={() => toggle(a.id)}
                  onCancel={(id) => { if (orgId) void cancelAction(orgId, clusterId, id).catch(() => {}); }}
                  onForceCancel={(id) => { if (orgId) void cancelAction(orgId, clusterId, id, true).catch(() => {}); }}
                  onRevert={(run) => {
                    if (!orgId || !run.revert) return;
                    void createAction(orgId, clusterId, {
                      kind: run.revert.kind as ClusterAction['kind'],
                      targetNamespace: run.revert.targetNamespace,
                      targetKind: run.revert.targetKind,
                      targetName: run.revert.targetName,
                      params: run.revert.params ?? {},
                    }).catch(() => {});
                  }}
                />
              );
              const order: string[] = [];
              const byGroup = new Map<string, ClusterAction[]>();
              for (const a of filtered) {
                const g = a.groupId ?? a.id;
                if (!byGroup.has(g)) { byGroup.set(g, []); order.push(g); }
                byGroup.get(g)!.push(a);
              }
              return order.map((g) => {
                const members = byGroup.get(g)!.slice().sort((x, y) => (x.groupSeq ?? 0) - (y.groupSeq ?? 0));
                if (members.length <= 1) return members[0] ? renderRow(members[0]) : null;
                const ok = members.filter((m) => m.status === 'succeeded').length;
                const bad = members.filter((m) => m.status === 'failed' || m.status === 'cancelled').length;
                return (
                  <div key={g} className="rounded-skin-sm border border-line bg-raised/40">
                    <div className="flex items-center gap-2 border-b border-line px-2.5 py-1.5">
                      <span className="h-3 w-[2px] rounded-full bg-accent" />
                      <span className="rp-micro text-muted">TRACE · {members.length} ACTIONS</span>
                      <span className="ml-auto font-mono text-[10px] tabular-nums text-faint">
                        {ok > 0 && <span className="text-[--rp-tone-green-fg]">{ok}✓</span>}
                        {bad > 0 && <span className="ml-1.5 text-[--rp-tone-red-fg]">▲{bad}</span>}
                      </span>
                    </div>
                    <div className="space-y-1.5 border-l-2 border-line/60 p-1.5 pl-2.5">
                      {members.map(renderRow)}
                    </div>
                  </div>
                );
              });
            })()
          )}
        </div>
      </div>
    </div>
  );
}
