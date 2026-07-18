'use client';

import '@xyflow/react/dist/style.css';

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import dagre from '@dagrejs/dagre';
import {
  Background,
  BackgroundVariant,
  Controls,
  MiniMap,
  ReactFlow,
  ReactFlowProvider,
  useEdgesState,
  useNodesInitialized,
  useNodesState,
  useReactFlow,
  type Edge,
  type Node,
} from '@xyflow/react';

import Link from 'next/link';
import { getActions, getServiceMap } from '@/lib/api/controlplane';
import type { ClusterAction, MapEdge, MapNode, ServiceMap, WorkloadHealth } from '@/lib/api/types';
import { Spinner } from '@/components/ui';
import { cn } from '@/lib/cn';
import { useClusterEvents } from '@/lib/hooks/use-cluster-events';
import { detectTech } from '@/lib/tech-icons';
import { WorkloadNode, nodeSize, type WorkloadNodeData } from './workload-node';
import { FlowEdge, type FlowEdgeData } from './flow-edge';
import { WorkloadDrawer } from './workload-drawer';

const nodeTypes = { workload: WorkloadNode };
const edgeTypes = { flow: FlowEdge };
const EMPTY_SET = new Set<string>();

const POLL_MS = 5000; // Fallback ohne SSE; mit Stream nur noch Sicherheitsnetz
const POLL_LIVE_MS = 30_000;
const ACTIONS_POLL_MS = 2500; // Fallback; live kommen Aktionen per Event
const ACTIONS_POLL_LIVE_MS = 15_000;
const RESULT_LINGER_MS = 12_000; // Ergebnis-Ring bleibt so lange am Node stehen
const NAME_H = 28; // vertikaler Platz für den Namen unter dem Squircle

type Pos = { x: number; y: number; w: number; h: number };
type Metrics = Map<string, { metric: number; caption: string; size: number }>;

// Kanten tragen je nach Quelle verschiedene Mengen-Einheiten (trace: req/s ·
// flow: Bytes/s · conntrack: conns). Für Breite/Größe braucht es EINEN
// vergleichbaren Kanal: das Gewicht 0..1, log-normiert INNERHALB seiner Quelle
// (Einheiten werden nie gemischt — die echten Werte zeigt der Hover).
function edgeWeights(map: ServiceMap): Map<MapEdge, number> {
  const maxOf = { trace: 0, flow: 0, conntrack: 0 };
  const volume = (e: MapEdge): [keyof typeof maxOf, number] => {
    if (e.source === 'trace') return ['trace', e.reqRate ?? 0];
    if (e.source === 'flow') return ['flow', e.bytesRate ?? 0];
    return ['conntrack', e.connCount];
  };
  for (const e of map.edges) {
    const [k, v] = volume(e);
    maxOf[k] = Math.max(maxOf[k], v);
  }
  const w = new Map<MapEdge, number>();
  for (const e of map.edges) {
    const [k, v] = volume(e);
    w.set(e, maxOf[k] > 0 ? Math.log(1 + v) / Math.log(1 + maxOf[k]) : 0);
  }
  return w;
}

// Traffic-Metrik je Node: die zentrale Zahl ist EHRLICH in ihrer Einheit —
// req/s-Summe der Trace-Kanten, wo es welche gibt; sonst conns (conntrack).
// Reine L4-Nachbarn (nur Bytes) zeigen keine erfundene Rate. Die GRÖSSE skaliert
// über die quell-normierten Kantengewichte (vergleichbarer Rang ohne
// Einheiten-Mix); nicht-gesunde Workloads behalten den Size-Floor, damit ein
// traffic-armer CrashLoop nicht zum kleinsten (= übersehenen) Node wird.
function computeMetrics(map: ServiceMap): Metrics {
  const weights = edgeWeights(map);
  const wSum = new Map<string, number>();
  const reqSum = new Map<string, number>();
  const connSum = new Map<string, number>();
  const add = (m: Map<string, number>, id: string, v: number) => m.set(id, (m.get(id) ?? 0) + v);
  for (const e of map.edges) {
    const w = weights.get(e) ?? 0;
    add(wSum, e.from, w);
    add(wSum, e.to, w);
    if (e.source === 'trace') {
      add(reqSum, e.from, e.reqRate ?? 0);
      add(reqSum, e.to, e.reqRate ?? 0);
    } else if (e.source !== 'flow') {
      add(connSum, e.from, e.connCount);
      add(connSum, e.to, e.connCount);
    }
  }
  let max = 1;
  for (const n of map.nodes) max = Math.max(max, wSum.get(n.id) ?? 0);
  const m: Metrics = new Map();
  for (const n of map.nodes) {
    const req = reqSum.get(n.id) ?? 0;
    const conns = connSum.get(n.id) ?? 0;
    const [metric, caption] = req > 0 ? ([req, 'req/s'] as const) : ([conns, 'conns'] as const);
    const t = Math.log(1 + (wSum.get(n.id) ?? 0)) / Math.log(1 + max);
    let size = nodeSize(t);
    if (n.health === 'critical' || n.health === 'degraded') size = Math.max(size, 64);
    m.set(n.id, { metric, caption, size });
  }
  return m;
}

