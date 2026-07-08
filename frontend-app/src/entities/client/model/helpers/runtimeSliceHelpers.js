function subscriptionUnsubscribe(subscription, label) {
  if (typeof subscription === 'function') return subscription;
  if (subscription && typeof subscription.unsubscribe === 'function') return subscription.unsubscribe;
  throw new Error(`${label} unsubscribe handler is required`);
}

function pendingRuntimeSubscriptions(runtime) {
  if (!runtime.pendingRuntimeSubscriptions) runtime.pendingRuntimeSubscriptions = new Set();
  return runtime.pendingRuntimeSubscriptions;
}

function clearRuntimeSubscriptionKey(runtime, key, unsubscribe) {
  if (runtime[key] === unsubscribe || !runtime[key]) runtime[key] = null;
}

function handleRuntimeSubscriptionReady(runtime, key, pending, unsubscribe, ready) {
  runtime.pendingRuntimeSubscriptions?.delete(pending);
  if (!pending.active) return;
  if (ready !== true) {
    clearRuntimeSubscriptionKey(runtime, key, unsubscribe);
    return;
  }
  if (runtime[key] && runtime[key] !== unsubscribe) {
    unsubscribe();
    return;
  }
  runtime[key] = unsubscribe;
}

function handleRuntimeSubscriptionFailure(context, error) {
  const { runtime, key, label, pending, unsubscribe } = context;
  runtime.pendingRuntimeSubscriptions?.delete(pending);
  if (runtime[key] === unsubscribe) runtime[key] = null;
  if (pending.active) runtime.addWarning('error', `${label}.failed`, { error: error?.message || String(error) });
}

export function trackRuntimeSubscription(runtime, key, subscription, label) {
  const unsubscribe = subscriptionUnsubscribe(subscription, label);
  if (subscription?.ready === undefined) {
    runtime[key] = unsubscribe;
    return;
  }
  if (!subscription.ready || typeof subscription.ready.then !== 'function') {
    throw new Error(`${label} ready promise is required`);
  }
  const pending = { unsubscribe, active: true };
  pendingRuntimeSubscriptions(runtime).add(pending);
  void subscription.ready.then((ready) => {
    handleRuntimeSubscriptionReady(runtime, key, pending, unsubscribe, ready);
  }).catch((error) => {
    handleRuntimeSubscriptionFailure({ runtime, key, label, pending, unsubscribe }, error);
  });
}

export function handleRuntimeReconnect(runtime, retryBootstrapAfterReconnect) {
  const { activeThreadId, bootstrapStatus } = runtime.get();
  if (bootstrapStatus === 'loading') {
    runtime.bootstrapRetryAfterReconnect = true;
    return;
  }
  if (bootstrapStatus !== 'ready') {
    retryBootstrapAfterReconnect();
    return;
  }
  if (activeThreadId) void runtime.get().syncThreadState(activeThreadId, { includeDiff: true, preserveActiveThreadId: true });
}

export function clearPendingRuntimeSubscriptions(runtime) {
  if (!runtime.pendingRuntimeSubscriptions) return;
  for (const pending of runtime.pendingRuntimeSubscriptions) {
    pending.active = false;
    pending.unsubscribe();
  }
  runtime.pendingRuntimeSubscriptions.clear();
}

export function clearRuntimeUnsubscribe(runtime, key) {
  if (!runtime[key]) return;
  runtime[key]();
  runtime[key] = null;
}

function retryBootstrapAfterFailedReconnect(runtime) {
  if (!runtime.bootstrapRetryAfterReconnect) return;
  runtime.bootstrapRetryAfterReconnect = false;
  void runtime.get().bootstrap().catch((retryError) => {
    runtime.addWarning('error', 'app.bootstrap.reconnect_failed', { error: retryError?.message || String(retryError) });
  });
}

export function handleBootstrapError(runtime, error) {
  runtime.set({ bootstrapStatus: 'failed', error: error.message });
  runtime.addWarning('error', 'app.bootstrap.failed', { error: error.message });
  retryBootstrapAfterFailedReconnect(runtime);
}

export function buildBootstrapState({ cwd, scopedCwd, activeProvider, bootstrapPage }) {
  const pageState = bootstrapPage ? { activePage: bootstrapPage } : {};
  return {
    cwd,
    projectScopeCwd: scopedCwd,
    activeProject: scopedCwd,
    provider: activeProvider,
    ...pageState,
  };
}

export function threadStateLoadingPatch(state, id, loading) {
  return {
    threadStateLoadingByThread: {
      ...state.threadStateLoadingByThread,
      [id]: loading,
    },
  };
}

export function markThreadDiffReady(runtime, id) {
  runtime.set((state) => ({
    threadDiffReadyByThread: {
      ...state.threadDiffReadyByThread,
      [id]: true,
    },
  }));
}
