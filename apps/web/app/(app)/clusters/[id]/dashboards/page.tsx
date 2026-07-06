'use client';

import { useCallback, useEffect, useMemo, useState } from 'react';
import { useParams } from 'next/navigation';
import { cn } from '@/lib/cn';
import { useMe } from '@/components/app/me-context';
import { PageHeader } from '@/components/app/page-header';
import { DashboardPanel } from '@/components/dashboards/dashboard-panel';
import {
  BUILTIN_DASHBOARDS,
  loadCustomDashboards,
  saveCustomDashboards,
  toPerses,
  parsePerses,
  uniqueId,
  type Dashboard,
} from '@/lib/dashboards';

const RANGES = [
  { label: '15m', min: 15 },
  { label: '1h', min: 60 },
  { label: '6h', min: 360 },
  { label: '24h', min: 1440 },
];

// Vorlage für „+ New" — als Perses-Dashboard, das direkt live rendert.
const NEW_DASHBOARD: Dashboard = {
  id: 'my-dashboard',
  name: 'My dashboard',
  description: 'Dashboards are code (Perses format) — edit this YAML and it renders live.',
  panels: [
    {
      title: 'Request rate — req/min',
      query: 'sum by (service_name) (rate(http_server_request_duration_count[5m])) * 60',
      unit: 'req/min',
      w: 2,
    },
    {
      title: 'Error ratio %',
      query:
        '100 * sum(rate(http_server_request_duration_count{http_response_status_code=~"5.."}[5m])) / sum(rate(http_server_request_duration_count[5m]))',
      unit: '%',
      w: 1,
    },
  ],
};

function spanClass(w?: number): string {
  if (w === 3) return 'md:col-span-2 xl:col-span-3';
  if (w === 2) return 'md:col-span-2 xl:col-span-2';
  return '';
}

