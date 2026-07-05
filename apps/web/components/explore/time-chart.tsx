import type { Point } from '@/lib/api';

// TimeChart rendert eine kompakte SVG-Linien-/Flächen-Zeitreihe (viewBox-basiert,
// responsiv). Ohne Achsen — als Panel-Chart im nordischen Stil.
export function TimeChart({
  points,
  color,
  label,
  value,
  unit = '',
  height = 64,
}: {
  points: Point[];
  color: string;
  label: string;
  value?: string;
  unit?: string;
  height?: number;
}) {
  const W = 300;
  const H = height;
  const pad = 4;

  const vals = points.map((p) => p.v);
  const max = Math.max(1e-9, ...vals);
  const min = Math.min(0, ...vals);
  const range = max - min || 1;

  const xy = (i: number, v: number): [number, number] => {
    const x = points.length <= 1 ? W / 2 : pad + (i / (points.length - 1)) * (W - 2 * pad);
    const y = H - pad - ((v - min) / range) * (H - 2 * pad);
    return [x, y];
  };

  const line = points.map((p, i) => `${i === 0 ? 'M' : 'L'} ${xy(i, p.v).map((n) => n.toFixed(1)).join(' ')}`).join(' ');
  const area =
    points.length > 0
      ? `${line} L ${xy(points.length - 1, points[points.length - 1]!.v)[0].toFixed(1)} ${H - pad} L ${xy(0, points[0]!.v)[0].toFixed(1)} ${H - pad} Z`
      : '';
  const gradId = `g-${label.replace(/\W/g, '')}`;

  return (
    <div className="rounded-lg border border-line bg-base p-3">
      <div className="mb-1 flex items-baseline justify-between">
        <span className="font-mono text-[10px] uppercase tracking-wider text-faint">{label}</span>
        {value !== undefined && (
          <span className="font-mono text-[13px] text-strong">
            {value}
            <span className="text-faint">{unit}</span>
          </span>
        )}
      </div>
      <svg viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" className="h-16 w-full" role="img" aria-label={label}>
        <defs>
          <linearGradient id={gradId} x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor={color} stopOpacity="0.28" />
            <stop offset="100%" stopColor={color} stopOpacity="0" />
          </linearGradient>
        </defs>
        {area && <path d={area} fill={`url(#${gradId})`} />}
        {line && <path d={line} fill="none" stroke={color} strokeWidth="1.6" vectorEffect="non-scaling-stroke" />}
      </svg>
    </div>
  );
}
