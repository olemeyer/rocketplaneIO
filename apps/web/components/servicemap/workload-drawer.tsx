'use client';

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import Link from 'next/link';
import { cn } from '@/lib/cn';
import { Spinner } from '@/components/ui';
import { cancelAction, createAction, getActions, getLogs, getTraces, getWorkloadPods, setWorkloadIcon } from '@/lib/api/controlplane';
import { useClusterEvents } from '@/lib/hooks/use-cluster-events';
import { ActionRunCard } from '@/components/actions/run-card';
import { LogDrawer } from '@/components/logs/log-drawer';
import { fmtMs, statusTone, TONE_CHIP } from '@/components/traces/trace-drawer';
import { detectTech, getTechIcon, listTechIcons } from '@/lib/tech-icons';
import type { ActionKind, ClusterAction, LogLine, MapNode, TraceRow, WorkloadPod } from '@/lib/api/types';

// workload-drawer.tsx — das Workload-SIDEPANEL der Service-Map: dockt rechts
// in der Map an (KEIN Scrim — die Map bleibt les- und bedienbar), zeigt die
// Vitals + Pods des selektierten Workloads und verbindet sie mit SAFE-ACTIONS:
// rollout restart, scale, pod recreate. Jede Aktion ist zweistufig bestätigt
// und zeigt ehrlich ihr kubectl-Äquivalent — der Agent führt sie im Cluster
// aus (outbound-only) und meldet das Ergebnis in den Activity-Feed zurück.

const POLL_MS = 2500;

type Confirm =
  | { kind: 'rollout_restart' }
  | { kind: 'scale'; replicas: number }
  | { kind: 'delete_pod'; pod: string }
  | { kind: 'set_image'; image: string }
  | { kind: 'rollout_pause' }
  | { kind: 'rollout_resume' };

// führenden Quell-Timestamp im Body trimmen (identisch zur Logs-Seite)
const LEADING_TS = /^\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:[.,]\d+)?(?:Z|[+-]\d{2}:?\d{2})?\s*/;

const TABS = ['logs', 'traces', 'pods', 'activity'] as const;
type Tab = (typeof TABS)[number];

function kubectlOf(c: Confirm, node: MapNode): string {
  const ns = node.namespace;
  const ref = `${node.kind.toLowerCase()}/${node.name}`;
  if (c.kind === 'rollout_restart') return `kubectl rollout restart ${ref} -n ${ns}`;
  if (c.kind === 'scale') return `kubectl scale ${ref} --replicas=${c.replicas} -n ${ns}`;
  if (c.kind === 'set_image') return `kubectl set image ${ref} *=${c.image} -n ${ns}`;
  if (c.kind === 'rollout_pause') return `kubectl rollout pause ${ref} -n ${ns}`;
  if (c.kind === 'rollout_resume') return `kubectl rollout resume ${ref} -n ${ns}`;
  return `kubectl delete pod ${c.pod} -n ${ns}`;
}