// Layout: verbundene Komponente als gerichtetes Flow-Layout (dagre LR) — die
// Kanten tragen ihr Traffic-Gewicht, damit dagre die HAUPT-Pfade begradigt und
// Nebenpfade drumherum legt (die Map liest sich dann entlang des Flusses).
// Isolierte Workloads (kein Traffic) kommen als Grid DARUNTER: der Flow läuft
// links→rechts, ein Block links davor würde wie sein Anfang wirken. Node-Zellen
// tragen die individuelle Squircle-Größe + Platz für den Namen.
function layout(map: ServiceMap, metrics: Metrics): Record<string, Pos> {
  const pos: Record<string, Pos> = {};
  const sizeOf = (id: string) => metrics.get(id)?.size ?? nodeSize(0);
  const weights = edgeWeights(map);

  const degree = new Map<string, number>();
  for (const e of map.edges) {
    if (e.from === e.to) continue;
    degree.set(e.from, (degree.get(e.from) ?? 0) + 1);
    degree.set(e.to, (degree.get(e.to) ?? 0) + 1);
  }
  const connected = map.nodes.filter((n) => degree.has(n.id));
  const isolated = map.nodes.filter((n) => !degree.has(n.id));

  let minX = 0;
  let minY = 0;
  let maxY = 0;
  let connW = 0;
  if (connected.length) {
    const g = new dagre.graphlib.Graph();
    g.setDefaultEdgeLabel(() => ({}));
    g.setGraph({ rankdir: 'LR', nodesep: 34, ranksep: 140, edgesep: 18, marginx: 20, marginy: 20 });
    for (const n of connected) {
      const s = sizeOf(n.id);
      g.setNode(n.id, { width: s, height: s + NAME_H });
    }
    for (const e of map.edges) {
      if (e.from === e.to) continue;
      // dagre-weight 1..5: schwere Kanten werden kurz + gerade gehalten.
      g.setEdge(e.from, e.to, { weight: 1 + Math.round((weights.get(e) ?? 0) * 4) });
    }
    dagre.layout(g);
    minX = Infinity;
    minY = Infinity;
    maxY = -Infinity;
    let maxX = -Infinity;
    for (const n of connected) {
      const s = sizeOf(n.id);
      const gn = g.node(n.id);
      const p: Pos = { x: gn.x - s / 2, y: gn.y - (s + NAME_H) / 2, w: s, h: s + NAME_H };
      pos[n.id] = p;
      minX = Math.min(minX, p.x);
      minY = Math.min(minY, p.y);
      maxX = Math.max(maxX, p.x + p.w);
      maxY = Math.max(maxY, p.y + p.h);
    }
    connW = maxX - minX;
  }

  if (isolated.length) {
    // Zellbreite muss die 140px-Labels unter den Nodes tragen — sonst überlappen
    // die Namen benachbarter Spalten.
    const cell = 156;
    const cellH = nodeSize(0) + NAME_H + 26;
    // Grid-Breite an der Flow-Breite ausrichten — ein Sockel unter der Map,
    // kein zweiter Turm daneben.
    const cols = Math.max(4, Math.min(isolated.length, Math.floor(Math.max(connW, cell * 4) / cell)));
    const startX = connected.length ? minX : 0;
    const startY = connected.length ? maxY + 72 : 0;
    isolated.forEach((n, i) => {
      const s = sizeOf(n.id);
      const col = i % cols;
      const row = Math.floor(i / cols);
      // Squircle in der Zelle zentrieren.
      pos[n.id] = {
        x: startX + col * cell + (cell - s) / 2,
        y: startY + row * cellH,
        w: s,
        h: s + NAME_H,
      };
    });
  }

  return pos;
}

function topoSig(map: ServiceMap): string {
  const ns = map.nodes.map((n) => n.id).sort().join(',');
  const es = map.edges.map((e) => `${e.from}>${e.to}`).sort().join(',');
  return ns + '|' + es;
}

// scopeMap grenzt die Map auf einen Namespace ein: Workloads DES Namespace + ihre
// direkten Nachbarn aus anderen Namespaces (als Kontext-Rand). So bleiben die
// cross-namespace-Abhängigkeiten (z.B. shop → kube-system) sichtbar, statt sie
// wegzufiltern. `context` = die Kontext-Knoten (werden gedimmt dargestellt).
function scopeMap(
  map: ServiceMap | null,
  ns: string | null,
): { map: ServiceMap; context: Set<string> } | null {
  if (!map) return null;
  if (!ns) return { map, context: new Set() };
  const inScope = new Set(map.nodes.filter((n) => n.namespace === ns).map((n) => n.id));
  const neighbor = new Set<string>();
  for (const e of map.edges) {
    if (inScope.has(e.from)) neighbor.add(e.to);
    if (inScope.has(e.to)) neighbor.add(e.from);
  }
  const visible = new Set<string>([...inScope, ...neighbor]);
  const nodes = map.nodes.filter((n) => visible.has(n.id));
  const edges = map.edges.filter((e) => visible.has(e.from) && visible.has(e.to));
  const namespaces = Array.from(new Set(nodes.map((n) => n.namespace)));
  const context = new Set([...neighbor].filter((id) => !inScope.has(id)));
  return { map: { namespaces, nodes, edges }, context };
}