export default function DashboardsPage() {
  const params = useParams<{ id: string }>();
  const clusterId = params.id;
  const { currentOrg } = useMe();
  const orgId = currentOrg?.id;

  const [custom, setCustom] = useState<Dashboard[]>([]);
  const [activeId, setActiveId] = useState<string>(BUILTIN_DASHBOARDS[0]!.id);
  const [range, setRange] = useState(RANGES[0]!);
  const [refreshKey, setRefreshKey] = useState(0);
  // Editor: null = zu; sonst {id|null, text, error}
  const [editor, setEditor] = useState<{ id: string | null; text: string; error: string | null } | null>(null);

  useEffect(() => setCustom(loadCustomDashboards(clusterId)), [clusterId]);

  // Live: alle 15s re-query (die Panels lauschen auf refreshKey).
  useEffect(() => {
    const t = setInterval(() => setRefreshKey((k) => k + 1), 15_000);
    return () => clearInterval(t);
  }, []);

  const all = useMemo(() => [...BUILTIN_DASHBOARDS, ...custom], [custom]);
  const active = all.find((d) => d.id === activeId) ?? all[0] ?? null;

  const persist = useCallback(
    (next: Dashboard[]) => {
      setCustom(next);
      saveCustomDashboards(clusterId, next);
    },
    [clusterId],
  );

  const saveEditor = useCallback(() => {
    if (!editor) return;
    const parsed = parsePerses(editor.text);
    if (!parsed.ok) {
      setEditor({ ...editor, error: parsed.error });
      return;
    }
    const taken = new Set([...BUILTIN_DASHBOARDS.map((d) => d.id), ...custom.map((d) => d.id)]);
    let id: string;
    if (editor.id) {
      id = editor.id; // bestehendes Custom-Dashboard behält seine id
    } else {
      id = uniqueId(parsed.spec.name, taken);
    }
    const dash: Dashboard = { id, name: parsed.spec.name, description: parsed.spec.description, panels: parsed.spec.panels };
    const idx = custom.findIndex((d) => d.id === id);
    const next = idx >= 0 ? custom.map((d) => (d.id === id ? dash : d)) : [...custom, dash];
    persist(next);
    setActiveId(id);
    setEditor(null);
  }, [editor, custom, persist]);

  const deleteActive = useCallback(() => {
    if (!active || active.builtin) return;
    persist(custom.filter((d) => d.id !== active.id));
    setActiveId(BUILTIN_DASHBOARDS[0]!.id);
    setEditor(null);
  }, [active, custom, persist]);

  return (
    <div className="flex h-[calc(100dvh-52px)] flex-col overflow-y-auto px-4 pb-6 pt-4 sm:px-5">
      <PageHeader kicker="observe / dashboards" title="Dashboards" meta={active?.description ?? 'dashboards as code'}>
        <div className="flex flex-wrap items-center gap-2">
          <span className="flex items-center gap-1.5 font-mono text-[11px] text-muted">
            <span className="rp-breath inline-block h-1.5 w-1.5 rounded-full" style={{ background: 'var(--rp-green)', color: 'var(--rp-green)' }} />
            <span className="text-ink">Live</span>
          </span>
          <div className="flex items-center rounded-skin-sm border border-line p-[2px]">
            {RANGES.map((r) => (
              <button
                key={r.label}
                type="button"
                onClick={() => setRange(r)}
                className={cn(
                  'rp-focus rounded-[3px] px-2 py-[3px] font-mono text-[10px] transition-colors',
                  range.label === r.label ? 'bg-hover text-ink' : 'text-muted hover:text-ink',
                )}
                style={range.label === r.label ? { boxShadow: 'inset 0 0 0 1px var(--rp-line-strong)' } : undefined}
              >
                {r.label}
              </button>
            ))}
          </div>
        </div>
      </PageHeader>

      {/* Dashboard-Auswahl (Pills) + Aktionen */}
      <div className="mt-3 flex flex-wrap items-center gap-2">
        <div className="flex flex-1 flex-wrap items-center gap-1.5">
          {all.map((d) => {
            const on = d.id === active?.id;
            return (
              <button
                key={d.id}
                type="button"
                onClick={() => setActiveId(d.id)}
                className={cn(
                  'rp-focus flex shrink-0 items-center gap-1.5 rounded-skin-sm border px-2.5 py-1.5 font-mono text-[11.5px] transition-colors',
                  on ? 'border-line-strong bg-hover text-ink' : 'border-line text-mid hover:bg-hover hover:text-ink',
                )}
              >
                {d.name}
                {d.builtin ? <span className="text-[8.5px] uppercase tracking-[0.06em] text-faint">core</span> : null}
              </button>
            );
          })}
        </div>
        <div className="flex shrink-0 items-center gap-1.5">
          {active ? (
            <button
              type="button"
              onClick={() =>
                setEditor(
                  active.builtin
                    ? { id: null, text: toPerses({ ...active, id: `${active.id}-copy`, name: `${active.name} (copy)` }), error: null }
                    : { id: active.id, text: toPerses(active), error: null },
                )
              }
              title={active.builtin ? 'fork this dashboard as your own' : 'edit as code'}
              className="rp-focus rounded-skin-sm border border-line px-2.5 py-1.5 font-mono text-[11px] text-mid transition-colors hover:bg-hover hover:text-ink"
            >
              {active.builtin ? '</> fork' : '</> edit'}
            </button>
          ) : null}
          <button
            type="button"
            onClick={() => setEditor({ id: null, text: toPerses(NEW_DASHBOARD), error: null })}
            className="rp-focus rounded-skin-sm px-3 py-1.5 font-mono text-[11px] font-semibold transition-opacity hover:opacity-90"
            style={{ background: 'var(--rp-btn-bg)', color: 'var(--rp-btn-fg)' }}
          >
            + New
          </button>
        </div>
      </div>

      {/* Panel-Grid */}
      {active ? (
        active.panels.length === 0 ? (
          <div className="mt-6 rounded-skin border border-dashed p-6 text-center font-mono text-[11px] text-muted" style={{ borderColor: 'var(--rp-line-strong)' }}>
            This dashboard has no panels yet — edit the spec and add one.
          </div>
        ) : (
          <div className="mt-3 grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-3">
            {active.panels.map((p, i) => (
              <div key={`${active.id}-${i}`} className={spanClass(p.w)}>
                {orgId ? (
                  <DashboardPanel orgId={orgId} clusterId={clusterId} panel={p} sinceMin={range.min} refreshKey={refreshKey} />
                ) : null}
              </div>
            ))}
          </div>
        )
      ) : null}

      {/* „as code"-Editor */}
      {editor ? (
        <div className="fixed inset-0 z-50" role="dialog" aria-modal="true" aria-label="Dashboard editor">
          <button type="button" aria-label="Close" onClick={() => setEditor(null)} className="absolute inset-0 cursor-default" style={{ backgroundColor: 'var(--rp-scrim)' }} />
          <aside
            className="absolute inset-y-0 right-0 flex w-[min(680px,94vw)] flex-col border-l border-line bg-raised"
            style={{ boxShadow: 'var(--rp-rim), var(--rp-shadow-pop)', animation: 'rp-drawer-in var(--rp-dur-large) var(--rp-ease-enter)' }}
          >
            <header className="shrink-0 px-5 pb-3 pt-4">
              <div className="rp-micro !text-[10px] text-faint">dashboard / perses · as code</div>
              <div className="rp-keyline mt-2 flex flex-wrap items-center justify-between gap-2 pb-3">
                <h2 className="font-display text-[20px] font-bold tracking-tightest text-ink">
                  {editor.id ? 'Edit dashboard' : 'New dashboard'}
                </h2>
                <div className="flex items-center gap-2">
                  {editor.id ? (
                    <button type="button" onClick={deleteActive} className="rounded-skin-sm border border-line px-2.5 py-1 font-mono text-[11px] transition-colors hover:bg-hover" style={{ color: 'var(--rp-tone-red-fg)' }}>
                      delete
                    </button>
                  ) : null}
                  <button type="button" onClick={saveEditor} className="rp-focus flex h-8 items-center gap-1.5 rounded-skin-sm px-3.5 font-mono text-[11.5px] font-semibold transition-opacity hover:opacity-90" style={{ background: 'var(--rp-btn-bg)', color: 'var(--rp-btn-fg)' }}>
                    Save <span aria-hidden>→</span>
                  </button>
                  <button type="button" onClick={() => setEditor(null)} className="rounded-skin-sm border border-line px-3 py-1.5 font-mono text-[11.5px] text-mid transition-colors hover:bg-hover hover:text-ink">
                    Cancel
                  </button>
                </div>
              </div>
            </header>
            <div className="min-h-0 flex-1 overflow-y-auto px-5 py-4">
              <p className="mb-2 font-mono text-[10.5px] leading-relaxed text-muted">
                <span className="text-ink">Perses</span> format (the open CNCF standard — same as Dash0):{' '}
                <span className="text-ink">kind: Dashboard</span> · <span className="text-ink">spec.panels</span> (each a
                {' '}<span className="text-ink">PrometheusTimeSeriesQuery</span>) · a{' '}
                <span className="text-ink">Grid</span> layout. Portable in/out of the Perses ecosystem. Renders live.
              </p>
              {editor.error ? (
                <div className="mb-2 rounded-skin-sm px-3 py-2 font-mono text-[11px]" style={{ color: 'var(--rp-tone-red-fg)', background: 'var(--rp-tone-red-bg)' }}>
                  {editor.error}
                </div>
              ) : null}
              <textarea
                value={editor.text}
                onChange={(e) => setEditor({ ...editor, text: e.target.value, error: null })}
                spellCheck={false}
                className="rp-focus h-[calc(100%-60px)] min-h-[360px] w-full resize-none rounded-skin-sm border border-line bg-inset p-3 font-mono text-[12px] leading-relaxed text-ink"
              />
            </div>
          </aside>
        </div>
      ) : null}
    </div>
  );
}
