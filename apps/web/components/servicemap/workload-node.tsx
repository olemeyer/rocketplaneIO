'use client';

import { memo } from 'react';
import { Handle, Position, type NodeProps } from '@xyflow/react';
import { getTechIcon } from '@/lib/tech-icons';
import type { WorkloadHealth } from '@/lib/api/types';

// workload-node.tsx — der Node als präzise INSTRUMENTEN-KACHEL (RETICLE), kein
// Blob: fester Radius, 1px-Hairline, Rim-Light oben, dezenter Gehäuse-Verlauf.
// Statt eines konkurrierenden Kreisrings zeigt eine LED-SEGMENTLEISTE unten in
// der Kachel die Replicas (ready gefüllt, fehlende glimmen rot) — Bauhaus-
// Geometrie, Instrumenten-Vokabular. Zentrale Zahl = conns-Gauge (ehrlich, ohne
// /s), Severity-Glyph als Colorblind-Redundanz, Restart-Badge oben rechts.

export interface WorkloadNodeData {
  name: string;
  namespace: string;
  kind: string;
  health: WorkloadHealth;
  podsReady: number;
  podsTotal: number;
  restarts: number;
  metric: number; // conns (Gauge) — bzw. calls im Trace-Graph
  caption?: string; // Beschriftung der zentralen Zahl (default „conns")
  /** simple-icons slug (Auto-Erkennung aus dem Image oder manueller Override) */
  icon?: string | null;
  size: number;
  dimmed?: boolean;
  focused?: boolean;
  /** Live-Action am Node: running = rotierender Ring, done/failed = Ergebnis-Puls. */
  action?: { kind: string; status: 'pending' | 'running' | 'succeeded' | 'failed' | 'cancelled'; detail?: string };
  [key: string]: unknown;
}

// Gehäuse je Health — Dark-Cockpit: gesund bleibt Graphit, nur Anomalien tragen Farbe.
const HEALTH_STYLE: Record<
  WorkloadHealth,
  { top: string; bottom: string; edge: string; fg: string; sub: string }
> = {
  healthy: {
    top: 'color-mix(in oklab, var(--rp-node-ok) 92%, white)',
    bottom: 'var(--rp-node-ok)',
    edge: 'var(--rp-line-strong)',
    fg: 'var(--rp-ink)',
    sub: 'var(--rp-ink-muted)',
  },
  degraded: {
    top: 'color-mix(in oklab, var(--rp-node-warn) 90%, white)',
    bottom: 'var(--rp-node-warn)',
    edge: 'var(--rp-yellow)',
    fg: '#ffffff',
    sub: 'rgba(255,255,255,0.75)',
  },
  critical: {
    top: 'color-mix(in oklab, var(--rp-node-crit) 90%, white)',
    bottom: 'var(--rp-node-crit)',
    edge: 'var(--rp-red)',
    fg: '#ffffff',
    sub: 'rgba(255,255,255,0.8)',
  },
  unknown: {
    top: 'var(--rp-node-idle)',
    bottom: 'var(--rp-node-idle)',
    edge: 'var(--rp-line)',
    fg: 'var(--rp-ink-mid)',
    sub: 'var(--rp-ink-faint)',
  },
};

function fmtMetric(n: number): string {
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(1).replace(/\.0$/, '') + 'M';
  if (n >= 1_000) return (n / 1_000).toFixed(1).replace(/\.0$/, '') + 'K';
  return String(n);
}

