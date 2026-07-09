'use client';

import { useState } from 'react';
import { useRouter } from 'next/navigation';
import { cn } from '@/lib/cn';
import { useMe } from './me-context';
import { useScope } from './scope-context';
import { Popover, MenuItem } from './popover';
import { switchOrg } from '@/lib/api/controlplane';
import { ConnectClusterDialog } from './connect-cluster';
import type { Cluster } from '@/lib/api/types';

// scope-collector.tsx — the compact scope entry point for mobile + tablet, where
// the 236px sidebar leaves no room for three inline pills. ONE button shows the
// current scope; tapping it opens a single panel to pick ORGANIZATION, CLUSTER
// and NAMESPACE (org switching stays always available). Desktop keeps the full
// inline breadcrumb (see ScopeSelector).

function dotColor(status: Cluster['status']) {
  return status === 'connected'
    ? 'var(--rp-tone-green-fg)'
    : status === 'stale'
      ? 'var(--rp-yellow)'
      : 'var(--rp-ink-faint)';
}

export function ScopeCollector() {
  const { me, currentOrg, refresh } = useMe();
  const { orgId, clusters, activeCluster, activeClusterId, namespaces, namespace, setNamespace } = useScope();
  const router = useRouter();
  const [busy, setBusy] = useState(false);
  const [connectOpen, setConnectOpen] = useState(false);

  if (!me || !currentOrg) return null;

  async function selectOrg(id: string, close: () => void) {
    if (id === currentOrg!.id) {
      close();
      return;
    }
    setBusy(true);
    try {
      await switchOrg(id);
      await refresh();
      close();
      router.push('/');
      router.refresh();
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <Popover
        contentClassName="w-[280px]"
        trigger={(open) => (
          <span
            className={cn(
              'inline-flex h-8 min-w-0 items-center gap-1.5 rounded-skin-sm border px-1.5 pr-2 text-[12px] transition-colors',
              open
                ? 'border-line-strong bg-hover text-ink'
                : 'border-line text-mid hover:border-line-strong hover:text-ink',
              busy && 'opacity-60',
            )}
          >
            <span className="grid h-5 w-5 shrink-0 place-items-center rounded-skin-sm bg-ink text-[10px] font-bold text-paper">
              {currentOrg.name.slice(0, 1).toUpperCase()}
            </span>
            {activeCluster ? (
              <span className="inline-flex min-w-0 items-center gap-1.5">
                <span className="h-1.5 w-1.5 shrink-0 rounded-full" style={{ background: dotColor(activeCluster.status) }} />
                <span className="min-w-0 max-w-[130px] truncate font-mono text-ink">{activeCluster.name}</span>
              </span>
            ) : (
              <span className="min-w-0 max-w-[130px] truncate font-mono">{currentOrg.name}</span>
            )}
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" aria-hidden className="shrink-0 text-faint">
              <path d="M6 9l6 6 6-6" stroke="currentColor" strokeWidth="2" strokeLinecap="square" strokeLinejoin="miter" />
            </svg>
          </span>
        )}
        content={(close) => (
          <div className="max-h-[70vh] overflow-y-auto">
            {/* Organization */}
            <div className="rp-micro px-2 pb-1 pt-1 !text-[10px]">organization</div>
            {me.orgs.map((org) => (
              <MenuItem key={org.id} active={org.id === currentOrg.id} onClick={() => selectOrg(org.id, close)}>
                <span className="grid h-5 w-5 shrink-0 place-items-center rounded-skin-sm bg-ink text-[10px] font-bold text-paper">
                  {org.name.slice(0, 1).toUpperCase()}
                </span>
                <span className="flex min-w-0 flex-1 flex-col">
                  <span className="truncate text-ink">{org.name}</span>
                  <span className="rp-label truncate font-mono text-[10px] text-faint">{org.isPersonal ? 'Personal' : org.role}</span>
                </span>
              </MenuItem>
            ))}

            <div className="my-1 h-px bg-line" />

            {/* Cluster */}
            <div className="rp-micro px-2 pb-1 pt-1 !text-[10px]">cluster</div>
            {clusters.length === 0 ? (
              <div className="px-2 py-1.5 font-mono text-[11px] text-faint">No clusters yet</div>
            ) : (
              clusters.map((c) => (
                <MenuItem
                  key={c.id}
                  active={c.id === activeClusterId}
                  onClick={() => {
                    if (c.id !== activeClusterId) router.push(`/clusters/${c.id}`);
                    close();
                  }}
                >
                  <span className="h-2 w-2 shrink-0 rounded-full" style={{ background: dotColor(c.status) }} />
                  <span className="min-w-0 flex-1 truncate font-mono text-ink">{c.name}</span>
                </MenuItem>
              ))
            )}
            <MenuItem
              onClick={() => {
                close();
                setConnectOpen(true);
              }}
            >
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" aria-hidden className="text-muted">
                <path d="M12 5v14M5 12h14" stroke="currentColor" strokeWidth="1.8" strokeLinecap="square" />
              </svg>
              <span className="rp-label">Add cluster</span>
            </MenuItem>

            {/* Namespace (only with an active cluster) */}
            {activeCluster ? (
              <>
                <div className="my-1 h-px bg-line" />
                <div className="rp-micro px-2 pb-1 pt-1 !text-[10px]">namespace</div>
                <MenuItem
                  active={!namespace}
                  onClick={() => {
                    setNamespace(null);
                    close();
                  }}
                >
                  <span className="flex-1 text-ink">All namespaces</span>
                </MenuItem>
                {namespaces.map((ns) => (
                  <MenuItem
                    key={ns.id ?? ns.name}
                    active={ns.name === namespace}
                    onClick={() => {
                      setNamespace(ns.name);
                      close();
                    }}
                  >
                    <span className="min-w-0 flex-1 truncate font-mono text-ink">{ns.name}</span>
                  </MenuItem>
                ))}
              </>
            ) : null}
          </div>
        )}
      />
      {connectOpen && orgId ? <ConnectClusterDialog orgId={orgId} onClose={() => setConnectOpen(false)} /> : null}
    </>
  );
}
