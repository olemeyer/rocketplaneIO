import type { Config } from 'tailwindcss';

// Tailwind-Theme für das Multi-Skin-System (swiss + aurora). Alle Werte sitzen auf
// den --rp-*-CSS-Variablen (skin×mode); Radius/Shadow sind variablen-getrieben,
// damit dieselben Primitives beide Skins bedienen. Siehe app/globals.css.
export default {
  content: ['./app/**/*.{ts,tsx}', './components/**/*.{ts,tsx}'],
  darkMode: ['selector', '[data-theme="dark"]'],
  theme: {
    extend: {
      colors: {
        // Flächen
        paper: 'var(--rp-base)',
        base: 'var(--rp-base)',
        raised: 'var(--rp-raised)',
        overlay: 'var(--rp-overlay)',
        inset: 'var(--rp-inset)',
        hover: 'var(--rp-hover)',
        active: 'var(--rp-active)',
        // Text (ink)
        ink: {
          DEFAULT: 'var(--rp-ink)',
          mid: 'var(--rp-ink-mid)',
          muted: 'var(--rp-ink-muted)',
          faint: 'var(--rp-ink-faint)',
        },
        strong: 'var(--rp-ink)',
        mid: 'var(--rp-ink-mid)',
        muted: 'var(--rp-ink-muted)',
        faint: 'var(--rp-ink-faint)',
        // Rules
        line: 'var(--rp-line)',
        'line-strong': 'var(--rp-line-strong)',
        // Interaktion
        accent: {
          DEFAULT: 'var(--rp-accent)',
          fg: 'var(--rp-accent-fg)',
          hover: 'var(--rp-accent-hover)',
        },
        // Bedeutungsfarben (roh, für Text/Marker)
        red: { DEFAULT: 'var(--rp-red)' },
        yellow: { DEFAULT: 'var(--rp-yellow)' },
        blue: { DEFAULT: 'var(--rp-blue)' },
        green: { DEFAULT: 'var(--rp-green)' },
        // Tone-Paare (Badges/Status: Skin entscheidet solid vs. soft)
        'tone-red-bg': 'var(--rp-tone-red-bg)',
        'tone-red-fg': 'var(--rp-tone-red-fg)',
        'tone-yellow-bg': 'var(--rp-tone-yellow-bg)',
        'tone-yellow-fg': 'var(--rp-tone-yellow-fg)',
        'tone-blue-bg': 'var(--rp-tone-blue-bg)',
        'tone-blue-fg': 'var(--rp-tone-blue-fg)',
        'tone-green-bg': 'var(--rp-tone-green-bg)',
        'tone-green-fg': 'var(--rp-tone-green-fg)',
        'tone-neutral-bg': 'var(--rp-tone-neutral-bg)',
        'tone-neutral-fg': 'var(--rp-tone-neutral-fg)',
        'tone-accent-bg': 'var(--rp-tone-accent-bg)',
        'tone-accent-fg': 'var(--rp-tone-accent-fg)',
        // Primary-Button
        'btn-bg': 'var(--rp-btn-bg)',
        'btn-fg': 'var(--rp-btn-fg)',
        'btn-hover-bg': 'var(--rp-btn-hover-bg)',
        'btn-hover-fg': 'var(--rp-btn-hover-fg)',
      },
      borderRadius: {
        none: '0',
        skin: 'var(--rp-radius)',
        'skin-sm': 'var(--rp-radius-sm)',
        'skin-chip': 'var(--rp-radius-chip)',
        'skin-lg': 'var(--rp-radius-lg)',
        full: '9999px',
      },
      boxShadow: {
        card: 'var(--rp-shadow-card)',
        pop: 'var(--rp-shadow-pop)',
      },
      fontFamily: {
        sans: ['Archivo', 'ui-sans-serif', 'system-ui', 'sans-serif'],
        display: ['Archivo', 'ui-sans-serif', 'sans-serif'],
        mono: ['"IBM Plex Mono"', 'ui-monospace', 'SFMono-Regular', 'monospace'],
      },
      letterSpacing: {
        tightest: '-0.02em',
        label: '0.06em',
      },
    },
  },
  plugins: [],
} satisfies Config;
