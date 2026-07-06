'use client';

import { useCallback, useEffect, useMemo, useState } from 'react';
import Link from 'next/link';
import { useParams, useSearchParams } from 'next/navigation';
import { cn } from '@/lib/cn';
import { Spinner } from '@/components/ui';
import { useMe } from '@/components/app/me-context';
import {
  buildTree,
  fmtMs,
  statusTone,
  SpanDetail,
  TONE_BAR,
  TONE_CHIP,
  type SpanNode,
} from '@/components/traces/trace-drawer';
import { FlameGraph } from '@/components/traces/flame-graph';
import { TraceGraph } from '@/components/traces/trace-graph';
import { serviceColorMap } from '@/components/traces/service-color';
import { LogDrawer } from '@/components/logs/log-drawer';
import { getLogs, getTraceDetail } from '@/lib/api/controlplane';
import type { LogLine, TraceSpan } from '@/lib/api/types';

// Die Trace-VOLLSEITE — der Debugging-Modus, wie ein Developer arbeitet:
// links der Trace in DREI Ansichten derselben Daten (dash0-Muster) —
// Waterfall (Hierarchie), Flame Graph (absolute Zeit × Call-Tiefe) und
// Trace Graph (Service-Topologie dieses Traces) — rechts die live
// KORRELIERTEN LOGS. Die Korrelation ist ehrlich Zeit × Service: ohne Auswahl
// alle beteiligten Workloads im Trace-Fenster; wählt man links einen Span,
// springen rechts die Logs auf dessen Service + Zeitfenster (±500ms Puffer).
// Tasten 1/2/3 wechseln die Ansicht; ?view= macht sie deep-linkbar.

const VIEWS = ['waterfall', 'flame', 'graph'] as const;
type TraceView = (typeof VIEWS)[number];

