import { NextResponse, type NextRequest } from 'next/server';
import { SESSION_COOKIE } from '@/lib/auth/config';
import { verifySession } from '@/lib/auth/session';

// Schützt die App-Routen und den Query-Proxy. Ohne gültige Session:
//  - HTML-Route -> Redirect auf /login?next=<pfad>
//  - API-Proxy  -> 401 JSON
export async function middleware(req: NextRequest) {
  const token = req.cookies.get(SESSION_COOKIE)?.value;
  const session = token ? await verifySession(token) : null;
  if (session) return NextResponse.next();

  const { pathname, search } = req.nextUrl;

  if (pathname.startsWith('/api/rp')) {
    return NextResponse.json(
      { status: 'error', errorType: 'unauthorized', error: 'authentication required' },
      { status: 401 },
    );
  }

  const url = req.nextUrl.clone();
  url.pathname = '/login';
  url.search = '';
  url.searchParams.set('next', pathname + search);
  return NextResponse.redirect(url);
}

export const config = {
  matcher: ['/explore', '/explore/:path*', '/api/rp/:path*'],
};
