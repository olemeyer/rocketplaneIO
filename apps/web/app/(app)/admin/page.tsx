'use client';

import { useCallback, useEffect, useState } from 'react';
import { useRouter } from 'next/navigation';
import { cn } from '@/lib/cn';
import { initials } from '@/lib/format';
import { Spinner } from '@/components/ui';
import { PageHeader } from '@/components/app/page-header';
import { useMe } from '@/components/app/me-context';
import { adminListOrgs, adminListUsers, adminSetUserFlags, adminStats } from '@/lib/api/controlplane';
import type { AdminOrg, AdminUser } from '@/lib/api/types';

// Admin — the platform-admin (super admin) console: instance-wide user & org
// directory, suspend/reactivate, grant/revoke platform-admin. Distinct from org
// roles: this operates the whole instance. Gated on me.user.isPlatformAdmin
// (the API enforces it independently).

type Tab = 'users' | 'orgs';

function relTime(iso?: string): string {
  if (!iso) return '';
  const t = Date.parse(iso);
  if (!t) return '';
  const s = Math.max(0, (Date.now() - t) / 1000);
  if (s < 3600) return `${Math.floor(s / 60)}m`;
  if (s < 86400) return `${Math.floor(s / 3600)}h`;
  return `${Math.floor(s / 86400)}d`;
}

export default function AdminPage() {
  const { me, loading } = useMe();
  const router = useRouter();
  const [tab, setTab] = useState<Tab>('users');
  const [stats, setStats] = useState<Record<string, number> | null>(null);

  const isAdmin = me?.user.isPlatformAdmin === true;

  useEffect(() => {
    if (!loading && me && !isAdmin) router.replace('/');
  }, [loading, me, isAdmin, router]);

  useEffect(() => {
    if (isAdmin) adminStats().then((r) => setStats(r.stats)).catch(() => {});
  }, [isAdmin]);

  if (loading || !me) return <div className="flex h-64 items-center justify-center text-muted"><Spinner /></div>;
  if (!isAdmin) return null;

  return (
    <div className="flex h-[calc(100dvh-52px)] flex-col px-4 pt-4 sm:px-5">
      <PageHeader kicker="platform / admin" title="Admin console" meta="instance-wide">
        {stats ? (
          <div className="flex items-center gap-3 font-mono text-[10.5px] text-muted tnum">
            <span><span className="text-ink">{stats.users}</span> users</span>
            <span><span className="text-ink">{stats.orgs}</span> orgs</span>
            <span><span className="text-ink">{stats.clusters}</span> clusters</span>
            {(stats.suspended ?? 0) > 0 ? <span style={{ color: 'var(--rp-tone-red-fg)' }}>{stats.suspended} suspended</span> : null}
          </div>
        ) : null}
      </PageHeader>

      <div className="mt-3 flex shrink-0 gap-1">
        {(['users', 'orgs'] as Tab[]).map((t) => (
          <button key={t} type="button" onClick={() => setTab(t)}
            className={cn('rp-focus rounded-skin-sm px-3 py-1.5 font-mono text-[11.5px] capitalize transition-colors', tab === t ? 'bg-hover text-ink' : 'text-muted hover:text-ink')}
            style={tab === t ? { boxShadow: 'inset 0 0 0 1px var(--rp-line-strong)' } : undefined}>
            {t}
          </button>
        ))}
      </div>

      <div className="mt-3 min-h-0 flex-1 overflow-y-auto pb-6">
        {tab === 'users' ? <UsersTab myId={me.user.id} /> : <OrgsTab />}
      </div>
    </div>
  );
}

