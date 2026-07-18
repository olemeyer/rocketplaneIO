'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import Link from 'next/link';
import { useParams, useRouter } from 'next/navigation';
import { cn } from '@/lib/cn';
import { Spinner } from '@/components/ui';
import { useMe } from '@/components/app/me-context';
import { useClusterEvents } from '@/lib/hooks/use-cluster-events';
import {
  addIncidentNote,
  assignIncident,
  getIncident,
  listMembers,
  listTransactions,
  setIncidentPostmortem,
  setIncidentStatus,
  updateIncident,
} from '@/lib/api/controlplane';
import type { Incident, IncidentEvent, IncidentStatus, MCPTransaction, Member } from '@/lib/api/types';
import { SEVERITY, STATUS, SEVERITY_ORDER, fmtDuration, durationBetween } from '@/lib/incidents';
import { txnChip } from '@/lib/transactions';
import { timeAgo } from '@/lib/format';

// Incident detail — the response console: lifecycle controls
// (acknowledge/mitigate/resolve/reopen), severity + assignment, the
// chronological timeline of alerts, status changes, notes, linked
// investigations and actions, plus the postmortem editor. Live via SSE.

const NEXT: Record<IncidentStatus, { to: IncidentStatus; label: string }[]> = {
  open: [
    { to: 'acknowledged', label: 'Acknowledge' },
    { to: 'resolved', label: 'Resolve' },
  ],
  acknowledged: [
    { to: 'mitigated', label: 'Mark mitigated' },
    { to: 'resolved', label: 'Resolve' },
  ],
  mitigated: [{ to: 'resolved', label: 'Resolve' }],
  resolved: [{ to: 'open', label: 'Reopen' }],
};

// Timeline glyphs per event type (colorblind-safe, RETICLE status colors).
function eventStyle(kind: IncidentEvent['kind']): { glyph: string; fg: string } {
  switch (kind) {
    case 'declared':
      return { glyph: '⚑', fg: 'var(--rp-tone-red-fg)' };
    case 'alert':
      return { glyph: '▲', fg: 'var(--rp-tone-red-fg)' };
    case 'alert_cleared':
      return { glyph: '●', fg: 'var(--rp-tone-green-fg)' };
    case 'status':
      return { glyph: '→', fg: 'var(--rp-ink-mid)' };
    case 'severity':
      return { glyph: '◆', fg: 'var(--rp-tone-yellow-fg)' };
    case 'assigned':
      return { glyph: '@', fg: 'var(--rp-ink-mid)' };
    case 'note':
      return { glyph: '›', fg: 'var(--rp-ink-mid)' };
    case 'investigation':
      return { glyph: '✦', fg: 'var(--rp-tone-blue-fg)' };
    case 'action':
      return { glyph: '⚙', fg: 'var(--rp-tone-blue-fg)' };
    case 'escalated':
      return { glyph: '↑', fg: 'var(--rp-tone-red-fg)' };
    case 'postmortem':
      return { glyph: '▤', fg: 'var(--rp-ink-mid)' };
    default:
      return { glyph: '·', fg: 'var(--rp-ink-faint)' };
  }
}

function StatusChip({ status }: { status: IncidentStatus }) {
  const c = STATUS[status];
  return (
    <span
      className="inline-flex items-center gap-1 rounded-skin-chip px-1.5 py-0.5 font-mono text-[10px] uppercase tracking-[0.04em]"
      style={{ color: c.fg, background: c.bg }}
    >
      <span aria-hidden>{c.glyph}</span>
      {c.label}
    </span>
  );
}

