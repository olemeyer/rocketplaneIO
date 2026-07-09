import type { IncidentSeverity, IncidentStatus } from '@/lib/api/types';

// Shared presentation for incidents (RETICLE): severity and lifecycle status
// as color- AND glyph-coded chips (crimson/gold/green/neutral, ▲◆●○), plus
// a compact duration formatter for MTTA/MTTR.

export type Chip = { fg: string; bg: string; glyph: string; label: string };

export const SEVERITY: Record<IncidentSeverity, Chip> = {
  critical: { fg: 'var(--rp-tone-red-fg)', bg: 'var(--rp-tone-red-bg)', glyph: '▲', label: 'critical' },
  high: { fg: 'var(--rp-tone-yellow-fg)', bg: 'var(--rp-tone-yellow-bg)', glyph: '◆', label: 'high' },
  medium: { fg: 'var(--rp-ink-mid)', bg: 'var(--rp-tone-neutral-bg)', glyph: '●', label: 'medium' },
  low: { fg: 'var(--rp-ink-faint)', bg: 'var(--rp-tone-neutral-bg)', glyph: '○', label: 'low' },
};

// open → acknowledged → mitigated → resolved. Crimson (active problem) →
// gold (seen, in progress) → green (impact stopped) → neutral (closed).
export const STATUS: Record<IncidentStatus, Chip> = {
  open: { fg: 'var(--rp-tone-red-fg)', bg: 'var(--rp-tone-red-bg)', glyph: '▲', label: 'open' },
  acknowledged: { fg: 'var(--rp-tone-yellow-fg)', bg: 'var(--rp-tone-yellow-bg)', glyph: '◆', label: 'acknowledged' },
  mitigated: { fg: 'var(--rp-tone-green-fg)', bg: 'var(--rp-tone-green-bg)', glyph: '●', label: 'mitigated' },
  resolved: { fg: 'var(--rp-ink-muted)', bg: 'var(--rp-tone-neutral-bg)', glyph: '○', label: 'resolved' },
};

export const SEVERITY_ORDER: IncidentSeverity[] = ['critical', 'high', 'medium', 'low'];
export const STATUS_ORDER: IncidentStatus[] = ['open', 'acknowledged', 'mitigated', 'resolved'];

// Urgency rank for sorting: open, critical ones first.
export function incidentRank(status: IncidentStatus, severity: IncidentSeverity): number {
  const s = STATUS_ORDER.indexOf(status);
  const sev = SEVERITY_ORDER.indexOf(severity);
  return s * 10 + sev;
}

// fmtDuration: compact duration (MTTA/MTTR, time-since). Seconds → "2h 14m".
export function fmtDuration(seconds: number): string {
  if (!seconds || seconds < 0) return '—';
  const s = Math.round(seconds);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ${m % 60}m`;
  const d = Math.floor(h / 24);
  return `${d}d ${h % 24}h`;
}

// Time span between two ISO timestamps (or until now) in seconds.
export function durationBetween(startIso: string, endIso?: string): number {
  const start = Date.parse(startIso);
  const end = endIso ? Date.parse(endIso) : Date.now();
  if (!start || !end) return 0;
  return Math.max(0, (end - start) / 1000);
}
