'use client';

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useParams } from 'next/navigation';
import { cn } from '@/lib/cn';
import { Spinner } from '@/components/ui';
import { useMe } from '@/components/app/me-context';
import { PageHeader } from '@/components/app/page-header';
import { useClusterEvents } from '@/lib/hooks/use-cluster-events';
import { LineChart } from '@/components/metrics/line-chart';
import { PromQLEditor } from '@/components/metrics/promql-editor';
import {
  createAlertProvider,
  createAlertRule,
  deleteAlertProvider,
  deleteAlertRule,
  getActionDefinitions,
  getAlertEvents,
  getAlertProviders,
  getAlertRuleSeries,
  getAlertRules,
  getMetricDefinitions,
  getServiceMap,
  muteAlertRule,
  testAlertProvider,
  updateAlertRule,
} from '@/lib/api/controlplane';
import type {
  ActionDefinition,
  AlertEvent,
  MetricDefinition,
  AlertProvider,
  AlertRule,
  MetricSeries,
  ProviderType,
  RuleKind,
  ServiceMap,
} from '@/lib/api/types';

// Alerts — Check Rules (Dash0-Muster): typed Bedingungen ODER echtes PromQL,
// Threshold + for-Dauer, State-Machine ok→pending→firing, Sparkline der
// Evaluator-Werte mit Threshold-Linie auf jeder Karte, Snooze, und
// Auto-Remediation (firing dispatcht einen Starlark-Workflow). Live via SSE.

const KINDS: { kind: RuleKind; label: string; unit: string; params: ('service' | 'workload' | 'namespace' | 'node')[]; hint: string }[] = [
  { kind: 'promql', label: 'promql', unit: '', params: [], hint: 'any promql expression (max over series)' },
  { kind: 'log_errors', label: 'log errors', unit: 'count', params: ['namespace', 'workload'], hint: 'ERROR lines in window' },
  { kind: 'trace_error_ratio', label: 'error ratio', unit: '%', params: ['service'], hint: '5xx/error spans ÷ all' },
  { kind: 'trace_p95_ms', label: 'latency p95', unit: 'ms', params: ['service'], hint: 'server span p95' },
  { kind: 'node_cpu_pct', label: 'node cpu', unit: '%', params: ['node'], hint: 'max node over window' },
  { kind: 'node_mem_pct', label: 'node memory', unit: '%', params: ['node'], hint: 'max node over window' },
  { kind: 'node_disk_pct', label: 'node disk', unit: '%', params: ['node'], hint: 'max node over window' },
  { kind: 'workload_unready', label: 'pods unready', unit: 'pods', params: ['workload'], hint: 'desired − ready' },
  { kind: 'derived', label: 'custom metric', unit: '', params: [], hint: 'a metric you defined' },
];

const FALLBACK_STATE = { fg: 'var(--rp-tone-green-fg)', bg: 'var(--rp-tone-green-bg)', glyph: '●' };
const STATE_CHIP: Record<string, { fg: string; bg: string; glyph: string }> = {
  ok: FALLBACK_STATE,
  pending: { fg: 'var(--rp-tone-yellow-fg)', bg: 'var(--rp-tone-yellow-bg)', glyph: '◆' },
  firing: { fg: 'var(--rp-tone-red-fg)', bg: 'var(--rp-tone-red-bg)', glyph: '▲' },
};

// Dringlichkeit: firing vor pending vor ok, critical vor warning, off zuletzt.
const STATE_RANK: Record<string, number> = { firing: 0, pending: 1, ok: 2 };
function ruleRank(r: AlertRule): number {
  if (!r.enabled) return 90;
  return (STATE_RANK[r.state] ?? 2) * 10 + (r.severity === 'critical' ? 0 : 1);
}

function isMuted(r: AlertRule): boolean {
  return !!r.mutedUntil && new Date(r.mutedUntil).getTime() > Date.now();
}

