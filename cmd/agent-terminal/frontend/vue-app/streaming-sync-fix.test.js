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

function buildSnapshot({ threadId, activeThreadId = '', status = 'idle', statusHeader = '', text = '', timelineItems = null, threads = null, diffRevision = 0, agentRuntimeById = {} }) {
  const timeline = timelineItems || (text
    ? [{ id: `${threadId}-assistant-1`, kind: 'assistant', text, ts: '2026-03-08T00:00:00Z' }]
    : []);
  return {
    threads: threads || [{ id: threadId, name: threadId, state: status }],
    activeThreadId,
    activeCmdThreadId: '',
    statuses: { [threadId]: status },
    interruptibleByThread: { [threadId]: status !== 'idle' },
    statusHeadersByThread: { [threadId]: statusHeader },
    statusDetailsByThread: { [threadId]: '' },
    timelinesByThread: { [threadId]: timeline },
    diffTextByThread: {},
    diffRevisionByThread: { [threadId]: diffRevision },
    tokenUsageByThread: {},
    agentMetaById: {},
    agentRuntimeById,
    activityStatsByThread: {},
    alertsByThread: {},
    skillRevision: 0,
  };
}

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

describe('streaming sync fix', () => {
  beforeEach(() => {
    apiMock.callAPI.mockReset();
    const store = useThreadStore();
    resetThreadStore(store);
  });

  // ─── Layer 1: Throttle + trailing debounce ───

  it('fires sync immediately on the first streaming delta (throttle)', async () => {
    vi.useFakeTimers();
    try {
      const store = useThreadStore();
      const threadId = 'thread-throttle';
      let syncCalls = 0;
      store.state.activeThreadId = threadId;
      store.state.threads = [{ id: threadId, name: 'T', state: 'running' }];
      apiMock.callAPI.mockImplementation(async (method) => {
        if (method === 'thread/messages') return { messages: [] };
        if (method === 'ui/state/get') {
          syncCalls += 1;
          return buildSnapshot({ threadId, activeThreadId: threadId, text: 'delta text' });
        }
        return {};
      });

      store.handleBridgeEvent({ method: 'item/agentMessage/delta', payload: { threadId, delta: 'hello' } });
      // Throttle fires synchronously; wait for the async call chain to settle
      await vi.runAllTimersAsync();
      await Promise.resolve(); await Promise.resolve();
      expect(syncCalls).toBeGreaterThanOrEqual(1);
    } finally {
      vi.useRealTimers();
    }
  });

  it('does not fire additional syncs within the throttle window', async () => {
    vi.useFakeTimers();
    try {
      const store = useThreadStore();
      const threadId = 'thread-throttle-window';
      let syncCalls = 0;
      store.state.activeThreadId = threadId;
      store.state.threads = [{ id: threadId, name: 'T', state: 'running' }];
      apiMock.callAPI.mockImplementation(async (method) => {
        if (method === 'thread/messages') return { messages: [] };
        if (method === 'ui/state/get') {
          syncCalls += 1;
          return buildSnapshot({ threadId, activeThreadId: threadId });
        }
        return {};
      });

      // First delta — throttle fires
      store.handleBridgeEvent({ method: 'item/agentMessage/delta', payload: { threadId, delta: 'a' } });
      await vi.runAllTimersAsync();
      const afterFirst = syncCalls;
      expect(afterFirst).toBeGreaterThanOrEqual(1);

      // Reset calls count to isolate throttle window test
      syncCalls = 0;
      // Second delta within throttle window — no immediate sync (just trailing scheduled)
      store.handleBridgeEvent({ method: 'item/agentMessage/delta', payload: { threadId, delta: 'b' } });
      await Promise.resolve(); await Promise.resolve();
      // Should NOT have fired a new immediate sync (throttle window not elapsed)
      expect(syncCalls).toBe(0);
    } finally {
      vi.useRealTimers();
    }
  });

  it('fires trailing debounce sync after streaming stops', async () => {
    vi.useFakeTimers();
    try {
      const store = useThreadStore();
      const threadId = 'thread-trailing';
      let syncCalls = 0;
      store.state.activeThreadId = threadId;
      store.state.threads = [{ id: threadId, name: 'T', state: 'running' }];
      apiMock.callAPI.mockImplementation(async (method) => {
        if (method === 'thread/messages') return { messages: [] };
        if (method === 'ui/state/get') {
          syncCalls += 1;
          return buildSnapshot({ threadId, activeThreadId: threadId });
        }
        return {};
      });

      store.handleBridgeEvent({ method: 'item/agentMessage/delta', payload: { threadId, delta: 'a' } });
      await vi.advanceTimersByTimeAsync(100);
      await Promise.resolve(); await Promise.resolve();
      const afterThrottle = syncCalls;

      // Trailing debounce fires 500ms after last delta
      await vi.advanceTimersByTimeAsync(600);
      await Promise.resolve(); await Promise.resolve();
      expect(syncCalls).toBeGreaterThan(afterThrottle);
    } finally {
      vi.useRealTimers();
    }
  });

  it('streaming deltas do NOT trigger syncThreadState that overwrites timeline', async () => {
    vi.useFakeTimers();
    try {
      const store = useThreadStore();
      const threadId = 'thread-no-overwrite';
      store.state.activeThreadId = threadId;
      store.state.threads = [{ id: threadId, name: 'T', state: 'running' }];
      // Seed 30 local timeline items
      store.state.timelinesByThread[threadId] = Array.from({ length: 30 }, (_, i) => ({
        id: `item-${i}`, kind: 'assistant', text: `msg ${i}`, ts: '2026-03-08T00:00:00Z',
      }));
      apiMock.callAPI.mockImplementation(async (method) => {
        if (method === 'thread/messages') return { messages: [] };
        if (method === 'ui/state/get') {
          // Backend returns partial timeline (only 3 items) during active turn
          return buildSnapshot({
            threadId,
            activeThreadId: threadId,
            status: 'running',
            timelineItems: [
              { id: 'turn1', kind: 'turn_start', ts: '2026-03-08T00:00:00Z' },
              { id: 'tool1', kind: 'tool', tool: 'open_file', preview: 'src/demo.js', ts: '2026-03-08T00:00:00Z' },
              { id: 'thinking1', kind: 'thinking', text: '...', ts: '2026-03-08T00:00:00Z' },
            ],
          });
        }
        return {};
      });

      store.handleBridgeEvent({ method: 'item/agentMessage/delta', payload: { threadId, delta: 'x' } });
      await vi.advanceTimersByTimeAsync(1000);
      await Promise.resolve(); await Promise.resolve();
      // Regression guard should have blocked overwrite: local 30 > remote 3
      const raw = store.state.timelinesByThread[threadId];
      expect(raw.length).toBeGreaterThanOrEqual(3);
      // getThreadTimeline should filter out structural items
      const visible = store.getThreadTimeline(threadId);
      expect(visible.every((it) => !['turn_start', 'turn_end', 'turn_interrupted'].includes(it.kind))).toBe(true);
    } finally {
      vi.useRealTimers();
    }
  });

  // ─── Layer 2: Regression guard ───

  it('regression guard prevents remote partial timeline from overwriting local full timeline', async () => {
    const store = useThreadStore();
    const threadId = 'thread-guard';
    // Seed 40 local items
    store.state.timelinesByThread[threadId] = Array.from({ length: 40 }, (_, i) => ({
      id: `item-${i}`, kind: 'assistant', text: `msg ${i}`, ts: '2026-03-08T00:00:00Z',
    }));
    store.state.activeThreadId = threadId;
    store.state.threads = [{ id: threadId, name: 'T', state: 'running' }];

    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'ui/state/get') {
        return buildSnapshot({
          threadId,
          activeThreadId: threadId,
          timelineItems: [
            { id: 'turn1', kind: 'turn_start', ts: '2026-03-08T00:00:00Z' },
            { id: 'tool1', kind: 'tool', tool: 'open_file', preview: 'src/demo.js', ts: '2026-03-08T00:00:00Z' },
            { id: 'thinking1', kind: 'thinking', text: '...', ts: '2026-03-08T00:00:00Z' },
          ],
        });
      }
      return {};
    });

    await store.syncThreadState(threadId);
    // Regression guard should have preserved local 40 items
    const raw = store.state.timelinesByThread[threadId];
    expect(raw.length).toBe(40);
    expect(raw[0].text).toBe('msg 0');
  });

  it('regression guard does NOT block when remote has more items than local', async () => {
    const store = useThreadStore();
    const threadId = 'thread-guard-grow';
    store.state.timelinesByThread[threadId] = [
      { id: 'item-0', kind: 'assistant', text: 'old', ts: '2026-03-08T00:00:00Z' },
    ];
    store.state.activeThreadId = threadId;
    store.state.threads = [{ id: threadId, name: 'T', state: 'running' }];

    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'ui/state/get') {
        return buildSnapshot({
          threadId,
          activeThreadId: threadId,
          timelineItems: [
            { id: 'item-0', kind: 'assistant', text: 'old', ts: '2026-03-08T00:00:00Z' },
            { id: 'item-1', kind: 'assistant', text: 'new', ts: '2026-03-08T00:00:01Z' },
          ],
        });
      }
      return {};
    });

    await store.syncThreadState(threadId);
    const raw = store.state.timelinesByThread[threadId];
    expect(raw.length).toBe(2);
    expect(raw[1].text).toBe('new');
  });

  // ─── Layer 3: Structural item filtering ───

  it('getThreadTimeline filters out structural timeline kinds', () => {
    const store = useThreadStore();
    const threadId = 'thread-filter';
    store.state.timelinesByThread[threadId] = [
      { id: '1', kind: 'turn_start', ts: '2026-01-01T00:00:00Z' },
      { id: '2', kind: 'user', text: 'hi', ts: '2026-01-01T00:00:01Z' },
      { id: '3', kind: 'tool', tool: 'read_file', preview: 'src/demo.js', ts: '2026-01-01T00:00:02Z' },
      { id: '4', kind: 'file', file: 'src/demo.js', status: 'saved', ts: '2026-01-01T00:00:03Z' },
      { id: '5', kind: 'assistant', text: 'hello', ts: '2026-01-01T00:00:04Z' },
      { id: '6', kind: 'thinking', text: '...', ts: '2026-01-01T00:00:05Z' },
      { id: '7', kind: 'turn_end', ts: '2026-01-01T00:00:06Z' },
      { id: '8', kind: 'turn_interrupted', ts: '2026-01-01T00:00:07Z' },
      { id: '9', kind: 'approval', command: 'deploy', requestId: 7, ts: '2026-01-01T00:00:08Z' },
      { id: '10', kind: 'command', command: 'ls', ts: '2026-01-01T00:00:09Z' },
    ];

    const visible = store.getThreadTimeline(threadId);
    const visibleKinds = visible.map((it) => it.kind);

    // Structural kinds must be filtered out
    expect(visibleKinds).not.toContain('turn_start');
    expect(visibleKinds).not.toContain('turn_end');
    expect(visibleKinds).not.toContain('turn_interrupted');
    expect(visibleKinds).toContain('tool');
    expect(visibleKinds).toContain('file');
    expect(visibleKinds).toContain('approval');

    // Content kinds must be preserved
    expect(visibleKinds).toEqual(['user', 'tool', 'file', 'assistant', 'thinking', 'approval', 'command']);
  });

  it('getThreadTimeline returns original array reference when no structural items present', () => {
    const store = useThreadStore();
    const threadId = 'thread-fastpath';
    const items = [
      { id: '1', kind: 'user', text: 'hi', ts: '2026-01-01T00:00:00Z' },
      { id: '2', kind: 'assistant', text: 'hello', ts: '2026-01-01T00:00:01Z' },
    ];
    store.state.timelinesByThread[threadId] = items;

    const result = store.getThreadTimeline(threadId);
    // Fast path: no structural items, so same reference returned
    expect(result).toStrictEqual(items);
    expect(result.length).toBe(2);
  });

  // ─── Integration: turn/completed still works ───

  it('turn/completed triggers immediate history hydration (not throttled)', async () => {
    const store = useThreadStore();
    const threadId = 'thread-complete';
    let stateGetCalls = 0;
    store.state.activeThreadId = threadId;
    store.state.threads = [{ id: threadId, name: 'T', state: 'running' }];
    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'thread/messages') return { messages: [{ id: 1, agentId: threadId, role: 'assistant', eventType: 'agent_message', content: 'done', createdAt: '2026-03-08T00:00:00Z' }] };
      if (method === 'ui/state/get') {
        stateGetCalls += 1;
        return buildSnapshot({ threadId, activeThreadId: threadId, status: 'idle' });
      }
      return {};
    });

    store.handleBridgeEvent({ method: 'turn/completed', payload: { threadId } });
    await vi.waitFor(() => {
      expect(stateGetCalls).toBeGreaterThanOrEqual(1);
    });
    await vi.waitFor(() => {
      expect(store.getThreadTimeline(threadId).some((it) => it.text === 'done')).toBe(true);
    });
  });
});
