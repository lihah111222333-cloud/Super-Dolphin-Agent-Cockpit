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
    sendBlockedNoticesByThread: {},
    sendHoldNoticesByThread: {},

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

function buildThreadPatch({
  threadId = 'thread-live',
  threadName = threadId,
  status = 'running',
  statusHeader = 'AI 正在输出',
  statusDetails = '',
  sequence = 1,
  source = 'item/reasoning/summaryTextDelta',
  timelineItems = [],
  removedItemIds = [],
  timelineOrder,
  diffText = '',
  diffRevision = 0,
} = {}) {
  return {
    threadId,
    source,
    sequence,
    thread: { id: threadId, name: threadName, state: status },
    status,
    interruptible: status !== 'idle',
    statusHeader,
    statusDetails,
    diffText,
    diffRevision,
    timelineItems,
    removedItemIds,
    timelineOrder: Array.isArray(timelineOrder) ? timelineOrder : timelineItems.map((item) => item.id),
  };
}

function buildSnapshot({
  threadId = 'thread-live',
  threadName = threadId,
  status = 'idle',
  statusHeader = '等待指示',
  timelineItems = [],
} = {}) {
  return {
    threads: [{ id: threadId, name: threadName, state: status }],
    statuses: { [threadId]: status },
    interruptibleByThread: { [threadId]: status !== 'idle' },
    statusHeadersByThread: { [threadId]: statusHeader },
    statusDetailsByThread: { [threadId]: '' },
    timelinesByThread: { [threadId]: timelineItems },
    diffTextByThread: { [threadId]: '' },
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

describe('thread store runtime thread patch', () => {
  beforeEach(() => {
    apiMock.callAPI.mockReset();
    const store = useThreadStore();
    resetThreadStore(store);
  });

  it('applies selected cmd thread item-level patches and suppresses duplicate delta pulls', async () => {
    const store = useThreadStore();
    const threadId = 'thread-live-command-output';
    const methods = [];
    store.state.activeCmdThreadId = threadId;
    store.state.threads = [{ id: threadId, name: 'Live Cmd', state: 'running' }];
    apiMock.callAPI.mockImplementation(async (method) => {
      methods.push(method);
      return {};
    });

    store.handleBridgeEvent({
      method: 'ui/thread/patch',
      payload: buildThreadPatch({
        threadId,
        sequence: 11,
        source: 'item/commandExecution/outputDelta',
        timelineItems: [{ id: 'command-1', kind: 'command', command: 'npm test', output: 'partial output', status: 'running', ts: '2026-03-08T00:00:00Z' }],
      }),
    });

    expect(store.getThreadTimeline(threadId)[0]?.output).toBe('partial output');

    store.handleBridgeEvent({ method: 'item/commandExecution/outputDelta', payload: { threadId, delta: 'partial output' } });
    store.handleBridgeEvent({ method: 'ui/thread/changed', payload: { threadId, source: 'item/commandExecution/outputDelta' } });
    await Promise.resolve();
    await Promise.resolve();

    expect(methods).toEqual([]);
  });

  it('applies live patch runtime metadata so relaunched agents are visible without snapshot refresh', async () => {
    const store = useThreadStore();
    const threadId = 'thread-relaunch';

    store.handleBridgeEvent({
      method: 'ui/thread/patch',
      payload: {
        ...buildThreadPatch({
          threadId,
          threadName: 'Relaunched',
          status: 'running',
          sequence: 21,
          source: 'agent/launched',
        }),
        thread: {
          id: threadId,
          name: 'Relaunched',
          state: 'running',
          createdAt: '2026-05-22T09:00:00Z',
          updatedAt: '2026-05-22T09:01:00Z',
        },
        agentRuntime: {
          agentId: 'agent-relaunch',
          provider: 'codex',
          providerThreadId: 'provider-thread',
          cwd: '/repo/current',
          state: 'running',
        },
      },
    });

    expect(store.state.threads.find((thread) => thread.id === threadId)).toEqual(expect.objectContaining({
      id: threadId,
      name: 'Relaunched',
      createdAt: '2026-05-22T09:00:00Z',
    }));
    expect(store.state.agentRuntimeById[threadId]).toEqual(expect.objectContaining({
      agentId: 'agent-relaunch',
      cwd: '/repo/current',
      providerThreadId: 'provider-thread',
    }));
  });

  it('updates thread timestamps from same name/state patches and reorders chat threads by latest updatedAt', () => {
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

    store.handleBridgeEvent({
      method: 'ui/thread/patch',
      payload: {
        threadId: repliedThreadId,
        source: 'item/agentMessage/delta',
        sequence: 22,
        thread: {
          id: repliedThreadId,
          name: 'Old with latest reply',
          state: 'idle',
          updatedAt: '2026-05-22T10:05:00Z',
        },
        status: 'idle',
      },
    });

    expect(store.state.threads.find((thread) => thread.id === repliedThreadId)?.updatedAt).toBe('2026-05-22T10:05:00Z');
    expect(store.getThreadsByMode('chat').map((thread) => thread.id)).toEqual([repliedThreadId, newerCreatedThreadId]);
  });

  it('does not reorder chat threads for running stream patches with newer activity timestamps', () => {
    const store = useThreadStore();
    const runningThreadId = 'thread-running';
    const idleThreadId = 'thread-idle';
    store.state.activeThreadId = runningThreadId;
    store.state.threads = [
      {
        id: runningThreadId,
        name: 'Running',
        state: 'running',
        createdAt: '2026-05-22T10:00:00Z',
        updatedAt: '2026-05-22T10:00:00Z',
      },
      {
        id: idleThreadId,
        name: 'Idle completion',
        state: 'idle',
        createdAt: '2026-05-22T10:04:00Z',
        updatedAt: '2026-05-22T10:04:00Z',
      },
    ];
    store.state.agentMetaById = {
      [idleThreadId]: { lastActiveAt: '2026-05-22T10:04:00Z' },
    };

    store.handleBridgeEvent({
      method: 'ui/thread/patch',
      payload: {
        threadId: runningThreadId,
        source: 'item/agentMessage/delta',
        sequence: 23,
        thread: {
          id: runningThreadId,
          name: 'Running',
          state: 'running',
          updatedAt: '2026-05-22T10:05:00Z',
        },
        status: 'running',
        agentMeta: { lastActiveAt: '2026-05-22T10:05:00Z' },
      },
    });

    expect(store.state.agentMetaById[runningThreadId]?.lastActiveAt).toBe('2026-05-22T10:05:00Z');
    expect(store.state.threads.find((thread) => thread.id === runningThreadId)?.updatedAt).toBe('2026-05-22T10:00:00Z');
    expect(store.getThreadsByMode('chat').map((thread) => thread.id)).toEqual([idleThreadId, runningThreadId]);
  });

  it('does not let turn started patches overwrite optimistic send ordering', () => {
    const store = useThreadStore();
    const sendingThreadId = 'thread-sending';
    const idleThreadId = 'thread-idle';
    store.state.activeThreadId = sendingThreadId;
    store.state.threads = [
      {
        id: sendingThreadId,
        name: 'Sending',
        state: 'running',
        createdAt: '2026-05-22T10:00:00Z',
        updatedAt: '2026-05-22T10:06:00Z',
      },
      {
        id: idleThreadId,
        name: 'Idle completion',
        state: 'idle',
        createdAt: '2026-05-22T10:05:00Z',
        updatedAt: '2026-05-22T10:05:00Z',
      },
    ];

    store.handleBridgeEvent({
      method: 'ui/thread/patch',
      payload: {
        threadId: sendingThreadId,
        source: 'turn/started',
        sequence: 24,
        thread: {
          id: sendingThreadId,
          name: 'Sending',
          state: 'thinking',
          updatedAt: '2026-05-22T10:00:00Z',
        },
        status: 'thinking',
        agentMeta: { lastActiveAt: '2026-05-22T10:00:00Z' },
      },
    });

    expect(store.state.threads.find((thread) => thread.id === sendingThreadId)?.updatedAt).toBe('2026-05-22T10:06:00Z');
    expect(store.getThreadsByMode('chat').map((thread) => thread.id)).toEqual([sendingThreadId, idleThreadId]);
  });

  it('reorders chat threads for idle patches with newer agent activity', () => {
    const store = useThreadStore();
    const completedThreadId = 'thread-completed';
    const olderThreadId = 'thread-older';
    store.state.activeThreadId = completedThreadId;
    store.state.threads = [
      {
        id: olderThreadId,
        name: 'Older completion',
        state: 'idle',
        createdAt: '2026-05-22T10:05:00Z',
        updatedAt: '2026-05-22T10:05:00Z',
      },
      {
        id: completedThreadId,
        name: 'Completed',
        state: 'running',
        createdAt: '2026-05-22T10:00:00Z',
        updatedAt: '2026-05-22T10:00:00Z',
      },
    ];
    store.state.agentMetaById = {
      [olderThreadId]: { lastActiveAt: '2026-05-22T10:05:00Z' },
    };

    store.handleBridgeEvent({
      method: 'ui/thread/patch',
      payload: {
        threadId: completedThreadId,
        source: 'turn/completed',
        sequence: 24,
        thread: {
          id: completedThreadId,
          name: 'Completed',
          state: 'idle',
        },
        status: 'idle',
        agentMeta: { lastActiveAt: '2026-05-22T10:06:00Z' },
      },
    });

    expect(store.state.agentMetaById[completedThreadId]?.lastActiveAt).toBe('2026-05-22T10:06:00Z');
    expect(store.getThreadsByMode('chat').map((thread) => thread.id)).toEqual([completedThreadId, olderThreadId]);
  });

  it('merges live agent metadata without dropping snapshot aliases', () => {
    const store = useThreadStore();
    const threadId = 'thread-completed';
    store.state.threads = [{ id: threadId, name: threadId, state: 'idle' }];
    store.state.agentMetaById = {
      [threadId]: { alias: 'Readable name' },
    };

    store.handleBridgeEvent({
      method: 'ui/thread/patch',
      payload: {
        threadId,
        source: 'turn/completed',
        sequence: 25,
        thread: { id: threadId, name: threadId, state: 'idle' },
        status: 'idle',
        agentMeta: { lastActiveAt: '2026-05-22T10:06:00Z' },
      },
    });

    expect(store.state.agentMetaById[threadId]).toEqual({
      alias: 'Readable name',
      lastActiveAt: '2026-05-22T10:06:00Z',
    });
  });

  it('does not preserve stale lifecycle fields when applying a normalized live patch', () => {
    const store = useThreadStore();
    const threadId = 'thread-reactivated';
    store.state.threads = [{
      id: threadId,
      name: 'Reactivated',
      state: 'archived',
      lifecycleStatus: 'archived',
      createdAt: '2026-05-22T10:00:00Z',
      updatedAt: '2026-05-22T10:00:00Z',
    }];

    store.handleBridgeEvent({
      method: 'ui/thread/patch',
      payload: {
        threadId,
        source: 'agent/launched',
        sequence: 23,
        thread: {
          id: threadId,
          name: 'Reactivated',
          state: 'running',
          updatedAt: '2026-05-22T10:05:00Z',
        },
        status: 'running',
      },
    });

    const updated = store.state.threads.find((thread) => thread.id === threadId);
    expect(updated).toEqual(expect.objectContaining({
      id: threadId,
      name: 'Reactivated',
      state: 'running',
      createdAt: '2026-05-22T10:00:00Z',
      updatedAt: '2026-05-22T10:05:00Z',
    }));
    expect(updated).not.toHaveProperty('lifecycleStatus');
  });

  it('does not let inactive child patches override the current selection', async () => {
    const store = useThreadStore();
    const currentThreadId = 'thread-current';
    const childThreadId = 'thread-child';
    store.state.activeThreadId = currentThreadId;
    store.state.activeCmdThreadId = 'thread-cmd-current';
    store.state.threads = [{ id: currentThreadId, name: 'Current', state: 'running' }];

    store.handleBridgeEvent({
      method: 'ui/thread/patch',
      payload: {
        ...buildThreadPatch({
          threadId: childThreadId,
          threadName: 'Child',
          status: 'running',
          sequence: 22,
          source: 'agent/launched',
        }),
        activeThreadId: childThreadId,
        activeCmdThreadId: childThreadId,
        agentRuntime: {
          agentId: 'agent-child',
          cwd: '/repo/current',
          state: 'running',
        },
      },
    });

    expect(store.state.threads.find((thread) => thread.id === childThreadId)).toEqual(expect.objectContaining({
      id: childThreadId,
      name: 'Child',
    }));
    expect(store.state.agentRuntimeById[childThreadId]).toEqual(expect.objectContaining({
      agentId: 'agent-child',
      cwd: '/repo/current',
    }));
    expect(store.state.activeThreadId).toBe(currentThreadId);
    expect(store.state.activeCmdThreadId).toBe('thread-cmd-current');
  });

  it('keeps completion events on push path without falling back to pull sync', async () => {
    const store = useThreadStore();
    const threadId = 'thread-live-complete';
    const methods = [];
    store.state.activeThreadId = threadId;
    store.state.threads = [{ id: threadId, name: 'Live', state: 'running' }];
    apiMock.callAPI.mockImplementation(async (method) => {
      methods.push(method);
      return {};
    });

    store.handleBridgeEvent({
      method: 'ui/thread/patch',
      payload: buildThreadPatch({
        threadId,
        sequence: 21,
        source: 'item/completed',
        status: 'idle',
        statusHeader: '等待指示',
        timelineItems: [{ id: 'assistant-1', kind: 'assistant', text: 'done', ts: '2026-03-08T00:00:00Z' }],
      }),
    });
    store.handleBridgeEvent({ method: 'item/completed', payload: { threadId } });
    store.handleBridgeEvent({ method: 'ui/thread/changed', payload: { threadId, source: 'item/completed' } });
    store.handleBridgeEvent({
      method: 'ui/thread/patch',
      payload: buildThreadPatch({
        threadId,
        sequence: 22,
        source: 'turn/completed',
        status: 'idle',
        statusHeader: '等待指示',
        timelineItems: [{ id: 'assistant-1', kind: 'assistant', text: 'done', ts: '2026-03-08T00:00:00Z' }],
      }),
    });
    store.handleBridgeEvent({ method: 'turn/completed', payload: { threadId } });
    store.handleBridgeEvent({ method: 'ui/thread/changed', payload: { threadId, source: 'turn/completed' } });
    await Promise.resolve();
    await Promise.resolve();

    expect(store.getThreadStatus(threadId)).toBe('idle');
    expect(methods).toEqual([]);
  });

  it('applies diff-only thread patches without pull fallback', () => {
    const store = useThreadStore();
    const threadId = 'thread-live-diff-only';
    store.state.activeThreadId = threadId;
    store.state.threads = [{ id: threadId, name: 'Live', state: 'running' }];

    store.handleBridgeEvent({
      method: 'ui/thread/patch',
      payload: { threadId, source: 'turn/diff/updated', sequence: 23, diffText: 'diff --git a/src/a.js b/src/a.js', diffRevision: 4 },
    });

    expect(store.state.diffTextByThread[threadId]).toContain('src/a.js');
    expect(store.state.diffRevisionByThread[threadId]).toBe(4);
    expect(apiMock.callAPI).not.toHaveBeenCalled();
  });

  it('applies bridge-only field drift patches without pull fallback', () => {
    const store = useThreadStore();
    const threadId = 'thread-live-field-drift';
    store.state.activeThreadId = threadId;
    store.state.threads = [{ id: threadId, name: 'Live', state: 'running' }];

    store.handleBridgeEvent({
      method: 'ui/thread/patch',
      payload: {
        threadId,
        source: 'ui/state/preferences',
        sequence: 24,
        overlayText: 'MCP 启动中',
        overlayType: 'mcp_startup',
        overlayPriority: 90,
        activeThreadId: 'thread-selected',
        activeCmdThreadId: 'thread-cmd',
        mainAgentId: 'agent-main',
        mainAgentState: 'running',
        partial: true,
      },
    });

    expect(store.state.overlayTextByThread[threadId]).toBe('MCP 启动中');
    expect(store.state.overlayTypeByThread[threadId]).toBe('mcp_startup');
    expect(store.state.overlayPriorityByThread[threadId]).toBe(90);
    expect(store.state.activeThreadId).toBe('thread-selected');
    expect(store.state.activeCmdThreadId).toBe('thread-cmd');
    expect(store.state.mainAgentId).toBe('agent-main');
    expect(store.state.mainAgentState).toBe('running');
    expect(store.state.partial).toBe(true);
    expect(apiMock.callAPI).not.toHaveBeenCalled();
  });

  it('ignores stale empty diff payloads from bridge patches while keeping the last full diff', () => {
    const store = useThreadStore();
    const threadId = 'thread-live-diff-sticky';
    store.state.activeThreadId = threadId;
    store.state.threads = [{ id: threadId, name: 'Live', state: 'running' }];
    store.state.diffTextByThread = { [threadId]: 'diff --git a/src/a.js b/src/a.js\n--- a/src/a.js\n+++ b/src/a.js' };
    store.state.diffRevisionByThread = { [threadId]: 4 };

    store.handleBridgeEvent({
      method: 'ui/thread/patch',
      payload: buildThreadPatch({
        threadId,
        source: 'item/assistantTextDelta',
        sequence: 24,
        diffText: '',
        diffRevision: 0,
        timelineItems: [{ id: 'assistant-1', kind: 'assistant', text: 'still streaming', ts: '2026-03-08T00:00:00Z' }],
      }),
    });

    expect(store.state.diffTextByThread[threadId]).toContain('src/a.js');
    expect(store.state.diffRevisionByThread[threadId]).toBe(4);
    expect(store.getThreadTimeline(threadId)[0]?.text).toBe('still streaming');
  });

  it('allows explicit diff clears when a newer diff revision arrives', () => {
    const store = useThreadStore();
    const threadId = 'thread-live-diff-cleared';
    store.state.activeThreadId = threadId;
    store.state.threads = [{ id: threadId, name: 'Live', state: 'running' }];
    store.state.diffTextByThread = { [threadId]: 'diff --git a/src/a.js b/src/a.js\n--- a/src/a.js\n+++ b/src/a.js' };
    store.state.diffRevisionByThread = { [threadId]: 4 };

    store.handleBridgeEvent({
      method: 'ui/thread/patch',
      payload: { threadId, source: 'turn/diff/updated', sequence: 25, diffText: '', diffRevision: 5 },
    });

    expect(store.state.diffTextByThread[threadId]).toBe('');
    expect(store.state.diffRevisionByThread[threadId]).toBe(5);
  });

  it('recovers from patch sequence gaps via scoped snapshot sync', async () => {
    const store = useThreadStore();
    const threadId = 'thread-live-gap';
    const methods = [];
    store.state.activeThreadId = threadId;
    store.state.threads = [{ id: threadId, name: 'Live', state: 'running' }];
    apiMock.callAPI.mockImplementation(async (method) => {
      methods.push(method);
      if (method !== 'ui/state/get') return {};
      return buildSnapshot({
        threadId,
        status: 'running',
        statusHeader: 'AI 正在输出',
        timelineItems: [{ id: 'assistant-1', kind: 'assistant', text: 'gap recovered', ts: '2026-03-08T00:00:00Z' }],
      });
    });

    store.handleBridgeEvent({
      method: 'ui/thread/patch',
      payload: buildThreadPatch({
        threadId,
        sequence: 30,
        source: 'item/agentMessage/delta',
        timelineItems: [{ id: 'assistant-1', kind: 'assistant', text: 'first', ts: '2026-03-08T00:00:00Z' }],
      }),
    });
    store.handleBridgeEvent({
      method: 'ui/thread/patch',
      payload: buildThreadPatch({
        threadId,
        sequence: 32,
        source: 'item/agentMessage/delta',
        timelineItems: [{ id: 'assistant-1', kind: 'assistant', text: 'second', ts: '2026-03-08T00:00:01Z' }],
      }),
    });
    await Promise.resolve();
    await Promise.resolve();
    await Promise.resolve();

    expect(methods[0]).toBe('ui/state/get');
    expect(methods).toContain('ui/state/get');
    expect(store.getThreadTimeline(threadId)[0]?.text).toBe('gap recovered');
  });


  it('survives long selected-thread patch streams without falling back to pull sync', async () => {
    const store = useThreadStore();
    const threadId = 'thread-live-pressure';
    const methods = [];
    store.state.activeThreadId = threadId;
    store.state.threads = [{ id: threadId, name: 'Live', state: 'running' }];
    apiMock.callAPI.mockImplementation(async (method) => {
      methods.push(method);
      return {};
    });

    let text = '';
    for (let i = 1; i <= 1200; i += 1) {
      text += 'x';
      store.handleBridgeEvent({
        method: 'ui/thread/patch',
        payload: buildThreadPatch({
          threadId,
          sequence: i,
          source: 'item/agentMessage/delta',
          timelineItems: [{ id: 'assistant-1', kind: 'assistant', text, ts: '2026-03-08T00:00:00Z' }],
        }),
      });
    }

    expect(store.getThreadTimeline(threadId)).toHaveLength(1);
    expect(store.getThreadTimeline(threadId)[0]?.text.length).toBe(1200);
    expect(methods).toEqual([]);
  });

  it('ignores stale thread patch sequences', () => {
    const store = useThreadStore();
    const threadId = 'thread-live-reasoning';
    store.state.activeThreadId = threadId;
    store.state.threads = [{ id: threadId, name: 'Live', state: 'running' }];

    store.handleBridgeEvent({
      method: 'ui/thread/patch',
      payload: buildThreadPatch({
        threadId,
        sequence: 20,
        source: 'item/reasoning/summaryTextDelta',
        timelineItems: [{ id: 'thinking-1', kind: 'thinking', text: '最新推理', ts: '2026-03-08T00:00:00Z' }],
      }),
    });
    store.handleBridgeEvent({
      method: 'ui/thread/patch',
      payload: buildThreadPatch({
        threadId,
        sequence: 19,
        source: 'item/reasoning/summaryTextDelta',
        timelineItems: [{ id: 'thinking-1', kind: 'thinking', text: '过期推理', ts: '2026-03-08T00:00:00Z' }],
      }),
    });

    expect(store.getThreadTimeline(threadId)[0]?.text).toBe('最新推理');
  });

  it('recovers when thread patch requests refresh for oversized payloads', async () => {
    const store = useThreadStore();
    const threadId = 'thread-live-refresh-required';
    const methods = [];
    store.state.activeThreadId = threadId;
    store.state.threads = [{ id: threadId, name: 'Live', state: 'running' }];
    apiMock.callAPI.mockImplementation(async (method) => {
      methods.push(method);
      if (method !== 'ui/state/get') return {};
      return buildSnapshot({
        threadId,
        status: 'running',
        statusHeader: 'Recovered from refresh-required patch',
        timelineItems: [{ id: 'assistant-1', kind: 'assistant', text: 'recovered after refresh', ts: '2026-03-16T00:00:00Z' }],
      });
    });

    store.handleBridgeEvent({
      method: 'ui/thread/patch',
      payload: {
        threadId,
        source: 'item/agentMessage/delta',
        sequence: 40,
        status: 'running',
        statusHeader: 'Streaming',
        recover: true,
        refreshRequired: true,
        fallbackReason: 'payload_too_large',
      },
    });
    await Promise.resolve();
    await Promise.resolve();
    await Promise.resolve();

    expect(methods).toContain('ui/state/get');
    expect(store.getThreadTimeline(threadId)[0]?.text).toBe('recovered after refresh');
  });

  it('consumes overlay and runtime metadata fields from thread patches', () => {
    const store = useThreadStore();
    const threadId = 'thread-live-metadata';
    const activeChatThreadId = 'thread-chat-selected';
    store.state.activeCmdThreadId = threadId;

    store.handleBridgeEvent({
      method: 'ui/thread/patch',
      payload: {
        ...buildThreadPatch({
          threadId,
          sequence: 41,
          source: 'thread/started',
          status: 'running',
          statusHeader: 'MCP 启动中',
        }),
        overlayText: '等待终端输入',
        overlayType: 'info',
        overlayPriority: 7,
        activeThreadId: activeChatThreadId,
        activeCmdThreadId: threadId,
        mainAgentId: 'agent-main',
        mainAgentState: 'running',
        partial: true,
      },
    });

    expect(store.state.overlayTextByThread[threadId]).toBe('等待终端输入');
    expect(store.state.overlayTypeByThread[threadId]).toBe('info');
    expect(store.state.overlayPriorityByThread[threadId]).toBe(7);
    expect(store.state.activeThreadId).toBe(activeChatThreadId);
    expect(store.state.activeCmdThreadId).toBe(threadId);
    expect(store.state.mainAgentId).toBe('agent-main');
    expect(store.state.mainAgentState).toBe('running');
    expect(store.state.partial).toBe(true);
  });
});
