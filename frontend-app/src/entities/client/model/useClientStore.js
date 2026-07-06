import { create } from 'zustand';
import {
  addProject as addProjectRPC,
  archiveThread as archiveThreadRPC,
  compactThread,
  deleteThread as deleteThreadRPC,
  forceCompleteTurn,
  getProjects,
  getSidebarState,
  getThreadConfig,
  getThreadMessages,
  getThreadState,
  getWindowBootstrap,
  getPreference,
  interruptTurn,
  listSharedFiles,
  onBridgeEvent,
  onRuntimeReconnect,
  openNewWindow as openNewWindowRPC,
  readSharedFile,
  readConfig,
  recoverThread,
  registerBridgeLogStore,
  renameThread as renameThreadRPC,
  respondApproval as respondApprovalRPC,
  resolveThreadIdentity,
  saveClipboardImage,
  beginTextClipboardWrite,
  copyTextToClipboard,
  emitFrontendTraceEvent,
  selectFiles,
  selectProjectDir,
  setActiveProject as setActiveProjectRPC,
  setPreference,
  setThreadConfig,
  removeProject as removeProjectRPC,
  unarchiveThread as unarchiveThreadRPC,
} from '../../../shared/api/backendApi.js';
import { positiveApprovalRequestIdFromFields } from '../../../shared/api/approvalRequestId.js';
import { sessionApi } from '../../../shared/api/sessionApi.js';
import {
  createComposerSlice,
} from './composerSlice.js';
import { createForkSlice } from './forkSlice.js';
import {
  normalizeKnownProviderName,
  providerPreferenceKey,
  RUNTIME_PROVIDER,
} from './providerPreferences.js';
import {
  PROVIDER_ACTIVE_PREF_KEY,
  codexLaunchConfigFromPreferences,
  isPreferenceAbsent,
  isPreferenceTombstone,
  knownProviderName,
  normalizeActiveProviderName,
  normalizeCodexIdentityValue,
  normalizeProviderConfigValue,
  normalizeProviderRuntimeConfig,
  providerDisplayDefaultConfig,
  requireActiveProviderPreference,
  requireProviderPreferenceValue,
} from './providerRuntimeConfig.js';
import {
  bridgePatchData,
  bridgePatchState,
} from './bridgePatchState.js';
import {
  ACTIVE_PROMPT_PREF_KEY,
  bridgeRevisionKey,
  isDagNodeStatusBridgeEvent,
} from './bridgeRevision.js';
import { createProjectSlice } from './projectSlice.js';
import {
  createRuntimeResultHelpers,
} from './runtimeResults.js';
import { createRuntimeSlice } from './runtimeSlice.js';
import {
  isVisibleTimelineItem,
  mergeTimelineItems,
  normalizeTimelineItem,
} from './timelineRuntime.js';
import {
  appendAssistantDeltaText,
  assistantDeltaBufferKey,
  isAssistantMessageDeltaEvent,
  mergeRuntimeAssistantCompletion,
  runtimeAssistantCompletion,
  runtimeAssistantFallbackId,
} from './runtimeAssistantTimeline.js';
import {
  isAgentRuntimeId,
  normalizeBackendThreadId,
  normalizeThreadId,
  normalizeThreadIdentity,
} from './threadIdentity.js';
import {
  appendUniqueAttachments,
  attachmentKey,
  basename,
  buildTurnInput,
  composerDraftKey,
  composerScopeCwd,
  createImageFileAttachment,
  droppedFilePath,
  fileListOf,
  fileLooksImage,
  isEmptyComposerDraftSnapshot,
  normalizeAttachment,
  normalizeComposerDraftSnapshot,
  normalizeFileAttachment,
} from './composerAttachments.js';
import {
  activeTurnPayload,
  isInterruptibleTurnSummary,
  isTerminalActiveTurnStatus,
  normalizeActivityStats,
  normalizeTokenUsage,
  normalizeTurnSummary,
  shouldFloatThreadPatch,
  threadActivityTimestamp,
} from './threadActivityMetrics.js';
import {
  buildThreadCopyPayload,
  firstThreadCopyText,
} from './threadCopyPayload.js';
import {
  applyThreadRename,
  archiveThreadFailureState,
  archiveThreadOptimisticState,
  isArchivedStatus,
} from './threadListMutations.js';
import { attachActiveThreadRpcRuntime } from './threadLifecycleRuntime.js';
import {
  threadOpenHistoryFallbackItems,
} from './threadHistoryTimeline.js';
import {
  attachThreadMessagesRuntime,
} from './threadMessagesRuntime.js';
import {
  buildForkThreadState,
  cachedForkSharedFiles,
  createLoadForkSharedFiles,
  forkSourceTitle,
  initialForkSharedFilePaths,
  mergeForkSharedFilesWithSelected,
  normalizeForkSharedFiles,
} from './threadForkState.js';
import { attachWarningRuntime } from './warningRuntime.js';

const DEFAULT_PROVIDER = RUNTIME_PROVIDER;
const ASSISTANT_DELTA_FLUSH_MS = 50;
const BRIDGE_PATCH_SLOW_MS = 50;
const THREAD_PINS_CHAT_PREF_KEY = 'threadPins.chat';
const objectPrototype = Object.prototype;
const BOOTSTRAP_PAGE_ALIASES = Object.freeze({
  dags: 'workflows',
  'memory-center': 'memory',
  memory: 'files',
});
const APP_PAGE_IDS = new Set(['chat', 'prompts', 'workflows', 'skills', 'memory', 'observability', 'files', 'settings']);
const ROOT_THREAD_IDENTITY_KEYS = Object.freeze(['threadId', 'thread_id', 'codexThreadId', 'codex_thread_id']);
const THREAD_IDENTITY_KEYS = Object.freeze(['threadId', 'thread_id', 'codexThreadId', 'codex_thread_id', 'id']);
const AGENT_IDENTITY_KEYS = Object.freeze(['agentId', 'agent_id']);
/*
 * 这个 store 把后端快照、历史消息和实时事件整理成 UI 状态。
 * 草稿、分页标记、delta 缓冲只是前端本地状态，不能当成后端真实状态。
 */

function emptyForkDraft() {
  return {
    open: false,
    sourceThreadId: '',
    sourceThreadName: '',
    sourceTitle: '',
    sharedFilePaths: [],
    availableSharedFiles: [],
    loadingSharedFiles: false,
    submitting: false,
    error: '',
    kickoffError: '',
  };
}

function normalizeString(value) {
  return (value || '').toString().trim();
}

function objectRecord(value) {
  return value && typeof value === 'object' && !Array.isArray(value) ? value : {};
}

function firstFieldValue(source, keys = []) {
  const record = objectRecord(source);
  for (const key of keys) {
    const value = record[key];
    if (value !== undefined && value !== null && value !== '') return value;
  }
  return undefined;
}

function firstValueFromSources(sources = []) {
  for (const [source, keys] of sources) {
    const value = firstFieldValue(source, keys);
    if (value !== undefined) return value;
  }
  return undefined;
}

function cleanObject(payload) {
  return Object.fromEntries(
    Object.entries(payload).filter(([, value]) => value !== undefined && value !== ''),
  );
}

function normalizePath(value) {
  const path = normalizeString(value);
  if (!path) return '';
  if (path !== '/' && !/^[a-zA-Z]:[\\/]?$/.test(path)) {
    return path.replace(/[\\/]+$/, '');
  }
  return path;
}

function sidebarProjectKey(value) {
  return normalizePath(value).replace(/\\/g, '/').replace(/\/+$/g, '').toLowerCase();
}

function sidebarThreadsByProjectWith(state, projectPath, threads) {
  const key = sidebarProjectKey(projectPath);
  if (!key) return state.sidebarThreadsByProject || {};
  return {
    ...(state.sidebarThreadsByProject || {}),
    [key]: Array.isArray(threads) ? threads : [],
  };
}

function sidebarThreadsByProjectUpsert(state, projectPath, thread) {
  const key = sidebarProjectKey(projectPath);
  if (!key || !thread?.id) return state.sidebarThreadsByProject || {};
  const current = objectRecord(state.sidebarThreadsByProject);
  const threads = Array.isArray(current[key]) ? current[key] : [];
  return {
    ...current,
    [key]: [
      thread,
      ...threads.filter((item) => item?.id !== thread.id),
    ],
  };
}

function mapSidebarThreadCache(state, mapThreads) {
  const current = objectRecord(state.sidebarThreadsByProject);
  let changed = false;
  const next = {};
  for (const [projectKey, threads] of Object.entries(current)) {
    const sourceThreads = Array.isArray(threads) ? threads : [];
    const mappedThreads = mapThreads(sourceThreads);
    next[projectKey] = mappedThreads;
    if (mappedThreads !== threads) changed = true;
  }
  return changed ? next : current;
}

function normalizeTimestamp(value) {
  if (typeof value === 'boolean' || value === null || value === undefined) return 0;
  if (typeof value === 'number') return Number.isFinite(value) && value > 0 ? value : 0;
  const text = normalizeString(value);
  if (!text) return 0;
  const asNumber = Number(text);
  if (Number.isFinite(asNumber)) return asNumber > 0 ? asNumber : 0;
  // 截断高精度时间戳中的多余小数秒，以兼容 JS Date.parse 的 3 位毫秒限制
  const sanitized = text.replace(/(\.\d{3})\d+/g, '$1');
  const parsed = Date.parse(sanitized);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : 0;
}

function normalizeTimestampMap(value) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return {};
  return Object.fromEntries(
    Object.entries(value)
      .map(([key, timestamp]) => [normalizeThreadId(key), normalizeTimestamp(timestamp)])
      .filter(([key, timestamp]) => key && timestamp > 0),
  );
}

function normalizeProviderName(value) {
  return normalizeKnownProviderName(value);
}

async function getScopedPreference(cwd, key) {
  const scope = normalizeString(cwd);
  if (scope) {
    const scoped = await getPreference({ cwd: scope, key });
    if (isPreferenceTombstone(scoped)) return '';
    if (!isPreferenceAbsent(scoped)) return scoped;
  }
  const globalValue = await getPreference({ key });
  if (isPreferenceTombstone(globalValue)) return '';
  return isPreferenceAbsent(globalValue) ? null : globalValue;
}

function sanitizeLaunchSandboxPreference(value) {
  if (isPreferenceAbsent(value) || isPreferenceTombstone(value)) return undefined;
  if (typeof value === 'string') return normalizeProviderConfigValue(value);
  if (!value || typeof value !== 'object' || Array.isArray(value)) return undefined;
  return value;
}

function normalizeThreadConfig(raw = {}, fallbackThreadId = '', fallbackProvider = DEFAULT_PROVIDER) {
  const source = raw && typeof raw === 'object' && !Array.isArray(raw) ? raw : {};
  const provider = normalizeProviderName(source.provider || fallbackProvider) || DEFAULT_PROVIDER;
  const defaults = providerDisplayDefaultConfig(provider);
  return {
    threadId: normalizeThreadId(source.threadId || source.thread_id || fallbackThreadId),
    provider,
    supportsThreadOverride: Boolean(source.supportsThreadOverride ?? source.supports_thread_override),
    override: {
      model: normalizeProviderConfigValue(source.override?.model),
      effort: normalizeProviderConfigValue(source.override?.effort),
    },
    effective: {
      model: normalizeProviderConfigValue(source.effective?.model) || defaults.model,
      effort: normalizeProviderConfigValue(source.effective?.effort) || defaults.effort,
    },
  };
}

function normalizeBootstrapSnapshot(raw) {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) {
    throw new Error('window bootstrap response must be an object');
  }
  if (!Object.prototype.hasOwnProperty.call(raw, 'snapshot')) {
    throw new Error('window bootstrap response snapshot is required');
  }
  if (raw.snapshot == null) return {};
  if (typeof raw.snapshot !== 'object' || Array.isArray(raw.snapshot)) {
    throw new Error('window bootstrap snapshot must be an object');
  }
  return raw.snapshot;
}

function normalizeBootstrapPage(value) {
  const raw = normalizeString(value);
  if (!raw) return '';
  const page = BOOTSTRAP_PAGE_ALIASES[raw] || raw;
  return APP_PAGE_IDS.has(page) ? page : '';
}

async function resolveLaunchPreferences(cwd, addWarning = null) {
  const activeProviderValue = await getPreference({ cwd, key: PROVIDER_ACTIVE_PREF_KEY });
  let provider;
  try {
    provider = normalizeActiveProviderName(activeProviderValue, 'startThread');
  } catch (error) {
    const unsupportedProvider = knownProviderName(activeProviderValue);
    if (unsupportedProvider && typeof addWarning === 'function') {
      addWarning('error', 'provider.unsupported', { provider: unsupportedProvider, reason: 'startThread' });
    }
    throw error;
  }
  if (!provider) {
    throw new Error('startThread: settings.provider.active preference is empty — cannot determine provider. Please select a provider in Settings.');
  }

  const [
    model,
    effort,
    activePromptKey,
    codexHome,
    codexInstanceKey,
    codexModelProvider,
    sandbox,
    approvalPolicy,
    personality,
    summary,
  ] = await Promise.all([
    getPreference({ cwd, key: providerPreferenceKey(provider, 'model') }),
    getPreference({ cwd, key: providerPreferenceKey(provider, 'effort') }),
    getPreference({ cwd, key: ACTIVE_PROMPT_PREF_KEY }),
    getScopedPreference(cwd, providerPreferenceKey('codex', 'codexHome')),
    getScopedPreference(cwd, providerPreferenceKey('codex', 'codexInstanceKey')),
    getScopedPreference(cwd, providerPreferenceKey('codex', 'codexModelProvider')),
    getScopedPreference(cwd, providerPreferenceKey(provider, 'sandbox')),
    getScopedPreference(cwd, providerPreferenceKey(provider, 'approvalPolicy')),
    getScopedPreference(cwd, providerPreferenceKey(provider, 'personality')),
    getScopedPreference(cwd, providerPreferenceKey(provider, 'summary')),
  ]);
  const launch = cleanObject({
    modelProvider: provider,
    model: normalizeProviderConfigValue(model),
    effort: normalizeProviderConfigValue(effort),
    prompt_key: normalizeProviderConfigValue(activePromptKey),
    sandbox: sanitizeLaunchSandboxPreference(sandbox),
    approvalPolicy: normalizeProviderConfigValue(approvalPolicy),
    personality: normalizeProviderConfigValue(personality),
    summary: normalizeProviderConfigValue(summary),
  });
  const normalizedCodexModelProvider = normalizeCodexIdentityValue(codexModelProvider);
  if (normalizedCodexModelProvider) launch.codexModelProvider = normalizedCodexModelProvider;
  const codexConfig = codexLaunchConfigFromPreferences({
    codexHome,
    codexInstanceKey,
    codexModelProvider,
  });
  if (codexConfig) launch.config = codexConfig;
  return launch;
}

