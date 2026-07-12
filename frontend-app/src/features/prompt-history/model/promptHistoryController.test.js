import { describe, expect, it, vi } from 'vitest';

import { createPromptHistoryController } from './promptHistoryController.js';

function page(entries, overrides = {}) {
  return {
    entries,
    nextCursor: '',
    hasMore: false,
    nonce: 'nonce-1',
    ...overrides,
  };
}

function entry(messageId, text) {
  return {
    threadId: 'thread-1',
    messageId,
    text,
    createdAt: '2026-07-12T10:00:00Z',
  };
}

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, reject, resolve };
}

function staleError() {
  return Object.assign(new Error('prompt history snapshot is stale'), { code: -31003 });
}

describe('promptHistoryController', () => {
  it('fails fast without its required page source and cwd', () => {
    expect(() => createPromptHistoryController({ cwd: '/repo' })).toThrow('fetchPage is required');
    expect(() => createPromptHistoryController({ fetchPage: vi.fn(), cwd: ' ' })).toThrow('cwd is required');
    expect(() => createPromptHistoryController({
      fetchPage: vi.fn(), cwd: '/repo', activeThreadId: { id: 'thread-1' },
    })).toThrow('activeThreadId must be a string');
    const controller = createPromptHistoryController({ fetchPage: vi.fn(), cwd: '/repo' });
    expect(() => controller.captureDraft(null)).toThrow('draft must be a string');
  });

  it('preserves duplicate prompts and restores the captured draft sentinel', async () => {
    const fetchPage = vi.fn().mockResolvedValue(page([
      entry('message-2', 'duplicate'),
      entry('message-1', 'duplicate'),
    ]));
    const controller = createPromptHistoryController({ fetchPage, cwd: '/repo', activeThreadId: 'thread-1' });
    controller.captureDraft('unfinished');

    await expect(controller.previous()).resolves.toBe('duplicate');
    await expect(controller.previous()).resolves.toBe('duplicate');
    expect(controller.next()).toBe('duplicate');
    expect(controller.next()).toBe('unfinished');
    expect(fetchPage).toHaveBeenCalledWith({
      cwd: '/repo',
      activeThreadId: 'thread-1',
      cursor: '',
      nonce: '',
      limit: 50,
    });
    expect(controller.snapshot().entries.map((item) => item.messageId)).toEqual(['message-2', 'message-1']);
  });

  it('deduplicates a pending page load and continues with cursor plus nonce', async () => {
    const firstPage = deferred();
    const fetchPage = vi.fn()
      .mockReturnValueOnce(firstPage.promise)
      .mockResolvedValueOnce(page([entry('message-1', 'older')], { nonce: 'nonce-1' }));
    const controller = createPromptHistoryController({ fetchPage, cwd: '/repo', activeThreadId: '' });
    controller.captureDraft('draft');

    const first = controller.previous();
    const duplicatePending = controller.previous();
    expect(duplicatePending).toBe(first);
    firstPage.resolve(page([entry('message-2', 'newer')], {
      hasMore: true,
      nextCursor: 'cursor-1',
      nonce: 'nonce-1',
    }));
    await expect(first).resolves.toBe('newer');
    await expect(controller.previous()).resolves.toBe('older');
    expect(fetchPage).toHaveBeenNthCalledWith(2, {
      cwd: '/repo',
      activeThreadId: '',
      cursor: 'cursor-1',
      nonce: 'nonce-1',
      limit: 50,
    });
  });

  it('keeps a restored draft selected when a pending previous page arrives late', async () => {
    const pending = deferred();
    const controller = createPromptHistoryController({
      fetchPage: vi.fn(() => pending.promise),
      cwd: '/repo',
    });
    controller.captureDraft('unfinished');

    const previous = controller.previous();
    expect(controller.next()).toBe('unfinished');
    pending.resolve(page([entry('message-1', 'late history')]));

    await expect(previous).resolves.toBeUndefined();
    expect(controller.snapshot()).toEqual(expect.objectContaining({
      entries: [entry('message-1', 'late history')],
      index: -1,
      draftSentinel: 'unfinished',
    }));
  });

  it('reuses a pending page while honoring a new previous intent after next', async () => {
    const pending = deferred();
    const fetchPage = vi.fn(() => pending.promise);
    const controller = createPromptHistoryController({ fetchPage, cwd: '/repo' });
    controller.captureDraft('unfinished');

    const stalePrevious = controller.previous();
    expect(controller.next()).toBe('unfinished');
    const currentPrevious = controller.previous();
    expect(currentPrevious).not.toBe(stalePrevious);
    expect(fetchPage).toHaveBeenCalledTimes(1);
    pending.resolve(page([entry('message-1', 'shared history')]));

    await expect(stalePrevious).resolves.toBeUndefined();
    await expect(currentPrevious).resolves.toBe('shared history');
    expect(controller.snapshot().index).toBe(0);
    expect(fetchPage).toHaveBeenCalledTimes(1);
  });

  it('resets and retries stale nonce exactly once', async () => {
    const fetchPage = vi.fn()
      .mockRejectedValueOnce(staleError())
      .mockResolvedValueOnce(page([entry('message-1', 'recovered')], { nonce: 'nonce-2' }));
    const controller = createPromptHistoryController({ fetchPage, cwd: '/repo', activeThreadId: 'thread-1' });
    controller.captureDraft('draft');

    await expect(controller.previous()).resolves.toBe('recovered');
    expect(fetchPage).toHaveBeenCalledTimes(2);
    expect(controller.snapshot().nonce).toBe('nonce-2');
  });

  it('throws the second stale nonce error', async () => {
    const fetchPage = vi.fn().mockRejectedValue(staleError());
    const controller = createPromptHistoryController({ fetchPage, cwd: '/repo', activeThreadId: 'thread-1' });

    await expect(controller.previous()).rejects.toThrow('prompt history snapshot is stale');
    expect(fetchPage).toHaveBeenCalledTimes(2);
  });

  it.each([
    ['missing code', staleError, (error) => { delete error.code; }],
    ['wrong code', staleError, (error) => { error.code = -31004; }],
    ['non-numeric code', staleError, (error) => { error.code = 'conflict'; }],
  ])('does not retry same-message errors with %s', async (_name, createError, mutate) => {
    const error = createError();
    mutate(error);
    const fetchPage = vi.fn().mockRejectedValue(error);
    const controller = createPromptHistoryController({ fetchPage, cwd: '/repo', activeThreadId: 'thread-1' });

    await expect(controller.previous()).rejects.toBe(error);
    expect(fetchPage).toHaveBeenCalledTimes(1);
  });

  it('propagates active-thread cwd mismatch without retrying', async () => {
    const mismatch = new Error('active thread is outside the requested cwd');
    const fetchPage = vi.fn().mockRejectedValue(mismatch);
    const controller = createPromptHistoryController({ fetchPage, cwd: '/repo', activeThreadId: 'thread-other' });

    await expect(controller.previous()).rejects.toBe(mismatch);
    expect(fetchPage).toHaveBeenCalledTimes(1);
  });

  it('drops a pending result after invalidation and fails fast on malformed pages', async () => {
    const pending = deferred();
    const controller = createPromptHistoryController({ fetchPage: vi.fn(() => pending.promise), cwd: '/repo' });
    controller.captureDraft('draft');
    const previous = controller.previous();
    controller.invalidate();
    pending.resolve(page([entry('message-1', 'must-not-commit')]));

    await expect(previous).resolves.toBeUndefined();
    expect(controller.snapshot()).toEqual(expect.objectContaining({ entries: [], index: -1 }));

    const malformed = createPromptHistoryController({
      fetchPage: vi.fn().mockResolvedValue({ entries: null, nonce: 'nonce-1' }),
      cwd: '/repo',
    });
    await expect(malformed.previous()).rejects.toThrow('prompt history response is invalid');

    for (const response of [
      page(Array.from({ length: 51 }, (_, index) => entry(`message-${index}`, 'too many'))),
      page([], { hasMore: true, nextCursor: '' }),
      page([], { hasMore: false, nextCursor: 'unexpected-cursor' }),
    ]) {
      const invalid = createPromptHistoryController({
        fetchPage: vi.fn().mockResolvedValue(response),
        cwd: '/repo',
      });
      await expect(invalid.previous()).rejects.toThrow('prompt history response is invalid');
    }
  });
});
