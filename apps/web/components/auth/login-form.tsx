'use client';

import { useState } from 'react';
import { useRouter } from 'next/navigation';
import { DEMO_CREDENTIALS } from '@/lib/auth/demo';
import { ArrowRight, Github } from '@/components/icons';

export function LoginForm({ next }: { next: string }) {
  const router = useRouter();
  const [email, setEmail] = useState<string>(DEMO_CREDENTIALS.email);
  const [password, setPassword] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  async function submit(creds: { email: string; password: string }) {
    setLoading(true);
    setError(null);
    try {
      const res = await fetch('/api/auth/login', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(creds),
      });
      if (!res.ok) {
        const body = (await res.json().catch(() => null)) as { error?: string } | null;
        setError(body?.error ?? 'Login fehlgeschlagen');
        return;
      }
      router.push(next || '/explore');
      router.refresh();
    } catch {
      setError('Netzwerkfehler — läuft der Server?');
    } finally {
      setLoading(false);
    }
  }

  return (
    <div className="rounded-xl border border-line bg-raised p-6 shadow-card">
      <h1 className="font-display text-[20px] font-semibold tracking-tight text-strong">Sign in</h1>
      <p className="mt-1 text-[13px] text-muted">Welcome back. Continue to your observability workspace.</p>

      {/* OAuth-Platzhalter (echte OIDC-Anbindung folgt) */}
      <div className="mt-5 grid grid-cols-2 gap-2">
        {[
          { label: 'GitHub', icon: <Github className="h-4 w-4" /> },
          { label: 'Google', icon: <span className="font-display text-[13px] font-bold">G</span> },
        ].map((p) => (
          <button
            key={p.label}
            type="button"
            disabled
            title="bald verfügbar"
            className="flex cursor-not-allowed items-center justify-center gap-2 rounded-lg border border-line bg-base px-3 py-2 text-[13px] text-faint"
          >
            {p.icon}
            {p.label}
          </button>
        ))}
      </div>

      <div className="my-5 flex items-center gap-3">
        <span className="h-px flex-1 bg-line" />
        <span className="font-mono text-[10px] uppercase tracking-wider text-faint">or</span>
        <span className="h-px flex-1 bg-line" />
      </div>

      <form
        onSubmit={(e) => {
          e.preventDefault();
          void submit({ email, password });
        }}
        className="space-y-3"
      >
        <label className="block">
          <span className="mb-1 block font-mono text-[11px] uppercase tracking-wider text-faint">Email</span>
          <input
            type="email"
            autoComplete="username"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            className="w-full rounded-lg border border-line bg-base px-3 py-2 text-[14px] text-strong outline-none transition-colors placeholder:text-faint focus:border-accent"
            placeholder="you@company.com"
          />
        </label>
        <label className="block">
          <span className="mb-1 block font-mono text-[11px] uppercase tracking-wider text-faint">Password</span>
          <input
            type="password"
            autoComplete="current-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            className="w-full rounded-lg border border-line bg-base px-3 py-2 text-[14px] text-strong outline-none transition-colors placeholder:text-faint focus:border-accent"
            placeholder="••••••••"
          />
        </label>

        {error && <p className="text-[12px] text-status-critical">{error}</p>}

        <button
          type="submit"
          disabled={loading}
          className="flex w-full items-center justify-center gap-2 rounded-lg bg-accent px-4 py-2.5 text-[14px] font-medium text-[#04140f] transition-[filter] hover:brightness-110 disabled:opacity-60"
        >
          {loading ? 'Signing in…' : 'Sign in'}
          {!loading && <ArrowRight className="h-4 w-4" />}
        </button>
      </form>

      <button
        type="button"
        onClick={() => void submit(DEMO_CREDENTIALS)}
        disabled={loading}
        className="mt-3 w-full rounded-lg border border-line bg-base px-4 py-2.5 text-[13px] text-muted transition-colors hover:text-strong disabled:opacity-60"
      >
        Continue with the demo workspace
      </button>

      <p className="mt-4 text-center font-mono text-[11px] text-faint">
        demo · {DEMO_CREDENTIALS.email} / {DEMO_CREDENTIALS.password}
      </p>
    </div>
  );
}
