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

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function buildSnapshot({
  threadId = 'thread-live',
  threadName = threadId,
  status = 'idle',
  statusHeader = '等待指示',
  text = '',
  diffText = '',
  diffRevision = 0,
  activeThreadId = threadId,
  threads,
  timelineItems,
  agentMetaById,
  agentRuntimeById,
} = {}) {
  const threadList = Array.isArray(threads) && threads.length > 0
    ? threads
    : [{ id: threadId, name: threadName, state: status }];
  const resolvedTimelineItems = Array.isArray(timelineItems)
    ? timelineItems
    : (text
      ? [{ id: `${threadId}-assistant-1`, kind: 'assistant', text, ts: '2026-03-08T00:00:00Z' }]
      : []);
  return {
    threads: threadList,
    statuses: { [threadId]: status },
    interruptibleByThread: { [threadId]: status !== 'idle' },
    statusHeadersByThread: { [threadId]: statusHeader },
    statusDetailsByThread: { [threadId]: '' },
    timelinesByThread: { [threadId]: resolvedTimelineItems },
    diffTextByThread: diffText || diffRevision ? { [threadId]: diffText } : {},
    diffRevisionByThread: { [threadId]: diffRevision },
    tokenUsageByThread: {},
    agentMetaById: agentMetaById || {},
    agentRuntimeById: agentRuntimeById || {},
    activityStatsByThread: {},
    alertsByThread: {},
    skillRevision: 0,
    activeThreadId,
    activeCmdThreadId: '',

  };
}

function buildSidebarSnapshot({
  threadId = 'thread-live',
  threadName = threadId,
  status = 'idle',
  statusHeader = '等待指示',
  activeThreadId = threadId,
  threads,
  agentRuntimeById,
} = {}) {
  const threadList = Array.isArray(threads) && threads.length > 0
    ? threads
    : [{ id: threadId, name: threadName, state: status }];
  return {
    threads: threadList,
    statuses: { [threadId]: status },
    interruptibleByThread: { [threadId]: status !== 'idle' },
    statusHeadersByThread: { [threadId]: statusHeader },
    statusDetailsByThread: { [threadId]: '' },
    timelinesByThread: {},
    diffTextByThread: {},
    diffRevisionByThread: { [threadId]: 0 },
    tokenUsageByThread: {},
    agentMetaById: {},
    agentRuntimeById: agentRuntimeById || {},
    activityStatsByThread: {},
    alertsByThread: {},
    skillRevision: 0,
    activeThreadId,
    activeCmdThreadId: '',

  };
}


