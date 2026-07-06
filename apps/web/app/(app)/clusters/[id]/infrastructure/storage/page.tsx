'use client';

import { useMemo } from 'react';
import Link from 'next/link';
import { useParams } from 'next/navigation';
import { Spinner } from '@/components/ui';
import { useMe } from '@/components/app/me-context';
import { PageHeader } from '@/components/app/page-header';
import { useInfra } from '@/lib/hooks/use-infra';
import { Gauge, fmtBytes, usageTone } from '@/components/infra/gauge';
import type { InfraPVC } from '@/lib/api/types';

// Storage — jeder PersistentVolumeClaim mit ECHTER Belegung (kubelet volume
// stats), Phase, Klasse und den Workloads, die ihn mounten. ADAPTIV: wenige
// Claims = Karten mit vollem Usage-Instrument; viele = dichte Zeilen mit
// Mini-Balken. Live über dasselbe SSE-topology-Signal.

const CARD_LIMIT = 12;

const PHASE_CHIP: Record<string, { fg: string; bg: string; glyph: string }> = {
  Bound: { fg: 'var(--rp-tone-green-fg)', bg: 'var(--rp-tone-green-bg)', glyph: '●' },
  Pending: { fg: 'var(--rp-tone-yellow-fg)', bg: 'var(--rp-tone-yellow-bg)', glyph: '◌' },
  Lost: { fg: 'var(--rp-tone-red-fg)', bg: 'var(--rp-tone-red-bg)', glyph: '▲' },
};
const PHASE_FALLBACK = { fg: 'var(--rp-ink-muted)', bg: 'var(--rp-tone-neutral-bg)', glyph: '○' };

