'use client';

// copilot-store.ts — der chat-gebundene Session-Store. Jede Konversation ist eine
// SESSION, die UNABHÄNGIG streamt (eigener Lock, eigener State) und in die DB
// persistiert. Die View rendert nur die aktive Session — ein laufender Stream in
// Chat A schreibt weiter nach A, egal was gerade angezeigt wird. Mehrere Sessions
// können PARALLEL streamen. Das entkoppelt Streaming vom React-Lebenszyklus.

import { loadApprovalPolicy, type RiskLevel } from '@/lib/approval';
import type { LLMConfig } from '@/lib/llm';

/* ── geteilte Typen ──────────────────────────────────────────────────────── */

export type ActionData = { kind: string; target: string; reason: string; klass: string; input: Record<string, unknown> };
export type Activity = {
  id: string;
  name: string;
  isAction?: boolean;
  isQuestion?: boolean; // ask_user — Human-in-the-loop-Fragebox
  args?: Record<string, unknown>;
  status: string;
  result?: string;
  learning?: string;
  kind?: string;
  target?: string;
  klass?: string; // Risk-Level: read | reversible | disruptive | destructive
  approvalMode?: string; // auto | click | confirm | off (aus der Guardrails-Policy)
  reason?: string;
  actionId?: string;
  progress?: string;
  steps?: unknown;
  terminal?: boolean;
  nodeId?: string; // Investigation-Graph-Knoten, zu dem dieser Call gehört
  source?: string; // propose_script: der Starlark-Quelltext
};
// Ein Knoten im Investigation-Graph (Hypothese→Verdict; Branch über parentId).
export type GraphNode = {
  id: string;
  parentId?: string | null;
  seq: number;
  kind: string; // hypothesis | action | question | conclusion
  hypothesis: string;
  task?: unknown;
  verdict?: unknown;
  status: string; // running | done | failed | abandoned
  confidence?: number;
  tokensIn?: number;
  tokensOut?: number;
};
export type Block =
  | { type: 'text'; text: string }
  | { type: 'tool'; refId: string; name: string }
  | { type: 'action'; refId: string }
  | { type: 'question'; refId: string }
  | { type: 'node'; nodeId: string }; // Investigation-Knoten (Hypothese/Fazit) inline im Chat
export type Item =
  | { role: 'user'; text: string }
  | { role: 'assistant'; blocks: Block[] }
  | { role: 'error'; text: string }
  | { role: 'note'; text: string };
export type ChatSummary = { id: string; title: string; updatedAt: string; summary?: string };

// Der reaktive (gerenderte) Teil einer Session — immutabel aktualisiert.
export type Session = {
  id: string;
  items: Item[];
  activities: Activity[];
  nodes: GraphNode[]; // Investigation-Graph (Hypothese→Verdict-Knoten)
  reasoning: string[]; // Master-Reasoning (Dev-Ansicht, optional eingeblendet)
  busy: boolean;
  title: string;
  summary: string;
  rewindUndo: { items: Item[]; activities: Activity[] } | null;
  lastActivityId?: string; // die zuletzt aktive Activity → Inspector folgt ihr live
};

// Nicht-reaktive Interna (Flags/Puffer) — leben ausserhalb der immutablen Session.
type Internal = {
  orgId: string;
  clusterId: string;
  config: LLMConfig | null;
  scope: string; // aktiver Namespace ("" = alle) — geht mit an /copilot/chat
  sending: boolean;
  runId: string;
  contQueue: string[];
  decided: Set<string>;
  watching: Set<string>;
  lastLearn: string | null;
  learnBuf: Record<string, string>;
};

export const GREETING: Item = {
  role: 'assistant',
  blocks: [{ type: 'text', text: 'Hi — I investigate this cluster with a team of sub-agents: every hypothesis becomes a node in the graph on the right, and I only change anything through safe, approved actions. Ask me what’s wrong, or what to fix.' }],
};

