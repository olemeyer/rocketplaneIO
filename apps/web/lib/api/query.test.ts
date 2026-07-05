import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  colorForService,
  getLogs,
  getServiceDetail,
  getServiceHealth,
  getTrace,
  getTraces,
  severityTone,
} from './query';
import { logList, serviceDetail, servicesResponse, traceDetail, traceList } from '@/src/test/fixtures/explore';

function stubFetch(data: unknown) {
  const fetchMock = vi.fn(
    async (_url: string) =>
      ({ ok: true, status: 200, json: async () => ({ status: 'success', data }) }) as unknown as Response,
  );
  vi.stubGlobal('fetch', fetchMock);
  return fetchMock;
}

afterEach(() => vi.unstubAllGlobals());

describe('getTraces', () => {
  it('baut Query-Params aus dem Filter und liefert die Liste', async () => {
    const fetchMock = stubFetch(traceList);
    const res = await getTraces({ service: 'checkout-api', status: 'error', minDurationMs: 100, limit: 25 });

    expect(res.traces).toHaveLength(1);
    const url = String(fetchMock.mock.calls[0]?.[0]);
    expect(url).toContain('/api/rp/v1/traces?');
    expect(url).toContain('service=checkout-api');
    expect(url).toContain('status=error');
    expect(url).toContain('minDurationMs=100');
    expect(url).toContain('limit=25');
  });

  it('lässt leere Filter weg', async () => {
    const fetchMock = stubFetch(traceList);
    await getTraces({});
    const url = String(fetchMock.mock.calls[0]?.[0]);
    expect(url).not.toContain('service=');
    expect(url).not.toContain('status=');
    expect(url).toContain('limit=25');
  });
});

describe('getServiceHealth', () => {
  it('mappt Service -> ServiceHealth (Status -> State, spark, p95)', async () => {
    stubFetch(servicesResponse);
    const health = await getServiceHealth();

    expect(health).toHaveLength(2);
    expect(health[0]).toMatchObject({ name: 'checkout-api', state: 'critical', p95Ms: 842, throughput: 1204 });
    expect(health[1]?.state).toBe('resolved'); // healthy -> resolved
    expect(health[0]?.spark).toEqual([5, 7, 6, 9, 8, 12, 16, 14]);
  });
});

describe('getTrace', () => {
  it('mappt Span -> TraceSpan mit offset/width in Prozent', async () => {
    stubFetch(traceDetail);
    const { spans } = await getTrace(traceDetail.traceId);

    const payment = spans.find((s) => s.name === 'payment.charge');
    expect(payment).toBeDefined();
    expect(payment?.offsetPct).toBeCloseTo((103 / 342) * 100, 1);
    expect(payment?.widthPct).toBeCloseTo((158 / 342) * 100, 1);
    expect(payment?.isError).toBe(true);
    expect(spans[0]?.widthPct).toBe(100); // Root deckt das ganze Fenster
  });
});

describe('getLogs', () => {
  it('baut Query-Params aus dem Filter', async () => {
    const fetchMock = stubFetch(logList);
    const res = await getLogs({ service: 'checkout-api', minSeverity: 17, search: 'boom', traceId: 'abc' });
    expect(res.logs).toHaveLength(2);
    const url = String(fetchMock.mock.calls[0]?.[0]);
    expect(url).toContain('/api/rp/v1/logs?');
    expect(url).toContain('service=checkout-api');
    expect(url).toContain('minSeverity=17');
    expect(url).toContain('search=boom');
    expect(url).toContain('traceId=abc');
  });
});

describe('getServiceDetail', () => {
  it('ruft /v1/services/{name} und liefert das Detail', async () => {
    const fetchMock = stubFetch(serviceDetail);
    const res = await getServiceDetail('checkout-api');
    expect(res.name).toBe('checkout-api');
    expect(res.operations).toHaveLength(2);
    expect(String(fetchMock.mock.calls[0]?.[0])).toBe('/api/rp/v1/services/checkout-api');
  });
});

describe('severityTone', () => {
  it('mappt Severity auf Ton-Klassen', () => {
    expect(severityTone('ERROR').className).toContain('critical');
    expect(severityTone('WARN').className).toContain('degraded');
    expect(severityTone('INFO').label).toBe('INFO');
    expect(severityTone('').label).toBe('INFO');
  });
});

describe('colorForService', () => {
  it('liefert stabile Token-Farben für bekannte Services', () => {
    expect(colorForService('checkout-api')).toMatch(/^#[0-9a-f]{6}$/i);
    expect(colorForService('unknown-svc')).toMatch(/^#[0-9a-f]{6}$/i);
    expect(colorForService('checkout-api')).toBe(colorForService('checkout-api'));
  });
});