function projectShortLabel(path) {
  const value = normalizeString(path);
  if (!value || value === '.') return '当前目录 (.)';
  const segments = value.split(/[\\/]/).filter(Boolean);
  return segments.slice(-2).join('/') || value;
}

const projectActionDeps = {
  addProject: (payload) => addProjectRPC(payload),
  normalizePath,
  openNewWindow: (payload) => openNewWindowRPC(payload),
  projectShortLabel,
  removeProject: (payload) => removeProjectRPC(payload),
  selectProjectDir: (seed) => selectProjectDir(seed),
  setActiveProject: (payload) => setActiveProjectRPC(payload),
};

function hasOwn(value, key) {
  return Boolean(value && typeof value === 'object' && objectPrototype.hasOwnProperty.call(value, key));
}

function archiveMapFromPayload(payload = {}) {
  const direct = payload['threadArchives.chat'] || payload.threadArchivesChat || payload.archivedThreadAtById;
  const nested = payload.threadArchives?.chat || payload.thread_archives?.chat;
  return normalizeTimestampMap(direct || nested || {});
}

function archiveMapFromThreads(threads = []) {
  const entries = [];
  for (const thread of threads || []) {
    const id = normalizeThreadId(thread?.id);
    if (!id) continue;
    const archivedAt = normalizeTimestamp(thread?.archivedAt);
    const value = archivedAt > 0 ? archivedAt : (thread?.archived ? 1 : 0);
    if (value > 0) {
      entries.push([id, value]);
      const agentId = normalizeThreadId(thread?.agentId);
      if (agentId && agentId !== id) {
        entries.push([agentId, value]);
      }
    }
  }
  return Object.fromEntries(entries);
}

function hasArchiveMapPayload(payload = {}) {
  return Boolean(payload && typeof payload === 'object' && (
    Object.prototype.hasOwnProperty.call(payload, 'threadArchives.chat') ||
    Object.prototype.hasOwnProperty.call(payload, 'threadArchivesChat') ||
    Object.prototype.hasOwnProperty.call(payload, 'archivedThreadAtById') ||
    Object.prototype.hasOwnProperty.call(payload.threadArchives || {}, 'chat') ||
    Object.prototype.hasOwnProperty.call(payload.thread_archives || {}, 'chat')
  ));
}

function pinMapFromPayload(payload = {}) {
  const direct = payload['threadPins.chat'] || payload.threadPinsChat || payload.pinnedThreadAtById;
  const nested = payload.threadPins?.chat || payload.thread_pins?.chat;
  return normalizeTimestampMap(direct || nested || {});
}

function hasPinMapPayload(payload = {}) {
  return Boolean(payload && typeof payload === 'object' && (
    Object.prototype.hasOwnProperty.call(payload, 'threadPins.chat') ||
    Object.prototype.hasOwnProperty.call(payload, 'threadPinsChat') ||
    Object.prototype.hasOwnProperty.call(payload, 'pinnedThreadAtById') ||
    Object.prototype.hasOwnProperty.call(payload.threadPins || {}, 'chat') ||
    Object.prototype.hasOwnProperty.call(payload.thread_pins || {}, 'chat')
  ));
}

function sourceThreadObject(raw) {
  return raw?.thread && typeof raw.thread === 'object' ? raw.thread : {};
}

function normalizeThreadStatusText(raw) {
  return firstThreadCopyText(
    raw?.status,
    raw?.state,
    raw?.lifecycleStatus,
    raw?.lifecycle_status,
    raw?.threadStatus,
    raw?.thread_status,
  ) || '等待指示';
}

function normalizeThreadCwdValue(raw, sourceThread, options = {}) {
  return normalizePath(firstThreadCopyText(
    raw?.cwd,
    raw?.CWD,
    raw?.workdir,
    raw?.workDir,
    raw?.work_dir,
    sourceThread?.cwd,
    sourceThread?.CWD,
    sourceThread?.workdir,
    sourceThread?.workDir,
    sourceThread?.work_dir,
    options.fallbackCwd,
  ));
}

function normalizeThreadProviderValue(raw, options = {}) {
  return firstThreadCopyText(
    raw?.provider,
    raw?.modelProvider,
    raw?.model_provider,
    options.fallbackProvider,
  );
}

function normalizeThreadPinnedAt(raw, identity, options = {}) {
  const threadId = identity && typeof identity === 'object' ? identity.threadId : normalizeThreadId(identity);
  const agentId = identity && typeof identity === 'object' ? identity.agentId : '';
  let resolvedDbId = '';
  if (options.state?.threads) {
    const existing = options.state.threads.find((t) =>
      (threadId && threadMatchesIdentifier(t, threadId)) ||
      (agentId && threadMatchesIdentifier(t, agentId))
    );
    if (existing) {
      resolvedDbId = normalizeBackendThreadId(existing.id);
    }
  }
  return normalizeTimestamp(firstThreadCopyText(
    raw?.pinnedAt,
    raw?.pinned_at,
    raw?.pinnedAtMs,
    raw?.pinned_at_ms,
    typeof raw?.pinned === 'boolean' ? 0 : raw?.pinned,
    (threadId && options.pinnedAtById?.[threadId]) ||
    (agentId && options.pinnedAtById?.[agentId]) ||
    (resolvedDbId && options.pinnedAtById?.[resolvedDbId]),
  ));
}

function normalizeThreadArchivedAt(raw, identity, options = {}) {
  const threadId = identity && typeof identity === 'object' ? identity.threadId : normalizeThreadId(identity);
  const agentId = identity && typeof identity === 'object' ? identity.agentId : '';
  let resolvedDbId = '';
  if (options.state?.threads) {
    const existing = options.state.threads.find((t) =>
      (threadId && threadMatchesIdentifier(t, threadId)) ||
      (agentId && threadMatchesIdentifier(t, agentId))
    );
    if (existing) {
      resolvedDbId = normalizeBackendThreadId(existing.id);
    }
  }
  return normalizeTimestamp(firstThreadCopyText(
    raw?.archivedAt,
    raw?.archived_at,
    raw?.archivedAtMs,
    raw?.archived_at_ms,
    typeof raw?.archived === 'boolean' ? 0 : raw?.archived,
    (threadId && options.archivedAtById?.[threadId]) ||
    (agentId && options.archivedAtById?.[agentId]) ||
    (resolvedDbId && options.archivedAtById?.[resolvedDbId]),
  ));
}

function normalizeThreadLifecycleStatus(raw) {
  return firstThreadCopyText(raw?.lifecycleStatus, raw?.lifecycle_status, raw?.threadStatus, raw?.thread_status);
}

function isThreadArchived(raw, status, lifecycleStatus, archivedAt) {
  return Boolean(raw?.archived || raw?.isArchived || archivedAt > 0 || isArchivedStatus(status) || isArchivedStatus(lifecycleStatus));
}

function normalizeThread(raw, options = {}) {
  const identity = normalizeThreadIdentity(raw);
  const sourceThread = sourceThreadObject(raw);
  const status = normalizeThreadStatusText(raw);
  const cwd = normalizeThreadCwdValue(raw, sourceThread, options);
  const provider = normalizeThreadProviderValue(raw, options);
  const pinnedAt = normalizeThreadPinnedAt(raw, identity, options);
  const archivedAt = normalizeThreadArchivedAt(raw, identity, options);
  const lifecycleStatus = normalizeThreadLifecycleStatus(raw);
  let archived = isThreadArchived(raw, status, lifecycleStatus, archivedAt);
  const recentOverride =
    (identity.threadId && options.lastArchivedStatesByThread?.[identity.threadId]) ||
    (identity.agentId && options.lastArchivedStatesByThread?.[identity.agentId]);
  const isLoading = Boolean(
    (identity.threadId && options.threadArchiveLoadingByThread?.[identity.threadId]) ||
    (identity.agentId && options.threadArchiveLoadingByThread?.[identity.agentId])
  );
  if (isLoading && recentOverride) {
    archived = recentOverride.archived;
  } else if (recentOverride && Date.now() - recentOverride.timestamp < 8000) {
    archived = recentOverride.archived;
  }
  return {
    id: identity.threadId,
    agentId: identity.agentId,
    providerThreadId: identity.providerThreadId,
    sessionId: identity.sessionId,
    cwd,
    name: normalizeString(raw?.name || raw?.title || raw?.displayName || raw?.summary) || '新对话',
    provider,
    status,
    agentKey: normalizeString(raw?.agentKey || raw?.agent_key || sourceThread?.agentKey || sourceThread?.agent_key),
    dagKey: normalizeString(raw?.dagKey || raw?.dag_key || sourceThread?.dagKey || sourceThread?.dag_key),
    workflowKey: normalizeString(raw?.workflowKey || raw?.workflow_key || sourceThread?.workflowKey || sourceThread?.workflow_key),
    runKey: normalizeString(raw?.runKey || raw?.run_key || sourceThread?.runKey || sourceThread?.run_key),
    taskId: normalizeString(raw?.taskId || raw?.task_id || sourceThread?.taskId || sourceThread?.task_id),
    source: normalizeString(raw?.source || raw?.origin || sourceThread?.source || sourceThread?.origin),
    lastMessage: normalizeString(raw?.lastMessage || raw?.last_message || raw?.preview),
    updatedAt: normalizeString(raw?.updatedAt || raw?.updated_at || raw?.createdAt || raw?.created_at),
    pinned: Boolean(raw?.pinned || raw?.isPinned || pinnedAt > 0),
    pinnedAt,
    archived,
    archivedAt,
  };
}

function runtimeMapFromPayload(payload = {}) {
  const value = payload.agentRuntimeById || payload.agent_runtime_by_id || {};
  if (!value || typeof value !== 'object' || Array.isArray(value)) return {};
  return value;
}

function runtimeProviderForThread(rawThread, runtimeById = {}) {
  const identity = normalizeThreadIdentity(rawThread);
  const candidates = [
    identity.threadId,
    identity.agentId,
    identity.providerThreadId,
    identity.sessionId,
    rawThread?.id,
    rawThread?.threadId,
    rawThread?.thread_id,
    rawThread?.agentId,
    rawThread?.agent_id,
  ].map(normalizeThreadId).filter(Boolean);
  for (const candidate of candidates) {
    const runtime = runtimeById[candidate];
    if (!runtime || typeof runtime !== 'object' || Array.isArray(runtime)) continue;
    const provider = normalizeString(runtime.provider || runtime.modelProvider || runtime.model_provider);
    if (provider) return provider;
  }
  return '';
}

function runtimeCwdForThread(rawThread, runtimeById = {}) {
  const identity = normalizeThreadIdentity(rawThread);
  const candidates = [
    identity.threadId,
    identity.agentId,
    identity.providerThreadId,
    identity.sessionId,
    rawThread?.id,
    rawThread?.threadId,
    rawThread?.thread_id,
    rawThread?.agentId,
    rawThread?.agent_id,
  ].map(normalizeThreadId).filter(Boolean);
  for (const candidate of candidates) {
    const runtime = runtimeById[candidate];
    if (!runtime || typeof runtime !== 'object' || Array.isArray(runtime)) continue;
    const cwd = normalizePath(runtime.cwd || runtime.CWD || runtime.workdir || runtime.workDir || runtime.work_dir);
    if (cwd) return cwd;
  }
  return '';
}

function snapshotThreadProjectPath(rawThread = {}) {
  const sourceThread = rawThread?.thread && typeof rawThread.thread === 'object' ? rawThread.thread : {};
  const direct = firstThreadCopyText(
    rawThread?.projectPath,
    rawThread?.project_path,
    rawThread?.workspacePath,
    rawThread?.workspace_path,
    rawThread?.rootPath,
    rawThread?.root_path,
    sourceThread?.projectPath,
    sourceThread?.project_path,
    sourceThread?.workspacePath,
    sourceThread?.workspace_path,
    sourceThread?.rootPath,
    sourceThread?.root_path,
  );
  if (direct) return direct;

  for (const key of ['project', 'workspace', 'metadata', 'meta']) {
    const nested = rawThread?.[key];
    if (!nested || typeof nested !== 'object' || Array.isArray(nested)) continue;
    const nestedPath = firstThreadCopyText(nested.path, nested.cwd, nested.root, nested.projectPath, nested.project_path);
    if (nestedPath) return nestedPath;
  }
  return '';
}

function snapshotThreadCwd(rawThread, runtimeById = {}) {
  const sourceThread = rawThread?.thread && typeof rawThread.thread === 'object' ? rawThread.thread : {};
  return normalizePath(
    rawThread?.cwd ||
    rawThread?.CWD ||
    rawThread?.workdir ||
    rawThread?.workDir ||
    rawThread?.work_dir ||
    sourceThread?.cwd ||
    sourceThread?.CWD ||
    sourceThread?.workdir ||
    sourceThread?.workDir ||
    sourceThread?.work_dir ||
    snapshotThreadProjectPath(rawThread) ||
    runtimeCwdForThread(rawThread, runtimeById),
  );
}

function threadMatchesCwdScope(rawThread, scopeCwd, runtimeById = {}) {
  const scope = sidebarProjectKey(scopeCwd);
  if (!scope || scope === '.') return true;
  const threadCwd = sidebarProjectKey(snapshotThreadCwd(rawThread, runtimeById));
  return !threadCwd || threadCwd === scope;
}

function threadMatchesIdentifier(thread, value) {
  const id = normalizeThreadId(value);
  return Boolean(id && (
    normalizeThreadId(thread?.id) === id ||
    normalizeThreadId(thread?.agentId) === id ||
    normalizeThreadId(thread?.providerThreadId) === id ||
    normalizeThreadId(thread?.sessionId) === id
  ));
}

function backendThreadIdFromThreads(value, threads = [], options = {}) {
  const id = normalizeThreadId(value);
  if (!id) return '';
  const direct = normalizeBackendThreadId(id);
  const directMatch = direct ? threads.find((thread) => threadMatchesIdentifier(thread, direct)) : null;
  if (directMatch) {
    return directMatch.archived && !options.includeArchived ? '' : normalizeBackendThreadId(directMatch.id);
  }
  const match = threads.find((thread) => threadMatchesIdentifier(thread, id));
  if (match?.archived && !options.includeArchived) return '';
  return normalizeBackendThreadId(match?.id);
}

function backendThreadIdForState(state, value, options = {}) {
  const id = normalizeThreadId(value);
  if (!id) return '';
  const matchedThread = state.threads.find((thread) => threadMatchesIdentifier(thread, id));
  if (matchedThread) return matchedThread.archived && !options.includeArchived ? '' : normalizeBackendThreadId(matchedThread.id);
  if (isAgentRuntimeId(id)) return '';
  return normalizeBackendThreadId(id);
}

function explicitThreadReplacementIds(thread, requestedId) {
  return [
    requestedId,
    thread?.id,
    thread?.agentId,
    thread?.providerThreadId,
    thread?.sessionId,
  ].map(normalizeThreadId).filter(Boolean);
}

