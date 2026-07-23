import { beforeEach, describe, expect, it, vi } from 'vitest';
import { getPreference } from '../../../shared/api/backendApi.js';
import { frontendHealthSnapshot, resetFrontendHealthForTest } from '../../../shared/diagnostics/frontendHealthStore.js';
import { createRuntimeSlice } from './runtimeSlice.js';

vi.mock('../../../shared/api/backendApi.js', () => ({
  getPreference: vi.fn(),
}));

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
    bridgeScopeRebindGeneration: 0,
    pendingBridgeScopeRebind: null,
    bridgeEventScopeGeneration: 0,
    assistantEventScope: '/repo/app',
    currentChatCwd: vi.fn(() => '/repo/app'),
    assertAssistantEventScopeCapacity: vi.fn(),
    activateAssistantEventScope: vi.fn(),
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
    reportFrontendReadiness: vi.fn().mockResolvedValue(1),
    ...overrides,
  };
}

beforeEach(() => {
  window.localStorage.clear();
  resetFrontendHealthForTest();
});

describe('runtime slice event lifecycle', () => {
  it('binds the first backend scope when no previous project exists', async () => {
    let runtime;
    runtime = createRuntime({
      assistantEventScope: '',
      currentChatCwd: vi.fn(() => ''),
      activateAssistantEventScope: vi.fn((scope) => {
        runtime.assistantEventScope = scope;
      }),
    });
    const actions = createRuntimeSlice(runtime, createDeps({
      onBridgeEvent: vi.fn(() => ({ ready: Promise.resolve(true), unsubscribe: vi.fn() })),
    }));

    await expect(actions.rebindBridgeEventScope('/repo/app')).resolves.toBe(true);
    expect(runtime.assistantEventScope).toBe('/repo/app');
  });

  it('matrix:FM-18 layer:frontend persists reconnect bootstrap failure in Health and permits recovery', async () => {
    const rawCause = 'provider reconnect token=secret';
    let reconnectHandler;
    let bootstrapFails = true;
    const bootstrap = vi.fn(() => (
      bootstrapFails ? Promise.reject(new Error(rawCause)) : Promise.resolve(true)
    ));
    const runtime = createRuntime({
      get: vi.fn(() => ({ activeThreadId: '', bootstrap, bootstrapStatus: 'failed' })),
    });
    const actions = createRuntimeSlice(runtime, createDeps({
      onRuntimeReconnect: vi.fn((handler) => {
        reconnectHandler = handler;
        return { ready: Promise.resolve(true), unsubscribe: vi.fn() };
      }),
    }));

    await actions.initializeEvents();
    reconnectHandler();
    await vi.waitFor(() => {
      expect(frontendHealthSnapshot()).toEqual(expect.arrayContaining([
        expect.objectContaining({ actionId: 'provider.reconnect.bootstrap' }),
      ]));
    });
    expect(JSON.stringify(frontendHealthSnapshot())).not.toContain(rawCause);

    bootstrapFails = false;
    reconnectHandler();
    await vi.waitFor(() => expect(bootstrap).toHaveBeenCalledTimes(2));
    expect(reconnectHandler).toEqual(expect.any(Function));
  });

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

  it('keeps the old bridge callback live until the replacement subscription is ready', async () => {
    const replacementReady = deferred();
    const oldUnsubscribe = vi.fn();
    const replacementUnsubscribe = vi.fn();
    let oldHandler;
    let replacementHandler;
    let runtime;
    runtime = createRuntime({
      activateAssistantEventScope: vi.fn((scope) => {
        runtime.assistantEventScope = scope;
      }),
    });
    const actions = createRuntimeSlice(runtime, createDeps({
      onBridgeEvent: vi.fn()
        .mockImplementationOnce((handler) => {
          oldHandler = handler;
          return { ready: Promise.resolve(true), unsubscribe: oldUnsubscribe };
        })
        .mockImplementationOnce((handler) => {
          replacementHandler = handler;
          return { ready: replacementReady.promise, unsubscribe: replacementUnsubscribe };
        }),
      onRuntimeReconnect: vi.fn(() => ({ ready: Promise.resolve(true), unsubscribe: vi.fn() })),
    }));

    await actions.initializeEvents();
    const rebinding = actions.rebindBridgeEventScope('/repo/other');
    await Promise.resolve();

    expect(oldUnsubscribe).not.toHaveBeenCalled();
    oldHandler({ type: 'turn/terminal', payload: { eventId: 'during-rebind-old' } });
    replacementHandler({ type: 'turn/terminal', payload: { eventId: 'during-rebind-new' } });
    expect(runtime.handleBridgeEvent).toHaveBeenCalledTimes(1);
    expect(runtime.handleBridgeEvent).toHaveBeenLastCalledWith(expect.objectContaining({
      payload: expect.objectContaining({ eventId: 'during-rebind-old' }),
    }));

    replacementReady.resolve(true);
    await expect(rebinding).resolves.toBe(true);

    expect(runtime.assistantEventScope).toBe('/repo/other');
    expect(runtime.bridgeEventScopeGeneration).toBe(1);
    expect(oldUnsubscribe).toHaveBeenCalledTimes(1);
    oldHandler({ type: 'turn/terminal', payload: { eventId: 'after-rebind-old' } });
    replacementHandler({ type: 'turn/terminal', payload: { eventId: 'after-rebind-new' } });
    expect(runtime.handleBridgeEvent).toHaveBeenCalledTimes(2);
    expect(runtime.handleBridgeEvent).toHaveBeenLastCalledWith(expect.objectContaining({
      payload: expect.objectContaining({ eventId: 'after-rebind-new' }),
    }));
  });

  it('rebinds the first authoritative scope without inventing a previous scope', async () => {
    const onBridgeEvent = vi.fn(() => ({ ready: Promise.resolve(true), unsubscribe: vi.fn() }));
    let runtime;
    runtime = createRuntime({
      assistantEventScope: '',
      currentChatCwd: vi.fn(() => '.'),
      activateAssistantEventScope: vi.fn((scope) => {
        runtime.assistantEventScope = scope;
      }),
    });
    const actions = createRuntimeSlice(runtime, createDeps({ onBridgeEvent }));

    await expect(actions.rebindBridgeEventScope('/repo/bootstrap')).resolves.toBe(true);

    expect(runtime.assistantEventScope).toBe('/repo/bootstrap');
    expect(onBridgeEvent).toHaveBeenCalledOnce();
  });

  it('requires a real previous scope for public prepared project switches', async () => {
    const onBridgeEvent = vi.fn(() => ({ ready: Promise.resolve(true), unsubscribe: vi.fn() }));
    const runtime = createRuntime({
      assistantEventScope: '',
      currentChatCwd: vi.fn(() => '.'),
    });
    const actions = createRuntimeSlice(runtime, createDeps({ onBridgeEvent }));

    await expect(actions.prepareBridgeEventScope('/repo/other')).rejects.toThrow(
      'runtime bridge previous scope is required',
    );

    expect(onBridgeEvent).not.toHaveBeenCalled();
  });

  it('restores the prepared scope and continues delivering events from the previous project', async () => {
    const handlers = [];
    let runtime;
    runtime = createRuntime({
      activateAssistantEventScope: vi.fn((scope) => {
        runtime.assistantEventScope = scope;
      }),
    });
    createRuntimeSlice(runtime, createDeps({
      onBridgeEvent: vi.fn((handler) => {
        handlers.push(handler);
        return { ready: Promise.resolve(true), unsubscribe: vi.fn() };
      }),
    }));

    const prepared = await runtime.prepareBridgeEventScope('/repo/other');
    await expect(runtime.restorePreparedBridgeEventScope(prepared)).resolves.toBe(true);

    expect(runtime.assistantEventScope).toBe('/repo/app');
    handlers.at(-1)({ type: 'turn/terminal', payload: { eventId: 'restored-old-scope' } });
    expect(runtime.handleBridgeEvent).toHaveBeenLastCalledWith(expect.objectContaining({
      payload: expect.objectContaining({ eventId: 'restored-old-scope' }),
    }));
  });

  it('rejects a prepared scope that only provides the legacy rebindGeneration field', async () => {
    const runtime = createRuntime({ bridgeScopeRebindGeneration: 1 });
    createRuntimeSlice(runtime, createDeps());

    await expect(runtime.restorePreparedBridgeEventScope({
      abort: vi.fn(),
      previousScope: '/repo/app',
      rebindGeneration: 1,
    })).rejects.toThrow('runtime prepared bridge scope is invalid');
  });

  it('generation-fences a stale restore after a newer bridge scope wins', async () => {
    let runtime;
    runtime = createRuntime({
      activateAssistantEventScope: vi.fn((scope) => {
        runtime.assistantEventScope = scope;
      }),
    });
    const actions = createRuntimeSlice(runtime, createDeps({
      onBridgeEvent: vi.fn(() => ({ ready: Promise.resolve(true), unsubscribe: vi.fn() })),
    }));

    const prepared = await runtime.prepareBridgeEventScope('/repo/older');
    await actions.rebindBridgeEventScope('/repo/newer');

    await expect(runtime.restorePreparedBridgeEventScope(prepared)).resolves.toBe(false);
    expect(runtime.assistantEventScope).toBe('/repo/newer');
  });

  it('rejects a prepared scope that only contains the retired generation field', async () => {
    const runtime = createRuntime({ bridgeScopeRebindGeneration: 1 });
    createRuntimeSlice(runtime, createDeps());

    await expect(runtime.restorePreparedBridgeEventScope({
      abort: vi.fn(),
      previousScope: '/repo/app',
      rebindGeneration: 1,
    })).rejects.toThrow('runtime prepared bridge scope is invalid');
  });
});

