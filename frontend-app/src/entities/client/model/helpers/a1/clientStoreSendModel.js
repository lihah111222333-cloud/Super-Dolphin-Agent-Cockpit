
import { firstOptionalPresent, optionalTextField } from '../../contractStoreModel.js';
import { deleteThread as deleteThreadRPC, listSharedFiles, recoverThread, readSharedFile, saveClipboardImage, selectFiles, setPreference, setThreadConfig } from '../../../../../shared/api/backendApi.js';
import { sessionApi } from '../../../../../shared/api/sessionApi.js';
import { recoveryActionMessageFromRPCError } from '../../../../../shared/recovery/recoveryFailure.js';
import { appendUniqueAttachments, attachmentKey, buildTurnInput, createImageFileAttachment, droppedFilePath, fileListOf, fileLooksImage, normalizeAttachment, normalizeFileAttachment } from '../../composerAttachments.js';
import {
  addComposerCapability,
  cloneComposerCapabilities,
  composerCapabilityRequestFields,
  reconcileComposerCapabilities,
  removeComposerCapability,
} from '../../capabilities/composerCapabilities.js';
import { buildForkThreadState, cachedForkSharedFiles, createLoadForkSharedFiles, forkSourceTitle, initialForkSharedFilePaths, mergeForkSharedFilesWithSelected, normalizeForkSharedFiles } from '../../threadForkState.js';
import { normalizeActiveProviderName, normalizeProviderConfigValue, normalizeProviderRuntimeConfig, requireProviderPreferenceValue } from '../providerRuntimeConfig.js';
import { providerPreferenceKey } from '../providerPreferences.js';
import { normalizeThreadId, normalizeThreadIdentity } from '../threadIdentity.js';
import { threadActivityTimestamp } from '../threadActivityMetrics.js';
import {
  DEFAULT_PROVIDER,
  clockNowISO,
  clockNowMillis,
  emptyForkDraft,
  normalizeProviderName,
  normalizeString,
  normalizeThreadConfig,
  optionalUiArray,
  resolveLaunchPreferences,
  mapSidebarThreadCache,
  sidebarThreadsByProjectUpsert,
} from './clientStoreUtils.js';
import {
  backendThreadIdForState,
  cwdForExistingThreadSend,
  reusableThreadIdForSend,
  threadConfigTargetIdForState,
  threadMatchesIdentifier,
} from './clientStoreRuntimeThreadModel.js';

function actionNotice(message, tone = 'info', category = '') {
  const normalized = normalizeString(message);
  if (!normalized) return null;
  const normalizedCategory = normalizeString(category);
  return {
    message: normalized,
    tone,
    timestamp: clockNowISO(),
    ...(normalizedCategory ? { category: normalizedCategory } : {}),
  };
}

function actionNoticeRuntimeFields(fields = {}) {
  const out = {};
  const error = normalizeString(fields.error || fields.message);
  const category = normalizeString(fields.category);
  if (error) out.error = error;
  if (category) out.category = category;
  if (typeof fields.recoverable === 'boolean') out.recoverable = fields.recoverable;
  return out;
}

const imageFileAttachment = createImageFileAttachment({ saveClipboardImage });

const composerAttachmentActionDeps = {
  actionNotice,
  appendUniqueAttachments,
  attachmentKey,
  droppedFilePath,
  fileListOf,
  fileLooksImage,
  imageFileAttachment,
  normalizeAttachment,
  normalizeFileAttachment,
  normalizeString,
  selectFiles: () => selectFiles(),
};

const composerActionDeps = {
  attachment: composerAttachmentActionDeps,
  capability: {
    addComposerCapability,
    reconcileComposerCapabilities,
    removeComposerCapability,
  },
  model: {
    actionNotice,
    composerModelConfigTarget,
    normalizeThreadConfig,
    saveGlobalComposerModelConfig,
    saveThreadComposerModelConfig,
    setThreadConfig: (payload) => setThreadConfig(payload),
    threadConfigTargetIdForState,
  },
  modelProvider: {
    actionNotice,
    defaultProvider: DEFAULT_PROVIDER,
    normalizeProviderRuntimeConfig,
    providerPreferenceKey,
    requireProviderPreferenceValue,
    setPreference: (payload) => setPreference(payload),
  },
  send: {
    actionNotice,
    createSendDraftRequest,
    createdThreadIdForSendRollback,
    deleteProvisionalThreadAfterSendFailure,
    freshThreadRetryRequest,
    isCodexIdentityAutoResumeError,
    optimisticSendDraftState,
    promotedDraftThreadState,
    resolveLaunchPreferences,
    rollbackSendDraftState,
    recoveryActionMessageFromRPCError,
    saveFailedSendDraftSnapshot,
    sendRollbackRestoresVisibleComposer,
    startNewDraftThread,
    startTurnWithStoppedThreadRecovery,
  },
};