function UsersTab({ myId }: { myId: string }) {
  const [users, setUsers] = useState<AdminUser[] | null>(null);
  const [search, setSearch] = useState('');
  const [q, setQ] = useState('');

  useEffect(() => { const t = setTimeout(() => setQ(search), 250); return () => clearTimeout(t); }, [search]);
  const load = useCallback(() => { adminListUsers(q, 300).then((r) => setUsers(r.users)).catch(() => setUsers([])); }, [q]);
  useEffect(load, [load]);

  const flag = async (userId: string, flags: { suspended?: boolean; platformAdmin?: boolean }) => {
    try { await adminSetUserFlags(userId, flags); load(); } catch { /* surfaced by reload */ }
  };

  return (
    <div className="max-w-[960px] space-y-3">
      <input value={search} onChange={(e) => setSearch(e.target.value)} placeholder="search users by email or name…" className="rp-focus h-9 w-full max-w-[360px] rounded-skin-sm border border-line bg-inset px-3 font-mono text-[12px] text-ink placeholder:text-faint" />
      {users === null ? (
        <div className="flex items-center gap-2 py-4 text-muted"><Spinner /> <span className="font-mono text-[11px]">loading…</span></div>
      ) : (
        <div className="divide-y divide-line overflow-hidden rounded-skin border border-line">
          {users.map((u) => {
            const suspended = !!u.suspendedAt;
            const isSelf = u.id === myId;
            return (
              <div key={u.id} className="flex items-center gap-3 bg-raised px-3 py-2">
                <span className="grid h-7 w-7 shrink-0 place-items-center overflow-hidden rounded-skin-sm border border-line bg-inset font-mono text-[10px] font-bold text-mid" style={suspended ? { opacity: 0.5 } : undefined}>
                  {u.avatarUrl ? (
                    // eslint-disable-next-line @next/next/no-img-element
                    <img src={u.avatarUrl} alt="" className="h-full w-full object-cover" referrerPolicy="no-referrer" />
                  ) : initials(u.name || u.email)}
                </span>
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-1.5">
                    <span className="truncate font-mono text-[11.5px] text-ink">{u.name || u.email}{isSelf ? <span className="text-faint"> (you)</span> : null}</span>
                    {u.isPlatformAdmin ? <span className="shrink-0 rounded-skin-chip px-1 py-px font-mono text-[8.5px] uppercase tracking-[0.05em]" style={{ color: 'var(--rp-accent)', background: 'color-mix(in oklab, var(--rp-accent) 12%, transparent)' }}>admin</span> : null}
                    {suspended ? <span className="shrink-0 rounded-skin-chip px-1 py-px font-mono text-[8.5px] uppercase tracking-[0.05em]" style={{ color: 'var(--rp-tone-red-fg)', background: 'var(--rp-tone-red-bg)' }}>suspended</span> : null}
                  </div>
                  {u.name ? <div className="truncate font-mono text-[9.5px] text-faint">{u.email}</div> : null}
                </div>
                <span className="font-mono text-[9.5px] tabular-nums text-faint">{u.orgCount} orgs</span>
                <span className="font-mono text-[9px] text-faint">{relTime(u.createdAt)}</span>
                {!isSelf ? (
                  <>
                    <button type="button" onClick={() => flag(u.id, { platformAdmin: !u.isPlatformAdmin })} className="rp-focus rounded-skin-chip border border-line px-2 py-0.5 font-mono text-[10px] text-muted transition-colors hover:bg-hover hover:text-ink">
                      {u.isPlatformAdmin ? 'revoke admin' : 'make admin'}
                    </button>
                    <button type="button" onClick={() => flag(u.id, { suspended: !suspended })} className="rp-focus rounded-skin-chip border border-line px-2 py-0.5 font-mono text-[10px] transition-colors hover:bg-hover" style={{ color: suspended ? 'var(--rp-tone-green-fg)' : 'var(--rp-tone-red-fg)' }}>
                      {suspended ? 'reactivate' : 'suspend'}
                    </button>
                  </>
                ) : <span className="font-mono text-[9px] text-faint">— you —</span>}
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

function OrgsTab() {
  const [orgs, setOrgs] = useState<AdminOrg[] | null>(null);
  const [search, setSearch] = useState('');
  const [q, setQ] = useState('');
  useEffect(() => { const t = setTimeout(() => setQ(search), 250); return () => clearTimeout(t); }, [search]);
  useEffect(() => { adminListOrgs(q, 300).then((r) => setOrgs(r.orgs)).catch(() => setOrgs([])); }, [q]);

  return (
    <div className="max-w-[960px] space-y-3">
      <input value={search} onChange={(e) => setSearch(e.target.value)} placeholder="search orgs…" className="rp-focus h-9 w-full max-w-[360px] rounded-skin-sm border border-line bg-inset px-3 font-mono text-[12px] text-ink placeholder:text-faint" />
      {orgs === null ? (
        <div className="flex items-center gap-2 py-4 text-muted"><Spinner /> <span className="font-mono text-[11px]">loading…</span></div>
      ) : (
        <div className="divide-y divide-line overflow-hidden rounded-skin border border-line">
          {orgs.map((o) => (
            <div key={o.id} className="flex items-center gap-3 bg-raised px-3 py-2 font-mono text-[11.5px]">
              <span className="min-w-0 flex-1 truncate text-ink">{o.name}
                {o.isPersonal ? <span className="ml-1.5 rounded-skin-chip px-1 py-px text-[8.5px] uppercase tracking-[0.05em] text-faint" style={{ background: 'var(--rp-bg-inset)' }}>personal</span> : null}
              </span>
              <span className="truncate text-[9.5px] text-faint">{o.slug}</span>
              <span className="tabular-nums text-muted">{o.memberCount} members</span>
              <span className="tabular-nums text-muted">{o.clusterCount} clusters</span>
              <span className="text-[9px] text-faint">{relTime(o.createdAt)}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
