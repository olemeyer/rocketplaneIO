'use client';

import { useMemo } from 'react';
import { useRouter } from 'next/navigation';
import { getServiceMap } from '@/lib/api/query';
import { useLiveQuery } from '@/lib/hooks/use-live-query';
import { status as statusColors } from '@rocketplane/ui';
import type { MapEdge, MapNode, ServiceMapResponse } from '@/lib/api';

const NODE_W = 148;
const NODE_H = 34;
const COL_GAP = 210;
const ROW_GAP = 20;
const PAD = 24;

const STATE: Record<MapNode['status'], keyof typeof statusColors> = {
  healthy: 'resolved',
  degraded: 'degraded',
  critical: 'critical',
};

interface Placed {
  node: MapNode;
  x: number;
  y: number;
  level: number;
}

// layout ordnet Knoten in Spalten nach Aufruf-Tiefe (Entry-Services links).
function layout(map: ServiceMapResponse): { placed: Map<string, Placed>; width: number; height: number } {
  const nodes = map.nodes;
  const outgoing = new Map<string, string[]>();
  const indeg = new Map<string, number>();
  nodes.forEach((n) => indeg.set(n.name, 0));
  for (const e of map.edges) {
    if (!indeg.has(e.from) || !indeg.has(e.to)) continue;
    outgoing.set(e.from, [...(outgoing.get(e.from) ?? []), e.to]);
    indeg.set(e.to, (indeg.get(e.to) ?? 0) + 1);
  }

  // Level per BFS von Entry-Knoten (in-degree 0). Fallback: alle auf 0.
  const level = new Map<string, number>();
  const queue: string[] = [];
  for (const n of nodes) {
    if ((indeg.get(n.name) ?? 0) === 0) {
      level.set(n.name, 0);
      queue.push(n.name);
    }
  }
  if (queue.length === 0 && nodes.length > 0) {
    level.set(nodes[0]!.name, 0);
    queue.push(nodes[0]!.name);
  }
  let guard = 0;
  while (queue.length && guard++ < 1000) {
    const cur = queue.shift()!;
    const lv = level.get(cur) ?? 0;
    for (const nb of outgoing.get(cur) ?? []) {
      const next = lv + 1;
      if (next > (level.get(nb) ?? -1)) {
        level.set(nb, next);
        queue.push(nb);
      }
    }
  }
  nodes.forEach((n) => {
    if (!level.has(n.name)) level.set(n.name, 0);
  });

  // Spalten gruppieren und vertikal verteilen.
  const cols = new Map<number, MapNode[]>();
  for (const n of nodes) {
    const lv = level.get(n.name)!;
    cols.set(lv, [...(cols.get(lv) ?? []), n]);
  }
  const placed = new Map<string, Placed>();
  let maxRows = 0;
  for (const [lv, list] of cols) {
    maxRows = Math.max(maxRows, list.length);
    list.forEach((n, i) => {
      placed.set(n.name, {
        node: n,
        level: lv,
        x: PAD + lv * COL_GAP,
        y: PAD + i * (NODE_H + ROW_GAP),
      });
    });
  }
  const width = PAD * 2 + (Math.max(0, cols.size - 1) * COL_GAP) + NODE_W;
  const height = PAD * 2 + Math.max(1, maxRows) * (NODE_H + ROW_GAP) - ROW_GAP;
  return { placed, width, height };
}

function edgePath(a: Placed, b: Placed): string {
  const x1 = a.x + NODE_W;
  const y1 = a.y + NODE_H / 2;
  const x2 = b.x;
  const y2 = b.y + NODE_H / 2;
  const mx = (x1 + x2) / 2;
  return `M ${x1} ${y1} C ${mx} ${y1}, ${mx} ${y2}, ${x2} ${y2}`;
}

export function ServiceMap() {
  const router = useRouter();
  const { data, status } = useLiveQuery(getServiceMap, { intervalMs: 15_000 });

  const geom = useMemo(() => (data ? layout(data) : null), [data]);

  if (status === 'loading') {
    return (
      <div className="h-64 overflow-hidden rounded-lg border border-line bg-base" role="status" aria-label="loading map">
        <div className="shimmer h-full w-full" />
      </div>
    );
  }
  if (!data || !geom || data.nodes.length === 0) {
    return (
      <div className="rounded-lg border border-line bg-base p-6 text-center text-[13px] text-muted">
        Noch keine Topologie im Zeitfenster.
      </div>
    );
  }

  const maxCalls = Math.max(1, ...data.edges.map((e) => e.callCount));

  return (
    <div className="overflow-x-auto rounded-lg border border-line bg-base p-3">
      <svg
        viewBox={`0 0 ${geom.width} ${geom.height}`}
        width={geom.width}
        height={geom.height}
        className="max-w-none"
        role="img"
        aria-label="service map"
      >
        {/* Kanten */}
        {data.edges.map((e: MapEdge, i) => {
          const a = geom.placed.get(e.from);
          const b = geom.placed.get(e.to);
          if (!a || !b) return null;
          const isErr = e.errorRatio > 0.01;
          const w = 1 + (e.callCount / maxCalls) * 2.5;
          return (
            <path
              key={i}
              d={edgePath(a, b)}
              fill="none"
              stroke={isErr ? statusColors.critical : 'var(--rp-border)'}
              strokeOpacity={isErr ? 0.8 : 1}
              strokeWidth={w}
            />
          );
        })}
        {/* Knoten */}
        {data.nodes.map((n) => {
          const p = geom.placed.get(n.name)!;
          const color = statusColors[STATE[n.status]];
          const href = `/services/${encodeURIComponent(n.name)}`;
          return (
            <g
              key={n.name}
              transform={`translate(${p.x} ${p.y})`}
              className="cursor-pointer"
              role="link"
              aria-label={n.name}
              tabIndex={0}
              onClick={() => router.push(href)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') router.push(href);
              }}
            >
              <rect width={NODE_W} height={NODE_H} rx={8} fill="var(--rp-bg-overlay)" stroke="var(--rp-border)" />
              <circle cx={14} cy={NODE_H / 2} r={4} fill={color} />
              <text x={26} y={NODE_H / 2 + 4} fill="var(--rp-text-strong)" fontSize="12" fontFamily="var(--rp-font-mono)">
                {n.name.length > 15 ? n.name.slice(0, 14) + '…' : n.name}
              </text>
            </g>
          );
        })}
      </svg>
    </div>
  );
}
