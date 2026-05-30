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

function buildSnapshot({ threadId, threads }) {
  return {
    threads,
    statuses: { [threadId]: 'idle' },
    interruptibleByThread: { [threadId]: false },
    statusHeadersByThread: { [threadId]: '等待指示' },
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
    activeThreadId: threadId,
    activeCmdThreadId: '',
  };
}

describe('thread store snapshot timestamp updates', () => {
  beforeEach(() => {
    apiMock.callAPI.mockReset();
    resetThreadStore(useThreadStore());
  });

  it('applies timestamp-only snapshot changes so chat ordering follows latest replies', async () => {
    const store = useThreadStore();
    const repliedThreadId = 'thread-old-replied';
    const newerCreatedThreadId = 'thread-newer-created';
    store.state.activeThreadId = repliedThreadId;
    store.state.threads = [
      {
        id: repliedThreadId,
        name: 'Old with latest reply',
        state: 'idle',
        createdAt: '2026-05-22T10:00:00Z',
        updatedAt: '2026-05-22T10:00:00Z',
      },
      {
        id: newerCreatedThreadId,
        name: 'Newer created',
        state: 'idle',
        createdAt: '2026-05-22T10:04:00Z',
        updatedAt: '2026-05-22T10:04:00Z',
      },
    ];
    apiMock.callAPI.mockImplementation(async (method) => {
      if (method !== 'ui/state/get') return {};
      return buildSnapshot({
        threadId: repliedThreadId,
        threads: [
          {
            id: repliedThreadId,
            name: 'Old with latest reply',
            state: 'idle',
            createdAt: '2026-05-22T10:00:00Z',
            updatedAt: '2026-05-22T10:05:00Z',
          },
          {
            id: newerCreatedThreadId,
            name: 'Newer created',
            state: 'idle',
            createdAt: '2026-05-22T10:04:00Z',
            updatedAt: '2026-05-22T10:04:00Z',
          },
        ],
      });
    });

    await store.syncThreadState(repliedThreadId);

    expect(store.state.threads.find((thread) => thread.id === repliedThreadId)?.updatedAt).toBe('2026-05-22T10:05:00Z');
    expect(store.getThreadsByMode('chat').map((thread) => thread.id)).toEqual([repliedThreadId, newerCreatedThreadId]);
  });
});
