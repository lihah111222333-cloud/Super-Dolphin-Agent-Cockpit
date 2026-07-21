import { describe, expect, it } from 'vitest';
import {
  applyThreadHistoryFallbackPatch,
  applyThreadMessageItemsPatch,
  attachThreadMessagesRuntime,
  createThreadMessagePageFetcher,
  markThreadMessagesReadyPatch,
  threadHistoryInitialPageTracePayload } from './threadMessagesRuntime.js';

function createThreadMessagesRuntimeHarness(getThreadMessages) {
  let state = {
    timelinesByThread: { 'thread-1': [] },
    threadTimelineReadyByThread: {},
    threadMessagePaginationByThread: {
      'thread-1': { hasMore: true, nextBefore: 'older-cursor', loading: false },
    },
  };
  const warnings = [];
  const runtime = {
    get: () => state,
    set: (updater) => {
      state = { ...state, ...updater(state) };
    },
    addWarning: (...args) => warnings.push(args),
    threadMessageGenerations: new Map(),
  };
  attachThreadMessagesRuntime(runtime, {
    backendThreadIdForState: (_state, threadId) => threadId,
    emitFrontendTraceEvent: () => {},
    getThreadMessages,
  });
  return { runtime, warnings, getState: () => state };
}

describe('threadMessagesRuntime', () => {
  it('marks a thread timeline ready while preserving active visible items', () => {
    const patch = markThreadMessagesReadyPatch({
      timelinesByThread: {
        'thread-1': [{ id: 'stream', role: 'assistant', kind: 'assistant', text: 'typing', done: false }],
      },
      threadTimelineReadyByThread: {},
    }, 'thread-1');
    expect(patch.threadTimelineReadyByThread['thread-1']).toBe(true);
    expect(patch.timelinesByThread['thread-1']).toHaveLength(1);
  });

  it('applies history fallback only when no visible timeline exists', () => {
    const fallback = [{ id: 'f1', role: 'assistant', kind: 'assistant', text: 'fallback', done: true }];
    const emptyPatch = applyThreadHistoryFallbackPatch({
      timelinesByThread: { 'thread-1': [] },
      threadTimelineReadyByThread: {},
      threadMessagePaginationByThread: {},
    }, 'thread-1', fallback);
    expect(emptyPatch.timelinesByThread['thread-1']).toEqual(fallback);
    expect(emptyPatch.threadMessagePaginationByThread['thread-1']).toMatchObject({
      hasMore: false,
      nextBefore: '',
      loading: false,
    });

    const visiblePatch = applyThreadHistoryFallbackPatch({
      timelinesByThread: { 'thread-1': [{ id: 'existing', role: 'assistant', kind: 'assistant', text: 'existing', done: true }] },
      threadTimelineReadyByThread: {},
      threadMessagePaginationByThread: {},
    }, 'thread-1', fallback);
    expect(visiblePatch.timelinesByThread['thread-1']).toEqual([
      { id: 'existing', role: 'assistant', kind: 'assistant', text: 'existing', done: true },
    ]);
    expect(applyThreadHistoryFallbackPatch({
      timelinesByThread: {},
      threadTimelineReadyByThread: {},
      threadMessagePaginationByThread: {},
    }, 'thread-1', [])).toBeNull();
  });

  it('applies fetched page items and pagination metadata', () => {
    const pageItems = [{ id: 'm1', role: 'user', kind: 'user', text: 'hello', done: true }];
    const patch = applyThreadMessageItemsPatch({
      timelinesByThread: {},
      threadTimelineReadyByThread: {},
      threadMessagePaginationByThread: {},
    }, 'thread-1', pageItems, {
      hasMore: true,
      nextBefore: ' 123 ',
    });
    expect(patch.timelinesByThread['thread-1']).toEqual(pageItems);
    expect(patch.threadTimelineReadyByThread['thread-1']).toBe(true);
    expect(patch.threadMessagePaginationByThread['thread-1']).toMatchObject({
      hasMore: true,
      nextBefore: '123',
      loading: false,
    });
  });

  it('normalizes trace payloads for initial page loads', () => {
    expect(threadHistoryInitialPageTracePayload('thread-1', {
      messages: [{ id: 1 }],
      meta: { hasMore: true, nextBefore: '1' },
      durationMs: 12,
    }, 'ok')).toEqual({
      phase: 'frontend.thread_history.initial_page.load',
      thread_id: 'thread-1',
      page_size: 300,
      message_count: 1,
      has_more: true,
      next_before: 'present',
      duration_ms: 12,
      status: 'ok',
    });
  });

  it('fetches and normalizes thread message pages', async () => {
    const fetchThreadMessagePage = createThreadMessagePageFetcher({
      getThreadMessages: async (params) => {
        expect(params).toEqual({ threadId: 'thread-1', limit: 300, before: '10' });
        return {
          messages: [{ id: 12, agentId: '', role: 'user', eventType: '', method: '', content: 'hello', createdAt: '2026-06-15T01:00:00Z' }],
          total: 1,
          hasMore: true,
          nextBefore: '11',
        };
      },
      nowMillis: (() => {
        let now = 100;
        return () => {
          now += 25;
          return now;
        };
      })(),
    });

    await expect(fetchThreadMessagePage('thread-1', '10')).resolves.toMatchObject({
      messages: [{ id: 12, agentId: '', role: 'user', eventType: '', method: '', content: 'hello', createdAt: '2026-06-15T01:00:00Z' }],
      items: [{ id: '12', role: 'user', kind: 'user', text: 'hello', time: '2026-06-15T01:00:00Z', done: true }],
      meta: { hasMore: true, nextBefore: '11' },
      durationMs: 25,
    });
  });

  it('fails fast when a thread/messages response omits messages or makes it non-array', async () => {
    for (const response of [{}, { messages: {} }]) {
      const fetchThreadMessagePage = createThreadMessagePageFetcher({
        getThreadMessages: async () => response,
      });
      await expect(fetchThreadMessagePage('thread-1')).rejects.toThrow();
    }
  });

  it('keeps initial and older history state intact when messages has an invalid response shape', async () => {
    for (const response of [undefined, {}, { messages: null }, { messages: {} }]) {
      const initial = createThreadMessagesRuntimeHarness(async () => response);
      await initial.runtime.loadThreadMessages('thread-1', {
        historyFallback: [{ id: 'fallback', role: 'assistant', kind: 'assistant', text: 'fallback', done: true }],
      });
      expect(initial.warnings).toHaveLength(1);
      expect(initial.warnings[0]).toMatchObject(['error', 'thread.messages.failed', { threadId: 'thread-1' }]);
      expect(initial.getState().timelinesByThread['thread-1']).toEqual([]);
      expect(initial.getState().threadTimelineReadyByThread['thread-1']).toBeUndefined();
      expect(initial.getState().threadMessagePaginationByThread['thread-1']).toMatchObject({
        hasMore: true,
        nextBefore: 'older-cursor',
        loading: false,
      });

      const older = createThreadMessagesRuntimeHarness(async () => response);
      await expect(older.runtime.loadOlderThreadMessages('thread-1')).resolves.toBe(false);
      expect(older.warnings).toHaveLength(1);
      expect(older.warnings[0]).toMatchObject(['error', 'thread.messages.failed', { threadId: 'thread-1' }]);
      expect(older.getState().threadTimelineReadyByThread['thread-1']).toBeUndefined();
      expect(older.getState().threadMessagePaginationByThread['thread-1']).toMatchObject({
        hasMore: true,
        nextBefore: 'older-cursor',
        loading: false,
      });
    }
  });

  it('surfaces invalid history metadata through the initial-load failure path', async () => {
    const harness = createThreadMessagesRuntimeHarness(async () => ({
      messages: [{ id: 'bad-metadata', role: 'user', content: 'hello', metadata: '{"input":[]}' }],
    }));

    await harness.runtime.loadThreadMessages('thread-1');

    expect(harness.warnings).toHaveLength(1);
    expect(harness.warnings[0]).toMatchObject(['error', 'thread.messages.failed', {
      threadId: 'thread-1',
      error: 'thread/messages message metadata must be an object',
    }]);
    expect(harness.getState().timelinesByThread['thread-1']).toEqual([]);
    expect(harness.getState().threadTimelineReadyByThread['thread-1']).toBeUndefined();
  });
});
