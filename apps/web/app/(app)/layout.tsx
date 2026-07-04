import type { ReactNode } from 'react';
import { cookies } from 'next/headers';
import { SESSION_COOKIE } from '@/lib/auth/config';
import { verifySession } from '@/lib/auth/session';
import { Sidebar } from '@/components/app/sidebar';
import { Topbar } from '@/components/app/topbar';
import { CommandPaletteHost } from '@/components/explore/command-palette-host';

// Authentifiziertes App-Shell-Layout: echtes Vollbild-Layout (Sidebar + Topbar
// + scrollbarer Content), kein Fenster-Frame. Die Middleware stellt sicher, dass
// hier nur mit gültiger Session gerendert wird.
export default async function AppLayout({ children }: { children: ReactNode }) {
  const token = (await cookies()).get(SESSION_COOKIE)?.value;
  const session = token ? await verifySession(token) : null;
  const email = session?.email ?? 'account';

  return (
    <div className="grain relative flex h-screen overflow-hidden">
      <Sidebar />
      <div className="flex min-w-0 flex-1 flex-col">
        <Topbar email={email} />
        <main className="relative flex-1 overflow-y-auto">
          <div className="aurora pointer-events-none opacity-40" aria-hidden />
          <div className="relative mx-auto max-w-6xl px-6 py-6">{children}</div>
        </main>
      </div>
      <CommandPaletteHost />
    </div>
  );
}
