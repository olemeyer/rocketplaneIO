'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import Link from 'next/link';
import { useParams } from 'next/navigation';
import { Button, EmptyState, Skeleton, Spinner, StatusBadge } from '@/components/ui';
import { useMe } from '@/components/app/me-context';
import { useScope } from '@/components/app/scope-context';
import { InstallPicker } from '@/components/app/copy-box';
import { installCommandKey } from '@/components/app/connect-cluster';
import type { InstallCommands } from '@/lib/api/types';
import { ServiceMapCanvas } from '@/components/servicemap/service-map';
import { getCluster, reconnectCluster } from '@/lib/api/controlplane';
import { ApiError } from '@/lib/api/client';
import type { ClusterDetail } from '@/lib/api/types';

// Die Cluster-Seite ist die Landing: connected → Service Map (full-bleed, auf den
// aktiven Namespace gescopt). Pending → der Connect-Flow, bis der Agent enrollt.
export default function ClusterPage() {
  const params = useParams<{ id: string }>();
  const clusterId = params.id;
  const { currentOrg, loading: meLoading } = useMe();
  const { namespace } = useScope();
  const orgId = currentOrg?.id;

  const [detail, setDetail] = useState<ClusterDetail | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [install, setInstall] = useState<{ command: string; commands?: InstallCommands } | null>(null);
  const [regenBusy, setRegenBusy] = useState(false);
  const firstLoad = useRef(true);

  useEffect(() => {
    try {
      const stored = sessionStorage.getItem(installCommandKey(clusterId));
      if (stored) {
        try {
          const p = JSON.parse(stored);
          setInstall(typeof p === 'string' ? { command: p } : p);
        } catch {
          setInstall({ command: stored });
        }
      }
    } catch {
      /* ignore */
    }
  }, [clusterId]);

  const load = useCallback(async () => {
    if (!orgId) return;
    try {
      const next = await getCluster(orgId, clusterId);
      setDetail(next);
      setError(null);
    } catch (err) {
      if (firstLoad.current) setError(err instanceof ApiError ? err.message : 'Failed to load cluster');
    } finally {
      firstLoad.current = false;
    }
  }, [orgId, clusterId]);

  const status = detail?.cluster.status;

  useEffect(() => {
    firstLoad.current = true;
    setDetail(null);
    setError(null);
    void load();
  }, [load]);

  useEffect(() => {
    const id = window.setInterval(load, status === 'connected' ? 8000 : 3000);
    return () => window.clearInterval(id);
  }, [load, status]);

  async function regenerate() {
    if (!orgId || regenBusy) return;
    setRegenBusy(true);
    try {
      const res = await reconnectCluster(orgId, clusterId);
      const next = { command: res.installCommand, commands: res.installCommands };
      setInstall(next);
      try {
        sessionStorage.setItem(installCommandKey(clusterId), JSON.stringify(next));
      } catch {
        /* ignore */
      }
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Could not regenerate token');
    } finally {
      setRegenBusy(false);
    }
  }

  const loading = (meLoading && !currentOrg) || (detail === null && !error);
  const connected = status === 'connected' || status === 'stale';

  // Connected → the service map fills the content area edge-to-edge.
  if (connected && orgId) {
    return (
      <div className="h-[calc(100dvh-3rem)]">
        <ServiceMapCanvas orgId={orgId} clusterId={clusterId} namespace={namespace} fill />
      </div>
    );
  }

  // Pending → the SAME (empty) service map is the canvas, with the connect
  // overlay floating over it. The moment the agent enrolls, the poll flips
  // `status` to connected and the real nodes draw themselves in behind — no
  // separate "install" page, no fake topology.
  if (detail && orgId) {
    return (
      <div className="relative h-[calc(100dvh-3rem)]">
        <ServiceMapCanvas orgId={orgId} clusterId={clusterId} namespace={namespace} fill />
        <ConnectOverlay
          clusterName={detail.cluster.name}
          install={install}
          onRegenerate={regenerate}
          regenBusy={regenBusy}
        />
      </div>
    );
  }

  // Loading / not-found.
  return (
    <div className="mx-auto max-w-[1120px] px-4 py-6 md:px-6 md:py-8">
      <Link
        href="/"
        className="rp-label inline-flex items-center gap-1.5 text-[11px] text-muted transition-colors hover:text-ink"
      >
        <span aria-hidden>←</span> Clusters
      </Link>
      {loading ? (
        <div className="mt-4 space-y-4">
          <Skeleton className="h-9 w-64" />
          <Skeleton className="h-40 w-full" />
        </div>
      ) : (
        <div className="mt-4">
          <EmptyState
            title="Cluster not found"
            description={error ?? 'This cluster does not exist.'}
            action={
              <Link href="/">
                <Button variant="default">Back to clusters</Button>
              </Link>
            }
          />
        </div>
      )}
    </div>
  );
}

