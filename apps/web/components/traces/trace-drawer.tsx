'use client';

import { useEffect, useMemo, useState } from 'react';
import { createPortal } from 'react-dom';
import { cn } from '@/lib/cn';
import { Spinner } from '@/components/ui';
import { getSpanStats, getTraceDetail } from '@/lib/api/controlplane';
import type { SpanStats, TraceSpan } from '@/lib/api/types';

// trace-drawer.tsx — das Trace-Detail als breites Side-Panel von rechts (dash0/
// metoro-Muster im RETICLE-Vokabular): Masthead mit Trace-Meta, darunter der
// WATERFALL — jeder Span als Balken auf der gemeinsamen Zeitachse, Baum-Einrückung
// über ParentSpanId, Fehler-Spans tragen Crimson. Klick auf einen Span pinnt
// seine Details unten. Esc/✕/Scrim schließen.

const DRAWER_W = 'min(780px, 92vw)';

// treeParentId = effektiver Parent NACH der zeitlichen Verschachtelung —
// Konsumenten (Trace-Graph) hängen Kausalität hieran, nie an parentSpanId.
export type SpanNode = TraceSpan & { depth: number; treeParentId: string };

// Baum aus ParentSpanId aufbauen und in Startzeit-Reihenfolge flatten.
export function buildTree(spans: TraceSpan[]): SpanNode[] {
  const ids = new Set(spans.map((s) => s.spanId));
  // Spans mit unbekanntem Parent (abgeschnittener Kontext, z.B. eBPF ohne
  // Header-Injection) gelten zunächst als Wurzeln …
  const parentOf = new Map<string, string>();
  for (const s of spans) {
    parentOf.set(s.spanId, ids.has(s.parentSpanId) ? s.parentSpanId : '');
  }
  // … werden aber zeitlich verschachtelt: liegt so eine „Wurzel" vollständig
  // in einem längeren Span, ist sie kausal dessen Kind — so bleibt die Kette
  // frontdoor→checkout→orders→payments lesbar, auch wenn der traceparent nur
  // die Trace-ID trägt. ε fängt Rundung/Clock-Jitter ab; der engste Container
  // (kleinste Duration) gewinnt. Streng absteigende Durations ⇒ zyklenfrei.
  const EPS_NS = 1.5e6;
  const endNs = (s: TraceSpan) => s.startUnixNs + s.durationMs * 1e6;
  for (const s of spans) {
    if (parentOf.get(s.spanId) !== '') continue;
    let best: TraceSpan | null = null;
    for (const c of spans) {
      if (c === s || c.durationMs <= s.durationMs) continue;
      if (c.startUnixNs <= s.startUnixNs + EPS_NS && endNs(c) >= endNs(s) - EPS_NS) {
        if (!best || c.durationMs < best.durationMs) best = c;
      }
    }
    if (best) parentOf.set(s.spanId, best.spanId);
  }
  const byParent = new Map<string, TraceSpan[]>();
  for (const s of spans) {
    const key = parentOf.get(s.spanId) ?? '';
    const list = byParent.get(key) ?? [];
    list.push(s);
    byParent.set(key, list);
  }
  const out: SpanNode[] = [];
  const walk = (parent: string, depth: number) => {
    const children = (byParent.get(parent) ?? []).sort((a, b) => a.startUnixNs - b.startUnixNs);
    for (const c of children) {
      out.push({ ...c, depth, treeParentId: parent });
      walk(c.spanId, depth + 1);
    }
  };
  walk('', 0);
  // Fallback: falls Zyklen/Waisen etwas verschluckt haben, hinten anfügen.
  if (out.length < spans.length) {
    const seen = new Set(out.map((s) => s.spanId));
    for (const s of spans) if (!seen.has(s.spanId)) out.push({ ...s, depth: 0, treeParentId: '' });
  }
  return out;
}

export function fmtMs(ms: number): string {
  if (ms >= 60_000) return `${Math.floor(ms / 60_000)}m ${Math.round((ms % 60_000) / 1000)}s`;
  if (ms >= 1000) return (ms / 1000).toFixed(2) + 's';
  if (ms >= 10) return ms.toFixed(0) + 'ms';
  return ms.toFixed(2) + 'ms';
}

