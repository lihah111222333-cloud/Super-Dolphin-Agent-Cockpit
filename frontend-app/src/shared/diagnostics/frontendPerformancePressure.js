const FRONTEND_PERFORMANCE_POLICY = Object.freeze({
  startupGraceMs: 15_000,
  resumeGraceMs: 5_000,
  cooldownMs: 600_000,
  longTaskMs: 200,
  eventLoopLagMs: 150,
  consecutiveSamples: 3,
  heapRatio: 0.85,
});

const REPORTER_CONTRACT_FAILURE = 'frontend.performance.reporter_contract_failed';

function lagBucket(lagMs) {
  if (lagMs >= 600) return '600_plus';
  if (lagMs >= 300) return '300_599';
  return '150_299';
}

function heapRatioBucket(ratio) {
  if (ratio >= 0.95) return '0.95_1.00';
  if (ratio >= 0.9) return '0.90_0.94';
  return '0.85_0.89';
}

function requireFunction(owner, key) {
  const value = owner?.[key];
  if (typeof value !== 'function') {
    throw new TypeError(`frontend performance pressure requires ${key}`);
  }
  return value.bind(owner);
}

function normalizeObserverContract(observer) {
  if (observer === null) return null;
  if (
    !observer
    || typeof observer !== 'object'
    || Array.isArray(observer)
    || typeof observer.disconnect !== 'function'
  ) {
    throw new TypeError('frontend performance pressure observerFactory returned an invalid observer');
  }
  return observer;
}

function normalizeDependencies(dependencies) {
  return {
    now: requireFunction(dependencies?.clock, 'now'),
    setTimer: requireFunction(dependencies?.scheduler, 'setTimeout'),
    clearTimer: requireFunction(dependencies?.scheduler, 'clearTimeout'),
    isVisible: requireFunction(dependencies?.visibility, 'isVisible'),
    subscribeVisibility: requireFunction(dependencies?.visibility, 'subscribe'),
    isFocused: requireFunction(dependencies?.focus, 'isFocused'),
    subscribeFocus: requireFunction(dependencies?.focus, 'subscribe'),
    reporter: requireFunction(dependencies, 'reporter'),
    onContractFailure: requireFunction(dependencies, 'onContractFailure'),
    observerFactory: requireFunction(dependencies, 'observerFactory'),
    heapSample: dependencies.heap === null
      ? null
      : requireFunction(dependencies.heap, 'sample'),
  };
}

function createPerformancePressureRuntime(dependencies, policy) {
  const ports = normalizeDependencies(dependencies);
  return {
    ...ports,
    policy,
    cooldownUntil: new Map(),
    pending: new Set(),
    unsubscribers: [],
    stopped: false,
    timerID: undefined,
    observer: null,
    longTaskSupported: false,
    heapSupported: ports.heapSample !== null,
    active: Boolean(ports.isVisible() && ports.isFocused()),
    graceUntil: ports.now() + policy.startupGraceMs,
    eventLoopConsecutive: 0,
    heapConsecutive: 0,
  };
}

function isReady(runtime) {
  return !runtime.stopped && runtime.active && runtime.now() >= runtime.graceUntil;
}

function failReport(runtime, category) {
  runtime.pending.delete(category);
  if (runtime.stopped) return;
  runtime.onContractFailure(REPORTER_CONTRACT_FAILURE);
}

function settleReport(runtime, category, accepted) {
  runtime.pending.delete(category);
  if (runtime.stopped) return;
  if (accepted !== true) {
    runtime.onContractFailure(REPORTER_CONTRACT_FAILURE);
    return;
  }
  runtime.cooldownUntil.set(category, runtime.now() + runtime.policy.cooldownMs);
}

function report(runtime, category, event) {
  const cooldownUntil = runtime.cooldownUntil.get(category) ?? 0;
  if (!isReady(runtime) || runtime.pending.has(category) || runtime.now() < cooldownUntil) return;
  runtime.pending.add(category);
  let result;
  try {
    result = runtime.reporter(event);
  }
  catch {
    failReport(runtime, category);
    return;
  }
  void Promise.resolve(result)
    .then((accepted) => settleReport(runtime, category, accepted))
    .catch(() => failReport(runtime, category));
}

function resetConsecutiveSamples(runtime) {
  runtime.eventLoopConsecutive = 0;
  runtime.heapConsecutive = 0;
}

function refreshActiveState(runtime) {
  const nextActive = Boolean(runtime.isVisible() && runtime.isFocused());
  if (!runtime.active && nextActive) {
    runtime.graceUntil = Math.max(
      runtime.graceUntil,
      runtime.now() + runtime.policy.resumeGraceMs,
    );
  }
  runtime.active = nextActive;
  if (!runtime.active) resetConsecutiveSamples(runtime);
}

function reportMissingCapability(runtime, capability) {
  report(runtime, `capability.${capability}`, {
    phase: 'frontend.performance.capability_absent',
    status: 'ok',
    metadata: { capability },
  });
}

function reportCapabilities(runtime) {
  if (!runtime.longTaskSupported) reportMissingCapability(runtime, 'longtask');
  if (!runtime.heapSupported) reportMissingCapability(runtime, 'heap');
}