function upsertExplicitThread(threads, thread, requestedId) {
  const ids = explicitThreadReplacementIds(thread, requestedId);
  const existingIndex = threads.findIndex((candidate) => ids.some((id) => threadMatchesIdentifier(candidate, id)));
  if (existingIndex < 0) return [...threads, thread];
  return threads.map((candidate, index) => (
    index === existingIndex ? { ...candidate, ...thread } : candidate
  ));
}

function pickThreadScopedEntry(map = {}, threadId = '') {
  return threadId && hasOwn(map, threadId) ? { [threadId]: map[threadId] } : {};
}

function providerForStateThread(state, value) {
  const id = normalizeThreadId(value);
  const matchedThread = id ? state.threads.find((thread) => threadMatchesIdentifier(thread, id)) : null;
  return normalizeProviderName(matchedThread?.provider || state?.provider);
}

function shouldAutoLoadThreadConfig(state, value) {
  const id = normalizeThreadId(value);
  if (!id) return false;
  if (state.threadConfigByThread?.[id]) return false;
  if (state.threadConfigLoadingByThread?.[id]) return false;
  if (state.threadConfigFailedByThread?.[id]) return false;
  const provider = providerForStateThread(state, id);
  return !(provider === 'codex' && isAgentRuntimeId(id));
}

function threadConfigTargetIdForState(state, value) {
  const id = backendThreadIdForState(state, value);
  if (!id || isAgentRuntimeId(id)) return '';
  return id;
}

function isFailedThreadStatus(value) {
  const status = normalizeString(value).toLowerCase();
  return status === 'failed' || status === 'error' || status.includes('错误') || status.includes('失败');
}

function reusableThreadIdForSend(state, value) {
  const id = normalizeThreadId(value);
  if (!id) return '';
  const matchedThread = state.threads.find((thread) => threadMatchesIdentifier(thread, id));
  if (matchedThread) {
    if (matchedThread.archived || isFailedThreadStatus(matchedThread.status)) return '';
    return normalizeBackendThreadId(matchedThread.id);
  }
  if (isAgentRuntimeId(id)) return '';
  return normalizeBackendThreadId(id);
}

function cwdForExistingThreadSend(state, threadId, fallbackCwd) {
  const id = normalizeThreadId(threadId);
  if (!id) return fallbackCwd;
  const matchedThread = state.threads.find((thread) => (
    threadMatchesIdentifier(thread, id) ||
    threadMatchesIdentifier(thread, state.activeThreadId)
  ));
  const threadCwd = normalizePath(matchedThread?.cwd);
  return threadCwd && threadCwd !== '.' ? threadCwd : fallbackCwd;
}

function activeProviderLockedThreadId(state) {
  const id = normalizeThreadId(state?.activeThreadId);
  if (!id) return '';
  const matchedThread = (state?.threads || []).find((thread) => threadMatchesIdentifier(thread, id));
  return normalizeBackendThreadId(matchedThread?.id || id);
}

function activeTurnIdForThread(state, threadId) {
  const id = normalizeBackendThreadId(threadId);
  if (!id) return '';
  const direct = normalizeTurnSummary(state.activeTurnByThread?.[id]);
  if (isInterruptibleTurnSummary(direct)) return direct.id;
  const activeId = normalizeThreadId(state.activeThreadId);
  if (activeId && activeId !== id) {
    const active = normalizeTurnSummary(state.activeTurnByThread?.[activeId]);
    if (isInterruptibleTurnSummary(active) && threadMatchesIdentifier({ id, thread_id: id }, active.threadId || activeId)) return active.id;
  }
  return '';
}

function statusEntryForInterruptTarget(state, threadId, activeId = '') {
  const candidates = [];
  const pushCandidate = (value) => {
    const id = normalizeThreadId(value);
    if (id && !candidates.includes(id)) candidates.push(id);
  };
  const matchedThread = (state?.threads || []).find((thread) => (
    threadMatchesIdentifier(thread, threadId) || threadMatchesIdentifier(thread, activeId)
  ));
  pushCandidate(threadId);
  pushCandidate(activeId);
  pushCandidate(matchedThread?.id);
  pushCandidate(matchedThread?.agentId);
  pushCandidate(matchedThread?.providerThreadId);
  pushCandidate(matchedThread?.sessionId);
  for (const candidate of candidates) {
    const entry = state?.statuses?.[candidate];
    if (entry && typeof entry === 'object' && !Array.isArray(entry)) {
      return { entry, thread: matchedThread };
    }
  }
  return { entry: null, thread: matchedThread };
}

function threadStatusBlocksInterrupt(state, threadId, activeId = '') {
  const { entry, thread } = statusEntryForInterruptTarget(state, threadId, activeId);
  if (entry?.interruptible === false) return true;
  return isTerminalActiveTurnStatus(entry?.status || thread?.status);
}

function activeThreadInterruptTarget(state) {
  const activeID = normalizeThreadId(state?.activeThreadId);
  const threadID = backendThreadIdForState(state, activeID) || normalizeBackendThreadId(activeID);
  if (!threadID) return { threadId: '', turnId: '', interruptible: false };
  if (threadStatusBlocksInterrupt(state, threadID, activeID)) {
    return { threadId: threadID, turnId: '', interruptible: false };
  }
  const turnID = activeTurnIdForThread(state, threadID);
  return {
    threadId: threadID,
    turnId: turnID,
    interruptible: Boolean(turnID),
  };
}

function backendThreadIdForArchiveState(state, value) {
  const id = normalizeThreadId(value);
  if (!id) return '';
  const matchedThread = state.threads.find((thread) => threadMatchesIdentifier(thread, id));
  if (matchedThread) return normalizeBackendThreadId(matchedThread.id);
  if (isAgentRuntimeId(id)) return '';
  return normalizeBackendThreadId(id);
}

function canonicalizeThreadKey(key, threads = []) {
  return backendThreadIdFromThreads(key, threads, { includeArchived: true }) || normalizeBackendThreadId(key) || normalizeThreadId(key);
}

function canonicalizeActiveTurnByThread(activeTurnByThread = {}, threads = []) {
  const next = {};
  for (const [threadId, turn] of Object.entries(activeTurnByThread || {})) {
    const normalized = normalizeTurnSummary(turn);
    if (!isInterruptibleTurnSummary(normalized)) continue;
    const canonicalThreadId = canonicalizeThreadKey(normalized.threadId || threadId, threads);
    if (canonicalThreadId) next[canonicalThreadId] = { ...normalized, threadId: canonicalThreadId };
  }
  return next;
}

function extractDeltaText(value) {
  if (value === null || value === undefined) return '';
  if (typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean') {
    return value.toString();
  }
  if (Array.isArray(value)) {
    return value.map((item) => extractDeltaText(item)).filter((item) => item !== '').join('\n');
  }
  if (typeof value === 'object') {
    for (const key of ['delta', 'text', 'content', 'message', 'output', 'result', 'answer', 'response']) {
      if (value[key] !== undefined && value[key] !== null) return extractDeltaText(value[key]);
    }
  }
  return '';
}

function snapshotArchiveMap(state, payload) {
  return hasArchiveMapPayload(payload) ? archiveMapFromPayload(payload) : archiveMapFromThreads(state.threads);
}

function snapshotPinMap(state, payload) {
  return hasPinMapPayload(payload) ? pinMapFromPayload(payload) : state.pinnedThreadAtById;
}

function normalizeSnapshotThreadList(payload, state, options, maps) {
  if (!Array.isArray(payload.threads)) return state.threads;
  const threads = payload.threads
    .filter((thread) => threadMatchesCwdScope(thread, maps.scopeCwd, maps.runtimeById))
    .map((thread) => normalizeThread(thread, {
      state,
      archivedAtById: maps.archivedAtById,
      pinnedAtById: maps.pinnedAtById,
      fallbackProvider: snapshotThreadFallbackProvider(thread, state, maps.runtimeById),
      fallbackCwd: snapshotThreadCwd(thread, maps.runtimeById),
      lastArchivedStatesByThread: state.lastArchivedStatesByThread,
      threadArchiveLoadingByThread: state.threadArchiveLoadingByThread,
    }))
    .map((thread) => (
      options.preserveLiveBusyStatus === true
        ? preserveLiveBusyStatusForSnapshotThread(state, thread)
        : thread
    ))
    .filter((thread) => thread.id);
  return threads;
}

function snapshotThreadFallbackProvider(thread, state, runtimeById) {
  const runtimeProvider = knownProviderName(runtimeProviderForThread(thread, runtimeById));
  if (runtimeProvider) return runtimeProvider;
  const existing = state.threads.find((candidate) => threadMatchesIdentifier(candidate, thread?.id));
  const existingProvider = knownProviderName(existing?.provider);
  if (existingProvider) return existingProvider;
  if (threadMatchesIdentifier(thread, state.activeThreadId)) return knownProviderName(state.provider);
  return '';
}

function shouldPreserveSnapshotThread(state, thread, nextThreads) {
  const hasTimeline = (state.timelinesByThread[thread.id] || []).length > 0;
  const alreadyIncluded = nextThreads.some((nextThread) => threadMatchesIdentifier(nextThread, thread.id));
  return !alreadyIncluded && (thread.id === state.activeThreadId || hasTimeline);
}

const LIVE_BUSY_THREAD_STATUS_KEYS = new Set([
  'starting',
  'preparing',
  'thinking',
  'running',
  'editing',
  'waiting',
  'syncing',
  'responding',
  'force_completing',
  'interrupting',
]);

function normalizeLiveThreadStatusKey(value) {
  const raw = normalizeString(value);
  if (raw === '工作中') return 'running';
  if (raw === '发送中') return 'preparing';
  return raw.toLowerCase().replace(/-/g, '_');
}

function isLiveBusyThreadStatus(value) {
  return LIVE_BUSY_THREAD_STATUS_KEYS.has(normalizeLiveThreadStatusKey(value));
}

function snapshotThreadStatusIds(snapshotThread, existingThread) {
  return [
    ...explicitThreadReplacementIds(snapshotThread),
    ...explicitThreadReplacementIds(existingThread),
  ].filter((id, index, ids) => id && ids.indexOf(id) === index);
}

function existingThreadForSnapshotStatus(state, snapshotThread) {
  const ids = snapshotThreadStatusIds(snapshotThread);
  return state.threads.find((thread) => ids.some((id) => threadMatchesIdentifier(thread, id)));
}

function liveStatusEntryForSnapshotThread(state, ids) {
  for (const id of ids) {
    const entry = state.statuses?.[id];
    if (isLiveBusyThreadStatus(entry?.status)) return entry.status;
  }
  return '';
}

function liveActiveTurnForSnapshotThread(state, ids) {
  for (const [threadId, turn] of Object.entries(state.activeTurnByThread || {})) {
    const normalized = normalizeTurnSummary(turn);
    if (!isInterruptibleTurnSummary(normalized)) continue;
    const turnThreadId = normalizeThreadId(normalized.threadId || threadId);
    if (ids.includes(normalizeThreadId(threadId)) || ids.includes(turnThreadId)) return normalized;
  }
  return null;
}

function liveBusyStatusForSnapshotThread(state, snapshotThread) {
  const existingThread = existingThreadForSnapshotStatus(state, snapshotThread);
  const ids = snapshotThreadStatusIds(snapshotThread, existingThread);
  const statusEntry = liveStatusEntryForSnapshotThread(state, ids);
  if (statusEntry) return statusEntry;
  const activeTurn = liveActiveTurnForSnapshotThread(state, ids);
  if (activeTurn && isLiveBusyThreadStatus(activeTurn.status)) return activeTurn.status;
  if (isLiveBusyThreadStatus(existingThread?.status)) return existingThread.status;
  return '';
}

function preserveLiveBusyStatusForSnapshotThread(state, snapshotThread) {
  /*
   * sidebar 快照是列表投影，可能落后于实时 ui/thread/patch。
   * 本地仍在运行时保留 live 状态，避免左侧项目树运行中图标被 stale idle 快照刷掉。
   */
  if (isLiveBusyThreadStatus(snapshotThread?.status)) return snapshotThread;
  const liveBusyStatus = liveBusyStatusForSnapshotThread(state, snapshotThread);
  return liveBusyStatus ? { ...snapshotThread, status: liveBusyStatus } : snapshotThread;
}

function snapshotThreadList(payload, state, options, maps) {
  const nextThreads = [...normalizeSnapshotThreadList(payload, state, options, maps)];
  if (!options.preserveActiveThreadId) return nextThreads;
  for (const thread of state.threads) {
    if (shouldPreserveSnapshotThread(state, thread, nextThreads)) nextThreads.push(thread);
  }
  return nextThreads;
}

function snapshotActiveThreadId(state, payload, nextThreads, options) {
  const preferredActiveThreadId = normalizeThreadId(options.preferredActiveThreadId);
  const autoSelectThread = options.autoSelectThread !== false;
  const activeLookupOptions = options.includeArchivedActiveThread ? { includeArchived: true } : {};

  if (options.preserveActiveThreadId) {
    return (
      backendThreadIdFromThreads(state.activeThreadId, nextThreads, { includeArchived: true }) ||
      (!nextThreads.some((thread) => threadMatchesIdentifier(thread, state.activeThreadId))
        ? normalizeBackendThreadId(state.activeThreadId)
        : '')
    );
  }

  const explicitActiveThreadId = backendThreadIdFromThreads(preferredActiveThreadId, nextThreads, activeLookupOptions);
  if (!autoSelectThread) return explicitActiveThreadId;
  const snapshotActive = normalizeThreadId(payload.activeThreadId || payload.active_thread_id);
  const selectableThreadId = nextThreads.find((thread) => !thread.archived)?.id || '';
  return (
    explicitActiveThreadId ||
    backendThreadIdFromThreads(snapshotActive, nextThreads, activeLookupOptions) ||
    backendThreadIdFromThreads(state.activeThreadId, nextThreads, activeLookupOptions) ||
    selectableThreadId
  );
}

function canonicalizeThreadValues(source = {}, nextThreads = [], normalizer = (value) => value) {
  const output = {};
  for (const [threadId, value] of Object.entries(source || {})) {
    output[canonicalizeThreadKey(threadId, nextThreads)] = normalizer(value);
  }
  return output;
}

function snapshotTimelineBase(state, nextThreads) {
  return {
    timelinesByThread: canonicalizeThreadValues(state.timelinesByThread, nextThreads),
    threadTimelineReadyByThread: canonicalizeThreadValues(
      state.threadTimelineReadyByThread || {},
      nextThreads,
      Boolean,
    ),
    threadMessagePaginationByThread: canonicalizeThreadValues(
      state.threadMessagePaginationByThread || {},
      nextThreads,
    ),
  };
}

