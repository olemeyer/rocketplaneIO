import type { ReactNode } from 'react';
import { cn } from '@/lib/cn';
import { Badge, type BadgeTone } from './primitives';

// Semantische Statussprache (Multi-Skin). Farbe = Bedeutung. Die Health-Marker-FORM
// ist skin-abhängig (Swiss: geometrisch/eckig · Aurora: runder Punkt) und wird per
// CSS über [data-skin] gesteuert (siehe unten), damit es auch in Server-Komponenten
// funktioniert.

/* ── Health ─────────────────────────────────────────────────────────────── */

export type Health = 'healthy' | 'degraded' | 'critical' | 'unknown';

const HEALTH_LABEL: Record<Health, string> = {
  healthy: 'Healthy',
  degraded: 'Degraded',
  critical: 'Critical',
  unknown: 'No data',
};

const HEALTH_COLOR: Record<Health, string> = {
  healthy: 'var(--rp-green)',
  degraded: 'var(--rp-yellow)',
  critical: 'var(--rp-red)',
  unknown: 'var(--rp-ink-faint)',
};

const HEALTH_TONE: Record<Health, BadgeTone> = {
  healthy: 'green',
  degraded: 'yellow',
  critical: 'red',
  unknown: 'outline',
};

/** Geometrischer/runder Health-Marker; Form je Skin via CSS-Klasse `rp-mark`. */
export function HealthMark({ health, className }: { health: Health; className?: string }) {
  return (
    <span
      className={cn('rp-mark inline-block h-2.5 w-2.5 rounded-skin-sm', className)}
      style={{ backgroundColor: HEALTH_COLOR[health] }}
      aria-label={HEALTH_LABEL[health]}
    />
  );
}

export function HealthTag({ health }: { health: Health }) {
  const textColor =
    health === 'critical'
      ? 'text-red'
      : health === 'degraded'
        ? 'text-yellow'
        : health === 'healthy'
          ? 'text-green'
          : 'text-muted';
  return (
    <span className="inline-flex items-center gap-1.5">
      <HealthMark health={health} />
      <span className={cn('rp-label font-mono text-[11px]', textColor)}>{HEALTH_LABEL[health]}</span>
    </span>
  );
}

/* ── Online-/OK-Puls (Standard für gesunde Verbindungen) ──────────────────── */

// Pulsierender grüner Punkt — der einheitliche „läuft / OK / online"-Indikator.
// Überall einsetzen, wo eine Verbindung/Komponente gesund und aktiv ist.
export function OnlineDot({ className, size = 8 }: { className?: string; size?: number }) {
  return (
    <span className={cn('relative inline-flex', className)} aria-label="online" style={{ width: size, height: size }}>
      <span
        className="absolute inline-flex h-full w-full animate-ping rounded-full"
        style={{ backgroundColor: 'var(--rp-green)', opacity: 0.55 }}
      />
      <span
        className="relative inline-flex h-full w-full rounded-full"
        style={{ backgroundColor: 'var(--rp-green)' }}
      />
    </span>
  );
}

/* ── Connection-Status (Cluster) ──────────────────────────────────────────── */

export type ConnStatus = 'pending' | 'connected' | 'stale';

const CONN_META: Record<ConnStatus, { tone: BadgeTone; label: string }> = {
  connected: { tone: 'green', label: 'Connected' },
  pending: { tone: 'yellow', label: 'Pending' },
  stale: { tone: 'red', label: 'Stale' },
};

export function StatusBadge({ status }: { status: ConnStatus }) {
  const m = CONN_META[status] ?? CONN_META.pending;
  // Gesunde Verbindung: pulsierender grüner Punkt als Standard-„OK"-Signal.
  return (
    <Badge tone={m.tone}>
      {status === 'connected' ? <OnlineDot size={7} className="mr-0.5" /> : null}
      {m.label}
    </Badge>
  );
}

/* ── Severity (Log-Level) ─────────────────────────────────────────────────── */

export type Severity = 'error' | 'fatal' | 'warn' | 'info' | 'debug' | 'trace' | 'unknown';

const SEVERITY_META: Record<Severity, { tone: BadgeTone; label: string }> = {
  fatal: { tone: 'red', label: 'FATAL' },
  error: { tone: 'red', label: 'ERROR' },
  warn: { tone: 'yellow', label: 'WARN' },
  info: { tone: 'blue', label: 'INFO' },
  debug: { tone: 'outline', label: 'DEBUG' },
  trace: { tone: 'outline', label: 'TRACE' },
  unknown: { tone: 'outline', label: '—' },
};

export function toSeverity(text?: string, number?: number): Severity {
  const t = (text ?? '').toLowerCase();
  if (t.startsWith('fatal') || (number ?? 0) >= 21) return 'fatal';
  if (t.startsWith('err') || ((number ?? 0) >= 17 && (number ?? 0) < 21)) return 'error';
  if (t.startsWith('warn') || ((number ?? 0) >= 13 && (number ?? 0) < 17)) return 'warn';
  if (t.startsWith('info') || ((number ?? 0) >= 9 && (number ?? 0) < 13)) return 'info';
  if (t.startsWith('debug') || ((number ?? 0) >= 5 && (number ?? 0) < 9)) return 'debug';
  if (t.startsWith('trace') || ((number ?? 0) >= 1 && (number ?? 0) < 5)) return 'trace';
  return 'unknown';
}

export function SeverityBadge({ severity }: { severity: Severity }) {
  return <Badge tone={SEVERITY_META[severity].tone}>{SEVERITY_META[severity].label}</Badge>;
}

export function SeverityBar({ severity, className }: { severity: Severity; className?: string }) {
  const color =
    severity === 'error' || severity === 'fatal'
      ? 'var(--rp-red)'
      : severity === 'warn'
        ? 'var(--rp-yellow)'
        : severity === 'info'
          ? 'var(--rp-blue)'
          : 'var(--rp-line)';
  return <span className={cn('block w-[3px] shrink-0', className)} style={{ backgroundColor: color }} />;
}

/* ── Fehlerquote ──────────────────────────────────────────────────────────── */

export function ErrorRatioText({ ratio }: { ratio: number }): ReactNode {
  const pct = (ratio * 100).toFixed(ratio >= 0.1 ? 0 : 2);
  const tone = ratio > 0.02 ? 'text-red' : ratio > 0.005 ? 'text-yellow' : 'text-muted';
  return <span className={cn('font-mono tnum', tone)}>{pct}%</span>;
}
