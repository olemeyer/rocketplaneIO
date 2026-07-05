import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';

vi.mock('next/link', () => ({
  default: ({ href, children }: { href: string; children: React.ReactNode }) => <a href={href}>{children}</a>,
}));
vi.mock('@/lib/api/query', async (orig) => ({
  ...(await orig<typeof import('@/lib/api/query')>()),
  getServiceHealth: vi.fn(),
  getLogs: vi.fn(),
}));

import { getLogs, getServiceHealth } from '@/lib/api/query';
import { LogList } from './log-list';
import { logList, serviceHealthSample } from '@/src/test/fixtures/explore';

const mockServices = vi.mocked(getServiceHealth);
const mockLogs = vi.mocked(getLogs);

beforeEach(() => {
  mockServices.mockReset().mockResolvedValue(serviceHealthSample);
  mockLogs.mockReset().mockResolvedValue(logList);
});

describe('<LogList>', () => {
  it('rendert Log-Zeilen mit Trace-Link', async () => {
    render(<LogList />);
    expect(await screen.findByText('POST /checkout failed: downstream error')).toBeInTheDocument();
    expect(screen.getByText('ERROR')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: logList.logs[0]!.traceId!.slice(0, 8) })).toHaveAttribute(
      'href',
      `/traces/${logList.logs[0]!.traceId}`,
    );
  });

  it('lädt bei Level-Wechsel mit minSeverity neu', async () => {
    render(<LogList />);
    await screen.findByText('POST /checkout failed: downstream error');
    fireEvent.change(screen.getByDisplayValue('all levels'), { target: { value: '17' } });
    await waitFor(() => {
      const last = mockLogs.mock.calls.at(-1)?.[0];
      expect(last?.minSeverity).toBe(17);
    });
  });
});