export default function AlertsPage() {
  const params = useParams<{ id: string }>();
  const clusterId = params.id;
  const { currentOrg } = useMe();
  const orgId = currentOrg?.id;

  const [rules, setRules] = useState<AlertRule[] | null>(null);
  const [providers, setProviders] = useState<AlertProvider[] | null>(null);
  const [events, setEvents] = useState<AlertEvent[] | null>(null);
  const [map, setMap] = useState<ServiceMap | null>(null);
  const [defs, setDefs] = useState<ActionDefinition[]>([]);
  const [ruleEditor, setRuleEditor] = useState<Partial<AlertRule> | null>(null);
  const [provEditor, setProvEditor] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const load = useCallback(() => {
    if (!orgId) return;
    getAlertRules(orgId, clusterId).then((r) => setRules(r.rules)).catch(() => setRules([]));
    getAlertProviders(orgId).then((r) => setProviders(r.providers)).catch(() => setProviders([]));
    getAlertEvents(orgId, clusterId, 40).then((r) => setEvents(r.events)).catch(() => setEvents([]));
  }, [orgId, clusterId]);

  const loadRef = useRef(load);
  loadRef.current = load;
  const { live } = useClusterEvents(orgId, clusterId, { alerts: () => loadRef.current() });

  useEffect(() => {
    load();
    const t = setInterval(load, live ? 30_000 : 5_000);
    return () => clearInterval(t);
  }, [load, live]);

  useEffect(() => {
    if (!orgId) return;
    getServiceMap(orgId, clusterId).then(setMap).catch(() => {});
    getActionDefinitions(orgId).then((r) => setDefs(r.definitions)).catch(() => {});
  }, [orgId, clusterId]);

  const sorted = useMemo(() => [...(rules ?? [])].sort((a, b) => ruleRank(a) - ruleRank(b) || a.name.localeCompare(b.name)), [rules]);
  const firing = (rules ?? []).filter((r) => r.enabled && r.state === 'firing').length;
  const pending = (rules ?? []).filter((r) => r.enabled && r.state === 'pending').length;

  return (
    <div className="flex h-[calc(100dvh-52px)] flex-col px-4 pt-4 sm:px-5">
      <PageHeader kicker="observe / alerts" title="Alerts">
        <div className="flex items-center gap-3 font-mono text-[11px] text-muted tnum">
          {live ? (
            <span className="flex items-center gap-1.5">
              <span className="rp-breath inline-block h-1.5 w-1.5 rounded-full" style={{ background: 'var(--rp-green)', color: 'var(--rp-green)' }} />
              <span className="!text-ink">Live</span>
            </span>
          ) : null}
          <span>{rules?.length ?? '…'} rules</span>
          {firing > 0 ? <span style={{ color: 'var(--rp-tone-red-fg)' }}>▲ {firing} firing</span> : null}
          {pending > 0 ? <span style={{ color: 'var(--rp-tone-yellow-fg)' }}>◆ {pending} pending</span> : null}
        </div>
      </PageHeader>

      <div className="mt-3 grid min-h-0 flex-1 grid-cols-1 gap-3 pb-3 lg:grid-cols-[minmax(0,3fr)_minmax(0,2fr)]">
        {/* ── Rules ── */}
        <section className="min-h-0 overflow-y-auto pr-1">
          <div className="flex items-center justify-between">
            <span className="rp-micro !text-[10px]">check rules</span>
            <button
              type="button"
              onClick={() => setRuleEditor({ kind: 'promql', op: 'gt', threshold: 10, windowSeconds: 300, forSeconds: 60, severity: 'warning', providerIds: [], params: {}, query: '' })}
              className="rp-focus h-8 rounded-skin-sm px-3 font-mono text-[11px] font-semibold transition-opacity hover:opacity-90"
              style={{ background: 'var(--rp-btn-bg)', color: 'var(--rp-btn-fg)' }}
            >
              + New rule
            </button>
          </div>
          {rules === null ? (
            <div className="mt-3 flex items-center gap-2 text-muted"><Spinner /> <span className="font-mono text-[11px]">loading…</span></div>
          ) : rules.length === 0 ? (
            <div className="mt-2 rounded-skin border border-dashed p-4 text-center font-mono text-[11px] text-muted" style={{ borderColor: 'var(--rp-line-strong)' }}>
              No check rules yet — alert on any PromQL expression, log errors, trace error-ratio/latency or node pressure.
            </div>
          ) : (
            <div className="mt-2 space-y-2">
              {sorted.map((r) => (
                <RuleCard
                  key={r.id}
                  rule={r}
                  orgId={orgId!}
                  clusterId={clusterId}
                  defs={defs}
                  onEdit={() => setRuleEditor({ ...r })}
                  onToggle={async () => {
                    if (!orgId) return;
                    await updateAlertRule(orgId, clusterId, r.id, { ...r, enabled: !r.enabled });
                    load();
                  }}
                  onSnooze={async (minutes) => {
                    if (!orgId) return;
                    await muteAlertRule(orgId, clusterId, r.id, minutes).catch(() => {});
                    load();
                  }}
                />
              ))}
            </div>
          )}
        </section>

        {/* ── Providers + Events ── */}
        <section className="flex min-h-0 flex-col gap-3 overflow-hidden">
          <div className="shrink-0 rounded-skin border border-line bg-raised p-3" style={{ boxShadow: 'var(--rp-rim)' }}>
            <div className="flex items-center justify-between">
              <span className="rp-micro !text-[10px]">providers · org-wide</span>
              <button
                type="button"
                onClick={() => setProvEditor(true)}
                className="rounded-skin-chip border border-line px-1.5 py-0.5 font-mono text-[10px] text-muted transition-colors hover:text-ink"
              >
                + add
              </button>
            </div>
            {providers === null ? (
              <div className="mt-2 font-mono text-[11px] text-faint">loading…</div>
            ) : providers.length === 0 ? (
              <div className="mt-2 font-mono text-[11px] text-faint">no channels — add webhook, slack or email</div>
            ) : (
              <div className="mt-2 space-y-1.5">
                {providers.map((p) => (
                  <div key={p.id} className="flex items-center gap-2 rounded-skin-sm border border-line px-2 py-1.5 font-mono text-[11px]">
                    <span className="rounded-skin-chip bg-inset px-1.5 py-0.5 text-[9px] uppercase tracking-[0.05em] text-muted">{p.type}</span>
                    <span className="min-w-0 flex-1 truncate text-ink">{p.name}</span>
                    <button
                      type="button"
                      onClick={async () => {
                        if (!orgId) return;
                        setErr(null);
                        try {
                          await testAlertProvider(orgId, p.id);
                        } catch (e) {
                          setErr(e instanceof Error ? e.message : 'test failed');
                        }
                      }}
                      className="shrink-0 rounded-skin-chip border border-line px-1.5 py-0.5 text-[9.5px] text-muted transition-colors hover:text-ink"
                    >
                      send test
                    </button>
                    <button
                      type="button"
                      onClick={async () => {
                        if (!orgId) return;
                        await deleteAlertProvider(orgId, p.id).catch(() => {});
                        load();
                      }}
                      className="shrink-0 rounded-skin-chip border border-line px-1.5 py-0.5 text-[9.5px] text-muted transition-colors hover:text-ink"
                      aria-label={`delete ${p.name}`}
                    >
                      ✕
                    </button>
                  </div>
                ))}
              </div>
            )}
            {err ? <div className="mt-2 font-mono text-[10.5px]" style={{ color: 'var(--rp-tone-red-fg)' }}>{err}</div> : null}
          </div>

          <div className="flex min-h-0 flex-1 flex-col overflow-hidden rounded-skin border border-line bg-raised" style={{ boxShadow: 'var(--rp-rim)' }}>
            <div className="flex shrink-0 items-center justify-between border-b border-line px-3 py-2">
              <span className="rp-micro !text-[10px]">events</span>
              <span className="font-mono text-[10px] text-muted tnum">{events?.length ?? 0}</span>
            </div>
            <div className="min-h-0 flex-1 overflow-y-auto">
              {events === null || events.length === 0 ? (
                <div className="flex h-20 items-center justify-center font-mono text-[11px] text-faint">no transitions yet</div>
              ) : (
                events.map((e) => {
                  const chip = STATE_CHIP[e.toState] ?? FALLBACK_STATE;
                  const remediation = e.message.startsWith('auto-remediation');
                  return (
                    <div key={e.id} className="flex items-start gap-2 border-b border-line/60 px-3 py-1.5 font-mono text-[10.5px] last:border-0">
                      <span className="shrink-0 pt-px" style={{ color: chip.fg }}>{chip.glyph}</span>
                      <span className="min-w-0 flex-1">
                        <span className="text-ink">{e.ruleName}</span>{' '}
                        <span className="text-muted">{e.fromState} → </span>
                        <span style={{ color: chip.fg }}>{e.toState}</span>
                        <span className={cn('block truncate', remediation ? 'text-mid' : 'text-faint')}>
                          {remediation ? '⚙ ' : ''}{e.message}
                        </span>
                      </span>
                      <span className="shrink-0 text-faint tnum">{e.at.slice(11, 19)}</span>
                    </div>
                  );
                })
              )}
            </div>
          </div>
        </section>
      </div>

      {ruleEditor && orgId ? (
        <RuleEditor
          rule={ruleEditor}
          providers={providers ?? []}
          map={map}
          defs={defs}
          orgId={orgId}
          clusterId={clusterId}
          onClose={() => setRuleEditor(null)}
          onSave={async (r) => {
            if (r.id) await updateAlertRule(orgId, clusterId, r.id, r);
            else await createAlertRule(orgId, clusterId, r);
            setRuleEditor(null);
            load();
          }}
          onDelete={
            ruleEditor.id
              ? async () => {
                  await deleteAlertRule(orgId, clusterId, ruleEditor.id!).catch(() => {});
                  setRuleEditor(null);
                  load();
                }
              : undefined
          }
        />
      ) : null}
      {provEditor && orgId ? (
        <ProviderEditor
          onClose={() => setProvEditor(false)}
          onSave={async (name, type, config) => {
            await createAlertProvider(orgId, { name, type, config });
            setProvEditor(false);
            load();
          }}
        />
      ) : null}
    </div>
  );
}