export default function IncidentDetailPage() {
  const params = useParams<{ id: string; incidentId: string }>();
  const clusterId = params.id;
  const incidentId = params.incidentId;
  const router = useRouter();
  const { currentOrg } = useMe();
  const orgId = currentOrg?.id;

  const [incident, setIncident] = useState<Incident | null>(null);
  const [timeline, setTimeline] = useState<IncidentEvent[]>([]);
  const [members, setMembers] = useState<Member[]>([]);
  const [txns, setTxns] = useState<MCPTransaction[]>([]);
  const [notFound, setNotFound] = useState(false);
  const [busy, setBusy] = useState(false);

  const load = useCallback(() => {
    if (!orgId) return;
    getIncident(orgId, clusterId, incidentId)
      .then((r) => {
        setIncident(r.incident);
        setTimeline(r.timeline);
      })
      .catch(() => setNotFound(true));
  }, [orgId, clusterId, incidentId]);

  // External-agent (MCP) transactions opened against this incident.
  const loadTxns = useCallback(() => {
    if (!orgId) return;
    listTransactions(orgId, clusterId, 100)
      .then((list) => setTxns(list.filter((t) => t.incidentId === incidentId)))
      .catch(() => {});
  }, [orgId, clusterId, incidentId]);

  const loadRef = useRef(load);
  loadRef.current = load;
  const loadTxnsRef = useRef(loadTxns);
  loadTxnsRef.current = loadTxns;
  const { live } = useClusterEvents(orgId, clusterId, {
    incidents: () => loadRef.current(),
    transactions: () => loadTxnsRef.current(),
  });

  useEffect(() => { loadTxns(); }, [loadTxns]);

  useEffect(() => {
    load();
  }, [load]);

  useEffect(() => {
    if (orgId) listMembers(orgId).then((r) => setMembers(r.members)).catch(() => {});
  }, [orgId]);

  const act = async (fn: () => Promise<unknown>) => {
    if (!orgId) return;
    setBusy(true);
    try {
      await fn();
      load();
    } finally {
      setBusy(false);
    }
  };

  if (notFound) {
    return (
      <div className="flex h-[calc(100dvh-52px)] flex-col items-center justify-center gap-3">
        <span className="font-mono text-[12px] text-muted">Incident not found.</span>
        <button
          type="button"
          onClick={() => router.push(`/clusters/${clusterId}/incidents`)}
          className="rp-focus h-9 rounded-skin-sm border border-line px-4 font-mono text-[11.5px] text-ink transition-colors hover:bg-hover"
        >
          Back to incidents
        </button>
      </div>
    );
  }
  if (!incident) {
    return (
      <div className="flex h-64 items-center justify-center text-muted">
        <Spinner />
      </div>
    );
  }

  return (
    <div className="flex h-[calc(100dvh-52px)] flex-col px-4 pt-4 sm:px-5">
      {/* Header */}
      <header className="shrink-0">
        <button
          type="button"
          onClick={() => router.push(`/clusters/${clusterId}/incidents`)}
          className="rp-focus font-mono text-[10.5px] text-faint transition-colors hover:text-ink"
        >
          ← incidents
        </button>
        <div className="rp-keyline mt-2 flex flex-wrap items-end justify-between gap-x-4 gap-y-2 pb-3">
          <div className="flex min-w-0 flex-col sm:flex-row sm:items-baseline sm:gap-2.5">
            <span className="font-mono text-[13px] tabular-nums text-faint">INC-{incident.number}</span>
            <h1 className="break-words font-display text-[21px] font-bold leading-tight tracking-tightest text-ink sm:truncate sm:text-[24px] sm:leading-none">
              {incident.title}
            </h1>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <StatusChip status={incident.status} />
            {NEXT[incident.status].map((n) => (
              <button
                key={n.to}
                type="button"
                disabled={busy}
                onClick={() => act(() => setIncidentStatus(orgId!, clusterId, incidentId, n.to))}
                className="rp-focus h-8 rounded-skin-sm border border-line px-3 font-mono text-[11px] text-ink transition-colors hover:bg-hover disabled:opacity-50"
                style={
                  n.to === 'resolved'
                    ? { borderColor: 'color-mix(in oklab, var(--rp-tone-green-fg) 45%, var(--rp-line))' }
                    : undefined
                }
              >
                {n.label}
              </button>
            ))}
          </div>
        </div>
      </header>

      <div className="mt-3 grid min-h-0 flex-1 grid-cols-1 gap-5 overflow-y-auto pb-8 lg:grid-cols-[1fr_300px]">
        {/* Main: summary + timeline + notes + postmortem */}
        <div className="min-w-0 space-y-5">
          <SummaryBlock
            incident={incident}
            onSave={(summary) => act(() => updateIncident(orgId!, clusterId, incidentId, { summary }))}
          />

          <section>
            <div className="rp-micro pb-2 text-faint">timeline</div>
            <Timeline events={timeline} />
            <NoteComposer
              onSubmit={async (note) => {
                if (!orgId) return;
                const r = await addIncidentNote(orgId, clusterId, incidentId, note);
                setTimeline(r.timeline);
              }}
            />
          </section>

          {/* External-agent transactions linked to this incident (the former
              copilot entry point): each one is a full MCP audit trail. */}
          {txns.length > 0 ? (
            <section>
              <div className="rp-micro pb-2 text-faint">agent transactions · {txns.length}</div>
              <div className="divide-y divide-line overflow-hidden rounded-skin border border-line">
                {txns.map((t) => {
                  const chip = txnChip(t.status);
                  return (
                    <Link
                      key={t.id}
                      href={`/clusters/${clusterId}/transactions/${t.id}`}
                      className="rp-focus flex items-center gap-2.5 bg-raised px-3 py-2 font-mono text-[11px] transition-colors hover:bg-hover"
                    >
                      <span className="flex w-[112px] shrink-0 items-center gap-1.5">
                        <span className={chip.pulse ? 'rp-breath' : ''} style={{ color: chip.fg }} aria-hidden>{chip.glyph}</span>
                        <span className="text-[9px] uppercase tracking-[0.06em]" style={{ color: chip.fg }}>{chip.label}</span>
                      </span>
                      <span className="min-w-0 flex-1 truncate text-ink">{t.title}</span>
                      {t.tokenName ? <span className="hidden shrink-0 text-[10px] text-muted sm:block">{t.tokenName}</span> : null}
                      <span className="shrink-0 text-[10px] tabular-nums text-faint">{timeAgo(t.createdAt)}</span>
                      <span className="shrink-0 text-faint" aria-hidden>›</span>
                    </Link>
                  );
                })}
              </div>
            </section>
          ) : null}

          <Postmortem
            value={incident.postmortem}
            onSave={(text) => act(() => setIncidentPostmortem(orgId!, clusterId, incidentId, text))}
          />
        </div>

        {/* Aside: metadata + controls */}
        <aside className="space-y-4">
          <MetaCard
            incident={incident}
            members={members}
            onSeverity={(sev) => act(() => updateIncident(orgId!, clusterId, incidentId, { severity: sev }))}
            onAssign={(userId) => act(() => assignIncident(orgId!, clusterId, incidentId, userId))}
            live={live}
          />
        </aside>
      </div>
    </div>
  );
}

