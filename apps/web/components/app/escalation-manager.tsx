'use client';

import { useCallback, useEffect, useState } from 'react';
import { cn } from '@/lib/cn';
import { Spinner } from '@/components/ui';
import {
  createEscalationPolicy,
  deleteEscalationPolicy,
  getAlertProviders,
  getClusterEscalation,
  getEscalationPolicies,
  setClusterEscalation,
  updateEscalationPolicy,
} from '@/lib/api/controlplane';
import type { AlertProvider, EscalationPolicy, EscalationStep } from '@/lib/api/types';

// EscalationManager — org-weite Eskalations-Policies verwalten und die
// Default-Policy dieses Clusters wählen. Eine Policy ist eine geordnete Kette
// von Notification-Schritten (afterMinutes + Provider-Auswahl). Reused die
// bestehenden Alert-Provider als Kanäle. Modal-Overlay, RETICLE-konform.

export function EscalationManager({
  orgId,
  clusterId,
  onClose,
}: {
  orgId: string;
  clusterId: string;
  onClose: () => void;
}) {
  const [providers, setProviders] = useState<AlertProvider[]>([]);
  const [policies, setPolicies] = useState<EscalationPolicy[] | null>(null);
  const [defaultId, setDefaultId] = useState<string | null>(null);
  const [editing, setEditing] = useState<string | 'new' | null>(null);
  const [err, setErr] = useState<string | null>(null);

  const load = useCallback(() => {
    getEscalationPolicies(orgId).then((r) => setPolicies(r.policies)).catch(() => setPolicies([]));
    getClusterEscalation(orgId, clusterId).then((r) => setDefaultId(r.policyId)).catch(() => {});
  }, [orgId, clusterId]);

  useEffect(() => {
    getAlertProviders(orgId).then((r) => setProviders(r.providers)).catch(() => setProviders([]));
    load();
  }, [orgId, load]);

  const pickDefault = async (id: string | null) => {
    setDefaultId(id);
    try {
      await setClusterEscalation(orgId, clusterId, id);
    } catch {
      setErr('Failed to set default policy.');
    }
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 px-4" onClick={onClose}>
      <div
        className="flex max-h-[86vh] w-[min(620px,100%)] flex-col rounded-skin border border-line bg-raised"
        style={{ boxShadow: 'var(--rp-rim), var(--rp-shadow-pop)' }}
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between border-b border-line px-5 py-3.5">
          <div>
            <div className="rp-micro !text-[10px] text-faint">respond / on-call</div>
            <h2 className="font-display text-[18px] font-bold tracking-tightest text-ink">Escalation policies</h2>
          </div>
          <button
            type="button"
            onClick={onClose}
            className="rp-focus grid h-7 w-7 place-items-center rounded-skin-sm text-muted transition-colors hover:bg-hover hover:text-ink"
            aria-label="close"
          >
            ✕
          </button>
        </div>

        <div className="min-h-0 flex-1 overflow-y-auto px-5 py-4">
          {/* Cluster default */}
          <div className="flex items-center justify-between gap-3 rounded-skin-sm border border-line bg-inset px-3 py-2.5">
            <div className="min-w-0">
              <div className="font-mono text-[11px] text-ink">Default for this cluster</div>
              <div className="font-mono text-[9.5px] text-faint">new incidents here page along this policy</div>
            </div>
            <select
              value={defaultId ?? ''}
              onChange={(e) => pickDefault(e.target.value || null)}
              className="rp-focus h-8 max-w-[220px] rounded-skin-sm border border-line bg-raised px-2 font-mono text-[11px] text-ink"
            >
              <option value="">none (no auto-paging)</option>
              {(policies ?? []).map((p) => (
                <option key={p.id} value={p.id}>
                  {p.name}
                </option>
              ))}
            </select>
          </div>

          {err ? <p className="mt-2 font-mono text-[10.5px]" style={{ color: 'var(--rp-tone-red-fg)' }}>{err}</p> : null}

          {providers.length === 0 ? (
            <p className="mt-3 rounded-skin-sm px-3 py-2 font-mono text-[10.5px]" style={{ color: 'var(--rp-tone-yellow-fg)', background: 'var(--rp-tone-yellow-bg)' }}>
              No notification providers yet. Add webhook/Slack/email providers on the Alerts page first — escalation steps notify through them.
            </p>
          ) : null}

          <div className="mt-4 flex items-center justify-between">
            <div className="rp-micro text-faint">policies</div>
            {editing !== 'new' ? (
              <button
                type="button"
                onClick={() => setEditing('new')}
                className="rp-focus font-mono text-[10.5px] text-muted transition-colors hover:text-ink"
              >
                + new policy
              </button>
            ) : null}
          </div>

          <div className="mt-2 space-y-2">
            {editing === 'new' ? (
              <PolicyEditor
                providers={providers}
                onCancel={() => setEditing(null)}
                onSave={async (name, steps) => {
                  await createEscalationPolicy(orgId, { name, steps });
                  setEditing(null);
                  load();
                }}
              />
            ) : null}

            {policies === null ? (
              <div className="flex items-center gap-2 py-3 text-muted">
                <Spinner /> <span className="font-mono text-[11px]">loading…</span>
              </div>
            ) : policies.length === 0 && editing !== 'new' ? (
              <p className="py-2 font-mono text-[10.5px] text-faint">No policies yet.</p>
            ) : (
              policies.map((p) =>
                editing === p.id ? (
                  <PolicyEditor
                    key={p.id}
                    initial={p}
                    providers={providers}
                    onCancel={() => setEditing(null)}
                    onDelete={async () => {
                      await deleteEscalationPolicy(orgId, p.id);
                      setEditing(null);
                      load();
                    }}
                    onSave={async (name, steps) => {
                      await updateEscalationPolicy(orgId, p.id, { name, steps });
                      setEditing(null);
                      load();
                    }}
                  />
                ) : (
                  <PolicyRow
                    key={p.id}
                    policy={p}
                    providers={providers}
                    isDefault={defaultId === p.id}
                    onEdit={() => setEditing(p.id)}
                  />
                ),
              )
            )}
          </div>
        </div>
      </div>
    </div>
  );
}

