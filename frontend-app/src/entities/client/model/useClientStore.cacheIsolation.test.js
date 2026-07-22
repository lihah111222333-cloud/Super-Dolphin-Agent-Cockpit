import { beforeEach, expect, it, vi } from 'vitest';

const bridgeState = vi.hoisted(() => {
  const state = {
    bridgeCallback: null,
    bridgeOptions: null,
    runtimeReconnectCallback: null,
  };
  state.backend = {
    deleteThread: vi.fn(),
    emitFrontendTraceEvent: vi.fn(),
    onBridgeEvent: vi.fn((callback, options = {}) => {
      state.bridgeCallback = callback;
      state.bridgeOptions = options;
      return () => {
        if (state.bridgeCallback === callback) {
          state.bridgeCallback = null;
          state.bridgeOptions = null;
        }
      };
    }),
    onRuntimeReconnect: vi.fn((callback) => {
      state.runtimeReconnectCallback = callback;
      return () => {
        if (state.runtimeReconnectCallback === callback) {
          state.runtimeReconnectCallback = null;
        }
      };
    }),
    setPreference: vi.fn(),
  };
  return state;
});

vi.mock('../../../shared/api/backendApi.js', async (importOriginal) => {
  const actual = await importOriginal();
  return {
    ...actual,
    ...bridgeState.backend,
  };
});

import { resetClientStoreForTests, useClientStore } from './useClientStore.js';

function bridgeEventForTest(event) {
  const callback = bridgeState.bridgeCallback;
  if (!callback) throw new Error('bridge callback was not registered');
  callback(event);
}

async function registerBridgeEventHandlersForTest() {
  const initialization = useClientStore.getState().initializeEvents();
  void initialization.catch((error) => {
    if (error?.message !== 'runtime event initialization superseded') throw error;
  });
  return initialization;
}

beforeEach(() => {
  vi.clearAllMocks();
  bridgeState.bridgeCallback = null;
  bridgeState.bridgeOptions = null;
  bridgeState.runtimeReconnectCallback = null;
  resetClientStoreForTests();
  bridgeState.backend.deleteThread.mockResolvedValue({ ok: true });
  bridgeState.backend.setPreference.mockResolvedValue({ ok: true });
});

it('accepts a Bloom-collision terminal absent from exact retired refs', async () => {
  const retiredThreads = Array.from({ length: 128 }, (_, offset) => {
    const index = offset + 1;
    return { id: 'thread-' + index, name: 'Thread ' + index, provider: 'codex', status: 'running' };
  });
  const candidateThreadId = 'candidate-thread-47395';
  const candidateTurnId = 'candidate-turn-47395';
  resetClientStoreForTests({
    cwd: '/repo/app',
    activeProject: '/repo/app',
    activeThreadId: candidateThreadId,
    threads: [...retiredThreads, {
      id: candidateThreadId,
      name: 'Candidate thread',
      provider: 'codex',
      status: 'running',
    }],
    timelinesByThread: Object.fromEntries([
      ...retiredThreads.map((thread) => [thread.id, []]),
      [candidateThreadId, []],
    ]),
  });
  await registerBridgeEventHandlersForTest();

  for (let index = 1; index <= 128; index += 1) {
    bridgeEventForTest({
      type: 'turn/terminal',
      payload: {
        schemaVersion: 2,
        eventId: 'retired-terminal-' + index,
        threadId: 'thread-' + index,
        turnId: 'turn-' + index,
        outcome: 'success',
        occurredAt: '2026-07-22T01:00:00Z',
      },
    });
  }
  expect(useClientStore.getState().getTurnTerminalCacheStats()).toMatchObject({ retiredTurns: 64 });

  await expect(useClientStore.getState().deleteStaleThreads(
    retiredThreads.map((thread) => thread.id),
  )).resolves.toEqual({ deleted: 128, failed: 0 });
  expect(useClientStore.getState().getTurnTerminalCacheStats()).toMatchObject({ retiredTurns: 0 });

  bridgeEventForTest({
    type: 'turn/terminal',
    payload: {
      schemaVersion: 2,
      eventId: 'candidate-terminal',
      threadId: candidateThreadId,
      turnId: candidateTurnId,
      outcome: 'success',
      occurredAt: '2026-07-22T01:00:01Z',
    },
  });

  expect(useClientStore.getState().timelinesByThread[candidateThreadId]).toEqual([
    expect.objectContaining({ kind: 'turn_terminal', turnId: candidateTurnId }),
  ]);
  expect(useClientStore.getState().warningEntries).not.toEqual(expect.arrayContaining([
    expect.objectContaining({
      event: 'turn.event.late',
      fields: expect.objectContaining({ turn_id: candidateTurnId }),
    }),
  ]));
});

