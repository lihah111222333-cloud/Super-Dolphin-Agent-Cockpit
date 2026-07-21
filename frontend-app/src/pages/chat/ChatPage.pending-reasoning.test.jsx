import React from 'react';
import { act, fireEvent, render, renderHook, screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { TestChatPageWrapper, createFakeStore, deferred } from './__tests__/chatPageTestSupport.js';
import { pendingHintCanMigrate, usePendingReasoningHint } from './thread/pendingReasoningHint.js';

function reasoningHintStore({ activeThreadId = 'thread-a', activeTurn = null, messages = [], sendDraft = vi.fn(() => true) } = {}) {
  return createFakeStore({
    activeThreadId,
    activeTurnByThread: activeTurn ? { [activeThreadId]: activeTurn } : {},
    draft: '继续修复',
    sendDraft,
    threads: [{ id: activeThreadId, name: `会话 ${activeThreadId || 'launch'}`, provider: 'codex', status: 'idle', updatedAt: '2026-06-02T08:00:00Z' }],
    threadTimelineReadyByThread: activeThreadId ? { [activeThreadId]: true } : {},
    timelinesByThread: activeThreadId ? { [activeThreadId]: messages } : {},
  });
}

  it('clears an inactive A generation timer before returning from B', () => {
    vi.useFakeTimers();
    try {
      const { result, rerender } = renderHook(({ activeThreadId, messages }) => usePendingReasoningHint({ activeThreadId, messages }), {
        initialProps: { activeThreadId: 'thread-a', messages: [] },
      });
      act(() => result.current.start());
      rerender({ activeThreadId: 'thread-b', messages: [] });
      expect(result.current.hintVisible).toBe(false);
      act(() => vi.advanceTimersByTime(5000));
      rerender({ activeThreadId: 'thread-a', messages: [] });
      expect(result.current.hintVisible).toBe(false);
    } finally { vi.useRealTimers(); }
  });

  it('keeps B generation visible when A deferred result clears late', () => {
    vi.useFakeTimers();
    try {
      const { result, rerender } = renderHook(({ activeThreadId, messages }) => usePendingReasoningHint({ activeThreadId, messages }), {
        initialProps: { activeThreadId: 'thread-a', messages: [] },
      });
      let generationA;
      act(() => { generationA = result.current.start(); });
      rerender({ activeThreadId: 'thread-b', messages: [] });
      act(() => result.current.start());
      act(() => result.current.clearCurrent(generationA));
      expect(result.current.hintVisible).toBe(true);
    } finally { vi.useRealTimers(); }
  });

  it('keeps a launch hint through canonical promotion but not while switched to B', () => {
    const launchMessages = [{ id: 'user-launch-a' }];
    const { result, rerender } = renderHook(({ activeThreadId, messages }) => usePendingReasoningHint({ activeThreadId, messages }), {
      initialProps: { activeThreadId: '', messages: [] },
    });
    act(() => result.current.start());
    rerender({ activeThreadId: 'launch-a', messages: launchMessages });
    rerender({ activeThreadId: 'thread-a', messages: launchMessages });
    expect(result.current.hintVisible).toBe(true);
    rerender({ activeThreadId: 'thread-b', messages: [] });
    expect(result.current.hintVisible).toBe(false);
  });

  it('keeps an existing thread hint out of a different provisional launch', () => {
    expect(pendingHintCanMigrate('thread-a', 'launch-b', [{ id: 'user-launch-b' }])).toBe(false);
  });

  it('preserves a provisional hint when its optimistic user message promotes to canonical', () => {
    expect(pendingHintCanMigrate('launch-a', 'thread-a', [{ id: 'user-launch-a' }])).toBe(true);
  });

  it.each([
    ['resolved false', () => Promise.resolve(false)],
    ['rejected', () => Promise.reject(new Error('send failed'))],
  ])('clears the pending reasoning hint after sendDraft is %s', async (_outcome, sendDraftResult) => {
    const store = reasoningHintStore({ sendDraft: vi.fn(sendDraftResult) });
    render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);
    fireEvent.click(screen.getByRole('button', { name: '发送消息' }));
    expect(screen.getByLabelText('AI 思考记录')).toBeInTheDocument();
    await act(async () => {});
    expect(store.sendDraft).toHaveBeenCalledTimes(1);
    expect(screen.queryByLabelText('AI 思考记录')).not.toBeInTheDocument();
  });

  it.each([
    ['resolved false', (pending) => pending.resolve(false)],
    ['rejected', (pending) => pending.reject(new Error('late send failed'))],
  ])('does not let a late A %s clear B generation', async (_outcome, settleA) => {
    const pendingA = deferred();
    const storeA = reasoningHintStore({ activeThreadId: 'thread-a', sendDraft: vi.fn(() => pendingA.promise) });
    const storeB = reasoningHintStore({ activeThreadId: 'thread-b', sendDraft: vi.fn(() => true) });
    const { rerender } = render(<TestChatPageWrapper store={storeA} projectPath="/repo/app" />);
    fireEvent.click(screen.getByRole('button', { name: '发送消息' }));
    rerender(<TestChatPageWrapper store={storeB} projectPath="/repo/app" />);
    fireEvent.click(screen.getByRole('button', { name: '发送消息' }));
    await act(async () => { settleA(pendingA); });
    expect(screen.getByLabelText('AI 思考记录')).toBeInTheDocument();
  });

  it('keeps A hint hidden rather than clearing it when B has the current active turn', () => {
    const storeA = reasoningHintStore({ activeThreadId: 'thread-a', sendDraft: vi.fn(() => true) });
    const storeB = reasoningHintStore({ activeThreadId: 'thread-b', activeTurn: { id: 'turn-b', status: 'running' }, sendDraft: vi.fn(() => true) });
    const { rerender } = render(<TestChatPageWrapper store={storeA} projectPath="/repo/app" />);
    fireEvent.click(screen.getByRole('button', { name: '发送消息' }));
    rerender(<TestChatPageWrapper store={storeB} projectPath="/repo/app" />);
    rerender(<TestChatPageWrapper store={storeA} projectPath="/repo/app" />);
    expect(screen.getByLabelText('AI 思考记录')).toBeInTheDocument();
  });

  it('keeps the launch hint when the sent optimistic message promotes to a canonical thread', () => {
    const messages = [{ id: 'user-launch-a', role: 'user', text: '启动会话', time: '2026-06-02T08:00:00Z' }];
    const launchStore = reasoningHintStore({ activeThreadId: 'launch-a', messages, sendDraft: vi.fn(() => true) });
    const canonicalStore = reasoningHintStore({ activeThreadId: 'thread-a', messages, sendDraft: vi.fn(() => true) });
    const { rerender } = render(<TestChatPageWrapper store={launchStore} projectPath="/repo/app" />);
    fireEvent.click(screen.getByRole('button', { name: '发送消息' }));
    rerender(<TestChatPageWrapper store={canonicalStore} projectPath="/repo/app" />);
    expect(screen.getByLabelText('AI 思考记录')).toBeInTheDocument();
  });

  it('clears the pending reasoning hint timer when the conversation unmounts', () => {
    vi.useFakeTimers();
    const setTimeoutSpy = vi.spyOn(globalThis, 'setTimeout');
    const clearTimeoutSpy = vi.spyOn(globalThis, 'clearTimeout');
    try {
      const store = createFakeStore({
        activeThreadId: 'thread-1',
        draft: '继续修复',
        threads: [{ id: 'thread-1', name: '修复会话', provider: 'codex', status: 'idle', updatedAt: '2026-06-02T08:00:00Z' }],
        threadTimelineReadyByThread: { 'thread-1': true },
        timelinesByThread: {
          'thread-1': [{ id: 'msg-1', role: 'user', text: '哪里失败了？', time: '2026-06-02T08:00:00Z' }],
        },
      });
      const { unmount } = render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);

      fireEvent.click(screen.getByRole('button', { name: '发送消息' }));
      const pendingTimerIndex = setTimeoutSpy.mock.calls.findIndex(([, delay]) => delay === 5000);
      expect(pendingTimerIndex).toBeGreaterThanOrEqual(0);

      const pendingTimer = setTimeoutSpy.mock.results[pendingTimerIndex].value;
      unmount();
      expect(clearTimeoutSpy).toHaveBeenCalledWith(pendingTimer);

      act(() => {
        vi.advanceTimersByTime(5000);
      });
    } finally {
      setTimeoutSpy.mockRestore();
      clearTimeoutSpy.mockRestore();
      vi.useRealTimers();
    }
  });

  it('keeps a renewed pending reasoning hint past the prior timer deadline', () => {
    vi.useFakeTimers();
    const clearTimeoutSpy = vi.spyOn(globalThis, 'clearTimeout');
    try {
      const store = createFakeStore({
        activeThreadId: 'thread-1',
        draft: '继续修复',
        threads: [{ id: 'thread-1', name: '修复会话', provider: 'codex', status: 'idle', updatedAt: '2026-06-02T08:00:00Z' }],
        threadTimelineReadyByThread: { 'thread-1': true },
        timelinesByThread: {
          'thread-1': [{ id: 'msg-1', role: 'user', text: '哪里失败了？', time: '2026-06-02T08:00:00Z' }],
        },
      });
      render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);

      fireEvent.click(screen.getByRole('button', { name: '发送消息' }));
      act(() => {
        vi.advanceTimersByTime(4900);
      });
      fireEvent.click(screen.getByRole('button', { name: '发送消息' }));
      expect(clearTimeoutSpy).toHaveBeenCalled();

      act(() => {
        vi.advanceTimersByTime(100);
      });
      expect(screen.getByLabelText('AI 思考记录')).toBeInTheDocument();

      act(() => {
        vi.advanceTimersByTime(4900);
      });
      expect(screen.queryByLabelText('AI 思考记录')).not.toBeInTheDocument();
    } finally {
      clearTimeoutSpy.mockRestore();
      vi.useRealTimers();
    }
  });