const HEALTH_ORDER: WorkloadHealth[] = ['critical', 'degraded', 'healthy', 'unknown'];

// Live-Actions je Node-ID: die frischeste zählt (running > jüngstes Ergebnis).
// delete_pod wird über den Pod-Namen-Präfix dem Workload zugeordnet.
function actionsByNode(actions: ClusterAction[], nodes: MapNode[]): Map<string, WorkloadNodeData['action']> {
  const out = new Map<string, WorkloadNodeData['action']>();
  const cutoff = Date.now() - RESULT_LINGER_MS;
  // actions kommen neueste zuerst; erste Zuordnung je Node gewinnt, außer eine
  // laufende überschreibt ein Ergebnis.
  for (const a of actions) {
    // Parked runs (MCP approval gate) execute nothing — keep the map calm.
    if (a.status === 'awaiting_approval') continue;
    const running = a.status === 'pending' || a.status === 'running';
    if (!running && new Date(a.updatedAt).getTime() < cutoff) continue;
    const node = nodes.find((n) => {
      if (n.namespace !== a.targetNamespace) return false;
      if (a.kind === 'delete_pod') return a.targetName.startsWith(n.name + '-');
      return n.name === a.targetName;
    });
    if (!node) continue;
    const cur = out.get(node.id);
    const curRunning = cur && (cur.status === 'pending' || cur.status === 'running');
    if (cur && (curRunning || !running)) continue;
    // Kurz-Detail für die Node-Caption: die Zahlen aus dem Live-Progress
    // („2/3 available") — sonst das Scale-Ziel.
    const counts = running ? a.progress.match(/\d+\/\d+[^·]*/)?.[0]?.trim() : undefined;
    const detail =
      counts ??
      (a.kind === 'scale' && a.params && typeof a.params === 'object' && 'replicas' in a.params
        ? `→ ${(a.params as { replicas?: number }).replicas}`
        : undefined);
    out.set(node.id, { kind: a.kind, status: a.status, detail });
  }
  return out;
}

function buildGraph(
  map: ServiceMap,
  pos: Record<string, Pos>,
  metrics: Metrics,
  selected: string | null,
  context: Set<string>,
  nodeActions: Map<string, WorkloadNodeData['action']>,
  dragPos: Map<string, { x: number; y: number }>,
): { nodes: Node[]; edges: Edge[] } {
  const weights = edgeWeights(map);

  const neighbors = new Set<string>();
  if (selected) {
    neighbors.add(selected);
    for (const e of map.edges) {
      if (e.from === selected) neighbors.add(e.to);
      if (e.to === selected) neighbors.add(e.from);
    }
  }

  const nodes: Node[] = map.nodes.map((n: MapNode) => {
    const mm = metrics.get(n.id);
    const p = pos[n.id];
    const dragged = dragPos.get(n.id);
    // Ohne Selektion dimmt der Namespace-Kontext; mit Selektion übersteuert diese.
    const dimmed = selected ? !neighbors.has(n.id) : context.has(n.id);
    return {
      id: n.id,
      type: 'workload',
      position: dragged ?? { x: p?.x ?? 0, y: p?.y ?? 0 },
      // RF-natives selected-Flag mitschreiben — sonst löscht jeder Rebuild die
      // Selektion und der Passer verschwindet.
      selected: selected === n.id,
      data: {
        name: n.name,
        namespace: n.namespace,
        kind: n.kind,
        health: n.health,
        podsReady: n.podsReady,
        podsTotal: n.podsTotal,
        restarts: n.restarts,
        metric: mm?.metric ?? 0,
        caption: mm?.caption,
        size: mm?.size ?? nodeSize(0),
        focused: selected ? neighbors.has(n.id) : false,
        dimmed,
        action: nodeActions.get(n.id),
        // Tech-Identität: manueller Override gewinnt, sonst Auto-Erkennung
        icon: n.icon || detectTech(n.image),
      } satisfies WorkloadNodeData,
    };
  });

  const edges: Edge[] = map.edges.map((e) => {
    const active = selected ? e.from === selected || e.to === selected : false;
    return {
      id: `${e.from}__${e.to}`,
      source: e.from,
      target: e.to,
      type: 'flow',
      data: {
        weight: weights.get(e) ?? 0,
        edgeSource: e.source ?? 'conntrack',
        protocol: e.protocol,
        reqRate: e.reqRate,
        errRate: e.errRate,
        p95Ms: e.p95Ms,
        bytesRate: e.bytesRate,
        connCount: e.connCount,
        errorRatio: e.errRate,
        focused: active,
        dimmed: selected ? !active : false,
      } satisfies FlowEdgeData,
    };
  });

  return { nodes, edges };
}

