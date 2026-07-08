'use client';

import { useEffect, useRef, useState, useSyncExternalStore, type ReactNode } from 'react';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { useScope } from './scope-context';
import { ToolResultView } from './copilot-panels';
import { InvestigationGraph } from './copilot-graph';
import { copilotStore, GREETING, type Item, type Block, type Activity, type ChatSummary } from './copilot-store';
import { APPROVAL_MODES, LEVEL_META, RISK_LEVELS, levelColor, loadApprovalPolicy, saveApprovalPolicy, type ApprovalMode, type ApprovalPolicy, type RiskLevel } from '@/lib/approval';
import type { LLMConfig } from '@/lib/llm';

// copilot.tsx — das Primärfeature: der AI-Infrastruktur-Copilot. Prominenter
// Topbar-Button → eigene Seite. Der Chat spricht das ECHTE LLM-Backend
// (/copilot/chat): Tool-Calling gegen die Safe-Actions + read (logs/metrics/…).
// Interaktiv: jeder Tool-Call öffnet ein Inspector-Panel (rechts) mit dem, was
// der Agent sieht; Safe-Actions stoppen den Loop (Human-in-the-Loop) bis Freigabe.

export function Sparkle({ size = 14 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" aria-hidden>
      <path d="M12 2.5c.5 3.7 2.3 5.5 6 6-3.7.5-5.5 2.3-6 6-.5-3.7-2.3-5.5-6-6 3.7-.5 5.5-2.3 6-6Z" fill="currentColor" />
      <path d="M19 13.5c.25 1.6 1 2.4 2.5 2.6-1.5.25-2.25 1-2.5 2.6-.25-1.6-1-2.35-2.5-2.6 1.5-.2 2.25-1 2.5-2.6Z" fill="currentColor" opacity="0.7" />
    </svg>
  );
}

export function CopilotButton() {
  const router = useRouter();
  const { activeClusterId } = useScope();
  const href = activeClusterId ? `/clusters/${activeClusterId}/copilot` : '/';
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'j') {
        e.preventDefault();
        if (activeClusterId) router.push(`/clusters/${activeClusterId}/copilot`);
      }
    }
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [router, activeClusterId]);
  return (
    <Link
      href={href}
      aria-label="Open Copilot"
      className="rp-focus group inline-flex h-8 shrink-0 items-center gap-1.5 rounded-skin-sm border px-2.5 font-mono text-[11.5px] font-semibold transition-all"
      style={{
        color: 'var(--rp-ink)',
        borderColor: 'color-mix(in oklab, var(--rp-accent) 42%, var(--rp-line-strong))',
        background: 'color-mix(in oklab, var(--rp-accent) 12%, var(--rp-bg-raised))',
        boxShadow: '0 0 0 1px color-mix(in oklab, var(--rp-accent) 16%, transparent), 0 6px 18px -8px color-mix(in oklab, var(--rp-accent) 55%, transparent)',
      }}
    >
      <span style={{ color: 'var(--rp-accent)' }} className="rp-breath">
        <Sparkle />
      </span>
      <span>Copilot</span>
      <kbd className="ml-0.5 hidden rounded-skin-chip border px-1 py-px text-[8.5px] font-normal sm:inline" style={{ borderColor: 'color-mix(in oklab, var(--rp-accent) 30%, var(--rp-line))', color: 'var(--rp-ink-muted)' }}>
        ⌘J
      </kbd>
    </Link>
  );
}

/* ── Mini-Markdown ────────────────────────────────────────────────────────
   Abhängigkeitsfreier Renderer fürs Chat: fett, inline-code, code-fences,
   Überschriften, Listen, Links. Streaming-tauglich — halb offene `**`/```
   bleiben literal, bis das Closing-Token im nächsten Frame nachkommt. */
function mdInline(text: string): ReactNode[] {
  const out: ReactNode[] = [];
  const re = /(`[^`]+`)|(\*\*[^*]+?\*\*)|(\[[^\]]+\]\([^)\s]+\))/g;
  let last = 0;
  let key = 0;
  let m: RegExpExecArray | null;
  while ((m = re.exec(text)) !== null) {
    if (m.index > last) out.push(text.slice(last, m.index));
    const tok = m[0];
    if (tok.startsWith('`')) {
      out.push(
        <code key={key++} className="rounded-skin-chip border border-line bg-inset px-1 py-px text-[11px] text-ink">{tok.slice(1, -1)}</code>,
      );
    } else if (tok.startsWith('**')) {
      out.push(<strong key={key++} className="font-semibold text-ink">{tok.slice(2, -2)}</strong>);
    } else {
      const lm = /\[([^\]]+)\]\(([^)\s]+)\)/.exec(tok);
      if (lm) out.push(<a key={key++} href={lm[2]} target="_blank" rel="noreferrer" className="text-accent underline underline-offset-2 hover:text-accent-hover">{lm[1]}</a>);
      else out.push(tok);
    }
    last = m.index + tok.length;
  }
  if (last < text.length) out.push(text.slice(last));
  return out;
}

const splitRow = (s: string) => s.trim().replace(/^\||\|$/g, '').split('|').map((c) => c.trim());
const isTableSep = (s: string) => /^\s*\|?[\s:|-]*-[\s:|-]*\|?\s*$/.test(s) && s.includes('-') && s.includes('|');

