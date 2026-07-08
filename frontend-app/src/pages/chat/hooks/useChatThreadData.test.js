import { expect, it } from 'vitest';
import {
  scopedActivityEntries,
  threadScopedTimelineValue,
  useChatThreadData,
} from './useChatThreadData.js';

const activeThread = {
  id: 'agent-1',
  threadId: 'thread-1',
  providerThreadId: 'provider-1',
  sessionId: 'session-1',
  name: 'Current thread',
};

function createStore(overrides = {}) {
  return {
    activeThreadId: 'agent-1',
    activeTurnByThread: {},
    activityStatsByThread: {},
    diffTextByThread: {},
    runtimeResultEntries: [],
    statuses: {},
    threadMessagePaginationByThread: {},
    threadStateLoadingByThread: {},
    threadTimelineReadyByThread: {},
    threads: [activeThread],
    timelinesByThread: {},
    tokenUsageByThread: {},
    warningEntries: [],
    ...overrides,
  };
}
  it('rejects invalid thread store shapes', () => {
    expect(() => useChatThreadData(createStore({ timelinesByThread: [] }), 'agent-1')).toThrow('timelinesByThread object');
  });

  it('rejects stores missing required backend thread fields', () => {
    const store = createStore();
    delete store.threadTimelineReadyByThread;
    expect(() => useChatThreadData(store, 'agent-1')).toThrow('threadTimelineReadyByThread object');
  });

  it('returns canonical thread DTO data without empty-array fallback for valid backend data', () => {
    const data = useChatThreadData(createStore({
      activeTurnByThread: { 'agent-1': { id: 'turn-1' } },
      activityStatsByThread: { 'agent-1': { commands: 2 } },
      diffTextByThread: { 'agent-1': 'diff --git a/a b/a' },
      runtimeResultEntries: [{ id: 'runtime-1', threadId: 'thread-1' }],
      statuses: { 'agent-1': { state: 'running' } },
      threadMessagePaginationByThread: { 'agent-1': { hasMore: true } },
      threadTimelineReadyByThread: { 'agent-1': true },
      timelinesByThread: { 'agent-1': [{ id: 'message-1', role: 'assistant', text: 'ok' }] },
      tokenUsageByThread: { 'agent-1': { total: 9 } },
      warningEntries: [{ id: 'warning-1', threadId: 'agent-1' }],
    }), 'agent-1');

    expect(data.messages).toEqual([expect.objectContaining({ id: 'message-1' })]);
    expect(data.runtimeResults).toEqual([expect.objectContaining({ id: 'runtime-1' })]);
    expect(data.statusEntry).toEqual({ state: 'running' });
    expect(data.diffText).toBe('diff --git a/a b/a');
    expect(data.tokenUsage).toEqual({ total: 9 });
  });

  it('merges timeline items from all known active thread identities', () => {
    const timeline = threadScopedTimelineValue({
      'agent-1': [
        { id: 'a', role: 'assistant', text: 'first', time: '2026-06-15T08:00:00Z' },
        { id: 'duplicate', role: 'assistant', text: 'old', time: '2026-06-15T08:01:00Z' },
      ],
      'provider-1': [
        { id: 'duplicate', role: 'assistant', text: 'new', completedAt: '2026-06-15T08:02:00Z' },
        { id: 'tool-noise', kind: 'command', tool: 'shell', text: 'hidden tool command' },
        { id: 'command-rendered', kind: 'command', title: '$ npm test' },
      ],
    }, 'agent-1', activeThread);

    expect(timeline).toEqual([
      expect.objectContaining({ id: 'a', text: 'first' }),
      expect.objectContaining({ id: 'duplicate', text: 'new' }),
      expect.objectContaining({ id: 'command-rendered' }),
    ]);
    expect(timeline.map((item) => item.id)).not.toContain('tool-noise');
  });

  it('filters activity entries to the active thread identity set while preserving unscoped entries when requested', () => {
    const entries = scopedActivityEntries([
      { id: 'for-thread', threadId: 'thread-1' },
      { id: 'for-patch', fields: { _threadPatch: { agent_id: 'agent-1' } } },
      { id: 'for-other-thread', threadId: 'other' },
      { id: 'unscoped' },
    ], 'agent-1', activeThread, { includeUnscoped: true });

    expect(entries.map((entry) => entry.id)).toEqual(['for-thread', 'for-patch', 'unscoped']);
  });

  it('blocks timeline content only until a ready timeline is available', () => {
    const blockedWithoutReady = useChatThreadData(createStore({
      threadStateLoadingByThread: { 'agent-1': true },
      threadTimelineReadyByThread: { 'agent-1': false },
      timelinesByThread: {
        'agent-1': [{ id: 'pending', role: 'assistant', text: 'pending' }],
      },
    }), 'agent-1');

    expect(blockedWithoutReady.timelineBlocked).toBe(true);
    expect(blockedWithoutReady.timelineContentBlocked).toBe(true);
    expect(blockedWithoutReady.messages).toEqual([]);

    const readyWithCachedTimeline = useChatThreadData(createStore({
      threadStateLoadingByThread: { 'agent-1': true },
      threadTimelineReadyByThread: { 'agent-1': true },
      timelinesByThread: {
        'agent-1': [{ id: 'cached', role: 'assistant', text: 'cached' }],
      },
      runtimeResultEntries: [{ id: 'runtime', threadId: 'provider-1' }],
      warningEntries: [{ id: 'warning' }],
    }), 'agent-1');

    expect(readyWithCachedTimeline.timelineBlocked).toBe(true);
    expect(readyWithCachedTimeline.timelineContentBlocked).toBe(false);
    expect(readyWithCachedTimeline.messages).toEqual([
      expect.objectContaining({ id: 'cached' }),
    ]);
    expect(readyWithCachedTimeline.runtimeResults).toEqual([
      expect.objectContaining({ id: 'runtime' }),
    ]);
    expect(readyWithCachedTimeline.warnings).toEqual([
      expect.objectContaining({ id: 'warning' }),
    ]);
  });
