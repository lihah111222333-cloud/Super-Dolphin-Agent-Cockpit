import { resolveThreadIdentity } from '../../../../shared/api/backendApi.js';
import { basename } from '../composerAttachments.js';
import { normalizeBackendThreadId } from './threadIdentity.js';
import { threadOpenHistoryFallbackItems } from './threadHistoryTimeline.js';
import { createThreadOpenCoordinator } from '../thread-open/threadOpenCoordinator.js';

function selectThreadDraft(runtime, current, id) {
  void runtime.saveActiveComposerDraft(current);
  return runtime.restoreComposerDraft(current, id);
}

function setThreadLoading(state, id, loading) {
  return {
    ...state.threadStateLoadingByThread,
    [id]: loading,
  };
}

function failedThreadSelectionPatch(runtime, state, context, deps, intentIsCurrent) {
  const { id, previousActiveThread } = context;
  const previousCwd = deps.normalizePath(previousActiveThread?.cwd);
  const currentCwd = deps.normalizePath(runtime.currentChatCwd());
  const cannotRestorePreviousScope = intentIsCurrent
    && previousActiveThread
    && previousCwd
    && currentCwd
    && previousCwd !== currentCwd;
  if (!cannotRestorePreviousScope) {
    return {
      pendingActiveThreadId: intentIsCurrent && state.pendingActiveThreadId === id ? '' : state.pendingActiveThreadId,
      threadStateLoadingByThread: setThreadLoading(state, id, false),
      ...(intentIsCurrent && previousActiveThread ? {
        threads: deps.upsertExplicitThread(state.threads, previousActiveThread, previousActiveThread.id),
      } : {}),
    };
  }
  const restored = runtime.restoreComposerDraft({
    ...state,
    activeProject: currentCwd,
    cwd: currentCwd,
    activeThreadId: '',
  }, '');
  return {
    activeThreadId: '',
    pendingActiveThreadId: '',
    draft: restored.draft,
    attachments: restored.attachments,
    composerCapabilities: restored.composerCapabilities,
    threadStateLoadingByThread: setThreadLoading(state, id, false),
    actionNotice: deps.actionNotice(
      '无法打开该会话；已取消旧项目会话选择，请重试后再发送。',
      'error',
    ),
  };
}

function clearFailedThreadSelection(runtime, context, deps, coordinator) {
  const { selectionIntent } = context;
  const intentIsCurrent = coordinator.isCurrent(selectionIntent);
  if (coordinator.canReleaseTarget(selectionIntent)) {
    runtime.set((state) => failedThreadSelectionPatch(runtime, state, context, deps, intentIsCurrent));
  }
  if (intentIsCurrent) coordinator.cancel(selectionIntent);
  return intentIsCurrent;
}

function selectionIntentForOpen(coordinator, targetThreadId, options = {}) {
  if (Object.prototype.hasOwnProperty.call(options, 'selectionSnapshot')) {
    return coordinator.beginIfUnchanged(options.selectionSnapshot, targetThreadId);
  }
  if (Object.prototype.hasOwnProperty.call(options, 'selectionIntent')) {
    return coordinator.isCurrent(options.selectionIntent) ? options.selectionIntent : null;
  }
  return coordinator.begin(targetThreadId);
}

function clearResolveLoading(runtime, id, selectionIntent, coordinator) {
  const intentIsCurrent = coordinator.isCurrent(selectionIntent);
  if (coordinator.canReleaseTarget(selectionIntent)) {
    runtime.set((state) => ({
      pendingActiveThreadId: intentIsCurrent && state.pendingActiveThreadId === id
        ? ''
        : state.pendingActiveThreadId,
      threadStateLoadingByThread: setThreadLoading(state, id, false),
    }));
  }
  return intentIsCurrent;
}

function beginOpeningThread(runtime, thread, deps, coordinator) {
  const rawThread = thread && typeof thread === 'object' ? thread : { id: thread };
  const requestedId = normalizeBackendThreadId(
    rawThread.id || rawThread.threadId || rawThread.thread_id || rawThread.agentId || rawThread.agent_id,
  );
  if (!requestedId) return null;
  const current = runtime.get();
  const openingThread = deps.normalizeThread(rawThread, {
    state: current,
    fallbackProvider: current.provider,
  });
  const id = normalizeBackendThreadId(openingThread.id || requestedId);
  if (!id) return null;
  const selectionIntent = coordinator.begin(id);
  const restored = selectThreadDraft(runtime, current, id);
  runtime.set((state) => ({
    activeThreadId: id,
    pendingActiveThreadId: id,
    threads: deps.upsertExplicitThread(state.threads, { ...openingThread, id }, requestedId),
    draft: restored.draft,
    attachments: restored.attachments,
    composerCapabilities: restored.composerCapabilities,
    threadStateLoadingByThread: setThreadLoading(state, id, true),
  }));
  return selectionIntent;
}

function resolvedThreadForOpen(runtime, requestedId, resolved, deps) {
  return deps.normalizeThread(resolved || deps.optionalUiObject(), {
    state: runtime.get(),
    fallbackProvider: runtime.get().provider,
  });
}

