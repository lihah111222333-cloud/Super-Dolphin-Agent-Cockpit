/*
 * runtime slice 管启动、bridge 订阅和线程同步。
 * React 组件只调用这里的动作，不直接碰 bridge 或 generation。
 */

import {
  buildBootstrapState,
  clearPendingRuntimeSubscriptions,
  clearRuntimeUnsubscribe,
  handleBootstrapError,
  handleRuntimeReconnect,
  markThreadDiffReady,
  threadStateLoadingPatch,
  trackRuntimeSubscription } from './helpers/runtimeSliceHelpers.js';
import { runBackgroundAction } from '../../../shared/ui/runUIAction.js';

export function createRuntimeSlice(runtime, deps) {
  return {
    ...createLifecycleActions(runtime, deps),
    ...createBootstrapActions(runtime, deps),
    ...createThreadSyncActions(runtime, deps),
  };
}

async function initializeRuntimeEventSubscriptions(runtime, deps, retryBootstrapAfterReconnect, generation) {
  const {
    isDagNodeStatusBridgeEvent,
    onBridgeEvent,
    onRuntimeReconnect,
  } = deps;
  let bridgeSubscription;
  let reconnectSubscription;
  try {
    if (generation !== runtime.eventInitializationGeneration) {
      throw new Error('runtime event initialization superseded');
    }
    const initialScope = currentChatScope(runtime);
    if (initialScope && initialScope !== '.') runtime.activateAssistantEventScope?.(initialScope);
    bridgeSubscription = trackRuntimeSubscription(runtime, onBridgeEvent(scopedBridgeEventHandler(runtime), {
      escalateCallbackError: (_error, evt) => isDagNodeStatusBridgeEvent(evt),
    }), 'runtime.bridge.subscribe', generation);
    reconnectSubscription = trackRuntimeSubscription(runtime, onRuntimeReconnect(() => runBackgroundAction(
      'provider.reconnect',
      () => handleRuntimeReconnect(runtime, retryBootstrapAfterReconnect),
    )), 'runtime.reconnect.subscribe', generation);
    await Promise.all([
      bridgeSubscription.ready,
      reconnectSubscription.ready,
    ]);
    if (generation !== runtime.eventInitializationGeneration) {
      throw new Error('runtime event initialization superseded');
    }
    runtime.bridgeUnsubscribe = bridgeSubscription.commit();
    runtime.reconnectUnsubscribe = reconnectSubscription.commit();
    runtime.eventInitializationState = 'ready';
    return true;
  }
  catch (error) {
    bridgeSubscription?.unsubscribe();
    reconnectSubscription?.unsubscribe();
    if (generation === runtime.eventInitializationGeneration) {
      runtime.eventInitializationGeneration += 1;
      runtime.eventInitializationState = 'idle';
    }
    throw error;
  }
}

