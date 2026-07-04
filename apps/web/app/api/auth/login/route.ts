import { cookies } from 'next/headers';
import { SESSION_COOKIE, SESSION_TTL_MS } from '@/lib/auth/config';
import { createSession } from '@/lib/auth/session';
import { verifyCredentials } from '@/lib/auth/users';

export async function POST(req: Request) {
  let email = '';
  let password = '';
  try {
    const body = (await req.json()) as { email?: string; password?: string };
    email = body.email ?? '';
    password = body.password ?? '';
  } catch {
    // ignore -> unten als ungültig behandelt
  }

  const user = verifyCredentials(email, password);
  if (!user) {
    return Response.json(
      { status: 'error', errorType: 'unauthorized', error: 'Ungültige Zugangsdaten' },
      { status: 401 },
    );
  }

  const exp = Date.now() + SESSION_TTL_MS;
  const token = await createSession({ sub: user.sub, email: user.email, exp });

  const jar = await cookies();
  jar.set(SESSION_COOKIE, token, {
    httpOnly: true,
    sameSite: 'lax',
    path: '/',
    expires: new Date(exp),
    secure: process.env.NODE_ENV === 'production',
  });

  return Response.json({ status: 'success', data: { email: user.email } });
}
