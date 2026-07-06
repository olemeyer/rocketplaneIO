import { NextResponse, type NextRequest } from 'next/server';

// Auth-Gate (Edge). NUR Presence-Check des Session-Cookies — die echte Validierung
// (HMAC-Signatur, Ablauf, Org-Kontext) macht die Control-Plane bei jedem /api-Call.
// Ziel: unangemeldete Navigation früh auf /login umlenken, ohne Roundtrip.
const SESSION_COOKIE = 'rp_session';

export function middleware(req: NextRequest) {
  const { pathname } = req.nextUrl;

  // Öffentliche / proxied Pfade nie gaten:
  //  - /auth/*  → OIDC-Redirects & Dev-Login (Control-Plane)
  //  - /api/*   → same-origin Proxy zur Control-Plane (macht eigene Auth)
  //  - /_next, favicon, statische Assets
  const isPublic =
    pathname.startsWith('/auth') ||
    pathname.startsWith('/api') ||
    pathname.startsWith('/_next') ||
    pathname === '/favicon.ico' ||
    pathname === '/robots.txt';

  if (isPublic) return NextResponse.next();

  const hasSession = req.cookies.has(SESSION_COOKIE);

  // /setup (First-User-Onboarding) ist öffentlich; die Seite prüft selbst per
  // /api/setup/status, ob überhaupt noch ein Setup nötig ist.
  if (pathname === '/setup') {
    if (hasSession) {
      const url = req.nextUrl.clone();
      url.pathname = '/';
      return NextResponse.redirect(url);
    }
    return NextResponse.next();
  }

  if (pathname === '/login') {
    // Bereits angemeldet? Dann weg von der Login-Seite.
    if (hasSession) {
      const url = req.nextUrl.clone();
      url.pathname = '/';
      return NextResponse.redirect(url);
    }
    return NextResponse.next();
  }

  if (!hasSession) {
    const url = req.nextUrl.clone();
    url.pathname = '/login';
    url.search = '';
    return NextResponse.redirect(url);
  }

  return NextResponse.next();
}

export const config = {
  // Alles ausser statischen Next-Assets durchläuft die Middleware.
  matcher: ['/((?!_next/static|_next/image|favicon.ico).*)'],
};
