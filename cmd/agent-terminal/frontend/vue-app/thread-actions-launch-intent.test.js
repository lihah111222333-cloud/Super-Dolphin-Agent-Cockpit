// @ts-nocheck
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { startThread } from './stores/thread-actions-helpers.js';

function createStartContext() {
  const callAPI = vi.fn(async (method, payload) => {
    if (method === 'ui/preferences/get') {
      return payload?.key === 'settings.provider.active' ? 'claude-3.7-sonnet' : undefined;
    }
    if (method === 'config/builtinTools/read') return {};
    if (method === 'thread/start') return { thread: { id: 'thread-intent' } };
    return {};
  });
  return {
    callAPI,
    logInfo: vi.fn(),
    logWarn: vi.fn(),
    state: {
      activeThreadId: '',
      activeCmdThreadId: '',
      threads: [],
      agentRuntimeById: {},
      timelinesByThread: {},
    },
    syncRuntimeState: vi.fn().mockResolvedValue(undefined),
  };
}

beforeEach(() => {
  vi.restoreAllMocks();
});

describe('thread action launch intent', () => {
  it('forwards launch intent id when starting a thread', async () => {
    const ctx = createStartContext();

    const id = await startThread(ctx, '/repo', {
      launchIntentId: 'launch_018f00e0-39fc-72ac-a47a-2a858c75d111',
      skipSaveActive: true,
    });

    expect(id).toBe('thread-intent');
    expect(ctx.callAPI).toHaveBeenCalledWith('thread/start', {
      cwd: '/repo',
      provider: 'claude',
      modelProvider: 'claude-3.7-sonnet',
      launchIntentId: 'launch_018f00e0-39fc-72ac-a47a-2a858c75d111',
    });
  });

  it('seeds the first user message before start runtime sync returns', async () => {
    let releaseSync = () => {};
    const syncGate = new Promise((resolve) => { releaseSync = resolve; });
    const ctx = createStartContext();
    ctx.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'ui/preferences/get') {
        return payload?.key === 'settings.provider.active' ? 'claude-3.7-sonnet' : undefined;
      }
      if (method === 'config/builtinTools/read') return {};
      if (method === 'thread/start') return { thread: { id: 'thread-intent' }, pending_launch: true };
      return {};
    });
    ctx.syncRuntimeState.mockImplementation(async () => { await syncGate; });

    const pending = startThread(ctx, '/repo', {
      deferSpawn: true,
      optimisticUserMessage: {
        text: 'hello pending launch',
        attachments: [],
      },
      skipSaveActive: true,
    });
    for (let i = 0; i < 30 && ctx.syncRuntimeState.mock.calls.length === 0; i += 1) await Promise.resolve();

    const timelineBeforeSync = ctx.state.timelinesByThread['thread-intent'] || [];

    releaseSync();
    await pending;

    expect(timelineBeforeSync).toHaveLength(1);
    expect(timelineBeforeSync[0]).toMatchObject({
      kind: 'user',
      text: 'hello pending launch',
    });
    expect((timelineBeforeSync[0]?.id || '').toString()).toContain('thread-intent-optimistic-user-');
  });

  it('can return after local pending-launch state without waiting for initial runtime sync', async () => {
    let releaseSync = () => {};
    const syncGate = new Promise((resolve) => { releaseSync = resolve; });
    const ctx = createStartContext();
    ctx.syncRuntimeState.mockImplementation(async () => { await syncGate; });

    const pending = startThread(ctx, '/repo', {
      deferSpawn: true,
      skipInitialRuntimeSync: true,
      skipSaveActive: true,
    });
    const onResolved = vi.fn();
    pending.then(onResolved);
    for (let i = 0; i < 20 && ctx.syncRuntimeState.mock.calls.length === 0; i += 1) await Promise.resolve();
    await Promise.resolve();

    expect(ctx.syncRuntimeState).toHaveBeenCalled();
    expect(onResolved).toHaveBeenCalledWith('thread-intent');

    releaseSync();
    await pending;
  });
});
