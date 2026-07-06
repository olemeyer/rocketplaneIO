// Kleine Darstellungs-Helfer (theme-agnostisch, reine Strings).

/** Kompaktes „vor X" — für last_seen_at etc. */
export function timeAgo(iso?: string | null): string {
  if (!iso) return 'never';
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return 'never';
  const s = Math.floor((Date.now() - t) / 1000);
  if (s < 0) return 'just now';
  if (s < 5) return 'just now';
  if (s < 60) return `${s}s ago`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  const d = Math.floor(h / 24);
  if (d < 30) return `${d}d ago`;
  const mo = Math.floor(d / 30);
  if (mo < 12) return `${mo}mo ago`;
  return `${Math.floor(mo / 12)}y ago`;
}

/** Gekürzte UID (z.B. k8s_uid) für dichte, mono Tabellenzellen. */
export function shortId(id?: string | null, len = 8): string {
  if (!id) return '—';
  return id.length <= len ? id : `${id.slice(0, len)}…`;
}

/** Initialen aus Name oder E-Mail für Avatar-Fallback. */
export function initials(nameOrEmail?: string): string {
  const src = (nameOrEmail ?? '').trim();
  if (!src) return '?';
  if (src.includes('@')) return src[0]!.toUpperCase();
  const parts = src.split(/\s+/).filter(Boolean);
  if (parts.length === 1) return parts[0]!.slice(0, 2).toUpperCase();
  return (parts[0]![0]! + parts[parts.length - 1]![0]!).toUpperCase();
}
