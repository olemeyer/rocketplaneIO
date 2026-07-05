// Single source of truth der Explore-API-Typen (spiegelt den query-Service 1:1).
import type { Status } from '@rocketplane/ui';

// ---- Envelope (Prometheus-kompatibel) ----
export type ApiResponse<T> =
  | { status: 'success'; data: T }
  | { status: 'error'; errorType: string; error: string };

export type HealthStatus = 'healthy' | 'degraded' | 'critical';
export type TraceStatus = 'ok' | 'error';
export type SpanKind = 'server' | 'client' | 'internal' | 'producer' | 'consumer';

// ---- GET /api/v1/services ----
export interface Service {
  name: string;
  status: HealthStatus;
  rate: number; // req/s über das Fenster
  errorRatio: number; // 0..1
  latencyMs: { p50: number; p95: number; p99: number };
  spanCount: number;
  errorCount: number;
  sparkline: { metric: 'rate' | 'errorRatio' | 'p95'; values: number[] };
}
export interface ServicesResponse {
  window: { start: number; end: number; step: number }; // unix-seconds
  services: Service[];
}

// ---- GET /api/v1/traces ----
export interface TraceSummary {
  traceId: string;
  rootName: string;
  rootService: string;
  startTimeUnixMs: number;
  durationMs: number;
  spanCount: number;
  errorCount: number;
  status: TraceStatus;
}
export interface TraceListResponse {
  traces: TraceSummary[];
  nextCursor?: string;
}

// ---- GET /api/v1/traces/{traceId} ----
export interface Span {
  spanId: string;
  parentSpanId: string; // "" => Root
  name: string;
  service: string;
  kind: SpanKind;
  startOffsetMs: number;
  durationMs: number;
  depth: number;
  status: TraceStatus;
  statusMessage?: string;
}
export interface TraceDetail {
  traceId: string;
  startTimeUnixMs: number;
  durationMs: number;
  spanCount: number;
  errorCount: number;
  services: string[];
  spans: Span[];
}

// ---- GET /api/v1/services/{name} ----
export interface Point {
  t: number; // unix seconds
  v: number;
}
export interface OperationStat {
  name: string;
  spanCount: number;
  errorCount: number;
  errorRatio: number;
  p95Ms: number;
}
export interface Dependency {
  service: string;
  callCount: number;
  errorRatio: number;
  p95Ms: number;
}
export interface ServiceDetail {
  name: string;
  status: HealthStatus;
  rate: number;
  errorRatio: number;
  latencyMs: { p50: number; p95: number; p99: number };
  spanCount: number;
  errorCount: number;
  window: { start: number; end: number; step: number };
  p95Series: Point[];
  rateSeries: Point[];
  errorSeries: Point[];
  operations: OperationStat[];
  dependencies: Dependency[];
}

// ---- GET /api/v1/logs ----
export interface LogRecord {
  timestamp: number; // epoch ms
  serviceName: string;
  severity: string; // INFO | WARN | ERROR | DEBUG | ...
  severityNumber: number; // OTLP 1..24
  body: string;
  traceId?: string;
  spanId?: string;
  attributes?: Record<string, string>;
}
export interface LogListResponse {
  logs: LogRecord[];
  nextCursor?: string;
}

// ---- UI-Domäne (Adapter-Output, an @rocketplane/ui-Tokens gebunden) ----
export type HealthState = Status; // 'resolved'|'degraded'|'critical'|'unknown'

export interface ServiceHealth {
  name: string;
  state: HealthState;
  p95Ms: number;
  errorRate: number; // 0..1
  throughput: number; // req/s
  spark: number[];
}
export interface TraceSpan {
  name: string;
  service: string;
  depth: number;
  offsetPct: number; // startOffsetMs / trace.durationMs * 100
  widthPct: number; // durationMs / trace.durationMs * 100
  durationMs: number;
  color: string;
  isError: boolean;
}

// ---- Fehler ----
export type RpErrorCode =
  | 'bad_data'
  | 'not_found'
  | 'timeout'
  | 'internal'
  | 'not_implemented'
  | 'network';

export interface RpApiError {
  status: number; // HTTP-Status (0 = kein Response)
  code: RpErrorCode;
  message: string;
}