// LED-Segmentleiste: ready-Segmente gefüllt, fehlende glimmen rot. Bei vielen
// Replicas (>10) eine kontinuierliche Ratio-Bar. Gesund + vollzählig = still
// (dezente Leiste), damit nur Anomalien sprechen.
function ReplicaBar({
  ready,
  total,
  width,
  health,
}: {
  ready: number;
  total: number;
  width: number;
  health: WorkloadHealth;
}) {
  // healthy = leises GRÜN (Status-Skala: ok darf grün sein — schlank, 3px);
  // fehlende Replicas glimmen rot.
  const ok =
    health === 'healthy'
      ? 'color-mix(in oklab, var(--rp-green) 72%, transparent)'
      : 'color-mix(in oklab, var(--rp-map-particle) 55%, transparent)';
  const missing = 'var(--rp-red)';
  if (total <= 0) return null;
  if (total > 10) {
    const frac = Math.max(0, Math.min(1, ready / total));
    return (
      <div className="absolute bottom-1.5 left-1/2 h-[3px] -translate-x-1/2 overflow-hidden rounded-full" style={{ width }}>
        <div className="absolute inset-0" style={{ background: frac < 1 ? missing : 'transparent', opacity: 0.55 }} />
        <div className="absolute inset-y-0 left-0 rounded-full" style={{ width: `${frac * 100}%`, background: ok }} />
      </div>
    );
  }
  const gap = 2;
  const seg = Math.max(3, (width - gap * (total - 1)) / total);
  return (
    <div className="absolute bottom-1.5 left-1/2 flex -translate-x-1/2" style={{ gap, width }} aria-hidden>
      {Array.from({ length: total }).map((_, i) => (
        <span
          key={i}
          className="h-[3px] rounded-full"
          style={{
            width: seg,
            background: i < ready ? ok : missing,
            boxShadow: i >= ready ? '0 0 5px var(--rp-red)' : undefined,
          }}
        />
      ))}
    </div>
  );
}

// Severity-Glyph — Colorblind-Redundanz (Form trägt Health ohne Farbe).
function SeverityGlyph({ health, size }: { health: WorkloadHealth; size: number }) {
  if (health === 'healthy') return null;
  const s = Math.max(9, Math.round(size * 0.15));
  const color =
    health === 'critical' ? 'var(--rp-red)' : health === 'degraded' ? 'var(--rp-yellow)' : 'var(--rp-ink-faint)';
  return (
    <span
      className="absolute bottom-1 right-1 flex items-center justify-center rounded-full"
      style={{ background: 'var(--rp-map-bg)', padding: 2 }}
    >
      {health === 'critical' ? (
        <svg width={s} height={s} viewBox="0 0 12 12" aria-hidden>
          <path d="M4 1h4l3 3v4l-3 3H4L1 8V4L4 1z" fill={color} />
        </svg>
      ) : health === 'degraded' ? (
        <svg width={s} height={s} viewBox="0 0 12 12" aria-hidden>
          <path d="M6 1.5 11 10.5H1L6 1.5z" fill={color} opacity={0.9} />
        </svg>
      ) : (
        <svg width={s} height={s} viewBox="0 0 12 12" aria-hidden>
          <circle cx="6" cy="6" r="4.5" fill="none" stroke={color} strokeWidth="1.4" strokeDasharray="2 2" />
        </svg>
      )}
    </span>
  );
}

