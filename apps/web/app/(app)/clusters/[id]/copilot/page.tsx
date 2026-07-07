'use client';

import { useEffect, useMemo, useState } from 'react';
import { useParams } from 'next/navigation';
import { cn } from '@/lib/cn';
import { Spinner } from '@/components/ui';
import { useMe } from '@/components/app/me-context';
import { PageHeader } from '@/components/app/page-header';
import { CopilotChat, Sparkle } from '@/components/app/copilot';
import { PROVIDER_PRESETS, loadLLMConfig, saveLLMConfig, type LLMConfig, type LLMPreset } from '@/lib/llm';

export default function CopilotPage() {
  const params = useParams<{ id: string }>();
  const clusterId = params.id;
  const { currentOrg } = useMe();
  const orgId = currentOrg?.id;

  const [cfg, setCfg] = useState<LLMConfig | null>(null);
  const [hydrated, setHydrated] = useState(false);
  const [forceSetup, setForceSetup] = useState(false);

  useEffect(() => {
    if (!orgId) return;
    setCfg(loadLLMConfig(orgId));
    setHydrated(true);
  }, [orgId]);

  const configured = !!cfg && !forceSetup;

  return (
    <div className="flex h-[calc(100dvh-52px)] flex-col px-4 pb-4 pt-4 sm:px-5">
      <PageHeader kicker="ai / copilot" title="Copilot" meta={configured ? cfg!.model : 'connect an AI provider'}>
        {configured ? (
          <button
            type="button"
            onClick={() => setForceSetup(true)}
            className="rp-focus rounded-skin-sm border border-line px-2.5 py-1.5 font-mono text-[11px] text-mid transition-colors hover:bg-hover hover:text-ink"
          >
            provider ▸
          </button>
        ) : null}
      </PageHeader>

      <div className="mt-3 flex min-h-0 flex-1 flex-col">
        {!hydrated ? (
          <div className="flex flex-1 items-center justify-center gap-2 text-muted">
            <Spinner /> <span className="font-mono text-[12px]">loading…</span>
          </div>
        ) : configured ? (
          <CopilotChat orgId={orgId ?? ''} clusterId={clusterId} config={cfg!} />
        ) : (
          <LLMSetup
            orgId={orgId ?? ''}
            initial={cfg}
            onDone={(c) => {
              setCfg(c);
              setForceSetup(false);
            }}
            onCancel={cfg ? () => setForceSetup(false) : undefined}
          />
        )}
      </div>
    </div>
  );
}

