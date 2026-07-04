import { afterEach, describe, expect, it, vi } from 'vitest';
import { rpFetch } from './client';

function fakeResponse(status: number, body: unknown): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    statusText: 'test',
    json: async () => body,
  } as unknown as Response;
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('rpFetch', () => {
  it('ruft same-origin /api/rp{path} und liefert data zurück', async () => {
    const fetchMock = vi.fn(async (_url: string) =>
      fakeResponse(200, { status: 'success', data: { hello: 1 } }),
    );
    vi.stubGlobal('fetch', fetchMock);

    const data = await rpFetch<{ hello: number }>('/v1/services');

    expect(data).toEqual({ hello: 1 });
    expect(fetchMock.mock.calls[0]?.[0]).toBe('/api/rp/v1/services');
  });

  it('mappt HTTP 404 auf RpApiError code not_found', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => fakeResponse(404, { status: 'error', errorType: 'not_found', error: 'nope' })),
    );
    await expect(rpFetch('/v1/traces/x')).rejects.toMatchObject({ code: 'not_found', status: 404 });
  });

  it('mappt HTTP 501 auf not_implemented', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => fakeResponse(501, { status: 'error', errorType: 'not_implemented', error: 'later' })),
    );
    await expect(rpFetch('/v1/query')).rejects.toMatchObject({ code: 'not_implemented' });
  });

  it('mappt Netzwerkfehler auf code network', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => {
        throw new TypeError('failed to fetch');
      }),
    );
    await expect(rpFetch('/v1/services')).rejects.toMatchObject({ code: 'network' });
  });
});
