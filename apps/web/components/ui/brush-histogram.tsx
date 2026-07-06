'use client';

import { useState } from 'react';

// brush-histogram.tsx — das gemeinsame, brushbare Volumen-Chart der Explorer
// (Logs + Traces): Drag über die Bars wählt ein absolutes Zeitfenster,
// Hover zeigt Bucket-Details, Fehleranteile glimmen rot (Status). Das
// Brush-Overlay ist bewusst NEUTRAL (ink) — neben Status-Rot darf die
// Selektion nicht farblich konkurrieren (Design-Gesetz №1).

export type HistogramBucket = { ts: string; count: number; errors: number; warns?: number };
export type BrushWindow = { since: string; until: string };

export function BrushHistogram({
  buckets,
  brush,
  onBrushChange,
  rangeLabel,
  unit = 'lines',
}: {
  buckets: HistogramBucket[];
  brush: BrushWindow | null;
  onBrushChange: (b: BrushWindow | null) => void;
  rangeLabel: string; // z.B. "−15m"
  unit?: string;
}) {
  const [drag, setDrag] = useState<{ a: number; b: number } | null>(null);
  const [hoverBar, setHoverBar] = useState<number | null>(null);
  const maxBucket = buckets.reduce((m, b) => Math.max(m, b.count), 1);

  return (
    <div
      className="mt-3 shrink-0 select-none rounded-skin border border-line bg-raised px-2 pb-1 pt-2"
      style={{ boxShadow: 'var(--rp-rim)' }}
      onMouseLeave={() => {
        setHoverBar(null);
        setDrag(null);
      }}
      onMouseUp={() => {
        if (drag && buckets.length > 0) {
          const a = Math.min(drag.a, drag.b);
          const b = Math.max(drag.a, drag.b);
          const toIso = (ts: string) => ts.replace(' ', 'T') + 'Z';
          const sinceTs = buckets[a]?.ts;
          const untilTs = buckets[b + 1]?.ts; // exklusives Ende = nächster Bucket
          if (sinceTs) {
            onBrushChange({
              since: toIso(sinceTs),
              until: untilTs ? toIso(untilTs) : new Date().toISOString(),
            });
          }
        }
        setDrag(null);
      }}
    >
      <div className="relative flex h-[52px] items-end gap-[2px]">
        {buckets.length === 0 ? (
          <span className="mx-auto mb-2 font-mono text-[10px] text-faint">
            no volume in range
          </span>
        ) : (
          buckets.map((b, i) => {
            const h = Math.max(2, Math.round((b.count / maxBucket) * 40));
            const errFrac = b.count > 0 ? b.errors / b.count : 0;
            const warnFrac = b.count > 0 ? (b.warns ?? 0) / b.count : 0;
            const inDrag = drag && i >= Math.min(drag.a, drag.b) && i <= Math.max(drag.a, drag.b);
            return (
              <div
                key={i}
                className="group relative flex h-full flex-1 cursor-crosshair items-end"
                onMouseDown={(e) => {
                  e.preventDefault();
                  setDrag({ a: i, b: i });
                }}
                onMouseEnter={() => {
                  setHoverBar(i);
                  if (drag) setDrag({ a: drag.a, b: i });
                }}
              >
                <div
                  className="w-full overflow-hidden rounded-[1px] transition-opacity"
                  style={{
                    height: h,
                    // VOLLER Severity-Stapel: Fehler crimson oben, Warnungen
                    // gold darunter, Rest ruhig neutral (Status-Skala).
                    background: `linear-gradient(180deg,
                      var(--rp-red) 0%, var(--rp-red) ${errFrac * 100}%,
                      var(--rp-yellow) ${errFrac * 100}%, var(--rp-yellow) ${(errFrac + warnFrac) * 100}%,
                      var(--rp-line-strong) ${(errFrac + warnFrac) * 100}%, var(--rp-line-strong) 100%)`,
                    opacity: hoverBar === i || inDrag ? 1 : 0.8,
                  }}
                />
                {inDrag ? (
                  <div
                    className="pointer-events-none absolute inset-y-0 -inset-x-px"
                    style={{
                      background: 'color-mix(in oklab, var(--rp-ink) 10%, transparent)',
                      borderTop: '1px solid var(--rp-ink-muted)',
                    }}
                  />
                ) : null}
                {hoverBar === i && !drag ? (
                  <div
                    className="pointer-events-none absolute bottom-full left-1/2 z-20 mb-1.5 -translate-x-1/2 whitespace-nowrap rounded-skin-sm border border-line bg-overlay px-2 py-1 font-mono text-[10px] leading-snug"
                    style={{ boxShadow: 'var(--rp-rim), var(--rp-shadow-pop)' }}
                  >
                    <div className="text-ink tnum">
                      {b.ts.slice(11, 16)} · {b.count.toLocaleString()} {unit}
                    </div>
                    {b.errors > 0 ? (
                      <div style={{ color: 'var(--rp-tone-red-fg)' }} className="tnum">
                        ▲ {b.errors.toLocaleString()} errors
                      </div>
                    ) : null}
                    {(b.warns ?? 0) > 0 ? (
                      <div style={{ color: 'var(--rp-tone-yellow-fg)' }} className="tnum">
                        ◆ {(b.warns ?? 0).toLocaleString()} warnings
                      </div>
                    ) : null}
                  </div>
                ) : null}
              </div>
            );
          })
        )}
      </div>
      <div className="mt-1 flex items-center justify-between border-t border-line/60 px-0.5 pt-0.5 font-mono text-[10px] text-muted tnum">
        <span>{brush ? brush.since.slice(11, 16) : rangeLabel}</span>
        <span className="text-faint">
          {drag ? 'release to select range' : 'drag to select a range'}
        </span>
        <span>{brush ? brush.until.slice(11, 16) : 'now'}</span>
      </div>
    </div>
  );
}

// Toolbar-Pill für ein aktives Brush-Fenster (✕ = zurück zu Live/Range).
export function BrushPill({
  brush,
  onClear,
}: {
  brush: BrushWindow;
  onClear: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClear}
      className="h-9 rounded-skin-sm border border-line bg-inset px-3 font-mono text-[11px] text-ink transition-colors hover:bg-hover"
      title="Zeitfenster-Auswahl aufheben"
    >
      ⧉ {brush.since.slice(11, 16)}–{brush.until.slice(11, 16)} ✕
    </button>
  );
}
