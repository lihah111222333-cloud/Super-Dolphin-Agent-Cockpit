import { describe, expect, it, vi } from 'vitest';
import { createRuntimeSlice } from './runtimeSlice.js';

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((promiseResolve, promiseReject) => {
    resolve = promiseResolve;
    reject = promiseReject;
  });
  return { promise, reject, resolve };
}

function createRuntime(overrides = {}) {
  return {
    bridgeUnsubscribe: null,
    reconnectUnsubscribe: null,
    eventInitializationPromise: null,
    eventInitializationGeneration: 0,
    eventInitializationState: 'idle',
    pendingRuntimeSubscriptions: new Set(),
    addWarning: vi.fn(),
    get: vi.fn(() => ({ activeThreadId: '', bootstrapStatus: 'ready' })),
    handleBridgeEvent: vi.fn(),
    sequencesByThread: new Map(),
    composerDrafts: new Map(),
    sidebarSnapshotsByCwd: new Map(),
    sidebarRefreshesByCwd: new Map(),
    threadMessageGenerations: new Map(),
    threadSyncGenerations: new Map(),
    assistantDeltaBuffers: new Map(),
    sidebarRefreshSeq: 0,
    clearAssistantDeltaFlushTimer: vi.fn(),
    ...overrides,
  };
}

function createDeps(overrides = {}) {
  return {
    isDagNodeStatusBridgeEvent: vi.fn(() => false),
    onBridgeEvent: vi.fn(() => vi.fn()),
    onRuntimeReconnect: vi.fn(() => vi.fn()),
    ...overrides,
  };
}

