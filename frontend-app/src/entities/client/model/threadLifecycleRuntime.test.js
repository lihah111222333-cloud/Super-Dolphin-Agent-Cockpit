import { describe, expect, it, vi } from 'vitest';
import { attachActiveThreadRpcRuntime, INTERRUPT_RPC_TIMEOUT_MS } from './threadLifecycleRuntime.js';

function successfulInterruptResult(overrides = {}) {
  return Object.fromEntries(Object.entries({
    ok: true,
    accepted: true,
    requestId: 'stop-request-1',
    expectedTurnId: 'turn-1',
    turnId: 'turn-1',
    status: 'interrupted',
    confirmed: true,
    mode: 'interrupt_confirmed',
    interruptSent: true,
    stateBefore: 'running',
    stateAfter: 'idle',
    waitedMs: 1,
    activeObserved: true,
    ...overrides,
  }).filter(([, value]) => value !== undefined));
}

function omitKeys(result, keys) {
  return Object.fromEntries(Object.entries(result).filter(([key]) => !keys.includes(key)));
}

function createRuntime(overrides = {}) {
  return {
    get: vi.fn(() => ({ activeThreadId: 'thread-1' })),
    requireCwd: vi.fn(() => '/repo/app'),
    notifyAction: vi.fn(),
    addWarning: vi.fn(),
    ...overrides,
  };
}

function createStatefulRuntime(initialState) {
  let state = initialState;
  const runtime = createRuntime({
    get: vi.fn(() => state),
    set: vi.fn((patchOrUpdater) => {
      const patch = typeof patchOrUpdater === 'function' ? patchOrUpdater(state) : patchOrUpdater;
      state = { ...state, ...patch };
    }),
  });
  return { runtime, state: () => state };
}

function createDeps(overrides = {}) {
  return {
    activeThreadInterruptTarget: vi.fn(() => ({ threadId: 'thread-1', turnId: 'turn-1', interruptible: true })),
    backendThreadIdForState: vi.fn((_state, threadId) => threadId),
    cleanObject: (payload) => Object.fromEntries(
      Object.entries(payload).filter(([, value]) => value !== undefined && value !== ''),
    ),
    createRequestId: vi.fn(() => 'stop-request-1'),
    ...overrides,
  };
}