describe('runtime slice preference validation', () => {
  it('rejects malformed active provider before applying bootstrap state', async () => {
    getPreference.mockReset();
    getPreference.mockResolvedValue('codex');
    const getValidatedPreference = vi.fn().mockRejectedValue(
      new Error('invalid UI preference response for settings.provider.active'),
    );
    const runtime = createRuntime({
      applyProjects: vi.fn(),
      applySnapshot: vi.fn(),
      cacheSidebarSnapshot: vi.fn(),
      loadProviderConfig: vi.fn(),
      set: vi.fn(),
    });
    const deps = createDeps({
      getPreference: getValidatedPreference,
      getProjects: vi.fn().mockResolvedValue([]),
      getSidebarState: vi.fn().mockResolvedValue({}),
      getWindowBootstrap: vi.fn().mockResolvedValue({ snapshot: { cwd: '/repo/app', page: 'chat' } }),
      normalizeBootstrapPage: vi.fn((value) => value),
      normalizeBootstrapSnapshot: vi.fn((value) => value.snapshot),
      normalizePath: vi.fn((value) => value || ''),
      onBridgeEvent: vi.fn(() => ({ ready: Promise.resolve(true), unsubscribe: vi.fn() })),
      onRuntimeReconnect: vi.fn(() => ({ ready: Promise.resolve(true), unsubscribe: vi.fn() })),
      providerActivePreferenceKey: 'settings.provider.active',
      readConfig: vi.fn().mockResolvedValue({ cwd: '/repo/app' }),
      requireActiveProviderPreference: vi.fn(() => 'codex'),
    });
    const actions = createRuntimeSlice(runtime, deps);
    runtime.get.mockImplementation(() => ({ initializeEvents: actions.initializeEvents }));

    await expect(actions.bootstrap()).rejects.toThrow(
      'invalid UI preference response for settings.provider.active',
    );

    expect(getValidatedPreference).toHaveBeenCalledOnce();
    expect(getValidatedPreference).toHaveBeenCalledWith({ cwd: '/repo/app', key: 'settings.provider.active' });
    expect(getPreference).not.toHaveBeenCalled();
    expect(runtime.loadProviderConfig).not.toHaveBeenCalled();
    expect(runtime.set).not.toHaveBeenCalledWith(expect.objectContaining({ provider: 'codex' }));
  });
});