describe('runtime slice event lifecycle', () => {
  it('shares one promise and commits two subscriptions atomically', async () => {
    const bridge = deferred();
    const reconnect = deferred();
    const bridgeUnsubscribe = vi.fn();
    const reconnectUnsubscribe = vi.fn();
    const runtime = createRuntime();
    const actions = createRuntimeSlice(runtime, createDeps({
      onBridgeEvent: vi.fn(() => ({ ready: bridge.promise, unsubscribe: bridgeUnsubscribe })),
      onRuntimeReconnect: vi.fn(() => ({ ready: reconnect.promise, unsubscribe: reconnectUnsubscribe })),
    }));

    const first = actions.initializeEvents();
    const second = actions.initializeEvents();
    expect(first).toBe(second);

    bridge.resolve(true);
    await bridge.promise;
    await Promise.resolve();
    expect(runtime.bridgeUnsubscribe).toBeNull();
    expect(runtime.reconnectUnsubscribe).toBeNull();

    reconnect.resolve(true);
    await first;
    expect(runtime.bridgeUnsubscribe).toBe(bridgeUnsubscribe);
    expect(runtime.reconnectUnsubscribe).toBe(reconnectUnsubscribe);
    expect(runtime.eventInitializationState).toBe('ready');
  });

  it('cleans both subscriptions when one readiness is false', async () => {
    const bridgeUnsubscribe = vi.fn();
    const reconnectUnsubscribe = vi.fn();
    const runtime = createRuntime();
    const actions = createRuntimeSlice(runtime, createDeps({
      onBridgeEvent: vi.fn(() => ({ ready: Promise.resolve(true), unsubscribe: bridgeUnsubscribe })),
      onRuntimeReconnect: vi.fn(() => ({ ready: Promise.resolve(false), unsubscribe: reconnectUnsubscribe })),
    }));

    await expect(actions.initializeEvents()).rejects.toThrow('runtime.reconnect.subscribe unavailable');
    expect(bridgeUnsubscribe).toHaveBeenCalledTimes(1);
    expect(reconnectUnsubscribe).toHaveBeenCalledTimes(1);
    expect(runtime.bridgeUnsubscribe).toBeNull();
    expect(runtime.reconnectUnsubscribe).toBeNull();
    expect(runtime.eventInitializationPromise).toBeNull();
  });

  it('cleans rejected readiness and permits a complete retry', async () => {
    const firstBridgeUnsubscribe = vi.fn();
    const firstReconnectUnsubscribe = vi.fn();
    const secondBridgeUnsubscribe = vi.fn();
    const secondReconnectUnsubscribe = vi.fn();
    const onBridgeEvent = vi.fn()
      .mockReturnValueOnce({ ready: Promise.reject(new Error('bridge rejected')), unsubscribe: firstBridgeUnsubscribe })
      .mockReturnValueOnce({ ready: Promise.resolve(true), unsubscribe: secondBridgeUnsubscribe });
    const onRuntimeReconnect = vi.fn()
      .mockReturnValueOnce({ ready: Promise.resolve(true), unsubscribe: firstReconnectUnsubscribe })
      .mockReturnValueOnce({ ready: Promise.resolve(true), unsubscribe: secondReconnectUnsubscribe });
    const runtime = createRuntime();
    const actions = createRuntimeSlice(runtime, createDeps({ onBridgeEvent, onRuntimeReconnect }));

    await expect(actions.initializeEvents()).rejects.toThrow('bridge rejected');
    expect(firstBridgeUnsubscribe).toHaveBeenCalledTimes(1);
    expect(firstReconnectUnsubscribe).toHaveBeenCalledTimes(1);

    await expect(actions.initializeEvents()).resolves.toBe(true);
    expect(onBridgeEvent).toHaveBeenCalledTimes(2);
    expect(onRuntimeReconnect).toHaveBeenCalledTimes(2);
    expect(runtime.bridgeUnsubscribe).toBe(secondBridgeUnsubscribe);
    expect(runtime.reconnectUnsubscribe).toBe(secondReconnectUnsubscribe);
  });

  it('invalidates late readiness when destroy supersedes initialization', async () => {
    const bridge = deferred();
    const reconnect = deferred();
    const bridgeUnsubscribe = vi.fn();
    const reconnectUnsubscribe = vi.fn();
    const runtime = createRuntime();
    const actions = createRuntimeSlice(runtime, createDeps({
      onBridgeEvent: vi.fn(() => ({ ready: bridge.promise, unsubscribe: bridgeUnsubscribe })),
      onRuntimeReconnect: vi.fn(() => ({ ready: reconnect.promise, unsubscribe: reconnectUnsubscribe })),
    }));

    const initialization = actions.initializeEvents();
    await Promise.resolve();
    actions.destroy();
    bridge.resolve(true);
    reconnect.resolve(true);

    await expect(initialization).rejects.toThrow('runtime event initialization superseded');
    expect(bridgeUnsubscribe).toHaveBeenCalledTimes(1);
    expect(reconnectUnsubscribe).toHaveBeenCalledTimes(1);
    expect(runtime.bridgeUnsubscribe).toBeNull();
    expect(runtime.reconnectUnsubscribe).toBeNull();
  });

  it('keeps a replacement bridge subscription after partial readiness is superseded', async () => {
    const bridge = deferred();
    const reconnect = deferred();
    let activeBridgeHandler = null;
    const firstBridgeUnsubscribe = vi.fn(() => {
      activeBridgeHandler = null;
    });
    const secondBridgeUnsubscribe = vi.fn(() => {
      activeBridgeHandler = null;
    });
    const onBridgeEvent = vi.fn()
      .mockImplementationOnce((handler) => {
        activeBridgeHandler = handler;
        return { ready: bridge.promise, unsubscribe: firstBridgeUnsubscribe };
      })
      .mockImplementationOnce((handler) => {
        activeBridgeHandler = handler;
        return { ready: Promise.resolve(true), unsubscribe: secondBridgeUnsubscribe };
      });
    const onRuntimeReconnect = vi.fn()
      .mockReturnValueOnce({ ready: reconnect.promise, unsubscribe: vi.fn() })
      .mockReturnValueOnce({ ready: Promise.resolve(true), unsubscribe: vi.fn() });
    const runtime = createRuntime();
    const actions = createRuntimeSlice(runtime, createDeps({ onBridgeEvent, onRuntimeReconnect }));

    const firstInitialization = actions.initializeEvents();
    bridge.resolve(true);
    for (let index = 0; index < 8; index += 1) await Promise.resolve();

    actions.destroy();
    const secondInitialization = actions.initializeEvents();
    reconnect.resolve(true);

    await expect(firstInitialization).rejects.toThrow('runtime event initialization superseded');
    await expect(secondInitialization).resolves.toBe(true);
    expect(activeBridgeHandler).toEqual(expect.any(Function));
    expect(firstBridgeUnsubscribe).toHaveBeenCalledTimes(1);
    expect(secondBridgeUnsubscribe).not.toHaveBeenCalled();
  });

  it('removes an unresolved sibling subscription after the other readiness rejects', async () => {
    const neverReady = deferred();
    const bridgeUnsubscribe = vi.fn();
    const reconnectUnsubscribe = vi.fn();
    const runtime = createRuntime();
    const actions = createRuntimeSlice(runtime, createDeps({
      onBridgeEvent: vi.fn(() => ({
        ready: Promise.reject(new Error('bridge unavailable')),
        unsubscribe: bridgeUnsubscribe,
      })),
      onRuntimeReconnect: vi.fn(() => ({
        ready: neverReady.promise,
        unsubscribe: reconnectUnsubscribe,
      })),
    }));

    await expect(actions.initializeEvents()).rejects.toThrow('bridge unavailable');

    expect(runtime.pendingRuntimeSubscriptions).toHaveLength(0);
    expect(bridgeUnsubscribe).toHaveBeenCalledTimes(1);
    expect(reconnectUnsubscribe).toHaveBeenCalledTimes(1);
  });
});
