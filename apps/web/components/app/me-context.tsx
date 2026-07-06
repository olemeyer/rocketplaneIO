'use client';

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useState,
  type ReactNode,
} from 'react';
import { getMe } from '@/lib/api/controlplane';
import type { Me, OrgSummary } from '@/lib/api/types';

// Session-Kontext für die App-Shell: /api/me einmal client laden, allen Teilen
// (Org-Switcher, User-Menu, Pages) zur Verfügung stellen, refreshbar nach Mutationen.
type MeContextValue = {
  me: Me | null;
  loading: boolean;
  error: string | null;
  currentOrg: OrgSummary | null;
  refresh: () => Promise<void>;
};

const MeContext = createContext<MeContextValue | null>(null);

export function MeProvider({ children }: { children: ReactNode }) {
  const [me, setMe] = useState<Me | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    try {
      const next = await getMe();
      setMe(next);
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load session');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const currentOrg = me
    ? (me.orgs.find((o) => o.id === me.currentOrgId) ?? me.orgs[0] ?? null)
    : null;

  return (
    <MeContext.Provider value={{ me, loading, error, currentOrg, refresh }}>
      {children}
    </MeContext.Provider>
  );
}

export function useMe(): MeContextValue {
  const ctx = useContext(MeContext);
  if (!ctx) throw new Error('useMe must be used within <MeProvider>');
  return ctx;
}
