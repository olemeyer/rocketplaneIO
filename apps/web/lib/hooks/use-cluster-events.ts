'use client';

import { useEffect, useRef, useState } from 'react';

// use-cluster-events — der Live-Kanal der UI: EIN SSE-Stream je Cluster-Scope
// liefert Invalidation-Signale (topology/actions/logs/namespaces); die
// Komponente refetcht daraufhin gezielt ihre Query. Ergebnis: Updates in
// Push-Latenz statt Poll-Intervall, und pro Browser EINE idle Verbindung
// statt Dauerfeuer — die Grundlage, auf der das System auf viele Nutzer
// skaliert (Signale sind winzig, Reads bleiben normale Query-Pfade).
//
// `live` = Stream verbunden → Komponenten strecken ihre Polls auf einen
// seltenen FALLBACK (Robustheit bei Proxy-Problemen); EventSource selbst
// reconnected automatisch.

export type ClusterEventType = 'topology' | 'actions' | 'logs' | 'namespaces' | 'alerts' | 'incidents';

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
    ];
    for (const [type, fn] of listeners) es.addEventListener(type, fn);
    es.onerror = () => setLive(false); // EventSource reconnected selbst

    return () => {
      es.close();
      setLive(false);
    };
  }, [orgId, clusterId]);

  return { live };
}
