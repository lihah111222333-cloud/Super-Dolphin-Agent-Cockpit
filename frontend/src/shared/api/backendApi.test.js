import { describe, expect, test, vi } from 'vitest';
import { createBackendApi, RPC_METHODS } from './backendApi';

describe('backend API facade', () => {
  test('starts an empty chat thread as a pending launch before the first turn', async () => {
    const callAPI = vi.fn().mockResolvedValue({ threadId: 'thread-123' });
    const api = createBackendApi({ callAPI });

    const result = await api.startThread({
      cwd: '/repo/app',
      name: 'Hello',
      provider: 'codex',
    });

    expect(result).toEqual({ threadId: 'thread-123' });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.THREAD_START, {
      cwd: '/repo/app',
      name: 'Hello',
      provider: 'codex',
      defer_spawn: true,
    });
  });

  test('normalizes legacy agentKey to provider for thread/start', async () => {
    const callAPI = vi.fn().mockResolvedValue({ threadId: 'thread-123' });
    const api = createBackendApi({ callAPI });

    await api.startThread({
      cwd: '/repo/app',
      name: 'Hello',
      agentKey: 'codex',
    });

    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.THREAD_START, {
      cwd: '/repo/app',
      name: 'Hello',
      provider: 'codex',
      defer_spawn: true,
    });
  });

  test('fails fast when thread/start has no provider', async () => {
    const callAPI = vi.fn();
    const api = createBackendApi({ callAPI });

    expect(() => api.startThread({ cwd: '/repo/app', name: 'Hello' })).toThrow('provider is required');
    expect(callAPI).not.toHaveBeenCalled();
  });

  test('sends the first user turn through turn/start with explicit cwd and file mentions', async () => {
    const callAPI = vi.fn().mockResolvedValue({ ok: true });
    const api = createBackendApi({ callAPI });

    await api.startTurn({
      cwd: '/repo/app',
      threadId: 'thread-123',
      input: 'build it',
      attachments: ['/tmp/a.txt'],
      manualSkillSelection: false,
    });

    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.TURN_START, {
      cwd: '/repo/app',
      threadId: 'thread-123',
      input: [
        { type: 'text', text: 'build it' },
        { type: 'mention', name: 'a.txt', path: '/tmp/a.txt' },
      ],
      manualSkillSelection: false,
    });
  });

  test('fails fast when a cwd-scoped RPC is called without cwd', async () => {
    const callAPI = vi.fn();
    const api = createBackendApi({ callAPI });

    expect(() => api.getProjects({ cwd: '' })).toThrow('cwd is required');
    expect(callAPI).not.toHaveBeenCalled();
  });

  test('uses the registered dashboard DAG RPC without leaking cwd to strict backend params', async () => {
    const callAPI = vi.fn().mockResolvedValue({ runKey: 'run-1' });
    const api = createBackendApi({ callAPI });

    await api.startDag({
      cwd: '/repo/app',
      dagKey: 'dag-1',
      triggerSource: 'manual',
      idempotencyKey: 'op-1',
    });

    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.DASHBOARD_DAG_START, {
      dagKey: 'dag-1',
      triggerSource: 'manual',
      idempotencyKey: 'op-1',
    });
  });
});
