import type { HTMLAttributes, ReactNode, ThHTMLAttributes, TdHTMLAttributes } from 'react';
import { cn } from '@/lib/cn';

// Datentabelle (Multi-Skin) — das Arbeitspferd. Dicht, tabellarisch, nummerierte
// Zeilen, mono rechtsbündig für Zahlen. Label-Case/Rules kommen aus --rp-*-Tokens
// (Swiss: uppercase + fette ink-Rule · Aurora: sentence-case + subtile Rule).

export function DataTable({ className, ...props }: HTMLAttributes<HTMLTableElement>) {
  return (
    <div className="overflow-x-auto">
      <table className={cn('w-full border-collapse font-mono text-[12px]', className)} {...props} />
    </div>
  );
}

export function THead({ className, ...props }: HTMLAttributes<HTMLTableSectionElement>) {
  return <thead className={cn('sticky top-0 z-10 bg-raised', className)} {...props} />;
}

export function TBody(props: HTMLAttributes<HTMLTableSectionElement>) {
  return <tbody {...props} />;
}

export function TR({
  className,
  interactive,
  selected,
  ...props
}: HTMLAttributes<HTMLTableRowElement> & { interactive?: boolean; selected?: boolean }) {
  return (
    <tr
      aria-selected={selected}
      className={cn(
        'border-b border-line',
        interactive && 'cursor-pointer transition-colors hover:bg-hover',
        selected && 'bg-active',
        className,
      )}
      {...props}
    />
  );
}

type Align = 'left' | 'right' | 'center';
const ALIGN: Record<Align, string> = { left: 'text-left', right: 'text-right', center: 'text-center' };

export function TH({
  className,
  align = 'left',
  children,
  ...props
}: ThHTMLAttributes<HTMLTableCellElement> & { align?: Align }) {
  return (
    <th
      className={cn('rp-micro whitespace-nowrap px-3 py-2 !text-[10px]', ALIGN[align], className)}
      style={{ borderBottom: 'var(--rp-header-rule-width) solid var(--rp-panel-border)' }}
      {...props}
    >
      {children}
    </th>
  );
}

export function TD({
  className,
  align = 'left',
  children,
  ...props
}: TdHTMLAttributes<HTMLTableCellElement> & { align?: Align }) {
  return (
    <td className={cn('px-3 py-2 text-ink tnum', ALIGN[align], className)} {...props}>
      {children}
    </td>
  );
}

export function SortableTH({
  label,
  active,
  dir,
  onSort,
  align = 'left',
}: {
  label: ReactNode;
  active?: boolean;
  dir?: 'asc' | 'desc';
  onSort?: () => void;
  align?: Align;
}) {
  return (
    <TH align={align} className="p-0">
      <button
        type="button"
        onClick={onSort}
        className={cn(
          'rp-micro flex w-full items-center gap-1 px-3 py-2 !text-[10px] transition-colors hover:!text-ink',
          align === 'right' && 'justify-end',
          align === 'center' && 'justify-center',
          active && '!text-ink',
        )}
      >
        {label}
        <span className={cn('text-[8px]', active ? 'opacity-100' : 'opacity-0')}>
          {dir === 'asc' ? '▲' : '▼'}
        </span>
      </button>
    </TH>
  );
}

export function RowIndex({ n }: { n: number }) {
  return <span className="text-faint">{String(n).padStart(2, '0')}</span>;
}
