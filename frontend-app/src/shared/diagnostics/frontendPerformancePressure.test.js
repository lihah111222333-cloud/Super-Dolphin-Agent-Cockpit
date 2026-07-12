import { describe, expect, it, vi } from 'vitest';
import {
  FRONTEND_PERFORMANCE_POLICY,
  startFrontendPerformancePressure,
} from './frontendPerformancePressure.js';

function createHarness({
  focused = true,
  heapSample = { used: 10, total: 100 },
  longTaskSupported = true,
  reporter = vi.fn(() => true),
  visible = true,
} = {}) {
  let now = 0;
  let nextTimerID = 1;
  let observerCallback;
  let observerDisconnectCount = 0;
  let currentHeapSample = heapSample;
  let currentVisible = visible;
  let currentFocused = focused;
  const timers = new Map();
  const visibilityListeners = new Set();
  const focusListeners = new Set();
  const onContractFailure = vi.fn();

  const runDueTimers = () => {
    const due = [...timers.entries()]
      .filter(([, timer]) => timer.due <= now)
      .sort(([, left], [, right]) => left.due - right.due);
    due.forEach(([id, timer]) => {
      if (!timers.delete(id)) return;
      timer.callback();
    });
  };

  const dependencies = {
    clock: { now: () => now },
    scheduler: {
      setTimeout(callback, delayMs) {
        const id = nextTimerID;
        nextTimerID += 1;
        timers.set(id, { callback, due: now + delayMs });
        return id;
      },
      clearTimeout(id) {
        timers.delete(id);
      },
    },
    visibility: {
      isVisible: () => currentVisible,
      subscribe(listener) {
        visibilityListeners.add(listener);
        return () => visibilityListeners.delete(listener);
      },
    },
    focus: {
      isFocused: () => currentFocused,
      subscribe(listener) {
        focusListeners.add(listener);
        return () => focusListeners.delete(listener);
      },
    },
    observerFactory: longTaskSupported
      ? (callback) => {
        observerCallback = callback;
        return {
          disconnect() {
            observerDisconnectCount += 1;
          },
        };
      }
      : () => null,
    heap: currentHeapSample === null
      ? null
      : { sample: () => currentHeapSample },
    reporter,
    onContractFailure,
  };

  return {
    dependencies,
    reporter,
    onContractFailure,
    advance(durationMs) {
      const target = now + durationMs;
      while (true) {
        const nextDue = Math.min(...[...timers.values()].map((timer) => timer.due));
        if (!Number.isFinite(nextDue) || nextDue > target) break;
        now = nextDue;
        runDueTimers();
      }
      now = target;
    },
    stall(durationMs) {
      now += durationMs;
      runDueTimers();
    },
    emitLongTasks(...durations) {
      observerCallback?.(durations.map((duration) => ({ duration })));
    },
    setVisible(nextVisible) {
      currentVisible = nextVisible;
      visibilityListeners.forEach((listener) => listener());
    },
    setFocused(nextFocused) {
      currentFocused = nextFocused;
      focusListeners.forEach((listener) => listener());
    },
    setHeapRatio(ratio) {
      currentHeapSample = { used: ratio * 100, total: 100 };
    },
    observerDisconnectCount: () => observerDisconnectCount,
    activeTimerCount: () => timers.size,
    listenerCount: () => visibilityListeners.size + focusListeners.size,
  };
}

async function flushReporter() {
  await Promise.resolve();
  await Promise.resolve();
}

