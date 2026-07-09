'use client';

import type { ReactNode } from 'react';
import { ConnectClusterButton } from './connect-cluster';

// onboarding.tsx — the first impression when an org has no cluster yet. Not an
// empty page: a dark-cockpit hero that states the value, shows the flagship
// capabilities, and makes connecting feel like one calm minute. RETICLE:
// monochrome chrome, the accent used once (the CTA + a single sparkle), grid +
// aurora as deliberate brand ornament, colorblind-safe glyphs on the cards.

function Sparkle() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" aria-hidden>
      <path d="M12 3l1.8 5.2L19 10l-5.2 1.8L12 17l-1.8-5.2L5 10l5.2-1.8L12 3Z" fill="currentColor" />
    </svg>
  );
}

// Compact capability icons (16px, 1.6 stroke — matches the nav language).
const ICONS: Record<string, ReactNode> = {
  copilot: <Sparkle />,
  map: (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" aria-hidden>
      <circle cx="6" cy="6" r="2.2" stroke="currentColor" strokeWidth="1.6" />
      <circle cx="18" cy="9" r="2.2" stroke="currentColor" strokeWidth="1.6" />
      <circle cx="9" cy="18" r="2.2" stroke="currentColor" strokeWidth="1.6" />
      <path d="M8 6.6l7.8 1.8M7.6 8l1 8M11 18l5.4-7" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
    </svg>
  ),
  telemetry: (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" aria-hidden>
      <path d="M3 15l4-6 4 4 4-8 3 5 3-3" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  ),
  action: (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" aria-hidden>
      <path d="M13 3L4 14h6l-1 7 9-11h-6l1-7Z" stroke="currentColor" strokeWidth="1.5" strokeLinejoin="round" />
    </svg>
  ),
  incident: (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" aria-hidden>
      <path d="M12 3l8 4v5c0 4.6-3.3 8-8 9.6C7.3 20 4 16.6 4 12V7l8-4Z" stroke="currentColor" strokeWidth="1.5" strokeLinejoin="round" />
      <path d="M12 8v4M12 15v.2" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" />
    </svg>
  ),
  team: (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" aria-hidden>
      <circle cx="9" cy="8" r="2.6" stroke="currentColor" strokeWidth="1.5" />
      <path d="M3.5 19c0-3 2.5-5 5.5-5s5.5 2 5.5 5" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
      <path d="M16 7.5a2.4 2.4 0 010 4.6M17.5 19c0-2.4-1-4-2.6-4.7" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
    </svg>
  ),
};

const CAPABILITIES: { key: string; title: string; body: string; glyph: string }[] = [
  {
    key: 'copilot',
    title: 'AI Copilot',
    body: 'Ask what’s wrong. It writes its own PromQL, reads logs, spawns investigators per hypothesis and returns a root-cause verdict with evidence.',
    glyph: '✦',
  },
  {
    key: 'map',
    title: 'Live service map',
    body: 'eBPF-traced topology with golden signals on every edge — see request rates, errors and the blast radius the moment it moves.',
    glyph: '◆',
  },
  {
    key: 'telemetry',
    title: 'Traces · logs · metrics',
    body: 'A full OpenTelemetry pipeline on ClickHouse. Fuzzy + regex log search, real PromQL, RED metrics and p95 per service.',
    glyph: '●',
  },
  {
    key: 'action',
    title: 'Safe remediation',
    body: 'Approval-gated, reversible actions — restart, scale, patch, cordon. Every change captures an automatic rollback before it runs.',
    glyph: '▲',
  },
  {
    key: 'incident',
    title: 'Incidents & on-call',
    body: 'Firing alerts auto-declare incidents, escalate through your channels step by step, and track MTTA / MTTR on a live timeline.',
    glyph: '◆',
  },
  {
    key: 'team',
    title: 'Multi-cluster & teams',
    body: 'Org → cluster → namespace RBAC, invitations, scoped API tokens and a full audit log. Built for teams from the first command.',
    glyph: '○',
  },
];

const STEPS: { n: string; title: string; body: string }[] = [
  { n: '01', title: 'Connect', body: 'Run one command against your cluster — it uses your current kubectl context. No config files.' },
  { n: '02', title: 'It enrolls', body: 'The in-cluster agent streams telemetry outbound-only. No inbound ports, no scraping to configure.' },
  { n: '03', title: 'It lights up', body: 'The service map, traces and Copilot come alive right here — usually in about a minute.' },
];