function mergeSnapshotTimelineItems(existingTimeline, ready, items = []) {
  const visibleExistingTimeline = existingTimeline.filter(isVisibleTimelineItem);
  const normalizedItems = items.map(normalizeTimelineItem);
  const visibleItems = normalizedItems.filter(isVisibleTimelineItem);
  if (visibleItems.length === 0 && ready) return visibleExistingTimeline;
  return mergeTimelineItems(visibleExistingTimeline, visibleItems, { preserveExistingVisible: true });
}

function snapshotTimelines(state, payload, nextThreads) {
  /*
   * 快照只补充后端看到的消息，不清空前端正在显示的 timeline。
   * thread id 变成真实 id 时，也要保住已加载历史和乐观消息。
   */
  const next = snapshotTimelineBase(state, nextThreads);
  const runtimeResultEntries = [];
  for (const [threadId, items] of Object.entries(objectRecord(payload.timelinesByThread || payload.timelines_by_thread))) {
    if (!Array.isArray(items)) continue;
    const canonicalId = canonicalizeThreadKey(threadId, nextThreads);
    runtimeResultEntries.push(...runtimeResultEntriesFromTimelineItems(items, canonicalId));
    next.timelinesByThread[canonicalId] = mergeSnapshotTimelineItems(
      next.timelinesByThread[canonicalId] || [],
      next.threadTimelineReadyByThread[canonicalId],
      items,
    );
    next.threadTimelineReadyByThread[canonicalId] = true;
  }
  return { ...next, runtimeResultEntries };
}

function snapshotNormalizedThreadMap(stateMap, payloadMap, nextThreads, normalizer) {
  const output = canonicalizeThreadValues(stateMap, nextThreads);
  for (const [threadId, value] of Object.entries(objectRecord(payloadMap))) {
    const normalized = normalizer(value);
    if (normalized) output[canonicalizeThreadKey(threadId, nextThreads)] = normalized;
  }
  return output;
}

function snapshotPayloadThreadMap(payload, camelKey, snakeKey) {
  if (hasOwn(payload, camelKey)) return payload[camelKey];
  if (hasOwn(payload, snakeKey)) return payload[snakeKey];
  return undefined;
}

function snapshotThreadMetrics(state, payload, nextThreads, activeThreadId) {
  const tokenUsagePayloadMap = snapshotPayloadThreadMap(payload, 'tokenUsageByThread', 'token_usage_by_thread');
  const activityStatsPayloadMap = snapshotPayloadThreadMap(payload, 'activityStatsByThread', 'activity_stats_by_thread');
  const tokenUsageByThread = snapshotNormalizedThreadMap(
    state.tokenUsageByThread,
    tokenUsagePayloadMap,
    nextThreads,
    normalizeTokenUsage,
  );
  const activityStatsByThread = snapshotNormalizedThreadMap(
    state.activityStatsByThread,
    activityStatsPayloadMap,
    nextThreads,
    normalizeActivityStats,
  );
  const activeTokenUsage = tokenUsagePayloadMap === undefined ? normalizeTokenUsage(payload.tokenUsage || payload.token_usage) : null;
  const activeActivityStats = activityStatsPayloadMap === undefined ? normalizeActivityStats(payload.activityStats || payload.activity_stats) : null;
  if (activeTokenUsage && activeThreadId) tokenUsageByThread[activeThreadId] = activeTokenUsage;
  if (activeActivityStats && activeThreadId) activityStatsByThread[activeThreadId] = activeActivityStats;
  return { tokenUsageByThread, activityStatsByThread };
}

function snapshotDiffText(state, payload, nextThreads, activeThreadId) {
  const diffTextByThread = canonicalizeThreadValues(state.diffTextByThread, nextThreads);
  const threadDiffReadyByThread = canonicalizeThreadValues(state.threadDiffReadyByThread || {}, nextThreads, Boolean);
  for (const [threadId, text] of Object.entries(objectRecord(payload.diffTextByThread || payload.diff_text_by_thread))) {
    const canonicalId = canonicalizeThreadKey(threadId, nextThreads);
    diffTextByThread[canonicalId] = text;
    threadDiffReadyByThread[canonicalId] = true;
  }
  if (activeThreadId && typeof payload.diffText === 'string') {
    diffTextByThread[activeThreadId] = payload.diffText;
    threadDiffReadyByThread[activeThreadId] = true;
  }
  return { diffTextByThread, threadDiffReadyByThread };
}

function snapshotActiveTurnByThread(state, payload, nextThreads) {
  const activeTurn = activeTurnPayload(payload);
  if (activeTurn === undefined) return canonicalizeActiveTurnByThread(state.activeTurnByThread, nextThreads);
  const normalizedActiveTurn = normalizeTurnSummary(activeTurn);
  if (!isInterruptibleTurnSummary(normalizedActiveTurn) || !normalizedActiveTurn.threadId) return {};
  const canonicalThreadId = canonicalizeThreadKey(normalizedActiveTurn.threadId, nextThreads);
  return { [canonicalThreadId]: { ...normalizedActiveTurn, threadId: canonicalThreadId } };
}

function buildSnapshotState(state, payload = {}, options = {}) {
  /*
   * 线程快照用来刷新列表、状态、指标和 diff。
   * 空 timeline 不代表后端要求清空消息，要继续走合并逻辑。
   */
  const maps = {
    archivedAtById: snapshotArchiveMap(state, payload),
    pinnedAtById: snapshotPinMap(state, payload),
    runtimeById: runtimeMapFromPayload(payload),
    scopeCwd: normalizePath(options.scopeCwd) || composerScopeCwd(state),
  };
  const nextThreads = snapshotThreadList(payload, state, options, maps);
  const activeThreadId = snapshotActiveThreadId(state, payload, nextThreads, options);
  const timelineState = snapshotTimelines(state, payload, nextThreads);
  const metrics = snapshotThreadMetrics(state, payload, nextThreads, activeThreadId);
  const diffState = snapshotDiffText(state, payload, nextThreads, activeThreadId);
  const sidebarThreadsByProject = options.cacheSidebarThreads === false
    ? state.sidebarThreadsByProject
    : sidebarThreadsByProjectWith(state, maps.scopeCwd, nextThreads);
  return {
    activeThreadId,
    threads: nextThreads,
    sidebarThreadsByProject,
    pinnedThreadAtById: maps.pinnedAtById,
    timelinesByThread: timelineState.timelinesByThread,
    threadTimelineReadyByThread: timelineState.threadTimelineReadyByThread,
    threadMessagePaginationByThread: timelineState.threadMessagePaginationByThread,
    runtimeResultEntries: mergeRuntimeResultEntries(state.runtimeResultEntries, timelineState.runtimeResultEntries),
    activeTurnByThread: snapshotActiveTurnByThread(state, payload, nextThreads),
    statuses: { ...state.statuses, ...(payload.statuses || {}) },
    ...metrics,
    ...diffState,
  };
}

function runtimeThreadIdentifier(payload = {}) {
  const patch = objectRecord(payload._threadPatch || payload._thread_patch);
  const thread = objectRecord(payload.thread);
  const patchThread = objectRecord(patch.thread);
  const runtime = objectRecord(payload.agentRuntime || payload.agent_runtime);
  const patchRuntime = objectRecord(patch.agentRuntime || patch.agent_runtime);
  return firstValueFromSources([
    [payload, ROOT_THREAD_IDENTITY_KEYS],
    [thread, THREAD_IDENTITY_KEYS],
    [runtime, ['threadId', 'thread_id']],
    [patch, ROOT_THREAD_IDENTITY_KEYS],
    [patchThread, THREAD_IDENTITY_KEYS],
    [patchRuntime, ['threadId', 'thread_id']],
    [payload, AGENT_IDENTITY_KEYS],
    [thread, AGENT_IDENTITY_KEYS],
    [runtime, AGENT_IDENTITY_KEYS],
    [patch, AGENT_IDENTITY_KEYS],
    [patchThread, AGENT_IDENTITY_KEYS],
    [patchRuntime, AGENT_IDENTITY_KEYS],
  ]);
}

const runtimeResultHelpers = createRuntimeResultHelpers({
  normalizeString,
  normalizeTimestamp,
  normalizeThreadId,
  runtimeThreadIdentifier,
});
const {
  mergeRuntimeResultEntries,
  runtimeResultEntriesFromTimelineItems,
  runtimeResultEntryFromRPCDone,
} = runtimeResultHelpers;

function runtimePayloadCwd(payload = {}) {
  return normalizePath(
    payload.cwd ||
    payload.CWD ||
    payload.agentRuntime?.cwd ||
    payload.agent_runtime?.cwd ||
    payload.thread?.cwd ||
    payload._threadPatch?.cwd ||
    payload._threadPatch?.agentRuntime?.cwd ||
    payload._threadPatch?.agent_runtime?.cwd,
  );
}

function actionNotice(message, tone = 'info') {
  const normalized = normalizeString(message);
  if (!normalized) return null;
  return {
    message: normalized,
    tone,
    timestamp: new Date().toISOString(),
  };
}

function actionNoticeRuntimeFields(fields = {}) {
  const out = {};
  const error = normalizeString(fields.error || fields.message);
  if (error) out.error = error;
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
    createSendDraftRequest,
    createdThreadIdForSendRollback,
    deleteProvisionalThreadAfterSendFailure,
    freshThreadRetryRequest,
    isCodexIdentityAutoResumeError,
    optimisticSendDraftState,
    promotedDraftThreadState,
    resolveLaunchPreferences,
    rollbackSendDraftState,
    saveFailedSendDraftSnapshot,
    sendRollbackRestoresVisibleComposer,
    startNewDraftThread,
    startTurnWithStoppedThreadRecovery,
  },
};

