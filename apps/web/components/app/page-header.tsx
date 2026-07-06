import type { ReactNode } from 'react';
import { cn } from '@/lib/cn';

// PageHeader — das geteilte Editorial-Kopf-Primitiv (Login-Design-Sprache):
// Mono-Kicker · großer Display-Titel · optionale Mono-Meta · Keyline mit
// Signal-Tick. Rechts die Seiten-Controls (umbrechen sauber auf Mobile).
//
//   <PageHeader kicker="observe / logs" title="Logs" meta="1.2k lines · live">
//     {…controls…}
//   </PageHeader>
export function PageHeader({
  kicker,
  title,
  meta,
  children,
  className,
}: {
  kicker: string;
  title: string;
  meta?: ReactNode;
  children?: ReactNode;
  className?: string;
}) {
  return (
    <header className={cn('shrink-0', className)}>
      <div className="rp-micro !text-[10px] text-faint">{kicker}</div>
      <div className="rp-keyline mt-2 flex flex-wrap items-end justify-between gap-x-4 gap-y-2.5 pb-3">
        <div className="flex items-baseline gap-3">
          <h1 className="font-display text-[23px] font-bold leading-none tracking-tightest text-ink sm:text-[26px]">
            {title}
          </h1>
          {meta ? <span className="font-mono text-[11px] text-muted tnum">{meta}</span> : null}
        </div>
        {children ? <div className="flex flex-wrap items-center gap-2">{children}</div> : null}
      </div>
    </header>
  );
}
