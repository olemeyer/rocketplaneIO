// Credential-Prüfung. In dieser Scheibe ein Demo-Account (plus optionaler
// Env-Override RP_AUTH_USERS="email:pass,email2:pass2"). Später gegen einen
// echten User-Store / OIDC-Provider tauschen — die Aufrufstelle bleibt gleich.

import { DEMO_CREDENTIALS } from './demo';

export interface AuthUser {
  sub: string;
  email: string;
}

export function verifyCredentials(email: string, password: string): AuthUser | null {
  const e = email.trim().toLowerCase();

  if (e === DEMO_CREDENTIALS.email && password === DEMO_CREDENTIALS.password) {
    return { sub: 'usr_demo', email: DEMO_CREDENTIALS.email };
  }

  const extra = process.env.RP_AUTH_USERS;
  if (extra) {
    for (const pair of extra.split(',')) {
      const idx = pair.indexOf(':');
      if (idx <= 0) continue;
      const pe = pair.slice(0, idx).trim().toLowerCase();
      const pp = pair.slice(idx + 1);
      if (pe === e && pp === password) return { sub: `usr_${e}`, email: e };
    }
  }
  return null;
}