/* ── Rule-Karte: Status + Sparkline + Snooze ────────────────────────────── */

function RuleCard({
  rule: r,
  orgId,
  clusterId,
  defs,
  onEdit,
  onToggle,
  onSnooze,
}: {
  rule: AlertRule;
  orgId: string;
  clusterId: string;
  defs: ActionDefinition[];
  onEdit: () => void;
  onToggle: () => Promise<void>;
  onSnooze: (minutes: number) => Promise<void>;
}) {
  const chip = STATE_CHIP[r.enabled ? r.state : 'ok'] ?? FALLBACK_STATE;
  const k = KINDS.find((k) => k.kind === r.kind);
  const muted = isMuted(r);
  const remediation = r.actionDefinitionId ? defs.find((d) => d.id === r.actionDefinitionId) : null;
  const since = r.stateSince ? r.stateSince.slice(11, 16) : '';

  return (
    <div className="rounded-skin border border-line bg-raised p-3" style={{ boxShadow: 'var(--rp-rim)' }}>
      <div className="flex flex-wrap items-center gap-2">
        <span
          className={cn('shrink-0 rounded-skin-chip px-1.5 py-0.5 font-mono text-[9px] uppercase tracking-[0.05em]', r.enabled && r.state !== 'ok' && !muted && 'rp-breath')}
          style={r.enabled ? { color: chip.fg, background: chip.bg } : { color: 'var(--rp-ink-faint)', background: 'var(--rp-tone-neutral-bg)' }}
        >
          {r.enabled ? `${chip.glyph} ${r.state}` : '○ off'}
        </span>
        {r.enabled && r.state !== 'ok' && since ? (
          <span className="shrink-0 font-mono text-[9.5px] text-muted tnum">since {since}</span>
        ) : null}
        <span className="min-w-[6rem] flex-1 truncate font-mono text-[12.5px] font-semibold text-ink">{r.name}</span>
        <span className="shrink-0 rounded-skin-chip bg-inset px-1.5 py-0.5 font-mono text-[9px] text-muted">{k?.label ?? r.kind}</span>
        {r.severity === 'critical' ? (
          <span className="shrink-0 rounded-skin-chip px-1.5 py-0.5 font-mono text-[9px] uppercase" style={{ color: 'var(--rp-tone-red-fg)', background: 'var(--rp-tone-red-bg)' }}>crit</span>
        ) : null}
        {muted ? (
          <span className="shrink-0 rounded-skin-chip px-1.5 py-0.5 font-mono text-[9px] uppercase text-muted" style={{ background: 'var(--rp-tone-neutral-bg)' }}>
            ⏾ muted til {r.mutedUntil!.slice(11, 16)}
          </span>
        ) : null}
        <span className="flex w-full shrink-0 items-center justify-end gap-1.5 sm:ml-auto sm:w-auto">
          <button
            type="button"
            onClick={() => void onSnooze(muted ? 0 : 60)}
            className="rounded-skin-sm border border-line px-2 py-1 font-mono text-[10px] text-mid transition-colors hover:bg-hover hover:text-ink"
            title={muted ? 'notifications resume' : 'mute notifications for 1h (state keeps evaluating)'}
          >
            {muted ? 'unmute' : 'snooze 1h'}
          </button>
          <button
            type="button"
            onClick={onEdit}
            className="rounded-skin-sm border border-line px-2 py-1 font-mono text-[10px] text-mid transition-colors hover:bg-hover hover:text-ink"
          >
            edit
          </button>
          <button
            type="button"
            onClick={() => void onToggle()}
            className="rounded-skin-sm border border-line px-2 py-1 font-mono text-[10px] text-mid transition-colors hover:bg-hover hover:text-ink"
          >
            {r.enabled ? 'disable' : 'enable'}
          </button>
        </span>
      </div>
      <div className="mt-1.5 flex flex-wrap items-center gap-x-4 gap-y-1 font-mono text-[10.5px] text-muted tnum">
        <span>
          <span className="text-ink">{r.lastValue.toFixed(1)}</span> {r.op === 'gt' ? '/' : '\\'} {r.threshold} {k?.unit}
        </span>
        <span>window {r.windowSeconds}s · for {r.forSeconds}s</span>
        {r.kind === 'promql' && r.query ? (
          <span className="min-w-0 max-w-[46ch] truncate text-mid" title={r.query}>{r.query}</span>
        ) : null}
        {Object.entries(r.params ?? {}).filter(([, v]) => v).map(([pk, pv]) => (
          <span key={pk}>{pk}=<span className="text-mid">{pv}</span></span>
        ))}
        {remediation ? (
          <span className="text-mid">⚙ auto-remediation: {remediation.name}</span>
        ) : null}
        {r.lastError ? <span style={{ color: 'var(--rp-tone-red-fg)' }}>eval error</span> : null}
      </div>
      {r.enabled ? (
        <RuleSparkline orgId={orgId} clusterId={clusterId} ruleId={r.id} threshold={r.threshold} refreshKey={r.lastEvalAt ?? ''} />
      ) : null}
    </div>
  );
}

