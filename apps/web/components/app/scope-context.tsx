'use client';

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react';
import { usePathname } from 'next/navigation';
import { getCluster, listClusters } from '@/lib/api/controlplane';
import type { Cluster, Namespace } from '@/lib/api/types';
import { useMe } from './me-context';

// scope-context.tsx hält den aktiven Beobachtungs-Scope org > cluster > namespace.
// Cluster + Namespace sind First-Class: der aktive Cluster kommt aus der URL
// (/clusters/{id}) — deep-linkbar —, der Namespace ist ein Filter-State (null =
// „All namespaces"). Der Kontext lädt die Cluster-Liste (für den Selektor) und die
// Namespaces des aktiven Clusters.

export const ALL_NAMESPACES = null;

type ScopeValue = {
  orgId: string | null;
  clusters: Cluster[];
  clustersLoading: boolean;
  activeClusterId: string | null;
  activeCluster: Cluster | null;
  namespaces: Namespace[];
  namespace: string | null; // null = alle Namespaces
  setNamespace: (ns: string | null) => void;
  reloadClusters: () => Promise<void>;
};

const ScopeContext = createContext<ScopeValue | null>(null);

function clusterIdFromPath(pathname: string): string | null {
  const m = pathname.match(/^\/clusters\/([0-9a-fA-F-]{8,})/);
  return m?.[1] ?? null;
}

export function ScopeProvider({ children }: { children: ReactNode }) {
  const { currentOrg } = useMe();
  const orgId = currentOrg?.id ?? null;
  const pathname = usePathname();
  const activeClusterId = clusterIdFromPath(pathname);

  const [clusters, setClusters] = useState<Cluster[]>([]);
  const [clustersLoading, setClustersLoading] = useState(true);
  const [namespaces, setNamespaces] = useState<Namespace[]>([]);
  const [namespace, setNamespace] = useState<string | null>(null);
  const lastCluster = useRef<string | null>(null);

  const reloadClusters = useCallback(async () => {
    if (!orgId) return;
    try {
      const list = await listClusters(orgId);
      setClusters(list);
    } catch {
      /* still gehalten — Selektor bleibt bedienbar */
    } finally {
      setClustersLoading(false);
    }
  }, [orgId]);

  // Cluster-Liste laden + leise pollen (Status/last-seen aktuell halten).
  useEffect(() => {
    setClustersLoading(true);
    setClusters([]);
    void reloadClusters();
    const id = window.setInterval(reloadClusters, 6000);
    return () => window.clearInterval(id);
  }, [reloadClusters]);

  // Namespaces des aktiven Clusters laden; Namespace-Filter bei Cluster-Wechsel reset.
  useEffect(() => {
    if (activeClusterId !== lastCluster.current) {
      lastCluster.current = activeClusterId;
      setNamespace(null);
      setNamespaces([]);
    }
    if (!orgId || !activeClusterId) return;
    let alive = true;
    const load = () =>
      getCluster(orgId, activeClusterId)
        .then((d) => alive && setNamespaces(d.namespaces ?? []))
        .catch(() => {});
    void load();
    const id = window.setInterval(load, 8000);
    return () => {
      alive = false;
      window.clearInterval(id);
    };
  }, [orgId, activeClusterId]);

  const activeCluster = useMemo(
    () => clusters.find((c) => c.id === activeClusterId) ?? null,
    [clusters, activeClusterId],
  );

  const value: ScopeValue = {
    orgId,
    clusters,
    clustersLoading,
    activeClusterId,
    activeCluster,
    namespaces,
    namespace,
    setNamespace,
    reloadClusters,
  };

  return <ScopeContext.Provider value={value}>{children}</ScopeContext.Provider>;
}

export function useScope(): ScopeValue {
  const ctx = useContext(ScopeContext);
  if (!ctx) throw new Error('useScope must be used within <ScopeProvider>');
  return ctx;
}
