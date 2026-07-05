'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import { getLogs, getServiceHealth, type LogFilter } from '@/lib/api/query';
import { isRpApiError } from '@/lib/api/client';
import type { LogRecord } from '@/lib/api';
import { ChevronDown, Search } from '../icons';
import { LogRows } from './log-rows';

const SEVERITIES = [
  { label: 'all levels', value: 0 },
  { label: 'debug+', value: 5 },
  { label: 'info+', value: 9 },
  { label: 'warn+', value: 13 },
  { label: 'error+', value: 17 },
];

export function LogList({ initialService = '' }: { initialService?: string }) {
  const [services, setServices] = useState<string[]>([]);
  const [service, setService] = useState(initialService);
  const [minSeverity, setMinSeverity] = useState(0);
  const [search, setSearch] = useState('');
  const [debounced, setDebounced] = useState('');

  const [logs, setLogs] = useState<LogRecord[]>([]);
  const [cursor, setCursor] = useState<string | undefined>();
  const [state, setState] = useState<'loading' | 'ready' | 'empty' | 'error'>('loading');
  const [error, setError] = useState<string | null>(null);
  const [loadingMore, setLoadingMore] = useState(false);

  useEffect(() => {
    const ctrl = new AbortController();
    getServiceHealth(ctrl.signal)
      .then((s) => setServices(s.map((x) => x.name)))
      .catch(() => {});
    return () => ctrl.abort();
  }, []);

  // Suche debouncen.
  useEffect(() => {
    const id = window.setTimeout(() => setDebounced(search.trim()), 300);
    return () => window.clearTimeout(id);
  }, [search]);

  const filter = useCallback(
    (cur?: string): LogFilter => ({
      service: service || undefined,
      minSeverity: minSeverity || undefined,
      search: debounced || undefined,
      limit: 60,
      cursor: cur,
    }),
    [service, minSeverity, debounced],
  );

  const timer = useRef<number | null>(null);
  useEffect(() => {
    const ctrl = new AbortController();
    setState('loading');
    getLogs(filter(), ctrl.signal)
      .then((res) => {
        setLogs(res.logs);
        setCursor(res.nextCursor);
        setState(res.logs.length === 0 ? 'empty' : 'ready');
      })
      .catch((e: unknown) => {
        if (ctrl.signal.aborted) return;
        setError(isRpApiError(e) ? e.message : String(e));
        setState('error');
      });

    // sanftes Live-Polling der ersten Seite alle 5s (nur bei Default-Cursor)
    timer.current = window.setInterval(() => {
      if (document.hidden) return;
      getLogs(filter())
        .then((res) => {
          setLogs(res.logs);
          setCursor(res.nextCursor);
          setState(res.logs.length === 0 ? 'empty' : 'ready');
        })
        .catch(() => {});
    }, 5000);

    return () => {
      ctrl.abort();
      if (timer.current) window.clearInterval(timer.current);
    };
  }, [filter]);

  async function loadMore() {
    if (!cursor) return;
    setLoadingMore(true);
    try {
      const res = await getLogs(filter(cursor));
      setLogs((prev) => [...prev, ...res.logs]);
      setCursor(res.nextCursor);
    } catch {
      /* Retry per erneutem Klick */
    } finally {
      setLoadingMore(false);
    }
  }

  return (
    <div className="space-y-3">
      {/* Filterleiste */}
      <div className="flex flex-wrap items-center gap-2 rounded-lg border border-line bg-base px-3 py-2">
        <label className="flex flex-1 items-center gap-2">
          <Search className="h-3.5 w-3.5 text-faint" />
          <input
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="search log body…"
            className="w-full bg-transparent font-mono text-[12px] text-strong outline-none placeholder:text-faint"
          />
        </label>

        <Select value={service} onChange={setService} label="service">
          <option value="">all services</option>
          {services.map((s) => (
            <option key={s} value={s}>
              {s}
            </option>
          ))}
        </Select>

        <Select value={String(minSeverity)} onChange={(v) => setMinSeverity(Number(v))} label="level">
          {SEVERITIES.map((s) => (
            <option key={s.value} value={s.value}>
              {s.label}
            </option>
          ))}
        </Select>

        <span className="font-mono text-[11px] text-faint">
          {state === 'ready' ? `${logs.length}${cursor ? '+' : ''}` : ''}
          <span className="ml-2 inline-flex items-center gap-1">
            <span className="h-1 w-1 animate-pulse rounded-full bg-accent" /> live
          </span>
        </span>
      </div>

      {/* Ergebnis */}
      {state === 'loading' && (
        <div className="space-y-[3px]" role="status" aria-label="loading logs">
          {Array.from({ length: 10 }).map((_, i) => (
            <div key={i} className="h-6 overflow-hidden rounded bg-base">
              <div className="shimmer h-full w-full" />
            </div>
          ))}
        </div>
      )}
      {state === 'error' && (
        <div className="rounded-lg border border-line bg-base p-4 text-[12px] text-muted">
          <span className="text-status-critical">Fehler beim Laden</span> — {error}
        </div>
      )}
      {state === 'empty' && (
        <div className="rounded-lg border border-line bg-base p-6 text-center text-[13px] text-muted">
          Keine Logs für diese Filter im Zeitfenster.
        </div>
      )}
      {state === 'ready' && (
        <div className="overflow-hidden rounded-lg border border-line bg-base">
          <LogRows logs={logs} />
          {cursor && (
            <button
              onClick={loadMore}
              disabled={loadingMore}
              className="w-full border-t border-line py-2.5 text-[12px] text-muted transition-colors hover:bg-overlay disabled:opacity-60"
            >
              {loadingMore ? 'Lädt…' : 'Mehr laden'}
            </button>
          )}
        </div>
      )}
    </div>
  );
}

function Select({
  value,
  onChange,
  label,
  children,
}: {
  value: string;
  onChange: (v: string) => void;
  label: string;
  children: React.ReactNode;
}) {
  return (
    <label className="relative flex items-center">
      <span className="pointer-events-none absolute left-2.5 font-mono text-[10px] uppercase tracking-wider text-faint">
        {label}
      </span>
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="appearance-none rounded-md border border-line bg-overlay py-1.5 pl-[52px] pr-7 font-mono text-[11px] text-strong outline-none transition-colors hover:border-faint focus:border-accent"
      >
        {children}
      </select>
      <ChevronDown className="pointer-events-none absolute right-2 h-3 w-3 text-faint" />
    </label>
  );
}
