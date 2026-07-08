'use client';

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useParams, useSearchParams } from 'next/navigation';
import { cn } from '@/lib/cn';
import { Spinner } from '@/components/ui';
import { BrushHistogram, BrushPill, type BrushWindow } from '@/components/ui/brush-histogram';
import { LogDrawer } from '@/components/logs/log-drawer';
import { useMe } from '@/components/app/me-context';
import { useScope } from '@/components/app/scope-context';
import { PageHeader } from '@/components/app/page-header';
import { getLogs } from '@/lib/api/controlplane';
import type { LogBucket, LogLine } from '@/lib/api/types';

// Logs-Explorer (RETICLE): Masthead-Keyline, Prompt-Caret-Suche, Volumen-
// Histogramm (Errors glimmen rot), dichte Mono-Zeilen mit Severity-Keyline
// links. Scope (Namespace) kommt aus dem globalen Selektor; Live-Poll alle 4s,
// pausiert automatisch bei Suche/Zeilen-Hover (keine springenden Zeilen).

const RANGES = [
  { key: '15m', label: '15m' },
  { key: '1h', label: '1h' },
  { key: '6h', label: '6h' },
  { key: '24h', label: '24h' },
] as const;

const SEVERITIES = [
  { key: 0, label: 'All' },
  { key: 9, label: 'Info+' },
  { key: 13, label: 'Warn+' },
  { key: 17, label: 'Error' },
] as const;

const SEV_COLOR: Record<string, string> = {
  FATAL: 'var(--rp-red)',
  ERROR: 'var(--rp-red)',
  WARN: 'var(--rp-yellow)',
  INFO: 'var(--rp-ink-faint)',
  DEBUG: 'var(--rp-ink-faint)',
  TRACE: 'var(--rp-ink-faint)',
};

