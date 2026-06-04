import React from 'react';
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
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

function deferredValue() {
  let resolve;
  let reject;
  const promise = new Promise((promiseResolve, promiseReject) => {
    resolve = promiseResolve;
    reject = promiseReject;
  });
  return { promise, reject, resolve };
}

function queryRecentLogs() {
  renderObservabilityPage();
  fireEvent.change(screen.getByLabelText('状态'), { target: { value: 'error' } });
  fireEvent.change(screen.getByLabelText('关键词'), { target: { value: 'thread/start' } });
  fireEvent.click(screen.getByRole('button', { name: '查询最新日志' }));
  return screen.findByTestId('observability-recent-logs');
}

function formatParsedTimestamp(value) {
  const parsed = new Date(value);
  const year = String(parsed.getFullYear()).padStart(4, '0');
  const month = String(parsed.getMonth() + 1).padStart(2, '0');
  const day = String(parsed.getDate()).padStart(2, '0');
  const hour = String(parsed.getHours()).padStart(2, '0');
  const minute = String(parsed.getMinutes()).padStart(2, '0');
  const second = String(parsed.getSeconds()).padStart(2, '0');
  return `${year}-${month}-${day} ${hour}:${minute}:${second}`;
}

describe('ObservabilityPage module', () => {
  beforeEach(() => {
    listObservabilityRecent.mockResolvedValue(recentResult);
    getObservabilityTrace.mockResolvedValue(traceResult);
    copyTextToClipboard.mockResolvedValue(true);
  });

  afterEach(() => {
    cleanup();
    vi.useRealTimers();
    vi.clearAllMocks();
  });

  it('exports the observability page component', () => {
    expect(ObservabilityPage).toBeTypeOf('function');
  });

  it('renders the trace page header without the old helper copy', () => {
    renderObservabilityPage();

    expect(screen.getByRole('heading', { name: '链路追踪' })).toBeInTheDocument();
    expect(screen.queryByText('Observability')).not.toBeInTheDocument();
    expect(screen.queryByText(/按条件筛选最近请求/)).not.toBeInTheDocument();
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

  it('renders expanded trace detail as a full-width grid panel instead of a colspan table row', async () => {
    const table = await queryRecentLogs();

    fireEvent.click(within(table).getByRole('button', { name: '打开 Trace trace-frontend-1' }));

    const inlineTrace = await within(table).findByTestId('observability-inline-trace-trace-frontend-1');
    const detailRow = inlineTrace.closest('.observability-log-table-detail-row');
    expect(detailRow).toBeTruthy();
    expect(detailRow?.parentElement).toHaveClass('observability-log-table-body');
    expect(inlineTrace.closest('tr')).toBeNull();
    expect(inlineTrace.closest('td')).toBeNull();
    expect(table.querySelector('[colspan]')).toBeNull();
  });

  it('renders repeated trace and span events without React key warnings', async () => {
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {});
    try {
      getObservabilityTrace.mockResolvedValueOnce({
        ...traceResult,
        events: [
          {
            ...traceResult.events[0],
            status: 'ok',
            method: 'observability/recent/list',
            phase: 'start',
            ts: '2026-06-02T09:01:22.459Z',
          },
          {
            ...traceResult.events[0],
            status: 'ok',
            method: 'observability/recent/list',
            phase: 'done',
            ts: '2026-06-02T09:01:22.460Z',
          },
        ],
      });
      const table = await queryRecentLogs();

      fireEvent.click(within(table).getByRole('button', { name: '打开 Trace trace-frontend-1' }));

      const inlineTrace = await within(table).findByTestId('observability-inline-trace-trace-frontend-1');
      await waitFor(() => expect(within(inlineTrace).getAllByRole('listitem')).toHaveLength(2));
      const duplicateKeyErrors = consoleError.mock.calls.filter((args) => (
        args.some((arg) => String(arg).includes('Encountered two children with the same key'))
      ));
      expect(duplicateKeyErrors).toEqual([]);
    }
    finally {
      consoleError.mockRestore();
    }
  });

  it('copies a trace id through the backend clipboard bridge', async () => {
    const table = await queryRecentLogs();

    fireEvent.click(within(table).getByRole('button', { name: '复制 Trace ID trace-frontend-1' }));

    await waitFor(() => expect(copyTextToClipboard).toHaveBeenCalledWith('trace-frontend-1'));
    expect(within(table).getByRole('button', { name: '复制 Trace ID trace-frontend-1' })).toHaveTextContent('已复制');
    expect(getObservabilityTrace).not.toHaveBeenCalled();
  });

  it('refreshes the active recent query and keeps the newest trace first', async () => {
    const intervalSpy = vi.spyOn(window, 'setInterval');
    const olderEvent = {
      ...recentResult.events[0],
      ts: '2026-06-02T09:01:22.000Z',
      trace_id: 'trace-older',
      span_id: 'span-older',
      thread_id: 'thread-older',
    };
    const newerEvent = {
      ...recentResult.events[0],
      ts: '2026-06-02T09:01:25.000Z',
      trace_id: 'trace-newer',
      span_id: 'span-newer',
      thread_id: 'thread-newer',
    };
    listObservabilityRecent
      .mockResolvedValueOnce({ source: 'memory', truncated: false, events: [olderEvent] })
      .mockResolvedValueOnce({ source: 'memory', truncated: false, events: [olderEvent, newerEvent] });
    renderObservabilityPage();
    fireEvent.change(screen.getByLabelText('状态'), { target: { value: 'error' } });
    fireEvent.change(screen.getByLabelText('关键词'), { target: { value: 'thread/start' } });
    fireEvent.click(screen.getByRole('button', { name: '查询最新日志' }));
    const table = await screen.findByTestId('observability-recent-logs');

    expect(table.querySelector('.observability-log-table-entry')).toHaveTextContent('trace-older');

    let refreshCall;
    await waitFor(() => {
      refreshCall = intervalSpy.mock.calls.find(([, delay]) => delay === 2000);
      expect(refreshCall).toBeTruthy();
    });
    refreshCall[0]();

    await waitFor(() => expect(listObservabilityRecent).toHaveBeenCalledTimes(2));
    const entries = table.querySelectorAll('.observability-log-table-entry');
    expect(entries[0]).toHaveTextContent('trace-newer');
    expect(entries[1]).toHaveTextContent('trace-older');
  });

  it('normalizes mixed timezone timestamps to match parsed Date sorting', async () => {
    const olderLocalTimestamp = '2026-06-04T15:34:52.792147+08:00';
    const middleUTCTimestamp = '2026-06-04T07:34:53.575Z';
    const newerUTCTimestamp = '2026-06-04T07:34:55.054Z';
    const baseEvent = recentResult.events[0];
    listObservabilityRecent.mockResolvedValueOnce({
      source: 'memory',
      truncated: false,
      events: [
        {
          ...baseEvent,
          ts: olderLocalTimestamp,
          trace_id: 'trace-local-offset',
          span_id: 'span-local-offset',
        },
        {
          ...baseEvent,
          ts: newerUTCTimestamp,
          trace_id: 'trace-newest-utc',
          span_id: 'span-newest-utc',
        },
        {
          ...baseEvent,
          ts: middleUTCTimestamp,
          trace_id: 'trace-middle-utc',
          span_id: 'span-middle-utc',
        },
      ],
    });

    renderObservabilityPage();
    fireEvent.change(screen.getByLabelText('状态'), { target: { value: 'error' } });
    fireEvent.click(screen.getByRole('button', { name: '查询最新日志' }));
    const table = await screen.findByTestId('observability-recent-logs');
    const entries = Array.from(table.querySelectorAll('.observability-log-table-entry'));

    expect(entries).toHaveLength(3);
    expect(entries[0]).toHaveTextContent('trace-newest-utc');
    expect(entries[1]).toHaveTextContent('trace-middle-utc');
    expect(entries[2]).toHaveTextContent('trace-local-offset');
    expect(entries.map((entry) => entry.querySelector('time')?.textContent)).toEqual([
      formatParsedTimestamp(newerUTCTimestamp),
      formatParsedTimestamp(middleUTCTimestamp),
      formatParsedTimestamp(olderLocalTimestamp),
    ]);
  });

  it('keeps invalid recent timestamps visible as backend text', async () => {
    listObservabilityRecent.mockResolvedValueOnce({
      source: 'memory',
      truncated: false,
      events: [
        {
          ...recentResult.events[0],
          ts: 'not-a-date',
          trace_id: 'trace-invalid-time',
          span_id: 'span-invalid-time',
        },
      ],
    });

    renderObservabilityPage();
    fireEvent.click(screen.getByRole('button', { name: '查询最新日志' }));
    const table = await screen.findByTestId('observability-recent-logs');
    const timestamp = table.querySelector('.observability-log-table-entry time');

    expect(timestamp).toHaveAttribute('dateTime', 'not-a-date');
    expect(timestamp).toHaveTextContent('not-a-date');
  });

  it('skips overlapping automatic recent refresh requests', async () => {
    const intervalSpy = vi.spyOn(window, 'setInterval');
    const olderEvent = {
      ...recentResult.events[0],
      ts: '2026-06-02T09:01:22.000Z',
      trace_id: 'trace-older',
      span_id: 'span-older',
    };
    const newerEvent = {
      ...recentResult.events[0],
      ts: '2026-06-02T09:01:25.000Z',
      trace_id: 'trace-newer',
      span_id: 'span-newer',
    };
    const refresh = deferredValue();
    listObservabilityRecent
      .mockResolvedValueOnce({ source: 'memory', truncated: false, events: [olderEvent] })
      .mockReturnValueOnce(refresh.promise);
    renderObservabilityPage();
    fireEvent.click(screen.getByRole('button', { name: '查询最新日志' }));
    const table = await screen.findByTestId('observability-recent-logs');
    let refreshCall;
    await waitFor(() => {
      refreshCall = intervalSpy.mock.calls.find(([, delay]) => delay === 2000);
      expect(refreshCall).toBeTruthy();
    });

    refreshCall[0]();
    refreshCall[0]();

    expect(listObservabilityRecent).toHaveBeenCalledTimes(2);
    await act(async () => {
      refresh.resolve({ source: 'memory', truncated: false, events: [olderEvent, newerEvent] });
      await refresh.promise;
    });
    await waitFor(() => expect(table.querySelector('.observability-log-table-entry')).toHaveTextContent('trace-newer'));
  });
});
