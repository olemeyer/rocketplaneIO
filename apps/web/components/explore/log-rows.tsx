import Link from 'next/link';
import { colorForService, severityTone } from '@/lib/api/query';
import type { LogRecord } from '@/lib/api';

function ts(ms: number): string {
  const d = new Date(ms);
  const p = (n: number, l = 2) => String(n).padStart(l, '0');
  return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}.${p(d.getMilliseconds(), 3)}`;
}

// LogRows rendert eine Liste von Log-Records (präsentational, geteilt von der
// Logs-Seite und den "Related Logs" im Trace-Detail).
export function LogRows({ logs, showTraceLink = true }: { logs: LogRecord[]; showTraceLink?: boolean }) {
  return (
    <div className="divide-y divide-line font-mono text-[12px]">
      {logs.map((l, i) => {
        const tone = severityTone(l.severity);
        return (
          <div key={i} className="flex items-start gap-3 px-3 py-1.5 hover:bg-overlay">
            <span className="shrink-0 text-faint">{ts(l.timestamp)}</span>
            <span className={`shrink-0 rounded px-1.5 py-0.5 text-[10px] ${tone.className}`}>
              {tone.label}
            </span>
            <span className="flex shrink-0 items-center gap-1.5" style={{ width: 132 }}>
              <span className="h-2 w-2 shrink-0 rounded-full" style={{ background: colorForService(l.serviceName) }} />
              <span className="truncate text-muted">{l.serviceName}</span>
            </span>
            <span className="min-w-0 flex-1 truncate text-strong">{l.body}</span>
            {showTraceLink && l.traceId && (
              <Link
                href={`/traces/${l.traceId}`}
                className="shrink-0 text-faint underline-offset-2 hover:text-accent hover:underline"
                title="zum Trace"
              >
                {l.traceId.slice(0, 8)}
              </Link>
            )}
          </div>
        );
      })}
    </div>
  );
}
