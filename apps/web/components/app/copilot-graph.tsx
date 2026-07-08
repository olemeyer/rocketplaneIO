'use client';

// copilot-graph.tsx — der Investigation-Graph: jede Hypothese→Verdict ist ein
// Knoten, Branching über parentId. Gerendert als eingerückter Baum (Tiefe =
// Einrückung, Kanten als Hairlines). RETICLE: Status trägt Farbe+Glyph
// (▲ crit · ◆ warn · ● ok · ○ neutral), Orange NUR für Fokus/Selektion,
// Zahlen mono+tabular.

import { useMemo, useState } from 'react';
import type { GraphNode } from './copilot-store';

type VerdictPayload = {
  verdict?: string; // confirmed | refuted | inconclusive
  summary?: string;
  recommendation?: string;
  root_cause?: string;
  result?: string;
  placeholder?: string;
  value?: string;
  values?: string[];
};

function verdictOf(n: GraphNode): VerdictPayload {
  if (!n.verdict || typeof n.verdict !== 'object') return {};
  return n.verdict as VerdictPayload;
}

// Status-Ton eines Knotens: Verdict-Ausgang schlägt Laufstatus.
function nodeTone(n: GraphNode): { color: string; glyph: string; label: string; live?: boolean } {
  const v = verdictOf(n);
  if (n.status === 'running') return { color: 'var(--rp-ink-muted)', glyph: '◐', label: 'running', live: true };
  if (n.status === 'failed') return { color: 'var(--rp-tone-red-fg)', glyph: '▲', label: 'failed' };
  if (n.status === 'abandoned') return { color: 'var(--rp-ink-faint)', glyph: '○', label: 'abandoned' };
  switch (v.verdict) {
    case 'confirmed':
      return { color: 'var(--rp-tone-red-fg)', glyph: '▲', label: 'confirmed' }; // bestätigte Hypothese = gefundenes Problem
    case 'refuted':
      return { color: 'var(--rp-tone-green-fg)', glyph: '●', label: 'refuted' };
    case 'inconclusive':
      return { color: 'var(--rp-tone-yellow-fg)', glyph: '◆', label: 'inconclusive' };
  }
  if (n.kind === 'conclusion') return { color: 'var(--rp-tone-green-fg)', glyph: '●', label: 'conclusion' };
  return { color: 'var(--rp-ink-muted)', glyph: '●', label: n.status };
}

const KIND_LABEL: Record<string, string> = {
  hypothesis: 'hypothesis',
  action: 'action',
  question: 'question',
  conclusion: 'conclusion',
};

function fmtTokens(n?: number): string {
  if (!n) return '';
  if (n >= 1000) return `${(n / 1000).toFixed(1)}k`;
  return String(n);
}

export function InvestigationGraph({ nodes, onFocus }: { nodes: GraphNode[]; onFocus?: (nodeId: string) => void }) {
  const [openId, setOpenId] = useState<string | null>(null);

  // Baumtiefe je Knoten (parentId-Kette); Reihenfolge bleibt seq (= zeitlich).
  const depth = useMemo(() => {
    const byId = new Map(nodes.map((n) => [n.id, n]));
    const d = new Map<string, number>();
    const calc = (n: GraphNode, guard = 0): number => {
      if (d.has(n.id)) return d.get(n.id)!;
      if (!n.parentId || !byId.has(n.parentId) || guard > 32) {
        d.set(n.id, 0);
        return 0;
      }
      const v = calc(byId.get(n.parentId)!, guard + 1) + 1;
      d.set(n.id, v);
      return v;
    };
    nodes.forEach((n) => calc(n));
    return d;
  }, [nodes]);

  const totalTokens = nodes.reduce((s, n) => s + (n.tokensIn ?? 0) + (n.tokensOut ?? 0), 0);

  if (!nodes.length) return null;
  return (
    <div className="flex flex-col overflow-hidden rounded-skin border border-line bg-raised" style={{ boxShadow: 'var(--rp-rim)' }}>
      <div className="flex shrink-0 items-center gap-2 border-b border-line px-3 py-2">
        <span className="rp-micro !text-[10px] flex-1">investigation graph</span>
        <span className="font-mono text-[9.5px] tabular-nums text-muted">{nodes.length} nodes{totalTokens ? ` · ${fmtTokens(totalTokens)} tok` : ''}</span>
      </div>
      <div className="max-h-[300px] min-h-0 overflow-y-auto p-1.5">
        {nodes.map((n) => {
          const t = nodeTone(n);
          const v = verdictOf(n);
          const d = Math.min(depth.get(n.id) ?? 0, 8);
          const open = openId === n.id;
          const summary = v.summary || v.root_cause || v.result || '';
          return (
            <div key={n.id} style={{ paddingLeft: `${d * 14}px` }}>
              <div className={`group rounded-skin-sm ${open ? 'bg-hover' : 'hover:bg-hover'}`} style={open ? { boxShadow: 'inset 0 0 0 1px var(--rp-line-strong)' } : undefined}>
                <button
                  type="button"
                  onClick={() => { setOpenId(open ? null : n.id); onFocus?.(n.id); }}
                  className="flex w-full items-start gap-1.5 px-1.5 py-1 text-left"
                >
                  {d > 0 ? <span className="mt-px shrink-0 font-mono text-[10px] text-faint" aria-hidden>└</span> : null}
                  <span className={`mt-px shrink-0 ${t.live ? 'rp-breath' : ''}`} style={{ color: t.color }}>{t.glyph}</span>
                  <span className="min-w-0 flex-1">
                    <span className="block truncate font-mono text-[10.5px] leading-snug text-ink">{n.hypothesis || KIND_LABEL[n.kind] || n.kind}</span>
                    <span className="mt-px flex items-center gap-1.5 font-mono text-[9px] text-muted">
                      <span>{KIND_LABEL[n.kind] ?? n.kind}</span>
                      <span style={{ color: t.color }}>{t.label}</span>
                      {typeof n.confidence === 'number' && n.confidence > 0 ? <span className="tabular-nums">{Math.round(n.confidence * 100)}%</span> : null}
                      {(n.tokensIn ?? 0) + (n.tokensOut ?? 0) > 0 ? <span className="tabular-nums">{fmtTokens((n.tokensIn ?? 0) + (n.tokensOut ?? 0))} tok</span> : null}
                    </span>
                  </span>
                </button>
                {open && summary ? (
                  <div className="border-t border-line px-2 py-1.5">
                    <p className="font-mono text-[10px] leading-relaxed text-mid">{summary}</p>
                    {v.recommendation ? (
                      <p className="mt-1 font-mono text-[9.5px] leading-relaxed text-muted">→ {v.recommendation}</p>
                    ) : null}
                  </div>
                ) : null}
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}
