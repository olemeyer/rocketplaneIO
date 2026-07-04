import { cookies } from 'next/headers';
import { SESSION_COOKIE } from '@/lib/auth/config';

export async function POST() {
  const jar = await cookies();
  jar.delete(SESSION_COOKIE);
  return Response.json({ status: 'success' });
}