/* Sparkline der Evaluator-Werte (30 min) mit gestrichelter Threshold-Linie. */
function RuleSparkline({
  orgId,
  clusterId,
  ruleId,
  threshold,
  refreshKey,
}: {
  orgId: string;
  clusterId: string;
  ruleId: string;
  threshold: number;
  refreshKey: string;
}) {
  const [series, setSeries] = useState<MetricSeries | null>(null);

  useEffect(() => {
    let alive = true;
    getAlertRuleSeries(orgId, clusterId, ruleId)
      .then((r) => {
        if (alive) setSeries(r.series?.[0] ?? { name: 'value', points: [] });
      })
      .catch(() => {});
    return () => {
      alive = false;
    };
  }, [orgId, clusterId, ruleId, refreshKey]);

  const pts = series?.points ?? [];
  if (pts.length < 2) return null;

  const W = 640;
  const H = 42;
  const PAD = 3;
  let vMax = threshold;
  for (const p of pts) if (p.v > vMax) vMax = p.v;
  if (vMax <= 0) vMax = 1;
  vMax *= 1.15;
  const tMin = pts[0]!.t;
  const tMax = pts[pts.length - 1]!.t;
  const x = (t: number) => PAD + ((t - tMin) / Math.max(1, tMax - tMin)) * (W - 2 * PAD);
  const y = (v: number) => PAD + (1 - v / vMax) * (H - 2 * PAD);
  const line = pts.map((p, i) => `${i === 0 ? 'M' : 'L'}${x(p.t).toFixed(1)},${y(p.v).toFixed(1)}`).join(' ');
  const area = `${line} L${x(tMax).toFixed(1)},${H - PAD} L${x(tMin).toFixed(1)},${H - PAD} Z`;
  const last = pts[pts.length - 1]!;
  const breach = pts.some((p) => p.v > threshold);

  return (
    <div className="mt-2 border-t border-line/60 pt-1.5" data-testid="rule-sparkline">
      <svg viewBox={`0 0 ${W} ${H}`} className="block w-full" preserveAspectRatio="none" style={{ height: 42 }}>
        <path d={area} fill="var(--rp-ink-mid)" opacity={0.07} />
        <path d={line} fill="none" stroke="var(--rp-ink-mid)" strokeWidth="1.2" strokeLinejoin="round" opacity={0.85} />
        {/* Threshold — crimson gestrichelt, nur bei Überschreitung laut */}
        <line x1={PAD} x2={W - PAD} y1={y(threshold)} y2={y(threshold)} stroke="var(--rp-red)" strokeWidth="1" strokeDasharray="4 3" opacity={breach ? 0.85 : 0.4} />
        <circle cx={x(last.t)} cy={y(last.v)} r="2.4" fill={last.v > threshold ? 'var(--rp-red)' : 'var(--rp-green)'} />
      </svg>
      <div className="mt-0.5 flex justify-between font-mono text-[8.5px] text-faint tnum">
        <span>−30m</span>
        <span>threshold {threshold >= 100 ? Math.round(threshold) : threshold} · eval history</span>
        <span>now</span>
      </div>
    </div>
  );
}

