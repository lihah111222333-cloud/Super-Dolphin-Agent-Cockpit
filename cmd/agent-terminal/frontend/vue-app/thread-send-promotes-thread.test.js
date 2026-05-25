// @ts-nocheck
import { beforeEach, describe, expect, it, vi } from 'vitest';

const apiMock = vi.hoisted(() => ({
  callAPI: vi.fn(),
}));

vi.mock('./services/api.js', () => ({
  callAPI: apiMock.callAPI,
}));

vi.mock('./services/log.js', () => ({
  logDebug: vi.fn(),
  logInfo: vi.fn(),
  logWarn: vi.fn(),
}));

import { useThreadStore } from './stores/threads.js';

function resetThreadStore(store) {
  store.setPreferenceScopeCwd('');
  Object.assign(store.state, {
    activeThreadId: '',
    activeCmdThreadId: '',
    pinnedThreadAtById: {},
    archivedThreadAtById: {},
    threads: [],
    statuses: {},
    interruptibleByThread: {},
    viewPrefsChat: null,
    viewPrefsCmd: null,
    statusHeadersByThread: {},
    statusDetailsByThread: {},
    overlayTextByThread: {},
    overlayTypeByThread: {},
    overlayPriorityByThread: {},
    timelinesByThread: {},
    diffTextByThread: {},
    diffRevisionByThread: {},
    tokenUsageByThread: {},
    agentMetaById: {},
    agentRuntimeById: {},
    mainAgentId: '',
    mainAgentState: '',
    partial: false,
    activityStatsByThread: {},
    alertsByThread: {},
    skillRevision: 0,
  });
}

async function flushAsync() {
  await Promise.resolve();
  await Promise.resolve();
}

describe('thread send chat ordering', () => {
  beforeEach(() => {
    apiMock.callAPI.mockReset();
    resetThreadStore(useThreadStore());
  });

  it('promotes the thread before turn/start resolves', async () => {
    const store = useThreadStore();
    const sendingThreadId = 'thread-old-sending';
    const newerThreadId = 'thread-newer-idle';
    store.state.activeThreadId = sendingThreadId;
    store.state.threads = [
      {
        id: sendingThreadId,
        name: 'Old thread',
        state: 'idle',
        createdAt: '2026-05-22T10:00:00Z',
        updatedAt: '2026-05-22T10:00:00Z',
      },
      {
        id: newerThreadId,
        name: 'Newer idle thread',
        state: 'idle',
        createdAt: '2026-05-22T10:04:00Z',
        updatedAt: '2026-05-22T10:04:00Z',
      },
    ];
    store.state.timelinesByThread[sendingThreadId] = [];

    let releaseTurnStart;
    const turnStartPending = new Promise((resolve) => {
      releaseTurnStart = resolve;
    });
    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'turn/start') {
        await turnStartPending;
        return { ok: true };
      }
      return {};
    });

    const sendPromise = store.sendMessage(sendingThreadId, 'promote this thread', []);
    await flushAsync();

    expect(store.getThreadsByMode('chat').map((thread) => thread.id)).toEqual([sendingThreadId, newerThreadId]);
    expect(store.state.threads.find((thread) => thread.id === sendingThreadId)?.updatedAt).not.toBe('2026-05-22T10:00:00Z');

    releaseTurnStart({ ok: true });
    await sendPromise;
  });
});
