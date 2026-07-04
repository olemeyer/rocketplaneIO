// Auth-Konstanten. Das Secret kommt aus RP_AUTH_SECRET (in Produktion setzen!);
// der Default ist nur für lokale Entwicklung gedacht.
export const SESSION_COOKIE = 'rp_session';
export const SESSION_TTL_MS = 7 * 24 * 60 * 60 * 1000; // 7 Tage

export function authSecret(): string {
  return process.env.RP_AUTH_SECRET || 'rocketplane-dev-secret-change-me';
}
