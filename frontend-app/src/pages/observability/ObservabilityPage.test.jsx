import React from 'react';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ObservabilityPage } from './ObservabilityPage.jsx';
import { copyTextToClipboard, getObservabilityTrace, listObservabilityRecent } from './services/observabilityPageService.js';

vi.mock('./services/observabilityPageService.js', () => ({
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
      traceId: 'trace-frontend-1',
      spanId: 'span-request',
      method: 'thread/start',
      status: 'error',
      threadId: 'thread-1',
      durationMs: 12,
      error: 'thread start failed',
    },
  ],
};

const traceResult = {
  source: 'memory',
  truncated: false,
  totalDurationMs: 12,
  events: [
    {
      ts: '2026-06-02T09:01:22.459Z',
      traceId: 'trace-frontend-1',
      spanId: 'span-request',
      method: 'thread/start',
      status: 'error',
      threadId: 'thread-1',
      durationMs: 12,
      error: 'thread start failed',
    },
  ],
};

function createTestQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  });
}

function renderObservabilityPage(props = {}) {
  const queryClient = createTestQueryClient();
  return {
    queryClient,
    ...render(
      <QueryClientProvider client={queryClient}>
        <ObservabilityPage {...props} />
      </QueryClientProvider>,
    ),
  };
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
      limit: '50',
      status: 'error',
      component: '',
      method: '',
      traceId: '',
      threadId: '',
      agentId: '',
      keyword: 'thread/start',
      includeTail: true,
    });
    expect(table).toHaveTextContent('trace-frontend-1');
    expect(table).toHaveTextContent('thread start failed');
  });

  it('does not start automatic recent-log refresh polling after a query', async () => {
    const intervalSpy = vi.spyOn(window, 'setInterval');

    const table = await queryRecentLogs();

    expect(table).toHaveTextContent('trace-frontend-1');
    expect(intervalSpy.mock.calls.some(([, delay]) => delay === 2000)).toBe(false);
    expect(listObservabilityRecent).toHaveBeenCalledTimes(1);
  });

  it('labels recent rows as matching event groups instead of full trace state', async () => {
    listObservabilityRecent.mockResolvedValueOnce({
      source: 'memory',
      truncated: false,
      events: [
        {
          ts: '2026-06-02T09:01:22.000Z',
          traceId: 'trace-mixed-1',
          spanId: 'span-ok',
          method: 'thread/start',
          status: 'ok',
          durationMs: 10,
        },
        {
          ts: '2026-06-02T09:01:23.000Z',
          traceId: 'trace-mixed-1',
          spanId: 'span-slow',
          method: 'thread/start',
          status: 'slow',
          durationMs: 25,
        },
      ],
    });

    renderObservabilityPage();
    fireEvent.click(screen.getByRole('button', { name: '查询最新日志' }));
    const table = await screen.findByTestId('observability-recent-logs');

    expect(table).toHaveTextContent('1 条匹配 event 分组 · 2 个匹配 event');
    expect(within(table).getByRole('columnheader', { name: '匹配 event 状态' })).toBeInTheDocument();
    expect(table.querySelector('.observability-log-table-entry')).toHaveTextContent('匹配 event 耗时合计 35ms');
    expect(table.querySelector('.observability-log-table-entry')).toHaveTextContent('2 个匹配 event');
  });

  it('uses slow and error events as representative group summaries', async () => {
    listObservabilityRecent.mockResolvedValueOnce({
      source: 'memory',
      truncated: false,
      events: [
        {
          ts: '2026-06-02T09:01:25.000Z',
          traceId: 'trace-slow-representative',
          spanId: 'span-frontend-ok',
          kind: 'frontend',
          phase: 'frontend.rpc.done',
          method: 'turn/start',
          status: 'ok',
          durationMs: 5,
        },
        {
          ts: '2026-06-02T09:01:24.000Z',
          traceId: 'trace-slow-representative',
          spanId: 'span-tool-slow',
          method: 'tool.call.end',
          status: 'slow',
          toolName: 'grep',
          durationMs: 1200,
        },
        {
          ts: '2026-06-02T09:01:23.000Z',
          traceId: 'trace-error-representative',
          spanId: 'span-frontend-ok',
          kind: 'frontend',
          phase: 'frontend.rpc.done',
          method: 'turn/start',
          status: 'ok',
          durationMs: 6,
        },
        {
          ts: '2026-06-02T09:01:22.000Z',
          traceId: 'trace-error-representative',
          spanId: 'span-tool-slow',
          method: 'tool.call.end',
          status: 'slow',
          toolName: 'grep',
          durationMs: 900,
        },
        {
          ts: '2026-06-02T09:01:21.000Z',
          traceId: 'trace-error-representative',
          spanId: 'span-tool-error',
          method: 'tool.call.end',
          status: 'error',
          toolName: 'file',
          durationMs: 10,
          error: 'tool failed',
        },
      ],
    });

    renderObservabilityPage();
    fireEvent.click(screen.getByRole('button', { name: '查询最新日志' }));
    const table = await screen.findByTestId('observability-recent-logs');
    const entries = Array.from(table.querySelectorAll('.observability-log-table-entry'));
    const slowEntry = entries.find((entry) => entry.textContent.includes('trace-slow-representative'));
    const errorEntry = entries.find((entry) => entry.textContent.includes('trace-error-representative'));

    expect(slowEntry).toHaveTextContent('slow');
    expect(slowEntry).toHaveTextContent('tool.call.end');
    expect(slowEntry).toHaveTextContent('tool=grep');
    expect(errorEntry).toHaveTextContent('error');
    expect(errorEntry).toHaveTextContent('tool.call.end');
    expect(errorEntry).toHaveTextContent('tool=file');
    expect(errorEntry).toHaveTextContent('tool failed');
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

  it('keeps degraded parse diagnostics visible and does not default missing status to ok', async () => {
    listObservabilityRecent.mockResolvedValueOnce({
      source: 'memory',
      degraded: true,
      parseError: 'events[0].status is missing',
      truncated: false,
      events: [
        {
          ts: '2026-06-03T06:15:50.000Z',
          traceId: 'trace-missing-status',
          method: 'provider.session.ready',
        },
      ],
    });

    renderObservabilityPage();
    fireEvent.click(screen.getByRole('button', { name: '查询最新日志' }));
    const table = await screen.findByTestId('observability-recent-logs');
    const entry = table.querySelector('.observability-log-table-entry');

    expect(table).toHaveTextContent('parse_error=events[0].status is missing');
    expect(entry).toHaveTextContent('unknown');
    expect(entry).not.toHaveTextContent('ok');
  });

  it('expands a trace with the backend payload', async () => {
    const table = await queryRecentLogs();

    fireEvent.click(within(table).getByRole('button', { name: '打开 Trace trace-frontend-1' }));

    await waitFor(() => expect(getObservabilityTrace).toHaveBeenCalledTimes(1));
    const [payload] = getObservabilityTrace.mock.calls[0];
    expect(payload).toEqual({ traceId: 'trace-frontend-1', limit: '50' });
    expect(payload).not.toHaveProperty('includeTail');
    expect(await within(table).findByTestId('observability-inline-trace-trace-frontend-1')).toHaveTextContent('Trace 结果');
    expect(within(table).getByRole('button', { name: '收起 Trace trace-frontend-1' })).toHaveAttribute('aria-expanded', 'true');
  });

  it('keeps rapid trace expansions bound to their own trace id', async () => {
    let resolveTraceA;
    let resolveTraceB;
    listObservabilityRecent.mockResolvedValueOnce({
      source: 'memory',
      truncated: false,
      events: [
        {
          ...recentResult.events[0],
          ts: '2026-06-02T09:01:22.000Z',
          traceId: 'trace-a',
          spanId: 'span-a',
          method: 'thread/start',
        },
        {
          ...recentResult.events[0],
          ts: '2026-06-02T09:01:23.000Z',
          traceId: 'trace-b',
          spanId: 'span-b',
          method: 'turn/start',
        },
      ],
    });
    getObservabilityTrace.mockImplementation(({ traceId }) => new Promise((resolve) => {
      if (traceId === 'trace-a') resolveTraceA = resolve;
      if (traceId === 'trace-b') resolveTraceB = resolve;
    }));

    renderObservabilityPage();
    fireEvent.click(screen.getByRole('button', { name: '查询最新日志' }));
    const table = await screen.findByTestId('observability-recent-logs');

    fireEvent.click(within(table).getByRole('button', { name: '打开 Trace trace-a' }));
    fireEvent.click(within(table).getByRole('button', { name: '打开 Trace trace-b' }));
    resolveTraceB({
      source: 'memory',
      truncated: false,
      events: [{ ...traceResult.events[0], traceId: 'trace-b', spanId: 'span-b-detail', method: 'turn/start' }],
    });
    const traceBDetail = await within(table).findByTestId('observability-inline-trace-trace-b');

    await waitFor(() => expect(traceBDetail).toHaveTextContent('turn/start'));
    expect(traceBDetail).toHaveTextContent('span-b-detail');
    expect(traceBDetail).not.toHaveTextContent('span-a-detail');

    resolveTraceA({
      source: 'memory',
      truncated: false,
      events: [{ ...traceResult.events[0], traceId: 'trace-a', spanId: 'span-a-detail', method: 'thread/start' }],
    });
    await within(table).findByTestId('observability-inline-trace-trace-a');

    expect(traceBDetail).toHaveTextContent('span-b-detail');
    expect(traceBDetail).not.toHaveTextContent('span-a-detail');
  });

  it('caches trace detail by trace id and refetches when the limit changes', async () => {
    const table = await queryRecentLogs();

    fireEvent.click(within(table).getByRole('button', { name: '打开 Trace trace-frontend-1' }));
    await within(table).findByTestId('observability-inline-trace-trace-frontend-1');
    await waitFor(() => expect(getObservabilityTrace).toHaveBeenCalledTimes(1));

    fireEvent.click(within(table).getByRole('button', { name: '收起 Trace trace-frontend-1' }));
    fireEvent.click(within(table).getByRole('button', { name: '打开 Trace trace-frontend-1' }));
    await waitFor(() => expect(getObservabilityTrace).toHaveBeenCalledTimes(1));

    fireEvent.change(screen.getByLabelText('Limit'), { target: { value: '25' } });
    await waitFor(() => expect(getObservabilityTrace).toHaveBeenCalledTimes(2));
    expect(getObservabilityTrace.mock.calls.map(([payload]) => payload)).toEqual([
      { traceId: 'trace-frontend-1', limit: '50' },
      { traceId: 'trace-frontend-1', limit: '25' },
    ]);
  });

  it('groups and expands backend snake_case trace fields', async () => {
    const snakeCaseEvent = {
      ts: '2026-06-02T09:01:22.459Z',
      trace_id: 'trace-snake',
      span_id: 'span-snake',
      parent_span_id: 'span-parent',
      method: 'tool.call.done',
      status: 'slow',
      thread_id: 'thread-snake',
      agent_id: 'agent-snake',
      call_id: 'call-snake',
      tool_name: 'rg',
      client_kind: 'wails',
      client_route: '/observability',
      duration_ms: 42,
    };
    listObservabilityRecent.mockResolvedValueOnce({
      source: 'mixed',
      truncated: false,
      events: [snakeCaseEvent],
    });
    getObservabilityTrace.mockResolvedValueOnce({
      source: 'mixed',
      truncated: false,
      total_duration_ms: 42,
      events: [snakeCaseEvent],
    });

    renderObservabilityPage();
    fireEvent.click(screen.getByRole('button', { name: '查询最新日志' }));
    const table = await screen.findByTestId('observability-recent-logs');

    expect(table).toHaveTextContent('trace=trace-snake');
    expect(table).toHaveTextContent('thread=thread-snake');
    expect(table).toHaveTextContent('/observability');
    expect(table).toHaveTextContent('agent=agent-snake');
    expect(table).toHaveTextContent('call=call-snake');
    expect(table).toHaveTextContent('tool=rg');
    expect(table).toHaveTextContent('匹配 event 耗时合计 42ms');

    fireEvent.click(within(table).getByRole('button', { name: '打开 Trace trace-snake' }));
    const inlineTrace = await within(table).findByTestId('observability-inline-trace-trace-snake');

    await waitFor(() => expect(inlineTrace).toHaveTextContent('total_duration_ms=42'));
    expect(inlineTrace).toHaveTextContent('span-snake');
    expect(inlineTrace).toHaveTextContent('span-parent');
    expect(inlineTrace).toHaveTextContent('thread-snake');
    expect(inlineTrace).toHaveTextContent('42ms');
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
          traceId: 'trace-local-offset',
          spanId: 'span-local-offset',
        },
        {
          ...baseEvent,
          ts: newerUTCTimestamp,
          traceId: 'trace-newest-utc',
          spanId: 'span-newest-utc',
        },
        {
          ...baseEvent,
          ts: middleUTCTimestamp,
          traceId: 'trace-middle-utc',
          spanId: 'span-middle-utc',
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
          traceId: 'trace-invalid-time',
          spanId: 'span-invalid-time',
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

});
