'use client';

import { useState } from 'react';
import { useRouter } from 'next/navigation';
import { cn } from '@/lib/cn';
import { useScope } from './scope-context';
import { Popover, MenuItem } from './popover';
import { OrgSwitcher } from './org-switcher';
import { ConnectClusterDialog } from './connect-cluster';
import type { Cluster } from '@/lib/api/types';

// scope-selector.tsx — the central scope breadcrumb at the top: Org / Cluster /
// Namespace. Cluster + Namespace are first-class selectors; "Add cluster" and
// "New org" (in the OrgSwitcher) live inside their dropdowns so new resources
// show up in the selector immediately.

function Sep() {
  return <span className="px-0.5 text-[13px] text-faint" aria-hidden>/</span>;
}

// Status-Glyph: In der TOPBAR-Pill ruhig-monochrom (Ambient-Chrome dimmt), im
// DROPDOWN semantisch differenziert (Form + Farbe, colorblind-sicher):
// connected = ● green, pending = ○ hollow, stale = ◆ gold.
function StatusDot({ status, detail }: { status: Cluster['status']; detail?: boolean }) {
  if (detail) {
    const map: Record<Cluster['status'], { glyph: string; color: string }> = {
      connected: { glyph: '●', color: 'var(--rp-tone-green-fg)' },
      pending: { glyph: '○', color: 'var(--rp-ink-faint)' },
      stale: { glyph: '◆', color: 'var(--rp-yellow)' },
    };
    const m = map[status];
    return (
      <span
        className="flex h-4 w-4 shrink-0 items-center justify-center font-mono text-[10px] leading-none"
        style={{ color: m.color }}
        aria-hidden
      >
        {m.glyph}
      </span>
    );
  }
  const color =
    status === 'connected'
      ? 'var(--rp-ink-muted)'
      : status === 'stale'
        ? 'var(--rp-yellow)'
        : 'var(--rp-ink-faint)';
  return (
    <span className="flex h-4 w-4 shrink-0 items-center justify-center" aria-hidden>
      <span className="inline-flex h-1.5 w-1.5 rounded-full" style={{ background: color }} />
    </span>
  );
}

function pillClass(open: boolean, muted?: boolean) {
  return cn(
    'inline-flex h-8 items-center gap-2 rounded-skin-sm border px-2 text-[12px] transition-colors',
    open
      ? 'border-line-strong bg-hover text-ink'
      : muted
        ? 'border-line text-muted hover:border-line-strong hover:text-ink'
        : 'border-line text-mid hover:border-line-strong hover:text-ink',
  );
}

function Chevron() {
  return (
    <svg width="12" height="12" viewBox="0 0 24 24" fill="none" aria-hidden className="text-faint">
      <path d="M6 9l6 6 6-6" stroke="currentColor" strokeWidth="2" strokeLinecap="square" strokeLinejoin="miter" />
    </svg>
  );
}

function ClusterPill() {
  const { clusters, activeCluster, orgId } = useScope();
  const router = useRouter();
  const [connectOpen, setConnectOpen] = useState(false);

  return (
    <>
      <Popover
        trigger={(open) => (
          <span className={pillClass(open, !activeCluster)}>
            {activeCluster ? (
              <StatusDot status={activeCluster.status} />
            ) : (
              <span className="h-2 w-2 rounded-full bg-line-strong" />
            )}
            <span className="max-w-[160px] truncate font-mono">
              {activeCluster?.name ?? 'Select cluster'}
            </span>
            <Chevron />
          </span>
        )}
        contentClassName="w-[280px]"
        content={(close) => (
          <div>
            <div className="rp-micro px-2 pb-1.5 pt-1 !text-[10px]">Clusters</div>
            <div className="max-h-72 overflow-y-auto">
              {clusters.length === 0 ? (
                <div className="px-2 py-2 font-mono text-[11px] text-faint">No clusters yet</div>
              ) : (
                clusters.map((c) => {
                  const active = c.id === activeCluster?.id;
                  return (
                    <MenuItem
                      key={c.id}
                      active={active}
                      onClick={() => {
                        close();
                        if (!active) router.push(`/clusters/${c.id}`);
                      }}
                    >
                      <StatusDot status={c.status} detail />
                      <span className="flex min-w-0 flex-1 flex-col">
                        <span className="truncate text-ink">{c.name}</span>
                        <span className="rp-label truncate font-mono text-[10px] text-faint">
                          {c.status}
                        </span>
                      </span>
                      
                    </MenuItem>
                  );
                })
              )}
            </div>
            <div className="my-1 h-px bg-line" />
            <MenuItem
              onClick={() => {
                close();
                setConnectOpen(true);
              }}
            >
              <span className="flex h-4 w-4 items-center justify-center text-muted">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" aria-hidden>
                  <path d="M12 5v14M5 12h14" stroke="currentColor" strokeWidth="1.8" strokeLinecap="square" />
                </svg>
              </span>
              <span className="rp-label">Add cluster</span>
            </MenuItem>
          </div>
        )}
      />
      {connectOpen && orgId ? (
        <ConnectClusterDialog orgId={orgId} onClose={() => setConnectOpen(false)} />
      ) : null}
    </>
  );
}

function NamespacePill() {
  const { activeCluster, namespaces, namespace, setNamespace } = useScope();
  if (!activeCluster) return null;

  const label = namespace ?? 'All namespaces';
  return (
    <>
      <Sep />
      <Popover
      trigger={(open) => (
        <span className={pillClass(open, !namespace)}>
          <svg width="13" height="13" viewBox="0 0 16 16" fill="none" aria-hidden className="text-faint">
            <rect x="2" y="2" width="12" height="12" rx="2" stroke="currentColor" strokeWidth="1.3" />
            <path d="M2 6h12M6 2v12" stroke="currentColor" strokeWidth="1.3" />
          </svg>
          <span className="max-w-[150px] truncate font-mono">{label}</span>
          <Chevron />
        </span>
      )}
      contentClassName="w-[240px]"
      content={(close) => (
        <div>
          <div className="rp-micro px-2 pb-1.5 pt-1 !text-[10px]">Namespace</div>
          <div className="max-h-72 overflow-y-auto">
            <MenuItem
              active={!namespace}
              onClick={() => {
                setNamespace(null);
                close();
              }}
            >
              <span className="flex-1 text-ink">All namespaces</span>
              
            </MenuItem>
            {namespaces
              .slice()
              .sort((a, b) => a.name.localeCompare(b.name))
              .map((ns) => {
                const active = ns.name === namespace;
                return (
                  <MenuItem
                    key={ns.id ?? ns.name}
                    active={active}
                    onClick={() => {
                      setNamespace(ns.name);
                      close();
                    }}
                  >
                    <span className="min-w-0 flex-1 truncate font-mono text-ink">{ns.name}</span>
                    
                  </MenuItem>
                );
              })}
          </div>
        </div>
      )}
      />
    </>
  );
}

export function ScopeSelector() {
  return (
    <div className="flex min-w-0 items-center gap-1">
      {/* Space budget: at md+ the 236px sidebar appears, leaving the top bar only
          ~600px on a tablet. So we reveal scope progressively — CLUSTER always,
          ORG from sm+, and the NAMESPACE pill only at lg+ (with the search field);
          on phones org+namespace move to the nav drawer. This keeps the Copilot
          button uncrushed at every width. */}
      <span className="hidden sm:contents">
        <OrgSwitcher />
        <Sep />
      </span>
      <ClusterPill />
      <span className="hidden lg:contents">
        <NamespacePill />
      </span>
    </div>
  );
}