function SummaryBlock({ incident, onSave }: { incident: Incident; onSave: (s: string) => void }) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(incident.summary);
  // A live refresh must NOT overwrite an open editor.
  useEffect(() => {
    if (!editing) setDraft(incident.summary);
  }, [incident.summary, editing]);
  if (editing) {
    return (
      <div>
        <textarea
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          rows={3}
          autoFocus
          placeholder="What's happening? Impact, scope, current understanding…"
          className="rp-focus w-full resize-y rounded-skin-sm border border-line bg-inset px-3 py-2 font-mono text-[12px] leading-relaxed text-ink placeholder:text-faint"
        />
        <div className="mt-1.5 flex gap-2">
          <button
            type="button"
            onClick={() => {
              onSave(draft);
              setEditing(false);
            }}
            className="rp-focus h-7 rounded-skin-sm px-3 font-mono text-[10.5px] font-semibold"
            style={{ background: 'var(--rp-btn-bg)', color: 'var(--rp-btn-fg)' }}
          >
            Save
          </button>
          <button
            type="button"
            onClick={() => {
              setDraft(incident.summary);
              setEditing(false);
            }}
            className="rp-focus h-7 rounded-skin-sm border border-line px-3 font-mono text-[10.5px] text-mid hover:bg-hover"
          >
            Cancel
          </button>
        </div>
      </div>
    );
  }
  return (
    <button
      type="button"
      onClick={() => setEditing(true)}
      className="rp-focus block w-full rounded-skin-sm border border-transparent px-0 py-0 text-left transition-colors hover:border-line hover:px-3 hover:py-2"
    >
      {incident.summary ? (
        <p className="whitespace-pre-wrap font-mono text-[12px] leading-relaxed text-mid">{incident.summary}</p>
      ) : (
        <p className="font-mono text-[11.5px] text-faint">+ add a summary</p>
      )}
    </button>
  );
}

