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

// InstallPicker — beide Wege (kubectl/YAML-apply und Helm) mit Tab-Umschalter.
// Fällt auf `fallback` zurück, wenn ein älteres Backend nur einen Command liefert.
export function InstallPicker({
  commands,
  fallback,
}: {
  commands?: { kubectl: string; helm: string };
  fallback: string;
}) {
  const [tab, setTab] = useState<'kubectl' | 'helm'>('helm');
  if (!commands) return <CommandBox command={fallback} />;
  const active = commands[tab];
  // Helm is the default route — it installs the full chart (agent + Beyla eBPF
  // + log collector) and is the upgrade/uninstall-friendly path. kubectl stays
  // as the plain-manifest alternative.
  const TABS: { key: 'kubectl' | 'helm'; label: string; hint: string }[] = [
    { key: 'helm', label: 'Helm', hint: 'installs the full chart (agent + Beyla)' },
    { key: 'kubectl', label: 'kubectl', hint: 'applies a rendered manifest' },
  ];
  return (
    <div>
      <div className="mb-2 flex gap-1">
        {TABS.map((t) => {
          const on = tab === t.key;
          return (
            <button
              key={t.key}
              type="button"
              onClick={() => setTab(t.key)}
              className={cn(
                'rp-focus rounded-skin-sm border px-2.5 py-1 font-mono text-[11px] transition-colors',
                on ? 'border-line-strong bg-hover text-ink' : 'border-line text-muted hover:text-ink',
              )}
            >
              {t.label}
            </button>
          );
        })}
        <span className="ml-1 self-center font-mono text-[10px] text-faint">
          {TABS.find((t) => t.key === tab)!.hint}
        </span>
      </div>
      <CommandBox command={active} caption={tab === 'helm' ? 'Helm install' : 'kubectl apply'} />
    </div>
  );
}