function createLifecycleActions(runtime, deps) {
  const retryBootstrapAfterReconnect = () => {
    runBackgroundAction('provider.reconnect.bootstrap', () => runtime.get().bootstrap().catch((error) => {
      runtime.addWarning('error', 'app.bootstrap.reconnect_failed', { error: 'background action failure; see Health diagnostic ID' });
      throw error;
    }));
  };

  const initializeEvents = () => {
    /*
     * bridge event 只注册一次。
     * 重连后 ready 只同步当前线程，其他状态重新 bootstrap。
     */
    if (runtime.eventInitializationPromise) return runtime.eventInitializationPromise;
    if (runtime.eventInitializationState === 'ready'
      && runtime.bridgeUnsubscribe
      && runtime.reconnectUnsubscribe) {
      return Promise.resolve(true);
    }
    if (runtime.bridgeUnsubscribe || runtime.reconnectUnsubscribe) {
      clearRuntimeUnsubscribe(runtime, 'bridgeUnsubscribe');
      clearRuntimeUnsubscribe(runtime, 'reconnectUnsubscribe');
    }
    const generation = runtime.eventInitializationGeneration + 1;
    runtime.eventInitializationGeneration = generation;
    runtime.eventInitializationState = 'initializing';
    const initialization = initializeRuntimeEventSubscriptions(
      runtime,
      deps,
      retryBootstrapAfterReconnect,
      generation,
    ).finally(() => {
      if (runtime.eventInitializationPromise === initialization) {
        runtime.eventInitializationPromise = null;
      }
    });
    runtime.eventInitializationPromise = initialization;
    return initialization;
  };

  const prepareBridgeEventScope = async (scope) => {
    let normalizedScope = scope;
    if (!normalizedScope) normalizedScope = currentChatScope(runtime);
    runtime.assertAssistantEventScopeCapacity?.(normalizedScope);
    const rebindGeneration = runtime.bridgeScopeRebindGeneration + 1;
    const bridgeEventScopeGeneration = runtime.bridgeEventScopeGeneration + 1;
    runtime.bridgeScopeRebindGeneration = rebindGeneration;
    runtime.pendingBridgeScopeRebind?.unsubscribe();
    const subscription = trackRuntimeSubscription(
      runtime,
      deps.onBridgeEvent(scopedBridgeEventHandler(runtime, {
        scope: normalizedScope,
        generation: bridgeEventScopeGeneration,
        silentBeforeCommit: true,
      }), {
        escalateCallbackError: (_error, evt) => deps.isDagNodeStatusBridgeEvent(evt),
      }),
      'runtime.bridge.subscribe',
      runtime.eventInitializationGeneration,
    );
    let closed = false;
    let transition = null;
    const abort = () => {
      if (closed) return false;
      closed = true;
      subscription.unsubscribe();
      if (runtime.pendingBridgeScopeRebind === transition) runtime.pendingBridgeScopeRebind = null;
      return true;
    };
    const commit = () => {
      if (closed) throw new Error('runtime bridge scope preparation is no longer pending');
      if (rebindGeneration !== runtime.bridgeScopeRebindGeneration) {
        abort();
        throw new Error('runtime bridge scope rebind superseded');
      }
      const previousBridgeUnsubscribe = runtime.bridgeUnsubscribe;
      try {
        const bridgeUnsubscribe = subscription.commit();
        runtime.activateAssistantEventScope?.(normalizedScope);
        closed = true;
        runtime.bridgeEventScopeGeneration = bridgeEventScopeGeneration;
        runtime.bridgeUnsubscribe = bridgeUnsubscribe;
        if (runtime.pendingBridgeScopeRebind === transition) runtime.pendingBridgeScopeRebind = null;
        previousBridgeUnsubscribe?.();
        return true;
      }
      catch (error) {
        abort();
        throw error;
      }
    };
    const previousScope = runtime.assistantEventScope || currentChatScope(runtime);
    if (!previousScope || previousScope === '.') {
      abort();
      throw new Error('runtime bridge previous scope is required');
    }
    transition = {
      abort,
      commit,
      generation: rebindGeneration,
      previousScope,
      unsubscribe: abort,
    };
    runtime.pendingBridgeScopeRebind = transition;
    try {
      await subscription.ready;
      if (rebindGeneration !== runtime.bridgeScopeRebindGeneration) {
        abort();
        throw new Error('runtime bridge scope rebind superseded');
      }
      return transition;
    }
    catch (error) {
      abort();
      throw error;
    }
  };

  const rebindBridgeEventScope = async (scope) => {
    const transition = await prepareBridgeEventScope(scope);
    return transition.commit();
  };

  const restorePreparedBridgeEventScope = async (prepared) => {
    const generation = Number(prepared?.generation ?? prepared?.rebindGeneration);
    const previousScope = typeof prepared?.previousScope === 'string' ? prepared.previousScope : '';
    if (typeof prepared?.abort !== 'function'
      || !Number.isSafeInteger(generation)
      || generation < 1
      || !previousScope
      || previousScope === '.') {
      throw new Error('runtime prepared bridge scope is invalid');
    }
    if (generation !== runtime.bridgeScopeRebindGeneration) return false;
    const restoreGeneration = runtime.bridgeScopeRebindGeneration + 1;
    try {
      prepared.abort();
      await rebindBridgeEventScope(previousScope);
      return true;
    }
    catch (error) {
      if (restoreGeneration !== runtime.bridgeScopeRebindGeneration) return false;
      throw error;
    }
  };

  const destroy = () => {
    runtime.eventInitializationGeneration += 1;
    runtime.eventInitializationPromise = null;
    runtime.eventInitializationState = 'idle';
    clearPendingRuntimeSubscriptions(runtime);
    runtime.pendingBridgeScopeRebind?.unsubscribe();
    runtime.pendingBridgeScopeRebind = null;
    clearRuntimeUnsubscribe(runtime, 'bridgeUnsubscribe');
    clearRuntimeUnsubscribe(runtime, 'reconnectUnsubscribe');
    runtime.sequencesByThread.clear();
    runtime.patchGenerationsByThread?.clear();
    runtime.composerDrafts.clear();
    runtime.sidebarSnapshotsByCwd.clear();
    runtime.sidebarRefreshesByCwd.clear();
    runtime.threadMessageGenerations.clear();
    runtime.threadSyncGenerations.clear();
    runtime.clearAssistantDeltaFlushTimer?.();
    runtime.assistantDeltaBuffers.clear();
    runtime.turnTerminalStates?.clear();
    runtime.observedTurnByThread?.clear();
    runtime.retiredTurnRefs?.clear();
    runtime.assistantEventLedgersByScope?.clear();
    runtime.agentFailureNoticeLedger?.clear();
    runtime.sidebarRefreshSeq += 1;
  };

  Object.assign(runtime, {
    prepareBridgeEventScope,
    rebindBridgeEventScope,
    restorePreparedBridgeEventScope,
  });
  return {
    initializeEvents,
    prepareBridgeEventScope,
    rebindBridgeEventScope,
    restorePreparedBridgeEventScope,
    destroy,
  };
}