function Timeline({ events }: { events: IncidentEvent[] }) {
  if (events.length === 0) {
    return <div className="font-mono text-[11px] text-faint">No events yet.</div>;
  }
  return (
    <ol className="relative space-y-0 pr-0.5">
      {events.map((e, i) => {
        const st = eventStyle(e.kind);
        const last = i === events.length - 1;
        return (
          <li key={e.id} className="relative flex gap-3 pb-3">
            {!last ? <span className="absolute left-[9px] top-5 h-full w-px bg-line" aria-hidden /> : null}
            <span
              className="relative z-10 mt-0.5 grid h-[19px] w-[19px] shrink-0 place-items-center rounded-full border border-line bg-raised font-mono text-[10px]"
              style={{ color: st.fg }}
              aria-hidden
            >
              {st.glyph}
            </span>
            <div className="min-w-0 flex-1">
              <div className="flex items-baseline justify-between gap-2">
                <span className="min-w-0 truncate text-[12px] text-ink">{e.message}</span>
                <span className="shrink-0 font-mono text-[10px] tabular-nums text-faint">{timeAgo(e.at)}</span>
              </div>
              {e.actorEmail ? (
                <span className="font-mono text-[10px] text-faint">{e.actorEmail}</span>
              ) : e.kind === 'alert' || e.kind === 'alert_cleared' || e.kind === 'declared' ? (
                <span className="font-mono text-[10px] text-faint">system</span>
              ) : null}
            </div>
          </li>
        );
      })}
    </ol>
  );
}

function NoteComposer({ onSubmit }: { onSubmit: (note: string) => Promise<void> }) {
  const [note, setNote] = useState('');
  const [busy, setBusy] = useState(false);
  const submit = async () => {
    const v = note.trim();
    if (!v) return;
    setBusy(true);
    try {
      await onSubmit(v);
      setNote('');
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="mt-2 flex gap-2">
      <input
        value={note}
        onChange={(e) => setNote(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter' && !e.shiftKey) {
            e.preventDefault();
            submit();
          }
        }}
        placeholder="add a note to the timeline…"
        className="rp-focus h-8 flex-1 rounded-skin-sm border border-line bg-inset px-3 font-mono text-[11.5px] text-ink placeholder:text-faint"
      />
      <button
        type="button"
        disabled={!note.trim() || busy}
        onClick={submit}
        className="rp-focus h-8 rounded-skin-sm border border-line px-3 font-mono text-[11px] text-ink transition-colors hover:bg-hover disabled:opacity-50"
      >
        Note
      </button>
    </div>
  );
}

function Postmortem({ value, onSave }: { value: string; onSave: (t: string) => void }) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(value);
  useEffect(() => {
    if (!editing) setDraft(value);
  }, [value, editing]);
  return (
    <section>
      <div className="flex items-center justify-between pb-2">
        <div className="rp-micro text-faint">postmortem</div>
        {!editing ? (
          <button
            type="button"
            onClick={() => setEditing(true)}
            className="rp-focus font-mono text-[10px] text-muted transition-colors hover:text-ink"
          >
            {value ? 'edit' : '+ write'}
          </button>
        ) : null}
      </div>
      {editing ? (
        <div>
          <textarea
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            rows={8}
            autoFocus
            placeholder={'## Summary\n## Impact\n## Root cause\n## Timeline\n## Action items'}
            className="rp-focus w-full resize-y rounded-skin-sm border border-line bg-inset px-3 py-2 font-mono text-[12px] leading-relaxed text-ink placeholder:text-faint"
          />
          <div className="mt-1.5 flex gap-2">
            <button
              type="button"
              onClick={() => {
                onSave(draft);
                setEditing(false);
              }}
              className="rp-focus h-7 rounded-skin-sm px-3 font-mono text-[10.5px] font-semibold"
              style={{ background: 'var(--rp-btn-bg)', color: 'var(--rp-btn-fg)' }}
            >
              Save postmortem
            </button>
            <button
              type="button"
              onClick={() => {
                setDraft(value);
                setEditing(false);
              }}
              className="rp-focus h-7 rounded-skin-sm border border-line px-3 font-mono text-[10.5px] text-mid hover:bg-hover"
            >
              Cancel
            </button>
          </div>
        </div>
      ) : value ? (
        <pre className="whitespace-pre-wrap rounded-skin-sm border border-line bg-inset px-3 py-2 font-mono text-[11.5px] leading-relaxed text-mid">
          {value}
        </pre>
      ) : (
        <p className="font-mono text-[11px] text-faint">No postmortem yet.</p>
      )}
    </section>
  );
}

