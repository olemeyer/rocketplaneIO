import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';

vi.mock('next/link', () => ({
  default: ({ href, children }: { href: string; children: React.ReactNode }) => <a href={href}>{children}</a>,
}));
vi.mock('@/lib/hooks/use-live-query', () => ({ useLiveQuery: vi.fn() }));

import { useLiveQuery } from '@/lib/hooks/use-live-query';
import { AlertList } from './alert-list';
import { alertList } from '@/src/test/fixtures/explore';

const mockHook = vi.mocked(useLiveQuery);

beforeEach(() => mockHook.mockReset());

describe('<AlertList>', () => {
  it('zeigt Firing-Summary, Alert-Namen und Severity-Badges', () => {
    mockHook.mockReturnValue({ data: alertList, status: 'success', error: null, refresh: vi.fn() });
    render(<AlertList />);

    expect(screen.getByText('2 firing')).toBeInTheDocument();
    expect(screen.getByText('checkout-api error rate high')).toBeInTheDocument();
    expect(screen.getByText('critical')).toBeInTheDocument();
    // ok-Badge für die nicht feuernde Regel
    expect(screen.getByText('ok')).toBeInTheDocument();
    // Service-Deep-Link
    expect(screen.getAllByRole('link', { name: 'View service' })[0]).toHaveAttribute(
      'href',
      '/services/checkout-api',
    );
  });
});
