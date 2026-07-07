'use client';

import type { ReactNode } from 'react';

// copilot-panels.tsx — die Inspector-Visualisierungen. Der Copilot zeigt NIE rohes
// JSON: jedes Tool-Result wird als das gerendert, was es ist — Log-Zeilen,
// Infra-Mini-Bars, Metrik-Sparklines, Service-Map-Health, Trace-Histogramme.

/* ── Parsing ─────────────────────────────────────────────────────────────── */

export function parseResult(raw?: string): unknown {
  if (!raw) return null;
  let s = raw;
  const cut = s.indexOf('\n…(truncated');
  if (cut >= 0) s = s.slice(0, cut);
  try { return JSON.parse(s); } catch { return null; }
}

const asObj = (v: unknown): Record<string, unknown> => (v && typeof v === 'object' && !Array.isArray(v) ? (v as Record<string, unknown>) : {});
const asArr = (v: unknown): unknown[] => (Array.isArray(v) ? v : []);
const num = (v: unknown): number => (typeof v === 'number' ? v : typeof v === 'string' ? parseFloat(v) || 0 : 0);
const str = (v: unknown): string => (v == null ? '' : String(v));

function fmtBytes(b: number): string {
  if (b < 0) return '—';
  const u = ['B', 'K', 'M', 'G', 'T'];
  let i = 0;
  while (b >= 1024 && i < u.length - 1) { b /= 1024; i++; }
  return `${b.toFixed(b < 10 && i > 0 ? 1 : 0)}${u[i]}`;
}
const hhmmss = (ts: string) => { const m = /(\d{2}:\d{2}:\d{2})/.exec(ts); return m ? m[1]! : ts.slice(0, 8); };

/* ── Primitive ───────────────────────────────────────────────────────────── */

function pctTone(p: number): string {
  return p >= 90 ? 'var(--rp-node-crit)' : p >= 70 ? 'var(--rp-node-warn)' : 'var(--rp-node-ok)';
}

function Meter({ label, pct, right }: { label: string; pct: number; right?: string }) {
  const p = Math.max(0, Math.min(100, pct));
  return (
    <div className="flex items-center gap-2">
      <span className="w-8 shrink-0 font-mono text-[9.5px] uppercase tracking-[0.04em] text-faint">{label}</span>
      <div className="h-1.5 flex-1 overflow-hidden rounded-full" style={{ background: 'var(--rp-bg-inset)' }}>
        <div className="h-full rounded-full" style={{ width: `${p}%`, background: pctTone(p) }} />
      </div>
      <span className="w-16 shrink-0 text-right font-mono text-[10px] tabular-nums text-mid">{right ?? `${p.toFixed(0)}%`}</span>
    </div>
  );
}

export function Sparkline({ values, height = 34 }: { values: number[]; height?: number }) {
  if (values.length < 2) return <div className="font-mono text-[10px] text-faint">not enough samples</div>;
  const min = Math.min(...values);
  const max = Math.max(...values);
  const span = max - min || 1;
  const n = values.length - 1;
  const pts = values.map((v, i) => `${(i / n) * 100},${30 - ((v - min) / span) * 28 + 1}`);
  const area = `0,31 ${pts.join(' ')} 100,31`;
  return (
    <svg viewBox="0 0 100 32" preserveAspectRatio="none" className="w-full" style={{ height }}>
      <polygon points={area} fill="var(--rp-accent)" opacity={0.1} />
      <polyline points={pts.join(' ')} fill="none" stroke="var(--rp-accent)" strokeWidth={1.2} vectorEffect="non-scaling-stroke" strokeLinejoin="round" strokeLinecap="round" />
    </svg>
  );
}

