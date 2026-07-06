'use client';

import { useState } from 'react';
import { useRouter } from 'next/navigation';
import { cn } from '@/lib/cn';
import { Button, Skeleton, Spinner } from '@/components/ui';
import { useMe } from './me-context';
import { Popover, MenuItem } from './popover';
import { createOrg, switchOrg } from '@/lib/api/controlplane';

// Org-Switcher: aktuelle Org, andere Orgs wechseln (POST /api/session/org),
// „New org…" inline (POST /api/orgs). Skin-agnostisch: line-strong-Rahmen, mono-
// Breadcrumb, ink-Monogramm als Org-Marker (Radius token-getrieben). Tenant-Grenze
// klar sichtbar — Swiss hart/eckig, Aurora weich/dezent.
export function OrgSwitcher() {
  const { me, currentOrg, refresh, loading } = useMe();
  const router = useRouter();
  const [busy, setBusy] = useState(false);
  const [creating, setCreating] = useState(false);
  const [newName, setNewName] = useState('');
  const [error, setError] = useState<string | null>(null);

  if (loading && !currentOrg) {
    return <Skeleton className="h-8 w-40" />;
  }
  if (!me || !currentOrg) return null;

  async function selectOrg(orgId: string, close: () => void) {
    if (orgId === currentOrg!.id) {
      close();
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await switchOrg(orgId);
      await refresh();
      close();
      router.push('/');
      router.refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not switch org');
    } finally {
      setBusy(false);
    }
  }

  async function submitNewOrg(close: () => void) {
    const name = newName.trim();
    if (!name || busy) return;
    setBusy(true);
    setError(null);
    try {
      const org = await createOrg(name);
      await switchOrg(org.id);
      await refresh();
      setNewName('');
      setCreating(false);
      close();
      router.push('/');
      router.refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not create org');
    } finally {
      setBusy(false);
    }
  }

  return (
    <Popover
      trigger={(open) => (
        <span
          className={cn(
            'inline-flex h-8 items-center gap-2 rounded-skin-sm border px-1.5 pr-2 text-[12px] transition-colors',
            open
              ? 'border-line-strong bg-hover text-ink'
              : 'border-line text-mid hover:border-line-strong hover:text-ink',
          )}
        >
          <span className="grid h-5 w-5 shrink-0 place-items-center rounded-skin-sm bg-ink text-[10px] font-bold text-paper">
            {currentOrg.name.slice(0, 1).toUpperCase()}
          </span>
          <span className="max-w-[160px] truncate font-mono text-ink">{currentOrg.name}</span>
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" aria-hidden className="text-faint">
            <path d="M6 9l6 6 6-6" stroke="currentColor" strokeWidth="2" strokeLinecap="square" strokeLinejoin="miter" />
          </svg>
        </span>
      )}
      contentClassName="w-[264px]"
      content={(close) => (
        <div>
          <div className="rp-micro px-2 pb-1.5 pt-1 !text-[10px]">
            Organizations
          </div>
          <div className="max-h-64 overflow-y-auto">
            {me.orgs.map((org) => {
              const active = org.id === currentOrg.id;
              return (
                <MenuItem key={org.id} onClick={() => selectOrg(org.id, close)} active={active}>
                  <span className="grid h-5 w-5 shrink-0 place-items-center rounded-skin-sm bg-ink text-[10px] font-bold text-paper">
                    {org.name.slice(0, 1).toUpperCase()}
                  </span>
                  <span className="flex min-w-0 flex-1 flex-col">
                    <span className="truncate text-ink">{org.name}</span>
                    <span className="rp-label truncate font-mono text-[10px] text-faint">
                      {org.isPersonal ? 'Personal' : org.role}
                    </span>
                  </span>
                  
                </MenuItem>
              );
            })}
          </div>

          <div className="my-1 h-px bg-line" />

          {creating ? (
            <form
              onSubmit={(e) => {
                e.preventDefault();
                void submitNewOrg(close);
              }}
              className="p-1"
            >
              <input
                autoFocus
                value={newName}
                onChange={(e) => setNewName(e.target.value)}
                placeholder="New org name"
                className="h-8 w-full rounded-skin-sm border border-line bg-inset px-2 font-mono text-[12px] text-ink outline-none placeholder:text-faint focus:border-ink rp-focus"
              />
              <div className="mt-1.5 flex items-center gap-1.5">
                <Button
                  type="submit"
                  variant="primary"
                  size="sm"
                  disabled={busy || !newName.trim()}
                  className="flex-1"
                >
                  {busy ? <Spinner className="h-3.5 w-3.5" /> : null}
                  Create
                </Button>
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  onClick={() => {
                    setCreating(false);
                    setNewName('');
                    setError(null);
                  }}
                >
                  Cancel
                </Button>
              </div>
            </form>
          ) : (
            <MenuItem onClick={() => setCreating(true)}>
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" aria-hidden className="text-muted">
                <path d="M12 5v14M5 12h14" stroke="currentColor" strokeWidth="1.8" strokeLinecap="square" />
              </svg>
              <span className="rp-label">New org</span>
            </MenuItem>
          )}

          {error ? <p className="px-2 pb-1 pt-0.5 font-mono text-[11px] text-red">{error}</p> : null}
        </div>
      )}
    />
  );
}
