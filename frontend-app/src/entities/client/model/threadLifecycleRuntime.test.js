import { describe, expect, it, vi } from 'vitest';
import { attachActiveThreadRpcRuntime } from './threadLifecycleRuntime.js';

function createRuntime(overrides = {}) {
  return {
    get: vi.fn(() => ({ activeThreadId: 'thread-1' })),
    requireCwd: vi.fn(() => '/repo/app'),
    notifyAction: vi.fn(),
    addWarning: vi.fn(),
    ...overrides,
  };
}

function createDeps(overrides = {}) {
  return {
    activeThreadInterruptTarget: vi.fn(() => ({ threadId: 'thread-1', interruptible: true })),
    backendThreadIdForState: vi.fn(() => 'thread-1'),
    cleanObject: (payload) => Object.fromEntries(
      Object.entries(payload).filter(([, value]) => value !== undefined && value !== ''),
    ),
    ...overrides,
  };
}

describe('thread lifecycle runtime', () => {
  it('sends interrupt with explicit ui_stop source', async () => {
    const runtime = createRuntime();
    const deps = createDeps();
    const rpc = vi.fn().mockResolvedValue({ ok: true });
    attachActiveThreadRpcRuntime(runtime, deps);

    await expect(runtime.activeThreadRPC('thread.interrupt', rpc)).resolves.toBe(true);

    expect(rpc).toHaveBeenCalledWith({ cwd: '/repo/app', threadId: 'thread-1', source: 'ui_stop' });
    expect(runtime.notifyAction).toHaveBeenCalledWith('已发送中断请求', 'success', { threadId: 'thread-1' });
  });

  it('reports interrupt ok:false as warning without showing success', async () => {
    const runtime = createRuntime();
    const deps = createDeps();
    const rpc = vi.fn().mockResolvedValue({ ok: false, error: 'turn already completed' });
    attachActiveThreadRpcRuntime(runtime, deps);

    await expect(runtime.activeThreadRPC('thread.interrupt', rpc)).resolves.toBe(false);

    expect(rpc).toHaveBeenCalledWith({ cwd: '/repo/app', threadId: 'thread-1', source: 'ui_stop' });
    expect(runtime.notifyAction).toHaveBeenCalledWith('中断当前执行失败：turn already completed', 'warning', { threadId: 'thread-1' });
    expect(runtime.notifyAction).not.toHaveBeenCalledWith('已发送中断请求', 'success', { threadId: 'thread-1' });
    expect(runtime.addWarning).toHaveBeenCalledWith('warn', 'thread.interrupt.failed', {
      threadId: 'thread-1',
      error: 'turn already completed',
    });
  });

  it('does not interrupt when the active target is not interruptible', async () => {
    const runtime = createRuntime();
    const deps = createDeps({
      activeThreadInterruptTarget: vi.fn(() => ({ threadId: 'thread-1', interruptible: false })),
    });
    const rpc = vi.fn().mockResolvedValue({ ok: true });
    attachActiveThreadRpcRuntime(runtime, deps);

    await expect(runtime.activeThreadRPC('thread.interrupt', rpc)).resolves.toBe(false);

    expect(rpc).not.toHaveBeenCalled();
    expect(runtime.notifyAction).toHaveBeenCalledWith('当前没有可中断任务', 'warning', { threadId: 'thread-1' });
  });

  it('records warning when backend lifecycle rpc fails', async () => {
    const runtime = createRuntime();
    const deps = createDeps();
    const rpc = vi.fn().mockRejectedValue(new Error('backend offline'));
    attachActiveThreadRpcRuntime(runtime, deps);

    await expect(runtime.activeThreadRPC('thread.compact', rpc)).resolves.toBe(false);

    expect(rpc).toHaveBeenCalledWith({ cwd: '/repo/app', threadId: 'thread-1' });
    expect(runtime.notifyAction).toHaveBeenCalledWith('压缩上下文失败：backend offline', 'error', { threadId: 'thread-1' });
    expect(runtime.addWarning).toHaveBeenCalledWith('error', 'thread.compact.failed', {
      threadId: 'thread-1',
      error: 'backend offline',
    });
  });
});
