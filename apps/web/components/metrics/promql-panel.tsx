'use client';

import { useCallback, useState } from 'react';
import { Spinner } from '@/components/ui';
import { LineChart } from '@/components/metrics/line-chart';
import { PromQLEditor } from '@/components/metrics/promql-editor';
import type { MetricSeries } from '@/lib/api/types';

// promql-panel.tsx — der „promql"-Tab: echter Prometheus-Editor (Autocomplete
// gegen unsere Metriknamen, Linter), Shift+Enter führt aus, das Ergebnis
// rendert als RETICLE-Chart. Beispiele als Ein-Klick-Chips — dev-freundlich.

const EXAMPLES: { label: string; q: string }[] = [
  {
    label: 'req/min by service',
    q: 'sum by (service_name) (rate(http_server_request_duration_count[5m])) * 60',
  },
  {
    label: 'p95 latency (ms)',
    q: 'histogram_quantile(0.95, sum by (le, service_name) (rate(http_server_request_duration_bucket[5m]))) * 1000',
  },
  {
    label: 'error ratio %',
    q: '100 * sum by (service_name) (rate(http_server_request_duration_count{http_response_status_code=~"5.."}[5m])) / sum by (service_name) (rate(http_server_request_duration_count[5m]))',
  },
  {
    label: 'db p99 (ms)',
    q: 'histogram_quantile(0.99, sum by (le, db_system_name) (rate(db_client_operation_duration_bucket[5m]))) * 1000',
  },
  { label: 'node cpu %', q: 'node_cpu_pct' },
];

function seriesName(metric: Record<string, string>): string {
  const parts = Object.entries(metric)
    .filter(([k]) => k !== '__name__')
    .map(([k, v]) => (k === 'service_name' ? v : `${k}=${v}`));
  return parts.join(' ') || metric['__name__'] || 'value';
}

export function PromQLPanel({
  orgId,
  clusterId,
  sinceMin,
  onSaveAsMetric,
}: {
  orgId: string;
  clusterId: string;
  sinceMin: number;
  /** übernimmt die aktuelle Query in den Custom-Metrik-Editor */
  onSaveAsMetric?: (query: string) => void;
}) {
  const [query, setQuery] = useState(EXAMPLES[0]!.q);
  const [series, setSeries] = useState<MetricSeries[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [lastQuery, setLastQuery] = useState('');
  const apiPrefix = `/api/orgs/${encodeURIComponent(orgId)}/clusters/${encodeURIComponent(clusterId)}/promql`;

  const run = useCallback(async (override?: string) => {
    // override: Beispiel-Chips führen ihre Query DIREKT aus (setQuery ist
    // async — das Closure hätte sonst die alte Query).
    const effective = override ?? query;
    setBusy(true);
    setErr(null);
    try {
      const end = Date.now() / 1000;
      const start = end - sinceMin * 60;
      const step = Math.max(5, Math.round((end - start) / 120));
      const q = new URLSearchParams({ query: effective, start: String(start), end: String(end), step: String(step) });
      const res = await fetch(`${apiPrefix}/api/v1/query_range?${q}`, { credentials: 'include' });
      const body = await res.json();
      if (body.status !== 'success') {
        setErr(body.error ?? 'query failed');
        setSeries(null);
      } else if (body.data.resultType === 'matrix') {
        setSeries(
          body.data.result.map((s: { metric: Record<string, string>; values: [number, string][] }) => ({
            name: seriesName(s.metric),
            points: s.values.map(([t, v]) => ({ t: t * 1000, v: Number(v) })),
          })),
        );
        setLastQuery(effective);
      } else {
        setErr(`result type ${body.data.resultType} — use a range expression`);
        setSeries(null);
      }
    } catch (e) {
      setErr(e instanceof Error ? e.message : 'query failed');
      setSeries(null);
    } finally {
      setBusy(false);
    }
  }, [apiPrefix, query, sinceMin]);

  return (
    <div>
      <div className="flex items-start gap-2">
        <div className="min-w-0 flex-1">
          <PromQLEditor value={query} apiPrefix={apiPrefix} onChange={setQuery} onRun={() => void run()} />
        </div>
        <button
          type="button"
          onClick={() => void run()}
          disabled={busy}
          className="rp-focus h-[44px] shrink-0 rounded-skin-sm px-4 font-mono text-[12px] font-semibold transition-opacity hover:opacity-90"
          style={{ background: 'var(--rp-btn-bg)', color: 'var(--rp-btn-fg)', opacity: busy ? 0.55 : 1 }}
        >
          {busy ? 'running…' : 'Run ⇧⏎'}
        </button>
      </div>

      {/* Beispiele — ein Klick, sofort ausgeführt */}
      <div className="mt-2 flex flex-wrap items-center gap-1.5">
        <span className="rp-micro !text-[9.5px]">examples</span>
        {EXAMPLES.map((e) => (
          <button
            key={e.label}
            type="button"
            onClick={() => {
              setQuery(e.q);
              void run(e.q);
            }}
            className="rounded-skin-chip border border-line px-2 py-0.5 font-mono text-[10px] text-muted transition-colors hover:bg-hover hover:text-ink"
          >
            {e.label}
          </button>
        ))}
        <span className="ml-auto font-mono text-[9.5px] text-faint">
          real prometheus engine on clickhouse · 100% promql
        </span>
      </div>

      {err ? (
        <div
          className="mt-3 rounded-skin-sm px-3 py-2 font-mono text-[11.5px] leading-relaxed"
          style={{ color: 'var(--rp-tone-red-fg)', background: 'var(--rp-tone-red-bg)' }}
        >
          {err}
        </div>
      ) : null}

      {busy && series === null ? (
        <div className="mt-6 flex items-center gap-2 text-muted">
          <Spinner /> <span className="font-mono text-[11px]">evaluating…</span>
        </div>
      ) : series ? (
        series.length === 0 ? (
          <div className="mt-4 font-mono text-[11.5px] text-faint">empty result</div>
        ) : (
          <div className="mt-3">
            <div className="mb-1.5 flex justify-end">
              {onSaveAsMetric ? (
                <button
                  type="button"
                  onClick={() => onSaveAsMetric(lastQuery)}
                  className="rounded-skin-sm border border-line px-2.5 py-1 font-mono text-[10.5px] text-ink transition-colors hover:bg-hover"
                >
                  save as metric →
                </button>
              ) : null}
            </div>
            <LineChart title={lastQuery.slice(0, 110)} unit={`${series.length} series`} series={series.slice(0, 12)} height={260} />
            {series.length > 12 ? (
              <p className="mt-1 font-mono text-[10px] text-faint">showing 12 of {series.length} series — aggregate with sum by (…)</p>
            ) : null}
          </div>
        )
      ) : null}
    </div>
  );
}
