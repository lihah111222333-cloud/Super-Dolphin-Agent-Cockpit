
import {
  archiveThread as archiveThreadRPC,
  beginTextClipboardWrite,
  compactThread,
  deleteThread as deleteThreadRPC,
  forceCompleteTurn,
  interruptTurn,
  recoverThread,
  renameThread as renameThreadRPC,
  resolveThreadIdentity,
  respondApproval as respondApprovalRPC,
  setPreference,
  unarchiveThread as unarchiveThreadRPC,
} from '../../../../../shared/api/backendApi.js';
import { positiveApprovalRequestIdFromFields } from '../../../../../shared/api/approvalRequestId.js';
import { sessionApi } from '../../../../../shared/api/sessionApi.js';
import { firstOptionalPresent } from '../../contractStoreModel.js';
import { buildThreadCopyPayload } from '../threadCopyPayload.js';
import { applyThreadRename, archiveThreadFailureState, archiveThreadOptimisticState } from '../threadListMutations.js';
import {
  DEFAULT_PROVIDER,
  THREAD_PINS_CHAT_PREF_KEY,
  clockNowMillis,
  mapSidebarThreadCache,
  normalizeString,
  normalizeTimestampMap,
  optionalUiObject,
  resolveLaunchPreferences,
} from './clientStoreUtils.js';
import {
  activeThreadInterruptTarget,
  backendThreadIdForArchiveState,
  backendThreadIdForState,
} from './clientStoreRuntimeThreadModel.js';
import {
  actionNotice,
  createDashboardCommandRequest,
  createdThreadIdForSendRollback,
  deleteProvisionalThreadAfterSendFailure,
  optimisticSendDraftState,
  promotedDraftThreadState,
  rollbackSendDraftState,
  saveFailedSendDraftSnapshot,
  sendRollbackRestoresVisibleComposer,
  startNewDraftThread,
} from './clientStoreSendModel.js';
import { writeThreadInfoClipboard } from './clientStoreThreadClipboard.js';

const APPROVAL_SUBMIT_TIMEOUT_MS = 15_000;
const APPROVAL_SUBMIT_TIMEOUT_CODE = 'APPROVAL_SUBMIT_TIMEOUT';

function approvalSubmitTimeoutError() {
  const error = new Error('审批提交超时');
  error.code = APPROVAL_SUBMIT_TIMEOUT_CODE;
  return error;
}

function approvalSubmitIsTimeout(error) {
  return error?.code === APPROVAL_SUBMIT_TIMEOUT_CODE;
}

async function respondApprovalWithinTimeout(params) {
  let timeoutId = 0;
  try {
    return await Promise.race([
      respondApprovalRPC(params),
      new Promise((_, reject) => {
        timeoutId = window.setTimeout(() => reject(approvalSubmitTimeoutError()), APPROVAL_SUBMIT_TIMEOUT_MS);
      }),
    ]);
  }
  finally {
    window.clearTimeout(timeoutId);
  }
}

function promotedDashboardCommandState(state, request, started) {
  return {
    ...promotedDraftThreadState(state, request, started),
    activePage: 'chat',
  };
}

function rollbackDashboardCommandState(state, request, error, createdThreadId) {
  return {
    ...rollbackSendDraftState(state, request, error, { createdThreadId }),
    activePage: 'commands',
  };
}

function approvalSubmitPatch(state, requestId, decision) {
  return {
    approvalSubmitByRequestId: {
      ...state.approvalSubmitByRequestId,
      [requestId]: {
        approved: decision,
        inFlight: true,
        startedAt: clockNowMillis(),
      },
    },
  };
}

function clearApprovalSubmitPatch(state, requestId) {
  const current = state.approvalSubmitByRequestId || optionalUiObject();
  if (!current[requestId]) return {};
  const next = { ...current };
  delete next[requestId];
  return { approvalSubmitByRequestId: next };
}

