'use client';

import { useMemo, useState } from 'react';
import { useParams } from 'next/navigation';
import { cn } from '@/lib/cn';
import { Spinner } from '@/components/ui';
import { useMe } from '@/components/app/me-context';
import { PageHeader } from '@/components/app/page-header';
import { useInfra } from '@/lib/hooks/use-infra';
import { Gauge, fmtBytes, fmtCores, usageTone } from '@/components/infra/gauge';
import Link from 'next/link';
import type { InfraNode } from '@/lib/api/types';

// Nodes — die Hardware-Wahrheit des Clusters, live (kubelet stats/summary
// über den Agenten, SSE-getrieben). ADAPTIV: wenige Nodes = große
// Instrumenten-Karten mit vollen Gauges; viele Nodes = dichte RACK-WAND
// (Heatmap-Kacheln, Farbe = heißeste Ressource), Klick vergrößert die Karte.

const CARD_LIMIT = 8; // bis hierhin große Karten, darüber Rack-Wand

function hottest(n: InfraNode): number {
  const fr = (u: number, t: number) => (u >= 0 && t > 0 ? u / t : 0);
  return Math.max(
    fr(n.cpuUsageM, n.cpuAllocatableM),
    fr(n.memUsage, n.memAllocatable),
    fr(n.fsUsed, n.fsCapacity),
  );
}

export default function NodesPage() {
  const params = useParams<{ id: string }>();
  const clusterId = params.id;
  const { currentOrg } = useMe();
  const orgId = currentOrg?.id;
  const { nodes, live } = useInfra(orgId, clusterId);
  const [focus, setFocus] = useState<string | null>(null);

  const sum = useMemo(() => {
    const s = { cpuU: 0, cpuT: 0, memU: 0, memT: 0, fsU: 0, fsT: 0, pods: 0, podCap: 0, notReady: 0 };
    for (const n of nodes ?? []) {
      if (n.cpuUsageM >= 0) s.cpuU += n.cpuUsageM;
      s.cpuT += n.cpuAllocatableM;
      if (n.memUsage >= 0) s.memU += n.memUsage;
      s.memT += n.memAllocatable;
      if (n.fsUsed >= 0) s.fsU += n.fsUsed;
      if (n.fsCapacity >= 0) s.fsT += n.fsCapacity;
      s.pods += n.podCount;
      s.podCap += n.podCapacity;
      if (!n.ready) s.notReady += 1;
    }
    return s;
  }, [nodes]);

  const dense = (nodes?.length ?? 0) > CARD_LIMIT;
  const focused = focus ? nodes?.find((n) => n.name === focus) : undefined;

  return (
    <div className="flex h-[calc(100dvh-52px)] flex-col overflow-y-auto px-4 pb-6 pt-4 sm:px-5">
      <PageHeader kicker="infrastructure / nodes" title="Nodes">
        <div className="flex items-center gap-3 font-mono text-[11px] text-muted tnum">
          {live ? (
            <span className="flex items-center gap-1.5">
              <span
                className="rp-breath inline-block h-1.5 w-1.5 rounded-full"
                style={{ background: 'var(--rp-green)', color: 'var(--rp-green)' }}
              />
              <span className="!text-ink">Live</span>
            </span>
          ) : null}
          <span>{nodes?.length ?? '…'} nodes</span>
          <span>
            {sum.pods}/{sum.podCap} pods
          </span>
          {sum.notReady > 0 ? (
            <span style={{ color: 'var(--rp-red)' }}>▲ {sum.notReady} not ready</span>
          ) : null}
        </div>
      </PageHeader>

      {/* Cluster-Summe — drei große Instrumente */}
      <div className="mt-3 grid shrink-0 grid-cols-1 gap-3 sm:grid-cols-3">
        <SummaryCard label="cpu" used={sum.cpuU} total={sum.cpuT} render={(u, t) => `${fmtCores(u)} / ${fmtCores(t)} cores`} />
        <SummaryCard label="memory" used={sum.memU} total={sum.memT} render={(u, t) => `${fmtBytes(u)} / ${fmtBytes(t)}`} />
        <SummaryCard label="disk" used={sum.fsU} total={sum.fsT} render={(u, t) => `${fmtBytes(u)} / ${fmtBytes(t)}`} />
      </div>

      {nodes === null ? (
        <div className="flex flex-1 items-center justify-center gap-2 text-muted">
          <Spinner /> <span className="font-mono text-[12px]">reading hardware…</span>
        </div>
      ) : nodes.length === 0 ? (
        <div className="flex flex-1 items-center justify-center font-mono text-[12px] text-muted">
          no nodes reported yet
        </div>
      ) : dense ? (
        <>
          {/* RACK-WAND: hunderte Nodes auf einen Blick — Farbe = heißeste Ressource */}
          <div className="mt-4 flex flex-wrap gap-1.5">
            {nodes.map((n) => {
              const h = hottest(n);
              const tone = usageTone(h);
              const bad = !n.ready || n.pressure !== '';
              return (
                <button
                  key={n.name}
                  type="button"
                  onClick={() => setFocus((f) => (f === n.name ? null : n.name))}
                  title={`${n.name} · cpu ${Math.round((n.cpuUsageM / Math.max(1, n.cpuAllocatableM)) * 100)}% · mem ${Math.round((n.memUsage / Math.max(1, n.memAllocatable)) * 100)}%`}
                  className={cn(
                    'rp-focus relative h-[46px] w-[92px] rounded-skin-sm border p-1.5 text-left transition-colors',
                    focus === n.name ? 'bg-hover' : 'bg-raised hover:bg-hover',
                  )}
                  style={{
                    borderColor: bad ? 'var(--rp-red)' : 'var(--rp-line)',
                    boxShadow: focus === n.name ? 'inset 0 0 0 1px var(--rp-line-strong)' : 'var(--rp-rim)',
                  }}
                >
                  <span className="block truncate font-mono text-[9px] leading-none text-mid">{n.name}</span>
                  <MiniBar frac={n.cpuUsageM >= 0 ? n.cpuUsageM / Math.max(1, n.cpuAllocatableM) : 0} />
                  <MiniBar frac={n.memUsage >= 0 ? n.memUsage / Math.max(1, n.memAllocatable) : 0} />
                  {bad ? (
                    <span className="absolute right-1 top-1 text-[8px]" style={{ color: 'var(--rp-red)' }}>
                      ▲
                    </span>
                  ) : h >= 0.9 ? (
                    <span className="absolute right-1 top-1 h-1.5 w-1.5 rounded-full" style={{ background: tone.bar }} />
                  ) : null}
                </button>
              );
            })}
          </div>
          {focused ? (
            <div className="mt-3">
              <NodeCard node={focused} clusterId={clusterId} />
            </div>
          ) : null}
        </>
      ) : (
        /* Wenige Nodes: volle Instrumenten-Karten */
        <div className="mt-4 grid grid-cols-1 gap-3 lg:grid-cols-2 2xl:grid-cols-3">
          {nodes.map((n) => (
            <NodeCard key={n.name} node={n} clusterId={clusterId} />
          ))}
        </div>
      )}
    </div>
  );
}

