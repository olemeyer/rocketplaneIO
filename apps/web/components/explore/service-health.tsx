'use client';

import { getServiceHealth } from '@/lib/api/query';
import { useLiveQuery } from '@/lib/hooks/use-live-query';
import { FilterChip, Sparkline, StatusDot, formatErrorRate, formatRate } from './primitives';
import { status as statusColors } from '@rocketplane/ui';

export function ServiceHealthPanel() {
  const { data, status, error, refresh } = useLiveQuery(getServiceHealth, { intervalMs: 10_000 });
  const services = data ?? [];

  return (
    <section aria-label="Service health">
      <div className="mb-2 flex items-center justify-between">
        <span className="flex items-center gap-2 font-mono text-[11px] uppercase tracking-wider text-faint">
          Service health
          {status === 'refreshing' && (
            <span className="h-1 w-1 animate-pulse rounded-full bg-accent" aria-label="refreshing" />
          )}
        </span>
        <span className="font-mono text-[11px] text-faint">p95 · error rate · throughput</span>
      </div>

      {status === 'loading' && (
        <div className="grid grid-cols-2 gap-2 lg:grid-cols-4" role="status" aria-label="loading services">
          {Array.from({ length: 4 }).map((_, i) => (
            <div key={i} className="h-[92px] overflow-hidden rounded-lg border border-line bg-base">
              <div className="shimmer h-full w-full" />
            </div>
          ))}
        </div>
      )}

      {status === 'error' && (
        <div className="flex items-center justify-between rounded-lg border border-line bg-base p-3 text-[12px]">
          <span className="text-muted">
            <span className="text-status-critical">Fehler beim Laden</span> — {error?.message}
          </span>
          <button
            onClick={refresh}
            className="rounded-md bg-accent px-2.5 py-1 text-[12px] font-medium text-[#04140f] transition-[filter] hover:brightness-110"
          >
            Erneut versuchen
          </button>
        </div>
      )}

      {status === 'empty' && (
        <div className="rounded-lg border border-line bg-base p-4 text-center text-[12px] text-muted">
          Keine Services im Zeitfenster.
        </div>
      )}

      {(status === 'success' || status === 'refreshing') && (
        <div className="grid grid-cols-2 gap-2 lg:grid-cols-4">
          {services.map((s) => (
            <div key={s.name} className="rounded-lg border border-line bg-base p-2.5">
              <div className="flex items-center gap-1.5">
                <StatusDot state={s.state} />
                <span className="truncate text-[12px] text-strong">{s.name}</span>
              </div>
              <div className="mt-2">
                <Sparkline values={s.spark} color={statusColors[s.state]} />
              </div>
              <div className="mt-2 flex items-center justify-between font-mono text-[11px]">
                <span className="text-strong">{Math.round(s.p95Ms)}ms</span>
                <span className={s.errorRate < 0.001 ? 'text-faint' : 'text-muted'}>
                  {formatErrorRate(s.errorRate)}
                </span>
                <span className="text-faint">{formatRate(s.throughput)}</span>
              </div>
            </div>
          ))}
        </div>
      )}
    </section>
  );
}

// FilterBar ist statisch (Facetten-Demo) und lebt neben den Live-Cards.
export function FilterBar() {
  return (
    <div className="flex items-center gap-2 rounded-lg border border-line bg-base px-3 py-2">
      <span className="font-mono text-[11px] text-faint">filter</span>
      <div className="flex flex-1 flex-wrap items-center gap-1.5">
        <FilterChip k="service.name" v="checkout-api" />
        <FilterChip k="http.status_code" v="≥ 500" />
        <span className="font-mono text-[11px] text-faint">+ add filter</span>
      </div>
      <kbd className="rounded border border-line bg-overlay px-1.5 py-0.5 font-mono text-[10px] text-faint">⌘K</kbd>
    </div>
  );
}
