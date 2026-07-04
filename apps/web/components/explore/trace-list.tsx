'use client';

import { useCallback, useEffect, useState } from 'react';
import Link from 'next/link';
import { getServiceHealth, getTraces, type TraceFilter } from '@/lib/api/query';
import { colorForService, type TraceSummary } from '@/lib/api';
import { isRpApiError } from '@/lib/api/client';
import { ChevronDown, Waterfall } from '../icons';

type Status = 'all' | 'ok' | 'error';
type Sort = 'start' | 'duration';

const MIN_DUR_OPTIONS = [
  { label: 'any latency', value: 0 },
  { label: '≥ 100ms', value: 100 },
  { label: '≥ 500ms', value: 500 },
  { label: '≥ 1s', value: 1000 },
];

export function TraceList({ initialService = '' }: { initialService?: string }) {
  const [services, setServices] = useState<string[]>([]);
  const [service, setService] = useState(initialService);
  const [status, setStatus] = useState<Status>('all');
  const [sort, setSort] = useState<Sort>('start');
  const [minDur, setMinDur] = useState(0);

  const [traces, setTraces] = useState<TraceSummary[]>([]);
  const [cursor, setCursor] = useState<string | undefined>();
  const [state, setState] = useState<'loading' | 'ready' | 'empty' | 'error'>('loading');
  const [error, setError] = useState<string | null>(null);
  const [loadingMore, setLoadingMore] = useState(false);

  // Service-Liste für das Filter-Dropdown.
  useEffect(() => {
    const ctrl = new AbortController();
    getServiceHealth(ctrl.signal)
      .then((s) => setServices(s.map((x) => x.name)))
      .catch(() => {});
    return () => ctrl.abort();
  }, []);

  const filter = useCallback(
    (cur?: string): TraceFilter => ({
      service: service || undefined,
      status: status === 'all' ? undefined : status,
      minDurationMs: minDur || undefined,
      sort,
      limit: 25,
      cursor: cur,
    }),
    [service, status, minDur, sort],
  );

  // Erste Seite bei Filter-Änderung neu laden.
  useEffect(() => {
    const ctrl = new AbortController();
    setState('loading');
    getTraces(filter(), ctrl.signal)
      .then((res) => {
        setTraces(res.traces);
        setCursor(res.nextCursor);
        setState(res.traces.length === 0 ? 'empty' : 'ready');
      })
      .catch((e: unknown) => {
        if (ctrl.signal.aborted) return;
        setError(isRpApiError(e) ? e.message : String(e));
        setState('error');
      });
    return () => ctrl.abort();
  }, [filter]);

  async function loadMore() {
    if (!cursor) return;
    setLoadingMore(true);
    try {
      const res = await getTraces(filter(cursor));
      setTraces((prev) => [...prev, ...res.traces]);
      setCursor(res.nextCursor);
    } catch {
      /* ignoriert — Retry über erneuten Klick */
    } finally {
      setLoadingMore(false);
    }
  }

  return (
    <div className="space-y-3">
      {/* Filterleiste */}
      <div className="flex flex-wrap items-center gap-2 rounded-lg border border-line bg-base px-3 py-2">
        <Select value={service} onChange={setService} label="service">
          <option value="">all services</option>
          {services.map((s) => (
            <option key={s} value={s}>
              {s}
            </option>
          ))}
        </Select>

        <Segmented
          value={status}
          onChange={(v) => setStatus(v as Status)}
          options={[
            { label: 'all', value: 'all' },
            { label: 'ok', value: 'ok' },
            { label: 'errors', value: 'error' },
          ]}
        />

        <Select value={String(minDur)} onChange={(v) => setMinDur(Number(v))} label="latency">
          {MIN_DUR_OPTIONS.map((o) => (
            <option key={o.value} value={o.value}>
              {o.label}
            </option>
          ))}
        </Select>

        <Select value={sort} onChange={(v) => setSort(v as Sort)} label="sort">
          <option value="start">most recent</option>
          <option value="duration">slowest</option>
        </Select>

        <span className="ml-auto font-mono text-[11px] text-faint">
          {state === 'ready' ? `${traces.length}${cursor ? '+' : ''} traces` : ''}
        </span>
      </div>

      {/* Ergebnis */}
      {state === 'loading' && (
        <div className="space-y-[3px]" role="status" aria-label="loading traces">
          {Array.from({ length: 8 }).map((_, i) => (
            <div key={i} className="h-9 overflow-hidden rounded-md bg-base">
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
          Keine Traces für diese Filter im Zeitfenster.
        </div>
      )}

      {state === 'ready' && (
        <div className="overflow-hidden rounded-lg border border-line">
          <div className="grid grid-cols-[1fr_140px_90px_70px_60px] gap-2 border-b border-line bg-overlay px-3 py-2 font-mono text-[10px] uppercase tracking-wider text-faint">
            <span>trace</span>
            <span>service</span>
            <span className="text-right">duration</span>
            <span className="text-right">spans</span>
            <span className="text-right">status</span>
          </div>
          {traces.map((t) => (
            <Link
              key={t.traceId}
              href={`/traces/${t.traceId}`}
              className="grid grid-cols-[1fr_140px_90px_70px_60px] items-center gap-2 border-b border-line bg-base px-3 py-2 text-[12px] transition-colors last:border-0 hover:bg-overlay"
            >
              <span className="flex items-center gap-2 truncate">
                <Waterfall className="h-3.5 w-3.5 shrink-0 text-faint" />
                <span className="truncate text-strong">{t.rootName}</span>
                <span className="shrink-0 font-mono text-[10px] text-faint">{t.traceId.slice(0, 8)}</span>
              </span>
              <span className="flex items-center gap-1.5 truncate">
                <span
                  className="h-2 w-2 shrink-0 rounded-full"
                  style={{ background: colorForService(t.rootService) }}
                />
                <span className="truncate text-muted">{t.rootService}</span>
              </span>
              <span className="text-right font-mono text-strong">{Math.round(t.durationMs)}ms</span>
              <span className="text-right font-mono text-faint">{t.spanCount}</span>
              <span className="text-right">
                <span
                  className={`rounded px-1.5 py-0.5 font-mono text-[10px] ${
                    t.status === 'error'
                      ? 'bg-[rgba(242,85,90,0.14)] text-status-critical'
                      : 'bg-overlay text-faint'
                  }`}
                >
                  {t.status}
                </span>
              </span>
            </Link>
          ))}

          {cursor && (
            <button
              onClick={loadMore}
              disabled={loadingMore}
              className="w-full border-t border-line bg-base py-2.5 text-[12px] text-muted transition-colors hover:bg-overlay disabled:opacity-60"
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
        className="appearance-none rounded-md border border-line bg-overlay py-1.5 pl-[62px] pr-7 font-mono text-[11px] text-strong outline-none transition-colors hover:border-faint focus:border-accent"
      >
        {children}
      </select>
      <ChevronDown className="pointer-events-none absolute right-2 h-3 w-3 text-faint" />
    </label>
  );
}

function Segmented({
  value,
  onChange,
  options,
}: {
  value: string;
  onChange: (v: string) => void;
  options: { label: string; value: string }[];
}) {
  return (
    <div className="flex items-center rounded-md border border-line bg-overlay p-0.5">
      {options.map((o) => (
        <button
          key={o.value}
          onClick={() => onChange(o.value)}
          className={`rounded px-2 py-1 font-mono text-[11px] transition-colors ${
            value === o.value ? 'bg-base text-strong' : 'text-faint hover:text-muted'
          }`}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}