export default function StoragePage() {
  const params = useParams<{ id: string }>();
  const clusterId = params.id;
  const { currentOrg } = useMe();
  const { pvcs, live } = useInfra(currentOrg?.id, clusterId);

  const sum = useMemo(() => {
    const s = { used: 0, cap: 0, unbound: 0, unknownUse: 0 };
    for (const c of pvcs ?? []) {
      if (c.usedBytes >= 0) s.used += c.usedBytes;
      else s.unknownUse += 1;
      s.cap += c.capacityBytes > 0 ? c.capacityBytes : c.requestedBytes;
      if (c.phase !== 'Bound') s.unbound += 1;
    }
    return s;
  }, [pvcs]);

  const dense = (pvcs?.length ?? 0) > CARD_LIMIT;

  return (
    <div className="flex h-[calc(100dvh-52px)] flex-col overflow-y-auto px-4 pb-6 pt-4 sm:px-5">
      <PageHeader kicker="infrastructure / storage" title="Storage">
        <div className="flex items-center gap-3 font-mono text-[11px] text-muted tnum">
          {live ? (
            <span className="flex items-center gap-1.5">
              <span
                className="rp-breath inline-block h-1.5 w-1.5 rounded-full"
                style={{ background: 'var(--rp-green)', color: 'var(--rp-green)' }}
              />
              <span className="!text-ink">Live</span>
            </span>
          ) : null}
          <span>{pvcs?.length ?? '…'} claims</span>
          <span title="volume usage is unavailable on some backends (e.g. minikube hostpath)">
            {sum.used === 0 && sum.unknownUse > 0
              ? `${fmtBytes(sum.cap)} · usage n/a`
              : `${fmtBytes(sum.used)} / ${fmtBytes(sum.cap)}`}
          </span>
          {sum.unbound > 0 ? (
            <span style={{ color: 'var(--rp-tone-yellow-fg)' }}>◌ {sum.unbound} unbound</span>
          ) : null}
        </div>
      </PageHeader>

      {pvcs === null ? (
        <div className="flex flex-1 items-center justify-center gap-2 text-muted">
          <Spinner /> <span className="font-mono text-[12px]">reading volumes…</span>
        </div>
      ) : pvcs.length === 0 ? (
        <div
          className="mt-6 rounded-skin border border-dashed p-8 text-center font-mono text-[12px] text-muted"
          style={{ borderColor: 'var(--rp-line-strong)' }}
        >
          No PersistentVolumeClaims in this cluster.
        </div>
      ) : dense ? (
        /* Dichte Zeilen — hunderte Claims scanbar */
        <div className="mt-4 overflow-hidden rounded-skin border border-line" style={{ boxShadow: 'var(--rp-rim)' }}>
          {pvcs.map((c) => {
            const chip = PHASE_CHIP[c.phase] ?? PHASE_FALLBACK;
            const cap = c.capacityBytes > 0 ? c.capacityBytes : c.requestedBytes;
            const frac = c.usedBytes >= 0 && cap > 0 ? Math.min(1, c.usedBytes / cap) : 0;
            const tone = usageTone(frac);
            return (
              <div
                key={`${c.namespace}/${c.name}`}
                className="flex items-center gap-3 border-b border-line/60 px-3 py-1.5 font-mono text-[11px] last:border-0 hover:bg-hover"
              >
                <span
                  className="w-14 shrink-0 rounded-skin-chip px-1.5 py-0.5 text-center text-[9px] uppercase tracking-[0.05em]"
                  style={{ color: chip.fg, background: chip.bg }}
                >
                  {chip.glyph} {c.phase}
                </span>
                <span className="min-w-0 flex-1 truncate text-ink">
                  {c.name} <span className="text-faint">· {c.namespace}</span>
                </span>
                <span className="hidden w-40 shrink-0 md:block">
                  <span className="block h-[4px] overflow-hidden rounded-full bg-inset">
                    <span className="block h-full rounded-full" style={{ width: `${frac * 100}%`, background: tone.bar, opacity: 0.8 }} />
                  </span>
                </span>
                <span className="w-40 shrink-0 text-right text-mid tnum">
                  {c.usedBytes >= 0 ? `${fmtBytes(c.usedBytes)} / ${fmtBytes(cap)}` : fmtBytes(cap)}
                </span>
              </div>
            );
          })}
        </div>
      ) : (
        /* Karten mit vollem Usage-Instrument */
        <div className="mt-4 grid grid-cols-1 gap-3 lg:grid-cols-2 2xl:grid-cols-3">
          {pvcs.map((c) => {
            const chip = PHASE_CHIP[c.phase] ?? PHASE_FALLBACK;
            const cap = c.capacityBytes > 0 ? c.capacityBytes : c.requestedBytes;
            return (
              <div
                key={`${c.namespace}/${c.name}`}
                className="rounded-skin border border-line bg-raised p-4"
                style={{ boxShadow: 'var(--rp-rim)' }}
              >
                <div className="flex items-center gap-2 border-b border-line-strong pb-2.5">
                  <span
                    className="shrink-0 rounded-skin-chip px-1.5 py-0.5 font-mono text-[9px] uppercase tracking-[0.05em]"
                    style={{ color: chip.fg, background: chip.bg }}
                  >
                    {chip.glyph} {c.phase}
                  </span>
                  <h2 className="truncate font-mono text-[13.5px] font-bold tracking-tight text-ink">{c.name}</h2>
                  <span className="ml-auto shrink-0 font-mono text-[10px] text-faint">{c.namespace}</span>
                </div>

                <div className="mt-3">
                  <Gauge
                    label="usage"
                    used={c.usedBytes}
                    total={cap}
                    render={(u, t) => `${fmtBytes(u)} / ${fmtBytes(t)}`}
                    height={8}
                  />
                </div>

                <div className="mt-3 grid grid-cols-2 gap-x-4 gap-y-1 font-mono text-[10.5px]">
                  <div>
                    <span className="text-muted">class </span>
                    <span className="text-ink">{c.storageClass || '—'}</span>
                  </div>
                  <div>
                    <span className="text-muted">modes </span>
                    <span className="text-ink">{c.accessModes.map((m) => m.replace('ReadWrite', 'RW').replace('ReadOnly', 'RO')).join(', ') || '—'}</span>
                  </div>
                  <div className="col-span-2 truncate">
                    <span className="text-muted">volume </span>
                    <span className="text-faint">{c.volumeName || '—'}</span>
                  </div>
                </div>

                {/* Wer hängt dran — direkt zur Map springen */}
                <div className="mt-2.5 flex flex-wrap items-center gap-1.5 border-t border-line pt-2">
                  <span className="rp-micro !text-[9px]">mounted by</span>
                  {c.mountedBy.length === 0 ? (
                    <span className="font-mono text-[10px]" style={{ color: 'var(--rp-tone-yellow-fg)' }}>
                      ◆ nothing — orphaned claim
                    </span>
                  ) : (
                    c.mountedBy.map((w) => (
                      <Link
                        key={w}
                        href={`/clusters/${clusterId}?ns=${encodeURIComponent(c.namespace)}`}
                        className="rounded-skin-chip border border-line px-1.5 py-0.5 font-mono text-[10px] text-mid transition-colors hover:bg-hover hover:text-ink"
                      >
                        {w} →
                      </Link>
                    ))
                  )}
                </div>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
