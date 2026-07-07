'use client';

import '@xyflow/react/dist/style.css';

import { useEffect, useMemo, useRef } from 'react';
import dagre from '@dagrejs/dagre';
import {
  Background,
  BackgroundVariant,
  ReactFlow,
  ReactFlowProvider,
  useReactFlow,
  type Edge,
  type Node,
  type NodeMouseHandler,
} from '@xyflow/react';
import { statusTone, type SpanNode } from '@/components/traces/trace-drawer';
import { WorkloadNode, nodeSize, type WorkloadNodeData } from '@/components/servicemap/workload-node';
import { FlowEdge, type FlowEdgeData } from '@/components/servicemap/flow-edge';

// trace-graph.tsx — der Trace als Service-Topologie (das dash0 „Trace Graph"-
// Muster), gebaut als ECHTER REUSE der Service-Map: dieselben Instrumenten-
// Kacheln (WorkloadNode) und Rails (FlowEdge), dieselbe Dots-Bühne — nur die
// Daten sind der EINE Trace. Zentrale Zahl = Calls in diesem Trace, Health =
// Status der Spans (Fehler → critical, 4xx → degraded), Kanten frozen (ein
// Trace ist ein Snapshot — Partikel würden Liveness lügen). Klick auf eine
// Kachel selektiert den relevantesten Span (Fehler zuerst, sonst langsamster).

const nodeTypes = { workload: WorkloadNode };
const edgeTypes = { flow: FlowEdge };
const NAME_H = 28;

type SvcAgg = {
  name: string;
  namespace: string;
  calls: number;
  errors: number;
  warns: number;
  worstSpanId: string;
};
type EdgeAgg = { from: string; to: string; calls: number; errors: number };

function aggregate(tree: SpanNode[]): { nodes: SvcAgg[]; edges: EdgeAgg[] } {
  const byId = new Map(tree.map((s) => [s.spanId, s]));
  const nodes = new Map<string, { entries: SpanNode[]; all: SpanNode[]; errors: number; warns: number }>();
  const edges = new Map<string, EdgeAgg>();

  for (const s of tree) {
    const parent = byId.get(s.treeParentId);
    const tone = statusTone(s.httpStatus ?? '', s.statusCode ?? '');
    const n = nodes.get(s.serviceName) ?? { entries: [], all: [], errors: 0, warns: 0 };
    n.all.push(s);
    if (!parent || parent.serviceName !== s.serviceName) n.entries.push(s);
    if (tone === 'err') n.errors += 1;
    if (tone === 'warn') n.warns += 1;
    nodes.set(s.serviceName, n);

    if (parent && parent.serviceName !== s.serviceName) {
      const key = `${parent.serviceName}→${s.serviceName}`;
      const e = edges.get(key) ?? { from: parent.serviceName, to: s.serviceName, calls: 0, errors: 0 };
      e.calls += 1;
      if (tone === 'err') e.errors += 1;
      edges.set(key, e);
    }
  }

  return {
    nodes: Array.from(nodes.entries()).map(([name, n]) => {
      const pool0 = n.entries.length > 0 ? n.entries : n.all;
      const errSpans = pool0.filter(
        (s) => statusTone(s.httpStatus ?? '', s.statusCode ?? '') === 'err',
      );
      const pool = errSpans.length > 0 ? errSpans : pool0;
      const worst = pool.reduce((a, b) => (b.durationMs > a.durationMs ? b : a));
      return {
        name,
        namespace: pool0[0]?.namespace ?? '',
        calls: Math.max(1, n.entries.length),
        errors: n.errors,
        warns: n.warns,
        worstSpanId: worst.spanId,
      };
    }),
    edges: Array.from(edges.values()),
  };
}