// Status-Color-Coding für Spans: 2xx/Ok = dezent grün, 4xx = gold (Client-
// Fehler), 5xx/Error = rot, interne Spans ohne HTTP-Status = neutral.
// Signal-Orange bleibt strikt der Selektion vorbehalten.
export type StatusTone = 'ok' | 'warn' | 'err' | 'neutral';

export function statusTone(httpStatus: string, statusCode: string): StatusTone {
  if (statusCode === 'Error' || httpStatus.startsWith('5')) return 'err';
  if (httpStatus.startsWith('4')) return 'warn';
  if (httpStatus.startsWith('2') || httpStatus.startsWith('3')) return 'ok';
  return 'neutral';
}

export const TONE_BAR: Record<StatusTone, string> = {
  ok: 'var(--rp-green)',
  warn: 'var(--rp-yellow)',
  err: 'var(--rp-red)',
  neutral: 'var(--rp-map-particle)',
};
export const TONE_CHIP: Record<StatusTone, { bg: string; fg: string }> = {
  ok: { bg: 'var(--rp-tone-green-bg)', fg: 'var(--rp-tone-green-fg)' },
  warn: { bg: 'var(--rp-tone-yellow-bg)', fg: 'var(--rp-tone-yellow-fg)' },
  err: { bg: 'var(--rp-tone-red-bg)', fg: 'var(--rp-tone-red-fg)' },
  neutral: { bg: 'var(--rp-tone-neutral-bg)', fg: 'var(--rp-ink-muted)' },
};

