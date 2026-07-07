'use client';

import { useEffect, useMemo, useState } from 'react';
import { useParams } from 'next/navigation';
import { cn } from '@/lib/cn';
import { Spinner } from '@/components/ui';
import { useMe } from '@/components/app/me-context';
import { useScope } from '@/components/app/scope-context';
import { PageHeader } from '@/components/app/page-header';
import { getInventory, type InventoryKind } from '@/lib/api/controlplane';

// Resources — das VOLLSTÄNDIGE K8s-Inventar des Clusters, übersichtlich getabbt:
// Services · Ingress · Config · Batch · Policies · Storage · RBAC. Vom Agenten
// alle 60s gesynct (Secrets: nur Metadaten). Der globale Namespace-Scope filtert.

const POLL_MS = 30_000;

// Tab-Reihenfolge + Anzeigenamen (ein Tab kann mehrere Kinds bündeln).
const TABS: { key: string; label: string; kinds: string[] }[] = [
  { key: 'services', label: 'Services', kinds: ['Service'] },
  { key: 'ingress', label: 'Ingress', kinds: ['Ingress'] },
  { key: 'config', label: 'Config', kinds: ['ConfigMap', 'Secret'] },
  { key: 'batch', label: 'Batch', kinds: ['CronJob', 'Job'] },
  { key: 'autoscaling', label: 'Autoscaling', kinds: ['HorizontalPodAutoscaler', 'PodDisruptionBudget'] },
  { key: 'network', label: 'Network policies', kinds: ['NetworkPolicy'] },
  { key: 'storage', label: 'Volumes', kinds: ['PersistentVolume'] },
  { key: 'rbac', label: 'Accounts & quotas', kinds: ['ServiceAccount', 'ResourceQuota', 'LimitRange'] },
];

type Item = { namespace?: string; name: string; createdAt: string; info: Record<string, string> };

function age(iso: string): string {
  const t = Date.parse(iso);
  if (!t) return '';
  const s = Math.max(0, (Date.now() - t) / 1000);
  if (s < 3600) return `${Math.floor(s / 60)}m`;
  if (s < 86400) return `${Math.floor(s / 3600)}h`;
  return `${Math.floor(s / 86400)}d`;
}

// Info-Spalten eines Kinds: Schlüssel in Erst-Auftrittsreihenfolge, max 4.
function infoColumns(items: Item[]): string[] {
  const cols: string[] = [];
  for (const it of items) {
    for (const k of Object.keys(it.info ?? {})) {
      if (!cols.includes(k)) cols.push(k);
    }
    if (cols.length >= 8) break;
  }
  return cols.slice(0, 4);
}

function KindTable({ kind, items }: { kind: string; items: Item[] }) {
  const cols = infoColumns(items);
  return (
    <div className="overflow-hidden rounded-skin border border-line bg-raised" style={{ boxShadow: 'var(--rp-rim)' }}>
      <div className="flex items-baseline gap-2 border-b border-line px-3 py-2">
        <span className="rp-micro !text-[10px]">{kind}</span>
        <span className="font-mono text-[9.5px] text-faint tnum">{items.length}</span>
      </div>
      <div className="overflow-x-auto">
        <table className="w-full border-collapse font-mono text-[10.5px]">
          <thead>
            <tr className="border-b border-line bg-inset">
              <th className="px-3 py-1.5 text-left font-semibold text-muted">name</th>
              <th className="px-3 py-1.5 text-left font-semibold text-muted">namespace</th>
              {cols.map((c) => (
                <th key={c} className="px-3 py-1.5 text-left font-semibold text-muted">{c}</th>
              ))}
              <th className="px-3 py-1.5 text-right font-semibold text-muted">age</th>
            </tr>
          </thead>
          <tbody>
            {items.map((it, i) => (
              <tr key={`${it.namespace}/${it.name}`} className={i < items.length - 1 ? 'border-b border-line' : ''}>
                <td className="max-w-[260px] truncate px-3 py-1.5 text-ink" title={it.name}>{it.name}</td>
                <td className="px-3 py-1.5 text-muted">{it.namespace || <span className="text-faint">cluster</span>}</td>
                {cols.map((c) => (
                  <td key={c} className="max-w-[340px] truncate px-3 py-1.5 text-mid" title={it.info?.[c] ?? ''}>
                    {it.info?.[c] || <span className="text-faint">—</span>}
                  </td>
                ))}
                <td className="px-3 py-1.5 text-right text-faint tnum">{age(it.createdAt)}</td>
              </tr>
            ))}
            {items.length === 0 ? (
              <tr><td colSpan={cols.length + 3} className="px-3 py-4 text-center text-faint">none in scope</td></tr>
            ) : null}
          </tbody>
        </table>
      </div>
    </div>
  );
}