function newId(): string {
  return typeof crypto !== 'undefined' && crypto.randomUUID ? crypto.randomUUID() : `s-${Date.now()}-${Math.floor(Math.random() * 1e6)}`;
}
function apiBase(o: string, c: string): string {
  return `/api/orgs/${encodeURIComponent(o)}/clusters/${encodeURIComponent(c)}`;
}

/* ── Store ───────────────────────────────────────────────────────────────── */

let sessions: Record<string, Session> = {};
const intern: Record<string, Internal> = {};
let chatsByCluster: Record<string, ChatSummary[]> = {};
const listeners = new Set<() => void>();
const saveTimers: Record<string, ReturnType<typeof setTimeout>> = {};

function emit() {
  for (const l of listeners) l();
}
function patch(id: string, updater: (s: Session) => Session) {
  const s = sessions[id];
  if (!s) return;
  sessions = { ...sessions, [id]: updater(s) };
  emit();
}
function summaryOf(s: Session): ChatSummary {
  const fu = s.items.find((i) => i.role === 'user') as { text: string } | undefined;
  return { id: s.id, title: (s.title || fu?.text || 'Investigation').slice(0, 80), summary: s.summary, updatedAt: new Date().toISOString() };
}
// optimisticChat trägt einen Chat SOFORT in die Rail-Liste ein (an die Spitze =
// neueste Aktivität), unabhängig vom aktiven View — so verschwindet ein gerade
// gestarteter Chat nie, auch wenn man sofort wegklickt (Race mit refreshChats).
function optimisticChat(id: string) {
  const s = sessions[id];
  const it = intern[id];
  if (!s || !it || !s.items.some((i) => i.role === 'user')) return;
  const list = chatsByCluster[it.clusterId] ?? [];
  chatsByCluster = { ...chatsByCluster, [it.clusterId]: [summaryOf(s), ...list.filter((c) => c.id !== id)] };
  emit();
}

function upsertActivity(id: string, aid: string, p: Partial<Activity>, focus = false) {
  patch(id, (s) => {
    const i = s.activities.findIndex((a) => a.id === aid);
    const activities = i === -1
      ? [...s.activities, { id: aid, name: aid, status: 'running', ...p } as Activity]
      : (() => { const n = s.activities.slice(); n[i] = { ...n[i]!, ...p }; return n; })();
    return { ...s, activities, ...(focus ? { lastActivityId: aid } : {}) };
  });
}

function upsertNode(id: string, nid: string, p: Partial<GraphNode>) {
  patch(id, (s) => {
    const i = s.nodes.findIndex((n) => n.id === nid);
    const nodes = i === -1
      ? [...s.nodes, { id: nid, seq: s.nodes.length + 1, kind: 'hypothesis', hypothesis: '', status: 'running', ...p } as GraphNode]
      : (() => { const n = s.nodes.slice(); n[i] = { ...n[i]!, ...p }; return n; })();
    return { ...s, nodes };
  });
}

