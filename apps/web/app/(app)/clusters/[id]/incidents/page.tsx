'use client';

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useParams, useRouter } from 'next/navigation';
import { cn } from '@/lib/cn';
import { Spinner } from '@/components/ui';
import { useMe } from '@/components/app/me-context';
import { PageHeader } from '@/components/app/page-header';
import { useClusterEvents } from '@/lib/hooks/use-cluster-events';
import { createIncident, getIncidents, getIncidentStats, getMaintenanceWindows } from '@/lib/api/controlplane';
import type { Incident, IncidentStats, IncidentStatus } from '@/lib/api/types';
import { SEVERITY, STATUS, SEVERITY_ORDER, incidentRank, fmtDuration } from '@/lib/incidents';
import { timeAgo } from '@/lib/format';
import { EscalationManager } from '@/components/app/escalation-manager';
import { MaintenanceManager } from '@/components/app/maintenance-manager';

// Incidents — the response hub: first-class incidents that bracket alerts, Copilot
// investigations, and actions across a lifecycle (open→acknowledged→
// mitigated→resolved). Auto-declared when an alert fires, or manually via
// "Declare". Header shows open load + MTTA/MTTR. Live via SSE.

type Filter = 'open' | 'all' | IncidentStatus;
const FILTERS: { key: Filter; label: string }[] = [
  { key: 'open', label: 'open' },
  { key: 'acknowledged', label: 'acknowledged' },
  { key: 'mitigated', label: 'mitigated' },
  { key: 'resolved', label: 'resolved' },
  { key: 'all', label: 'all' },
];

function Chip({ kind, value }: { kind: 'severity' | 'status'; value: string }) {
  const c = kind === 'severity' ? SEVERITY[value as keyof typeof SEVERITY] : STATUS[value as keyof typeof STATUS];
  if (!c) return null;
  return (
    <span
      className="inline-flex shrink-0 items-center gap-1 rounded-skin-chip px-1.5 py-px font-mono text-[9.5px] uppercase tracking-[0.04em]"
      style={{ color: c.fg, background: c.bg }}
    >
      <span aria-hidden>{c.glyph}</span>
      {c.label}
    </span>
  );
}