function createLaunchIntentId() {
  const id = globalThis.crypto?.randomUUID?.() || `${Date.now()}-${Math.random().toString(16).slice(2)}`;
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
  const previousActiveThreadId = state.activeThreadId;
  const previousThreadId = reusableThreadIdForSend(state, previousActiveThreadId);
  const launchIntentId = createLaunchIntentId();
  const provisionalThreadId = previousThreadId || launchIntentId;
  const requestCwd = previousThreadId ? cwdForExistingThreadSend(state, previousThreadId, cwd) : cwd;
  return {
    cwd: requestCwd,
    text,
    attachments,
    input,
    previousDraft: state.draft,
    previousAttachments: state.attachments,
    previousActiveThreadId,
    previousThreadId,
    launchIntentId,
    provisionalThreadId,
    optimisticItem: {
      id: `user-${launchIntentId}`,
      role: 'user',
      text,
      attachments,
      time: new Date().toISOString(),
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
  return normalizeString(card?.command_template || card?.commandTemplate);
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

function addForkThreadState(state, threadId, identity, launchPreferences, name, kickoffText) {
  return buildForkThreadState(state, threadId, identity, launchPreferences, name, kickoffText, {
    actionNotice,
    defaultProvider: DEFAULT_PROVIDER,
    emptyForkDraft,
    threadActivityTimestamp,
    threadMatchesIdentifier,
  });
}

const loadForkSharedFiles = createLoadForkSharedFiles({ readSharedFile });

const forkActionDeps = {
  actionNotice,
  addForkThreadState,
  backendThreadIdForState,
  cachedForkSharedFiles,
  createLaunchIntentId,
  emptyForkDraft,
  forkSourceThread,
  forkSourceTitle,
  initialForkSharedFilePaths,
  listSharedFiles: () => listSharedFiles(),
  loadForkSharedFiles,
  mergeForkSharedFilesWithSelected,
  normalizeForkSharedFiles,
  normalizeString,
  normalizeThreadIdentity,
  resolveLaunchPreferences,
  startThread: (payload) => sessionApi.start(payload),
  startTurn: (payload) => sessionApi.startTurn(payload),
};

const runtimeActionDeps = {
  backendThreadIdForState,
  getPreference: (payload) => getPreference(payload),
  getProjects: (payload) => getProjects(payload),
  getSidebarState: (payload) => getSidebarState(payload),
  getThreadState: (payload) => getThreadState(payload),
  getWindowBootstrap: () => getWindowBootstrap(),
  isDagNodeStatusBridgeEvent,
  normalizeBootstrapPage,
  normalizeBootstrapSnapshot,
  normalizePath,
  normalizeThreadId,
  onBridgeEvent: (callback, options) => onBridgeEvent(callback, options),
  onRuntimeReconnect: (callback) => onRuntimeReconnect(callback),
  providerActivePreferenceKey: PROVIDER_ACTIVE_PREF_KEY,
  readConfig: () => readConfig(),
  requireActiveProviderPreference,
  shouldAutoLoadThreadConfig,
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
        ...(state.timelinesByThread[request.provisionalThreadId] || []),
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
  const provisionalTimeline = timelinesByThread[request.provisionalThreadId] || [];
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
  const createdThreadId = normalizeString(options.createdThreadId);
  const localDeleteIds = !request.previousThreadId
    ? [request.provisionalThreadId, createdThreadId].filter(Boolean)
    : [];
  const timelinesByThread = { ...state.timelinesByThread };
  const timelineTargetId = request.previousThreadId || createdThreadId || request.provisionalThreadId;
  const requestTimeline = timelinesByThread[timelineTargetId] || [];
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
    error: error.message,
    actionNotice: actionNotice(`发送失败：${error.message}`, 'error'),
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
  const message = normalizeString(error?.message || error?.cause?.message || String(error || '')).toLowerCase();
  return message.includes('resolve session: thread') && message.includes(' is stopped');
}

function isCodexIdentityAutoResumeError(error) {
  const message = normalizeString(error?.message || error?.cause?.message || String(error || '')).toLowerCase();
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
      actionNotice: actionNotice(`线程配置保存失败：${error.message}`, 'error'),
    });
    addWarning('error', 'thread.config.set.failed', { threadId: target.threadId, error: error.message });
    return false;
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
    return notifyRPCFailure('全局模型配置保存', 'provider.config.save.failed', error, { provider });
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

function resolveInitialLevel() {
  try {
    if (typeof localStorage !== 'undefined') {
      return localStorage.getItem('agent-orchestrator.log.level') || 'info';
    }
  }
  catch (error) {
    void error;
  }
  return 'info';
}

const baseState = {
  bootstrapStatus: 'idle',
  error: '',
  cwd: '',
  projectScopeCwd: '',
  activeProject: '',
  projects: [],
  provider: DEFAULT_PROVIDER,
  providerConfig: normalizeProviderRuntimeConfig({}, DEFAULT_PROVIDER),
  activePage: 'chat',
  promptRevision: 0,
  promptPageCacheByCwd: {},
  workflowRevision: 0,
  workflowPageCacheByCwd: {},
  skillRevision: 0,
  skillPageCacheByCwd: {},
  sharedFilesRevision: 0,
  sharedFilesPageCacheByCwd: {},
  memoryRevision: 0,
  memoryPageCacheByCwd: {},
  chatSurfaceLoadingCwd: '',
  threads: [],
  sidebarThreadsByProject: {},
  pinnedThreadAtById: {},
  activityThreadAtById: {},
  statuses: {},
  activeTurnByThread: {},
  activeThreadId: '',
  pendingActiveThreadId: '',
  threadConfigByThread: {},
  threadConfigLoadingByThread: {},
  threadConfigFailedByThread: {},
  threadStateLoadingByThread: {},
  threadArchiveLoadingByThread: {},
  lastListMutationTime: 0,
  lastArchivedStatesByThread: {},
  threadConfigSaving: false,
  timelinesByThread: {},
  threadTimelineReadyByThread: {},
  threadMessagePaginationByThread: {},
  tokenUsageByThread: {},
  activityStatsByThread: {},
  diffTextByThread: {},
  threadDiffReadyByThread: {},
  activityEntries: [],
  runtimeResultEntries: [],
  warningEntries: [],
  draft: '',
  attachments: [],
  forkDraft: emptyForkDraft(),
  sending: false,
  rightPanelWidth: 380,
  logLevel: resolveInitialLevel(),
  logEntries: [],
  actionNotice: null,
  approvalSubmitByRequestId: {},
  smoothStreaming: false,
};

function stateWithPatch(patch = {}) {
  return {
    ...baseState,
    ...patch,
  };
}

function createClientStoreRuntime(set, get) {
  /*
   * runtime 放前端临时工具：sequence、分页 generation、delta buffer、sidebar cache。
   * 这些不是可持久化状态，destroy/reset 时要一起清掉。
   */
  const runtime = {
    set,
    get,
    bridgeUnsubscribe: null,
    sequencesByThread: new Map(),
    patchGenerationsByThread: new Map(),
    composerDrafts: new Map(),
    sidebarSnapshotsByCwd: new Map(),
    sidebarRefreshesByCwd: new Map(),
    threadMessageGenerations: new Map(),
    threadSyncGenerations: new Map(),
    assistantDeltaBuffers: new Map(),
    assistantDeltaFlushTimer: null,
    sidebarRefreshSeq: 0,
    bootstrapRetryAfterReconnect: false,
  };
  attachComposerDraftRuntime(runtime);
  attachWarningRuntime(runtime, {
    cleanObject,
    emitFrontendTraceEvent,
    normalizeString,
    normalizeThreadId,
    runtimeThreadIdentifier,
  });
  attachLogRuntime(runtime);
  attachScopeRuntime(runtime);
  attachProviderRuntime(runtime);
  attachSidebarRuntime(runtime);
  attachThreadMessagesRuntime(runtime, {
    backendThreadIdForState,
    emitFrontendTraceEvent,
    getThreadMessages,
  });
  attachNotificationRuntime(runtime);
  attachBridgeIdentityRuntime(runtime);
  attachAssistantEventRuntime(runtime);
  attachBridgePatchRuntime(runtime);
  attachBridgeEventRuntime(runtime);
  attachActiveThreadRpcRuntime(runtime, {
    activeThreadInterruptTarget,
    backendThreadIdForState,
    cleanObject,
  });
  return runtime;
}

function attachComposerDraftRuntime(runtime) {
  const { get } = runtime;
  const { composerDrafts } = runtime;

  const saveActiveComposerDraft = (state = get()) => {
    const key = composerDraftKey(state);
    const snapshot = normalizeComposerDraftSnapshot(state);
    if (isEmptyComposerDraftSnapshot(snapshot)) {
      composerDrafts.delete(key);
      return;
    }
    composerDrafts.set(key, snapshot);
  };

  const saveComposerDraftSnapshot = (state = get(), threadId = state.activeThreadId, snapshot = {}) => {
    const key = composerDraftKey(state, threadId);
    const normalized = normalizeComposerDraftSnapshot(snapshot);
    if (isEmptyComposerDraftSnapshot(normalized)) {
      composerDrafts.delete(key);
      return;
    }
    composerDrafts.set(key, normalized);
  };

  const restoreComposerDraft = (state, threadId) => {
    const key = composerDraftKey(state, threadId);
    return normalizeComposerDraftSnapshot(composerDrafts.get(key));
  };

  const clearComposerDraft = (state, threadId) => {
    composerDrafts.delete(composerDraftKey(state, threadId));
  };


  Object.assign(runtime, { saveActiveComposerDraft, saveComposerDraftSnapshot, restoreComposerDraft, clearComposerDraft });
}

function attachLogRuntime(runtime) {
  const { set, addWarning } = runtime;

  const addLog = (level, event, fields = {}) => {
    const parts = (event || '').split('.');
    const scope = parts.length > 1 ? parts[0] : 'terminal';
    const eventName = parts.length > 1 ? parts.slice(1).join('.') : event;

    const entry = {
      id: `${event}-${Date.now()}-${Math.random().toString(16).slice(2)}`,
      ts: new Date().toISOString(),
      level,
      scope,
      event: eventName,
      fields,
    };
    set((state) => ({
      logEntries: [entry, ...state.logEntries].slice(0, 600),
      runtimeResultEntries: mergeRuntimeResultEntries(
        state.runtimeResultEntries,
        [runtimeResultEntryFromRPCDone(event, fields)].filter(Boolean),
      ),
    }));

    if (level === 'warn' || level === 'error') {
      addWarning(level, event, fields);
    }
  };

  const setLogLevel = (level) => {
    try {
      if (typeof localStorage !== 'undefined') {
        localStorage.setItem('agent-orchestrator.log.level', level);
      }
    }
    catch (error) {
      void error;
    }
    set({ logLevel: level });
  };

  Object.assign(runtime, { addLog, setLogLevel });
}

function attachScopeRuntime(runtime) {
  const { set, get, addWarning } = runtime;
  const { sequencesByThread, patchGenerationsByThread, sidebarSnapshotsByCwd, threadMessageGenerations, threadSyncGenerations } = runtime;

  const requireCwd = (reason) => {
    const activeProject = normalizePath(get().activeProject);
    const cwd = activeProject && activeProject !== '.' ? activeProject : normalizePath(get().cwd);
    if (!cwd || cwd === '.') {
      const error = new Error(`frontend-app: cwd is required for ${reason}`);
      addWarning('error', 'missing.cwd', { reason });
      throw error;
    }
    return cwd;
  };

  const requireProjectScopeCwd = (reason) => {
    const cwd = normalizePath(get().projectScopeCwd) || normalizePath(get().cwd);
    if (!cwd || cwd === '.') {
      const error = new Error(`frontend-app: project scope cwd is required for ${reason}`);
      addWarning('error', 'missing.project_scope_cwd', { reason });
      throw error;
    }
    return cwd;
  };

  const currentChatCwd = () => {
    const activeProject = normalizePath(get().activeProject);
    return activeProject && activeProject !== '.' ? activeProject : normalizePath(get().cwd);
  };

  const clearChatSurfaceForCwdSwitch = (cwdValue = '', options = {}) => {
    const cwd = normalizePath(cwdValue);
    const preserveActiveThreadId = options.preserveActiveThreadId === true;
    sequencesByThread.clear();
    patchGenerationsByThread.clear();
    threadMessageGenerations.clear();
    threadSyncGenerations.clear();
    set((state) => {
      const activeThreadId = preserveActiveThreadId ? normalizeBackendThreadId(state.activeThreadId) : '';
      const preservedThreads = activeThreadId
        ? state.threads.filter((thread) => threadMatchesIdentifier(thread, activeThreadId))
        : [];
      return {
        activeThreadId,
        threads: preservedThreads,
        pinnedThreadAtById: {},
        statuses: pickThreadScopedEntry(state.statuses, activeThreadId),
        activeTurnByThread: pickThreadScopedEntry(state.activeTurnByThread, activeThreadId),
        threadConfigByThread: pickThreadScopedEntry(state.threadConfigByThread, activeThreadId),
        threadConfigLoadingByThread: pickThreadScopedEntry(state.threadConfigLoadingByThread, activeThreadId),
        threadConfigFailedByThread: pickThreadScopedEntry(state.threadConfigFailedByThread, activeThreadId),
        threadStateLoadingByThread: activeThreadId ? { [activeThreadId]: true } : {},
        threadArchiveLoadingByThread: pickThreadScopedEntry(state.threadArchiveLoadingByThread, activeThreadId),
        pendingActiveThreadId: activeThreadId ? (state.pendingActiveThreadId || activeThreadId) : '',
        timelinesByThread: pickThreadScopedEntry(state.timelinesByThread, activeThreadId),
        threadTimelineReadyByThread: pickThreadScopedEntry(state.threadTimelineReadyByThread, activeThreadId),
        threadMessagePaginationByThread: pickThreadScopedEntry(state.threadMessagePaginationByThread, activeThreadId),
        tokenUsageByThread: pickThreadScopedEntry(state.tokenUsageByThread, activeThreadId),
        activityStatsByThread: pickThreadScopedEntry(state.activityStatsByThread, activeThreadId),
        diffTextByThread: pickThreadScopedEntry(state.diffTextByThread, activeThreadId),
        threadDiffReadyByThread: pickThreadScopedEntry(state.threadDiffReadyByThread, activeThreadId),
        runtimeResultEntries: [],
        draft: activeThreadId ? state.draft : '',
        attachments: activeThreadId ? state.attachments : [],
        chatSurfaceLoadingCwd: cwd,
      };
    });
  };

  const applyProjects = (payload, fallbackCwd) => {
    const projects = Array.isArray(payload?.projects)
      ? payload.projects.map(normalizePath).filter(Boolean)
      : [];
    const active = normalizePath(payload?.active || payload?.activeProject || fallbackCwd);
    set({
      projects,
      activeProject: active || normalizePath(fallbackCwd),
    });
  };

  const cacheSidebarSnapshot = (cwdValue, snapshot) => {
    const cwd = normalizePath(cwdValue);
    if (!cwd || cwd === '.' || !snapshot || typeof snapshot !== 'object' || Array.isArray(snapshot)) return;
    sidebarSnapshotsByCwd.set(cwd, snapshot);
  };


  Object.assign(runtime, { requireCwd, requireProjectScopeCwd, currentChatCwd, clearChatSurfaceForCwdSwitch, applyProjects, cacheSidebarSnapshot });
}

function attachProviderRuntime(runtime) {
  const { set, get, requireCwd } = runtime;

  const loadProviderConfig = async (cwdValue, providerValue) => {
    const cwd = normalizePath(cwdValue) || requireCwd('provider.config');
    const provider = normalizeActiveProviderName(providerValue || get().provider, 'provider.config') || DEFAULT_PROVIDER;
    const modelKey = providerPreferenceKey(provider, 'model');
    const effortKey = providerPreferenceKey(provider, 'effort');
    const codexModelProviderKey = providerPreferenceKey('codex', 'codexModelProvider');
    const [model, effort, codexModelProvider] = await Promise.all([
      getPreference({ cwd, key: modelKey }),
      getPreference({ cwd, key: effortKey }),
      getPreference({ cwd, key: codexModelProviderKey }),
    ]);
    const providerConfig = normalizeProviderRuntimeConfig({
      model: normalizeProviderConfigValue(model),
      effort: normalizeProviderConfigValue(effort),
      codexModelProvider: normalizeCodexIdentityValue(codexModelProvider),
    }, provider);
    set({ provider, providerConfig });
    return providerConfig;
  };

  const applySnapshot = (payload = {}, options = {}) => {
    set((state) => buildSnapshotState(state, payload, options));
  };


  Object.assign(runtime, { loadProviderConfig, applySnapshot });
}

function attachSidebarRuntime(runtime) {
  const { set, addWarning, currentChatCwd, clearChatSurfaceForCwdSwitch, applySnapshot, cacheSidebarSnapshot } = runtime;
  const { sidebarSnapshotsByCwd, sidebarRefreshesByCwd } = runtime;

  const performSidebarRefreshForCwd = (cwd, options, refreshEntry) => {
    const seq = ++runtime.sidebarRefreshSeq;
    if (options.clearSurface) {
      const cachedSidebar = sidebarSnapshotsByCwd.get(cwd);
      clearChatSurfaceForCwdSwitch(cwd, { preserveActiveThreadId: options.preserveActiveThreadId === true });
      if (cachedSidebar) {
        applySnapshot(cachedSidebar, {
          autoSelectThread: false,
          scopeCwd: cwd,
          preserveActiveThreadId: options.preserveActiveThreadId === true,
          preserveLiveBusyStatus: true,
        });
      }
    }
    return getSidebarState({ cwd })
      .then((sidebar) => {
        if (refreshEntry.cancelled || sidebarRefreshesByCwd.get(cwd) !== refreshEntry) return;
        cacheSidebarSnapshot(cwd, sidebar);
        if (seq !== runtime.sidebarRefreshSeq || normalizePath(currentChatCwd()) !== cwd) return;
        applySnapshot(sidebar, {
          autoSelectThread: false,
          scopeCwd: cwd,
          preserveActiveThreadId: options.preserveActiveThreadId === true,
          preserveLiveBusyStatus: true,
        });
        if (options.clearSurface) {
          set((state) => ({
            chatSurfaceLoadingCwd: state.chatSurfaceLoadingCwd === cwd ? '' : state.chatSurfaceLoadingCwd,
          }));
        }
      })
      .catch((error) => {
        if (refreshEntry.cancelled || sidebarRefreshesByCwd.get(cwd) !== refreshEntry) return;
        if (seq !== runtime.sidebarRefreshSeq || normalizePath(currentChatCwd()) !== cwd) return;
        if (options.clearSurface) {
          set((state) => ({
            chatSurfaceLoadingCwd: state.chatSurfaceLoadingCwd === cwd ? '' : state.chatSurfaceLoadingCwd,
            actionNotice: actionNotice(`刷新会话列表失败：${error.message}`, 'error'),
          }));
        }
        addWarning('error', 'thread.sidebar.refresh.failed', { cwd, error: error.message });
      });
  };

  const runSidebarRefreshEntry = (cwd, refreshEntry, options) => {
    refreshEntry.pending = false;
    refreshEntry.clearSurface = options.clearSurface === true;
    void performSidebarRefreshForCwd(cwd, options, refreshEntry)
      .finally(() => {
        if (refreshEntry.cancelled || sidebarRefreshesByCwd.get(cwd) !== refreshEntry) return;
        if (refreshEntry.pending) {
          runSidebarRefreshEntry(cwd, refreshEntry, { preserveActiveThreadId: true });
          return;
        }
        sidebarRefreshesByCwd.delete(cwd);
      });
  };

  const refreshSidebarSnapshotForCwdInBackground = (cwdValue, options = {}) => {
    const cwd = normalizePath(cwdValue);
    if (!cwd || cwd === '.') {
      throw new Error('frontend-app: cwd is required for project chat refresh');
    }
    const needsClearSurface = options.clearSurface === true;
    const existingRefresh = sidebarRefreshesByCwd.get(cwd);
    if (existingRefresh) {
      if (needsClearSurface && !existingRefresh.clearSurface) {
        existingRefresh.cancelled = true;
        const refreshEntry = { pending: false, cancelled: false, clearSurface: true };
        sidebarRefreshesByCwd.set(cwd, refreshEntry);
        runSidebarRefreshEntry(cwd, refreshEntry, options);
        return;
      }
      existingRefresh.pending = true;
      return;
    }
    const refreshEntry = { pending: false, cancelled: false, clearSurface: needsClearSurface };
    sidebarRefreshesByCwd.set(cwd, refreshEntry);
    runSidebarRefreshEntry(cwd, refreshEntry, options);
  };

  const refreshChatSurfaceForCwdInBackground = (cwdValue, options = {}) => {
    refreshSidebarSnapshotForCwdInBackground(cwdValue, {
      clearSurface: true,
      preserveActiveThreadId: options.preserveActiveThreadId === true,
    });
  };

  const refreshActiveChatSidebarInBackground = () => {
    const cwd = currentChatCwd();
    if (!cwd || cwd === '.') {
      addWarning('warn', 'thread.sidebar.refresh.skipped', { reason: 'missing_cwd' });
      return;
    }
    refreshSidebarSnapshotForCwdInBackground(cwd, { preserveActiveThreadId: true });
  };


  Object.assign(runtime, { refreshSidebarSnapshotForCwdInBackground, refreshChatSurfaceForCwdInBackground, refreshActiveChatSidebarInBackground });
}

function attachNotificationRuntime(runtime) {
  const { set, addWarning } = runtime;

  const notifyAction = (message, tone = 'info', fields = {}) => {
    const baseNotice = actionNotice(message, tone);
    const notice = baseNotice ? { ...baseNotice, ...actionNoticeRuntimeFields(fields) } : null;
    if (!notice) return;
    set((state) => ({
      actionNotice: notice,
      activityEntries: [{
        id: `action-${Date.now()}-${Math.random().toString(16).slice(2)}`,
        method: 'ui/action',
        threadId: normalizeThreadId(fields.threadId),
        message: notice.message,
        timestamp: notice.timestamp,
      }, ...state.activityEntries].slice(0, 120),
    }));
  };

  const notifyRPCFailure = (messagePrefix, warningEvent, error, fields = {}) => {
    const message = error?.message || String(error);
    notifyAction(`${messagePrefix}失败：${message}`, 'error', fields);
    addWarning('error', warningEvent, { ...fields, error: message });
    return false;
  };


  Object.assign(runtime, { notifyAction, notifyRPCFailure });
}

function attachBridgeIdentityRuntime(runtime) {
  const { get, addWarning, currentChatCwd } = runtime;

  const bridgeThreadIdForPayload = (payload) => {
    const identifier = runtimeThreadIdentifier(payload);
    const id = normalizeThreadId(identifier);
    if (!id) return '';
    const payloadCwd = runtimePayloadCwd(payload);
    const activeCwd = currentChatCwd();
    if (payloadCwd && activeCwd && payloadCwd !== activeCwd) {
      addWarning('warn', 'thread.patch.cwd_mismatch', { threadId: id, payloadCwd, activeCwd });
      return '';
    }

    const state = get();
    const matchedThread = state.threads.find((thread) => threadMatchesIdentifier(thread, id));
    if (matchedThread) return matchedThread.archived ? '' : normalizeBackendThreadId(matchedThread.id);

    const fallback = normalizeBackendThreadId(id);
    if (!fallback) return '';
    if (fallback === normalizeBackendThreadId(state.activeThreadId)) return fallback;

    const eventAgentId = normalizeThreadId(
      payload.agentId ||
      payload.agent_id ||
      payload.agentRuntime?.agentId ||
      payload.agent_runtime?.agentId ||
      payload.agent_runtime?.agent_id
    );
    if (eventAgentId && eventAgentId === normalizeThreadId(state.activeThreadId)) {
      return fallback;
    }

    if (payloadCwd && (!activeCwd || payloadCwd === activeCwd)) return fallback;

    if (isAgentRuntimeId(id)) return '';

    addWarning('warn', 'thread.patch.unknown_thread', { threadId: fallback, activeCwd });
    return '';
  };


  Object.assign(runtime, { bridgeThreadIdForPayload });
}

function relatedThreadTimelineKeys(state, threadId) {
  const keys = new Set();
  const addKey = (value) => {
    const key = normalizeThreadId(value);
    if (key) keys.add(key);
  };
  addKey(threadId);
  const matchedThread = (state.threads || []).find((thread) => threadMatchesIdentifier(thread, threadId));
  if (matchedThread) {
    addKey(matchedThread.id);
    addKey(matchedThread.agentId);
    addKey(matchedThread.providerThreadId);
    addKey(matchedThread.sessionId);
  }
  return [...keys];
}

function attachAssistantEventRuntime(runtime) {
  /*
   * assistant、reasoning、命令输出的 delta 先攒一下再写 timeline。
   * 最终完成事件会和流式片段合并，避免同一段回复出现两次。
   */
  const { set, get, bridgeThreadIdForPayload } = runtime;
  const { assistantDeltaBuffers } = runtime;

  const clearAssistantDeltaFlushTimer = () => {
    if (!runtime.assistantDeltaFlushTimer) return;
    clearTimeout(runtime.assistantDeltaFlushTimer);
    runtime.assistantDeltaFlushTimer = null;
  };

  const flushAssistantDeltasNow = () => {
    clearAssistantDeltaFlushTimer();
    if (assistantDeltaBuffers.size === 0) return false;

    const entries = Array.from(assistantDeltaBuffers.values());
    assistantDeltaBuffers.clear();
    const flushTime = new Date().toISOString();
    const flushId = Date.now();

    set((state) => {
      const timelinesByThread = { ...state.timelinesByThread };
      for (const entry of entries) {
        const timeline = timelinesByThread[entry.threadId] || [];
        let found = false;
        const nextTimeline = timeline.map((item) => {
          if (item.id !== entry.itemId) return item;
          found = true;
          return {
            ...item,
            role: 'assistant',
            text: appendAssistantDeltaText(item.text, entry.delta),
            done: false,
          };
        });
        if (!found) {
          if (entry.kind === 'thinking') {
            nextTimeline.push({
              id: entry.itemId,
              role: 'assistant',
              kind: 'thinking',
              text: entry.delta,
              time: entry.timestamp || flushTime,
              done: false,
              turnId: entry.turnId,
            });
          } else {
            nextTimeline.push({
              id: entry.itemId,
              role: 'assistant',
              kind: 'assistant',
              text: entry.delta,
              time: entry.timestamp || flushTime,
              done: false,
              optimistic: false,
              runtime: true,
            });
          }
        }
        timelinesByThread[entry.threadId] = nextTimeline;
      }

      return {
        timelinesByThread,
        activityEntries: [
          ...entries.map((entry, index) => ({
            id: `${entry.method}-${flushId}-${index}`,
            method: entry.method,
            threadId: entry.threadId,
            timestamp: flushTime,
          })),
          ...state.activityEntries,
        ].slice(0, 120),
      };
    });
    return true;
  };

  const scheduleAssistantDeltaFlush = () => {
    if (runtime.assistantDeltaFlushTimer) return;
    runtime.assistantDeltaFlushTimer = setTimeout(() => {
      runtime.assistantDeltaFlushTimer = null;
      flushAssistantDeltasNow();
    }, ASSISTANT_DELTA_FLUSH_MS);
  };

  const enqueueAssistantDelta = (method, payload) => {
    const threadId = bridgeThreadIdForPayload(payload);
    const delta = extractDeltaText(payload.delta ?? payload.text ?? payload.content);
    if (!threadId || delta === '') return false;
    const itemId = normalizeString(payload.itemId || payload.item_id || payload.messageId || payload.message_id) ||
      runtimeAssistantFallbackId(payload, { normalizeThreadId, runtimeThreadIdentifier });
    const key = assistantDeltaBufferKey(threadId, itemId);
    const existing = assistantDeltaBuffers.get(key);
    assistantDeltaBuffers.set(key, {
      threadId,
      itemId,
      method: existing?.method || method,
      delta: appendAssistantDeltaText(existing?.delta, delta),
      timestamp: existing?.timestamp || normalizeString(payload.timestamp) || new Date().toISOString(),
    });
    scheduleAssistantDeltaFlush();
    return true;
  };

  const enqueueReasoningDelta = (method, payload) => {
    const threadId = bridgeThreadIdForPayload(payload);
    const delta = extractDeltaText(payload.delta ?? payload.text ?? payload.content);
    if (!threadId || delta === '') return false;
    const turnId = normalizeString(payload.turnId || payload.turn_id);
    if (!turnId) return false;

    const itemId = `thinking:${turnId}`;
    const key = assistantDeltaBufferKey(threadId, itemId);
    const existing = assistantDeltaBuffers.get(key);
    assistantDeltaBuffers.set(key, {
      threadId,
      itemId,
      method: existing?.method || method,
      delta: appendAssistantDeltaText(existing?.delta, delta),
      timestamp: existing?.timestamp || normalizeString(payload.timestamp) || new Date().toISOString(),
      kind: 'thinking',
      turnId,
    });
    scheduleAssistantDeltaFlush();
    return true;
  };

  const enqueueCommandOutputDelta = (method, payload) => {
    const threadId = bridgeThreadIdForPayload(payload);
    const delta = extractDeltaText(payload.delta ?? payload.text ?? payload.content);
    if (!threadId || delta === '') return false;

    const timeline = get().timelinesByThread[threadId] || [];
    let itemId = '';
    for (let i = timeline.length - 1; i >= 0; i--) {
      if (timeline[i].kind === 'command' && timeline[i].done !== true) {
        itemId = timeline[i].id;
        break;
      }
    }
    if (!itemId) {
      for (let i = timeline.length - 1; i >= 0; i--) {
        if (timeline[i].kind === 'command') {
          itemId = timeline[i].id;
          break;
        }
      }
    }
    if (!itemId) return false;

    const key = assistantDeltaBufferKey(threadId, itemId);
    const existing = assistantDeltaBuffers.get(key);
    assistantDeltaBuffers.set(key, {
      threadId,
      itemId,
      method: existing?.method || method,
      delta: appendAssistantDeltaText(existing?.delta, delta),
      timestamp: existing?.timestamp || normalizeString(payload.timestamp) || new Date().toISOString(),
      kind: 'command',
    });
    scheduleAssistantDeltaFlush();
    return true;
  };

  const finalizeActiveAssistantMessages = (threadId) => {
    if (!threadId) return false;
    set((state) => {
      let mutated = false;
      const timelinesByThread = { ...state.timelinesByThread };
      for (const key of relatedThreadTimelineKeys(state, threadId)) {
        if (!hasOwn(state.timelinesByThread, key)) continue;
        let keyMutated = false;
        const timeline = state.timelinesByThread[key] || [];
        const nextTimeline = timeline.map((item) => {
          if ((item.role === 'assistant' || item.kind === 'assistant' || item.kind === 'thinking' || item.kind === 'command') && item.done === false) {
            keyMutated = true;
            return { ...item, done: true };
          }
          return item;
        });
        if (keyMutated) {
          timelinesByThread[key] = nextTimeline;
          mutated = true;
        }
      }
      if (!mutated) return {};
      return {
        timelinesByThread,
      };
    });
    return true;
  };

  const applyAssistantCompletion = (method, payload) => {
    const threadId = bridgeThreadIdForPayload(payload);
    const completion = runtimeAssistantCompletion(payload);
    if (!threadId || !completion) return false;
    set((state) => {
      const timelinesByThread = { ...state.timelinesByThread };
      const targetKeys = relatedThreadTimelineKeys(state, threadId)
        .filter((key) => key === threadId || hasOwn(state.timelinesByThread, key));
      for (const key of targetKeys) {
        timelinesByThread[key] = mergeRuntimeAssistantCompletion(state.timelinesByThread[key] || [], completion);
      }
      return {
        timelinesByThread,
        actionNotice: actionNotice('已收到回复', 'success'),
        activityEntries: [{
          id: `${method}-${Date.now()}`,
          method,
          threadId,
          timestamp: new Date().toISOString(),
        }, ...state.activityEntries].slice(0, 120),
      };
    });
    return true;
  };


  Object.assign(runtime, {
    enqueueAssistantDelta,
    enqueueReasoningDelta,
    enqueueCommandOutputDelta,
    finalizeActiveAssistantMessages,
    flushAssistantDeltasNow,
    clearAssistantDeltaFlushTimer,
    applyAssistantCompletion,
  });
}

function attachBridgePatchRuntime(runtime) {
  /*
   * ui/thread/patch 是实时线程状态入口。
   * 先确认 thread/cwd 属于当前页面，再按 sequence 跳过旧事件。
   */
  const { set, bridgeThreadIdForPayload } = runtime;
  const { sequencesByThread, patchGenerationsByThread } = runtime;

  const applyBridgePatch = (method, payload) => {
    const threadId = bridgeThreadIdForPayload(payload);
    if (!threadId) return;

    const generation = normalizeString(payload.generation || payload.epoch);
    if (generation) {
      const previousGeneration = patchGenerationsByThread.get(threadId) || '';
      if (previousGeneration && compareSequence(generation, previousGeneration) < 0) {
        return;
      }
      if (!previousGeneration || compareSequence(generation, previousGeneration) > 0) {
        patchGenerationsByThread.set(threadId, generation);
      }
    }

    const sequence = normalizeString(payload.sequence);
    const sequenceKey = generation ? `${threadId}::${generation}` : threadId;
    const previousSequence = sequencesByThread.get(sequenceKey) || '';
    if (sequence) {
      if (previousSequence && compareSequence(sequence, previousSequence) <= 0) {
        return;
      }
      sequencesByThread.set(sequenceKey, sequence);
    }

      const patchStart = Date.now();
      try {
        const patch = {
          ...bridgePatchData(method, payload, threadId, {
            normalizeThread,
            runtimeResultEntriesFromTimelineItems,
          }),
          promoteForActivity: shouldFloatThreadPatch(payload),
        };
        set((state) => bridgePatchState(state, patch, {
          mergeRuntimeResultEntries,
          threadActivityTimestamp,
          threadMatchesIdentifier,
        }));
      }
    finally {
      const durationMs = Date.now() - patchStart;
      if (durationMs >= BRIDGE_PATCH_SLOW_MS) {
        emitFrontendTraceEvent({
          phase: 'frontend.patch.apply.slow',
          method,
          thread_id: threadId,
          agent_id: normalizeString(payload.agentId || payload.agent_id || payload.agentRuntime?.agentId || payload.agent_runtime?.agent_id),
          turn_id: normalizeString(payload.turnId || payload.turn_id || payload.activeTurn?.id || payload.active_turn?.id),
          duration_ms: durationMs,
          status: 'ok',
        });
      }
    }
  };


  Object.assign(runtime, { applyBridgePatch });
}

function attachBridgeEventRuntime(runtime) {
  /*
   * bridge event handler 只负责分流：刷新标记、thread patch、delta、结束事件。
   * 结束事件先刷完 delta，再把未完成的 timeline item 标记完成。
   */
  const {
    set,
    addWarning,
    refreshActiveChatSidebarInBackground,
    applyBridgePatch,
    enqueueAssistantDelta,
    enqueueReasoningDelta,
    enqueueCommandOutputDelta,
    finalizeActiveAssistantMessages,
    flushAssistantDeltasNow,
    applyAssistantCompletion,
    bridgeThreadIdForPayload,
    notifyAction,
  } = runtime;

  const handleFailedBridgeEvent = (eventName, method, payload) => {
    flushAssistantDeltasNow();
    const threadId = bridgeThreadIdForPayload(payload);
    if (threadId) {
      finalizeActiveAssistantMessages(threadId);
    }
    addWarning('error', method, { ...payload, eventName });
    const message = normalizeString(payload?.error || payload?.message || payload?.reason) || 'provider reported failure';
    notifyAction(`运行失败：${message}`, 'error', {
      ...payload,
      threadId,
      error: message,
      recoverable: payload?.recoverable,
    });
  };

  const handleBridgeEvent = (evt) => {
    const method = normalizeString(evt?.method || evt?.type);
    const eventName = method.toLowerCase();
    const payload = evt?.payload || evt?.params || evt?.data || {};
    if (!method) {
      addWarning('error', 'bridge.event.method_missing', {
        eventKeys: evt && typeof evt === 'object' ? Object.keys(evt) : [],
        payloadKeys: payload && typeof payload === 'object' && !Array.isArray(payload) ? Object.keys(payload) : [],
      });
      return;
    }

    const revisionKey = bridgeRevisionKey(eventName, payload);
    if (revisionKey) {
      set((state) => ({ [revisionKey]: state[revisionKey] + 1 }));
      return;
    }
    if (eventName === 'ui/sidebar/changed') {
      refreshActiveChatSidebarInBackground();
      return;
    }
    if (method === 'ui/thread/patch') {
      flushAssistantDeltasNow();
      applyBridgePatch(method, payload);
      return;
    }
    if (isAssistantMessageDeltaEvent(eventName, payload)) {
      enqueueAssistantDelta(method, payload);
      return;
    }
    if (eventName === 'item/reasoning/textdelta' || eventName === 'item/reasoning/text_delta') {
      enqueueReasoningDelta(method, payload);
      return;
    }
    if (eventName === 'item/commandexecution/outputdelta' || eventName === 'item/command_execution/output_delta') {
      enqueueCommandOutputDelta(method, payload);
      return;
    }
    if (eventName === 'item/completed') {
      flushAssistantDeltasNow();
      applyAssistantCompletion(method, payload);
      return;
    }
    if (eventName === 'agent/failed') {
      handleFailedBridgeEvent(eventName, method, payload);
      return;
    }
    if (
      eventName === 'turn/completed' ||
      eventName === 'turn/interrupted' ||
      eventName === 'agent/stopped' ||
      eventName === 'thread/stopped'
    ) {
      flushAssistantDeltasNow();
      const threadId = bridgeThreadIdForPayload(payload);
      if (threadId) {
        finalizeActiveAssistantMessages(threadId);
      }
      if (eventName === 'turn/completed') {
        if (payload._threadPatch) applyBridgePatch('ui/thread/patch', payload._threadPatch);
        applyAssistantCompletion(method, payload);
      }
      return;
    }
    if (eventName === 'thread/tokenusage/updated') {
      const threadId = bridgeThreadIdForPayload(payload);
      const usage = normalizeTokenUsage(payload);
      if (threadId && usage) {
        set((state) => ({
          tokenUsageByThread: {
            ...state.tokenUsageByThread,
            [threadId]: usage,
          },
        }));
      }
      return;
    }
    if (eventName === 'bridge.event.parse_failed') {
      addWarning('error', method, bridgeParseFailureWarningFields(payload));
      return;
    }
    if (eventName === 'rpc.failed' || eventName.endsWith('/failed') || eventName.endsWith('.failed')) {
      addWarning('error', method, payload);
    }
  };

  Object.assign(runtime, { handleBridgeEvent });
}

function bridgeParseFailureWarningFields(payload = {}) {
  const out = {};
  const eventName = normalizeString(payload.eventName || payload.event_name);
  if (eventName) out.eventName = eventName;
  const error = normalizeString(payload.error || payload.message);
  if (error) out.error = error;
  const rawLen = Number(payload.rawLen ?? payload.raw_len);
  if (Number.isFinite(rawLen) && rawLen >= 0) out.rawLen = rawLen;
  return out;
}

function createNavigationActions(runtime) {
  return {
    setActivePage: (activePage) => runtime.set({ activePage }),
    resolveLaunchPreferences: (cwdArg) => {
      const cwd = normalizePath(cwdArg) || runtime.requireCwd('thread.launchPreferences');
      return resolveLaunchPreferences(cwd, runtime.addWarning);
    },

  };
}

function createPromptWorkflowCacheActions(runtime) {
  return {
    setPromptPageCache: (cwd, patch = {}) => {
      const key = normalizeString(cwd);
      if (!key) return;
      runtime.set((state) => ({
        promptPageCacheByCwd: {
          ...state.promptPageCacheByCwd,
          [key]: {
            items: [],
            activePromptId: '',
            fallbackMode: false,
            hasLoadedPrompts: false,
            ...(state.promptPageCacheByCwd?.[key] || {}),
            ...patch,
          },
        },
      }));
    },
    setWorkflowPageCache: (cwd, patch = {}) => {
      const key = normalizeString(cwd);
      if (!key) return;
      runtime.set((state) => ({
        workflowPageCacheByCwd: {
          ...state.workflowPageCacheByCwd,
          [key]: {
            items: [],
            selectedDagKey: '',
            detailsByDagKey: {},
            hasLoadedDags: false,
            ...(state.workflowPageCacheByCwd?.[key] || {}),
            ...patch,
          },
        },
      }));
    },

  };
}

function createResourcePageCacheActions(runtime) {
  return {
    setSkillPageCache: (cwd, patch = {}) => {
      const key = normalizeString(cwd);
      if (!key) return;
      runtime.set((state) => ({
        skillPageCacheByCwd: {
          ...state.skillPageCacheByCwd,
          [key]: {
            items: [],
            resolutionConflicts: [],
            hasLoadedSkills: false,
            ...(state.skillPageCacheByCwd?.[key] || {}),
            ...patch,
          },
        },
      }));
    },
    setSharedFilesPageCache: (cwd, patch = {}) => {
      const key = normalizeString(cwd);
      if (!key) return;
      runtime.set((state) => ({
        sharedFilesPageCacheByCwd: {
          ...state.sharedFilesPageCacheByCwd,
          [key]: {
            files: [],
            finalOutputRefs: [],
            retention: { items: [], protectedCount: 0, cleanupCandidateCount: 0 },
            hasLoadedFiles: false,
            ...(state.sharedFilesPageCacheByCwd?.[key] || {}),
            ...patch,
          },
        },
      }));
    },
    setMemoryPageCache: (cwd, patch = {}) => {
      const key = normalizeString(cwd);
      if (!key) return;
      runtime.set((state) => ({
        memoryPageCacheByCwd: {
          ...state.memoryPageCacheByCwd,
          [key]: {
            snapshot: { overview: {}, entries: [] },
            hasLoadedMemory: false,
            ...(state.memoryPageCacheByCwd?.[key] || {}),
            ...patch,
          },
        },
      }));
    },
    setRightPanelWidth: (rightPanelWidth) => runtime.set({ rightPanelWidth }),


  };
}

function createProviderConfigActions(runtime) {
  return {
    refreshProviderConfig: () => {
      const cwd = runtime.requireCwd('provider.config');
      const provider = normalizeActiveProviderName(runtime.get().provider, 'provider.config') || DEFAULT_PROVIDER;
      return runtime.loadProviderConfig(cwd, provider);
    },

    loadThreadConfig: async (threadId) => {
      const id = threadConfigTargetIdForState(runtime.get(), threadId);
      if (!id) return null;
      runtime.set((state) => ({
        threadConfigLoadingByThread: {
          ...state.threadConfigLoadingByThread,
          [id]: true,
        },
      }));
      try {
        const raw = await getThreadConfig({ threadId: id });
        const config = normalizeThreadConfig(raw, id, runtime.get().provider);
        runtime.set((state) => ({
          threadConfigByThread: {
            ...state.threadConfigByThread,
            [id]: config,
          },
          threadConfigLoadingByThread: {
            ...state.threadConfigLoadingByThread,
            [id]: false,
          },
          threadConfigFailedByThread: {
            ...state.threadConfigFailedByThread,
            [id]: false,
          },
        }));
        return config;
      }
      catch (error) {
        runtime.set((state) => ({
          threadConfigLoadingByThread: {
            ...state.threadConfigLoadingByThread,
            [id]: false,
          },
          threadConfigFailedByThread: {
            ...state.threadConfigFailedByThread,
            [id]: true,
          },
        }));
        runtime.addWarning('error', 'thread.config.get.failed', { threadId: id, error: error.message });
        return null;
      }
    },


  };
}

function createProviderActions(runtime) {
  return {
    toggleProviderMode: async () => {
      const lockedThreadId = activeProviderLockedThreadId(runtime.get());
      if (lockedThreadId) {
        runtime.notifyAction('已开启的聊天不能更改 provider，请新建对话后切换', 'warning', { threadId: lockedThreadId });
        return false;
      }
      runtime.set({
        provider: DEFAULT_PROVIDER,
        actionNotice: actionNotice('当前桌面仅支持 Codex provider', 'warning'),
      });
      runtime.addWarning('warn', 'provider.toggle.unsupported', { requestedProvider: 'claude' });
      return false;
    },


  };
}

function createThreadSelectionActions(runtime) {
  return {
    beginOpeningThread: (thread) => {
      const rawThread = thread && typeof thread === 'object' ? thread : { id: thread };
      const requestedId = normalizeBackendThreadId(
        rawThread.id || rawThread.threadId || rawThread.thread_id || rawThread.agentId || rawThread.agent_id,
      );
      if (!requestedId) return false;
      const current = runtime.get();
      const openingThread = normalizeThread(rawThread, {
        state: current,
        fallbackProvider: current.provider,
      });
      const id = normalizeBackendThreadId(openingThread.id || requestedId);
      if (!id) return false;
      void runtime.saveActiveComposerDraft(current);
      const restored = runtime.restoreComposerDraft(current, id);
      runtime.set((state) => ({
        activeThreadId: id,
        pendingActiveThreadId: id,
        threads: upsertExplicitThread(state.threads, { ...openingThread, id }, requestedId),
        draft: restored.draft,
        attachments: restored.attachments,
        threadStateLoadingByThread: {
          ...state.threadStateLoadingByThread,
          [id]: true,
        },
      }));
      return true;
    },

    openThreadById: async (threadId, options = {}) => {
      const requestedId = normalizeBackendThreadId(threadId);
      if (!requestedId) return false;
      const source = normalizeString(options?.source);
      const cwd = runtime.requireCwd('thread.open');
      let resolved;
      try {
        resolved = await resolveThreadIdentity({ cwd, threadId: requestedId });
      }
      catch (error) {
        return runtime.notifyRPCFailure('打开会话', 'thread.open.resolve.failed', error, { threadId: requestedId, source });
      }
      const resolvedThread = normalizeThread(resolved || {}, {
        state: runtime.get(),
        fallbackProvider: runtime.get().provider,
      });
      if (!resolvedThread.id || !threadMatchesIdentifier(resolvedThread, requestedId)) {
        return runtime.notifyRPCFailure('打开会话', 'thread.open.resolve.invalid', new Error('thread/resolve returned a different or empty thread id'), { threadId: requestedId, source });
      }
      const id = normalizeBackendThreadId(resolvedThread.id);
      const historyFallback = threadOpenHistoryFallbackItems(id, options);
      const current = runtime.get();
      void runtime.saveActiveComposerDraft(current);
      const restored = runtime.restoreComposerDraft(current, id);
      runtime.set((state) => ({
        activeThreadId: id,
        pendingActiveThreadId: '',
        threads: upsertExplicitThread(state.threads, resolvedThread, requestedId),
        draft: restored.draft,
        attachments: restored.attachments,
        threadStateLoadingByThread: {
          ...state.threadStateLoadingByThread,
          [id]: true,
        },
      }));
      try {
        const synced = await runtime.get().syncThreadState(id, {
          includeArchived: true,
          includeDiff: false,
          preserveActiveThreadId: true,
          ...(historyFallback.length > 0 ? { historyFallback } : {}),
        });
        if (!synced) return false;
      }
      catch (error) {
        runtime.set((state) => ({
          threadStateLoadingByThread: {
            ...state.threadStateLoadingByThread,
            [id]: false,
          },
        }));
        return runtime.notifyRPCFailure('打开会话', 'thread.open.failed', error, { threadId: id, source });
      }
      return true;
    },

    setActiveThread: async (threadId) => {
      const id = backendThreadIdForState(runtime.get(), threadId, { includeArchived: true });
      const current = runtime.get();
      const lastListMutationTime = current.lastListMutationTime || 0;
      if (Date.now() - lastListMutationTime < 350) {
        const currentActiveId = backendThreadIdForState(current, current.activeThreadId);
        if (id !== currentActiveId) {
          return false;
        }
      }
      void runtime.saveActiveComposerDraft(current);
      const restored = runtime.restoreComposerDraft(current, id);
      if (!id) {
        runtime.set({
          activeThreadId: '',
          pendingActiveThreadId: '',
          draft: restored.draft,
          attachments: restored.attachments,
        });
        return;
      }
      const currentThreadId = backendThreadIdForState(current, current.activeThreadId);
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
        threadStateLoadingByThread: {
          ...state.threadStateLoadingByThread,
          [id]: true,
        },
      }));
      try {
        const synced = await runtime.get().syncThreadState(id, { includeArchived: true, includeDiff: false });
        if (!synced) return false;
      }
      catch (error) {
        runtime.set((state) => ({
          threadStateLoadingByThread: {
            ...state.threadStateLoadingByThread,
            [id]: false,
          },
        }));
        return runtime.notifyRPCFailure('切换会话', 'thread.select.failed', error, { threadId: id });
      }
      return true;
    },

    newThread: () => {
      const current = runtime.get();
      void runtime.saveActiveComposerDraft(current);
      const restored = runtime.restoreComposerDraft(current, '');
      runtime.set({ activeThreadId: '', draft: restored.draft, attachments: restored.attachments, actionNotice: actionNotice('已创建新对话草稿', 'info') });
    },

    continueWithSharedFile: (path) => {
      const target = normalizeString(path);
      if (!target) return false;
      const current = runtime.get();
      const sourceThreadId = backendThreadIdForState(current, current.activeThreadId);
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
        attachments: state.attachments.some((item) => item.path === target)
          ? state.attachments
          : [attachment],
      }));
      return true;
    },


  };
}

