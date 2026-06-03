import React from 'react';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ObservabilityPage } from './ObservabilityPage.jsx';
import { copyTextToClipboard, getObservabilityTrace, listObservabilityRecent } from '../../shared/api/backendApi.js';

vi.mock('../../shared/api/backendApi.js', () => ({
  copyTextToClipboard: vi.fn(),
  getObservabilityTrace: vi.fn(),
  listObservabilityRecent: vi.fn(),
}));

const recentResult = {
  source: 'memory',
  truncated: false,
  events: [
    {
      ts: '2026-06-02T09:01:22.459Z',
      trace_id: 'trace-frontend-1',
      span_id: 'span-request',
      method: 'thread/start',
      status: 'error',
      thread_id: 'thread-1',
      duration_ms: 12,
      error: 'thread start failed',
    },
  ],
};

const traceResult = {
  source: 'memory',
  truncated: false,
  total_duration_ms: 12,
  events: [
    {
      ts: '2026-06-02T09:01:22.459Z',
      trace_id: 'trace-frontend-1',
      span_id: 'span-request',
      method: 'thread/start',
      status: 'error',
      thread_id: 'thread-1',
      duration_ms: 12,
      error: 'thread start failed',
    },
  ],
};

function renderObservabilityPage() {
  return render(<ObservabilityPage />);
}

async function queryRecentLogs() {
  renderObservabilityPage();
  fireEvent.change(screen.getByLabelText('状态'), { target: { value: 'error' } });
  fireEvent.change(screen.getByLabelText('关键词'), { target: { value: 'thread/start' } });
  fireEvent.click(screen.getByRole('button', { name: '查询最新日志' }));
  return screen.findByTestId('observability-recent-logs');
}

describe('ObservabilityPage module', () => {
  beforeEach(() => {
    listObservabilityRecent.mockResolvedValue(recentResult);
    getObservabilityTrace.mockResolvedValue(traceResult);
    copyTextToClipboard.mockResolvedValue(true);
  });

  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it('exports the observability page component', () => {
    expect(ObservabilityPage).toBeTypeOf('function');
  });

  it('queries recent logs with the backend payload', async () => {
    const table = await queryRecentLogs();
    const [payload] = listObservabilityRecent.mock.calls[0];

    expect(payload).toEqual({
      limit: 50,
      status: 'error',
      component: '',
      method: '',
      traceId: '',
      threadId: '',
      agentId: '',
      keyword: 'thread/start',
    });
    expect(payload).not.toHaveProperty('includeTail');
    expect(table).toHaveTextContent('trace-frontend-1');
    expect(table).toHaveTextContent('thread start failed');
  });

  it('shows recent events that do not have a trace id', async () => {
    listObservabilityRecent.mockResolvedValueOnce({
      source: 'jsonl_tail',
      truncated: false,
      events: [
        {
          ts: '2026-06-03T06:15:50.000Z',
          method: 'provider.session.ready',
          phase: 'provider.session.ready',
          kind: 'provider',
          status: 'ok',
        },
      ],
    });

    renderObservabilityPage();
    fireEvent.click(screen.getByRole('button', { name: '查询最新日志' }));
    const table = await screen.findByTestId('observability-recent-logs');

    expect(table).toHaveTextContent('provider.session.ready');
    expect(table).toHaveTextContent('trace=-');
    expect(within(table).getByRole('button', { name: '打开 Trace -' })).toBeDisabled();
    expect(within(table).getByRole('button', { name: '复制 Trace ID -' })).toBeDisabled();
  });

  it('expands a trace with the backend payload', async () => {
    const table = await queryRecentLogs();

    fireEvent.click(within(table).getByRole('button', { name: '打开 Trace trace-frontend-1' }));

    await waitFor(() => expect(getObservabilityTrace).toHaveBeenCalledTimes(1));
    const [payload] = getObservabilityTrace.mock.calls[0];
    expect(payload).toEqual({ traceId: 'trace-frontend-1', limit: 50 });
    expect(payload).not.toHaveProperty('includeTail');
    expect(await within(table).findByTestId('observability-inline-trace-trace-frontend-1')).toHaveTextContent('Trace 结果');
    expect(within(table).getByRole('button', { name: '收起 Trace trace-frontend-1' })).toHaveAttribute('aria-expanded', 'true');
  });

  it('copies a trace id through the backend clipboard bridge', async () => {
    const table = await queryRecentLogs();

    fireEvent.click(within(table).getByRole('button', { name: '复制 Trace ID trace-frontend-1' }));

    await waitFor(() => expect(copyTextToClipboard).toHaveBeenCalledWith('trace-frontend-1'));
    expect(within(table).getByRole('button', { name: '复制 Trace ID trace-frontend-1' })).toHaveTextContent('已复制');
    expect(getObservabilityTrace).not.toHaveBeenCalled();
  });
});