/* ── Rule-Editor ────────────────────────────────────────────────────────── */

function RuleEditor({
  rule,
  providers,
  map,
  defs,
  orgId,
  clusterId,
  onClose,
  onSave,
  onDelete,
}: {
  rule: Partial<AlertRule>;
  providers: AlertProvider[];
  map: ServiceMap | null;
  defs: ActionDefinition[];
  orgId: string;
  clusterId: string;
  onClose: () => void;
  onSave: (r: Partial<AlertRule>) => Promise<void>;
  onDelete?: () => Promise<void>;
}) {
  const orgIdRef = useRef(orgId);
  const clusterIdRef = useRef(clusterId);
  const [r, setR] = useState<Partial<AlertRule>>({ ...rule });
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [mdefs, setMdefs] = useState<MetricDefinition[]>([]);
  const k = KINDS.find((x) => x.kind === r.kind) ?? KINDS[0]!;
  const set = (patch: Partial<AlertRule>) => setR((cur) => ({ ...cur, ...patch }));
  // funktional — schnelle Eingaben in mehrere Felder dürfen sich nicht überschreiben
  const setParam = (key: string, v: string) => setR((cur) => ({ ...cur, params: { ...(cur.params ?? {}), [key]: v } }));
  const setArg = (key: string, v: string) => setR((cur) => ({ ...cur, actionArgs: { ...(cur.actionArgs ?? {}), [key]: v } }));
  const apiPrefix = `/api/orgs/${encodeURIComponent(orgId)}/clusters/${encodeURIComponent(clusterId)}/promql`;
  const remediationDef = r.actionDefinitionId ? defs.find((d) => d.id === r.actionDefinitionId) : null;

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') onClose();
    }
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [onClose]);

  const inputCls = 'rp-focus mt-1 h-9 w-full rounded-skin-sm border border-line bg-inset px-2.5 font-mono text-[12px] text-ink';
  // dedupe: derselbe Workload-Name kann in mehreren Namespaces existieren
  const workloads = [...new Set((map?.nodes ?? []).map((n) => n.name))].sort();
  useEffect(() => {
    if (r.kind === 'derived' && orgIdRef.current && clusterIdRef.current) {
      getMetricDefinitions(orgIdRef.current, clusterIdRef.current)
        .then((x) => setMdefs(x.definitions))
        .catch(() => {});
    }
  }, [r.kind]);

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center px-3" role="dialog" aria-modal="true" aria-label="Rule editor">
      <button type="button" aria-label="Close" onClick={onClose} className="absolute inset-0 cursor-default" style={{ backgroundColor: 'var(--rp-scrim)' }} />
      <div
        className={cn('relative max-h-[92vh] w-full overflow-y-auto rounded-skin border border-line bg-raised', r.kind === 'promql' ? 'sm:w-[640px]' : 'sm:w-[480px]')}
        style={{ boxShadow: 'var(--rp-rim), var(--rp-shadow-pop)', animation: 'reveal-up var(--rp-dur-med) var(--rp-ease-enter)' }}
      >
        <div className="border-b border-line px-4 py-3">
          <span className="font-mono text-[13px] font-semibold text-ink">{r.id ? 'Edit rule' : 'New check rule'}</span>
        </div>
        <div className="space-y-3 px-4 py-3">
          <label className="block">
            <span className="rp-micro !text-[10px]">name</span>
            <input value={r.name ?? ''} onChange={(e) => set({ name: e.target.value })} placeholder="payments error spike" className={inputCls} />
          </label>
          <label className="block">
            <span className="rp-micro !text-[10px]">condition</span>
            <select value={r.kind} onChange={(e) => set({ kind: e.target.value as RuleKind, params: {} })} className={inputCls}>
              {KINDS.map((x) => (
                <option key={x.kind} value={x.kind}>{x.label} — {x.hint}</option>
              ))}
            </select>
          </label>
          {r.kind === 'promql' ? (
            <PromQLRuleBlock apiPrefix={apiPrefix} query={r.query ?? ''} threshold={r.threshold} onChange={(q) => set({ query: q })} />
          ) : null}
          {r.kind === 'derived' ? (
            <label className="block">
              <span className="rp-micro !text-[10px]">metric</span>
              <select
                value={r.params?.definitionId ?? ''}
                onChange={(e) => setParam('definitionId', e.target.value)}
                className={inputCls}
              >
                <option value="">— select custom metric —</option>
                {mdefs.map((m) => (
                  <option key={m.id} value={m.id}>{m.name}{m.unit ? ` (${m.unit})` : ''}</option>
                ))}
              </select>
            </label>
          ) : null}
          {k.params.map((pk) => (
            <label key={pk} className="block">
              <span className="rp-micro !text-[10px]">{pk} <span className="text-faint">(optional = any)</span></span>
              {pk === 'service' || pk === 'workload' ? (
                <select value={r.params?.[pk] ?? ''} onChange={(e) => setParam(pk, e.target.value)} className={inputCls}>
                  <option value="">any</option>
                  {workloads.map((w) => <option key={w} value={w}>{w}</option>)}
                </select>
              ) : (
                <input value={r.params?.[pk] ?? ''} onChange={(e) => setParam(pk, e.target.value)} className={inputCls} />
              )}
            </label>
          ))}
          <div className="grid grid-cols-3 gap-2">
            <label className="block">
              <span className="rp-micro !text-[10px]">operator</span>
              <select value={r.op ?? 'gt'} onChange={(e) => set({ op: e.target.value as 'gt' | 'lt' })} className={inputCls}>
                <option value="gt">above &gt;</option>
                <option value="lt">below &lt;</option>
              </select>
            </label>
            <label className="block">
              <span className="rp-micro !text-[10px]">threshold{k.unit ? ` (${k.unit})` : ''}</span>
              <input type="number" value={r.threshold ?? 0} onChange={(e) => set({ threshold: Number(e.target.value) })} className={inputCls} />
            </label>
            <label className="block">
              <span className="rp-micro !text-[10px]">severity</span>
              <select value={r.severity ?? 'warning'} onChange={(e) => set({ severity: e.target.value as 'warning' | 'critical' })} className={inputCls}>
                <option value="warning">warning</option>
                <option value="critical">critical</option>
              </select>
            </label>
          </div>
          <div className="grid grid-cols-2 gap-2">
            <label className="block">
              <span className="rp-micro !text-[10px]">window (s) — min 30</span>
              <input type="number" min={30} max={3600} value={r.windowSeconds ?? 300} onChange={(e) => set({ windowSeconds: Number(e.target.value) })} className={inputCls} />
            </label>
            <label className="block">
              <span className="rp-micro !text-[10px]">for (s) — sustained before firing</span>
              <input type="number" value={r.forSeconds ?? 60} onChange={(e) => set({ forSeconds: Number(e.target.value) })} className={inputCls} />
            </label>
          </div>
          <div>
            <span className="rp-micro !text-[10px]">notify via</span>
            <div className="mt-1.5 flex flex-wrap gap-1.5">
              {providers.length === 0 ? (
                <span className="font-mono text-[10.5px] text-faint">no providers yet</span>
              ) : (
                providers.map((p) => {
                  const on = (r.providerIds ?? []).includes(p.id);
                  return (
                    <button
                      key={p.id}
                      type="button"
                      onClick={() =>
                        set({ providerIds: on ? (r.providerIds ?? []).filter((x) => x !== p.id) : [...(r.providerIds ?? []), p.id] })
                      }
                      className={cn('rounded-skin-chip border px-2 py-1 font-mono text-[10.5px] transition-colors', on ? 'bg-hover text-ink' : 'text-muted hover:text-ink')}
                      style={{ borderColor: on ? 'var(--rp-line-strong)' : 'var(--rp-line)' }}
                    >
                      {on ? '✓ ' : ''}{p.name}
                    </button>
                  );
                })
              )}
            </div>
          </div>

          {/* Auto-Remediation: firing dispatcht einen Starlark-Workflow */}
          <div className="rounded-skin-sm border border-line bg-inset/40 p-2.5">
            <span className="rp-micro !text-[10px]">auto-remediation <span className="text-faint">— run a workflow when this fires</span></span>
            <select
              value={r.actionDefinitionId ?? ''}
              onChange={(e) => {
                const def = defs.find((d) => d.id === e.target.value);
                const seeded: Record<string, string> = {};
                for (const p of def?.params ?? []) if (p.default) seeded[p.name] = p.default;
                set({ actionDefinitionId: e.target.value || null, actionArgs: seeded });
              }}
              className={inputCls}
            >
              <option value="">— none —</option>
              {defs.map((d) => (
                <option key={d.id} value={d.id}>{d.name}{d.description ? ` — ${d.description.slice(0, 48)}` : ''}</option>
              ))}
            </select>
            {remediationDef && (remediationDef.params ?? []).length > 0 ? (
              <div className="mt-2 grid grid-cols-2 gap-2">
                {(remediationDef.params ?? []).map((p) => (
                  <label key={p.name} className="block">
                    <span className="rp-micro !text-[10px]">{p.label || p.name}{p.required ? ' *' : ''}</span>
                    <input
                      value={r.actionArgs?.[p.name] ?? ''}
                      onChange={(e) => setArg(p.name, e.target.value)}
                      placeholder={p.default ?? ''}
                      className={inputCls}
                    />
                  </label>
                ))}
              </div>
            ) : null}
            {remediationDef ? (
              <p className="mt-1.5 font-mono text-[9.5px] leading-relaxed text-faint">
                dispatched once per firing transition · full pipeline with verify + rollback · shows up under actions → runs
              </p>
            ) : null}
          </div>
          {err ? <div className="font-mono text-[11px]" style={{ color: 'var(--rp-tone-red-fg)' }}>{err}</div> : null}
        </div>
        <div className="flex items-center gap-2 border-t border-line px-4 py-3">
          <button
            type="button"
            disabled={busy}
            onClick={async () => {
              setBusy(true);
              setErr(null);
              try {
                await onSave(r);
              } catch (e) {
                setErr(e instanceof Error ? e.message : 'save failed');
                setBusy(false);
                return;
              }
              setBusy(false);
            }}
            className="rp-focus h-8 rounded-skin-sm px-3.5 font-mono text-[11.5px] font-semibold transition-opacity hover:opacity-90"
            style={{ background: 'var(--rp-btn-bg)', color: 'var(--rp-btn-fg)', opacity: busy ? 0.55 : 1 }}
          >
            Save
          </button>
          <button type="button" onClick={onClose} className="h-8 rounded-skin-sm border border-line px-3 font-mono text-[11.5px] text-mid transition-colors hover:bg-hover hover:text-ink">
            Cancel
          </button>
          {onDelete ? (
            <button type="button" onClick={() => void onDelete()} className="ml-auto rounded-skin-sm border border-line px-2.5 py-1.5 font-mono text-[11px] transition-colors hover:bg-hover" style={{ color: 'var(--rp-tone-red-fg)' }}>
              delete
            </button>
          ) : null}
        </div>
      </div>
    </div>
  );
}

