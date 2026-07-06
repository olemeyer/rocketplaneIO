'use client';

import { useEffect, useState } from 'react';
import { createPortal } from 'react-dom';
import Link from 'next/link';
import { cn } from '@/lib/cn';
import { Spinner } from '@/components/ui';
import { getLogs } from '@/lib/api/controlplane';
import type { LogLine } from '@/lib/api/types';

// log-drawer.tsx — das Log-Detail als Side-Panel von rechts (Symmetrie zum
// Trace-Drawer): voller Body, Meta-Grid, und die dash0-Killer-Sektion
// „surrounding logs" — ±30s desselben Pods, die angeklickte Zeile markiert.
// Farbe trägt ausschließlich Severity (Design-Gesetz №1).

const DRAWER_W = 'min(720px, 92vw)';

function sevColors(line: Pick<LogLine, 'severityNumber'>): { fg: string; bg: string } {
  if (line.severityNumber >= 17) return { fg: 'var(--rp-tone-red-fg)', bg: 'var(--rp-tone-red-bg)' };
  if (line.severityNumber >= 13) return { fg: 'var(--rp-tone-yellow-fg)', bg: 'var(--rp-tone-yellow-bg)' };
  return { fg: 'var(--rp-ink-muted)', bg: 'var(--rp-tone-neutral-bg)' };
}

