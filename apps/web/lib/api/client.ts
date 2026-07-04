import type { ApiResponse, RpApiError, RpErrorCode } from './types';

const BASE = '/api/rp';
const TIMEOUT_MS = 8000;

export function isRpApiError(e: unknown): e is RpApiError {
  return typeof e === 'object' && e !== null && 'code' in e && 'status' in e;
}

function rpError(status: number, code: RpErrorCode, message: string): RpApiError {
  return { status, code, message };
}

function codeForStatus(status: number): RpErrorCode {
  switch (status) {
    case 400:
      return 'bad_data';
    case 404:
      return 'not_found';
    case 501:
      return 'not_implemented';
    default:
      return 'internal';
  }
}

/**
 * rpFetch ruft den query-Service same-origin über den Next-Rewrite-Proxy auf
 * (/api/rp{path} -> :7080). Non-2xx bzw. {status:'error'} -> typisierter RpApiError.
 * 8s-Timeout via AbortController; ein extern übergebenes signal wird gemergt und
 * bricht die Anfrage ebenfalls ab (Cancellation propagiert als AbortError).
 */
export async function rpFetch<T>(path: string, init?: RequestInit): Promise<T> {
  const controller = new AbortController();
  let timedOut = false;
  const timer = setTimeout(() => {
    timedOut = true;
    controller.abort();
  }, TIMEOUT_MS);

  const external = init?.signal;
  if (external) {
    if (external.aborted) controller.abort();
    else external.addEventListener('abort', () => controller.abort(), { once: true });
  }

  let res: Response;
  try {
    res = await fetch(`${BASE}${path}`, {
      ...init,
      signal: controller.signal,
      headers: { Accept: 'application/json', ...init?.headers },
    });
  } catch (err) {
    if (timedOut) throw rpError(0, 'timeout', 'request timed out');
    if (external?.aborted) throw err; // externe Cancellation -> weiterreichen
    throw rpError(0, 'network', err instanceof Error ? err.message : 'network error');
  } finally {
    clearTimeout(timer);
  }

  let body: ApiResponse<T> | undefined;
  try {
    body = (await res.json()) as ApiResponse<T>;
  } catch {
    body = undefined;
  }

  if (!res.ok || body?.status === 'error') {
    const message = body?.status === 'error' ? body.error : res.statusText || 'request failed';
    throw rpError(res.status, codeForStatus(res.status), message);
  }
  if (!body || body.status !== 'success') {
    throw rpError(res.status, 'internal', 'malformed response');
  }
  return body.data;
}
