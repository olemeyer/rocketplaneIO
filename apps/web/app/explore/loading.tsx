export default function Loading() {
  return (
    <div className="grid min-h-screen place-items-center">
      <div className="flex items-center gap-2 font-mono text-[12px] text-faint">
        <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-accent" />
        Loading explore…
      </div>
    </div>
  );
}