export function TraceDrawer({
  orgId,
  clusterId,
  traceId,
  onClose,
}: {
  orgId: string;
  clusterId: string;
  traceId: string;
  onClose: () => void;
}) {
  const [spans, setSpans] = useState<TraceSpan[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [pinned, setPinned] = useState<string | null>(null);

  useEffect(() => {
    setSpans(null);
    setError(null);
    setPinned(null);
    getTraceDetail(orgId, clusterId, traceId)
      .then((d) => setSpans(d.spans))
      .catch((err) => setError(err instanceof Error ? err.message : 'Failed to load trace'));
  }, [orgId, clusterId, traceId]);

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') onClose();
    }
    document.addEventListener('keydown', onKey);
    const prev = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    return () => {
      document.removeEventListener('keydown', onKey);
      document.body.style.overflow = prev;
    };
  }, [onClose]);

  const tree = useMemo(() => (spans ? buildTree(spans) : []), [spans]);

  // Beim Öffnen automatisch pinnen: bei Fehler-Traces den tiefsten Fehler-Span
  // (die Kaskaden-Ursache — konsistent mit der Vollseite), sonst den Root. Das
  // Span-Detail ist so sofort sichtbar statt hinter einem Klick versteckt.
  const autoPinned = useMemo(() => ({ done: false }), [traceId]);
  useEffect(() => {
    if (!autoPinned.done && tree.length > 0) {
      autoPinned.done = true;
      const errSpans = tree.filter(
        (s) => statusTone(s.httpStatus ?? '', s.statusCode ?? '') === 'err',
      );
      const target =
        errSpans.length > 0
          ? errSpans.reduce((a, b) =>
              b.depth > a.depth || (b.depth === a.depth && b.startUnixNs > a.startUnixNs)
                ? b
                : a,
            )
          : tree[0];
      setPinned(target?.spanId ?? null);
    }
  }, [tree, autoPinned]);

  // Gemeinsame Zeitachse des Waterfalls.
  const t0 = useMemo(() => Math.min(...(spans ?? []).map((s) => s.startUnixNs)), [spans]);
  const total = useMemo(() => {
    if (!spans || spans.length === 0) return 1;
    const end = Math.max(...spans.map((s) => s.startUnixNs + s.durationMs * 1e6));
    return Math.max(0.05, (end - t0) / 1e6); // ms
  }, [spans, t0]);

  const root = tree[0];
  const errCount = (spans ?? []).filter(
    (s) => s.statusCode === 'Error' || (s.httpStatus && s.httpStatus >= '500'),
  ).length;
  const pinnedSpan = pinned ? tree.find((s) => s.spanId === pinned) : undefined;

  return createPortal(
    <div className="fixed inset-0 z-50" role="dialog" aria-modal="true" aria-label="Trace detail">
      {/* Scrim */}
      <button
        type="button"
        aria-label="Close"
        onClick={onClose}
        className="absolute inset-0 cursor-default"
        style={{ backgroundColor: 'var(--rp-scrim)' }}
      />

      {/* Panel von rechts */}
      <aside
        className="absolute inset-y-0 right-0 flex flex-col border-l border-line bg-raised"
        style={{
          width: DRAWER_W,
          boxShadow: 'var(--rp-rim), var(--rp-shadow-pop)',
          animation: 'rp-drawer-in var(--rp-dur-large) var(--rp-ease-enter)',
        }}
      >
        {/* Masthead */}
        <header className="shrink-0 border-b border-line px-5 pb-3 pt-4">
          <div className="flex items-start justify-between gap-3">
            <div className="min-w-0">
              <div className="rp-micro !text-[10px]">
                trace / {traceId.slice(0, 16)}…
              </div>
              <h2 className="mt-1.5 truncate border-b border-line-strong pb-2.5 text-[19px] font-bold leading-tight tracking-tightest text-ink">
                {root ? root.spanName : 'Trace'}
              </h2>
            </div>
            <div className="flex shrink-0 items-center gap-1.5">
              <a
                href={`/clusters/${clusterId}/traces/${traceId}`}
                aria-label="Als Seite öffnen (mit korrelierten Logs)"
                title="Open full page — waterfall + correlated logs"
                className="rp-focus mt-0.5 inline-flex h-7 w-7 items-center justify-center rounded-skin-sm border border-line text-muted transition-colors hover:text-ink"
              >
                <svg width="13" height="13" viewBox="0 0 24 24" fill="none" aria-hidden>
                  <path d="M14 4h6v6M20 4l-8 8M10 20H4v-6M4 20l8-8" stroke="currentColor" strokeWidth="1.8" strokeLinecap="square" />
                </svg>
              </a>
              <button
                type="button"
                onClick={onClose}
                aria-label="Close"
                className="rp-focus mt-0.5 inline-flex h-7 w-7 items-center justify-center rounded-skin-sm border border-line text-muted transition-colors hover:text-ink"
              >
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" aria-hidden>
                  <path d="M6 6l12 12M18 6L6 18" stroke="currentColor" strokeWidth="1.8" strokeLinecap="square" />
                </svg>
              </button>
            </div>
          </div>
          {root ? (
            <div className="mt-2 flex flex-wrap items-center gap-3 font-mono text-[11px] text-muted tnum">
              <span className="text-mid">{root.serviceName}</span>
              {root.namespace ? <span>{root.namespace}</span> : null}
              <span>{fmtMs(total)} total</span>
              <span>{spans?.length ?? 0} spans</span>
              {errCount > 0 ? (
                <span style={{ color: 'var(--rp-red)' }}>▲ {errCount} errors</span>
              ) : null}
            </div>
          ) : null}
        </header>

        {/* Zeitachse */}
        <div className="flex shrink-0 items-center justify-between px-5 pb-1 pt-2.5 font-mono text-[10px] text-muted tnum">
          <span>0</span>
          <span>{fmtMs(total / 2)}</span>
          <span>{fmtMs(total)}</span>
        </div>

        {/* Waterfall */}
        <div className="min-h-0 flex-1 overflow-y-auto px-5 pb-4">
          {error ? (
            <div className="flex h-40 items-center justify-center font-mono text-[12px] text-red">{error}</div>
          ) : spans === null ? (
            <div className="flex h-40 items-center justify-center gap-2 text-muted">
              <Spinner /> <span className="font-mono text-[12px]">Loading trace…</span>
            </div>
          ) : (
            <div className="space-y-[3px]">
              {tree.map((s) => {
                const tone = statusTone(s.httpStatus ?? '', s.statusCode ?? '');
                const isErr = tone === 'err';
                const offsetMs = (s.startUnixNs - t0) / 1e6;
                const left = Math.min(99, (offsetMs / total) * 100);
                const width = Math.max(0.6, Math.min(100 - left, (s.durationMs / total) * 100));
                const isPinned = pinned === s.spanId;
                return (
                  <button
                    key={s.spanId}
                    type="button"
                    onClick={() => setPinned((p) => (p === s.spanId ? null : s.spanId))}
                    className={cn(
                      'group relative block w-full rounded-skin-sm px-1.5 py-1 text-left transition-colors',
                      isPinned ? 'bg-hover' : 'hover:bg-hover',
                    )}
                    style={
                      // Selektion NEUTRAL (Fläche + Hairline-Ring) — in status-
                      // gefärbten Listen trägt Farbe ausschließlich Status; ein
                      // oranger Tick neben grünem 200er-Balken widerspräche sich.
                      isPinned
                        ? { boxShadow: 'inset 0 0 0 1px var(--rp-line-strong)' }
                        : undefined
                    }
                  >
                    {/* Label-Zeile */}
                    <div
                      className="flex items-baseline gap-2 font-mono text-[11px] leading-tight"
                      style={{ paddingLeft: s.depth * 14 }}
                    >
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
                    {/* Balken auf gemeinsamer Zeitachse */}
                    <div className="mt-[3px] h-[5px] w-full rounded-full bg-inset">
                      <div
                        className="h-full rounded-full"
                        style={{
                          marginLeft: `${left}%`,
                          width: `${width}%`,
                          background: TONE_BAR[tone],
                          // Hierarchie über Opacity (Root satter), Farbe = Status
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

        {/* gepinnter Span — dash0-Style-Detail: Meta + Duration-Vergleich + Attribute */}
        {pinnedSpan ? (
          <SpanDetail span={pinnedSpan} orgId={orgId} clusterId={clusterId} />
        ) : null}
      </aside>
    </div>,
    document.body,
  );
}

function Meta({ k, v }: { k: string; v: string }) {
  return (
    <div className="min-w-0">
      <span className="text-faint">{k} </span>
      <span className="truncate text-ink">{v}</span>
    </div>
  );
}

// SpanDetail — der untere Detail-Bereich des gepinnten Spans (dash0-Muster):
// Meta-Zeile, die Duration-Verteilungs-Bar („wie normal ist dieser Span?" —
// Histogramm derselben Operation im letzten 1h-Fenster mit Quantil-Markern und
// dem Signal-orangenen „this span"-Marker) und die vollen Span-Attribute.
export function SpanDetail({
  span,
  orgId,
  clusterId,
}: {
  span: TraceSpan & { depth: number };
  orgId: string;
  clusterId: string;
}) {
  const [stats, setStats] = useState<SpanStats | null>(null);

  useEffect(() => {
    setStats(null);
    getSpanStats(orgId, clusterId, span.serviceName, span.spanName)
      .then(setStats)
      .catch(() => {});
  }, [orgId, clusterId, span.serviceName, span.spanName]);

  const attrs = Object.entries(span.attributes ?? {}).sort(([a], [b]) => a.localeCompare(b));
  const res = Object.entries(span.resource ?? {})
    .filter(([k]) => k.startsWith('k8s.') || k === 'service.name')
    .sort(([a], [b]) => a.localeCompare(b));
  const isErr = span.statusCode === 'Error' || (span.httpStatus && span.httpStatus >= '500');

  return (
    <footer className="max-h-[46%] shrink-0 overflow-y-auto border-t border-line px-5 py-3">
      <div className="flex items-baseline justify-between gap-3">
        <div className="rp-micro !text-[10px]">span / {span.spanId.slice(0, 12)}…</div>
        <span className="font-mono text-[10px] text-faint">{span.kind}</span>
      </div>
      <div className="mt-1.5 grid grid-cols-2 gap-x-6 gap-y-1 font-mono text-[11px] tnum md:grid-cols-4">
        <Meta k="service" v={span.serviceName} />
        <Meta k="namespace" v={span.namespace || '—'} />
        <Meta k="duration" v={fmtMs(span.durationMs)} />
        <Meta k="status" v={span.httpStatus || span.statusCode || '—'} />
      </div>

      {/* Duration-Vergleich */}
      <div className="mt-3">
        <div className="flex items-baseline justify-between">
          <span className="rp-micro !text-[10px]">span duration · vs same operation (1h)</span>
          {stats ? (
            <span className="font-mono text-[10px] text-faint tnum">
              {stats.count.toLocaleString()} samples
            </span>
          ) : null}
        </div>
        {stats === null ? (
          <div className="mt-2 h-[46px] animate-pulse rounded-skin-sm bg-inset" />
        ) : stats.count < 3 || stats.histogram.length === 0 ? (
          <div className="mt-2 font-mono text-[10.5px] text-faint">
            not enough samples in range
          </div>
        ) : (
          <DurationBar stats={stats} valueMs={span.durationMs} isErr={!!isErr} />
        )}
      </div>

      {/* Attribute */}
      {attrs.length > 0 || res.length > 0 ? (
        <div className="mt-3">
          <div className="rp-micro !text-[10px]">attributes · {attrs.length + res.length}</div>
          <div className="mt-1.5 grid grid-cols-1 gap-x-6 gap-y-[3px] md:grid-cols-2">
            {[...attrs, ...res].map(([k, v]) => {
              const highlight = isErr && k === 'http.response.status_code';
              return (
                <div key={k} className="flex min-w-0 items-baseline gap-2 font-mono text-[11px]">
                  {highlight ? (
                    <span className="h-2.5 w-[2px] shrink-0 self-center" style={{ background: 'var(--rp-red)' }} />
                  ) : null}
                  <span className="shrink-0 text-muted">{k}</span>
                  <span
                    className="min-w-0 flex-1 truncate text-right"
                    style={{ color: highlight ? 'var(--rp-tone-red-fg)' : 'var(--rp-ink)' }}
                    title={v}
                  >
                    {v || '—'}
                  </span>
                </div>
              );
            })}
          </div>
        </div>
      ) : null}
    </footer>
  );
}

// DurationBar — die „geile Bar": Verteilungs-Histogramm derselben Operation,
// Quantil-Ticks (p50/p95/p99) und der Signal-orangene Marker dieses Spans.
function DurationBar({
  stats,
  valueMs,
  isErr,
}: {
  stats: SpanStats;
  valueMs: number;
  isErr: boolean;
}) {
  const lo = stats.histogram[0]?.[0] ?? 0;
  const hi = stats.histogram[stats.histogram.length - 1]?.[1] ?? Math.max(valueMs, 1);
  const range = Math.max(hi - lo, 0.001);
  const maxH = Math.max(...stats.histogram.map((b) => b[2] ?? 0), 1);
  const posPct = (ms: number) => Math.max(0, Math.min(100, ((ms - lo) / range) * 100));

  return (
    <div className="mt-2">
      <div className="relative flex h-[38px] items-end gap-px">
        {stats.histogram.map((b, i) => {
          const [blo = 0, bhi = 0, h = 0] = b;
          const w = Math.max(0.5, ((bhi - blo) / range) * 100);
          return (
            <div
              key={i}
              className="rounded-[1px]"
              style={{
                width: `${w}%`,
                height: `${Math.max(8, (h / maxH) * 100)}%`,
                background: 'var(--rp-line-strong)',
              }}
            />
          );
        })}
        {/* Quantil-Ticks */}
        {([['p50', stats.p50], ['p95', stats.p95], ['p99', stats.p99]] as const).map(([label, q]) => (
          <div key={label} className="absolute inset-y-0" style={{ left: `${posPct(q)}%` }}>
            <div className="h-full w-px" style={{ background: 'var(--rp-ink-faint)' }} />
          </div>
        ))}
        {/* dieser Span */}
        <div className="absolute -inset-y-1" style={{ left: `${posPct(valueMs)}%` }}>
          <div
            className="h-full w-[2px] rounded-full"
            style={{ background: isErr ? 'var(--rp-red)' : 'var(--rp-ink)' }}
          />
        </div>
      </div>
      {/* Legende */}
      <div className="mt-1 flex items-center justify-between font-mono text-[10px] text-muted tnum">
        <span>{fmtMs(lo)}</span>
        <span>
          p50 {fmtMs(stats.p50)} · p95 {fmtMs(stats.p95)} · p99 {fmtMs(stats.p99)} ·{' '}
          <span className="font-bold" style={{ color: isErr ? 'var(--rp-red)' : 'var(--rp-ink)' }}>
            this {fmtMs(valueMs)}
          </span>
        </span>
        <span>{fmtMs(hi)}</span>
      </div>
    </div>
  );
}
