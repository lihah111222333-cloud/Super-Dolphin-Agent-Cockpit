import { describe, expect, it } from 'vitest';
import { bridgePatchData, bridgePatchState } from './bridgePatchState.js';

const normalizeThread = (raw) => ({
  id: raw.threadId,
  agentId: raw.agentId || '',
  providerThreadId: raw.providerThreadId || '',
  name: raw.name || '新对话',
  provider: raw.provider || '',
  status: raw.status || '',
});

const threadMatchesIdentifier = (thread, id) => thread.id === id || thread.agentId === id || thread.providerThreadId === id;
const mergeRuntimeResultEntries = (existing, incoming) => [...incoming, ...existing];
const runtimeResultEntriesFromTimelineItems = (items, threadId) => Array.isArray(items) ? items.map((item) => ({ id: item.id, threadId })) : [];
const baseState = {
  threads: [],
  activityThreadAtById: {},
  timelinesByThread: {},
  threadTimelineReadyByThread: {},
  tokenUsageByThread: {},
  activityStatsByThread: {},
  diffTextByThread: {},
  threadDiffReadyByThread: {},
  runtimeResultEntries: [],
  activeTurnByThread: {},
  statuses: {},
  activityEntries: [],
  lastArchivedStatesByThread: {},
  threadArchiveLoadingByThread: {},
};

describe('bridgePatchState', () => {
  it('builds patch data from runtime/thread payload fields', () => {
    const patch = bridgePatchData('ui/thread/patch', {
      status: 'running',
      statusDetails: 'Working',
      thread: { name: 'Worker' },
      agentRuntime: {
        agentId: 'agent-1',
        providerThreadId: 'provider-1',
        provider: 'codex',
      },
      timelineItems: [{ id: 'tool-1', kind: 'tool', status: 'completed', output: 'done' }],
    }, 'thread-1', { normalizeThread, runtimeResultEntriesFromTimelineItems });

    expect(patch).toEqual(expect.objectContaining({
      method: 'ui/thread/patch',
      threadId: 'thread-1',
      patchProvider: 'codex',
      statusText: 'running',
      patchedThread: expect.objectContaining({
        id: 'thread-1',
        agentId: 'agent-1',
        providerThreadId: 'provider-1',
        name: 'Worker',
      }),
      runtimeResultEntries: [{ id: 'tool-1', threadId: 'thread-1' }],
    }));
  });

  it('merges visible timeline items, metrics, statuses, and runtime results', () => {
    const patch = bridgePatchData('ui/thread/patch', {
      status: 'running',
      thread: { name: 'Worker' },
      tokenUsage: { usedPercent: 42 },
      activityStats: { count: 2 },
      diffText: 'diff',
      timelineItems: [{ id: 'assistant-1', role: 'assistant', text: 'reply', done: true }],
    }, 'thread-1', { normalizeThread, runtimeResultEntriesFromTimelineItems });
    const next = bridgePatchState({
      ...baseState,
      runtimeResultEntries: [{ id: 'old' }],
    }, { ...patch, promoteForActivity: true }, {
      mergeRuntimeResultEntries,
      threadActivityTimestamp: () => 123,
      threadMatchesIdentifier,
      nowISO: () => '2026-06-15T00:00:00.000Z',
      nowMillis: () => 456,
    });

    expect(next.threads[0]).toEqual(expect.objectContaining({ id: 'thread-1', name: 'Worker', status: 'running' }));
    expect(next.activityThreadAtById).toEqual({ 'thread-1': 123 });
    expect(next.threadTimelineReadyByThread).toEqual({ 'thread-1': true });
    expect(next.timelinesByThread['thread-1'][0]).toEqual(expect.objectContaining({ id: 'assistant-1', role: 'assistant', text: 'reply' }));
    expect(next.tokenUsageByThread).toEqual({ 'thread-1': { usedTokens: 0, contextWindowTokens: 0, usedPercent: 42 } });
    expect(next.activityStatsByThread).toEqual({ 'thread-1': { lspCalls: 0, fileEdits: 0, commands: 0, toolCalls: {} } });
    expect(next.diffTextByThread).toEqual({ 'thread-1': 'diff' });
    expect(next.threadDiffReadyByThread).toEqual({ 'thread-1': true });
    expect(next.runtimeResultEntries).toEqual([{ id: 'assistant-1', threadId: 'thread-1' }, { id: 'old' }]);
    expect(next.statuses['thread-1']).toEqual(expect.objectContaining({ status: 'running', activityStats: { lspCalls: 0, fileEdits: 0, commands: 0, toolCalls: {} } }));
    expect(next.activityEntries[0]).toEqual({
      id: 'ui/thread/patch-456',
      method: 'ui/thread/patch',
      threadId: 'thread-1',
      timestamp: '2026-06-15T00:00:00.000Z',
    });
  });

  it('syncs patched running status into cached sidebar project threads', () => {
    const patch = bridgePatchData('ui/thread/patch', {
      status: 'running',
      thread: { name: 'Main agent' },
    }, 'thread-main', { normalizeThread });
    const next = bridgePatchState({
      ...baseState,
      threads: [{ id: 'thread-main', name: 'Main agent', provider: 'codex', status: 'idle', cwd: '/repo/app' }],
      sidebarThreadsByProject: {
        '/repo/app': [
          { id: 'thread-main', name: 'Main agent', provider: 'codex', status: 'idle', cwd: '/repo/app' },
          { id: 'thread-other', name: 'Other agent', provider: 'codex', status: 'idle', cwd: '/repo/app' },
        ],
      },
    }, patch, { threadMatchesIdentifier });

    expect(next.threads[0]).toEqual(expect.objectContaining({ id: 'thread-main', status: 'running' }));
    expect(next.sidebarThreadsByProject['/repo/app']).toEqual([
      expect.objectContaining({ id: 'thread-main', status: 'running' }),
      expect.objectContaining({ id: 'thread-other', status: 'idle' }),
    ]);
  });

  it('does not mark structural-only patches as timeline ready and clears completed active turns', () => {
    const patch = bridgePatchData('ui/thread/patch', {
      status: 'completed',
      thread: { name: 'Worker' },
      activeTurn: { id: 'turn-1', status: 'completed' },
      timelineItems: [{ id: 'meta-1', kind: 'metadata', text: '' }],
    }, 'thread-1', { normalizeThread });
    const next = bridgePatchState({
      ...baseState,
      activeTurnByThread: { 'thread-1': { id: 'turn-old', status: 'running' } },
      threadTimelineReadyByThread: {},
    }, patch, { threadMatchesIdentifier });

    expect(next.threadTimelineReadyByThread).toEqual(baseState.threadTimelineReadyByThread);
    expect(next.activeTurnByThread).toEqual({});
  });
});
