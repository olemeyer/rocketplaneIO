import type { ButtonHTMLAttributes, HTMLAttributes, ReactNode } from 'react';
import { cn } from '@/lib/cn';

// UI-Primitive-Set (Multi-Skin). Alle Persönlichkeit (Radius, Schatten, Rahmen,
// Label-Typo, Tone-Stil) kommt aus --rp-*-Tokens → dieselben Primitives bedienen
// swiss (hart) UND aurora (weich). Siehe app/globals.css.

/* ── Panel ────────────────────────────────────────────────────────────────── */

export function Panel({ className, ...props }: HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn('rounded-skin bg-raised', className)}
      style={{
        border: 'var(--rp-panel-border-width) solid var(--rp-panel-border)',
        boxShadow: 'var(--rp-rim), var(--rp-shadow-card)',
      }}
      {...props}
    />
  );
}

export function PanelHeader({
  title,
  subtitle,
  actions,
  index,
  className,
}: {
  title: ReactNode;
  subtitle?: ReactNode;
  actions?: ReactNode;
  index?: string;
  className?: string;
}) {
  return (
    <div
      className={cn('flex items-center justify-between gap-3 px-4 py-2.5', className)}
      style={{ borderBottom: 'var(--rp-header-rule-width) solid var(--rp-panel-border)' }}
    >
      <div className="flex min-w-0 items-baseline gap-2.5">
        {index ? <span className="font-mono text-[10px] text-faint">{index}</span> : null}
        <div className="min-w-0">
          <div className="truncate text-[13px] font-medium tracking-[-0.006em] text-ink">
            {title}
          </div>
          {subtitle ? (
            <div className="mt-0.5 truncate font-mono text-[11px] text-muted">{subtitle}</div>
          ) : null}
        </div>
      </div>
      {actions ? <div className="flex shrink-0 items-center gap-2">{actions}</div> : null}
    </div>
  );
}

export function PanelBody({ className, ...props }: HTMLAttributes<HTMLDivElement>) {
  return <div className={cn('p-4', className)} {...props} />;
}

/* ── Button ───────────────────────────────────────────────────────────────── */

type ButtonVariant = 'primary' | 'default' | 'ghost' | 'danger';
type ButtonSize = 'sm' | 'md';

const BUTTON_BASE =
  'rp-label inline-flex select-none items-center justify-center gap-1.5 rounded-skin-sm transition-colors ' +
  // Disabled klar als inert lesbar (inset-Fläche + faint) statt verwaschenem
  // 40%-Primary — sonst liest der CTA sich als „kaputt".
  'rp-focus disabled:cursor-not-allowed disabled:border disabled:border-line disabled:bg-inset disabled:text-faint';

const BUTTON_VARIANT: Record<ButtonVariant, string> = {
  primary: 'bg-btn-bg text-btn-fg hover:bg-btn-hover-bg hover:text-btn-hover-fg',
  default: 'border border-line bg-transparent text-ink hover:bg-hover',
  ghost: 'text-muted hover:bg-hover hover:text-ink',
  danger: 'bg-tone-red-bg text-tone-red-fg hover:opacity-90',
};

const BUTTON_SIZE: Record<ButtonSize, string> = {
  sm: 'h-7 px-2.5 text-[11px]',
  md: 'h-9 px-4 text-[12px]',
};

export function Button({
  variant = 'default',
  size = 'md',
  className,
  ...props
}: ButtonHTMLAttributes<HTMLButtonElement> & { variant?: ButtonVariant; size?: ButtonSize }) {
  return (
    <button
      className={cn(BUTTON_BASE, BUTTON_VARIANT[variant], BUTTON_SIZE[size], className)}
      {...props}
    />
  );
}

export function IconButton({
  className,
  label,
  ...props
}: ButtonHTMLAttributes<HTMLButtonElement> & { label: string }) {
  return (
    <button
      aria-label={label}
      title={label}
      className={cn(
        'inline-flex h-7 w-7 items-center justify-center rounded-skin-sm border border-line text-muted transition-colors',
        'hover:border-line-strong hover:text-ink rp-focus',
        className,
      )}
      {...props}
    />
  );
}

/* ── Badge / Tag ───────────────────────────────────────────────────────────── */

export type BadgeTone = 'ink' | 'red' | 'yellow' | 'blue' | 'green' | 'outline';

