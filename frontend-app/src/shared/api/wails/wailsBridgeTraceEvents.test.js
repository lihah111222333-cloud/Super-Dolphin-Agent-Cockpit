import { afterEach, describe, expect, it, vi } from 'vitest';

const runtimeModule = '/wails/runtime.js';

function traceEvent(traceID) {
  return {
    phase: 'frontend.warning',
    status: 'error',
    trace_id: traceID,
    error: 'unit failure',
  };
}

async function loadTraceEvents(runtime) {
  vi.resetModules();
  vi.doMock(runtimeModule, () => ({ Events: {}, ...runtime }));
  return import('./wailsBridgeTraceEvents.js');
}

describe('frontend trace ACK and retry queue', () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.doUnmock(runtimeModule);
    vi.restoreAllMocks();
  });

  it('retries the same rejected batch in order with one bounded retry timer', async () => {
    vi.useFakeTimers();
    const byID = vi.fn()
      .mockRejectedValueOnce(new Error('transport rejected'))
      .mockResolvedValueOnce({ enabled: true, recorded: 2, dropped: 0 });
    const bridge = await loadTraceEvents({ Call: { ByID: byID } });

    bridge.emitFrontendTraceEvent(traceEvent('trace-a'), { flush: false });
    bridge.emitFrontendTraceEvent(traceEvent('trace-b'), { flush: false });
    await bridge.flushFrontendTraceQueueForTest();

    const firstBatch = byID.mock.calls[0][2].events;
    expect(bridge.getFrontendTraceQueueHealth()).toMatchObject({
      queueLength: 2,
      retryPending: true,
      retryAttempt: 1,
      retryDelayMS: 100,
    });
    expect(vi.getTimerCount()).toBe(1);

    await vi.advanceTimersByTimeAsync(100);

    expect(byID).toHaveBeenCalledTimes(2);
    expect(byID.mock.calls[1][2].events).toEqual(firstBatch);
    expect(byID.mock.calls[1][2].events.map((event) => event.trace_id)).toEqual(['trace-a', 'trace-b']);
    expect(bridge.getFrontendTraceQueueHealth()).toMatchObject({
      queueLength: 0,
      acknowledged: 2,
      retryPending: false,
      retryAttempt: 0,
      retryDelayMS: 0,
    });
    expect(vi.getTimerCount()).toBe(0);
  });

  it('retains a batch on malformed ACK and enters disabled terminal state only on a strict disabled ACK', async () => {
    vi.useFakeTimers();
    const byID = vi.fn()
      .mockResolvedValueOnce({ enabled: true, recorded: 0, dropped: 0 })
      .mockResolvedValueOnce({ enabled: false, recorded: 0, dropped: 1, disabled_reason: 'unit disabled' });
    const bridge = await loadTraceEvents({ Call: { ByID: byID } });

    bridge.emitFrontendTraceEvent(traceEvent('trace-disabled'), { flush: false });
    await bridge.flushFrontendTraceQueueForTest();

    expect(bridge.getFrontendTraceQueueHealth()).toMatchObject({
      queueLength: 1,
      malformedACKs: 1,
      retryPending: true,
      disabled: false,
    });

    await vi.advanceTimersByTimeAsync(100);

    expect(bridge.getFrontendTraceQueueHealth()).toMatchObject({
      queueLength: 0,
      disabled: true,
      disabledReason: 'unit disabled',
      terminalDropped: 1,
      retryPending: false,
    });
    expect(bridge.emitFrontendTraceEvent(traceEvent('trace-after-disabled'), { flush: false })).toBe(false);
    expect(vi.getTimerCount()).toBe(0);
  });

  it('caps exponential retry backoff while retaining one timer', async () => {
    vi.useFakeTimers();
    const byID = vi.fn().mockResolvedValue({ enabled: true, recorded: 0, dropped: 0 });
    const bridge = await loadTraceEvents({ Call: { ByID: byID } });
    bridge.emitFrontendTraceEvent(traceEvent('trace-backoff'), { flush: false });

    await bridge.flushFrontendTraceQueueForTest();
    for (let attempt = 1; attempt <= 6; attempt += 1) {
      const expectedDelay = 100 * (2 ** (attempt - 1));
      expect(bridge.getFrontendTraceQueueHealth()).toMatchObject({
        retryDelayMS: expectedDelay,
        retryPending: true,
      });
      expect(vi.getTimerCount()).toBe(1);
      await vi.advanceTimersByTimeAsync(expectedDelay);
    }

    expect(bridge.getFrontendTraceQueueHealth()).toMatchObject({
      queueLength: 1,
      retryAttempt: 7,
      retryDelayMS: 5000,
      retryPending: true,
    });
    expect(vi.getTimerCount()).toBe(1);
  });

  it('retains the batch when runtime Call.ByID is missing', async () => {
    vi.useFakeTimers();
    const bridge = await loadTraceEvents({ Call: {} });

    bridge.emitFrontendTraceEvent(traceEvent('trace-no-by-id'), { flush: false });
    await bridge.flushFrontendTraceQueueForTest();

    expect(bridge.getFrontendTraceQueueHealth()).toMatchObject({
      queueLength: 1,
      failures: 1,
      retryPending: true,
      lastFailure: 'runtime Call.ByID is unavailable',
    });
    expect(vi.getTimerCount()).toBe(1);
  });

  it('accepts a strict partial-drop ACK without retrying recorded events', async () => {
    const byID = vi.fn().mockResolvedValue({
      enabled: true,
      recorded: 47,
      dropped: 3,
    });
    const bridge = await loadTraceEvents({ Call: { ByID: byID } });
    for (let index = 0; index < 50; index += 1) {
      bridge.emitFrontendTraceEvent(traceEvent(`trace-partial-${index}`), { flush: false });
    }

    await bridge.flushFrontendTraceQueueForTest();

    expect(byID).toHaveBeenCalledTimes(1);
    expect(bridge.getFrontendTraceQueueHealth()).toMatchObject({
      queueLength: 0,
      acknowledged: 47,
      serverDropped: 3,
      retryPending: false,
      malformedACKs: 0,
    });
  });

  it('preserves FIFO order after observable queue overflow', async () => {
    const byID = vi.fn((_methodID, _method, payload) => Promise.resolve({
      enabled: true,
      recorded: payload.events.length,
      dropped: 0,
    }));
    const bridge = await loadTraceEvents({ Call: { ByID: byID } });

    for (let index = 0; index < 501; index += 1) {
      bridge.emitFrontendTraceEvent(traceEvent(`trace-${index}`), { flush: false });
    }
    expect(bridge.getFrontendTraceQueueHealth()).toMatchObject({
      queueLength: 500,
      accepted: 501,
      overflowDropped: 1,
    });

    await bridge.flushFrontendTraceQueueForTest();

    expect(byID.mock.calls[0][2].events.map((event) => event.trace_id)).toEqual(
      Array.from({ length: 50 }, (_, index) => `trace-${index + 1}`),
    );
    expect(bridge.getFrontendTraceQueueHealth()).toMatchObject({
      queueLength: 450,
      acknowledged: 50,
      overflowDropped: 1,
    });
  });
});
