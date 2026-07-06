'use client';

import { useMemo, useState } from 'react';
import type { MetricSeries } from '@/lib/api/types';
import { SERVICE_PALETTE } from '@/components/traces/service-color';

// line-chart.tsx — das RETICLE-Zeitreihen-Instrument: 1px-Hairline-Grid,
// präzise Mono-Achsen, Serien in der nicht-semantischen Palette (Status-
// Serien wie error/warn dürfen explizit crimson/gold tragen), Hover-
// Crosshair mit Legende. Bewusst eigenes SVG statt Chart-Lib — jede Linie
// gehorcht den Design-Gesetzen.

const H = 168;
const PAD_L = 46;
const PAD_R = 10;
const PAD_T = 10;
const PAD_B = 20;

// feste Farben für Status-Serien; sonst Palette in Serienreihenfolge
const STATUS_COLORS: Record<string, string> = {
  error: 'var(--rp-red)',
  warn: 'var(--rp-yellow)',
  info: 'var(--rp-ink-muted)',
};

export function LineChart({
  title,
  unit,
  series,
  height = H,
  threshold,
  formatValue = (v: number) => (v >= 100 ? Math.round(v).toString() : v.toFixed(1)),
}: {
  title: string;
  unit: string;
  series: MetricSeries[];
  height?: number;
  /** Alert-Threshold: gestrichelte crimson-Linie im Chart */
  threshold?: number;
  formatValue?: (v: number) => string;
}) {
  const [hoverX, setHoverX] = useState<number | null>(null);
  const W = 560; // viewBox-Breite; skaliert responsiv

  const { tMin, tMax, vMax } = useMemo(() => {
    let tMin = Infinity;
    let tMax = -Infinity;
    let vMax = 0;
    for (const s of series) {
      for (const p of s.points) {
        if (p.t < tMin) tMin = p.t;
        if (p.t > tMax) tMax = p.t;
        if (p.v > vMax) vMax = p.v;
      }
    }
    if (!isFinite(tMin)) {
      tMin = Date.now() - 900_000;
      tMax = Date.now();
    }
    if (threshold !== undefined && threshold > vMax) vMax = threshold;
    if (vMax <= 0) vMax = 1;
    return { tMin, tMax, vMax: vMax * 1.12 };
  }, [series, threshold]);

  const x = (t: number) => PAD_L + ((t - tMin) / Math.max(1, tMax - tMin)) * (W - PAD_L - PAD_R);
  const y = (v: number) => PAD_T + (1 - v / vMax) * (height - PAD_T - PAD_B);

  const colorOf = (name: string, i: number) =>
    STATUS_COLORS[name] ?? SERVICE_PALETTE[i % SERVICE_PALETTE.length] ?? 'var(--rp-ink-mid)';

  // Hover: nächster Zeitpunkt über alle Serien
  const hover = useMemo(() => {
    if (hoverX === null) return null;
    const t = tMin + ((hoverX - PAD_L) / Math.max(1, W - PAD_L - PAD_R)) * (tMax - tMin);
    const vals: { name: string; v: number; color: string }[] = [];
    let ts = 0;
    series.forEach((s, i) => {
      let best: { t: number; v: number } | null = null;
      for (const p of s.points) {
        if (!best || Math.abs(p.t - t) < Math.abs(best.t - t)) best = p;
      }
      if (best) {
        vals.push({ name: s.name, v: best.v, color: colorOf(s.name, i) });
        ts = best.t;
      }
    });
    return vals.length ? { ts, vals, px: x(ts) } : null;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hoverX, series, tMin, tMax]);

  const yTicks = [0.25, 0.5, 0.75, 1].map((f) => vMax * f);
  const fmtT = (t: number) => new Date(t).toISOString().slice(11, 16);

  return (
    <div className="rounded-skin border border-line bg-raised p-3" style={{ boxShadow: 'var(--rp-rim)' }}>
      <div className="flex items-baseline justify-between">
        <span className="rp-micro !text-[10px]">{title}</span>
        <span className="font-mono text-[9.5px] text-faint">{unit}</span>
      </div>
      <svg
        viewBox={`0 0 ${W} ${height}`}
        className="mt-1 w-full"
        onMouseMove={(e) => {
          const r = e.currentTarget.getBoundingClientRect();
          setHoverX(((e.clientX - r.left) / r.width) * W);
        }}
        onMouseLeave={() => setHoverX(null)}
      >
        {/* Hairline-Grid + Y-Werte */}
        {yTicks.map((v) => (
          <g key={v}>
            <line x1={PAD_L} x2={W - PAD_R} y1={y(v)} y2={y(v)} stroke="var(--rp-line)" strokeWidth="1" />
            <text x={PAD_L - 6} y={y(v) + 3} textAnchor="end" fontSize="8.5" fill="var(--rp-ink-muted)" fontFamily="monospace">
              {formatValue(v)}
            </text>
          </g>
        ))}
        {/* X-Achse */}
        <line x1={PAD_L} x2={W - PAD_R} y1={height - PAD_B} y2={height - PAD_B} stroke="var(--rp-line-strong)" strokeWidth="1" />
        <text x={PAD_L} y={height - 6} fontSize="8.5" fill="var(--rp-ink-muted)" fontFamily="monospace">
          {fmtT(tMin)}
        </text>
        <text x={W - PAD_R} y={height - 6} textAnchor="end" fontSize="8.5" fill="var(--rp-ink-muted)" fontFamily="monospace">
          {fmtT(tMax)}
        </text>

        {/* Serien */}
        {series.map((s, i) => {
          const c = colorOf(s.name, i);
          const d = s.points.map((p, j) => `${j === 0 ? 'M' : 'L'}${x(p.t).toFixed(1)},${y(p.v).toFixed(1)}`).join(' ');
          return (
            <g key={s.name}>
              <path d={d} fill="none" stroke={c} strokeWidth="1.5" strokeLinejoin="round" opacity={0.9} />
            </g>
          );
        })}

        {/* Alert-Threshold */}
        {threshold !== undefined ? (
          <g>
            <line x1={PAD_L} x2={W - PAD_R} y1={y(threshold)} y2={y(threshold)} stroke="var(--rp-red)" strokeWidth="1" strokeDasharray="4 3" opacity={0.75} />
            <text x={W - PAD_R} y={y(threshold) - 3} textAnchor="end" fontSize="8" fill="var(--rp-red)" fontFamily="monospace">
              ▲ {formatValue(threshold)}
            </text>
          </g>
        ) : null}

        {/* Crosshair */}
        {hover ? (
          <line x1={hover.px} x2={hover.px} y1={PAD_T} y2={height - PAD_B} stroke="var(--rp-ink-muted)" strokeWidth="1" strokeDasharray="2 3" />
        ) : null}
      </svg>

      {/* Legende / Hover-Werte */}
      <div className="mt-1 flex min-h-[16px] flex-wrap items-center gap-x-3 gap-y-0.5">
        {(hover?.vals ?? series.map((s, i) => ({ name: s.name, v: s.points.at(-1)?.v ?? 0, color: colorOf(s.name, i) })))
          .slice(0, 8)
          .map((v) => (
            <span key={v.name} className="flex items-center gap-1 font-mono text-[9.5px] text-muted tnum">
              <span className="h-[6px] w-[6px] rounded-full" style={{ background: v.color }} />
              {v.name} <span className="text-ink">{formatValue(v.v)}</span>
            </span>
          ))}
        {hover ? (
          <span className="ml-auto font-mono text-[9px] text-faint tnum">{fmtT(hover.ts)} UTC</span>
        ) : null}
      </div>
    </div>
  );
}
