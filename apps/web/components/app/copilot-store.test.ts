// copilot-store.test.ts — Store-Verhalten gegen einen gemockten SSE-Stream:
// neue Orchestrator-Events (node_started/verdict/ask_user/reasoning, tool_call
// mit nodeId), Frage-Antworten inkl. Secret-Maskierung vor jeder Persistenz.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { copilotStore, type Session } from './copilot-store';
import { actionLevelOf } from '@/lib/approval';

function sseResponse(frames: Array<[string, unknown]>): Response {
  const enc = new TextEncoder();
  const stream = new ReadableStream<Uint8Array>({
    start(controller) {
      for (const [event, data] of frames) {
        controller.enqueue(enc.encode(`event: ${event}\ndata: ${JSON.stringify(data)}\n\n`));
      }
      controller.close();
    },
  });
  return new Response(stream, { status: 200, headers: { 'Content-Type': 'text/event-stream' } });
}

const putBodies: Array<Record<string, unknown>> = [];
const decisionBodies: Array<Record<string, unknown>> = [];

function mockFetch(frames: Array<[string, unknown]>) {
  vi.stubGlobal('fetch', vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
    const u = String(url);
    if (u.includes('/copilot/chat') && init?.method === 'POST') return sseResponse(frames);
    if (u.includes('/copilot/action')) {
      decisionBodies.push(JSON.parse(String(init?.body ?? '{}')));
      return new Response('{"ok":true}', { status: 200 });
    }
    if (init?.method === 'PUT') {
      putBodies.push(JSON.parse(String(init?.body ?? '{}')));
      return new Response('{}', { status: 200 });
    }
    if (u.endsWith('/graph')) return new Response('{"nodes":[]}', { status: 200 });
    return new Response('{"chats":[]}', { status: 200 });
  }));
}

async function runTurn(frames: Array<[string, unknown]>): Promise<Session> {
  mockFetch(frames);
  const id = copilotStore.ensure('org1', 'cluster1', undefined, { api: 'anthropic', baseUrl: 'x', model: 'm', apiKey: 'k' } as never);
  await copilotStore.send(id, 'was ist los?');
  const s = copilotStore.getSession(id);
  if (!s) throw new Error('session missing');
  return s;
}

beforeEach(() => {
  putBodies.length = 0;
  decisionBodies.length = 0;
});
afterEach(() => {
  vi.unstubAllGlobals();
});

describe('copilot-store orchestrator events', () => {
  it('streams text and builds the investigation graph', async () => {
    const s = await runTurn([
      ['meta', { runId: 'r1' }],
      ['text', { text: 'Prüfe ' }],
      ['text', { text: 'ClickHouse.' }],
      ['node_started', { nodeId: 'n1', parentId: null, seq: 1, kind: 'hypothesis', hypothesis: 'crash wegen config' }],
      ['tool_call', { id: 'c1', name: 'query_logs', args: {}, nodeId: 'n1' }],
      ['tool_result', { id: 'c1', name: 'query_logs', ok: true, result: '{}', nodeId: 'n1' }],
      ['verdict', { nodeId: 'n1', verdict: { verdict: 'confirmed', summary: 'ja', confidence: 0.9 }, tokens: { in: 100, out: 50 } }],
      ['reasoning', { text: 'erst die Logs' }],
      ['done', {}],
    ]);
    const text = s.items.filter((i) => i.role === 'assistant').map((i) => ('blocks' in i ? i.blocks : [])).flat()
      .filter((b) => b.type === 'text').map((b) => (b as { text: string }).text).join('');
    expect(text).toContain('Prüfe ClickHouse.');
    expect(s.nodes).toHaveLength(1);
    expect(s.nodes[0]).toMatchObject({ id: 'n1', kind: 'hypothesis', status: 'done', confidence: 0.9, tokensIn: 100 });
    expect(s.reasoning).toContain('erst die Logs');
    // Investigator-Tool-Call (mit nodeId) erscheint als Activity, NICHT als Chat-Block.
    const act = s.activities.find((a) => a.id === 'c1');
    expect(act?.nodeId).toBe('n1');
    const toolBlocks = s.items.flatMap((i) => ('blocks' in i ? i.blocks : [])).filter((b) => b.type === 'tool');
    expect(toolBlocks).toHaveLength(0);
  });

  it('renders ask_user as awaiting question activity', async () => {
    const s = await runTurn([
      ['meta', { runId: 'r2' }],
      ['ask_user', { id: 'q1', kind: 'secret', question: 'Registry-Token?', secretHint: 'GHCR token', nodeId: 'n2' }],
      ['done', {}],
    ]);
    const q = s.activities.find((a) => a.isQuestion);
    expect(q).toBeTruthy();
    expect(q?.status).toBe('awaiting');
    expect(q?.args?.kind).toBe('secret');
    const qBlocks = s.items.flatMap((i) => ('blocks' in i ? i.blocks : [])).filter((b) => b.type === 'question');
    expect(qBlocks).toHaveLength(1);
  });

  it('masks secret answers before any persistence', async () => {
    const s = await runTurn([
      ['meta', { runId: 'r3' }],
      ['ask_user', { id: 'q9', kind: 'secret', question: 'Token?' }],
      ['done', {}],
    ]);
    await copilotStore.answer(s.id, 'q9', { value: 'super-geheim-123' }, { secret: true });
    const q = copilotStore.getSession(s.id)?.activities.find((a) => a.id === 'q9');
    expect(q?.result).toBe('•••');
    // Klartext geht NUR im Decision-POST raus, nie in die Chat-Persistenz (PUT).
    expect(JSON.stringify(putBodies)).not.toContain('super-geheim-123');
    expect(JSON.stringify(decisionBodies)).toContain('super-geheim-123');
    expect(decisionBodies[0]).toMatchObject({ decision: 'answer', callId: 'q9' });
  });

  it('persists nodes with the chat data', async () => {
    const s = await runTurn([
      ['meta', { runId: 'r4' }],
      ['node_started', { nodeId: 'n5', seq: 1, kind: 'hypothesis', hypothesis: 'h' }],
      ['verdict', { nodeId: 'n5', verdict: { verdict: 'refuted', summary: 'nein' } }],
      ['done', {}],
    ]);
    expect(s.nodes[0]?.status).toBe('done');
    const last = putBodies[putBodies.length - 1] as { data?: { nodes?: unknown[] } };
    expect(last?.data?.nodes).toHaveLength(1);
  });
});

describe('actionLevelOf mirror', () => {
  it('classifies new kinds like the backend', () => {
    expect(actionLevelOf('get_resource')).toBe('read');
    expect(actionLevelOf('get_secret')).toBe('read');
    expect(actionLevelOf('exec_readonly')).toBe('disruptive');
    expect(actionLevelOf('patch_secret')).toBe('reversible');
    expect(actionLevelOf('pdb_set')).toBe('reversible');
    expect(actionLevelOf('patch_resource')).toBe('destructive');
    expect(actionLevelOf('pvc_expand')).toBe('destructive');
    expect(actionLevelOf('restore_resource')).toBe('destructive');
    expect(actionLevelOf('script')).toBe('destructive');
    expect(actionLevelOf('delete_job')).toBe('disruptive');
    // fail-closed: scale ohne parsebare replicas = destructive
    expect(actionLevelOf('scale', {})).toBe('destructive');
    expect(actionLevelOf('scale', { replicas: 3 })).toBe('reversible');
    expect(actionLevelOf('scale', { replicas: 0 })).toBe('destructive');
  });
});