function providerName(providers: AlertProvider[], id: string): string {
  return providers.find((p) => p.id === id)?.name ?? '?';
}

function PolicyRow({
  policy,
  providers,
  isDefault,
  onEdit,
}: {
  policy: EscalationPolicy;
  providers: AlertProvider[];
  isDefault: boolean;
  onEdit: () => void;
}) {
  return (
    <div className="rounded-skin-sm border border-line bg-inset px-3 py-2.5">
      <div className="flex items-center gap-2">
        <span className="font-mono text-[11.5px] text-ink">{policy.name}</span>
        {isDefault ? (
          <span className="rounded-skin-chip px-1 py-px font-mono text-[8px] uppercase tracking-[0.05em]" style={{ color: 'var(--rp-tone-green-fg)', background: 'var(--rp-tone-green-bg)' }}>
            cluster default
          </span>
        ) : null}
        <button
          type="button"
          onClick={onEdit}
          className="rp-focus ml-auto font-mono text-[10px] text-muted transition-colors hover:text-ink"
        >
          edit
        </button>
      </div>
      <div className="mt-1.5 space-y-0.5">
        {policy.steps.length === 0 ? (
          <span className="font-mono text-[9.5px] text-faint">no steps</span>
        ) : (
          policy.steps.map((s, i) => (
            <div key={i} className="flex items-center gap-2 font-mono text-[9.5px] text-muted">
              <span className="tabular-nums text-faint">{i + 1}.</span>
              <span className="tabular-nums">after {s.afterMinutes}m</span>
              <span className="text-faint">→</span>
              <span className="truncate">{s.providerIds.map((id) => providerName(providers, id)).join(', ') || '(no channel)'}</span>
            </div>
          ))
        )}
      </div>
    </div>
  );
}

