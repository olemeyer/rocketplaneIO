'use client';

import { useTheme } from '@/lib/theme/theme-provider';
import { cn } from '@/lib/cn';

// Dark/Light-Umschalter (ein Design, zwei Modi). Halb gefüllter Kreis als Marker.
export function ThemeSwitcher({ className }: { className?: string }) {
  const { mode, toggleMode } = useTheme();
  const isDark = mode === 'dark';
  return (
    <button
      type="button"
      onClick={toggleMode}
      aria-label={isDark ? 'Switch to light' : 'Switch to dark'}
      title={isDark ? 'Light' : 'Dark'}
      className={cn(
        'inline-flex h-7 w-7 items-center justify-center rounded-skin-sm border border-line text-ink transition-colors hover:border-line-strong rp-focus',
        className,
      )}
    >
      <svg width="14" height="14" viewBox="0 0 16 16" aria-hidden>
        <circle cx="8" cy="8" r="6.5" fill="none" stroke="currentColor" strokeWidth="1.4" />
        <path d="M8 1.5 A6.5 6.5 0 0 1 8 14.5 Z" fill="currentColor" />
      </svg>
    </button>
  );
}

// Alias für bestehende Aufrufer.
export function ThemeToggle({ className }: { className?: string }) {
  return <ThemeSwitcher className={className} />;
}
