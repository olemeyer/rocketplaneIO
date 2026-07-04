import { brand, signal as signalColors, status as statusColors } from '@rocketplane/ui';
import { rpFetch } from './client';
import type {
  HealthState,
  HealthStatus,
  Service,
  ServiceHealth,
  ServicesResponse,
  Span,
  TraceDetail,
  TraceListResponse,
  TraceSpan,
} from './types';

const HEALTH_TO_STATE: Record<HealthStatus, HealthState> = {
  healthy: 'resolved',
  degraded: 'degraded',
  critical: 'critical',
};

// Stabile Service-Farbe aus den Design-Tokens (Signal-Identität).
const SERVICE_COLORS: Record<string, string> = {
  'checkout-api': signalColors.traces,
  'payment-gateway': signalColors.rum,
  'cart-service': signalColors.logs,
  inventory: '#34d399',
  auth: brand.from,
  stripe: signalColors.synthetics,
  notifier: statusColors.resolved,
  cache: signalColors.metrics,
};
const FALLBACK = [
  signalColors.traces,
  signalColors.metrics,
  signalColors.logs,
  signalColors.rum,
  signalColors.synthetics,
];

export function colorForService(name: string): string {
  const known = SERVICE_COLORS[name];
  if (known) return known;
  let h = 0;
  for (let i = 0; i < name.length; i++) h = (h * 31 + name.charCodeAt(i)) >>> 0;
  return FALLBACK[h % FALLBACK.length] ?? signalColors.traces;
}

// ---- Adapter: Domain -> UI ----

export async function getServiceHealth(sig?: AbortSignal): Promise<ServiceHealth[]> {
  const data = await rpFetch<ServicesResponse>('/v1/services', { signal: sig });
  return data.services.map(toServiceHealth);
}

function toServiceHealth(s: Service): ServiceHealth {
  return {
    name: s.name,
    state: HEALTH_TO_STATE[s.status] ?? 'unknown',
    p95Ms: s.latencyMs.p95,
    errorRate: s.errorRatio,
    throughput: s.rate,
    spark: s.sparkline.values,
  };
}

export interface TraceView {
  detail: TraceDetail;
  spans: TraceSpan[];
}

export async function getTrace(id: string, sig?: AbortSignal): Promise<TraceView> {
  const detail = await rpFetch<TraceDetail>(`/v1/traces/${id}`, { signal: sig });
  return { detail, spans: detail.spans.map((sp) => toTraceSpan(sp, detail.durationMs)) };
}

// getLatestTrace holt den neuesten Trace eines Service und dessen Spans.
export async function getLatestTrace(service: string, sig?: AbortSignal): Promise<TraceView | null> {
  const list = await rpFetch<TraceListResponse>(
    `/v1/traces?service=${encodeURIComponent(service)}&limit=1`,
    { signal: sig },
  );
  const first = list.traces[0];
  if (!first) return null;
  return getTrace(first.traceId, sig);
}

// Filter für die Trace-Liste (deckt die Backend-Query-Params ab).
export interface TraceFilter {
  service?: string;
  status?: 'ok' | 'error';
  minDurationMs?: number;
  sort?: 'start' | 'duration';
  limit?: number;
  cursor?: string;
}

// getTraces liefert eine gefilterte, paginierte Trace-Liste.
export async function getTraces(f: TraceFilter, sig?: AbortSignal): Promise<TraceListResponse> {
  const p = new URLSearchParams();
  if (f.service) p.set('service', f.service);
  if (f.status) p.set('status', f.status);
  if (f.minDurationMs) p.set('minDurationMs', String(f.minDurationMs));
  if (f.sort) p.set('sort', f.sort);
  p.set('limit', String(f.limit ?? 25));
  if (f.cursor) p.set('cursor', f.cursor);
  return rpFetch<TraceListResponse>(`/v1/traces?${p.toString()}`, { signal: sig });
}

function toTraceSpan(sp: Span, totalMs: number): TraceSpan {
  const total = totalMs || 1;
  return {
    name: sp.name,
    service: sp.service,
    depth: sp.depth,
    offsetPct: (sp.startOffsetMs / total) * 100,
    widthPct: Math.max((sp.durationMs / total) * 100, 0.6),
    durationMs: sp.durationMs,
    color: colorForService(sp.service),
    isError: sp.status === 'error',
  };
}