/* PromQL-Bedingung: echter Editor + debounced Live-Preview mit Threshold. */
function PromQLRuleBlock({
  apiPrefix,
  query,
  threshold,
  onChange,
}: {
  apiPrefix: string;
  query: string;
  threshold: number | undefined;
  onChange: (q: string) => void;
}) {
  const [series, setSeries] = useState<MetricSeries[] | null>(null);
  const [perr, setPerr] = useState<string | null>(null);

  useEffect(() => {
    if (!query.trim()) {
      setSeries(null);
      setPerr(null);
      return;
    }
    const t = setTimeout(async () => {
      try {
        const end = Date.now() / 1000;
        const start = end - 15 * 60;
        const q = new URLSearchParams({ query, start: String(start), end: String(end), step: '15' });
        const res = await fetch(`${apiPrefix}/api/v1/query_range?${q}`, { credentials: 'include' });
        const body = await res.json();
        if (body.status !== 'success') {
          setPerr(body.error ?? 'query failed');
          setSeries(null);
        } else if (body.data.resultType === 'matrix') {
          setPerr(null);
          setSeries(
            body.data.result.map((s: { metric: Record<string, string>; values: [number, string][] }) => ({
              name: s.metric['service_name'] ?? s.metric['__name__'] ?? 'value',
              points: s.values.map(([ts, v]: [number, string]) => ({ t: ts * 1000, v: Number(v) })),
            })),
          );
        }
      } catch {
        setPerr('preview failed');
      }
    }, 700);
    return () => clearTimeout(t);
  }, [query, apiPrefix]);

  return (
    <div>
      <span className="rp-micro !text-[10px]">promql expression <span className="text-faint">— alert on max over all series</span></span>
      <div className="mt-1">
        <PromQLEditor value={query} apiPrefix={apiPrefix} onChange={onChange} onRun={() => {}} />
      </div>
      {perr ? (
        <div className="mt-1.5 font-mono text-[10.5px]" style={{ color: 'var(--rp-tone-red-fg)' }}>{perr}</div>
      ) : series && series.length > 0 ? (
        <div className="mt-2">
          <LineChart title="preview · last 15m" unit={`${series.length} series`} series={series.slice(0, 8)} height={150} threshold={threshold} />
        </div>
      ) : series && series.length === 0 ? (
        <div className="mt-1.5 font-mono text-[10.5px] text-faint">empty result — rule would stay ok</div>
      ) : null}
    </div>
  );
}

