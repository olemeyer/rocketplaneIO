'use client';

import { memo } from 'react';
import { getBezierPath, type EdgeProps } from '@xyflow/react';

// flow-edge.tsx — Kanal-Entkopplung (Design-Panel): die RAIL (Linie) ist der Held und
// trägt die MENGE (Breite = log(connCount)) — immer sichtbar, präziser Längenkanal.
// Die PARTIKEL sind nur sekundäre Textur und tragen NUR Liveness ("lebt es jetzt"):
// klein, leise, wenige, konstante Geschwindigkeit (Speed kodiert nichts — conntrack
// kennt keine Latenz). Rote Partikel = Fehler-Traffic (erst mit OTel). Bei Fokus wird
// die Rail betont und die Partikel promotet; nicht-fokussierte Kanten dimmen ganz weg.

export interface FlowEdgeData {
  connCount: number;
  maxCount: number;
  errorRatio?: number; // 0..1 Fehler-Traffic (später aus OTel)
  focused?: boolean;
  dimmed?: boolean;
  frozen?: boolean; // historischer Modus → keine Partikel
  [key: string]: unknown;
}

function safeId(id: string): string {
  return 'fp-' + id.replace(/[^a-zA-Z0-9_-]/g, '-');
}

// Rail-Breite: 1px … 3px (Menge auf dem Positions-/Längenkanal, nicht auf Dichte).
function railWidth(count: number, max: number): number {
  if (max <= 1) return 1.5;
  const t = Math.log(1 + count) / Math.log(1 + max);
  return 1.2 + t * 1.8;
}

// Partikelanzahl: 1 … 4 (nur Liveness-Textur, bewusst zurückhaltend).
function particleCount(count: number, max: number): number {
  if (max <= 1) return 2;
  const t = Math.log(1 + count) / Math.log(1 + max);
  return Math.round(1 + t * 3);
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
  const d = (data ?? { connCount: 0, maxCount: 1 }) as FlowEdgeData;
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
  const w = railWidth(d.connCount, d.maxCount);

  // ── RAIL (der Held) ── Fokus betont, aber gemessen (keine Neon-Röhre)
  const railColor = d.focused ? 'var(--rp-accent)' : 'var(--rp-map-edge)';
  const railOpacity = d.dimmed ? 0.08 : d.focused ? 0.65 : 0.5;
  const railW = w; // Fokus spricht über Farbe/Partikel, nicht über Masse

  // ── FLOW (Liveness-Textur) — live-only, nicht im frozen/dimmed-Zustand ──
  const showParticles = !d.dimmed && !d.frozen;
  const baseN = particleCount(d.connCount, d.maxCount);
  const n = d.focused ? baseN + 2 : baseN;
  const dur = 2.2; // konstant — kodiert NICHTS
  const errN = Math.round(n * Math.min(1, Math.max(0, d.errorRatio ?? 0)));
  const dotColor = d.focused ? 'var(--rp-accent)' : 'var(--rp-map-particle)';
  const dotOpacity = d.focused ? 0.95 : 0.6;
  const r = 1.5;

  return (
    <g>
      <path
        id={pid}
        d={path}
        fill="none"
        stroke={railColor}
        strokeWidth={railW}
        strokeOpacity={railOpacity}
        strokeLinecap="round"
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
