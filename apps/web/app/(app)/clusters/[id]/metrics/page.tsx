'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import { useParams } from 'next/navigation';
import { cn } from '@/lib/cn';
import { Spinner } from '@/components/ui';
import { useMe } from '@/components/app/me-context';
import { PageHeader } from '@/components/app/page-header';
import { useClusterEvents } from '@/lib/hooks/use-cluster-events';
import { LineChart } from '@/components/metrics/line-chart';
import { CustomMetrics, newPromQLDraft, type MetricDraft } from '@/components/metrics/custom-metrics';
import { PromQLPanel } from '@/components/metrics/promql-panel';
import { getMetricSeries, getServiceMap } from '@/lib/api/controlplane';
import type { MetricSeries, ServiceMap } from '@/lib/api/types';

// Metrics — die Zeitreihen-Wahrheit des Clusters aus EINER Quelle je Signal:
// RED direkt aus den Beyla-Spans (keine zweite Metrik-Pipeline, kein Drift),
// Log-Raten aus otel_logs, Infrastruktur aus den kubelet-Snapshots.
// Live: das topology/logs-SSE-Signal + sanfter Refresh.

const RANGES = [
  { label: '15m', min: 15 },
  { label: '1h', min: 60 },
  { label: '6h', min: 360 },
] as const;

