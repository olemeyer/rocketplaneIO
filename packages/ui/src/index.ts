export * from './tokens.ts';

import { theme, type ThemeName } from './tokens.ts';

/**
 * Erzeugt die CSS-Custom-Properties für ein Theme, z. B. um sie auf :root oder
 * ein [data-theme]-Element zu setzen. Prefix `--rp-`.
 *
 * @example
 * Object.assign(document.documentElement.style, {}) // ...
 * for (const [k, v] of Object.entries(cssVars('dark'))) el.style.setProperty(k, v);
 */
export function cssVars(name: ThemeName): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [key, value] of Object.entries(theme[name])) {
    out[`--rp-${key}`] = value;
  }
  return out;
}
