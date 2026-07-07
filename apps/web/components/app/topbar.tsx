'use client';

import { ThemeSwitcher } from '@/components/ui';
import { CommandPalette, CommandTrigger } from './command-palette';
import { ScopeSelector } from './scope-selector';
import { UserMenu } from './user-menu';
import { useNav } from './sidebar';
import { CopilotButton } from './copilot';

// Globale Kopfleiste — die Instrumenten-Blende: h-52, transluzent + backdrop-blur,
// EINE 1px-Hairline unten (kein harter Strich mehr). Links mobil der Nav-Hamburger
// (öffnet den Drawer) + Scope-Breadcrumb, rechts Theme + User. Rim-Light oben.
export function Topbar() {
  const { setOpen } = useNav();
  return (
    <header
      className="sticky top-0 z-30 flex h-[52px] items-center justify-between gap-2 border-b border-line px-3 backdrop-blur-md sm:gap-3 sm:px-4 md:px-6"
      style={{
        background: 'color-mix(in oklab, var(--rp-base) 80%, transparent)',
        boxShadow: 'var(--rp-rim)',
      }}
    >
      <div className="flex min-w-0 items-center gap-2">
        {/* Nav-Hamburger — nur mobil (Sidebar ist dort ausgeblendet) */}
        <button
          type="button"
          onClick={() => setOpen(true)}
          aria-label="Open navigation"
          className="rp-focus -ml-1 inline-flex h-9 w-9 shrink-0 items-center justify-center rounded-skin-sm text-mid transition-colors hover:bg-hover hover:text-ink md:hidden"
        >
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" aria-hidden>
            <path d="M3.5 6.5h17M3.5 12h17M3.5 17.5h17" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" />
          </svg>
        </button>
        <ScopeSelector />
      </div>
      <div className="flex items-center gap-1.5 sm:gap-2">
        {/* Primärfeature — der AI-Copilot, prominent */}
        <CopilotButton />
        <span className="mx-0.5 hidden h-4 w-px bg-line sm:block" aria-hidden />
        <div className="hidden sm:block">
          <CommandTrigger />
        </div>
        <span className="mx-1 hidden h-4 w-px bg-line md:block" aria-hidden />
        <ThemeSwitcher />
        <span className="mx-1 hidden h-4 w-px bg-line sm:block" aria-hidden />
        <UserMenu />
      </div>
      <CommandPalette />
    </header>
  );
}
