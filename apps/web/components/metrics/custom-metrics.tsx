'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import { Spinner } from '@/components/ui';
import { LineChart } from '@/components/metrics/line-chart';
import { PromQLEditor } from '@/components/metrics/promql-editor';
import {
  createMetricDefinition,
  deleteMetricDefinition,
  getDerivedSeries,
  getMetricDefinitions,
  previewMetricDefinition,
  updateMetricDefinition,
} from '@/lib/api/controlplane';
import type { MetricDefinition, MetricSeries } from '@/lib/api/types';

// custom-metrics.tsx — Custom-Metriken SIND PromQL (sauber wie Dash0, kein
// Formular-Builder): Name + Unit + Ausdruck, Live-Preview, Save-Zeit-
// Verifikation (Parse + Probe-Ausführung im Backend). Jede Metrik rendert
// als eigenes Chart und steht dem Alert-Editor als Bedingung zur Verfügung.

export type MetricDraft = Partial<MetricDefinition>;

export function newPromQLDraft(query: string): MetricDraft {
  return { name: '', description: '', source: 'promql', query, unit: '', agg: 'rate', valueMode: 'count' };
}

export function CustomMetrics({
  orgId,
  clusterId,
  sinceMin,
  openWith,
  onConsumedOpenWith,
}: {
  orgId: string;
  clusterId: string;
  sinceMin: number;
  /** vom promql-Tab: „Save as metric" öffnet den Editor mit dieser Query */
  openWith?: MetricDraft | null;
  onConsumedOpenWith?: () => void;
}) {
  const [defs, setDefs] = useState<MetricDefinition[] | null>(null);
  const [charts, setCharts] = useState<Record<string, MetricSeries[]>>({});
  const [editor, setEditor] = useState<MetricDraft | null>(null);

  useEffect(() => {
    if (openWith) {
      setEditor(openWith);
      onConsumedOpenWith?.();
    }
  }, [openWith, onConsumedOpenWith]);

  const load = useCallback(() => {
    getMetricDefinitions(orgId, clusterId)
      .then((r) => setDefs(r.definitions))
      .catch(() => setDefs([]));
  }, [orgId, clusterId]);

  useEffect(load, [load]);

  useEffect(() => {
    if (!defs) return;
    let alive = true;
    const fetchAll = async () => {
      const since = new Date(Date.now() - sinceMin * 60_000).toISOString();
      const out: Record<string, MetricSeries[]> = {};
      await Promise.all(
        defs.map(async (d) => {
          const r = await getDerivedSeries(orgId, clusterId, d.id, since).catch(() => null);
          out[d.id] = r?.series ?? [];
        }),
      );
      if (alive) setCharts(out);
    };
    void fetchAll();
    const t = setInterval(fetchAll, 15_000);
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, [defs, orgId, clusterId, sinceMin]);

  return (
    <div>
      <div className="flex items-center justify-between">
        <span className="rp-micro !text-[10px]">custom metrics — named promql expressions</span>
        <button
          type="button"
          onClick={() => setEditor(newPromQLDraft('sum by (service_name) (rate(http_server_request_duration_count[5m])) * 60'))}
          className="rp-focus h-8 rounded-skin-sm px-3 font-mono text-[11px] font-semibold transition-opacity hover:opacity-90"
          style={{ background: 'var(--rp-btn-bg)', color: 'var(--rp-btn-fg)' }}
        >
          + New metric
        </button>
      </div>

      {defs === null ? (
        <div className="mt-4 flex items-center gap-2 text-muted">
          <Spinner /> <span className="font-mono text-[11px]">loading…</span>
        </div>
      ) : defs.length === 0 ? (
        <div
          className="mt-3 rounded-skin border border-dashed p-6 text-center font-mono text-[11.5px] leading-relaxed text-muted"
          style={{ borderColor: 'var(--rp-line-strong)' }}
        >
          No custom metrics yet — write a PromQL expression, give it a name,
          <br />
          and it becomes a chart here and an alert condition everywhere.
        </div>
      ) : (
        <div className="mt-3 grid grid-cols-1 gap-3 lg:grid-cols-2">
          {defs.map((d) => (
            <div key={d.id} className="relative">
              <LineChart
                title={`${d.name}${d.description ? ` — ${d.description}` : ''}`}
                unit={d.unit || ''}
                series={charts[d.id] ?? []}
              />
              <div className="absolute right-3 top-2.5 flex items-center gap-1.5">
                <span className="rounded-skin-chip bg-inset px-1.5 py-0.5 font-mono text-[8.5px] uppercase tracking-[0.05em] text-faint">
                  {d.source === 'promql' ? 'promql' : `${d.source} · ${d.valueMode}`}
                </span>
                <button
                  type="button"
                  onClick={() => setEditor({ ...d })}
                  className="rounded-skin-chip border border-line px-1.5 py-0.5 font-mono text-[9px] text-muted transition-colors hover:text-ink"
                >
                  edit
                </button>
              </div>
            </div>
          ))}
        </div>
      )}

      {editor ? (
        <MetricEditor
          orgId={orgId}
          clusterId={clusterId}
          def={editor}
          onClose={() => setEditor(null)}
          onSaved={() => {
            setEditor(null);
            load();
          }}
        />
      ) : null}
    </div>
  );
}

/* ── Editor: Name + Unit + PromQL + Live-Preview ────────────────────────── */

function MetricEditor({
  orgId,
  clusterId,
  def,
  onClose,
  onSaved,
}: {
  orgId: string;
  clusterId: string;
  def: MetricDraft;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [d, setD] = useState<MetricDraft>({ ...def });
  const [preview, setPreview] = useState<MetricSeries[] | null>(null);
  const [previewErr, setPreviewErr] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const set = (patch: MetricDraft) => setD((cur) => ({ ...cur, ...patch }));
  const isPromql = d.source === 'promql';
  const apiPrefix = `/api/orgs/${encodeURIComponent(orgId)}/clusters/${encodeURIComponent(clusterId)}/promql`;

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') onClose();
    }
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [onClose]);

  // Live-Preview (debounced) — für promql UND legacy-Definitionen.
  const debounce = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(() => {
    if (debounce.current) clearTimeout(debounce.current);
    debounce.current = setTimeout(() => {
      previewMetricDefinition(orgId, clusterId, d)
        .then((r) => {
          setPreview(r.series);
          setPreviewErr(null);
        })
        .catch((e) => {
          setPreview(null);
          setPreviewErr(e instanceof Error ? e.message : 'preview failed');
        });
    }, 500);
    return () => {
      if (debounce.current) clearTimeout(debounce.current);
    };
  }, [orgId, clusterId, d]);

  const inputCls =
    'rp-focus mt-1 h-9 w-full rounded-skin-sm border border-line bg-inset px-2.5 font-mono text-[12px] text-ink placeholder:text-faint';

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center" role="dialog" aria-modal="true" aria-label="Metric editor">
      <button type="button" aria-label="Close" onClick={onClose} className="absolute inset-0 cursor-default" style={{ backgroundColor: 'var(--rp-scrim)' }} />
      <div
        className="relative flex max-h-[92vh] w-[760px] flex-col overflow-hidden rounded-skin border border-line bg-raised"
        style={{ boxShadow: 'var(--rp-rim), var(--rp-shadow-pop)', animation: 'reveal-up var(--rp-dur-med) var(--rp-ease-enter)' }}
      >
        <div className="border-b border-line px-4 py-3">
          <span className="font-mono text-[13px] font-semibold text-ink">
            {d.id ? `Edit ${def.name}` : 'New custom metric'}
          </span>
          <span className="rp-micro ml-2 !text-[9px]">verified on save — parse + probe run</span>
        </div>

        <div className="min-h-0 flex-1 space-y-3 overflow-y-auto px-4 py-3">
          <div className="grid grid-cols-[2fr_1fr] gap-2">
            <label className="block">
              <span className="rp-micro !text-[10px]">name (kebab-case)</span>
              <input value={d.name ?? ''} onChange={(e) => set({ name: e.target.value })} placeholder="checkout-p95" className={inputCls} />
            </label>
            <label className="block">
              <span className="rp-micro !text-[10px]">unit label</span>
              <input value={d.unit ?? ''} onChange={(e) => set({ unit: e.target.value })} placeholder="ms · req/min · %" className={inputCls} />
            </label>
          </div>
          <label className="block">
            <span className="rp-micro !text-[10px]">description <span className="text-faint">(optional)</span></span>
            <input value={d.description ?? ''} onChange={(e) => set({ description: e.target.value })} className={inputCls} />
          </label>

          {isPromql ? (
            <div>
              <span className="rp-micro !text-[10px]">promql expression</span>
              <div className="mt-1">
                <PromQLEditor value={d.query ?? ''} apiPrefix={apiPrefix} onChange={(q) => set({ query: q })} onRun={() => {}} />
              </div>
            </div>
          ) : (
            <div
              className="rounded-skin-sm border border-dashed px-3 py-2 font-mono text-[10.5px] leading-relaxed"
              style={{ borderColor: 'var(--rp-line-strong)', color: 'var(--rp-ink-muted)' }}
            >
              legacy typed metric ({d.source} · {d.valueMode}) — still evaluated, but new metrics are
              defined in PromQL. Delete and recreate to migrate.
            </div>
          )}

          {/* Live-Preview */}
          <div>
            <span className="rp-micro !text-[10px]">live preview — last 15 minutes</span>
            <div className="mt-1.5">
              {previewErr ? (
                <div className="rounded-skin-sm px-3 py-2 font-mono text-[11px] leading-relaxed" style={{ color: 'var(--rp-tone-red-fg)', background: 'var(--rp-tone-red-bg)' }}>
                  {previewErr}
                </div>
              ) : preview ? (
                preview.length === 0 ? (
                  <div className="font-mono text-[10.5px] text-faint">empty result in the last 15 minutes</div>
                ) : (
                  <LineChart title={d.name || 'preview'} unit={d.unit || ''} series={preview.slice(0, 10)} height={150} />
                )
              ) : (
                <div className="flex items-center gap-2 text-muted">
                  <Spinner /> <span className="font-mono text-[11px]">evaluating…</span>
                </div>
              )}
            </div>
          </div>
          {err ? <div className="font-mono text-[11px]" style={{ color: 'var(--rp-tone-red-fg)' }}>{err}</div> : null}
        </div>

        <div className="flex items-center gap-2 border-t border-line px-4 py-3">
          <button
            type="button"
            disabled={busy || !isPromql}
            onClick={async () => {
              setBusy(true);
              setErr(null);
              try {
                if (d.id) await updateMetricDefinition(orgId, clusterId, d.id, d);
                else await createMetricDefinition(orgId, clusterId, d);
              } catch (e) {
                setErr(e instanceof Error ? e.message : 'save failed');
                setBusy(false);
                return;
              }
              setBusy(false);
              onSaved();
            }}
            className="rp-focus h-8 rounded-skin-sm px-3.5 font-mono text-[11.5px] font-semibold transition-opacity hover:opacity-90"
            style={{ background: 'var(--rp-btn-bg)', color: 'var(--rp-btn-fg)', opacity: busy || !isPromql ? 0.55 : 1 }}
          >
            Save
          </button>
          <button type="button" onClick={onClose} className="h-8 rounded-skin-sm border border-line px-3 font-mono text-[11.5px] text-mid transition-colors hover:bg-hover hover:text-ink">
            Cancel
          </button>
          {d.id ? (
            <button
              type="button"
              onClick={async () => {
                await deleteMetricDefinition(orgId, clusterId, d.id!).catch(() => {});
                onSaved();
              }}
              className="ml-auto rounded-skin-sm border border-line px-2.5 py-1.5 font-mono text-[11px] transition-colors hover:bg-hover"
              style={{ color: 'var(--rp-tone-red-fg)' }}
            >
              delete
            </button>
          ) : null}
        </div>
      </div>
    </div>
  );
}
