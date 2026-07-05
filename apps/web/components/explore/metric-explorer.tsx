'use client';

import { useCallback, useEffect, useMemo, useState } from 'react';
import { getMetric, getMetrics } from '@/lib/api/query';
import { useLiveQuery } from '@/lib/hooks/use-live-query';
import { isRpApiError } from '@/lib/api/client';
import type { MetricData, MetricMeta } from '@/lib/api';
import { Metrics as MetricsIcon, Search } from '../icons';
import { MultiLineChart } from './multi-line-chart';

export function MetricExplorer({ initial = '' }: { initial?: string }) {
  const { data: list, status: listStatus } = useLiveQuery(getMetrics, { intervalMs: 30_000 });
  const metrics = useMemo(() => list?.metrics ?? [], [list]);

  const [selected, setSelected] = useState(initial);
  const [groupByService, setGroupByService] = useState(true);
  const [filter, setFilter] = useState('');

  // Ersten Metrik-Namen wählen, sobald die Liste da ist.
  useEffect(() => {
    if (!selected && metrics.length > 0) setSelected(metrics[0]!.name);
  }, [metrics, selected]);

  const shown = filter
    ? metrics.filter((m) => m.name.toLowerCase().includes(filter.toLowerCase()))
    : metrics;

  return (
    <div className="grid grid-cols-1 gap-4 lg:grid-cols-[240px_1fr]">
      {/* Metrik-Liste */}
      <aside className="space-y-2">
        <label className="flex items-center gap-2 rounded-lg border border-line bg-base px-2.5 py-1.5">
          <Search className="h-3.5 w-3.5 text-faint" />
          <input
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder="filter metrics…"
            className="w-full bg-transparent font-mono text-[12px] text-strong outline-none placeholder:text-faint"
          />
        </label>
        <div className="max-h-[520px] space-y-0.5 overflow-y-auto rounded-lg border border-line bg-base p-1">
          {listStatus === 'loading' && (
            <div className="h-40 overflow-hidden rounded" role="status" aria-label="loading metrics">
              <div className="shimmer h-full w-full" />
            </div>
          )}
          {shown.map((m: MetricMeta) => (
            <button
              key={m.name}
              onClick={() => setSelected(m.name)}
              className={`flex w-full items-center gap-2 rounded-md px-2.5 py-1.5 text-left font-mono text-[11px] transition-colors ${
                selected === m.name ? 'bg-overlay text-strong' : 'text-muted hover:text-strong'
              }`}
            >
              <MetricsIcon className={`h-3.5 w-3.5 shrink-0 ${selected === m.name ? 'text-accent' : 'text-faint'}`} />
              <span className="truncate">{m.name}</span>
              <span className="ml-auto shrink-0 rounded bg-base px-1 text-[9px] uppercase text-faint">{m.type}</span>
            </button>
          ))}
          {shown.length === 0 && listStatus !== 'loading' && (
            <p className="px-2.5 py-3 text-center text-[12px] text-muted">Keine Metriken.</p>
          )}
        </div>
      </aside>

      {/* Chart */}
      <section>
        {selected ? (
          <MetricPanel name={selected} groupByService={groupByService} onToggleGroup={() => setGroupByService((v) => !v)} />
        ) : (
          <div className="grid h-64 place-items-center rounded-lg border border-line bg-base text-[13px] text-muted">
            Wähle eine Metrik.
          </div>
        )}
      </section>
    </div>
  );
}

function MetricPanel({
  name,
  groupByService,
  onToggleGroup,
}: {
  name: string;
  groupByService: boolean;
  onToggleGroup: () => void;
}) {
  const load = useCallback(
    (sig: AbortSignal) => getMetric(name, { groupByService }, sig),
    [name, groupByService],
  );
  const { data, status, error } = useLiveQuery<MetricData>(load, {
    intervalMs: 10_000,
    isEmpty: (v) => v.series.length === 0 || v.series.every((s) => s.points.length === 0),
  });

  return (
    <div className="rounded-lg border border-line bg-base p-4">
      <div className="mb-3 flex items-center gap-3">
        <div>
          <h2 className="font-mono text-[13px] text-strong">{name}</h2>
          {data && (
            <span className="font-mono text-[11px] text-faint">
              {data.type}
              {data.unit ? ` · ${data.unit}` : ''}
            </span>
          )}
        </div>
        <button
          onClick={onToggleGroup}
          className={`ml-auto rounded-md border border-line px-2.5 py-1 font-mono text-[11px] transition-colors ${
            groupByService ? 'bg-overlay text-strong' : 'text-muted hover:text-strong'
          }`}
        >
          per service
        </button>
      </div>

      {status === 'loading' && (
        <div className="h-60 overflow-hidden rounded" role="status" aria-label="loading metric">
          <div className="shimmer h-full w-full" />
        </div>
      )}
      {status === 'error' && (
        <div className="text-[12px] text-muted">
          <span className="text-status-critical">
            {isRpApiError(error) && error.code === 'not_found' ? 'Metrik nicht gefunden' : 'Fehler'}
          </span>{' '}
          — {error?.message}
        </div>
      )}
      {status === 'empty' && <div className="py-6 text-center text-[12px] text-muted">Keine Datenpunkte.</div>}
      {(status === 'success' || status === 'refreshing') && data && (
        <MultiLineChart series={data.series} unit={data.unit ?? ''} />
      )}
    </div>
  );
}