function createLaunchIntentId() {
  const id = globalThis.crypto?.randomUUID?.() || `${clockNowMillis()}-${Math.random().toString(16).slice(2)}`;
  return `launch_${id}`;
}

function sendDraftThreadName(text) {
  return normalizeString(text).slice(0, 40) || '新对话';
}

function createSendDraftRequest(state, cwd) {
  const text = normalizeString(state.draft);
  const attachments = state.attachments.map(normalizeAttachment).filter(Boolean);
  const input = buildTurnInput(text, attachments);
  if (input.length === 0) return null;
  const capabilityPayload = composerCapabilityRequestFields(state.composerCapabilities);
  const previousActiveThreadId = state.activeThreadId;
  const previousThreadId = reusableThreadIdForSend(state, previousActiveThreadId);
  const launchIntentId = createLaunchIntentId();
  const provisionalThreadId = previousThreadId || launchIntentId;
  const requestCwd = previousThreadId ? cwdForExistingThreadSend(state, previousThreadId) : cwd;
  return {
    cwd: requestCwd,
    text,
    attachments,
    input,
    capabilityPayload,
    previousDraft: state.draft,
    previousAttachments: state.attachments,
    previousComposerCapabilities: cloneComposerCapabilities(state.composerCapabilities),
    previousActiveThreadId,
    previousThreadId,
    launchIntentId,
    provisionalThreadId,
    optimisticItem: {
      id: `user-${launchIntentId}`,
      role: 'user',
      text,
      attachments,
      time: clockNowISO(),
      done: true,
      optimistic: true,
    },
  };
}

function freshThreadRetryRequest(request) {
  const launchIntentId = createLaunchIntentId();
  return {
    ...request,
    previousThreadId: '',
    launchIntentId,
    provisionalThreadId: launchIntentId,
    optimisticItem: {
      ...request.optimisticItem,
      id: `user-${launchIntentId}`,
    },
  };
}

function dashboardCommandTemplate(card) {
  return normalizeString(firstOptionalPresent(card?.command_template, card?.commandTemplate));
}

function dashboardCommandPrompt(card) {
  const command = dashboardCommandTemplate(card);
  if (!command) throw new Error('dashboard command card command_template is required');
  return `请执行以下命令并反馈结果：\n${command}`;
}

function createDashboardCommandRequest(state, cwd, card) {
  return createSendDraftRequest({
    ...state,
    draft: dashboardCommandPrompt(card),
    attachments: [],
  }, cwd);
}

function forkSourceThread(state, threadId) {
  const id = normalizeThreadId(threadId);
  if (!id) return null;
  return state.threads.find((thread) => threadMatchesIdentifier(thread, id)) || null;
}

function addForkThreadState(options) {
  const {
    state,
    threadId,
    sourceThreadId,
    sourceThread,
    identity,
    provisionalName,
    kickoffText,
  } = options;
  return buildForkThreadState({
    state,
    threadId,
    sourceThreadId,
    sourceThread,
    identity,
    provisionalName,
    kickoffText,
    deps: {
      actionNotice,
      defaultProvider: DEFAULT_PROVIDER,
      emptyForkDraft,
      threadActivityTimestamp,
      threadMatchesIdentifier,
    },
  });
}

const loadForkSharedFiles = createLoadForkSharedFiles({ readSharedFile });

