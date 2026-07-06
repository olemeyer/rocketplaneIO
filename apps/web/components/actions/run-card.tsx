'use client';

import { cn } from '@/lib/cn';
import type { ClusterAction } from '@/lib/api/types';

// run-card.tsx — die kanonische Darstellung EINES Action-Laufs: Status-Rail,
// Live-Progress-Bar und eine verbundene Step-TIMELINE (Knoten + Linie). Gleiches
// Vokabular überall: Workload-Panel, Actions-Tab.

type Tone = { fg: string; bg: string; glyph: string; rail: string };
const FALLBACK: Tone = { fg: 'var(--rp-ink-muted)', bg: 'var(--rp-tone-neutral-bg)', glyph: '○', rail: 'var(--rp-line-strong)' };
const STATUS: Record<string, Tone> = {
  pending: { ...FALLBACK, glyph: '◌' },
  running: { fg: 'var(--rp-ink-mid)', bg: 'var(--rp-tone-neutral-bg)', glyph: '◌', rail: 'var(--rp-green)' },
  succeeded: { fg: 'var(--rp-tone-green-fg)', bg: 'var(--rp-tone-green-bg)', glyph: '●', rail: 'var(--rp-green)' },
  failed: { fg: 'var(--rp-tone-red-fg)', bg: 'var(--rp-tone-red-bg)', glyph: '▲', rail: 'var(--rp-red)' },
  cancelled: { fg: 'var(--rp-tone-yellow-fg)', bg: 'var(--rp-tone-yellow-bg)', glyph: '⊘', rail: 'var(--rp-yellow)' },
};

function stepColor(status: string): string {
  return status === 'ok'
    ? 'var(--rp-tone-green-fg)'
    : status === 'failed'
      ? 'var(--rp-tone-red-fg)'
      : status === 'running'
        ? 'var(--rp-ink-mid)'
        : 'var(--rp-ink-faint)';
}

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

        {/* Step-TIMELINE — Knoten auf einer Linie */}
        {steps.length > 0 ? (
          <ol className="mt-2">
            {steps.map((s, i) => {
              const c = stepColor(s.status);
              const last = i === steps.length - 1;
              return (
                <li key={`${s.name}-${i}`} className="relative flex gap-2.5 pb-1.5 last:pb-0">
                  {!last ? (
                    <span
                      className="absolute bottom-0 left-[4.5px] top-[11px] w-px"
                      style={{ background: 'var(--rp-line-strong)' }}
                      aria-hidden
                    />
                  ) : null}
                  <span className="relative z-10 mt-[3px] shrink-0">
                    {s.status === 'ok' ? (
                      <span className="flex h-2.5 w-2.5 items-center justify-center rounded-full" style={{ background: c }}>
                        <svg width="6" height="6" viewBox="0 0 8 8" aria-hidden>
                          <path d="M1.5 4.2 3 5.7 6.5 2.2" stroke="var(--rp-bg-base)" strokeWidth="1.4" fill="none" strokeLinecap="round" strokeLinejoin="round" />
                        </svg>
                      </span>
                    ) : s.status === 'running' ? (
                      <span className="animate-ping-slow flex h-2.5 w-2.5 rounded-full" style={{ background: c, color: c }} />
                    ) : s.status === 'failed' ? (
                      <span className="text-[10px] leading-none" style={{ color: c }}>▲</span>
                    ) : (
                      <span className="block h-2.5 w-2.5 rounded-full border" style={{ borderColor: 'var(--rp-line-strong)' }} />
                    )}
                  </span>
                  <div className="min-w-0 flex-1 leading-snug">
                    <span style={{ color: s.status === 'pending' ? 'var(--rp-ink-faint)' : 'var(--rp-ink)' }}>{s.name}</span>
                    {s.detail ? (
                      <span className="text-faint" title={s.detail}> · {s.detail}</span>
                    ) : null}
                  </div>
                </li>
              );
            })}
          </ol>
        ) : null}

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
