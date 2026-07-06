'use client';

import { useState, type ReactNode } from 'react';
import { cn } from '@/lib/cn';

// Copy-Interaktionen für den Connect-Flow: ein Button + eine Command-Well.
// Swiss: eckig, bg-inset, fette ink-Rules, mono. „Nicht-Techniker-freundlich":
// grosse Trefferfläche, klares Copied-Feedback (blau = interaktiv/Bestätigung).

function useCopy(value: string) {
  const [copied, setCopied] = useState(false);
  async function copy() {
    try {
      await navigator.clipboard.writeText(value);
    } catch {
      // Fallback für unsichere Kontexte / ältere Browser.
      const el = document.createElement('textarea');
      el.value = value;
      el.style.position = 'fixed';
      el.style.opacity = '0';
      document.body.appendChild(el);
      el.select();
      try {
        document.execCommand('copy');
      } catch {
        /* ignore */
      }
      document.body.removeChild(el);
    }
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1600);
  }
  return { copied, copy };
}

function CheckIcon() {
  return (
    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" aria-hidden>
      <path
        d="M5 13l4 4L19 7"
        stroke="currentColor"
        strokeWidth="2.2"
        strokeLinecap="square"
        strokeLinejoin="miter"
      />
    </svg>
  );
}

function CopyIcon() {
  return (
    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" aria-hidden>
      <rect x="9" y="9" width="11" height="11" stroke="currentColor" strokeWidth="1.7" />
      <path d="M5 15V6a2 2 0 0 1 2-2h9" stroke="currentColor" strokeWidth="1.7" strokeLinecap="square" />
    </svg>
  );
}

export function CopyButton({
  value,
  label = 'Copy',
  className,
}: {
  value: string;
  label?: string;
  className?: string;
}) {
  const { copied, copy } = useCopy(value);
  return (
    <button
      type="button"
      onClick={copy}
      className={cn(
        'rp-label inline-flex h-7 items-center gap-1.5 rounded-skin-sm border border-line bg-raised px-2 text-[11px] transition-colors',
        'hover:bg-hover rp-focus',
        copied ? 'text-accent' : 'text-ink',
        className,
      )}
    >
      {copied ? <CheckIcon /> : <CopyIcon />}
      {copied ? 'Copied' : label}
    </button>
  );
}

/** Command-Well: mono, eckige Vertiefung (bg-inset), Copy-Button oben rechts. */
export function CommandBox({
  command,
  prompt = '$',
  caption,
}: {
  command: string;
  prompt?: string;
  caption?: ReactNode;
}) {
  return (
    <div className="rounded-skin border border-line bg-inset">
      <div className="flex items-center justify-between gap-2 border-b border-line px-3 py-1.5">
        <span className="rp-label text-[10px] text-muted">{caption ?? 'Install command'}</span>
        <CopyButton value={command} />
      </div>
      <pre className="max-h-52 overflow-auto px-3 py-3 text-[12px] leading-relaxed">
        <code className="font-mono">
          <span className="mr-2 select-none text-accent">{prompt}</span>
          <span className="whitespace-pre-wrap break-all text-ink">{command}</span>
        </code>
      </pre>
    </div>
  );
}
