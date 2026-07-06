'use client';

import { useEffect, useRef, useState, type ReactNode } from 'react';
import { cn } from '@/lib/cn';

// Leichtgewichtiges Dropdown ohne Dependency: Click-outside + Escape schliessen.
// Skin-agnostisch: fetter Rahmen (border-line-strong), raised-Fläche, Radius/Schatten
// token-getrieben (rounded-skin + shadow-pop). Swiss → eckig/flach, Aurora → weich/gehoben.
// `trigger` und `content` sind Render-Props; content bekommt `close()`.
export function Popover({
  trigger,
  content,
  align = 'start',
  contentClassName,
}: {
  trigger: (open: boolean) => ReactNode;
  content: (close: () => void) => ReactNode;
  align?: 'start' | 'end';
  contentClassName?: string;
}) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    function onDown(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    }
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') setOpen(false);
    }
    document.addEventListener('mousedown', onDown);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDown);
      document.removeEventListener('keydown', onKey);
    };
  }, [open]);

  return (
    <div ref={ref} className="relative">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-haspopup="menu"
        aria-expanded={open}
        className="block rounded-skin-sm outline-none rp-focus"
      >
        {trigger(open)}
      </button>
      {open ? (
        <div
          role="menu"
          className={cn(
            'absolute z-50 mt-1.5 min-w-[224px] rounded-skin-lg border border-line bg-overlay p-1 shadow-pop',
            align === 'end' ? 'right-0' : 'left-0',
            contentClassName,
          )}
          style={{ boxShadow: 'var(--rp-rim), var(--rp-shadow-pop)' }}
        >
          {content(() => setOpen(false))}
        </div>
      ) : null}
    </div>
  );
}

/** Einheitliche Menü-Zeile für Popover-Inhalte. */
export function MenuItem({
  onClick,
  children,
  active,
  className,
}: {
  onClick?: () => void;
  children: ReactNode;
  active?: boolean;
  className?: string;
}) {
  return (
    <button
      type="button"
      role="menuitem"
      onClick={onClick}
      className={cn(
        'relative flex w-full items-center gap-2 rounded-skin-sm px-2 py-1.5 text-left text-[13px] text-mid transition-colors',
        'hover:bg-hover hover:text-ink focus-visible:bg-hover focus-visible:outline-none',
        active && 'bg-hover text-ink',
        className,
      )}
    >
      {active ? (
        <span
          className="absolute left-0 top-1/2 h-3.5 w-[2px] -translate-y-1/2 rounded-full"
          style={{ background: 'var(--rp-accent)' }}
          aria-hidden
        />
      ) : null}
      {children}
    </button>
  );
}