function WorkloadNodeImpl({ data, selected }: NodeProps) {
  const d = data as WorkloadNodeData;
  const s = HEALTH_STYLE[d.health];
  const size = d.size;
  const icon = getTechIcon(d.icon);
  const radius = Math.max(10, Math.round(size * 0.17)); // Kachel, kein Kreis
  const showCaption = size >= 56;
  const barWidth = Math.round(size * 0.5);

  return (
    <div
      className="relative flex items-center justify-center transition-opacity duration-200"
      style={{ width: size, height: size, opacity: d.dimmed ? 0.4 : 1 }}
      title={`${d.name} — ${d.podsTotal > 0 ? `${d.podsReady}/${d.podsTotal} pods ready · ` : ''}${d.metric} ${d.caption ?? 'conns'}${d.restarts ? ` · ${d.restarts} restarts` : ''}`}
    >
      <Handle type="target" position={Position.Left} className="!h-1 !w-1 !border-0 !bg-transparent" />
      <Handle type="source" position={Position.Right} className="!h-1 !w-1 !border-0 !bg-transparent" />

      {/* DER PASSER — Signal-Fadenkreuz, rastet auf die Selektion ein */}
      {selected ? <Reticle size={size} /> : null}

      {/* LIVE-ACTION — rotierender Arbeitsring (running) bzw. Ergebnis-Puls */}
      {d.action ? <ActionRing size={size} radius={radius} action={d.action} /> : null}

      {/* Instrumenten-Kachel */}
      <div
        className="relative flex flex-col items-center justify-center"
        style={{
          width: size,
          height: size,
          borderRadius: radius,
          background: `linear-gradient(180deg, ${s.top} 0%, ${s.bottom} 38%)`,
          border: `1px solid color-mix(in oklab, ${s.edge} 45%, transparent)`,
          boxShadow: selected
            ? 'inset 0 1px 0 rgba(255,255,255,0.08), var(--rp-glow-accent)'
            : d.health === 'critical'
              ? 'inset 0 1px 0 rgba(255,255,255,0.1), 0 0 22px -4px color-mix(in oklab, var(--rp-red) 60%, transparent)'
              : d.focused
                ? `inset 0 1px 0 rgba(255,255,255,0.08), 0 0 0 1px color-mix(in oklab, ${s.edge} 55%, transparent)`
                : 'inset 0 1px 0 rgba(255,255,255,0.06), 0 8px 20px -12px rgba(0,0,0,0.8)',
        }}
      >
        {icon ? (
          /* TECH-IDENTITÄT: monochromes Logo in Gehäuse-Tinte — Status spricht
             über Kachel/LED, nie über das Logo (RETICLE №4: ein Kanal je Info) */
          <svg
            width={Math.round(size * 0.44)}
            height={Math.round(size * 0.44)}
            viewBox="0 0 24 24"
            aria-label={icon.title}
            role="img"
          >
            <path d={icon.path} fill={s.fg} opacity={d.health === 'healthy' ? 0.82 : 0.95} />
          </svg>
        ) : (
          <>
            <span
              className="font-mono font-semibold leading-none tnum"
              style={{
                // idle (0 conns) flüstert — nur aktive Kacheln sprechen laut
                color:
                  d.metric === 0 && d.health === 'healthy'
                    ? `color-mix(in oklab, ${s.fg} 62%, transparent)`
                    : s.fg,
                fontSize: Math.max(11, Math.round(size * 0.21)),
              }}
            >
              {fmtMetric(d.metric)}
            </span>
            {showCaption ? (
              <span
                className="mt-1 font-mono uppercase leading-none tracking-[0.1em]"
                style={{ color: s.sub, fontSize: Math.max(7, Math.round(size * 0.09)) }}
              >
                {d.caption ?? 'conns'}
              </span>
            ) : null}
          </>
        )}

        <ReplicaBar ready={d.podsReady} total={d.podsTotal} width={barWidth} health={d.health} />
      </div>

      {d.restarts > 0 ? (
        <span
          className="absolute -right-1.5 -top-1.5 z-10 flex h-4 min-w-4 items-center justify-center rounded-skin-chip px-1 font-mono text-[9px] font-bold leading-none tnum"
          style={{
            // Schwellenbasiert: nur wirkliche Krisen tragen Rot (Dark-Cockpit) —
            // vereinzelte Restarts bleiben neutral, erhöhte gold.
            ...(d.health === 'critical' || d.restarts >= 10
              ? { background: 'var(--rp-red)', color: '#fff' }
              : d.restarts >= 3
                ? { background: 'var(--rp-tone-yellow-bg)', color: 'var(--rp-tone-yellow-fg)' }
                : { background: 'var(--rp-tone-neutral-bg)', color: 'var(--rp-tone-neutral-fg)' }),
            boxShadow: '0 0 0 2px var(--rp-map-bg)',
          }}
          title={`${d.restarts} restarts`}
        >
          ↻{d.restarts}
        </span>
      ) : null}

      <SeverityGlyph health={d.health} size={size} />

      {/* Name darunter */}
      <div
        className="pointer-events-none absolute left-1/2 top-full mt-2 -translate-x-1/2 text-center"
        style={{ width: 144 }}
      >
        <div className="truncate text-[11px] font-medium leading-tight text-ink" title={d.name}>
          {d.name}
        </div>
        <div className="truncate font-mono text-[10px] leading-tight text-muted" title={d.namespace}>
          {d.namespace} · {d.kind}
        </div>
        {icon && d.metric > 0 ? (
          <div className="font-mono text-[9.5px] leading-tight text-faint tnum">
            {fmtMetric(d.metric)} {d.caption ?? 'conns'}
          </div>
        ) : null}
        {d.action ? <ActionCaption action={d.action} /> : null}
      </div>
    </div>
  );
}

// Verb je Action-Kind (Badge unter dem Node + Ring-Tooltip).
const ACTION_VERB: Record<string, string> = {
  rollout_restart: 'restarting',
  scale: 'scaling',
  delete_pod: 'recreating pod',
};

