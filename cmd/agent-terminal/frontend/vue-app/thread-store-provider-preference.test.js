// @ts-nocheck
import { beforeEach, describe, expect, it, vi } from 'vitest';

const apiMock = vi.hoisted(() => ({ callAPI: vi.fn() }));
const logMock = vi.hoisted(() => ({ logDebug: vi.fn(), logInfo: vi.fn(), logWarn: vi.fn() }));

vi.mock('./services/api.js', () => ({ callAPI: apiMock.callAPI }));
vi.mock('./services/log.js', () => ({
  logDebug: logMock.logDebug,
  logInfo: logMock.logInfo,
  logWarn: logMock.logWarn,
}));

import { useThreadStore } from './stores/threads.js';

function buildSnapshot(threadId = 'thread-scoped-provider') {
  return {
    threads: [{ id: threadId, name: threadId, state: 'idle' }],
    statuses: { [threadId]: 'idle' },
    interruptibleByThread: { [threadId]: false },
    statusHeadersByThread: { [threadId]: '' },
    statusDetailsByThread: { [threadId]: '' },
    timelinesByThread: { [threadId]: [] },
    diffTextByThread: {},
    diffRevisionByThread: { [threadId]: 0 },
    tokenUsageByThread: {},
    agentMetaById: {},
    agentRuntimeById: {},
    activityStatsByThread: {},
    alertsByThread: {},
    skillRevision: 0,
    activeThreadId: '',
    activeCmdThreadId: '',
  };
}

function resetThreadStore(store) {
  store.setPreferenceScopeCwd('');
  Object.assign(store.state, {
    activeThreadId: '',
    activeCmdThreadId: '',
    sendBlockedNoticesByThread: {},
    sendHoldNoticesByThread: {},
    threads: [],
    statuses: {},
    interruptibleByThread: {},
    statusHeadersByThread: {},
    statusDetailsByThread: {},
    timelinesByThread: {},
    diffTextByThread: {},
    diffRevisionByThread: {},
    tokenUsageByThread: {},
    agentMetaById: {},
    agentRuntimeById: {},
    activityStatsByThread: {},
    alertsByThread: {},
    skillRevision: 0,
  });
}

describe('thread store provider preference scope', () => {
  beforeEach(() => {
    apiMock.callAPI.mockReset();
    logMock.logDebug.mockReset();
    logMock.logInfo.mockReset();
    logMock.logWarn.mockReset();
    resetThreadStore(useThreadStore());
  });

  it('prefers the launch cwd scoped active provider over the global toolbar provider', async () => {
    const store = useThreadStore();
    let startPayload = null;
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'ui/preferences/get') {
        if (payload?.key === 'settings.provider.active' && payload?.cwd === '/repo') return 'claude';
        if (payload?.key === 'settings.provider.active') return 'codex';
        return undefined;
      }
      if (method === 'config/builtinTools/read') return { tools: [] };
      if (method === 'thread/start') {
        startPayload = payload;
        return { thread: { id: 'thread-scoped-provider' } };
      }
      if (method === 'ui/state/get') return buildSnapshot();
      if (method === 'ui/preferences/set') return {};
      return {};
    });

    await store.startThread('/repo', {});

    expect(startPayload).toEqual(expect.objectContaining({
      cwd: '/repo',
      modelProvider: 'claude',
    }));
  });

  it('does not fall back to global provider when the launch cwd provider read fails', async () => {
    const store = useThreadStore();
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'ui/preferences/get') {
        if (payload?.key === 'settings.provider.active' && payload?.cwd === '/repo') {
          throw new Error('scoped read failed');
        }
        if (payload?.key === 'settings.provider.active') return 'codex';
        return undefined;
      }
      if (method === 'thread/start') return { thread: { id: 'thread-should-not-start' } };
      return {};
    });

    await expect(store.startThread('/repo', {})).rejects.toThrow('scoped read failed');
    expect(apiMock.callAPI).not.toHaveBeenCalledWith('thread/start', expect.anything());
  });
});