const forkActionDeps = {
  actionNotice,
  addForkThreadState,
  backendThreadIdForState,
  cachedForkSharedFiles,
  emptyForkDraft,
  forkThread: (payload) => sessionApi.fork(payload),
  forkSourceThread,
  forkSourceTitle,
  initialForkSharedFilePaths,
  listSharedFiles: () => listSharedFiles(),
  loadForkSharedFiles,
  mergeForkSharedFilesWithSelected,
  normalizeForkSharedFiles,
  normalizeString,
  normalizeThreadIdentity,
  startTurn: (payload) => sessionApi.startTurn(payload),
};

function optimisticSendThreads(threads = [], previousThreadId = '') {
  if (!previousThreadId || !threads.some((thread) => threadMatchesIdentifier(thread, previousThreadId))) return threads;
  return [
    threads.find((thread) => threadMatchesIdentifier(thread, previousThreadId)),
    ...threads.filter((thread) => !threadMatchesIdentifier(thread, previousThreadId)),
  ];
}

function optimisticSendDraftState(state, request) {
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
      [request.provisionalThreadId]: [
        ...(state.timelinesByThread[request.provisionalThreadId] || optionalUiArray()),
        request.optimisticItem,
      ],
    },
  };
}

async function startNewDraftThread(request, resolveLaunchPreferences) {
  const launchPreferences = await resolveLaunchPreferences(request.cwd);
  const thread = await sessionApi.start({
    cwd: request.cwd,
    name: sendDraftThreadName(request.text),
    ...launchPreferences,
    deferSpawn: true,
    launchIntentId: request.launchIntentId,
  });
  const identity = normalizeThreadIdentity(thread);
  if (!identity.threadId) throw new Error('thread/start response missing threadId');
  return { identity, launchPreferences, threadId: identity.threadId };
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
  const activeThreadId = state.activeThreadId === request.provisionalThreadId ? started.threadId : state.activeThreadId;
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
  for (const threadId of localDeleteIds) delete timelinesByThread[threadId];
  const threadTimelineReadyByThread = { ...state.threadTimelineReadyByThread };
  const activityThreadAtById = { ...state.activityThreadAtById };
  for (const threadId of localDeleteIds) {
    delete threadTimelineReadyByThread[threadId];
    delete activityThreadAtById[threadId];
  }
  const activeThreadId = localDeleteIds.includes(state.activeThreadId) ? request.previousActiveThreadId : state.activeThreadId;
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
    {
      ...runtime.get(),
      cwd: request.cwd,
      activeProject: request.cwd,
    },
    request.previousActiveThreadId,
    {
      draft: request.previousDraft,
      attachments: request.previousAttachments,
      composerCapabilities: request.previousComposerCapabilities,
    },
  );
}

async function deleteProvisionalThreadAfterSendFailure(threadId, addWarning) {
  if (!threadId) return;
  try {
    await deleteThreadRPC({ threadId });
  }
  catch (cleanupError) {
    addWarning('warn', 'thread.provisional.delete.failed', {
      threadId,
      error: cleanupError.message || String(cleanupError),
    });
  }
}

function isStoppedThreadTurnStartError(error) {
  const message = normalizeString(firstOptionalPresent(error?.message, error?.cause?.message, optionalTextField(error))).toLowerCase();
  return message.includes('resolve session: thread') && message.includes(' is stopped');
}

function isCodexIdentityAutoResumeError(error) {
  const message = normalizeString(firstOptionalPresent(error?.message, error?.cause?.message, optionalTextField(error))).toLowerCase();
  return message.includes('resolve session: auto-resume failed') &&
    message.includes('codex identity required for resume');
}

async function startTurnWithStoppedThreadRecovery(params) {
  try {
    return await sessionApi.startTurn(params);
  } catch (error) {
    if (!isStoppedThreadTurnStartError(error)) throw error;
    await recoverThread({ cwd: params.cwd, threadId: params.threadId });
    return sessionApi.startTurn(params);
  }
}

function hasComposerConfigKey(config, key) {
  return Object.prototype.hasOwnProperty.call(config, key);
}

function composerConfigRequestedThreadId(config, state) {
  const hasThreadTarget = hasComposerConfigKey(config, 'threadId') || hasComposerConfigKey(config, 'thread_id');
  return hasThreadTarget ? (config.threadId || config.thread_id) : state.activeThreadId;
}