async function runDashboardCommandAction(runtime, card) {
  const cwd = runtime.requireCwd('dashboard command');
  const request = createDashboardCommandRequest(runtime.get(), cwd, card);
  if (!request) return false;

  runtime.set((state) => ({
    ...optimisticSendDraftState(state, request),
    activePage: 'chat',
  }));

  let threadId = request.previousThreadId;
  try {
    if (!threadId) {
      const started = await startNewDraftThread(request, (launchCwd) => resolveLaunchPreferences(launchCwd, runtime.addWarning, runtime.getPreference));
      threadId = started.threadId;
      runtime.set((state) => promotedDashboardCommandState(state, request, started));
    }

    await sessionApi.startTurn({
      cwd: request.cwd,
      threadId,
      input: request.input,
      manualSkillSelection: false,
    });
    runtime.clearComposerDraft({ ...runtime.get(), activeThreadId: request.previousActiveThreadId }, request.previousActiveThreadId);
    runtime.clearComposerDraft(runtime.get(), request.provisionalThreadId);
    runtime.clearComposerDraft(runtime.get(), threadId);
    runtime.set({ sending: false });
    return true;
  }
  catch (error) {
    const rollbackState = runtime.get();
    const createdThreadId = createdThreadIdForSendRollback(rollbackState, request, threadId);
    const shouldCacheFailedDraft = !sendRollbackRestoresVisibleComposer(rollbackState, request, createdThreadId);
    runtime.set((state) => rollbackDashboardCommandState(state, request, error, createdThreadId));
    if (shouldCacheFailedDraft) saveFailedSendDraftSnapshot(runtime, request);
    await deleteProvisionalThreadAfterSendFailure(createdThreadId, runtime.addWarning);
    runtime.addWarning('error', 'dashboard.command.send.failed', { error: error.message });
    throw error;
  }
}

function createDashboardCommandActions(runtime) {
  return {
    runDashboardCommand: (card) => runDashboardCommandAction(runtime, card),
  };
}

function hasInterruptibleThreadAction(runtime) {
  return activeThreadInterruptTarget(runtime.get()).interruptible;
}

async function refreshActiveThreadStatusAction(runtime) {
  const threadId = backendThreadIdForState(runtime.get(), runtime.get().activeThreadId);
  if (!threadId) return false;
  await runtime.get().syncThreadState(threadId);
  runtime.notifyAction('线程状态已刷新', 'success', { threadId });
  return true;
}

function warnMissingApprovalRequest(runtime, item) {
  runtime.notifyAction('当前审批缺少请求编号，无法提交', 'error');
  runtime.addWarning('error', 'timeline.approval.request_id_missing', {
    command: normalizeString(firstOptionalPresent(item?.command, item?.title)),
  });
}

function warnDuplicateApprovalSubmit(runtime, requestId, decision) {
  runtime.notifyAction('审批结果正在提交，请等待当前请求完成', 'warning', { requestId });
  runtime.addWarning('warn', 'timeline.approval.respond_duplicate', { requestId, approved: decision });
}

function approvalSubmitIsInFlight(runtime, requestId) {
  return runtime.get().approvalSubmitByRequestId?.[requestId]?.inFlight === true;
}

function warnApprovalFailed(runtime, requestId, decision, error) {
  const message = error?.message || String(error);
  runtime.notifyAction(`审批提交失败：${message}`, 'error', { requestId });
  runtime.addWarning('error', 'timeline.approval.respond.failed', { requestId, approved: decision, error: message });
}

async function respondApprovalAction(runtime, deps, item, approved) {
  const requestId = positiveApprovalRequestIdFromFields(item);
  if (requestId <= 0) {
    warnMissingApprovalRequest(runtime, item);
    return false;
  }
  if (typeof approved !== 'boolean') {
    warnApprovalFailed(
      runtime,
      requestId,
      undefined,
      new TypeError('approval decision must be a boolean'),
    );
    return false;
  }
  const decision = approved;
  if (approvalSubmitIsInFlight(runtime, requestId)) {
    warnDuplicateApprovalSubmit(runtime, requestId, decision);
    return false;
  }
  deps.recordApproval('start');
  runtime.set((state) => approvalSubmitPatch(state, requestId, decision));
  try {
    const result = await respondApprovalWithinTimeout({ requestId, approved: decision });
    if (result !== null) {
      throw new TypeError('approval/respond response must be null');
    }
    deps.recordApproval('success');
    runtime.notifyAction('审批结果已提交', 'success', { requestId });
    return true;
  }
  catch (error) {
    deps.recordApproval(approvalSubmitIsTimeout(error) ? 'timeout' : 'failure');
    warnApprovalFailed(runtime, requestId, decision, error);
    if (approvalSubmitIsTimeout(error)) throw error;
    return false;
  }
  finally {
    runtime.set((state) => clearApprovalSubmitPatch(state, requestId));
  }
}

