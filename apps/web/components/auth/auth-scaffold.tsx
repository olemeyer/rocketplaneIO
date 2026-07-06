import type { ReactNode } from 'react';
import { ThemeToggle } from '@/components/ui';
import { Lockup } from '@/components/brand/logo';

// Auth-Layout (Login + Setup) — Konzept „I/O": das „IO" im Namen als Input→Output.
// Swiss/Bauhaus-Raster + Aurora. Links das I/O-Diagramm (rohe Signale → Aurora-
// Engine → eine saubere Antwort), rechts das Login als „OUTPUT"-Terminal. Auf
// kleinen Screens kollabiert alles auf die Formularspalte.
export function AuthScaffold({
  title,
  subtitle,
  children,
  footer,
}: {
  title: string;
  subtitle: string;
  children: ReactNode;
  footer?: ReactNode;
}) {
  return (
    <main className="relative min-h-screen overflow-hidden bg-base">
      <div className="swiss-grid pointer-events-none absolute inset-0 opacity-70" aria-hidden />
      <div className="aurora-glow" aria-hidden />

      {/* Swiss-Kopfzeile */}
      <div className="relative z-10 flex items-center justify-between border-b border-line px-6 py-5 lg:px-10 lg:py-6">
        <Lockup />
        <div className="flex items-center gap-5">
          <span className="hidden font-mono text-[10px] uppercase tracking-[0.16em] text-faint sm:inline">
            input · output
          </span>
          <ThemeToggle />
        </div>
      </div>

      <div className="relative z-10 grid min-h-[calc(100vh-73px)] grid-cols-1 lg:grid-cols-[1.5fr_1fr]">
        {/* ── Bühne: I/O-Diagramm + Swiss-Headline (nur ≥lg) ── */}
        <section className="reveal hidden flex-col justify-between border-r border-line px-10 py-12 lg:flex">
          <div className="rp-micro !text-[10px] text-faint">fig. 01 — telemetry pipeline</div>

          <div className="my-auto">
            <IOFigure className="w-full max-w-[560px]" />
            <h1 className="mt-12 font-display text-[46px] font-bold leading-[0.98] tracking-tightest text-ink">
              Signals in.
              <br />
              Fixes <span style={{ color: 'var(--rp-ink-mid)' }}>out.</span>
            </h1>
            <p className="mt-4 max-w-[40ch] font-mono text-[12.5px] leading-relaxed text-mid">
              Zero-code eBPF ingests every trace, log and flow — and returns safe,
              reversible actions. Raw in, control out.
            </p>
          </div>

          <div className="mt-10 grid grid-cols-4 gap-4 border-t border-line pt-4 font-mono text-mid tnum">
            {[['IN', '1.24k/s'], ['OUT', '3 fixes'], ['p95', '240ms'], ['ANOMALY', '01']].map(([k, v], i) => (
              <div key={i}>
                <div className="text-[9.5px] uppercase tracking-[0.14em] text-faint">{k}</div>
                <div className="mt-0.5 text-[15px] text-ink" style={i === 3 ? { color: 'var(--rp-node-crit)' } : undefined}>{v}</div>
              </div>
            ))}
          </div>
        </section>

        {/* ── Output: die Formularspalte ── */}
        <section className="reveal flex flex-col justify-center px-6 py-12 lg:px-9">
          <div className="mb-8 lg:hidden">
            <Lockup />
          </div>

          <div className="mb-6 flex items-baseline justify-between border-b border-line-strong pb-3">
            <h2 className="font-display text-[26px] font-bold leading-none tracking-tightest text-ink">{title}</h2>
            <span className="font-mono text-[10px] uppercase tracking-[0.14em] text-faint">output ›</span>
          </div>
          {subtitle ? <p className="mb-6 text-[12.5px] leading-relaxed text-muted">{subtitle}</p> : null}

          {children}

          {footer ? (
            <div className="mt-6 text-[12px] leading-relaxed text-muted">{footer}</div>
          ) : null}

          <div className="mt-8 flex items-center gap-2.5 font-mono text-[11px] text-muted">
            <span className="rp-breath inline-block h-1.5 w-1.5 rounded-full" style={{ background: 'var(--rp-green)', color: 'var(--rp-green)' }} />
            self-hosted · open source
          </div>
        </section>
      </div>
    </main>
  );
}

// Nummeriertes Swiss-Feld (Bauhaus: die Zahl ordnet; Underline statt Box).
export function NumField({
  n,
  label,
  hint,
  ...props
}: React.InputHTMLAttributes<HTMLInputElement> & { n: string; label: string; hint?: string }) {
  return (
    <label className="block">
      <span className="mb-1.5 flex items-baseline gap-2">
        <span className="font-mono text-[10px] font-semibold text-faint tnum">{n}</span>
        <span className="rp-micro !text-[10px]">{label}</span>
        {hint ? <span className="ml-auto font-mono text-[10px] text-faint">{hint}</span> : null}
      </span>
      <input
        {...props}
        className="h-11 w-full border-0 border-b bg-transparent px-0.5 font-mono text-[13px] text-ink outline-none transition-colors placeholder:text-faint rp-focus"
        style={{ borderColor: 'var(--rp-line-strong)' }}
      />
    </label>
  );
}

// Text-Input (Legacy-API, weiterhin exportiert): inset-Fläche, Mono-Value.
export function Field({
  label,
  hint,
  ...props
}: React.InputHTMLAttributes<HTMLInputElement> & { label: string; hint?: string }) {
  return (
    <label className="block">
      <span className="mb-1.5 flex items-center justify-between gap-2">
        <span className="rp-micro">{label}</span>
        {hint ? <span className="font-mono text-[10px] text-faint">{hint}</span> : null}
      </span>
      <input
        {...props}
        className="h-10 w-full rounded-skin-sm border border-line bg-inset px-3 font-mono text-[13px] text-ink outline-none transition-colors placeholder:text-faint focus:border-ink rp-focus"
      />
    </label>
  );
}

