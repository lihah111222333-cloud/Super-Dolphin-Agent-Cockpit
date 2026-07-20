import { beforeEach, expect, it, vi } from 'vitest';

let bridgeCallback;

const backend = vi.hoisted(() => ({
  emitFrontendTraceEvent: vi.fn(),
  onBridgeEvent: vi.fn((callback) => {
    bridgeCallback = callback;
    return () => {
      bridgeCallback = null;
    };
  }),
  onRuntimeReconnect: vi.fn(() => () => {}),
}));

vi.mock('../../../shared/api/backendApi.js', async (importOriginal) => ({
  ...await importOriginal(),
  ...backend,
}));

import { resetClientStoreForTests, useClientStore } from './useClientStore.js';

const threadId = 'thread-matrix';
const turnId = 'turn-1';

function terminal(overrides = {}) {
  const outcome = overrides.outcome || 'failed';
  return {
    schemaVersion: 2,
    eventId: overrides.eventId || `event-${outcome}`,
    threadId,
    turnId,
    outcome,
    ...(outcome === 'success' ? {} : {
      publicError: {
        code: 'PROVIDER_FAILED',
        title: '运行失败',
        message: '提供方未能完成本轮响应',
        diagnosticId: 'diag-matrix',
        retryable: false,
        recoveryActions: ['copy_diagnostics'],
      },
    }),
    occurredAt: '2026-07-17T01:02:03Z',
    ...overrides,
  };
}

function initializeMatrixStore(initial = {}) {
  resetClientStoreForTests({
    cwd: '/repo/matrix',
    activeProject: '/repo/matrix',
    activeThreadId: threadId,
    threads: [{ id: threadId, name: 'Matrix', provider: 'codex', status: 'running' }],
    timelinesByThread: { [threadId]: [] },
    ...initial,
  });
  void useClientStore.getState().initializeEvents();
  if (typeof bridgeCallback !== 'function') throw new Error('failure matrix bridge callback was not registered');
}

function emit(type, payload) {
  bridgeCallback({ type, payload });
}

async function flushAssistantDeltaBatch() {
  vi.advanceTimersByTime(50);
  await Promise.resolve();
  await Promise.resolve();
}

beforeEach(() => {
  vi.clearAllMocks();
  bridgeCallback = null;
  resetClientStoreForTests();
});

it('matrix:FM-01 layer:frontend preserves partial output and exposes failed terminal without success', async () => {
  vi.useFakeTimers();
  try {
    initializeMatrixStore();
    emit('turn/output/delta', { threadId, turnId, itemId: 'partial-1', delta: 'partial answer' });
    await flushAssistantDeltaBatch();
    emit('turn/terminal', terminal({ partialItemIds: ['partial-1'] }));

    const state = useClientStore.getState();
    expect(state.actionNotice).toEqual(expect.objectContaining({ tone: 'error' }));
    expect(state.timelinesByThread[threadId]).toEqual([
      expect.objectContaining({ id: 'partial-1', text: 'partial answer', done: true }),
      expect.objectContaining({ kind: 'turn_terminal', terminalOutcome: 'failed' }),
    ]);
    expect(state.timelinesByThread[threadId]).not.toEqual(expect.arrayContaining([
      expect.objectContaining({ terminalOutcome: 'success' }),
    ]));
  } finally {
    vi.useRealTimers();
  }
});

it('matrix:FM-02 layer:frontend exposes failed terminal without manufacturing blank completion', () => {
  initializeMatrixStore();
  emit('turn/terminal', terminal());

  const state = useClientStore.getState();
  expect(state.actionNotice).toEqual(expect.objectContaining({ tone: 'error' }));
  expect(state.timelinesByThread[threadId]).toEqual([
    expect.objectContaining({ kind: 'turn_terminal', terminalOutcome: 'failed' }),
  ]);
  expect(state.timelinesByThread[threadId]).not.toEqual(expect.arrayContaining([
    expect.objectContaining({ kind: 'assistant', text: '' }),
  ]));
});

it('matrix:FM-03 layer:frontend keeps the first failed terminal when a conflicting success arrives', () => {
  initializeMatrixStore();
  emit('turn/terminal', terminal({ eventId: 'event-first' }));
  const firstTimeline = useClientStore.getState().timelinesByThread[threadId];
  emit('turn/terminal', {
    schemaVersion: 2,
    eventId: 'event-conflict',
    threadId,
    turnId,
    outcome: 'success',
    occurredAt: '2026-07-17T01:02:04Z',
  });

  const state = useClientStore.getState();
  expect(state.timelinesByThread[threadId]).toBe(firstTimeline);
  expect(state.actionNotice).toEqual(expect.objectContaining({ tone: 'error' }));
  expect(backend.emitFrontendTraceEvent).toHaveBeenCalledWith(expect.objectContaining({
    method: 'turn.terminal.conflict',
    turn_id: turnId,
  }));
});