export function WorkloadDrawer({
  orgId,
  clusterId,
  node,
  onClose,
}: {
  orgId: string;
  clusterId: string;
  node: MapNode;
  onClose: () => void;
}) {
  const [pods, setPods] = useState<WorkloadPod[] | null>(null);
  const [actions, setActions] = useState<ClusterAction[] | null>(null);
  const [confirm, setConfirm] = useState<Confirm | null>(null);
  const [replicas, setReplicas] = useState(node.podsTotal);
  // Image-Tag-Eingabe für set_image (vorbelegt mit dem aktuellen Image).
  const [image, setImage] = useState(node.image ?? '');
  const [firing, setFiring] = useState(false);
  // Investigations-Tabs: der Klick auf einen Node beantwortet sofort
  // „was passiert hier GERADE" — Logs, Spans, Pods, Aktionen.
  const [tab, setTab] = useState<Tab>('logs');
  const [iconPicker, setIconPicker] = useState(false);
  const [iconQuery, setIconQuery] = useState('');
  const [logs, setLogs] = useState<LogLine[] | null>(null);
  const [traces, setTraces] = useState<TraceRow[] | null>(null);
  const [openLine, setOpenLine] = useState<LogLine | null>(null);

  useEffect(() => setReplicas(node.podsTotal), [node.id, node.podsTotal]);
  useEffect(() => setImage(node.image ?? ''), [node.id, node.image]);
  useEffect(() => setConfirm(null), [node.id]);

  // Esc schließt erst die Bestätigung, dann (Map-Handler) das Panel —
  // capture-Phase, damit wir vor dem Map-Escape drankommen.
  useEffect(() => {
    if (!confirm) return;
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') {
        e.stopPropagation();
        setConfirm(null);
      }
    }
    document.addEventListener('keydown', onKey, true);
    return () => document.removeEventListener('keydown', onKey, true);
  }, [confirm]);

  const refresh = useCallback(() => {
    getWorkloadPods(orgId, clusterId, node.namespace, node.kind, node.name)
      .then((r) => setPods(r.pods))
      .catch(() => setPods([]));
    getActions(orgId, clusterId, node.namespace, node.name, 12)
      .then((r) => setActions(r.actions))
      .catch(() => setActions([]));
  }, [orgId, clusterId, node.namespace, node.kind, node.name]);

  const loadLogs = useCallback(() => {
    getLogs(orgId, clusterId, { workload: node.name, limit: 40 })
      .then((r) => setLogs(r.lines))
      .catch(() => setLogs([]));
  }, [orgId, clusterId, node.name]);

  const loadTraces = useCallback(() => {
    getTraces(orgId, clusterId, { service: node.name })
      .then((r) => setTraces(r.traces.slice(0, 25)))
      .catch(() => setTraces([]));
  }, [orgId, clusterId, node.name]);

  // Tab-Daten: beim Node-Wechsel zurücksetzen; aktiver Tab lädt sofort.
  useEffect(() => {
    setLogs(null);
    setTraces(null);
  }, [node.id]);
  useEffect(() => {
    if (tab === 'logs') loadLogs();
    if (tab === 'traces') loadTraces();
  }, [tab, loadLogs, loadTraces]);
  // Traces haben kein Push-Signal (Collector → ClickHouse) → sanfter Poll,
  // nur solange der Tab offen ist.
  useEffect(() => {
    if (tab !== 'traces') return;
    const t = setInterval(loadTraces, 10_000);
    return () => clearInterval(t);
  }, [tab, loadTraces]);

  // Live: SSE-Signale (topology → Pods, actions → Feed) treiben den Refresh;
  // der Poll bleibt als seltener Fallback.
  const refreshRef = useRef(refresh);
  refreshRef.current = refresh;
  const tabRef = useRef(tab);
  tabRef.current = tab;
  const loadLogsRef = useRef(loadLogs);
  loadLogsRef.current = loadLogs;
  const { live } = useClusterEvents(orgId, clusterId, {
    topology: () => refreshRef.current(),
    actions: () => refreshRef.current(),
    logs: () => {
      if (tabRef.current === 'logs') loadLogsRef.current();
    },
  });
  useEffect(() => {
    setPods(null);
    setActions(null);
    refresh();
    const t = setInterval(refresh, live ? 15_000 : POLL_MS);
    return () => clearInterval(t);
  }, [refresh, live]);

  const fire = useCallback(async () => {
    if (!confirm || firing) return;
    setFiring(true);
    try {
      const body =
        confirm.kind === 'delete_pod'
          ? {
              kind: 'delete_pod' as ActionKind,
              targetNamespace: node.namespace,
              targetKind: 'Pod',
              targetName: confirm.pod,
            }
          : {
              kind: confirm.kind as ActionKind,
              targetNamespace: node.namespace,
              targetKind: node.kind,
              targetName: node.name,
              params:
                confirm.kind === 'scale'
                  ? { replicas: confirm.replicas }
                  : confirm.kind === 'set_image'
                    ? { image: confirm.image }
                    : {},
            };
      await createAction(orgId, clusterId, body);
      setConfirm(null);
      refresh();
    } catch {
      // Feed zeigt den Zustand; Fehler beim Anlegen lässt die Bestätigung stehen.
    } finally {
      setFiring(false);
    }
  }, [confirm, firing, node, orgId, clusterId, refresh]);

  // Read-only Investigation-Actions (debug_bundle, pod_events) mutieren nichts —
  // sie feuern ohne Bestätigung und springen in den Activity-Tab, wo die
  // Run-Karte die gesammelten Sektionen als Step-Details zeigt.
  const fireReadonly = useCallback(
    async (kind: ActionKind, targetKind: string, targetName: string) => {
      try {
        await createAction(orgId, clusterId, {
          kind,
          targetNamespace: node.namespace,
          targetKind,
          targetName,
          params: {},
        });
        setTab('activity');
        refresh();
      } catch {
        /* Feed zeigt den Zustand; ein Fehler beim Anlegen ist selbsterklärend. */
      }
    },
    [orgId, clusterId, node.namespace, refresh],
  );

  const canScale = node.kind === 'Deployment' || node.kind === 'StatefulSet';
  const canRestart = canScale || node.kind === 'DaemonSet';
  // set_image gilt für alle Template-Workloads; pause/resume nur für Deployment.
  const canSetImage = canRestart;
  const canPause = node.kind === 'Deployment';
  const imageChanged = image.trim() !== '' && image.trim() !== (node.image ?? '');
  const healthColor =
    node.health === 'critical'
      ? 'var(--rp-red)'
      : node.health === 'degraded'
        ? 'var(--rp-yellow)'
        : 'var(--rp-ink-muted)';

  const activity = useMemo(() => actions ?? [], [actions]);
  const techIcon = getTechIcon(node.icon || detectTech(node.image));
  const pickIcon = async (slug: string) => {
    setIconPicker(false);
    setIconQuery('');
    // '' = zurück zur Auto-Erkennung; sonst manueller Override — persistiert
    // in der workloads-Tabelle, SSE bringt die Map sofort auf Stand.
    await setWorkloadIcon(orgId, clusterId, {
      namespace: node.namespace,
      kind: node.kind,
      name: node.name,
      icon: slug,
    }).catch(() => {});
  };

  return (
    <aside
      className="absolute inset-y-2 right-2 z-20 flex w-[min(400px,92%)] flex-col overflow-hidden rounded-skin border border-line bg-raised"
      style={{
        boxShadow: 'var(--rp-rim), var(--rp-shadow-pop)',
        animation: 'rp-drawer-in var(--rp-dur-large) var(--rp-ease-enter)',
      }}
      aria-label="Workload panel"
    >
      {/* Masthead */}
      <header className="shrink-0 border-b border-line px-4 pb-3 pt-3.5">
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <div className="rp-micro !text-[10px]">workload / {node.namespace}</div>
            <div className="mt-1 flex items-center gap-2 border-b border-line-strong pb-2">
              <span style={{ color: healthColor }} className="font-mono text-[12px]" title={node.health}>
                {node.health === 'critical' ? '▲' : node.health === 'degraded' ? '◆' : '●'}
              </span>
              {/* Tech-Logo — Klick öffnet den Picker (Override wird gemerkt) */}
              <button
                type="button"
                onClick={() => setIconPicker((v) => !v)}
                title={techIcon ? `${techIcon.title} — click to change` : 'set technology icon'}
                className="rp-focus flex h-6 w-6 shrink-0 items-center justify-center rounded-skin-sm border border-line transition-colors hover:bg-hover"
              >
                {techIcon ? (
                  <svg width="14" height="14" viewBox="0 0 24 24" aria-hidden>
                    <path d={techIcon.path} fill="var(--rp-ink)" opacity={0.85} />
                  </svg>
                ) : (
                  <svg width="12" height="12" viewBox="0 0 16 16" fill="none" aria-hidden>
                    <rect x="2.5" y="2.5" width="11" height="11" rx="2" stroke="var(--rp-ink-faint)" strokeWidth="1.4" strokeDasharray="2.5 2" />
                  </svg>
                )}
              </button>
              <h2 className="truncate text-[16px] font-bold leading-tight tracking-tightest text-ink">
                {node.name}
              </h2>
              <span className="shrink-0 rounded-skin-chip bg-inset px-1.5 py-0.5 font-mono text-[9.5px] uppercase tracking-[0.06em] text-muted">
                {node.kind}
              </span>
            </div>
          </div>
          <button
            type="button"
            onClick={onClose}
            aria-label="Close"
            className="rp-focus mt-0.5 inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-skin-sm border border-line text-muted transition-colors hover:text-ink"
          >
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" aria-hidden>
              <path d="M6 6l12 12M18 6L6 18" stroke="currentColor" strokeWidth="1.8" strokeLinecap="square" />
            </svg>
          </button>
        </div>
        {/* Vitals */}
        <div className="mt-2.5 flex items-center gap-4 font-mono text-[11px] text-mid tnum">
          <span title="pods ready / desired">pods {node.podsReady}/{node.podsTotal}</span>
          {node.restarts > 0 ? (
            <span style={{ color: 'var(--rp-yellow)' }}>↻ {node.restarts}</span>
          ) : null}
          <span style={{ color: healthColor }}>
            {node.podsTotal === 0 ? 'scaled to zero' : node.health}
          </span>
          <span className="ml-auto flex items-center gap-1.5">
            <Link
              href={`/clusters/${clusterId}/logs?workload=${encodeURIComponent(node.name)}`}
              className="rounded-skin-sm border border-line px-2 py-1 text-[10.5px] text-mid transition-colors hover:bg-hover hover:text-ink"
            >
              Logs →
            </Link>
            <Link
              href={`/clusters/${clusterId}/traces?service=${encodeURIComponent(node.name)}`}
              className="rounded-skin-sm border border-line px-2 py-1 text-[10.5px] text-mid transition-colors hover:bg-hover hover:text-ink"
            >
              Traces →
            </Link>
          </span>
        </div>
      </header>

      <div className="min-h-0 flex-1 overflow-y-auto px-4 py-3.5">
        {iconPicker ? (
          <div
            className="mb-3 rounded-skin-sm border p-2.5"
            style={{ borderColor: 'var(--rp-line-strong)', animation: 'reveal-up var(--rp-dur-med) var(--rp-ease-enter)' }}
          >
            <div className="flex items-center gap-2">
              <input
                autoFocus
                value={iconQuery}
                onChange={(e) => setIconQuery(e.target.value)}
                placeholder="search 170+ technologies…"
                className="rp-focus h-8 min-w-0 flex-1 rounded-skin-sm border border-line bg-inset px-2.5 font-mono text-[11.5px] text-ink placeholder:text-faint"
              />
              {node.icon ? (
                <button
                  type="button"
                  onClick={() => void pickIcon('')}
                  className="shrink-0 rounded-skin-sm border border-line px-2 py-1.5 font-mono text-[10px] text-muted transition-colors hover:text-ink"
                  title="zurück zur Auto-Erkennung"
                >
                  auto
                </button>
              ) : null}
            </div>
            <div className="mt-2 grid max-h-44 grid-cols-8 gap-1 overflow-y-auto">
              {listTechIcons()
                .filter((i) => i.title.toLowerCase().includes(iconQuery.toLowerCase()))
                .slice(0, 64)
                .map((i) => (
                  <button
                    key={i.slug}
                    type="button"
                    title={i.title}
                    onClick={() => void pickIcon(i.slug)}
                    className={cn(
                      'rp-focus flex h-9 items-center justify-center rounded-skin-sm border transition-colors hover:bg-hover',
                      techIcon?.slug === i.slug ? 'bg-hover' : '',
                    )}
                    style={{
                      borderColor: techIcon?.slug === i.slug ? 'var(--rp-line-strong)' : 'var(--rp-line)',
                    }}
                  >
                    <svg width="16" height="16" viewBox="0 0 24 24" aria-hidden>
                      <path d={i.path} fill="var(--rp-ink)" opacity={0.8} />
                    </svg>
                  </button>
                ))}
            </div>
          </div>
        ) : null}
        {/* ── Safe-Actions ── */}
        <div className="rp-micro !text-[10px]">actions</div>
        <div className="mt-2 flex flex-wrap items-center gap-2">
          {canRestart ? (
            <button
              type="button"
              onClick={() => setConfirm({ kind: 'rollout_restart' })}
              className="rp-focus rounded-skin-sm border border-line px-2.5 py-1.5 font-mono text-[11px] text-ink transition-colors hover:bg-hover"
            >
              ↻ rollout restart
            </button>
          ) : null}
          {canScale ? (
            <span className="flex items-center overflow-hidden rounded-skin-sm border border-line font-mono text-[11px]">
              <button
                type="button"
                onClick={() => setReplicas((r) => Math.max(0, r - 1))}
                className="px-2 py-1.5 text-muted transition-colors hover:bg-hover hover:text-ink"
                aria-label="decrease replicas"
              >
                −
              </button>
              <span className="min-w-[54px] px-1 text-center text-ink tnum">
                {replicas} <span className="text-faint">repl</span>
              </span>
              <button
                type="button"
                onClick={() => setReplicas((r) => Math.min(50, r + 1))}
                className="px-2 py-1.5 text-muted transition-colors hover:bg-hover hover:text-ink"
                aria-label="increase replicas"
              >
                +
              </button>
              <button
                type="button"
                disabled={replicas === node.podsTotal}
                onClick={() => setConfirm({ kind: 'scale', replicas })}
                className={cn(
                  'border-l border-line px-2.5 py-1.5 transition-colors',
                  replicas === node.podsTotal
                    ? 'cursor-default text-faint'
                    : 'text-ink hover:bg-hover',
                )}
              >
                scale
              </button>
            </span>
          ) : null}
          {canPause ? (
            <>
              <button
                type="button"
                onClick={() => setConfirm({ kind: 'rollout_pause' })}
                className="rp-focus rounded-skin-sm border border-line px-2.5 py-1.5 font-mono text-[11px] text-ink transition-colors hover:bg-hover"
                title="kubectl rollout pause"
              >
                ⏸ pause
              </button>
              <button
                type="button"
                onClick={() => setConfirm({ kind: 'rollout_resume' })}
                className="rp-focus rounded-skin-sm border border-line px-2.5 py-1.5 font-mono text-[11px] text-ink transition-colors hover:bg-hover"
                title="kubectl rollout resume"
              >
                ▷ resume
              </button>
            </>
          ) : null}
          <button
            type="button"
            onClick={() => void fireReadonly('debug_bundle', node.kind, node.name)}
            className="rp-focus rounded-skin-sm border border-line px-2.5 py-1.5 font-mono text-[11px] text-mid transition-colors hover:bg-hover hover:text-ink"
            title="read-only triage snapshot: rollout state + container statuses + events"
          >
            ⚑ debug bundle
          </button>
        </div>

        {/* set_image: der häufigste Release-Handgriff — Tag setzen, voller
            verifizierter Rollout. Vorbelegt mit dem aktuellen Image. */}
        {canSetImage ? (
          <div className="mt-2 flex items-center gap-2">
            <input
              value={image}
              onChange={(e) => setImage(e.target.value)}
              spellCheck={false}
              placeholder="repo/image:tag"
              className="rp-focus h-8 min-w-0 flex-1 rounded-skin-sm border border-line bg-inset px-2.5 font-mono text-[11px] text-ink placeholder:text-faint"
            />
            <button
              type="button"
              disabled={!imageChanged}
              onClick={() => setConfirm({ kind: 'set_image', image: image.trim() })}
              className={cn(
                'rp-focus h-8 shrink-0 rounded-skin-sm border border-line px-2.5 font-mono text-[11px] transition-colors',
                imageChanged ? 'text-ink hover:bg-hover' : 'cursor-default text-faint',
              )}
              title="kubectl set image"
            >
              deploy
            </button>
          </div>
        ) : null}

        {/* Bestätigung — Twin-Turbine-Muster: Titel, ehrliches kubectl-Äquivalent,
            monochromer Primary (btn-Tokens — Orange ist nie CTA) */}
        {confirm ? (
          <div
            className="mt-2.5 overflow-hidden rounded-skin-sm border"
            style={{
              borderColor: 'var(--rp-line-strong)',
              animation: 'reveal-up var(--rp-dur-med) var(--rp-ease-enter)',
            }}
          >
            <div className="flex items-center justify-between border-b border-line bg-raised px-3 py-2">
              <span className="font-mono text-[11.5px] font-semibold text-ink">
                {confirm.kind === 'rollout_restart'
                  ? `Restart ${node.name}?`
                  : confirm.kind === 'scale'
                    ? `Scale ${node.name} to ${confirm.replicas} replica${confirm.replicas === 1 ? '' : 's'}?`
                    : confirm.kind === 'set_image'
                      ? `Deploy new image to ${node.name}?`
                      : confirm.kind === 'rollout_pause'
                        ? `Pause rollout of ${node.name}?`
                        : confirm.kind === 'rollout_resume'
                          ? `Resume rollout of ${node.name}?`
                          : `Recreate pod?`}
              </span>
              <span className="rp-micro !text-[9px]">runs in cluster</span>
            </div>
            <div className="bg-inset px-3 py-2.5">
              <code className="block break-all font-mono text-[11px] leading-relaxed text-mid">
                <span className="text-faint">$ </span>
                {kubectlOf(confirm, node)}
              </code>
            </div>
            <div className="flex items-center gap-2 border-t border-line bg-raised px-3 py-2">
              <button
                type="button"
                onClick={fire}
                disabled={firing}
                className="rp-focus h-8 rounded-skin-sm px-3.5 font-mono text-[11.5px] font-semibold transition-opacity hover:opacity-90"
                style={{
                  background: 'var(--rp-btn-bg)',
                  color: 'var(--rp-btn-fg)',
                  opacity: firing ? 0.55 : 1,
                }}
              >
                {firing ? 'dispatching…' : 'Execute'}
              </button>
              <button
                type="button"
                onClick={() => setConfirm(null)}
                className="rp-focus h-8 rounded-skin-sm border border-line px-3 font-mono text-[11.5px] text-mid transition-colors hover:bg-hover hover:text-ink"
              >
                Cancel
              </button>
              <kbd className="ml-auto rounded-skin-chip border border-line px-1.5 py-0.5 font-mono text-[9px] text-faint">
                esc
              </kbd>
            </div>
          </div>
        ) : null}

        {/* ── Investigations-Tabs: logs · traces · pods · activity ── */}
        <div className="mt-4 flex items-center justify-between">
          <div className="flex items-center rounded-skin-sm border border-line p-[2px]">
            {TABS.map((t) => (
              <button
                key={t}
                type="button"
                onClick={() => setTab(t)}
                className={cn(
                  'rp-focus rounded-[3px] px-2 py-[3px] font-mono text-[10px] uppercase tracking-[0.06em] transition-colors',
                  tab === t ? 'bg-hover text-ink' : 'text-muted hover:text-ink',
                )}
                style={tab === t ? { boxShadow: 'inset 0 0 0 1px var(--rp-line-strong)' } : undefined}
              >
                {t}
              </button>
            ))}
          </div>
          <span className="font-mono text-[10px] text-faint tnum">
            {tab === 'pods'
              ? (pods?.length ?? '…')
              : tab === 'logs'
                ? (logs?.length ?? '…')
                : tab === 'traces'
                  ? (traces?.length ?? '…')
                  : (actions?.length ?? '…')}
          </span>
        </div>

        {/* ── LOGS: die aktuellsten Zeilen des Workloads, live (SSE) ── */}
        {tab === 'logs' ? (
          logs === null ? (
            <div className="mt-2 flex items-center gap-2 text-muted">
              <Spinner /> <span className="font-mono text-[11px]">loading…</span>
            </div>
          ) : logs.length === 0 ? (
            <div className="mt-2 font-mono text-[11px] text-faint">no recent logs</div>
          ) : (
            <div className="mt-2 overflow-hidden rounded-skin-sm border border-line">
              {logs.map((l, i) => {
                const isErr = l.severityNumber >= 17;
                const isWarn = l.severityNumber >= 13 && !isErr;
                return (
                  <button
                    key={`${l.ts}-${i}`}
                    type="button"
                    onClick={() => setOpenLine(l)}
                    className="flex w-full items-start gap-2 border-b border-line/60 px-2 py-[3px] text-left font-mono text-[10.5px] leading-relaxed transition-colors last:border-0 hover:bg-hover"
                  >
                    <span className="w-[3px] shrink-0 self-stretch">
                      <span
                        className="block h-full w-[2px]"
                        style={{
                          background: isErr ? 'var(--rp-red)' : isWarn ? 'var(--rp-yellow)' : 'transparent',
                        }}
                      />
                    </span>
                    <span className="shrink-0 pt-px text-muted tnum">{l.ts.slice(11, 19)}</span>
                    <span
                      className="min-w-0 flex-1 truncate"
                      style={{
                        color: isErr
                          ? 'var(--rp-tone-red-fg)'
                          : isWarn
                            ? 'var(--rp-tone-yellow-fg)'
                            : 'var(--rp-ink)',
                      }}
                    >
                      {l.body.replace(LEADING_TS, '')}
                    </span>
                  </button>
                );
              })}
            </div>
          )
        ) : null}

        {/* ── TRACES: die jüngsten Spans dieses Service ── */}
        {tab === 'traces' ? (
          traces === null ? (
            <div className="mt-2 flex items-center gap-2 text-muted">
              <Spinner /> <span className="font-mono text-[11px]">loading…</span>
            </div>
          ) : traces.length === 0 ? (
            <div className="mt-2 font-mono text-[11px] text-faint">no recent spans</div>
          ) : (
            <div className="mt-2 overflow-hidden rounded-skin-sm border border-line">
              {traces.map((t) => {
                const tone = statusTone(t.httpStatus ?? '', t.statusCode ?? '');
                const chip = TONE_CHIP[tone];
                return (
                  <Link
                    key={t.traceId}
                    href={`/clusters/${clusterId}/traces/${t.traceId}`}
                    className="flex w-full items-center gap-2 border-b border-line/60 px-2 py-[4px] font-mono text-[10.5px] transition-colors last:border-0 hover:bg-hover"
                  >
                    <span className="shrink-0 text-muted tnum">{t.ts.slice(11, 19)}</span>
                    <span className="min-w-0 flex-1 truncate text-ink">{t.spanName}</span>
                    {t.httpStatus ? (
                      <span
                        className="shrink-0 rounded-skin-chip px-1 py-px text-[9px]"
                        style={{ color: chip.fg, background: chip.bg }}
                      >
                        {t.httpStatus}
                      </span>
                    ) : null}
                    {t.errorCount > 0 ? (
                      <span className="shrink-0 text-[9.5px]" style={{ color: 'var(--rp-tone-red-fg)' }}>
                        ▲{t.errorCount}
                      </span>
                    ) : null}
                    <span
                      className="w-[56px] shrink-0 text-right tnum"
                      style={{ color: tone === 'err' ? 'var(--rp-red)' : 'var(--rp-ink)' }}
                    >
                      {fmtMs(t.durationMs)}
                    </span>
                  </Link>
                );
              })}
            </div>
          )
        ) : null}

        {/* ── PODS ── */}
        {tab !== 'pods' ? null : pods === null ? (
          <div className="mt-2 flex items-center gap-2 text-muted">
            <Spinner /> <span className="font-mono text-[11px]">loading…</span>
          </div>
        ) : pods.length === 0 ? (
          node.podsTotal === 0 ? (
            // Scaled to zero — ein bewusster Zustand, kein Datenloch.
            <div
              className="mt-2 flex items-center gap-2 rounded-skin-sm border border-dashed px-2.5 py-2 font-mono text-[11px]"
              style={{ borderColor: 'var(--rp-line-strong)', color: 'var(--rp-ink-muted)' }}
            >
              <span className="inline-block h-[7px] w-[7px] rounded-full border" style={{ borderColor: 'var(--rp-ink-faint)' }} />
              scaled to zero — no pods running
            </div>
          ) : (
            <div className="mt-2 font-mono text-[11px] text-faint">no pods reported</div>
          )
        ) : (
          <div className="mt-2 overflow-hidden rounded-skin-sm border border-line">
            {pods.map((p) => {
              // Lifecycle sichtbar machen: Terminating fährt raus (gold, gedimmt),
              // frisch gestartete Pods tragen kurz einen „new"-Puls.
              const terminating = p.phase === 'Terminating';
              const isNew =
                !terminating && Date.now() - new Date(p.firstSeenAt).getTime() < 60_000;
              const starting = !terminating && !p.ready;
              return (
                <div
                  key={p.name}
                  className="flex items-center gap-2 border-b border-line/60 px-2 py-1.5 font-mono text-[10.5px] last:border-0"
                  style={terminating ? { opacity: 0.62 } : undefined}
                >
                  <span
                    className={cn(
                      'h-[7px] w-[7px] shrink-0 rounded-full',
                      (terminating || starting) && 'rp-breath',
                    )}
                    style={{
                      background: terminating
                        ? 'var(--rp-yellow)'
                        : p.ready
                          ? 'var(--rp-green)'
                          : 'var(--rp-yellow)',
                      color: terminating || starting ? 'var(--rp-yellow)' : undefined,
                    }}
                    title={terminating ? 'terminating' : p.ready ? 'ready' : p.phase}
                  />
                  <span
                    className={cn('min-w-0 flex-1 truncate', terminating ? 'text-muted line-through decoration-1' : 'text-ink')}
                    title={p.name}
                  >
                    {p.name}
                  </span>
                  {terminating ? (
                    <span className="shrink-0 text-[9.5px]" style={{ color: 'var(--rp-tone-yellow-fg)' }}>
                      terminating…
                    </span>
                  ) : starting ? (
                    <span className="shrink-0 text-[9.5px]" style={{ color: 'var(--rp-tone-yellow-fg)' }}>
                      starting…
                    </span>
                  ) : isNew ? (
                    <span
                      className="shrink-0 rounded-skin-chip px-1.5 py-0.5 text-[9px] uppercase tracking-[0.05em]"
                      style={{ color: 'var(--rp-tone-green-fg)', background: 'var(--rp-tone-green-bg)' }}
                    >
                      new
                    </span>
                  ) : null}
                  {p.restarts > 0 ? (
                    <span className="shrink-0 tnum" style={{ color: 'var(--rp-yellow)' }}>
                      ↻{p.restarts}
                    </span>
                  ) : null}
                  <span className="max-w-[90px] shrink-0 truncate text-faint" title={p.nodeName}>
                    {p.nodeName}
                  </span>
                  {!terminating ? (
                    <>
                      <button
                        type="button"
                        onClick={() => void fireReadonly('pod_events', 'Pod', p.name)}
                        className="shrink-0 rounded-skin-chip border border-line px-1.5 py-0.5 text-[9.5px] text-muted transition-colors hover:bg-hover hover:text-ink"
                        title="read-only: recent events of this pod"
                      >
                        events
                      </button>
                      <button
                        type="button"
                        onClick={() => setConfirm({ kind: 'delete_pod', pod: p.name })}
                        className="shrink-0 rounded-skin-chip border border-line px-1.5 py-0.5 text-[9.5px] text-muted transition-colors hover:bg-hover hover:text-ink"
                        title="delete pod (owner recreates it)"
                      >
                        recreate
                      </button>
                    </>
                  ) : null}
                </div>
              );
            })}
          </div>
        )}

        {/* ── ACTIVITY: Aktionen + Ergebnis vom Agenten (gemeinsame Karte) ── */}
        {tab === 'activity' ? (
          activity.length === 0 ? (
            <div className="mt-2 font-mono text-[11px] text-faint">no actions yet</div>
          ) : (
            <div className="mt-2 space-y-1.5">
              {activity.map((a) => (
                <ActionRunCard
                  key={a.id}
                  action={a}
                  onCancel={(id) => void cancelAction(orgId, clusterId, id).catch(() => {})}
                />
              ))}
            </div>
          )
        ) : null}
      </div>

      {/* Log-Detail als Portal ÜBER der Map (Symmetrie zur Logs-Seite) */}
      {openLine ? (
        <LogDrawer orgId={orgId} clusterId={clusterId} line={openLine} onClose={() => setOpenLine(null)} />
      ) : null}
    </aside>
  );
}
