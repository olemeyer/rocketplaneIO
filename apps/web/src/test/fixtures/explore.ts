import type {
  ServiceHealth,
  ServicesResponse,
  TraceDetail,
  TraceListResponse,
  TraceSpan,
} from '@/lib/api';

// Domain-Fixtures (API-Shape) für Client-/Adapter-Tests.
export const servicesResponse: ServicesResponse = {
  window: { start: 1, end: 901, step: 112 },
  services: [
    {
      name: 'checkout-api',
      status: 'critical',
      rate: 1204,
      errorRatio: 0.042,
      latencyMs: { p50: 210, p95: 842, p99: 1310 },
      spanCount: 100000,
      errorCount: 4200,
      sparkline: { metric: 'rate', values: [5, 7, 6, 9, 8, 12, 16, 14] },
    },
    {
      name: 'cart-service',
      status: 'healthy',
      rate: 2103,
      errorRatio: 0.001,
      latencyMs: { p50: 38, p95: 96, p99: 180 },
      spanCount: 200000,
      errorCount: 200,
      sparkline: { metric: 'rate', values: [6, 6, 5, 7, 6, 6, 7, 6] },
    },
  ],
};

export const traceDetail: TraceDetail = {
  traceId: '7f3a9c2b1e8d4a5f9c0b3d2e1a4f6c7d',
  startTimeUnixMs: 1_700_000_000_000,
  durationMs: 342,
  spanCount: 3,
  errorCount: 1,
  services: ['cart-service', 'checkout-api', 'payment-gateway'],
  spans: [
    { spanId: 'a', parentSpanId: '', name: 'POST /checkout', service: 'checkout-api', kind: 'server', startOffsetMs: 0, durationMs: 342, depth: 0, status: 'error', statusMessage: 'payment declined' },
    { spanId: 'b', parentSpanId: 'a', name: 'cart.load', service: 'cart-service', kind: 'internal', startOffsetMs: 41, durationMs: 55, depth: 1, status: 'ok' },
    { spanId: 'c', parentSpanId: 'a', name: 'payment.charge', service: 'payment-gateway', kind: 'client', startOffsetMs: 103, durationMs: 158, depth: 1, status: 'error' },
  ],
};

export const traceList: TraceListResponse = {
  traces: [
    {
      traceId: traceDetail.traceId,
      rootName: 'POST /checkout',
      rootService: 'checkout-api',
      startTimeUnixMs: traceDetail.startTimeUnixMs,
      durationMs: 342,
      spanCount: 3,
      errorCount: 1,
      status: 'error',
    },
  ],
};

// UI-Fixtures (Adapter-Output) für Component-Tests.
export const serviceHealthSample: ServiceHealth[] = [
  { name: 'checkout-api', state: 'critical', p95Ms: 842, errorRate: 0.042, throughput: 1204, spark: [5, 7, 6, 9, 8, 12, 16, 14] },
  { name: 'cart-service', state: 'resolved', p95Ms: 96, errorRate: 0.001, throughput: 2103, spark: [6, 6, 5, 7, 6, 6, 7, 6] },
];

export const traceSpansSample: TraceSpan[] = [
  { name: 'POST /checkout', service: 'checkout-api', depth: 0, offsetPct: 0, widthPct: 100, durationMs: 342, color: '#818cf8', isError: true },
  { name: 'cart.load', service: 'cart-service', depth: 1, offsetPct: 12, widthPct: 16, durationMs: 55, color: '#38bdf8', isError: false },
  { name: 'payment.charge', service: 'payment-gateway', depth: 1, offsetPct: 30.1, widthPct: 46.2, durationMs: 158, color: '#fb7185', isError: true },
];

export const traceViewSample = { detail: traceDetail, spans: traceSpansSample };