// Log/Trace-Histogramm: Balken mit Fehler-Overlay.
function Histogram({ data }: { data: Array<{ count?: number; errors?: number; warns?: number }> }) {
  if (!data.length) return null;
  const max = Math.max(1, ...data.map((d) => num(d.count)));
  return (
    <div className="flex h-9 items-end gap-px">
      {data.map((d, i) => {
        const c = num(d.count);
        const e = num(d.errors);
        const h = (c / max) * 100;
        const eh = c > 0 ? (e / c) * h : 0;
        return (
          <div key={i} className="flex-1" style={{ height: '100%' }} title={`${c} · ${e} err`}>
            <div className="relative w-full" style={{ height: '100%' }}>
              <div className="absolute bottom-0 w-full rounded-t-[1px]" style={{ height: `${h}%`, background: 'var(--rp-line-strong)' }} />
              {eh > 0 ? <div className="absolute bottom-0 w-full rounded-t-[1px]" style={{ height: `${eh}%`, background: 'var(--rp-tone-red-fg)' }} /> : null}
            </div>
          </div>
        );
      })}
    </div>
  );
}

function Stat({ label, value, tone }: { label: string; value: ReactNode; tone?: string }) {
  return (
    <div className="rounded-skin-sm border border-line bg-inset px-2 py-1.5">
      <div className="font-mono text-[13px] font-semibold tabular-nums" style={{ color: tone ?? 'var(--rp-ink)' }}>{value}</div>
      <div className="rp-micro !text-[9px]">{label}</div>
    </div>
  );
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div>
      <p className="rp-micro !text-[10px] mb-1.5">{title}</p>
      {children}
    </div>
  );
}

/* ── Tool-spezifische Panels ─────────────────────────────────────────────── */

function LogsPanel({ d }: { d: Record<string, unknown> }) {
  const lines = asArr(d.lines);
  const hist = asArr(d.histogram) as Array<{ count?: number; errors?: number; warns?: number }>;
  const sevTone = (s: string) => {
    const u = s.toUpperCase();
    if (u.startsWith('ERR') || u === 'FATAL') return 'var(--rp-tone-red-fg)';
    if (u.startsWith('WARN')) return 'var(--rp-tone-yellow-fg)';
    if (u.startsWith('DEBUG') || u.startsWith('TRACE')) return 'var(--rp-ink-faint)';
    return 'var(--rp-ink-muted)';
  };
  return (
    <div className="space-y-3">
      {hist.length ? <Section title="volume · 15m"><Histogram data={hist} /></Section> : null}
      <Section title={`log lines · ${lines.length}`}>
        <div className="space-y-px overflow-hidden rounded-skin-sm border border-line">
          {lines.slice(0, 60).map((l, i) => {
            const o = asObj(l);
            const sev = str(o.severityText || o.severity);
            return (
              <div key={i} className="flex items-start gap-2 border-b border-line bg-inset px-2 py-1 last:border-0">
                <span className="shrink-0 font-mono text-[9.5px] tabular-nums text-faint">{hhmmss(str(o.ts))}</span>
                <span className="w-9 shrink-0 font-mono text-[9px] uppercase" style={{ color: sevTone(sev) }}>{sev.slice(0, 4)}</span>
                <span className="shrink-0 truncate font-mono text-[9.5px] text-muted" style={{ maxWidth: 90 }}>{str(o.workloadName)}</span>
                <span className="min-w-0 flex-1 break-words font-mono text-[10px] leading-snug text-mid">{str(o.body)}</span>
              </div>
            );
          })}
          {!lines.length ? <div className="bg-inset px-2 py-3 text-center font-mono text-[10.5px] text-faint">no log lines in window</div> : null}
        </div>
      </Section>
    </div>
  );
}