// Das I/O-Diagramm: rohe Signale (Input, „I") → Aurora-Kreis („O", die Engine)
// → eine saubere Ausgabe-Linie (Output). EINE crimson Anomalie unterm Passer.
function IOFigure({ className }: { className?: string }) {
  const ink = 'var(--rp-ink)';
  const edge = 'var(--rp-map-edge)';
  const crit = 'var(--rp-node-crit)';
  const ac = 'var(--rp-accent)';
  const mono = 'var(--rp-font-mono)';
  const rows = [46, 30, 58, 24, 40, 52, 34, 48, 28];
  const critRow = 5;
  const y0 = 44, dy = 20;
  return (
    <svg viewBox="0 0 560 300" className={className} aria-hidden>
      <defs>
        <radialGradient id="io-aurora" cx="50%" cy="42%" r="60%">
          <stop offset="0%" stopColor="var(--rp-ember-1)" stopOpacity="0.95" />
          <stop offset="52%" stopColor="var(--rp-ember-2)" stopOpacity="0.55" />
          <stop offset="100%" stopColor="var(--rp-ember-2)" stopOpacity="0" />
        </radialGradient>
      </defs>

      {/* INPUT — der „I": gestapelte rohe Signale (eine crimson) */}
      <g>
        {rows.map((len, i) => (
          <rect key={i} x={20} y={y0 + i * dy} width={len} height={6} rx={1}
            fill={i === critRow ? crit : edge} opacity={i === critRow ? 0.95 : 0.7} />
        ))}
      </g>
      <text x={20} y={y0 + rows.length * dy + 16} fontSize="10" fontFamily={mono}
        fill="var(--rp-ink-muted)" letterSpacing="0.14em">INPUT</text>
      <text x={20} y={y0 - 12} fontSize="9" fontFamily={mono} fill="var(--rp-ink-faint)" letterSpacing="0.1em">traces · logs · flows</text>

      {/* Transformationspfeil */}
      <g stroke={edge} strokeWidth="1.4" fill="none" opacity="0.7">
        <path d="M96 150 H150" />
        <path d="M150 150 l-7 -4 M150 150 l-7 4" stroke={ink} />
      </g>

      {/* OUTPUT — der „O": Aurora-Kreis = die Engine */}
      <circle cx={330} cy={150} r={98} fill="url(#io-aurora)" />
      <circle cx={330} cy={150} r={98} fill="none" stroke={ink} strokeWidth="1.5" opacity="0.4" />
      <circle cx={330} cy={150} r={66} fill="none" stroke={ink} strokeWidth="1" opacity="0.18" strokeDasharray="2 6" />
      <rect x={330 - 15} y={150 - 15} width={30} height={30} rx={4} fill={crit} className="rp-breath" />
      <g stroke={ac} strokeWidth="1.4" fill="none" opacity="0.9" strokeLinecap="square">
        {([[-30, -30, 9, 9], [30, -30, -9, 9], [-30, 30, 9, -9], [30, 30, -9, -9]] as const).map(
          ([dx, dyy, hx, vy], i) => (
            <path key={i} d={`M${330 + dx} ${150 + dyy} h${hx} M${330 + dx} ${150 + dyy} v${vy}`} />
          ),
        )}
      </g>

      {/* saubere Output-Linie → eine Antwort */}
      <g>
        <path d="M428 150 H540" stroke={ink} strokeWidth="1.5" opacity="0.55" />
        <path d="M540 150 l-8 -4 M540 150 l-8 4" stroke={ink} strokeWidth="1.5" fill="none" />
        <rect x={470} y={132} width={44} height={6} rx={1} fill="var(--rp-green)" opacity="0.85" />
      </g>
      <text x={540} y={175} textAnchor="end" fontSize="10" fontFamily={mono}
        fill="var(--rp-ink-muted)" letterSpacing="0.14em">OUTPUT</text>
      <text x={540} y={128} textAnchor="end" fontSize="9" fontFamily={mono} fill="var(--rp-ink-faint)" letterSpacing="0.1em">safe action · rollback ready</text>
    </svg>
  );
}

// Google-Logo (offizielle Markenfarben — bewusst als einzige hardcoded Farben).
export function GoogleGlyph() {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" aria-hidden>
      <path fill="#4285F4" d="M23.52 12.27c0-.79-.07-1.54-.2-2.27H12v4.51h6.47a5.53 5.53 0 0 1-2.4 3.63v3h3.88c2.27-2.09 3.57-5.17 3.57-8.87Z" />
      <path fill="#34A853" d="M12 24c3.24 0 5.95-1.08 7.93-2.91l-3.88-3c-1.08.72-2.45 1.16-4.05 1.16-3.12 0-5.76-2.11-6.7-4.94H1.29v3.09A12 12 0 0 0 12 24Z" />
      <path fill="#FBBC05" d="M5.3 14.31a7.2 7.2 0 0 1 0-4.62V6.6H1.29a12 12 0 0 0 0 10.8l4.01-3.09Z" />
      <path fill="#EA4335" d="M12 4.75c1.77 0 3.35.61 4.6 1.8l3.44-3.44C17.95 1.19 15.24 0 12 0A12 12 0 0 0 1.29 6.6l4.01 3.09C6.24 6.86 8.88 4.75 12 4.75Z" />
    </svg>
  );
}