export default function MetricsPage() {
  const params = useParams<{ id: string }>();
  const clusterId = params.id;
  const { currentOrg } = useMe();
  const orgId = currentOrg?.id;

  const [range, setRange] = useState<(typeof RANGES)[number]>(RANGES[0]!);
  const [tab, setTab] = useState<'overview' | 'custom' | 'promql'>('overview');
  const [pendingDraft, setPendingDraft] = useState<MetricDraft | null>(null);
  const [service, setService] = useState('');
  const [map, setMap] = useState<ServiceMap | null>(null);
  const [data, setData] = useState<Record<string, MetricSeries[]>>({});
  const [loaded, setLoaded] = useState(false);

  useEffect(() => {
    if (!orgId) return;
    getServiceMap(orgId, clusterId).then(setMap).catch(() => {});
  }, [orgId, clusterId]);

  const load = useCallback(async () => {
    if (!orgId) return;
    const since = new Date(Date.now() - range.min * 60_000).toISOString();
    const base: Record<string, string> = { since };
    const svc: Record<string, string> = service ? { service } : {};
    const [rate, err, p95, logs, cpu, mem, disk] = await Promise.all([
      getMetricSeries(orgId, clusterId, { kind: 'red', metric: 'rate', ...base, ...svc }),
      getMetricSeries(orgId, clusterId, { kind: 'red', metric: 'error_ratio', ...base, ...svc }),
      getMetricSeries(orgId, clusterId, { kind: 'red', metric: 'p95', ...base, ...svc }),
      getMetricSeries(orgId, clusterId, { kind: 'logs', ...base }),
      getMetricSeries(orgId, clusterId, { kind: 'infra', scope: 'node', metric: 'cpu_pct', ...base }),
      getMetricSeries(orgId, clusterId, { kind: 'infra', scope: 'node', metric: 'mem_pct', ...base }),
      getMetricSeries(orgId, clusterId, { kind: 'infra', scope: 'node', metric: 'disk_pct', ...base }),
    ]).catch(() => [null, null, null, null, null, null, null] as const);
    setData({
      rate: rate?.series ?? [],
      err: err?.series ?? [],
      p95: p95?.series ?? [],
      logs: logs?.series ?? [],
      cpu: cpu?.series ?? [],
      mem: mem?.series ?? [],
      disk: disk?.series ?? [],
    });
    setLoaded(true);
  }, [orgId, clusterId, range.min, service]);

  const loadRef = useRef(load);
  loadRef.current = load;
  const { live } = useClusterEvents(orgId, clusterId, {
    logs: () => void loadRef.current(),
    topology: () => void loadRef.current(),
  });

  useEffect(() => {
    setLoaded(false);
    void load();
    const t = setInterval(() => void load(), live ? 30_000 : 10_000);
    return () => clearInterval(t);
  }, [load, live]);

  const services = (map?.nodes ?? []).filter((n) => n.namespace === 'shop' || !map?.namespaces.includes('shop'));

  return (
    <div className="flex h-[calc(100dvh-52px)] flex-col overflow-y-auto px-4 pb-6 pt-4 sm:px-5">
      <PageHeader kicker="observe / metrics" title="Metrics">
        <div className="flex flex-wrap items-center gap-2">
            <div className="flex items-center rounded-skin-sm border border-line p-[2px]">
              {(['overview', 'custom', 'promql'] as const).map((t) => (
                <button
                  key={t}
                  type="button"
                  onClick={() => setTab(t)}
                  className={cn(
                    'rp-focus rounded-[3px] px-2.5 py-[3px] font-mono text-[10px] uppercase tracking-[0.06em] transition-colors',
                    tab === t ? 'bg-hover text-ink' : 'text-muted hover:text-ink',
                  )}
                  style={tab === t ? { boxShadow: 'inset 0 0 0 1px var(--rp-line-strong)' } : undefined}
                >
                  {t}
                </button>
              ))}
            </div>
            {live ? (
              <span className="mr-1 flex items-center gap-1.5 font-mono text-[11px] text-muted">
                <span className="rp-breath inline-block h-1.5 w-1.5 rounded-full" style={{ background: 'var(--rp-green)', color: 'var(--rp-green)' }} />
                <span className="!text-ink">Live</span>
              </span>
            ) : null}
            <select
              value={service}
              onChange={(e) => setService(e.target.value)}
              className="rp-focus h-8 w-full min-w-0 flex-1 rounded-skin-sm border border-line bg-inset px-2 font-mono text-[11px] text-ink sm:w-auto sm:flex-none"
            >
              <option value="">all services</option>
              {services.map((n) => (
                <option key={n.id} value={n.name}>{n.name}</option>
              ))}
            </select>
            <div className="flex items-center rounded-skin-sm border border-line p-[2px]">
              {RANGES.map((r) => (
                <button
                  key={r.label}
                  type="button"
                  onClick={() => setRange(r)}
                  className={cn(
                    'rp-focus rounded-[3px] px-2 py-[3px] font-mono text-[10px] transition-colors',
                    range.label === r.label ? 'bg-hover text-ink' : 'text-muted hover:text-ink',
                  )}
                  style={range.label === r.label ? { boxShadow: 'inset 0 0 0 1px var(--rp-line-strong)' } : undefined}
                >
                  {r.label}
                </button>
              ))}
            </div>
          </div>
      </PageHeader>

      {tab === 'promql' ? (
        <div className="mt-4">
          {orgId ? (
            <PromQLPanel
              orgId={orgId}
              clusterId={clusterId}
              sinceMin={range.min}
              onSaveAsMetric={(q) => {
                setPendingDraft(newPromQLDraft(q));
                setTab('custom');
              }}
            />
          ) : null}
        </div>
      ) : tab === 'custom' ? (
        <div className="mt-4">
          {orgId ? (
            <CustomMetrics
              orgId={orgId}
              clusterId={clusterId}
              sinceMin={range.min}
              openWith={pendingDraft}
              onConsumedOpenWith={() => setPendingDraft(null)}
            />
          ) : null}
        </div>
      ) : !loaded ? (
        <div className="flex flex-1 items-center justify-center gap-2 text-muted">
          <Spinner /> <span className="font-mono text-[12px]">aggregating…</span>
        </div>
      ) : (
        <>
          <div className="rp-micro mt-4 !text-[10px]">red — from ebpf spans{service ? ` · ${service}` : ''}</div>
          <div className="mt-2 grid grid-cols-1 gap-3 lg:grid-cols-3">
            <LineChart title="request rate" unit="req/min" series={data.rate ?? []} />
            <LineChart title="error ratio" unit="%" series={data.err ?? []} />
            <LineChart title="latency p95" unit="ms" series={data.p95 ?? []} />
          </div>

          <div className="rp-micro mt-5 !text-[10px]">logs</div>
          <div className="mt-2 grid grid-cols-1 gap-3">
            <LineChart title="log volume by severity" unit="lines/min" series={data.logs ?? []} height={150} />
          </div>

          <div className="rp-micro mt-5 !text-[10px]">infrastructure — kubelet</div>
          <div className="mt-2 grid grid-cols-1 gap-3 lg:grid-cols-3">
            <LineChart title="node cpu" unit="%" series={data.cpu ?? []} />
            <LineChart title="node memory" unit="%" series={data.mem ?? []} />
            <LineChart title="node disk" unit="%" series={data.disk ?? []} />
          </div>
        </>
      )}
    </div>
  );
}