function InfraPanel({ d }: { d: Record<string, unknown> }) {
  const nodes = asArr(d.nodes);
  const pvcs = asArr(d.pvcs);
  return (
    <div className="space-y-3">
      <Section title={`nodes · ${nodes.length}`}>
        <div className="space-y-2.5">
          {nodes.map((n, i) => {
            const o = asObj(n);
            const cpu = (num(o.cpuUsageM) / (num(o.cpuAllocatableM) || 1)) * 100;
            const mem = (num(o.memUsage) / (num(o.memAllocatable) || 1)) * 100;
            const disk = (num(o.fsUsed) / (num(o.fsCapacity) || 1)) * 100;
            const pods = (num(o.podCount) / (num(o.podCapacity) || 1)) * 100;
            const ready = o.ready === true;
            const pressure = str(o.pressure);
            return (
              <div key={i} className="rounded-skin-sm border border-line bg-inset p-2.5">
                <div className="mb-2 flex items-center gap-2">
                  <span style={{ color: ready ? 'var(--rp-tone-green-fg)' : 'var(--rp-tone-red-fg)' }}>{ready ? '●' : '▲'}</span>
                  <span className="font-mono text-[11px] font-semibold text-ink">{str(o.name)}</span>
                  <span className="font-mono text-[9px] text-faint">{str(o.role)}</span>
                  {pressure ? <span className="ml-auto rounded-skin-chip px-1 font-mono text-[8.5px] uppercase" style={{ color: 'var(--rp-tone-yellow-fg)', background: 'var(--rp-tone-yellow-bg)' }}>{pressure}</span> : null}
                </div>
                <div className="space-y-1">
                  <Meter label="cpu" pct={cpu} right={`${(num(o.cpuUsageM) / 1000).toFixed(1)} / ${(num(o.cpuAllocatableM) / 1000).toFixed(0)} c`} />
                  <Meter label="mem" pct={mem} right={`${fmtBytes(num(o.memUsage))} / ${fmtBytes(num(o.memAllocatable))}`} />
                  <Meter label="disk" pct={disk} right={`${fmtBytes(num(o.fsUsed))} / ${fmtBytes(num(o.fsCapacity))}`} />
                  <Meter label="pods" pct={pods} right={`${num(o.podCount)} / ${num(o.podCapacity)}`} />
                </div>
              </div>
            );
          })}
        </div>
      </Section>
      {pvcs.length ? (
        <Section title={`persistent volumes · ${pvcs.length}`}>
          <div className="space-y-1.5">
            {pvcs.map((p, i) => {
              const o = asObj(p);
              const used = num(o.usedBytes);
              const cap = num(o.capacityBytes) || num(o.requestedBytes);
              const pct = used >= 0 && cap > 0 ? (used / cap) * 100 : -1;
              const bound = str(o.phase) === 'Bound';
              return (
                <div key={i} className="rounded-skin-sm border border-line bg-inset p-2">
                  <div className="mb-1 flex items-center gap-2">
                    <span style={{ color: bound ? 'var(--rp-tone-green-fg)' : 'var(--rp-tone-yellow-fg)' }}>◆</span>
                    <span className="font-mono text-[10.5px] text-ink">{str(o.namespace)}/{str(o.name)}</span>
                    <span className="ml-auto font-mono text-[9px] text-faint">{str(o.storageClass)}</span>
                  </div>
                  {pct >= 0 ? <Meter label="use" pct={pct} right={`${fmtBytes(used)} / ${fmtBytes(cap)}`} /> : (
                    <div className="font-mono text-[9.5px] text-faint">{str(o.phase)} · {fmtBytes(cap)} · {(asArr(o.mountedBy)).map(str).join(', ') || 'unmounted'}</div>
                  )}
                </div>
              );
            })}
          </div>
        </Section>
      ) : null}
    </div>
  );
}

