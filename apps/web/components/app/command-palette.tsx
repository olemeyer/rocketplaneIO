'use client';

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { useRouter } from 'next/navigation';
import { cn } from '@/lib/cn';
import { getServiceMap } from '@/lib/api/controlplane';
import type { MapNode } from '@/lib/api/types';
import { useMe } from './me-context';
import { useScope } from './scope-context';

// command-palette.tsx — das ⌘K-Kronjuwel (RETICLE): Prompt-Glyph › + Block-Caret,
// Mono-Ergebnisse, Sektions-Micro-Labels, Signal-Keyline an der aktiven Zeile.
// Springt zu Views des aktiven Clusters, wechselt Cluster und setzt den
// Namespace-Scope — keyboard-first, fuzzy, sofort.

type Item = {
  section: string;
  label: string;
  hint?: string;
  run: () => void;
};

function fuzzyMatch(query: string, target: string): boolean {
  const q = query.toLowerCase();
  const t = target.toLowerCase();
  let qi = 0;
  for (let ti = 0; ti < t.length && qi < q.length; ti++) {
    if (t[ti] === q[qi]) qi++;
  }
  return qi === q.length;
}

export function CommandPalette() {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');
  const [index, setIndex] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const router = useRouter();
  const { currentOrg } = useMe();
  const { clusters, activeClusterId, namespaces, setNamespace } = useScope();

  // Workloads on-demand laden (beim Öffnen) — macht die Palette vom View-Router
  // zum Investigations-Einstieg: „gateway" tippen → direkt in dessen Logs.
  const [workloads, setWorkloads] = useState<MapNode[]>([]);
  useEffect(() => {
    if (!open || !currentOrg?.id || !activeClusterId) return;
    getServiceMap(currentOrg.id, activeClusterId)
      .then((m) => setWorkloads(m.nodes))
      .catch(() => {});
  }, [open, currentOrg?.id, activeClusterId]);

  // Global: ⌘K / Ctrl+K öffnet, Esc schließt.
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault();
        setOpen((o) => !o);
        setQuery('');
        setIndex(0);
      } else if (e.key === 'Escape') {
        setOpen(false);
      }
    }
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, []);

  useEffect(() => {
    if (open) inputRef.current?.focus();
  }, [open]);

  const close = useCallback(() => setOpen(false), []);

  const items = useMemo<Item[]>(() => {
    const out: Item[] = [];
    if (activeClusterId) {
      out.push(
        { section: 'Views', label: 'Service Map', hint: 'topology & flows', run: () => router.push(`/clusters/${activeClusterId}`) },
        { section: 'Views', label: 'Logs', hint: 'search & live tail', run: () => router.push(`/clusters/${activeClusterId}/logs`) },
        { section: 'Views', label: 'Traces', hint: 'eBPF spans & RED', run: () => router.push(`/clusters/${activeClusterId}/traces`) },
      );
      for (const w of workloads) {
        out.push({
          section: 'Workloads',
          label: w.name,
          hint: `${w.namespace} · logs ↵`,
          run: () =>
            router.push(
              `/clusters/${activeClusterId}/logs?workload=${encodeURIComponent(w.name)}`,
            ),
        });
      }
      out.push({ section: 'Namespace', label: 'All namespaces', hint: 'clear scope', run: () => setNamespace(null) });
      for (const ns of namespaces) {
        out.push({ section: 'Namespace', label: ns.name, hint: 'scope to namespace', run: () => setNamespace(ns.name) });
      }
    }
    for (const c of clusters) {
      out.push({
        section: 'Clusters',
        label: c.name,
        hint: c.status,
        run: () => router.push(`/clusters/${c.id}`),
      });
    }
    return out;
  }, [activeClusterId, clusters, namespaces, workloads, router, setNamespace]);

  const filtered = useMemo(
    () => (query ? items.filter((i) => fuzzyMatch(query, i.label)) : items),
    [items, query],
  );

  useEffect(() => setIndex(0), [query]);

  function onInputKey(e: React.KeyboardEvent) {
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      setIndex((i) => Math.min(i + 1, filtered.length - 1));
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      setIndex((i) => Math.max(i - 1, 0));
    } else if (e.key === 'Enter' && filtered[index]) {
      e.preventDefault();
      filtered[index].run();
      close();
    }
  }

  if (!open) return null;

  let lastSection = '';

  return createPortal(
    <div className="fixed inset-0 z-[60] flex items-start justify-center px-4 pt-[14vh] sm:pt-[18vh]" role="dialog" aria-modal="true" aria-label="Command palette">
      <button
        type="button"
        aria-label="Close"
        onClick={close}
        className="absolute inset-0 cursor-default"
        style={{ backgroundColor: 'var(--rp-scrim)' }}
      />
      <div
        className="reveal relative w-full max-w-[560px] overflow-hidden rounded-skin-lg border border-line bg-overlay"
        style={{ boxShadow: 'var(--rp-rim), var(--rp-shadow-pop)' }}
      >
        {/* Prompt-Zeile: › + Block-Caret */}
        <div className="flex h-12 items-center gap-2.5 border-b border-line px-4">
          <span className="font-mono text-[15px]" style={{ color: 'var(--rp-accent)' }} aria-hidden>
            ›
          </span>
          <input
            ref={inputRef}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={onInputKey}
            placeholder="jump to view, workload, namespace, cluster…"
            className="h-full w-full bg-transparent font-mono text-[13px] text-ink outline-none placeholder:text-faint"
          />
          <kbd className="rounded-skin-chip border border-line px-1.5 py-0.5 font-mono text-[9.5px] text-faint">
            esc
          </kbd>
        </div>

        <div className="max-h-[46vh] overflow-y-auto p-1.5">
          {filtered.length === 0 ? (
            <div className="px-3 py-6 text-center font-mono text-[12px] text-faint">
              nothing matches „{query}"
            </div>
          ) : (
            filtered.map((item, i) => {
              const sectionHeader = item.section !== lastSection ? item.section : null;
              lastSection = item.section;
              const active = i === index;
              return (
                <div key={`${item.section}-${item.label}`}>
                  {sectionHeader ? (
                    <div className="rp-micro px-2.5 pb-1 pt-2.5 !text-[10px]">{sectionHeader}</div>
                  ) : null}
                  <button
                    type="button"
                    onClick={() => {
                      item.run();
                      close();
                    }}
                    onMouseEnter={() => setIndex(i)}
                    className={cn(
                      'relative flex w-full items-center gap-2.5 rounded-skin-sm px-2.5 py-2 text-left font-mono text-[12.5px] transition-colors',
                      active ? 'bg-hover text-ink' : 'text-mid',
                    )}
                  >
                    {active ? (
                      <span
                        className="absolute left-0 top-1/2 h-3.5 w-[2px] -translate-y-1/2 rounded-full"
                        style={{ background: 'var(--rp-accent)' }}
                        aria-hidden
                      />
                    ) : null}
                    <span className="min-w-0 flex-1 truncate">{item.label}</span>
                    {item.hint ? (
                      <span className="shrink-0 font-mono text-[10px] text-faint">{item.hint}</span>
                    ) : null}
                    {active ? (
                      <kbd className="shrink-0 rounded-skin-chip border border-line px-1 py-0.5 font-mono text-[9px] text-faint">
                        ↵
                      </kbd>
                    ) : null}
                  </button>
                </div>
              );
            })
          )}
        </div>
      </div>
    </div>,
    document.body,
  );
}

// Trigger für die Topbar: ›-Prompt + kbd-Chip. Öffnet via synthetischem ⌘K.
export function CommandTrigger() {
  return (
    <button
      type="button"
      onClick={() => {
        document.dispatchEvent(new KeyboardEvent('keydown', { key: 'k', metaKey: true }));
      }}
      className="rp-focus hidden h-8 items-center gap-2 rounded-skin-sm border border-line px-2.5 font-mono text-[11px] text-muted transition-colors hover:border-line-strong hover:text-ink md:inline-flex"
    >
      <span style={{ color: 'var(--rp-accent)' }} aria-hidden>
        ›
      </span>
      <span>search</span>
      <kbd className="rounded-skin-chip border border-line px-1 py-0.5 text-[9px] text-faint">⌘K</kbd>
    </button>
  );
}
