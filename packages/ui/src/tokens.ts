/**
 * rocketplane Design-Tokens — "Nordic Dusk"
 * ─────────────────────────────────────────
 * Single source of truth für die visuelle Sprache. Framework-agnostisch (nur
 * Daten), damit Web-App, Tailwind-Theme, Docs und künftige Surfaces exakt
 * dieselben Werte teilen.
 *
 * Ästhetik: sehr dunkle, kühle Blau-Schwarz-Fläche (Fjord bei Nacht), eine
 * zurückhaltende Aurora (Teal → Sky → Indigo) als einziger Farbakzent,
 * präzise Hairlines. Farbe wird gezielt eingesetzt, nicht gestreut.
 */

/** Health-/Alert-Status — dieselbe Semantik überall (ruhig → warnend → kritisch). */
export const status = {
  resolved: '#64748b', // slate  — alles ok / RESOLVED
  degraded: '#f5b544', // amber  — DEGRADED / Warning
  critical: '#f2555a', // red    — CRITICAL
  unknown: '#3a4453', // gedämpft — keine Daten
} as const;
export type Status = keyof typeof status;

/** Feste Akzentfarbe je Signaltyp — durchgängige Signal-Identität. */
export const signal = {
  traces: '#818cf8', // indigo
  metrics: '#2dd4bf', // teal
  logs: '#38bdf8', // sky
  rum: '#fb7185', // rose (Frontend)
  synthetics: '#fbbf24', // amber (Uptime)
} as const;
export type Signal = keyof typeof signal;

/** Marken-Aurora — der einzige „laute" Moment, sonst sparsam. */
export const brand = {
  from: '#2dd4bf', // teal
  via: '#38bdf8', // sky
  to: '#818cf8', // indigo
  gradient: 'linear-gradient(120deg, #2dd4bf 0%, #38bdf8 50%, #818cf8 100%)',
} as const;

/** Semantische Flächen-/Text-Paletten je Theme. */
export const theme = {
  dark: {
    'bg-base': '#0a0e14', // Fjord-Nacht
    'bg-raised': '#0e131b',
    'bg-overlay': '#131a24',
    border: '#1c2531',
    'text-strong': '#e9eef5',
    'text-muted': '#8a96a7',
    'text-faint': '#515c6b',
    accent: '#2dd4bf',
  },
  light: {
    'bg-base': '#f7f9fb', // Frost
    'bg-raised': '#ffffff',
    'bg-overlay': '#ffffff',
    border: '#e2e8f0',
    'text-strong': '#0a0e14',
    'text-muted': '#4a5566',
    'text-faint': '#8a96a7',
    accent: '#0d9488',
  },
} as const;
export type ThemeName = keyof typeof theme;

/** 4px-Basis-Raster. */
export const space = {
  0: '0',
  1: '4px',
  2: '8px',
  3: '12px',
  4: '16px',
  5: '24px',
  6: '32px',
  8: '48px',
  10: '64px',
} as const;

export const radius = {
  sm: '6px',
  md: '10px',
  lg: '14px',
  xl: '20px',
  full: '9999px',
} as const;

export const font = {
  sans: "'Schibsted Grotesk', ui-sans-serif, system-ui, -apple-system, sans-serif",
  mono: "'Geist Mono', ui-monospace, 'SFMono-Regular', 'Cascadia Code', monospace",
} as const;

export const fontSize = {
  xs: '11px',
  sm: '12px',
  md: '13px',
  lg: '15px',
  xl: '20px',
  '2xl': '28px',
  '3xl': '40px',
  '4xl': '56px',
} as const;

/** Perceived Performance: Motion bewusst knapp halten. */
export const motion = {
  fast: '90ms',
  base: '150ms',
  slow: '260ms',
  easing: 'cubic-bezier(0.2, 0, 0, 1)',
} as const;

export const zIndex = {
  base: 0,
  sticky: 100,
  overlay: 800,
  commandPalette: 900,
  toast: 1000,
} as const;

export const tokens = {
  status,
  signal,
  brand,
  theme,
  space,
  radius,
  font,
  fontSize,
  motion,
  zIndex,
} as const;

export type Tokens = typeof tokens;
