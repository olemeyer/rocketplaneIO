// @vitest-environment node
import { describe, expect, it } from 'vitest';
import { DEMO_CREDENTIALS } from './demo';
import { verifyCredentials } from './users';

describe('verifyCredentials', () => {
  it('accepts the demo account', () => {
    expect(verifyCredentials(DEMO_CREDENTIALS.email, DEMO_CREDENTIALS.password)).toMatchObject({
      email: DEMO_CREDENTIALS.email,
    });
  });

  it('treats the email case-insensitively', () => {
    expect(verifyCredentials(DEMO_CREDENTIALS.email.toUpperCase(), DEMO_CREDENTIALS.password)).not.toBeNull();
  });

  it('rejects a wrong password', () => {
    expect(verifyCredentials(DEMO_CREDENTIALS.email, 'nope')).toBeNull();
  });

  it('rejects an unknown user', () => {
    expect(verifyCredentials('stranger@example.com', 'whatever')).toBeNull();
  });
});
