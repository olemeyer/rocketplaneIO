'use client';

import { useState } from 'react';
import { cn } from '@/lib/cn';
import { fmtMs, statusTone, type SpanNode } from '@/components/traces/trace-drawer';
import { serviceFill } from '@/components/traces/service-color';

// flame-graph.tsx — Spans als Blöcke auf der gemeinsamen absoluten Zeitachse,
// vertikal nach Call-Tiefe gestapelt (Root oben), Farbe = Service-Identität
// (identisch mit Waterfall-Dot und Trace-Graph-Node). Fehler-Spans tragen einen
// Crimson-Rahmen + ▲ (Status bleibt der Status-Skala, Design-Gesetz №2);
// Selektion ist ein neutraler Ink-Ring (№1). Hairline-Grid + Tick-Labels geben
// die Zeit, ein Custom-Tooltip die Details.

const ROW_H = 26;
const ROW_GAP = 3;

export function FlameGraph({
  tree,
  t0,
  totalMs,
  selected,
  onSelect,
  colorOf,
}: {
  tree: SpanNode[];
  t0: number;
  totalMs: number;
  selected: string | null;
  onSelect: (spanId: string) => void;
  colorOf: (service: string) => string;
}) {
  const [hover, setHover] = useState<string | null>(null);
  const maxDepth = tree.reduce((m, s) => Math.max(m, s.depth), 0);
  const height = (maxDepth + 1) * (ROW_H + ROW_GAP) - ROW_GAP;
  const ticks = [0, 0.25, 0.5, 0.75, 1];

  return (
    <div className="p-3">
      {/* Zeitachse */}
      <div className="relative mb-1.5 h-4 font-mono text-[9.5px] text-muted tnum">
        {ticks.map((f) => (
          <span
            key={f}
            className="absolute -translate-x-1/2"
            style={{ left: `${f * 100}%`, transform: f === 0 ? 'none' : f === 1 ? 'translateX(-100%)' : undefined }}
          >
            {fmtMs(totalMs * f)}
          </span>
        ))}
      </div>

      <div className="relative" style={{ height }}>
        {/* Hairline-Grid */}
        {ticks.slice(1, -1).map((f) => (
          <div
            key={f}
            className="pointer-events-none absolute inset-y-0 w-px"
            style={{ left: `${f * 100}%`, background: 'var(--rp-line)' }}
          />
        ))}

        {tree.map((s) => {
          const tone = statusTone(s.httpStatus ?? '', s.statusCode ?? '');
          const isErr = tone === 'err';
          const color = colorOf(s.serviceName);
          const fill = serviceFill(color);
          const offsetMs = (s.startUnixNs - t0) / 1e6;
          const left = Math.min(99.2, Math.max(0, (offsetMs / totalMs) * 100));
          const width = Math.max(0.35, Math.min(100 - left, (s.durationMs / totalMs) * 100));
          const isSel = selected === s.spanId;
          const isHover = hover === s.spanId;
          // Tooltip unterhalb der oberen Reihen, oberhalb der unteren
          const tipBelow = s.depth < 2;
          return (
            <button
              key={s.spanId}
              type="button"
              onClick={() => onSelect(s.spanId)}
              onMouseEnter={() => setHover(s.spanId)}
              onMouseLeave={() => setHover(null)}
              className={cn(
                'absolute overflow-visible rounded-[3px] text-left transition-[opacity,box-shadow]',
                hover && !isHover && !isSel ? 'opacity-60' : 'opacity-100',
              )}
              style={{
                top: s.depth * (ROW_H + ROW_GAP),
                height: ROW_H,
                left: `${left}%`,
                width: `${width}%`,
                minWidth: 3,
                background: fill.bg,
                border: `1px solid ${isErr ? 'var(--rp-red)' : fill.border}`,
                boxShadow: isSel ? '0 0 0 1.5px var(--rp-ink-mid)' : undefined,
                zIndex: isHover || isSel ? 2 : 1,
              }}
            >
              <span className="pointer-events-none flex h-full items-center gap-1.5 overflow-hidden whitespace-nowrap px-1.5 font-mono text-[10.5px] leading-none">
                {isErr ? <span style={{ color: 'var(--rp-red)' }}>▲</span> : null}
                <span className="truncate text-ink">{s.spanName}</span>
                <span className="text-muted tnum">{fmtMs(s.durationMs)}</span>
              </span>

              {isHover ? (
                <span
                  className={cn(
                    'pointer-events-none absolute z-30 block whitespace-nowrap rounded-skin-sm border border-line bg-overlay px-2 py-1.5 font-mono text-[10px] leading-snug',
                    tipBelow ? 'top-full mt-1.5' : 'bottom-full mb-1.5',
                    left > 60 ? 'right-0' : 'left-0',
                  )}
                  style={{ boxShadow: 'var(--rp-rim), var(--rp-shadow-pop)' }}
                >
                  <span className="flex items-center gap-1.5">
                    <span className="h-[7px] w-[7px] rounded-full" style={{ background: color }} />
                    <span className="text-mid">{s.serviceName}</span>
                    <span className="font-medium text-ink">{s.spanName}</span>
                  </span>
                  <span className="mt-0.5 block text-muted tnum">
                    {fmtMs(s.durationMs)} · starts +{fmtMs(offsetMs)}
                    {s.httpStatus ? (
                      <span style={{ color: isErr ? 'var(--rp-tone-red-fg)' : undefined }}>
                        {' '}
                        · {s.httpStatus}
                      </span>
                    ) : null}
                  </span>
                </span>
              ) : null}
            </button>
          );
        })}
      </div>
    </div>
  );
}