export default function IncidentsPage() {
  const params = useParams<{ id: string }>();
  const clusterId = params.id;
  const router = useRouter();
  const { currentOrg } = useMe();
  const orgId = currentOrg?.id;
  const canManage = currentOrg ? currentOrg.role === 'owner' || currentOrg.role === 'admin' : false;

  const [incidents, setIncidents] = useState<Incident[] | null>(null);
  const [stats, setStats] = useState<IncidentStats | null>(null);
  const [filter, setFilter] = useState<Filter>('open');
  const [declaring, setDeclaring] = useState(false);
  const [escalation, setEscalation] = useState(false);
  const [maintenance, setMaintenance] = useState(false);
  const [maintActive, setMaintActive] = useState(0);
  const [err, setErr] = useState<string | null>(null);

  const load = useCallback(() => {
    if (!orgId) return;
    const status = filter === 'all' ? '' : filter;
    getIncidents(orgId, clusterId, status, 150).then((r) => setIncidents(r.incidents)).catch(() => setIncidents([]));
    getIncidentStats(orgId, clusterId).then((r) => setStats(r.stats)).catch(() => {});
    getMaintenanceWindows(orgId, clusterId)
      .then((r) => setMaintActive(r.windows.filter((wnd) => wnd.status === 'active').length))
      .catch(() => {});
  }, [orgId, clusterId, filter]);

  const loadRef = useRef(load);
  loadRef.current = load;
  const { live } = useClusterEvents(orgId, clusterId, { incidents: () => loadRef.current(), alerts: () => loadRef.current() });

  useEffect(() => {
    load();
    const t = setInterval(load, live ? 30_000 : 8_000);
    return () => clearInterval(t);
  }, [load, live]);

  const sorted = useMemo(
    () =>
      [...(incidents ?? [])].sort(
        (a, b) =>
          incidentRank(a.status, a.severity) - incidentRank(b.status, b.severity) ||
          Date.parse(b.createdAt) - Date.parse(a.createdAt),
      ),
    [incidents],
  );

  return (
    <div className="flex h-[calc(100dvh-52px)] flex-col px-4 pt-4 sm:px-5">
      <PageHeader
        kicker="respond / incidents"
        title="Incidents"
        meta={
          stats ? (
            <span className="flex flex-wrap items-center gap-x-2.5 gap-y-1">
              <span>
                <span className={cn(stats.open > 0 ? 'text-ink' : 'text-muted')}>{stats.open}</span> open
              </span>
              {stats.critical > 0 ? (
                <span className="whitespace-nowrap" style={{ color: 'var(--rp-tone-red-fg)' }}>▲ {stats.critical} critical</span>
              ) : null}
              <span className="hidden text-faint sm:inline">·</span>
              <span className="whitespace-nowrap text-muted">MTTA {fmtDuration(stats.mttaSeconds)}</span>
              <span className="whitespace-nowrap text-muted">MTTR {fmtDuration(stats.mttrSeconds)}</span>
            </span>
          ) : undefined
        }
      >
        <div className="flex flex-wrap items-center justify-end gap-2">
          <span className={cn('inline-flex shrink-0 items-center gap-1.5 font-mono text-[10px]', live ? 'text-mid' : 'text-faint')}>
            <span
              className="h-1.5 w-1.5 rounded-full"
              style={{ background: live ? 'var(--rp-tone-green-fg)' : 'var(--rp-ink-faint)' }}
            />
            {live ? 'live' : 'poll'}
          </span>
          <button
            type="button"
            onClick={() => setMaintenance(true)}
            className="rp-focus h-8 shrink-0 whitespace-nowrap rounded-skin-sm border px-3 font-mono text-[11.5px] transition-colors hover:bg-hover"
            style={maintActive > 0
              ? { color: 'var(--rp-tone-yellow-fg)', borderColor: 'color-mix(in oklab, var(--rp-tone-yellow-fg) 45%, var(--rp-line))' }
              : { color: 'var(--rp-ink-mid)', borderColor: 'var(--rp-line)' }}
            title={maintActive > 0 ? `${maintActive} active maintenance window(s) — alerts suppressed` : 'Maintenance windows'}
          >
            {maintActive > 0 ? `◈ Maintenance · ${maintActive}` : 'Maintenance'}
          </button>
          <button
            type="button"
            onClick={() => setEscalation(true)}
            className="rp-focus h-8 shrink-0 whitespace-nowrap rounded-skin-sm border border-line px-3 font-mono text-[11.5px] text-mid transition-colors hover:bg-hover hover:text-ink"
          >
            Escalation ↑
          </button>
          <button
            type="button"
            onClick={() => setDeclaring(true)}
            className="rp-focus h-8 shrink-0 whitespace-nowrap rounded-skin-sm px-3 font-mono text-[11.5px] font-semibold transition-opacity hover:opacity-90"
            style={{ background: 'var(--rp-btn-bg)', color: 'var(--rp-btn-fg)' }}
          >
            Declare incident
          </button>
        </div>
      </PageHeader>

      <div className="mt-3 flex shrink-0 gap-1">
        {FILTERS.map((f) => (
          <button
            key={f.key}
            type="button"
            onClick={() => setFilter(f.key)}
            className={cn(
              'rp-focus rounded-skin-sm px-2.5 py-1 font-mono text-[11px] capitalize transition-colors',
              filter === f.key ? 'bg-hover text-ink' : 'text-muted hover:text-ink',
            )}
            style={filter === f.key ? { boxShadow: 'inset 0 0 0 1px var(--rp-line-strong)' } : undefined}
          >
            {f.label}
          </button>
        ))}
      </div>

      <div className="mt-3 min-h-0 flex-1 overflow-y-auto pb-6">
        {incidents === null ? (
          <div className="flex items-center gap-2 py-6 text-muted">
            <Spinner /> <span className="font-mono text-[11px]">loading incidents…</span>
          </div>
        ) : sorted.length === 0 ? (
          <EmptyState filter={filter} />
        ) : (
          <div className="max-w-[1100px] divide-y divide-line overflow-hidden rounded-skin border border-line">
            {sorted.map((inc) => (
              <button
                key={inc.id}
                type="button"
                onClick={() => router.push(`/clusters/${clusterId}/incidents/${inc.id}`)}
                className="flex w-full items-center gap-3 bg-raised px-3 py-2.5 text-left transition-colors hover:bg-hover"
              >
                <span className="w-14 shrink-0 font-mono text-[10.5px] tabular-nums text-faint">INC-{inc.number}</span>
                <Chip kind="severity" value={inc.severity} />
                <span className="min-w-0 flex-1 truncate text-[12.5px] text-ink">{inc.title}</span>
                {inc.source === 'alert' ? (
                  <span className="hidden shrink-0 rounded-skin-chip px-1.5 py-px font-mono text-[8.5px] uppercase tracking-[0.05em] text-faint sm:inline" style={{ background: 'var(--rp-inset)' }}>
                    auto
                  </span>
                ) : null}
                {inc.assigneeName ? (
                  <span className="hidden shrink-0 font-mono text-[9.5px] text-muted md:inline">@{inc.assigneeName}</span>
                ) : null}
                <span className="hidden shrink-0 font-mono text-[9.5px] tabular-nums text-faint sm:inline">
                  {inc.status === 'resolved' && inc.resolvedAt
                    ? `resolved · ${fmtDuration((Date.parse(inc.resolvedAt) - Date.parse(inc.createdAt)) / 1000)}`
                    : timeAgo(inc.createdAt)}
                </span>
                <Chip kind="status" value={inc.status} />
              </button>
            ))}
          </div>
        )}
      </div>

      {declaring && orgId ? (
        <DeclareModal
          onClose={() => setDeclaring(false)}
          onSubmit={async (title, severity) => {
            setErr(null);
            try {
              const inc = await createIncident(orgId, clusterId, { title, severity });
              setDeclaring(false);
              router.push(`/clusters/${clusterId}/incidents/${inc.id}`);
              return true;
            } catch {
              setErr('Failed to declare incident.');
              return false;
            }
          }}
          err={err}
        />
      ) : null}

      {escalation && orgId ? (
        <EscalationManager orgId={orgId} clusterId={clusterId} onClose={() => setEscalation(false)} />
      ) : null}

      {maintenance && orgId ? (
        <MaintenanceManager
          orgId={orgId}
          clusterId={clusterId}
          canManage={canManage}
          onClose={() => setMaintenance(false)}
          onChange={() => loadRef.current()}
        />
      ) : null}
    </div>
  );
}

