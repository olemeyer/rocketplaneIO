import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';

const push = vi.fn();
vi.mock('next/navigation', () => ({ useRouter: () => ({ push }) }));
vi.mock('@/lib/hooks/use-live-query', () => ({ useLiveQuery: vi.fn() }));

import { useLiveQuery } from '@/lib/hooks/use-live-query';
import { ServiceMap } from './service-map';
import { serviceMap } from '@/src/test/fixtures/explore';

const mockHook = vi.mocked(useLiveQuery);

beforeEach(() => {
  push.mockReset();
  mockHook.mockReset();
});

describe('<ServiceMap>', () => {
  it('rendert einen Knoten je Service und Kanten', () => {
    mockHook.mockReturnValue({ data: serviceMap, status: 'success', error: null, refresh: vi.fn() });
    const { container } = render(<ServiceMap />);

    for (const n of serviceMap.nodes) {
      expect(screen.getByRole('link', { name: n.name })).toBeInTheDocument();
    }
    // Kanten als <path> im SVG (edges + …), mindestens so viele wie edges.
    expect(container.querySelectorAll('path').length).toBeGreaterThanOrEqual(serviceMap.edges.length);
  });

  it('navigiert bei Klick auf einen Knoten', () => {
    mockHook.mockReturnValue({ data: serviceMap, status: 'success', error: null, refresh: vi.fn() });
    render(<ServiceMap />);
    fireEvent.click(screen.getByRole('link', { name: 'payment-gateway' }));
    expect(push).toHaveBeenCalledWith('/services/payment-gateway');
  });

  it('zeigt einen Empty-State ohne Knoten', () => {
    mockHook.mockReturnValue({
      data: { window: { start: 0, end: 0 }, nodes: [], edges: [] },
      status: 'success',
      error: null,
      refresh: vi.fn(),
    });
    render(<ServiceMap />);
    expect(screen.getByText(/Noch keine Topologie/)).toBeInTheDocument();
  });
});
