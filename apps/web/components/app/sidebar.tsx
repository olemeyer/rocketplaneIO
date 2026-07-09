'use client';

import Link from 'next/link';
import { usePathname } from 'next/navigation';
import { createContext, useContext, useEffect, useState, type ReactNode } from 'react';
import { cn } from '@/lib/cn';
import { OnlineDot } from '@/components/ui';
import { Lockup } from '@/components/brand/logo';
import { useScope } from './scope-context';

// ── Mobile-Nav-Zustand (geteilt zwischen Topbar-Hamburger und Drawer) ──
const NavContext = createContext<{ open: boolean; setOpen: (v: boolean) => void }>({
  open: false,
  setOpen: () => {},
});
export function NavProvider({ children }: { children: ReactNode }) {
  const [open, setOpen] = useState(false);
  return <NavContext.Provider value={{ open, setOpen }}>{children}</NavContext.Provider>;
}
export function useNav() {
  return useContext(NavContext);
}

// Cluster-scoped Navigation im RETICLE-Stil: elevated Fläche (bg-raised, heller als
// der Canvas → schwebt ohne Schatten), 1px-Hairline rechts, Brand-Header exakt in
// Topbar-Höhe (die beiden Hairlines kreuzen sich an einer Passermarke). Sektionen
// als großgesperrte Mono-Micro-Labels; der aktive Eintrag trägt einen ruhigen,
// MONOCHROMEN 2px-Ink-Tick (Farbe ist ein Ereignis, keine Navigation).

function MapIcon() {
  return (
    <svg width="15" height="15" viewBox="0 0 16 16" fill="none" aria-hidden>
      <circle cx="3.5" cy="4" r="1.6" stroke="currentColor" strokeWidth="1.5" />
      <circle cx="12.5" cy="4" r="1.6" stroke="currentColor" strokeWidth="1.5" />
      <circle cx="8" cy="12" r="1.6" stroke="currentColor" strokeWidth="1.5" />
      <path d="M5 4.6 11 4.6M4.4 5.4 7.2 10.8M11.6 5.4 8.8 10.8" stroke="currentColor" strokeWidth="1.2" />
    </svg>
  );
}

function LogsIcon() {
  return (
    <svg width="15" height="15" viewBox="0 0 16 16" fill="none" aria-hidden>
      <path d="M2.5 3.5h11M2.5 6.5h8M2.5 9.5h11M2.5 12.5h6" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
    </svg>
  );
}

function DashboardsIcon() {
  return (
    <svg width="15" height="15" viewBox="0 0 16 16" fill="none" aria-hidden>
      <rect x="2.5" y="2.5" width="4.5" height="5.5" rx="1" stroke="currentColor" strokeWidth="1.5" />
      <rect x="9" y="2.5" width="4.5" height="3" rx="1" stroke="currentColor" strokeWidth="1.5" />
      <rect x="2.5" y="10.5" width="4.5" height="3" rx="1" stroke="currentColor" strokeWidth="1.5" />
      <rect x="9" y="7.5" width="4.5" height="6" rx="1" stroke="currentColor" strokeWidth="1.5" />
    </svg>
  );
}

function MetricsIcon() {
  return (
    <svg width="15" height="15" viewBox="0 0 16 16" fill="none" aria-hidden>
      <path d="M2.5 13.5V9M6.5 13.5V5.5M10.5 13.5V8M14 13.5V3" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" />
    </svg>
  );
}

function AlertsIcon() {
  return (
    <svg width="15" height="15" viewBox="0 0 16 16" fill="none" aria-hidden>
      <path d="M8 2.5 14 13H2L8 2.5Z" stroke="currentColor" strokeWidth="1.5" strokeLinejoin="round" />
      <path d="M8 6.5v3M8 11.2v.2" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
    </svg>
  );
}

function IncidentsIcon() {
  return (
    <svg width="15" height="15" viewBox="0 0 16 16" fill="none" aria-hidden>
      <path d="M8 1.8 2 4.5v3.7c0 3.4 2.4 5.6 6 6.5 3.6-.9 6-3.1 6-6.5V4.5L8 1.8Z" stroke="currentColor" strokeWidth="1.4" strokeLinejoin="round" />
      <path d="M8 5.3v3.1M8 10.5v.2" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
    </svg>
  );
}

