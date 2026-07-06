'use client';

import { useEffect, useState, type FormEvent, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { useRouter } from 'next/navigation';
import { cn } from '@/lib/cn';
import { Button, IconButton, Panel, Spinner } from '@/components/ui';
import { ApiError } from '@/lib/api/client';
import { connectCluster } from '@/lib/api/controlplane';
import type { ConnectClusterResponse } from '@/lib/api/types';
import { CommandBox } from './copy-box';

// sessionStorage-Key, unter dem der Install-Command für die Detailseite abgelegt
// wird (Klartext-Token existiert nur einmal → über die Navigation weiterreichen).
export const installCommandKey = (clusterId: string) => `rp_install_${clusterId}`;

function PlusIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" aria-hidden>
      <path d="M12 5v14M5 12h14" stroke="currentColor" strokeWidth="2" strokeLinecap="square" />
    </svg>
  );
}

/** Prominenter Auslöser + Dialog. `children`/`variant` erlauben Reuse im Empty-State. */
export function ConnectClusterButton({
  orgId,
  variant = 'primary',
  className,
  children,
}: {
  orgId: string;
  variant?: 'primary' | 'default';
  className?: string;
  children?: ReactNode;
}) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button variant={variant} onClick={() => setOpen(true)} className={className}>
        <PlusIcon />
        {children ?? 'Connect cluster'}
      </Button>
      {open ? <ConnectClusterDialog orgId={orgId} onClose={() => setOpen(false)} /> : null}
    </>
  );
}

const STEP_HINTS = [
  'Copy the command below.',
  'Run it against the cluster you want to observe (uses your current kubectl context).',
  'The agent enrolls automatically — this page will light up when it connects.',
];

// DialogMasthead — die Masthead-Keyline als Dialog-Header (Signature-Element):
// Mono-Kicker, Archivo-20px-Headline, 1px-Hairline mit Signal-Tick (rp-keyline).
function DialogMasthead({
  kicker,
  title,
  subtitle,
  actions,
}: {
  kicker: string;
  title: string;
  subtitle?: string;
  actions?: ReactNode;
}) {
  return (
    <div className="px-4 pt-3.5">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="rp-micro !text-[10px]">{kicker}</div>
          <h2 className="rp-keyline mt-1.5 pb-2.5 text-[20px] font-bold leading-none tracking-tightest text-ink">
            {title}
          </h2>
        </div>
        {actions ? <div className="shrink-0 pt-0.5">{actions}</div> : null}
      </div>
      {subtitle ? <p className="mt-2 text-[12.5px] leading-relaxed text-mid">{subtitle}</p> : null}
    </div>
  );
}

export function ConnectClusterDialog({ orgId, onClose }: { orgId: string; onClose: () => void }) {
  const router = useRouter();
  const [name, setName] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [created, setCreated] = useState<ConnectClusterResponse | null>(null);

  // Escape schliesst, Body-Scroll sperren.
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') onClose();
    }
    document.addEventListener('keydown', onKey);
    const prev = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    return () => {
      document.removeEventListener('keydown', onKey);
      document.body.style.overflow = prev;
    };
  }, [onClose]);

  async function submit(e: FormEvent) {
    e.preventDefault();
    const value = name.trim();
    if (!value || busy) return;
    setBusy(true);
    setError(null);
    try {
      const res = await connectCluster(orgId, value);
      try {
        sessionStorage.setItem(installCommandKey(res.cluster.id), res.installCommand);
      } catch {
        /* sessionStorage kann fehlen — Command bleibt im Dialog sichtbar */
      }
      setCreated(res);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Could not create cluster');
    } finally {
      setBusy(false);
    }
  }

  function openCluster() {
    if (!created) return;
    router.push(`/clusters/${created.cluster.id}`);
    onClose();
  }

  const closeButton = (
    <IconButton label="Close" onClick={onClose}>
      <svg width="15" height="15" viewBox="0 0 24 24" fill="none" aria-hidden>
        <path d="M6 6l12 12M18 6L6 18" stroke="currentColor" strokeWidth="1.8" strokeLinecap="square" />
      </svg>
    </IconButton>
  );

  // Portal auf body: Vorfahren mit backdrop-filter (Topbar) würden sonst zum
  // Containing-Block für position:fixed und der Dialog klebte im Header.
  return createPortal(
    <div
      className="fixed inset-0 z-50 flex items-center justify-center p-4"
      role="dialog"
      aria-modal="true"
      aria-label="Connect cluster"
    >
      {/* Scrim — flach, kein Blur */}
      <button
        type="button"
        aria-label="Close"
        onClick={onClose}
        className="absolute inset-0 cursor-default"
        style={{ backgroundColor: 'var(--rp-scrim)' }}
      />

      <Panel className="reveal relative w-full max-w-md shadow-pop">
        {created ? (
          <>
            <DialogMasthead
              kicker={`connect cluster / 02 · ${created.cluster.name}`}
              title="Run the command"
              actions={closeButton}
            />
            <div className="space-y-4 p-4">
              <ol className="space-y-3">
                {STEP_HINTS.map((hint, i) => (
                  <li key={i} className="flex gap-3">
                    <span className="rp-label font-mono text-[11px] text-accent">
                      {String(i + 1).padStart(2, '0')}
                    </span>
                    <span className="text-[12.5px] leading-relaxed text-mid">{hint}</span>
                  </li>
                ))}
              </ol>

              <CommandBox command={created.installCommand} />

              <div className="flex items-center gap-2 rounded-skin border border-line bg-inset px-3 py-2 font-mono text-[11px] text-muted">
                <Spinner />
                Waiting for the agent to enroll…
              </div>

              <div className="flex justify-end gap-2 pt-1">
                <Button variant="ghost" onClick={onClose}>
                  Later
                </Button>
                <Button variant="primary" onClick={openCluster}>
                  Open cluster
                </Button>
              </div>
            </div>
          </>
        ) : (
          <>
            <DialogMasthead
              kicker="connect cluster / 01"
              title="Connect a cluster"
              subtitle="Give it a recognizable name."
              actions={closeButton}
            />
            <form onSubmit={submit} className="space-y-4 p-4">
              <div className="space-y-1.5">
                <label htmlFor="cluster-name" className="rp-micro block !text-[10px]">
                  Cluster name
                </label>
                <input
                  id="cluster-name"
                  autoFocus
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder="production-eu, staging, minikube…"
                  className={cn(
                    'h-10 w-full rounded-skin-sm border bg-inset px-3 font-mono text-[13px] text-ink outline-none transition-colors placeholder:text-faint',
                    'focus:border-ink rp-focus',
                    error ? 'border-red' : 'border-line',
                  )}
                />
                <p className="text-[12px] leading-relaxed text-muted">
                  Just a label for you — the real identity is the cluster&apos;s{' '}
                  <code className="rounded-skin-chip bg-inset px-1 font-mono text-[11px] text-mid">
                    kube-system
                  </code>{' '}
                  UID, detected on enroll.
                </p>
                {error ? <p className="font-mono text-[11px] text-red">{error}</p> : null}
              </div>

              <div className="flex justify-end gap-2">
                <Button type="button" variant="ghost" onClick={onClose}>
                  Cancel
                </Button>
                <Button type="submit" variant="primary" disabled={busy || !name.trim()}>
                  {busy ? <Spinner /> : null}
                  Create &amp; get command
                </Button>
              </div>
            </form>
          </>
        )}
      </Panel>
    </div>,
    document.body,
  );
}
