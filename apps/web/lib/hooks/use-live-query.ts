'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import { isRpApiError } from '@/lib/api/client';
import type { RpApiError } from '@/lib/api/types';

export type QueryStatus = 'loading' | 'refreshing' | 'success' | 'empty' | 'error';

export interface LiveQuery<T> {
  data: T | null;
  status: QueryStatus;
  error: RpApiError | null;
  refresh: () => void;
}

export interface LiveQueryOptions<T> {
  intervalMs?: number;
  isEmpty?: (data: T) => boolean;
}

function defaultIsEmpty<T>(data: T): boolean {
  return data == null || (Array.isArray(data) && data.length === 0);
}

/**
 * useLiveQuery lädt fn() und pollt optional. Bei Re-Fetch bleibt data sichtbar
 * (status 'refreshing') — kein Flackern. Polling pausiert bei document.hidden.
 * In-flight-Requests werden bei Unmount/Refresh via AbortController abgebrochen
 * (idempotent trotz React-StrictMode-Doppelmount).
 */
export function useLiveQuery<T>(
  fn: (signal: AbortSignal) => Promise<T>,
  opts: LiveQueryOptions<T> = {},
): LiveQuery<T> {
  const { intervalMs } = opts;
  const [data, setData] = useState<T | null>(null);
  const [status, setStatus] = useState<QueryStatus>('loading');
  const [error, setError] = useState<RpApiError | null>(null);
  const [tick, setTick] = useState(0);

  const fnRef = useRef(fn);
  fnRef.current = fn;
  const isEmptyRef = useRef(opts.isEmpty);
  isEmptyRef.current = opts.isEmpty;
  const hasData = useRef(false);

  const refresh = useCallback(() => setTick((t) => t + 1), []);

  useEffect(() => {
    const controller = new AbortController();
    let cancelled = false;
    setStatus(hasData.current ? 'refreshing' : 'loading');

    fnRef.current(controller.signal).then(
      (result) => {
        if (cancelled) return;
        hasData.current = true;
        setData(result);
        setError(null);
        const empty = (isEmptyRef.current ?? defaultIsEmpty)(result);
        setStatus(empty ? 'empty' : 'success');
      },
      (err: unknown) => {
        if (cancelled || controller.signal.aborted) return;
        setError(isRpApiError(err) ? err : { status: 0, code: 'internal', message: String(err) });
        setStatus('error');
      },
    );

    return () => {
      cancelled = true;
      controller.abort();
    };
  }, [tick]);

  useEffect(() => {
    if (!intervalMs) return;
    const id = window.setInterval(() => {
      if (!document.hidden) refresh();
    }, intervalMs);
    return () => window.clearInterval(id);
  }, [intervalMs, refresh]);

  return { data, status, error, refresh };
}
