import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';

vi.mock('@/lib/api/query', () => ({
  getServiceHealth: vi.fn(),
  getLatestTrace: vi.fn(),
}));

import { getServiceHealth } from '@/lib/api/query';
import { ServiceHealthPanel } from './service-health';
import { serviceHealthSample } from '@/src/test/fixtures/explore';

const mockGet = vi.mocked(getServiceHealth);

beforeEach(() => mockGet.mockReset());

describe('<ServiceHealthPanel>', () => {
  it('zeigt Service-Karten bei Erfolg', async () => {
    mockGet.mockResolvedValue(serviceHealthSample);
    render(<ServiceHealthPanel />);
    expect(await screen.findByText('checkout-api')).toBeInTheDocument();
    expect(screen.getByText('842ms')).toBeInTheDocument();
    expect(screen.getByText('4.2%')).toBeInTheDocument();
  });

  it('zeigt Empty-State bei leerer Liste', async () => {
    mockGet.mockResolvedValue([]);
    render(<ServiceHealthPanel />);
    expect(await screen.findByText(/Keine Services/)).toBeInTheDocument();
  });

  it('zeigt Error-State und lädt bei Retry neu', async () => {
    mockGet.mockRejectedValueOnce({ status: 500, code: 'internal', message: 'boom' });
    render(<ServiceHealthPanel />);
    expect(await screen.findByText(/Fehler beim Laden/)).toBeInTheDocument();

    mockGet.mockResolvedValueOnce(serviceHealthSample);
    fireEvent.click(screen.getByText('Erneut versuchen'));
    expect(await screen.findByText('checkout-api')).toBeInTheDocument();
    expect(mockGet).toHaveBeenCalledTimes(2);
  });
});