function createActiveThreadActions(runtime, deps) {
  return {
    interruptActiveThread: () => runtime.activeThreadRPC('thread.interrupt', interruptTurn),
    forceCompleteActiveThread: () => runtime.activeThreadRPC('thread.force_complete', forceCompleteTurn),
    compactActiveThread: () => runtime.activeThreadRPC('thread.compact', compactThread),
    recoverActiveThread: () => runtime.recoverActiveThreadRPC(recoverThread),

    hasActiveThreadActions: () => Boolean(backendThreadIdForState(runtime.get(), runtime.get().activeThreadId)),
    hasInterruptibleThreadAction: () => hasInterruptibleThreadAction(runtime),
    hasForceCompleteThreadAction: () => hasInterruptibleThreadAction(runtime),
    refreshActiveThreadStatus: () => refreshActiveThreadStatusAction(runtime),
    respondApproval: (item, approved) => respondApprovalAction(runtime, deps, item, approved),
  };
}

async function resolveThreadCopyIdentity(runtime, preparedClipboardWrite, cwd, threadId) {
  try {
    return await resolveThreadIdentity({ cwd, threadId });
  }
  catch (error) {
    preparedClipboardWrite?.cancel?.(error);
    runtime.notifyAction(`复制失败：线程信息接口调用失败：${error.message || String(error)}`, 'warning', { threadId });
    runtime.addWarning('warn', 'thread.identity.resolve.failed', { threadId, error: error.message || String(error) });
    return null;
  }
}

function validateThreadCopyIdentity(runtime, preparedClipboardWrite, identity, threadId) {
  if (identity && typeof identity === 'object' && !Array.isArray(identity)) return true;
  preparedClipboardWrite?.cancel?.();
  runtime.notifyAction('复制失败：线程信息接口返回值不是 JSON 对象', 'warning', { threadId });
  runtime.addWarning('warn', 'thread.identity.resolve.invalid', { threadId });
  return false;
}

async function copyActiveThreadInfoAction(runtime) {
  const state = runtime.get();
  const threadId = backendThreadIdForState(state, state.activeThreadId);
  if (!threadId) {
    runtime.notifyAction('当前没有可复制的后端线程', 'warning');
    return false;
  }
  const preparedClipboardWrite = beginTextClipboardWrite();
  const thread = state.threads.find((item) => item.id === threadId) || optionalUiObject();
  const cwd = runtime.requireCwd('thread.copy');
  const identity = await resolveThreadCopyIdentity(runtime, preparedClipboardWrite, cwd, threadId);
  if (!validateThreadCopyIdentity(runtime, preparedClipboardWrite, identity, threadId)) return false;
  const threadConfig = state.threadConfigByThread[threadId] || await runtime.get().loadThreadConfig(threadId);
  const payload = buildThreadCopyPayload({
    state: runtime.get(),
    threadId,
    thread,
    identity,
    threadConfig,
    defaultProvider: DEFAULT_PROVIDER,
  });
  try {
    const text = JSON.stringify(payload, null, 2);
    await writeThreadInfoClipboard(runtime, preparedClipboardWrite, text, threadId);
    runtime.notifyAction('线程信息已复制', 'success', { threadId });
    return true;
  }
  catch (error) {
    runtime.notifyAction(`复制失败：${error.message || String(error)}`, 'warning', { threadId });
    runtime.addWarning('warn', 'thread.copy.clipboard.failed', { threadId, error: error.message || String(error) });
    return false;
  }
}

function createThreadCopyActions(runtime) {
  return {
    copyActiveThreadInfo: () => copyActiveThreadInfoAction(runtime),
  };
}

function nextPinnedThreadMap(currentMap, id, pinned) {
  const nextMap = { ...currentMap };
  if (pinned) {
    delete nextMap[id];
    return nextMap;
  }
  nextMap[id] = clockNowMillis();
  return nextMap;
}

