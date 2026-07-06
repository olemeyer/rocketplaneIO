'use client';

import { useEffect } from 'react';
import { useRouter } from 'next/navigation';
import { EmptyState, Spinner } from '@/components/ui';
import { useMe } from '@/components/app/me-context';
import { useScope } from '@/components/app/scope-context';
import { ConnectClusterButton } from '@/components/app/connect-cluster';

// Landing: der Cluster ist First-Class → wir leiten direkt auf die Service Map des
// ersten (bevorzugt verbundenen) Clusters weiter. Ohne Cluster: Onboarding.
export default function HomePage() {
  const router = useRouter();
  const { currentOrg } = useMe();
  const { clusters, clustersLoading } = useScope();
  const orgId = currentOrg?.id;

  const target =
    clusters.find((c) => c.status === 'connected') ??
    clusters.find((c) => c.status === 'stale') ??
    clusters[0];

  useEffect(() => {
    if (!clustersLoading && target) {
      router.replace(`/clusters/${target.id}`);
    }
  }, [clustersLoading, target, router]);

  if (clustersLoading || target) {
    return (
      <div className="flex h-[calc(100dvh-3rem)] items-center justify-center gap-2 text-muted">
        <Spinner />
        <span className="font-mono text-[12px]">Loading…</span>
      </div>
    );
  }

  // Keine Cluster → Onboarding.
  return (
    <div className="mx-auto max-w-[720px] px-4 py-16 md:px-6">
      <EmptyState
        className="swiss-grid"
        icon={
          <svg width="44" height="44" viewBox="0 0 24 24" fill="none" aria-hidden>
            <path d="M12 2l9 5v10l-9 5-9-5V7l9-5Z" stroke="currentColor" strokeWidth="1.4" strokeLinejoin="miter" />
            <path d="M12 2v20M3 7l9 5 9-5" stroke="currentColor" strokeWidth="1.4" strokeLinejoin="miter" />
          </svg>
        }
        title="Connect your first cluster"
        description="One command, about a minute — then the live service map lights up here."
        action={orgId ? <ConnectClusterButton orgId={orgId} /> : null}
      />
    </div>
  );
}
