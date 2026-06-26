/*
 * runtime slice 管启动、bridge 订阅和线程同步。
 * React 组件只调用这里的动作，不直接碰 bridge 或 generation。
 */

export function createRuntimeSlice(runtime, deps) {
  return {
    ...createLifecycleActions(runtime, deps),
    ...createBootstrapActions(runtime, deps),
    ...createThreadSyncActions(runtime, deps),
  };
}

function subscriptionUnsubscribe(subscription, label) {
  if (typeof subscription === 'function') return subscription;
  if (subscription && typeof subscription.unsubscribe === 'function') return subscription.unsubscribe;
  throw new Error(`${label} unsubscribe handler is required`);
}

function pendingRuntimeSubscriptions(runtime) {
  if (!runtime.pendingRuntimeSubscriptions) runtime.pendingRuntimeSubscriptions = new Set();
  return runtime.pendingRuntimeSubscriptions;
}

function trackRuntimeSubscription(runtime, key, subscription, label) {
  /*
   * Wails 事件订阅可能异步发现 runtime 尚不可用。
   * 只有 ready=true 后才记录 unsubscribe，destroy 会额外清理 pending 订阅。
   */
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
    runtime.pendingRuntimeSubscriptions?.delete(pending);
    if (!pending.active) return;
    if (ready === true) {
      if (runtime[key] && runtime[key] !== unsubscribe) {
        unsubscribe();
        return;
      }
      runtime[key] = unsubscribe;
      return;
    }
    if (runtime[key] === unsubscribe || !runtime[key]) runtime[key] = null;
  }).catch((error) => {
    runtime.pendingRuntimeSubscriptions?.delete(pending);
    if (runtime[key] === unsubscribe) runtime[key] = null;
    if (pending.active) runtime.addWarning('error', `${label}.failed`, { error: error?.message || String(error) });
  });
}

function createLifecycleActions(runtime, deps) {
  const {
    isDagNodeStatusBridgeEvent,
    onBridgeEvent,
    onRuntimeReconnect,
  } = deps;

  const retryBootstrapAfterReconnect = () => {
    void runtime.get().bootstrap().catch((error) => {
      runtime.addWarning('error', 'app.bootstrap.reconnect_failed', { error: error?.message || String(error) });
    });
  };

  return {
    initializeEvents: () => {
      /*
       * bridge event 只注册一次。
       * 重连后 ready 只同步当前线程，其他状态重新 bootstrap。
       */
      if (runtime.bridgeUnsubscribe) return;
      trackRuntimeSubscription(runtime, 'bridgeUnsubscribe', onBridgeEvent(runtime.handleBridgeEvent, {
        escalateCallbackError: (_error, evt) => isDagNodeStatusBridgeEvent(evt),
      }), 'runtime.bridge.subscribe');
      trackRuntimeSubscription(runtime, 'reconnectUnsubscribe', onRuntimeReconnect(() => {
        const { activeThreadId, bootstrapStatus } = runtime.get();
        if (bootstrapStatus !== 'ready') {
          if (bootstrapStatus === 'loading') {
            runtime.bootstrapRetryAfterReconnect = true;
            return;
          }
          retryBootstrapAfterReconnect();
          return;
        }
        if (activeThreadId) void runtime.get().syncThreadState(activeThreadId, { includeDiff: true, preserveActiveThreadId: true });
      }), 'runtime.reconnect.subscribe');
    },

    destroy: () => {
      if (runtime.pendingRuntimeSubscriptions) {
        for (const pending of runtime.pendingRuntimeSubscriptions) {
          pending.active = false;
          pending.unsubscribe();
        }
        runtime.pendingRuntimeSubscriptions.clear();
      }
      if (runtime.bridgeUnsubscribe) {
        runtime.bridgeUnsubscribe();
        runtime.bridgeUnsubscribe = null;
      }
      if (runtime.reconnectUnsubscribe) {
        runtime.reconnectUnsubscribe();
        runtime.reconnectUnsubscribe = null;
      }
      runtime.sequencesByThread.clear();
      runtime.composerDrafts.clear();
      runtime.sidebarSnapshotsByCwd.clear();
      runtime.sidebarRefreshesByCwd.clear();
      runtime.threadMessageGenerations.clear();
      runtime.threadSyncGenerations.clear();
      runtime.clearAssistantDeltaFlushTimer?.();
      runtime.assistantDeltaBuffers.clear();
      runtime.sidebarRefreshSeq += 1;
    },


  };
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

  return {
    bootstrap: async () => {
      /*
       * bootstrap 会拿 cwd、窗口快照、项目列表和 provider。
       * cwd/provider 缺失就报错，后续页面都依赖它们。
       */
      runtime.set({ bootstrapStatus: 'loading', error: '' });
      void runtime.get().initializeEvents();
      try {
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
          await getPreference({ cwd: scopedCwd, key: providerActivePreferenceKey }),
          'frontend-app bootstrap',
        );
        runtime.set({
          cwd,
          projectScopeCwd: scopedCwd,
          activeProject: scopedCwd,
          provider: activeProvider,
          ...(bootstrapPage ? { activePage: bootstrapPage } : {}),
        });
        const [projects, sidebar] = await Promise.all([
          getProjects({ cwd: scopedCwd }),
          getSidebarState({ cwd: scopedCwd }),
          runtime.loadProviderConfig(scopedCwd, activeProvider),
        ]);
        runtime.applyProjects(projects, scopedCwd);
        runtime.cacheSidebarSnapshot(scopedCwd, sidebar);
        runtime.applySnapshot(sidebar);
        runtime.bootstrapRetryAfterReconnect = false;
        runtime.set({ bootstrapStatus: 'ready' });
      }
      catch (error) {
        runtime.set({ bootstrapStatus: 'failed', error: error.message });
        runtime.addWarning('error', 'app.bootstrap.failed', { error: error.message });
        if (runtime.bootstrapRetryAfterReconnect) {
          runtime.bootstrapRetryAfterReconnect = false;
          void runtime.get().bootstrap().catch((retryError) => {
            runtime.addWarning('error', 'app.bootstrap.reconnect_failed', { error: retryError?.message || String(retryError) });
          });
        }
        throw error;
      }
    },


  };
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
      return {
        threadStateLoadingByThread: {
          ...state.threadStateLoadingByThread,
          [id]: loading,
        },
      };
    });
  };

  return {
    syncThreadState: async (threadId, options = {}) => {
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
        if (includeDiff) {
          runtime.set((state) => ({
            threadDiffReadyByThread: {
              ...state.threadDiffReadyByThread,
              [id]: true,
            },
          }));
        }
        await messagesPromise;
        if (!activeChanged && shouldAutoLoadThreadConfig(runtime.get(), id)) await runtime.get().loadThreadConfig(id);
        return true;
      }
      catch (error) {
        if (!isCurrentThreadSyncGeneration(id, generation)) return false;
        const message = error?.message || String(error);
        runtime.notifyAction(`同步会话失败：${message}`, 'error', { threadId: id });
        runtime.addWarning('error', 'thread.sync.failed', { threadId: id, error: message });
        return false;
      }
      finally {
        setThreadStateLoading(id, generation, false);
      }
    },

    loadOlderThreadMessages: async (threadId, options = {}) => runtime.loadOlderThreadMessages(threadId, options),


  };
}
