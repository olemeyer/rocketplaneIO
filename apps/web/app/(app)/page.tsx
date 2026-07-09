'use client';

import { useEffect } from 'react';
import { useRouter } from 'next/navigation';
import { Spinner } from '@/components/ui';
import { useMe } from '@/components/app/me-context';
import { useScope } from '@/components/app/scope-context';
import { Onboarding } from '@/components/app/onboarding';

// Landing: the cluster is first-class → we redirect straight to the service map
// of the first (preferably connected) cluster. With no cluster: onboarding.
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

  // No cluster → the onboarding hero (first impression).
  return <Onboarding orgId={orgId} />;
}
