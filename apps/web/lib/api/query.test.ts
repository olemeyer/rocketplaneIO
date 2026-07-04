import { afterEach, describe, expect, it, vi } from 'vitest';
import { colorForService, getServiceHealth, getTrace } from './query';
import { servicesResponse, traceDetail } from '@/src/test/fixtures/explore';

function stubFetch(data: unknown) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => ({ ok: true, status: 200, json: async () => ({ status: 'success', data }) }) as unknown as Response),
  );
}

afterEach(() => vi.unstubAllGlobals());

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

describe('colorForService', () => {
  it('liefert stabile Token-Farben für bekannte Services', () => {
    expect(colorForService('checkout-api')).toMatch(/^#[0-9a-f]{6}$/i);
    expect(colorForService('unknown-svc')).toMatch(/^#[0-9a-f]{6}$/i);
    expect(colorForService('checkout-api')).toBe(colorForService('checkout-api'));
  });
});
