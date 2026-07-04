import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';

vi.mock('next/link', () => ({
  default: ({ href, children }: { href: string; children: React.ReactNode }) => <a href={href}>{children}</a>,
}));
vi.mock('@/lib/api/query', async (orig) => ({
  ...(await orig<typeof import('@/lib/api/query')>()),
  getServiceHealth: vi.fn(),
  getTraces: vi.fn(),
}));

import { getServiceHealth, getTraces } from '@/lib/api/query';
import { TraceList } from './trace-list';
import { serviceHealthSample, traceList } from '@/src/test/fixtures/explore';

const mockServices = vi.mocked(getServiceHealth);
const mockTraces = vi.mocked(getTraces);

beforeEach(() => {
  mockServices.mockReset().mockResolvedValue(serviceHealthSample);
  mockTraces.mockReset().mockResolvedValue(traceList);
});

describe('<TraceList>', () => {
  it('rendert die Trace-Zeilen als Links zur Detail-Seite', async () => {
    render(<TraceList />);
    const row = await screen.findByRole('link', { name: /POST \/checkout/ });
    expect(row).toHaveAttribute('href', `/traces/${traceList.traces[0]!.traceId}`);
  });

  it('leitet den initialen Service als Filter weiter', async () => {
    render(<TraceList initialService="payment-gateway" />);
    await waitFor(() => expect(mockTraces).toHaveBeenCalled());
    const arg = mockTraces.mock.calls[0]?.[0];
    expect(arg?.service).toBe('payment-gateway');
  });

  it('lädt bei Status-Wechsel neu mit status=error', async () => {
    render(<TraceList />);
    await screen.findByText('POST /checkout');
    fireEvent.click(screen.getByRole('button', { name: 'errors' }));
    await waitFor(() => {
      const last = mockTraces.mock.calls.at(-1)?.[0];
      expect(last?.status).toBe('error');
    });
  });

  it('zeigt einen Empty-State ohne Ergebnisse', async () => {
    mockTraces.mockResolvedValue({ traces: [] });
    render(<TraceList />);
    expect(await screen.findByText(/Keine Traces/)).toBeInTheDocument();
  });
});
