import type { Config } from 'tailwindcss';
import { brand, signal, status } from '@rocketplane/ui';

// Tailwind-Theme sitzt auf den @rocketplane/ui Design-Tokens auf:
//  - Flächen/Text/Border als CSS-Variablen (theme-fähig, Dark/Light per --rp-*)
//  - Marken-/Signal-/Status-Farben direkt aus den Tokens (fix, semantisch)
export default {
  content: ['./app/**/*.{ts,tsx}', './components/**/*.{ts,tsx}'],
  darkMode: ['selector', '[data-theme="dark"]'],
  theme: {
    extend: {
      colors: {
        base: 'var(--rp-bg-base)',
        raised: 'var(--rp-bg-raised)',
        overlay: 'var(--rp-bg-overlay)',
        line: 'var(--rp-border)',
        strong: 'var(--rp-text-strong)',
        muted: 'var(--rp-text-muted)',
        faint: 'var(--rp-text-faint)',
        accent: 'var(--rp-accent)',
        brand: { from: brand.from, via: brand.via, to: brand.to },
        signal,
        status,
      },
      fontFamily: {
        sans: ['"Schibsted Grotesk"', 'ui-sans-serif', 'system-ui', 'sans-serif'],
        display: ['"Schibsted Grotesk"', 'ui-sans-serif', 'sans-serif'],
        mono: ['"Geist Mono"', 'ui-monospace', 'SFMono-Regular', 'monospace'],
      },
      letterSpacing: {
        tightest: '-0.04em',
      },
      boxShadow: {
        glow: '0 0 60px -12px rgba(45,212,191,0.35)',
        card: '0 1px 0 0 rgba(255,255,255,0.03) inset, 0 24px 48px -28px rgba(0,0,0,0.7)',
        palette: '0 1px 0 0 rgba(255,255,255,0.04) inset, 0 40px 80px -24px rgba(0,0,0,0.8)',
      },
      backgroundImage: {
        aurora: brand.gradient,
      },
    },
  },
  plugins: [],
} satisfies Config;
