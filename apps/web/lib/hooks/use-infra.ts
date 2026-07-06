'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import { getInfra } from '@/lib/api/controlplane';
import type { InfraNode, InfraPVC } from '@/lib/api/types';
import { useClusterEvents } from './use-cluster-events';

// use-infra — Nodes + PVCs, live: der Agent liefert beides im Topologie-Push,
// also treibt dasselbe SSE-topology-Signal den Refetch. Poll nur als Fallback.

export function useInfra(orgId: string | undefined, clusterId: string) {
  const [nodes, setNodes] = useState<InfraNode[] | null>(null);
  const [pvcs, setPvcs] = useState<InfraPVC[] | null>(null);

  const load = useCallback(() => {
    if (!orgId) return;
    getInfra(orgId, clusterId)
      .then((r) => {
        setNodes(r.nodes);
        setPvcs(r.pvcs);
      })
      .catch(() => {
        setNodes((n) => n ?? []);
        setPvcs((p) => p ?? []);
      });
  }, [orgId, clusterId]);

  const loadRef = useRef(load);
  loadRef.current = load;
  const { live } = useClusterEvents(orgId, clusterId, {
    topology: () => loadRef.current(),
  });

  useEffect(() => {
    load();
    const t = setInterval(load, live ? 30_000 : 5_000);
    return () => clearInterval(t);
  }, [load, live]);

  return { nodes, pvcs, live };
}
