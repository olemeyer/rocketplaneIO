// service-color.ts — konsistente Service-Identität über alle Trace-Ansichten
// (Waterfall-Dot, Flame-Graph-Block, Trace-Graph-Node), das dash0-Muster:
// „Spans from the same service share a color". Die Palette ist bewusst
// NICHT-semantisch (kein Grün/Gold/Crimson — die gehören der Status-Skala,
// Design-Gesetz №2): kühle, gedeckte Hues, die im Dark-Cockpit ruhig bleiben.

export const SERVICE_PALETTE = [
  '#7C9EC9', // steel
  '#5FAFA7', // teal
  '#9C8DC9', // violet
  '#C78FAD', // mauve
  '#6FA8CC', // sky
  '#8E9DAD', // slate
  '#B08FC9', // orchid
  '#7CB3BE', // glacier
];

// Zuordnung nach Erst-Auftreten im Trace — garantiert kollisionsfrei bis die
// Palette erschöpft ist, und stabil innerhalb der Seite (alle Views teilen
// dieselbe Map).
export function serviceColorMap(services: string[]): Map<string, string> {
  const m = new Map<string, string>();
  services.forEach((s, i) => m.set(s, SERVICE_PALETTE[i % SERVICE_PALETTE.length] ?? '#8E9DAD'));
  return m;
}

// Block-/Node-Flächen aus der Service-Farbe ableiten (theme-fest via color-mix).
export function serviceFill(color: string): { bg: string; border: string } {
  return {
    bg: `color-mix(in oklab, ${color} 24%, transparent)`,
    border: `color-mix(in oklab, ${color} 62%, transparent)`,
  };
}
