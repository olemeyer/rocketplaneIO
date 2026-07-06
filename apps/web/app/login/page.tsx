'use client';

import { useEffect, useState } from 'react';
import { useRouter } from 'next/navigation';
import { AuthScaffold, NumField, GoogleGlyph } from '@/components/auth/auth-scaffold';
import { Spinner } from '@/components/ui';

// Login. Lokaler Account (E-Mail + Passwort) ist der Default-Weg; „Continue with
// Google" erscheint NUR, wenn die Control-Plane SSO konfiguriert hat. Ist die
// Instanz noch nicht eingerichtet, geht es zum First-User-Setup.
export default function LoginPage() {
  const router = useRouter();
  const [checking, setChecking] = useState(true);
  const [googleEnabled, setGoogleEnabled] = useState(false);
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
        if (s.needsSetup) {
          router.replace('/setup');
          return;
        }
        setGoogleEnabled(Boolean(s.googleEnabled));
        setChecking(false);
      })
      .catch(() => active && setChecking(false));
    return () => {
      active = false;
    };
  }, [router]);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setSubmitting(true);
    try {
      const res = await fetch('/auth/login', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({ email, password }),
      });
      if (!res.ok) {
        const body = await res.json().catch(() => ({}));
        setError(body.error ?? 'Sign in failed.');
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
      <AuthScaffold title="Sign in" subtitle="Checking this instance…">
        <div className="flex items-center justify-center py-8">
          <Spinner className="h-5 w-5" />
        </div>
      </AuthScaffold>
    );
  }

  return (
    <AuthScaffold
      title="Sign in"
      subtitle="Monitor and operate Kubernetes — zero setup."
      footer={
        <>
          By continuing you agree to the{' '}
          <a href="#" className="text-accent underline underline-offset-2 hover:text-accent-hover">Terms</a>
          {' '}&amp;{' '}
          <a href="#" className="text-accent underline underline-offset-2 hover:text-accent-hover">Privacy Policy</a>.
        </>
      }
    >
      <form onSubmit={submit} className="space-y-5">
        <NumField
          n="01"
          label="Email"
          type="email"
          autoComplete="email"
          placeholder="you@company.com"
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          required
        />
        <NumField
          n="02"
          label="Password"
          type="password"
          autoComplete="current-password"
          placeholder="Enter password"
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
              <span>Sign in</span>
              <span aria-hidden>→</span>
            </>
          )}
        </button>
      </form>

      {googleEnabled ? (
        <>
          <div className="my-6 flex items-center gap-3">
            <span className="h-px flex-1 bg-line" />
            <span className="font-mono text-[10px] uppercase tracking-[0.14em] text-faint">or</span>
            <span className="h-px flex-1 bg-line" />
          </div>
          <a
            href="/auth/google/start"
            className="rp-focus inline-flex h-11 w-full items-center justify-center gap-2.5 rounded-skin-sm border border-line bg-transparent font-mono text-[12px] text-ink transition-colors hover:bg-hover"
          >
            <GoogleGlyph />
            Continue with Google
          </a>
        </>
      ) : null}
    </AuthScaffold>
  );
}
