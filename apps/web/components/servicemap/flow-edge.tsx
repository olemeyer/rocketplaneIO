'use client';

import { memo, useState } from 'react';
import { EdgeLabelRenderer, getBezierPath, useViewport, type EdgeProps } from '@xyflow/react';

// flow-edge.tsx — Kanal-Entkopplung (Design-Panel): die RAIL (Linie) ist der Held
// und trägt die MENGE (Breite = quell-normiertes Gewicht 0..1) — immer sichtbar,
// präziser Längenkanal. Kantenherkunft spricht über STRUKTUR, nicht Farbe:
// L7-Trace-Kanten (Request-Einsicht) durchgezogen, L4-Kanten (flow/conntrack —
// nur Verbindung, kein Request-Parsing) gestrichelt. Die PARTIKEL sind sekundäre
// Textur und tragen NUR Liveness; rote Partikel = Fehleranteil (errRate aus den
// Trace-Spans — der einzige Fehler-Kanal der Kante, keine Doppel-Kodierung auf
// der Rail). Hover zeigt die ECHTEN Werte in ihrer ECHTEN Einheit (req/s + err%
// + p95 bei trace · Bytes/s bei flow · conns bei conntrack) — ehrliche Kodierung,
// nichts wird zwischen Einheiten umgerechnet.

export interface FlowEdgeData {
  /** quell-normiertes Volumen 0..1 (trace: reqRate · flow: bytesRate · conntrack: connCount) */
  weight: number;
  edgeSource: 'trace' | 'flow' | 'conntrack';
  protocol?: string;
  reqRate?: number;
  errRate?: number;
  p95Ms?: number;
  bytesRate?: number;
  connCount: number;
  errorRatio?: number; // 0..1 Fehler-Traffic → Anteil roter Partikel
  focused?: boolean;
  dimmed?: boolean;
  frozen?: boolean; // historischer Modus → keine Partikel
  [key: string]: unknown;
}

function safeId(id: string): string {
  return 'fp-' + id.replace(/[^a-zA-Z0-9_-]/g, '-');
}

// Rail-Breite: 1.2px … 3px aus dem normierten Gewicht (Menge auf dem Längenkanal).
function railWidth(weight: number): number {
  return 1.2 + Math.min(1, Math.max(0, weight)) * 1.8;
}

// ── Partikel-Flussrate: 1 Request = 1 Partikel-Durchlauf ──
// Die Frequenz spiegelt die ECHTE Rate der Kante, beidseitig gecappt: unten,
// damit eine lebende Kante nie tot wirkt (alle ~7s ein Partikel), oben, damit
// High-Traffic-Kanten kein Partikel-Sturm werden (Dichte-Cap ≈ MAX_PARTICLES
// gleichzeitig). Die TRANSIT-Zeit bleibt konstant — Geschwindigkeit kodiert
// weiterhin nichts, nur die Frequenz spricht.
const TRANSIT_S = 2.2; // Sekunden über die Kante (konstant)
const MIN_PPS = 0.15; // Liveness-Boden
const MAX_PPS = 3; // Cap — mehr Requests/s werden nicht mehr einzeln gezeigt
const MAX_PARTICLES = 7;

// particleRate leitet die Partikel/Sekunde aus der ehrlichsten Mengen-Metrik
// der Kante ab: req/s bei Trace-Kanten; bei L4-Kanten gibt es keine Requests —
// dort dient das Volumen (Bytes bzw. conns) als grobe Aktivitäts-Textur.
function particleRate(d: FlowEdgeData): number {
  let rate: number;
  if (d.edgeSource === 'trace') {
    rate = d.reqRate ?? Math.max(d.connCount, 1) / 60; // Trace-Graph: calls als schwacher Proxy
  } else if (d.edgeSource === 'flow') {
    rate = (d.bytesRate ?? 0) / 1024; // 1 Partikel je KiB/s — reine Textur
  } else {
    rate = d.connCount / 10;
  }
  return Math.min(MAX_PPS, Math.max(MIN_PPS, rate));
}

function fmtRate(v: number): string {
  if (v >= 1000) return `${(v / 1000).toFixed(1)}k`;
  if (v >= 10) return v.toFixed(0);
  return v.toFixed(2);
}

