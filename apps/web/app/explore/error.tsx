'use client';

export default function ExploreError({ error, reset }: { error: Error; reset: () => void }) {
  return (
    <div className="grid min-h-screen place-items-center px-6">
      <div className="max-w-md rounded-xl border border-line bg-raised p-6 text-center shadow-card">
        <h2 className="font-display text-[16px] font-semibold text-strong">Etwas ist schiefgelaufen</h2>
        <p className="mt-2 text-[13px] text-muted">{error.message}</p>
        <button
          onClick={reset}
          className="mt-4 rounded-md bg-accent px-3 py-1.5 text-[13px] font-medium text-[#04140f] transition-[filter] hover:brightness-110"
        >
          Erneut versuchen
        </button>
      </div>
    </div>
  );
}
