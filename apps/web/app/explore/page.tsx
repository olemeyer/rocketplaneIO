import type { Metadata } from 'next';
import { AppShell } from '@/components/explore/app-shell';

export const metadata: Metadata = {
  title: 'Explore — rocketplane',
  description: 'Live service health and distributed traces.',
};

export default function ExplorePage() {
  return (
    <div className="grain relative min-h-screen">
      <div className="aurora opacity-50" aria-hidden />

      <header className="sticky top-0 z-[100] border-b border-line frost backdrop-blur-md">
        <div className="mx-auto flex h-14 max-w-6xl items-center gap-3 px-4">
          <a href="/" className="flex items-center gap-2.5">
            <span className="beacon h-6 w-6 rounded-[7px]" aria-hidden />
            <span className="font-display text-[15px] font-semibold tracking-tight text-strong">
              rocketplane
            </span>
          </a>
          <span className="font-mono text-[12px] text-faint">/ explore</span>
          <span className="ml-auto flex items-center gap-1.5 rounded-md border border-line bg-raised px-2.5 py-1 font-mono text-[11px] text-muted">
            <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-accent" /> live
          </span>
        </div>
      </header>

      <main className="relative mx-auto max-w-6xl px-4 pb-16 pt-5">
        <AppShell />
      </main>
    </div>
  );
}