function TraceGraphInner({
  tree,
  selected,
  onSelect,
}: {
  tree: SpanNode[];
  selected: string | null;
  onSelect: (spanId: string) => void;
}) {
  const selectedSvc = selected ? tree.find((s) => s.spanId === selected)?.serviceName : undefined;
  const { fitView } = useReactFlow();
  const rootRef = useRef<HTMLDivElement | null>(null);

  // Der Container atmet (SpanDetail unten klappt auf/zu) — bei jeder Größen-
  // änderung nachfitten, sonst rutschen Nodes aus dem Sichtfenster.
  useEffect(() => {
    const el = rootRef.current;
    if (!el) return;
    const ro = new ResizeObserver(() => {
      requestAnimationFrame(() => fitView({ padding: 0.3, maxZoom: 1.1, duration: 160 }));
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, [fitView]);

  const { nodes, edges } = useMemo(() => {
    const agg = aggregate(tree);
    const maxCalls = agg.nodes.reduce((m, n) => Math.max(m, n.calls), 1);
    const maxEdge = agg.edges.reduce((m, e) => Math.max(m, e.calls), 1);

    // Layout wie auf der Map: gerichtetes dagre-LR mit Namensraum unter der Kachel.
    const sizeOf = new Map(
      agg.nodes.map((n) => {
        const t = Math.log(1 + n.calls) / Math.log(1 + maxCalls);
        let size = nodeSize(t);
        if (n.errors > 0 || n.warns > 0) size = Math.max(size, 64);
        return [n.name, size];
      }),
    );
    const g = new dagre.graphlib.Graph();
    g.setDefaultEdgeLabel(() => ({}));
    g.setGraph({ rankdir: 'LR', nodesep: 26, ranksep: 120, marginx: 20, marginy: 20 });
    for (const n of agg.nodes) {
      const s = sizeOf.get(n.name) ?? nodeSize(0);
      g.setNode(n.name, { width: s, height: s + NAME_H });
    }
    for (const e of agg.edges) g.setEdge(e.from, e.to);
    dagre.layout(g);

    const nodes: Node[] = agg.nodes.map((n) => {
      const s = sizeOf.get(n.name) ?? nodeSize(0);
      const p = g.node(n.name);
      const data: WorkloadNodeData = {
        name: n.name,
        namespace: n.namespace,
        kind: 'service',
        health: n.errors > 0 ? 'critical' : n.warns > 0 ? 'degraded' : 'healthy',
        podsReady: 0,
        podsTotal: 0,
        restarts: 0,
        metric: n.calls,
        caption: 'calls',
        size: s,
      };
      return {
        id: n.name,
        type: 'workload',
        position: { x: p.x - s / 2, y: p.y - (s + NAME_H) / 2 },
        data,
        selected: selectedSvc === n.name,
        draggable: false,
      };
    });

    const edges: Edge[] = agg.edges.map((e) => {
      const data: FlowEdgeData = {
        // Gewicht log-normiert über die calls dieses Traces (gleicher Längenkanal
        // wie die Service-Map); Herkunft ist per Definition L7 (Spans).
        weight: maxEdge > 0 ? Math.log(1 + e.calls) / Math.log(1 + maxEdge) : 0,
        edgeSource: 'trace',
        connCount: e.calls,
        errorRatio: e.calls > 0 ? e.errors / e.calls : 0,
        // ein Trace ist Vergangenheit — Partikel (Liveness) wären gelogen
        frozen: true,
        focused: selectedSvc === e.from || selectedSvc === e.to,
      };
      return {
        id: `${e.from}→${e.to}`,
        source: e.from,
        target: e.to,
        type: 'flow',
        data,
      };
    });

    return { nodes, edges };
  }, [tree, selectedSvc]);

  const onNodeClick: NodeMouseHandler = (_, node) => {
    const agg = aggregate(tree);
    const svc = agg.nodes.find((n) => n.name === node.id);
    if (svc) onSelect(svc.worstSpanId);
  };

  return (
    <div ref={rootRef} className="h-full w-full">
    <ReactFlow
      nodes={nodes}
      edges={edges}
      nodeTypes={nodeTypes}
      edgeTypes={edgeTypes}
      onNodeClick={onNodeClick}
      fitView
      fitViewOptions={{ padding: 0.3, maxZoom: 1.1 }}
      minZoom={0.2}
      maxZoom={2}
      nodesDraggable={false}
      nodesConnectable={false}
      proOptions={{ hideAttribution: true }}
      className="rp-servicemap"
    >
      <Background variant={BackgroundVariant.Dots} gap={26} size={1} color="var(--rp-map-edge)" />
    </ReactFlow>
    </div>
  );
}

export function TraceGraph(props: {
  tree: SpanNode[];
  selected: string | null;
  onSelect: (spanId: string) => void;
}) {
  return (
    <div className="h-full w-full" style={{ background: 'var(--rp-map-bg)' }}>
      <ReactFlowProvider>
        <TraceGraphInner {...props} />
      </ReactFlowProvider>
    </div>
  );
}
