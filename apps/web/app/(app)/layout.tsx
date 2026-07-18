'use client';

import type { ReactNode } from 'react';
import { MeProvider } from '@/components/app/me-context';
import { ScopeProvider } from '@/components/app/scope-context';
import { Sidebar, MobileNav, NavProvider } from '@/components/app/sidebar';
import { Topbar } from '@/components/app/topbar';

// App shell behind the auth gate: sidebar (≥md) + mobile drawer (<md) + topbar
// + shared contexts.
export default function AppLayout({ children }: { children: ReactNode }) {
  return (
    <MeProvider>
      <ScopeProvider>
        <NavProvider>
          <div className="flex min-h-screen bg-base">
            <Sidebar />
            <MobileNav />
            <div className="flex min-w-0 flex-1 flex-col">
              <Topbar />
              <main className="flex-1">{children}</main>
            </div>
          </div>
        </NavProvider>
      </ScopeProvider>
    </MeProvider>
  );
}
