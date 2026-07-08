'use client';

import { useEffect, useState } from 'react';
import { useParams, useRouter } from 'next/navigation';
import { Spinner } from '@/components/ui';
import { useMe } from '@/components/app/me-context';
import { acceptInvitation, previewInvitation, switchOrg } from '@/lib/api/controlplane';
import type { OrgRole } from '@/lib/api/types';
import { ApiError } from '@/lib/api/client';

// Invite accept — a signed-in user lands here from an invite link. We preview
// which org + role the token grants, then accept (joins the org and switches
// their session to it). If the token is invalid/expired the page says so.

export default function InviteAcceptPage() {
  const { token } = useParams<{ token: string }>();
  const router = useRouter();
  const { me, refresh } = useMe();
  const [preview, setPreview] = useState<{ orgName: string; email: string; role: OrgRole } | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    previewInvitation(token)
      .then(setPreview)
      .catch((e) => setErr(e instanceof ApiError && e.status === 404 ? 'This invitation is invalid or has expired.' : 'Could not load the invitation.'));
  }, [token]);

  const accept = async () => {
    setBusy(true); setErr(null);
    try {
      const r = await acceptInvitation(token);
      await switchOrg(r.orgId).catch(() => {});
      await refresh();
      router.replace('/');
    } catch (e) {
      setErr(e instanceof ApiError ? String(e.message) : 'Could not accept the invitation.');
      setBusy(false);
    }
  };

  return (
    <div className="flex min-h-[calc(100dvh-52px)] items-center justify-center px-4">
      <div className="w-[min(440px,100%)] rounded-skin border border-line bg-raised p-6" style={{ boxShadow: 'var(--rp-rim), var(--rp-shadow-pop)' }}>
        <div className="rp-micro !text-[10px] text-faint">team invitation</div>
        {err ? (
          <>
            <h1 className="mt-2 font-display text-[20px] font-bold tracking-tightest text-ink">Invitation unavailable</h1>
            <p className="mt-2 font-mono text-[11.5px] leading-relaxed text-muted">{err}</p>
            <button type="button" onClick={() => router.replace('/')} className="rp-focus mt-4 h-9 rounded-skin-sm border border-line px-4 font-mono text-[11.5px] text-ink transition-colors hover:bg-hover">Go to dashboard</button>
          </>
        ) : !preview ? (
          <div className="mt-4 flex items-center gap-2 text-muted"><Spinner /> <span className="font-mono text-[11px]">loading invitation…</span></div>
        ) : (
          <>
            <h1 className="mt-2 font-display text-[22px] font-bold leading-tight tracking-tightest text-ink">Join {preview.orgName}</h1>
            <p className="mt-2 font-mono text-[11.5px] leading-relaxed text-muted">
              You&apos;ve been invited to <span className="text-ink">{preview.orgName}</span> as{' '}
              <span className="font-semibold" style={{ color: preview.role === 'owner' ? 'var(--rp-accent)' : 'var(--rp-ink)' }}>{preview.role}</span>.
            </p>
            {me && me.user.email.toLowerCase() !== preview.email.toLowerCase() ? (
              <p className="mt-2 rounded-skin-sm px-2.5 py-1.5 font-mono text-[10.5px] leading-relaxed" style={{ color: 'var(--rp-tone-yellow-fg)', background: 'var(--rp-tone-yellow-bg)' }}>
                This invite was addressed to {preview.email}, but you&apos;re signed in as {me.user.email}. Accepting will add <span className="font-semibold">your</span> account to the org.
              </p>
            ) : null}
            <div className="mt-5 flex items-center gap-2">
              <button type="button" disabled={busy} onClick={accept} className="rp-focus h-9 flex-1 rounded-skin-sm font-mono text-[12px] font-semibold transition-opacity hover:opacity-90 disabled:opacity-50" style={{ background: 'var(--rp-btn-bg)', color: 'var(--rp-btn-fg)' }}>
                {busy ? 'joining…' : `Accept & join`}
              </button>
              <button type="button" onClick={() => router.replace('/')} className="rp-focus h-9 rounded-skin-sm border border-line px-4 font-mono text-[11.5px] text-mid transition-colors hover:bg-hover hover:text-ink">Decline</button>
            </div>
          </>
        )}
      </div>
    </div>
  );
}