export function Onboarding({ orgId }: { orgId?: string }) {
  return (
    <div className="relative min-h-[calc(100dvh-52px)] overflow-hidden">
      {/* Deliberate brand ornament — sunrise aurora + module grid, masked to top. */}
      <div className="aurora-glow" aria-hidden />
      <div className="swiss-grid pointer-events-none absolute inset-0" aria-hidden />

      <div className="relative mx-auto max-w-[1080px] px-5 pb-20 pt-14 sm:px-8 sm:pt-20">
        {/* ── Hero ─────────────────────────────────────────────────────────── */}
        <div className="max-w-[760px]">
          <div className="inline-flex items-center gap-1.5 rounded-skin-chip border border-line bg-raised px-2.5 py-1 font-mono text-[10.5px] uppercase tracking-[0.14em] text-muted">
            <span style={{ color: 'var(--rp-accent)' }}>
              <Sparkle />
            </span>
            observability, investigation-first
          </div>

          <h1 className="mt-5 font-display text-[34px] font-bold leading-[1.03] tracking-tightest text-ink sm:text-[46px] lg:text-[54px]">
            Your cluster, fully observed —
            <br className="hidden sm:block" />{' '}
            <span style={{ color: 'var(--rp-accent)' }}>and an AI that investigates.</span>
          </h1>

          <p className="mt-5 max-w-[620px] text-[14px] leading-relaxed text-mid sm:text-[15.5px]">
            Live service maps, traces, logs and metrics in one dark cockpit. When something breaks, the
            Copilot finds the root cause and proposes safe, reversible fixes — you stay in control. One
            command to connect. About a minute.
          </p>

          <div className="mt-8 flex flex-wrap items-center gap-x-4 gap-y-3">
            {orgId ? <ConnectClusterButton orgId={orgId}>Connect your first cluster</ConnectClusterButton> : null}
            <span className="flex flex-wrap items-center gap-x-2 gap-y-1 font-mono text-[11px] text-muted">
              <span className="inline-flex items-center gap-1.5">
                <span className="h-1.5 w-1.5 rounded-full" style={{ background: 'var(--rp-tone-green-fg)' }} />
                one command
              </span>
              <span className="text-faint">·</span>
              <span>~60s</span>
              <span className="text-faint">·</span>
              <span>outbound-only, no inbound ports</span>
            </span>
          </div>
        </div>

        {/* ── Capability showcase ──────────────────────────────────────────── */}
        <div className="mt-14 sm:mt-20">
          <div className="rp-micro pb-3 text-faint">what you get</div>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {CAPABILITIES.map((c) => (
              <div
                key={c.key}
                className="group rounded-skin border border-line bg-raised p-4 transition-colors hover:border-line-strong"
                style={{ boxShadow: 'var(--rp-rim)' }}
              >
                <div className="flex items-center gap-2.5">
                  <span className="grid h-8 w-8 shrink-0 place-items-center rounded-skin-sm border border-line bg-inset text-mid transition-colors group-hover:text-ink">
                    {ICONS[c.key]}
                  </span>
                  <span className="font-mono text-[13px] font-semibold text-ink">{c.title}</span>
                </div>
                <p className="mt-2.5 text-[12.5px] leading-relaxed text-muted">{c.body}</p>
              </div>
            ))}
          </div>
        </div>

        {/* ── How it works ─────────────────────────────────────────────────── */}
        <div className="mt-14 sm:mt-20">
          <div className="rp-micro pb-3 text-faint">how it works</div>
          <div className="grid grid-cols-1 gap-x-6 gap-y-6 sm:grid-cols-3">
            {STEPS.map((s, i) => (
              <div key={s.n} className="relative">
                <div className="flex items-baseline gap-2.5">
                  <span className="font-mono text-[12px] tabular-nums" style={{ color: 'var(--rp-accent)' }}>{s.n}</span>
                  <span className="font-mono text-[13px] font-semibold text-ink">{s.title}</span>
                </div>
                <p className="mt-2 text-[12.5px] leading-relaxed text-muted">{s.body}</p>
                {i < STEPS.length - 1 ? (
                  <span className="absolute -right-3 top-2 hidden text-faint sm:block" aria-hidden>→</span>
                ) : null}
              </div>
            ))}
          </div>
        </div>

        {/* ── Closing CTA ──────────────────────────────────────────────────── */}
        <div className="mt-14 flex flex-wrap items-center justify-between gap-4 rounded-skin border border-line bg-raised px-5 py-4 sm:mt-20" style={{ boxShadow: 'var(--rp-rim)' }}>
          <div className="min-w-0">
            <div className="font-display text-[17px] font-bold tracking-tightest text-ink">Ready when you are.</div>
            <div className="mt-0.5 font-mono text-[11.5px] text-muted">Connect a cluster and this screen becomes your live service map.</div>
          </div>
          {orgId ? <ConnectClusterButton orgId={orgId}>Connect your first cluster</ConnectClusterButton> : null}
        </div>
      </div>
    </div>
  );
}
