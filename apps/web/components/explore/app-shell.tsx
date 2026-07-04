import type { ComponentType, SVGProps } from 'react';
import { Bell, ChevronDown, Compass, Cube, Dashboards, Logs, Metrics, Waterfall } from '../icons';
import { CommandPaletteHost } from './command-palette-host';
import { FilterBar, ServiceHealthPanel } from './service-health';
import { TraceWaterfall } from './trace-waterfall';

type NavItem = {
  label: string;
  icon: ComponentType<SVGProps<SVGSVGElement>>;
  badge?: string;
  active?: boolean;
  alert?: boolean;
};

const NAV: NavItem[] = [
  { label: 'Explore', icon: Compass, active: true },
  { label: 'Services', icon: Cube, badge: '38' },
  { label: 'Traces', icon: Waterfall },
  { label: 'Logs', icon: Logs, badge: '2.4k' },
  { label: 'Metrics', icon: Metrics },
  { label: 'Dashboards', icon: Dashboards },
  { label: 'Alerts', icon: Bell, badge: '3', alert: true },
];

// AppShell ist die datengetriebene Variante des früheren Mocks: statisches
// Fenster-Chrome + Sidebar (Server), Live-Islands für Health & Waterfall (Client).
export function AppShell() {
  return (
    <div className="overflow-hidden rounded-xl border border-line bg-raised shadow-card">
      {/* Fensterleiste */}
      <div className="flex items-center gap-3 border-b border-line bg-overlay px-4 py-2.5">
        <div className="flex gap-1.5">
          <span className="h-2.5 w-2.5 rounded-full border border-line" />
          <span className="h-2.5 w-2.5 rounded-full border border-line" />
          <span className="h-2.5 w-2.5 rounded-full border border-line" />
        </div>
        <div className="ml-1 flex items-center gap-2 font-mono text-[11px] text-faint">
          <span className="text-muted">acme-prod</span>
          <span>/</span>
          <span className="text-strong">Explore</span>
        </div>
        <div className="ml-auto flex items-center gap-2">
          <span className="flex items-center gap-1 rounded-md border border-line px-2 py-1 font-mono text-[11px] text-muted">
            production <ChevronDown className="h-3 w-3" />
          </span>
          <span className="flex items-center gap-1 rounded-md border border-line px-2 py-1 font-mono text-[11px] text-muted">
            Last 15m <ChevronDown className="h-3 w-3" />
          </span>
        </div>
      </div>

      <div className="grid grid-cols-[168px_1fr]">
        {/* Sidebar */}
        <nav className="border-r border-line p-2.5">
          {NAV.map(({ label, icon: Icon, badge, active, alert }) => (
            <div
              key={label}
              className={`relative mb-0.5 flex items-center gap-2.5 rounded-md px-2.5 py-1.5 text-[13px] ${
                active ? 'bg-overlay text-strong' : 'text-muted hover:text-strong'
              }`}
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
            </div>
          ))}
        </nav>

        {/* Explore-Hauptbereich (Live) */}
        <div className="min-w-0 space-y-3.5 p-3.5">
          <FilterBar />
          <ServiceHealthPanel />
          <TraceWaterfall />
        </div>
      </div>

      <CommandPaletteHost />
    </div>
  );
}