/* ── Legende ──────────────────────────────────────────────────────────────── */

// Health-Legende als RETICLE-Chips: Colorblind-Glyph (▲/◆/○) + Mono-Uppercase +
// tabular-Count. Gesund bleibt neutral — nur Anomalien tragen Ton.
const HEALTH_META: Record<
  WorkloadHealth,
  { label: string; glyph: string; varName: string; bg?: string; fg?: string }
> = {
  critical: { label: 'Critical', glyph: '▲', varName: 'var(--rp-red)', bg: 'var(--rp-tone-red-bg)', fg: 'var(--rp-tone-red-fg)' },
  degraded: { label: 'Degraded', glyph: '◆', varName: 'var(--rp-yellow)', bg: 'var(--rp-tone-yellow-bg)', fg: 'var(--rp-tone-yellow-fg)' },
  healthy: { label: 'Healthy', glyph: '○', varName: 'var(--rp-line-strong)' },
  unknown: { label: 'Unknown', glyph: '◌', varName: 'var(--rp-ink-faint)' },
};

function HealthLegend({ counts }: { counts: Record<WorkloadHealth, number> }) {
  return (
    <div className="flex items-center gap-1.5">
      {HEALTH_ORDER.filter((h) => counts[h] > 0).map((h) => {
        const m = HEALTH_META[h];
        return (
          <span
            key={h}
            className="inline-flex items-center gap-1 rounded-skin-chip px-1.5 py-0.5 font-mono text-[10px] font-medium uppercase leading-none tracking-[0.08em]"
            style={{ background: m.bg ?? 'transparent', color: m.fg ?? 'var(--rp-ink-muted)' }}
          >
            <span aria-hidden>{m.glyph}</span>
            {m.label}
            <span className="font-bold tnum" style={{ color: m.fg ?? 'var(--rp-ink)' }}>
              {counts[h]}
            </span>
          </span>
        );
      })}
    </div>
  );
}

/* ── Innere Map ───────────────────────────────────────────────────────────── */

