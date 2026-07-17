function requiredUnsubscribe(subscription, label) {
  if (typeof subscription === 'function') return subscription;
  if (subscription && typeof subscription.unsubscribe === 'function') return subscription.unsubscribe;
  throw new Error(`${label} unsubscribe handler is required`);
}

function onceUnsubscribe(unsubscribe) {
  let active = true;
  return () => {
    if (!active) return;
    active = false;
    unsubscribe();
  };
}

export function trackRuntimeSubscription(runtime, subscription, label, generation) {
  const committedUnsubscribe = requiredUnsubscribe(subscription, label);
  const closeNativeSubscription = onceUnsubscribe(committedUnsubscribe);
  const pending = { generation, unsubscribe: null, active: true };
  const cancel = () => {
    pending.active = false;
    runtime.pendingRuntimeSubscriptions.delete(pending);
    closeNativeSubscription();
  };
  const commit = () => {
    if (!pending.active || generation !== runtime.eventInitializationGeneration) {
      throw new Error('runtime event initialization superseded');
    }
    pending.active = false;
    runtime.pendingRuntimeSubscriptions.delete(pending);
    return committedUnsubscribe;
  };
  pending.unsubscribe = cancel;
  runtime.pendingRuntimeSubscriptions.add(pending);
  const ready = Promise.resolve(subscription?.ready ?? true)
    .then((value) => {
      if (!pending.active || generation !== runtime.eventInitializationGeneration) {
        throw new Error('runtime event initialization superseded');
      }
      if (value !== true) throw new Error(`${label} unavailable`);
      return true;
    })
    .catch((error) => {
      cancel();
      throw error;
    });
  return { commit, ready, unsubscribe: cancel };
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
  runBackgroundAction('provider.reconnect.bootstrap-deferred', () => runtime.get().bootstrap().catch((retryError) => {
    runtime.addWarning('error', 'app.bootstrap.reconnect_failed', { error: 'background action failure; see Health diagnostic ID' });
    throw retryError;
  }));
}

export function handleBootstrapError(runtime, _error) {
  runtime.set({ bootstrapStatus: 'failed', error: '连接后端失败，请重试。' });
  runtime.addWarning('error', 'app.bootstrap.failed', { error: 'background action failure; see Health diagnostic ID' });
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
import { runBackgroundAction } from '../../../../shared/ui/runUIAction.js';
