import React from 'react';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ObservabilityPage } from '../../pages/observability/ObservabilityPage.jsx';
import { copyTextToClipboard, getObservabilityTrace, listObservabilityRecent } from './observabilityService.js';

vi.mock('./observabilityService.js', () => ({
  copyTextToClipboard: vi.fn(),
  getObservabilityTrace: vi.fn(),
  listObservabilityRecent: vi.fn(),
}));

describe('ObservabilityPage tail degradation display', () => {
  beforeEach(() => {
    listObservabilityRecent.mockResolvedValue({
      source: 'memory',
      degraded: true,
      tailError: 'tail reader unavailable',
      tailTimedOut: true,
      tailFilesScanned: 5,
      truncated: false,
      events: [{
        ts: '2026-06-02T09:01:22.459Z',
        traceId: 'trace-degraded',
        spanId: 'span-request',
        method: 'thread/start',
        status: 'error',
        durationMs: 12,
      }],
    });
    getObservabilityTrace.mockResolvedValue({ source: 'memory', events: [] });
    copyTextToClipboard.mockResolvedValue(true);
  });

  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it('shows degraded tail diagnostics on recent results', async () => {
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    render(
      <QueryClientProvider client={queryClient}>
        <ObservabilityPage />
      </QueryClientProvider>,
    );

    fireEvent.click(screen.getByRole('button', { name: '查询最新日志' }));
    const table = await screen.findByTestId('observability-recent-logs');

    expect(table).toHaveTextContent('degraded=true');
    expect(table).toHaveTextContent('tail_error=tail reader unavailable');
    expect(table).toHaveTextContent('tail_timed_out=true');
    expect(table).toHaveTextContent('tail_files_scanned=5');
  });
});