function scopedBridgeEventHandler(runtime, options = {}) {
  const generation = options.generation ?? runtime.bridgeEventScopeGeneration;
  let scope = options.scope ?? runtime.assistantEventScope;
  if (!scope) scope = currentChatScope(runtime);
  return (event) => {
    if (generation !== runtime.bridgeEventScopeGeneration || scope !== runtime.assistantEventScope) {
      if (options.silentBeforeCommit) return;
      runtime.addWarning('warn', 'bridge.event.scope_stale', { scope, generation });
      return;
    }
    runtime.handleBridgeEvent(event);
  };
}

function currentChatScope(runtime) {
  const scope = runtime.currentChatCwd?.();
  if (typeof scope !== 'string') return '';
  return scope;
}

function publishThreadSyncFailure(runtime, syncOptions, id, _error) {
  if (typeof syncOptions.shouldPublishFailure === 'function' && !syncOptions.shouldPublishFailure()) return;
  runtime.notifyAction('同步会话失败，请重试。', 'error', { threadId: id });
  runtime.addWarning('error', 'thread.sync.failed', { threadId: id, error: 'action failure; see Health diagnostic ID' });
}

function createBootstrapActions(runtime, deps) {
  const {
    getPreference,
    getProjects,
    getSidebarState,
    getWindowBootstrap,
    normalizeBootstrapPage,
    normalizeBootstrapSnapshot,
    normalizePath,
    providerActivePreferenceKey,
    readConfig,
    requireActiveProviderPreference,
  } = deps;

  const bootstrap = async () => {
    /*
     * bootstrap 会拿 cwd、窗口快照、项目列表和 provider。
     * cwd/provider 缺失就报错，后续页面都依赖它们。
     */
    runtime.set({ bootstrapStatus: 'loading' });
    try {
      await runtime.get().initializeEvents();
      const [config, rawWindowBootstrap] = await Promise.all([readConfig(), getWindowBootstrap()]);
      const cwd = normalizePath(config?.cwd);
      if (!cwd || cwd === '.') {
        throw new Error('frontend-app bootstrap cwd is required');
      }
      const windowSnapshot = normalizeBootstrapSnapshot(rawWindowBootstrap);
      const windowCwd = normalizePath(windowSnapshot.cwd);
      const scopedCwd = windowCwd || cwd;
      const bootstrapPage = normalizeBootstrapPage(windowSnapshot.page);
      if (runtime.assistantEventScope !== scopedCwd) {
        await runtime.rebindBridgeEventScope(scopedCwd);
      }
      const activeProvider = requireActiveProviderPreference(
        await getPreference({
          cwd: scopedCwd,
          key: providerActivePreferenceKey,
        }),
        'frontend-app bootstrap',
      );
      runtime.set(buildBootstrapState({ cwd, scopedCwd, activeProvider, bootstrapPage }));
      const [projects, sidebar] = await Promise.all([
        getProjects({ cwd: scopedCwd }),
        getSidebarState({ cwd: scopedCwd }),
        runtime.loadProviderConfig(scopedCwd, activeProvider),
      ]);
      runtime.applyProjects(projects, scopedCwd);
      runtime.cacheSidebarSnapshot(scopedCwd, sidebar);
      runtime.applySnapshot(sidebar, { preserveLiveBusyStatus: true });
      runtime.bootstrapRetryAfterReconnect = false;
      runtime.set({ bootstrapStatus: 'ready', error: '' });
    }
    catch (error) {
      handleBootstrapError(runtime, error);
      throw error;
    }
  };

  return { bootstrap };
}

