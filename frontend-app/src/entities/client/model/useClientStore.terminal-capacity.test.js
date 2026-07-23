import { beforeEach, expect, it, vi } from 'vitest';

const bridge = vi.hoisted(() => ({ callback: null }));

vi.mock('../../../shared/api/backendApi.js', async (importOriginal) => {
  const actual = await importOriginal();
  return {
    ...actual,
    onBridgeEvent: vi.fn((callback) => {
      bridge.callback = callback;
      return () => {
        if (bridge.callback === callback) bridge.callback = null;
      };
    }),
    onRuntimeReconnect: vi.fn(() => () => {}),
    registerBridgeLogStore: actual.registerBridgeLogStore,
    sendFrontendLogBatch: vi.fn(),
  };
});

import { resetClientStoreForTests, useClientStore } from './useClientStore.js';

function terminal(threadId, turnId, index) {
  bridge.callback({
    type: 'turn/terminal',
    payload: {
      schemaVersion: 2,
      eventId: `terminal-${index}`,
      threadId,
      turnId,
      outcome: 'success',
      occurredAt: '2026-07-23T01:00:00Z',
    },
  });
}

beforeEach(() => {
  vi.clearAllMocks();
  bridge.callback = null;
  resetClientStoreForTests();
});

it('preserves a newer active turn when an evicted completed turn emits late terminal and delta events', () => {
  const threads = Array.from({ length: 65 }, (_, index) => ({
    id: `thread-${index}`,
    name: `Thread ${index}`,
    provider: 'codex',
    status: 'running',
  }));
  resetClientStoreForTests({
    cwd: '/repo/app',
    activeProject: '/repo/app',
    activeThreadId: 'thread-0',
    activeTurnByThread: Object.fromEntries(threads.map((thread, index) => [
      thread.id,
      { id: `turn-${index}`, status: 'running' },
    ])),
    threads,
    timelinesByThread: { 'thread-0': [] },
  });
  void useClientStore.getState().initializeEvents();

  for (let index = 0; index < 65; index += 1) {
    terminal(`thread-${index}`, `turn-${index}`, index);
  }
  expect(useClientStore.getState().getTurnTerminalCacheStats()).toEqual(
    expect.objectContaining({
      capacity: 64,
      terminalStates: 64,
      observedTurns: 64,
      retiredTurns: 1,
    }),
  );

  useClientStore.setState((state) => ({
    activeTurnByThread: {
      ...state.activeTurnByThread,
      'thread-0': { id: 'turn-new', status: 'running' },
    },
    statuses: { ...state.statuses, 'thread-0': { status: 'running' } },
  }));
  terminal('thread-0', 'turn-0', 'late');
  bridge.callback({
    type: 'turn/output/delta',
    payload: {
      threadId: 'thread-0',
      turnId: 'turn-0',
      itemId: 'late-item',
      delta: 'late mutation',
    },
  });

  const state = useClientStore.getState();
  expect(state.activeTurnByThread['thread-0']).toEqual(
    expect.objectContaining({ id: 'turn-new', status: 'running' }),
  );
  expect(state.timelinesByThread['thread-0']).not.toEqual(expect.arrayContaining([
    expect.objectContaining({ id: 'late-item' }),
  ]));
  expect(state.warningEntries).toEqual(expect.arrayContaining([
    expect.objectContaining({
      event: 'turn.terminal.stale',
      fields: expect.objectContaining({ turn_id: 'turn-0' }),
    }),
    expect.objectContaining({
      event: 'turn.event.late',
      fields: expect.objectContaining({ turn_id: 'turn-0' }),
    }),
  ]));
});

it('rejects a late active-turn patch after both terminal and retired ledgers evict the completed turn', () => {
  resetClientStoreForTests({
    cwd: '/repo/app',
    activeProject: '/repo/app',
    activeThreadId: 'thread-1',
    threads: [{ id: 'thread-1', name: 'Thread 1', provider: 'codex', status: 'running' }],
    timelinesByThread: { 'thread-1': [] },
  });
  void useClientStore.getState().initializeEvents();

  for (let index = 0; index <= 65; index += 1) {
    bridge.callback({
      type: 'turn/output/delta',
      payload: {
        threadId: 'thread-1',
        turnId: `turn-${index}`,
        itemId: `item-${index}`,
        delta: `answer ${index}`,
      },
    });
    terminal('thread-1', `turn-${index}`, index);
  }
  expect(useClientStore.getState().getTurnTerminalCacheStats()).toEqual(
    expect.objectContaining({
      terminalStates: 1,
      observedTurns: 1,
      retiredTurns: 64,
    }),
  );

  bridge.callback({
    type: 'ui/thread/patch',
    payload: {
      threadId: 'thread-1',
      sequence: '999',
      status: 'running',
      activeTurn: { id: 'turn-0', status: 'running' },
    },
  });

  const state = useClientStore.getState();
  expect(state.activeTurnByThread).not.toHaveProperty('thread-1');
  expect(state.threads).toEqual([
    expect.objectContaining({ id: 'thread-1', status: 'completed' }),
  ]);
  expect(state.warningEntries).toEqual(expect.arrayContaining([
    expect.objectContaining({
      event: 'turn.event.late',
      fields: expect.objectContaining({
        eventName: 'ui/thread/patch',
        turn_id: 'turn-0',
      }),
    }),
  ]));
});