function MapInner({
  orgId,
  clusterId,
  namespace,
}: {
  orgId: string;
  clusterId: string;
  namespace: string | null;
}) {
  const [map, setMap] = useState<ServiceMap | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [selected, setSelected] = useState<string | null>(null);
  const firstLoad = useRef(true);

  // Auf den aktiven Namespace eingrenzen (Namespace + direkte Nachbarn als Kontext).
  const scopedRes = useMemo(() => scopeMap(map, namespace), [map, namespace]);
  const scoped = scopedRes?.map ?? null;
  const context = scopedRes?.context ?? EMPTY_SET;

  const [nodes, setNodes, onNodesChange] = useNodesState<Node>([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>([]);

  const rf = useReactFlow();
  const nodesInited = useNodesInitialized();
  const fittedSig = useRef<string>('');
  const wrapperRef = useRef<HTMLDivElement>(null);
  const posRef = useRef<{ sig: string; pos: Record<string, Pos> }>({ sig: '', pos: {} });
  const renderSigRef = useRef('');
  // Von Hand gezogene Nodes bleiben, wo der Mensch sie hingelegt hat: die
  // Overrides übersteuern das berechnete Layout bei JEDEM Background-Rebuild
  // (Poll/SSE/Action-Tick), bis „re-layout" sie explizit verwirft. Während
  // eines aktiven Drags wird gar nicht gefüttert — sonst springt der Node
  // unter dem Cursor zurück.
  const dragPos = useRef(new Map<string, { x: number; y: number }>());
  const draggingRef = useRef(false);
  const [layoutGen, setLayoutGen] = useState(0); // re-layout: erzwingt Neuaufbau

  const lastMapJson = useRef('');
  const load = useCallback(async () => {
    try {
      const next = await getServiceMap(orgId, clusterId);
      // Anti-Blink: identische Daten → KEIN setState, kein Rerender, keine
      // neu startenden Partikel-Animationen.
      const j = JSON.stringify(next);
      if (j !== lastMapJson.current) {
        lastMapJson.current = j;
        setMap(next);
      }
      setError(null);
    } catch (err) {
      if (firstLoad.current) setError(err instanceof Error ? err.message : 'Failed to load map');
    } finally {
      firstLoad.current = false;
    }
  }, [orgId, clusterId]);

  // SSE: Topologie/Aktionen kommen als Push — Polls degradieren zum Fallback.
  const loadRef = useRef(load);
  loadRef.current = load;
  const pollActionsRef = useRef<() => void>(() => {});
  const { live } = useClusterEvents(orgId, clusterId, {
    topology: () => void loadRef.current(),
    actions: () => pollActionsRef.current(),
  });

  useEffect(() => {
    firstLoad.current = true;
    setMap(null);
    void load();
    const id = window.setInterval(load, live ? POLL_LIVE_MS : POLL_MS);
    return () => window.clearInterval(id);
  }, [load, live]);

  // Live-Actions: schneller Takt, damit Ring/Ticker sofort reagieren. `tick`
  // treibt zusätzlich das Ausblenden der Ergebnis-Ringe (linger) an. Wechselt
  // ein Aktions-Status (z.B. running→succeeded), wird die Map SOFORT neu
  // geladen — Pods/Health ziehen ohne Poll-Latenz nach (der Agent pusht nach
  // Aktionen ohnehin im Burst).
  const [actions, setActions] = useState<ClusterAction[]>([]);
  const [tick, setTick] = useState(0);
  const actionSig = useRef('');
  const fastUntil = useRef(0); // nach Aktionen: Map kurz im schnellen Takt laden
  useEffect(() => {
    let alive = true;
    const poll = () =>
      getActions(orgId, clusterId, '', '', 25)
        .then((r) => {
          if (!alive) return;
          setActions(r.actions);
          const sig = r.actions.map((a) => `${a.id}:${a.status}`).join('|');
          const busy = r.actions.some((a) => a.status === 'pending' || a.status === 'running');
          // Laufende Aktion oder frischer Statuswechsel → kurz im schnellen
          // Takt laden, bis der Rollout (neue Pods ready) sichtbar durch ist.
          if (busy || (actionSig.current && sig !== actionSig.current)) {
            fastUntil.current = Date.now() + 30_000;
          }
          if (Date.now() < fastUntil.current) void load();
          actionSig.current = sig;
        })
        .catch(() => {});
    pollActionsRef.current = () => void poll();
    void poll();
    const id = window.setInterval(() => void poll(), live ? ACTIONS_POLL_LIVE_MS : ACTIONS_POLL_MS);
    return () => {
      alive = false;
      window.clearInterval(id);
    };
  }, [orgId, clusterId, load, live]);

  // UI-Aging der Ergebnis-Ringe (linger) — reiner Render-Tick, kein Netz.
  useEffect(() => {
    const id = window.setInterval(() => setTick((t) => t + 1), 2500);
    return () => window.clearInterval(id);
  }, []);

  useEffect(() => {
    if (!scoped) return;
    // Während eines aktiven Drags NIE füttern — setNodes würde den Node unter
    // dem Cursor auf die Layout-Position zurückreißen.
    if (draggingRef.current) return;
    const sig = (namespace ?? '*') + '|' + layoutGen + '|' + topoSig(scoped);
    const metrics = computeMetrics(scoped);
    if (sig !== posRef.current.sig) {
      posRef.current = { sig, pos: layout(scoped, metrics) };
      // Overrides verschwundener Nodes aufräumen — bestehende bleiben, wo der
      // Mensch sie hingelegt hat, auch wenn die Topologie sich ändert.
      for (const id of dragPos.current.keys()) {
        if (!(id in posRef.current.pos)) dragPos.current.delete(id);
      }
    }
    const nodeActions = actionsByNode(actions, scoped.nodes);
    const built = buildGraph(scoped, posRef.current.pos, metrics, selected, context, nodeActions, dragPos.current);
    // Anti-Blink: React Flow nur füttern, wenn sich sichtbare Daten geändert
    // haben — sonst remounten die SVG-Kanten und ihre Animationen springen.
    const renderSig = JSON.stringify({
      n: built.nodes.map((n) => [n.id, n.position, n.selected, n.data]),
      e: built.edges.map((e) => [e.id, e.data]),
    });
    if (renderSig !== renderSigRef.current) {
      renderSigRef.current = renderSig;
      setNodes(built.nodes);
      setEdges(built.edges);
    }
    // tick lässt abgelaufene Ergebnis-Ringe (linger) auch ohne neue Daten altern
  }, [scoped, context, namespace, selected, actions, tick, layoutGen, setNodes, setEdges]);

  // Fit je Topologie — GENAU EINMAL pro Signatur. Retry über `nodes` (bis sig +
  // Messung bereit sind), aber die Signatur wird SOFORT geclaimt und der Fit läuft
  // deferred OHNE Cleanup-Cancel — sonst räumen Tick/Actions/Live-Updates den Fit
  // weg (→ nur halber Fit oder gar keiner). doFit bailt bei unmount (null-Ref).
  useEffect(() => {
    if (!nodesInited) return;
    const sig = posRef.current.sig;
    if (!sig || fittedSig.current === sig) return;
    if (Object.keys(posRef.current.pos).length === 0) return;
    fittedSig.current = sig;

    const doFit = () => {
      const el = wrapperRef.current;
      if (!el || el.clientWidth < 40 || el.clientHeight < 40) return;
      // Schmale Screens: React Flows fitView passt GARANTIERT alle Nodes ein
      // (kein Desktop-Zoom-Floor → sonst laufen Nodes aus dem Viewport).
      if (el.clientWidth < 720) {
        void rf.fitView({ padding: 0.08, duration: 500, minZoom: 0.1, maxZoom: 1 });
        return;
      }
      // Desktop: „Landing-Hero"-Fit mit Zoom-Floor, damit die Fläche gefüllt wird.
      let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;
      for (const p of Object.values(posRef.current.pos)) {
        minX = Math.min(minX, p.x);
        minY = Math.min(minY, p.y);
        maxX = Math.max(maxX, p.x + p.w);
        maxY = Math.max(maxY, p.y + p.h);
      }
      const gw = Math.max(1, maxX - minX);
      const gh = Math.max(1, maxY - minY);
      const pw = el.clientWidth;
      const ph = el.clientHeight;
      const insetTop = 54, insetBottom = 76, insetX = 40;
      const availW = Math.max(1, pw - 2 * insetX);
      const availH = Math.max(1, ph - insetTop - insetBottom);
      const zoomRaw = Math.min(availW / gw, availH / gh);
      const zoom = Math.min(1.2, Math.max(zoomRaw, 0.68));
      const x = insetX + (availW - gw * zoom) / 2 - minX * zoom;
      const y = insetTop + Math.min(0, (availH - gh * zoom) / 2) + Math.max(0, (availH - gh * zoom) / 2) - minY * zoom;
      void rf.setViewport({ x, y, zoom }, { duration: 500 });
    };

    // Deferred, bewusst OHNE Cleanup-Cancel (überlebt Re-Renders durch Live-Ticks).
    window.setTimeout(doFit, 90);
  }, [nodes, nodesInited, rf]);

  const counts = useMemo(() => {
    const c: Record<WorkloadHealth, number> = { healthy: 0, degraded: 0, critical: 0, unknown: 0 };
    scoped?.nodes.forEach((n) => (c[n.health] += 1));
    return c;
  }, [scoped]);

  const onNodeClick = useCallback(
    (_: unknown, node: Node) => setSelected((cur) => (cur === node.id ? null : node.id)),
    [],
  );

  // Escape hebt die Selektion auf (die Investigation-Leiste verspricht es).
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') setSelected(null);
    }
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, []);

  const nsCount = scoped?.namespaces.length ?? 0;
  const nodeCount = scoped?.nodes.length ?? 0;
  const edgeCount = scoped?.edges.length ?? 0;
  const selectedNode = selected ? (scoped?.nodes.find((n) => n.id === selected) ?? null) : null;

  return (
    <div ref={wrapperRef} className="relative h-full w-full">
      {/* Toolbar */}
      <div className="pointer-events-none absolute inset-x-0 top-0 z-10 flex flex-wrap items-center justify-between gap-2 px-2 py-2 sm:gap-3 sm:px-3 sm:py-2.5">
        <div
          className="pointer-events-auto flex items-center gap-2.5 rounded-skin px-3 py-1.5 backdrop-blur"
          style={{
            border: '1px solid color-mix(in oklab, var(--rp-map-edge) 55%, transparent)',
            background: 'color-mix(in oklab, var(--rp-map-bg) 78%, transparent)',
          }}
        >
          <span className="flex items-center gap-1.5">
            <span
              className="rp-breath inline-block h-1.5 w-1.5 rounded-full"
              style={{ background: 'var(--rp-green)', color: 'var(--rp-green)' }}
            />
            <span className="rp-micro !text-[10px] !text-ink">Live</span>
          </span>
          <span className="h-3 w-px" style={{ background: 'var(--rp-map-edge)' }} />
          <span className="font-mono text-[10px] text-muted tnum">
            {nodeCount} workloads · {edgeCount} flows · {nsCount} namespaces
          </span>
          <span className="h-3 w-px" style={{ background: 'var(--rp-map-edge)' }} />
          {/* verwirft manuelle Drag-Positionen und berechnet das Layout neu */}
          <button
            type="button"
            className="rp-focus rp-micro !text-[10px] !text-muted transition-colors hover:!text-ink"
            onClick={() => {
              dragPos.current.clear();
              fittedSig.current = '';
              setLayoutGen((g) => g + 1);
            }}
            title="Recompute the layout (discards manually moved nodes)"
          >
            re-layout
          </button>
        </div>

        <div
          className="pointer-events-auto rounded-skin px-3 py-1.5 backdrop-blur"
          style={{
            border: '1px solid color-mix(in oklab, var(--rp-map-edge) 55%, transparent)',
            background: 'color-mix(in oklab, var(--rp-map-bg) 78%, transparent)',
          }}
        >
          <HealthLegend counts={counts} />
        </div>
      </div>

      {/* Investigation-Leiste — erscheint mit der Selektion: Node → Logs/Traces */}
      {selected && !selectedNode ? (
        <SelectionBar
          selected={selected}
          node={null}
          clusterId={clusterId}
          onClear={() => setSelected(null)}
        />
      ) : null}

      {/* Workload-Panel — Vitals, Pods und Safe-Actions des selektierten Nodes */}
      {selectedNode ? (
        <WorkloadDrawer
          orgId={orgId}
          clusterId={clusterId}
          node={selectedNode}
          onClose={() => setSelected(null)}
        />
      ) : null}

      {/* Activity-Ticker — laufende + frische Aktionen, unten links über der Map */}
      <ActivityTicker
        actions={actions}
        onFocus={(a) => {
          const n = scoped?.nodes.find(
            (n) =>
              n.namespace === a.targetNamespace &&
              (a.kind === 'delete_pod' ? a.targetName.startsWith(n.name + '-') : n.name === a.targetName),
          );
          if (n) setSelected(n.id);
        }}
      />

      {error && !map ? (
        <div className="flex h-full flex-col items-center justify-center gap-2 text-center">
          <div className="rp-micro !text-[10px] text-faint">topology / error</div>
          <span className="font-mono text-[12px]" style={{ color: 'var(--rp-tone-red-fg)' }}>{error}</span>
        </div>
      ) : !map ? (
        <div className="flex h-full flex-col items-center justify-center gap-3 px-6 text-center reveal">
          <div className="rp-micro !text-[10px] text-faint">topology / discovering</div>
          <div className="flex items-center gap-2 text-muted">
            <Spinner /> <span className="font-mono text-[12.5px]">Mapping workloads &amp; flows…</span>
          </div>
          <p className="max-w-[34ch] font-mono text-[11px] leading-relaxed text-faint">
            eBPF observes every connection — the graph draws itself as traffic appears.
          </p>
        </div>
      ) : nodeCount === 0 ? (
        <div className="flex h-full flex-col items-center justify-center gap-2 px-6 text-center">
          <div className="rp-micro !text-[10px] text-faint">topology / empty</div>
          <p className="font-mono text-[12.5px] text-muted">No workloads reported yet.</p>
          <p className="max-w-[34ch] font-mono text-[11px] leading-relaxed text-faint">
            The agent syncs every 20s — services appear here the moment they talk.
          </p>
        </div>
      ) : (
        <ReactFlow
          nodes={nodes}
          edges={edges}
          onNodesChange={onNodesChange}
          onEdgesChange={onEdgesChange}
          nodeTypes={nodeTypes}
          edgeTypes={edgeTypes}
          onNodeClick={onNodeClick}
          onNodeDragStart={() => {
            draggingRef.current = true;
          }}
          onNodeDragStop={(_, node) => {
            draggingRef.current = false;
            dragPos.current.set(node.id, { x: node.position.x, y: node.position.y });
          }}
          onPaneClick={() => setSelected(null)}
          minZoom={0.1}
          maxZoom={2}
          proOptions={{ hideAttribution: true }}
          className="rp-servicemap"
        >
          <Background variant={BackgroundVariant.Dots} gap={26} size={1} color="var(--rp-map-edge)" />
          <Controls showInteractive={false} className="rp-flow-controls" />
          <MiniMap
            pannable
            zoomable
            className="rp-flow-minimap !hidden sm:!block"
            maskColor="color-mix(in oklab, var(--rp-map-bg) 72%, transparent)"
            nodeColor={(n) => {
              const h = (n.data as WorkloadNodeData | undefined)?.health;
              if (h === 'critical') return 'var(--rp-node-crit)';
              if (h === 'degraded') return 'var(--rp-node-warn)';
              if (h === 'unknown') return 'var(--rp-node-idle)';
              return 'var(--rp-node-ok)';
            }}
            nodeStrokeWidth={0}
          />
        </ReactFlow>
      )}
    </div>
  );
}