function createDashboardCommandActions(runtime) {
  return {
    runDashboardCommand: async (card) => {
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
          const started = await startNewDraftThread(request, (cwd) => resolveLaunchPreferences(cwd, runtime.addWarning));
          threadId = started.threadId;
          runtime.set((state) => ({
            ...promotedDraftThreadState(state, request, started),
            activePage: 'chat',
          }));
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
        runtime.set((state) => ({
          ...rollbackSendDraftState(state, request, error, { createdThreadId }),
          activePage: 'commands',
        }));
        if (shouldCacheFailedDraft) saveFailedSendDraftSnapshot(runtime, request);
        await deleteProvisionalThreadAfterSendFailure(createdThreadId, runtime.addWarning);
        runtime.addWarning('error', 'dashboard.command.send.failed', { error: error.message });
        throw error;
      }
    },
  };
}

function createActiveThreadActions(runtime) {
  return {
    interruptActiveThread: () => runtime.activeThreadRPC('thread.interrupt', interruptTurn),
    forceCompleteActiveThread: () => runtime.activeThreadRPC('thread.force_complete', forceCompleteTurn),
    compactActiveThread: () => runtime.activeThreadRPC('thread.compact', compactThread),
    recoverActiveThread: () => runtime.activeThreadRPC('thread.recover', recoverThread),

    hasActiveThreadActions: () => Boolean(backendThreadIdForState(runtime.get(), runtime.get().activeThreadId)),
    hasInterruptibleThreadAction: () => {
      return activeThreadInterruptTarget(runtime.get()).interruptible;
    },
    hasForceCompleteThreadAction: () => {
      return activeThreadInterruptTarget(runtime.get()).interruptible;
    },

    refreshActiveThreadStatus: async () => {
      const threadId = backendThreadIdForState(runtime.get(), runtime.get().activeThreadId);
      if (!threadId) return false;
      await runtime.get().syncThreadState(threadId);
      runtime.notifyAction('线程状态已刷新', 'success', { threadId });
      return true;
    },

    respondApproval: async (item, approved) => {
      const requestId = positiveApprovalRequestIdFromFields(item);
      const decision = Boolean(approved);
      if (requestId <= 0) {
        runtime.notifyAction('当前审批缺少请求编号，无法提交', 'error');
        runtime.addWarning('error', 'timeline.approval.request_id_missing', {
          command: normalizeString(item?.command || item?.title),
        });
        return false;
      }
      const existingSubmit = runtime.get().approvalSubmitByRequestId?.[requestId];
      if (existingSubmit?.inFlight) {
        runtime.notifyAction('审批结果正在提交，请等待当前请求完成', 'warning', { requestId });
        runtime.addWarning('warn', 'timeline.approval.respond_duplicate', { requestId, approved: decision });
        return false;
      }
      runtime.set((state) => ({
        approvalSubmitByRequestId: {
          ...state.approvalSubmitByRequestId,
          [requestId]: {
            approved: decision,
            inFlight: true,
            startedAt: Date.now(),
          },
        },
      }));
      try {
        const result = await respondApprovalRPC({ requestId, approved: decision });
        if (result?.ok === false) {
          runtime.notifyAction('审批请求已不再等待处理', 'warning', { requestId });
          runtime.addWarning('warn', 'timeline.approval.respond_not_pending', { requestId, approved: decision });
          return false;
        }
        runtime.notifyAction('审批结果已提交', 'success', { requestId });
        return true;
      }
      catch (error) {
        const message = error?.message || String(error);
        runtime.notifyAction(`审批提交失败：${message}`, 'error', { requestId });
        runtime.addWarning('error', 'timeline.approval.respond.failed', { requestId, approved: decision, error: message });
        return false;
      }
      finally {
        runtime.set((state) => {
          const current = state.approvalSubmitByRequestId || {};
          if (!current[requestId]) return {};
          const next = { ...current };
          delete next[requestId];
          return { approvalSubmitByRequestId: next };
        });
      }
    },


  };
}

