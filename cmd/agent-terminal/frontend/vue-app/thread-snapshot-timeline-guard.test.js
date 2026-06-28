// @ts-nocheck
import { describe, expect, it, vi } from 'vitest';

vi.mock('./services/log.js', () => ({
  logDebug: vi.fn(),
  logInfo: vi.fn(),
  logWarn: vi.fn(),
}));

import { applyRuntimeSnapshot } from './stores/thread-snapshot.js';

describe('applyRuntimeSnapshot timeline guard', () => {
  it('does not overwrite non-empty local timeline with empty remote timeline', () => {
    const localTimeline = Object.freeze([
      Object.freeze({ id: 'turn:t1', kind: 'turn_start', status: 'running', turn_id: 't1' }),
      Object.freeze({ id: 'turn-end:t1', kind: 'turn_end', status: 'completed', turn_id: 't1' }),
    ]);
    const state = {
      threads: [{ id: 'thread-1', name: 'thread-1', state: 'idle' }],
      statuses: { 'thread-1': 'idle' },
      statusHeadersByThread: { 'thread-1': '' },
      statusDetailsByThread: { 'thread-1': '' },
      interruptibleByThread: {},
      timelinesByThread: { 'thread-1': localTimeline },
      diffTextByThread: {},
      diffRevisionByThread: {},
      tokenUsageByThread: {},
      agentMetaById: {},
      agentRuntimeById: {},
      activityStatsByThread: {},
      alertsByThread: {},
      activeThreadId: 'thread-1',
      activeCmdThreadId: '',
    };

    // Remote snapshot returns empty timeline for thread-1 (turn just started, no data yet).
    const remoteSnapshot = {
      threads: [{ id: 'thread-1', name: 'thread-1', state: 'idle' }],
      statuses: { 'thread-1': 'idle' },
      statusHeadersByThread: { 'thread-1': '' },
      statusDetailsByThread: { 'thread-1': '' },
      interruptibleByThread: {},
      timelinesByThread: { 'thread-1': [] },
      diffTextByThread: {},
      diffRevisionByThread: {},
      tokenUsageByThread: {},
      agentMetaById: {},
      agentRuntimeById: {},
      activityStatsByThread: {},
      alertsByThread: {},
      activeThreadId: 'thread-1',
      activeCmdThreadId: '',
    };

    applyRuntimeSnapshot(state, remoteSnapshot, {
      requestedThreadId: 'thread-1',
      allowActiveSelectionPatch: false,
      loadedRevisionByThread: new Map(),
    });

    // Local timeline must NOT be cleared.
    expect(state.timelinesByThread['thread-1']).toHaveLength(2);
    expect(state.timelinesByThread['thread-1'][0].id).toBe('turn:t1');
  });

  it('hydrates overlay maps and main agent metadata from snapshot state', () => {
    const state = {
      threads: [],
      statuses: {},
      statusHeadersByThread: {},
      statusDetailsByThread: {},
      overlayTextByThread: {},
      overlayTypeByThread: {},
      overlayPriorityByThread: {},
      interruptibleByThread: {},
      timelinesByThread: {},
      diffTextByThread: {},
      diffRevisionByThread: {},
      tokenUsageByThread: {},
      agentMetaById: {},
      agentRuntimeById: {},
      mainAgentId: '',
      mainAgentState: 'running',
      partial: true,
      activityStatsByThread: {},
      alertsByThread: {},
      activeThreadId: '',
      activeCmdThreadId: '',
      pinnedThreadAtById: {},
      archivedThreadAtById: {},
      viewPrefsChat: null,
      viewPrefsCmd: null,
    };

    const remoteSnapshot = {
      threads: [{
        id: 'thread-1',
        name: 'thread-1',
        state: 'running',
        overlayText: '等待终端输入',
        overlayType: 'info',
        overlayPriority: 7,
      }],
      statuses: { 'thread-1': 'running' },
      statusHeadersByThread: { 'thread-1': 'MCP 启动中' },
      statusDetailsByThread: { 'thread-1': '' },
      interruptibleByThread: { 'thread-1': true },
      timelinesByThread: {},
      diffTextByThread: {},
      diffRevisionByThread: { 'thread-1': 0 },
      tokenUsageByThread: {},
      agentMetaById: {},
      agentRuntimeById: {},
      activityStatsByThread: {},
      alertsByThread: {},
      mainAgentId: 'agent-main',
      activeThreadId: 'thread-1',
      activeCmdThreadId: '',
    };

    applyRuntimeSnapshot(state, remoteSnapshot, {
      requestedThreadId: 'thread-1',
      allowActiveSelectionPatch: true,
      loadedRevisionByThread: new Map(),
    });

    expect(state.overlayTextByThread['thread-1']).toBe('等待终端输入');
    expect(state.overlayTypeByThread['thread-1']).toBe('info');
    expect(state.overlayPriorityByThread['thread-1']).toBe(7);
    expect(state.mainAgentId).toBe('agent-main');
    expect(state.mainAgentState).toBe('');
    expect(state.partial).toBe(false);
  });

});
