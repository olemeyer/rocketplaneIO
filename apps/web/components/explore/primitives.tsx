import { status as statusColors, type Status } from '@rocketplane/ui';

// StatusDot — semantischer Health-Punkt (aus den Tokens).
export function StatusDot({ state }: { state: Status }) {
  const color = statusColors[state];
  return (
    <span
      className="inline-block h-1.5 w-1.5 shrink-0 rounded-full"
      style={{ background: color, boxShadow: `0 0 8px ${color}66` }}
    />
  );
}

// Sparkline — kompakte, nach hinten heller werdende Balkenreihe.
export function Sparkline({ values, color }: { values: number[]; color: string }) {
  const max = Math.max(1, ...values);
  return (
    <div className="flex h-7 items-end gap-[3px]" aria-hidden>
      {values.map((h, i) => (
        <span
          key={i}
          className="w-full rounded-[1px]"
          style={{
            height: `${(h / max) * 24 + 2}px`,
            background: color,
            opacity: 0.35 + (i / Math.max(1, values.length)) * 0.6,
          }}
        />
      ))}
    </div>
  );
}

// FilterChip — key=value-Facette in der Filterleiste.
export function FilterChip({ k, v }: { k: string; v: string }) {
  return (
    <span className="flex items-center gap-1 rounded-md border border-line bg-overlay px-1.5 py-0.5 font-mono text-[11px]">
      <span className="text-faint">{k}</span>
      <span className="text-accent">{v}</span>
    </span>
  );
}

// formatRate — req/s hübsch (1204 -> "1.2k/s").
export function formatRate(rate: number): string {
  if (rate >= 1000) return `${(rate / 1000).toFixed(1)}k/s`;
  return `${Math.round(rate)}/s`;
}

// formatErrorRate — 0.042 -> "4.2%".
export function formatErrorRate(ratio: number): string {
  const pct = ratio * 100;
  if (pct > 0 && pct < 0.1) return '<0.1%';
  return `${pct.toFixed(1)}%`;
}