function sampleHeap(runtime) {
  if (!runtime.heapSample) return;
  const sample = runtime.heapSample();
  const used = Number(sample?.used);
  const total = Number(sample?.total);
  const ratio = total > 0 ? used / total : Number.NaN;
  if (!Number.isFinite(ratio) || ratio < runtime.policy.heapRatio) {
    runtime.heapConsecutive = 0;
    return;
  }
  runtime.heapConsecutive += 1;
  if (runtime.heapConsecutive < runtime.policy.consecutiveSamples) return;
  runtime.heapConsecutive = 0;
  report(runtime, 'heap', {
    phase: 'frontend.performance.heap_pressure',
    status: 'slow',
    metadata: { heap_ratio_bucket: heapRatioBucket(ratio) },
  });
}

function sampleEventLoop(runtime, lagMs) {
  if (lagMs < runtime.policy.eventLoopLagMs) {
    runtime.eventLoopConsecutive = 0;
    return;
  }
  runtime.eventLoopConsecutive += 1;
  if (runtime.eventLoopConsecutive < runtime.policy.consecutiveSamples) return;
  runtime.eventLoopConsecutive = 0;
  report(runtime, 'eventLoop', {
    phase: 'frontend.performance.event_loop_pressure',
    status: 'slow',
    duration_ms: Math.round(lagMs),
    metadata: { lag_bucket: lagBucket(lagMs) },
  });
}

function scheduleSample(runtime) {
  const expectedAt = runtime.now() + runtime.policy.eventLoopLagMs;
  runtime.timerID = runtime.setTimer(() => runSample(runtime, expectedAt), runtime.policy.eventLoopLagMs);
}

function runSample(runtime, expectedAt) {
  if (runtime.stopped) return;
  if (!isReady(runtime)) {
    resetConsecutiveSamples(runtime);
    scheduleSample(runtime);
    return;
  }
  reportCapabilities(runtime);
  sampleEventLoop(runtime, Math.max(0, runtime.now() - expectedAt));
  sampleHeap(runtime);
  scheduleSample(runtime);
}

function handleLongTasks(runtime, entries) {
  if (!isReady(runtime) || !Array.isArray(entries)) return;
  const durations = entries
    .map((entry) => Number(entry?.duration))
    .filter((duration) => Number.isFinite(duration) && duration >= runtime.policy.longTaskMs);
  if (durations.length === 0) return;
  const totalMS = durations.reduce((total, duration) => total + duration, 0);
  const maxMS = Math.max(...durations);
  report(runtime, 'longTask', {
    phase: 'frontend.performance.long_task_pressure',
    status: 'slow',
    duration_ms: Math.round(maxMS),
    metadata: {
      count: durations.length,
      total_ms: Math.round(totalMS),
      max_ms: Math.round(maxMS),
    },
  });
}

function stopRuntime(runtime) {
  if (runtime.stopped) return;
  runtime.stopped = true;
  let cleanupError;
  const cleanup = (callback) => {
    try {
      callback();
    }
    catch (error) {
      cleanupError ??= error;
    }
  };
  if (runtime.timerID !== undefined) {
    const timerID = runtime.timerID;
    runtime.timerID = undefined;
    cleanup(() => runtime.clearTimer(timerID));
  }
  runtime.unsubscribers.splice(0).reverse().forEach((unsubscribe) => cleanup(unsubscribe));
  if (runtime.observer !== null) {
    const observer = runtime.observer;
    runtime.observer = null;
    cleanup(() => observer.disconnect());
  }
  runtime.pending.clear();
  return cleanupError;
}

function stopRuntimeOrThrow(runtime) {
  const cleanupError = stopRuntime(runtime);
  if (cleanupError !== undefined) throw cleanupError;
}

function registerSubscription(runtime, subscribe, listener, owner) {
  const unsubscribe = subscribe(listener);
  if (typeof unsubscribe !== 'function') {
    throw new TypeError(`frontend performance pressure ${owner} subscribe must return a function`);
  }
  runtime.unsubscribers.push(unsubscribe);
}

function startFrontendPerformancePressure(dependencies) {
  const runtime = createPerformancePressureRuntime(dependencies, FRONTEND_PERFORMANCE_POLICY);
  try {
    runtime.observer = normalizeObserverContract(
      runtime.observerFactory((entries) => handleLongTasks(runtime, entries)),
    );
    runtime.longTaskSupported = runtime.observer !== null;
    registerSubscription(
      runtime,
      runtime.subscribeVisibility,
      () => refreshActiveState(runtime),
      'visibility',
    );
    registerSubscription(
      runtime,
      runtime.subscribeFocus,
      () => refreshActiveState(runtime),
      'focus',
    );
    scheduleSample(runtime);
  }
  catch (error) {
    // The initialization error is authoritative; cleanup still attempts every acquired resource.
    stopRuntime(runtime);
    throw error;
  }
  return Object.freeze({
    stop: () => stopRuntimeOrThrow(runtime),
    capabilities: Object.freeze({
      longTask: runtime.longTaskSupported,
      heap: runtime.heapSupported,
    }),
  });
}

export { FRONTEND_PERFORMANCE_POLICY, startFrontendPerformancePressure };