async function composerModelConfigTarget(config, state, loadThreadConfig) {
  const threadId = threadConfigTargetIdForState(state, composerConfigRequestedThreadId(config, state));
  const existingConfig = threadId ? state.threadConfigByThread[threadId] : null;
  return {
    threadId,
    threadConfig: existingConfig || (threadId ? await loadThreadConfig(threadId) : null),
    hasModel: hasComposerConfigKey(config, 'model'),
    hasEffort: hasComposerConfigKey(config, 'effort'),
    nextModel: normalizeProviderConfigValue(config.model),
    nextEffort: normalizeProviderConfigValue(config.effort),
  };
}

async function saveThreadComposerModelConfig(target, set, addWarning) {
  const provider = normalizeProviderName(target.threadConfig.provider) || DEFAULT_PROVIDER;
  set({ threadConfigSaving: true });
  try {
    const saved = await setThreadConfig({
      threadId: target.threadId,
      model: target.hasModel ? target.nextModel : normalizeProviderConfigValue(target.threadConfig.override.model),
      effort: target.hasEffort ? target.nextEffort : normalizeProviderConfigValue(target.threadConfig.override.effort),
    });
    const normalized = normalizeThreadConfig(saved, target.threadId, provider);
    set((current) => ({
      threadConfigByThread: {
        ...current.threadConfigByThread,
        [target.threadId]: normalized,
      },
      threadConfigSaving: false,
      actionNotice: actionNotice('线程配置已保存，下次发送生效。', 'success'),
    }));
    return true;
  }
  catch (error) {
    set({
      threadConfigSaving: false,
      actionNotice: actionNotice('线程配置保存失败，请重试。', 'error'),
    });
    addWarning('error', 'thread.config.set.failed', { threadId: target.threadId, error: 'action failure; see Health diagnostic ID' });
    throw error;
  }
}

async function saveGlobalComposerModelConfig(cwd, state, target, set, notifyRPCFailure) {
  const provider = normalizeActiveProviderName(state.provider, 'provider.config') || DEFAULT_PROVIDER;
  const current = state.providerConfig || normalizeProviderRuntimeConfig({}, provider);
  const value = normalizeProviderRuntimeConfig({
    model: target.hasModel ? target.nextModel || current.model : current.model,
    effort: target.hasEffort ? target.nextEffort || current.effort : current.effort,
    codexModelProvider: current.codexModelProvider,
  }, provider);
  try {
    await setPreference({ cwd, key: providerPreferenceKey(provider, 'model'), value: value.model });
    await setPreference({ cwd, key: providerPreferenceKey(provider, 'effort'), value: value.effort });
    set({
      providerConfig: value,
      actionNotice: actionNotice('全局模型配置已保存', 'success'),
    });
    return true;
  }
  catch (error) {
    notifyRPCFailure('全局模型配置保存', 'provider.config.save.failed', error, { provider });
    throw error;
  }
}

function compareSequence(left, right) {
  try {
    const a = BigInt(normalizeString(left) || '0');
    const b = BigInt(normalizeString(right) || '0');
    if (a === b) return 0;
    return a < b ? -1 : 1;
  }
  catch {
    return 0;
  }
}

export {
  actionNotice,
  actionNoticeRuntimeFields,
  addForkThreadState,
  composerActionDeps,
  composerAttachmentActionDeps,
  compareSequence,
  composerConfigRequestedThreadId,
  composerModelConfigTarget,
  createDashboardCommandRequest,
  createLaunchIntentId,
  createSendDraftRequest,
  createdThreadIdForSendRollback,
  deleteProvisionalThreadAfterSendFailure,
  forkActionDeps,
  forkSourceThread,
  freshThreadRetryRequest,
  imageFileAttachment,
  isCodexIdentityAutoResumeError,
  isStoppedThreadTurnStartError,
  loadForkSharedFiles,
  optimisticSendDraftState,
  optimisticSendThreads,
  promotedDraftThreadState,
  rollbackSendDraftState,
  recoveryActionMessageFromRPCError,
  saveFailedSendDraftSnapshot,
  saveGlobalComposerModelConfig,
  saveThreadComposerModelConfig,
  sendDraftThreadName,
  sendRollbackRestoresVisibleComposer,
  startNewDraftThread,
  startTurnWithStoppedThreadRecovery,
};
