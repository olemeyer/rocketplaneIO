'use client';

import { useCallback } from 'react';
import Link from 'next/link';
import { getTrace } from '@/lib/api/query';
import { colorForService } from '@/lib/api';
import { useLiveQuery } from '@/lib/hooks/use-live-query';
import { ArrowRight, Waterfall as WaterfallIcon } from '../icons';
import { Waterfall } from './waterfall';

export function TraceDetail({ traceId }: { traceId: string }) {
  const load = useCallback((signal: AbortSignal) => getTrace(traceId, signal), [traceId]);
  const { data: view, status, error } = useLiveQuery(load);
  const notFound = status === 'error' && error?.code === 'not_found';

  return (
    <div className="space-y-5">
      <div className="flex items-center gap-2 font-mono text-[12px] text-faint">
        <Link href="/traces" className="hover:text-strong">
          Traces
        </Link>
        <ArrowRight className="h-3 w-3" />
        <span className="text-strong">{traceId.slice(0, 16)}…</span>
      </div>

      {status === 'loading' && (
        <div className="space-y-2" role="status" aria-label="loading trace">
          <div className="h-16 overflow-hidden rounded-lg bg-base">
            <div className="shimmer h-full w-full" />
          </div>
          <div className="h-64 overflow-hidden rounded-lg bg-base">
            <div className="shimmer h-full w-full" />
          </div>
        </div>
      )}

      {notFound && (
        <div className="rounded-lg border border-line bg-base p-8 text-center">
          <p className="text-[14px] text-strong">Trace nicht gefunden</p>
          <p className="mt-1 text-[13px] text-muted">
            Dieser Trace liegt nicht (mehr) im Speicher-Fenster.
          </p>
          <Link
            href="/traces"
            className="mt-4 inline-block rounded-md bg-accent px-3 py-1.5 text-[13px] font-medium text-[#04140f]"
          >
            Zur Trace-Liste
          </Link>
        </div>
      )}

      {status === 'error' && !notFound && (
        <div className="rounded-lg border border-line bg-base p-4 text-[12px] text-muted">
          <span className="text-status-critical">Fehler</span> — {error?.message}
        </div>
      )}

      {(status === 'success' || status === 'refreshing') && view && (
        <>
          {/* Header-KPIs */}
          <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
            <Kpi label="duration" value={`${Math.round(view.detail.durationMs)}ms`} />
            <Kpi label="spans" value={String(view.detail.spanCount)} />
            <Kpi
              label="errors"
              value={String(view.detail.errorCount)}
              tone={view.detail.errorCount > 0 ? 'critical' : 'muted'}
            />
            <Kpi label="services" value={String(view.detail.services.length)} />
          </div>

          {/* Service-Legende */}
          <div className="flex flex-wrap gap-1.5">
            {view.detail.services.map((s) => (
              <span
                key={s}
                className="flex items-center gap-1.5 rounded-full border border-line bg-base px-2 py-0.5 font-mono text-[11px] text-muted"
              >
                <span className="h-2 w-2 rounded-full" style={{ background: colorForService(s) }} />
                {s}
              </span>
            ))}
          </div>

          {/* Waterfall */}
          <div className="rounded-lg border border-line bg-base p-3">
            <div className="mb-2.5 flex items-center gap-2 font-mono text-[11px] text-muted">
              <WaterfallIcon className="h-3.5 w-3.5 text-accent" />
              <span className="text-strong">{view.detail.traceId}</span>
            </div>
            <Waterfall spans={view.spans} />
          </div>
        </>
      )}
    </div>
  );
}

function Kpi({ label, value, tone = 'muted' }: { label: string; value: string; tone?: 'muted' | 'critical' }) {
  return (
    <div className="rounded-lg border border-line bg-base p-3">
      <div className="font-mono text-[10px] uppercase tracking-wider text-faint">{label}</div>
      <div className={`mt-1 text-[20px] font-semibold ${tone === 'critical' ? 'text-status-critical' : 'text-strong'}`}>
        {value}
      </div>
    </div>
  );
}