function createThreadSyncActions(runtime, deps) {
  const {
    backendThreadIdForState,
    getThreadState,
    normalizeThreadId,
    shouldAutoLoadThreadConfig,
  } = deps;

  const nextThreadSyncGeneration = (id) => {
    const nextGeneration = (runtime.threadSyncGenerations.get(id) || 0) + 1;
    runtime.threadSyncGenerations.set(id, nextGeneration);
    return nextGeneration;
  };

  const isCurrentThreadSyncGeneration = (id, generation) => runtime.threadSyncGenerations.get(id) === generation;

  const setThreadStateLoading = (id, generation, loading) => {
    runtime.set((state) => {
      if (!isCurrentThreadSyncGeneration(id, generation)) return {};
      return threadStateLoadingPatch(state, id, loading);
    });
  };

  const syncThreadState = async (threadId, options = {}) => {
    /*
     * 同步线程时会并行拉快照和历史消息。
     * generation 防止旧请求写回，includeDiff 决定是否补右侧 diff。
     */
    const syncOptions = options && typeof options === 'object' ? options : {};
    const id = backendThreadIdForState(runtime.get(), threadId, { includeArchived: syncOptions.includeArchived === true });
    if (!id) return false;
    const cwd = runtime.requireCwd('thread.sync');
    const activeAtRequest = runtime.get().activeThreadId;
    const includeDiff = syncOptions.includeDiff !== false;
    const generation = nextThreadSyncGeneration(id);
    setThreadStateLoading(id, generation, true);
    try {
      const snapshotPromise = getThreadState({ cwd, threadId: id, includeDiff });
      const messagesPromise = runtime.startThreadMessagesLoad(id, syncOptions);
      const snapshot = await snapshotPromise;
      if (!isCurrentThreadSyncGeneration(id, generation)) {
        await messagesPromise;
        return true;
      }
      const activeChanged = normalizeThreadId(runtime.get().activeThreadId) !== normalizeThreadId(activeAtRequest);
      runtime.applySnapshot(snapshot, {
        preferredActiveThreadId: id,
        preserveActiveThreadId: activeChanged || syncOptions.preserveActiveThreadId === true,
        includeArchivedActiveThread: syncOptions.includeArchived === true,
        cacheSidebarThreads: false,
      });
      if (includeDiff) markThreadDiffReady(runtime, id);
      await messagesPromise;
      if (!activeChanged && shouldAutoLoadThreadConfig(runtime.get(), id)) await runtime.get().loadThreadConfig(id);
      return true;
    }
    catch (error) {
      if (!isCurrentThreadSyncGeneration(id, generation)) return false;
      publishThreadSyncFailure(runtime, syncOptions, id, error);
      return false;
    }
    finally {
      setThreadStateLoading(id, generation, false);
    }
  };

  const loadOlderThreadMessages = async (threadId, options = {}) => runtime.loadOlderThreadMessages(threadId, options);

  return { syncThreadState, loadOlderThreadMessages };
}