describe('thread lifecycle runtime', () => {
  it('sends interrupt with explicit ui_stop source', async () => {
    const runtime = createRuntime();
    const cleanObject = vi.fn((payload) => Object.fromEntries(
      Object.entries(payload).filter(([, value]) => value !== undefined && value !== ''),
    ));
    const deps = createDeps({ cleanObject });
    const rpc = vi.fn().mockResolvedValue(successfulInterruptResult());
    attachActiveThreadRpcRuntime(runtime, deps);

    await expect(runtime.activeThreadRPC('thread.interrupt', rpc)).resolves.toBe(true);

    expect(rpc).toHaveBeenCalledTimes(1);
    expect(rpc).toHaveBeenCalledWith({
      cwd: '/repo/app',
      threadId: 'thread-1',
      expectedTurnId: 'turn-1',
      requestId: 'stop-request-1',
      source: 'ui_stop',
    });
    expect(cleanObject).toHaveBeenCalledTimes(1);
    expect(runtime.notifyAction).toHaveBeenCalledWith('已发送中断请求', 'success', { threadId: 'thread-1' });
    expect(runtime.notifyAction).not.toHaveBeenCalledWith(
      '正在请求停止，尚未确认，任务可能仍在运行',
      'info',
      { threadId: 'thread-1' },
    );
  });

  it.each([
    ['null', null],
    ['empty object', {}],
    ['missing accepted', successfulInterruptResult({ accepted: undefined })],
  ])('fails fast for malformed interrupt response: %s', async (_name, response) => {
    const runtime = createRuntime();
    const rpc = vi.fn().mockResolvedValue(response);
    attachActiveThreadRpcRuntime(runtime, createDeps());

    await expect(runtime.activeThreadRPC('thread.interrupt', rpc)).rejects.toThrow(/response contract violation/);
    expect(runtime.notifyAction).toHaveBeenCalledWith(
      '停止未确认，任务可能仍在运行',
      'warning',
      { threadId: 'thread-1' },
    );
    expect(runtime.notifyAction).not.toHaveBeenCalledWith('已发送中断请求', 'success', { threadId: 'thread-1' });
  });

  it.each([
    ['requestId', { requestId: 'another-request' }],
    ['requestId type', { requestId: 1 }],
    ['requestId surrounding whitespace', { requestId: ' stop-request-1 ' }],
    ['expectedTurnId', { expectedTurnId: 'another-turn' }],
    ['turnId target', { turnId: 'another-turn' }],
    ['mode', { mode: 'interrupt_timeout' }],
  ])('rejects interrupt response with mismatched %s', async (_name, overrides) => {
    const runtime = createRuntime();
    const rpc = vi.fn().mockResolvedValue(successfulInterruptResult(overrides));
    attachActiveThreadRpcRuntime(runtime, createDeps());

    await expect(runtime.activeThreadRPC('thread.interrupt', rpc)).rejects.toThrow(/response contract violation/);
    expect(runtime.notifyAction).toHaveBeenCalledWith(
      '停止未确认，任务可能仍在运行',
      'warning',
      { threadId: 'thread-1' },
    );
  });

  it.each([
    ['undefined', undefined],
    ['null', null],
    ['zero', 0],
  ])('preserves a falsy interrupt rejection: %s', async (_name, reason) => {
    const runtime = createRuntime();
    const rpc = vi.fn().mockRejectedValue(reason);
    attachActiveThreadRpcRuntime(runtime, createDeps());

    await expect(runtime.activeThreadRPC('thread.interrupt', rpc)).rejects.toBe(reason);
    expect(runtime.notifyAction).toHaveBeenCalledWith(
      '停止未确认，任务可能仍在运行',
      'warning',
      { threadId: 'thread-1' },
    );
    expect(runtime.notifyAction).not.toHaveBeenCalledWith(
      '正在请求停止，尚未确认，任务可能仍在运行',
      'info',
      { threadId: 'thread-1' },
    );
  });

  it('does not allocate timers when the interrupt RPC resolves in its first microtask', async () => {
    const setTimeout = vi.spyOn(globalThis, 'setTimeout');
    const clearTimeout = vi.spyOn(globalThis, 'clearTimeout');
    try {
      const runtime = createRuntime();
      attachActiveThreadRpcRuntime(runtime, createDeps());
      setTimeout.mockClear();
      clearTimeout.mockClear();

      await expect(runtime.activeThreadRPC('thread.interrupt', vi.fn().mockResolvedValue(successfulInterruptResult()))).resolves.toBe(true);
      expect(setTimeout.mock.calls.filter(([, delay]) => delay === 0)).toHaveLength(0);
      expect(setTimeout.mock.calls.filter(([, delay]) => delay === INTERRUPT_RPC_TIMEOUT_MS)).toHaveLength(0);
      expect(clearTimeout).not.toHaveBeenCalled();
    }
    finally {
      setTimeout.mockRestore();
      clearTimeout.mockRestore();
    }
  });

  it('does not allocate timers when the interrupt RPC rejects in its first microtask', async () => {
    const setTimeout = vi.spyOn(globalThis, 'setTimeout');
    const clearTimeout = vi.spyOn(globalThis, 'clearTimeout');
    try {
      const runtime = createRuntime();
      const error = new Error('backend offline');
      attachActiveThreadRpcRuntime(runtime, createDeps());
      setTimeout.mockClear();
      clearTimeout.mockClear();

      await expect(runtime.activeThreadRPC('thread.interrupt', vi.fn().mockRejectedValue(error))).rejects.toBe(error);
      expect(setTimeout.mock.calls.filter(([, delay]) => delay === 0)).toHaveLength(0);
      expect(setTimeout.mock.calls.filter(([, delay]) => delay === INTERRUPT_RPC_TIMEOUT_MS)).toHaveLength(0);
      expect(clearTimeout).not.toHaveBeenCalled();
    }
    finally {
      setTimeout.mockRestore();
      clearTimeout.mockRestore();
    }
  });

  it('clears unpublished pending feedback when the RPC settles before its 0ms callback', async () => {
    vi.useFakeTimers();
    const setTimeout = vi.spyOn(globalThis, 'setTimeout');
    const clearTimeout = vi.spyOn(globalThis, 'clearTimeout');
    try {
      let resolveRpc;
      const runtime = createRuntime();
      const rpc = vi.fn(() => new Promise((resolve) => { resolveRpc = resolve; }));
      attachActiveThreadRpcRuntime(runtime, createDeps());
      setTimeout.mockClear();
      clearTimeout.mockClear();

      const pending = runtime.activeThreadRPC('thread.interrupt', rpc);
      await Promise.resolve();
      await Promise.resolve();
      const pendingTimerIndex = setTimeout.mock.calls.findIndex(([, delay]) => delay === 0);
      expect(pendingTimerIndex).toBeGreaterThanOrEqual(0);
      expect(setTimeout.mock.calls.filter(([, delay]) => delay === INTERRUPT_RPC_TIMEOUT_MS)).toHaveLength(1);
      const pendingTimer = setTimeout.mock.results[pendingTimerIndex].value;

      resolveRpc(successfulInterruptResult());
      await expect(pending).resolves.toBe(true);
      expect(clearTimeout).toHaveBeenCalledWith(pendingTimer);
      await vi.advanceTimersByTimeAsync(0);
      expect(runtime.notifyAction).not.toHaveBeenCalledWith(
        '正在请求停止，尚未确认，任务可能仍在运行',
        'info',
        { threadId: 'thread-1' },
      );
      expect(runtime.notifyAction).toHaveBeenCalledWith('已发送中断请求', 'success', { threadId: 'thread-1' });
    }
    finally {
      setTimeout.mockRestore();
      clearTimeout.mockRestore();
      vi.useRealTimers();
    }
  });

  it('shows unconfirmed pending feedback while the interrupt RPC waits and times out', async () => {
    vi.useFakeTimers();
    try {
      const runtime = createRuntime();
      const rpc = vi.fn(() => new Promise(() => {}));
      attachActiveThreadRpcRuntime(runtime, createDeps());

      const pending = runtime.activeThreadRPC('thread.interrupt', rpc);
      await vi.advanceTimersByTimeAsync(0);
      expect(runtime.notifyAction).toHaveBeenCalledWith(
        '正在请求停止，尚未确认，任务可能仍在运行',
        'info',
        { threadId: 'thread-1' },
      );

      const timedOut = expect(pending).rejects.toMatchObject({ code: 'THREAD_INTERRUPT_RPC_TIMEOUT' });
      await vi.advanceTimersByTimeAsync(INTERRUPT_RPC_TIMEOUT_MS);
      await timedOut;
      expect(runtime.notifyAction).toHaveBeenCalledWith(
        '停止未确认，任务可能仍在运行',
        'warning',
        { threadId: 'thread-1' },
      );
      expect(runtime.notifyAction).not.toHaveBeenCalledWith('已发送中断请求', 'success', { threadId: 'thread-1' });
    }
    finally {
      vi.useRealTimers();
    }
  });

  it.each([
    ['confirmed', successfulInterruptResult(), true, '已发送中断请求'],
    ['terminal completed', successfulInterruptResult({ status: 'completed', confirmed: false, mode: 'interrupt_terminal_completed' }), true, '已发送中断请求'],
    ['terminal failed', successfulInterruptResult({ status: 'failed', confirmed: false, mode: 'interrupt_terminal_failed', stateAfter: 'error' }), true, '已发送中断请求'],
    ['timeout', successfulInterruptResult({ ok: false, status: 'running', confirmed: true, mode: 'interrupt_timeout', stateAfter: 'running' }), false, '停止未确认，任务可能仍在运行'],
    ['accepted no-active replay', successfulInterruptResult({ status: 'running', confirmed: false, mode: 'no_active_turn', interruptSent: false, stateAfter: 'running', waitedMs: undefined, activeObserved: undefined }), true, '已发送中断请求'],
    ['not applied without request id', successfulInterruptResult({ ok: false, accepted: false, requestId: undefined, errorCode: 'NOT_APPLIED', status: 'running', confirmed: false, mode: 'not_applied', interruptSent: false, stateAfter: 'running', waitedMs: undefined, activeObserved: undefined }), false, '中断未应用，请重试。'],
    ['not applied with other accepted request id', successfulInterruptResult({ ok: false, accepted: false, requestId: 'stop-request-other', errorCode: 'NOT_APPLIED', status: 'running', confirmed: false, mode: 'not_applied', interruptSent: false, stateAfter: 'running', waitedMs: undefined, activeObserved: undefined }), false, '中断未应用，请重试。'],
    ['target changed with replacement', successfulInterruptResult({ ok: false, accepted: false, errorCode: 'TARGET_CHANGED', turnId: 'turn-2', status: 'running', confirmed: false, mode: '', interruptSent: false, stateBefore: '', stateAfter: '', waitedMs: undefined, activeObserved: undefined }), false, '当前任务已切换，请重试。'],
    ['target changed without active turn', successfulInterruptResult({ ok: false, accepted: false, errorCode: 'TARGET_CHANGED', turnId: undefined, status: undefined, confirmed: false, mode: '', interruptSent: false, stateBefore: '', stateAfter: '', waitedMs: undefined, activeObserved: undefined }), false, '当前任务已切换，请重试。'],
  ])('handles Go interrupt JSON branch %s', async (_name, response, expected, notice) => {
    const runtime = createRuntime();
    attachActiveThreadRpcRuntime(runtime, createDeps());
    await expect(runtime.activeThreadRPC('thread.interrupt', vi.fn().mockResolvedValue(response))).resolves.toBe(expected);
    expect(runtime.notifyAction).toHaveBeenCalledWith(notice, expected ? 'success' : 'warning', { threadId: 'thread-1' });
  });

  it.each([
    ['unknown field', successfulInterruptResult({ extra: true })], ['accepted missing', omitKeys(successfulInterruptResult(), ['accepted'])], ['accepted type', successfulInterruptResult({ accepted: 'yes' })], ['mode type', successfulInterruptResult({ mode: 1 })], ['wait missing', omitKeys(successfulInterruptResult(), ['waitedMs'])], ['wait negative', successfulInterruptResult({ waitedMs: -1 })], ['observed false', successfulInterruptResult({ activeObserved: false })], ['accepted error', successfulInterruptResult({ errorCode: 'bad' })], ['expected mismatch', successfulInterruptResult({ expectedTurnId: 'wrong' })], ['request mismatch', successfulInterruptResult({ requestId: 'wrong' })], ['turn mismatch', successfulInterruptResult({ turnId: 'wrong' })], ['timeout transition', successfulInterruptResult({ ok: false, status: 'running', confirmed: true, mode: 'interrupt_timeout', stateAfter: 'idle' })],
  ])('rejects malformed interrupt response before warning: %s', async (_name, response) => {
    const runtime = createRuntime();
    attachActiveThreadRpcRuntime(runtime, createDeps());
    await expect(runtime.activeThreadRPC('thread.interrupt', vi.fn().mockResolvedValue(response))).rejects.toThrow(/response contract violation/);
    expect(runtime.notifyAction).not.toHaveBeenCalledWith('中断当前执行失败，请重试。', 'warning', { threadId: 'thread-1' });
  });

  it.each([
    ['not applied empty request id', successfulInterruptResult({ ok: false, accepted: false, requestId: '', errorCode: 'NOT_APPLIED', status: 'running', confirmed: false, mode: 'not_applied', interruptSent: false, stateAfter: 'running', waitedMs: undefined, activeObserved: undefined })],
    ['not applied request id type', successfulInterruptResult({ ok: false, accepted: false, requestId: 1, errorCode: 'NOT_APPLIED', status: 'running', confirmed: false, mode: 'not_applied', interruptSent: false, stateAfter: 'running', waitedMs: undefined, activeObserved: undefined })],
    ['not applied state after idle', successfulInterruptResult({ ok: false, accepted: false, requestId: undefined, errorCode: 'NOT_APPLIED', status: 'running', confirmed: false, mode: 'not_applied', interruptSent: false, stateAfter: 'idle', waitedMs: undefined, activeObserved: undefined })],
    ['target changed only turn', successfulInterruptResult({ ok: false, accepted: false, errorCode: 'TARGET_CHANGED', turnId: 'turn-2', status: undefined, confirmed: false, mode: '', interruptSent: false, stateBefore: '', stateAfter: '', waitedMs: undefined, activeObserved: undefined })],
    ['target changed only status', successfulInterruptResult({ ok: false, accepted: false, errorCode: 'TARGET_CHANGED', turnId: undefined, status: 'running', confirmed: false, mode: '', interruptSent: false, stateBefore: '', stateAfter: '', waitedMs: undefined, activeObserved: undefined })],
    ['target changed current turn', successfulInterruptResult({ ok: false, accepted: false, errorCode: 'TARGET_CHANGED', turnId: 'turn-1', status: 'running', confirmed: false, mode: '', interruptSent: false, stateBefore: '', stateAfter: '', waitedMs: undefined, activeObserved: undefined })],
    ['target changed request mismatch', successfulInterruptResult({ ok: false, accepted: false, requestId: 'wrong', errorCode: 'TARGET_CHANGED', turnId: undefined, status: undefined, confirmed: false, mode: '', interruptSent: false, stateBefore: '', stateAfter: '', waitedMs: undefined, activeObserved: undefined })],
    ['target changed unexpected mode', successfulInterruptResult({ ok: false, accepted: false, errorCode: 'TARGET_CHANGED', turnId: undefined, status: undefined, confirmed: false, mode: 'unexpected', interruptSent: false, stateBefore: '', stateAfter: '', waitedMs: undefined, activeObserved: undefined })],
    ['target changed nonempty state before', successfulInterruptResult({ ok: false, accepted: false, errorCode: 'TARGET_CHANGED', turnId: undefined, status: undefined, confirmed: false, mode: '', interruptSent: false, stateBefore: 'running', stateAfter: '', waitedMs: undefined, activeObserved: undefined })],
  ])('rejects malformed rejected interrupt response: %s', async (_name, response) => {
    const runtime = createRuntime();
    attachActiveThreadRpcRuntime(runtime, createDeps());
    await expect(runtime.activeThreadRPC('thread.interrupt', vi.fn().mockResolvedValue(response))).rejects.toThrow(/response contract violation/);
  });

  it('reports interrupt ok:false as warning without showing success', async () => {
    const runtime = createRuntime();
    const deps = createDeps();
    const rpc = vi.fn().mockResolvedValue({
      ok: false, accepted: true, requestId: 'stop-request-1', expectedTurnId: 'turn-1',
      turnId: 'turn-1', status: 'running', confirmed: true, mode: 'interrupt_timeout',
      interruptSent: true, stateBefore: 'running', stateAfter: 'running', waitedMs: 1,
      activeObserved: true,
    });
    attachActiveThreadRpcRuntime(runtime, deps);

    await expect(runtime.activeThreadRPC('thread.interrupt', rpc)).resolves.toBe(false);

    expect(rpc).toHaveBeenCalledTimes(1);
    expect(rpc).toHaveBeenCalledWith({
      cwd: '/repo/app',
      threadId: 'thread-1',
      expectedTurnId: 'turn-1',
      requestId: 'stop-request-1',
      source: 'ui_stop',
    });
    expect(runtime.notifyAction).toHaveBeenCalledWith('停止未确认，任务可能仍在运行', 'warning', { threadId: 'thread-1' });
    expect(runtime.notifyAction).not.toHaveBeenCalledWith(
      '正在请求停止，尚未确认，任务可能仍在运行',
      'info',
      { threadId: 'thread-1' },
    );
    expect(runtime.notifyAction).not.toHaveBeenCalledWith('已发送中断请求', 'success', { threadId: 'thread-1' });
    expect(runtime.addWarning).toHaveBeenCalledWith('warn', 'thread.interrupt.unconfirmed', {
      threadId: 'thread-1',
      error: 'stop confirmation timed out; see Health diagnostic ID',
    });
  });

  it('does not interrupt when the active target is not interruptible', async () => {
    const runtime = createRuntime();
    const deps = createDeps({
      activeThreadInterruptTarget: vi.fn(() => ({ threadId: 'thread-1', interruptible: false })),
    });
    const rpc = vi.fn().mockResolvedValue(successfulInterruptResult());
    attachActiveThreadRpcRuntime(runtime, deps);

    await expect(runtime.activeThreadRPC('thread.interrupt', rpc)).resolves.toBe(false);

    expect(rpc).not.toHaveBeenCalled();
    expect(runtime.notifyAction).toHaveBeenCalledWith('当前没有可中断任务', 'warning', { threadId: 'thread-1' });
    expect(deps.createRequestId).not.toHaveBeenCalled();
  });

  it('generates one unique request id at each accepted user stop boundary', async () => {
    const runtime = createRuntime();
    const deps = createDeps({
      createRequestId: vi.fn()
        .mockReturnValueOnce('stop-request-1')
        .mockReturnValueOnce('stop-request-2'),
    });
    const rpc = vi.fn()
      .mockResolvedValueOnce(successfulInterruptResult({ requestId: 'stop-request-1' }))
      .mockResolvedValueOnce(successfulInterruptResult({ requestId: 'stop-request-2' }));
    attachActiveThreadRpcRuntime(runtime, deps);

    await expect(runtime.activeThreadRPC('thread.interrupt', rpc)).resolves.toBe(true);
    await expect(runtime.activeThreadRPC('thread.interrupt', rpc)).resolves.toBe(true);

    expect(deps.createRequestId).toHaveBeenCalledTimes(2);
    expect(rpc).toHaveBeenNthCalledWith(1, expect.objectContaining({
      expectedTurnId: 'turn-1',
      requestId: 'stop-request-1',
    }));
    expect(rpc).toHaveBeenNthCalledWith(2, expect.objectContaining({
      expectedTurnId: 'turn-1',
      requestId: 'stop-request-2',
    }));
  });

  it('records warning when backend lifecycle rpc fails', async () => {
    const runtime = createRuntime();
    const deps = createDeps();
    const rpc = vi.fn().mockRejectedValue(new Error('backend offline'));
    attachActiveThreadRpcRuntime(runtime, deps);

    await expect(runtime.activeThreadRPC('thread.compact', rpc)).rejects.toThrow('backend offline');

    expect(rpc).toHaveBeenCalledWith({ cwd: '/repo/app', threadId: 'thread-1' });
    expect(runtime.notifyAction).toHaveBeenCalledWith('压缩上下文失败，请重试。', 'error', { threadId: 'thread-1' });
    expect(runtime.addWarning).toHaveBeenCalledWith('error', 'thread.compact.failed', {
      threadId: 'thread-1',
      error: 'action failure; see Health diagnostic ID',
    });
  });

  it('keeps generic lifecycle actions boolean while recovery retains the validated result', async () => {
    const { runtime, state } = createStatefulRuntime({
      activeThreadId: 'thread-1',
      threadRecoveryPendingByThread: {},
    });
    const deps = createDeps();
    const genericRpc = vi.fn().mockResolvedValue({ recovered: true });
    const recoverRpc = vi.fn().mockResolvedValue({ recovered: true });
    attachActiveThreadRpcRuntime(runtime, deps);

    await expect(runtime.activeThreadRPC('thread.compact', genericRpc)).resolves.toBe(true);
    await expect(runtime.recoverActiveThreadRPC(recoverRpc)).resolves.toBe(true);

    expect(genericRpc).toHaveBeenCalledTimes(1);
    expect(recoverRpc).toHaveBeenCalledTimes(1);
    expect(runtime.notifyAction).toHaveBeenCalledWith('恢复请求已接受，正在恢复', 'success', { threadId: 'thread-1' });
    expect(state().threadRecoveryPendingByThread).toEqual({});
  });

  it('projects recovered false as a typed failure without an accepted notice', async () => {
    const { runtime, state } = createStatefulRuntime({
      activeThreadId: 'thread-1',
      threadRecoveryPendingByThread: {},
    });
    const deps = createDeps();
    const rpc = vi.fn().mockResolvedValue({ recovered: false });
    attachActiveThreadRpcRuntime(runtime, deps);

    await expect(runtime.recoverActiveThreadRPC(rpc)).resolves.toBe(false);

    expect(rpc).toHaveBeenCalledTimes(1);
    expect(runtime.notifyAction).toHaveBeenCalledWith('恢复请求失败', 'warning', { threadId: 'thread-1' });
    expect(runtime.notifyAction).not.toHaveBeenCalledWith('恢复请求已接受，正在恢复', 'success', { threadId: 'thread-1' });
    expect(runtime.addWarning).toHaveBeenCalledWith('warn', 'thread.recover.failed', { threadId: 'thread-1' });
    expect(state().threadRecoveryPendingByThread).toEqual({});
  });

  it('clears stale recovery pending without publishing notice to a new active thread', async () => {
    let resolveRecover;
    const response = new Promise((resolve) => { resolveRecover = resolve; });
    const { runtime, state } = createStatefulRuntime({
      activeThreadId: 'thread-1',
      threadRecoveryPendingByThread: {},
    });
    const deps = createDeps();
    const rpc = vi.fn(() => response);
    attachActiveThreadRpcRuntime(runtime, deps);

    const pending = runtime.recoverActiveThreadRPC(rpc);
    expect(state().threadRecoveryPendingByThread).toEqual({ 'thread-1': true });
    runtime.set({ activeThreadId: 'thread-2' });
    resolveRecover({ recovered: true });

    await expect(pending).resolves.toBe(true);
    expect(rpc).toHaveBeenCalledTimes(1);
    expect(state().threadRecoveryPendingByThread).toEqual({});
    expect(runtime.notifyAction).not.toHaveBeenCalled();
    expect(runtime.addWarning).not.toHaveBeenCalled();
  });

  it('fails fast when the validated recovery result is missing', async () => {
    const { runtime, state } = createStatefulRuntime({
      activeThreadId: 'thread-1',
      threadRecoveryPendingByThread: {},
    });
    const rpc = vi.fn().mockResolvedValue(undefined);
    attachActiveThreadRpcRuntime(runtime, createDeps());

    await expect(runtime.recoverActiveThreadRPC(rpc)).rejects.toBeInstanceOf(TypeError);
    expect(rpc).toHaveBeenCalledTimes(1);
    expect(state().threadRecoveryPendingByThread).toEqual({});
    expect(runtime.notifyAction).not.toHaveBeenCalledWith('恢复请求失败', 'warning', { threadId: 'thread-1' });
  });

  it('fails fast when the recovery pending truth source is missing', async () => {
    const { runtime } = createStatefulRuntime({ activeThreadId: 'thread-1' });
    const rpc = vi.fn().mockResolvedValue({ recovered: true });
    attachActiveThreadRpcRuntime(runtime, createDeps());

    await expect(runtime.recoverActiveThreadRPC(rpc)).rejects.toBeInstanceOf(TypeError);
    expect(rpc).not.toHaveBeenCalled();
  });

  it('publishes accepted notice when the active alias resolves to the captured backend thread', async () => {
    const { runtime } = createStatefulRuntime({
      activeThreadId: 'agent-1',
      threadRecoveryPendingByThread: {},
    });
    const deps = createDeps({
      backendThreadIdForState: vi.fn((_state, threadId) => (threadId === 'agent-1' ? 'thread-1' : threadId)),
    });
    const rpc = vi.fn().mockResolvedValue({ recovered: true });
    attachActiveThreadRpcRuntime(runtime, deps);

    await expect(runtime.recoverActiveThreadRPC(rpc)).resolves.toBe(true);
    expect(rpc).toHaveBeenCalledTimes(1);
    expect(runtime.notifyAction).toHaveBeenCalledWith('恢复请求已接受，正在恢复', 'success', { threadId: 'thread-1' });
  });
});