function MetricsPanel({ d }: { d: Record<string, unknown> }) {
  const data = asObj(d.data);
  const result = asArr(data.result);
  if (!result.length) {
    return <div className="rounded-skin-sm border border-line bg-inset px-3 py-4 text-center font-mono text-[10.5px] text-faint">query ran — no samples in the window</div>;
  }
  const seriesLabel = (metric: Record<string, unknown>): string => {
    const name = str(metric.__name__);
    const rest = Object.entries(metric).filter(([k]) => k !== '__name__').map(([k, v]) => `${k}=${str(v)}`);
    return [name, ...rest].filter(Boolean).join(' · ') || 'series';
  };
  return (
    <div className="space-y-2.5">
      {result.slice(0, 8).map((s, i) => {
        const o = asObj(s);
        const values = asArr(o.values).map((pt) => num(asArr(pt)[1]));
        const last = values[values.length - 1] ?? 0;
        return (
          <div key={i} className="rounded-skin-sm border border-line bg-inset p-2.5">
            <div className="mb-1 flex items-baseline gap-2">
              <span className="min-w-0 flex-1 truncate font-mono text-[10px] text-muted">{seriesLabel(asObj(o.metric))}</span>
              <span className="font-mono text-[12px] font-semibold tabular-nums text-ink">{last < 1 && last > 0 ? last.toPrecision(2) : last.toFixed(last % 1 === 0 ? 0 : 2)}</span>
            </div>
            <Sparkline values={values} />
          </div>
        );
      })}
    </div>
  );
}

function ServiceMapPanel({ d }: { d: Record<string, unknown> }) {
  const nodes = asArr(d.nodes);
  const byHealth: Record<string, number> = {};
  for (const n of nodes) { const h = str(asObj(n).health) || 'unknown'; byHealth[h] = (byHealth[h] ?? 0) + 1; }
  const tone: Record<string, string> = { healthy: 'var(--rp-tone-green-fg)', degraded: 'var(--rp-tone-red-fg)', progressing: 'var(--rp-tone-yellow-fg)', unknown: 'var(--rp-ink-faint)' };
  const unhealthy = nodes.filter((n) => str(asObj(n).health) !== 'healthy');
  return (
    <div className="space-y-3">
      <div className="grid grid-cols-3 gap-1.5">
        <Stat label="workloads" value={nodes.length} />
        <Stat label="healthy" value={byHealth.healthy ?? 0} tone="var(--rp-tone-green-fg)" />
        <Stat label="unhealthy" value={nodes.length - (byHealth.healthy ?? 0)} tone={unhealthy.length ? 'var(--rp-tone-red-fg)' : undefined} />
      </div>
      <Section title="health">
        <div className="flex flex-wrap gap-1.5">
          {Object.entries(byHealth).map(([h, c]) => (
            <span key={h} className="inline-flex items-center gap-1 rounded-skin-chip border border-line bg-inset px-1.5 py-0.5 font-mono text-[9.5px]">
              <span style={{ color: tone[h] ?? 'var(--rp-ink-muted)' }}>●</span>{h} <span className="tabular-nums text-mid">{c}</span>
            </span>
          ))}
        </div>
      </Section>
      {unhealthy.length ? (
        <Section title={`needs attention · ${unhealthy.length}`}>
          <div className="space-y-1">
            {unhealthy.slice(0, 20).map((n, i) => {
              const o = asObj(n);
              return (
                <div key={i} className="flex items-center gap-2 rounded-skin-sm border border-line bg-inset px-2 py-1 font-mono text-[10px]">
                  <span style={{ color: tone[str(o.health)] ?? 'var(--rp-tone-red-fg)' }}>▲</span>
                  <span className="min-w-0 flex-1 truncate text-ink">{str(o.namespace)}/{str(o.name)}</span>
                  <span className="tabular-nums text-mid">{num(o.podsReady)}/{num(o.podsTotal)} ready</span>
                  {num(o.restarts) > 0 ? <span className="tabular-nums" style={{ color: 'var(--rp-tone-yellow-fg)' }}>↻{num(o.restarts)}</span> : null}
                </div>
              );
            })}
          </div>
        </Section>
      ) : null}
    </div>
  );
}

