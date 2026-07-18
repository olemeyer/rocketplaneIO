'use client';

import { useEffect, useRef, useState } from 'react';

// use-cluster-events — the UI's live channel: ONE SSE stream per cluster scope
// delivers invalidation signals (topology/actions/logs/namespaces); the
// component then refetches its query in a targeted way. The result: updates at
// push latency instead of poll interval, and ONE idle connection per browser
// instead of constant fire — the foundation on which the system scales to many
// users (signals are tiny, reads stay normal query paths).
//
// `live` = stream connected → components stretch their polls to a rare
// FALLBACK (robustness against proxy problems); EventSource itself
// reconnects automatically.

export type ClusterEventType = 'topology' | 'actions' | 'logs' | 'namespaces' | 'alerts' | 'incidents' | 'transactions';

export function useClusterEvents(
  orgId: string | undefined,
  clusterId: string,
  handlers: Partial<Record<ClusterEventType, () => void>>,
): { live: boolean } {
  const [live, setLive] = useState(false);
  const handlersRef = useRef(handlers);
  handlersRef.current = handlers;

  useEffect(() => {
    if (!orgId || !clusterId) return;
    const es = new EventSource(
      `/api/orgs/${encodeURIComponent(orgId)}/clusters/${encodeURIComponent(clusterId)}/events`,
      { withCredentials: true },
    );
    const on = (type: ClusterEventType) => () => handlersRef.current[type]?.();
    const listeners: [string, () => void][] = [
      ['hello', () => setLive(true)],
      ['topology', on('topology')],
      ['actions', on('actions')],
      ['logs', on('logs')],
      ['namespaces', on('namespaces')],
      ['alerts', on('alerts')],
      ['incidents', on('incidents')],
      ['transactions', on('transactions')],
    ];
    for (const [type, fn] of listeners) es.addEventListener(type, fn);
    es.onerror = () => setLive(false); // EventSource reconnects on its own

    return () => {
      es.close();
      setLive(false);
    };
  }, [orgId, clusterId]);

  return { live };
}
