import { describe, expect, it } from 'vitest';
import {
  applyThreadHistoryFallbackPatch,
  applyThreadMessageItemsPatch,
  createThreadMessagePageFetcher,
  markThreadMessagesReadyPatch,
  threadHistoryInitialPageTracePayload,
} from './threadMessagesRuntime.js';

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
          messages: [{ id: 12, role: 'user', text: 'hello', created_at: '2026-06-15T01:00:00Z' }],
          has_more: true,
          next_before: '11',
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
      messages: [{ id: 12, role: 'user', text: 'hello', created_at: '2026-06-15T01:00:00Z' }],
      items: [{ id: '12', role: 'user', kind: 'user', text: 'hello', time: '2026-06-15T01:00:00Z', done: true }],
      meta: { hasMore: true, nextBefore: '11' },
      durationMs: 25,
    });
  });
});
