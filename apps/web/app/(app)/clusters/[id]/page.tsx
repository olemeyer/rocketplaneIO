'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import Link from 'next/link';
import { useParams } from 'next/navigation';
import {
  Button,
  EmptyState,
  Panel,
  PanelHeader,
  Skeleton,
  Spinner,
  StatusBadge,
} from '@/components/ui';
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

  // Connected → Service Map füllt den Content-Bereich randlos.
  if (connected && orgId) {
    return (
      <div className="h-[calc(100dvh-3rem)]">
        <ServiceMapCanvas orgId={orgId} clusterId={clusterId} namespace={namespace} fill />
      </div>
    );
  }

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
      ) : error && !detail ? (
        <div className="mt-4">
          <EmptyState
            title="Cluster not found"
            description={error}
            action={
              <Link href="/">
                <Button variant="default">Back to clusters</Button>
              </Link>
            }
          />
        </div>
      ) : detail ? (
        <>
          <div className="mt-4 flex flex-wrap items-center justify-between gap-3 border-b border-line pb-4">
            <div className="flex items-center gap-3">
              <h1 className="rp-headline text-[24px] font-extrabold tracking-tightest text-ink">
                {detail.cluster.name}
              </h1>
              <StatusBadge status={detail.cluster.status} />
            </div>
          </div>
          <PendingView
            install={install}
            onRegenerate={regenerate}
            regenBusy={regenBusy}
          />
        </>
      ) : null}
    </div>
  );
}

/* ── Pending: warten auf Enroll ─────────────────────────────────────────── */

const STEPS = [
  'Copy the install command.',
  'Run it against your cluster (uses your current kubectl context).',
  'The agent reads the kube-system UID and enrolls — no inbound access needed.',
];

function PendingView({
  install,
  onRegenerate,
  regenBusy,
}: {
  install: { command: string; commands?: InstallCommands } | null;
  onRegenerate: () => void;
  regenBusy: boolean;
}) {
  return (
    <div className="mt-5 grid gap-4 lg:grid-cols-[1.5fr_1fr]">
      <Panel>
        <PanelHeader index="01" title="Install the agent" subtitle="One command, outbound-only" />
        <div className="space-y-5 p-4">
          <ol className="space-y-3">
            {STEPS.map((s, i) => (
              <li key={i} className="flex gap-3">
                <span className="font-mono text-[11px] font-bold text-accent">
                  {String(i + 1).padStart(2, '0')}
                </span>
                <span className="text-[12.5px] leading-relaxed text-mid">{s}</span>
              </li>
            ))}
          </ol>

          {install ? (
            <InstallPicker commands={install.commands} fallback={install.command} />
          ) : (
            <div className="rounded-skin border border-line bg-inset px-4 py-6 text-center">
              <p className="font-mono text-[12px] text-muted">
                The enroll token isn’t available on this page anymore.
              </p>
              <Button variant="default" onClick={onRegenerate} disabled={regenBusy} className="mt-3">
                {regenBusy ? <Spinner /> : null}
                Generate install command
              </Button>
            </div>
          )}
        </div>
      </Panel>

      <Panel>
        <PanelHeader index="02" title="Status" subtitle="Waiting for enrollment" />
        <div className="flex flex-col items-center justify-center gap-4 px-4 py-12 text-center">
          <div className="flex h-16 w-16 items-center justify-center rounded-skin border border-line bg-inset">
            <Spinner />
          </div>
          <div className="rp-label text-[13px] text-ink">Waiting for connection…</div>
          <StatusBadge status="pending" />
          <p className="max-w-[220px] font-mono text-[11px] leading-relaxed text-muted">
            This page updates automatically the moment the agent enrolls.
          </p>
        </div>
      </Panel>
    </div>
  );
}
