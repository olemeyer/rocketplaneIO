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

// Partikelanzahl: 1 … 4 (nur Liveness-Textur, bewusst zurückhaltend).
function particleCount(weight: number): number {
  return Math.round(1 + Math.min(1, Math.max(0, weight)) * 3);
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

  // ── FLOW (Liveness-Textur) — live-only, nicht im frozen/dimmed-Zustand ──
  const showParticles = !d.dimmed && !d.frozen;
  const baseN = particleCount(d.weight);
  const n = d.focused ? baseN + 2 : baseN;
  const dur = 2.2; // konstant — kodiert NICHTS
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
                  dur={`${dur}s`}
                  begin={`${(i * dur) / n}s`}
                  repeatCount="indefinite"
                  keyPoints="0;1"
                  keyTimes="0;1"
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