function createThreadCopyActions(runtime) {
  return {
    copyActiveThreadInfo: async () => {
      const state = runtime.get();
      const threadId = backendThreadIdForState(state, state.activeThreadId);
      if (!threadId) {
        runtime.notifyAction('当前没有可复制的后端线程', 'warning');
        return false;
      }
      const preparedClipboardWrite = beginTextClipboardWrite();
      const thread = state.threads.find((item) => item.id === threadId) || {};
      const cwd = runtime.requireCwd('thread.copy');
      let identity;
      try {
        identity = await resolveThreadIdentity({ cwd, threadId });
      }
      catch (error) {
        preparedClipboardWrite?.cancel?.(error);
        runtime.notifyAction(`复制失败：线程信息接口调用失败：${error.message || String(error)}`, 'warning', { threadId });
        runtime.addWarning('warn', 'thread.identity.resolve.failed', { threadId, error: error.message || String(error) });
        return false;
      }
      if (!identity || typeof identity !== 'object' || Array.isArray(identity)) {
        preparedClipboardWrite?.cancel?.();
        runtime.notifyAction('复制失败：线程信息接口返回值不是 JSON 对象', 'warning', { threadId });
        runtime.addWarning('warn', 'thread.identity.resolve.invalid', { threadId });
        return false;
      }
      const threadConfig = state.threadConfigByThread[threadId] || await runtime.get().loadThreadConfig(threadId);
      const payload = buildThreadCopyPayload({ state: runtime.get(), threadId, thread, identity, threadConfig, defaultProvider: DEFAULT_PROVIDER });
      try {
        const text = JSON.stringify(payload, null, 2);
        const copyFailures = [];
        if (preparedClipboardWrite?.commit) {
          try {
            await preparedClipboardWrite.commit(text);
            runtime.notifyAction('线程信息已复制', 'success', { threadId });
            return true;
          }
          catch (error) {
            copyFailures.push(`prepared clipboard write failed: ${error.message || String(error)}`);
            runtime.addWarning('warn', 'thread.copy.prepared_clipboard.failed', { threadId, error: error.message || String(error) });
          }
        }
        try {
          await copyTextToClipboard(text);
        }
        catch (error) {
          if (copyFailures.length > 0) {
            throw new Error(`${copyFailures.join('; ')}; fallback copy failed: ${error.message || String(error)}`, { cause: error });
          }
          throw error;
        }
        runtime.notifyAction('线程信息已复制', 'success', { threadId });
        return true;
      }
      catch (error) {
        runtime.notifyAction(`复制失败：${error.message || String(error)}`, 'warning', { threadId });
        runtime.addWarning('warn', 'thread.copy.clipboard.failed', { threadId, error: error.message || String(error) });
        return false;
      }
    },


  };
}

