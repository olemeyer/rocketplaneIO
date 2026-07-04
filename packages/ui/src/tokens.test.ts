import { describe, it, expect } from 'vitest';
import { cssVars, signal, status, theme, tokens } from './index.ts';

describe('design tokens', () => {
  it('deckt die vollständige Status-Semantik ab', () => {
    expect(Object.keys(status)).toEqual(['resolved', 'degraded', 'critical', 'unknown']);
  });

  it('vergibt jedem Signaltyp eine gültige Hex-Farbe', () => {
    for (const value of Object.values(signal)) {
      expect(value).toMatch(/^#[0-9a-f]{6}$/i);
    }
  });

  it('definiert Dark- und Light-Theme mit denselben Keys', () => {
    expect(Object.keys(theme.dark)).toEqual(Object.keys(theme.light));
  });

  it('bündelt alle Token-Gruppen im tokens-Objekt', () => {
    expect(Object.keys(tokens)).toEqual(
      expect.arrayContaining(['status', 'signal', 'brand', 'theme', 'space', 'radius']),
    );
  });
});

describe('cssVars()', () => {
  it('erzeugt --rp-präfixierte Custom-Properties', () => {
    const vars = cssVars('dark');
    expect(vars['--rp-bg-base']).toBe('#0a0e14');
    expect(Object.keys(vars).every((key) => key.startsWith('--rp-'))).toBe(true);
  });

  it('liefert für Light ein anderes Grundflächen-Token', () => {
    expect(cssVars('light')['--rp-bg-base']).toBe('#f7f9fb');
  });
});
