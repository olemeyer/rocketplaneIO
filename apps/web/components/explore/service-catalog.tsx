'use client';

import Link from 'next/link';
import { getServiceHealth } from '@/lib/api/query';
import { useLiveQuery } from '@/lib/hooks/use-live-query';
import { status as statusColors } from '@rocketplane/ui';
import { StatusDot, Sparkline, formatErrorRate, formatRate } from './primitives';

export function ServiceCatalog() {
  const { data, status } = useLiveQuery(getServiceHealth, { intervalMs: 10_000 });
  const services = data ?? [];

  if (status === 'loading') {
    return (
      <div className="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-3" role="status" aria-label="loading services">
        {Array.from({ length: 6 }).map((_, i) => (
          <div key={i} className="h-[116px] overflow-hidden rounded-lg border border-line bg-base">
            <div className="shimmer h-full w-full" />
          </div>
        ))}
      </div>
    );
  }

  return (
    <div className="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-3">
      {services.map((s) => (
        <Link
          key={s.name}
          href={`/services/${encodeURIComponent(s.name)}`}
          className="rounded-lg border border-line bg-base p-3 transition-colors hover:border-faint hover:bg-overlay"
        >
          <div className="flex items-center gap-1.5">
            <StatusDot state={s.state} />
            <span className="truncate text-[13px] font-medium text-strong">{s.name}</span>
            <span className="ml-auto font-mono text-[10px] uppercase tracking-wider text-faint">{s.state}</span>
          </div>
          <div className="mt-2">
            <Sparkline values={s.spark} color={statusColors[s.state]} />
          </div>
          <div className="mt-2 grid grid-cols-3 gap-1 font-mono text-[11px]">
            <Metric label="p95" value={`${Math.round(s.p95Ms)}ms`} />
            <Metric label="err" value={formatErrorRate(s.errorRate)} />
            <Metric label="rate" value={formatRate(s.throughput)} />
          </div>
        </Link>
      ))}
    </div>
  );
}

function Metric({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <div className="text-[9px] uppercase tracking-wider text-faint">{label}</div>
      <div className="text-strong">{value}</div>
    </div>
  );
}