function PolicyEditor({
  initial,
  providers,
  onSave,
  onCancel,
  onDelete,
}: {
  initial?: EscalationPolicy;
  providers: AlertProvider[];
  onSave: (name: string, steps: EscalationStep[]) => Promise<void>;
  onCancel: () => void;
  onDelete?: () => Promise<void>;
}) {
  const [name, setName] = useState(initial?.name ?? '');
  const [steps, setSteps] = useState<EscalationStep[]>(
    initial?.steps.length ? initial.steps : [{ afterMinutes: 0, providerIds: [] }],
  );
  const [busy, setBusy] = useState(false);

  const setStep = (i: number, patch: Partial<EscalationStep>) =>
    setSteps((prev) => prev.map((s, j) => (j === i ? { ...s, ...patch } : s)));
  const toggleProvider = (i: number, id: string) =>
    setSteps((prev) =>
      prev.map((s, j) => {
        if (j !== i) return s;
        const has = s.providerIds.includes(id);
        return { ...s, providerIds: has ? s.providerIds.filter((p) => p !== id) : [...s.providerIds, id] };
      }),
    );

  return (
    <div className="rounded-skin-sm border px-3 py-3" style={{ borderColor: 'var(--rp-line-strong)', background: 'var(--rp-inset)' }}>
      <input
        value={name}
        onChange={(e) => setName(e.target.value)}
        autoFocus
        placeholder="policy name (e.g. prod on-call)"
        className="rp-focus h-8 w-full rounded-skin-sm border border-line bg-raised px-2.5 font-mono text-[11.5px] text-ink placeholder:text-faint"
      />
      <div className="mt-2.5 space-y-2">
        {steps.map((s, i) => (
          <div key={i} className="rounded-skin-sm border border-line bg-raised px-2.5 py-2">
            <div className="flex items-center gap-2">
              <span className="font-mono text-[10px] tabular-nums text-faint">step {i + 1}</span>
              <span className="font-mono text-[10px] text-muted">after</span>
              <input
                type="number"
                min={0}
                value={s.afterMinutes}
                onChange={(e) => setStep(i, { afterMinutes: Math.max(0, parseInt(e.target.value || '0', 10)) })}
                className="rp-focus h-6 w-14 rounded-skin-sm border border-line bg-inset px-1.5 text-center font-mono text-[10.5px] tabular-nums text-ink"
              />
              <span className="font-mono text-[10px] text-muted">min</span>
              {steps.length > 1 ? (
                <button
                  type="button"
                  onClick={() => setSteps((prev) => prev.filter((_, j) => j !== i))}
                  className="rp-focus ml-auto font-mono text-[10px] text-faint transition-colors hover:text-red"
                >
                  remove
                </button>
              ) : null}
            </div>
            <div className="mt-1.5 flex flex-wrap gap-1">
              {providers.map((prov) => {
                const on = s.providerIds.includes(prov.id);
                return (
                  <button
                    key={prov.id}
                    type="button"
                    onClick={() => toggleProvider(i, prov.id)}
                    className={cn(
                      'rp-focus rounded-skin-chip border px-1.5 py-0.5 font-mono text-[9.5px] transition-colors',
                      on ? 'border-line-strong text-ink' : 'border-line text-muted hover:text-ink',
                    )}
                    style={on ? { background: 'var(--rp-hover)' } : undefined}
                  >
                    {on ? '✓ ' : ''}
                    {prov.name}
                    <span className="text-faint"> · {prov.type}</span>
                  </button>
                );
              })}
            </div>
          </div>
        ))}
      </div>
      <button
        type="button"
        onClick={() => setSteps((prev) => [...prev, { afterMinutes: 5, providerIds: [] }])}
        className="rp-focus mt-2 font-mono text-[10px] text-muted transition-colors hover:text-ink"
      >
        + add step
      </button>

      <div className="mt-3 flex items-center gap-2">
        <button
          type="button"
          disabled={!name.trim() || busy}
          onClick={async () => {
            setBusy(true);
            try {
              await onSave(name.trim(), steps);
            } finally {
              setBusy(false);
            }
          }}
          className="rp-focus h-7 rounded-skin-sm px-3 font-mono text-[10.5px] font-semibold disabled:opacity-50"
          style={{ background: 'var(--rp-btn-bg)', color: 'var(--rp-btn-fg)' }}
        >
          {busy ? 'saving…' : 'Save policy'}
        </button>
        <button
          type="button"
          onClick={onCancel}
          className="rp-focus h-7 rounded-skin-sm border border-line px-3 font-mono text-[10.5px] text-mid hover:bg-hover"
        >
          Cancel
        </button>
        {onDelete ? (
          <button
            type="button"
            onClick={async () => {
              setBusy(true);
              try {
                await onDelete();
              } finally {
                setBusy(false);
              }
            }}
            className="rp-focus ml-auto h-7 rounded-skin-sm px-3 font-mono text-[10.5px] transition-colors hover:bg-tone-red-bg"
            style={{ color: 'var(--rp-tone-red-fg)' }}
          >
            Delete
          </button>
        ) : null}
      </div>
    </div>
  );
}