function TracesIcon() {
  return (
    <svg width="15" height="15" viewBox="0 0 16 16" fill="none" aria-hidden>
      <path d="M2.5 4h9M5 8h8.5M2.5 12h6" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
      <circle cx="13.5" cy="4" r="1.2" fill="currentColor" />
      <circle cx="2.8" cy="8" r="1.2" fill="currentColor" />
      <circle cx="11" cy="12" r="1.2" fill="currentColor" />
    </svg>
  );
}

function NodesIcon() {
  return (
    <svg width="15" height="15" viewBox="0 0 16 16" fill="none" aria-hidden>
      <rect x="2" y="2.5" width="12" height="4" rx="1" stroke="currentColor" strokeWidth="1.5" />
      <rect x="2" y="9.5" width="12" height="4" rx="1" stroke="currentColor" strokeWidth="1.5" />
      <circle cx="4.6" cy="4.5" r="0.9" fill="currentColor" />
      <circle cx="4.6" cy="11.5" r="0.9" fill="currentColor" />
    </svg>
  );
}

function StorageIcon() {
  return (
    <svg width="15" height="15" viewBox="0 0 16 16" fill="none" aria-hidden>
      <ellipse cx="8" cy="4" rx="5.5" ry="2" stroke="currentColor" strokeWidth="1.5" />
      <path d="M2.5 4v8c0 1.1 2.46 2 5.5 2s5.5-.9 5.5-2V4" stroke="currentColor" strokeWidth="1.5" />
      <path d="M2.5 8c0 1.1 2.46 2 5.5 2s5.5-.9 5.5-2" stroke="currentColor" strokeWidth="1.2" />
    </svg>
  );
}

function ActionsIcon() {
  return (
    <svg width="15" height="15" viewBox="0 0 16 16" fill="none" aria-hidden>
      <path d="M8.8 1.8 3.2 9h4l-.9 5.2L11.9 7h-4l.9-5.2Z" stroke="currentColor" strokeWidth="1.4" strokeLinejoin="round" />
    </svg>
  );
}

function ResourcesIcon() {
  return (
    <svg width="15" height="15" viewBox="0 0 16 16" fill="none" aria-hidden>
      <rect x="2" y="2" width="5" height="5" rx="1" stroke="currentColor" strokeWidth="1.3" />
      <rect x="9" y="2" width="5" height="5" rx="1" stroke="currentColor" strokeWidth="1.3" />
      <rect x="2" y="9" width="5" height="5" rx="1" stroke="currentColor" strokeWidth="1.3" />
      <rect x="9" y="9" width="5" height="5" rx="1" stroke="currentColor" strokeWidth="1.3" />
    </svg>
  );
}

function RunsIcon() {
  return (
    <svg width="15" height="15" viewBox="0 0 16 16" fill="none" aria-hidden>
      <path d="M2.5 12.5V9M6 12.5V5.5M9.5 12.5V7.5M13 12.5V3.5" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
    </svg>
  );
}

// Ein Nav-Eintrag (geteilt zwischen Desktop-Sidebar und Mobile-Drawer). Mobil
// etwas höher (Touch-Target), Desktop kompakt.
function NavLink({
  item,
  active,
  disabled,
  onNavigate,
}: {
  item: { label: string; href?: string; icon: ReactNode };
  active?: boolean;
  disabled?: boolean;
  onNavigate?: () => void;
}) {
  const cls = cn(
    'relative flex h-[40px] items-center gap-3 rounded-skin-sm px-2.5 text-[13.5px] font-medium transition-colors duration-[120ms] md:h-[34px] md:text-[13px]',
    disabled
      ? 'cursor-default text-faint'
      : active
        ? 'bg-hover text-ink'
        : 'text-mid hover:bg-hover hover:text-ink',
  );
  const inner = (
    <>
      {active ? (
        <span className="absolute left-0 top-1/2 h-4 w-[2px] -translate-y-1/2 rounded-full bg-ink" aria-hidden />
      ) : null}
      <span className={disabled ? 'opacity-50' : active ? 'text-ink' : 'text-muted'}>{item.icon}</span>
      <span>{item.label}</span>
    </>
  );
  if (disabled || !item.href) return <div className={cls}>{inner}</div>;
  return (
    <Link href={item.href} aria-current={active ? 'page' : undefined} onClick={onNavigate} className={cls}>
      {inner}
    </Link>
  );
}