it('matrix:FM-04 layer:frontend isolates active T2 from late T1 delta, item, and terminal events', async () => {
  vi.useFakeTimers();
  try {
    const activeTurn = { id: 'turn-2', status: 'running' };
    const timeline = [{ id: 't2-open', role: 'assistant', text: 'current', done: false, turnId: 'turn-2' }];
    initializeMatrixStore({
      activeTurnByThread: { [threadId]: activeTurn },
      timelinesByThread: { [threadId]: timeline },
      actionNotice: { message: 'T2 正在运行', tone: 'info' },
    });
    emit('turn/output/delta', { threadId, turnId, itemId: 'late-delta', delta: 'late' });
    emit('item/completed', { threadId, turnId, item: { id: 'late-item', type: 'assistant', text: 'late item' } });
    emit('turn/terminal', terminal({ eventId: 'late-terminal' }));
    await flushAssistantDeltaBatch();

    const state = useClientStore.getState();
    expect(state.activeTurnByThread[threadId]).toBe(activeTurn);
    expect(state.timelinesByThread[threadId]).toBe(timeline);
    expect(state.timelinesByThread[threadId][0]).toEqual(expect.objectContaining({ turnId: 'turn-2', done: false }));
    expect(backend.emitFrontendTraceEvent).toHaveBeenCalledWith(expect.objectContaining({
      phase: 'frontend.turn_event.rejected',
      turn_id: turnId,
    }));
  } finally {
    vi.useRealTimers();
  }
});

it('matrix:FM-05 layer:frontend makes an unknown outcome visible as a contract error', () => {
  initializeMatrixStore();
  const invalid = terminal({ outcome: 'mystery' });
  delete invalid.publicError;
  emit('turn/terminal', invalid);

  const state = useClientStore.getState();
  expect(state.actionNotice).toEqual(expect.objectContaining({
    message: '响应契约错误',
    tone: 'error',
  }));
  expect(state.warningEntries).toEqual([
    expect.objectContaining({ event: 'turn.terminal.contract_invalid', level: 'error' }),
  ]);
  expect(state.timelinesByThread[threadId]).toEqual([]);
});

it('matrix:FM-06 layer:frontend makes a missing outcome visible as a contract error', () => {
  initializeMatrixStore();
  const invalid = terminal();
  delete invalid.outcome;
  emit('turn/terminal', invalid);

  const state = useClientStore.getState();
  expect(state.actionNotice).toEqual(expect.objectContaining({
    message: '响应契约错误',
    tone: 'error',
  }));
  expect(state.warningEntries).toEqual([
    expect.objectContaining({ event: 'turn.terminal.contract_invalid', level: 'error' }),
  ]);
  expect(state.timelinesByThread[threadId]).toEqual([]);
});

it('matrix:FM-19 layer:frontend keeps matched user cancellation neutral and non-successful', () => {
  initializeMatrixStore();
  emit('turn/terminal', {
    schemaVersion: 2,
    eventId: 'event-user-cancel',
    threadId,
    turnId,
    outcome: 'cancelled',
    terminationCause: 'user_request',
    terminationRequestId: 'stop-user-1',
    occurredAt: '2026-07-17T01:02:03Z',
  });

  const state = useClientStore.getState();
  expect(state.actionNotice).toEqual(expect.objectContaining({ message: '本轮已取消', tone: 'info' }));
  expect(state.warningEntries).toEqual([]);
  expect(state.timelinesByThread[threadId]).toEqual([
    expect.objectContaining({ kind: 'turn_terminal', terminalOutcome: 'cancelled' }),
  ]);
});

it('matrix:FM-20 layer:frontend keeps provider cancellation as a safe visible error', () => {
  initializeMatrixStore();
  emit('turn/terminal', terminal({
    eventId: 'event-provider-cancel',
    outcome: 'cancelled',
    terminationCause: 'provider',
  }));

  const state = useClientStore.getState();
  expect(state.actionNotice).toEqual(expect.objectContaining({ tone: 'error' }));
  expect(state.warningEntries).toEqual([
    expect.objectContaining({ event: 'turn.terminal.cancelled', level: 'error' }),
  ]);
  expect(state.timelinesByThread[threadId]).toEqual([
    expect.objectContaining({
      kind: 'turn_terminal',
      terminalOutcome: 'cancelled',
      terminationCause: 'provider',
    }),
  ]);
});
