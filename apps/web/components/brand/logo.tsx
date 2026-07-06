import { cn } from '@/lib/cn';

// rocketplaneIO — App-Icon im RETICLE-Stil: Graphit-Gehäuse-Kachel (Instrument,
// nicht Marketing), weiße Ogive-Rakete, und der Heartbeat als Signal-Orange-
// Kontrollleuchte. Die Marke IST das Designsystem: monochromes Gehäuse, ein
// warmes Signal. Mode-unabhängig (Graphit bleibt Graphit).

const CASE_A = '#1a1a1e'; // Gehäuse oben
const CASE_B = '#0e0e11'; // Gehäuse unten
const SIGNAL = '#ff6a3d'; // Signal-Orange (Kontrollleuchte)

export function Logo({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 32 32" className={className} role="img" aria-label="rocketplaneIO">
      <defs>
        <linearGradient id="rp-logo-case" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0" stopColor={CASE_A} />
          <stop offset="1" stopColor={CASE_B} />
        </linearGradient>
      </defs>
      <rect width="32" height="32" rx="7" fill="url(#rp-logo-case)" />
      {/* Rim-Light an der Oberkante des Gehäuses */}
      <rect x="0.5" y="0.5" width="31" height="31" rx="6.5" fill="none" stroke="rgba(255,255,255,0.09)" strokeWidth="1" />
      {/* Rakete — sleeke Ogive-Silhouette (weiß) */}
      <path
        fill="#f4f4f5"
        d="M16 4.6 C18.9 7 20.1 10.6 20.1 14 L20.1 17.4 C20.1 18.8 19.1 19.8 17.7 19.8 L14.3 19.8 C12.9 19.8 11.9 18.8 11.9 17.4 L11.9 14 C11.9 10.6 13.1 7 16 4.6 Z"
      />
      {/* geschwungene Finnen (weiß) */}
      <path fill="#f4f4f5" d="M11.9 15 C9.6 16 8.7 18.2 9 20.6 L11.9 18.7 Z" />
      <path fill="#f4f4f5" d="M20.1 15 C22.4 16 23.3 18.2 23 20.6 L20.1 18.7 Z" />
      {/* Bullauge (Gehäuse-Ton) */}
      <circle cx="16" cy="12.4" r="1.7" fill={CASE_B} />
      {/* Heartbeat — die Signal-Orange-Kontrollleuchte */}
      <path
        d="M7.5 25 H12 L13.5 23 L16 27.2 L18.5 23.4 L19.6 25 H24.5"
        stroke={SIGNAL}
        strokeWidth="1.6"
        strokeLinecap="round"
        strokeLinejoin="round"
        fill="none"
      />
    </svg>
  );
}

// Wortmarke: „rocketplane" + „I/O"-Chip — das „IO" im Namen wird zum Input/Output-
// Signet (der „/" trägt die eine Signal-Kontrastfarbe). Chip skaliert relativ (em).
export function Wordmark({ className }: { className?: string }) {
  return (
    <span className={cn('inline-flex items-center gap-1.5 font-display text-[14px] font-bold tracking-tightest text-ink', className)}>
      rocketplane
      <span
        className="inline-flex items-center rounded-[3px] border px-1 py-px font-mono text-[0.68em] font-semibold leading-none tracking-[0.06em]"
        style={{ borderColor: 'var(--rp-line-strong)', color: 'var(--rp-ink-mid)' }}
      >
        I<span style={{ color: 'var(--rp-accent)' }}>/</span>O
      </span>
    </span>
  );
}

// Kombiniertes Lockup (App-Icon + Wortmarke) für Header/Sidebar.
export function Lockup({ className, logoClass }: { className?: string; logoClass?: string }) {
  return (
    <span className={cn('inline-flex items-center gap-2.5', className)}>
      <Logo className={cn('h-7 w-7 rounded-skin-sm', logoClass)} />
      <Wordmark />
    </span>
  );
}
