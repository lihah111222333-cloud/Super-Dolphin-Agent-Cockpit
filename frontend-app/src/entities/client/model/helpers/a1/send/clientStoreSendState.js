import {
  DEFAULT_PROVIDER,
  mapSidebarThreadCache,
  normalizeString,
  optionalUiArray,
  sidebarThreadsByProjectUpsert,
} from '../clientStoreUtils.js';
import { backendThreadIdForState, threadMatchesIdentifier } from '../clientStoreRuntimeThreadModel.js';
import { threadActivityTimestamp } from '../../threadActivityMetrics.js';
import { actionNotice } from './clientStoreActionNotice.js';
import { sendDraftThreadName } from './clientStoreSendInput.js';
import { recoveryActionMessageFromRPCError } from '../../../../../../shared/recovery/recoveryFailure.js';

function optimisticSendThreads(threads = [], previousThreadId = '') {
  if (!previousThreadId || !threads.some((thread) => threadMatchesIdentifier(thread, previousThreadId))) {
    return threads;
  }
  return [
    threads.find((thread) => threadMatchesIdentifier(thread, previousThreadId)),
    ...threads.filter((thread) => !threadMatchesIdentifier(thread, previousThreadId)),
  ];
}

function optimisticSendDraftState(state, request) {
  const existingTimeline = state.timelinesByThread[request.provisionalThreadId] || optionalUiArray();
  return {
    sending: true,
    error: '',
    draft: '',
    attachments: [],
    composerCapabilities: [],
    actionNotice: actionNotice('消息已发送，等待回复', 'info'),
    activeThreadId: request.provisionalThreadId,
    activityThreadAtById: {
      ...state.activityThreadAtById,
      [request.provisionalThreadId]: threadActivityTimestamp(),
    },
    threads: optimisticSendThreads(state.threads, request.previousThreadId),
    timelinesByThread: {
      ...state.timelinesByThread,
      [request.provisionalThreadId]: [...existingTimeline, request.optimisticItem],
    },
  };
}

function promotedDraftThreadState(state, request, started) {
  const timelinesByThread = { ...state.timelinesByThread };
  const activityThreadAtById = { ...state.activityThreadAtById };
  const provisionalTimeline = timelinesByThread[request.provisionalThreadId] || optionalUiArray();
  delete timelinesByThread[request.provisionalThreadId];
  timelinesByThread[started.threadId] = provisionalTimeline;
  if (activityThreadAtById[request.provisionalThreadId]) {
    activityThreadAtById[started.threadId] = activityThreadAtById[request.provisionalThreadId];
    delete activityThreadAtById[request.provisionalThreadId];
  }
  const provider = started.launchPreferences.modelProvider || started.launchPreferences.provider || DEFAULT_PROVIDER;
  const activeThreadId = state.activeThreadId === request.provisionalThreadId
    ? started.threadId
    : state.activeThreadId;
  const promotedThread = {
    id: started.threadId,
    agentId: started.identity.agentId,
    providerThreadId: started.identity.providerThreadId,
    sessionId: started.identity.sessionId,
    cwd: request.cwd,
    name: sendDraftThreadName(request.text),
    provider,
    status: '工作中',
  };
  return {
    activeThreadId,
    provider,
    activityThreadAtById,
    timelinesByThread,
    threadTimelineReadyByThread: {
      ...state.threadTimelineReadyByThread,
      [started.threadId]: true,
    },
    sidebarThreadsByProject: sidebarThreadsByProjectUpsert(state, request.cwd, promotedThread),
    threads: [
      promotedThread,
      ...state.threads.filter((item) => item.id !== started.threadId),
    ],
  };
}

function rollbackSendDraftState(state, request, error, options = {}) {
  const displayMessage = recoveryActionMessageFromRPCError(error) || '发送失败，请重试。';
  const createdThreadId = normalizeString(options.createdThreadId);
  const localDeleteIds = !request.previousThreadId
    ? [request.provisionalThreadId, createdThreadId].filter(Boolean)
    : [];
  const timelinesByThread = { ...state.timelinesByThread };
  const timelineTargetId = request.previousThreadId || createdThreadId || request.provisionalThreadId;
  const requestTimeline = timelinesByThread[timelineTargetId] || optionalUiArray();
  timelinesByThread[timelineTargetId] = requestTimeline.filter((item) => item.id !== request.optimisticItem.id);
  for (const threadId of localDeleteIds) {
    delete timelinesByThread[threadId];
  }
  const threadTimelineReadyByThread = { ...state.threadTimelineReadyByThread };
  const activityThreadAtById = { ...state.activityThreadAtById };
  for (const threadId of localDeleteIds) {
    delete threadTimelineReadyByThread[threadId];
    delete activityThreadAtById[threadId];
  }
  const activeThreadId = localDeleteIds.includes(state.activeThreadId)
    ? request.previousActiveThreadId
    : state.activeThreadId;
  const restoreComposer = [
    request.previousThreadId,
    request.provisionalThreadId,
    createdThreadId,
  ].filter(Boolean).includes(state.activeThreadId);
  return {
    sending: false,
    ...(restoreComposer ? {
      draft: request.previousDraft,
      attachments: request.previousAttachments,
      composerCapabilities: request.previousComposerCapabilities,
    } : {}),
    activeThreadId,
    timelinesByThread,
    threadTimelineReadyByThread,
    activityThreadAtById,
    threads: createdThreadId
      ? state.threads.filter((thread) => thread.id !== createdThreadId)
      : state.threads,
    sidebarThreadsByProject: createdThreadId
      ? mapSidebarThreadCache(state, (threads) => threads.filter((thread) => thread.id !== createdThreadId))
      : state.sidebarThreadsByProject,
    error: displayMessage,
    actionNotice: actionNotice(displayMessage, 'error', 'send'),
  };
}

function createdThreadIdForSendRollback(state, request, threadId) {
  if (request.previousThreadId || !threadId) return '';
  return backendThreadIdForState(state, threadId);
}

function sendRollbackRestoresVisibleComposer(state, request, createdThreadId = '') {
  const activeThreadId = normalizeString(state.activeThreadId);
  return [
    request.previousThreadId,
    request.provisionalThreadId,
    createdThreadId,
  ].map(normalizeString).filter(Boolean).includes(activeThreadId);
}

function saveFailedSendDraftSnapshot(runtime, request) {
  runtime.saveComposerDraftSnapshot(
    { ...runtime.get(), cwd: request.cwd, activeProject: request.cwd },
    request.previousActiveThreadId,
    {
      draft: request.previousDraft,
      attachments: request.previousAttachments,
      composerCapabilities: request.previousComposerCapabilities,
    },
  );
}

export {
  createdThreadIdForSendRollback,
  optimisticSendDraftState,
  optimisticSendThreads,
  promotedDraftThreadState,
  rollbackSendDraftState,
  saveFailedSendDraftSnapshot,
  sendRollbackRestoresVisibleComposer,
};
