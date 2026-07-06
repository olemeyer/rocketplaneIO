// Minimaler className-Merger (ohne Dependency). Nimmt Strings/Falsy/Records und
// verbindet die truthy Klassen mit Leerzeichen.
export type ClassValue = string | number | false | null | undefined | Record<string, boolean>;

export function cn(...parts: ClassValue[]): string {
  const out: string[] = [];
  for (const p of parts) {
    if (!p) continue;
    if (typeof p === 'string' || typeof p === 'number') {
      out.push(String(p));
    } else {
      for (const [key, active] of Object.entries(p)) {
        if (active) out.push(key);
      }
    }
  }
  return out.join(' ');
}
