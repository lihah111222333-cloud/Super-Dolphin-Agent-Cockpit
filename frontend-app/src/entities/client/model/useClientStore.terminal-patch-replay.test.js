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

beforeEach(() => {
  vi.clearAllMocks();
  bridge.callback = null;
  resetClientStoreForTests({
    cwd: '/repo/app',
    activeProject: '/repo/app',
    activeThreadId: 'thread-1',
    threads: [{ id: 'thread-1', name: 'Thread 1', provider: 'codex', status: 'running' }],
    timelinesByThread: { 'thread-1': [] },
    actionNotice: { message: '消息已发送，等待回复', tone: 'info' },
  });
  void useClientStore.getState().initializeEvents();
});

it('seals a capacity-tracked pending terminal after a same-turn patch supplies its partial item', () => {
  bridge.callback({
    type: 'turn/terminal',
    payload: {
      schemaVersion: 2,
      eventId: 'terminal-before-patch',
      threadId: 'thread-1',
      turnId: 'turn-1',
      outcome: 'success',
      partialItemIds: ['assistant-partial'],
      occurredAt: '2026-07-22T01:00:00Z',
    },
  });

  expect(useClientStore.getState().getTurnTerminalCacheStats()).toEqual(
    expect.objectContaining({ terminalStates: 1 }),
  );
  expect(useClientStore.getState().actionNotice).toEqual(
    expect.objectContaining({ message: '消息已发送，等待回复' }),
  );

  bridge.callback({
    type: 'ui/thread/patch',
    payload: {
      threadId: 'thread-1',
      sequence: '1',
      status: 'running',
      activeTurn: { id: 'turn-1', status: 'running' },
      timelineItems: [{
        id: 'assistant-partial',
        kind: 'assistant',
        role: 'assistant',
        text: 'patched answer',
        turnId: 'turn-1',
        done: false,
      }],
    },
  });

  const state = useClientStore.getState();
  expect(state.timelinesByThread['thread-1']).toEqual(expect.arrayContaining([
    expect.objectContaining({ id: 'assistant-partial', turnId: 'turn-1', done: true }),
    expect.objectContaining({ kind: 'turn_terminal', turnId: 'turn-1', terminalOutcome: 'success' }),
  ]));
  expect(state.actionNotice).toEqual(expect.objectContaining({ message: '已收到回复', tone: 'success' }));
});

it('clears matching active-turn aliases and running state when the canonical terminal seals', () => {
  resetClientStoreForTests({
    cwd: '/repo/app',
    activeProject: '/repo/app',
    activeThreadId: 'thread-1',
    threads: [{
      id: 'thread-1',
      agentId: 'agent-1',
      name: 'Thread 1',
      provider: 'codex',
      status: 'running',
    }],
    statuses: {
      'thread-1': { status: 'running', interruptible: true },
      'agent-1': { status: 'running', interruptible: true },
    },
    activeTurnByThread: {
      'thread-1': { id: 'turn-1', threadId: 'thread-1', status: 'running' },
      'agent-1': { id: 'turn-1', threadId: 'thread-1', status: 'running' },
    },
    timelinesByThread: {
      'thread-1': [{ id: 'final-1', role: 'assistant', text: 'done', done: true, turnId: 'turn-1' }],
    },
  });
  void useClientStore.getState().initializeEvents();

  bridge.callback({
    type: 'turn/terminal',
    payload: {
      schemaVersion: 2,
      eventId: 'terminal-turn-1',
      threadId: 'thread-1',
      turnId: 'turn-1',
      outcome: 'success',
      partialItemIds: ['final-1'],
      occurredAt: '2026-07-23T01:00:00Z',
    },
  });

  const state = useClientStore.getState();
  expect(state.activeTurnByThread).not.toHaveProperty('thread-1');
  expect(state.activeTurnByThread).not.toHaveProperty('agent-1');
  expect(state.threads[0]).toEqual(expect.objectContaining({ status: 'completed' }));
  expect(state.statuses['thread-1']).toEqual(expect.objectContaining({
    status: 'completed',
    interruptible: false,
  }));
  expect(state.statuses['agent-1']).toEqual(expect.objectContaining({
    status: 'completed',
    interruptible: false,
  }));
});