/* ── Provider-Editor ────────────────────────────────────────────────────── */

const PROVIDER_FIELDS: Record<ProviderType, { key: string; label: string; placeholder: string }[]> = {
  webhook: [
    { key: 'url', label: 'url', placeholder: 'https://example.com/hooks/rocketplane' },
    { key: 'authorization', label: 'authorization header (optional)', placeholder: 'Bearer …' },
  ],
  slack: [{ key: 'url', label: 'incoming webhook url', placeholder: 'https://hooks.slack.com/services/…' }],
  email: [
    { key: 'host', label: 'smtp host', placeholder: 'smtp.example.com' },
    { key: 'port', label: 'smtp port', placeholder: '587' },
    { key: 'from', label: 'from', placeholder: 'alerts@example.com' },
    { key: 'to', label: 'to (comma-separated)', placeholder: 'oncall@example.com' },
    { key: 'user', label: 'user (optional)', placeholder: '' },
    { key: 'password', label: 'password (optional)', placeholder: '' },
  ],
};

function ProviderEditor({
  onClose,
  onSave,
}: {
  onClose: () => void;
  onSave: (name: string, type: ProviderType, config: Record<string, string>) => Promise<void>;
}) {
  const [name, setName] = useState('');
  const [type, setType] = useState<ProviderType>('webhook');
  const [config, setConfig] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') onClose();
    }
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [onClose]);

  const inputCls = 'rp-focus mt-1 h-9 w-full rounded-skin-sm border border-line bg-inset px-2.5 font-mono text-[12px] text-ink placeholder:text-faint';

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center px-3" role="dialog" aria-modal="true" aria-label="Provider editor">
      <button type="button" aria-label="Close" onClick={onClose} className="absolute inset-0 cursor-default" style={{ backgroundColor: 'var(--rp-scrim)' }} />
      <div className="relative w-full max-w-[400px] rounded-skin border border-line bg-raised" style={{ boxShadow: 'var(--rp-rim), var(--rp-shadow-pop)', animation: 'reveal-up var(--rp-dur-med) var(--rp-ease-enter)' }}>
        <div className="border-b border-line px-4 py-3">
          <span className="font-mono text-[13px] font-semibold text-ink">New provider</span>
        </div>
        <div className="space-y-2.5 px-4 py-3">
          <label className="block">
            <span className="rp-micro !text-[10px]">name</span>
            <input value={name} onChange={(e) => setName(e.target.value)} placeholder="oncall-webhook" className={inputCls} />
          </label>
          <label className="block">
            <span className="rp-micro !text-[10px]">type</span>
            <select value={type} onChange={(e) => { setType(e.target.value as ProviderType); setConfig({}); }} className={inputCls}>
              <option value="webhook">webhook — POST JSON</option>
              <option value="slack">slack — incoming webhook</option>
              <option value="email">email — smtp</option>
            </select>
          </label>
          {PROVIDER_FIELDS[type].map((f) => (
            <label key={f.key} className="block">
              <span className="rp-micro !text-[10px]">{f.label}</span>
              <input
                value={config[f.key] ?? ''}
                onChange={(e) => setConfig((c) => ({ ...c, [f.key]: e.target.value }))}
                placeholder={f.placeholder}
                className={inputCls}
              />
            </label>
          ))}
          {err ? <div className="font-mono text-[11px]" style={{ color: 'var(--rp-tone-red-fg)' }}>{err}</div> : null}
        </div>
        <div className="flex items-center gap-2 border-t border-line px-4 py-3">
          <button
            type="button"
            disabled={busy}
            onClick={async () => {
              setBusy(true);
              setErr(null);
              try {
                await onSave(name, type, config);
              } catch (e) {
                setErr(e instanceof Error ? e.message : 'save failed');
                setBusy(false);
                return;
              }
              setBusy(false);
            }}
            className="rp-focus h-8 rounded-skin-sm px-3.5 font-mono text-[11.5px] font-semibold transition-opacity hover:opacity-90"
            style={{ background: 'var(--rp-btn-bg)', color: 'var(--rp-btn-fg)', opacity: busy ? 0.55 : 1 }}
          >
            Save
          </button>
          <button type="button" onClick={onClose} className="h-8 rounded-skin-sm border border-line px-3 font-mono text-[11.5px] text-mid transition-colors hover:bg-hover hover:text-ink">
            Cancel
          </button>
        </div>
      </div>
    </div>
  );
}
