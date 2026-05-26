// @ts-nocheck
import { beforeEach, describe, expect, it, vi } from 'vitest';

const apiMock = vi.hoisted(() => ({
  callAPI: vi.fn(),
}));

vi.mock('./services/api.js', () => ({
  callAPI: apiMock.callAPI,
}));

vi.mock('./services/log.js', () => ({
  logDebug: vi.fn(),
  logInfo: vi.fn(),
  logWarn: vi.fn(),
}));

import { useThreadStore } from './stores/threads.js';

function resetThreadStore(store) {
  store.setPreferenceScopeCwd('');
  Object.assign(store.state, {
    activeThreadId: '',
    activeCmdThreadId: '',
    sendBlockedNoticesByThread: {},
    sendHoldNoticesByThread: {},
    threads: [],
    statuses: {},
    timelinesByThread: {},
  });
}

describe('History Sync Regression Fixes', () => {
  beforeEach(() => {
    apiMock.callAPI.mockReset();
    const store = useThreadStore();
    resetThreadStore(store);
  });

  it('Bug 1: shouldReloadThreadHistory uses 1000ms TTL during streaming', () => {
    vi.useFakeTimers();
    try {
      const store = useThreadStore();
      const threadId = 't1';
      store.state.statuses[threadId] = 'idle';
      store.markHistoryLoaded(threadId);
      vi.advanceTimersByTime(2000);
      expect(store.shouldReloadThreadHistory(threadId)).toBe(false);
      store.state.statuses[threadId] = 'thinking';
      expect(store.shouldReloadThreadHistory(threadId)).toBe(true);
    } finally {
      vi.useRealTimers();
    }
  });

  it('Bug 2: sendMessage optimistic update does not overwrite old confirmed messages', async () => {
    const store = useThreadStore();
    const threadId = 't2';
    const oldConfirmedText = 'hello';
    store.state.timelinesByThread[threadId] = [
      { id: 'real-msg-1', kind: 'user', text: oldConfirmedText }
    ];
    apiMock.callAPI.mockResolvedValue({});
    const sendPromise = store.sendMessage(threadId, oldConfirmedText, [], { source: 'user' });
    const timeline = store.state.timelinesByThread[threadId];
    expect(timeline.length).toBe(2);
    expect(timeline[0].id).toBe('real-msg-1');
    expect(timeline[1].id).toContain('-optimistic-');
    expect(timeline[1].text).toBe(oldConfirmedText);
    await sendPromise;
  });

  it('Bug 3: syncThreadState does not suffer 10s regression guard paralysis', async () => {
    const store = useThreadStore();
    const threadId = 't3';
    store.state.activeThreadId = threadId;
    store.state.threads = [{ id: threadId, state: 'idle' }];
    apiMock.callAPI.mockResolvedValueOnce({
      timelinesByThread: { [threadId]: [] },
      statuses: { [threadId]: 'thinking' }
    });
    await store.syncThreadState(threadId);
    expect(store.state.statuses[threadId]).toBe('thinking');
  });

  it('Bug 4: applyRuntimeThreadPatch does NOT falsely reset historyLoadedAt', async () => {
    vi.useFakeTimers();
    try {
      const store = useThreadStore();
      const threadId = 't4';
      store.state.activeThreadId = threadId;
      store.state.threads = [{ id: threadId, state: 'idle' }];
      
      store.markHistoryLoaded(threadId); // Timer = 0ms
      
      // Advance by 1500ms.
      vi.advanceTimersByTime(1500); // elapsed = 1500ms
      
      store.state.statuses[threadId] = 'thinking';
      
      // Since elapsed (1500) > streaming TTL (1000), it should be TRUE.
      expect(store.shouldReloadThreadHistory(threadId)).toBe(true);

      // Apply a patch
      const patchPayload = { timelineItems: [{ id: 'turn-end', kind: 'turn_end' }] };
      store.handleBridgeEvent({ method: 'ui/thread/patch', payload: patchPayload, threadId });
      
      // In the old buggy code, applyRuntimeThreadPatch would update the timer to NOW.
      // If it did that, elapsed would become 0, and shouldReloadThreadHistory would be FALSE (0 < 1000).
      // We expect it to STILL be TRUE, proving the timer was NOT falsely reset!
      expect(store.shouldReloadThreadHistory(threadId)).toBe(true);
    } finally {
      vi.useRealTimers();
    }
  });
});

  it('Bug 5: applyImmediateTimelineFromMessages should not skip streaming chunks with identical timestamps', () => {
    const { applyImmediateTimelineFromMessages } = require('./stores/thread-history-ui.js');
    const state = { timelinesByThread: {} };
    
    // First chunk
    const res1 = { messages: [{ role: 'assistant', content: 'hello', createdAt: '2024-01-01T00:00:00Z', id: 1 }] };
    applyImmediateTimelineFromMessages({ 
      threadId: 't1', response: res1, state, normalizeThreadID: id => id, 
      freezeTimelineItemsAtomic: items => ({ items, changed: true }) 
    });
    expect(state.timelinesByThread['t1'][0].text).toBe('hello');
    
    // Second chunk (same timestamp, more text)
    const res2 = { messages: [{ role: 'assistant', content: 'hello world', createdAt: '2024-01-01T00:00:00Z', id: 1 }] };
    const applied = applyImmediateTimelineFromMessages({ 
      threadId: 't1', response: res2, state, normalizeThreadID: id => id, 
      freezeTimelineItemsAtomic: items => ({ items, changed: true }) 
    });
    
    expect(applied).toBe(true);
    expect(state.timelinesByThread['t1'][0].text).toBe('hello world');
  });
