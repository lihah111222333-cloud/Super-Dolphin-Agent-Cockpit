import { resolveThreadIdentity } from '../../../../shared/api/backendApi.js';
import { basename } from '../composerAttachments.js';
import { normalizeBackendThreadId } from './threadIdentity.js';
import { threadOpenHistoryFallbackItems } from './threadHistoryTimeline.js';

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

function beginOpeningThread(runtime, thread, deps) {
  const rawThread = thread && typeof thread === 'object' ? thread : { id: thread };
  const requestedId = normalizeBackendThreadId(
    rawThread.id || rawThread.threadId || rawThread.thread_id || rawThread.agentId || rawThread.agent_id,
  );
  if (!requestedId) return false;
  const current = runtime.get();
  const openingThread = deps.normalizeThread(rawThread, {
    state: current,
    fallbackProvider: current.provider,
  });
  const id = normalizeBackendThreadId(openingThread.id || requestedId);
  if (!id) return false;
  const restored = selectThreadDraft(runtime, current, id);
  runtime.set((state) => ({
    activeThreadId: id,
    pendingActiveThreadId: id,
    threads: deps.upsertExplicitThread(state.threads, { ...openingThread, id }, requestedId),
    draft: restored.draft,
    attachments: restored.attachments,
    threadStateLoadingByThread: setThreadLoading(state, id, true),
  }));
  return true;
}

function resolvedThreadForOpen(runtime, requestedId, resolved, deps) {
  return deps.normalizeThread(resolved || deps.optionalUiObject(), {
    state: runtime.get(),
    fallbackProvider: runtime.get().provider,
  });
}

async function openThreadById(runtime, threadId, options, deps) {
  const requestedId = normalizeBackendThreadId(threadId);
  if (!requestedId) return false;
  const source = deps.normalizeString(options?.source);
  const cwd = runtime.requireCwd('thread.open');
  let resolved;
  try {
    resolved = await resolveThreadIdentity({ cwd, threadId: requestedId });
  } catch (error) {
    return runtime.notifyRPCFailure('打开会话', 'thread.open.resolve.failed', error, { threadId: requestedId, source });
  }
  const resolvedThread = resolvedThreadForOpen(runtime, requestedId, resolved, deps);
  if (!resolvedThread.id || !deps.threadMatchesIdentifier(resolvedThread, requestedId)) {
    return runtime.notifyRPCFailure('打开会话', 'thread.open.resolve.invalid', new Error('thread/resolve returned a different or empty thread id'), { threadId: requestedId, source });
  }
  return openResolvedThread(runtime, { requestedId, resolvedThread, options, source }, deps);
}

async function openResolvedThread(runtime, context, deps) {
  const { requestedId, resolvedThread, options, source } = context;
  const id = normalizeBackendThreadId(resolvedThread.id);
  const historyFallback = threadOpenHistoryFallbackItems(id, options);
  const current = runtime.get();
  const restored = selectThreadDraft(runtime, current, id);
  runtime.set((state) => ({
    activeThreadId: id,
    pendingActiveThreadId: '',
    threads: deps.upsertExplicitThread(state.threads, resolvedThread, requestedId),
    draft: restored.draft,
    attachments: restored.attachments,
    threadStateLoadingByThread: setThreadLoading(state, id, true),
  }));
  try {
    const synced = await runtime.get().syncThreadState(id, {
      includeArchived: true,
      includeDiff: false,
      preserveActiveThreadId: true,
      ...(historyFallback.length > 0 ? { historyFallback } : {}),
    });
    if (!synced) return false;
  } catch (error) {
    runtime.set((state) => ({ threadStateLoadingByThread: setThreadLoading(state, id, false) }));
    return runtime.notifyRPCFailure('打开会话', 'thread.open.failed', error, { threadId: id, source });
  }
  return true;
}

function listMutationBlocksSelection(current, id, deps) {
  const lastListMutationTime = current.lastListMutationTime || 0;
  if (deps.clockNowMillis() - lastListMutationTime >= 350) return false;
  const currentActiveId = deps.backendThreadIdForState(current, current.activeThreadId);
  return id !== currentActiveId;
}

async function setActiveThread(runtime, threadId, deps) {
  const id = deps.backendThreadIdForState(runtime.get(), threadId, { includeArchived: true });
  const current = runtime.get();
  if (listMutationBlocksSelection(current, id, deps)) return false;
  const restored = selectThreadDraft(runtime, current, id);
  if (!id) {
    runtime.set({
      activeThreadId: '',
      pendingActiveThreadId: '',
      draft: restored.draft,
      attachments: restored.attachments,
    });
    return undefined;
  }
  return activateExistingThread(runtime, current, id, restored, deps);
}

async function activateExistingThread(runtime, current, id, restored, deps) {
  const currentThreadId = deps.backendThreadIdForState(current, current.activeThreadId);
  if (currentThreadId === id) {
    runtime.set({
      pendingActiveThreadId: '',
      draft: restored.draft,
      attachments: restored.attachments,
    });
    return runtime.get().syncThreadState(id, { includeArchived: true, includeDiff: false });
  }
  runtime.set((state) => ({
    activeThreadId: id,
    pendingActiveThreadId: '',
    draft: restored.draft,
    attachments: restored.attachments,
    threadStateLoadingByThread: setThreadLoading(state, id, true),
  }));
  try {
    const synced = await runtime.get().syncThreadState(id, { includeArchived: true, includeDiff: false });
    if (!synced) return false;
  } catch (error) {
    runtime.set((state) => ({ threadStateLoadingByThread: setThreadLoading(state, id, false) }));
    return runtime.notifyRPCFailure('切换会话', 'thread.select.failed', error, { threadId: id });
  }
  return true;
}

function newThread(runtime, deps) {
  const current = runtime.get();
  const restored = selectThreadDraft(runtime, current, '');
  runtime.set({
    activeThreadId: '',
    draft: restored.draft,
    attachments: restored.attachments,
    actionNotice: deps.actionNotice('已创建新对话草稿', 'info'),
  });
}

function continueWithSharedFile(runtime, path, deps) {
  const target = deps.normalizeString(path);
  if (!target) return false;
  const current = runtime.get();
  const sourceThreadId = deps.backendThreadIdForState(current, current.activeThreadId);
  if (sourceThreadId && typeof current.openForkDraft === 'function') {
    void runtime.saveActiveComposerDraft(current);
    runtime.set({ activePage: 'chat' });
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
  }));
  return true;
}

export function createThreadSelectionActions(runtime, deps) {
  return {
    beginOpeningThread: (thread) => beginOpeningThread(runtime, thread, deps),
    openThreadById: (threadId, options = {}) => openThreadById(runtime, threadId, options, deps),
    setActiveThread: (threadId) => setActiveThread(runtime, threadId, deps),
    newThread: () => newThread(runtime, deps),
    continueWithSharedFile: (path) => continueWithSharedFile(runtime, path, deps),
  };
}
