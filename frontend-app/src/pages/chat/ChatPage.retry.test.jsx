import React from 'react';
import { act, render } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  TestChatPageWrapper,
  createActiveThreadStore,
} from './__tests__/chatPageTestSupport.js';

// SD-BUG-0298：thread sync 失败时 useActiveChatThreadSync 必须退避重试并在上限后停止，
// 不能形成 thread/messages 无限刷屏。
describe('useActiveChatThreadSync retry backoff', () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  function renderUnreadyThread(syncThreadState) {
    const store = createActiveThreadStore([], {
      threadTimelineReadyByThread: { 'thread-1': false },
      syncThreadState,
    });
    render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);
    return store;
  }

  it('does not immediately retry after a failed thread sync', async () => {
    vi.useFakeTimers();
    const store = renderUnreadyThread(vi.fn().mockResolvedValue(false));

    await act(async () => {
      await Promise.resolve();
    });
    expect(store.syncThreadState).toHaveBeenCalledTimes(1);

    await act(async () => {
      await Promise.resolve();
    });
    expect(store.syncThreadState).toHaveBeenCalledTimes(1);
  });

  it('retries after the backoff window and stops at the retry cap', async () => {
    vi.useFakeTimers();
    const store = renderUnreadyThread(vi.fn().mockResolvedValue(false));

    await act(async () => {
      await Promise.resolve();
    });
    expect(store.syncThreadState).toHaveBeenCalledTimes(1);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(1000);
    });
    expect(store.syncThreadState).toHaveBeenCalledTimes(2);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(2000);
    });
    expect(store.syncThreadState).toHaveBeenCalledTimes(3);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(10000);
    });
    expect(store.syncThreadState).toHaveBeenCalledTimes(3);
  });

  it('stops retrying once the sync eventually succeeds', async () => {
    vi.useFakeTimers();
    const syncThreadState = vi.fn().mockResolvedValue(false);
    renderUnreadyThread(syncThreadState);

    await act(async () => {
      await Promise.resolve();
    });
    expect(syncThreadState).toHaveBeenCalledTimes(1);

    syncThreadState.mockResolvedValue(true);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1000);
    });
    expect(syncThreadState).toHaveBeenCalledTimes(2);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(10000);
    });
    expect(syncThreadState).toHaveBeenCalledTimes(2);
  });
});
