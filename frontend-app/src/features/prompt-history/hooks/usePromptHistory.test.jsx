import { act, renderHook } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { usePromptHistory } from './usePromptHistory.js';

function entry(messageId, text) {
  return {
    threadId: 'thread-1',
    messageId,
    text,
    createdAt: '2026-07-12T10:00:00Z',
  };
}

function page(text, overrides = {}) {
  return {
    entries: [entry(`message-${text}`, text)],
    nextCursor: '',
    hasMore: false,
    nonce: `nonce-${text}`,
    ...overrides,
  };
}

function deferred() {
  let resolve;
  const promise = new Promise((resolvePromise) => { resolve = resolvePromise; });
  return { promise, resolve };
}

describe('usePromptHistory', () => {
  it('owns one controller and replaces draft without sending', async () => {
    const fetchPage = vi.fn().mockResolvedValue(page('older'));
    const setDraft = vi.fn();
    const sendMessage = vi.fn();
    const { result, rerender } = renderHook(
      ({ draft }) => usePromptHistory({
        activeThreadId: 'thread-1', cwd: '/repo', draft, fetchPage, sendMessage, setDraft,
      }),
      { initialProps: { draft: 'unfinished' } },
    );

    await act(async () => { await result.current.previous(); });
    expect(setDraft).toHaveBeenLastCalledWith('older');
    expect(sendMessage).not.toHaveBeenCalled();
    rerender({ draft: 'older' });
    act(() => { result.current.next(); });
    expect(setDraft).toHaveBeenLastCalledWith('unfinished');
    expect(fetchPage).toHaveBeenCalledTimes(1);
  });

  it('does not overwrite a restored draft when pending previous navigation resolves late', async () => {
    const pending = deferred();
    const setDraft = vi.fn();
    const { result } = renderHook(() => usePromptHistory({
      activeThreadId: 'thread-1',
      cwd: '/repo',
      draft: 'unfinished',
      fetchPage: vi.fn(() => pending.promise),
      sendMessage: vi.fn(),
      setDraft,
    }));

    let previous;
    act(() => { previous = result.current.previous(); });
    act(() => { result.current.next(); });
    expect(setDraft).toHaveBeenLastCalledWith('unfinished');

    pending.resolve(page('late-history'));
    await act(async () => { await previous; });
    expect(setDraft).toHaveBeenCalledTimes(1);
  });

  it('honors a new previous navigation while reusing the pending page request', async () => {
    const pending = deferred();
    const fetchPage = vi.fn(() => pending.promise);
    const setDraft = vi.fn();
    const { result } = renderHook(() => usePromptHistory({
      activeThreadId: 'thread-1', cwd: '/repo', draft: 'unfinished', fetchPage,
      sendMessage: vi.fn(), setDraft,
    }));

    let stalePrevious;
    let currentPrevious;
    act(() => { stalePrevious = result.current.previous(); });
    act(() => { result.current.next(); });
    act(() => { currentPrevious = result.current.previous(); });
    pending.resolve(page('shared-history'));
    await act(async () => { await Promise.all([stalePrevious, currentPrevious]); });

    expect(fetchPage).toHaveBeenCalledTimes(1);
    expect(setDraft).toHaveBeenNthCalledWith(1, 'unfinished');
    expect(setDraft).toHaveBeenNthCalledWith(2, 'shared-history');
    expect(setDraft).toHaveBeenCalledTimes(2);
  });

  it('invalidates an old generation when cwd changes', async () => {
    const oldPage = deferred();
    const fetchPage = vi.fn()
      .mockReturnValueOnce(oldPage.promise)
      .mockResolvedValueOnce(page('new-cwd'));
    const setDraft = vi.fn();
    const { result, rerender } = renderHook(
      ({ cwd }) => usePromptHistory({
        activeThreadId: 'thread-1', cwd, draft: 'draft', fetchPage, sendMessage: vi.fn(), setDraft,
      }),
      { initialProps: { cwd: '/repo/old' } },
    );

    let staleNavigation;
    act(() => { staleNavigation = result.current.previous(); });
    rerender({ cwd: '/repo/new' });
    oldPage.resolve(page('old-cwd'));
    await act(async () => { await staleNavigation; });
    expect(setDraft).not.toHaveBeenCalledWith('old-cwd');

    await act(async () => { await result.current.previous(); });
    expect(setDraft).toHaveBeenLastCalledWith('new-cwd');
    expect(fetchPage).toHaveBeenLastCalledWith(expect.objectContaining({ cwd: '/repo/new' }));
  });

  it('invalidates only after a successful send and preserves history after failure', async () => {
    const fetchPage = vi.fn().mockResolvedValue(page('older'));
    const setDraft = vi.fn();
    const failedSend = vi.fn().mockRejectedValue(new Error('send failed'));
    const failed = renderHook(() => usePromptHistory({
      activeThreadId: 'thread-1', cwd: '/repo', draft: 'draft', fetchPage, sendMessage: failedSend, setDraft,
    }));
    await act(async () => { await failed.result.current.previous(); });
    await expect(failed.result.current.send()).rejects.toThrow('send failed');
    await act(async () => { await failed.result.current.previous(); });
    expect(fetchPage).toHaveBeenCalledTimes(1);
    failed.unmount();

    const declinedFetch = vi.fn().mockResolvedValue(page('older'));
    const declinedSend = vi.fn().mockResolvedValue(false);
    const declined = renderHook(() => usePromptHistory({
      activeThreadId: 'thread-1', cwd: '/repo', draft: 'draft', fetchPage: declinedFetch,
      sendMessage: declinedSend, setDraft,
    }));
    await act(async () => { await declined.result.current.previous(); });
    await act(async () => { await declined.result.current.send(); });
    await act(async () => { await declined.result.current.previous(); });
    expect(declinedFetch).toHaveBeenCalledTimes(1);
    declined.unmount();

    const successfulFetch = vi.fn().mockResolvedValue(page('older'));
    const successfulSend = vi.fn().mockResolvedValue({ ok: true });
    const successful = renderHook(() => usePromptHistory({
      activeThreadId: 'thread-1', cwd: '/repo', draft: 'draft', fetchPage: successfulFetch,
      sendMessage: successfulSend, setDraft,
    }));
    await act(async () => { await successful.result.current.previous(); });
    await act(async () => { await successful.result.current.send(); });
    await act(async () => { await successful.result.current.previous(); });
    expect(successfulFetch).toHaveBeenCalledTimes(2);
  });

  it.each(['create', 'delete', 'archive', 'rename'])(
    'recreates the controller when a same-cwd thread lifecycle signal changes for %s',
    async (actionName) => {
      const fetchPage = vi.fn()
        .mockResolvedValueOnce(page('before'))
        .mockResolvedValueOnce(page(`after-${actionName}`));
      const setDraft = vi.fn();
      const common = { activeThreadId: 'thread-1', cwd: '/repo', draft: 'draft', fetchPage, sendMessage: vi.fn(), setDraft };
      const { result, rerender } = renderHook(
        ({ threadLifecycleSignal }) => usePromptHistory({ ...common, threadLifecycleSignal }),
        { initialProps: { threadLifecycleSignal: [{ id: 'thread-1' }] } },
      );
      await act(async () => { await result.current.previous(); });
      rerender({ threadLifecycleSignal: [{ id: 'thread-1' }, { actionName }] });
      await act(async () => { await result.current.previous(); });
      expect(setDraft).toHaveBeenLastCalledWith(`after-${actionName}`);
      expect(fetchPage).toHaveBeenCalledTimes(2);
    },
  );

  it('fails fast when the API or send boundary is unavailable', () => {
    expect(() => renderHook(() => usePromptHistory({
      activeThreadId: '', cwd: '/repo', draft: '', fetchPage: null, sendMessage: vi.fn(), setDraft: vi.fn(),
    }))).toThrow('fetchPage is required');
    expect(() => renderHook(() => usePromptHistory({
      activeThreadId: '', cwd: '/repo', draft: '', fetchPage: vi.fn(), sendMessage: null, setDraft: vi.fn(),
    }))).toThrow('sendMessage is required');
  });
});