function MetaCard({
  incident,
  members,
  onSeverity,
  onAssign,
  live,
}: {
  incident: Incident;
  members: Member[];
  onSeverity: (sev: string) => void;
  onAssign: (userId: string | null) => void;
  live: boolean;
}) {
  const mtta = incident.acknowledgedAt ? durationBetween(incident.createdAt, incident.acknowledgedAt) : 0;
  const mttr = incident.resolvedAt ? durationBetween(incident.createdAt, incident.resolvedAt) : 0;
  const openFor = durationBetween(incident.createdAt, incident.resolvedAt);
  return (
    <div className="rounded-skin border border-line bg-raised p-3.5">
      <div className="flex items-center justify-between">
        <div className="rp-micro text-faint">details</div>
        <span className={cn('inline-flex items-center gap-1 font-mono text-[9px]', live ? 'text-mid' : 'text-faint')}>
          <span className="h-1 w-1 rounded-full" style={{ background: live ? 'var(--rp-tone-green-fg)' : 'var(--rp-ink-faint)' }} />
          {live ? 'live' : 'poll'}
        </span>
      </div>

      <EscalationLine incident={incident} />

      <Row label="Severity">
        <select
          value={incident.severity}
          onChange={(e) => onSeverity(e.target.value)}
          className="rp-focus h-7 rounded-skin-sm border border-line bg-inset px-1.5 font-mono text-[10.5px] text-ink"
          style={{ color: SEVERITY[incident.severity].fg }}
        >
          {SEVERITY_ORDER.map((s) => (
            <option key={s} value={s} style={{ color: 'var(--rp-ink)' }}>
              {SEVERITY[s].glyph} {s}
            </option>
          ))}
        </select>
      </Row>

      <Row label="Assignee">
        <select
          value={incident.assigneeId ?? ''}
          onChange={(e) => onAssign(e.target.value || null)}
          className="rp-focus h-7 max-w-[170px] rounded-skin-sm border border-line bg-inset px-1.5 font-mono text-[10.5px] text-ink"
        >
          <option value="">unassigned</option>
          {members.map((m) => (
            <option key={m.userId} value={m.userId}>
              {m.name || m.email}
            </option>
          ))}
        </select>
      </Row>

      <Row label="Source">
        {/* Historical rows may still carry source='copilot' — show a neutral label. */}
        <span className="font-mono text-[10.5px] text-mid">{incident.source === 'copilot' ? 'assistant' : incident.source}</span>
      </Row>
      <Row label="Declared">
        <span className="font-mono text-[10.5px] tabular-nums text-mid">{timeAgo(incident.createdAt)}</span>
      </Row>
      {incident.createdByName ? (
        <Row label="By">
          <span className="font-mono text-[10.5px] text-mid">{incident.createdByName}</span>
        </Row>
      ) : null}
      <Row label="MTTA">
        <span className="font-mono text-[10.5px] tabular-nums text-mid">{mtta ? fmtDuration(mtta) : '—'}</span>
      </Row>
      <Row label={incident.status === 'resolved' ? 'MTTR' : 'Open for'}>
        <span className="font-mono text-[10.5px] tabular-nums text-mid">
          {incident.status === 'resolved' ? (mttr ? fmtDuration(mttr) : '—') : fmtDuration(openFor)}
        </span>
      </Row>
    </div>
  );
}

// EscalationLine shows the escalation state: active (next step in Xm),
// stopped (after acknowledge), or no policy.
function EscalationLine({ incident }: { incident: Incident }) {
  if (!incident.escalationPolicyId) return null;
  const active = incident.status === 'open' && !!incident.nextEscalationAt;
  let text: string;
  let fg = 'var(--rp-ink-muted)';
  if (active) {
    const secs = (Date.parse(incident.nextEscalationAt!) - Date.now()) / 1000;
    text = secs <= 0 ? `escalating now (step ${incident.escalationStep + 1})` : `escalates in ${fmtDuration(secs)}`;
    fg = 'var(--rp-tone-red-fg)';
  } else if (incident.status === 'open') {
    text = 'escalation complete';
  } else {
    text = 'escalation stopped';
  }
  return (
    <div className="mt-2 flex items-center gap-1.5 rounded-skin-sm px-2 py-1.5" style={{ background: 'var(--rp-inset)' }}>
      <span aria-hidden style={{ color: fg }}>
        ↑
      </span>
      <span className="font-mono text-[10px] tabular-nums" style={{ color: fg }}>
        {text}
      </span>
      {incident.escalationStep > 0 ? (
        <span className="ml-auto font-mono text-[9px] text-faint">{incident.escalationStep} paged</span>
      ) : null}
    </div>
  );
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-2 border-t border-line py-2 first-of-type:mt-2">
      <span className="font-mono text-[10px] uppercase tracking-[0.04em] text-faint">{label}</span>
      {children}
    </div>
  );
}