export default function LogsPage() {
  const params = useParams<{ id: string }>();
  const clusterId = params.id;
  const { currentOrg } = useMe();
  const { namespace } = useScope();
  const orgId = currentOrg?.id;
  // Deep-Link aus der Service-Map: /logs?workload=<name> scoped auf den Workload.
  const searchParams = useSearchParams();
  const [workload, setWorkload] = useState<string | null>(searchParams.get('workload'));

  const [range, setRange] = useState<string>('15m');
  const [minSev, setMinSev] = useState<number>(0);
  const [search, setSearch] = useState('');
  const [rx, setRx] = useState(false); // Regex-Modus (RE2) für den Hauptterm
  const [debounced, setDebounced] = useState('');
  const [lines, setLines] = useState<LogLine[] | null>(null);
  const [histogram, setHistogram] = useState<LogBucket[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [paused, setPaused] = useState(false);
  // Brush-Selektion im Histogramm → absolutes Zeitfenster (friert Live ein).
  const [brush, setBrush] = useState<BrushWindow | null>(null);
  const [openLine, setOpenLine] = useState<LogLine | null>(null);
  const firstLoad = useRef(true);

  // Suche entprellen (300ms) — jede Eingabe soll nicht sofort queryen.
  useEffect(() => {
    const t = window.setTimeout(() => setDebounced(search), 300);
    return () => window.clearTimeout(t);
  }, [search]);

  const load = useCallback(async () => {
    if (!orgId) return;
    try {
      // Prompt-Syntax: `-token` schließt aus (NOT), `~token` sucht fuzzy
      // (tippfehlertolerant), der Rest ist Substring — oder RE2 bei aktivem .*
      const q = parseLogQuery(debounced, rx);
      const res = await getLogs(orgId, clusterId, {
        since: brush?.since ?? range,
        until: brush?.until,
        namespace: namespace ?? undefined,
        workload: workload ?? undefined,
        minSeverity: minSev || undefined,
        ...q,
        limit: 300,
      });
      setLines(res.lines);
      setHistogram(res.histogram);
      setError(null);
    } catch (err) {
      if (firstLoad.current) setError(err instanceof Error ? err.message : 'Failed to load logs');
    } finally {
      firstLoad.current = false;
    }
  }, [orgId, clusterId, range, brush, namespace, workload, minSev, debounced, rx]);

  useEffect(() => {
    firstLoad.current = true;
    setLines(null);
    void load();
  }, [load]);

  useEffect(() => {
    if (paused || brush) return; // Brush = eingefrorenes Fenster, kein Live-Poll
    const id = window.setInterval(load, 4000);
    return () => window.clearInterval(id);
  }, [load, paused, brush]);

  const total = useMemo(() => histogram.reduce((s, b) => s + b.count, 0), [histogram]);
  const errors = useMemo(() => histogram.reduce((s, b) => s + b.errors, 0), [histogram]);

  return (
    <div className="flex h-[calc(100dvh-52px)] flex-col px-4 pt-4 sm:px-5">
      <PageHeader kicker={`${namespace ?? 'all namespaces'} / logs`} title="Logs">
        <div className="flex items-center gap-3 font-mono text-[11px] text-muted tnum">
          <span>{total.toLocaleString()} lines</span>
          {errors > 0 ? (
            <span style={{ color: 'var(--rp-red)' }}>{errors.toLocaleString()} errors</span>
          ) : null}
          <span className="flex items-center gap-1.5">
            <span
              className={cn('inline-block h-1.5 w-1.5 rounded-full', !paused && 'rp-breath')}
              style={{ background: paused ? 'var(--rp-ink-faint)' : 'var(--rp-green)', color: 'var(--rp-green)' }}
            />
            {paused ? 'paused' : 'live'}
          </span>
        </div>
      </PageHeader>

      {/* Toolbar: Prompt-Suche + Range + Severity */}
      <div className="mt-3 flex shrink-0 flex-wrap items-center gap-2">
        <div
          className="flex h-9 min-w-[260px] flex-1 items-center gap-2 rounded-skin-sm border border-line bg-inset px-3"
          onFocusCapture={() => setPaused(true)}
          onBlurCapture={() => setPaused(false)}
        >
          <span className="font-mono text-[13px]" style={{ color: 'var(--rp-accent)' }}>
            ›
          </span>
          <input
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            spellCheck={false}
            placeholder="search log bodies…   -exclude   ~fuzzy"
            title="plain = substring · -token excludes · ~token is typo-tolerant · .* switches the main term to RE2 regex"
            className="h-full w-full bg-transparent font-mono text-[12.5px] text-ink outline-none placeholder:text-faint"
          />
          <button
            type="button"
            onClick={() => setRx((v) => !v)}
            title={rx ? 'regex mode ON (RE2) — click for plain substring' : 'plain substring — click for RE2 regex mode'}
            aria-pressed={rx}
            className={cn(
              'shrink-0 rounded-skin-chip border px-1.5 py-0.5 font-mono text-[10px] transition-colors',
              rx ? 'border-line-strong bg-hover text-ink' : 'border-line text-faint hover:text-mid',
            )}
          >
            .*
          </button>
          {search ? (
            <button
              type="button"
              onClick={() => setSearch('')}
              className="font-mono text-[11px] text-faint hover:text-ink"
            >
              ✕
            </button>
          ) : null}
        </div>

        {workload ? (
          <button
            type="button"
            onClick={() => setWorkload(null)}
            className="h-9 rounded-skin-sm border px-3 font-mono text-[11px] transition-colors"
            style={{
              borderColor: 'color-mix(in oklab, var(--rp-accent) 40%, transparent)',
              color: 'var(--rp-tone-accent-fg)',
              background: 'var(--rp-tone-accent-bg)',
            }}
            title="Workload-Filter entfernen"
          >
            {workload} ✕
          </button>
        ) : null}

        {brush ? <BrushPill brush={brush} onClear={() => setBrush(null)} /> : null}
        <div className={brush ? 'pointer-events-none opacity-40' : undefined}>
          <Segmented value={range} options={RANGES.map((r) => ({ key: r.key, label: r.label }))} onChange={setRange} />
        </div>
        <Segmented
          value={String(minSev)}
          options={SEVERITIES.map((s) => ({ key: String(s.key), label: s.label }))}
          onChange={(v) => setMinSev(Number(v))}
        />
      </div>

      <BrushHistogram
        buckets={histogram}
        brush={brush}
        onBrushChange={setBrush}
        rangeLabel={`−${range}`}
        unit="lines"
      />

      {/* Zeilen */}
      <div
        className="mt-3 min-h-0 flex-1 overflow-y-auto rounded-t-skin border border-b-0 border-line bg-raised"
        style={{ boxShadow: 'var(--rp-rim)' }}
        onMouseEnter={() => setPaused(true)}
        onMouseLeave={() => setPaused(false)}
      >
        {error ? (
          <div className="flex h-full items-center justify-center font-mono text-[12px] text-red">{error}</div>
        ) : lines === null ? (
          <div className="flex h-full items-center justify-center gap-2 text-muted">
            <Spinner /> <span className="font-mono text-[12px]">Loading logs…</span>
          </div>
        ) : lines.length === 0 ? (
          <div className="flex h-full flex-col items-center justify-center gap-2 text-center">
            <div className="rp-micro !text-[10px] text-faint">no output</div>
            <p className="font-mono text-[12.5px] text-muted">No log lines match.</p>
            <p className="max-w-[38ch] font-mono text-[11px] leading-relaxed text-faint">
              The agent tails /var/log/pods — new lines appear within seconds.
            </p>
          </div>
        ) : (
          <table className="w-full border-collapse font-mono text-[11.5px] leading-[1.5]">
            <tbody>
              {lines.map((l, i) => {
                const sevColor = SEV_COLOR[l.severityText] ?? 'var(--rp-ink-faint)';
                const isErr = l.severityNumber >= 17;
                const isWarn = l.severityNumber >= 13 && l.severityNumber < 17;
                return (
                  <tr
                    key={`${l.ts}-${i}`}
                    onClick={() => setOpenLine(l)}
                    className="group cursor-pointer border-b border-line/60 align-top transition-colors hover:bg-hover"
                  >
                    {/* Severity-Keyline */}
                    <td className="w-[3px] p-0">
                      <div
                        className="h-full min-h-[24px] w-[2px]"
                        style={{ background: isErr || isWarn ? sevColor : 'transparent' }}
                      />
                    </td>
                    <td className="whitespace-nowrap py-1 pl-2 pr-3 text-muted tnum">
                      {l.ts.slice(11, 23)}
                    </td>
                    <td className="whitespace-nowrap py-1 pr-3">
                      <span
                        className="rounded-skin-chip px-1 py-0.5 text-[9.5px] font-medium uppercase tracking-[0.06em]"
                        style={{
                          color: isErr
                            ? 'var(--rp-tone-red-fg)'
                            : isWarn
                              ? 'var(--rp-tone-yellow-fg)'
                              : l.severityNumber <= 5
                                ? 'var(--rp-ink-faint)'
                                : 'var(--rp-ink-muted)',
                          background: isErr
                            ? 'var(--rp-tone-red-bg)'
                            : isWarn
                              ? 'var(--rp-tone-yellow-bg)'
                              : 'var(--rp-tone-neutral-bg)',
                        }}
                      >
                        {l.severityText || 'INFO'}
                      </span>
                    </td>
                    <td className="hidden whitespace-nowrap py-1 pr-3 text-mid sm:table-cell">
                      {l.workloadName}
                      <span className="text-faint"> · {l.namespace}</span>
                    </td>
                    {/* Body im Level-Ton (dash0-Stil): Fehler crimson, Warn gold */}
                    <td
                      className="w-full py-1 pr-4"
                      style={{
                        wordBreak: 'break-all',
                        color: isErr
                          ? 'var(--rp-tone-red-fg)'
                          : isWarn
                            ? 'var(--rp-tone-yellow-fg)'
                            : l.severityNumber <= 5
                              ? 'var(--rp-ink-muted)'
                              : 'var(--rp-ink)',
                      }}
                    >
                      <LogBody body={l.body} />
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
      </div>

      {openLine && orgId ? (
        <LogDrawer
          orgId={orgId}
          clusterId={clusterId}
          line={openLine}
          onClose={() => setOpenLine(null)}
        />
      ) : null}
    </div>
  );
}

// Führende Quell-Timestamps duplizieren die geparste Zeit-Spalte links — sie
// werden aus der Anzeige getrimmt (die Rohdaten bleiben unangetastet), damit
// die Message ohne totes Spaltenloch sofort beginnt.
const LEADING_TS = /^((?:\d{4}[-/]\d{2}[-/]\d{2}[T ])?\d{2}:\d{2}:\d{2}(?:[.,]\d+)?(?:Z|[+-]\d{2}:?\d{2})?\s+)/;

function LogBody({ body }: { body: string }) {
  const m = body.match(LEADING_TS);
  const text = m && m[1] ? body.slice(m[1].length) : body;
  return <>{text}</>;
}

/* Segmented-Control (RETICLE): geometrische Chips, aktiver Tab mit Ink. */
function Segmented({
  value,
  options,
  onChange,
}: {
  value: string;
  options: { key: string; label: string }[];
  onChange: (v: string) => void;
}) {
  return (
    <div className="flex h-9 items-center gap-[3px] rounded-skin-sm border border-line bg-inset p-[3px]">
      {options.map((o) => {
        const active = o.key === value;
        return (
          <button
            key={o.key}
            type="button"
            onClick={() => onChange(o.key)}
            className={cn(
              'h-full rounded-[4px] px-2.5 font-mono text-[11px] transition-colors',
              active ? 'bg-raised text-ink' : 'text-muted hover:text-ink',
            )}
            style={active ? { boxShadow: 'var(--rp-rim), 0 1px 2px rgba(0,0,0,0.2)' } : undefined}
          >
            {o.label}
          </button>
        );
      })}
    </div>
  );
}

// parseLogQuery zerlegt die Prompt-Eingabe: `-token` → exclude (NOT),
// `~token` → fuzzy (ngram), Rest = Hauptterm (Substring oder RE2 bei rx).
function parseLogQuery(input: string, rx: boolean): { search?: string; regexes?: string[]; exclude?: string[]; fuzzy?: string } {
  const exclude: string[] = [];
  let fuzzy = '';
  const rest: string[] = [];
  for (const tok of input.split(/\s+/)) {
    if (!tok) continue;
    if (tok.startsWith('-') && tok.length > 1) exclude.push(tok.slice(1));
    else if (tok.startsWith('~') && tok.length > 1) fuzzy = tok.slice(1);
    else rest.push(tok);
  }
  const main = rest.join(' ').trim();
  return {
    ...(main ? (rx ? { regexes: [main] } : { search: main }) : {}),
    ...(exclude.length ? { exclude: exclude.slice(0, 5) } : {}),
    ...(fuzzy ? { fuzzy } : {}),
  };
}
