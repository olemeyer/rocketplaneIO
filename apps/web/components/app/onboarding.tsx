'use client';

import { useState, type FormEvent } from 'react';
import { useRouter } from 'next/navigation';
import { cn } from '@/lib/cn';
import { Spinner } from '@/components/ui';
import { ApiError } from '@/lib/api/client';
import { connectCluster } from '@/lib/api/controlplane';
import { installCommandKey } from './connect-cluster';

// onboarding.tsx — the no-cluster landing. Keep it honest and calm: name the
// first cluster, then jump to its (empty) service map where the connect overlay
// waits for the agent and the REAL nodes draw themselves in. No fake topology.
export function Onboarding({ orgId }: { orgId?: string }) {
  const router = useRouter();
  const [name, setName] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function submit(e: FormEvent) {
    e.preventDefault();
    const value = name.trim();
    if (!value || busy || !orgId) return;
    setBusy(true);
    setError(null);
    try {
      const res = await connectCluster(orgId, value);
      try {
        sessionStorage.setItem(
          installCommandKey(res.cluster.id),
          JSON.stringify({ command: res.installCommand, commands: res.installCommands }),
        );
      } catch {
        /* the command still shows on the cluster page via regenerate */
      }
      router.push(`/clusters/${res.cluster.id}`);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Could not create cluster');
      setBusy(false);
    }
  }

  return (
    <div className="relative flex min-h-[calc(100dvh-52px)] items-center justify-center overflow-hidden px-5">
      <div className="aurora-glow" aria-hidden />
      <div className="swiss-grid pointer-events-none absolute inset-0" aria-hidden />

      <div className="relative w-full max-w-[440px]">
        <div className="rp-micro !text-[10px] text-faint">get started</div>
        <h1 className="mt-2 font-display text-[30px] font-bold leading-[1.05] tracking-tightest text-ink sm:text-[36px]">
          Connect your first cluster.
        </h1>
        <p className="mt-3 text-[13.5px] leading-relaxed text-mid">
          One command against your cluster — then the live service map, traces and Copilot draw themselves in,
          usually in under a minute.
        </p>

        <form
          onSubmit={submit}
          className="mt-6 rounded-skin border border-line bg-raised p-4"
          style={{ boxShadow: 'var(--rp-rim), var(--rp-shadow-pop)' }}
        >
          <label htmlFor="cluster-name" className="rp-micro block !text-[10px] text-faint">
            Cluster name
          </label>
          <input
            id="cluster-name"
            autoFocus
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="production-eu, staging, minikube…"
            className={cn(
              'mt-1.5 h-11 w-full rounded-skin-sm border bg-inset px-3.5 font-mono text-[14px] text-ink outline-none transition-colors placeholder:text-faint rp-focus',
              error ? 'border-red' : 'border-line focus:border-ink',
            )}
          />
          <p className="mt-2 text-[11.5px] leading-relaxed text-muted">
            Just a label — the real identity is the cluster&apos;s{' '}
            <code className="rounded-skin-chip bg-inset px-1 font-mono text-[10.5px] text-mid">kube-system</code> UID,
            detected on enroll.
          </p>
          {error ? <p className="mt-1.5 font-mono text-[11px] text-red">{error}</p> : null}
          <button
            type="submit"
            disabled={busy || !name.trim() || !orgId}
            className="rp-focus mt-4 inline-flex h-10 w-full items-center justify-center gap-2 rounded-skin-sm px-4 font-mono text-[12.5px] font-semibold transition-opacity hover:opacity-90 disabled:opacity-40"
            style={{ background: 'var(--rp-btn-bg)', color: 'var(--rp-btn-fg)' }}
          >
            {busy ? <Spinner className="h-3.5 w-3.5" /> : null}
            Get the install command
            <span aria-hidden>→</span>
          </button>
        </form>

        <div className="mt-4 flex flex-wrap items-center gap-x-2 gap-y-1 font-mono text-[11px] text-muted">
          <span className="inline-flex items-center gap-1.5">
            <span className="h-1.5 w-1.5 rounded-full" style={{ background: 'var(--rp-tone-green-fg)' }} />
            one command
          </span>
          <span className="text-faint">·</span>
          <span>~60s</span>
          <span className="text-faint">·</span>
          <span>outbound-only, no inbound ports</span>
        </div>
      </div>
    </div>
  );
}
