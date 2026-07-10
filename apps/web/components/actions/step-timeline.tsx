'use client';

// step-timeline.tsx — the ONE canonical step timeline: connected nodes on a
// vertical rail, live-pulsing on the running step. Rendered everywhere an action
// appears — the Actions tab, the workload panel, the Runs page AND inline in the
// copilot chat — so the trace looks and reads identically wherever you meet it.

export type TimelineStep = { name: string; status: string; detail?: string };

// normalizeSteps accepts the loosely-typed steps that flow through the copilot
// store (unknown) or the strict ActionStep[] from the API and returns a clean
// list, so a single component serves both callers.
export function normalizeSteps(raw: unknown): TimelineStep[] {
  if (!Array.isArray(raw)) return [];
  const out: TimelineStep[] = [];
  for (const s of raw) {
    if (s && typeof s === 'object') {
      const o = s as Record<string, unknown>;
      const name = typeof o.name === 'string' ? o.name : '';
      if (!name) continue;
      out.push({
        name,
        status: typeof o.status === 'string' ? o.status : 'pending',
        detail: typeof o.detail === 'string' ? o.detail : undefined,
      });
    }
  }
  return out;
}

function stepColor(status: string): string {
  return status === 'ok'
    ? 'var(--rp-tone-green-fg)'
    : status === 'failed'
      ? 'var(--rp-tone-red-fg)'
      : status === 'running'
        ? 'var(--rp-ink-mid)'
        : 'var(--rp-ink-faint)';
}

export function StepTimeline({
  steps,
  className,
}: {
  steps: TimelineStep[];
  className?: string;
}) {
  if (steps.length === 0) return null;
  return (
    <ol className={className}>
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
              ) : s.status === 'compensated' ? (
                <span className="text-[10px] leading-none" style={{ color: 'var(--rp-tone-yellow-fg)' }}>↺</span>
              ) : (
                <span className="block h-2.5 w-2.5 rounded-full border" style={{ borderColor: 'var(--rp-line-strong)' }} />
              )}
            </span>
            <div className="min-w-0 flex-1 leading-snug">
              <span style={{ color: s.status === 'pending' ? 'var(--rp-ink-faint)' : 'var(--rp-ink)' }}>{s.name}</span>
              {s.detail ? <span className="text-faint" title={s.detail}> · {s.detail}</span> : null}
            </div>
          </li>
        );
      })}
    </ol>
  );
}