// ActivityTicker — das Live-Fenster der Safe-Actions unten links: laufende
// Aktionen atmen, frische Ergebnisse stehen noch RESULT_LINGER_MS. Klick auf
// eine Zeile springt zum betroffenen Node (Selektion + Panel).
function ActivityTicker({
  actions,
  onFocus,
}: {
  actions: ClusterAction[];
  onFocus: (a: ClusterAction) => void;
}) {
  const cutoff = Date.now() - RESULT_LINGER_MS;
  const visible = actions
    .filter(
      (a) =>
        a.status === 'pending' ||
        a.status === 'running' ||
        new Date(a.updatedAt).getTime() >= cutoff,
    )
    .slice(0, 4);
  if (visible.length === 0) return null;

  const verb = (k: string) => k.replace(/_/g, ' ');
  return (
    <div className="pointer-events-none absolute bottom-4 left-3 z-10 flex flex-col gap-1.5">
      {visible.map((a) => {
        const running = a.status === 'pending' || a.status === 'running';
        const ok = a.status === 'succeeded';
        const color = running ? 'var(--rp-ink-mid)' : ok ? 'var(--rp-tone-green-fg)' : 'var(--rp-tone-red-fg)';
        return (
          <button
            key={a.id}
            type="button"
            onClick={() => onFocus(a)}
            className="pointer-events-auto flex items-center gap-2 rounded-skin border border-line px-2.5 py-1.5 text-left font-mono text-[10.5px] backdrop-blur transition-colors hover:bg-hover"
            style={{
              background: 'color-mix(in oklab, var(--rp-map-bg) 82%, transparent)',
              boxShadow: 'var(--rp-rim)',
              animation: 'reveal-up var(--rp-dur-med) var(--rp-ease-enter)',
            }}
            title={a.result || `${verb(a.kind)} ${a.targetName}`}
          >
            <span
              className={cn('inline-block h-1.5 w-1.5 shrink-0 rounded-full', running && 'rp-breath')}
              style={{ background: color, color }}
              aria-hidden
            />
            <span className="text-ink">{verb(a.kind)}</span>
            <span className="max-w-[160px] truncate text-mid">{a.targetName}</span>
            <span className="max-w-[220px] truncate" style={{ color }}>
              {running ? a.progress || a.status : ok ? '✓' : '▲ failed'}
            </span>
            <span className="text-faint tnum">{a.createdAt.slice(11, 19)}</span>
          </button>
        );
      })}
    </div>
  );
}

