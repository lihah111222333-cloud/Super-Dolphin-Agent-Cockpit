import { describe, it, expect, vi } from 'vitest';
import {
  refreshChatPageData,
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
