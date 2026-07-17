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
    bridgeSubscription = trackRuntimeSubscription(runtime, onBridgeEvent(runtime.handleBridgeEvent, {
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

  const destroy = () => {
    runtime.eventInitializationGeneration += 1;
    runtime.eventInitializationPromise = null;
    runtime.eventInitializationState = 'idle';
    clearPendingRuntimeSubscriptions(runtime);
    clearRuntimeUnsubscribe(runtime, 'bridgeUnsubscribe');
    clearRuntimeUnsubscribe(runtime, 'reconnectUnsubscribe');
    runtime.sequencesByThread.clear();
    runtime.composerDrafts.clear();
    runtime.sidebarSnapshotsByCwd.clear();
    runtime.sidebarRefreshesByCwd.clear();
    runtime.threadMessageGenerations.clear();
    runtime.threadSyncGenerations.clear();
    runtime.clearAssistantDeltaFlushTimer?.();
    runtime.assistantDeltaBuffers.clear();
    runtime.sealedTurnTerminals?.clear();
    runtime.observedTurnByThread?.clear();
    runtime.retiredTurnRefs?.clear();
    runtime.sidebarRefreshSeq += 1;
  };

  return { initializeEvents, destroy };
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