function Markdown({ text }: { text: string }) {
  const lines = text.replace(/\r/g, '').split('\n');
  const at = (n: number) => lines[n] ?? '';
  const blocks: ReactNode[] = [];
  let i = 0;
  let key = 0;
  const isUL = (s: string) => /^\s*[-*]\s+/.test(s);
  const isOL = (s: string) => /^\s*\d+\.\s+/.test(s);
  while (i < lines.length) {
    const line = at(i);
    // Tabelle: Header-Zeile mit `|` + Separatorzeile `|---|---|` darunter.
    if (line.includes('|') && isTableSep(at(i + 1))) {
      const header = splitRow(line);
      i += 2;
      const rows: string[][] = [];
      while (i < lines.length && at(i).includes('|') && at(i).trim() !== '') { rows.push(splitRow(at(i))); i++; }
      blocks.push(
        <div key={key++} className="overflow-x-auto rounded-skin-sm border border-line">
          <table className="w-full border-collapse text-[11px]">
            <thead>
              <tr className="border-b border-line bg-inset">
                {header.map((h, k) => <th key={k} className="px-2.5 py-1.5 text-left font-semibold text-ink">{mdInline(h)}</th>)}
              </tr>
            </thead>
            <tbody>
              {rows.map((r, rk) => (
                <tr key={rk} className={rk < rows.length - 1 ? 'border-b border-line' : ''}>
                  {header.map((_, ck) => <td key={ck} className="px-2.5 py-1.5 align-top text-mid">{mdInline(r[ck] ?? '')}</td>)}
                </tr>
              ))}
            </tbody>
          </table>
        </div>,
      );
      continue;
    }
    if (line.trimStart().startsWith('```')) {
      const buf: string[] = [];
      i++;
      while (i < lines.length && !at(i).trimStart().startsWith('```')) { buf.push(at(i)); i++; }
      i++; // closing fence (falls vorhanden)
      blocks.push(
        <pre key={key++} className="overflow-x-auto rounded-skin-sm border border-line bg-inset p-2.5 text-[11px] leading-relaxed text-mid"><code>{buf.join('\n')}</code></pre>,
      );
      continue;
    }
    const h = /^(#{1,6})\s+(.*)$/.exec(line);
    if (h) { blocks.push(<p key={key++} className="pt-0.5 text-[12.5px] font-semibold text-ink">{mdInline(h[2] ?? '')}</p>); i++; continue; }
    if (isUL(line)) {
      const items: string[] = [];
      while (i < lines.length && isUL(at(i))) { items.push(at(i).replace(/^\s*[-*]\s+/, '')); i++; }
      blocks.push(<ul key={key++} className="ml-1 space-y-1">{items.map((it, k) => <li key={k} className="flex gap-2"><span className="mt-[3px] shrink-0 text-faint">•</span><span className="min-w-0">{mdInline(it)}</span></li>)}</ul>);
      continue;
    }
    if (isOL(line)) {
      const items: string[] = [];
      while (i < lines.length && isOL(at(i))) { items.push(at(i).replace(/^\s*\d+\.\s+/, '')); i++; }
      blocks.push(<ol key={key++} className="ml-1 space-y-1">{items.map((it, k) => <li key={k} className="flex gap-2"><span className="mt-px shrink-0 tabular-nums text-faint">{k + 1}.</span><span className="min-w-0">{mdInline(it)}</span></li>)}</ol>);
      continue;
    }
    if (line.trim() === '') { i++; continue; }
    const buf: string[] = [];
    while (i < lines.length && at(i).trim() !== '' && !at(i).trimStart().startsWith('```') && !/^#{1,6}\s/.test(at(i)) && !isUL(at(i)) && !isOL(at(i))) { buf.push(at(i)); i++; }
    blocks.push(
      <p key={key++} className="whitespace-pre-wrap leading-relaxed">
        {buf.map((l, k) => <span key={k}>{k > 0 ? <br /> : null}{mdInline(l)}</span>)}
      </p>,
    );
  }
  return <div className="space-y-1.5 text-[12px] text-mid">{blocks}</div>;
}

// Blinkender Stream-Cursor am Ende des gestreamten Textes (wie ChatGPT/Claude).
function Caret() {
  return <span className="rp-caret" aria-hidden />;
}

// Drei pulsierende Punkte — nur VOR dem ersten Token.
function ThinkingDots() {
  return (
    <div className="flex items-center gap-1 py-1" aria-label="thinking">
      {[0, 1, 2].map((n) => (
        <span
          key={n}
          className="rp-breath inline-block h-1.5 w-1.5 rounded-full"
          style={{ color: 'var(--rp-accent)', background: 'currentColor', animationDelay: `${n * 0.18}s` }}
        />
      ))}
      <span className="ml-1.5 font-mono text-[11px] text-muted">thinking…</span>
    </div>
  );
}

/* ── Modell (Typen + Store in ./copilot-store) ──────────────────────────── */

const SUGGESTIONS = ["What’s failing right now?", 'Why is payments slow?', 'Scale checkout to 3', 'Clean up failed pods'];
function relTime(iso: string): string {
  const t = Date.parse(iso);
  if (!t) return '';
  const s = Math.max(0, (Date.now() - t) / 1000);
  if (s < 60) return 'just now';
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86400)}d ago`;
}
// Risk-Level-Badges (Quelle: lib/approval.ts — dieselbe Skala wie die Guardrails).
const KLASS: Record<string, { label: string; glyph: string }> = Object.fromEntries(
  RISK_LEVELS.map((l) => [l, { label: LEVEL_META[l].label, glyph: LEVEL_META[l].glyph }]),
);

const TOOL_LABEL: Record<string, string> = {
  query_logs: 'read logs',
  query_traces: 'list traces',
  get_trace: 'open trace',
  trace_logs: 'trace → logs',
  span_stats: 'span latency',
  query_metrics: 'query metrics',
  list_metrics: 'list metrics',
  wait: 'wait',
  wait_for_action: 'wait for action',
  get_service_map: 'service map',
  get_infra: 'read infrastructure',
  debug_bundle: 'debug bundle',
};
const toolLabel = (n: string) => TOOL_LABEL[n] ?? n.replace(/_/g, ' ');

// Statusfarbe/-glyph für einen Aktivitäts-Status.
function statusTone(s: string): { color: string; glyph: string; live?: boolean } {
  switch (s) {
    case 'running':
    case 'starting':
    case 'pending':
      return { color: 'var(--rp-ink-muted)', glyph: '◐', live: true };
    case 'ok':
    case 'succeeded':
      return { color: 'var(--rp-tone-green-fg)', glyph: '●' };
    case 'error':
    case 'failed':
      return { color: 'var(--rp-tone-red-fg)', glyph: '▲' };
    case 'awaiting':
      return { color: 'var(--rp-accent)', glyph: '◆', live: true };
    case 'rejected':
    case 'cancelled':
      return { color: 'var(--rp-ink-faint)', glyph: '○' };
    case 'blocked':
      return { color: 'var(--rp-ink-faint)', glyph: '⊘' };
    default:
      return { color: 'var(--rp-ink-muted)', glyph: '◐', live: true };
  }
}

function pretty(v: unknown): string {
  if (v == null) return '';
  if (typeof v === 'string') {
    const t = v.trim();
    if (t.startsWith('{') || t.startsWith('[')) {
      try { return JSON.stringify(JSON.parse(t), null, 2); } catch { /* not json */ }
    }
    return v;
  }
  try { return JSON.stringify(v, null, 2); } catch { return String(v); }
}

/* ── Chat + Inspector ─────────────────────────────────────────────────────── */

export function CopilotChat({ orgId, clusterId, config }: { orgId: string; clusterId: string; config: LLMConfig }) {
  // View-lokaler Zustand. Der eigentliche Chat-/Streaming-Zustand lebt im STORE,
  // keyed by chatId — dadurch bleibt ein laufender Stream an SEINEN Chat gebunden,
  // Streams laufen parallel und persistieren unabhängig vom angezeigten View.
  const [activeId, setActiveId] = useState('');
  const [input, setInput] = useState('');
  const [selected, setSelected] = useState<string | null>(null);
  const [inspectorOpen, setInspectorOpen] = useState(false);
  const [historyOpen, setHistoryOpen] = useState(false);
  const scrollRef = useRef<HTMLDivElement>(null);

  const session = useSyncExternalStore(copilotStore.subscribe, () => copilotStore.getSession(activeId), () => undefined);
  const chats = useSyncExternalStore(copilotStore.subscribe, () => copilotStore.getChats(clusterId), () => copilotStore.getChats(clusterId));
  const { namespace } = useScope(); // aktiver Namespace-Scope (null = alle)

  useEffect(() => {
    if (!activeId) setActiveId(copilotStore.ensure(orgId, clusterId, undefined, config));
    else copilotStore.setConfig(activeId, config);
  }, [activeId, orgId, clusterId, config]);
  useEffect(() => { copilotStore.refreshChats(orgId, clusterId); }, [orgId, clusterId]);
  // Scope an die Session binden: Reads filtern darauf, Actions sind hart darauf begrenzt.
  useEffect(() => { if (activeId) copilotStore.setScope(activeId, namespace ?? ''); }, [activeId, namespace]);

  const items = session?.items ?? [GREETING];
  const activities = session?.activities ?? [];
  const busy = session?.busy ?? false;
  const rewindUndo = session?.rewindUndo ?? null;

  useEffect(() => { scrollRef.current?.scrollTo({ top: scrollRef.current.scrollHeight, behavior: 'smooth' }); }, [items]);

  // Inspector folgt LIVE dem zuletzt aktiven Tool/der Action (bis der Nutzer selbst
  // einen Chip anklickt — bis zum nächsten Tool). Beim Chat-Wechsel neu setzen.
  const lastActivityId = session?.lastActivityId;
  useEffect(() => { if (lastActivityId) setSelected(lastActivityId); }, [lastActivityId]);

  function newChat() {
    const id = copilotStore.ensure(orgId, clusterId, undefined, config);
    setActiveId(id); setInput(''); setSelected(null); setHistoryOpen(false); setInspectorOpen(false);
  }
  async function loadChat(id: string) {
    if (id === activeId) { setHistoryOpen(false); return; }
    await copilotStore.load(orgId, clusterId, id);
    copilotStore.setConfig(id, config);
    setActiveId(id); setSelected(null); setHistoryOpen(false); setInspectorOpen(false);
  }
  async function deleteChat(id: string) {
    if (id === activeId) newChat();
    await copilotStore.remove(orgId, clusterId, id);
  }
  function send(text: string) {
    const t = text.trim();
    if (!t) return;
    setInput(''); setInspectorOpen(false);
    copilotStore.send(activeId, t);
  }
  const rewindTo = (cut: number, refill?: string) => copilotStore.rewind(activeId, cut, (t) => setInput(refill !== undefined ? refill : t));
  const undoRewind = () => copilotStore.undoRewind(activeId);
  const dismissRewind = () => copilotStore.clearRewindUndo(activeId);
  const decide = (a: Activity, approve: boolean) => copilotStore.decide(activeId, a.id, approve);
  const answer = (a: Activity, value: { value?: string; values?: string[] }, secret?: boolean) => copilotStore.answer(activeId, a.id, value, { secret });

  // Action ODER Frage wartet auf Entscheidung? Dann ist alles gesperrt bis zur Antwort.
  const awaiting = activities.find((a) => (a.isAction || a.isQuestion) && a.status === 'awaiting');
  useEffect(() => { if (awaiting?.isAction) setInspectorOpen(true); }, [awaiting?.id, awaiting?.isAction]);
  const activeAction = activities.find((a) => a.isAction && !a.terminal && a.status !== 'rejected' && a.status !== 'cancelled');
  const locked = busy;
  const selectedActivity = activities.find((a) => a.id === selected) ?? null;
  const nodes = session?.nodes ?? [];

  // Graph-Knoten anklicken → die zugehörigen Tool-Calls im Inspector fokussieren.
  const focusNode = (nodeId: string) => {
    const acts = activities.filter((a) => a.nodeId === nodeId);
    const a = acts[acts.length - 1];
    if (a) { setSelected(a.id); setInspectorOpen(true); }
  };

  const inspector = (
    <Inspector activity={selectedActivity} onDecide={decide} onClose={() => setInspectorOpen(false)} />
  );

  const firstUserText = (items.find((i) => i.role === 'user') as { text: string } | undefined)?.text;
  const currentTitle = session?.title || chats.find((c) => c.id === activeId)?.title || firstUserText || 'New investigation';
  // Reihenfolge = letzte AKTIVITÄT (bumpt nur bei neuer Nachricht, nicht beim
  // Anklicken). Der Store hält die Liste inkl. gerade gestarteter Chats aktuell;
  // der aktive Chat wird nur HERVORGEHOBEN, nicht umsortiert.
  const railChats: ChatSummary[] = chats;
  const historyRail = (
    <div className="flex h-full flex-col overflow-hidden rounded-skin border border-line bg-raised" style={{ boxShadow: 'var(--rp-rim)' }}>
      <div className="flex shrink-0 items-center gap-2 border-b border-line px-2.5 py-2">
        <span className="rp-micro !text-[10px] flex-1">chats</span>
        <button type="button" onClick={newChat} className="rp-focus inline-flex items-center gap-1 rounded-skin-sm border border-line px-1.5 py-0.5 font-mono text-[10px] text-mid transition-colors hover:bg-hover hover:text-ink">＋ new</button>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto p-1.5">
        {railChats.length === 0 ? (
          <p className="px-1.5 py-3 font-mono text-[10px] leading-relaxed text-faint">Saved investigations appear here. Ask a question to start one.</p>
        ) : (
          railChats.map((c) => {
            const on = c.id === activeId;
            return (
              <div key={c.id} className={`group flex items-start gap-1 rounded-skin-sm px-2 py-1.5 ${on ? 'bg-hover' : 'hover:bg-hover'}`} style={on ? { boxShadow: 'inset 0 0 0 1px var(--rp-line-strong)' } : undefined}>
                <button type="button" onClick={() => loadChat(c.id)} className="min-w-0 flex-1 text-left">
                  <div className="truncate font-mono text-[11px] text-ink">{c.title || 'Untitled'}</div>
                  {c.summary ? <div className="mt-0.5 line-clamp-2 font-mono text-[9.5px] leading-snug text-muted">{c.summary}</div> : null}
                  <div className="mt-0.5 font-mono text-[9px] text-faint">{relTime(c.updatedAt)}</div>
                </button>
                <button type="button" onClick={() => deleteChat(c.id)} className="rp-focus mt-0.5 shrink-0 rounded-skin-chip px-1 font-mono text-[10px] text-faint opacity-0 transition-opacity hover:text-ink group-hover:opacity-100" aria-label="delete conversation">✕</button>
              </div>
            );
          })
        )}
      </div>
    </div>
  );

  return (
    <div className="flex min-h-0 flex-1 gap-3">
      {/* ── Vorgänge-Rail (Desktop) ─────────────────────────────────── */}
      <aside className="hidden w-56 shrink-0 xl:block">{historyRail}</aside>

      {/* ── Chat-Spalte ─────────────────────────────────────────────── */}
      <div className="relative flex min-h-0 flex-1 flex-col overflow-hidden rounded-skin border border-line bg-raised" style={{ boxShadow: 'var(--rp-rim)' }}>
        <div className="pointer-events-none absolute inset-x-0 top-0 h-24" aria-hidden style={{ background: 'radial-gradient(70% 100% at 50% 0%, color-mix(in oklab, var(--rp-accent) 12%, transparent), transparent 70%)' }} />

        {/* Kopfzeile: Vorgänge-Toggle (mobil) · neuer Vorgang · Titel · rewind */}
        <div className="relative flex shrink-0 items-center gap-2 border-b border-line px-3 py-2 sm:px-4">
          <button type="button" onClick={() => setHistoryOpen(true)} className="rp-focus rounded-skin-sm border border-line px-1.5 py-1 font-mono text-[11px] text-mid hover:bg-hover hover:text-ink xl:hidden" aria-label="conversations">☰</button>
          <button type="button" onClick={newChat} className="rp-focus inline-flex items-center gap-1 rounded-skin-sm border border-line px-2 py-1 font-mono text-[10.5px] text-mid transition-colors hover:bg-hover hover:text-ink">＋ new</button>
          <span className="min-w-0 flex-1 truncate font-mono text-[11.5px] text-mid">{currentTitle}</span>
          <span className="hidden shrink-0 items-center gap-1 rounded-skin-chip border border-line bg-inset px-1.5 py-0.5 font-mono text-[9.5px] text-muted sm:inline-flex" title="the Copilot investigates and acts only within this scope">
            <span style={{ color: namespace ? 'var(--rp-accent)' : 'var(--rp-ink-faint)' }}>▣</span>
            {namespace ? `ns: ${namespace}` : 'all namespaces'}
          </span>
          <GuardrailsMenu clusterId={clusterId} />
          {items.some((it) => it.role === 'user') ? (
            <button type="button" disabled={busy} onClick={() => { const i = items.findIndex((it) => it.role === 'user'); if (i >= 0) rewindTo(i, ''); }} title="Rewind to the start" className="rp-focus rounded-skin-sm border border-line px-2 py-1 font-mono text-[10.5px] text-mid transition-colors hover:bg-hover hover:text-ink disabled:opacity-40">↺ start</button>
          ) : null}
        </div>

        {rewindUndo ? (
          <div className="flex shrink-0 items-center gap-2 border-b px-3 py-1.5 sm:px-4" style={{ borderColor: 'color-mix(in oklab, var(--rp-accent) 30%, var(--rp-line))', background: 'color-mix(in oklab, var(--rp-accent) 8%, transparent)' }}>
            <span className="font-mono text-[10.5px] text-mid">↺ Rewound the conversation.</span>
            <button type="button" onClick={undoRewind} className="rp-focus ml-auto rounded-skin-chip border border-line px-2 py-0.5 font-mono text-[10px] text-ink hover:bg-hover">undo</button>
            <button type="button" onClick={dismissRewind} className="rp-focus rounded-skin-chip px-1 font-mono text-[10px] text-faint hover:text-ink" aria-label="dismiss">✕</button>
          </div>
        ) : null}

        <div ref={scrollRef} className="min-h-0 flex-1 overflow-y-auto px-4 py-5 sm:px-6">
          <div className="mx-auto max-w-[720px] space-y-3.5">
            {items.map((it, i) => {
              if (it.role === 'user') {
                return (
                  <div key={i} className="group flex items-start justify-end gap-1.5">
                    <button
                      type="button"
                      onClick={() => rewindTo(i, it.text)}
                      disabled={busy}
                      title="Rewind to before this message"
                      className="rp-focus mt-1.5 shrink-0 rounded-skin-chip border border-line px-1.5 py-0.5 font-mono text-[9.5px] text-faint opacity-0 transition-opacity hover:bg-hover hover:text-ink group-hover:opacity-100 disabled:opacity-0"
                    >↺ rewind</button>
                    <div className="max-w-[85%] rounded-skin border px-3.5 py-2 font-mono text-[12px] leading-relaxed text-ink" style={{ borderColor: 'color-mix(in oklab, var(--rp-accent) 30%, var(--rp-line))', background: 'color-mix(in oklab, var(--rp-accent) 8%, var(--rp-bg-inset))' }}>
                      {it.text}
                    </div>
                  </div>
                );
              }
              if (it.role === 'error') {
                return (
                  <div key={i} className="flex gap-3">
                    <span className="mt-0.5 shrink-0" style={{ color: 'var(--rp-tone-red-fg)' }}>▲</span>
                    <div className="rounded-skin-sm px-2.5 py-1.5 font-mono text-[11px] leading-relaxed" style={{ color: 'var(--rp-tone-red-fg)', background: 'var(--rp-tone-red-bg)' }}>{it.text}</div>
                  </div>
                );
              }
              if (it.role === 'note') {
                const line = it.text.replace(/^\[system\]\s*/, '').split('\n')[0]!.slice(0, 90);
                return (
                  <div key={i} className="flex justify-center py-0.5">
                    <span className="inline-flex items-center gap-1.5 rounded-skin-chip border border-line bg-inset px-2 py-0.5 font-mono text-[9.5px] text-faint">
                      <span style={{ color: 'var(--rp-ink-muted)' }}>↻</span> {line}
                    </span>
                  </div>
                );
              }
              const live = busy && i === items.length - 1;
              const lastText = (() => { for (let k = it.blocks.length - 1; k >= 0; k--) if (it.blocks[k]!.type === 'text') return k; return -1; })();
              return (
                <div key={i} className="flex gap-3">
                  <span className="mt-0.5 shrink-0" style={{ color: 'var(--rp-accent)' }}><Sparkle size={14} /></span>
                  <div className="min-w-0 flex-1 space-y-2">
                    {it.blocks.length === 0 && live ? (
                      <ThinkingDots />
                    ) : (
                      it.blocks.map((b, j) => {
                        if (b.type === 'text') {
                          return (
                            <div key={j} className="font-mono">
                              <Markdown text={b.text} />
                              {live && j === lastText ? <Caret /> : null}
                            </div>
                          );
                        }
                        if (b.type === 'tool') {
                          const act = activities.find((a) => a.id === b.refId);
                          const st = statusTone(act?.status ?? 'running');
                          const on = selected === b.refId;
                          return (
                            <button
                              key={j}
                              type="button"
                              onClick={() => { setSelected(b.refId); setInspectorOpen(true); }}
                              className={`inline-flex items-center gap-1.5 rounded-skin-chip border px-1.5 py-0.5 font-mono text-[9.5px] transition-colors ${on ? 'border-line-strong bg-hover text-ink' : 'border-line text-faint hover:bg-hover hover:text-mid'}`}
                            >
                              <span className={st.live ? 'rp-breath' : ''} style={{ color: st.color }}>{st.glyph}</span>
                              <span>{toolLabel(b.name)}</span>
                              <span className="text-faint">›</span>
                            </button>
                          );
                        }
                        if (b.type === 'question') {
                          const act = activities.find((a) => a.id === b.refId);
                          if (!act) return null;
                          return <QuestionCard key={j} act={act} onAnswer={answer} onDismiss={() => decide(act, false)} />;
                        }
                        // action
                        const act = activities.find((a) => a.id === b.refId);
                        if (!act) return null;
                        return <ActionCard key={j} act={act} selected={selected === b.refId} onOpen={() => { setSelected(b.refId); setInspectorOpen(true); }} onDecide={decide} />;
                      })
                    )}
                  </div>
                </div>
              );
            })}
          </div>
        </div>

        {/* Sperr-Banner solange eine Action auf Freigabe wartet oder läuft */}
        {awaiting ? (
          <div className="shrink-0 border-t px-4 py-2 sm:px-6" style={{ borderColor: 'color-mix(in oklab, var(--rp-accent) 40%, var(--rp-line))', background: 'color-mix(in oklab, var(--rp-accent) 10%, transparent)' }}>
            <div className="mx-auto flex max-w-[720px] items-center gap-2 font-mono text-[11px]">
              <span className="rp-breath" style={{ color: 'var(--rp-accent)' }}>◆</span>
              <span className="text-ink">{awaiting.isQuestion ? 'Copilot is paused — answer or dismiss the question to continue.' : 'Copilot is paused — approve or dismiss the action to continue.'}</span>
              <button type="button" onClick={() => setInspectorOpen(true)} className="ml-auto rounded-skin-chip border border-line px-2 py-0.5 text-mid hover:bg-hover hover:text-ink lg:hidden">review ›</button>
            </div>
          </div>
        ) : activeAction ? (
          <div className="shrink-0 border-t border-line px-4 py-2 sm:px-6" style={{ background: 'var(--rp-bg-inset)' }}>
            <div className="mx-auto flex max-w-[720px] items-center gap-2 font-mono text-[11px]">
              <span className="rp-breath" style={{ color: 'var(--rp-ink-muted)' }}>◐</span>
              <span className="text-mid">Running {activeAction.kind?.replace(/_/g, ' ')} on {activeAction.target} — everything is locked until it finishes.</span>
            </div>
          </div>
        ) : null}

        <div className="shrink-0 border-t border-line px-4 py-3 sm:px-6">
          <div className="mx-auto max-w-[720px]">
            {!locked ? (
              <div className="mb-2.5 flex flex-wrap gap-1.5">
                {SUGGESTIONS.map((s) => (
                  <button key={s} type="button" onClick={() => send(s)} className="rp-focus rounded-skin-chip border border-line px-2 py-1 font-mono text-[10px] text-muted transition-colors hover:bg-hover hover:text-ink">
                    {s}
                  </button>
                ))}
              </div>
            ) : null}
            <form onSubmit={(e) => { e.preventDefault(); send(input); }} className="flex items-center gap-2 rounded-skin-sm border bg-inset px-3" style={{ borderColor: awaiting ? 'color-mix(in oklab, var(--rp-accent) 40%, var(--rp-line))' : 'color-mix(in oklab, var(--rp-accent) 24%, var(--rp-line))' }}>
              <span style={{ color: 'var(--rp-accent)' }}><Sparkle size={14} /></span>
              <input value={input} onChange={(e) => setInput(e.target.value)} disabled={locked} placeholder={awaiting ? 'Waiting for your decision…' : locked ? 'Working…' : 'Ask Copilot to investigate or fix something…'} className="h-11 min-w-0 flex-1 bg-transparent font-mono text-[12.5px] text-ink outline-none placeholder:text-faint disabled:opacity-60" />
              <span className="hidden font-mono text-[9.5px] text-faint sm:inline" title="active model">{config.model}</span>
              <button type="submit" disabled={!input.trim() || locked} className="rp-focus flex h-8 w-8 items-center justify-center rounded-skin-sm text-[14px] transition-opacity disabled:opacity-40" style={{ background: 'var(--rp-btn-bg)', color: 'var(--rp-btn-fg)' }} aria-label="Send">
                ↑
              </button>
            </form>
          </div>
        </div>
      </div>

      {/* ── Inspector-Spalte (Desktop): Investigation-Graph + Inspector ── */}
      <aside className="hidden w-[360px] shrink-0 lg:flex lg:flex-col lg:gap-3 xl:w-[400px]">
        <InvestigationGraph nodes={nodes} onFocus={focusNode} />
        <div className="min-h-0 flex-1">{inspector}</div>
        <ReasoningLog lines={session?.reasoning ?? []} />
      </aside>

      {/* ── Inspector-Overlay (Mobil/Tablet) ───────────────────────── */}
      {inspectorOpen ? (
        <div className="fixed inset-0 z-40 lg:hidden">
          <div className="absolute inset-0 bg-black/40" onClick={() => setInspectorOpen(false)} />
          <div className="absolute inset-y-0 right-0 w-[min(420px,92vw)] p-2" style={{ animation: 'rp-drawer-in 200ms ease-out' }}>{inspector}</div>
        </div>
      ) : null}

      {/* ── Vorgänge-Overlay (unter xl) ────────────────────────────── */}
      {historyOpen ? (
        <div className="fixed inset-0 z-40 xl:hidden">
          <div className="absolute inset-0 bg-black/40" onClick={() => setHistoryOpen(false)} />
          <div className="absolute inset-y-0 left-0 w-[min(300px,86vw)] p-2" style={{ animation: 'rp-drawer-in 200ms ease-out' }}>{historyRail}</div>
        </div>
      ) : null}
    </div>
  );
}

/* ── Reasoning-Log (Dev-Transparenz): interne Master-Notizen, einklappbar ── */
function ReasoningLog({ lines }: { lines: string[] }) {
  const [open, setOpen] = useState(false);
  if (!lines.length) return null;
  return (
    <div className="shrink-0 overflow-hidden rounded-skin border border-line bg-raised" style={{ boxShadow: 'var(--rp-rim)' }}>
      <button type="button" onClick={() => setOpen((o) => !o)} className="rp-focus flex w-full items-center gap-2 px-3 py-2 text-left">
        <span className="rp-micro !text-[10px] flex-1">agent reasoning</span>
        <span className="font-mono text-[9.5px] tabular-nums text-muted">{lines.length}</span>
        <span className="font-mono text-[10px] text-faint">{open ? '▾' : '▸'}</span>
      </button>
      {open ? (
        <div className="max-h-[160px] overflow-y-auto border-t border-line px-3 py-2">
          {lines.map((l, i) => (
            <p key={i} className="py-0.5 font-mono text-[10px] leading-relaxed text-muted">· {l}</p>
          ))}
        </div>
      ) : null}
    </div>
  );
}

/* ── Guardrails: Approval-Modus je Risk-Level, einstellbar ───────────────── */
export function GuardrailsMenu({ clusterId }: { clusterId: string }) {
  const [open, setOpen] = useState(false);
  const [policy, setPolicy] = useState<ApprovalPolicy>(() => loadApprovalPolicy(clusterId));
  useEffect(() => { setPolicy(loadApprovalPolicy(clusterId)); }, [clusterId]);
  const set = (lvl: RiskLevel, m: ApprovalMode) => {
    const p = { ...policy, [lvl]: m };
    setPolicy(p);
    saveApprovalPolicy(clusterId, p);
  };
  return (
    <div className="relative shrink-0">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        title="Guardrails — how each risk level must be approved"
        className={`rp-focus inline-flex items-center gap-1 rounded-skin-sm border px-2 py-1 font-mono text-[10.5px] transition-colors ${open ? 'border-line-strong bg-hover text-ink' : 'border-line text-mid hover:bg-hover hover:text-ink'}`}
      >⛨ guardrails</button>
      {open ? (
        <>
          <div className="fixed inset-0 z-30" onClick={() => setOpen(false)} />
          <div className="absolute right-0 top-full z-40 mt-1 w-[320px] rounded-skin border border-line bg-raised p-2.5" style={{ boxShadow: 'var(--rp-rim), 0 16px 40px -12px rgba(0,0,0,0.5)' }}>
            <p className="rp-micro !text-[10px] mb-2">approval per risk level</p>
            <div className="space-y-2">
              {RISK_LEVELS.map((lvl) => (
                <div key={lvl}>
                  <div className="mb-1 flex items-baseline gap-1.5">
                    <span className="font-mono text-[10px]" style={{ color: levelColor(lvl) }}>{LEVEL_META[lvl].glyph}</span>
                    <span className="font-mono text-[11px] font-semibold text-ink">{LEVEL_META[lvl].label}</span>
                    <span className="min-w-0 flex-1 truncate font-mono text-[9px] text-faint">{LEVEL_META[lvl].hint}</span>
                  </div>
                  <div className="grid grid-cols-4 gap-1">
                    {APPROVAL_MODES.map((m) => {
                      const on = policy[lvl] === m;
                      return (
                        <button key={m} type="button" onClick={() => set(lvl, m)} className={`rp-focus rounded-skin-chip border px-1 py-0.5 font-mono text-[9.5px] transition-colors ${on ? 'border-line-strong bg-hover text-ink' : 'border-line text-faint hover:text-mid'}`}>
                          {m}
                        </button>
                      );
                    })}
                  </div>
                </div>
              ))}
            </div>
            <p className="mt-2 font-mono text-[9px] leading-relaxed text-faint">auto = runs unasked · click = one click · confirm = type the target name · off = the Copilot may not run this level</p>
          </div>
        </>
      ) : null}
    </div>
  );
}

/* ── Inline-Action-Karte im Chat (full-stop) ─────────────────────────────── */
function ActionCard({ act, selected, onOpen, onDecide }: { act: Activity; selected: boolean; onOpen: () => void; onDecide: (a: Activity, ok: boolean) => void }) {
  const k = KLASS[act.klass ?? 'reversible'] ?? KLASS.reversible!;
  const st = statusTone(act.status);
  const lvlColor = levelColor(act.klass);
  const isDestr = act.klass === 'destructive';
  const needsConfirm = act.approvalMode === 'confirm';
  const params = (act.args?.params as Record<string, unknown>) ?? {};
  const paramStr = JSON.stringify(params) !== '{}' ? JSON.stringify(params) : '';
  return (
    <div className={`overflow-hidden rounded-skin border ${selected ? 'border-line-strong' : 'border-line'}`} style={{ background: 'var(--rp-bg-inset)', boxShadow: act.status === 'awaiting' ? '0 0 0 1px color-mix(in oklab, var(--rp-accent) 40%, transparent)' : undefined }}>
      <button type="button" onClick={onOpen} className="flex w-full items-center gap-2 border-b border-line px-3 py-2 text-left">
        <span className={st.live ? 'rp-breath' : ''} style={{ color: st.color }}>{st.glyph}</span>
        <span className="font-mono text-[11px] font-semibold text-ink">{(act.kind ?? '').replace(/_/g, ' ')}</span>
        <span className="text-faint">·</span>
        <span className="min-w-0 flex-1 truncate font-mono text-[11px] text-mid">{act.target}</span>
        <span className="shrink-0 font-mono text-[9px] uppercase tracking-[0.05em]" style={{ color: lvlColor }}>{k.glyph} {k.label}</span>
      </button>
      <div className="px-3 py-2">
        <code className="block break-all font-mono text-[10px] leading-relaxed text-faint">
          {`${act.kind} ${act.args?.targetKind ?? ''}/${act.args?.targetName ?? act.target}${paramStr ? ' ' + paramStr : ''}`}
        </code>
      </div>
      <div className="flex items-center gap-2 border-t border-line px-3 py-2">
        {act.status === 'awaiting' ? (
          needsConfirm ? (
            <>
              {/* confirm-Modus: das Armieren (Ziel-Name tippen) lebt im Inspector */}
              <button type="button" onClick={onOpen} className="rp-focus flex h-7 items-center gap-1.5 rounded-skin-sm px-3 font-mono text-[11px] font-semibold transition-opacity hover:opacity-90" style={{ background: 'var(--rp-node-crit)', color: 'var(--rp-btn-fg)' }}>
                Review &amp; confirm <span aria-hidden>›</span>
              </button>
              <button type="button" onClick={() => onDecide(act, false)} className="rp-focus h-7 rounded-skin-sm border border-line px-2.5 font-mono text-[11px] text-mid transition-colors hover:bg-hover hover:text-ink">Dismiss</button>
              <span className="ml-auto font-mono text-[9px]" style={{ color: 'var(--rp-node-crit)' }}>{k.glyph} requires typed confirmation</span>
            </>
          ) : (
            <>
              <button type="button" onClick={() => onDecide(act, true)} className="rp-focus flex h-7 items-center gap-1.5 rounded-skin-sm px-3 font-mono text-[11px] font-semibold transition-opacity hover:opacity-90" style={{ background: isDestr ? 'var(--rp-node-crit)' : 'var(--rp-btn-bg)', color: 'var(--rp-btn-fg)' }}>
                Approve &amp; run <span aria-hidden>→</span>
              </button>
              <button type="button" onClick={() => onDecide(act, false)} className="rp-focus h-7 rounded-skin-sm border border-line px-2.5 font-mono text-[11px] text-mid transition-colors hover:bg-hover hover:text-ink">Dismiss</button>
              <span className="ml-auto font-mono text-[9px] text-faint">details in the inspector →</span>
            </>
          )
        ) : act.terminal && act.status === 'succeeded' ? (
          <span className="font-mono text-[11px]" style={{ color: 'var(--rp-tone-green-fg)' }}>● {act.progress || 'done'} — see Runs for the audit trail</span>
        ) : act.terminal && act.status === 'failed' ? (
          <span className="font-mono text-[11px]" style={{ color: 'var(--rp-tone-red-fg)' }}>▲ failed — {act.result || 'see inspector'}</span>
        ) : act.status === 'rejected' ? (
          <span className="font-mono text-[11px] text-faint">○ dismissed</span>
        ) : act.status === 'blocked' ? (
          <span className="font-mono text-[11px] text-faint">⊘ blocked by guardrails</span>
        ) : act.status === 'starting' && act.approvalMode === 'auto' ? (
          <span className="rp-breath font-mono text-[11px] text-mid">◐ auto-approved by guardrails — starting…</span>
        ) : (
          <span className="rp-breath font-mono text-[11px] text-mid">◐ {act.progress || 'running…'}</span>
        )}
      </div>
    </div>
  );
}

/* ── Inline-Fragebox (ask_user, Human-in-the-Loop) ───────────────────────── */
function QuestionCard({ act, onAnswer, onDismiss }: { act: Activity; onAnswer: (a: Activity, v: { value?: string; values?: string[] }, secret?: boolean) => void; onDismiss: () => void }) {
  const q = act.args ?? {};
  const kind = String(q.kind ?? 'text');
  const options = Array.isArray(q.options) ? (q.options as string[]) : [];
  const allowFree = q.allowFreeText === true;
  const isSecret = kind === 'secret';
  const [text, setText] = useState('');
  const [picked, setPicked] = useState<string[]>([]);
  const awaitingQ = act.status === 'awaiting';

  const toggle = (o: string) => {
    if (kind === 'choice') setPicked([o]);
    else setPicked((p) => (p.includes(o) ? p.filter((x) => x !== o) : [...p, o]));
  };
  const canSubmit = kind === 'text' || isSecret ? text.trim() !== '' : picked.length > 0 || (allowFree && text.trim() !== '');
  const submit = () => {
    if (!canSubmit) return;
    if (kind === 'multi') onAnswer(act, { values: picked.length ? picked : [text.trim()] });
    else if (kind === 'choice') onAnswer(act, { value: picked[0] ?? text.trim() });
    else onAnswer(act, { value: text.trim() }, isSecret);
    setText('');
  };

  return (
    <div className="overflow-hidden rounded-skin border border-line" style={{ background: 'var(--rp-bg-inset)', boxShadow: awaitingQ ? '0 0 0 1px color-mix(in oklab, var(--rp-accent) 40%, transparent)' : undefined }}>
      <div className="flex items-center gap-2 border-b border-line px-3 py-2">
        <span className={awaitingQ ? 'rp-breath' : ''} style={{ color: awaitingQ ? 'var(--rp-accent)' : 'var(--rp-ink-faint)' }}>{awaitingQ ? '◆' : '○'}</span>
        <span className="font-mono text-[11px] font-semibold text-ink">Copilot asks</span>
        {isSecret ? <span className="ml-auto shrink-0 rounded-skin-chip border border-line px-1.5 py-px font-mono text-[9px] uppercase tracking-[0.05em] text-muted">🔒 secret — stored server-side only</span> : null}
      </div>
      <div className="space-y-2 px-3 py-2.5">
        <p className="font-mono text-[11.5px] leading-relaxed text-ink">{String(q.question ?? '')}</p>
        {awaitingQ ? (
          <>
            {options.length > 0 ? (
              <div className="space-y-1">
                {options.map((o) => {
                  const on = picked.includes(o);
                  return (
                    <button key={o} type="button" onClick={() => toggle(o)} className={`flex w-full items-center gap-2 rounded-skin-sm border px-2.5 py-1.5 text-left font-mono text-[11px] transition-colors ${on ? 'border-line-strong bg-hover text-ink' : 'border-line text-mid hover:bg-hover'}`}>
                      <span aria-hidden className="shrink-0 text-[10px]" style={{ color: on ? 'var(--rp-accent)' : 'var(--rp-ink-faint)' }}>
                        {kind === 'multi' ? (on ? '▣' : '□') : on ? '◉' : '○'}
                      </span>
                      <span className="min-w-0 flex-1 break-all">{o}</span>
                    </button>
                  );
                })}
              </div>
            ) : null}
            {kind === 'text' || isSecret || allowFree ? (
              <input
                type={isSecret ? 'password' : 'text'}
                value={text}
                onChange={(e) => setText(e.target.value)}
                onKeyDown={(e) => { if (e.key === 'Enter') { e.preventDefault(); submit(); } }}
                spellCheck={false}
                autoComplete={isSecret ? 'off' : undefined}
                placeholder={isSecret ? String(q.secretHint || 'secret value — never shown to the model') : allowFree && options.length ? 'or type your own answer…' : 'your answer…'}
                className="rp-focus h-8 w-full rounded-skin-sm border border-line bg-raised px-2.5 font-mono text-[11.5px] text-ink placeholder:text-faint"
              />
            ) : null}
            <div className="flex items-center gap-2 pt-0.5">
              <button type="button" disabled={!canSubmit} onClick={submit} className="rp-focus flex h-7 items-center gap-1.5 rounded-skin-sm px-3 font-mono text-[11px] font-semibold transition-opacity hover:opacity-90 disabled:opacity-40" style={{ background: 'var(--rp-btn-bg)', color: 'var(--rp-btn-fg)' }}>
                Answer <span aria-hidden>→</span>
              </button>
              <button type="button" onClick={onDismiss} className="rp-focus h-7 rounded-skin-sm border border-line px-2.5 font-mono text-[11px] text-mid transition-colors hover:bg-hover hover:text-ink">Dismiss</button>
              {isSecret ? <span className="ml-auto font-mono text-[9px] text-faint">value goes to the run vault, the model sees a placeholder</span> : null}
            </div>
          </>
        ) : (
          <p className="font-mono text-[10.5px] text-muted">
            {act.status === 'answered' ? <>answered: <span className="text-ink">{act.result}</span></> : act.status === 'rejected' ? '○ dismissed' : act.status}
          </p>
        )}
      </div>
    </div>
  );
}

/* ── Inspector-Panel: „was der Agent sieht" ──────────────────────────────── */
function Inspector({ activity, onDecide, onClose }: { activity: Activity | null; onDecide: (a: Activity, ok: boolean) => void; onClose: () => void }) {
  return (
    <div className="flex h-full flex-col overflow-hidden rounded-skin border border-line bg-raised" style={{ boxShadow: 'var(--rp-rim)' }}>
      <div className="flex shrink-0 items-center gap-2 border-b border-line px-3 py-2.5">
        <span className="rp-micro !text-[10px]">inspector</span>
        {activity ? <span className="truncate font-mono text-[11px] text-ink">{activity.isAction ? (activity.kind ?? '').replace(/_/g, ' ') : toolLabel(activity.name)}</span> : null}
        <button type="button" onClick={onClose} className="ml-auto rounded-skin-chip border border-line px-1.5 py-0.5 font-mono text-[10px] text-mid hover:bg-hover hover:text-ink lg:hidden">✕</button>
      </div>

      {!activity ? (
        <div className="flex flex-1 flex-col items-center justify-center gap-2 px-6 text-center">
          <span style={{ color: 'var(--rp-ink-faint)' }}><Sparkle size={20} /></span>
          <p className="font-mono text-[11px] leading-relaxed text-faint">Every tool the Copilot runs shows up here — logs, metrics, the service map, and the exact result the agent saw.</p>
        </div>
      ) : activity.isAction ? (
        <ActionInspector act={activity} onDecide={onDecide} />
      ) : (
        <ToolInspector act={activity} />
      )}
    </div>
  );
}

function argChips(name: string, args?: Record<string, unknown>): { k: string; v: string }[] {
  if (!args) return [];
  if (name === 'query_metrics') {
    const out = [{ k: 'promql', v: String(args.query ?? '') }];
    if (args.sinceMin) out.push({ k: 'window', v: `${args.sinceMin}m` });
    return out;
  }
  return Object.entries(args)
    .filter(([k, v]) => k !== 'expect' && v != null && v !== '' && typeof v !== 'object')
    .map(([k, v]) => ({ k, v: String(v) }));
}

function ToolInspector({ act }: { act: Activity }) {
  const st = statusTone(act.status);
  const chips = argChips(act.name, act.args);
  const expect = String(act.args?.expect ?? '').trim();
  return (
    <div className="min-h-0 flex-1 space-y-3 overflow-y-auto px-3 py-3">
      <div className="flex items-center gap-1.5 font-mono text-[10px]">
        <span className={st.live ? 'rp-breath' : ''} style={{ color: st.color }}>{st.glyph}</span>
        <span style={{ color: st.color }}>{act.status === 'ok' ? 'read complete' : act.status}</span>
      </div>

      {/* 1 · Ziel & Annahme (die Hypothese des Agenten VOR dem Ergebnis) */}
      {expect ? (
        <div className="rounded-skin-sm border-l-2 py-1.5 pl-2.5 pr-2" style={{ borderColor: 'var(--rp-accent)', background: 'color-mix(in oklab, var(--rp-accent) 7%, transparent)' }}>
          <p className="rp-micro !text-[9.5px] mb-0.5" style={{ color: 'var(--rp-accent)' }}>goal &amp; assumption</p>
          <p className="font-mono text-[11px] leading-relaxed text-mid">{expect}</p>
        </div>
      ) : null}

      {chips.length ? (
        <div className="flex flex-wrap gap-1.5">
          {chips.map((c, i) => (
            <span key={i} className="inline-flex items-center gap-1 rounded-skin-chip border border-line bg-inset px-1.5 py-0.5 font-mono text-[9.5px]">
              <span className="text-faint">{c.k}</span>
              <span className="max-w-[220px] truncate text-mid">{c.v}</span>
            </span>
          ))}
        </div>
      ) : null}

      {/* 2 · Ergebnis (was der Agent sah) */}
      <div>
        <p className="rp-micro !text-[10px] mb-1.5">result</p>
        <ToolResultView name={act.name} result={act.result} status={act.status} />
      </div>

      {/* 3 · Learning (vom LLM formuliert, nachdem es das Ergebnis gelesen hat) */}
      {act.learning && act.learning.trim() ? (
        <div className="rounded-skin-sm border border-line p-2" style={{ background: 'var(--rp-bg-inset)' }}>
          <p className="rp-micro !text-[9.5px] mb-1" style={{ color: 'var(--rp-tone-green-fg)' }}>learning</p>
          <p className="font-mono text-[11px] leading-relaxed text-mid">{act.learning.trim()}</p>
        </div>
      ) : null}
    </div>
  );
}

type Step = { name?: string; status?: string; detail?: string };
function ActionInspector({ act, onDecide }: { act: Activity; onDecide: (a: Activity, ok: boolean) => void }) {
  const k = KLASS[act.klass ?? 'reversible'] ?? KLASS.reversible!;
  const st = statusTone(act.status);
  const isDestr = act.klass === 'destructive';
  const needsConfirm = act.approvalMode === 'confirm';
  const targetName = String(act.args?.targetName ?? '');
  const [armText, setArmText] = useState('');
  const armed = !needsConfirm || (armText.trim() !== '' && armText.trim() === targetName);
  let steps: Step[] = [];
  if (Array.isArray(act.steps)) steps = act.steps as Step[];
  else if (typeof act.steps === 'string') { try { const p = JSON.parse(act.steps); if (Array.isArray(p)) steps = p; } catch { /* none */ } }

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="min-h-0 flex-1 overflow-y-auto px-3 py-3">
        <div className="mb-2 flex items-center gap-1.5 font-mono text-[10px]">
          <span className={st.live ? 'rp-breath' : ''} style={{ color: st.color }}>{st.glyph}</span>
          <span style={{ color: st.color }}>{act.status}</span>
          <span className="ml-auto uppercase tracking-[0.05em]" style={{ color: levelColor(act.klass) }}>{k.glyph} {k.label}</span>
        </div>

        <p className="rp-micro !text-[10px] mb-1">action</p>
        <pre className="mb-3 overflow-x-auto rounded-skin-sm border border-line bg-inset p-2 font-mono text-[10.5px] leading-relaxed text-mid">{pretty(act.args)}</pre>

        {act.source ? (
          <>
            <p className="rp-micro !text-[10px] mb-1" style={{ color: 'var(--rp-node-crit)' }}>script source — read before you arm</p>
            <pre className="mb-3 max-h-[280px] overflow-auto rounded-skin-sm border p-2 font-mono text-[10.5px] leading-relaxed text-ink" style={{ borderColor: 'color-mix(in oklab, var(--rp-node-crit) 40%, var(--rp-line))', background: 'var(--rp-bg-inset)' }}>{act.source}</pre>
          </>
        ) : null}

        {act.reason ? <p className="mb-3 font-mono text-[10.5px] leading-relaxed text-muted">“{act.reason}”</p> : null}

        {act.progress ? (
          <>
            <p className="rp-micro !text-[10px] mb-1">progress</p>
            <div className="mb-3 rounded-skin-sm border border-line bg-inset px-2 py-1.5 font-mono text-[11px] text-ink">{act.progress}</div>
          </>
        ) : null}

        {steps.length > 0 ? (
          <>
            <p className="rp-micro !text-[10px] mb-1">steps</p>
            <ol className="mb-3 space-y-1">
              {steps.map((s, i) => {
                const ss = statusTone(s.status ?? '');
                return (
                  <li key={i} className="flex items-start gap-2 font-mono text-[10.5px]">
                    <span className={ss.live ? 'rp-breath mt-px' : 'mt-px'} style={{ color: ss.color }}>{ss.glyph}</span>
                    <span className="min-w-0"><span className="text-ink">{s.name}</span>{s.detail ? <span className="text-faint"> — {s.detail}</span> : null}</span>
                  </li>
                );
              })}
            </ol>
          </>
        ) : null}

        {act.result && act.terminal ? (
          <>
            <p className="rp-micro !text-[10px] mb-1">result</p>
            <pre className="overflow-x-auto whitespace-pre-wrap rounded-skin-sm border border-line bg-inset p-2 font-mono text-[10.5px] leading-relaxed text-mid">{act.result}</pre>
          </>
        ) : null}
      </div>

      {/* Freigabe — sofortiger full-stop bis wir antworten */}
      {act.status === 'awaiting' ? (
        <div className="shrink-0 space-y-2 border-t px-3 py-3" style={{ borderColor: needsConfirm ? 'color-mix(in oklab, var(--rp-node-crit) 45%, var(--rp-line))' : 'color-mix(in oklab, var(--rp-accent) 40%, var(--rp-line))', background: needsConfirm ? 'color-mix(in oklab, var(--rp-node-crit) 7%, transparent)' : 'color-mix(in oklab, var(--rp-accent) 8%, transparent)' }}>
          {needsConfirm ? (
            <>
              <p className="font-mono text-[10.5px] leading-relaxed" style={{ color: 'var(--rp-node-crit)' }}>
                {k.glyph} {k.label} — this can take workloads down. Type the target name to arm.
              </p>
              <input
                value={armText}
                onChange={(e) => setArmText(e.target.value)}
                spellCheck={false}
                placeholder={`type "${targetName}" to arm`}
                className="rp-focus h-8 w-full rounded-skin-sm border bg-inset px-2.5 font-mono text-[11.5px] text-ink placeholder:text-faint"
                style={{ borderColor: armed ? 'var(--rp-node-crit)' : 'var(--rp-line)' }}
              />
            </>
          ) : (
            <p className="font-mono text-[10.5px] leading-relaxed text-mid">This will run against the live cluster. Nothing executes until you approve.</p>
          )}
          <div className="flex items-center gap-2">
            <button type="button" disabled={!armed} onClick={() => onDecide(act, true)} className="rp-focus flex h-8 flex-1 items-center justify-center gap-1.5 rounded-skin-sm font-mono text-[11.5px] font-semibold transition-opacity hover:opacity-90 disabled:opacity-40" style={{ background: isDestr || needsConfirm ? 'var(--rp-node-crit)' : 'var(--rp-btn-bg)', color: 'var(--rp-btn-fg)' }}>
              {needsConfirm && !armed ? 'Locked — type the name above' : 'Approve & run'} {armed ? <span aria-hidden>→</span> : null}
            </button>
            <button type="button" onClick={() => onDecide(act, false)} className="rp-focus h-8 rounded-skin-sm border border-line px-3 font-mono text-[11.5px] text-mid transition-colors hover:bg-hover hover:text-ink">Dismiss</button>
          </div>
        </div>
      ) : act.status === 'starting' || (!act.terminal && (act.status === 'running' || act.status === 'pending')) ? (
        <div className="shrink-0 border-t border-line px-3 py-2.5 font-mono text-[11px] text-mid" style={{ background: 'var(--rp-bg-inset)' }}>
          <span className="rp-breath">◐</span> executing — everything is locked until it finishes.
        </div>
      ) : null}
    </div>
  );
}
