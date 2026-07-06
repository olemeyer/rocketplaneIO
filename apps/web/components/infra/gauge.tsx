'use client';

// gauge.tsx — das RETICLE-Instrument für Auslastung: 1px-Rahmen, präziser
// Füllbalken, Werte mono+tabular. Farbe folgt der STATUS-SKALA und spricht
// erst bei Anomalie: neutral < 70% · gold ≥ 70% · crimson ≥ 90% (Dark-Cockpit
// — gesunde Auslastung ist ruhig grau).

export function fmtBytes(n: number): string {
  if (n < 0) return '—';
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
  let v = n;
  let u = 0;
  while (v >= 1024 && u < units.length - 1) {
    v /= 1024;
    u += 1;
  }
  return `${v >= 100 ? Math.round(v) : v.toFixed(1)} ${units[u]}`;
}

export function fmtCores(milli: number): string {
  if (milli < 0) return '—';
  return milli >= 1000 ? `${(milli / 1000).toFixed(milli >= 10_000 ? 0 : 2)}` : `${(milli / 1000).toFixed(2)}`;
}

export function usageTone(frac: number): { bar: string; fg: string } {
  if (frac >= 0.9) return { bar: 'var(--rp-red)', fg: 'var(--rp-tone-red-fg)' };
  if (frac >= 0.7) return { bar: 'var(--rp-yellow)', fg: 'var(--rp-tone-yellow-fg)' };
  return { bar: 'var(--rp-ink-mid)', fg: 'var(--rp-ink)' };
}

export function Gauge({
  label,
  used,
  total,
  render,
  height = 6,
}: {
  label: string;
  /** -1 = unbekannt → Balken leer, Wert „—" */
  used: number;
  total: number;
  render: (used: number, total: number) => string;
  height?: number;
}) {
  const known = used >= 0 && total > 0;
  const frac = known ? Math.min(1, used / total) : 0;
  const tone = usageTone(frac);
  return (
    <div>
      <div className="flex items-baseline justify-between gap-2">
        <span className="rp-micro !text-[9.5px]">{label}</span>
        <span className="font-mono text-[10.5px] tnum" style={{ color: known ? tone.fg : 'var(--rp-ink-faint)' }}>
          {known ? render(used, total) : '—'}
          {known ? (
            <span className="text-faint"> · {Math.round(frac * 100)}%</span>
          ) : null}
        </span>
      </div>
      <div
        className="mt-1 w-full overflow-hidden rounded-full bg-inset"
        style={{ height, boxShadow: 'inset 0 0 0 1px var(--rp-line)' }}
      >
        <div
          className="h-full rounded-full transition-[width] duration-500"
          style={{ width: `${frac * 100}%`, background: tone.bar, opacity: frac >= 0.7 ? 0.9 : 0.65 }}
        />
      </div>
    </div>
  );
}
