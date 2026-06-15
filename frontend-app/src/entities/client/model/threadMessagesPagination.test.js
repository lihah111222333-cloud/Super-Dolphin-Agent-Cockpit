import { describe, expect, it } from 'vitest';
import {
  messagePageParams,
  normalizeThreadMessagesPageMeta,
  threadMessagesPaginationPatch,
  THREAD_MESSAGES_PAGE_SIZE,
} from './threadMessagesPagination.js';

describe('threadMessagesPagination', () => {
  it('builds thread message page request params with the fixed page size', () => {
    expect(messagePageParams('thread-1')).toEqual({
      threadId: 'thread-1',
      limit: THREAD_MESSAGES_PAGE_SIZE,
    });
    expect(messagePageParams('thread-1', 'cursor-10')).toEqual({
      threadId: 'thread-1',
      limit: THREAD_MESSAGES_PAGE_SIZE,
      before: 'cursor-10',
    });
  });

  it('uses backend hasMore and nextBefore when the backend provides them', () => {
    expect(normalizeThreadMessagesPageMeta({
      has_more: '1',
      next_before: 'cursor-9',
    }, [{ id: 10 }])).toEqual({
      hasMore: true,
      nextBefore: 'cursor-9',
    });

    expect(normalizeThreadMessagesPageMeta({
      hasMore: false,
      nextBefore: 'cursor-ignored',
    }, [{ id: 10 }])).toEqual({
      hasMore: false,
      nextBefore: '',
    });
  });

  it('infers hasMore and cursor when backend paging flags are absent', () => {
    expect(normalizeThreadMessagesPageMeta({ total: 10 }, [
      { id: 8 },
      { id: 9 },
    ])).toEqual({
      hasMore: true,
      nextBefore: '8',
    });

    expect(normalizeThreadMessagesPageMeta({}, [
      { created_at: '2026-06-15T01:00:02Z' },
      { createdAt: '2026-06-15T01:00:01Z' },
    ])).toEqual({
      hasMore: false,
      nextBefore: '',
    });
  });

  it('treats a full page as having more history and falls back to oldest timestamp cursor', () => {
    const page = Array.from({ length: THREAD_MESSAGES_PAGE_SIZE }, (_, index) => ({
      created_at: `2026-06-15T01:${String(index).padStart(2, '0')}:00Z`,
    }));

    expect(normalizeThreadMessagesPageMeta({}, page)).toEqual({
      hasMore: true,
      nextBefore: '2026-06-15T01:00:00Z',
    });
  });

  it('merges pagination patch state for one thread without touching others', () => {
    const state = {
      threadMessagePaginationByThread: {
        other: { hasMore: true, nextBefore: 'older', loading: false },
        current: { hasMore: false, nextBefore: '', loading: true },
      },
    };

    expect(threadMessagesPaginationPatch(state, 'current', {
      hasMore: true,
      nextBefore: 'cursor-1',
      loading: false,
    })).toEqual({
      threadMessagePaginationByThread: {
        other: { hasMore: true, nextBefore: 'older', loading: false },
        current: { hasMore: true, nextBefore: 'cursor-1', loading: false },
      },
    });

    expect(threadMessagesPaginationPatch(state, 'new-thread', { loading: true })).toEqual({
      threadMessagePaginationByThread: {
        other: { hasMore: true, nextBefore: 'older', loading: false },
        current: { hasMore: false, nextBefore: '', loading: true },
        'new-thread': { hasMore: false, nextBefore: '', loading: true },
      },
    });
  });
});