function pinnedThreadView(thread, id, pinned, nextMap) {
  if (thread.id !== id) return thread;
  return {
    ...thread,
    pinned: !pinned,
    pinnedAt: nextMap[id] || 0,
  };
}

function threadPinPatch(state, id, pinned, nextMap) {
  const applyPin = (thread) => pinnedThreadView(thread, id, pinned, nextMap);
  return {
    pinnedThreadAtById: nextMap,
    threads: state.threads.map(applyPin),
    sidebarThreadsByProject: mapSidebarThreadCache(state, (threads) => threads.map(applyPin)),
    actionNotice: actionNotice(pinned ? '会话已取消置顶' : '会话已置顶', 'success'),
  };
}

function renamedThreadPatch(state, id, nextName) {
  return {
    threads: applyThreadRename(state.threads, id, nextName),
    sidebarThreadsByProject: mapSidebarThreadCache(state, (threads) => applyThreadRename(threads, id, nextName)),
    actionNotice: actionNotice('线程已重命名', 'success'),
  };
}

async function renameThreadAction(runtime, threadId, name) {
  const id = backendThreadIdForState(runtime.get(), threadId);
  const nextName = normalizeString(name);
  if (!id || !nextName) return false;
  try {
    await renameThreadRPC({ threadId: id, name: nextName });
    runtime.set((state) => renamedThreadPatch(state, id, nextName));
    return true;
  }
  catch (error) {
    return runtime.notifyRPCFailure('重命名会话', 'thread.rename.failed', error, { threadId: id });
  }
}

async function toggleThreadPinAction(runtime, threadId) {
  const id = backendThreadIdForArchiveState(runtime.get(), threadId);
  if (!id) return false;
  const cwd = runtime.requireCwd('thread.pin');
  const currentMap = normalizeTimestampMap(runtime.get().pinnedThreadAtById);
  const pinned = currentMap[id] > 0;
  const nextMap = nextPinnedThreadMap(currentMap, id, pinned);
  try {
    await setPreference({
      cwd,
      key: THREAD_PINS_CHAT_PREF_KEY,
      value: nextMap,
    });
    runtime.set((state) => threadPinPatch(state, id, pinned, nextMap));
    return true;
  }
  catch (error) {
    return runtime.notifyRPCFailure(pinned ? '取消置顶会话' : '置顶会话', 'thread.pin.failed', error, { threadId: id });
  }
}

function createThreadRenamePinActions(runtime) {
  return {
    renameThread: (threadId, name) => renameThreadAction(runtime, threadId, name),
    toggleThreadPin: (threadId) => toggleThreadPinAction(runtime, threadId),
  };
}

function threadArchiveLoadingClearedPatch(state, id) {
  return {
    threadArchiveLoadingByThread: {
      ...state.threadArchiveLoadingByThread,
      [id]: false,
    },
  };
}

async function writeThreadArchivePreference(cwd, id, archivedAt) {
  await setPreference({
    cwd,
    key: `archivedThreadAtById.${id}`,
    value: archivedAt > 0 ? archivedAt : null,
  });
}

async function applyThreadArchiveRPC(id, archived) {
  if (archived) {
    await archiveThreadRPC({ threadId: id });
    return;
  }
  await unarchiveThreadRPC({ threadId: id });
}

function archiveFailureNotice(archived, message) {
  const action = archived ? '归档' : '恢复';
  return actionNotice(`${action}会话失败：${message}`, 'error');
}

function archivePreferenceFailureNotice(archived, message) {
  const action = archived ? '归档' : '恢复';
  return actionNotice(`${action}偏好保存失败：${message}`, 'error');
}

