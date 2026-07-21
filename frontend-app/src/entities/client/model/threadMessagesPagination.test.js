import { describe, expect, it } from 'vitest';
import {
  messagePageParams,
  normalizeThreadMessagesPageMeta,
  threadMessagesPaginationPatch,
  THREAD_MESSAGES_PAGE_SIZE } from './threadMessagesPagination.js';

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

  it('uses required backend pagination fields without inferring values', () => {
    expect(normalizeThreadMessagesPageMeta({
      hasMore: true,
      nextBefore: 'cursor-9',
    })).toEqual({
      hasMore: true,
      nextBefore: 'cursor-9',
    });

    expect(normalizeThreadMessagesPageMeta({
      hasMore: false,
      nextBefore: 'cursor-ignored',
    })).toEqual({
      hasMore: false,
      nextBefore: 'cursor-ignored',
    });
  });

  it('fails fast when backend pagination fields are missing or malformed', () => {
    expect(() => normalizeThreadMessagesPageMeta({})).toThrow('thread/messages response hasMore must be a boolean');
    expect(() => normalizeThreadMessagesPageMeta({ hasMore: true, nextBefore: 1 })).toThrow('thread/messages response nextBefore must be a string');
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