function fmtBytes(v: number): string {
  if (v >= 1 << 20) return `${(v / (1 << 20)).toFixed(1)} MiB/s`;
  if (v >= 1 << 10) return `${(v / (1 << 10)).toFixed(1)} KiB/s`;
  return `${Math.round(v)} B/s`;
}

function fmtMs(v: number): string {
  if (v >= 1000) return `${(v / 1000).toFixed(2)}s`;
  return `${v >= 10 ? v.toFixed(0) : v.toFixed(1)}ms`;
}

function FlowEdgeImpl({
  id,
  sourceX,
  sourceY,
  targetX,
  targetY,
  sourcePosition,
  targetPosition,
  data,
}: EdgeProps) {
  const d = (data ?? { weight: 0, edgeSource: 'conntrack', connCount: 0 }) as FlowEdgeData;
  const [hovered, setHovered] = useState(false);
  // Tooltip in konstanter Lesegröße: EdgeLabelRenderer skaliert mit dem Map-Zoom,
  // der Zoom wird daher herausgerechnet (rausgezoomt wäre er sonst unlesbar).
  const { zoom } = useViewport();
  const [path, labelX, labelY] = getBezierPath({
    sourceX,
    sourceY,
    sourcePosition,
    targetX,
    targetY,
    targetPosition,
    curvature: 0.28,
  });

  const pid = safeId(id);
  const isL4 = d.edgeSource !== 'trace';

  // ── RAIL (der Held) ── Fokus betont, aber gemessen (keine Neon-Röhre)
  const railColor = d.focused ? 'var(--rp-accent)' : 'var(--rp-map-edge)';
  const railOpacity = d.dimmed ? 0.08 : d.focused || hovered ? 0.7 : 0.5;
  const railW = railWidth(d.weight);

  // ── FLOW (Fluss-Textur) — live-only, nicht im frozen/dimmed-Zustand ──
  // Frequenz = echte Rate (gecappt). n Slots im Abstand 1/pps; cycle streckt
  // sich bei seltenen Requests über die Transit-Zeit hinaus — der Partikel
  // parkt die Totzeit UNSICHTBAR unter dem Ziel-Node (keyPoints 0;1;1), statt
  // künstlich langsamer zu fliegen (Speed bleibt konstant, kodiert nichts).
  const showParticles = !d.dimmed && !d.frozen;
  const pps = particleRate(d);
  const n = Math.max(1, Math.min(MAX_PARTICLES, Math.round(pps * TRANSIT_S)));
  const cycle = Math.max(TRANSIT_S, n / pps);
  const travelFrac = Math.min(1, TRANSIT_S / cycle);
  const errN = Math.round(n * Math.min(1, Math.max(0, d.errorRatio ?? 0)));
  const dotColor = d.focused ? 'var(--rp-accent)' : 'var(--rp-map-particle)';
  const dotOpacity = d.focused ? 0.95 : 0.6;
  const r = 1.5;

  const errPct = (d.errRate ?? d.errorRatio ?? 0) * 100;
  const hasErr = errPct >= 0.05;

  return (
    <g
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
      style={{ pointerEvents: 'all' }}
    >
      {/* unsichtbare Hit-Fläche — 12px, damit die 1–3px-Rail hoverbar ist */}
      <path d={path} fill="none" stroke="transparent" strokeWidth={12} />

      <path
        id={pid}
        d={path}
        fill="none"
        stroke={railColor}
        strokeWidth={railW}
        strokeOpacity={railOpacity}
        strokeLinecap={isL4 ? 'butt' : 'round'}
        strokeDasharray={isL4 ? '4 3' : undefined}
      />

      {/* statischer Richtungs-Chevron bei ~Mitte (auch bei reduced-motion / frozen) */}
      {!d.dimmed ? (
        <Chevron
          x={labelX}
          y={labelY}
          angle={Math.atan2(targetY - sourceY, targetX - sourceX)}
          color={railColor}
          opacity={d.focused ? 0.9 : 0.5}
        />
      ) : null}

      {showParticles
        ? Array.from({ length: n }).map((_, i) => {
            const isErr = i < errN;
            return (
              <circle key={i} r={r} fill={isErr ? 'var(--rp-red)' : dotColor} opacity={dotOpacity}>
                <animateMotion
                  dur={`${cycle.toFixed(2)}s`}
                  begin={`${(i / pps).toFixed(2)}s`}
                  repeatCount="indefinite"
                  keyPoints={travelFrac >= 0.999 ? '0;1' : '0;1;1'}
                  keyTimes={travelFrac >= 0.999 ? '0;1' : `0;${travelFrac.toFixed(3)};1`}
                  calcMode="linear"
                >
                  <mpath href={`#${pid}`} />
                </animateMotion>
              </circle>
            );
          })
        : null}

      {/* ── Edge-Hover: die echten Werte in der echten Einheit ── */}
      {hovered && !d.dimmed ? (
        <EdgeLabelRenderer>
          <div
            style={{
              position: 'absolute',
              transform: `translate(${labelX}px, ${labelY}px) scale(${1 / Math.max(0.2, zoom)}) translate(-50%, -130%)`,
              transformOrigin: '0 0',
              pointerEvents: 'none',
              zIndex: 20,
            }}
            className="flex items-center gap-2 whitespace-nowrap border border-[var(--rp-line-strong)] bg-[var(--rp-bg-raised)] px-2 py-1 font-mono text-[10px] leading-none shadow-sm"
          >
            {d.protocol ? (
              <span className="uppercase text-[var(--rp-ink-muted)]">{d.protocol}</span>
            ) : null}
            {d.edgeSource === 'trace' ? (
              <>
                {/* Trace-Graph liefert calls statt Raten — nur zeigen, was da ist */}
                <span className="text-[var(--rp-ink)] [font-variant-numeric:tabular-nums]">
                  {d.reqRate !== undefined ? (
                    <>
                      {fmtRate(d.reqRate)} <span className="text-[var(--rp-ink-muted)]">req/s</span>
                    </>
                  ) : (
                    <>
                      {d.connCount} <span className="text-[var(--rp-ink-muted)]">calls</span>
                    </>
                  )}
                </span>
                <span
                  className="[font-variant-numeric:tabular-nums]"
                  style={{ color: hasErr ? 'var(--rp-red)' : 'var(--rp-ink-muted)' }}
                >
                  {hasErr ? '▲ ' : ''}
                  {errPct >= 10 ? errPct.toFixed(0) : errPct.toFixed(1)}%
                </span>
                {d.p95Ms !== undefined ? (
                  <span className="text-[var(--rp-ink)] [font-variant-numeric:tabular-nums]">
                    p95 {fmtMs(d.p95Ms)}
                  </span>
                ) : null}
              </>
            ) : d.edgeSource === 'flow' ? (
              <span className="text-[var(--rp-ink)] [font-variant-numeric:tabular-nums]">
                {fmtBytes(d.bytesRate ?? 0)}
              </span>
            ) : (
              <span className="text-[var(--rp-ink)] [font-variant-numeric:tabular-nums]">
                {d.connCount} <span className="text-[var(--rp-ink-muted)]">conns</span>
              </span>
            )}
            {/* L4-Hinweis: Verbindung ohne Request-Einsicht (ehrlich gated) */}
            {d.edgeSource !== 'trace' ? (
              <span className="text-[var(--rp-ink-faint)]">L4</span>
            ) : null}
          </div>
        </EdgeLabelRenderer>
      ) : null}
    </g>
  );
}

// kleiner gefüllter Richtungs-Chevron, entlang der Kantenrichtung gedreht.
function Chevron({
  x,
  y,
  angle,
  color,
  opacity,
}: {
  x: number;
  y: number;
  angle: number;
  color: string;
  opacity: number;
}) {
  const deg = (angle * 180) / Math.PI;
  return (
    <g transform={`translate(${x} ${y}) rotate(${deg})`} opacity={opacity}>
      <path d="M -2 -3 L 3 0 L -2 3" fill="none" stroke={color} strokeWidth={1.4} strokeLinecap="round" strokeLinejoin="round" />
    </g>
  );
}

export const FlowEdge = memo(FlowEdgeImpl);