// ActionRing: die Kachel bekommt einen ARBEITSRING knapp außerhalb der Kante —
// running: rotierendes Strichsegment (neutral ink — Arbeit ist kein Status);
// succeeded: geschlossener grüner Ring, der einmal aufatmet; failed: crimson.
function ActionRing({
  size,
  radius,
  action,
}: {
  size: number;
  radius: number;
  action: NonNullable<WorkloadNodeData['action']>;
}) {
  const off = 5;
  const box = size + off * 2;
  const r = radius + off - 1;
  const running = action.status === 'running' || action.status === 'pending';
  const color = running
    ? 'var(--rp-ink-mid)'
    : action.status === 'succeeded'
      ? 'var(--rp-green)'
      : action.status === 'cancelled'
        ? 'var(--rp-yellow)'
        : 'var(--rp-red)';
  return (
    <svg
      className="pointer-events-none absolute"
      width={box}
      height={box}
      style={{ left: -off, top: -off }}
      aria-hidden
    >
      <g style={running ? { transformOrigin: '50% 50%', animation: 'rp-rotate 1.6s linear infinite' } : undefined}>
        <rect
          x="1"
          y="1"
          width={box - 2}
          height={box - 2}
          rx={r}
          fill="none"
          stroke={color}
          strokeWidth="1.5"
          strokeLinecap="round"
          strokeDasharray={running ? `${box * 0.55} ${box * 0.75}` : undefined}
          style={
            !running
              ? {
                  transformOrigin: '50% 50%',
                  animation: 'rp-ring-settle 900ms var(--rp-ease-precise) both',
                }
              : undefined
          }
          opacity={running ? 0.9 : 0.8}
        />
      </g>
    </svg>
  );
}

// ActionCaption: die Verb-Zeile unterm Namen — running atmet, Ergebnis steht.
function ActionCaption({ action }: { action: NonNullable<WorkloadNodeData['action']> }) {
  const running = action.status === 'running' || action.status === 'pending';
  const color = running
    ? 'var(--rp-ink-mid)'
    : action.status === 'succeeded'
      ? 'var(--rp-tone-green-fg)'
      : action.status === 'cancelled'
        ? 'var(--rp-tone-yellow-fg)'
        : 'var(--rp-tone-red-fg)';
  const verb = ACTION_VERB[action.kind] ?? action.kind;
  const label = running
    ? `${verb}${action.detail ? ` ${action.detail}` : ''}…`
    : action.status === 'succeeded'
      ? `✓ ${verb.replace(/ing\b/, 'ed').replace('recreated pod', 'pod recreated')}`
      : action.status === 'cancelled'
        ? `⊘ ${verb} cancelled`
        : `▲ ${verb} failed`;
  return (
    <div
      className={`mt-0.5 truncate font-mono text-[9.5px] leading-tight ${running ? 'rp-breath' : ''}`}
      style={{ color }}
      title={label}
    >
      {label}
    </div>
  );
}

// Der Passer: vier 1px-Eckwinkel in Signal-Orange, ~9px außerhalb der Kachel.
function Reticle({ size }: { size: number }) {
  const off = 9;
  const arm = Math.max(10, Math.round(size * 0.16));
  const box = size + off * 2;
  const c = 'var(--rp-accent)';
  return (
    <svg
      className="pointer-events-none absolute"
      width={box}
      height={box}
      style={{ left: -off, top: -off, animation: 'reveal-up 120ms var(--rp-ease-precise)' }}
      aria-hidden
    >
      <g stroke={c} strokeWidth="1.25" fill="none">
        <path d={`M0.5 ${arm} V0.5 H${arm}`} />
        <path d={`M${box - arm} 0.5 H${box - 0.5} V${arm}`} />
        <path d={`M${box - 0.5} ${box - arm} V${box - 0.5} H${box - arm}`} />
        <path d={`M${arm} ${box - 0.5} H0.5 V${box - arm}`} />
      </g>
    </svg>
  );
}

export const WorkloadNode = memo(WorkloadNodeImpl);

// Node-Größe aus Traffic-Rang (0..1) → 52…96 px.
export function nodeSize(t: number): number {
  return Math.round(52 + Math.min(1, Math.max(0, t)) * 44);
}
export const NODE_BASE = 64;
