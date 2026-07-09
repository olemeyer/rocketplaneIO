'use client';

import { useCallback, useEffect, useMemo, useState } from 'react';
import { cn } from '@/lib/cn';
import { initials } from '@/lib/format';
import { Spinner } from '@/components/ui';
import { PageHeader } from '@/components/app/page-header';
import { useMe } from '@/components/app/me-context';
import {
  changePassword, createAPIToken, createInvitation, deleteAPIToken, deleteOrg, listAPITokens,
  listAudit, listInvitations, listMembers, removeMember, renameOrg, revokeAPIToken,
  revokeInvitation, transferOwnership, updateMemberRole,
} from '@/lib/api/controlplane';
import type { APIToken, AuditEntry, Invitation, Member, OrgRole } from '@/lib/api/types';
import { ApiError } from '@/lib/api/client';

// Settings — org-level Access Control: who is in the org (members + pending
// invites), what happened (audit), and the org itself (rename / transfer /
// delete). Gated by the caller's role: members read, admins manage people,
// owners manage the org. Mirrors the control-plane RBAC exactly.

type Tab = 'members' | 'api keys' | 'account' | 'audit' | 'organization';
const ROLE_RANK: Record<OrgRole, number> = { owner: 3, admin: 2, member: 1 };

function relTime(iso: string): string {
  const t = Date.parse(iso);
  if (!t) return '';
  const s = Math.max(0, (Date.now() - t) / 1000);
  if (s < 60) return 'just now';
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86400)}d ago`;
}

// expiryLabel handles FUTURE timestamps (relTime clamps them to "just now").
function expiryLabel(iso: string): string {
  const t = Date.parse(iso);
  if (!t) return '';
  const s = (t - Date.now()) / 1000;
  if (s <= 0) return 'expired';
  const d = Math.floor(s / 86400);
  if (d >= 1) return `expires in ${d}d`;
  const h = Math.floor(s / 3600);
  return h >= 1 ? `expires in ${h}h` : 'expires soon';
}

// RETICLE: signal-orange (--rp-accent) is reserved for focus/selection chrome,
// so the owner role uses the gold status tone (highlight without alerting).
const ROLE_TONE: Record<OrgRole, string> = {
  owner: 'var(--rp-tone-yellow-fg)',
  admin: 'var(--rp-ink)',
  member: 'var(--rp-ink-muted)',
};

export default function SettingsPage() {
  const { currentOrg, me, refresh } = useMe();
  const [tab, setTab] = useState<Tab>('members');
  const role = (currentOrg?.role ?? 'member') as OrgRole;
  const isAdmin = ROLE_RANK[role] >= ROLE_RANK.admin;
  const isOwner = role === 'owner';

  if (!currentOrg) {
    return <div className="flex h-64 items-center justify-center text-muted"><Spinner /></div>;
  }

  return (
    <div className="flex h-[calc(100dvh-52px)] flex-col px-4 pt-4 sm:px-5">
      <PageHeader kicker="org / settings" title="Settings" meta={currentOrg.name}>
        <span className="inline-flex items-center gap-1.5 rounded-skin-chip border border-line bg-inset px-2 py-0.5 font-mono text-[10px]" style={{ color: ROLE_TONE[role] }}>
          your role: {role}
        </span>
      </PageHeader>

      <div className="mt-3 -mx-4 flex shrink-0 gap-1 overflow-x-auto px-4 sm:mx-0 sm:px-0">
        {(['members', 'api keys', 'account', 'audit', 'organization'] as Tab[]).map((t) => (
          <button
            key={t}
            type="button"
            onClick={() => setTab(t)}
            className={cn(
              'rp-focus shrink-0 whitespace-nowrap rounded-skin-sm px-3 py-1.5 font-mono text-[11.5px] capitalize transition-colors',
              tab === t ? 'bg-hover text-ink' : 'text-muted hover:text-ink',
            )}
            style={tab === t ? { boxShadow: 'inset 0 0 0 1px var(--rp-line-strong)' } : undefined}
          >
            {t}
          </button>
        ))}
      </div>

      <div className="mt-3 min-h-0 flex-1 overflow-y-auto pb-6">
        {tab === 'api keys' ? (
          // API keys work for both personal and team orgs (solo automation too).
          <ApiKeysTab orgId={currentOrg.id} isAdmin={isAdmin} />
        ) : tab === 'account' ? (
          // Account (password) is per-user, so it works regardless of org kind.
          <AccountTab />
        ) : currentOrg.isPersonal ? (
          <PersonalNotice />
        ) : tab === 'members' ? (
          <MembersTab orgId={currentOrg.id} isAdmin={isAdmin} isOwner={isOwner} myId={me?.user.id ?? ''} onLeave={refresh} />
        ) : tab === 'audit' ? (
          <AuditTab orgId={currentOrg.id} isAdmin={isAdmin} />
        ) : (
          <OrganizationTab orgId={currentOrg.id} orgName={currentOrg.name} isOwner={isOwner} onChanged={refresh} />
        )}
      </div>
    </div>
  );
}

function PersonalNotice() {
  return (
    <div className="mt-2 max-w-[560px] rounded-skin border border-dashed p-5 font-mono text-[11.5px] leading-relaxed text-muted" style={{ borderColor: 'var(--rp-line-strong)' }}>
      This is your <span className="text-ink">personal org</span> — it always has exactly one member (you) and cannot be shared, renamed or deleted. Create a team org from the org switcher to invite people and manage roles.
    </div>
  );
}

/* ── Members + invitations ─────────────────────────────────────────────── */

function MembersTab({ orgId, isAdmin, isOwner, myId, onLeave }: { orgId: string; isAdmin: boolean; isOwner: boolean; myId: string; onLeave: () => void }) {
  const [members, setMembers] = useState<Member[] | null>(null);
  const [invites, setInvites] = useState<Invitation[]>([]);
  const [err, setErr] = useState<string | null>(null);

  const load = useCallback(() => {
    listMembers(orgId).then((r) => setMembers(r.members)).catch(() => setMembers([]));
    if (isAdmin) listInvitations(orgId).then((r) => setInvites(r.invitations)).catch(() => {});
  }, [orgId, isAdmin]);
  useEffect(load, [load]);

  const act = async (fn: () => Promise<unknown>) => {
    setErr(null);
    try { await fn(); load(); } catch (e) { setErr(e instanceof ApiError ? String(e.message) : 'failed'); }
  };

  return (
    <div className="max-w-[820px] space-y-5">
      {isAdmin ? <InviteForm orgId={orgId} isOwner={isOwner} onInvited={load} /> : null}

      {err ? <div className="rounded-skin-sm px-2.5 py-1.5 font-mono text-[11px]" style={{ color: 'var(--rp-tone-red-fg)', background: 'var(--rp-tone-red-bg)' }}>{err}</div> : null}

      {isAdmin && invites.length > 0 ? (
        <section>
          <p className="rp-micro !text-[10px] mb-1.5">pending invitations</p>
          <div className="divide-y divide-line overflow-hidden rounded-skin border border-line">
            {invites.map((iv) => (
              <div key={iv.id} className="flex flex-wrap items-center gap-x-3 gap-y-1.5 bg-raised px-3 py-2 sm:flex-nowrap">
                <span className="grid h-7 w-7 shrink-0 place-items-center rounded-skin-sm border border-line bg-inset font-mono text-[10px] text-faint">✉</span>
                <span className="min-w-0 flex-1 truncate font-mono text-[11.5px] text-ink">{iv.email}</span>
                <span className="shrink-0 font-mono text-[10px] uppercase tracking-[0.05em]" style={{ color: ROLE_TONE[iv.role] }}>{iv.role}</span>
                <span className="shrink-0 font-mono text-[10px] text-muted">expires {relTime(iv.expiresAt).replace(' ago', '')}</span>
                <button type="button" onClick={() => act(() => revokeInvitation(orgId, iv.id))} className="rp-focus ml-auto shrink-0 rounded-skin-chip border border-line px-2 py-0.5 font-mono text-[10px] text-muted transition-colors hover:bg-hover hover:text-ink sm:ml-0">revoke</button>
              </div>
            ))}
          </div>
        </section>
      ) : null}

      <section>
        <p className="rp-micro !text-[10px] mb-1.5">members {members ? `· ${members.length}` : ''}</p>
        {members === null ? (
          <div className="flex items-center gap-2 py-4 text-muted"><Spinner /> <span className="font-mono text-[11px]">loading…</span></div>
        ) : (
          <div className="divide-y divide-line overflow-hidden rounded-skin border border-line">
            {members.map((m) => (
              <MemberRow key={m.userId} m={m} orgId={orgId} isAdmin={isAdmin} isOwner={isOwner} isSelf={m.userId === myId}
                onAct={(fn) => act(async () => { await fn(); if (m.userId === myId) onLeave(); })} />
            ))}
          </div>
        )}
      </section>
    </div>
  );
}

function MemberRow({ m, orgId, isAdmin, isOwner, isSelf, onAct }: { m: Member; orgId: string; isAdmin: boolean; isOwner: boolean; isSelf: boolean; onAct: (fn: () => Promise<unknown>) => void }) {
  // Only owners can touch owner rows or grant owner. Admins manage member/admin.
  const canEditRole = isAdmin && (m.role !== 'owner' || isOwner) && !isSelf;
  const roleOptions: OrgRole[] = isOwner ? ['owner', 'admin', 'member'] : ['admin', 'member'];
  return (
    <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5 bg-raised px-3 py-2 sm:flex-nowrap">
      <span className="grid h-7 w-7 shrink-0 place-items-center overflow-hidden rounded-skin-sm border border-line bg-inset font-mono text-[10px] font-bold text-mid">
        {m.avatarUrl ? (
          // eslint-disable-next-line @next/next/no-img-element
          <img src={m.avatarUrl} alt="" className="h-full w-full object-cover" referrerPolicy="no-referrer" />
        ) : initials(m.name || m.email)}
      </span>
      <div className="min-w-0 flex-1 basis-[calc(100%-2.5rem)] sm:basis-auto">
        <div className="truncate font-mono text-[11.5px] text-ink">{m.name || m.email}{isSelf ? <span className="text-faint"> (you)</span> : null}</div>
        {m.name ? <div className="truncate font-mono text-[10px] text-muted">{m.email}</div> : null}
      </div>
      <span className="shrink-0 font-mono text-[10px] text-muted">{relTime(m.joinedAt)}</span>
      {canEditRole ? (
        <select
          value={m.role}
          onChange={(e) => onAct(() => updateMemberRole(orgId, m.userId, e.target.value as OrgRole))}
          className="rp-focus h-7 rounded-skin-sm border border-line bg-inset px-1.5 font-mono text-[10.5px] text-ink"
        >
          {roleOptions.map((r) => <option key={r} value={r}>{r}</option>)}
        </select>
      ) : (
        <span className="w-[64px] text-right font-mono text-[10px] uppercase tracking-[0.05em]" style={{ color: ROLE_TONE[m.role] }}>{m.role}</span>
      )}
      {(isAdmin && !isSelf && (m.role !== 'owner' || isOwner)) || isSelf ? (
        <button
          type="button"
          onClick={() => onAct(() => removeMember(orgId, m.userId))}
          title={isSelf ? 'leave this org' : 'remove member'}
          className="rp-focus ml-auto shrink-0 rounded-skin-chip border border-line px-2 py-0.5 font-mono text-[10px] text-muted transition-colors hover:bg-hover sm:ml-0"
          style={{ color: isSelf ? 'var(--rp-tone-red-fg)' : undefined }}
        >
          {isSelf ? 'leave' : 'remove'}
        </button>
      ) : <span className="hidden w-[52px] sm:block" />}
    </div>
  );
}

function InviteForm({ orgId, isOwner, onInvited }: { orgId: string; isOwner: boolean; onInvited: () => void }) {
  const [email, setEmail] = useState('');
  const [role, setRole] = useState<OrgRole>('member');
  const [busy, setBusy] = useState(false);
  const [link, setLink] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);

  const submit = async () => {
    if (!email.trim() || busy) return;
    setBusy(true); setErr(null); setLink(null);
    try {
      const r = await createInvitation(orgId, email.trim(), role);
      setLink(r.invitation.acceptUrl ?? null);
      setEmail('');
      onInvited();
    } catch (e) { setErr(e instanceof ApiError ? String(e.message) : 'invite failed'); }
    setBusy(false);
  };

  return (
    <section className="rounded-skin border border-line bg-raised p-3" style={{ boxShadow: 'var(--rp-rim)' }}>
      <p className="rp-micro !text-[10px] mb-2">invite a teammate</p>
      <div className="flex flex-wrap items-center gap-2">
        <input
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          onKeyDown={(e) => { if (e.key === 'Enter') submit(); }}
          type="email"
          placeholder="name@company.com"
          className="rp-focus h-9 min-w-[220px] flex-1 rounded-skin-sm border border-line bg-inset px-3 font-mono text-[12px] text-ink placeholder:text-faint"
        />
        <select value={role} onChange={(e) => setRole(e.target.value as OrgRole)} className="rp-focus h-9 rounded-skin-sm border border-line bg-inset px-2 font-mono text-[11.5px] text-ink">
          <option value="member">member</option>
          <option value="admin">admin</option>
          {isOwner ? <option value="owner">owner</option> : null}
        </select>
        <button type="button" disabled={!email.trim() || busy} onClick={submit} className="rp-focus h-9 rounded-skin-sm px-3.5 font-mono text-[11.5px] font-semibold transition-opacity hover:opacity-90 disabled:opacity-40" style={{ background: 'var(--rp-btn-bg)', color: 'var(--rp-btn-fg)' }}>
          {busy ? 'inviting…' : 'Send invite'}
        </button>
      </div>
      {err ? <p className="mt-2 font-mono text-[10.5px]" style={{ color: 'var(--rp-tone-red-fg)' }}>{err}</p> : null}
      {link ? (
        <div className="mt-2 rounded-skin-sm border border-line bg-inset p-2">
          <p className="font-mono text-[9.5px] text-faint">invite created — share this link (the invitee accepts after signing in):</p>
          <div className="mt-1 flex items-center gap-2">
            <code className="min-w-0 flex-1 truncate font-mono text-[10.5px] text-ink">{link}</code>
            <button type="button" onClick={() => navigator.clipboard?.writeText(link)} className="rp-focus rounded-skin-chip border border-line px-2 py-0.5 font-mono text-[10px] text-mid transition-colors hover:bg-hover hover:text-ink">copy</button>
          </div>
        </div>
      ) : null}
    </section>
  );
}

/* ── Audit ──────────────────────────────────────────────────────────────── */

function AuditTab({ orgId, isAdmin }: { orgId: string; isAdmin: boolean }) {
  const [entries, setEntries] = useState<AuditEntry[] | null>(null);
  useEffect(() => {
    if (!isAdmin) { setEntries([]); return; }
    listAudit(orgId, 200).then((r) => setEntries(r.entries)).catch(() => setEntries([]));
  }, [orgId, isAdmin]);

  if (!isAdmin) return <p className="font-mono text-[11px] text-muted">The audit log is visible to admins and owners.</p>;
  if (entries === null) return <div className="flex items-center gap-2 py-4 text-muted"><Spinner /> <span className="font-mono text-[11px]">loading…</span></div>;
  if (entries.length === 0) return <p className="font-mono text-[11px] text-faint">No activity recorded yet.</p>;

  return (
    <div className="max-w-[820px] divide-y divide-line overflow-hidden rounded-skin border border-line">
      {entries.map((e) => (
        <div key={e.id} className="flex items-baseline gap-3 bg-raised px-3 py-1.5 font-mono text-[10.5px]">
          <span className="w-[68px] shrink-0 text-faint tnum">{relTime(e.createdAt)}</span>
          <span className="shrink-0 text-ink">{e.action.replace(/[._]/g, ' ')}</span>
          {e.targetName ? <span className="min-w-0 flex-1 truncate text-muted">{e.targetName}</span> : <span className="flex-1" />}
          <span className="shrink-0 text-faint">{e.actorEmail}</span>
        </div>
      ))}
    </div>
  );
}

/* ── Organization (owner) ──────────────────────────────────────────────── */

function OrganizationTab({ orgId, orgName, isOwner, onChanged }: { orgId: string; orgName: string; isOwner: boolean; onChanged: () => void }) {
  const [name, setName] = useState(orgName);
  const [members, setMembers] = useState<Member[]>([]);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [confirmDelete, setConfirmDelete] = useState('');

  useEffect(() => { listMembers(orgId).then((r) => setMembers(r.members)).catch(() => {}); }, [orgId]);
  const others = useMemo(() => members.filter((m) => m.role !== 'owner'), [members]);

  const run = async (fn: () => Promise<unknown>, ok: string) => {
    setBusy(true); setErr(null); setMsg(null);
    try { await fn(); setMsg(ok); onChanged(); } catch (e) { setErr(e instanceof ApiError ? String(e.message) : 'failed'); }
    setBusy(false);
  };

  if (!isOwner) return <p className="font-mono text-[11px] text-muted">Only the org owner can change these settings.</p>;

  return (
    <div className="max-w-[560px] space-y-6">
      <section>
        <p className="rp-micro !text-[10px] mb-1.5">organization name</p>
        <div className="flex items-center gap-2">
          <input value={name} onChange={(e) => setName(e.target.value)} className="rp-focus h-9 flex-1 rounded-skin-sm border border-line bg-inset px-3 font-mono text-[12px] text-ink" />
          <button type="button" disabled={busy || !name.trim() || name === orgName} onClick={() => run(() => renameOrg(orgId, name.trim()), 'renamed')} className="rp-focus h-9 rounded-skin-sm px-3.5 font-mono text-[11.5px] font-semibold transition-opacity hover:opacity-90 disabled:opacity-40" style={{ background: 'var(--rp-btn-bg)', color: 'var(--rp-btn-fg)' }}>Save</button>
        </div>
      </section>

      <section>
        <p className="rp-micro !text-[10px] mb-1.5">transfer ownership</p>
        <p className="mb-2 font-mono text-[10.5px] leading-relaxed text-muted">Hand the org to another member. You become an admin.</p>
        {others.length === 0 ? (
          <p className="font-mono text-[10.5px] text-faint">Invite another member first.</p>
        ) : (
          <div className="flex items-center gap-2">
            <select id="xfer" className="rp-focus h-9 flex-1 rounded-skin-sm border border-line bg-inset px-2 font-mono text-[11.5px] text-ink">
              {others.map((m) => <option key={m.userId} value={m.userId}>{m.name || m.email}</option>)}
            </select>
            <button type="button" disabled={busy} onClick={() => { const el = document.getElementById('xfer') as HTMLSelectElement; run(() => transferOwnership(orgId, el.value), 'ownership transferred'); }} className="rp-focus h-9 rounded-skin-sm border border-line px-3.5 font-mono text-[11.5px] text-ink transition-colors hover:bg-hover">Transfer</button>
          </div>
        )}
      </section>

      <section className="rounded-skin border p-3" style={{ borderColor: 'color-mix(in oklab, var(--rp-node-crit) 40%, var(--rp-line))' }}>
        <p className="rp-micro !text-[10px] mb-1.5" style={{ color: 'var(--rp-node-crit)' }}>danger zone</p>
        <p className="mb-2 font-mono text-[10.5px] leading-relaxed text-muted">Deleting <span className="text-ink">{orgName}</span> permanently removes its clusters, members, alerts and history. This cannot be undone.</p>
        <div className="flex items-center gap-2">
          <input value={confirmDelete} onChange={(e) => setConfirmDelete(e.target.value)} placeholder={`type "${orgName}" to confirm`} className="rp-focus h-9 flex-1 rounded-skin-sm border bg-inset px-3 font-mono text-[11.5px] text-ink placeholder:text-faint" style={{ borderColor: confirmDelete === orgName ? 'var(--rp-node-crit)' : 'var(--rp-line)' }} />
          <button type="button" disabled={busy || confirmDelete !== orgName} onClick={() => run(async () => { await deleteOrg(orgId); window.location.href = '/'; }, 'deleted')} className="rp-focus h-9 rounded-skin-sm px-3.5 font-mono text-[11.5px] font-semibold transition-opacity hover:opacity-90 disabled:opacity-40" style={{ background: 'var(--rp-node-crit)', color: 'var(--rp-btn-fg)' }}>Delete org</button>
        </div>
      </section>

      {msg ? <p className="font-mono text-[10.5px]" style={{ color: 'var(--rp-tone-green-fg)' }}>✓ {msg}</p> : null}
      {err ? <p className="font-mono text-[10.5px]" style={{ color: 'var(--rp-tone-red-fg)' }}>{err}</p> : null}
    </div>
  );
}

// ApiKeysTab — programmatic access: create org-scoped API tokens (rp_…) with a
// member/admin role and optional expiry. The secret is shown exactly ONCE at
// creation. Managing tokens requires an admin session (the API enforces it).
function ApiKeysTab({ orgId, isAdmin }: { orgId: string; isAdmin: boolean }) {
  const [tokens, setTokens] = useState<APIToken[] | null>(null);
  const [name, setName] = useState('');
  const [role, setRole] = useState<OrgRole>('member');
  const [expiry, setExpiry] = useState('never');
  const [busy, setBusy] = useState(false);
  const [secret, setSecret] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);

  const load = useCallback(() => {
    listAPITokens(orgId).then((r) => setTokens(r.tokens)).catch(() => setTokens([]));
  }, [orgId]);
  useEffect(load, [load]);

  const create = async () => {
    if (!name.trim() || busy) return;
    setBusy(true); setErr(null); setSecret(null);
    try {
      const days = expiry === 'never' ? 0 : parseInt(expiry, 10);
      const t = await createAPIToken(orgId, { name: name.trim(), role, expiresInDays: days });
      setSecret(t.secret ?? null);
      setName('');
      load();
    } catch (e) { setErr(e instanceof ApiError ? String(e.message) : 'failed to create token'); }
    setBusy(false);
  };

  const chip = (t: APIToken) => {
    if (t.status === 'revoked') return { fg: 'var(--rp-ink-muted)', bg: 'var(--rp-tone-neutral-bg)', label: 'revoked' };
    if (t.status === 'expired') return { fg: 'var(--rp-tone-yellow-fg)', bg: 'var(--rp-tone-yellow-bg)', label: 'expired' };
    return { fg: 'var(--rp-tone-green-fg)', bg: 'var(--rp-tone-green-bg)', label: 'active' };
  };

  return (
    <div className="max-w-[720px] space-y-3">
      {!isAdmin ? (
        <p className="rounded-skin-sm px-3 py-2 font-mono text-[10.5px]" style={{ color: 'var(--rp-tone-yellow-fg)', background: 'var(--rp-tone-yellow-bg)' }}>
          API tokens are managed by org admins. You can view them but not create or revoke.
        </p>
      ) : (
        <section className="rounded-skin border border-line bg-raised p-3" style={{ boxShadow: 'var(--rp-rim)' }}>
          <p className="rp-micro !text-[10px] mb-2">create an API token</p>
          <div className="flex flex-wrap items-center gap-2">
            <input
              value={name}
              onChange={(e) => setName(e.target.value)}
              onKeyDown={(e) => { if (e.key === 'Enter') create(); }}
              placeholder="e.g. ci-deploy-bot"
              className="rp-focus h-9 min-w-[200px] flex-1 rounded-skin-sm border border-line bg-inset px-3 font-mono text-[12px] text-ink placeholder:text-faint"
            />
            <select value={role} onChange={(e) => setRole(e.target.value as OrgRole)} className="rp-focus h-9 rounded-skin-sm border border-line bg-inset px-2 font-mono text-[11.5px] text-ink" title="token role">
              <option value="member">member</option>
              <option value="admin">admin</option>
            </select>
            <select value={expiry} onChange={(e) => setExpiry(e.target.value)} className="rp-focus h-9 rounded-skin-sm border border-line bg-inset px-2 font-mono text-[11.5px] text-ink" title="expiry">
              <option value="never">no expiry</option>
              <option value="30">30 days</option>
              <option value="90">90 days</option>
              <option value="365">1 year</option>
            </select>
            <button type="button" disabled={!name.trim() || busy} onClick={create} className="rp-focus h-9 rounded-skin-sm px-3.5 font-mono text-[11.5px] font-semibold transition-opacity hover:opacity-90 disabled:opacity-40" style={{ background: 'var(--rp-btn-bg)', color: 'var(--rp-btn-fg)' }}>
              {busy ? 'creating…' : 'Create token'}
            </button>
          </div>
          {err ? <p className="mt-2 font-mono text-[10.5px]" style={{ color: 'var(--rp-tone-red-fg)' }}>{err}</p> : null}
          {secret ? (
            <div className="mt-2 rounded-skin-sm border p-2.5" style={{ borderColor: 'var(--rp-tone-green-fg)', background: 'var(--rp-tone-green-bg)' }}>
              <p className="font-mono text-[9.5px]" style={{ color: 'var(--rp-tone-green-fg)' }}>copy this token now — it will NOT be shown again:</p>
              <div className="mt-1 flex items-center gap-2">
                <code className="min-w-0 flex-1 truncate rounded-skin-sm bg-inset px-2 py-1 font-mono text-[11px] text-ink">{secret}</code>
                <button type="button" onClick={() => navigator.clipboard?.writeText(secret)} className="rp-focus shrink-0 rounded-skin-chip border border-line bg-raised px-2 py-1 font-mono text-[10px] text-mid transition-colors hover:bg-hover hover:text-ink">copy</button>
                <button type="button" onClick={() => setSecret(null)} className="rp-focus shrink-0 rounded-skin-chip px-2 py-1 font-mono text-[10px] text-faint transition-colors hover:text-ink">dismiss</button>
              </div>
            </div>
          ) : null}
          <p className="mt-2 font-mono text-[9.5px] leading-relaxed text-faint">
            Use it as a bearer token: <code className="text-muted">Authorization: Bearer rp_…</code>. Scoped to this org with the chosen role; it cannot access the platform-admin console or manage tokens.
          </p>
        </section>
      )}

      {tokens === null ? (
        <div className="flex items-center gap-2 py-3 text-muted"><Spinner /> <span className="font-mono text-[11px]">loading…</span></div>
      ) : tokens.length === 0 ? (
        <p className="py-2 font-mono text-[11px] text-faint">No API tokens yet.</p>
      ) : (
        <div className="divide-y divide-line overflow-hidden rounded-skin border border-line">
          {tokens.map((t) => {
            const c = chip(t);
            const dim = t.status !== 'active';
            return (
              <div key={t.id} className="flex items-center gap-3 bg-raised px-3 py-2.5" style={dim ? { opacity: 0.6 } : undefined}>
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-1.5">
                    <span className="truncate font-mono text-[11.5px] text-ink">{t.name}</span>
                    <span className="shrink-0 rounded-skin-chip px-1 py-px font-mono text-[8.5px] uppercase tracking-[0.05em]" style={{ color: t.role === 'admin' ? 'var(--rp-accent)' : 'var(--rp-ink-muted)', background: 'var(--rp-tone-neutral-bg)' }}>{t.role}</span>
                    <span className="shrink-0 rounded-skin-chip px-1 py-px font-mono text-[8.5px] uppercase tracking-[0.05em]" style={{ color: c.fg, background: c.bg }}>{c.label}</span>
                  </div>
                  <div className="mt-0.5 flex items-center gap-2 font-mono text-[9.5px] text-faint">
                    <code className="text-muted">{t.prefix}…</code>
                    <span>·</span>
                    <span>{t.lastUsedAt ? `last used ${relTime(t.lastUsedAt)}` : 'never used'}</span>
                    <span>·</span>
                    <span>created {relTime(t.createdAt)}{t.createdByName ? ` by ${t.createdByName}` : ''}</span>
                    {t.expiresAt ? <><span>·</span><span>{expiryLabel(t.expiresAt)}</span></> : null}
                  </div>
                </div>
                {isAdmin ? (
                  <div className="flex shrink-0 items-center gap-1.5">
                    {t.status === 'active' ? (
                      <button type="button" onClick={() => revokeAPIToken(orgId, t.id).then(load)} className="rp-focus rounded-skin-chip border border-line px-2 py-0.5 font-mono text-[10px] transition-colors hover:bg-hover" style={{ color: 'var(--rp-tone-red-fg)' }}>revoke</button>
                    ) : null}
                    <button type="button" onClick={() => deleteAPIToken(orgId, t.id).then(load)} className="rp-focus rounded-skin-chip border border-line px-2 py-0.5 font-mono text-[10px] text-faint transition-colors hover:bg-hover hover:text-ink">delete</button>
                  </div>
                ) : null}
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

// AccountTab — self-service password change for local (email+password) accounts.
// Complements the `reset-password` CLI (locked-out recovery). SSO-only users can
// set an initial password by leaving the current-password field blank.
function AccountTab() {
  const [cur, setCur] = useState('');
  const [next, setNext] = useState('');
  const [confirm, setConfirm] = useState('');
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);

  const submit = async () => {
    setMsg(null);
    setErr(null);
    if (next.length < 8) { setErr('New password must be at least 8 characters.'); return; }
    if (next !== confirm) { setErr('New password and confirmation do not match.'); return; }
    setBusy(true);
    try {
      await changePassword(cur, next);
      setMsg('Password updated.');
      setCur(''); setNext(''); setConfirm('');
    } catch (e) {
      setErr(e instanceof ApiError ? String(e.message) : 'Could not update password.');
    }
    setBusy(false);
  };

  return (
    <div className="max-w-[440px] space-y-3">
      <section className="rounded-skin border border-line bg-raised p-4" style={{ boxShadow: 'var(--rp-rim)' }}>
        <p className="rp-micro !text-[10px] mb-1">change password</p>
        <p className="mb-3 font-mono text-[10.5px] leading-relaxed text-faint">
          For local (email + password) accounts. If you signed in with SSO and have no password yet, leave “current password” blank to set one.
        </p>
        <label className="block font-mono text-[10px] uppercase tracking-[0.05em] text-muted">Current password</label>
        <input type="password" autoComplete="current-password" value={cur} onChange={(e) => setCur(e.target.value)}
          className="rp-focus mt-1 h-9 w-full rounded-skin-sm border border-line bg-inset px-3 font-mono text-[12px] text-ink" />
        <label className="mt-3 block font-mono text-[10px] uppercase tracking-[0.05em] text-muted">New password</label>
        <input type="password" autoComplete="new-password" value={next} onChange={(e) => setNext(e.target.value)}
          className="rp-focus mt-1 h-9 w-full rounded-skin-sm border border-line bg-inset px-3 font-mono text-[12px] text-ink" />
        <label className="mt-3 block font-mono text-[10px] uppercase tracking-[0.05em] text-muted">Confirm new password</label>
        <input type="password" autoComplete="new-password" value={confirm} onChange={(e) => setConfirm(e.target.value)}
          onKeyDown={(e) => { if (e.key === 'Enter') submit(); }}
          className="rp-focus mt-1 h-9 w-full rounded-skin-sm border border-line bg-inset px-3 font-mono text-[12px] text-ink" />
        {err ? <p className="mt-2 font-mono text-[10.5px]" style={{ color: 'var(--rp-tone-red-fg)' }}>{err}</p> : null}
        {msg ? <p className="mt-2 font-mono text-[10.5px]" style={{ color: 'var(--rp-tone-green-fg)' }}>✓ {msg}</p> : null}
        <button type="button" disabled={busy || !next || !confirm} onClick={submit}
          className="rp-focus mt-4 h-9 rounded-skin-sm px-4 font-mono text-[11.5px] font-semibold transition-opacity hover:opacity-90 disabled:opacity-40"
          style={{ background: 'var(--rp-btn-bg)', color: 'var(--rp-btn-fg)' }}>
          {busy ? 'updating…' : 'Update password'}
        </button>
      </section>
    </div>
  );
}