const httpTone = (s: number) => (s >= 500 ? 'var(--rp-tone-red-fg)' : s >= 400 ? 'var(--rp-tone-yellow-fg)' : s >= 200 ? 'var(--rp-tone-green-fg)' : 'var(--rp-ink-muted)');

function TracesPanel({ d }: { d: Record<string, unknown> }) {
  const hist = asArr(d.histogram) as Array<{ count?: number; errors?: number }>;
  const red = asArr(d.red);
  const list = asArr(d.traces);
  const total = hist.reduce((a, b) => a + num(b.count), 0);
  const errs = hist.reduce((a, b) => a + num(b.errors), 0);
  return (
    <div className="space-y-3">
      <div className="grid grid-cols-2 gap-1.5">
        <Stat label="spans · window" value={total} />
        <Stat label="errors" value={errs} tone={errs ? 'var(--rp-tone-red-fg)' : undefined} />
      </div>
      {hist.length ? <Section title="volume"><Histogram data={hist} /></Section> : null}
      {red.length ? (
        <Section title="RED · per service">
          <div className="overflow-hidden rounded-skin-sm border border-line">
            <div className="flex items-center gap-2 border-b border-line bg-inset px-2 py-1 font-mono text-[9px] uppercase tracking-[0.04em] text-faint">
              <span className="min-w-0 flex-1">service</span><span className="w-12 text-right">req/m</span><span className="w-10 text-right">err</span><span className="w-12 text-right">p95</span>
            </div>
            {red.slice(0, 12).map((r, i) => {
              const o = asObj(r);
              const er = num(o.errorRatio) * 100;
              return (
                <div key={i} className="flex items-center gap-2 border-b border-line bg-inset px-2 py-1 font-mono text-[10px] last:border-0">
                  <span className="min-w-0 flex-1 truncate text-ink">{str(o.serviceName)}</span>
                  <span className="w-12 text-right tabular-nums text-mid">{num(o.ratePerMin).toFixed(0)}</span>
                  <span className="w-10 text-right tabular-nums" style={{ color: er >= 1 ? 'var(--rp-tone-red-fg)' : 'var(--rp-tone-green-fg)' }}>{er.toFixed(er < 10 ? 1 : 0)}%</span>
                  <span className="w-12 text-right tabular-nums text-mid">{num(o.p95Ms).toFixed(0)}ms</span>
                </div>
              );
            })}
          </div>
        </Section>
      ) : null}
      {list.length ? (
        <Section title={`traces · ${list.length}`}>
          <div className="space-y-1">
            {list.slice(0, 30).map((t, i) => {
              const o = asObj(t);
              const http = num(o.httpStatus);
              const dur = num(o.durationMs);
              const err = num(o.errorCount) > 0 || str(o.statusCode).toUpperCase().includes('ERROR');
              return (
                <div key={i} className="flex items-center gap-2 rounded-skin-sm border border-line bg-inset px-2 py-1 font-mono text-[10px]">
                  <span style={{ color: err ? 'var(--rp-tone-red-fg)' : 'var(--rp-ink-faint)' }}>{err ? '▲' : '·'}</span>
                  <span className="min-w-0 flex-1 truncate text-ink">{str(o.serviceName)} <span className="text-faint">{str(o.spanName)}</span></span>
                  {dur ? <span className="tabular-nums text-mid">{dur.toFixed(dur < 10 ? 1 : 0)}ms</span> : null}
                  {http ? <span className="tabular-nums" style={{ color: httpTone(http) }}>{http}</span> : null}
                </div>
              );
            })}
          </div>
        </Section>
      ) : null}
    </div>
  );
}