it('continues sequential terminals beyond exact tombstone capacity', async () => {
  resetClientStoreForTests({
    cwd: '/repo/app',
    activeProject: '/repo/app',
    activeThreadId: 'thread-1',
    threads: [{ id: 'thread-1', name: 'Thread 1', provider: 'codex', status: 'running' }],
    timelinesByThread: { 'thread-1': [] },
  });
  await registerBridgeEventHandlersForTest();

  for (let index = 0; index <= 65; index++) {
    bridgeEventForTest({
      type: 'turn/output/delta',
      payload: {
        threadId: 'thread-1',
        turnId: `turn-${index}`,
        itemId: `item-${index}`,
        delta: `answer ${index}`,
      },
    });
    bridgeEventForTest({
      type: 'turn/terminal',
      payload: {
        schemaVersion: 2,
        eventId: `terminal-${index}`,
        threadId: 'thread-1',
        turnId: `turn-${index}`,
        outcome: 'success',
        occurredAt: '2026-07-20T01:00:00Z',
      },
    });
  }

  expect(useClientStore.getState().timelinesByThread['thread-1']).toEqual(expect.arrayContaining([
    expect.objectContaining({ kind: 'turn_terminal', turnId: 'turn-65', terminalOutcome: 'success' }),
  ]));
  expect(useClientStore.getState().getTurnTerminalCacheStats()).toMatchObject({
    capacity: 64,
    terminalStates: 1,
    observedTurns: 1,
    retiredTurns: 64,
  });

  bridgeEventForTest({
    type: 'turn/output/delta',
    payload: {
      threadId: 'thread-1',
      turnId: 'turn-0',
      itemId: 'late-item',
      delta: 'late mutation',
    },
  });

  const state = useClientStore.getState();
  expect(state.timelinesByThread['thread-1']).not.toEqual(expect.arrayContaining([
    expect.objectContaining({ id: 'late-item' }),
  ]));
  expect(state.warningEntries).not.toEqual(expect.arrayContaining([
    expect.objectContaining({
      event: 'turn.event.cache_exhausted',
      fields: expect.objectContaining({ turn_id: 'turn-65', reason: 'capacity' }),
    }),
  ]));
  expect(state.warningEntries).toEqual(expect.arrayContaining([
    expect.objectContaining({
      event: 'turn.event.late',
      fields: expect.objectContaining({ turn_id: 'turn-0' }),
    }),
  ]));
});

it('clears only a deleted thread bridge patch cache before the same id is recreated', async () => {
  resetClientStoreForTests({
    cwd: '/repo/app',
    activeProject: '/repo/app',
    activeThreadId: 'thread-1',
    threads: [
      { id: 'thread-1', name: 'Deleted thread', provider: 'codex', status: 'running' },
      { id: 'thread-2', name: 'Retained thread', provider: 'codex', status: 'running' },
    ],
    timelinesByThread: { 'thread-1': [], 'thread-2': [] },
  });
  await registerBridgeEventHandlersForTest();

  bridgeEventForTest({
    type: 'ui/thread/patch',
    payload: {
      threadId: 'thread-1',
      generation: '2',
      sequence: '2',
      timelineItems: [{ id: 'deleted-old', kind: 'assistant', text: 'old deleted generation' }],
    },
  });
  bridgeEventForTest({
    type: 'ui/thread/patch',
    payload: {
      threadId: 'thread-2',
      generation: '2',
      sequence: '2',
      timelineItems: [{ id: 'retained-current', kind: 'assistant', text: 'retained generation' }],
    },
  });

  await expect(useClientStore.getState().deleteStaleThreads(['thread-1'])).resolves.toEqual({
    deleted: 1,
    failed: 0,
  });
  useClientStore.setState({
    activeThreadId: 'thread-1',
    threads: [
      { id: 'thread-1', name: 'Recreated thread', provider: 'codex', status: 'running' },
      { id: 'thread-2', name: 'Retained thread', provider: 'codex', status: 'running' },
    ],
    timelinesByThread: { 'thread-1': [], 'thread-2': useClientStore.getState().timelinesByThread['thread-2'] },
  });

  bridgeEventForTest({
    type: 'ui/thread/patch',
    payload: {
      threadId: 'thread-1',
      generation: '1',
      sequence: '1',
      timelineItems: [{ id: 'recreated-fresh', kind: 'assistant', text: 'fresh recreated generation' }],
    },
  });
  bridgeEventForTest({
    type: 'ui/thread/patch',
    payload: {
      threadId: 'thread-2',
      generation: '1',
      sequence: '99',
      timelineItems: [{ id: 'retained-stale', kind: 'assistant', text: 'stale retained generation' }],
    },
  });

  expect(useClientStore.getState().timelinesByThread['thread-1']).toEqual([
    expect.objectContaining({ id: 'recreated-fresh', text: 'fresh recreated generation' }),
  ]);
  expect(useClientStore.getState().timelinesByThread['thread-2']).toEqual([
    expect.objectContaining({ id: 'retained-current', text: 'retained generation' }),
  ]);
});
