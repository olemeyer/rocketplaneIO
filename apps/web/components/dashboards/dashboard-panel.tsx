'use client';

import { useEffect, useState } from 'react';
import { Spinner } from '@/components/ui';
import { LineChart } from '@/components/metrics/line-chart';
import type { MetricSeries } from '@/lib/api/types';
import type { DashPanel } from '@/lib/dashboards';

// dashboard-panel.tsx — EIN Panel: führt seine PromQL-Query gegen die eingebettete
// Engine aus und rendert das RETICLE-Zeitreihen-Instrument. Live-refresh über refreshKey.

function seriesName(metric: Record<string, string>): string {
  const parts = Object.entries(metric)
    .filter(([k]) => k !== '__name__')
    .map(([k, v]) => (k === 'service_name' ? v : `${k}=${v}`));
  return parts.join(' ') || metric['__name__'] || 'value';
}

export function DashboardPanel({
  orgId,
  clusterId,
  panel,
  sinceMin,
  refreshKey,
}: {
  orgId: string;
  clusterId: string;
  panel: DashPanel;
  sinceMin: number;
  refreshKey: number;
}) {
  const [series, setSeries] = useState<MetricSeries[] | null>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    let active = true;
    (async () => {
      setErr(null);
      try {
        const end = Date.now() / 1000;
        const start = end - sinceMin * 60;
        const step = Math.max(5, Math.round((end - start) / 120));
        const q = new URLSearchParams({
          query: panel.query,
          start: String(start),
          end: String(end),
          step: String(step),
        });
        const res = await fetch(
          `/api/orgs/${encodeURIComponent(orgId)}/clusters/${encodeURIComponent(clusterId)}/promql/api/v1/query_range?${q}`,
          { credentials: 'include' },
        );
        const body = await res.json();
        if (!active) return;
        if (body.status !== 'success') {
          setErr(body.error ?? 'query failed');
          setSeries(null);
        } else if (body.data?.resultType === 'matrix') {
          setSeries(
            body.data.result.map((s: { metric: Record<string, string>; values: [number, string][] }) => ({
              name: seriesName(s.metric),
              points: s.values.map(([t, v]) => ({ t: t * 1000, v: Number(v) })),
            })),
          );
        } else {
          setErr('not a range result — use a range expression');
          setSeries(null);
        }
      } catch (e) {
        if (active) {
          setErr(e instanceof Error ? e.message : 'query failed');
          setSeries(null);
        }
      }
    })();
    return () => {
      active = false;
    };
  }, [orgId, clusterId, panel.query, sinceMin, refreshKey]);

  return (
    <div className="overflow-hidden rounded-skin border border-line bg-raised p-3" style={{ boxShadow: 'var(--rp-rim)' }}>
      {err ? (
        <div className="flex h-[180px] flex-col">
          <div className="flex items-center gap-1.5 font-mono text-[11.5px] font-semibold text-ink">
            {/* Crimson error must carry a colorblind glyph (RETICLE law 4). */}
            <span aria-hidden style={{ color: 'var(--rp-tone-red-fg)' }}>▲</span>
            <span className="truncate">{panel.title}</span>
          </div>
          <div className="mt-1 flex flex-1 items-start overflow-hidden">
            <p
              className="max-h-full w-full overflow-y-auto break-words font-mono text-[10px] leading-relaxed"
              style={{ color: 'var(--rp-tone-red-fg)' }}
              title={err}
            >
              {err}
            </p>
          </div>
        </div>
      ) : series === null ? (
        <div className="flex h-[180px] flex-col">
          <div className="font-mono text-[11.5px] font-semibold text-ink">{panel.title}</div>
          <div className="flex flex-1 items-center justify-center gap-2 text-muted">
            <Spinner /> <span className="font-mono text-[11px]">querying…</span>
          </div>
        </div>
      ) : series.length === 0 ? (
        <div className="flex h-[180px] flex-col">
          <div className="font-mono text-[11.5px] font-semibold text-ink">{panel.title}</div>
          <div className="flex flex-1 items-center justify-center">
            <span className="font-mono text-[11px] text-faint">no data in range</span>
          </div>
        </div>
      ) : (
        <LineChart
          title={panel.title}
          unit={panel.unit ?? `${series.length} series`}
          series={series.slice(0, 12)}
          height={168}
        />
      )}
    </div>
  );
}
