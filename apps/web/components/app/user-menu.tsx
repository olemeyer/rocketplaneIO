'use client';

import { useState } from 'react';
import { useRouter } from 'next/navigation';

export function UserMenu({ email }: { email: string }) {
  const router = useRouter();
  const [loading, setLoading] = useState(false);
  const initial = (email.trim()[0] ?? '?').toUpperCase();

  async function signOut() {
    setLoading(true);
    try {
      await fetch('/api/auth/logout', { method: 'POST' });
      router.push('/login');
      router.refresh();
    } finally {
      setLoading(false);
    }
  }

  return (
    <div className="flex items-center gap-2">
      <div className="flex items-center gap-2 rounded-md border border-line bg-raised px-2 py-1">
        <span className="grid h-5 w-5 place-items-center rounded-full bg-accent text-[10px] font-semibold text-[#04140f]">
          {initial}
        </span>
        <span className="hidden max-w-[160px] truncate font-mono text-[11px] text-muted sm:inline">
          {email}
        </span>
      </div>
      <button
        onClick={signOut}
        disabled={loading}
        className="rounded-md border border-line px-2.5 py-1.5 text-[12px] text-muted transition-colors hover:text-strong disabled:opacity-60"
      >
        {loading ? '…' : 'Sign out'}
      </button>
    </div>
  );
}
