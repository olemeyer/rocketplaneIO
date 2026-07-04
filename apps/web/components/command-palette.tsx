'use client';

import { useEffect, useMemo, useRef, useState } from 'react';
import type { ComponentType, SVGProps } from 'react';
import {
  Compass,
  CornerReturn,
  Cube,
  Dashboards,
  Logs,
  Metrics,
  Search,
  Terminal,
  Waterfall,
} from './icons';

type Item = { label: string; hint: string; icon: ComponentType<SVGProps<SVGSVGElement>> };
type Group = { heading: string; items: Item[] };

const GROUPS: Group[] = [
  {
    heading: 'Navigate',
    items: [
      { label: 'Explore', hint: 'G E', icon: Compass },
      { label: 'Services', hint: 'G S', icon: Cube },
      { label: 'Traces', hint: 'G T', icon: Waterfall },
      { label: 'Logs', hint: 'G L', icon: Logs },
      { label: 'Metrics', hint: 'G M', icon: Metrics },
      { label: 'Dashboards', hint: 'G D', icon: Dashboards },
    ],
  },
  {
    heading: 'Query',
    items: [
      { label: 'Run PromQL…', hint: '↵', icon: Terminal },
      { label: 'Filter by service.name', hint: 'F', icon: Search },
    ],
  },
  {
    heading: 'Recent',
    items: [
      { label: 'checkout-api · errors · last 15m', hint: '', icon: Waterfall },
      { label: 'payment-gateway · p95 spike', hint: '', icon: Metrics },
    ],
  },
];

export function CommandPalette({ open, onClose }: { open: boolean; onClose: () => void }) {
  const inputRef = useRef<HTMLInputElement>(null);
  const [active, setActive] = useState(0);

  const flat = useMemo(() => GROUPS.flatMap((g) => g.items), []);

  useEffect(() => {
    if (!open) return;
    setActive(0);
    // Fokus auf das Suchfeld beim Öffnen.
    const id = window.setTimeout(() => inputRef.current?.focus(), 0);

    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') {
        e.preventDefault();
        onClose();
      } else if (e.key === 'ArrowDown') {
        e.preventDefault();
        setActive((i) => (i + 1) % flat.length);
      } else if (e.key === 'ArrowUp') {
        e.preventDefault();
        setActive((i) => (i - 1 + flat.length) % flat.length);
      } else if (e.key === 'Enter') {
        e.preventDefault();
        onClose();
      }
    }
    window.addEventListener('keydown', onKey);
    return () => {
      window.clearTimeout(id);
      window.removeEventListener('keydown', onKey);
    };
  }, [open, onClose, flat.length]);

  if (!open) return null;

  let idx = -1;
  return (
    <div
      className="fixed inset-0 z-[900] flex items-start justify-center px-4 pt-[14vh]"
      role="dialog"
      aria-modal="true"
      aria-label="Command Menu"
    >
      <button
        aria-label="Close command menu"
        onClick={onClose}
        className="absolute inset-0 cursor-default bg-black/50 backdrop-blur-sm"
      />
      <div className="reveal relative w-full max-w-xl overflow-hidden rounded-xl border border-line bg-raised shadow-palette">
        {/* Suchzeile */}
        <div className="flex items-center gap-2.5 border-b border-line px-3.5 py-3">
          <Search className="h-4 w-4 text-faint" />
          <input
            ref={inputRef}
            placeholder="Type a command or search…"
            className="w-full bg-transparent text-[14px] text-strong caret-[color:var(--rp-accent)] outline-none placeholder:text-faint"
          />
          <kbd className="rounded border border-line bg-overlay px-1.5 py-0.5 font-mono text-[10px] text-faint">
            esc
          </kbd>
        </div>

        {/* Ergebnisse */}
        <div className="max-h-[46vh] overflow-y-auto p-1.5">
          {GROUPS.map((group) => (
            <div key={group.heading} className="mb-1">
              <div className="px-2.5 py-1 font-mono text-[10px] uppercase tracking-wider text-faint">
                {group.heading}
              </div>
              {group.items.map((item) => {
                idx += 1;
                const isActive = idx === active;
                const Icon = item.icon;
                return (
                  <div
                    key={item.label}
                    className={`flex items-center gap-2.5 rounded-md px-2.5 py-2 text-[13px] ${
                      isActive ? 'bg-overlay text-strong' : 'text-muted'
                    }`}
                  >
                    <Icon className={`h-4 w-4 ${isActive ? 'text-accent' : 'text-faint'}`} />
                    <span className="flex-1 truncate">{item.label}</span>
                    {item.hint && (
                      <kbd className="rounded border border-line px-1.5 py-0.5 font-mono text-[10px] text-faint">
                        {item.hint}
                      </kbd>
                    )}
                  </div>
                );
              })}
            </div>
          ))}
        </div>

        {/* Fußleiste */}
        <div className="flex items-center gap-4 border-t border-line px-3.5 py-2 font-mono text-[10px] text-faint">
          <span className="flex items-center gap-1">
            <span className="rounded border border-line px-1">↑</span>
            <span className="rounded border border-line px-1">↓</span>
            navigate
          </span>
          <span className="flex items-center gap-1">
            <CornerReturn className="h-3 w-3" /> select
          </span>
          <span className="ml-auto flex items-center gap-1.5 text-muted">
            <span className="h-1.5 w-1.5 rounded-full bg-accent" /> rocketplaneIO
          </span>
        </div>
      </div>
    </div>
  );
}
