'use client';

import type { ComponentType, SVGProps } from 'react';
import { usePathname } from 'next/navigation';
import { Bell, Compass, Cube, Dashboards, Gear, Logs, Metrics, Waterfall } from '@/components/icons';
import { Wordmark } from '@/components/brand/wordmark';

type NavItem = {
  label: string;
  href: string;
  icon: ComponentType<SVGProps<SVGSVGElement>>;
  badge?: string;
  alert?: boolean;
  ready?: boolean;
};

const NAV: NavItem[] = [
  { label: 'Explore', href: '/explore', icon: Compass, ready: true },
  { label: 'Services', href: '/services', icon: Cube, ready: true },
  { label: 'Traces', href: '/traces', icon: Waterfall, ready: true },
  { label: 'Logs', href: '/logs', icon: Logs, ready: true },
  { label: 'Metrics', href: '#', icon: Metrics },
  { label: 'Dashboards', href: '#', icon: Dashboards },
  { label: 'Alerts', href: '#', icon: Bell, badge: '3', alert: true },
];

export function Sidebar() {
  const pathname = usePathname();

  return (
    <aside className="flex w-[224px] shrink-0 flex-col border-r border-line bg-raised">
      <div className="flex h-14 items-center gap-2.5 border-b border-line px-4">
        <span className="beacon h-6 w-6 rounded-[7px]" aria-hidden />
        <Wordmark className="font-display text-[15px] font-semibold tracking-tight text-strong" />
      </div>

      <nav className="flex-1 space-y-0.5 overflow-y-auto p-2.5">
        {NAV.map(({ label, href, icon: Icon, badge, alert, ready }) => {
          const active = ready ? pathname.startsWith(href) : false;
          return (
            <a
              key={label}
              href={href}
              aria-disabled={!ready}
              className={`relative flex items-center gap-2.5 rounded-md px-2.5 py-1.5 text-[13px] transition-colors ${
                active ? 'bg-overlay text-strong' : 'text-muted hover:text-strong'
              } ${ready ? '' : 'cursor-not-allowed opacity-70'}`}
            >
              {active && (
                <span className="absolute left-0 top-1/2 h-4 w-0.5 -translate-y-1/2 rounded-full bg-accent" />
              )}
              <Icon className={`h-4 w-4 ${active ? 'text-accent' : 'text-faint'}`} />
              <span className="flex-1">{label}</span>
              {badge && (
                <span
                  className={`rounded px-1.5 py-0.5 font-mono text-[10px] ${
                    alert ? 'bg-[rgba(242,85,90,0.14)] text-status-critical' : 'bg-base text-faint'
                  }`}
                >
                  {badge}
                </span>
              )}
            </a>
          );
        })}
      </nav>

      <div className="border-t border-line p-2.5">
        <a
          href="#"
          className="flex items-center gap-2.5 rounded-md px-2.5 py-1.5 text-[13px] text-muted transition-colors hover:text-strong"
        >
          <Gear className="h-4 w-4 text-faint" />
          Settings
        </a>
      </div>
    </aside>
  );
}
