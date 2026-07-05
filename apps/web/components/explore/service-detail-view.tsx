'use client';

import { useCallback } from 'react';
import Link from 'next/link';
import { getServiceDetail } from '@/lib/api/query';
import { colorForService } from '@/lib/api';
import { useLiveQuery } from '@/lib/hooks/use-live-query';
import { brand, signal, status as statusColors } from '@rocketplane/ui';
import { ArrowRight, Cube, Logs, Waterfall } from '../icons';
import { TimeChart } from './time-chart';
import { formatErrorRate, formatRate } from './primitives';

export function ServiceDetailView({ name }: { name: string }) {
  const load = useCallback((sig: AbortSignal) => getServiceDetail(name, sig), [name]);
  const { data, status, error } = useLiveQuery(load, { intervalMs: 10_000 });
  const notFound = status === 'error' && error?.code === 'not_found';

  return (
    <div className="space-y-5">
      <div className="flex items-center gap-2 font-mono text-[12px] text-faint">
        <Link href="/services" className="hover:text-strong">
          Services
        </Link>
        <ArrowRight className="h-3 w-3" />
        <span className="text-strong">{name}</span>
      </div>

      {status === 'loading' && (
        <div className="space-y-2" role="status" aria-label="loading service">
          <div className="h-16 overflow-hidden rounded-lg bg-base"><div className="shimmer h-full w-full" /></div>
          <div className="h-24 overflow-hidden rounded-lg bg-base"><div className="shimmer h-full w-full" /></div>
        </div>
      )}

      {notFound && (
        <div className="rounded-lg border border-line bg-base p-8 text-center">
          <p className="text-[14px] text-strong">Service nicht gefunden</p>
          <p className="mt-1 text-[13px] text-muted">Kein Service „{name}" im Zeitfenster.</p>
          <Link href="/services" className="mt-4 inline-block rounded-md bg-accent px-3 py-1.5 text-[13px] font-medium text-[#04140f]">
            Zum Katalog
          </Link>
        </div>
      )}

      {status === 'error' && !notFound && (
        <div className="rounded-lg border border-line bg-base p-4 text-[12px] text-muted">
          <span className="text-status-critical">Fehler</span> — {error?.message}
        </div>
      )}

      {(status === 'success' || status === 'refreshing') && data && (
        <>
          {/* Kopf */}
          <div className="flex flex-wrap items-center gap-3">
            <span className="grid h-9 w-9 place-items-center rounded-lg border border-line bg-base text-accent">
              <Cube className="h-[18px] w-[18px]" />
            </span>
            <div>
              <h1 className="font-display text-[22px] font-semibold tracking-tight text-strong">{data.name}</h1>
              <span className="font-mono text-[11px] uppercase tracking-wider text-faint">{data.status}</span>
            </div>
            <div className="ml-auto flex gap-2">
              <Link
                href={`/traces?service=${encodeURIComponent(data.name)}`}
                className="flex items-center gap-1.5 rounded-md border border-line px-2.5 py-1.5 text-[12px] text-muted transition-colors hover:text-strong"
              >
                <Waterfall className="h-3.5 w-3.5" /> Traces
              </Link>
              <Link
                href={`/logs?service=${encodeURIComponent(data.name)}`}
                className="flex items-center gap-1.5 rounded-md border border-line px-2.5 py-1.5 text-[12px] text-muted transition-colors hover:text-strong"
              >
                <Logs className="h-3.5 w-3.5" /> Logs
              </Link>
            </div>
          </div>

          {/* Charts */}
          <div className="grid gap-2 md:grid-cols-3">
            <TimeChart
              points={data.rateSeries}
              color={signal.metrics}
              label="throughput"
              value={formatRate(data.rate)}
            />
            <TimeChart
              points={data.p95Series}
              color={brand.via}
              label="latency p95"
              value={`${Math.round(data.latencyMs.p95)}`}
              unit="ms"
            />
            <TimeChart
              points={data.errorSeries}
              color={statusColors.critical}
              label="error rate"
              value={formatErrorRate(data.errorRatio)}
            />
          </div>

          {/* Latenz-Quantile */}
          <div className="grid grid-cols-3 gap-2">
            <Kpi label="p50" value={`${Math.round(data.latencyMs.p50)}ms`} />
            <Kpi label="p95" value={`${Math.round(data.latencyMs.p95)}ms`} />
            <Kpi label="p99" value={`${Math.round(data.latencyMs.p99)}ms`} />
          </div>

          {/* Operationen */}
          <section>
            <h2 className="mb-2 font-mono text-[11px] uppercase tracking-wider text-faint">Operations</h2>
            <div className="overflow-hidden rounded-lg border border-line">
              <div className="grid grid-cols-[1fr_90px_80px_80px] gap-2 border-b border-line bg-overlay px-3 py-2 font-mono text-[10px] uppercase tracking-wider text-faint">
                <span>operation</span>
                <span className="text-right">p95</span>
                <span className="text-right">errors</span>
                <span className="text-right">count</span>
              </div>
              {data.operations.length === 0 && (
                <div className="bg-base px-3 py-3 text-center text-[12px] text-muted">Keine Operationen.</div>
              )}
              {data.operations.map((op) => (
                <div key={op.name} className="grid grid-cols-[1fr_90px_80px_80px] gap-2 border-b border-line bg-base px-3 py-2 font-mono text-[12px] last:border-0">
                  <span className="truncate text-strong">{op.name}</span>
                  <span className="text-right text-muted">{Math.round(op.p95Ms)}ms</span>
                  <span className={`text-right ${op.errorRatio > 0.005 ? 'text-status-critical' : 'text-faint'}`}>
                    {formatErrorRate(op.errorRatio)}
                  </span>
                  <span className="text-right text-faint">{op.spanCount}</span>
                </div>
              ))}
            </div>
          </section>

          {/* Dependencies */}
          <section>
            <h2 className="mb-2 font-mono text-[11px] uppercase tracking-wider text-faint">Downstream dependencies</h2>
            {data.dependencies.length === 0 ? (
              <div className="rounded-lg border border-line bg-base px-3 py-3 text-center text-[12px] text-muted">
                Keine nachgelagerten Services.
              </div>
            ) : (
              <div className="flex flex-wrap gap-2">
                {data.dependencies.map((d) => (
                  <Link
                    key={d.service}
                    href={`/services/${encodeURIComponent(d.service)}`}
                    className="flex items-center gap-2 rounded-lg border border-line bg-base px-3 py-2 transition-colors hover:border-faint hover:bg-overlay"
                  >
                    <span className="h-2 w-2 rounded-full" style={{ background: colorForService(d.service) }} />
                    <span className="text-[12px] text-strong">{d.service}</span>
                    <span className="font-mono text-[11px] text-faint">{Math.round(d.p95Ms)}ms</span>
                    <span className={`font-mono text-[11px] ${d.errorRatio > 0.005 ? 'text-status-critical' : 'text-faint'}`}>
                      {formatErrorRate(d.errorRatio)}
                    </span>
                  </Link>
                ))}
              </div>
            )}
          </section>
        </>
      )}
    </div>
  );
}

function Kpi({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-lg border border-line bg-base p-3">
      <div className="font-mono text-[10px] uppercase tracking-wider text-faint">{label}</div>
      <div className="mt-1 text-[18px] font-semibold text-strong">{value}</div>
    </div>
  );
}
