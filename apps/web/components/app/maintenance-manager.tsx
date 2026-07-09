'use client';

import { useCallback, useEffect, useState } from 'react';
import { cn } from '@/lib/cn';
import { Spinner } from '@/components/ui';
import {
  createMaintenanceWindow,
  deleteMaintenanceWindow,
  getMaintenanceWindows,
} from '@/lib/api/controlplane';
import type { MaintenanceWindow } from '@/lib/api/types';

// MaintenanceManager — schedule maintenance windows that suppress alert
// notifications + auto-incident declaration for a cluster (optionally a single
// namespace) during a time range. Modal overlay, RETICLE-compliant.

// datetime-local value (local time, no tz) → RFC3339 with the browser's offset.
function toRFC3339(local: string): string {
  const d = new Date(local);
  return isNaN(d.getTime()) ? '' : d.toISOString();
}
function defaultRange(): { start: string; end: string } {
  const now = new Date();
  const pad = (n: number) => String(n).padStart(2, '0');
  const fmt = (d: Date) =>
    `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
  const end = new Date(now.getTime() + 60 * 60 * 1000);
  return { start: fmt(now), end: fmt(end) };
}

const STATUS_CHIP: Record<MaintenanceWindow['status'], { fg: string; bg: string }> = {
  active: { fg: 'var(--rp-tone-yellow-fg)', bg: 'var(--rp-tone-yellow-bg)' },
  scheduled: { fg: 'var(--rp-tone-blue-fg)', bg: 'var(--rp-tone-blue-bg)' },
  ended: { fg: 'var(--rp-ink-muted)', bg: 'var(--rp-tone-neutral-bg)' },
};

function fmtWhen(iso: string): string {
  const d = new Date(iso);
  if (isNaN(d.getTime())) return '';
  return d.toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
}

export function MaintenanceManager({
  orgId,
  clusterId,
  canManage,
  onClose,
  onChange,
}: {
  orgId: string;
  clusterId: string;
  canManage: boolean;
  onClose: () => void;
  onChange?: () => void;
}) {
  const [windows, setWindows] = useState<MaintenanceWindow[] | null>(null);
  const [title, setTitle] = useState('');
  const [ns, setNs] = useState('');
  const initial = defaultRange();
  const [start, setStart] = useState(initial.start);
  const [end, setEnd] = useState(initial.end);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const load = useCallback(() => {
    getMaintenanceWindows(orgId, clusterId).then((r) => setWindows(r.windows)).catch(() => setWindows([]));
  }, [orgId, clusterId]);
  useEffect(load, [load]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose(); };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

  const create = async () => {
    setErr(null);
    if (!title.trim()) { setErr('Title is required.'); return; }
    const s = toRFC3339(start);
    const e = toRFC3339(end);
    if (!s || !e || new Date(e) <= new Date(s)) { setErr('End must be after start.'); return; }
    setBusy(true);
    try {
      await createMaintenanceWindow(orgId, clusterId, { title: title.trim(), scopeNamespace: ns.trim(), startsAt: s, endsAt: e });
      setTitle(''); setNs('');
      load();
      onChange?.();
    } catch {
      setErr('Failed to create maintenance window.');
    }
    setBusy(false);
  };

  const remove = async (id: string) => {
    await deleteMaintenanceWindow(orgId, clusterId, id).then(load).catch(() => {});
    onChange?.();
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 px-4" onClick={onClose}>
      <div
        className="flex max-h-[86vh] w-[min(600px,100%)] flex-col rounded-skin border border-line bg-raised"
        style={{ boxShadow: 'var(--rp-rim), var(--rp-shadow-pop)' }}
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between border-b border-line px-5 py-3.5">
          <div>
            <div className="rp-micro !text-[10px] text-faint">respond / maintenance</div>
            <h2 className="font-display text-[18px] font-bold tracking-tightest text-ink">Maintenance windows</h2>
          </div>
          <button type="button" onClick={onClose} className="rp-focus grid h-7 w-7 place-items-center rounded-skin-sm text-muted transition-colors hover:bg-hover hover:text-ink" aria-label="close">✕</button>
        </div>

        <div className="min-h-0 flex-1 overflow-y-auto px-5 py-4">
          {canManage ? (
            <section className="rounded-skin-sm border border-line bg-inset p-3">
              <p className="rp-micro !text-[10px] mb-2 text-faint">schedule a window</p>
              <input value={title} onChange={(e) => setTitle(e.target.value)} placeholder="e.g. payments deploy" className="rp-focus h-9 w-full rounded-skin-sm border border-line bg-raised px-3 font-mono text-[12px] text-ink placeholder:text-faint" />
              <div className="mt-2 flex flex-wrap gap-2">
                <input value={ns} onChange={(e) => setNs(e.target.value)} placeholder="namespace (blank = whole cluster)" className="rp-focus h-8 min-w-[180px] flex-1 rounded-skin-sm border border-line bg-raised px-2.5 font-mono text-[11px] text-ink placeholder:text-faint" />
              </div>
              <div className="mt-2 flex flex-wrap gap-2">
                <label className="flex flex-col gap-0.5">
                  <span className="font-mono text-[9px] uppercase tracking-[0.05em] text-faint">start</span>
                  <input type="datetime-local" value={start} onChange={(e) => setStart(e.target.value)} className="rp-focus h-8 rounded-skin-sm border border-line bg-raised px-2 font-mono text-[11px] text-ink" />
                </label>
                <label className="flex flex-col gap-0.5">
                  <span className="font-mono text-[9px] uppercase tracking-[0.05em] text-faint">end</span>
                  <input type="datetime-local" value={end} onChange={(e) => setEnd(e.target.value)} className="rp-focus h-8 rounded-skin-sm border border-line bg-raised px-2 font-mono text-[11px] text-ink" />
                </label>
                <button type="button" disabled={busy || !title.trim()} onClick={create} className="rp-focus mt-auto h-8 rounded-skin-sm px-3.5 font-mono text-[11px] font-semibold transition-opacity hover:opacity-90 disabled:opacity-40" style={{ background: 'var(--rp-btn-bg)', color: 'var(--rp-btn-fg)' }}>
                  {busy ? 'scheduling…' : 'Schedule'}
                </button>
              </div>
              {err ? <p className="mt-2 font-mono text-[10.5px]" style={{ color: 'var(--rp-tone-red-fg)' }}>{err}</p> : null}
            </section>
          ) : (
            <p className="rounded-skin-sm px-3 py-2 font-mono text-[10.5px]" style={{ color: 'var(--rp-tone-yellow-fg)', background: 'var(--rp-tone-yellow-bg)' }}>
              Maintenance windows are managed by org admins.
            </p>
          )}

          <div className="mt-4">
            <div className="rp-micro pb-2 text-faint">windows</div>
            {windows === null ? (
              <div className="flex items-center gap-2 py-3 text-muted"><Spinner /> <span className="font-mono text-[11px]">loading…</span></div>
            ) : windows.length === 0 ? (
              <p className="py-2 font-mono text-[10.5px] text-faint">No maintenance windows.</p>
            ) : (
              <div className="divide-y divide-line overflow-hidden rounded-skin border border-line">
                {windows.map((wnd) => {
                  const c = STATUS_CHIP[wnd.status];
                  return (
                    <div key={wnd.id} className="flex items-center gap-3 bg-raised px-3 py-2.5" style={wnd.status === 'ended' ? { opacity: 0.6 } : undefined}>
                      <span className={cn('shrink-0 rounded-skin-chip px-1.5 py-px font-mono text-[8.5px] uppercase tracking-[0.05em]')} style={{ color: c.fg, background: c.bg }}>{wnd.status}</span>
                      <div className="min-w-0 flex-1">
                        <div className="truncate font-mono text-[11.5px] text-ink">{wnd.title}</div>
                        <div className="font-mono text-[9.5px] text-faint">
                          {wnd.scopeNamespace ? `ns:${wnd.scopeNamespace}` : 'whole cluster'} · {fmtWhen(wnd.startsAt)} → {fmtWhen(wnd.endsAt)}
                        </div>
                      </div>
                      {canManage && wnd.status !== 'ended' ? (
                        <button type="button" onClick={() => remove(wnd.id)} className="rp-focus shrink-0 rounded-skin-chip border border-line px-2 py-0.5 font-mono text-[10px] text-muted transition-colors hover:bg-hover hover:text-ink">
                          {wnd.status === 'active' ? 'end now' : 'cancel'}
                        </button>
                      ) : null}
                    </div>
                  );
                })}
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
