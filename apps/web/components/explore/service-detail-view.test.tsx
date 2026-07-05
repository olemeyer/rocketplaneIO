import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';

vi.mock('next/link', () => ({
  default: ({ href, children }: { href: string; children: React.ReactNode }) => <a href={href}>{children}</a>,
}));
vi.mock('@/lib/hooks/use-live-query', () => ({ useLiveQuery: vi.fn() }));

import { useLiveQuery } from '@/lib/hooks/use-live-query';
import { ServiceDetailView } from './service-detail-view';
import { serviceDetail } from '@/src/test/fixtures/explore';

const mockHook = vi.mocked(useLiveQuery);
const refresh = vi.fn();

beforeEach(() => mockHook.mockReset());

describe('<ServiceDetailView>', () => {
  it('rendert Charts, Operationen, Dependencies und Deep-Links', () => {
    mockHook.mockReturnValue({ data: serviceDetail, status: 'success', error: null, refresh });
    render(<ServiceDetailView name="checkout-api" />);

    expect(screen.getByRole('heading', { name: 'checkout-api' })).toBeInTheDocument();
    expect(screen.getByText('POST /checkout')).toBeInTheDocument();
    expect(screen.getByText('payment-gateway')).toBeInTheDocument();
    // Chart-Labels
    expect(screen.getByText('latency p95')).toBeInTheDocument();
    // Deep-Links zu Traces/Logs des Service
    expect(screen.getByRole('link', { name: /Traces/ })).toHaveAttribute('href', '/traces?service=checkout-api');
    expect(screen.getByRole('link', { name: /Logs/ })).toHaveAttribute('href', '/logs?service=checkout-api');
  });

  it('zeigt Not-Found bei code=not_found', () => {
    mockHook.mockReturnValue({
      data: null,
      status: 'error',
      error: { status: 404, code: 'not_found', message: 'x' },
      refresh,
    });
    render(<ServiceDetailView name="ghost" />);
    expect(screen.getByText(/Service nicht gefunden/)).toBeInTheDocument();
  });
});
