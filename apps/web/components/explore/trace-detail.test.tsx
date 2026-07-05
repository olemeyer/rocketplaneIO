import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';

vi.mock('next/link', () => ({
  default: ({ href, children }: { href: string; children: React.ReactNode }) => <a href={href}>{children}</a>,
}));
// useLiveQuery direkt mocken: entkoppelt den Rendering-Test vom Fetch/Promise.
vi.mock('@/lib/hooks/use-live-query', () => ({ useLiveQuery: vi.fn() }));
// RelatedLogs hat einen eigenen useLiveQuery-Aufruf -> hier ausblenden.
vi.mock('./related-logs', () => ({ RelatedLogs: () => null }));

import { useLiveQuery } from '@/lib/hooks/use-live-query';
import { TraceDetail } from './trace-detail';
import { traceViewSample } from '@/src/test/fixtures/explore';

const mockHook = vi.mocked(useLiveQuery);
const refresh = vi.fn();

beforeEach(() => mockHook.mockReset());

describe('<TraceDetail>', () => {
  it('zeigt KPIs, Legende und Waterfall', () => {
    mockHook.mockReturnValue({ data: traceViewSample, status: 'success', error: null, refresh });
    render(<TraceDetail traceId={traceViewSample.detail.traceId} />);

    expect(screen.getByText('POST /checkout')).toBeInTheDocument();
    expect(screen.getByText('payment.charge')).toBeInTheDocument();
    expect(screen.getByText('duration')).toBeInTheDocument();
    expect(screen.getByText('services')).toBeInTheDocument();
  });

  it('zeigt einen Not-Found-Zustand bei code=not_found', () => {
    mockHook.mockReturnValue({
      data: null,
      status: 'error',
      error: { status: 404, code: 'not_found', message: 'trace not found' },
      refresh,
    });
    render(<TraceDetail traceId="deadbeef" />);
    expect(screen.getByText(/Trace nicht gefunden/)).toBeInTheDocument();
  });

  it('zeigt einen generischen Fehler bei anderem Fehlercode', () => {
    mockHook.mockReturnValue({
      data: null,
      status: 'error',
      error: { status: 500, code: 'internal', message: 'boom' },
      refresh,
    });
    render(<TraceDetail traceId="deadbeef" />);
    expect(screen.getByText(/boom/)).toBeInTheDocument();
    expect(screen.queryByText(/Trace nicht gefunden/)).not.toBeInTheDocument();
  });
});
