// @ts-nocheck
import { beforeEach, describe, expect, it, vi } from 'vitest';

const apiMock = vi.hoisted(() => ({
  callAPI: vi.fn(),
}));

const runtimeMock = vi.hoisted(() => ({
  handlers: new Map(),
  onCalls: [],
  offCalls: [],
}));

vi.mock('/wails/runtime.js', () => ({
  Call: { ByID: vi.fn() },
  Events: {
    On: vi.fn((eventName, handler) => {
      runtimeMock.onCalls.push(eventName);
      runtimeMock.handlers.set(eventName, handler);
      return () => runtimeMock.handlers.delete(eventName);
    }),
    Off: vi.fn((eventName) => {
      runtimeMock.offCalls.push(eventName);
      runtimeMock.handlers.delete(eventName);
    }),
  },
}), { virtual: true });

vi.mock('./services/api.js', async (importOriginal) => {
  const actual = await importOriginal();
  return {
    ...actual,
    callAPI: apiMock.callAPI,
  };
});

vi.mock('./services/log.js', () => ({
  logDebug: vi.fn(),
  logInfo: vi.fn(),
  logWarn: vi.fn(),
  logError: vi.fn(),
  registerLogBridgeSink: vi.fn(),
}));

import { onBridgeEvent } from './services/api.js';
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

function buildThreadPatch({ threadId = 'thread-live', sequence = 1, source = 'item/agentMessage/delta', text = 'hello' } = {}) {
  return {
    threadId,
    source,
    sequence,
    thread: { id: threadId, name: threadId, state: 'running' },
    status: 'running',
    interruptible: true,
    statusHeader: 'AI 正在输出',
    statusDetails: '',
    timelineItems: [{ id: 'assistant-1', kind: 'assistant', text, ts: '2026-03-08T00:00:00Z' }],
    timelineOrder: ['assistant-1'],
  };
}

describe('thread patch desktop bridge integration', () => {
  beforeEach(() => {
    apiMock.callAPI.mockReset();
    runtimeMock.handlers.clear();
    runtimeMock.onCalls.length = 0;
    runtimeMock.offCalls.length = 0;
    const store = useThreadStore();
    resetThreadStore(store);
  });

  it('routes Wails bridge patch events into the selected thread without pull fallback', async () => {
    const store = useThreadStore();
    const threadId = 'thread-live-bridge';
    store.state.activeThreadId = threadId;
    store.state.threads = [{ id: threadId, name: 'Live', state: 'running' }];
    store.markHistoryLoaded(threadId);
    apiMock.callAPI.mockImplementation(async () => ({}));

    const unsubscribe = onBridgeEvent((evt) => {
      store.handleBridgeEvent(evt);
    });
    await Promise.resolve();
    await Promise.resolve();
    await new Promise((resolve) => setTimeout(resolve, 0));

    const bridgeHandler = runtimeMock.handlers.get('bridge-event');
    expect(typeof bridgeHandler).toBe('function');

    bridgeHandler({
      name: 'bridge-event',
      data: {
        type: 'ui/thread/patch',
        payload: buildThreadPatch({ threadId, sequence: 41, source: 'item/agentMessage/delta', text: 'bridge patch' }),
      },
    });
    bridgeHandler({
      name: 'bridge-event',
      data: {
        type: 'item/agentMessage/delta',
        payload: { threadId, delta: 'bridge patch' },
      },
    });
    bridgeHandler({
      name: 'bridge-event',
      data: {
        type: 'ui/thread/changed',
        payload: { threadId, source: 'item/agentMessage/delta' },
      },
    });
    await Promise.resolve();
    await Promise.resolve();

    expect(store.getThreadTimeline(threadId)[0]?.text).toBe('bridge patch');
    expect(apiMock.callAPI).not.toHaveBeenCalled();
    expect(runtimeMock.onCalls).toContain('bridge-event');

    unsubscribe();
  });
});
