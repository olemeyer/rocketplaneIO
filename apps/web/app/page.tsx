'use client';

import { useEffect, useState } from 'react';
import type { CSSProperties } from 'react';
import { AppShellMock } from '../components/app-shell-mock';
import { CommandPalette } from '../components/command-palette';
import { ArrowRight, Bolt, Github, Layers, Shield } from '../components/icons';

const NAV_LINKS = ['Product', 'Docs', 'Changelog', 'Pricing'];

const WORKS_WITH = ['OpenTelemetry', 'OTLP', 'PromQL', 'Perses', 'Prometheus'];

const FEATURES = [
  {
    icon: Bolt,
    title: 'Keyboard-first',
    body: 'Command palette, remappable keys and a discoverable ⌘-overlay. Navigate, filter and query production without ever touching the mouse.',
  },
  {
    icon: Layers,
    title: 'OpenTelemetry-native',
    body: 'OTLP in, PromQL out — over metrics, logs, spans and web events. No translation layer, no proprietary schema, no lock-in.',
  },
  {
    icon: Shield,
    title: 'Self-hostable',
    body: 'Your cluster, your data, your rules. Docker-compose quickstart, Helm chart, air-gapped-ready. Apache-2.0, forever.',
  },
];

// leichte, gestaffelte Reveal-Verzögerung
const delay = (ms: number): CSSProperties => ({ animationDelay: `${ms}ms` });