function resetThreadStore(store) {
  store.setPreferenceScopeCwd('');
  Object.assign(store.state, {
    activeThreadId: '',
    activeCmdThreadId: '', sendBlockedNoticesByThread: {}, sendHoldNoticesByThread: {},

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

describe('thread store runtime sync', () => {
  beforeEach(() => {
    apiMock.callAPI.mockReset();
    const store = useThreadStore();
    resetThreadStore(store);
  });

  it('keeps newer scoped thread snapshot when an older sidebar refresh snapshot resolves later', async () => {
    const store = useThreadStore();
    store.state.activeThreadId = 'thread-live';

    const refreshSyncStarted = deferred();
    const staleRefreshSnapshot = deferred();
    const freshScopedSnapshot = deferred();

    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'ui/sidebar/get') {
        refreshSyncStarted.resolve();
        return staleRefreshSnapshot.promise;
      }
      if (method === 'ui/state/get') {
        return freshScopedSnapshot.promise;
      }
      return {};
    });

    const refreshPromise = store.refreshSidebarState();
    await refreshSyncStarted.promise;

    const scopedPromise = store.syncThreadState('thread-live');
    freshScopedSnapshot.resolve(buildSnapshot({
      threadId: 'thread-live',
      status: 'running',
      statusHeader: 'AI 正在输出',
      text: '最新输出',
      activeThreadId: 'thread-live',
    }));
    await scopedPromise;

    staleRefreshSnapshot.resolve(buildSidebarSnapshot({
      threadId: 'thread-live',
      status: 'idle',
      statusHeader: '等待指示',
      activeThreadId: 'thread-live',
    }));
    await refreshPromise;

    expect(store.getThreadStatus('thread-live')).toBe('running');
    expect(store.getThreadStatusHeader('thread-live')).toBe('AI 正在输出');
    expect(store.getThreadTimeline('thread-live')).toEqual([
      { id: 'thread-live-assistant-1', kind: 'assistant', text: '最新输出', ts: '2026-03-08T00:00:00Z' },
    ]);
  });


  it('does not let an explicit thread sync rewrite the current active selection', async () => {
    const store = useThreadStore();
    store.state.activeThreadId = 'thread-current';
    store.state.threads = [
      { id: 'thread-current', name: 'Current', state: 'running' },
      { id: 'thread-other', name: 'Other', state: 'idle' },
    ];

    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'ui/state/get') {
        return buildSnapshot({
          threadId: 'thread-other',
          status: 'running',
          statusHeader: '旧线程同步完成',
          text: '旧线程输出',
          activeThreadId: 'thread-other',
          threads: [
            { id: 'thread-current', name: 'Current', state: 'running' },
            { id: 'thread-other', name: 'Other', state: 'running' },
          ],
        });
      }
      return {};
    });

    await store.syncThreadState('thread-other');

    expect(store.state.activeThreadId).toBe('thread-current');
    expect(store.getThreadStatus('thread-other')).toBe('running');
    expect(store.getThreadStatusHeader('thread-other')).toBe('旧线程同步完成');
    expect(store.getThreadTimeline('thread-other')).toEqual([
      { id: 'thread-other-assistant-1', kind: 'assistant', text: '旧线程输出', ts: '2026-03-08T00:00:00Z' },
    ]);
  });

  it('applies current-thread chat history immediately before scoped sync resolves', async () => {
    const store = useThreadStore();
    const threadId = 'thread-live-immediate';
    store.state.activeThreadId = threadId;
    const syncDeferred = deferred();
    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'thread/messages') return { messages: [{ id: 1, agentId: threadId, role: 'assistant', eventType: 'agent_message', method: '', content: '即时历史', createdAt: '2026-03-08T00:00:00Z' }] };
      if (method === 'ui/state/get') return syncDeferred.promise;
      return {};
    });
    const pending = store.loadMessages(threadId);
    await Promise.resolve();
    await Promise.resolve();
    expect(store.getThreadTimeline(threadId)[0]?.text).toBe('即时历史');
    syncDeferred.resolve(buildSnapshot({ threadId, activeThreadId: threadId, text: '即时历史' }));
    await pending;
  });

  it('does not mark an in-progress thread with freshly hydrated history as stale', async () => {
    const store = useThreadStore();
    const threadId = 'thread-live-hydrated';
    const providerThreadId = 'provider-thread-live-hydrated';

    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'thread/messages') {
        return { messages: [] };
      }
      if (method === 'ui/state/get') {
        return buildSnapshot({
          threadId,
          status: 'running',
          statusHeader: 'AI 正在工作',
          activeThreadId: threadId,
          timelineItems: [
            { id: `${threadId}-user-1`, kind: 'user', text: '最新提示词', ts: '2026-03-08T00:00:00Z' },
            { id: `${threadId}-thinking-1`, kind: 'thinking', text: '', done: false, ts: '2026-03-08T00:00:01Z' },
          ],
          agentRuntimeById: {
            [threadId]: {
              providerThreadId,
            },
          },
        });
      }
      return {};
    });

    await store.loadMessages(threadId);

    expect(store.shouldReloadThreadHistory(threadId)).toBe(false);
  });

  it('marks an active thread with runtime prompt and thinking state as stale until history hydration metadata exists', () => {
    const store = useThreadStore();
    const threadId = 'thread-live-runtime-only';

    store.state.threads = [{ id: threadId, name: threadId, state: 'running' }];
    store.state.statuses = {
      [threadId]: 'running',
    };
    store.state.statusHeadersByThread = {
      [threadId]: 'AI 正在工作',
    };
    store.state.timelinesByThread = {
      [threadId]: [
        { id: `${threadId}-user-1`, kind: 'user', text: '最新提示词', ts: '2026-03-08T00:00:00Z' },
        { id: `${threadId}-thinking-1`, kind: 'thinking', text: '', done: false, ts: '2026-03-08T00:00:01Z' },
      ],
    };
    store.state.agentRuntimeById = {
      [threadId]: {
        providerThreadId: 'provider-thread-runtime-only',
      },
    };

    expect(store.shouldReloadThreadHistory(threadId)).toBe(true);
  });
  it('reloads history for the active thread after scoped sync reveals runtime-only state', async () => {
    const store = useThreadStore();
    const threadId = 'thread-live-runtime-sync'; store.state.activeThreadId = threadId; store.state.threads = [{ id: threadId, name: 'Live', state: 'running' }];
    let stateSyncCalls = 0; const methods = [];
    apiMock.callAPI.mockImplementation(async (method) => {
      methods.push(method); if (method === 'thread/messages') return { messages: [{ id: 1, agentId: threadId, role: 'user', content: 'latest prompt', createdAt: '2026-03-08T00:00:00Z' }, { id: 2, agentId: threadId, role: 'assistant', eventType: 'agent_message', method: '', content: 'hydrated output', createdAt: '2026-03-08T00:00:02Z' }] }; if (method !== 'ui/state/get') return {};
      stateSyncCalls += 1;
      return buildSnapshot({ threadId, status: 'running', activeThreadId: threadId, timelineItems: [{ id: `${threadId}-user-1`, kind: 'user', text: 'latest prompt', ts: '2026-03-08T00:00:00Z' }, { id: `${threadId}-thinking-1`, kind: 'thinking', text: '', done: false, ts: '2026-03-08T00:00:01Z' }], agentRuntimeById: { [threadId]: { providerThreadId: 'provider-runtime' } } });
    });
    await store.syncThreadState(threadId);
    expect(methods).toEqual(['ui/state/get', 'thread/messages']);
    expect(store.getThreadTimeline(threadId).map((item) => item.kind)).toEqual(['user', 'assistant']);
    expect(store.getThreadTimeline(threadId)[1]?.text).toBe('hydrated output');
    expect(store.shouldReloadThreadHistory(threadId)).toBe(false);
  });
  it('keeps idle hydrated history fresh for 30 seconds before forcing reload', async () => {
    vi.useFakeTimers();
    try {
      vi.setSystemTime(new Date('2026-03-10T00:00:00Z'));
      const store = useThreadStore();
      const threadId = 'thread-idle-hydrated';
      apiMock.callAPI.mockImplementation(async (method) => (method === 'thread/messages'
        ? { messages: [] }
        : buildSnapshot({ threadId, activeThreadId: threadId })));
      await store.loadMessages(threadId);
      vi.setSystemTime(new Date('2026-03-10T00:00:29.999Z'));
      expect(store.shouldReloadThreadHistory(threadId)).toBe(false);
      vi.setSystemTime(new Date('2026-03-10T00:00:30.001Z'));
      expect(store.shouldReloadThreadHistory(threadId)).toBe(true);
    } finally {
      vi.useRealTimers();
    }
  });

  it('replays a pending sidebar refresh only once when overlapping refreshes join', async () => {
    const store = useThreadStore();
    store.state.activeThreadId = 'thread-live';
    const firstStarted = deferred();
    const firstSnapshot = deferred();
    const secondSnapshot = deferred();
    let sidebarRequestCount = 0;
    apiMock.callAPI.mockImplementation(async (method) => {
      if (method !== 'ui/sidebar/get') return {};
      sidebarRequestCount += 1;
      if (sidebarRequestCount === 1) { firstStarted.resolve(); return firstSnapshot.promise; }
      if (sidebarRequestCount === 2) return secondSnapshot.promise;
      return buildSidebarSnapshot({ threadId: 'thread-live', statusHeader: `unexpected-${sidebarRequestCount}`, activeThreadId: 'thread-live' });
    });
    const refreshA = store.refreshSidebarState(); await firstStarted.promise; const refreshB = store.refreshSidebarState();
    firstSnapshot.resolve(buildSidebarSnapshot({ threadId: 'thread-live', statusHeader: 'first', activeThreadId: 'thread-live' })); await Promise.resolve();
    secondSnapshot.resolve(buildSidebarSnapshot({ threadId: 'thread-live', statusHeader: 'second', activeThreadId: 'thread-live' })); await Promise.all([refreshA, refreshB]);
    expect(sidebarRequestCount).toBe(2); expect(store.getThreadStatusHeader('thread-live')).toBe('second');
  });

  it('keeps existing sidebar state when ui/sidebar/get fails', async () => {
    const store = useThreadStore();
    store.state.activeThreadId = 'thread-live';
    store.state.threads = [{ id: 'thread-live', name: 'Live', state: 'running' }];
    store.state.statuses = { 'thread-live': 'running' };
    store.state.statusHeadersByThread = { 'thread-live': 'Still here' };

    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'ui/sidebar/get') throw new Error('catalog unavailable');
      return {};
    });

    await store.refreshSidebarState();

    expect(store.state.threads).toEqual([{ id: 'thread-live', name: 'Live', state: 'running' }]);
    expect(store.getThreadStatusHeader('thread-live')).toBe('Still here');
  });



  it('routes sidebar and active-thread bridge signals to separate APIs', async () => {
    vi.useFakeTimers();
    try {
      const store = useThreadStore();
      store.state.activeThreadId = 'thread-live';
      store.state.threads = [
        { id: 'thread-live', name: 'Live', state: 'running' },
        { id: 'thread-other', name: 'Other', state: 'idle' },
      ];
      const methods = [];
      apiMock.callAPI.mockImplementation(async (method) => {
        methods.push(method);
        if (method === 'ui/sidebar/get') {
          return buildSidebarSnapshot({
            threadId: 'thread-other',
            threads: [
              { id: 'thread-live', name: 'Live', state: 'running' },
              { id: 'thread-other', name: 'Other', state: 'running' },
            ],
            status: 'running',
            statusHeader: 'Sidebar updated',
            activeThreadId: 'thread-live',
          });
        }
        if (method === 'ui/state/get') {
          return buildSnapshot({
            threadId: 'thread-live',
            status: 'running',
            statusHeader: 'Thread updated',
            text: 'latest',
            activeThreadId: 'thread-live',
          });
        }
        return {};
      });

      store.handleBridgeEvent({ method: 'ui/sidebar/changed', payload: { source: 'thread/started', threadId: 'thread-other' } });
      vi.advanceTimersByTime(150);
      await Promise.resolve();
      await Promise.resolve();
      expect(methods.filter((method) => method === 'ui/sidebar/get')).toHaveLength(1);
      expect(store.getThreadStatusHeader('thread-other')).toBe('Sidebar updated');

      store.handleBridgeEvent({ method: 'ui/thread/changed', payload: { source: 'item/completed', threadId: 'thread-other' } });
      store.handleBridgeEvent({ method: 'ui/thread/changed', payload: { source: 'item/completed', threadId: 'thread-live' } });
      vi.advanceTimersByTime(50);
      await Promise.resolve();
      await Promise.resolve();
      expect(methods.filter((method) => method === 'ui/state/get')).toHaveLength(1);
      expect(store.getThreadTimeline('thread-live')[0]?.text).toBe('latest');
    } finally {
      vi.useRealTimers();
    }
  });



  it('replays active direct delta sync after an in-flight scoped sync finishes', async () => {
    vi.useFakeTimers();
    try {
      const store = useThreadStore();
      const threadId = 'thread-live-delta';
      let uiStateCalls = 0;
      store.state.activeThreadId = threadId;
      store.state.threads = [{ id: threadId, name: 'Live', state: 'running' }];
      apiMock.callAPI.mockImplementation(async (method) => {
        if (method === 'thread/messages') return { messages: [{ id: 1, agentId: threadId, role: 'assistant', eventType: 'agent_message', content: 'live delta', createdAt: '2026-03-08T00:00:00Z' }] };
        if (method !== 'ui/state/get') return {};
        uiStateCalls += 1;
        return buildSnapshot({ threadId, activeThreadId: threadId, text: 'live delta' });
      });
      // Streaming deltas use throttle (immediate first) + trailing debounce.
      // First delta fires immediately via throttle.
      store.handleBridgeEvent({ method: 'item/agentMessage/delta', payload: { threadId, delta: 'live ' } });
      await vi.waitFor(() => { expect(uiStateCalls).toBeGreaterThanOrEqual(1); });
      const afterFirst = uiStateCalls;
      store.handleBridgeEvent({ method: 'item/agentMessage/delta', payload: { threadId, delta: 'delta' } });
      await Promise.resolve();
      // Second delta within throttle window, no additional sync yet
      expect(uiStateCalls).toBe(afterFirst);
      // Trailing debounce fires after 500ms
      vi.advanceTimersByTime(600);
      await vi.waitFor(() => { expect(uiStateCalls).toBeGreaterThan(afterFirst); });
    } finally {
      vi.useRealTimers();
    }
  });

  it('syncs the active thread on direct turn-completed bridge events', async () => {
    const store = useThreadStore();
    const threadId = 'thread-live-complete';
    let uiStateCalls = 0;
    store.state.activeThreadId = threadId;
    store.state.threads = [{ id: threadId, name: 'Live', state: 'running' }];
    apiMock.callAPI.mockImplementation(async (method) => {
      if (method !== 'ui/state/get') return {};
      uiStateCalls += 1;
      return buildSnapshot({ threadId, activeThreadId: threadId, text: 'completed snapshot' });
    });
    store.handleBridgeEvent({ method: 'turn/completed', payload: { threadId } });
    await Promise.resolve(); await Promise.resolve();
    expect(uiStateCalls).toBeGreaterThanOrEqual(1);
    expect(store.getThreadTimeline(threadId)[0]?.text).toBe('completed snapshot');
  });
  it('hydrates active completed history even when recent cache would otherwise skip ttl reload', async () => {
    const store = useThreadStore();
    const threadId = 'thread-live-complete-history';
    const userMessage = { id: 1, agentId: threadId, role: 'user', content: 'latest prompt', createdAt: '2026-03-08T00:00:00Z' };
    const assistantMessage = { id: 2, agentId: threadId, role: 'assistant', eventType: 'agent_message', method: '', content: 'done reply', createdAt: '2026-03-08T00:00:02Z' };
    store.state.activeThreadId = threadId;
    store.state.threads = [{ id: threadId, name: 'Live', state: 'running' }];
    apiMock.callAPI.mockImplementation(async (method) => (method === 'thread/messages'
      ? { messages: [userMessage] }
      : buildSnapshot({ threadId, activeThreadId: threadId, status: 'running', timelineItems: [{ id: `${threadId}-user-1`, kind: 'user', text: 'latest prompt', ts: '2026-03-08T00:00:00Z' }] })));
    await store.loadMessages(threadId);
    const methods = [];
    apiMock.callAPI.mockReset();
    apiMock.callAPI.mockImplementation(async (method) => {
      methods.push(method);
      if (method === 'thread/messages') return { messages: [userMessage, assistantMessage] };
      if (method === 'ui/state/get') return buildSnapshot({ threadId, activeThreadId: threadId, status: 'idle', timelineItems: [{ id: `${threadId}-user-1`, kind: 'user', text: 'latest prompt', ts: '2026-03-08T00:00:00Z' }] });
      return {};
    });
    store.handleBridgeEvent({ method: 'turn/completed', payload: { threadId } });
    await vi.waitFor(() => {
      expect(methods).toEqual(['ui/state/get', 'thread/messages']);
      expect(store.getThreadTimeline(threadId).map((item) => item.kind)).toEqual(['user', 'assistant']);
    });
    expect(store.getThreadTimeline(threadId)[1]?.text).toBe('done reply');
  });

  it('hydrates background completed thread history before the card is reopened', async () => {
    const store = useThreadStore();
    const activeThreadId = 'thread-live';
    const threadId = 'thread-background-complete';
    const userMessage = { id: 1, agentId: threadId, role: 'user', content: 'latest prompt', createdAt: '2026-03-08T00:00:00Z' };
    const assistantMessage = { id: 2, agentId: threadId, role: 'assistant', eventType: 'agent_message', method: '', content: 'background done', createdAt: '2026-03-08T00:00:02Z' };
    store.state.activeThreadId = activeThreadId;
    store.state.threads = [{ id: activeThreadId, name: 'Live', state: 'running' }, { id: threadId, name: 'Done', state: 'running' }];
    const methods = [];
    apiMock.callAPI.mockImplementation(async (method) => {
      methods.push(method);
      if (method === 'thread/messages') return { messages: [userMessage, assistantMessage] };
      if (method === 'ui/state/get') return buildSnapshot({ threadId, activeThreadId, status: 'idle', timelineItems: [{ id: `${threadId}-user-1`, kind: 'user', text: 'latest prompt', ts: '2026-03-08T00:00:00Z' }] });
      return {};
    });
    store.handleBridgeEvent({ method: 'turn/completed', payload: { threadId } });
    await vi.waitFor(() => {
      expect(methods).toEqual(['ui/state/get', 'thread/messages']);
      expect(store.getThreadTimeline(threadId).map((item) => item.kind)).toEqual(['user', 'assistant']);
    });
    expect(store.state.activeThreadId).toBe(activeThreadId);
    expect(store.getThreadTimeline(threadId)[1]?.text).toBe('background done');
  });

  it('syncs the selected cmd card on direct reasoning refresh signals', async () => {
    vi.useFakeTimers();
    try {
      const store = useThreadStore();
      const threadId = 'thread-live-reasoning';
      let uiStateCalls = 0;
      store.state.activeCmdThreadId = threadId;
      store.state.threads = [{ id: threadId, name: 'Live Cmd', state: 'running' }];
      apiMock.callAPI.mockImplementation(async (method) => {
        if (method !== 'ui/state/get') return {};
        uiStateCalls += 1;
        return buildSnapshot({
          threadId,
          activeThreadId: '',
          timelineItems: [{ id: 'thinking-1', kind: 'thinking', text: '实时推理', ts: '2026-03-08T00:00:00Z' }],
        });
      });
      // Streaming deltas now use debounced sync (500ms)
      store.handleBridgeEvent({ method: 'ui/thread/changed', payload: { source: 'item/reasoning/summaryTextDelta', threadId } });
      vi.advanceTimersByTime(600);
      await Promise.resolve(); await Promise.resolve();
      expect(uiStateCalls).toBeGreaterThanOrEqual(1);
      expect(store.getThreadTimeline(threadId)[0]?.text).toBe('实时推理');
    } finally {
      vi.useRealTimers();
    }
  });

  it('syncs the selected cmd card on raw command output delta bridge events', async () => {
    vi.useFakeTimers();
    try {
      const store = useThreadStore();
      const threadId = 'thread-live-command-output';
      let uiStateCalls = 0;
      store.state.activeCmdThreadId = threadId;
      store.state.threads = [{ id: threadId, name: 'Live Cmd', state: 'running' }];
      apiMock.callAPI.mockImplementation(async (method) => {
        if (method !== 'ui/state/get') return {};
        uiStateCalls += 1;
        return buildSnapshot({
          threadId,
          activeThreadId: '',
          timelineItems: [{ id: 'command-1', kind: 'command', command: 'npm test', output: 'partial output', status: 'running', ts: '2026-03-08T00:00:00Z' }],
        });
      });
      // Streaming deltas now use debounced sync (500ms)
      store.handleBridgeEvent({ method: 'item/commandExecution/outputDelta', payload: { threadId, delta: 'partial output' } });
      vi.advanceTimersByTime(600);
      await Promise.resolve(); await Promise.resolve();
      expect(uiStateCalls).toBeGreaterThanOrEqual(1);
      expect(store.getThreadTimeline(threadId)[0]?.output).toBe('partial output');
    } finally {
      vi.useRealTimers();
    }
  });



  it('syncs internal worker reports into the active main-agent timeline with aliases', async () => {
    vi.useFakeTimers();
    try {
      const store = useThreadStore();
      const threadId = 'main-agent';
      store.state.activeThreadId = threadId;
      store.state.threads = [{ id: threadId, name: '主控代理', state: 'running' }];
      store.state.threads = [{ id: threadId, name: '主控代理', state: 'running' }];
      apiMock.callAPI.mockImplementation(async (method) => {
        if (method !== 'ui/state/get') return {};
        return buildSnapshot({
          threadId,
          activeThreadId: threadId,
          threads: [
            { id: threadId, name: '主控代理', state: 'running' },
            { id: 'worker-agent', name: 'worker-agent', state: 'idle' },
          ],
          timelineItems: [{
            id: 'internal-1',
            kind: 'user',
            internal: true,
            text: '任务已完成',
            fromThreadId: 'worker-agent',
            toThreadId: threadId,
            fromDisplay: 'worker-fallback',
            toDisplay: 'main-fallback',
            ts: '2026-03-08T00:00:00Z',
          }],
          agentMetaById: {
            'worker-agent': { alias: '代码修复代理' },
            [threadId]: { alias: '主控代理' },
          },
        });
      });
      store.handleBridgeEvent({ method: 'ui/thread/changed', payload: { source: 'agent/event/user_message', threadId } });
      vi.advanceTimersByTime(250);
      await Promise.resolve();
      await Promise.resolve();
      const item = store.getThreadTimeline(threadId)[0];
      expect(item?.internal).toBe(true);
      expect(item?.fromThreadId).toBe('worker-agent');
      expect(item?.toThreadId).toBe(threadId);
      expect(store.displayName({ id: 'worker-agent', name: 'worker-agent', state: '' })).toBe('代码修复代理');
    } finally {
      vi.useRealTimers();
    }
  });



  it('reuses unchanged timeline prefix items when only the tail grows', async () => {
    const store = useThreadStore();
    apiMock.callAPI
      .mockResolvedValueOnce(buildSnapshot({ threadId: 'thread-live', activeThreadId: 'thread-live', timelineItems: [{ id: 'user-1', kind: 'user', text: 'hi', ts: '2026-03-08T00:00:00Z' }, { id: 'assistant-1', kind: 'assistant', text: 'hello', ts: '2026-03-08T00:00:01Z' }] }))
      .mockResolvedValueOnce(buildSnapshot({ threadId: 'thread-live', activeThreadId: 'thread-live', timelineItems: [{ id: 'user-1', kind: 'user', text: 'hi', ts: '2026-03-08T00:00:00Z' }, { id: 'assistant-1', kind: 'assistant', text: 'hello', ts: '2026-03-08T00:00:01Z' }, { id: 'assistant-2', kind: 'assistant', text: 'tail', ts: '2026-03-08T00:00:02Z' }] }));
    await store.syncThreadState('thread-live');
    const firstTimeline = store.getThreadTimeline('thread-live');
    await store.syncThreadState('thread-live');
    const secondTimeline = store.getThreadTimeline('thread-live');
    expect(secondTimeline).not.toBe(firstTimeline);
    expect(secondTimeline[0]).toBe(firstTimeline[0]);
    expect(secondTimeline[1]).toBe(firstTimeline[1]);
    expect(secondTimeline[2]?.text).toBe('tail');
  });

  it('preserves local chat selection when sidebar refresh still carries stale global active thread', async () => {
    const store = useThreadStore();
    const threads = [
      { id: 'thread-old', name: 'Old', state: 'idle' },
      { id: 'thread-new', name: 'New', state: 'running' },
    ];
    store.state.activeThreadId = 'thread-old';
    store.state.threads = threads;
    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'ui/preferences/set') return {};
      if (method === 'ui/state/get') {
        return buildSnapshot({
          threadId: 'thread-new',
          threads,
          status: 'running',
          statusHeader: '新线程',
          activeThreadId: 'thread-new',
        });
      }
      if (method === 'ui/sidebar/get') {
        return buildSidebarSnapshot({
          threadId: 'thread-old',
          threads,
          statusHeader: '旧线程',
          activeThreadId: '',
        });
      }
      return {};
    });

    store.saveActiveThread('thread-new');
    await store.syncThreadState('thread-new');
    await store.refreshSidebarState();

    expect(store.getCurrentThreadId('chat')).toBe('thread-new');
    expect(store.state.activeThreadId).toBe('thread-new');
  });

  it('preserves local cmd selection when sidebar refresh returns an empty active cmd thread id', async () => {
    const store = useThreadStore();
    const threads = [
      { id: 'thread-chat', name: 'Chat', state: 'running' },
      { id: 'thread-cmd-old', name: 'Cmd Old', state: 'idle' },
      { id: 'thread-cmd-new', name: 'Cmd New', state: 'running' },
    ];
    store.state.activeThreadId = 'thread-chat';
    store.state.activeCmdThreadId = 'thread-cmd-old';
    store.state.threads = threads;
    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'ui/preferences/set') return {};
      if (method === 'ui/state/get') {
        return {
          ...buildSnapshot({
            threadId: 'thread-cmd-new',
            threads,
            status: 'running',
            statusHeader: 'Cmd New',
            activeThreadId: 'thread-chat',
          }),
          activeCmdThreadId: 'thread-cmd-new',
        };
      }
      if (method === 'ui/sidebar/get') {
        return {
          ...buildSidebarSnapshot({
            threadId: 'thread-chat',
            threads,
            statusHeader: 'Chat selected',
            activeThreadId: 'thread-chat',
          }),
          activeCmdThreadId: '',
        };
      }
      return {};
    });

    store.saveActiveCmdThread('thread-cmd-new');
    await store.syncThreadState('thread-cmd-new');
    await store.refreshSidebarState();

    expect(store.getCurrentThreadId('cmd')).toBe('thread-cmd-new');
    expect(store.state.activeCmdThreadId).toBe('thread-cmd-new');
  });
  it('keeps existing thread state when ui/state/get returns an empty snapshot', async () => {
    const store = useThreadStore();
    const threadId = 'thread-empty-snapshot';
    const preservedTimeline = [
      { id: 'assistant-1', kind: 'assistant', text: 'keep me', ts: '2026-03-08T00:00:00Z' },
    ];

    store.state.activeThreadId = threadId;
    store.state.threads = [{ id: threadId, name: 'Live', state: 'running' }];
    store.state.statuses = { [threadId]: 'running' };
    store.state.statusHeadersByThread = { [threadId]: 'Working' };
    store.state.timelinesByThread = { [threadId]: preservedTimeline };

    apiMock.callAPI.mockResolvedValueOnce({});
    await store.syncThreadState(threadId);

    expect(store.getThreadStatus(threadId)).toBe('running');
    expect(store.getThreadStatusHeader(threadId)).toBe('Working');
    expect(store.getThreadTimeline(threadId)).toEqual(preservedTimeline);
  });


  it('does not block active thread sync on background diff fetch', async () => {
    const store = useThreadStore();
    const threadId = 'thread-diff-background';
    const diffDeferred = deferred();
    const calls = [];
    store.state.activeThreadId = threadId;

    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'thread/messages') {
        return { total: 1, messages: [{ id: 1, agentId: threadId, role: 'assistant', content: 'hello', createdAt: '2026-03-08T00:00:00Z' }] };
      }
      if (method === 'ui/state/get') {
        return buildSnapshot({ threadId, activeThreadId: threadId, text: 'hello', diffRevision: 0 });
      }
      return {};
    });
    await store.loadMessages(threadId);

    apiMock.callAPI.mockReset();
    apiMock.callAPI.mockImplementation(async (method, params = {}) => {
      if (method !== 'ui/state/get') return {};
      calls.push({ includeDiff: Boolean(params.includeDiff), knownDiffRevision: Number(params.knownDiffRevision || 0) });
      if (params.includeDiff) return diffDeferred.promise;
      return { ...buildSnapshot({ threadId, activeThreadId: threadId, text: 'hello', diffRevision: 4 }), diffTextByThread: {} };
    });

    await store.syncThreadState(threadId);
    await Promise.resolve();
    await Promise.resolve();

    expect(store.getThreadTimeline(threadId)[0]?.text).toBe('hello');
    expect(store.state.diffRevisionByThread[threadId]).toBe(4);
    expect(store.getThreadDiff(threadId)).toBe('');
    expect(calls).toEqual([
      { includeDiff: false, knownDiffRevision: 0 },
      { includeDiff: true, knownDiffRevision: 0 },
    ]);

    diffDeferred.resolve(buildSnapshot({ threadId, activeThreadId: threadId, diffRevision: 4, diffText: 'diff --git a/src/a.js b/src/a.js' }));
    await diffDeferred.promise;
    await Promise.resolve();
    expect(store.getThreadDiff(threadId)).toContain('diff --git a/src/a.js b/src/a.js');
  });

  it('skips full diff reload when revision stays unchanged', async () => {
    const store = useThreadStore();
    const threadId = 'thread-diff-stable';
    const calls = [];
    store.state.activeThreadId = threadId;

    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'thread/messages') {
        return { total: 1, messages: [{ id: 1, agentId: threadId, role: 'assistant', content: 'stable', createdAt: '2026-03-08T00:00:00Z' }] };
      }
      if (method === 'ui/state/get') {
        return buildSnapshot({ threadId, activeThreadId: threadId, text: 'stable', diffRevision: 0 });
      }
      return {};
    });
    await store.loadMessages(threadId);

    apiMock.callAPI.mockReset();
    apiMock.callAPI.mockImplementation(async (method, params = {}) => {
      if (method !== 'ui/state/get') return {};
      calls.push({ includeDiff: Boolean(params.includeDiff), knownDiffRevision: Number(params.knownDiffRevision || 0) });
      if (params.includeDiff) {
        return buildSnapshot({ threadId, activeThreadId: threadId, diffRevision: 5, diffText: 'diff --git a/src/stable.js b/src/stable.js' });
      }
      return { ...buildSnapshot({ threadId, activeThreadId: threadId, text: 'stable', diffRevision: 5 }), diffTextByThread: {} };
    });

    await store.syncThreadState(threadId);
    await Promise.resolve();
    await Promise.resolve();
    await store.syncThreadState(threadId);
    await Promise.resolve();
    await Promise.resolve();

    expect(calls.filter((call) => call.includeDiff)).toHaveLength(1);
    expect(store.getThreadDiff(threadId)).toContain('diff --git a/src/stable.js b/src/stable.js');
  });

  it('restores active thread diff through the page-enter history refresh path', async () => {
    const store = useThreadStore();
    const threadId = 'thread-page-enter-diff';
    const calls = [];
    store.state.activeThreadId = threadId;

    apiMock.callAPI.mockImplementation(async (method, params = {}) => {
      if (method === 'thread/messages') {
        calls.push({ method });
        return { total: 1, messages: [{ id: 1, agentId: threadId, role: 'assistant', content: 'hello', createdAt: '2026-03-08T00:00:00Z' }] };
      }
      if (method !== 'ui/state/get') return {};
      calls.push({ method, includeDiff: Boolean(params.includeDiff), knownDiffRevision: Number(params.knownDiffRevision || 0) });
      if (params.includeDiff) {
        return buildSnapshot({ threadId, activeThreadId: threadId, text: 'hello', diffRevision: 7, diffText: 'diff --git a/src/page.js b/src/page.js' });
      }
      return { ...buildSnapshot({ threadId, activeThreadId: threadId, text: 'hello', diffRevision: 7 }), diffTextByThread: {} };
    });

    await store.loadMessages(threadId);
    await Promise.resolve();
    await Promise.resolve();

    expect(store.state.diffRevisionByThread[threadId]).toBe(7);
    expect(store.getThreadDiff(threadId)).toContain('diff --git a/src/page.js b/src/page.js');
    expect(calls).toEqual([
      { method: 'thread/messages' },
      { method: 'ui/state/get', includeDiff: false, knownDiffRevision: 0 },
      { method: 'ui/state/get', includeDiff: true, knownDiffRevision: 0 },
    ]);
  });

  it('skips an older sidebar snapshot after a newer scoped sync for the same thread scope', async () => {
    const store = useThreadStore();
    store.state.activeThreadId = 'thread-live';

    const sidebarStarted = deferred();
    const staleSidebarSnapshot = deferred();
    const freshScopedSnapshot = deferred();

    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'ui/sidebar/get') {
        sidebarStarted.resolve();
        return staleSidebarSnapshot.promise;
      }
      if (method === 'ui/state/get') return freshScopedSnapshot.promise;
      return {};
    });

    const refreshPromise = store.refreshSidebarState();
    await sidebarStarted.promise;

    const scopedPromise = store.syncThreadState('thread-live');
    freshScopedSnapshot.resolve(buildSnapshot({
      threadId: 'thread-live',
      status: 'running',
      text: 'fresh output',
      activeThreadId: 'thread-live',
      agentRuntimeById: {
        'thread-live': { provider: 'claude' },
      },
    }));
    await scopedPromise;

    staleSidebarSnapshot.resolve(buildSidebarSnapshot({
      threadId: 'thread-live',
      status: 'idle',
      statusHeader: 'stale sidebar',
      activeThreadId: 'thread-live',
      agentRuntimeById: {
        'thread-live': { provider: 'codex' },
      },
    }));
    await refreshPromise;

    expect(store.getThreadStatus('thread-live')).toBe('running');
    expect(store.getThreadTimeline('thread-live')[0]?.text).toBe('fresh output');
    expect(store.state.agentRuntimeById['thread-live']).toEqual(expect.objectContaining({ provider: 'claude' }));
  });

  it('normalizes provider thread ids and strips legacy codex ids from runtime snapshots', async () => {
    const store = useThreadStore();
    const threadId = 'thread-runtime-provider';

    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'ui/state/get') {
        return buildSnapshot({
          threadId,
          activeThreadId: threadId,
          agentRuntimeById: {
            [threadId]: {
              provider: 'claude',
              provider_thread_id: 'provider-1',
              codexThreadId: 'legacy-a',
              codex_thread_id: 'legacy-b',
            },
          },
        });
      }
      return {};
    });

    await store.syncThreadState(threadId);

    expect(store.state.agentRuntimeById[threadId]).toEqual(expect.objectContaining({
      provider: 'claude',
      providerThreadId: 'provider-1',
    }));
    expect(store.state.agentRuntimeById[threadId]).not.toHaveProperty('codexThreadId');
    expect(store.state.agentRuntimeById[threadId]).not.toHaveProperty('codex_thread_id');
  });

  it('keeps cwd mismatch flags from runtime snapshots', async () => {
    const store = useThreadStore();
    const threadId = 'thread-runtime-cwd';

    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'ui/state/get') {
        return buildSnapshot({
          threadId,
          activeThreadId: threadId,
          agentRuntimeById: {
            [threadId]: {
              provider: 'codex',
              cwdMismatch: true,
              cwdMismatchReason: 'selected cwd differs from runtime cwd',
            },
          },
        });
      }
      return {};
    });

    await store.syncThreadState(threadId);

    expect(store.state.agentRuntimeById[threadId]).toEqual(expect.objectContaining({
      provider: 'codex',
      cwdMismatch: true,
      cwdMismatchReason: 'selected cwd differs from runtime cwd',
    }));
  });

});