// get_trace → Waterfall: Spans nach Startzeit, Balken relativ zur Trace-Dauer.
function TraceWaterfallPanel({ d }: { d: Record<string, unknown> }) {
  const spans = asArr(d.spans).map(asObj);
  if (!spans.length) return <div className="rounded-skin-sm border border-line bg-inset px-3 py-4 text-center font-mono text-[10.5px] text-faint">no spans for this trace</div>;
  const starts = spans.map((s) => num(s.startUnixNs));
  const t0 = Math.min(...starts);
  const ends = spans.map((s) => num(s.startUnixNs) + num(s.durationMs) * 1e6);
  const totalNs = Math.max(...ends) - t0 || 1;
  const totalMs = totalNs / 1e6;
  const ordered = spans.map((s, i) => ({ s, i })).sort((a, b) => num(a.s.startUnixNs) - num(b.s.startUnixNs));
  return (
    <div className="space-y-2">
      <div className="flex items-baseline justify-between font-mono text-[10px]">
        <span className="text-muted">{spans.length} spans</span>
        <span className="tabular-nums text-ink">{totalMs.toFixed(totalMs < 10 ? 1 : 0)}ms total</span>
      </div>
      <div className="space-y-[3px]">
        {ordered.map(({ s }, k) => {
          const off = ((num(s.startUnixNs) - t0) / totalNs) * 100;
          const w = Math.max(1.5, (num(s.durationMs) * 1e6 / totalNs) * 100);
          const err = str(s.statusCode).toUpperCase().includes('ERROR') || num(s.httpStatus) >= 500;
          return (
            <div key={k} className="group">
              <div className="flex items-center gap-1.5 font-mono text-[9.5px]">
                <span className="min-w-0 flex-1 truncate text-mid">{str(s.serviceName)}<span className="text-faint"> · {str(s.spanName)}</span></span>
                <span className="shrink-0 tabular-nums text-faint">{num(s.durationMs).toFixed(num(s.durationMs) < 10 ? 1 : 0)}ms</span>
                {num(s.httpStatus) ? <span className="shrink-0 tabular-nums" style={{ color: httpTone(num(s.httpStatus)) }}>{num(s.httpStatus)}</span> : null}
              </div>
              <div className="relative mt-px h-1.5 w-full overflow-hidden rounded-full" style={{ background: 'var(--rp-bg-inset)' }}>
                <div className="absolute h-full rounded-full" style={{ left: `${off}%`, width: `${w}%`, background: err ? 'var(--rp-tone-red-fg)' : 'var(--rp-accent)', opacity: err ? 0.9 : 0.7 }} />
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}

// span_stats → Perzentile + Dauer-Histogramm.
function SpanStatsPanel({ d }: { d: Record<string, unknown> }) {
  const ps: Array<[string, number]> = [['p50', num(d.p50)], ['p75', num(d.p75)], ['p90', num(d.p90)], ['p95', num(d.p95)], ['p99', num(d.p99)]];
  const maxP = Math.max(1, ...ps.map(([, v]) => v));
  const histo = asArr(d.histogram).map((b) => asArr(b));
  const maxH = Math.max(1, ...histo.map((b) => num(b[2])));
  return (
    <div className="space-y-3">
      <Stat label="samples" value={num(d.count)} />
      <Section title="latency percentiles">
        <div className="space-y-1">
          {ps.map(([k, v]) => (
            <div key={k} className="flex items-center gap-2">
              <span className="w-8 shrink-0 font-mono text-[9.5px] uppercase text-faint">{k}</span>
              <div className="h-1.5 flex-1 overflow-hidden rounded-full" style={{ background: 'var(--rp-bg-inset)' }}>
                <div className="h-full rounded-full" style={{ width: `${(v / maxP) * 100}%`, background: k === 'p99' || k === 'p95' ? 'var(--rp-node-warn)' : 'var(--rp-accent)' }} />
              </div>
              <span className="w-16 shrink-0 text-right font-mono text-[10px] tabular-nums text-mid">{v.toFixed(v < 10 ? 1 : 0)}ms</span>
            </div>
          ))}
        </div>
      </Section>
      {histo.length ? (
        <Section title="distribution">
          <div className="flex h-10 items-end gap-px">
            {histo.map((b, i) => (
              <div key={i} className="flex-1 rounded-t-[1px]" style={{ height: `${(num(b[2]) / maxH) * 100}%`, background: 'var(--rp-line-strong)' }} title={`${num(b[0]).toFixed(0)}–${num(b[1]).toFixed(0)}ms`} />
            ))}
          </div>
        </Section>
      ) : null}
    </div>
  );
}

function MetricsListPanel({ d }: { d: Record<string, unknown> }) {
  const metrics = asArr(d.metrics).map(str);
  return (
    <Section title={`metrics · ${num(d.count) || metrics.length}`}>
      <div className="flex flex-wrap gap-1">
        {metrics.slice(0, 120).map((m, i) => (
          <span key={i} className="rounded-skin-chip border border-line bg-inset px-1.5 py-0.5 font-mono text-[9.5px] text-mid">{m}</span>
        ))}
        {!metrics.length ? <span className="font-mono text-[10.5px] text-faint">no metric names available</span> : null}
      </div>
    </Section>
  );
}

// Generischer Struktur-View (kein roher JSON-Dump) als Fallback für unbekannte Shapes.
function KeyValue({ d }: { d: Record<string, unknown> }) {
  const entries = Object.entries(d);
  return (
    <div className="divide-y divide-line overflow-hidden rounded-skin-sm border border-line">
      {entries.map(([k, v]) => {
        let val: ReactNode;
        if (Array.isArray(v)) val = <span className="text-mid">{v.length} item{v.length === 1 ? '' : 's'}</span>;
        else if (v && typeof v === 'object') val = <span className="text-faint">{Object.keys(v).length} fields</span>;
        else val = <span className="break-all text-mid">{str(v)}</span>;
        return (
          <div key={k} className="flex items-start gap-2 bg-inset px-2 py-1.5 font-mono text-[10.5px]">
            <span className="w-28 shrink-0 truncate text-faint">{k}</span>
            <span className="min-w-0 flex-1">{val}</span>
          </div>
        );
      })}
    </div>
  );
}

/* ── Dispatcher ──────────────────────────────────────────────────────────── */

export function ToolResultView({ name, result, status }: { name: string; result?: string; status: string }) {
  if (status === 'running') {
    return <div className="flex items-center gap-2 rounded-skin-sm border border-line bg-inset p-3 font-mono text-[11px] text-muted"><span className="rp-breath">◐</span> running…</div>;
  }
  if (status === 'error') {
    return <pre className="overflow-x-auto whitespace-pre-wrap rounded-skin-sm border border-line bg-inset p-2 font-mono text-[10.5px] leading-relaxed" style={{ color: 'var(--rp-tone-red-fg)' }}>{result || 'error'}</pre>;
  }
  const parsed = parseResult(result);
  const d = asObj(parsed);
  if (parsed && typeof parsed === 'object') {
    switch (name) {
      case 'query_logs': return <LogsPanel d={d} />;
      case 'trace_logs': return <LogsPanel d={d} />;
      case 'get_infra': return <InfraPanel d={d} />;
      case 'query_metrics': return <MetricsPanel d={d} />;
      case 'get_service_map': return <ServiceMapPanel d={d} />;
      case 'query_traces': return <TracesPanel d={d} />;
      case 'get_trace': return <TraceWaterfallPanel d={d} />;
      case 'span_stats': return <SpanStatsPanel d={d} />;
      case 'list_metrics': return <MetricsListPanel d={d} />;
      default: return <KeyValue d={d} />;
    }
  }
  // Nicht-JSON (z.B. gekürzt): als Textausschnitt zeigen, nicht als Dump.
  return <pre className="overflow-x-auto whitespace-pre-wrap rounded-skin-sm border border-line bg-inset p-2 font-mono text-[10.5px] leading-relaxed text-mid">{result || '(empty)'}</pre>;
}
