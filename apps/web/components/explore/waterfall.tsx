import type { TraceSpan } from '@/lib/api';

// Waterfall rendert die Span-Zeilen eines Trace (rein präsentational, damit die
// Explore-Vorschau und die Trace-Detail-Seite dieselbe Darstellung teilen).
export function Waterfall({ spans }: { spans: TraceSpan[] }) {
  return (
    <div className="space-y-[3px]">
      {spans.map((sp, i) => (
        <div key={i} className="grid grid-cols-[minmax(160px,220px)_1fr_56px] items-center gap-2">
          <div
            className="flex items-center gap-1.5 truncate font-mono text-[11px]"
            style={{ paddingLeft: `${sp.depth * 12}px` }}
          >
            <span className="h-2.5 w-[3px] shrink-0 rounded-full" style={{ background: sp.color }} />
            <span className={`truncate ${sp.isError ? 'text-status-critical' : 'text-strong'}`}>
              {sp.name}
            </span>
            <span className="truncate text-faint">{sp.service}</span>
          </div>
          <div className="relative h-3.5 rounded-[3px] bg-overlay">
            <div
              className="absolute top-0 h-full rounded-[3px]"
              style={{
                left: `${sp.offsetPct}%`,
                width: `${sp.widthPct}%`,
                background: sp.color,
                opacity: sp.isError ? 1 : 0.85,
                boxShadow: sp.isError ? `0 0 0 1px ${sp.color}` : undefined,
              }}
            />
          </div>
          <span className="text-right font-mono text-[11px] text-muted">{Math.round(sp.durationMs)}ms</span>
        </div>
      ))}
    </div>
  );
}
