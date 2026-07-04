// Wortmarke „rocketplaneIO" — „IO" trägt den Aurora-Verlauf. Eine Quelle für
// alle Stellen (Nav, Login, Sidebar, Footer), damit die Marke konsistent bleibt.
export function Wordmark({ className }: { className?: string }) {
  return (
    <span className={className}>
      rocketplane
      <span className="text-aurora font-bold">IO</span>
    </span>
  );
}
