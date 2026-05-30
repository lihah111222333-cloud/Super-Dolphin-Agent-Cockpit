import { describe, expect, it, vi } from 'vitest';
import { createBackendApi, RPC_METHODS } from './backendApi.js';

describe('frontend-app backend API facade', () => {
  it('starts a pending backend thread with explicit cwd and launch intent', async () => {
    const callAPI = vi.fn().mockResolvedValue({ threadId: 'thread-123' });
    const api = createBackendApi({ callAPI });

    await api.startThread({
      cwd: '/repo/app',
      name: 'Hello',
      provider: 'codex',
      deferSpawn: true,
      launchIntentId: 'launch_018f00e0-39fc-72ac-a47a-2a858c75d111',
      optimisticUserMessage: 'Hello',
      skipInitialRuntimeSync: true,
    });

    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.THREAD_START, {
      cwd: '/repo/app',
      name: 'Hello',
      provider: 'codex',
      defer_spawn: true,
      launchIntentId: 'launch_018f00e0-39fc-72ac-a47a-2a858c75d111',
      optimisticUserMessage: 'Hello',
      skipInitialRuntimeSync: true,
    });
  });

  it('sends turn/start with explicit cwd and file mentions', async () => {
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

  it('fails fast before cwd-scoped RPCs when cwd is missing', () => {
    const callAPI = vi.fn();
    const api = createBackendApi({ callAPI });

    expect(() => api.getProjects({ cwd: '' })).toThrow('cwd is required');
    expect(() => api.startThread({ cwd: '/repo/app', name: 'Hello' })).toThrow('provider is required');
    expect(callAPI).not.toHaveBeenCalled();
  });
});