function createThreadRenamePinActions(runtime) {
  return {
    renameThread: async (threadId, name) => {
      const id = backendThreadIdForState(runtime.get(), threadId);
      const nextName = normalizeString(name);
      if (!id || !nextName) return false;
      try {
        await renameThreadRPC({ threadId: id, name: nextName });
        runtime.set((state) => ({
          threads: applyThreadRename(state.threads, id, nextName),
          sidebarThreadsByProject: mapSidebarThreadCache(state, (threads) => applyThreadRename(threads, id, nextName)),
          actionNotice: actionNotice('线程已重命名', 'success'),
        }));
        return true;
      }
      catch (error) {
        return runtime.notifyRPCFailure('重命名会话', 'thread.rename.failed', error, { threadId: id });
      }
    },

    toggleThreadPin: async (threadId) => {
      const id = backendThreadIdForArchiveState(runtime.get(), threadId);
      if (!id) return false;
      const cwd = runtime.requireCwd('thread.pin');
      const currentMap = normalizeTimestampMap(runtime.get().pinnedThreadAtById);
      const pinned = currentMap[id] > 0;
      const nextMap = { ...currentMap };
      if (pinned) {
        delete nextMap[id];
      } else {
        nextMap[id] = Date.now();
      }
      try {
        await setPreference({
          cwd,
          key: THREAD_PINS_CHAT_PREF_KEY,
          value: nextMap,
        });
        runtime.set((state) => ({
          pinnedThreadAtById: nextMap,
          threads: state.threads.map((thread) => (thread.id === id ? {
            ...thread,
            pinned: !pinned,
            pinnedAt: nextMap[id] || 0,
          } : thread)),
          sidebarThreadsByProject: mapSidebarThreadCache(state, (threads) => threads.map((thread) => (thread.id === id ? {
            ...thread,
            pinned: !pinned,
            pinnedAt: nextMap[id] || 0,
          } : thread))),
          actionNotice: actionNotice(pinned ? '会话已取消置顶' : '会话已置顶', 'success'),
        }));
        return true;
      }
      catch (error) {
        return runtime.notifyRPCFailure(pinned ? '取消置顶会话' : '置顶会话', 'thread.pin.failed', error, { threadId: id });
      }
    },


  };
}

function createThreadArchiveActions(runtime) {
  return {
    archiveThread: async (threadId, archived) => {
      const id = backendThreadIdForArchiveState(runtime.get(), threadId);
      if (!id) return false;
      const cwd = runtime.requireCwd('thread.archive');
      if (runtime.get().threadArchiveLoadingByThread?.[id]) return false;

      const originalThreads = runtime.get().threads;
      const originalActiveThreadId = runtime.get().activeThreadId;
      const archivedAt = archived ? Date.now() : 0;

      // 1. Optimistic Update: Immediately apply the archived state to the UI
      runtime.set((state) => archiveThreadOptimisticState(state, {
        id,
        archived,
        archivedAt,
        timestamp: Date.now(),
      }));

      // 2. Perform the main backend archive operation
      try {
        if (archived) {
          await archiveThreadRPC({ threadId: id });
        } else {
          await unarchiveThreadRPC({ threadId: id });
        }
      }
      catch (error) {
        // Rollback on main RPC failure
        const message = error?.message || String(error);
        const action = archived ? '归档' : '恢复';
        
        runtime.set((state) => archiveThreadFailureState(state, {
          id,
          originalThreads,
          originalActiveThreadId,
          actionNotice: actionNotice(`${action}会话失败：${message}`, 'error'),
        }));
        
        runtime.addWarning('error', `thread.${archived ? 'archive' : 'unarchive'}.failed`, { threadId: id, error: message });
        return false;
      }

      // 3. Clear loading state since the database update succeeded
      runtime.set((state) => ({
        threadArchiveLoadingByThread: {
          ...state.threadArchiveLoadingByThread,
          [id]: false,
        },
      }));

      // 4. Perform the secondary preference storage
      try {
        await setPreference({
          cwd,
          key: `archivedThreadAtById.${id}`,
          value: archivedAt > 0 ? archivedAt : null,
        });
      }
      catch (error) {
        // Keep the archived state (no rollback), but notify about preference failure
        const message = error?.message || String(error);
        const action = archived ? '归档' : '恢复';
        
        runtime.set(() => ({
          actionNotice: actionNotice(`${action}偏好保存失败：${message}`, 'error'),
        }));
        
        runtime.addWarning('error', `thread.${archived ? 'archive' : 'unarchive'}.preference.failed`, { threadId: id, error: message });
        return true;
      }

      // 5. Success: Show success notice
      runtime.set(() => ({
        actionNotice: actionNotice(archived ? '线程已归档' : '线程已恢复到列表', 'success'),
      }));
      return true;
    },


  };
}

function createThreadDeleteActions(runtime) {
  return {
    deleteStaleThreads: async (threadIds) => {
      const ids = [...new Set((Array.isArray(threadIds) ? threadIds : [])
        .map((threadId) => backendThreadIdForArchiveState(runtime.get(), threadId))
        .filter(Boolean))];
      if (ids.length === 0) return { deleted: 0, failed: 0 };
      const cwd = runtime.requireCwd('thread.delete');
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
      if (deletedIds.length > 0) {
        await Promise.all(deletedIds.map((id) => setPreference({
          cwd,
          key: `archivedThreadAtById.${id}`,
          value: null,
        })));
        const deletedSet = new Set(deletedIds);
        runtime.set((state) => ({
          activeThreadId: deletedSet.has(state.activeThreadId) ? '' : state.activeThreadId,
          threads: state.threads.filter((thread) => !deletedSet.has(thread.id)),
          sidebarThreadsByProject: mapSidebarThreadCache(state, (threads) => threads.filter((thread) => !deletedSet.has(thread.id))),
          actionNotice: actionNotice(
            failedIds.length > 0
              ? `已删除 ${deletedIds.length} 个无用会话，${failedIds.length} 个失败`
              : `已删除 ${deletedIds.length} 个无用会话`,
            failedIds.length > 0 ? 'warning' : 'success',
          ),
          lastListMutationTime: Date.now(),
        }));
      } else {
        runtime.set({
          actionNotice: actionNotice(`删除无用会话失败：${failedIds.length} 个失败`, 'error'),
        });
      }
      return { deleted: deletedIds.length, failed: failedIds.length };
    },


  };
}

function createClientStore(set, get) {
  /*
   * 公开 store 由多个 slice 拼起来。
   * 页面可见状态放 baseState 和 slice；临时 helper 才放 runtime。
   */
  const runtime = createClientStoreRuntime(set, get);
  const composerDeps = {
    ...composerActionDeps,
    send: {
      ...composerActionDeps.send,
      resolveLaunchPreferences: (cwd) => resolveLaunchPreferences(cwd, runtime.addWarning),
    },
  };
  return {
    ...baseState,
    ...createRuntimeSlice(runtime, runtimeActionDeps),
    ...createNavigationActions(runtime),
    ...createPromptWorkflowCacheActions(runtime),
    ...createResourcePageCacheActions(runtime),
    ...createProviderConfigActions(runtime),
    ...createProjectSlice(runtime, projectActionDeps),
    ...createProviderActions(runtime),
    ...createThreadSelectionActions(runtime),
    ...createForkSlice(runtime, forkActionDeps),
    ...createComposerSlice(runtime, composerDeps),
    ...createDashboardCommandActions(runtime),
    ...createActiveThreadActions(runtime),
    ...createThreadCopyActions(runtime),
    ...createThreadRenamePinActions(runtime),
    ...createThreadArchiveActions(runtime),
    ...createThreadDeleteActions(runtime),
    addWarning: runtime.addWarning,
    addLog: runtime.addLog,
    setLogLevel: runtime.setLogLevel,
    toggleSmoothStreaming: () => {
      runtime.set((state) => ({ smoothStreaming: !state.smoothStreaming }));
    },
  };
}

export const useClientStore = create(createClientStore);

export function resetClientStoreForTests(patch = {}) {
  useClientStore.getState().destroy();
  useClientStore.setState(stateWithPatch(patch));
}

registerBridgeLogStore({
  info: (event, fields) => useClientStore.getState().addLog('info', event, fields),
  debug: (event, fields) => useClientStore.getState().addLog('debug', event, fields),
  warn: (event, fields) => useClientStore.getState().addLog('warn', event, fields),
  error: (event, fields) => useClientStore.getState().addLog('error', event, fields),
});
