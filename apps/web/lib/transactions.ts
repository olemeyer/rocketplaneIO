import type { MCPTransactionStatus } from '@/lib/api/types';

// Shared presentation for MCP transactions (RETICLE): lifecycle status as
// color- AND glyph-coded chips. Healthy states stay calm (green/neutral),
// gold marks in-flight rollback, crimson only the real anomaly
// (rollback_failed). Live-pulse is reserved for the OPEN state (green =
// operating, same family as "connected").

export type TxnChip = { fg: string; bg: string; glyph: string; label: string; pulse?: boolean };

export const TXN_STATUS: Record<MCPTransactionStatus, TxnChip> = {
  open: { fg: 'var(--rp-tone-green-fg)', bg: 'var(--rp-tone-green-bg)', glyph: '●', label: 'open', pulse: true },
  committed: { fg: 'var(--rp-ink)', bg: 'var(--rp-tone-neutral-bg)', glyph: '✓', label: 'committed' },
  cancelling: { fg: 'var(--rp-tone-yellow-fg)', bg: 'var(--rp-tone-yellow-bg)', glyph: '◆', label: 'cancelling', pulse: true },
  rolled_back: { fg: 'var(--rp-ink-muted)', bg: 'var(--rp-tone-neutral-bg)', glyph: '○', label: 'rolled back' },
  rollback_failed: { fg: 'var(--rp-tone-red-fg)', bg: 'var(--rp-tone-red-bg)', glyph: '▲', label: 'rollback failed' },
};

export const TXN_FALLBACK: TxnChip = { fg: 'var(--rp-ink-muted)', bg: 'var(--rp-tone-neutral-bg)', glyph: '○', label: 'unknown' };

export function txnChip(status: string): TxnChip {
  return TXN_STATUS[status as MCPTransactionStatus] ?? { ...TXN_FALLBACK, label: status.replace(/_/g, ' ') };
}

/** Countdown to the deadline: "12m 04s" / "2h 14m" / "expired". Mono+tabular. */
export function deadlineCountdown(deadlineIso: string, nowMs = Date.now()): string {
  const t = Date.parse(deadlineIso);
  if (!t) return '—';
  const s = Math.floor((t - nowMs) / 1000);
  if (s <= 0) return 'expired';
  if (s < 3600) return `${Math.floor(s / 60)}m ${String(s % 60).padStart(2, '0')}s`;
  const h = Math.floor(s / 3600);
  if (h < 24) return `${h}h ${Math.floor((s % 3600) / 60)}m`;
  return `${Math.floor(h / 24)}d ${h % 24}h`;
}