// SelectionBar — die Investigation-Leiste unten mittig: der selektierte Workload
// mit Ein-Klick-Sprüngen in seine Logs/Traces (Scope wandert mit — man landet
// nie im ungefilterten Nichts).
function SelectionBar({
  selected,
  node,
  clusterId,
  onClear,
}: {
  selected: string;
  node: MapNode | null;
  clusterId: string;
  onClear: () => void;
}) {
  const parts = selected.split('/'); // namespace/kind/name
  const name = parts[2] ?? selected;
  const ns = parts[0] ?? '';
  return (
    <div className="pointer-events-none absolute inset-x-0 bottom-4 z-10 flex justify-center">
      <div
        className="pointer-events-auto flex items-center gap-1 rounded-skin-lg border border-line px-1.5 py-1 backdrop-blur"
        style={{
          background: 'color-mix(in oklab, var(--rp-overlay) 88%, transparent)',
          boxShadow: 'var(--rp-rim), var(--rp-shadow-pop)',
        }}
      >
        <span className="flex items-center gap-2 px-2 font-mono text-[11.5px] text-ink">
          <span
            className="inline-block h-1.5 w-1.5 rounded-full"
            style={{ background: 'var(--rp-ink-muted)' }}
          />
          {name}
          <span className="text-faint">{ns}</span>
        </span>
        {node ? (
          <>
            <span className="h-4 w-px bg-line" aria-hidden />
            <span className="flex items-center gap-2.5 px-2 font-mono text-[10.5px] text-mid tnum">
              <span title="pods ready / desired">
                pods {node.podsReady}/{node.podsTotal}
              </span>
              {node.restarts > 0 ? (
                <span style={{ color: 'var(--rp-yellow)' }} title="container restarts">
                  ↻ {node.restarts}
                </span>
              ) : null}
              <span
                title="workload health"
                style={{
                  color:
                    node.health === 'critical'
                      ? 'var(--rp-red)'
                      : node.health === 'degraded'
                        ? 'var(--rp-yellow)'
                        : 'var(--rp-ink-muted)',
                }}
              >
                {node.health === 'critical' ? '▲' : node.health === 'degraded' ? '◆' : '●'}{' '}
                {node.health}
              </span>
            </span>
          </>
        ) : null}
        <span className="h-4 w-px bg-line" aria-hidden />
        <Link
          href={`/clusters/${clusterId}/logs?workload=${encodeURIComponent(name)}`}
          className="rounded-skin-sm px-2.5 py-1 font-mono text-[11px] text-mid transition-colors hover:bg-hover hover:text-ink"
        >
          Logs →
        </Link>
        <Link
          href={`/clusters/${clusterId}/traces?service=${encodeURIComponent(name)}`}
          className="rounded-skin-sm px-2.5 py-1 font-mono text-[11px] text-mid transition-colors hover:bg-hover hover:text-ink"
        >
          Traces →
        </Link>
        <button
          type="button"
          onClick={onClear}
          className="px-1.5 py-1 transition-colors hover:opacity-80"
          aria-label="Selektion aufheben"
        >
          <kbd className="rounded-skin-chip border border-line px-1.5 py-0.5 font-mono text-[9.5px] text-faint">
            esc
          </kbd>
        </button>
      </div>
    </div>
  );
}