/* ── LLM-Setup: Provider wählen + verbinden ──────────────────────────────── */
function LLMSetup({
  orgId,
  initial,
  onDone,
  onCancel,
}: {
  orgId: string;
  initial: LLMConfig | null;
  onDone: (cfg: LLMConfig) => void;
  onCancel?: () => void;
}) {
  const [selId, setSelId] = useState<string>(initial?.provider ?? PROVIDER_PRESETS[0]!.id);
  const preset = useMemo(() => PROVIDER_PRESETS.find((p) => p.id === selId) ?? PROVIDER_PRESETS[0]!, [selId]);
  const [baseUrl, setBaseUrl] = useState(initial?.baseUrl ?? preset.baseUrl);
  const [model, setModel] = useState(initial?.model ?? preset.defaultModel);
  const [apiKey, setApiKey] = useState(initial?.apiKey ?? '');

  function pick(p: LLMPreset) {
    setSelId(p.id);
    setBaseUrl(p.baseUrl);
    setModel(p.defaultModel);
  }

  function connect() {
    const cfg: LLMConfig = { provider: preset.id, api: preset.api, baseUrl: baseUrl.trim(), model: model.trim(), apiKey: apiKey.trim() };
    saveLLMConfig(orgId, cfg);
    onDone(cfg);
  }

  const needsKey = !preset.local;
  const canConnect = baseUrl.trim() && model.trim() && (!needsKey || apiKey.trim());

  return (
    <div className="relative min-h-0 flex-1 overflow-y-auto rounded-skin border border-line bg-raised" style={{ boxShadow: 'var(--rp-rim)' }}>
      <div className="pointer-events-none absolute inset-x-0 top-0 h-28" aria-hidden style={{ background: 'radial-gradient(60% 100% at 50% 0%, color-mix(in oklab, var(--rp-accent) 12%, transparent), transparent 70%)' }} />

      <div className="relative mx-auto max-w-[820px] px-5 py-8 sm:px-8">
        <div className="flex items-center gap-2.5">
          <span className="flex h-9 w-9 items-center justify-center rounded-skin-sm border" style={{ borderColor: 'color-mix(in oklab, var(--rp-accent) 40%, var(--rp-line-strong))', background: 'color-mix(in oklab, var(--rp-accent) 12%, transparent)', color: 'var(--rp-accent)' }}>
            <Sparkle size={16} />
          </span>
          <div>
            <h2 className="font-display text-[19px] font-bold leading-none tracking-tightest text-ink">Connect an AI provider</h2>
            <p className="mt-1.5 font-mono text-[11px] text-muted">
              Bring your own model — Anthropic- or OpenAI-compatible. Your key stays on your instance.
            </p>
          </div>
        </div>

        {/* Provider-Karten */}
        <div className="mt-6 grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-3">
          {PROVIDER_PRESETS.map((p) => {
            const on = p.id === selId;
            return (
              <button
                key={p.id}
                type="button"
                onClick={() => pick(p)}
                className={cn('rp-focus flex flex-col rounded-skin border p-3 text-left transition-colors', on ? 'border-line-strong bg-hover' : 'border-line hover:bg-hover')}
                style={on ? { boxShadow: 'inset 0 0 0 1px color-mix(in oklab, var(--rp-accent) 45%, transparent)' } : undefined}
              >
                <div className="flex items-center gap-2">
                  <span className="font-mono text-[12px] font-semibold text-ink">{p.name}</span>
                  {p.id === 'glm' ? <span className="rounded-skin-chip px-1 py-px font-mono text-[8.5px] uppercase tracking-[0.05em]" style={{ color: 'var(--rp-accent)', background: 'color-mix(in oklab, var(--rp-accent) 14%, transparent)' }}>value</span> : null}
                  {p.local ? <span className="rounded-skin-chip bg-inset px-1 py-px font-mono text-[8.5px] uppercase tracking-[0.05em] text-muted">local</span> : null}
                </div>
                <span className="mt-1 font-mono text-[10px] leading-relaxed text-muted">{p.tagline}</span>
              </button>
            );
          })}
        </div>

        {/* Konfig-Form */}
        <div className="mt-5 rounded-skin border border-line bg-inset p-4">
          <p className="mb-3 font-mono text-[10.5px] leading-relaxed text-muted">{preset.note}</p>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <label className="block sm:col-span-2">
              <span className="rp-micro !text-[10px]">endpoint (base URL)</span>
              <input value={baseUrl} onChange={(e) => setBaseUrl(e.target.value)} spellCheck={false} placeholder="https://api…" className="rp-focus mt-1 h-9 w-full rounded-skin-sm border border-line bg-raised px-2.5 font-mono text-[11.5px] text-ink" />
            </label>
            <label className="block">
              <span className="rp-micro !text-[10px]">model</span>
              <input value={model} onChange={(e) => setModel(e.target.value)} spellCheck={false} placeholder="model name" className="rp-focus mt-1 h-9 w-full rounded-skin-sm border border-line bg-raised px-2.5 font-mono text-[11.5px] text-ink" />
            </label>
            <label className="block">
              <span className="flex items-center justify-between">
                <span className="rp-micro !text-[10px]">{preset.keyLabel}</span>
                {preset.keyUrl ? (
                  <a href={preset.keyUrl} target="_blank" rel="noreferrer" className="font-mono text-[9.5px] text-accent underline underline-offset-2 hover:text-accent-hover">get one ↗</a>
                ) : null}
              </span>
              <input
                value={apiKey}
                onChange={(e) => setApiKey(e.target.value)}
                type="password"
                spellCheck={false}
                disabled={preset.local}
                placeholder={preset.local ? 'not required for local' : 'paste key…'}
                className="rp-focus mt-1 h-9 w-full rounded-skin-sm border border-line bg-raised px-2.5 font-mono text-[11.5px] text-ink disabled:opacity-50"
              />
            </label>
          </div>

          <div className="mt-4 flex items-center gap-2">
            <button
              type="button"
              disabled={!canConnect}
              onClick={connect}
              className="rp-focus flex h-9 items-center gap-1.5 rounded-skin-sm px-4 font-mono text-[12px] font-semibold transition-opacity hover:opacity-90 disabled:opacity-40"
              style={{ background: 'var(--rp-btn-bg)', color: 'var(--rp-btn-fg)' }}
            >
              Connect <span aria-hidden>→</span>
            </button>
            {onCancel ? (
              <button type="button" onClick={onCancel} className="h-9 rounded-skin-sm border border-line px-3.5 font-mono text-[12px] text-mid transition-colors hover:bg-hover hover:text-ink">
                Cancel
              </button>
            ) : null}
            <span className="ml-auto font-mono text-[9.5px] text-faint">key stored locally · server-side vault next</span>
          </div>
        </div>
      </div>
    </div>
  );
}
