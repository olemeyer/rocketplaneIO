import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';

vi.mock('@/lib/api/query', () => ({
  getLatestTrace: vi.fn(),
  getServiceHealth: vi.fn(),
}));

import { getLatestTrace } from '@/lib/api/query';
import { TraceWaterfall } from './trace-waterfall';
import { traceViewSample } from '@/src/test/fixtures/explore';

const mockGet = vi.mocked(getLatestTrace);

beforeEach(() => mockGet.mockReset());

describe('<TraceWaterfall>', () => {
  it('rendert die Spans des neuesten Trace', async () => {
    mockGet.mockResolvedValue(traceViewSample);
    render(<TraceWaterfall />);
    expect(await screen.findByText('POST /checkout')).toBeInTheDocument();
    expect(screen.getByText('payment.charge')).toBeInTheDocument();
    expect(screen.getByText('cart.load')).toBeInTheDocument();
    // Fehleranzahl im Header.
    expect(screen.getByText(/1 errors/)).toBeInTheDocument();
  });

  it('zeigt Empty-State, wenn kein Trace existiert', async () => {
    mockGet.mockResolvedValue(null);
    render(<TraceWaterfall />);
    expect(await screen.findByText(/Kein Trace gefunden/)).toBeInTheDocument();
  });
});