export default function TracePage() {
  const params = useParams<{ id: string; traceId: string }>();
  const clusterId = params.id;
  const traceId = params.traceId;
  const { currentOrg } = useMe();
  const orgId = currentOrg?.id;
  const sp = useSearchParams();

  const [spans, setSpans] = useState<TraceSpan[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [selected, setSelected] = useState<string | null>(null);
  const [logs, setLogs] = useState<LogLine[] | null>(null);
  const [openLine, setOpenLine] = useState<LogLine | null>(null);
  const [view, setView] = useState<TraceView>(() => {
    const v = sp.get('view');
    return VIEWS.includes(v as TraceView) ? (v as TraceView) : 'waterfall';
  });

  // View wechseln + URL nachziehen (deep-linkbar, ohne Navigation).
  const switchView = useCallback((v: TraceView) => {
    setView(v);
    const url = new URL(window.location.href);
    if (v === 'waterfall') url.searchParams.delete('view');
    else url.searchParams.set('view', v);
    window.history.replaceState(null, '', url);
  }, []);

  // Tasten 1/2/3 — Ansichten wie Werkzeuge griffbereit.
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.metaKey || e.ctrlKey || e.altKey) return;
      const t = e.target as HTMLElement | null;
      if (t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.isContentEditable)) return;
      const idx = ['1', '2', '3'].indexOf(e.key);
      const v = idx >= 0 ? VIEWS[idx] : undefined;
      if (v) switchView(v);
    }
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [switchView]);

  useEffect(() => {
    if (!orgId) return;
    getTraceDetail(orgId, clusterId, traceId)
      .then((d) => setSpans(d.spans))
      .catch((err) => setError(err instanceof Error ? err.message : 'Failed to load trace'));
  }, [orgId, clusterId, traceId]);

  const tree = useMemo(() => (spans ? buildTree(spans) : []), [spans]);
  const root = tree[0];

  // Debug-Erwartung: Bei Fehler-Traces direkt auf den tiefsten Fehler-Span
  // springen (die Wurzel der Kaskade) — Logs rechts zeigen sofort die Ursache.
  const autoFocused = useMemo(() => ({ done: false }), [traceId]);
  useEffect(() => {
    if (autoFocused.done || tree.length === 0) return;
    autoFocused.done = true;
    const errSpans = tree.filter(
      (s) => statusTone(s.httpStatus ?? '', s.statusCode ?? '') === 'err',
    );
    if (errSpans.length > 0) {
      // tiefster Fehler = Ursprung der Kaskade (payments 502, nicht checkout
      // 500); bei gleicher Tiefe der später gestartete (kausal weiter unten).
      const deepest = errSpans.reduce((a, b) =>
        b.depth > a.depth || (b.depth === a.depth && b.startUnixNs > a.startUnixNs) ? b : a,
      );
      setSelected(deepest.spanId);
    }
  }, [tree, autoFocused]);
  const t0 = useMemo(() => Math.min(...(spans ?? []).map((s) => s.startUnixNs)), [spans]);
  const totalMs = useMemo(() => {
    if (!spans || spans.length === 0) return 1;
    const end = Math.max(...spans.map((s) => s.startUnixNs + s.durationMs * 1e6));
    return Math.max(0.05, (end - t0) / 1e6);
  }, [spans, t0]);

  const services = useMemo(
    () => Array.from(new Set((spans ?? []).map((s) => s.serviceName))).filter(Boolean),
    [spans],
  );
  // Service-Identität: eine Farbe pro Service, konsistent über alle drei Views
  // (Waterfall-Dot, Flame-Block, Graph-Node) — Reihenfolge = Trace-Reihenfolge.
  const svcColors = useMemo(
    () => serviceColorMap(Array.from(new Set(tree.map((s) => s.serviceName)))),
    [tree],
  );
  const colorOf = useCallback(
    (svc: string) => svcColors.get(svc) ?? 'var(--rp-ink-muted)',
    [svcColors],
  );
  const selectedSpan = selected ? tree.find((s) => s.spanId === selected) : undefined;

  // Korrelations-Scope: Span-Auswahl → dessen Service + Zeitfenster (±500ms);
  // sonst der ganze Trace (alle Services, Fenster ±1s).
  const logScope = useMemo(() => {
    if (!spans || spans.length === 0) return null;
    if (selectedSpan) {
      const start = selectedSpan.startUnixNs / 1e6 - 500;
      const end = selectedSpan.startUnixNs / 1e6 + selectedSpan.durationMs + 500;
      return {
        workloads: [selectedSpan.serviceName],
        since: new Date(start).toISOString(),
        until: new Date(end).toISOString(),
        label: `${selectedSpan.serviceName} · span ±0.5s`,
      };
    }
    return {
      workloads: services,
      since: new Date(t0 / 1e6 - 1000).toISOString(),
      until: new Date(t0 / 1e6 + totalMs + 1000).toISOString(),
      label: `${services.length} services · trace ±1s`,
    };
  }, [spans, selectedSpan, services, t0, totalMs]);

  const loadLogs = useCallback(async () => {
    if (!orgId || !logScope) return;
    try {
      const res = await getLogs(orgId, clusterId, {
        since: logScope.since,
        until: logScope.until,
        workloads: logScope.workloads,
        limit: 200,
      });
      setLogs(res.lines);
    } catch {
      setLogs([]);
    }
  }, [orgId, clusterId, logScope]);

  useEffect(() => {
    setLogs(null);
    void loadLogs();
  }, [loadLogs]);

  const errCount = (spans ?? []).filter(
    (s) => s.statusCode === 'Error' || (s.httpStatus && s.httpStatus >= '500'),
  ).length;

  return (
    <div className="flex h-[calc(100dvh-52px)] flex-col px-4 pt-4 sm:px-5">
      {/* Masthead */}
      <header className="shrink-0">
        <Link
          href={`/clusters/${clusterId}/traces`}
          className="rp-micro !text-[10px] transition-colors hover:!text-ink"
        >
          ← traces / {traceId.slice(0, 16)}…
        </Link>
        <div className="rp-keyline mt-2 flex flex-wrap items-end justify-between gap-x-4 gap-y-2 pb-3">
          <h1 className="min-w-0 truncate font-display text-[22px] font-bold tracking-tightest text-ink sm:text-[24px]">
            {root?.spanName ?? 'Trace'}
          </h1>
          <div className="flex flex-wrap items-center gap-3 font-mono text-[11px] text-muted tnum">
            <span className="text-mid">{root?.serviceName}</span>
            <span>{fmtMs(totalMs)} total</span>
            <span>{spans?.length ?? 0} spans</span>
            <span>{services.length} services</span>
            {errCount > 0 ? (
              <span style={{ color: 'var(--rp-red)' }}>▲ {errCount} errors</span>
            ) : null}
          </div>
        </div>
      </header>

      {error ? (
        <div className="flex flex-1 items-center justify-center font-mono text-[12px] text-red">{error}</div>
      ) : spans === null ? (
        <div className="flex flex-1 items-center justify-center gap-2 text-muted">
          <Spinner /> <span className="font-mono text-[12px]">Loading trace…</span>
        </div>
      ) : (
        <div className="mt-3 grid min-h-0 flex-1 grid-cols-1 gap-3 pb-3 lg:grid-cols-[minmax(0,11fr)_minmax(0,9fr)]">
          {/* ── Links: Waterfall ── */}
          <section
            className="flex min-h-0 flex-col overflow-hidden rounded-skin border border-line bg-raised"
            style={{ boxShadow: 'var(--rp-rim)' }}
          >
            <div className="flex shrink-0 items-center justify-between gap-3 border-b border-line px-3 py-1.5">
              {/* View-Switcher — drei Ansichten derselben Daten (Tasten 1/2/3) */}
              <div className="flex items-center rounded-skin-sm border border-line p-[2px]">
                {VIEWS.map((v, i) => (
                  <button
                    key={v}
                    type="button"
                    onClick={() => switchView(v)}
                    className={cn(
                      'rp-focus rounded-[3px] px-2 py-[3px] font-mono text-[10px] uppercase tracking-[0.06em] transition-colors',
                      view === v ? 'bg-hover text-ink' : 'text-muted hover:text-ink',
                    )}
                    style={view === v ? { boxShadow: 'inset 0 0 0 1px var(--rp-line-strong)' } : undefined}
                    title={`${v} (${i + 1})`}
                  >
                    {v}
                  </button>
                ))}
              </div>
              {/* Service-Legende — dieselbe Identität in allen Views */}
              <div className="flex min-w-0 items-center gap-2.5 overflow-hidden">
                {services.map((svc) => (
                  <span key={svc} className="flex shrink-0 items-center gap-1 font-mono text-[9.5px] text-muted">
                    <span className="h-[6px] w-[6px] rounded-full" style={{ background: colorOf(svc) }} />
                    {svc}
                  </span>
                ))}
              </div>
              <span className="shrink-0 font-mono text-[10px] text-muted tnum">0 … {fmtMs(totalMs)}</span>
            </div>
            <div className={cn('min-h-0 flex-1 overflow-y-auto', view === 'waterfall' && 'p-2.5')}>
              {view === 'flame' ? (
                <FlameGraph
                  tree={tree}
                  t0={t0}
                  totalMs={totalMs}
                  selected={selected}
                  onSelect={(id) => setSelected((p) => (p === id ? null : id))}
                  colorOf={colorOf}
                />
              ) : view === 'graph' ? (
                <TraceGraph
                  tree={tree}
                  selected={selected}
                  onSelect={(id) => setSelected((p) => (p === id ? null : id))}
                />
              ) : (
              <div className="space-y-[3px]">
                {tree.map((s: SpanNode) => {
                  const tone = statusTone(s.httpStatus ?? '', s.statusCode ?? '');
                  const isErr = tone === 'err';
                  const offsetMs = (s.startUnixNs - t0) / 1e6;
                  const left = Math.min(99, (offsetMs / totalMs) * 100);
                  const width = Math.max(0.6, Math.min(100 - left, (s.durationMs / totalMs) * 100));
                  const isSel = selected === s.spanId;
                  return (
                    <button
                      key={s.spanId}
                      type="button"
                      onClick={() => setSelected((p) => (p === s.spanId ? null : s.spanId))}
                      className={cn(
                        'group relative block w-full rounded-skin-sm px-1.5 py-1 text-left transition-colors',
                        isSel ? 'bg-hover' : 'hover:bg-hover',
                      )}
                      style={isSel ? { boxShadow: 'inset 0 0 0 1px var(--rp-line-strong)' } : undefined}
                    >
                      <div
                        className="flex items-baseline gap-2 font-mono text-[11px] leading-tight"
                        style={{ paddingLeft: s.depth * 14 }}
                      >
                        <span
                          className="h-[7px] w-[7px] shrink-0 self-center rounded-full"
                          style={{ background: colorOf(s.serviceName) }}
                        />
                        <span className="shrink-0 text-mid">{s.serviceName}</span>
                        <span className="min-w-0 flex-1 truncate text-ink">{s.spanName}</span>
                        {s.httpStatus ? (
                          <span
                            className="shrink-0 rounded-skin-chip px-1 py-px text-[9px]"
                            style={{ color: TONE_CHIP[tone].fg, background: TONE_CHIP[tone].bg }}
                          >
                            {s.httpStatus}
                          </span>
                        ) : null}
                        <span
                          className="w-[76px] shrink-0 text-right tnum"
                          style={{ color: isErr ? 'var(--rp-red)' : 'var(--rp-ink)' }}
                        >
                          {fmtMs(s.durationMs)}
                        </span>
                      </div>
                      <div className="mt-[3px] h-[5px] w-full rounded-full bg-inset">
                        <div
                          className="h-full rounded-full"
                          style={{
                            marginLeft: `${left}%`,
                            width: `${width}%`,
                            background: TONE_BAR[tone],
                            opacity: tone === 'err' ? 0.95 : s.depth === 0 ? 0.85 : 0.55,
                          }}
                        />
                      </div>
                    </button>
                  );
                })}
              </div>
              )}
            </div>
            {/* SpanDetail des gewählten Spans (Duration-Vergleich + Attribute) */}
            {selectedSpan && orgId ? (
              <SpanDetail span={selectedSpan} orgId={orgId} clusterId={clusterId} />
            ) : null}
          </section>

          {/* ── Rechts: korrelierte Logs ── */}
          <section
            className="flex min-h-0 flex-col overflow-hidden rounded-skin border border-line bg-raised"
            style={{ boxShadow: 'var(--rp-rim)' }}
          >
            <div className="flex shrink-0 items-center justify-between gap-2 border-b border-line px-3 py-2">
              <span className="rp-micro min-w-0 truncate !text-[10px]">
                correlated logs · {logScope?.label ?? '—'}
              </span>
              {selectedSpan ? (
                <button
                  type="button"
                  onClick={() => setSelected(null)}
                  className="shrink-0 rounded-skin-chip border border-line px-1.5 py-0.5 font-mono text-[9.5px] text-muted transition-colors hover:text-ink"
                >
                  whole trace ✕
                </button>
              ) : null}
            </div>
            <div className="min-h-0 flex-1 overflow-y-auto">
              {logs === null ? (
                <div className="flex h-32 items-center justify-center gap-2 text-muted">
                  <Spinner /> <span className="font-mono text-[11px]">correlating…</span>
                </div>
              ) : logs.length === 0 ? (
                <div className="flex h-32 flex-col items-center justify-center gap-1">
                  <span className="font-mono text-[11px] text-muted">no logs in this window</span>
                  <span className="font-mono text-[10px] text-faint">{logScope?.label}</span>
                </div>
              ) : (
                <div>
                  {[...logs].reverse().map((l, i) => {
                    const isErr = l.severityNumber >= 17;
                    const isWarn = l.severityNumber >= 13 && !isErr;
                    return (
                      <button
                        key={`${l.ts}-${i}`}
                        type="button"
                        onClick={() => setOpenLine(l)}
                        className="flex w-full items-start gap-2 border-b border-line/60 px-2.5 py-[3px] text-left font-mono text-[10.5px] leading-relaxed transition-colors last:border-0 hover:bg-hover"
                      >
                        <span className="w-[3px] shrink-0 self-stretch">
                          <span
                            className="block h-full w-[2px]"
                            style={{
                              background: isErr
                                ? 'var(--rp-red)'
                                : isWarn
                                  ? 'var(--rp-yellow)'
                                  : 'transparent',
                            }}
                          />
                        </span>
                        <span className="shrink-0 pt-px text-muted tnum">{l.ts.slice(11, 23)}</span>
                        <span className="shrink-0 pt-px text-mid">{l.workloadName}</span>
                        <span
                          className="min-w-0 flex-1 break-all"
                          style={{
                            color: isErr
                              ? 'var(--rp-tone-red-fg)'
                              : isWarn
                                ? 'var(--rp-tone-yellow-fg)'
                                : 'var(--rp-ink)',
                          }}
                        >
                          {l.body}
                        </span>
                      </button>
                    );
                  })}
                </div>
              )}
            </div>
          </section>
        </div>
      )}

      {openLine && orgId ? (
        <LogDrawer orgId={orgId} clusterId={clusterId} line={openLine} onClose={() => setOpenLine(null)} />
      ) : null}
    </div>
  );
}
