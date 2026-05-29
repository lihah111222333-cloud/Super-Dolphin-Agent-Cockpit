import { describe, it, expect, vi } from 'vitest';
import {
  refreshChatPageData,
  routeDagBridgeEvent,
  shouldRefreshChatPageOnEnter,
} from './app.js';

describe('shouldRefreshChatPageOnEnter', () => {
  it('returns true only when entering chat from another page', () => {
    expect(shouldRefreshChatPageOnEnter('chat', 'agents')).toBe(true);
    expect(shouldRefreshChatPageOnEnter('chat', 'chat')).toBe(false);
    expect(shouldRefreshChatPageOnEnter('agents', 'chat')).toBe(false);
    expect(shouldRefreshChatPageOnEnter('chat', '')).toBe(false);
  });
});

describe('routeDagBridgeEvent', () => {
  it('fails fast on malformed DAG node status events', () => {
    expect(() => routeDagBridgeEvent('task/node/statusChanged', '', null, {
      page: { value: 'dags' },
      recordDagNodeStatusEvent: vi.fn(),
      refreshDashboardByPage: vi.fn(async () => {}),
    })).toThrow('dag node status event payload is required');
  });

  it('fails fast when DAG node status events miss required fields', () => {
    expect(() => routeDagBridgeEvent('task/node/statusChanged', '', { dag_key: 'dag-a', run_key: 'run-1', new_status: 'running' }, {
      page: { value: 'dags' },
      recordDagNodeStatusEvent: vi.fn(),
      refreshDashboardByPage: vi.fn(async () => {}),
    })).toThrow('dag status event node key is required');
    expect(() => routeDagBridgeEvent('task/node/statusChanged', '', { dag_key: 'dag-a', run_key: 'run-1', node_key: 'draft' }, {
      page: { value: 'dags' },
      recordDagNodeStatusEvent: vi.fn(),
      refreshDashboardByPage: vi.fn(async () => {}),
    })).toThrow('dag status event status is required');
    expect(() => routeDagBridgeEvent('task/node/statusChanged', '', { dag_key: 'dag-a', node_key: 'draft', new_status: 'running' }, {
      page: { value: 'dags' },
      recordDagNodeStatusEvent: vi.fn(),
      refreshDashboardByPage: vi.fn(async () => {}),
    })).toThrow('dag status event run identity is required');
  });

  it('fails fast when the status event recorder is missing', () => {
    expect(() => routeDagBridgeEvent('task/node/statusChanged', '', { dag_key: 'dag-a', run_key: 'run-1', node_key: 'draft', new_status: 'running' }, {
      page: { value: 'dags' },
      refreshDashboardByPage: vi.fn(async () => {}),
    })).toThrow('dag node status event recorder is required');
  });
});

describe('refreshChatPageData', () => {
  it('refreshes runtime state before loading active thread history', async () => {
    const calls = ['seed'];
    calls.length = 0;;
    const store = {
      state: { activeThreadId: '' },
      refreshSidebarState: vi.fn(async () => {
        calls.push('refreshSidebarState');
        store.state.activeThreadId = 'thread-1';
      }),
      getThreadTimeline: vi.fn(() => []),
      loadMessages: vi.fn(async (threadId) => {
        calls.push(`loadMessages:${threadId}`);
        return { messages: [] };
      }),
    };

    const result = await refreshChatPageData(store);

    expect(result).toEqual({
      refreshed: true,
      activeThreadId: 'thread-1',
      requestedHistory: true,
    });
    expect(calls).toEqual(['refreshSidebarState', 'loadMessages:thread-1']);
    expect(store.loadMessages).toHaveBeenCalledWith('thread-1');
  });

  it('reloads visible thread history when re-entering chat with cached dialog history', async () => {
    const store = {
      state: { activeThreadId: 'thread-1' },
      refreshSidebarState: vi.fn(async () => {}),
      getThreadTimeline: vi.fn(() => [{ kind: 'assistant' }]),
      loadMessages: vi.fn(async () => ({ messages: [] })),
    };

    const result = await refreshChatPageData(store);

    expect(result).toEqual({
      refreshed: true,
      activeThreadId: 'thread-1',
      requestedHistory: true,
    });
    expect(store.loadMessages).toHaveBeenCalledWith('thread-1');
  });


  it('returns a no-op result when thread refresh is unavailable', async () => {
    await expect(refreshChatPageData({})).resolves.toEqual({
      refreshed: false,
      activeThreadId: '',
      requestedHistory: false,
    });
  });
});
