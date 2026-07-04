'use client';

import Link from 'next/link';
import { getLatestTrace } from '@/lib/api/query';
import { useLiveQuery } from '@/lib/hooks/use-live-query';
import { Waterfall as WaterfallIcon } from '../icons';
import { Waterfall } from './waterfall';

const loadLatest = (signal: AbortSignal) => getLatestTrace('checkout-api', signal);

export function TraceWaterfall() {
  const { data, status, error, refresh } = useLiveQuery(loadLatest, { isEmpty: (v) => v == null });
  const detail = data?.detail;
  const spans = data?.spans ?? [];

  return (
    <div className="rounded-lg border border-line bg-base p-3">
      <div className="mb-2.5 flex items-center gap-2 font-mono text-[11px]">
        <WaterfallIcon className="h-3.5 w-3.5 text-accent" />
        <span className="text-muted">trace</span>
        {detail ? (
          <>
            <Link href={`/traces/${detail.traceId}`} className="text-strong hover:text-accent">
              {detail.traceId.slice(0, 12)}…
            </Link>
            <span className="text-faint">·</span>
            <span className="text-strong">{Math.round(detail.durationMs)}ms</span>
            <span className="text-faint">·</span>
            <span className="text-faint">{detail.spanCount} spans</span>
            {detail.errorCount > 0 && (
              <span className="ml-auto text-status-critical">{detail.errorCount} errors</span>
            )}
          </>
        ) : (
          <span className="text-faint">latest · checkout-api</span>
        )}
      </div>

      {status === 'loading' && (
        <div className="space-y-[3px]" role="status" aria-label="loading trace">
          {Array.from({ length: 6 }).map((_, i) => (
            <div key={i} className="h-3.5 overflow-hidden rounded-[3px] bg-overlay">
              <div className="shimmer h-full w-full" />
            </div>
          ))}
        </div>
      )}

      {status === 'error' && (
        <div className="flex items-center justify-between text-[12px]">
          <span className="text-muted">
            <span className="text-status-critical">Fehler</span> — {error?.message}
          </span>
          <button
            onClick={refresh}
            className="rounded-md bg-accent px-2.5 py-1 text-[12px] font-medium text-[#04140f] transition-[filter] hover:brightness-110"
          >
            Erneut
          </button>
        </div>
      )}

      {status === 'empty' && (
        <div className="py-3 text-center text-[12px] text-muted">Kein Trace gefunden.</div>
      )}

      {(status === 'success' || status === 'refreshing') && <Waterfall spans={spans} />}
    </div>
  );
}