const BADGE_TONE: Record<BadgeTone, string> = {
  ink: 'bg-tone-accent-bg text-tone-accent-fg',
  red: 'bg-tone-red-bg text-tone-red-fg',
  yellow: 'bg-tone-yellow-bg text-tone-yellow-fg',
  blue: 'bg-tone-blue-bg text-tone-blue-fg',
  green: 'bg-tone-green-bg text-tone-green-fg',
  outline: 'border border-line text-muted',
};

export function Badge({
  tone = 'outline',
  className,
  children,
}: {
  tone?: BadgeTone;
  className?: string;
  children: ReactNode;
}) {
  return (
    <span
      className={cn(
        'inline-flex items-center gap-1 rounded-skin-chip px-1.5 py-0.5 font-mono text-[10px] font-medium uppercase leading-none tracking-[0.08em]',
        BADGE_TONE[tone],
        className,
      )}
    >
      {children}
    </span>
  );
}

/* ── Kbd ─────────────────────────────────────────────────────────────────── */

export function Kbd({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <kbd
      className={cn(
        'inline-flex h-[18px] min-w-[18px] items-center justify-center rounded-skin-sm border border-line px-1 font-mono text-[10px] font-bold text-muted',
        className,
      )}
    >
      {children}
    </kbd>
  );
}

/* ── StatTile ─────────────────────────────────────────────────────────────── */

export function StatTile({
  label,
  value,
  unit,
  tone = 'ink',
  hint,
  index,
  className,
}: {
  label: string;
  value: ReactNode;
  unit?: string;
  tone?: 'ink' | 'red' | 'yellow' | 'blue' | 'green';
  hint?: ReactNode;
  index?: string;
  className?: string;
}) {
  const valueColor =
    tone === 'red'
      ? 'text-red'
      : tone === 'yellow'
        ? 'text-yellow'
        : tone === 'blue'
          ? 'text-blue'
          : tone === 'green'
            ? 'text-green'
            : 'text-ink';
  return (
    <div
      className="rounded-skin bg-raised px-4 py-3"
      style={{
        border: 'var(--rp-panel-border-width) solid var(--rp-panel-border)',
        boxShadow: 'var(--rp-rim), var(--rp-shadow-card)',
      }}
    >
      <div className="flex items-center justify-between">
        <span className="rp-micro !text-[10px]">{label}</span>
        {index ? <span className="font-mono text-[10px] text-faint">{index}</span> : null}
      </div>
      <div className={cn('mt-2 flex items-baseline gap-1.5 border-t border-line pt-2', className)}>
        <span className={cn('font-mono text-[30px] font-bold leading-none tnum', valueColor)}>
          {value}
        </span>
        {unit ? <span className="font-mono text-[12px] text-muted">{unit}</span> : null}
      </div>
      {hint ? <div className="mt-1.5 font-mono text-[10px] text-muted">{hint}</div> : null}
    </div>
  );
}

/* ── Skeleton / Spinner / EmptyState ────────────────────────────────────────── */

export function Skeleton({ className }: { className?: string }) {
  return <div className={cn('animate-pulse rounded-skin-sm bg-inset', className)} />;
}

export function Spinner({ className }: { className?: string }) {
  // Aurora: runder Ring; Swiss: eckig (rounded-skin-sm = 0). Ein Component, beide Skins.
  return (
    <span
      className={cn(
        'inline-block animate-spin rounded-skin-sm border-2 border-line border-t-ink',
        className,
      )}
      style={{ width: 14, height: 14 }}
      aria-hidden
    />
  );
}

export function EmptyState({
  title,
  description,
  icon,
  action,
  className,
}: {
  title: string;
  description?: ReactNode;
  icon?: ReactNode;
  action?: ReactNode;
  className?: string;
}) {
  return (
    <div
      className={cn(
        'flex flex-col items-center justify-center gap-3 rounded-skin border border-dashed border-line px-6 py-16 text-center',
        className,
      )}
    >
      {icon ? <div className="text-ink">{icon}</div> : null}
      <div className="rp-headline text-[15px] font-bold text-ink">{title}</div>
      {description ? (
        <div className="max-w-sm font-mono text-[12px] leading-relaxed text-muted">
          {description}
        </div>
      ) : null}
      {action ? <div className="mt-1">{action}</div> : null}
    </div>
  );
}