async function openThreadById(runtime, threadId, options, deps, coordinator) {
  const requestedId = normalizeBackendThreadId(threadId);
  if (!requestedId) return false;
  const selectionIntent = selectionIntentForOpen(coordinator, requestedId, options);
  if (!selectionIntent) return false;
  const source = deps.normalizeString(options?.source);
  const cwd = runtime.requireCwd('thread.open');
  let resolved;
  try {
    resolved = await resolveThreadIdentity({ cwd, threadId: requestedId });
  } catch (error) {
    if (!clearResolveLoading(runtime, requestedId, selectionIntent, coordinator)) return false;
    runtime.notifyRPCFailure('打开会话', 'thread.open.resolve.failed', error, { threadId: requestedId, source });
    throw error;
  }
  const resolvedThread = resolvedThreadForOpen(runtime, requestedId, resolved, deps);
  if (!resolvedThread.id || !deps.threadMatchesIdentifier(resolvedThread, requestedId)) {
    if (!clearResolveLoading(runtime, requestedId, selectionIntent, coordinator)) return false;
    const error = new Error('thread/resolve returned a different or empty thread id');
    runtime.notifyRPCFailure('打开会话', 'thread.open.resolve.invalid', error, { threadId: requestedId, source });
    throw error;
  }
  return openResolvedThread(runtime, {
    requestedId,
    resolvedThread,
    options,
    selectionIntent,
    source,
  }, deps, coordinator);
}

async function openResolvedThread(runtime, context, deps, coordinator) {
  const { requestedId, resolvedThread, options, selectionIntent, source } = context;
  const id = normalizeBackendThreadId(resolvedThread.id);
  const historyFallback = threadOpenHistoryFallbackItems(id, options);
  const selectionState = runtime.get();
  const previousActiveThread = selectionState.threads.find((thread) => (
    deps.threadMatchesIdentifier(thread, selectionState.activeThreadId)
  ));
  runtime.set((state) => ({
    threads: deps.upsertExplicitThread(state.threads, resolvedThread, requestedId),
    threadStateLoadingByThread: setThreadLoading(state, id, true),
    ...(coordinator.isCurrent(selectionIntent) ? {
      pendingActiveThreadId: id,
    } : {}),
  }));
  try {
    const synced = await runtime.get().syncThreadState(id, {
      includeArchived: true,
      includeDiff: false,
      preserveActiveThreadId: true,
      preserveActiveThreadRecord: false,
      shouldPublishFailure: () => coordinator.isCurrent(selectionIntent),
      ...(historyFallback.length > 0 ? { historyFallback } : {}),
    });
    if (!synced) {
      clearFailedThreadSelection(runtime, { id, previousActiveThread, selectionIntent }, deps, coordinator);
      return false;
    }
    if (!coordinator.isCurrent(selectionIntent)) return false;
    const restored = selectThreadDraft(runtime, runtime.get(), id);
    runtime.set((state) => ({
      threads: deps.upsertExplicitThread(state.threads, resolvedThread, requestedId),
      activeThreadId: id,
      pendingActiveThreadId: '',
      draft: restored.draft,
      attachments: restored.attachments,
      composerCapabilities: restored.composerCapabilities,
    }));
  } catch (error) {
    if (!coordinator.isCurrent(selectionIntent)) return false;
    clearFailedThreadSelection(runtime, { id, previousActiveThread, selectionIntent }, deps, coordinator);
    runtime.notifyRPCFailure('打开会话', 'thread.open.failed', error, { threadId: id, source });
    throw error;
  }
  return coordinator.isCurrent(selectionIntent);
}

async function setActiveThread(runtime, threadId, options, deps, coordinator) {
  const id = deps.backendThreadIdForState(runtime.get(), threadId, { includeArchived: true });
  const current = runtime.get();
  const selectionIntent = id ? selectionIntentForOpen(coordinator, id, options) : null;
  if (id && !selectionIntent) return false;
  const restored = selectThreadDraft(runtime, current, id);
  if (!id) {
    coordinator.invalidate();
    runtime.set({
      activeThreadId: '',
      pendingActiveThreadId: '',
      draft: restored.draft,
      attachments: restored.attachments,
      composerCapabilities: restored.composerCapabilities,
    });
    return undefined;
  }
  return activateExistingThread(runtime, {
    current,
    id,
    restored,
    selectionIntent,
  }, deps, coordinator);
}

