import { ChevronDown } from '@/components/icons';
import { UserMenu } from './user-menu';

export function Topbar({ email }: { email: string }) {
  return (
    <header className="flex h-14 shrink-0 items-center gap-3 border-b border-line px-4">
      <div className="flex items-center gap-2 font-mono text-[12px] text-faint">
        <span className="text-muted">acme-prod</span>
        <span>/</span>
        <span className="text-strong">Explore</span>
      </div>

      <div className="ml-auto flex items-center gap-2">
        <span className="hidden items-center gap-1 rounded-md border border-line px-2 py-1 font-mono text-[11px] text-muted md:flex">
          production <ChevronDown className="h-3 w-3" />
        </span>
        <span className="hidden items-center gap-1 rounded-md border border-line px-2 py-1 font-mono text-[11px] text-muted md:flex">
          Last 15m <ChevronDown className="h-3 w-3" />
        </span>
        <kbd className="hidden rounded border border-line bg-overlay px-1.5 py-0.5 font-mono text-[10px] text-faint sm:inline">
          ⌘K
        </kbd>
        <UserMenu email={email} />
      </div>
    </header>
  );
}
