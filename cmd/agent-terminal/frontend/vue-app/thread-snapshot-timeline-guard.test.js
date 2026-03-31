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
});
