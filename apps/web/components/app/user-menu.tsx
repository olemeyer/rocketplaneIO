'use client';

import { useState } from 'react';
import { cn } from '@/lib/cn';
import { initials } from '@/lib/format';
import { Skeleton } from '@/components/ui';
import { logout } from '@/lib/api/controlplane';
import { useMe } from './me-context';
import { Popover, MenuItem } from './popover';

// User-Menu: Identität + Logout (POST /auth/logout → /login). Skin-agnostisch:
// ink-Monogramm als Avatar, Radius token-getrieben (Swiss eckig, Aurora dezent weich).
export function UserMenu() {
  const { me, loading } = useMe();
  const [busy, setBusy] = useState(false);

  if (loading && !me) {
    return <Skeleton className="h-8 w-8" />;
  }
  if (!me) return null;

  const { user } = me;

  async function doLogout() {
    if (busy) return;
    setBusy(true);
    try {
      await logout();
    } catch {
      /* auch bei Fehler zum Login — Cookie ist ggf. schon weg */
    }
    window.location.href = '/login';
  }

  return (
    <Popover
      align="end"
      trigger={(open) => (
        <span
          className={cn(
            'grid h-8 w-8 place-items-center overflow-hidden rounded-skin-sm border text-[12px] font-bold transition-colors',
            open ? 'border-line-strong' : 'border-line hover:border-line-strong',
          )}
        >
          {user.avatarUrl ? (
            // eslint-disable-next-line @next/next/no-img-element
            <img
              src={user.avatarUrl}
              alt=""
              className="h-full w-full object-cover"
              referrerPolicy="no-referrer"
            />
          ) : (
            <span className="grid h-full w-full place-items-center bg-ink text-paper">
              {initials(user.name || user.email)}
            </span>
          )}
        </span>
      )}
      contentClassName="w-[240px]"
      content={(close) => (
        <div>
          <div className="flex items-center gap-2.5 px-2 py-2">
            <span className="grid h-9 w-9 shrink-0 place-items-center overflow-hidden rounded-skin-sm border border-line bg-raised text-[12px] font-bold text-mid">
              {user.avatarUrl ? (
                // eslint-disable-next-line @next/next/no-img-element
                <img
                  src={user.avatarUrl}
                  alt=""
                  className="h-full w-full object-cover"
                  referrerPolicy="no-referrer"
                />
              ) : (
                initials(user.name || user.email)
              )}
            </span>
            <div className="min-w-0">
              <div className="truncate text-[13px] font-bold text-ink">
                {user.name || user.email}
              </div>
              {user.name ? (
                <div className="truncate font-mono text-[11px] text-faint">{user.email}</div>
              ) : null}
            </div>
          </div>

          <div className="my-1 h-px bg-line" />

          <MenuItem
            onClick={() => {
              close();
              void doLogout();
            }}
            className="text-red hover:bg-tone-red-bg hover:text-tone-red-fg"
          >
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" aria-hidden>
              <path
                d="M15 12H4m0 0l3.5-3.5M4 12l3.5 3.5M14 4h4a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2h-4"
                stroke="currentColor"
                strokeWidth="1.6"
                strokeLinecap="square"
                strokeLinejoin="miter"
              />
            </svg>
            <span className="rp-label">
              {busy ? 'Signing out…' : 'Sign out'}
            </span>
          </MenuItem>
        </div>
      )}
    />
  );
}
