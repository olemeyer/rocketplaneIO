'use client';

import { cn } from '@/lib/cn';
import type { ClusterAction } from '@/lib/api/types';
import { StepTimeline } from './step-timeline';

// run-card.tsx — die kanonische Darstellung EINES Action-Laufs: Status-Rail,
// Live-Progress-Bar und eine verbundene Step-TIMELINE (Knoten + Linie). Gleiches
// Vokabular überall: Workload-Panel, Actions-Tab.

type Tone = { fg: string; bg: string; glyph: string; rail: string };
const FALLBACK: Tone = { fg: 'var(--rp-ink-muted)', bg: 'var(--rp-tone-neutral-bg)', glyph: '○', rail: 'var(--rp-line-strong)' };
const STATUS: Record<string, Tone> = {
  pending: { ...FALLBACK, glyph: '◌' },
  // MCP approval gate: parked until a human decides on the Transactions page.
  awaiting_approval: { fg: 'var(--rp-tone-yellow-fg)', bg: 'var(--rp-tone-yellow-bg)', glyph: '◆', rail: 'var(--rp-yellow)' },
  running: { fg: 'var(--rp-ink-mid)', bg: 'var(--rp-tone-neutral-bg)', glyph: '◌', rail: 'var(--rp-green)' },
  succeeded: { fg: 'var(--rp-tone-green-fg)', bg: 'var(--rp-tone-green-bg)', glyph: '●', rail: 'var(--rp-green)' },
  failed: { fg: 'var(--rp-tone-red-fg)', bg: 'var(--rp-tone-red-bg)', glyph: '▲', rail: 'var(--rp-red)' },
  cancelled: { fg: 'var(--rp-tone-yellow-fg)', bg: 'var(--rp-tone-yellow-bg)', glyph: '⊘', rail: 'var(--rp-yellow)' },
};

export function ActionRunCard({
  action: a,
  onCancel,
}: {
  action: ClusterAction;
  onCancel?: (id: string) => void;
}) {
  const tone = STATUS[a.status] ?? FALLBACK;
  const running = a.status === 'pending' || a.status === 'running';
  const isScript = a.kind === 'script';
  const steps = a.steps ?? [];
  const okCount = steps.filter((s) => s.status === 'ok').length;
  const ratio = steps.length ? okCount / steps.length : running ? 0.08 : 0;

  const t0 = new Date(a.createdAt).getTime();
  const t1 = new Date(a.updatedAt).getTime();
  const durMs = Math.max(0, t1 - t0);
  const dur = durMs < 1000 ? `${durMs}ms` : `${(durMs / 1000).toFixed(1)}s`;

  return (
    <div
      className="flex overflow-hidden rounded-skin-sm border border-line bg-raised"
      style={{ boxShadow: 'var(--rp-rim)' }}
    >
      {/* Status-Rail */}
      <span className="w-[3px] shrink-0" style={{ background: tone.rail, opacity: running ? 0.9 : 0.75 }} />

      <div className="min-w-0 flex-1 px-2.5 py-2 font-mono text-[10.5px]">
        {/* Kopf: Status + Ziel + Dauer + Cancel */}
        <div className="flex items-center gap-2">
          <span className="flex shrink-0 items-center gap-1.5">
            <span className="relative flex h-3 w-3 items-center justify-center">
              {running ? (
                <span className="animate-ping-slow absolute inset-0 rounded-full" style={{ color: tone.rail }} />
              ) : null}
              <span className="text-[9px]" style={{ color: tone.fg }}>{tone.glyph}</span>
            </span>
            <span className="text-[9px] uppercase tracking-[0.06em]" style={{ color: tone.fg }}>{a.status}</span>
          </span>

          <span className="min-w-0 flex-1 truncate text-ink">
            {isScript ? (
              <>
                <span className="rounded-skin-chip bg-inset px-1 py-px text-[9px] uppercase tracking-[0.05em] text-muted">
                  workflow
                </span>{' '}
                {a.targetName}
              </>
            ) : (
              <>
                <span className="text-mid">{a.kind.replace(/_/g, ' ')}</span>
                <span className="text-faint"> · </span>
                {a.targetName}
              </>
            )}
          </span>

          <span className="shrink-0 text-faint tnum">{dur}</span>
          {running && onCancel ? (
            a.cancelRequested ? (
              <span className="shrink-0 text-[9.5px]" style={{ color: 'var(--rp-tone-yellow-fg)' }}>cancelling…</span>
            ) : (
              <button
                type="button"
                onClick={() => onCancel(a.id)}
                className="shrink-0 rounded-skin-chip border border-line px-1.5 py-0.5 text-[9.5px] text-muted transition-colors hover:bg-hover hover:text-ink"
                title="Cancel — the engine rolls back automatically"
              >
                cancel
              </button>
            )
          ) : null}
        </div>

        {/* Live-Progress-Bar */}
        {running ? (
          <div className="mt-1.5 h-[3px] w-full overflow-hidden rounded-full bg-inset">
            <div
              className={cn('h-full rounded-full transition-all duration-500', steps.length === 0 && 'rp-breath')}
              style={{ width: `${Math.max(6, ratio * 100)}%`, background: 'var(--rp-green)' }}
            />
          </div>
        ) : null}

        {running && a.progress ? (
          <div className="mt-1 truncate leading-snug text-mid">{a.progress}</div>
        ) : null}

        {/* Step timeline — the shared, canonical rendering */}
        <StepTimeline steps={steps} className="mt-2" />

        {/* Ergebnis */}
        {!running && a.result ? (
          <div
            className="mt-1.5 break-all leading-snug"
            style={{ color: a.status === 'failed' ? 'var(--rp-tone-red-fg)' : 'var(--rp-ink-muted)' }}
          >
            {a.result}
          </div>
        ) : null}
      </div>
    </div>
  );
}
