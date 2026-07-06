import YAML from 'yaml';

// dashboards.ts — „Dashboards as code" auf dem OFFENEN STANDARD (Perses, CNCF —
// wie Dash0). Die Quelle der Wahrheit ist eine Perses-kompatible YAML-Spec
// (kind: Dashboard · metadata · spec.panels-Map · spec.layouts-Grid mit $ref ·
// Prometheus-Query-Plugin). Intern rendern wir aus einem schlanken Modell; toPerses/
// parsePerses übersetzen hin und zurück → import/export mit dem Perses-Ökosystem.

export type DashPanel = {
  title: string;
  query: string; // PromQL
  unit?: string;
  /** Grid-Spannweite 1–3 (→ Perses-Grid 8/16/24 von 24 Spalten). */
  w?: number;
};

export type Dashboard = {
  id: string;
  name: string;
  description?: string;
  panels: DashPanel[];
  builtin?: boolean;
};

export const BUILTIN_DASHBOARDS: Dashboard[] = [
  {
    id: 'golden-signals',
    name: 'Golden Signals',
    description: 'Rate · Errors · Latency — per service, straight from eBPF spans.',
    builtin: true,
    panels: [
      {
        title: 'Request rate — req/min by service',
        query: 'sum by (service_name) (rate(http_server_request_duration_count[5m])) * 60',
        unit: 'req/min',
        w: 2,
      },
      {
        title: 'Error ratio %',
        query:
          '100 * sum by (service_name) (rate(http_server_request_duration_count{http_response_status_code=~"5.."}[5m])) / sum by (service_name) (rate(http_server_request_duration_count[5m]))',
        unit: '%',
        w: 1,
      },
      {
        title: 'p95 latency (ms)',
        query:
          'histogram_quantile(0.95, sum by (le, service_name) (rate(http_server_request_duration_bucket[5m]))) * 1000',
        unit: 'ms',
        w: 2,
      },
      {
        title: 'p99 latency (ms)',
        query:
          'histogram_quantile(0.99, sum by (le, service_name) (rate(http_server_request_duration_bucket[5m]))) * 1000',
        unit: 'ms',
        w: 1,
      },
    ],
  },
  {
    id: 'infrastructure',
    name: 'Infrastructure',
    description: 'Node pressure at a glance — CPU, memory and disk across the cluster.',
    builtin: true,
    panels: [
      { title: 'Node CPU %', query: 'node_cpu_pct', unit: '%', w: 1 },
      { title: 'Node memory %', query: 'node_mem_pct', unit: '%', w: 1 },
      { title: 'Node disk %', query: 'node_fs_pct', unit: '%', w: 1 },
    ],
  },
];

/* ── Perses (offener Standard) ⇄ internes Modell ─────────────────────────── */

const GRID_COLS = 24;
const ROW_H = 8;

function panelKey(title: string, i: number): string {
  const pascal = title
    .replace(/[^A-Za-z0-9]+/g, ' ')
    .trim()
    .split(' ')
    .filter(Boolean)
    .map((w) => w[0]!.toUpperCase() + w.slice(1))
    .join('');
  return pascal || `Panel${i + 1}`;
}

// Internes Dashboard → Perses-YAML (das ist die „source of truth"-Repräsentation).
export function toPerses(d: Dashboard): string {
  const panels: Record<string, unknown> = {};
  const items: unknown[] = [];
  let x = 0;
  let y = 0;
  d.panels.forEach((p, i) => {
    const key = panelKey(p.title, i);
    const w = p.w && p.w >= 1 && p.w <= 3 ? p.w : 1;
    const width = w * 8;
    if (x + width > GRID_COLS) {
      x = 0;
      y += ROW_H;
    }
    panels[key] = {
      kind: 'Panel',
      spec: {
        display: { name: p.title },
        plugin: {
          kind: 'TimeSeriesChart',
          spec: p.unit ? { yAxis: { format: { unit: p.unit } } } : {},
        },
        queries: [
          {
            kind: 'TimeSeriesQuery',
            spec: { plugin: { kind: 'PrometheusTimeSeriesQuery', spec: { query: p.query } } },
          },
        ],
      },
    };
    items.push({ x, y, width, height: ROW_H, content: { $ref: `#/spec/panels/${key}` } });
    x += width;
  });

  const doc = {
    kind: 'Dashboard',
    metadata: { name: d.id },
    spec: {
      display: { name: d.name, ...(d.description ? { description: d.description } : {}) },
      duration: '1h',
      panels,
      layouts: [{ kind: 'Grid', spec: { items } }],
    },
  };
  return YAML.stringify(doc);
}

