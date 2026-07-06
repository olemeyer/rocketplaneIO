// Schlanker fetch-Wrapper für die Control-Plane. Immer same-origin (Next-Rewrites
// proxien /api/* + /auth/* weiter), credentials:'include' damit rp_session mitgeht.
// Fehler-Shape des Backends: { "error": "..." } mit passendem HTTP-Status.

export class ApiError extends Error {
  readonly status: number;
  readonly body: unknown;
  constructor(message: string, status: number, body?: unknown) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.body = body;
  }
}

function parseBody(text: string): unknown {
  if (!text) return null;
  try {
    return JSON.parse(text);
  } catch {
    return text;
  }
}

export async function apiFetch<T>(path: string, init: RequestInit = {}): Promise<T> {
  const hasBody = init.body != null;
  const res = await fetch(path, {
    credentials: 'include',
    cache: 'no-store',
    ...init,
    headers: {
      Accept: 'application/json',
      ...(hasBody ? { 'Content-Type': 'application/json' } : {}),
      ...(init.headers as Record<string, string> | undefined),
    },
  });

  const data = parseBody(await res.text());

  if (!res.ok) {
    // Session ungültig/abgelaufen mitten in der Sitzung → sauber zurück zum Login.
    // Die Control-Plane hat das Stale-Cookie mit diesem 401 bereits gelöscht, daher
    // bounct die Middleware /login nicht mehr auf / (kein Flackern/Redirect-Loop).
    // `replace` statt `href`, damit die kaputte Route nicht in der History landet.
    if (
      res.status === 401 &&
      typeof window !== 'undefined' &&
      !window.location.pathname.startsWith('/login') &&
      !window.location.pathname.startsWith('/setup')
    ) {
      window.location.replace('/login');
    }
    const message =
      data && typeof data === 'object' && 'error' in (data as Record<string, unknown>)
        ? String((data as Record<string, unknown>).error)
        : `Request failed with status ${res.status}`;
    throw new ApiError(message, res.status, data);
  }

  return data as T;
}
