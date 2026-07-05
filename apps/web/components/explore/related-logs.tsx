'use client';

import { useCallback } from 'react';
import { getLogs } from '@/lib/api/query';
import { useLiveQuery } from '@/lib/hooks/use-live-query';
import { Logs } from '../icons';
import { LogRows } from './log-rows';

// RelatedLogs zeigt die über die TraceId korrelierten Logs eines Trace.
export function RelatedLogs({ traceId }: { traceId: string }) {
  const load = useCallback((signal: AbortSignal) => getLogs({ traceId, limit: 50 }, signal), [traceId]);
  const { data, status } = useLiveQuery(load, { isEmpty: (v) => v.logs.length === 0 });

  return (
    <div className="rounded-lg border border-line bg-base p-3">
      <div className="mb-2 flex items-center gap-2 font-mono text-[11px] uppercase tracking-wider text-faint">
        <Logs className="h-3.5 w-3.5 text-accent" />
        related logs
        {data && data.logs.length > 0 && <span className="text-muted">({data.logs.length})</span>}
      </div>
      {status === 'loading' && (
        <div className="h-16 overflow-hidden rounded">
          <div className="shimmer h-full w-full" />
        </div>
      )}
      {status === 'empty' && <p className="py-2 text-center text-[12px] text-muted">Keine Logs für diesen Trace.</p>}
      {(status === 'success' || status === 'refreshing') && data && (
        <div className="-mx-3 -mb-3 overflow-hidden rounded-b-lg">
          <LogRows logs={data.logs} showTraceLink={false} />
        </div>
      )}
    </div>
  );
}
