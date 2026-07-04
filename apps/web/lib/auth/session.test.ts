// @vitest-environment node
import { describe, expect, it } from 'vitest';
import { createSession, verifySession } from './session';

const SECRET = 'unit-test-secret';

describe('session tokens', () => {
  it('roundtrips a valid payload', async () => {
    const exp = Date.now() + 60_000;
    const token = await createSession({ sub: 'u1', email: 'a@b.c', exp }, SECRET);
    expect(await verifySession(token, SECRET)).toMatchObject({ sub: 'u1', email: 'a@b.c', exp });
  });

  it('rejects a tampered signature', async () => {
    const token = await createSession({ sub: 'u1', email: 'a@b.c', exp: Date.now() + 60_000 }, SECRET);
    const body = token.split('.')[0] ?? '';
    expect(await verifySession(`${body}.deadbeef`, SECRET)).toBeNull();
  });

  it('rejects a token signed with a different secret', async () => {
    const token = await createSession({ sub: 'u1', email: 'a@b.c', exp: Date.now() + 60_000 }, SECRET);
    expect(await verifySession(token, 'other-secret')).toBeNull();
  });

  it('rejects an expired token', async () => {
    const token = await createSession({ sub: 'u1', email: 'a@b.c', exp: Date.now() - 1 }, SECRET);
    expect(await verifySession(token, SECRET)).toBeNull();
  });

  it('rejects malformed input', async () => {
    expect(await verifySession('not-a-token', SECRET)).toBeNull();
  });
});
