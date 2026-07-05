'use client';

import Link from 'next/link';
import { getAlerts, formatMetricValue } from '@/lib/api/query';
import { useLiveQuery } from '@/lib/hooks/use-live-query';
import { Bell } from '../icons';

const METRIC_LABEL: Record<string, string> = { errorRatio: 'error rate', p95: 'p95 latency' };

export function AlertList() {
  const { data, status, error, refresh } = useLiveQuery(getAlerts, { intervalMs: 10_000 });
  const alerts = data?.alerts ?? [];

  return (
    <div className="space-y-3">
      {/* Zusammenfassung */}
      <div className="flex items-center gap-3 rounded-lg border border-line bg-base px-3 py-2.5">
        <Bell className="h-4 w-4 text-accent" />
        {data ? (
          <span className="text-[13px] text-strong">
            <span className={data.firing > 0 ? 'text-status-critical' : 'text-status-resolved'}>
              {data.firing} firing
            </span>
            <span className="text-faint"> / {data.total} rules</span>
          </span>
        ) : (
          <span className="text-[13px] text-muted">Alert rules</span>
        )}
        <span className="ml-auto flex items-center gap-1.5 font-mono text-[11px] text-faint">
          <span className="h-1 w-1 animate-pulse rounded-full bg-accent" /> live
        </span>
      </div>

      {status === 'loading' && (
        <div className="space-y-[3px]" role="status" aria-label="loading alerts">
          {Array.from({ length: 6 }).map((_, i) => (
            <div key={i} className="h-14 overflow-hidden rounded-lg bg-base">
              <div className="shimmer h-full w-full" />
            </div>
          ))}
        </div>
      )}

      {status === 'error' && (
        <div className="flex items-center justify-between rounded-lg border border-line bg-base p-4 text-[12px] text-muted">
          <span>
            <span className="text-status-critical">Fehler</span> — {error?.message}
          </span>
          <button onClick={refresh} className="rounded-md bg-accent px-2.5 py-1 text-[12px] font-medium text-[#04140f]">
            Erneut
          </button>
        </div>
      )}

      {(status === 'success' || status === 'refreshing' || status === 'empty') && (
        <div className="space-y-2">
          {alerts.map((a) => {
            const critical = a.severity === 'critical';
            return (
              <div
                key={a.id}
                className={`flex items-center gap-3 rounded-lg border bg-base p-3 ${
                  a.firing ? (critical ? 'border-status-critical/40' : 'border-status-degraded/40') : 'border-line'
                }`}
              >
                <span
                  className={`h-2 w-2 shrink-0 rounded-full ${
                    !a.firing ? 'bg-status-resolved' : critical ? 'bg-status-critical' : 'bg-status-degraded'
                  }`}
                  style={a.firing ? { boxShadow: '0 0 8px currentColor' } : undefined}
                />
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <span className="truncate text-[13px] text-strong">{a.name}</span>
                    <span
                      className={`rounded px-1.5 py-0.5 font-mono text-[10px] uppercase ${
                        !a.firing
                          ? 'bg-overlay text-faint'
                          : critical
                            ? 'bg-[rgba(242,85,90,0.14)] text-status-critical'
                            : 'bg-[rgba(245,181,68,0.14)] text-status-degraded'
                      }`}
                    >
                      {a.firing ? a.severity : 'ok'}
                    </span>
                  </div>
                  <div className="mt-0.5 font-mono text-[11px] text-faint">
                    <Link href={`/services/${encodeURIComponent(a.service)}`} className="hover:text-accent">
                      {a.service}
                    </Link>
                    {' · '}
                    {METRIC_LABEL[a.metric] ?? a.metric} {formatMetricValue(a.metric, a.value)}
                    <span className="text-faint"> (threshold {formatMetricValue(a.metric, a.threshold)})</span>
                  </div>
                </div>
                <Link
                  href={`/services/${encodeURIComponent(a.service)}`}
                  className="shrink-0 rounded-md border border-line px-2.5 py-1.5 text-[12px] text-muted transition-colors hover:text-strong"
                >
                  View service
                </Link>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