async function activateExistingThread(runtime, context, deps, coordinator) {
  const { current, id, restored, selectionIntent } = context;
  if (!coordinator.isCurrent(selectionIntent)) return false;
  const currentThreadId = deps.backendThreadIdForState(current, current.activeThreadId);
  if (currentThreadId === id) {
    runtime.set({
      pendingActiveThreadId: '',
      draft: restored.draft,
      attachments: restored.attachments,
      composerCapabilities: restored.composerCapabilities,
    });
    const synced = await runtime.get().syncThreadState(id, {
      includeArchived: true,
      includeDiff: false,
      preserveActiveThreadId: true,
      shouldPublishFailure: () => coordinator.isCurrent(selectionIntent),
    });
    if (!synced) {
      clearFailedThreadSelection(runtime, { id, previousActiveThread: null, selectionIntent }, deps, coordinator);
      return false;
    }
    return coordinator.isCurrent(selectionIntent);
  }
  runtime.set((state) => ({
    pendingActiveThreadId: id,
    threadStateLoadingByThread: setThreadLoading(state, id, true),
  }));
  try {
    const synced = await runtime.get().syncThreadState(id, {
      includeArchived: true,
      includeDiff: false,
      preserveActiveThreadId: true,
      preserveActiveThreadRecord: false,
      shouldPublishFailure: () => coordinator.isCurrent(selectionIntent),
    });
    if (!synced) {
      const previousActiveThread = current.threads.find((thread) => (
        deps.threadMatchesIdentifier(thread, current.activeThreadId)
      ));
      clearFailedThreadSelection(runtime, { id, previousActiveThread, selectionIntent }, deps, coordinator);
      return false;
    }
    if (!coordinator.isCurrent(selectionIntent)) return false;
    runtime.set({
      activeThreadId: id,
      pendingActiveThreadId: '',
      draft: restored.draft,
      attachments: restored.attachments,
      composerCapabilities: restored.composerCapabilities,
    });
  } catch (error) {
    if (!coordinator.isCurrent(selectionIntent)) return false;
    const previousActiveThread = current.threads.find((thread) => (
      deps.threadMatchesIdentifier(thread, current.activeThreadId)
    ));
    clearFailedThreadSelection(runtime, { id, previousActiveThread, selectionIntent }, deps, coordinator);
    runtime.notifyRPCFailure('切换会话', 'thread.select.failed', error, { threadId: id });
    throw error;
  }
  return coordinator.isCurrent(selectionIntent);
}

function cancelOpeningThread(runtime, selectionIntent, coordinator) {
  if (!coordinator.cancel(selectionIntent)) return false;
  const current = runtime.get();
  const restored = selectThreadDraft(runtime, current, '');
  runtime.set((state) => ({
    activeThreadId: '',
    pendingActiveThreadId: '',
    draft: restored.draft,
    attachments: restored.attachments,
    composerCapabilities: restored.composerCapabilities,
    threadStateLoadingByThread: setThreadLoading(state, selectionIntent.targetThreadId, false),
  }));
  return true;
}

function invalidatedOpeningPatch(state, invalidatedIntent) {
  return {
    pendingActiveThreadId: '',
    threadStateLoadingByThread: invalidatedIntent
      ? setThreadLoading(state, invalidatedIntent.targetThreadId, false)
      : state.threadStateLoadingByThread,
  };
}

function newThread(runtime, deps, coordinator) {
  const invalidatedIntent = coordinator.invalidate();
  const current = runtime.get();
  const restored = selectThreadDraft(runtime, current, '');
  runtime.set((state) => ({
    activeThreadId: '',
    draft: restored.draft,
    attachments: restored.attachments,
    composerCapabilities: restored.composerCapabilities,
    actionNotice: deps.actionNotice('已创建新对话草稿', 'info'),
    ...invalidatedOpeningPatch(state, invalidatedIntent),
  }));
}

function continueWithSharedFile(runtime, path, deps, coordinator) {
  const target = deps.normalizeString(path);
  if (!target) return false;
  const invalidatedIntent = coordinator.invalidate();
  const current = runtime.get();
  const sourceThreadId = deps.backendThreadIdForState(current, current.activeThreadId);
  if (sourceThreadId && typeof current.openForkDraft === 'function') {
    void runtime.saveActiveComposerDraft(current);
    runtime.set((state) => ({
      activePage: 'chat',
      ...invalidatedOpeningPatch(state, invalidatedIntent),
    }));
    void current.openForkDraft({ origin: 'shared-files', sharedFilePath: target });
    return true;
  }
  const attachment = { path: target, name: basename(target) };
  void runtime.saveActiveComposerDraft(current);
  runtime.set((state) => ({
    activePage: 'chat',
    activeThreadId: '',
    draft: `请基于共享文件 ${target} 继续对话。`,
    attachments: state.attachments.some((item) => item.path === target) ? state.attachments : [attachment],
    ...invalidatedOpeningPatch(state, invalidatedIntent),
  }));
  return true;
}

export function createThreadSelectionActions(runtime, deps) {
  const coordinator = createThreadOpenCoordinator();
  return {
    beginOpeningThread: (thread) => beginOpeningThread(runtime, thread, deps, coordinator),
    cancelOpeningThread: (selectionIntent) => cancelOpeningThread(runtime, selectionIntent, coordinator),
    captureThreadSelection: () => coordinator.capture(),
    openThreadById: (threadId, options = {}) => openThreadById(runtime, threadId, options, deps, coordinator),
    setActiveThread: (threadId, options = {}) => setActiveThread(runtime, threadId, options, deps, coordinator),
    newThread: () => newThread(runtime, deps, coordinator),
    continueWithSharedFile: (path) => continueWithSharedFile(runtime, path, deps, coordinator),
  };
}
