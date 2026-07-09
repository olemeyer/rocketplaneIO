import { cn } from '@/lib/cn';

// rocketplaneIO brand mark — a single clean signal-orange glyph (an upward
// navigation chevron / rocket nose) plus a monochrome wordmark. The mark IS the
// one warm signal; everything else stays graphite. Ported 1:1 from the marketing
// site so the app and the landing page share the exact same identity.

const GLYPH = 'M9 23 L16 7 L23 23 L16 19 Z';
const SIGNAL = '#ff6a3d'; // signal-orange

// Logo — the bare mark. Signal-orange, transparent background; drop it straight
// into a lockup. For a filled app-icon tile (favicon, avatars) use LogoTile.
export function Logo({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 32 32" className={className} role="img" aria-label="rocketplaneIO">
      <path d={GLYPH} fill={SIGNAL} />
    </svg>
  );
}

// LogoTile — the mark inside a graphite rounded case, for standalone app-icon
// contexts (matches the favicon). Mode-independent (graphite stays graphite).
export function LogoTile({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 32 32" className={className} role="img" aria-label="rocketplaneIO">
      <rect width="32" height="32" rx="7" fill="#0b0b0d" />
      <rect x="0.5" y="0.5" width="31" height="31" rx="6.5" fill="none" stroke="rgba(255,255,255,0.10)" />
      <path d={GLYPH} fill={SIGNAL} />
    </svg>
  );
}

// Wordmark — "rocketplane" + a muted "IO" that reads as one word.
export function Wordmark({ className }: { className?: string }) {
  return (
    <span className={cn('font-display text-[14px] font-bold tracking-tightest text-ink', className)}>
      rocketplane<span className="font-semibold" style={{ color: 'var(--rp-ink-muted)' }}>IO</span>
    </span>
  );
}

// Combined lockup (mark + wordmark) for the header/sidebar — the landing lockup.
export function Lockup({ className, logoClass }: { className?: string; logoClass?: string }) {
  return (
    <span className={cn('inline-flex items-center gap-2', className)}>
      <Logo className={cn('h-[18px] w-[18px]', logoClass)} />
      <Wordmark />
    </span>
  );
}