export function LogDrawer({
  orgId,
  clusterId,
  line,
  onClose,
}: {
  orgId: string;
  clusterId: string;
  line: LogLine;
  onClose: () => void;
}) {
  const [context, setContext] = useState<LogLine[] | null>(null);

  // Kontext: ±30s desselben Pods rund um den Zeitstempel.
  useEffect(() => {
    setContext(null);
    const t = new Date(line.ts.replace(' ', 'T') + 'Z').getTime();
    getLogs(orgId, clusterId, {
      since: new Date(t - 30_000).toISOString(),
      until: new Date(t + 30_000).toISOString(),
      pod: line.podName,
      limit: 80,
    })
      .then((r) => setContext(r.lines))
      .catch(() => setContext([]));
  }, [orgId, clusterId, line]);

  useEffect(() => {
    // capture + stopPropagation: Esc schließt NUR diesen Drawer, nicht auch
    // darunterliegende Selektionen (z.B. das Workload-Panel auf der Map).
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') {
        e.stopPropagation();
        onClose();
      }
    }
    document.addEventListener('keydown', onKey, true);
    const prev = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    return () => {
      document.removeEventListener('keydown', onKey, true);
      document.body.style.overflow = prev;
    };
  }, [onClose]);

  const c = sevColors(line);
  const isCurrent = (l: LogLine) => l.ts === line.ts && l.body === line.body;

  // Trace-Sprung: ±30s um den Log, Service vorgefiltert, bei ERROR nur Fehler.
  const traceLink = (() => {
    const t = new Date(line.ts.replace(' ', 'T') + 'Z').getTime();
    const q = new URLSearchParams({
      since: new Date(t - 30_000).toISOString(),
      until: new Date(t + 30_000).toISOString(),
      service: line.workloadName,
    });
    if (line.severityNumber >= 17) q.set('onlyError', 'true');
    return `/clusters/${clusterId}/traces?${q}`;
  })();

  return createPortal(
    <div className="fixed inset-0 z-50" role="dialog" aria-modal="true" aria-label="Log detail">
      <button
        type="button"
        aria-label="Close"
        onClick={onClose}
        className="absolute inset-0 cursor-default"
        style={{ backgroundColor: 'var(--rp-scrim)' }}
      />
      <aside
        className="absolute inset-y-0 right-0 flex flex-col border-l border-line bg-raised"
        style={{
          width: DRAWER_W,
          boxShadow: 'var(--rp-rim), var(--rp-shadow-pop)',
          animation: 'rp-drawer-in var(--rp-dur-large) var(--rp-ease-enter)',
        }}
      >
        {/* Masthead */}
        <header className="shrink-0 border-b border-line px-5 pb-3 pt-4">
          <div className="flex items-start justify-between gap-3">
            <div className="min-w-0">
              <div className="rp-micro !text-[10px]">log / {line.ts.slice(0, 19)}</div>
              <div className="mt-1.5 flex items-center gap-2 border-b border-line-strong pb-2.5">
                <span
                  className="rounded-skin-chip px-1.5 py-0.5 font-mono text-[10px] font-medium uppercase tracking-[0.06em]"
                  style={{ color: c.fg, background: c.bg }}
                >
                  {line.severityText || 'INFO'}
                </span>
                <h2 className="truncate text-[17px] font-bold leading-tight tracking-tightest text-ink">
                  {line.workloadName}
                </h2>
                <span className="font-mono text-[11px] text-muted">· {line.namespace}</span>
              </div>
            </div>
            <button
              type="button"
              onClick={onClose}
              aria-label="Close"
              className="rp-focus mt-0.5 inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-skin-sm border border-line text-muted transition-colors hover:text-ink"
            >
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" aria-hidden>
                <path d="M6 6l12 12M18 6L6 18" stroke="currentColor" strokeWidth="1.8" strokeLinecap="square" />
              </svg>
            </button>
          </div>
        </header>

        <div className="min-h-0 flex-1 overflow-y-auto px-5 py-4">
          {/* Voller Body */}
          <div className="rp-micro !text-[10px]">message</div>
          <pre
            className="mt-1.5 whitespace-pre-wrap break-all rounded-skin-sm border border-line bg-inset p-3 font-mono text-[12px] leading-relaxed"
            style={{ color: line.severityNumber >= 13 ? c.fg : 'var(--rp-ink)' }}
          >
            {line.body}
          </pre>

          {/* Meta */}
          <div className="mt-4 grid grid-cols-2 gap-x-6 gap-y-1.5 font-mono text-[11px] tnum md:grid-cols-3">
            <Meta k="namespace" v={line.namespace} />
            <Meta k="workload" v={line.workloadName} />
            <Meta k="pod" v={line.podName} />
            <Meta k="container" v={line.containerName} />
            <Meta k="stream" v={line.stream} />
            <Meta k="severity" v={`${line.severityText || 'INFO'} (${line.severityNumber})`} />
          </div>

          {/* Sprünge — Deep-Links mit Zeitfenster (der Dev landet nie ungefiltert) */}
          <div className="mt-4 flex items-center gap-2">
            <Link
              href={traceLink}
              className="rounded-skin-sm border border-line px-2.5 py-1.5 font-mono text-[11px] text-mid transition-colors hover:bg-hover hover:text-ink"
            >
              Traces around this log →
            </Link>
            <Link
              href={`/clusters/${clusterId}/logs?workload=${encodeURIComponent(line.workloadName)}`}
              className="rounded-skin-sm border border-line px-2.5 py-1.5 font-mono text-[11px] text-mid transition-colors hover:bg-hover hover:text-ink"
            >
              All {line.workloadName} logs →
            </Link>
          </div>

          {/* Surrounding logs — ±30s desselben Pods */}
          <div className="mt-5">
            <div className="flex items-baseline justify-between">
              <span className="rp-micro !text-[10px]">surrounding logs · pod {line.podName}</span>
              <span className="font-mono text-[10px] text-faint">±30s</span>
            </div>
            {context === null ? (
              <div className="mt-3 flex items-center gap-2 text-muted">
                <Spinner /> <span className="font-mono text-[11px]">loading context…</span>
              </div>
            ) : context.length === 0 ? (
              <div className="mt-3 font-mono text-[11px] text-faint">no context lines</div>
            ) : (
              <div className="mt-2 overflow-hidden rounded-skin-sm border border-line">
                {[...context].reverse().map((l, i) => {
                  const lc = sevColors(l);
                  const current = isCurrent(l);
                  return (
                    <div
                      key={`${l.ts}-${i}`}
                      className={cn(
                        'flex items-start gap-2 border-b border-line/60 px-2 py-[3px] font-mono text-[10.5px] leading-relaxed last:border-0',
                        current && 'bg-hover',
                      )}
                      style={current ? { boxShadow: 'inset 0 0 0 1px var(--rp-line-strong)' } : undefined}
                    >
                      <span className="shrink-0 pt-px text-muted tnum">{l.ts.slice(11, 23)}</span>
                      <span
                        className="min-w-0 flex-1 break-all"
                        style={{
                          color: l.severityNumber >= 13 ? lc.fg : current ? 'var(--rp-ink)' : 'var(--rp-ink-mid)',
                        }}
                      >
                        {l.body}
                      </span>
                    </div>
                  );
                })}
              </div>
            )}
          </div>
        </div>
      </aside>
    </div>,
    document.body,
  );
}

function Meta({ k, v }: { k: string; v: string }) {
  return (
    <div className="min-w-0">
      <span className="text-muted">{k} </span>
      <span className="truncate text-ink">{v || '—'}</span>
    </div>
  );
}