export default function Page() {
  const [paletteOpen, setPaletteOpen] = useState(false);

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault();
        setPaletteOpen((o) => !o);
      }
    }
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  return (
    <div className="grain relative min-h-screen overflow-x-hidden">
      {/* Atmosphäre */}
      <div className="aurora" aria-hidden />
      <div className="pointer-events-none absolute inset-0 h-[80vh] grid-fade" aria-hidden />

      {/* Navigation */}
      <header className="sticky top-0 z-[100] border-b border-line frost backdrop-blur-md">
        <nav className="mx-auto flex h-14 max-w-6xl items-center gap-6 px-6">
          <a href="#top" className="flex items-center gap-2.5">
            <span className="beacon h-6 w-6 rounded-[7px]" aria-hidden />
            <span className="font-display text-[15px] font-semibold tracking-tight text-strong">
              rocketplane
            </span>
          </a>
          <div className="ml-2 hidden items-center gap-6 md:flex">
            {NAV_LINKS.map((l) => (
              <a key={l} href="#" className="text-[13px] text-muted transition-colors hover:text-strong">
                {l}
              </a>
            ))}
          </div>
          <div className="ml-auto flex items-center gap-3">
            <a
              href="#"
              aria-label="GitHub"
              className="flex items-center gap-1.5 rounded-md border border-line px-2.5 py-1.5 text-[12px] text-muted transition-colors hover:text-strong"
            >
              <Github className="h-4 w-4" />
              <span className="font-mono">4.8k</span>
            </a>
            <a href="#" className="hidden text-[13px] text-muted transition-colors hover:text-strong sm:block">
              Sign in
            </a>
            <a
              href="#"
              className="rounded-md bg-accent px-3 py-1.5 text-[13px] font-medium text-[#04140f] transition-[filter] hover:brightness-110"
            >
              Start free
            </a>
          </div>
        </nav>
      </header>

      <main id="top" className="relative mx-auto max-w-6xl px-6">
        {/* Hero */}
        <section className="pt-20 pb-14 text-center sm:pt-28">
          <div
            className="reveal mx-auto mb-5 flex w-fit items-center gap-2 rounded-full border border-line bg-raised px-3 py-1"
            style={delay(0)}
          >
            <span className="h-1.5 w-1.5 rounded-full bg-accent" />
            <span className="font-mono text-[11px] uppercase tracking-wider text-muted">
              Open source · OpenTelemetry-native
            </span>
          </div>

          <h1
            className="reveal mx-auto max-w-3xl font-display text-[40px] font-semibold leading-[1.04] tracking-tightest text-strong sm:text-[58px]"
            style={delay(70)}
          >
            Observability at the <span className="text-aurora">speed of thought</span>.
          </h1>

          <p
            className="reveal mx-auto mt-6 max-w-xl text-[15px] leading-relaxed text-muted sm:text-base"
            style={delay(140)}
          >
            rocketplane unifies traces, metrics, logs, RUM and synthetics on one
            keyboard-first canvas. Self-hostable, EU-sovereign, no lock-in.
          </p>

          <div className="reveal mt-8 flex items-center justify-center gap-3" style={delay(210)}>
            <a
              href="#"
              className="group flex items-center gap-2 rounded-lg bg-accent px-4 py-2.5 text-[14px] font-medium text-[#04140f] transition-[filter] hover:brightness-110"
            >
              Start free
              <ArrowRight className="h-4 w-4 transition-transform group-hover:translate-x-0.5" />
            </a>
            <button
              onClick={() => setPaletteOpen(true)}
              className="flex items-center gap-2 rounded-lg border border-line bg-raised px-4 py-2.5 text-[14px] text-strong transition-colors hover:bg-overlay"
            >
              Explore the demo
              <kbd className="rounded border border-line bg-overlay px-1.5 py-0.5 font-mono text-[11px] text-faint">
                ⌘K
              </kbd>
            </button>
          </div>

          <div
            className="reveal mt-14 flex flex-wrap items-center justify-center gap-x-5 gap-y-2"
            style={delay(300)}
          >
            <span className="font-mono text-[10px] uppercase tracking-wider text-faint">Works with</span>
            {WORKS_WITH.map((w) => (
              <span key={w} className="font-mono text-[12px] text-muted">
                {w}
              </span>
            ))}
          </div>
        </section>

        {/* Showpiece: die App-Shell */}
        <section className="reveal relative pb-24" style={delay(380)}>
          <div className="pointer-events-none absolute -inset-x-8 -top-8 bottom-16 rounded-[32px] bg-aurora opacity-[0.06] blur-2xl" />
          <div className="relative shadow-glow">
            <AppShellMock />
          </div>
        </section>

        {/* Feature-Trio */}
        <section className="border-t border-line py-20">
          <div className="grid gap-4 md:grid-cols-3">
            {FEATURES.map(({ icon: Icon, title, body }) => (
              <div
                key={title}
                className="group rounded-xl border border-line bg-raised p-5 transition-colors hover:bg-overlay"
              >
                <div className="mb-4 flex h-9 w-9 items-center justify-center rounded-lg border border-line bg-base text-accent">
                  <Icon className="h-[18px] w-[18px]" />
                </div>
                <h3 className="mb-1.5 font-display text-[15px] font-semibold text-strong">{title}</h3>
                <p className="text-[13px] leading-relaxed text-muted">{body}</p>
              </div>
            ))}
          </div>
        </section>
      </main>

      {/* Footer */}
      <footer className="border-t border-line">
        <div className="mx-auto flex max-w-6xl flex-col items-center justify-between gap-3 px-6 py-8 sm:flex-row">
          <div className="flex items-center gap-2.5">
            <span className="beacon h-5 w-5 rounded-md" aria-hidden />
            <span className="font-display text-[13px] font-semibold text-strong">rocketplane</span>
            <span className="font-mono text-[11px] text-faint">· Apache-2.0</span>
          </div>
          <div className="flex items-center gap-5 font-mono text-[12px] text-muted">
            <a href="#" className="transition-colors hover:text-strong">Docs</a>
            <a href="#" className="transition-colors hover:text-strong">GitHub</a>
            <a href="#" className="transition-colors hover:text-strong">Changelog</a>
            <span className="text-faint">Built for engineers, in the open.</span>
          </div>
        </div>
      </footer>

      <CommandPalette open={paletteOpen} onClose={() => setPaletteOpen(false)} />
    </div>
  );
}
