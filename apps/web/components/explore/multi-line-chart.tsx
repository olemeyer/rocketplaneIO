import { brand, signal } from '@rocketplane/ui';
import type { MetricSeries } from '@/lib/api';

const PALETTE = [signal.metrics, brand.via, signal.traces, signal.rum, signal.synthetics, '#34d399', '#f472b6', '#a3e635'];

export function colorForIndex(i: number): string {
  return PALETTE[i % PALETTE.length]!;
}

// MultiLineChart rendert mehrere Zeitreihen als überlagerte SVG-Linien mit Legende.
export function MultiLineChart({ series, unit = '', height = 240 }: { series: MetricSeries[]; unit?: string; height?: number }) {
  const W = 720;
  const H = height;
  const padX = 8;
  const padY = 12;

  const all = series.flatMap((s) => s.points.map((p) => p.v));
  const max = Math.max(1e-9, ...all);
  const min = Math.min(0, ...all);
  const range = max - min || 1;
  const len = Math.max(...series.map((s) => s.points.length), 1);

  const xy = (i: number, v: number): [number, number] => {
    const x = len <= 1 ? W / 2 : padX + (i / (len - 1)) * (W - 2 * padX);
    const y = H - padY - ((v - min) / range) * (H - 2 * padY);
    return [x, y];
  };

  const fmt = (v: number) => (v >= 1000 ? `${(v / 1000).toFixed(1)}k` : v >= 10 ? Math.round(v).toString() : v.toFixed(2));

  return (
    <div>
      <svg viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" className="h-60 w-full" role="img" aria-label="metric chart">
        {/* Gridlinien */}
        {[0.25, 0.5, 0.75].map((f) => (
          <line key={f} x1={0} x2={W} y1={padY + f * (H - 2 * padY)} y2={padY + f * (H - 2 * padY)} stroke="var(--rp-border)" strokeOpacity={0.5} strokeWidth={0.5} />
        ))}
        {series.map((s, si) => {
          const color = colorForIndex(si);
          const d = s.points.map((p, i) => `${i === 0 ? 'M' : 'L'} ${xy(i, p.v).map((n) => n.toFixed(1)).join(' ')}`).join(' ');
          return <path key={s.label || si} d={d} fill="none" stroke={color} strokeWidth={1.6} vectorEffect="non-scaling-stroke" />;
        })}
        {/* Y-Achsen-Labels */}
        <text x={4} y={padY + 8} fill="var(--rp-text-faint)" fontSize="10" fontFamily="var(--rp-font-mono)">
          {fmt(max)}
          {unit}
        </text>
        <text x={4} y={H - padY} fill="var(--rp-text-faint)" fontSize="10" fontFamily="var(--rp-font-mono)">
          {fmt(min)}
        </text>
      </svg>
      {series.length > 1 && (
        <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1">
          {series.map((s, si) => (
            <span key={s.label || si} className="flex items-center gap-1.5 font-mono text-[11px] text-muted">
              <span className="h-2 w-2 rounded-full" style={{ background: colorForIndex(si) }} />
              {s.label || 'all'}
            </span>
          ))}
        </div>
      )}
    </div>
  );
}
