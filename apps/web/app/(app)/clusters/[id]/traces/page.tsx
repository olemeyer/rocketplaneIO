'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import { useParams, useSearchParams } from 'next/navigation';
import { cn } from '@/lib/cn';
import { Spinner } from '@/components/ui';
import { BrushHistogram, BrushPill, type BrushWindow } from '@/components/ui/brush-histogram';
import { useMe } from '@/components/app/me-context';
import { useScope } from '@/components/app/scope-context';
import { PageHeader } from '@/components/app/page-header';
import { getTraces } from '@/lib/api/controlplane';
import { TraceDrawer } from '@/components/traces/trace-drawer';
import type { LogBucket, TraceRow } from '@/lib/api/types';

// Traces-Explorer (RETICLE): oben die RED-Metriken je Service (eBPF, zero-code),
// darunter die jüngsten Spans. Scope kommt aus dem globalen Namespace-Selektor;
// Live-Poll (5s), pausiert bei Hover. Kein SDK, kein Restart — Beyla liest die
// Requests aus dem Kernel.

const RANGES = ['15m', '1h', '6h', '24h'] as const;

export default function TracesPage() {
  const params = useParams<{ id: string }>();
  const clusterId = params.id;
  const { currentOrg } = useMe();
  const { namespace } = useScope();
  const orgId = currentOrg?.id;

  // Deep-Links (z.B. aus dem Log-Drawer): ?since&until → Brush, ?onlyError, ?service
  const sp = useSearchParams();
  const [range, setRange] = useState<string>('15m');
  const [onlyError, setOnlyError] = useState(sp.get('onlyError') === 'true');
  const [traces, setTraces] = useState<TraceRow[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [paused, setPaused] = useState(false);
  const [openTrace, setOpenTrace] = useState<string | null>(null);
  const [histogram, setHistogram] = useState<LogBucket[]>([]);
  const [brush, setBrush] = useState<BrushWindow | null>(() => {
    const since = sp.get('since');
    const until = sp.get('until');
    return since && until ? { since, until } : null;
  });
  const [service, setService] = useState<string | null>(sp.get('service'));
  const firstLoad = useRef(true);

  const load = useCallback(async () => {
    if (!orgId) return;
    try {
      const res = await getTraces(orgId, clusterId, {
        since: brush?.since ?? range,
        until: brush?.until,
        namespace: namespace ?? undefined,
        service: service ?? undefined,
        onlyError,
      });
      setTraces(res.traces);
      setHistogram(res.histogram);
      setError(null);
    } catch (err) {
      if (firstLoad.current) setError(err instanceof Error ? err.message : 'Failed to load traces');
    } finally {
      firstLoad.current = false;
    }
  }, [orgId, clusterId, range, brush, namespace, service, onlyError]);

  useEffect(() => {
    firstLoad.current = true;
    setTraces(null);
    void load();
  }, [load]);

  useEffect(() => {
    if (paused || brush) return; // Brush = eingefrorenes Fenster
    const id = window.setInterval(load, 5000);
    return () => window.clearInterval(id);
  }, [load, paused, brush]);

  return (
    <div className="flex h-[calc(100dvh-52px)] flex-col px-4 pt-4 sm:px-5">
      <PageHeader kicker={`${namespace ?? 'all namespaces'} / traces`} title="Traces">
        <div className="flex items-center gap-3 font-mono text-[11px] text-muted tnum">
          <span className="hidden sm:inline">eBPF · zero-instrumentation</span>
          <span className="flex items-center gap-1.5">
            <span
              className={cn('inline-block h-1.5 w-1.5 rounded-full', !paused && 'rp-breath')}
              style={{ background: paused ? 'var(--rp-ink-faint)' : 'var(--rp-green)', color: 'var(--rp-green)' }}
            />
            {paused ? 'paused' : 'live'}
          </span>
        </div>
      </PageHeader>

      {/* Toolbar */}
      <div className="mt-3 flex shrink-0 flex-wrap items-center gap-2">
        {brush ? <BrushPill brush={brush} onClear={() => setBrush(null)} /> : null}
        {service ? (
          <button
            type="button"
            onClick={() => setService(null)}
            className="h-9 rounded-skin-sm border border-line bg-inset px-3 font-mono text-[11px] text-ink transition-colors hover:bg-hover"
            title="Service-Filter entfernen"
          >
            {service} ✕
          </button>
        ) : null}
        <div className={brush ? 'pointer-events-none opacity-40' : undefined}>
        <div className="flex h-9 items-center gap-[3px] rounded-skin-sm border border-line bg-inset p-[3px]">
          {RANGES.map((r) => (
            <button
              key={r}
              type="button"
              onClick={() => setRange(r)}
              className={cn(
                'h-full rounded-[4px] px-2.5 font-mono text-[11px] transition-colors',
                r === range ? 'bg-raised text-ink' : 'text-muted hover:text-ink',
              )}
              style={r === range ? { boxShadow: 'var(--rp-rim), 0 1px 2px rgba(0,0,0,0.2)' } : undefined}
            >
              {r}
            </button>
          ))}
        </div>
        </div>
        <button
          type="button"
          onClick={() => setOnlyError((v) => !v)}
          className={cn(
            'h-9 rounded-skin-sm border px-3 font-mono text-[11px] transition-colors',
            onlyError
              ? 'border-transparent'
              : 'border-line text-muted hover:text-ink',
          )}
          style={
            onlyError
              ? { background: 'var(--rp-tone-red-bg)', color: 'var(--rp-tone-red-fg)' }
              : undefined
          }
        >
          ▲ errors only
        </button>
      </div>

      <BrushHistogram
        buckets={histogram}
        brush={brush}
        onBrushChange={setBrush}
        rangeLabel={`−${range}`}
        unit="traces"
      />

      {/* Trace-Liste */}
      <div
        className="mt-3 min-h-0 flex-1 overflow-y-auto rounded-t-skin border border-b-0 border-line bg-raised"
        style={{ boxShadow: 'var(--rp-rim)' }}
        onMouseEnter={() => setPaused(true)}
        onMouseLeave={() => setPaused(false)}
      >
        {error ? (
          <div className="flex h-full items-center justify-center font-mono text-[12px] text-red">{error}</div>
        ) : traces === null ? (
          <div className="flex h-full items-center justify-center gap-2 text-muted">
            <Spinner /> <span className="font-mono text-[12px]">Loading traces…</span>
          </div>
        ) : traces.length === 0 ? (
          <div className="flex h-full flex-col items-center justify-center gap-2 text-center">
            <div className="rp-micro !text-[10px] text-faint">no output</div>
            <p className="font-mono text-[12.5px] text-muted">No spans match this window.</p>
          </div>
        ) : (
          <table className="w-full border-collapse font-mono text-[11px] leading-[1.4]">
            <tbody>
              {traces.map((t) => {
                const isErr = t.errorCount > 0;
                const maxDur = traces.reduce((m, x) => Math.max(m, x.durationMs), 1);
                return (
                  <tr
                    key={t.traceId}
                    onClick={() => setOpenTrace(t.traceId)}
                    className="cursor-pointer border-b border-line/60 transition-colors hover:bg-hover"
                  >
                    <td className="w-[3px] p-0">
                      <div className="h-full min-h-[22px] w-[2px]" style={{ background: isErr ? 'var(--rp-red)' : 'transparent' }} />
                    </td>
                    <td className="whitespace-nowrap py-[3px] pl-2 pr-3 text-muted tnum">{t.ts.slice(11, 23)}</td>
                    <td className="whitespace-nowrap py-[3px] pr-3 text-mid">
                      {t.serviceName}
                      {t.namespace ? <span className="text-faint"> · {t.namespace}</span> : null}
                    </td>
                    <td className="w-full max-w-0 truncate py-[3px] pr-3 text-ink">{t.spanName}</td>
                    <td className="hidden whitespace-nowrap py-[3px] pr-3 lg:table-cell">
                      <span className="rounded-skin-chip px-1 py-px text-[9px] text-muted" style={{ background: 'var(--rp-tone-neutral-bg)' }}>
                        {t.spanCount} spans
                      </span>
                    </td>
                    {/* Duration-Bar: Ausreißer stechen sofort heraus */}
                    <td className="hidden w-[110px] py-[3px] pr-2 md:table-cell">
                      <div className="h-[3px] w-full overflow-hidden rounded-full bg-inset">
                        <div
                          className="h-full rounded-full"
                          style={{
                            width: `${Math.max(2, (Math.log(1 + t.durationMs) / Math.log(1 + maxDur)) * 100)}%`,
                            background: t.durationMs > 500 ? 'var(--rp-yellow)' : 'var(--rp-map-particle)',
                          }}
                        />
                      </div>
                    </td>
                    <td className="whitespace-nowrap py-[3px] pr-3 text-right tnum"
                      style={{ color: t.durationMs > 500 ? 'var(--rp-yellow)' : 'var(--rp-ink)' }}>
                      {fmtMs(t.durationMs)}
                    </td>
                    <td className="whitespace-nowrap py-[3px] pr-3">
                      {isErr ? (
                        <span className="rounded-skin-chip px-1 py-px text-[9px]" style={{ color: 'var(--rp-tone-red-fg)', background: 'var(--rp-tone-red-bg)' }}>
                          ▲ {t.errorCount}
                        </span>
                      ) : t.httpStatus ? (
                        <span
                          className="rounded-skin-chip px-1 py-px text-[9px]"
                          style={
                            t.httpStatus.startsWith('4')
                              ? { color: 'var(--rp-tone-yellow-fg)', background: 'var(--rp-tone-yellow-bg)' }
                              : t.httpStatus.startsWith('2') || t.httpStatus.startsWith('3')
                                ? { color: 'var(--rp-tone-green-fg)', background: 'var(--rp-tone-green-bg)' }
                                : { color: 'var(--rp-ink-muted)', background: 'var(--rp-tone-neutral-bg)' }
                          }
                        >
                          {t.httpStatus}
                        </span>
                      ) : null}
                    </td>
                    <td className="hidden whitespace-nowrap py-[3px] pr-4 text-faint sm:table-cell">{t.traceId.slice(0, 10)}…</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
      </div>

      {openTrace && orgId ? (
        <TraceDrawer
          orgId={orgId}
          clusterId={clusterId}
          traceId={openTrace}
          onClose={() => setOpenTrace(null)}
        />
      ) : null}
    </div>
  );
}

function fmtMs(ms: number): string {
  if (ms >= 60_000) {
    const m = Math.floor(ms / 60_000);
    const sec = Math.round((ms % 60_000) / 1000);
    return `${m}m ${sec}s`;
  }
  if (ms >= 1000) return (ms / 1000).toFixed(2) + 's';
  if (ms >= 10) return ms.toFixed(0) + 'ms';
  return ms.toFixed(1) + 'ms';
}

// Sichtbares Maximum für die Inline-Duration-Bar (log-skaliert, Ausreißer poppen).
function durFrac(ms: number, maxMs: number): number {
  if (maxMs <= 0) return 0;
  return Math.min(1, Math.log(1 + ms) / Math.log(1 + maxMs));
}