function EmptyState({ filter }: { filter: Filter }) {
  return (
    <div className="flex max-w-[1100px] flex-col items-center justify-center gap-2 rounded-skin border border-dashed border-line py-16 text-center">
      <span className="font-mono text-[11px] text-muted">
        {filter === 'open' ? 'No open incidents. All quiet.' : `No ${filter} incidents.`}
      </span>
      <span className="font-mono text-[10px] text-faint">Incidents auto-declare when an alert fires, or declare one manually.</span>
    </div>
  );
}

function DeclareModal({
  onClose,
  onSubmit,
  err,
}: {
  onClose: () => void;
  onSubmit: (title: string, severity: string) => Promise<boolean>;
  err: string | null;
}) {
  const [title, setTitle] = useState('');
  const [severity, setSeverity] = useState('high');
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 px-4" onClick={onClose}>
      <div
        className="w-[min(480px,100%)] rounded-skin border border-line bg-raised p-5"
        style={{ boxShadow: 'var(--rp-rim), var(--rp-shadow-pop)' }}
        onClick={(e) => e.stopPropagation()}
      >
        <div className="rp-micro !text-[10px] text-faint">respond / incidents</div>
        <h2 className="mt-1.5 font-display text-[19px] font-bold tracking-tightest text-ink">Declare incident</h2>
        <label className="mt-4 block font-mono text-[10.5px] uppercase tracking-[0.05em] text-muted">Title</label>
        <input
          autoFocus
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          placeholder="e.g. checkout latency spike"
          className="rp-focus mt-1 h-9 w-full rounded-skin-sm border border-line bg-inset px-3 font-mono text-[12px] text-ink placeholder:text-faint"
        />
        <label className="mt-3 block font-mono text-[10.5px] uppercase tracking-[0.05em] text-muted">Severity</label>
        <div className="mt-1 flex gap-1.5">
          {SEVERITY_ORDER.map((s) => (
            <button
              key={s}
              type="button"
              onClick={() => setSeverity(s)}
              className={cn(
                'rp-focus flex-1 rounded-skin-sm px-2 py-1.5 font-mono text-[10.5px] capitalize transition-colors',
                severity === s ? 'text-ink' : 'text-muted hover:text-ink',
              )}
              style={
                severity === s
                  ? { background: SEVERITY[s].bg, color: SEVERITY[s].fg, boxShadow: 'inset 0 0 0 1px var(--rp-line-strong)' }
                  : { background: 'var(--rp-inset)' }
              }
            >
              {SEVERITY[s].glyph} {s}
            </button>
          ))}
        </div>
        {err ? <p className="mt-3 font-mono text-[10.5px]" style={{ color: 'var(--rp-tone-red-fg)' }}>{err}</p> : null}
        <div className="mt-5 flex items-center justify-end gap-2">
          <button
            type="button"
            onClick={onClose}
            className="rp-focus h-9 rounded-skin-sm border border-line px-4 font-mono text-[11.5px] text-mid transition-colors hover:bg-hover hover:text-ink"
          >
            Cancel
          </button>
          <button
            type="button"
            disabled={!title.trim() || busy}
            onClick={async () => {
              setBusy(true);
              const ok = await onSubmit(title.trim(), severity);
              if (!ok) setBusy(false);
            }}
            className="rp-focus h-9 rounded-skin-sm px-4 font-mono text-[11.5px] font-semibold transition-opacity hover:opacity-90 disabled:opacity-50"
            style={{ background: 'var(--rp-btn-bg)', color: 'var(--rp-btn-fg)' }}
          >
            {busy ? 'declaring…' : 'Declare'}
          </button>
        </div>
      </div>
    </div>
  );
}
