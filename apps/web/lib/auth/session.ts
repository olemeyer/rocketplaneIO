// Stateless Session-Token: base64url(payload).base64url(HMAC-SHA256).
// Bewusst mit Web-Crypto implementiert, damit derselbe Code in der Edge-
// Middleware UND in Node-Route-Handlern läuft.
import { authSecret } from './config';

export interface SessionPayload {
  sub: string;
  email: string;
  exp: number; // epoch ms
}

const enc = new TextEncoder();
const dec = new TextDecoder();

function b64urlEncode(bytes: Uint8Array): string {
  let s = '';
  for (const b of bytes) s += String.fromCharCode(b);
  return btoa(s).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

function b64urlDecode(str: string): Uint8Array<ArrayBuffer> {
  const s = str.replace(/-/g, '+').replace(/_/g, '/');
  const pad = s.length % 4 ? '='.repeat(4 - (s.length % 4)) : '';
  const bin = atob(s + pad);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

async function hmacKey(secret: string): Promise<CryptoKey> {
  return crypto.subtle.importKey('raw', enc.encode(secret), { name: 'HMAC', hash: 'SHA-256' }, false, [
    'sign',
    'verify',
  ]);
}

export async function createSession(payload: SessionPayload, secret = authSecret()): Promise<string> {
  const body = b64urlEncode(enc.encode(JSON.stringify(payload)));
  const sig = await crypto.subtle.sign('HMAC', await hmacKey(secret), enc.encode(body));
  return `${body}.${b64urlEncode(new Uint8Array(sig))}`;
}

export async function verifySession(
  token: string,
  secret = authSecret(),
): Promise<SessionPayload | null> {
  const parts = token.split('.');
  const body = parts[0];
  const sig = parts[1];
  if (parts.length !== 2 || !body || !sig) return null;

  const ok = await crypto.subtle.verify('HMAC', await hmacKey(secret), b64urlDecode(sig), enc.encode(body));
  if (!ok) return null;

  try {
    const payload = JSON.parse(dec.decode(b64urlDecode(body))) as SessionPayload;
    if (typeof payload.exp !== 'number' || payload.exp < Date.now()) return null;
    if (typeof payload.email !== 'string' || typeof payload.sub !== 'string') return null;
    return payload;
  } catch {
    return null;
  }
}
