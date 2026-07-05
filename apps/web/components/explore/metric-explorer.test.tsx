import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';

vi.mock('@/lib/api/query', async (orig) => ({
  ...(await orig<typeof import('@/lib/api/query')>()),
  getMetrics: vi.fn(),
  getMetric: vi.fn(),
}));

import { getMetric, getMetrics } from '@/lib/api/query';
import { MetricExplorer } from './metric-explorer';
import { metricData, metricList } from '@/src/test/fixtures/explore';

const mockList = vi.mocked(getMetrics);
const mockMetric = vi.mocked(getMetric);

beforeEach(() => {
  mockList.mockReset().mockResolvedValue(metricList);
  mockMetric.mockReset().mockResolvedValue(metricData);
});

describe('<MetricExplorer>', () => {
  it('listet Metriken und lädt die erste automatisch', async () => {
    render(<MetricExplorer />);
    // Liste zeigt die Metrik-Namen
    expect(await screen.findByText('system.cpu.utilization')).toBeInTheDocument();
    // erste Metrik wird geladen (Chart-Panel-Titel)
    await waitFor(() => expect(mockMetric).toHaveBeenCalled());
    expect(mockMetric.mock.calls[0]?.[0]).toBe(metricList.metrics[0]!.name);
  });

  it('lädt eine andere Metrik bei Klick', async () => {
    render(<MetricExplorer />);
    await screen.findByText('system.cpu.utilization');
    fireEvent.click(screen.getByText('system.cpu.utilization'));
    await waitFor(() => {
      expect(mockMetric.mock.calls.some((c) => c[0] === 'system.cpu.utilization')).toBe(true);
    });
  });

  it('filtert die Metrik-Liste', async () => {
    render(<MetricExplorer />);
    await screen.findByText('system.cpu.utilization');
    fireEvent.change(screen.getByPlaceholderText(/filter metrics/i), { target: { value: 'cpu' } });
    expect(screen.getByText('system.cpu.utilization')).toBeInTheDocument();
    expect(screen.queryByText('http.server.request.count')).not.toBeInTheDocument();
  });
});