export type ParsedDashboard = { name: string; description?: string; panels: DashPanel[] };

// Perses-YAML → internes Modell (tolerant; nützliche Fehlermeldungen).
export function parsePerses(text: string): { ok: true; spec: ParsedDashboard } | { ok: false; error: string } {
  let doc: unknown;
  try {
    doc = YAML.parse(text);
  } catch (e) {
    return { ok: false, error: e instanceof Error ? e.message : 'invalid YAML' };
  }
  if (!doc || typeof doc !== 'object') return { ok: false, error: 'empty document' };
  const d = doc as Record<string, any>;
  if (d.kind !== 'Dashboard') return { ok: false, error: 'kind must be "Dashboard" (Perses format)' };
  const spec = d.spec ?? {};
  const name: string | undefined = spec.display?.name ?? d.metadata?.name;
  if (!name || typeof name !== 'string') {
    return { ok: false, error: 'spec.display.name (or metadata.name) is required' };
  }
  const pmap = spec.panels;
  if (!pmap || typeof pmap !== 'object') return { ok: false, error: 'spec.panels must be a map of panels' };

  // Layout-Reihenfolge + Breiten aus dem Grid ($ref → panel key).
  const widthByRef: Record<string, number> = {};
  const orderByRef: Record<string, number> = {};
  let ord = 0;
  for (const l of Array.isArray(spec.layouts) ? spec.layouts : []) {
    for (const it of l?.spec?.items ?? []) {
      const ref: unknown = it?.content?.$ref;
      if (typeof ref === 'string') {
        const key = ref.split('/').pop();
        if (key) {
          widthByRef[key] = Number(it.width) || 8;
          orderByRef[key] = ord++;
        }
      }
    }
  }

  const entries = Object.entries(pmap as Record<string, any>);
  entries.sort((a, b) => (orderByRef[a[0]] ?? 999) - (orderByRef[b[0]] ?? 999));

  const panels: DashPanel[] = [];
  for (const [key, pv] of entries) {
    const ps = pv?.spec ?? {};
    const title: string = ps.display?.name ?? key;
    const query: unknown = ps.queries?.[0]?.spec?.plugin?.spec?.query;
    if (!query || typeof query !== 'string') {
      return { ok: false, error: `panel "${key}": missing query at spec.queries[0].spec.plugin.spec.query` };
    }
    const unit: unknown = ps.plugin?.spec?.yAxis?.format?.unit;
    const cols = widthByRef[key] ?? 8;
    const w = cols >= 24 ? 3 : cols >= 16 ? 2 : 1;
    panels.push({ title, query, ...(typeof unit === 'string' && unit ? { unit } : {}), w });
  }
  if (panels.length === 0) return { ok: false, error: 'a dashboard needs at least one panel' };

  return {
    ok: true,
    spec: { name, description: spec.display?.description, panels },
  };
}

/* ── Persistenz (localStorage — Backend-Store ist der nächste Schritt) ────── */

const LS_KEY = (cluster: string) => `rp-dashboards-${cluster}`;

export function loadCustomDashboards(cluster: string): Dashboard[] {
  if (typeof window === 'undefined') return [];
  try {
    const raw = window.localStorage.getItem(LS_KEY(cluster));
    if (!raw) return [];
    const arr = JSON.parse(raw) as Dashboard[];
    return Array.isArray(arr) ? arr : [];
  } catch {
    return [];
  }
}

export function saveCustomDashboards(cluster: string, dashboards: Dashboard[]): void {
  if (typeof window === 'undefined') return;
  try {
    window.localStorage.setItem(LS_KEY(cluster), JSON.stringify(dashboards));
  } catch {
    /* Quota/Privacy — die eingebauten funktionieren weiter */
  }
}

// Eindeutige, kollisionsfreie id aus einem Namen.
export function uniqueId(name: string, taken: Set<string>): string {
  const base = name.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '') || 'dashboard';
  let id = base;
  let n = 2;
  while (taken.has(id)) id = `${base}-${n++}`;
  return id;
}