// Die Nav-Sektionen — von Sidebar (Desktop) und MobileNav (Drawer) geteilt.
function SidebarNav({ onNavigate }: { onNavigate?: () => void }) {
  const pathname = usePathname();
  const { activeCluster, activeClusterId } = useScope();
  if (!activeCluster || !activeClusterId) {
    return (
      <div className="px-3 py-8 text-center">
        <p className="font-mono text-[11px] leading-relaxed text-muted">
          Select a cluster above to explore its service map.
        </p>
      </div>
    );
  }
  const base = `/clusters/${activeClusterId}`;
  const sections: {
    label: string;
    items: { label: string; href: string; icon: ReactNode; exact?: boolean }[];
  }[] = [
    {
      label: 'Observe',
      items: [
        { label: 'Service Map', href: base, icon: <MapIcon />, exact: true },
        { label: 'Logs', href: `${base}/logs`, icon: <LogsIcon /> },
        { label: 'Traces', href: `${base}/traces`, icon: <TracesIcon /> },
        { label: 'Metrics', href: `${base}/metrics`, icon: <MetricsIcon /> },
        { label: 'Dashboards', href: `${base}/dashboards`, icon: <DashboardsIcon /> },
        { label: 'Alerts', href: `${base}/alerts`, icon: <AlertsIcon /> },
      ],
    },
    {
      label: 'Infrastructure',
      items: [
        { label: 'Nodes', href: `${base}/infrastructure/nodes`, icon: <NodesIcon /> },
        { label: 'Storage', href: `${base}/infrastructure/storage`, icon: <StorageIcon /> },
        { label: 'Resources', href: `${base}/infrastructure/resources`, icon: <ResourcesIcon /> },
      ],
    },
    {
      label: 'Respond',
      items: [
        { label: 'Incidents', href: `${base}/incidents`, icon: <IncidentsIcon /> },
        { label: 'Actions', href: `${base}/actions`, icon: <ActionsIcon /> },
        { label: 'Runs', href: `${base}/runs`, icon: <RunsIcon /> },
      ],
    },
  ];
  return (
    <>
      {sections.map((sec, si) => (
        <div key={sec.label}>
          <div className={cn('rp-micro px-2 pb-2', si === 0 ? 'pt-1' : 'pt-6')}>{sec.label}</div>
          {sec.items.map((item) => (
            <NavLink
              key={item.href}
              item={item}
              active={item.exact ? pathname === item.href : pathname.startsWith(item.href)}
              onNavigate={onNavigate}
            />
          ))}
        </div>
      ))}
    </>
  );
}

function BrandHeader() {
  return (
    <Link href="/" className="flex h-[52px] shrink-0 items-center border-b border-line px-4">
      <Lockup />
    </Link>
  );
}

function StatusFooter() {
  return (
    <div className="shrink-0 border-t border-line px-4 py-3">
      <div className="flex items-center gap-2">
        <OnlineDot size={7} className="shrink-0" />
        <span className="rp-micro !text-[10px]">Control-plane online</span>
      </div>
    </div>
  );
}

// Desktop-Sidebar (≥md) — elevated Fläche, rechts Hairline.
export function Sidebar() {
  return (
    <aside
      className="sticky top-0 hidden h-screen w-[236px] shrink-0 flex-col border-r border-line bg-raised md:flex"
      style={{ boxShadow: 'var(--rp-rim)' }}
    >
      <BrandHeader />
      <nav className="flex-1 overflow-y-auto px-2 py-3">
        <SidebarNav />
      </nav>
      <StatusFooter />
    </aside>
  );
}

// Mobile-Drawer (<md) — slide-in mit Scrim; schließt bei Nav-Klick, Scrim, Esc.
export function MobileNav() {
  const { open, setOpen } = useNav();
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false);
    };
    document.addEventListener('keydown', onKey);
    document.body.style.overflow = 'hidden';
    return () => {
      document.removeEventListener('keydown', onKey);
      document.body.style.overflow = '';
    };
  }, [open, setOpen]);
  if (!open) return null;
  return (
    <div className="fixed inset-0 z-50 md:hidden" role="dialog" aria-modal="true">
      <button
        type="button"
        aria-label="Close navigation"
        onClick={() => setOpen(false)}
        className="absolute inset-0 bg-black/50"
      />
      <aside
        className="absolute inset-y-0 left-0 flex w-[82%] max-w-[300px] flex-col border-r border-line bg-raised"
        style={{
          boxShadow: 'var(--rp-shadow-pop)',
          animation: 'rp-drawer-in var(--rp-dur-large) var(--rp-ease-enter)',
        }}
      >
        <BrandHeader />
        <nav className="flex-1 overflow-y-auto px-2 py-3">
          <SidebarNav onNavigate={() => setOpen(false)} />
        </nav>
        <StatusFooter />
      </aside>
    </div>
  );
}