function SummaryCard({
  label,
  used,
  total,
  render,
}: {
  label: string;
  used: number;
  total: number;
  render: (u: number, t: number) => string;
}) {
  return (
    <div className="rounded-skin border border-line bg-raised p-3" style={{ boxShadow: 'var(--rp-rim)' }}>
      <Gauge label={`cluster ${label}`} used={used} total={total} render={render} height={8} />
    </div>
  );
}

function MiniBar({ frac }: { frac: number }) {
  const f = Math.min(1, Math.max(0, frac));
  const tone = usageTone(f);
  return (
    <span className="mt-[3px] block h-[3px] w-full overflow-hidden rounded-full bg-inset">
      <span className="block h-full rounded-full" style={{ width: `${f * 100}%`, background: tone.bar, opacity: 0.8 }} />
    </span>
  );
}

function NodeCard({
  node: n,
  clusterId,
}: {
  node: InfraNode;
  clusterId: string;
}) {
  const pressures = n.pressure ? n.pressure.split(',') : [];
  return (
    <div className="rounded-skin border border-line bg-raised p-4" style={{ boxShadow: 'var(--rp-rim)' }}>
      <div className="flex items-center gap-2 border-b border-line-strong pb-2.5">
        <span
          className={cn('inline-block h-2 w-2 rounded-full', n.ready && 'rp-breath')}
          style={{
            background: n.ready ? 'var(--rp-green)' : 'var(--rp-red)',
            color: n.ready ? 'var(--rp-green)' : 'var(--rp-red)',
          }}
          title={n.ready ? 'Ready' : 'NotReady'}
        />
        <h2 className="truncate font-mono text-[14px] font-bold tracking-tight text-ink">{n.name}</h2>
        <span className="rounded-skin-chip bg-inset px-1.5 py-0.5 font-mono text-[9px] uppercase tracking-[0.06em] text-muted">
          {n.role}
        </span>
        {n.unschedulable ? (
          <span
            className="rounded-skin-chip px-1.5 py-0.5 font-mono text-[9px] uppercase tracking-[0.05em]"
            style={{ color: 'var(--rp-tone-yellow-fg)', background: 'var(--rp-tone-yellow-bg)' }}
          >
            ◆ cordoned
          </span>
        ) : null}
        {pressures.map((p) => (
          <span
            key={p}
            className="rounded-skin-chip px-1.5 py-0.5 font-mono text-[9px] uppercase tracking-[0.05em]"
            style={{ color: 'var(--rp-tone-red-fg)', background: 'var(--rp-tone-red-bg)' }}
          >
            ◆ {p} pressure
          </span>
        ))}
        <span className="ml-auto font-mono text-[10px] text-faint tnum">{n.internalIp}</span>
      </div>

      <div className="mt-3 space-y-2.5">
        <Gauge label="cpu" used={n.cpuUsageM} total={n.cpuAllocatableM} render={(u, t) => `${fmtCores(u)} / ${fmtCores(t)} cores`} />
        <Gauge label="memory" used={n.memUsage} total={n.memAllocatable} render={(u, t) => `${fmtBytes(u)} / ${fmtBytes(t)}`} />
        <Gauge label="disk" used={n.fsUsed} total={n.fsCapacity} render={(u, t) => `${fmtBytes(u)} / ${fmtBytes(t)}`} />
        <Gauge label="pods" used={n.podCount} total={n.podCapacity} render={(u, t) => `${u} / ${t}`} height={4} />
      </div>

      <div className="mt-3 flex items-center justify-between border-t border-line pt-2 font-mono text-[9.5px] text-faint">
        <span className="truncate" title={n.osImage}>
          {n.osImage}
        </span>
        <span className="shrink-0 pl-3">
          {n.kubeletVersion} · {n.arch}
          {n.imageFsUsed >= 0 ? ` · images ${fmtBytes(n.imageFsUsed)}` : ''}
        </span>
      </div>

      {/* Node operations (cordon/drain) are now performed by external agents via
          Transactions — see the Transactions page for the audit trail. */}
      <div className="mt-2 border-t border-line pt-2">
        <Link
          href={`/clusters/${clusterId}/transactions`}
          className="font-mono text-[10px] text-muted transition-colors hover:text-ink"
        >
          view transactions →
        </Link>
      </div>
    </div>
  );
}
