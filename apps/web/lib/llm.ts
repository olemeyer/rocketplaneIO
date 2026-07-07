// llm.ts — BYO-LLM: der Copilot spricht Anthropic- ODER OpenAI-kompatible
// Endpoints, also lässt sich (fast) jeder Provider per base-URL + Key einhängen.
// Das ist der „own your data / kein Lock-in"-Wedge — inkl. lokal/air-gapped.

export type LLMProviderId = 'anthropic' | 'glm' | 'openai' | 'openrouter' | 'ollama' | 'custom';
export type LLMApi = 'anthropic' | 'openai';

export type LLMPreset = {
  id: LLMProviderId;
  name: string;
  tagline: string;
  api: LLMApi;
  baseUrl: string;
  defaultModel: string;
  keyLabel: string;
  keyUrl?: string;
  local?: boolean;
  note: string;
};

// Reihenfolge = Empfehlung. GLM zuerst hervorgehoben (günstige Subscription).
export const PROVIDER_PRESETS: LLMPreset[] = [
  {
    id: 'anthropic',
    name: 'Anthropic · Claude',
    tagline: 'Best reasoning — Claude Opus / Sonnet',
    api: 'anthropic',
    baseUrl: 'https://api.anthropic.com',
    defaultModel: 'claude-sonnet-4-6',
    keyLabel: 'Anthropic API key',
    keyUrl: 'https://console.anthropic.com/settings/keys',
    note: 'Pay-per-token API key (BYO). Strongest reasoning for incident work.',
  },
  {
    id: 'glm',
    name: 'GLM · Z.ai',
    tagline: 'Flat subscription — GLM Coding Plan, Anthropic-compatible',
    api: 'anthropic',
    baseUrl: 'https://api.z.ai/api/anthropic',
    defaultModel: 'glm-5.2',
    keyLabel: 'GLM Coding Plan token',
    keyUrl: 'https://z.ai/subscribe',
    note: 'The GLM Coding Plan gives an Anthropic-compatible token — a cheap flat monthly subscription usable as an API. Great value.',
  },
  {
    id: 'openai',
    name: 'OpenAI · GPT',
    tagline: 'GPT models',
    api: 'openai',
    baseUrl: 'https://api.openai.com/v1',
    defaultModel: 'gpt-4o',
    keyLabel: 'OpenAI API key',
    keyUrl: 'https://platform.openai.com/api-keys',
    note: 'Pay-per-token API key (BYO).',
  },
  {
    id: 'openrouter',
    name: 'OpenRouter',
    tagline: 'One key, any model (Claude · GLM · GPT · Llama …)',
    api: 'openai',
    baseUrl: 'https://openrouter.ai/api/v1',
    defaultModel: 'anthropic/claude-sonnet-4.6',
    keyLabel: 'OpenRouter key',
    keyUrl: 'https://openrouter.ai/keys',
    note: 'Route to almost any model with a single key — handy for trying providers.',
  },
  {
    id: 'ollama',
    name: 'Local · Ollama',
    tagline: 'Self-hosted, air-gapped — data never leaves your box',
    api: 'openai',
    baseUrl: 'http://localhost:11434/v1',
    defaultModel: 'llama3.1',
    keyLabel: '(no key needed)',
    local: true,
    note: 'Runs on your own hardware. Matches own-your-data / regulated / air-gapped setups.',
  },
  {
    id: 'custom',
    name: 'Custom endpoint',
    tagline: 'Any OpenAI- or Anthropic-compatible URL',
    api: 'openai',
    baseUrl: '',
    defaultModel: '',
    keyLabel: 'API key',
    note: 'Point at any compatible gateway — vLLM, LiteLLM, LM Studio, a corporate proxy …',
  },
];

export type LLMConfig = {
  provider: LLMProviderId;
  api: LLMApi;
  baseUrl: string;
  model: string;
  apiKey: string;
};

const KEY = (org: string) => `rp-llm-${org}`;

export function loadLLMConfig(org: string): LLMConfig | null {
  if (typeof window === 'undefined') return null;
  try {
    const raw = window.localStorage.getItem(KEY(org));
    if (!raw) return null;
    const c = JSON.parse(raw) as LLMConfig;
    return c && c.provider && (c.model || c.provider === 'custom') ? c : null;
  } catch {
    return null;
  }
}

export function saveLLMConfig(org: string, cfg: LLMConfig): void {
  if (typeof window === 'undefined') return;
  try {
    window.localStorage.setItem(KEY(org), JSON.stringify(cfg));
  } catch {
    /* Quota/Privacy */
  }
}

export function clearLLMConfig(org: string): void {
  if (typeof window === 'undefined') return;
  try {
    window.localStorage.removeItem(KEY(org));
  } catch {
    /* noop */
  }
}