export const copilotStore = {
  subscribe(cb: () => void) {
    listeners.add(cb);
    return () => {
      listeners.delete(cb);
    };
  },
  getSession(id: string): Session | undefined {
    return sessions[id];
  },
  getChats(clusterId: string): ChatSummary[] {
    return chatsByCluster[clusterId] ?? EMPTY_CHATS;
  },

  // ensure legt (falls nötig) eine frische Session an und liefert ihre id.
  ensure(orgId: string, clusterId: string, id?: string, config?: LLMConfig | null): string {
    const sid = id || newId();
    if (!sessions[sid]) {
      sessions[sid] = { id: sid, items: [GREETING], activities: [], nodes: [], reasoning: [], busy: false, title: '', summary: '', rewindUndo: null };
      intern[sid] = { orgId, clusterId, config: config ?? null, scope: "", sending: false, runId: '', contQueue: [], decided: new Set(), watching: new Set(), lastLearn: null, learnBuf: {} };
      emit();
    } else if (config) {
      intern[sid]!.config = config;
    }
    return sid;
  },

  setConfig(id: string, config: LLMConfig | null) {
    if (intern[id]) intern[id]!.config = config;
  },
  setScope(id: string, scope: string) {
    if (intern[id]) intern[id]!.scope = scope;
  },

  async refreshChats(orgId: string, clusterId: string) {
    try {
      const r = await fetch(`${apiBase(orgId, clusterId)}/copilot/chats`, { credentials: 'include' });
      if (!r.ok) return;
      const server: ChatSummary[] = (await r.json()).chats ?? [];
      const serverIds = new Set(server.map((c) => c.id));
      // Lokal gestartete Chats, die der Server (noch) nicht kennt, oben behalten —
      // sonst würde ein gerade angelegter Chat kurz aus der Liste fallen.
      const localExtra = Object.values(sessions)
        .filter((s) => intern[s.id]?.clusterId === clusterId && !serverIds.has(s.id) && s.items.some((i) => i.role === 'user'))
        .map(summaryOf);
      chatsByCluster = { ...chatsByCluster, [clusterId]: [...localExtra, ...server] };
      emit();
    } catch {
      /* offline */
    }
  },

  async load(orgId: string, clusterId: string, id: string) {
    // Läuft in dieser Session schon ein Stream? Dann NICHT überschreiben.
    if (sessions[id] && intern[id]?.sending) return;
    try {
      const r = await fetch(`${apiBase(orgId, clusterId)}/copilot/chats/${encodeURIComponent(id)}`, { credentials: 'include' });
      if (!r.ok) return;
      const chat = await r.json();
      const data = (chat.data ?? {}) as { items?: Item[]; activities?: Activity[]; nodes?: GraphNode[]; reasoning?: string[] };
      // Graph: der Server ist die Wahrheit (DB) — Fallback auf den in data
      // persistierten Stand, wenn die Route (noch) nichts liefert.
      let nodes: GraphNode[] = data.nodes ?? [];
      try {
        const g = await fetch(`${apiBase(orgId, clusterId)}/copilot/chats/${encodeURIComponent(id)}/graph`, { credentials: 'include' });
        if (g.ok) {
          const serverNodes = ((await g.json()).nodes ?? []) as GraphNode[];
          if (serverNodes.length) nodes = serverNodes;
        }
      } catch { /* offline */ }
      sessions = {
        ...sessions,
        [id]: {
          id,
          items: data.items?.length ? data.items : [GREETING],
          activities: data.activities ?? [],
          nodes,
          reasoning: data.reasoning ?? [],
          busy: intern[id]?.sending ?? false,
          title: String(chat.title ?? ''),
          summary: String(chat.summary ?? ''),
          rewindUndo: null,
        },
      };
      intern[id] = intern[id] ?? { orgId, clusterId, config: null, scope: "", sending: false, runId: '', contQueue: [], decided: new Set(), watching: new Set(), lastLearn: null, learnBuf: {} };
      intern[id]!.orgId = orgId;
      intern[id]!.clusterId = clusterId;
      emit();
    } catch {
      /* offline */
    }
  },

  async remove(orgId: string, clusterId: string, id: string) {
    try {
      await fetch(`${apiBase(orgId, clusterId)}/copilot/chats/${encodeURIComponent(id)}`, { method: 'DELETE', credentials: 'include' });
    } catch {
      /* offline */
    }
    delete sessions[id];
    delete intern[id];
    sessions = { ...sessions };
    emit();
    this.refreshChats(orgId, clusterId);
  },

  rewind(id: string, cut: number, refill?: (t: string) => void) {
    const s = sessions[id];
    const it = intern[id];
    if (!s || !it || it.sending) return;
    const before = { items: s.items, activities: s.activities };
    const next = s.items.slice(0, Math.max(1, cut));
    const keep = new Set<string>();
    for (const item of next) if (item.role === 'assistant' && 'blocks' in item) for (const b of item.blocks) if (b.type === 'tool' || b.type === 'action') keep.add(b.refId);
    const removed = s.items[cut];
    it.lastLearn = null;
    it.learnBuf = {};
    patch(id, (cur) => ({ ...cur, items: next.length ? next : [GREETING], activities: cur.activities.filter((a) => keep.has(a.id)), rewindUndo: before }));
    if (refill && removed && removed.role === 'user') refill(removed.text);
    this.save(id);
  },
  undoRewind(id: string) {
    const s = sessions[id];
    if (!s || !s.rewindUndo) return;
    const u = s.rewindUndo;
    patch(id, (cur) => ({ ...cur, items: u.items, activities: u.activities, rewindUndo: null }));
    this.save(id);
  },
  clearRewindUndo(id: string) {
    patch(id, (cur) => (cur.rewindUndo ? { ...cur, rewindUndo: null } : cur));
  },

  // scheduleSave debounced während des Streams — NICHTS geht verloren (immer safe),
  // ohne pro Token zu schreiben.
  // scheduleSave persistiert mid-stream OHNE die Liste neu zu holen (kein Umsortieren
  // pro Token) — nur Datensicherheit. Reihenfolge-Refresh nur bei Turn-Start/-Ende.
  scheduleSave(id: string) {
    clearTimeout(saveTimers[id]);
    saveTimers[id] = setTimeout(() => {
      void this.save(id, false);
    }, 1200);
  },

  async save(id: string, refresh = true) {
    clearTimeout(saveTimers[id]);
    const s = sessions[id];
    const it = intern[id];
    if (!s || !it) return;
    if (!s.items.some((i) => i.role === 'user')) return; // leere Vorgänge nicht speichern
    const firstUser = s.items.find((i) => i.role === 'user') as { text: string } | undefined;
    const title = (s.title || firstUser?.text || 'Investigation').slice(0, 80);
    try {
      await fetch(`${apiBase(it.orgId, it.clusterId)}/copilot/chats/${id}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({ title, summary: s.summary, data: { items: s.items, activities: s.activities, nodes: s.nodes, reasoning: s.reasoning } }),
      });
      if (refresh) this.refreshChats(it.orgId, it.clusterId);
    } catch {
      /* offline */
    }
  },

  // answer beantwortet eine ask_user-Frage. Secrets werden VOR jeder lokalen
  // Persistenz maskiert — der Klartext geht ausschließlich im Decision-POST an
  // den Server (dort in den Run-Vault) und taucht nie in items/activities auf.
  async answer(id: string, activityId: string, answer: { value?: string; values?: string[] }, opts?: { secret?: boolean }) {
    const it = intern[id];
    if (!it || it.decided.has(activityId)) return;
    it.decided.add(activityId);
    const shown = opts?.secret ? '•••' : (answer.values?.length ? answer.values.join(', ') : (answer.value ?? ''));
    upsertActivity(id, activityId, { status: 'answered', terminal: true, result: shown });
    copilotStore.scheduleSave(id);
    try {
      await fetch(`${apiBase(it.orgId, it.clusterId)}/copilot/action`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({ runId: it.runId, callId: activityId, decision: 'answer', answer }),
      });
    } catch {
      /* Backend läuft über den offenen Stream weiter */
    }
  },

  async decide(id: string, activityId: string, approve: boolean) {
    const it = intern[id];
    if (!it || it.decided.has(activityId)) return;
    it.decided.add(activityId);
    upsertActivity(id, activityId, { status: approve ? 'starting' : 'rejected', terminal: approve ? false : true });
    try {
      await fetch(`${apiBase(it.orgId, it.clusterId)}/copilot/action`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({ runId: it.runId, callId: activityId, decision: approve ? 'approve' : 'reject' }),
      });
    } catch {
      /* Backend läuft über den offenen Stream weiter */
    }
  },

  // send streamt eine Nachricht in die Session id — gebunden, unabhängig, parallel.
  async send(id: string, text: string, opts?: { note?: boolean }) {
    const it = intern[id];
    const s = sessions[id];
    const t = text.trim();
    if (!it || !s || !t || it.sending) return;
    if (!it.config) return;
    it.sending = true;
    const config = it.config;

    const history = s.items;
    patch(id, (cur) => ({ ...cur, busy: true, items: [...cur.items, opts?.note ? { role: 'note', text: t } : { role: 'user', text: t }, { role: 'assistant', blocks: [] }] }));
    if (!opts?.note) optimisticChat(id); // sofort in die Rail — verschwindet nie beim Wegklicken
    void this.save(id); // sofort persistieren — kein „unsaved"-Fenster

    const msgs: { role: string; text: string }[] = [];
    for (const item of history) {
      if (item.role === 'user' || item.role === 'note') msgs.push({ role: 'user', text: item.text });
      else if (item.role === 'assistant' && 'blocks' in item) {
        const txt = item.blocks.filter((b) => b.type === 'text').map((b) => (b as { text: string }).text).join('\n');
        if (txt) msgs.push({ role: 'assistant', text: txt });
      }
    }
    msgs.push({ role: 'user', text: t });

    // die (immer letzte) Assistant-Blockliste dieser Session wächst live
    const blocks: Block[] = [];
    const flush = () => { patch(id, (cur) => ({ ...cur, items: [...cur.items.slice(0, -1), { role: 'assistant', blocks: blocks.map((b) => ({ ...b })) }] })); copilotStore.scheduleSave(id); };

    const onEvent = (event: string, data: Record<string, unknown>) => {
      switch (event) {
        case 'meta':
          it.runId = String(data.runId ?? '');
          break;
        case 'text': {
          const tx = String(data.text ?? '');
          const last = blocks[blocks.length - 1];
          if (last && last.type === 'text') last.text += tx;
          else blocks.push({ type: 'text', text: tx });
          const lid = it.lastLearn;
          if (lid) {
            it.learnBuf[lid] = (it.learnBuf[lid] ?? '') + tx;
            upsertActivity(id, lid, { learning: it.learnBuf[lid] });
          }
          flush();
          break;
        }
        case 'tool_call': {
          const cid = String(data.id ?? '');
          it.lastLearn = null;
          const nodeId = data.nodeId != null ? String(data.nodeId) : undefined;
          upsertActivity(id, cid, { name: String(data.name ?? ''), args: (data.args as Record<string, unknown>) ?? {}, status: 'running', isAction: false, nodeId }, true);
          // Investigator-Calls (mit nodeId) laufen im Graph-Kontext — sie erscheinen
          // im Inspector/Graph, nicht als eigener Chat-Block (der Chat bleibt ruhig).
          if (!nodeId) {
            blocks.push({ type: 'tool', refId: cid, name: String(data.name ?? '') });
            flush();
          }
          break;
        }
        case 'tool_result': {
          const cid = String(data.id ?? '');
          upsertActivity(id, cid, { status: data.ok === false ? 'error' : 'ok', result: String(data.result ?? '') }, true);
          it.lastLearn = cid;
          break;
        }
        case 'action': {
          const cid = String(data.id ?? '');
          it.lastLearn = null;
          // Guardrails: Level (vom Backend klassifiziert) → Approval-Modus (Policy).
          const level = String(data.level ?? data.klass ?? 'reversible') as RiskLevel;
          const mode = loadApprovalPolicy(it.clusterId)[level] ?? 'click';
          upsertActivity(id, cid, {
            name: 'run_safe_action', isAction: true, status: 'awaiting', terminal: false,
            kind: String(data.kind ?? ''), target: String(data.target ?? ''), klass: level, approvalMode: mode,
            reason: String(data.reason ?? ''), args: (data.input as Record<string, unknown>) ?? {},
            nodeId: data.nodeId != null ? String(data.nodeId) : undefined,
            source: data.source != null ? String(data.source) : undefined,
          }, true);
          blocks.push({ type: 'action', refId: cid });
          flush();
          if (mode === 'auto') {
            void copilotStore.decide(id, cid, true); // Policy: ohne Nachfrage ausführen
          } else if (mode === 'off') {
            void copilotStore.decide(id, cid, false); // Policy: Stufe für den Copilot gesperrt
            upsertActivity(id, cid, { status: 'blocked', terminal: true, result: `blocked by guardrails — ${level} actions are disabled for the Copilot` });
          }
          break;
        }
        case 'ask_user': {
          const cid = String(data.id ?? '');
          it.lastLearn = null;
          upsertActivity(id, cid, {
            name: 'ask_user', isQuestion: true, status: 'awaiting', terminal: false,
            args: {
              question: String(data.question ?? ''),
              kind: String(data.kind ?? 'text'),
              options: (data.options as string[]) ?? [],
              allowFreeText: data.allowFreeText === true,
              secretHint: data.secretHint != null ? String(data.secretHint) : '',
            },
            nodeId: data.nodeId != null ? String(data.nodeId) : undefined,
          }, true);
          blocks.push({ type: 'question', refId: cid });
          flush();
          break;
        }
        case 'node_started': {
          const nid = String(data.nodeId ?? '');
          const kind = String(data.kind ?? 'hypothesis');
          upsertNode(id, nid, {
            parentId: data.parentId != null ? String(data.parentId) : null,
            seq: Number(data.seq ?? 0),
            kind,
            hypothesis: String(data.hypothesis ?? ''),
            task: data.task,
            status: 'running',
          });
          // Hypothesen + Fazit erscheinen INLINE im Chat (Investigation ist Teil
          // des Gesprächs, nicht ein Panel rechts). Actions/Fragen haben eigene
          // Blöcke; der reine hypothesis/conclusion-Knoten kriegt hier seinen.
          if (kind === 'hypothesis' || kind === 'conclusion') {
            blocks.push({ type: 'node', nodeId: nid });
            flush();
          }
          break;
        }
        case 'verdict': {
          const nid = String(data.nodeId ?? '');
          const tokens = (data.tokens as { in?: number; out?: number }) ?? {};
          const verdict = data.verdict as Record<string, unknown> | undefined;
          const conf = verdict && typeof verdict.confidence === 'number' ? (verdict.confidence as number) : undefined;
          upsertNode(id, nid, { verdict, status: 'done', confidence: conf, tokensIn: tokens.in, tokensOut: tokens.out });
          copilotStore.scheduleSave(id);
          break;
        }
        case 'node_status': {
          upsertNode(id, String(data.nodeId ?? ''), { status: String(data.status ?? 'running') });
          break;
        }
        case 'reasoning': {
          const tx = String(data.text ?? '').trim();
          if (tx) patch(id, (cur) => ({ ...cur, reasoning: [...cur.reasoning, tx] }));
          break;
        }
        case 'action_status': {
          const cid = String(data.id ?? '');
          upsertActivity(id, cid, {
            status: String(data.status ?? 'running'),
            progress: data.progress != null ? String(data.progress) : undefined,
            steps: data.steps,
            result: data.result != null ? String(data.result) : undefined,
            actionId: data.actionId != null ? String(data.actionId) : undefined,
            terminal: data.terminal === true,
          });
          copilotStore.scheduleSave(id);
          break;
        }
        case 'title': {
          const tt = String(data.title ?? '').trim();
          const su = String(data.summary ?? '').trim();
          patch(id, (cur) => ({ ...cur, title: tt || cur.title, summary: su || cur.summary }));
          break;
        }
        case 'error':
          patch(id, (cur) => ({ ...cur, items: [...cur.items.slice(0, -1), { role: 'error', text: String(data.error ?? 'llm error') }] }));
          break;
        case 'done':
          if (!blocks.length) { blocks.push({ type: 'text', text: '(no response)' }); flush(); }
          break;
        default:
          break; // ping
      }
    };

    try {
      const res = await fetch(`${apiBase(it.orgId, it.clusterId)}/copilot/chat`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({ messages: msgs, config, scope: it.scope, chatId: id }),
      });
      if (!res.ok || !res.body) {
        let msg = 'request failed';
        try { msg = (await res.json()).error ?? msg; } catch { /* SSE */ }
        patch(id, (cur) => ({ ...cur, items: [...cur.items.slice(0, -1), { role: 'error', text: msg }] }));
        return;
      }
      const reader = res.body.getReader();
      const decoder = new TextDecoder();
      let buf = '';
      for (;;) {
        const { value, done } = await reader.read();
        if (done) break;
        buf += decoder.decode(value, { stream: true });
        const frames = buf.split('\n\n');
        buf = frames.pop() ?? '';
        for (const frame of frames) {
          let event = 'message';
          const dataLines: string[] = [];
          for (const line of frame.split('\n')) {
            if (line.startsWith('event:')) event = line.slice(6).trim();
            else if (line.startsWith('data:')) dataLines.push(line.slice(5).trim());
          }
          if (!dataLines.length) continue;
          try { onEvent(event, JSON.parse(dataLines.join('\n'))); } catch { /* skip */ }
        }
      }
    } catch (e) {
      patch(id, (cur) => ({ ...cur, items: [...cur.items.slice(0, -1), { role: 'error', text: e instanceof Error ? e.message : 'network error' }] }));
    } finally {
      it.sending = false;
      patch(id, (cur) => ({ ...cur, busy: false }));
      this.save(id);
      // Hintergrund-Actions dieser Session beobachten → automatischer Folge-Turn.
      const cur = sessions[id];
      if (cur) {
        for (const a of cur.activities) {
          if (a.isAction && a.actionId && !a.terminal && a.status !== 'rejected' && a.status !== 'cancelled' && !it.watching.has(a.actionId)) {
            it.watching.add(a.actionId);
            this.watchAction(id, a.id, a.actionId);
          }
        }
      }
      this.drain(id);
    }
  },

  drain(id: string) {
    const it = intern[id];
    if (!it || it.sending) return;
    if (!it.contQueue.length) return;
    const items = it.contQueue;
    it.contQueue = [];
    const body = items.length === 1 ? items[0]! : `${items.length} background actions just finished:\n${items.map((x) => `- ${x}`).join('\n')}`;
    this.send(id, `[system] ${body}\nRe-verify the affected workloads with read tools and report the outcome in ONE concise summary. If everything is healthy, say so once — do not repeat per action.`, { note: true });
  },

  async watchAction(id: string, callId: string, actionId: string) {
    const it = intern[id];
    if (!it) return;
    for (let i = 0; i < 300; i++) {
      await new Promise((r) => setTimeout(r, 1600));
      let acts: Array<Record<string, unknown>> = [];
      try {
        const r = await fetch(`${apiBase(it.orgId, it.clusterId)}/actions?limit=40`, { credentials: 'include' });
        if (r.ok) acts = (await r.json()).actions ?? [];
      } catch { continue; }
      const a = acts.find((x) => String(x.id) === actionId);
      if (!a) continue;
      const st = String(a.status);
      const terminal = st === 'succeeded' || st === 'failed';
      upsertActivity(id, callId, { status: st, progress: a.progress != null ? String(a.progress) : undefined, steps: a.steps, result: a.result != null ? String(a.result) : undefined, terminal });
      if (terminal) {
        it.watching.delete(actionId);
        const act = sessions[id]?.activities.find((x) => x.id === callId);
        const label = `${act?.kind ?? 'action'} on ${act?.target ?? ''}`.trim();
        it.contQueue.push(`${label} → ${st}${a.result ? ` (${String(a.result)})` : ''}`);
        this.save(id);
        this.drain(id);
        break;
      }
    }
  },
};

const EMPTY_CHATS: ChatSummary[] = [];
