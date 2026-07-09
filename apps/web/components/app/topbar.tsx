'use client';

import { ThemeSwitcher } from '@/components/ui';
import { CommandPalette, CommandTrigger } from './command-palette';
import { ScopeSelector } from './scope-selector';
import { UserMenu } from './user-menu';
import { useNav } from './sidebar';
import { CopilotButton } from './copilot';

// Global top bar — the instrument bezel: h-52, translucent + backdrop-blur, ONE
// 1px hairline at the bottom (no hard rule). On mobile the left holds the nav
// hamburger (opens the drawer) + scope breadcrumb; the right holds theme + user.
// Rim light on top.
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
        {/* Primary feature — the AI Copilot, kept prominent at every width. */}
        <CopilotButton />
        {/* Search only appears at lg+; on tablet the 236px sidebar leaves too
            little room, so it would collide with the scope + Copilot. */}
        <span className="mx-0.5 hidden h-4 w-px bg-line lg:block" aria-hidden />
        <div className="hidden lg:block">
          <CommandTrigger />
        </div>
        <span className="mx-1 hidden h-4 w-px bg-line sm:block" aria-hidden />
        <ThemeSwitcher />
        <span className="mx-1 hidden h-4 w-px bg-line sm:block" aria-hidden />
        <UserMenu />
      </div>
      <CommandPalette />
    </header>
  );
}
