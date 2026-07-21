import { describe, expect, it, vi } from 'vitest';
import { attachAssistantEventRuntime } from './assistantEventRuntime.js';

function createHarness(initialTimeline = []) {
  const state = {
    timelinesByThread: { 'thread-1': initialTimeline },
    activityEntries: [],
    threads: [],
    activeTurnByThread: {},
  };
  const runtime = {
    assistantDeltaBuffers: new Map(),
    turnTerminalStates: new Map(),
    observedTurnByThread: new Map(),
    retiredTurnRefs: new Map(),
    retiredTurnFilter: new Uint32Array(256),
    assistantEventLedgersByScope: new Map(),
    assistantEventScope: '',
    assistantDeltaFlushTimer: null,
    assistantEventScopeEpoch: 0,
    get: () => state,
    set: (updater) => Object.assign(state, updater(state)),
    addWarning: vi.fn(),
    notifyAction: vi.fn(),
    bridgeThreadIdForPayload: (payload) => payload.threadId || '',
    currentChatCwd: () => '/workspace',
  };
  attachAssistantEventRuntime(runtime, {
    ASSISTANT_DELTA_FLUSH_MS: 1000,
    activeTurnIdForThread: (value, threadId) => value.activeTurnByThread[threadId] || '',
    actionNotice: (message, kind) => ({ message, kind }),
    clockNowISO: () => '2026-07-21T00:00:00Z',
    clockNowMillis: () => 42,
    emitFrontendTraceEvent: vi.fn(),
    extractDeltaText: (value) => typeof value === 'string' ? value : '',
    hasOwn: (value, key) => Object.prototype.hasOwnProperty.call(value, key),
    normalizeString: (value) => typeof value === 'string' ? value.trim() : '',
    optionalUiArray: () => [],
    runtimeThreadIdentifier: (payload) => payload.threadId || '',
    threadMatchesIdentifier: (thread, threadId) => thread.id === threadId,
  });
  runtime.activateAssistantEventScope('/workspace');
  return { runtime, state };
}

describe('assistantEventRuntime', () => {
  it('fails closed for an unknown assistant event shape', () => {
    const { runtime, state } = createHarness();

    expect(runtime.enqueueAssistantDelta('item/unknown', { threadId: 'thread-1', delta: 'ignored' })).toBe(false);
    expect(state.timelinesByThread['thread-1']).toEqual([]);
    expect(runtime.addWarning).toHaveBeenCalledWith('error', 'turn.event.contract_invalid', expect.any(Object));
  });

  it('flushes a delta before merging its completion', () => {
    const { runtime, state } = createHarness();
    const delta = { threadId: 'thread-1', turnId: 'turn-1', itemId: 'stream-1', delta: 'partial', timestamp: '2026-07-21T00:00:00Z' };
    const completion = { threadId: 'thread-1', turnId: 'turn-1', item: { id: 'final-1', type: 'agent_message', text: 'partial answer' } };

    expect(runtime.enqueueAssistantDelta('item/agentmessage/delta', delta)).toBe(true);
    expect(runtime.applyAssistantCompletion('item/completed', completion)).toBe(true);
    expect(state.timelinesByThread['thread-1']).toContainEqual(expect.objectContaining({ id: 'stream-1', text: 'partial', done: false, turnId: 'turn-1' }));
    expect(state.timelinesByThread['thread-1']).toContainEqual(expect.objectContaining({ id: 'final-1', text: 'partial answer', done: true, turnId: 'turn-1' }));
  });

  it('buffers and applies command output against the command lifecycle item', () => {
    const { runtime, state } = createHarness([{ id: 'command-1', kind: 'command', turnId: 'turn-1', text: '', done: false }]);

    expect(runtime.enqueueCommandOutputDelta('item/commandexecution/outputdelta', { threadId: 'thread-1', turnId: 'turn-1', delta: 'tool output' })).toBe(true);
    expect(runtime.flushAssistantDeltasNow()).toBe(true);
    expect(state.timelinesByThread['thread-1']).toMatchObject([{ id: 'command-1', kind: 'command', text: 'tool output', done: false }]);
  });
});