export default function ResourcesPage() {
  const params = useParams<{ id: string }>();
  const clusterId = params.id;
  const { currentOrg } = useMe();
  const orgId = currentOrg?.id;
  const { namespace } = useScope();

  const [kinds, setKinds] = useState<InventoryKind[] | null>(null);
  const [tab, setTab] = useState('services');
  const [q, setQ] = useState('');

  useEffect(() => {
    if (!orgId) return;
    let alive = true;
    const poll = () => getInventory(orgId, clusterId).then((r) => { if (alive) setKinds(r.kinds); }).catch(() => {});
    void poll();
    const t = setInterval(poll, POLL_MS);
    return () => { alive = false; clearInterval(t); };
  }, [orgId, clusterId]);

  const byKind = useMemo(() => {
    const m = new Map<string, Item[]>();
    for (const k of kinds ?? []) {
      let items: Item[] = [];
      try { items = (typeof k.items === 'string' ? JSON.parse(k.items as unknown as string) : k.items) as Item[]; } catch { /* skip */ }
      m.set(k.kind, items ?? []);
    }
    return m;
  }, [kinds]);

  const needle = q.trim().toLowerCase();
  const itemsFor = (kind: string): Item[] =>
    (byKind.get(kind) ?? []).filter((it) => {
      if (namespace && it.namespace && it.namespace !== namespace) return false;
      if (!needle) return true;
      return `${it.name} ${it.namespace ?? ''} ${Object.values(it.info ?? {}).join(' ')}`.toLowerCase().includes(needle);
    });

  const tabCount = (t: (typeof TABS)[number]) => t.kinds.reduce((n, k) => n + itemsFor(k).length, 0);
  const active = TABS.find((t) => t.key === tab) ?? TABS[0]!;
  const total = (kinds ?? []).reduce((n, k) => n + (byKind.get(k.kind)?.length ?? 0), 0);
  const updated = kinds?.length ? kinds.reduce((max, k) => (k.updatedAt > max ? k.updatedAt : max), '') : '';

  return (
    <div className="flex h-[calc(100dvh-52px)] flex-col px-4 pt-4 sm:px-5">
      <PageHeader kicker="infrastructure / inventory" title="Resources" meta={kinds ? `${total} objects · synced ${updated ? age(updated) + ' ago' : '—'}` : undefined}>
        <input
          value={q}
          onChange={(e) => setQ(e.target.value)}
          spellCheck={false}
          placeholder="search name / info…"
          className="rp-focus h-8 w-56 rounded-skin-sm border border-line bg-inset px-2.5 font-mono text-[11px] text-ink placeholder:text-faint"
        />
      </PageHeader>

      <div className="mt-3 flex gap-1 overflow-x-auto pb-1">
        {TABS.map((t) => {
          const on = tab === t.key;
          return (
            <button
              key={t.key}
              type="button"
              onClick={() => setTab(t.key)}
              className={cn('rp-focus flex shrink-0 items-center gap-1.5 rounded-skin-sm px-2.5 py-1.5 font-mono text-[11px] transition-colors', on ? 'bg-hover text-ink' : 'text-muted hover:text-ink')}
              style={on ? { boxShadow: 'inset 0 0 0 1px var(--rp-line-strong)' } : undefined}
            >
              {t.label}
              <span className="text-[9.5px] text-faint tnum">{kinds ? tabCount(t) : '…'}</span>
            </button>
          );
        })}
      </div>

      <div className="mt-2 min-h-0 flex-1 space-y-3 overflow-y-auto pb-3 pr-1">
        {kinds === null ? (
          <div className="flex h-24 items-center justify-center gap-2 text-muted"><Spinner /> <span className="font-mono text-[11px]">loading inventory…</span></div>
        ) : kinds.length === 0 ? (
          <div className="rounded-skin border border-dashed p-6 text-center font-mono text-[11px] leading-relaxed text-muted" style={{ borderColor: 'var(--rp-line-strong)' }}>
            No inventory yet — the agent syncs it every 60s. If this persists, update the agent (it needs the inventory collector and its RBAC).
          </div>
        ) : (
          active.kinds.map((k) => <KindTable key={k} kind={k} items={itemsFor(k)} />)
        )}
      </div>
    </div>
  );
}