/* ── Connect overlay — floats over the empty map while the agent enrolls ───── */

const STEPS = [
  'Copy the install command.',
  'Run it against your cluster (uses your current kube-context).',
  'The agent reads the kube-system UID and enrolls — no inbound access needed.',
];

function ConnectOverlay({
  clusterName,
  install,
  onRegenerate,
  regenBusy,
}: {
  clusterName: string;
  install: { command: string; commands?: InstallCommands } | null;
  onRegenerate: () => void;
  regenBusy: boolean;
}) {
  return (
    <div className="pointer-events-none absolute inset-0 flex items-center justify-center px-4 py-6">
      {/* Soft scrim so the empty map grid stays visible behind the card. */}
      <div className="pointer-events-none absolute inset-0" style={{ background: 'radial-gradient(60% 60% at 50% 45%, color-mix(in oklab, var(--rp-base) 72%, transparent), transparent)' }} aria-hidden />
      <div
        className="reveal pointer-events-auto relative w-full max-w-[540px] overflow-hidden rounded-skin border border-line bg-raised"
        style={{ boxShadow: 'var(--rp-rim), var(--rp-shadow-pop)' }}
      >
        <div className="flex items-center justify-between gap-3 border-b border-line px-4 py-3">
          <div className="min-w-0">
            <div className="rp-micro !text-[10px] text-faint">connecting</div>
            <h1 className="mt-0.5 truncate font-display text-[18px] font-bold tracking-tightest text-ink">{clusterName}</h1>
          </div>
          <StatusBadge status="pending" />
        </div>

        <div className="space-y-4 p-4">
          <ol className="space-y-2">
            {STEPS.map((s, i) => (
              <li key={i} className="flex gap-2.5">
                <span className="font-mono text-[10.5px] font-bold text-accent">{String(i + 1).padStart(2, '0')}</span>
                <span className="text-[12px] leading-relaxed text-mid">{s}</span>
              </li>
            ))}
          </ol>

          {install ? (
            <InstallPicker commands={install.commands} fallback={install.command} />
          ) : (
            <div className="rounded-skin border border-line bg-inset px-4 py-5 text-center">
              <p className="font-mono text-[11.5px] text-muted">The enroll token isn’t on this page anymore.</p>
              <Button variant="default" onClick={onRegenerate} disabled={regenBusy} className="mt-3">
                {regenBusy ? <Spinner /> : null}
                Generate install command
              </Button>
            </div>
          )}

          <div className="flex items-center gap-2.5 rounded-skin-sm border border-line bg-inset px-3 py-2.5">
            <span className="relative flex h-2.5 w-2.5 items-center justify-center">
              <span className="absolute h-2.5 w-2.5 rounded-full" style={{ background: 'var(--rp-accent)', animation: 'onboard-ping 1.8s ease-out infinite' }} />
              <span className="h-2 w-2 rounded-full" style={{ background: 'var(--rp-accent)' }} />
            </span>
            <span className="font-mono text-[11.5px] leading-relaxed text-mid">
              Waiting for the agent — your workloads draw themselves onto this map the moment it enrolls.
            </span>
          </div>
        </div>
      </div>
    </div>
  );
}