export function ServiceMapCanvas({
  orgId,
  clusterId,
  namespace = null,
  fill = false,
  className,
}: {
  orgId: string;
  clusterId: string;
  namespace?: string | null;
  /** fill: randlos, füllt den Elternbereich (Landing-Page). Sonst gerahmtes Panel. */
  fill?: boolean;
  className?: string;
}) {
  return (
    <div
      data-servicemap-root
      className={cn('relative w-full overflow-hidden', fill ? 'h-full' : 'rounded-skin', className)}
      style={{
        border: fill ? 'none' : '1px solid color-mix(in oklab, var(--rp-map-edge) 50%, transparent)',
        background: 'var(--rp-map-bg)',
        height: fill ? '100%' : 'min(72vh, 720px)',
      }}
    >
      {/* Cockpit-Vignette — neutral, fokussiert die Mitte (kein Farbstich) */}
      <div
        aria-hidden
        className="pointer-events-none absolute inset-0 z-0"
        style={{
          background:
            'radial-gradient(120% 100% at 50% 45%, transparent 55%, var(--rp-map-vignette) 100%)',
        }}
      />
      <ReactFlowProvider>
        <MapInner orgId={orgId} clusterId={clusterId} namespace={namespace} />
      </ReactFlowProvider>
    </div>
  );
}