describe('frontend performance pressure policy', () => {
  it('keeps the accepted thresholds in one immutable owner', () => {
    expect(FRONTEND_PERFORMANCE_POLICY).toEqual({
      startupGraceMs: 15_000,
      resumeGraceMs: 5_000,
      cooldownMs: 600_000,
      longTaskMs: 200,
      eventLoopLagMs: 150,
      consecutiveSamples: 3,
      heapRatio: 0.85,
    });
    expect(Object.isFrozen(FRONTEND_PERFORMANCE_POLICY)).toBe(true);
  });

  it('enforces startup and resume grace while ignoring hidden or unfocused samples', async () => {
    const harness = createHarness();
    const monitor = startFrontendPerformancePressure(harness.dependencies);

    harness.advance(14_999);
    harness.emitLongTasks(250);
    expect(harness.reporter).not.toHaveBeenCalled();
    harness.advance(1);
    harness.emitLongTasks(250);
    await flushReporter();
    expect(harness.reporter).toHaveBeenCalledTimes(1);

    harness.setVisible(false);
    harness.emitLongTasks(300);
    harness.setVisible(true);
    harness.advance(4_999);
    harness.emitLongTasks(300);
    expect(harness.reporter).toHaveBeenCalledTimes(1);
    harness.advance(1);
    harness.emitLongTasks(300);
    await flushReporter();
    expect(harness.reporter).toHaveBeenCalledTimes(1);

    harness.setFocused(false);
    harness.advance(600_000);
    harness.emitLongTasks(300);
    expect(harness.reporter).toHaveBeenCalledTimes(1);
    harness.setFocused(true);
    harness.advance(5_000);
    harness.emitLongTasks(300);
    await flushReporter();
    expect(harness.reporter).toHaveBeenCalledTimes(2);
    monitor.stop();
  });

  it('does not let an early resume shorten the startup grace', async () => {
    const harness = createHarness({ visible: false });
    const monitor = startFrontendPerformancePressure(harness.dependencies);

    harness.advance(1_000);
    harness.setVisible(true);
    harness.advance(5_000);
    harness.emitLongTasks(250);
    harness.advance(8_999);
    harness.emitLongTasks(250);
    expect(harness.reporter).not.toHaveBeenCalled();
    harness.advance(1);
    harness.emitLongTasks(250);
    await flushReporter();
    expect(harness.reporter).toHaveBeenCalledTimes(1);
    monitor.stop();
  });

  it('aggregates only long tasks at or above 200ms and applies a per-category 600s cooldown', async () => {
    const harness = createHarness();
    const monitor = startFrontendPerformancePressure(harness.dependencies);
    harness.advance(15_000);

    harness.emitLongTasks(199, 200, 420);
    await flushReporter();
    expect(harness.reporter).toHaveBeenCalledWith({
      phase: 'frontend.performance.long_task_pressure',
      status: 'slow',
      duration_ms: 420,
      metadata: { count: 2, total_ms: 620, max_ms: 420 },
    });
    harness.emitLongTasks(500);
    await flushReporter();
    expect(harness.reporter).toHaveBeenCalledTimes(1);
    harness.advance(599_999);
    harness.emitLongTasks(500);
    await flushReporter();
    expect(harness.reporter).toHaveBeenCalledTimes(1);
    harness.advance(1);
    harness.emitLongTasks(500);
    await flushReporter();
    expect(harness.reporter).toHaveBeenCalledTimes(2);
    monitor.stop();
  });

  it('requires three consecutive event-loop lag samples at 150ms', async () => {
    const harness = createHarness();
    const monitor = startFrontendPerformancePressure(harness.dependencies);
    harness.advance(15_000);

    harness.stall(300);
    harness.stall(300);
    expect(harness.reporter).not.toHaveBeenCalled();
    harness.advance(150);
    harness.stall(300);
    harness.stall(300);
    harness.stall(300);
    await flushReporter();
    expect(harness.reporter).toHaveBeenCalledWith({
      phase: 'frontend.performance.event_loop_pressure',
      status: 'slow',
      duration_ms: 150,
      metadata: { lag_bucket: '150_299' },
    });
    monitor.stop();
  });

  it('requires three consecutive heap samples at ratio 0.85', async () => {
    const harness = createHarness();
    const monitor = startFrontendPerformancePressure(harness.dependencies);
    harness.advance(15_000);
    harness.setHeapRatio(0.85);

    harness.advance(150);
    harness.advance(150);
    expect(harness.reporter).not.toHaveBeenCalled();
    harness.advance(150);
    await flushReporter();
    expect(harness.reporter).toHaveBeenCalledWith({
      phase: 'frontend.performance.heap_pressure',
      status: 'slow',
      metadata: { heap_ratio_bucket: '0.85_0.89' },
    });
    monitor.stop();
  });

  it('reports unsupported capabilities through the same bounded protocol', async () => {
    const harness = createHarness({ heapSample: null, longTaskSupported: false });
    const monitor = startFrontendPerformancePressure(harness.dependencies);
    expect(monitor.capabilities).toEqual({ longTask: false, heap: false });

    harness.advance(15_000);
    await flushReporter();
    expect(harness.reporter.mock.calls.map(([event]) => event)).toEqual([
      {
        phase: 'frontend.performance.capability_absent',
        status: 'ok',
        metadata: { capability: 'longtask' },
      },
      {
        phase: 'frontend.performance.capability_absent',
        status: 'ok',
        metadata: { capability: 'heap' },
      },
    ]);
    monitor.stop();
  });

  it('rolls back the observer and visibility subscription when focus subscription fails', () => {
    const harness = createHarness();
    const initializationError = new Error('focus subscribe failed');
    harness.dependencies.focus.subscribe = () => {
      throw initializationError;
    };

    let caughtError;
    try {
      startFrontendPerformancePressure(harness.dependencies);
    }
    catch (error) {
      caughtError = error;
    }
    expect(caughtError).toBe(initializationError);
    expect(harness.observerDisconnectCount()).toBe(1);
    expect(harness.listenerCount()).toBe(0);
    expect(harness.activeTimerCount()).toBe(0);
  });

  it('preserves the initialization error when observer cleanup also fails', () => {
    const harness = createHarness();
    const initializationError = new Error('focus subscribe failed');
    const disconnect = vi.fn(() => {
      throw new Error('secondary observer cleanup failed');
    });
    harness.dependencies.observerFactory = () => ({ disconnect });
    harness.dependencies.focus.subscribe = () => {
      throw initializationError;
    };

    let caughtError;
    try {
      startFrontendPerformancePressure(harness.dependencies);
    }
    catch (error) {
      caughtError = error;
    }
    expect(caughtError).toBe(initializationError);
    expect(disconnect).toHaveBeenCalledTimes(1);
    expect(harness.listenerCount()).toBe(0);
    expect(harness.activeTimerCount()).toBe(0);
  });

  it('rolls back the observer and both subscriptions when initial scheduling fails', () => {
    const harness = createHarness();
    const initializationError = new Error('scheduler failed');
    harness.dependencies.scheduler.setTimeout = () => {
      throw initializationError;
    };

    let caughtError;
    try {
      startFrontendPerformancePressure(harness.dependencies);
    }
    catch (error) {
      caughtError = error;
    }
    expect(caughtError).toBe(initializationError);
    expect(harness.observerDisconnectCount()).toBe(1);
    expect(harness.listenerCount()).toBe(0);
    expect(harness.activeTimerCount()).toBe(0);
  });

  it.each([
    ['null', null, false],
    ['undefined', undefined, true],
    ['false', false, true],
    ['empty object', {}, true],
  ])('enforces the exact observer capability contract for %s', (_label, observer, shouldThrow) => {
    const harness = createHarness();
    harness.dependencies.observerFactory = () => observer;

    if (!shouldThrow) {
      const monitor = startFrontendPerformancePressure(harness.dependencies);
      expect(monitor.capabilities.longTask).toBe(false);
      monitor.stop();
      return;
    }
    expect(() => startFrontendPerformancePressure(harness.dependencies)).toThrow(TypeError);
    expect(harness.activeTimerCount()).toBe(0);
    expect(harness.listenerCount()).toBe(0);
  });

  it.each([
    ['throw', vi.fn(() => { throw new Error('private reporter failure'); })],
    ['reject', vi.fn(() => Promise.reject(new Error('private reporter rejection')))],
    ['false', vi.fn(() => false)],
  ])('surfaces reporter %s as a fixed contract failure and does not enter cooldown', async (_label, reporter) => {
    const harness = createHarness({ reporter });
    const monitor = startFrontendPerformancePressure(harness.dependencies);
    harness.advance(15_000);

    harness.emitLongTasks(250);
    await flushReporter();
    harness.emitLongTasks(250);
    await flushReporter();
    expect(reporter).toHaveBeenCalledTimes(2);
    expect(harness.onContractFailure).toHaveBeenCalledTimes(2);
    expect(harness.onContractFailure).toHaveBeenNthCalledWith(
      1,
      'frontend.performance.reporter_contract_failed',
    );
    expect(JSON.stringify(harness.onContractFailure.mock.calls)).not.toContain('private reporter');
    monitor.stop();
  });

  it('deduplicates a category while its reporter contract is pending', async () => {
    let resolveReporter;
    const reporter = vi.fn(() => new Promise((resolve) => { resolveReporter = resolve; }));
    const harness = createHarness({ reporter });
    const monitor = startFrontendPerformancePressure(harness.dependencies);
    harness.advance(15_000);

    harness.emitLongTasks(250);
    harness.emitLongTasks(300);
    expect(reporter).toHaveBeenCalledTimes(1);
    resolveReporter(true);
    await flushReporter();
    monitor.stop();
  });

  it.each(['false', 'reject'])(
    'ignores a pending reporter %s settlement after stop',
    async (settlement) => {
      let settleReporter;
      const reporter = vi.fn(() => new Promise((resolve, reject) => {
        settleReporter = settlement === 'false' ? () => resolve(false) : () => reject(new Error('private'));
      }));
      const harness = createHarness({ reporter });
      const monitor = startFrontendPerformancePressure(harness.dependencies);
      harness.advance(15_000);
      harness.emitLongTasks(250);
      expect(reporter).toHaveBeenCalledTimes(1);

      monitor.stop();
      settleReporter();
      await flushReporter();
      expect(harness.onContractFailure).not.toHaveBeenCalled();
    },
  );

  it('stops observers, timers, subscriptions and future emissions idempotently', async () => {
    const harness = createHarness();
    const first = startFrontendPerformancePressure(harness.dependencies);
    const second = startFrontendPerformancePressure(harness.dependencies);
    expect(harness.listenerCount()).toBe(4);
    expect(harness.activeTimerCount()).toBe(2);

    first.stop();
    first.stop();
    second.stop();
    second.stop();
    expect(harness.observerDisconnectCount()).toBe(2);
    expect(harness.listenerCount()).toBe(0);
    expect(harness.activeTimerCount()).toBe(0);
    harness.advance(30_000);
    harness.emitLongTasks(500);
    await flushReporter();
    expect(harness.reporter).not.toHaveBeenCalled();
  });
});
