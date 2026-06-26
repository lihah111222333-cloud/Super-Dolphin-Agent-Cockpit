import { describe, expect, it, vi } from 'vitest';
import { createRuntimeSlice } from './runtimeSlice.js';

function createRuntime() {
  return {
    bridgeUnsubscribe: null,
    reconnectUnsubscribe: null,
    addWarning: vi.fn(),
    get: vi.fn(() => ({ activeThreadId: '', bootstrapStatus: 'ready' })),
    handleBridgeEvent: vi.fn(),
  };
}

function createDeps(overrides = {}) {
  return {
    isDagNodeStatusBridgeEvent: vi.fn(() => false),
    onBridgeEvent: vi.fn(),
    onRuntimeReconnect: vi.fn(() => vi.fn()),
    ...overrides,
  };
}

describe('runtime slice event lifecycle', () => {
  function deferredReady() {
    let resolve;
    let reject;
    const ready = new Promise((promiseResolve, promiseReject) => {
      resolve = promiseResolve;
      reject = promiseReject;
    });
    return { ready, resolve, reject };
  }

  it('clears failed bridge subscription readiness so initializeEvents can retry', async () => {
    const firstUnsubscribe = vi.fn();
    const secondUnsubscribe = vi.fn();
    const firstSubscription = {
      ready: Promise.resolve(false),
      unsubscribe: firstUnsubscribe,
    };
    const secondSubscription = {
      ready: Promise.resolve(true),
      unsubscribe: secondUnsubscribe,
    };
    const onBridgeEvent = vi.fn()
      .mockReturnValueOnce(firstSubscription)
      .mockReturnValueOnce(secondSubscription);
    const runtime = createRuntime();
    const actions = createRuntimeSlice(runtime, createDeps({ onBridgeEvent }));

    actions.initializeEvents();
    await firstSubscription.ready;

    expect(runtime.bridgeUnsubscribe).toBeNull();

    actions.initializeEvents();
    await secondSubscription.ready;

    expect(onBridgeEvent).toHaveBeenCalledTimes(2);
    expect(runtime.bridgeUnsubscribe).toBe(secondUnsubscribe);
  });

  it('records bridge unsubscribe only after subscription readiness resolves true', async () => {
    const bridgeReady = deferredReady();
    const bridgeUnsubscribe = vi.fn();
    const onBridgeEvent = vi.fn(() => ({
      ready: bridgeReady.ready,
      unsubscribe: bridgeUnsubscribe,
    }));
    const runtime = createRuntime();
    const actions = createRuntimeSlice(runtime, createDeps({ onBridgeEvent }));

    actions.initializeEvents();

    expect(runtime.bridgeUnsubscribe).toBeNull();

    bridgeReady.resolve(true);
    await bridgeReady.ready;

    expect(runtime.bridgeUnsubscribe).toBe(bridgeUnsubscribe);
  });
});
