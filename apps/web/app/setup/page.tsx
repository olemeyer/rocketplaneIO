'use client';

import { useEffect, useState } from 'react';
import { useRouter } from 'next/navigation';
import { AuthScaffold, NumField } from '@/components/auth/auth-scaffold';
import { Spinner } from '@/components/ui';

// First-User-Setup (n8n-Muster): der allererste User legt seinen Owner-Account an —
// ohne SSO-Konfiguration. Ist die Instanz bereits eingerichtet, geht es zu /login.
export default function SetupPage() {
  const router = useRouter();
  const [checking, setChecking] = useState(true);
  const [name, setName] = useState('');
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let active = true;
    fetch('/api/setup/status', { credentials: 'include' })
      .then((r) => r.json())
      .then((s) => {
        if (!active) return;
        if (!s.needsSetup) router.replace('/login');
        else setChecking(false);
      })
      .catch(() => active && setChecking(false));
    return () => {
      active = false;
    };
  }, [router]);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    if (password.length < 8) {
      setError('Password must be at least 8 characters.');
      return;
    }
    setSubmitting(true);
    try {
      const res = await fetch('/api/setup', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({ name, email, password }),
      });
      if (!res.ok) {
        const body = await res.json().catch(() => ({}));
        setError(body.error ?? 'Setup failed.');
        setSubmitting(false);
        return;
      }
      router.replace('/');
      router.refresh();
    } catch {
      setError('Network error.');
      setSubmitting(false);
    }
  }

  if (checking) {
    return (
      <AuthScaffold title="Setup" subtitle="Getting things ready…">
        <div className="flex items-center justify-center py-8">
          <Spinner className="h-5 w-5" />
        </div>
      </AuthScaffold>
    );
  }

  return (
    <AuthScaffold
      title="Create owner account"
      subtitle="First run — create the owner account for this rocketplaneIO instance."
      footer="You’ll be the instance owner. Invite your team afterwards."
    >
      <form onSubmit={submit} className="space-y-5">
        <NumField
          n="01"
          label="Your name"
          type="text"
          autoComplete="name"
          placeholder="Ada Lovelace"
          value={name}
          onChange={(e) => setName(e.target.value)}
          required
        />
        <NumField
          n="02"
          label="Email"
          type="email"
          autoComplete="email"
          placeholder="you@company.com"
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          required
        />
        <NumField
          n="03"
          label="Password"
          hint="min 8 characters"
          type="password"
          autoComplete="new-password"
          placeholder="••••••••"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          required
        />

        {error ? (
          <p className="rounded-skin-sm bg-tone-red-bg px-3 py-2 font-mono text-[11.5px] leading-snug text-tone-red-fg">
            {error}
          </p>
        ) : null}

        <button
          type="submit"
          disabled={submitting}
          className="rp-focus mt-1 flex h-11 w-full items-center justify-between rounded-skin-sm px-4 font-mono text-[12.5px] font-semibold transition-opacity hover:opacity-90"
          style={{ background: 'var(--rp-btn-bg)', color: 'var(--rp-btn-fg)', opacity: submitting ? 0.6 : 1 }}
        >
          {submitting ? (
            <Spinner className="mx-auto h-4 w-4" />
          ) : (
            <>
              <span>Create account</span>
              <span aria-hidden>→</span>
            </>
          )}
        </button>
      </form>
    </AuthScaffold>
  );
}
