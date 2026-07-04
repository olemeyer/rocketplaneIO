import type { ComponentType, SVGProps } from 'react';
import { brand, signal, status } from '@rocketplane/ui';
import {
  Bell,
  ChevronDown,
  Compass,
  Cube,
  Dashboards,
  Logs,
  Metrics,
  Search,
  Waterfall,
} from './icons';

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

const SERVICES = [
  { name: 'checkout-api', st: 'critical', p95: '842ms', err: '4.2%', rate: '1.2k/s', spark: [5, 7, 6, 9, 8, 12, 16, 14] },
  { name: 'payment-gateway', st: 'degraded', p95: '310ms', err: '0.8%', rate: '640/s', spark: [8, 7, 9, 8, 10, 9, 11, 10] },
  { name: 'cart-service', st: 'resolved', p95: '96ms', err: '0.1%', rate: '2.1k/s', spark: [6, 6, 5, 7, 6, 6, 7, 6] },
  { name: 'inventory', st: 'resolved', p95: '54ms', err: '0.0%', rate: '880/s', spark: [4, 5, 4, 4, 5, 4, 5, 4] },
] as const;

const SPANS = [
  { name: 'POST /checkout', svc: 'checkout-api', color: signal.traces, off: 0, len: 100, dur: '342ms', depth: 0 },
  { name: 'auth.verify', svc: 'auth', color: brand.from, off: 3, len: 12, dur: '41ms', depth: 1 },
  { name: 'cart.load', svc: 'cart-service', color: signal.logs, off: 12, len: 16, dur: '55ms', depth: 1 },
  { name: 'payment.charge', svc: 'payment-gateway', color: signal.rum, off: 30, len: 46, dur: '158ms', depth: 1 },
  { name: 'stripe.charge', svc: 'stripe', color: signal.synthetics, off: 34, len: 38, dur: '130ms', depth: 2 },
  { name: 'inventory.reserve', svc: 'inventory', color: '#34d399', off: 78, len: 10, dur: '34ms', depth: 1 },
  { name: 'email.enqueue', svc: 'notifier', color: status.resolved, off: 90, len: 8, dur: '24ms', depth: 1 },
] as const;

function StatusDot({ st }: { st: keyof typeof status }) {
  return (
    <span
      className="inline-block h-1.5 w-1.5 shrink-0 rounded-full"
      style={{ background: status[st], boxShadow: `0 0 8px ${status[st]}66` }}
    />
  );
}

export function AppShellMock() {
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
              className={`group relative mb-0.5 flex items-center gap-2.5 rounded-md px-2.5 py-1.5 text-[13px] ${
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

        {/* Explore-Hauptbereich */}
        <div className="min-w-0 p-3.5">
          {/* Faceted Filter Bar */}
          <div className="flex items-center gap-2 rounded-lg border border-line bg-base px-3 py-2">
            <Search className="h-3.5 w-3.5 text-faint" />
            <div className="flex flex-1 flex-wrap items-center gap-1.5">
              <FilterChip k="service.name" v="checkout-api" />
              <FilterChip k="http.status_code" v="≥ 500" />
              <span className="font-mono text-[11px] text-faint">+ add filter</span>
            </div>
            <span className="hidden shrink-0 font-mono text-[11px] text-muted sm:inline">
              <span className="text-strong">3,481</span> spans · <span className="text-status-critical">147</span> errors
            </span>
            <kbd className="rounded border border-line bg-overlay px-1.5 py-0.5 font-mono text-[10px] text-faint">
              ⌘K
            </kbd>
          </div>

          {/* Service-Health (RED) */}
          <div className="mt-3.5">
            <div className="mb-2 flex items-center justify-between">
              <span className="font-mono text-[11px] uppercase tracking-wider text-faint">Service health</span>
              <span className="font-mono text-[11px] text-faint">p95 · error rate · throughput</span>
            </div>
            <div className="grid grid-cols-2 gap-2 lg:grid-cols-4">
              {SERVICES.map((s) => (
                <div key={s.name} className="rounded-lg border border-line bg-base p-2.5">
                  <div className="flex items-center gap-1.5">
                    <StatusDot st={s.st as keyof typeof status} />
                    <span className="truncate text-[12px] text-strong">{s.name}</span>
                  </div>
                  <div className="mt-2 flex items-end gap-[3px]" aria-hidden>
                    {s.spark.map((h, i) => (
                      <span
                        key={i}
                        className="w-full rounded-[1px]"
                        style={{
                          height: `${h * 1.6}px`,
                          background: status[s.st as keyof typeof status],
                          opacity: 0.35 + (i / s.spark.length) * 0.6,
                        }}
                      />
                    ))}
                  </div>
                  <div className="mt-2 flex items-center justify-between font-mono text-[11px]">
                    <span className="text-strong">{s.p95}</span>
                    <span className={s.err === '0.0%' ? 'text-faint' : 'text-muted'}>{s.err}</span>
                    <span className="text-faint">{s.rate}</span>
                  </div>
                </div>
              ))}
            </div>
          </div>

          {/* Trace-Waterfall */}
          <div className="mt-3.5 rounded-lg border border-line bg-base p-3">
            <div className="mb-2.5 flex items-center gap-2 font-mono text-[11px]">
              <Waterfall className="h-3.5 w-3.5 text-accent" />
              <span className="text-muted">trace</span>
              <span className="text-strong">7f3a9c…</span>
              <span className="text-faint">·</span>
              <span className="text-strong">342ms</span>
              <span className="text-faint">·</span>
              <span className="text-faint">14 spans</span>
              <span className="ml-auto text-status-critical">1 error</span>
            </div>
            <div className="space-y-[3px]">
              {SPANS.map((sp, i) => (
                <div key={i} className="grid grid-cols-[168px_1fr_52px] items-center gap-2">
                  <div
                    className="flex items-center gap-1.5 truncate font-mono text-[11px]"
                    style={{ paddingLeft: `${sp.depth * 12}px` }}
                  >
                    <span className="h-2.5 w-[3px] shrink-0 rounded-full" style={{ background: sp.color }} />
                    <span className="truncate text-strong">{sp.name}</span>
                  </div>
                  <div className="relative h-3.5 rounded-[3px] bg-overlay">
                    <div
                      className="absolute top-0 h-full rounded-[3px]"
                      style={{
                        left: `${sp.off}%`,
                        width: `${sp.len}%`,
                        background: sp.color,
                        opacity: 0.85,
                      }}
                    />
                  </div>
                  <span className="text-right font-mono text-[11px] text-muted">{sp.dur}</span>
                </div>
              ))}
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}

function FilterChip({ k, v }: { k: string; v: string }) {
  return (
    <span className="flex items-center gap-1 rounded-md border border-line bg-overlay px-1.5 py-0.5 font-mono text-[11px]">
      <span className="text-faint">{k}</span>
      <span className="text-accent">{v}</span>
    </span>
  );
}