async function archiveThreadAction(runtime, threadId, archived) {
  const id = backendThreadIdForArchiveState(runtime.get(), threadId);
  if (!id) return false;
  const cwd = runtime.requireCwd('thread.archive');
  if (runtime.get().threadArchiveLoadingByThread?.[id]) return false;

  const originalThreads = runtime.get().threads;
  const originalActiveThreadId = runtime.get().activeThreadId;
  const archivedAt = archived ? clockNowMillis() : 0;

  runtime.set((state) => archiveThreadOptimisticState(state, {
    id,
    archived,
    archivedAt,
    timestamp: clockNowMillis(),
  }));

  try {
    await applyThreadArchiveRPC(id, archived);
  }
  catch (error) {
    const message = error?.message || String(error);
    runtime.set((state) => archiveThreadFailureState(state, {
      id,
      originalThreads,
      originalActiveThreadId,
      actionNotice: archiveFailureNotice(archived, message),
    }));
    runtime.addWarning('error', `thread.${archived ? 'archive' : 'unarchive'}.failed`, { threadId: id, error: message });
    return false;
  }

  runtime.set((state) => threadArchiveLoadingClearedPatch(state, id));
  try {
    await writeThreadArchivePreference(cwd, id, archivedAt);
  }
  catch (error) {
    const message = error?.message || String(error);
    runtime.set({ actionNotice: archivePreferenceFailureNotice(archived, message) });
    runtime.addWarning('error', `thread.${archived ? 'archive' : 'unarchive'}.preference.failed`, { threadId: id, error: message });
    return true;
  }

  runtime.set({
    actionNotice: actionNotice(archived ? '线程已归档' : '线程已恢复到列表', 'success'),
  });
  return true;
}

function createThreadArchiveActions(runtime) {
  return {
    archiveThread: (threadId, archived) => archiveThreadAction(runtime, threadId, archived),
  };
}

function normalizedDeleteThreadIds(runtime, threadIds) {
  return [...new Set((Array.isArray(threadIds) ? threadIds : [])
    .map((threadId) => backendThreadIdForArchiveState(runtime.get(), threadId))
    .filter(Boolean))];
}

async function deleteThreadsById(runtime, ids) {
  const deletedIds = [];
  const failedIds = [];
  for (const id of ids) {
    try {
      await deleteThreadRPC({ threadId: id });
      deletedIds.push(id);
    }
    catch (error) {
      failedIds.push(id);
      runtime.addWarning('warn', 'thread.delete.failed', { threadId: id, error: error.message || String(error) });
    }
  }
  return { deletedIds, failedIds };
}

function clearArchivedPreference(cwd, id) {
  return setPreference({
    cwd,
    key: `archivedThreadAtById.${id}`,
    value: null,
  });
}

function deletedThreadsNotice(deletedIds, failedIds) {
  return actionNotice(
    failedIds.length > 0
      ? `已删除 ${deletedIds.length} 个无用会话，${failedIds.length} 个失败`
      : `已删除 ${deletedIds.length} 个无用会话`,
    failedIds.length > 0 ? 'warning' : 'success',
  );
}

function deletedThreadsPatch(state, deletedIds, failedIds) {
  const deletedSet = new Set(deletedIds);
  const threadIsRetained = (thread) => !deletedSet.has(thread.id);
  return {
    activeThreadId: deletedSet.has(state.activeThreadId) ? '' : state.activeThreadId,
    threads: state.threads.filter(threadIsRetained),
    sidebarThreadsByProject: mapSidebarThreadCache(state, (threads) => threads.filter(threadIsRetained)),
    actionNotice: deletedThreadsNotice(deletedIds, failedIds),
    lastListMutationTime: clockNowMillis(),
  };
}

async function deleteStaleThreadsAction(runtime, threadIds) {
  const ids = normalizedDeleteThreadIds(runtime, threadIds);
  if (ids.length === 0) return { deleted: 0, failed: 0 };
  const cwd = runtime.requireCwd('thread.delete');
  const { deletedIds, failedIds } = await deleteThreadsById(runtime, ids);
  if (deletedIds.length > 0) {
    await Promise.all(deletedIds.map((id) => clearArchivedPreference(cwd, id)));
    runtime.set((state) => deletedThreadsPatch(state, deletedIds, failedIds));
  } else {
    runtime.set({
      actionNotice: actionNotice(`删除无用会话失败：${failedIds.length} 个失败`, 'error'),
    });
  }
  return { deleted: deletedIds.length, failed: failedIds.length };
}

function createThreadDeleteActions(runtime) {
  return {
    deleteStaleThreads: (threadIds) => deleteStaleThreadsAction(runtime, threadIds),
  };
}
export { createActiveThreadActions, createDashboardCommandActions, createThreadArchiveActions, createThreadCopyActions, createThreadDeleteActions, createThreadRenamePinActions };
