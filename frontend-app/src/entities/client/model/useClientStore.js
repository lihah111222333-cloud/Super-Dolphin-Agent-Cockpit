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
  startThread,
  startTurn,
  removeProject as removeProjectRPC,
  unarchiveThread as unarchiveThreadRPC,
} from '../../../shared/api/backendApi.js';
import {
  createComposerSlice,
} from './composerSlice.js';
import { createForkSlice } from './forkSlice.js';
import { createProjectSlice } from './projectSlice.js';
import { createRuntimeSlice } from './runtimeSlice.js';

const DEFAULT_PROVIDER = 'codex';
const MAX_WARNING_ENTRIES = 300;
const MAX_RUNTIME_RESULT_ENTRIES = 120;
const RUNTIME_RESULT_DETAIL_LIMIT = 1600;
const RUNTIME_ASSISTANT_PREFIX_DUPLICATE_MIN_CHARS = 24;
const ASSISTANT_DELTA_FLUSH_MS = 50;
const BRIDGE_PATCH_SLOW_MS = 50;
const THREAD_MESSAGES_PAGE_SIZE = 300;
const PROVIDER_ACTIVE_PREF_KEY = 'settings.provider.active';
const ACTIVE_PROMPT_PREF_KEY = 'settings.activePromptKey';
const THREAD_PINS_CHAT_PREF_KEY = 'threadPins.chat';
const objectPrototype = Object.prototype;
const PROVIDER_DISPLAY_DEFAULT_CONFIGS = Object.freeze({
  codex: Object.freeze({ model: 'gpt-5.5', effort: 'xhigh' }),
  claude: Object.freeze({ model: 'sonnet', effort: 'high' }),
});
const BOOTSTRAP_PAGE_ALIASES = Object.freeze({
  dags: 'workflows',
  'memory-center': 'memory',
  memory: 'files',
});
const APP_PAGE_IDS = new Set(['chat', 'prompts', 'workflows', 'skills', 'memory', 'observability', 'files', 'settings']);
const IMAGE_ATTACHMENT_RE = /\.(png|jpe?g|gif|webp|bmp|svg)$/i;
const IMAGE_PLACEHOLDER_RE = /<image\s[^>]*><\/image>/gi;
const ACTIVITY_COUNT_FIELDS = Object.freeze({
  lspCalls: Object.freeze(['lspCalls', 'lsp_calls']),
  commands: Object.freeze(['commands']),
  fileEdits: Object.freeze(['fileEdits', 'file_edits']),
});
const RUNTIME_ASSISTANT_LOOSE_DUPLICATE_MIN_CHARS = 80;
const RUNTIME_ASSISTANT_LOOSE_DUPLICATE_SHINGLE_SIZE = 12;
const RUNTIME_ASSISTANT_LOOSE_DUPLICATE_MIN_MATCHES = 4;
const RUNTIME_ASSISTANT_LOOSE_DUPLICATE_MIN_RATIO = 0.65;
const TIMELINE_KIND_KEYS = Object.freeze(['kind', 'type', 'eventType', 'event_type', 'role']);
const TIMELINE_ROLE_KEYS = Object.freeze(['role', 'kind', 'type', 'eventType', 'event_type']);
const TIMELINE_TEXT_KEYS = Object.freeze(['text', 'content', 'message', 'delta', 'output', 'result', 'answer', 'response', 'summary', 'preview', 'error']);
const TIMELINE_ID_KEYS = Object.freeze(['id', 'messageId', 'message_id']);
const TIMELINE_TITLE_KEYS = Object.freeze(['title', 'label', 'name', 'tool', 'toolName', 'command']);
const TIMELINE_TIME_KEYS = Object.freeze(['time', 'startedAt', 'started_at', 'ts', 'createdAt', 'created_at']);
const TIMELINE_COMPLETED_KEYS = Object.freeze(['completedAt', 'completed_at', 'finishedAt', 'finished_at']);
const TIMELINE_CALL_ID_KEYS = Object.freeze(['callId', 'call_id', 'toolCallId', 'tool_call_id']);
const ROOT_THREAD_IDENTITY_KEYS = Object.freeze(['threadId', 'thread_id', 'codexThreadId', 'codex_thread_id']);
const THREAD_IDENTITY_KEYS = Object.freeze(['threadId', 'thread_id', 'codexThreadId', 'codex_thread_id', 'id']);
const AGENT_IDENTITY_KEYS = Object.freeze(['agentId', 'agent_id']);
const TIMELINE_LIFECYCLE_KINDS = new Set(['tool', 'command', 'process']);
const RUNTIME_TOOL_FAILED_STATUSES = new Set(['failed', 'error']);
const RUNTIME_TOOL_TERMINAL_STATUSES = new Set(['completed', 'complete', 'done', 'ok', 'success', 'succeeded', 'failed', 'error']);
const TIMELINE_TERMINAL_STATUSES = new Set([...RUNTIME_TOOL_TERMINAL_STATUSES, 'skipped', 'cancelled', 'canceled', 'aborted']);
const PROMPT_REVISION_EVENTS = new Set(['prompts/changed', 'prompt-assets/changed', 'ui/prompts/changed']);
const BRIDGE_REVISION_EVENTS = Object.freeze([
  Object.freeze({ key: 'skillRevision', events: new Set(['skills/changed']) }),
  Object.freeze({ key: 'sharedFilesRevision', events: new Set(['ui/shared-files/changed', 'shared-files/changed', 'shared_file/changed']) }),
  Object.freeze({ key: 'memoryRevision', events: new Set(['ui/memory/changed', 'memory/changed']) }),
  Object.freeze({ key: 'workflowRevision', events: new Set(['task/node/statuschanged', 'cron/job/runstatechanged', 'task/dag/changed', 'dags/changed']) }),
]);

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

function positiveNumberFromFields(source, keys = []) {
  const numeric = Number(firstFieldValue(source, keys));
  return Math.max(0, Number.isFinite(numeric) ? numeric : 0);
}

function requiredDagStatusPayloadString(payload, field, message) {
  if (!Object.prototype.hasOwnProperty.call(payload, field)) throw new Error(message);
  const value = normalizeString(payload[field]);
  if (!value) throw new Error(message);
  return value;
}

function requireDagNodeStatusPayload(payload) {
  if (!payload || typeof payload !== 'object' || Array.isArray(payload)) throw new Error('dag status event payload is required');
  requiredDagStatusPayloadString(payload, 'dag_key', 'dag status event dag key is required');
  requiredDagStatusPayloadString(payload, 'node_key', 'dag status event node key is required');
  requiredDagStatusPayloadString(payload, 'new_status', 'dag status event status is required');
  const runKey = Object.prototype.hasOwnProperty.call(payload, 'run_key') ? normalizeString(payload.run_key) : '';
  const runID = Object.prototype.hasOwnProperty.call(payload, 'run_id') ? Number(payload.run_id) : 0;
  if (!runKey && (!Number.isFinite(runID) || runID <= 0)) throw new Error('dag status event run identity is required');
}

function bridgeRevisionKey(eventName, payload = {}) {
  if (
    PROMPT_REVISION_EVENTS.has(eventName) ||
    (eventName === 'ui/preferences/changed' && normalizeString(payload.key) === ACTIVE_PROMPT_PREF_KEY)
  ) {
    return 'promptRevision';
  }
  if (eventName === 'task/node/statuschanged') requireDagNodeStatusPayload(payload);
  const match = BRIDGE_REVISION_EVENTS.find((entry) => entry.events.has(eventName));
  return match?.key || '';
}

function isDagNodeStatusBridgeEvent(evt) {
  return normalizeString(evt?.method || evt?.type).toLowerCase() === 'task/node/statuschanged';
}

function normalizeProviderConfigValue(value) {
  if (value && typeof value === 'object' && !Array.isArray(value)) {
    for (const key of ['value', 'id', 'key', 'name', 'model', 'provider']) {
      const normalized = normalizeString(value[key]);
      if (normalized) return normalized;
    }
    return '';
  }
  return normalizeString(value);
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

function normalizeTimestamp(value) {
  if (typeof value === 'boolean' || value === null || value === undefined) return 0;
  if (typeof value === 'number') return Number.isFinite(value) && value > 0 ? value : 0;
  const text = normalizeString(value);
  if (!text) return 0;
  const asNumber = Number(text);
  if (Number.isFinite(asNumber) && asNumber > 0) return asNumber;
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
  const provider = normalizeProviderConfigValue(value).toLowerCase();
  if (!provider) return '';
  if (provider === 'codex' || provider === 'claude') return 'codex';
  throw new Error(`invalid provider preference: ${normalizeProviderConfigValue(value)}`);
}

function knownProviderName(value) {
  try {
    return normalizeProviderName(value);
  } catch {
    return '';
  }
}

function requireActiveProviderPreference(value, reason) {
  const provider = normalizeProviderName(value);
  if (!provider) {
    throw new Error(`${reason}: settings.provider.active preference is required`);
  }
  return provider;
}

function providerPreferenceScope(provider) {
  return provider === 'codex' ? 'codex' : 'claude';
}

function providerPreferenceKey(provider, suffix) {
  return `settings.provider.${provider}.${suffix}`;
}

function normalizeCodexIdentityValue(value) {
  if (typeof value === 'boolean') return '';
  return normalizeProviderConfigValue(value);
}

function providerDisplayDefaultConfig(provider) {
  return PROVIDER_DISPLAY_DEFAULT_CONFIGS[provider] || PROVIDER_DISPLAY_DEFAULT_CONFIGS[DEFAULT_PROVIDER];
}

function normalizeProviderRuntimeConfig(raw = {}, providerValue = DEFAULT_PROVIDER) {
  const provider = normalizeProviderName(providerValue) || DEFAULT_PROVIDER;
  return {
    provider,
    model: normalizeProviderConfigValue(raw.model),
    effort: normalizeProviderConfigValue(raw.effort),
    codexModelProvider: normalizeCodexIdentityValue(raw.codexModelProvider),
  };
}

function requireProviderPreferenceValue(value, key, reason) {
  const normalized = normalizeProviderConfigValue(value);
  if (!normalized) {
    throw new Error(`${reason}: ${key} preference is required`);
  }
  return normalized;
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

async function resolveLaunchPreferences(cwd) {
  const activeProviderValue = await getPreference({ cwd, key: PROVIDER_ACTIVE_PREF_KEY });
  const provider = normalizeProviderName(activeProviderValue);
  if (!provider) {
    throw new Error('startThread: settings.provider.active preference is empty — cannot determine provider. Please select a provider in Settings.');
  }

  const providerScope = providerPreferenceScope(provider);
  const [
    model,
    effort,
    activePromptKey,
    codexHome,
    codexInstanceKey,
    codexModelProvider,
  ] = await Promise.all([
    getPreference({ cwd, key: providerPreferenceKey(providerScope, 'model') }),
    getPreference({ cwd, key: providerPreferenceKey(providerScope, 'effort') }),
    getPreference({ cwd, key: ACTIVE_PROMPT_PREF_KEY }),
    providerScope === 'codex' ? getPreference({ cwd, key: providerPreferenceKey('codex', 'codexHome') }) : Promise.resolve(null),
    providerScope === 'codex' ? getPreference({ cwd, key: providerPreferenceKey('codex', 'codexInstanceKey') }) : Promise.resolve(null),
    providerScope === 'codex' ? getPreference({ cwd, key: providerPreferenceKey('codex', 'codexModelProvider') }) : Promise.resolve(null),
  ]);
  const launch = cleanObject({
    modelProvider: provider,
    model: normalizeProviderConfigValue(model),
    effort: normalizeProviderConfigValue(effort),
    prompt_key: normalizeProviderConfigValue(activePromptKey),
  });
  if (providerScope === 'codex') {
    launch.config = cleanObject({
      codexHome: normalizeProviderConfigValue(codexHome),
      codexInstanceKey: normalizeProviderConfigValue(codexInstanceKey),
      codexModelProvider: normalizeCodexIdentityValue(codexModelProvider),
    });
  }
  return launch;
}

function basename(path) {
  const value = normalizeString(path);
  return value.split(/[\\/]/).filter(Boolean).pop() || value;
}

function isImagePath(path) {
  return IMAGE_ATTACHMENT_RE.test(normalizeString(path));
}

function normalizeFileAttachment(path) {
  const value = normalizeString(path);
  if (!value) return null;
  const name = basename(value);
  const image = isImagePath(name);
  return {
    path: value,
    name,
    kind: image ? 'image' : 'file',
    previewUrl: image ? `file://${value}` : '',
  };
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

function normalizeThreadId(value) {
  return normalizeString(value);
}

function hasOwn(value, key) {
  return Boolean(value && typeof value === 'object' && objectPrototype.hasOwnProperty.call(value, key));
}

function isAgentRuntimeId(value) {
  return /^agent[_-]/i.test(normalizeThreadId(value));
}

function isLaunchIntentId(value) {
  return /^launch[_-]/i.test(normalizeThreadId(value));
}

function normalizeBackendThreadId(value) {
  const id = normalizeThreadId(value);
  if (!id || isLaunchIntentId(id)) return '';
  return id;
}

function firstBackendThreadId(...values) {
  for (const value of values) {
    const id = normalizeBackendThreadId(value);
    if (id) return id;
  }
  return '';
}

function firstRuntimeAgentId(...values) {
  for (const value of values) {
    const id = normalizeThreadId(value);
    if (id && isAgentRuntimeId(id)) return id;
  }
  return '';
}

function normalizeThreadIdentity(raw) {
  const thread = raw?.thread || {};
  const id = firstBackendThreadId(
    raw?.threadId,
    raw?.threadID,
    raw?.thread_id,
    raw?.codexThreadId,
    raw?.codex_thread_id,
    thread?.threadId,
    thread?.threadID,
    thread?.thread_id,
    thread?.codexThreadId,
    thread?.codex_thread_id,
    raw?.id,
    thread?.id,
    raw?.agentId,
    raw?.agent_id,
    thread?.agentId,
    thread?.agent_id,
  );
  const agentId = normalizeThreadId(
    raw?.agentId ||
    raw?.agent_id ||
    thread?.agentId ||
    thread?.agent_id ||
    firstRuntimeAgentId(raw?.id, thread?.id),
  );
  return {
    threadId: id,
    agentId,
    providerThreadId: normalizeThreadId(raw?.providerThreadId || raw?.provider_thread_id || thread?.providerThreadId || thread?.provider_thread_id),
    sessionId: normalizeThreadId(raw?.sessionId || raw?.session_id || thread?.sessionId || thread?.session_id),
  };
}

function firstThreadCopyText(...values) {
  for (const value of values) {
    if (value === null || value === undefined || typeof value === 'boolean') continue;
    if (typeof value === 'number' && Number.isFinite(value)) return String(value);
    if (typeof value !== 'string') continue;
    const text = value.trim();
    if (text && text !== '.' && text !== '[object Object]') return text;
  }
  return '';
}

function positiveThreadCopyPort(...values) {
  for (const value of values) {
    const number = Number(value);
    if (Number.isFinite(number) && number > 0) return number;
  }
  return null;
}

function normalizeLogScopeCwd(value) {
  const raw = normalizePath(value);
  if (!raw || raw === '.') return '';
  return raw;
}

function buildCwdLogPath(cwd) {
  const normalized = normalizeLogScopeCwd(cwd);
  if (!normalized || /^[A-Za-z]:$/.test(normalized) || /^[\\/]+$/.test(normalized)) return null;
  const projectName = normalized.split(/[\\/]/).filter(Boolean).pop() || '';
  if (!projectName || projectName === '.' || projectName === '/') return null;
  return `~/.multi-agent/log/${projectName}/`;
}

function formatUTC8HumanReadable(value = new Date()) {
  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime())) return '';
  const utc8 = new Date(date.getTime() + (8 * 60 * 60 * 1000));
  const year = utc8.getUTCFullYear();
  const month = String(utc8.getUTCMonth() + 1).padStart(2, '0');
  const day = String(utc8.getUTCDate()).padStart(2, '0');
  const hours = String(utc8.getUTCHours()).padStart(2, '0');
  const minutes = String(utc8.getUTCMinutes()).padStart(2, '0');
  const seconds = String(utc8.getUTCSeconds()).padStart(2, '0');
  return `${year}-${month}-${day} ${hours}:${minutes}:${seconds} UTC+8`;
}

function buildThreadCopyPayload({ state, threadId, thread = {}, identity = {}, threadConfig = null }) {
  const providerThreadId = firstThreadCopyText(
    identity.providerThreadId,
    identity.provider_thread_id,
    thread.providerThreadId,
    thread.provider_thread_id,
  );
  const agentId = firstThreadCopyText(
    identity.agentId,
    identity.agent_id,
    thread.agentId,
    thread.agent_id,
    threadId,
  );
  const provider = firstThreadCopyText(identity.provider, thread.provider, state.provider) || DEFAULT_PROVIDER;
  const cwd = firstThreadCopyText(identity.cwd, identity.CWD, thread.cwd, state.activeProject, state.cwd);
  const model = firstThreadCopyText(
    identity.model,
    identity.effective?.model,
    thread.model,
    thread.effective?.model,
    threadConfig?.effective?.model,
    state.providerConfig?.model,
  );
  const effort = firstThreadCopyText(
    identity.effort,
    identity.reasoningEffort,
    identity.reasoning_effort,
    identity.effective?.effort,
    thread.effort,
    thread.effective?.effort,
    threadConfig?.effective?.effort,
    state.providerConfig?.effort,
  );

  return {
    agentId,
    providerThreadId,
    uuid: firstThreadCopyText(identity.uuid, identity.sessionId, identity.session_id, providerThreadId),
    name: firstThreadCopyText(identity.name, thread.name),
    status: firstThreadCopyText(identity.status, thread.status, state.statuses?.[threadId]),
    provider,
    model: model || null,
    effort: effort || null,
    port: positiveThreadCopyPort(identity.port, thread.port),
    cwd: cwd || null,
    'log-path': firstThreadCopyText(
      identity['log-path'],
      identity.logPath,
      identity.log_path,
      thread.logPath,
      thread.log_path,
    ) || buildCwdLogPath(cwd),
    copiedAt: formatUTC8HumanReadable(),
  };
}
function isArchivedStatus(value) {
  const status = normalizeString(value).toLowerCase();
  return status === 'archived' || status === '归档' || status === '已归档';
}

function threadArchiveStatus(thread, archived) {
  if (archived) return 'archived';
  if (isArchivedStatus(thread.status)) return 'created';
  return thread.status;
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
    runtimeCwdForThread(rawThread, runtimeById),
  );
}

function threadMatchesCwdScope(rawThread, scopeCwd, runtimeById = {}) {
  const scope = normalizePath(scopeCwd);
  if (!scope || scope === '.') return true;
  const threadCwd = snapshotThreadCwd(rawThread, runtimeById);
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
  const existing = threads.find((candidate) => ids.some((id) => threadMatchesIdentifier(candidate, id)));
  const merged = existing ? { ...existing, ...thread } : thread;
  return [
    merged,
    ...threads.filter((candidate) => !ids.some((id) => threadMatchesIdentifier(candidate, id))),
  ];
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

function normalizeTurnSummary(value) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null;
  const id = normalizeString(value.id || value.turnId || value.turn_id);
  if (!id) return null;
  return {
    id,
    threadId: normalizeBackendThreadId(value.threadId || value.thread_id),
    agentId: normalizeThreadId(value.agentId || value.agent_id),
    status: normalizeString(value.status),
    startedAt: normalizeString(value.startedAt || value.started_at || value.createdAt || value.created_at || value.ts || value.time),
    updatedAt: normalizeString(value.updatedAt || value.updated_at),
    completedAt: normalizeString(value.completedAt || value.completed_at || value.finishedAt || value.finished_at),
  };
}

const TERMINAL_ACTIVE_TURN_STATUSES = new Set([
  'idle',
  'completed',
  'complete',
  'done',
  'ok',
  'success',
  'succeeded',
  'failed',
  'fail',
  'error',
  'interrupted',
  'canceled',
  'cancelled',
  'aborted',
  'stopped',
  'ended',
  'closed',
  'ready',
  'stalled',
  'archived',
  '空闲',
  '已完成',
  '失败',
  '错误',
  '已中断',
  '已停止',
]);

function isTerminalActiveTurnStatus(status) {
  const normalized = normalizeString(status).toLowerCase();
  return Boolean(normalized && TERMINAL_ACTIVE_TURN_STATUSES.has(normalized));
}

function isInterruptibleTurnSummary(turn) {
  return Boolean(turn?.id && !isTerminalActiveTurnStatus(turn.status));
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

function activeTurnPayload(payload = {}) {
  if (hasOwn(payload, 'active_turn')) return payload.active_turn;
  if (hasOwn(payload, 'activeTurn')) return payload.activeTurn;
  return undefined;
}

function shouldFloatThreadPatch(payload = {}) {
  if (normalizeString(payload.source || payload.event || payload.type) !== 'turn/completed') return false;
  const thread = payload.thread && typeof payload.thread === 'object' ? payload.thread : {};
  const status = normalizeString(payload.status || thread.state || thread.status).toLowerCase();
  return !status || ['idle', 'completed', 'success', 'succeeded'].includes(status);
}

function threadActivityTimestamp() {
  return Date.now();
}

function tokenUsageObject(value) {
  return value && typeof value === 'object' && !Array.isArray(value) ? value : null;
}

function tokenUsageNumber(source, keys) {
  const object = tokenUsageObject(source);
  if (!object) return null;
  for (const key of keys) {
    if (!hasOwn(object, key)) continue;
    const number = Number(object[key]);
    if (Number.isFinite(number)) return number;
  }
  return null;
}

function tokenUsageIO(source) {
  const input = tokenUsageNumber(source, ['input', 'inputTokens', 'input_tokens', 'promptTokens', 'prompt_tokens']);
  const output = tokenUsageNumber(source, ['output', 'outputTokens', 'output_tokens', 'completionTokens', 'completion_tokens']);
  if (input === null && output === null) return null;
  return (input || 0) + (output || 0);
}

function firstTokenUsageNumber(...values) {
  return values.find((value) => Number.isFinite(value)) ?? null;
}

function normalizeTokenUsage(value) {
  if (!value || typeof value !== 'object') return null;
  const usage = tokenUsageObject(value.usage);
  const info = tokenUsageObject(value.info);
  const tokenUsage = tokenUsageObject(value.tokenUsage);
  const currentUsage = tokenUsageObject(tokenUsage?.last) || tokenUsageObject(info?.last_token_usage);
  const cumulativeUsage = tokenUsageObject(tokenUsage?.total) || tokenUsageObject(info?.total_token_usage);
  const inputTokens = firstTokenUsageNumber(
    tokenUsageNumber(currentUsage, ['input', 'inputTokens', 'input_tokens', 'promptTokens', 'prompt_tokens']),
    tokenUsageNumber(usage, ['input', 'inputTokens', 'input_tokens', 'promptTokens', 'prompt_tokens']),
    tokenUsageNumber(value, ['input', 'inputTokens', 'input_tokens', 'promptTokens', 'prompt_tokens']),
    tokenUsageNumber(cumulativeUsage, ['input', 'inputTokens', 'input_tokens', 'promptTokens', 'prompt_tokens']),
    0,
  );
  const outputTokens = firstTokenUsageNumber(
    tokenUsageNumber(currentUsage, ['output', 'outputTokens', 'output_tokens', 'completionTokens', 'completion_tokens']),
    tokenUsageNumber(usage, ['output', 'outputTokens', 'output_tokens', 'completionTokens', 'completion_tokens']),
    tokenUsageNumber(value, ['output', 'outputTokens', 'output_tokens', 'completionTokens', 'completion_tokens']),
    tokenUsageNumber(cumulativeUsage, ['output', 'outputTokens', 'output_tokens', 'completionTokens', 'completion_tokens']),
    0,
  );
  const usedTokens = firstTokenUsageNumber(
    tokenUsageNumber(value, ['usedTokens', 'used_tokens']),
    tokenUsageNumber(currentUsage, ['totalTokens', 'total_tokens']),
    tokenUsageIO(currentUsage),
    tokenUsageNumber(usage, ['totalTokens', 'total_tokens']),
    tokenUsageNumber(value, ['totalTokens', 'total_tokens']),
    tokenUsageNumber(cumulativeUsage, ['totalTokens', 'total_tokens']),
    tokenUsageIO(cumulativeUsage),
    inputTokens + outputTokens,
    0,
  );
  const contextWindowTokens = firstTokenUsageNumber(
    tokenUsageNumber(value, ['contextWindowTokens', 'context_window_tokens', 'contextWindow', 'context_window', 'modelContextWindow', 'model_context_window']),
    tokenUsageNumber(tokenUsage, ['contextWindowTokens', 'context_window_tokens', 'contextWindow', 'context_window', 'modelContextWindow', 'model_context_window']),
    tokenUsageNumber(usage, ['contextWindowTokens', 'context_window_tokens', 'contextWindow', 'context_window', 'modelContextWindow', 'model_context_window']),
    tokenUsageNumber(info, ['contextWindowTokens', 'context_window_tokens', 'contextWindow', 'context_window', 'modelContextWindow', 'model_context_window']),
    0,
  );
  const rawPercent = firstTokenUsageNumber(
    tokenUsageNumber(value, ['usedPercent', 'used_percent']),
    contextWindowTokens > 0 ? (usedTokens / contextWindowTokens) * 100 : 0,
  ) || 0;
  const usedPercent = Math.min(100, Math.max(0, rawPercent));
  return { usedTokens, contextWindowTokens, usedPercent };
}

function normalizeActivityStats(value) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null;
  const rawToolCalls = value.toolCalls || value.tool_calls || {};
  const toolCalls = {};
  if (Object.keys(objectRecord(rawToolCalls)).length > 0) {
    for (const [name, count] of Object.entries(rawToolCalls)) {
      const key = normalizeString(name);
      const numeric = Number(count);
      if (key && Number.isFinite(numeric) && numeric > 0) toolCalls[key] = numeric;
    }
  }
  return {
    lspCalls: positiveNumberFromFields(value, ACTIVITY_COUNT_FIELDS.lspCalls),
    commands: positiveNumberFromFields(value, ACTIVITY_COUNT_FIELDS.commands),
    fileEdits: positiveNumberFromFields(value, ACTIVITY_COUNT_FIELDS.fileEdits),
    toolCalls,
  };
}

function extractText(value) {
  if (value === null || value === undefined) return '';
  if (typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean') {
    return normalizeString(value);
  }
  if (Array.isArray(value)) {
    return value.map((item) => extractText(item)).filter(Boolean).join('\n');
  }
  if (typeof value === 'object') {
    return extractText(value.text || value.content || value.message || value.delta || value.output || value.result || value.answer || value.response);
  }
  return '';
}

function normalizeTimelineKindFromRaw(rawRole, rawKind) {
  if (rawRole.includes('user')) return 'user';
  if (rawKind.includes('approval')) return 'approval';
  if (rawKind.includes('thinking') || rawKind.includes('reasoning')) return 'thinking';
  if (rawKind.includes('command') || rawKind.includes('exec')) return 'command';
  if (rawKind.includes('tool')) return 'tool';
  if (rawKind.includes('plan')) return 'plan';
  return 'assistant';
}

function normalizeTimelineElapsedMs(item) {
  if (item?.elapsedMs !== undefined) return Number(item.elapsedMs);
  if (item?.elapsed_ms !== undefined) return Number(item.elapsed_ms);
  if (item?.durationMs !== undefined) return Number(item.durationMs);
  if (item?.duration_ms !== undefined) return Number(item.duration_ms);
  return undefined;
}

function normalizeTimelineDone(item, status) {
  const normalizedStatus = normalizeString(status).toLowerCase();
  if (TIMELINE_TERMINAL_STATUSES.has(normalizedStatus)) return true;
  if (typeof item?.done === 'boolean') return item.done;
  if (!normalizedStatus) return true;
  return false;
}

function normalizeUserTimelineText(text) {
  let processed = text;
  if (processed) {
    processed = processed.replace(IMAGE_PLACEHOLDER_RE, '').trim();
  }
  const trimmed = normalizeString(processed).trim();
  const closeTag = '</turn_aborted>';
  const lower = trimmed.toLowerCase();
  if (!lower.startsWith('<turn_aborted>')) {
    return { text: processed, controlOnly: false };
  }
  const closeIndex = lower.indexOf(closeTag);
  const remaining = closeIndex >= 0 ? trimmed.slice(closeIndex + closeTag.length).trimStart() : '';
  return {
    text: remaining,
    controlOnly: !remaining,
  };
}

function normalizeTimelineItem(item) {
  const rawKind = normalizeString(firstFieldValue(item, TIMELINE_KIND_KEYS)).toLowerCase();
  const rawRole = normalizeString(firstFieldValue(item, TIMELINE_ROLE_KEYS)).toLowerCase();
  const normalizedRole = rawRole.includes('user') ? 'user' : 'assistant';
  const normalizedKind = normalizeTimelineKindFromRaw(rawRole, rawKind);
  const rawText = extractText(firstFieldValue(item, TIMELINE_TEXT_KEYS));
  const userText = normalizedRole === 'user' ? normalizeUserTimelineText(rawText) : { text: rawText, controlOnly: false };
  const status = normalizeString(item?.status);
  return {
    id: normalizeString(firstFieldValue(item, TIMELINE_ID_KEYS)) || `${normalizedRole}-${Date.now()}`,
    role: normalizedRole,
    kind: normalizedKind,
    text: userText.text,
    controlOnly: userText.controlOnly,
    title: normalizeString(firstFieldValue(item, TIMELINE_TITLE_KEYS)),
    callId: normalizeString(firstFieldValue(item, TIMELINE_CALL_ID_KEYS)),
    requestId: positiveNumberFromFields(item, ['requestId', 'request_id']),
    command: normalizeString(item?.command),
    toolName: normalizeString(item?.tool || item?.toolName || item?.tool_name),
    status,
    time: normalizeString(firstFieldValue(item, TIMELINE_TIME_KEYS)) || new Date().toISOString(),
    completedAt: normalizeString(firstFieldValue(item, TIMELINE_COMPLETED_KEYS)),
    done: normalizeTimelineDone(item, status),
    optimistic: Boolean(item?.optimistic),
    elapsedMs: normalizeTimelineElapsedMs(item),
  };
}

function normalizeThreadMessagesTotal(value) {
  if (value === null || value === undefined || value === '') return null;
  const total = Number(value);
  return Number.isFinite(total) && total >= 0 ? total : null;
}

function normalizeThreadMessagesBoolean(value) {
  if (typeof value === 'boolean') return value;
  if (typeof value === 'number') return value > 0;
  const normalized = normalizeString(value).toLowerCase();
  if (normalized === 'true') return true;
  if (normalized === 'false') return false;
  if (normalized === '1') return true;
  if (normalized === '0') return false;
  return Boolean(value);
}

function threadMessageNumericId(message) {
  const value = Number(message?.id);
  return Number.isSafeInteger(value) && value > 0 ? value : 0;
}

function oldestThreadMessageCursor(messages) {
  const ids = messages.map(threadMessageNumericId).filter((id) => id > 0);
  if (ids.length > 0) return String(Math.min(...ids));

  const timestamps = messages
    .map((message) => normalizeString(message?.createdAt || message?.created_at))
    .map((raw) => ({ raw, timestamp: Date.parse(raw) }))
	    .filter(({ raw, timestamp }) => raw && Number.isFinite(timestamp) && timestamp > 0)
	    .sort((left, right) => left.timestamp - right.timestamp);
	  return timestamps[0]?.raw || '';
}

function normalizeThreadMessagesPageMeta(res, page) {
  const backendHasMore = hasOwn(res, 'hasMore') || hasOwn(res, 'has_more');
  const hasMore = backendHasMore
    ? normalizeThreadMessagesBoolean(res.hasMore ?? res.has_more)
    : (normalizeThreadMessagesTotal(res?.total) ?? page.length) > page.length || page.length >= THREAD_MESSAGES_PAGE_SIZE;
  const nextBefore = normalizeString(res?.nextBefore || res?.next_before);
  return {
    hasMore,
    nextBefore: hasMore ? nextBefore || (backendHasMore ? '' : oldestThreadMessageCursor(page)) : '',
  };
}

function threadMessagesPaginationPatch(state, id, patch = {}) {
  return {
    threadMessagePaginationByThread: {
      ...state.threadMessagePaginationByThread,
      [id]: {
        ...(state.threadMessagePaginationByThread[id] || { hasMore: false, nextBefore: '', loading: false }),
        ...patch,
      },
    },
  };
}

function compactRuntimeResultText(value) {
  if (value === null || value === undefined) return '';
  const text = typeof value === 'string' ? value : JSON.stringify(value);
  const normalized = normalizeString(text);
  if (!normalized) return '';
  if (normalized.length <= RUNTIME_RESULT_DETAIL_LIMIT) return normalized;
  return `${normalized.slice(0, RUNTIME_RESULT_DETAIL_LIMIT)}...`;
}

function normalizeRuntimeToolName(name) {
  const raw = normalizeString(name);
  if (!raw) return '';
  const lower = raw.toLowerCase();
  const mcpParts = lower.startsWith('mcp__') ? lower.split('__') : [];
  const withoutMCPServer = mcpParts.length >= 3 ? mcpParts.slice(2).join('__') : raw;
  return (
    withoutMCPServer
    .replace(/[./:-]+/g, '_')
    .replace(/^functions_+/, '')
    .replace(/^function_+/, '')
    .replace(/^tools_+/, '')
    .replace(/^tool_+/, '')
    .replace(/^lsp_+/, '')
    .replace(/_+/g, '_')
    .replace(/^_+|_+$/g, '')
  );
}

function runtimeToolResultDetail(item = {}) {
  for (const key of ['output', 'preview', 'result', 'error', 'message', 'text']) {
    const detail = compactRuntimeResultText(item[key]);
    if (detail) return detail;
  }
  return '';
}

function runtimeToolResultEntry(item, threadId, index = 0) {
  const kind = normalizeString(item?.kind || item?.type).toLowerCase();
  if (kind !== 'tool') return null;
  const toolName = normalizeRuntimeToolName(item.tool || item.toolName || item.name) || 'tool';
  const status = normalizeString(item.status).toLowerCase();
  const failed = RUNTIME_TOOL_FAILED_STATUSES.has(status) || item.success === false || Boolean(normalizeString(item.error));
  const detail = runtimeToolResultDetail(item);
  const terminal = RUNTIME_TOOL_TERMINAL_STATUSES.has(status);
  if (!detail && !terminal) return null;
  const summary = detail ? detail.replace(/\s+/g, ' ').slice(0, 180) : '';
  return {
    id: normalizeString(item.id) || `tool-result-${threadId}-${index}-${Date.now()}`,
    timestamp: normalizeString(item.ts || item.time || item.createdAt || item.created_at) || new Date().toISOString(),
    level: failed ? 'error' : 'info',
    event: 'tool.result',
    threadId,
    message: `${toolName} ${failed ? '失败' : '返回'}${summary ? ` · ${summary}` : ''}`,
    detail,
    fields: item,
    signature: `tool.result|${threadId}|${normalizeString(item.id) || toolName}|${detail}`,
  };
}

function runtimeResultEntriesFromTimelineItems(items, threadId) {
  if (!Array.isArray(items) || !threadId) return [];
  return (
    items
    .map((item, index) => runtimeToolResultEntry(item, threadId, index))
    .filter(Boolean)
  );
}

function runtimeResultEntryFromRPCDone(event, fields = {}) {
  if (event !== 'api.rpc.done') return null;
  const method = normalizeString(fields.method || fields.rpcMethod || fields.rpc_method);
  const detail = compactRuntimeResultText(fields.result_preview || fields.result);
  if (!method || !detail) return null;
  const threadId = normalizeThreadId(runtimeThreadIdentifier(fields));
  const summary = detail.replace(/\s+/g, ' ').slice(0, 180);
  return {
    id: `${event}-${fields.req_id || Date.now()}-${Math.random().toString(16).slice(2)}`,
    timestamp: new Date().toISOString(),
    level: 'info',
    event,
    threadId,
    message: `${method} 返回 · ${summary}`,
    detail,
    fields,
    signature: `${event}|${threadId}|${method}|${detail}`,
  };
}

function mergeRuntimeResultEntries(existingEntries = [], incomingEntries = []) {
  const nextById = new Map();
  for (const entry of [...incomingEntries, ...existingEntries]) {
    const key = entry?.signature || entry?.id;
    if (!key) continue;
    const existing = nextById.get(key);
    if (existing) {
      nextById.set(key, {
        ...existing,
        occurrenceCount: (Number(existing.occurrenceCount) || 1) + (Number(entry.occurrenceCount) || 1),
      });
      continue;
    }
    nextById.set(key, entry);
  }
  return (
    [...nextById.values()]
    .sort((left, right) => {
      const leftTime = normalizeTimestamp(left.timestamp);
      const rightTime = normalizeTimestamp(right.timestamp);
      return rightTime - leftTime;
    })
    .slice(0, MAX_RUNTIME_RESULT_ENTRIES)
  );
}

function sortTimelineChronologically(items = []) {
  return (
    [...items]
    .map((item, index) => ({ item, index, timestamp: normalizeTimestamp(item?.time) }))
    .sort((left, right) => {
      if (left.timestamp !== right.timestamp) return left.timestamp - right.timestamp;
      return left.index - right.index;
    })
    .map(({ item }) => item)
  );
}

function lifecycleTimelineKey(item) {
  const kind = normalizeTimelineKind(item);
  const callId = normalizeString(item?.callId);
  if (item?.role !== 'assistant' || !callId || !TIMELINE_LIFECYCLE_KINDS.has(kind)) return '';
  return `${kind}:${callId}`;
}

function isTerminalTimelineItem(item) {
  const status = normalizeString(item?.status).toLowerCase();
  return normalizeTimelineDone(item, status);
}

function timelineItemTextLength(item) {
  return normalizeString(item?.text).length;
}

function timelineItemSortTime(item) {
  return normalizeTimestamp(item?.completedAt || item?.time);
}

function preferredLifecycleTimelineItem(existingItem, incomingItem) {
  const existingTerminal = isTerminalTimelineItem(existingItem);
  const incomingTerminal = isTerminalTimelineItem(incomingItem);
  if (existingTerminal !== incomingTerminal) return incomingTerminal ? incomingItem : existingItem;

  const existingTextLength = timelineItemTextLength(existingItem);
  const incomingTextLength = timelineItemTextLength(incomingItem);
  if (existingTextLength !== incomingTextLength) return incomingTextLength > existingTextLength ? incomingItem : existingItem;

  const existingTime = timelineItemSortTime(existingItem);
  const incomingTime = timelineItemSortTime(incomingItem);
  if (existingTime !== incomingTime) return incomingTime > existingTime ? incomingItem : existingItem;

  return incomingItem;
}

function earlierTimelineTime(left, right) {
  const leftTime = normalizeString(left?.time);
  const rightTime = normalizeString(right?.time);
  const leftTimestamp = normalizeTimestamp(leftTime);
  const rightTimestamp = normalizeTimestamp(rightTime);
  if (leftTimestamp > 0 && rightTimestamp > 0) return leftTimestamp <= rightTimestamp ? leftTime : rightTime;
  return leftTime || rightTime;
}

function mergeLifecycleTimelineItem(existingItem, incomingItem) {
  const preferred = preferredLifecycleTimelineItem(existingItem, incomingItem);
  const fallback = preferred === incomingItem ? existingItem : incomingItem;
  const status = normalizeString(preferred?.status) || normalizeString(fallback?.status);
  const text = normalizeString(preferred?.text) ? preferred.text : fallback.text;

  return {
    ...fallback,
    ...preferred,
    id: normalizeString(preferred?.id) || normalizeString(fallback?.id),
    callId: normalizeString(preferred?.callId) || normalizeString(fallback?.callId),
    text,
    title: normalizeString(preferred?.title) || normalizeString(fallback?.title),
    status,
    time: earlierTimelineTime(existingItem, incomingItem),
    completedAt: normalizeString(preferred?.completedAt) || normalizeString(fallback?.completedAt),
    done: normalizeTimelineDone(preferred, status),
    elapsedMs: preferred?.elapsedMs ?? fallback?.elapsedMs,
  };
}

function coalesceTimelineLifecycleItems(items = []) {
  const output = [];
  const indexByKey = new Map();

  for (const item of items) {
    const key = lifecycleTimelineKey(item);
    if (!key) {
      output.push(item);
      continue;
    }

    const existingIndex = indexByKey.get(key);
    if (existingIndex === undefined) {
      indexByKey.set(key, output.length);
      output.push(item);
      continue;
    }

    output[existingIndex] = mergeLifecycleTimelineItem(output[existingIndex], item);
  }

  return output;
}

function sameTimelineContent(left, right) {
  return left?.role === right?.role && normalizeTimelineKind(left) === normalizeTimelineKind(right) && normalizeString(left?.text) === normalizeString(right?.text);
}

function compactTimelineText(value) {
  return normalizeString(value).replace(/\s+/g, '');
}

function looseTimelineText(value) {
  return compactTimelineText(value).toLowerCase().replace(/[^\p{L}\p{N}]+/gu, '');
}

function sameTimelineContentCompact(left, right) {
  return (
    left?.role === right?.role &&
    normalizeTimelineKind(left) === normalizeTimelineKind(right) &&
    compactTimelineText(left?.text) &&
    compactTimelineText(left?.text) === compactTimelineText(right?.text)
  );
}

function looseTimelineShingleMatch(shorterText, longerText) {
  if (shorterText.length < RUNTIME_ASSISTANT_LOOSE_DUPLICATE_MIN_CHARS) return false;
  const shingleSize = Math.min(RUNTIME_ASSISTANT_LOOSE_DUPLICATE_SHINGLE_SIZE, Math.floor(shorterText.length / RUNTIME_ASSISTANT_LOOSE_DUPLICATE_MIN_MATCHES));
  if (shingleSize <= 0) return false;
  const shingles = new Set();
  for (let index = 0; index <= shorterText.length - shingleSize; index += shingleSize) {
    shingles.add(shorterText.slice(index, index + shingleSize));
  }
  shingles.add(shorterText.slice(-shingleSize));
  const candidates = [...shingles].filter(Boolean);
  if (candidates.length < RUNTIME_ASSISTANT_LOOSE_DUPLICATE_MIN_MATCHES) return false;
  const matches = candidates.filter((candidate) => longerText.includes(candidate)).length;
  return matches >= RUNTIME_ASSISTANT_LOOSE_DUPLICATE_MIN_MATCHES && (matches / candidates.length) >= RUNTIME_ASSISTANT_LOOSE_DUPLICATE_MIN_RATIO;
}

function sameRuntimeAssistantContentLoose(left, right) {
  if (!left?.runtime && !right?.runtime) return false;
  if (left?.role !== right?.role || normalizeTimelineKind(left) !== normalizeTimelineKind(right)) return false;
  const leftText = looseTimelineText(left?.text);
  const rightText = looseTimelineText(right?.text);
  if (!leftText || !rightText) return false;
  const shorterText = leftText.length <= rightText.length ? leftText : rightText;
  const longerText = leftText.length > rightText.length ? leftText : rightText;
  if (shorterText.length < RUNTIME_ASSISTANT_LOOSE_DUPLICATE_MIN_CHARS) return false;
  return longerText.includes(shorterText) || looseTimelineShingleMatch(shorterText, longerText);
}

function sameTimelineContentPrefix(left, right) {
  if (left?.role !== right?.role || normalizeTimelineKind(left) !== normalizeTimelineKind(right)) return false;
  const leftText = compactTimelineText(left?.text);
  const rightText = compactTimelineText(right?.text);
  const shorterLength = Math.min(leftText.length, rightText.length);
  if (shorterLength < RUNTIME_ASSISTANT_PREFIX_DUPLICATE_MIN_CHARS) return false;
  return leftText.startsWith(rightText) || rightText.startsWith(leftText);
}

function sameTimelineSubstring(left, right) {
  if (left?.role !== right?.role || normalizeTimelineKind(left) !== normalizeTimelineKind(right)) return false;
  const leftText = compactTimelineText(left?.text);
  const rightText = compactTimelineText(right?.text);
  if (!leftText || !rightText) return false;
  const shorterLength = Math.min(leftText.length, rightText.length);
  if (shorterLength < 15) return false;
  return leftText.includes(rightText) || rightText.includes(leftText);
}

function sameTimelineDuplicateContent(left, right) {
  return sameTimelineContent(left, right) || sameTimelineContentCompact(left, right) || sameRuntimeAssistantContentLoose(left, right) || sameTimelineSubstring(left, right);
}

function normalizeTimelineKind(item) {
  const kind = normalizeString(item?.kind).toLowerCase();
  if (kind) return kind;
  return item?.role === 'user' ? 'user' : 'assistant';
}

function isInjectedPromptTimelineItem(item) {
  if (item?.role !== 'user') return false;
  const text = normalizeString(item?.text).trim();
  if (!text) return false;
  return /^#\s+AGENTS\.md instructions for .+\n/i.test(text) && /<INSTRUCTIONS>[\s\S]*<\/INSTRUCTIONS>/i.test(text);
}

const MESSAGE_LIFECYCLE_ITEM_TYPES = new Set(['message', 'usermessage', 'user_message', 'assistantmessage', 'assistant_message']);
const GENERIC_COMMAND_TITLES = new Set(['command', 'execute command', 'running command', '执行命令', '命令', '终端命令']);

function isMessageLifecycleTimelineItem(item) {
  if (item?.role) return false;
  return MESSAGE_LIFECYCLE_ITEM_TYPES.has(normalizeString(item?.itemType).toLowerCase());
}

function isToolBackedCommandTimelineItem(item) {
  return Boolean(normalizeString(item?.tool || item?.toolName || item?.tool_name));
}

function isMeaningfulCommandTimelineItem(item) {
  if (normalizeString(item?.command)) return true;
  if (normalizeString(item?.text || item?.output || item?.error)) return true;
  const title = normalizeString(item?.title).trim();
  return Boolean(title.startsWith('$ ') && !GENERIC_COMMAND_TITLES.has(title.toLowerCase()));
}

function isVisibleTimelineItem(item) {
  if (item?.controlOnly) return false;
  if (isInjectedPromptTimelineItem(item)) return false;
  if (isMessageLifecycleTimelineItem(item)) return false;
  if (item?.role === 'user') return true;
  if (normalizeString(item?.text)) return true;
  const kind = normalizeTimelineKind(item);
  if (kind === 'command') return !isToolBackedCommandTimelineItem(item) && isMeaningfulCommandTimelineItem(item);
  return kind === 'thinking' || kind === 'reasoning' || kind === 'tool' || kind === 'process' || kind === 'plan';
}

function preferredAssistantTimelineItem(existingItem, incomingItem) {
  const isRuntime = Boolean(existingItem?.runtime || incomingItem?.runtime);
  if (existingItem?.runtime !== incomingItem?.runtime) {
    const base = incomingItem?.runtime ? existingItem : incomingItem;
    return isRuntime ? { ...base, runtime: true } : base;
  }
  return (
    normalizeString(incomingItem?.text).length > normalizeString(existingItem?.text).length
    ? incomingItem
    : existingItem
  );
}

function dedupeAssistantTimelineItems(items = []) {
  const output = [];
  let lastUserIndex = -1;
  const seenIds = new Set();

  for (const item of items) {
    if (item?.role === 'user') {
      output.push(item);
      lastUserIndex = output.length - 1;
      continue;
    }

    if (item?.role !== 'assistant' || !compactTimelineText(item.text)) {
      output.push(item);
      continue;
    }

    // fast-path: skip exact id duplicates (cross-turn reconnect dedup)
    if (item.id && seenIds.has(item.id)) continue;

    const duplicateIndices = [];
    for (let index = output.length - 1; index > lastUserIndex; index -= 1) {
      const candidate = output[index];
      if (candidate?.role === 'assistant' && sameTimelineDuplicateContent(candidate, item)) {
        duplicateIndices.push(index);
      }
    }

    if (duplicateIndices.length > 0) {
      let mergedItem = item;
      let anyDone = Boolean(item.done);
      for (const index of duplicateIndices) {
        if (output[index].done) anyDone = true;
        mergedItem = preferredAssistantTimelineItem(output[index], mergedItem);
      }
      if (anyDone) {
        mergedItem = { ...mergedItem, done: true };
      }
      const primaryIndex = duplicateIndices[duplicateIndices.length - 1]; // earliest duplicate index
      output[primaryIndex] = mergedItem;

      const indicesToRemove = new Set(duplicateIndices.slice(0, -1));
      if (indicesToRemove.size > 0) {
        let writeIdx = lastUserIndex + 1;
        for (let readIdx = lastUserIndex + 1; readIdx < output.length; readIdx++) {
          if (!indicesToRemove.has(readIdx)) {
            output[writeIdx] = output[readIdx];
            writeIdx++;
          }
        }
        output.length = writeIdx;
      }
      continue;
    }

    output.push(item);
    if (item.id) seenIds.add(item.id);
  }

  return output;
}

function areTimelineItemsEquivalent(left, right) {
  if (!left || !right) return left === right;
  if (left.id !== right.id) return false;
  if (left.role !== right.role) return false;
  if (left.kind !== right.kind) return false;

  const normText = (val) => (val || '').toString().trim();
  if (normText(left.text) !== normText(right.text)) return false;
  if (normText(left.status) !== normText(right.status)) return false;
  if (normText(left.completedAt) !== normText(right.completedAt)) return false;
  if (normText(left.title) !== normText(right.title)) return false;
  if (normText(left.command) !== normText(right.command)) return false;
  if (normText(left.toolName) !== normText(right.toolName)) return false;
  if (normText(left.callId) !== normText(right.callId)) return false;
  if (normText(left.time) !== normText(right.time)) return false;

  if (Boolean(left.done) !== Boolean(right.done)) return false;
  if (Boolean(left.optimistic) !== Boolean(right.optimistic)) return false;
  if (Boolean(left.runtime) !== Boolean(right.runtime)) return false;
  if (Boolean(left.controlOnly) !== Boolean(right.controlOnly)) return false;

  if ((left.elapsedMs ?? 0) !== (right.elapsedMs ?? 0)) return false;
  if ((left.requestId ?? 0) !== (right.requestId ?? 0)) return false;

  return true;
}

function mergeTimelineItems(existingItems = [], incomingItems = [], options = {}) {
  const preserveExistingVisible = options?.preserveExistingVisible === true;
  const visibleIncomingItems = incomingItems.filter(isVisibleTimelineItem);
  const incomingById = new Map(visibleIncomingItems.map((item) => [item.id, item]));
  const uniqueIncomingItems = visibleIncomingItems.filter((item) => incomingById.get(item.id) === item);
  const incomingIds = new Set(incomingById.keys());
  
  // Maps to associate duplicate existing items and preserve their attachments
  const duplicateExistingByIncomingId = new Map();
  const duplicateExistingIds = new Set();
  
  for (const existingItem of existingItems) {
    if (incomingIds.has(existingItem.id)) continue;
    
    // Find if this existing item is a duplicate of any incoming item
    const duplicateIncoming = uniqueIncomingItems.find((incomingItem) => 
      sameTimelineDuplicateContent(existingItem, incomingItem)
    );
    
    if (duplicateIncoming) {
      duplicateExistingByIncomingId.set(duplicateIncoming.id, existingItem);
      duplicateExistingIds.add(existingItem.id);
    }
  }

  const consumedIncomingIds = new Set();
  const merged = [];

  for (const existingItem of existingItems) {
    // If it was marked as a duplicate of a different-ID incoming item, skip it
    if (duplicateExistingIds.has(existingItem.id)) {
      continue;
    }

    const replacement = incomingById.get(existingItem.id);
    if (replacement) {
      if (areTimelineItemsEquivalent(existingItem, replacement)) {
        merged.push(existingItem);
      } else {
        // Keep attachments from existingItem if replacement doesn't have them
        merged.push({
          ...replacement,
          attachments: replacement.attachments || existingItem.attachments,
        });
      }
      consumedIncomingIds.add(replacement.id);
      continue;
    }

    const shouldPreserveExistingMessage = (
      ((preserveExistingVisible && isVisibleTimelineItem(existingItem)) || existingItem.role === 'user' || existingItem.optimistic || existingItem.runtime)
    );
    if (shouldPreserveExistingMessage) {
      merged.push(existingItem);
    }
  }

  for (const incomingItem of uniqueIncomingItems) {
    if (!consumedIncomingIds.has(incomingItem.id)) {
      // If there was a duplicate existing item with attachments, preserve them!
      const duplicateExisting = duplicateExistingByIncomingId.get(incomingItem.id);
      const mergedIncoming = duplicateExisting && duplicateExisting.attachments
        ? { ...incomingItem, attachments: incomingItem.attachments || duplicateExisting.attachments }
        : incomingItem;
        
      merged.push(mergedIncoming);
    }
  }

  return dedupeAssistantTimelineItems(coalesceTimelineLifecycleItems(sortTimelineChronologically(merged)));
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
  return {
    activeThreadId,
    threads: nextThreads,
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

function runtimeTurnId(payload = {}) {
  return normalizeString(payload.turnId || payload.turn_id || payload.turn?.id);
}

function runtimeAssistantStreamId(payload = {}) {
  const turnId = runtimeTurnId(payload);
  return turnId ? `assistant-stream-${turnId}` : '';
}

function runtimeAssistantFallbackId(payload = {}) {
  return (
    runtimeAssistantStreamId(payload) ||
    `assistant-stream-${normalizeThreadId(runtimeThreadIdentifier(payload)) || Date.now()}`
  );
}

function isRuntimeAssistantItem(item) {
  const type = normalizeString(item?.type || item?.kind || item?.role).toLowerCase();
  return (
    type.includes('agentmessage') ||
    type.includes('agent_message') ||
    type.includes('assistant') ||
    type === 'final_answer'
  );
}

function runtimeAssistantCompletion(payload = {}) {
  const item = payload.item && typeof payload.item === 'object' ? payload.item : {};
  const hasItem = Object.keys(item).length > 0;
  if (hasItem && !isRuntimeAssistantItem(item)) return null;

  const text = extractText(item.text || item.content || payload.text || payload.content || payload.result);
  if (!text) return null;

  const explicitId = normalizeString(item.id || payload.messageId || payload.message_id);
  return {
    item: {
      id: explicitId || `assistant-final-${runtimeTurnId(payload) || Date.now()}`,
      role: 'assistant',
      kind: 'assistant',
      text,
      time: normalizeString(payload.timestamp || item.ts || item.createdAt || item.created_at) || new Date().toISOString(),
      done: true,
      optimistic: false,
      runtime: true,
    },
    explicitId: Boolean(explicitId),
    streamId: runtimeAssistantStreamId(payload),
  };
}

function isAssistantMessageDeltaEvent(eventName, payload = {}) {
  if (eventName === 'item/agentmessage/delta' || eventName === 'item/agent_message/delta') return true;
  if (eventName === 'message.delta' || eventName === 'agent_message_delta' || eventName === 'assistant:message_delta') return true;
  if (eventName !== 'turn/output/delta' && eventName !== 'turn/outputdelta') return false;
  const stream = normalizeString(payload.stream).toLowerCase();
  return !stream || stream === 'message' || stream === 'assistant' || stream === 'agentmessage' || stream === 'agent_message';
}

function appendAssistantDeltaText(existingText, deltaText) {
  const base = (existingText || '').toString();
  const incoming = (deltaText || '').toString();
  if (!incoming) return base;
  if (!base) return incoming;
  if (base.endsWith(incoming)) return base;
  if (incoming.endsWith(base)) return incoming;
  const maxOverlap = Math.min(base.length, incoming.length, 32);
  for (let overlap = maxOverlap; overlap > 0; overlap -= 1) {
    if (base.slice(-overlap) === incoming.slice(0, overlap)) {
      return base + incoming.slice(overlap);
    }
  }
  return base + incoming;
}

function assistantDeltaBufferKey(threadId, itemId) {
  return `${threadId}\u0000${itemId}`;
}

function mergeRuntimeAssistantCompletion(existingItems = [], completion) {
  if (!completion?.item) return existingItems;
  const finalItem = completion.item;

  let lastUserIndex = -1;
  for (let index = existingItems.length - 1; index >= 0; index -= 1) {
    if (existingItems[index]?.role === 'user') {
      lastUserIndex = index;
      break;
    }
  }

  const turnAssistantItems = existingItems.slice(lastUserIndex + 1).filter(
    (item) => item?.role === 'assistant' && (item?.kind === 'assistant' || !item?.kind)
  );
  const accumulatedText = turnAssistantItems.map((item) => (item.text || '').toString()).join('');
  const compactAccumulated = compactTimelineText(accumulatedText);
  const compactFinal = compactTimelineText(finalItem.text);

  if (compactAccumulated && compactAccumulated === compactFinal) {
    if (turnAssistantItems.length === 1) {
      const singleItem = turnAssistantItems[0];
      const isStreamItem = singleItem.id === completion.streamId;
      const preferred = isStreamItem ? finalItem : preferredAssistantTimelineItem(singleItem, finalItem);
      return existingItems.map((item) => {
        if (item.id === singleItem.id) {
          return { ...preferred, done: true };
        }
        return item;
      });
    }
    return existingItems.map((item, index) => {
      if (index > lastUserIndex && item.role === 'assistant' && item.done === false) {
        return { ...item, done: true };
      }
      return item;
    });
  }

  const dropIds = new Set([finalItem.id, completion.streamId].filter(Boolean));
  const withoutReplaced = existingItems.filter((item) => !dropIds.has(item.id));
  lastUserIndex = -1;
  for (let index = withoutReplaced.length - 1; index >= 0; index -= 1) {
    if (withoutReplaced[index]?.role === 'user') {
      lastUserIndex = index;
      break;
    }
  }
  const duplicateIndex = withoutReplaced.findIndex((item, index) => (
    item.role === 'assistant' &&
    item.done !== false &&
    (
      sameTimelineContent(item, finalItem) ||
      (index > lastUserIndex && (
        sameTimelineContentCompact(item, finalItem) ||
        sameRuntimeAssistantContentLoose(item, finalItem) ||
        (item.runtime && finalItem.runtime && sameTimelineContentPrefix(item, finalItem))
      ))
    )
  ));
  if (duplicateIndex >= 0 && (
    !completion.explicitId ||
    withoutReplaced[duplicateIndex].runtime ||
    duplicateIndex > lastUserIndex
  )) {
    return dedupeAssistantTimelineItems(sortTimelineChronologically(withoutReplaced.map((item, index) => (
      index === duplicateIndex ? preferredAssistantTimelineItem(item, finalItem) : item
    ))));
  }
  return dedupeAssistantTimelineItems([...withoutReplaced, finalItem]);
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

function normalizeAttachment(value) {
  if (typeof value === 'string') {
    return normalizeFileAttachment(value);
  }
  if (!value || typeof value !== 'object') return null;
  const path = normalizeString(value.path || value.url);
  if (!path) return null;
  const kind = normalizeString(value.kind) || (isImagePath(path) ? 'image' : 'file');
  const previewUrl = normalizeString(value.previewUrl || value.url) || (kind === 'image' && isImagePath(path) ? `file://${path}` : '');
  return {
    path,
    name: normalizeString(value.name) || basename(path),
    kind,
    previewUrl,
  };
}

function cloneComposerAttachments(attachments) {
  return (
    Array.isArray(attachments)
    ? attachments.map((item) => ({ ...item })).map(normalizeAttachment).filter(Boolean)
    : []
  );
}

function normalizeComposerDraftSnapshot(value = {}) {
  return {
    draft: (value.draft || '').toString(),
    attachments: cloneComposerAttachments(value.attachments),
  };
}

function isEmptyComposerDraftSnapshot(value = {}) {
  const draft = normalizeComposerDraftSnapshot(value);
  return !draft.draft && draft.attachments.length === 0;
}

function composerScopeCwd(state = {}) {
  const activeProject = normalizePath(state.activeProject);
  if (activeProject && activeProject !== '.') return activeProject;
  return normalizePath(state.cwd);
}

function composerDraftKey(state = {}, threadId = state.activeThreadId) {
  const cwd = composerScopeCwd(state);
  const id = normalizeThreadId(threadId);
  return `${cwd || '__missing_cwd__'}::${id ? `thread:${id}` : 'new:chat'}`;
}

function attachmentKey(value) {
  const attachment = normalizeAttachment(value);
  return attachment ? (attachment.path || attachment.previewUrl) : '';
}

function appendUniqueAttachments(current, incoming) {
  const next = [...current];
  const seen = new Set(next.map(attachmentKey).filter(Boolean));
  for (const item of incoming || []) {
    const attachment = normalizeAttachment(item);
    const key = attachmentKey(attachment);
    if (!attachment || !key || seen.has(key)) continue;
    seen.add(key);
    next.push(attachment);
  }
  return next;
}

function fileListOf(value) {
  return Array.from(value || []).filter(Boolean);
}

function droppedFilePath(file) {
  return normalizeString(file?.path);
}

function fileLooksImage(file) {
  return normalizeString(file?.type).toLowerCase().startsWith('image/') || isImagePath(file?.name);
}

function blobToDataURL(blob) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(normalizeString(reader.result));
    reader.onerror = () => reject(reader.error || new Error('read image blob failed'));
    reader.readAsDataURL(blob);
  });
}

async function imageFileAttachment(file, index, fallbackPrefix) {
  const dataUrl = await blobToDataURL(file);
  const base64 = dataUrl.split(',')[1] || '';
  if (!base64) throw new Error('image attachment data is empty');
  const path = normalizeString(await saveClipboardImage(base64));
  if (!path) throw new Error('clipboard image save returned empty path');
  return {
    path,
    name: normalizeString(file?.name) || `${fallbackPrefix}-${Date.now()}-${index}.png`,
    kind: 'image',
    previewUrl: dataUrl,
  };
}

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
    optimisticSendDraftState,
    promotedDraftThreadState,
    resolveLaunchPreferences,
    rollbackSendDraftState,
    startNewDraftThread,
    startTurnWithStoppedThreadRecovery,
  },
};

function attachmentToInputItem(item) {
  const attachment = normalizeAttachment(item);
  if (!attachment) return null;
  if (attachment.kind === 'image') {
    const payload = { type: 'localImage', path: attachment.path };
    if (attachment.previewUrl.toLowerCase().startsWith('data:image/')) {
      payload.url = attachment.previewUrl;
    }
    return payload;
  }
  return { type: 'mention', name: attachment.name || basename(attachment.path), path: attachment.path };
}

function buildTurnInput(text, attachments) {
  const items = [];
  const message = normalizeString(text);
  if (message) items.push({ type: 'text', text: message });
  for (const attachment of attachments || []) {
    const item = attachmentToInputItem(attachment);
    if (item) items.push(item);
  }
  return items;
}

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

function forkSourceTitle(thread, threadId) {
  const name = normalizeString(thread?.name);
  if (name) return `继承自会话：${name}`;
  const id = normalizeThreadId(threadId || thread?.id);
  return id ? `继承自会话：${id}` : '继承自前一个对话';
}

function forkSourceThread(state, threadId) {
  const id = normalizeThreadId(threadId);
  if (!id) return null;
  return state.threads.find((thread) => threadMatchesIdentifier(thread, id)) || null;
}

function normalizeForkSharedFiles(response) {
  const files = Array.isArray(response?.files) ? response.files : [];
  const seen = new Set();
  const normalized = [];
  for (const file of files) {
    const path = normalizeString(typeof file === 'string' ? file : file?.path);
    if (!path || seen.has(path)) continue;
    seen.add(path);
    normalized.push({ path });
  }
  return normalized;
}

function cachedForkSharedFiles(state) {
  const cwd = normalizePath(state.activeProject || state.cwd);
  const cache = cwd ? state.sharedFilesPageCacheByCwd?.[cwd] : null;
  return normalizeForkSharedFiles(cache || {});
}

function initialForkSharedFilePaths(state, availableSharedFiles = [], seedPath = '') {
  const available = new Set(availableSharedFiles.map((file) => file.path));
  const selected = [];
  const add = (path, requireAvailable) => {
    const value = normalizeString(path);
    if (!value || selected.includes(value)) return;
    if (requireAvailable && !available.has(value)) return;
    selected.push(value);
  };
  (state.attachments || []).forEach((item) => add(item?.path, true));
  add(seedPath, false);
  return selected;
}

function mergeForkSharedFilesWithSelected(availableSharedFiles = [], selectedPaths = []) {
  const seen = new Set();
  const merged = [];
  for (const file of availableSharedFiles) {
    const path = normalizeString(file?.path);
    if (!path || seen.has(path)) continue;
    seen.add(path);
    merged.push({ path });
  }
  for (const path of selectedPaths) {
    const value = normalizeString(path);
    if (!value || seen.has(value)) continue;
    seen.add(value);
    merged.push({ path: value });
  }
  return merged;
}

async function loadForkSharedFiles(paths = []) {
  const selected = paths.map(normalizeString).filter(Boolean);
  if (selected.length === 0) return [];
  return Promise.all(selected.map(async (path) => {
    const detail = await readSharedFile({ path });
    if (!detail || typeof detail !== 'object' || Array.isArray(detail)) {
      throw new Error(`shared file ${path} returned empty response`);
    }
    return {
      path: normalizeString(detail.path) || path,
      content: (detail.content || '').toString(),
    };
  }));
}

function addForkThreadState(state, threadId, identity, launchPreferences, name, kickoffText) {
  const provider = launchPreferences.modelProvider || launchPreferences.provider || state.provider || DEFAULT_PROVIDER;
  return {
    activePage: 'chat',
    activeThreadId: threadId,
    provider,
    activityThreadAtById: {
      ...state.activityThreadAtById,
      [threadId]: threadActivityTimestamp(),
    },
    forkDraft: emptyForkDraft(),
    actionNotice: actionNotice('已创建继承对话', 'success'),
    threads: [
      {
        id: threadId,
        agentId: identity.agentId,
        providerThreadId: identity.providerThreadId,
        sessionId: identity.sessionId,
        name,
        provider,
        status: '工作中',
      },
      ...state.threads.filter((item) => !threadMatchesIdentifier(item, threadId)),
    ],
    timelinesByThread: {
      ...state.timelinesByThread,
      [threadId]: [{
        id: `fork-kickoff-${Date.now()}`,
        role: 'user',
        text: kickoffText,
        time: new Date().toISOString(),
        done: true,
        optimistic: true,
      }],
    },
  };
}

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
  startThread: (payload) => startThread(payload),
  startTurn: (payload) => startTurn(payload),
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
  const thread = await startThread({
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
  return {
    activeThreadId,
    provider,
    activityThreadAtById,
    timelinesByThread,
    threadTimelineReadyByThread: {
      ...state.threadTimelineReadyByThread,
      [started.threadId]: true,
    },
    threads: [
      {
        id: started.threadId,
        agentId: started.identity.agentId,
        providerThreadId: started.identity.providerThreadId,
        sessionId: started.identity.sessionId,
        name: sendDraftThreadName(request.text),
        provider,
        status: '工作中',
      },
      ...state.threads.filter((item) => item.id !== started.threadId),
    ],
  };
}

function rollbackSendDraftState(state, request, error) {
  const timelinesByThread = { ...state.timelinesByThread };
  const activeTimeline = timelinesByThread[state.activeThreadId] || [];
  timelinesByThread[state.activeThreadId] = activeTimeline.filter((item) => item.id !== request.optimisticItem.id);
  if (!request.previousThreadId) delete timelinesByThread[request.provisionalThreadId];
  const activeThreadId = state.activeThreadId === request.provisionalThreadId ? request.previousActiveThreadId : state.activeThreadId;
  return {
    sending: false,
    draft: request.previousDraft,
    attachments: request.previousAttachments,
    activeThreadId,
    timelinesByThread,
    error: error.message,
    actionNotice: actionNotice(`发送失败：${error.message}`, 'error'),
  };
}

function createdThreadIdForSendRollback(state, request, threadId) {
  if (request.previousThreadId || !threadId) return '';
  return backendThreadIdForState(state, threadId);
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

async function startTurnWithStoppedThreadRecovery(params) {
  try {
    return await startTurn(params);
  } catch (error) {
    if (!isStoppedThreadTurnStartError(error)) throw error;
    await recoverThread({ cwd: params.cwd, threadId: params.threadId });
    return startTurn(params);
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
  const provider = normalizeProviderName(state.provider) || DEFAULT_PROVIDER;
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

function bridgePatchRuntime(payload) {
  return payload.agentRuntime || payload.agent_runtime || {};
}

function bridgePatchRawThread(payload) {
  return payload.thread && typeof payload.thread === 'object' ? payload.thread : {};
}

function bridgePatchProvider(rawRuntime, rawThread) {
  return firstThreadCopyText(
    rawRuntime.provider,
    rawRuntime.modelProvider,
    rawRuntime.model_provider,
    rawThread.provider,
    rawThread.modelProvider,
    rawThread.model_provider,
  );
}

function bridgePatchStatusText(payload, rawThread) {
  return firstThreadCopyText(payload.statusHeader, payload.status, rawThread.state, rawThread.status);
}

function bridgePatchedThread({ payload, threadId, rawRuntime, rawThread, patchProvider, statusText }) {
  return normalizeThread({
    ...rawThread,
    threadId,
    agentId: firstThreadCopyText(rawRuntime.agentId, rawRuntime.agent_id, rawThread.agentId, rawThread.agent_id),
    providerThreadId: firstThreadCopyText(rawRuntime.providerThreadId, rawRuntime.provider_thread_id, rawThread.providerThreadId, rawThread.provider_thread_id),
    provider: patchProvider,
    lastMessage: firstThreadCopyText(rawRuntime.lastMessage, rawRuntime.last_message, payload.statusDetails, payload.status_details, rawThread.lastMessage),
    status: statusText || rawThread.status,
  });
}

function bridgePatchData(method, payload, threadId) {
  const timelineItems = payload.timelineItems || payload.timeline_items;
  const rawRuntime = bridgePatchRuntime(payload);
  const rawThread = bridgePatchRawThread(payload);
  const patchProvider = bridgePatchProvider(rawRuntime, rawThread);
  const statusText = bridgePatchStatusText(payload, rawThread);
  const patchedThread = bridgePatchedThread({ payload, threadId, rawRuntime, rawThread, patchProvider, statusText });
  return {
    method,
    payload,
    threadId,
    timelineItems,
    runtimeResultEntries: runtimeResultEntriesFromTimelineItems(timelineItems, threadId),
    tokenUsage: normalizeTokenUsage(payload.tokenUsage || payload.token_usage),
    activityStats: normalizeActivityStats(payload.activityStats || payload.activity_stats),
    diffText: typeof payload.diffText === 'string' ? payload.diffText : payload.diff_text,
    rawRuntime,
    patchProvider,
    statusText,
    patchedThread,
  };
}

function bridgePatchTimeline(state, patch) {
  const timelinesByThread = { ...state.timelinesByThread };
  if (Array.isArray(patch.timelineItems)) {
    const visibleItems = patch.timelineItems.map(normalizeTimelineItem).filter(isVisibleTimelineItem);
    timelinesByThread[patch.threadId] = mergeTimelineItems(
      timelinesByThread[patch.threadId] || [],
      visibleItems,
      { preserveExistingVisible: true },
    );
  }
  return timelinesByThread;
}

function bridgePatchHasVisibleTimelineItems(patch) {
  if (!Array.isArray(patch.timelineItems)) return false;
  return patch.timelineItems
    .map(normalizeTimelineItem)
    .some(isVisibleTimelineItem);
}

function bridgePatchActiveTurn(state, patch) {
  const activeTurnByThread = { ...state.activeTurnByThread };
  const patchActiveTurn = activeTurnPayload(patch.payload);
  if (patchActiveTurn !== undefined) {
    delete activeTurnByThread[patch.threadId];
    const normalizedActiveTurn = normalizeTurnSummary(patchActiveTurn);
    if (isInterruptibleTurnSummary(normalizedActiveTurn)) activeTurnByThread[patch.threadId] = { ...normalizedActiveTurn, threadId: patch.threadId };
    return activeTurnByThread;
  }
  if (patch.payload.interruptible === false || patch.statusText === 'idle' || patch.statusText === 'interrupted' || patch.statusText === 'completed') {
    delete activeTurnByThread[patch.threadId];
  }
  return activeTurnByThread;
}

function bridgePatchThreadName(existingThread, patchedThread) {
  if (patchedThread.name !== '新对话') return patchedThread.name;
  return existingThread?.name || patchedThread.name;
}

function shouldPromoteBridgePatchThread(existingThread, patch) {
  return !existingThread || patch.promoteForActivity;
}

function bridgePatchThreads(state, patch) {
  const existingThread = state.threads.find((thread) => threadMatchesIdentifier(thread, patch.threadId));
  if (!patch.patchedThread.id) return state.threads;
  let archived = Boolean(existingThread?.archived || patch.patchedThread.archived);
  const recentOverride =
    state.lastArchivedStatesByThread?.[patch.threadId] ||
    (existingThread?.id && state.lastArchivedStatesByThread?.[existingThread.id]) ||
    (existingThread?.agentId && state.lastArchivedStatesByThread?.[existingThread.agentId]);
  const isLoading = Boolean(
    state.threadArchiveLoadingByThread?.[patch.threadId] ||
    (existingThread?.id && state.threadArchiveLoadingByThread?.[existingThread.id]) ||
    (existingThread?.agentId && state.threadArchiveLoadingByThread?.[existingThread.agentId])
  );
  if (isLoading && recentOverride) {
    archived = recentOverride.archived;
  } else if (recentOverride && Date.now() - recentOverride.timestamp < 8000) {
    archived = recentOverride.archived;
  }
  const mergedThread = {
    ...(existingThread || {}),
    ...patch.patchedThread,
    name: bridgePatchThreadName(existingThread, patch.patchedThread),
    provider: patch.patchProvider || existingThread?.provider || patch.patchedThread.provider,
    status: patch.statusText || patch.patchedThread.status || existingThread?.status || '等待指示',
    pinned: Boolean(existingThread?.pinned || patch.patchedThread.pinned),
    pinnedAt: existingThread?.pinnedAt || patch.patchedThread.pinnedAt || 0,
    archived,
  };
  if (shouldPromoteBridgePatchThread(existingThread, patch)) {
    return [
      mergedThread,
      ...state.threads.filter((thread) => !threadMatchesIdentifier(thread, patch.threadId)),
    ];
  }
  return state.threads.map((thread) => (threadMatchesIdentifier(thread, patch.threadId) ? mergedThread : thread));
}

function bridgePatchActivityThreadAt(state, patch) {
  if (!patch.promoteForActivity) return state.activityThreadAtById;
  return {
    ...state.activityThreadAtById,
    [patch.threadId]: threadActivityTimestamp(),
  };
}

function bridgePatchStatuses(state, patch) {
  return {
    ...state.statuses,
    [patch.threadId]: cleanObject({
      status: patch.payload.status,
      statusHeader: patch.payload.statusHeader,
      statusDetails: patch.payload.statusDetails || patch.payload.status_details,
      interruptible: patch.payload.interruptible,
      activityStats: patch.activityStats,
      agentRuntime: patch.rawRuntime,
    }),
  };
}

function bridgePatchActivityEntries(state, patch) {
  return [{
    id: `${patch.method}-${Date.now()}`,
    method: patch.method,
    threadId: patch.threadId,
    timestamp: new Date().toISOString(),
  }, ...state.activityEntries].slice(0, 120);
}

function bridgePatchState(state, patch) {
  const tokenUsageByThread = { ...state.tokenUsageByThread };
  if (patch.tokenUsage) tokenUsageByThread[patch.threadId] = patch.tokenUsage;
  const activityStatsByThread = { ...state.activityStatsByThread };
  if (patch.activityStats) activityStatsByThread[patch.threadId] = patch.activityStats;
  const diffTextByThread = { ...state.diffTextByThread };
  const threadDiffReadyByThread = { ...state.threadDiffReadyByThread };
  if (typeof patch.diffText === 'string') {
    diffTextByThread[patch.threadId] = patch.diffText;
    threadDiffReadyByThread[patch.threadId] = true;
  }
  return {
    threads: bridgePatchThreads(state, patch),
    activityThreadAtById: bridgePatchActivityThreadAt(state, patch),
    timelinesByThread: bridgePatchTimeline(state, patch),
    threadTimelineReadyByThread: bridgePatchHasVisibleTimelineItems(patch)
      ? { ...state.threadTimelineReadyByThread, [patch.threadId]: true }
      : state.threadTimelineReadyByThread,
    tokenUsageByThread,
    activityStatsByThread,
    diffTextByThread,
    threadDiffReadyByThread,
    runtimeResultEntries: mergeRuntimeResultEntries(state.runtimeResultEntries, patch.runtimeResultEntries),
    activeTurnByThread: bridgePatchActiveTurn(state, patch),
    statuses: bridgePatchStatuses(state, patch),
    activityEntries: bridgePatchActivityEntries(state, patch),
  };
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
  smoothStreaming: false,
};

function stateWithPatch(patch = {}) {
  return {
    ...baseState,
    ...patch,
  };
}

function createClientStoreRuntime(set, get) {
  const runtime = {
    set,
    get,
    bridgeUnsubscribe: null,
    sequencesByThread: new Map(),
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
  attachWarningRuntime(runtime);
  attachLogRuntime(runtime);
  attachScopeRuntime(runtime);
  attachProviderRuntime(runtime);
  attachSidebarRuntime(runtime);
  attachThreadMessagesRuntime(runtime);
  attachNotificationRuntime(runtime);
  attachBridgeIdentityRuntime(runtime);
  attachAssistantEventRuntime(runtime);
  attachBridgePatchRuntime(runtime);
  attachBridgeEventRuntime(runtime);
  attachActiveThreadRpcRuntime(runtime);
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

  const restoreComposerDraft = (state, threadId) => {
    const key = composerDraftKey(state, threadId);
    return normalizeComposerDraftSnapshot(composerDrafts.get(key));
  };

  const clearComposerDraft = (state, threadId) => {
    composerDrafts.delete(composerDraftKey(state, threadId));
  };


  Object.assign(runtime, { saveActiveComposerDraft, restoreComposerDraft, clearComposerDraft });
}

function attachWarningRuntime(runtime) {
  const { set } = runtime;

  const warningErrorKey = (fields = {}) => {
    const error = fields?.error;
    if (typeof error === 'string') return error;
    if (error && typeof error === 'object') {
      return normalizeString(error.message || error.code || error.data || JSON.stringify(error));
    }
    return '';
  };

  const warningSignature = (level, event, threadId, fields = {}) => [
    level,
    event,
    threadId,
    normalizeString(fields.method || fields.action || fields.rpcMethod || fields.rpc_method),
    warningErrorKey(fields),
  ].join('|');

  const emitWarningTrace = (level, event, threadId, fields = {}) => {
    const method = normalizeString(event);
    if (!method) return;
    const metadata = cleanObject({
      component: warningTraceComponent(method),
      req_id: fields.req_id ?? fields.reqId,
    });
    emitFrontendTraceEvent(cleanObject({
      phase: 'frontend.warning',
      method,
      trace_id: normalizeString(fields.trace_id || fields.traceId),
      span_id: normalizeString(fields.span_id || fields.spanId),
      parent_span_id: normalizeString(fields.parent_span_id || fields.parentSpanId),
      thread_id: threadId,
      agent_id: normalizeString(fields.agent_id || fields.agentId),
      turn_id: normalizeString(fields.turn_id || fields.turnId),
      call_id: normalizeString(fields.call_id || fields.callId),
      status: warningTraceStatus(level, method),
      error: warningErrorKey(fields),
      metadata: Object.keys(metadata).length > 0 ? metadata : undefined,
    }));
  };

  const addWarning = (level, event, fields = {}) => {
    if (level !== 'warn' && level !== 'error') return;
    const threadId = normalizeThreadId(runtimeThreadIdentifier(fields));
    const signature = warningSignature(level, event, threadId, fields);
    const entry = {
      id: `${event}-${Date.now()}-${Math.random().toString(16).slice(2)}`,
      timestamp: new Date().toISOString(),
      level,
      event,
      threadId,
      fields,
      occurrenceCount: 1,
      signature,
    };
    set((state) => ({
      warningEntries: mergeWarningEntries(state.warningEntries, entry, fields),
    }));
    emitWarningTrace(level, event, threadId, fields);
  };

  Object.assign(runtime, { addWarning });
}

function warningTraceComponent(event) {
  return normalizeString(event).split(/[./]/).filter(Boolean)[0] || '';
}

function warningTraceStatus(level, event) {
  const method = normalizeString(event).toLowerCase();
  if (level === 'error' || method.endsWith('.failed') || method.endsWith('/failed')) return 'error';
  return 'ok';
}

function mergeWarningEntries(warningEntries, entry, fields) {
  const existingIndex = warningEntries.findIndex((item) => item.signature === entry.signature);
  if (existingIndex < 0) return [entry, ...warningEntries].slice(0, MAX_WARNING_ENTRIES);
  const existing = warningEntries[existingIndex];
  const updated = {
    ...existing,
    id: entry.id,
    timestamp: entry.timestamp,
    fields,
    occurrenceCount: (Number(existing.occurrenceCount) || 1) + 1,
  };
  return [
    updated,
    ...warningEntries.slice(0, existingIndex),
    ...warningEntries.slice(existingIndex + 1),
  ].slice(0, MAX_WARNING_ENTRIES);
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
  const { sequencesByThread, sidebarSnapshotsByCwd, threadMessageGenerations, threadSyncGenerations } = runtime;

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

  const clearChatSurfaceForCwdSwitch = (cwdValue = '') => {
    const cwd = normalizePath(cwdValue);
    sequencesByThread.clear();
    threadMessageGenerations.clear();
    threadSyncGenerations.clear();
    set({
      activeThreadId: '',
      threads: [],
      pinnedThreadAtById: {},
      statuses: {},
      activeTurnByThread: {},
      threadConfigByThread: {},
      threadConfigLoadingByThread: {},
      threadConfigFailedByThread: {},
      threadStateLoadingByThread: {},
      threadArchiveLoadingByThread: {},
      pendingActiveThreadId: '',
      timelinesByThread: {},
      threadTimelineReadyByThread: {},
      threadMessagePaginationByThread: {},
      tokenUsageByThread: {},
      activityStatsByThread: {},
      diffTextByThread: {},
      threadDiffReadyByThread: {},
      runtimeResultEntries: [],
      draft: '',
      attachments: [],
      chatSurfaceLoadingCwd: cwd,
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
    const provider = normalizeProviderName(providerValue || get().provider) || DEFAULT_PROVIDER;
    const providerScope = providerPreferenceScope(provider);
    const modelKey = providerPreferenceKey(providerScope, 'model');
    const effortKey = providerPreferenceKey(providerScope, 'effort');
    const codexModelProviderKey = providerPreferenceKey('codex', 'codexModelProvider');
    const [model, effort, codexModelProvider] = await Promise.all([
      getPreference({ cwd, key: modelKey }),
      getPreference({ cwd, key: effortKey }),
      providerScope === 'codex'
        ? getPreference({ cwd, key: codexModelProviderKey })
        : Promise.resolve(''),
    ]);
    const providerConfig = normalizeProviderRuntimeConfig({
      model: normalizeProviderConfigValue(model),
      effort: normalizeProviderConfigValue(effort),
      codexModelProvider: providerScope === 'codex'
        ? normalizeCodexIdentityValue(codexModelProvider)
        : '',
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
      clearChatSurfaceForCwdSwitch(cwd);
      if (cachedSidebar) {
        applySnapshot(cachedSidebar, { autoSelectThread: false, scopeCwd: cwd });
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

  const refreshChatSurfaceForCwdInBackground = (cwdValue) => {
    refreshSidebarSnapshotForCwdInBackground(cwdValue, { clearSurface: true });
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

function messagePageParams(id, before) {
  if (before) return { threadId: id, limit: THREAD_MESSAGES_PAGE_SIZE, before };
  return { threadId: id, limit: THREAD_MESSAGES_PAGE_SIZE };
}

function markThreadMessagesReady(set, id) {
  set((state) => {
    const timelineWasReady = Boolean(state.threadTimelineReadyByThread?.[id]);
    const currentItems = state.timelinesByThread[id] || [];
    const hasActiveItems = currentItems.some((item) => item.done === false || item.optimistic);
    const preserve = timelineWasReady || hasActiveItems;
    return {
      timelinesByThread: {
        ...state.timelinesByThread,
        [id]: mergeTimelineItems(state.timelinesByThread[id] || [], [], { preserveExistingVisible: preserve }),
      },
      threadTimelineReadyByThread: {
        ...state.threadTimelineReadyByThread,
        [id]: true,
      },
    };
  });
}

async function fetchThreadMessagePage(id, before = '') {
  const startedAt = Date.now();
  const res = await getThreadMessages(messagePageParams(id, before));
  const page = Array.isArray(res?.messages) ? res.messages : [];
  return {
    messages: page,
    items: normalizeThreadMessageItems(page),
    meta: normalizeThreadMessagesPageMeta(res, page),
    durationMs: Date.now() - startedAt,
  };
}

// IMAGE_PLACEHOLDER_RE is defined globally at the top of the file

function extractHistoryMetadata(message) {
  const meta = message.metadata || message.meta;
  if (!meta || typeof meta !== 'object') return null;
  try {
    return typeof meta === 'string' ? JSON.parse(meta) : meta;
  } catch {
    return null;
  }
}

function buildHistoryMessageAttachments(message) {
  const meta = extractHistoryMetadata(message);
  const inputs = Array.isArray(meta?.input) ? meta.input : [];
  const attachments = [];
  for (const item of inputs) {
    if (!item || typeof item !== 'object') continue;
    if (item.type !== 'image' && item.type !== 'localImage') continue;
    const rawPath = normalizeString(item.path || item.url || item.source);
    if (!rawPath) continue;
    // clipboard 路径转为 Wails HTTP 路由，本地路径保留原样
    let previewUrl = rawPath;
    if (rawPath.startsWith('/') && !rawPath.startsWith('/clipboard/')) {
      const base = rawPath.split('/').pop() || '';
      if (/\.(png|jpe?g|gif|webp|bmp|svg)$/i.test(base)) {
        previewUrl = `/clipboard/${base}`;
      }
    }
    attachments.push({
      kind: 'image',
      name: normalizeString(item.name) || rawPath.split('/').pop() || rawPath,
      path: rawPath,
      previewUrl,
    });
  }
  return attachments;
}

function stripHistoryImagePlaceholders(text, hasAttachments) {
  if (!hasAttachments || !text) return text;
  return text.replace(IMAGE_PLACEHOLDER_RE, '').trim();
}

function normalizeThreadMessageItems(allMessages) {
  return sortTimelineChronologically(allMessages.map((message) => {
    const rawText = message.content || message.text || message.message || message.delta || message.output || message.result || message.answer || message.response;
    const historyAttachments = message.role === 'user' ? buildHistoryMessageAttachments(message) : [];
    const text = stripHistoryImagePlaceholders(rawText, historyAttachments.length > 0);
    const normalized = normalizeTimelineItem({
      id: message.id || message.messageId || message.message_id,
      role: message.role,
      kind: message.kind || message.type || message.eventType || message.event_type,
      text,
      createdAt: message.createdAt || message.created_at,
      completedAt: message.completedAt || message.completed_at || message.finishedAt || message.finished_at,
    });
    // normalizeTimelineItem 不透传 attachments，手动合并
    return historyAttachments.length > 0 ? { ...normalized, attachments: historyAttachments } : normalized;
  }).filter(isVisibleTimelineItem));
}

function dagNodeFallbackText(value) {
  const text = extractText(value);
  if (text) return text;
  if (!value || typeof value !== 'object') return '';
  try {
    return normalizeString(JSON.stringify(value, null, 2));
  }
  catch {
    return '';
  }
}

function dagNodeFallbackPrompt(node) {
  const config = objectRecord(node?.config || node?.raw?.config);
  const exec = objectRecord(config.exec);
  const verifier = objectRecord(exec.verifier);
  const prompt = firstValueFromSources([
    [config, ['first_turn', 'firstTurn', 'prompt', 'instructions', 'message', 'text', 'input']],
    [exec, ['first_turn', 'firstTurn', 'prompt', 'instructions', 'message', 'text', 'input']],
    [verifier, ['first_turn', 'firstTurn', 'prompt', 'instructions', 'message', 'text', 'input']],
  ]);
  return dagNodeFallbackText(prompt);
}

function dagNodeFallbackResult(node) {
  const result = firstValueFromSources([
    [node, ['result', 'output', 'summary', 'message', 'text', 'content']],
    [objectRecord(node?.raw), ['result', 'output', 'summary', 'message', 'text', 'content']],
  ]);
  return dagNodeFallbackText(result);
}

function dagNodeHistoryFallbackItems(threadId, dagNode) {
  const node = objectRecord(dagNode);
  if (Object.keys(node).length === 0) return [];
  const nodeKey = normalizeString(node.nodeKey || node.node_key || node.id || threadId) || 'dag-node';
  const title = normalizeString(node.title || node.name || nodeKey);
  const startedAt = normalizeString(node.startedAt || node.started_at || node.createdAt || node.created_at) || new Date().toISOString();
  const finishedAt = normalizeString(node.finishedAt || node.finished_at) || startedAt;
  const prompt = dagNodeFallbackPrompt(node);
  const result = dagNodeFallbackResult(node);
  const items = [];
  if (prompt) {
    items.push(normalizeTimelineItem({
      id: `dag-node:${nodeKey}:prompt`,
      role: 'user',
      text: prompt,
      createdAt: startedAt,
      done: true,
    }));
  }
  if (result) {
    items.push(normalizeTimelineItem({
      id: `dag-node:${nodeKey}:result`,
      role: 'assistant',
      text: result,
      title: title ? `DAG 节点结果：${title}` : 'DAG 节点结果',
      createdAt: finishedAt,
      done: true,
    }));
  }
  return sortTimelineChronologically(items.filter(isVisibleTimelineItem));
}

function threadOpenHistoryFallbackItems(threadId, options = {}) {
  if (normalizeString(options?.source) !== 'dag-node') return [];
  return dagNodeHistoryFallbackItems(threadId, options?.dagNode);
}

function applyThreadHistoryFallback(set, id, fallbackItems) {
  const items = Array.isArray(fallbackItems) ? fallbackItems.filter(isVisibleTimelineItem) : [];
  if (items.length === 0) return false;
  set((state) => {
    const existing = state.timelinesByThread[id] || [];
    const nextTimeline = existing.some(isVisibleTimelineItem)
      ? existing
      : mergeTimelineItems(existing, items, { preserveExistingVisible: true });
    return {
      timelinesByThread: {
        ...state.timelinesByThread,
        [id]: nextTimeline,
      },
      threadTimelineReadyByThread: {
        ...state.threadTimelineReadyByThread,
        [id]: true,
      },
      ...threadMessagesPaginationPatch(state, id, {
        hasMore: false,
        nextBefore: '',
        loading: false,
      }),
    };
  });
  return true;
}

function emitThreadHistoryInitialPageTrace(id, page, status, error) {
  emitFrontendTraceEvent(cleanObject({
    phase: 'frontend.thread_history.initial_page.load',
    thread_id: id,
    page_size: THREAD_MESSAGES_PAGE_SIZE,
    message_count: page?.messages?.length || 0,
    has_more: Boolean(page?.meta?.hasMore),
    next_before: page?.meta?.nextBefore ? 'present' : '',
    duration_ms: page?.durationMs,
    status,
    error_name: error?.name || '',
  }));
}

function applyThreadMessageItems(set, id, pageItems, pageMeta = {}) {
  set((state) => ({
    timelinesByThread: {
      ...state.timelinesByThread,
      [id]: mergeTimelineItems(state.timelinesByThread[id] || [], pageItems, { preserveExistingVisible: true }),
    },
    threadTimelineReadyByThread: {
      ...state.threadTimelineReadyByThread,
      [id]: true,
    },
    ...threadMessagesPaginationPatch(state, id, {
      hasMore: Boolean(pageMeta.hasMore),
      nextBefore: normalizeString(pageMeta.nextBefore),
      loading: false,
    }),
  }));
}

function attachThreadMessagesRuntime(runtime) {
  const { set, get, addWarning } = runtime;
  const { threadMessageGenerations } = runtime;

  const nextThreadMessageGeneration = (id) => {
    const nextGeneration = (threadMessageGenerations.get(id) || 0) + 1;
    threadMessageGenerations.set(id, nextGeneration);
    return nextGeneration;
  };

  const isCurrentThreadMessageGeneration = (id, generation) => threadMessageGenerations.get(id) === generation;

  const setThreadMessagesLoading = (id, generation, loading) => {
    set((state) => {
      if (!isCurrentThreadMessageGeneration(id, generation)) return {};
      return threadMessagesPaginationPatch(state, id, { loading });
    });
  };

  const loadThreadMessages = async (threadId, options = {}) => {
    const loadOptions = options && typeof options === 'object' ? options : {};
    const id = backendThreadIdForState(get(), threadId, { includeArchived: loadOptions.includeArchived === true });
    if (!id) return;
    const generation = nextThreadMessageGeneration(id);
    setThreadMessagesLoading(id, generation, true);
    try {
      const page = await fetchThreadMessagePage(id);
      emitThreadHistoryInitialPageTrace(id, page, 'ok');
      if (!isCurrentThreadMessageGeneration(id, generation)) return;
      if (page.messages.length === 0) {
        if (!applyThreadHistoryFallback(set, id, loadOptions.historyFallback)) {
          markThreadMessagesReady(set, id);
        }
        setThreadMessagesLoading(id, generation, false);
        return;
      }
      applyThreadMessageItems(set, id, page.items, page.meta);
    }
    catch (error) {
      emitThreadHistoryInitialPageTrace(id, null, 'error', error);
      addWarning('error', 'thread.messages.failed', { threadId: id, error: error.message });
    }
    finally {
      setThreadMessagesLoading(id, generation, false);
    }
  };

  const startThreadMessagesLoad = async (threadId, syncOptions) => {
    if (syncOptions.loadMessages === false) return;
    await loadThreadMessages(threadId, {
      includeArchived: syncOptions.includeArchived === true,
      historyFallback: syncOptions.historyFallback,
    });
  };

  const loadOlderThreadMessages = async (threadId, options = {}) => {
    const loadOptions = options && typeof options === 'object' ? options : {};
    const id = backendThreadIdForState(get(), threadId, { includeArchived: loadOptions.includeArchived === true });
    if (!id) return false;
    const pagination = get().threadMessagePaginationByThread?.[id] || {};
    if (pagination.loading) return false;
    if (!pagination.hasMore) return false;
    const before = normalizeString(pagination.nextBefore);
    if (!before) {
      addWarning('error', 'thread.messages.pagination.missing_cursor', { threadId: id });
      return false;
    }
    const generation = threadMessageGenerations.get(id) || nextThreadMessageGeneration(id);
    setThreadMessagesLoading(id, generation, true);
    try {
      const page = await fetchThreadMessagePage(id, before);
      if (!isCurrentThreadMessageGeneration(id, generation)) return false;
      if (page.messages.length === 0) {
        markThreadMessagesReady(set, id);
        set((state) => threadMessagesPaginationPatch(state, id, {
          hasMore: false,
          nextBefore: '',
          loading: false,
        }));
        return true;
      }
      applyThreadMessageItems(set, id, page.items, page.meta);
      return true;
    }
    catch (error) {
      if (isCurrentThreadMessageGeneration(id, generation)) {
        addWarning('error', 'thread.messages.failed', { threadId: id, error: error.message });
      }
      return false;
    }
    finally {
      setThreadMessagesLoading(id, generation, false);
    }
  };

  Object.assign(runtime, { loadThreadMessages, startThreadMessagesLoad, loadOlderThreadMessages });
}

function attachNotificationRuntime(runtime) {
  const { set, addWarning } = runtime;

  const notifyAction = (message, tone = 'info', fields = {}) => {
    const notice = actionNotice(message, tone);
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

function attachAssistantEventRuntime(runtime) {
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
    const delta = extractText(payload.delta || payload.text || payload.content);
    if (!threadId || !delta) return false;
    const itemId = normalizeString(payload.itemId || payload.item_id || payload.messageId || payload.message_id) ||
      runtimeAssistantFallbackId(payload);
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
    const delta = extractText(payload.delta || payload.text || payload.content);
    if (!threadId || !delta) return false;
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
    const delta = extractText(payload.delta || payload.text || payload.content);
    if (!threadId || !delta) return false;

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
      const timeline = state.timelinesByThread[threadId] || [];
      let mutated = false;
      const nextTimeline = timeline.map((item) => {
        if ((item.role === 'assistant' || item.kind === 'assistant' || item.kind === 'thinking' || item.kind === 'command') && item.done === false) {
          mutated = true;
          return { ...item, done: true };
        }
        return item;
      });
      if (!mutated) return {};
      return {
        timelinesByThread: {
          ...state.timelinesByThread,
          [threadId]: nextTimeline,
        },
      };
    });
    return true;
  };

  const applyAssistantCompletion = (method, payload) => {
    const threadId = bridgeThreadIdForPayload(payload);
    const completion = runtimeAssistantCompletion(payload);
    if (!threadId || !completion) return false;
    set((state) => ({
      timelinesByThread: {
        ...state.timelinesByThread,
        [threadId]: mergeRuntimeAssistantCompletion(state.timelinesByThread[threadId] || [], completion),
      },
      actionNotice: actionNotice('已收到回复', 'success'),
      activityEntries: [{
        id: `${method}-${Date.now()}`,
        method,
        threadId,
        timestamp: new Date().toISOString(),
      }, ...state.activityEntries].slice(0, 120),
    }));
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
  const { set, bridgeThreadIdForPayload } = runtime;
  const { sequencesByThread } = runtime;

  const applyBridgePatch = (method, payload) => {
    const threadId = bridgeThreadIdForPayload(payload);
    if (!threadId) return;

    const sequence = normalizeString(payload.sequence);
    const previousSequence = sequencesByThread.get(threadId) || '';
    if (sequence) {
      if (previousSequence && compareSequence(sequence, previousSequence) <= 0) {
        return;
      }
      sequencesByThread.set(threadId, sequence);
    }

    const patchStart = Date.now();
    try {
      const patch = {
        ...bridgePatchData(method, payload, threadId),
        promoteForActivity: shouldFloatThreadPatch(payload),
      };
      set((state) => bridgePatchState(state, patch));
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
  } = runtime;

  const handleBridgeEvent = (evt) => {
    const method = normalizeString(evt?.method || evt?.type);
    const eventName = method.toLowerCase();
    const payload = evt?.payload || evt?.params || evt?.data || {};
    if (!method) return;

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
    if (
      eventName === 'turn/completed' ||
      eventName === 'turn/interrupted' ||
      eventName === 'agent/stopped' ||
      eventName === 'thread/stopped' ||
      eventName === 'agent/failed'
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
    if (eventName === 'rpc.failed' || eventName.endsWith('/failed') || eventName.endsWith('.failed')) {
      addWarning('error', method, payload);
    }
  };

  Object.assign(runtime, { handleBridgeEvent });
}

function attachActiveThreadRpcRuntime(runtime) {
  const { get, requireCwd, notifyAction, addWarning } = runtime;

  const activeThreadRPC = async (action, rpc) => {
    const currentState = get();
    const interruptTarget = action === 'thread.interrupt' ? activeThreadInterruptTarget(currentState) : null;
    const threadId = interruptTarget?.threadId || backendThreadIdForState(currentState, currentState.activeThreadId);
    if (!threadId) {
      notifyAction('当前没有可操作的后端线程', 'warning');
      return false;
    }
    const actionLabels = {
      'thread.interrupt': '中断当前执行',
      'thread.force_complete': '强制完成当前执行',
      'thread.compact': '压缩上下文',
      'thread.recover': '恢复连接',
    };
    try {
      const cwd = requireCwd(action);
      let payload = { cwd, threadId };
      if (action === 'thread.interrupt') {
        const target = interruptTarget || activeThreadInterruptTarget(currentState);
        if (!target.interruptible) {
          notifyAction('当前没有可中断任务', 'warning', { threadId });
          return false;
        }
        payload = cleanObject({ cwd, threadId: target.threadId, source: 'ui_stop' });
      }
      await rpc(cleanObject(payload));
      notifyAction({
        'thread.interrupt': '已发送中断请求',
        'thread.force_complete': '已发送强制完成请求',
        'thread.compact': '已发送压缩请求',
        'thread.recover': '已发送恢复请求',
      }[action] || '线程操作已提交', 'success', { threadId });
      return true;
    }
    catch (error) {
      const message = error?.message || String(error);
      notifyAction(`${actionLabels[action] || '线程操作'}失败：${message}`, 'error', { threadId });
      addWarning('error', `${action}.failed`, { threadId, error: message });
      return false;
    }
  };


  Object.assign(runtime, { activeThreadRPC });
}

function createNavigationActions(runtime) {
  return {
    setActivePage: (activePage) => runtime.set({ activePage }),
    resolveLaunchPreferences: (cwdArg) => {
      const cwd = normalizePath(cwdArg) || runtime.requireCwd('thread.launchPreferences');
      return resolveLaunchPreferences(cwd);
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
      const provider = normalizeProviderName(runtime.get().provider) || DEFAULT_PROVIDER;
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
      const current = normalizeProviderName(runtime.get().provider) || DEFAULT_PROVIDER;
      const next = current === 'claude' ? 'codex' : 'claude';
      const cwd = runtime.requireCwd('provider.toggle');
      try {
        await setPreference({ cwd, key: PROVIDER_ACTIVE_PREF_KEY, value: next });
        await runtime.loadProviderConfig(cwd, next);
        runtime.set({
          provider: next,
          actionNotice: actionNotice(`已切换为 ${next === 'claude' ? 'Claude' : 'Codex'}`, 'success'),
        });
        return true;
      }
      catch (error) {
        return runtime.notifyRPCFailure('切换 provider', 'provider.toggle.failed', error);
      }
    },


  };
}

function createThreadSelectionActions(runtime) {
  return {
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
          const started = await startNewDraftThread(request, resolveLaunchPreferences);
          threadId = started.threadId;
          runtime.set((state) => ({
            ...promotedDraftThreadState(state, request, started),
            activePage: 'chat',
          }));
        }

        await startTurn({
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
        const createdThreadId = createdThreadIdForSendRollback(runtime.get(), request, threadId);
        runtime.set((state) => ({
          ...rollbackSendDraftState(state, request, error),
          activePage: 'commands',
        }));
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

    refreshActiveThreadStatus: async () => {
      const threadId = backendThreadIdForState(runtime.get(), runtime.get().activeThreadId);
      if (!threadId) return false;
      await runtime.get().syncThreadState(threadId);
      runtime.notifyAction('线程状态已刷新', 'success', { threadId });
      return true;
    },

    respondApproval: async (item, approved) => {
      const requestId = positiveNumberFromFields(item, ['requestId', 'request_id']);
      const decision = Boolean(approved);
      if (requestId <= 0) {
        runtime.notifyAction('当前审批缺少请求编号，无法提交', 'error');
        runtime.addWarning('error', 'timeline.approval.request_id_missing', {
          command: normalizeString(item?.command || item?.title),
        });
        return false;
      }
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
      const payload = buildThreadCopyPayload({ state: runtime.get(), threadId, thread, identity, threadConfig });
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
          threads: state.threads.map((thread) => (thread.id === id ? { ...thread, name: nextName } : thread)),
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
      runtime.set((state) => ({
        activeThreadId: archived && normalizeThreadId(state.activeThreadId) === id ? '' : state.activeThreadId,
        threads: state.threads.map((thread) => (thread.id === id ? {
          ...thread,
          archived: Boolean(archived),
          archivedAt,
          status: threadArchiveStatus(thread, archived),
        } : thread)),
        threadArchiveLoadingByThread: {
          ...state.threadArchiveLoadingByThread,
          [id]: true,
        },
        lastListMutationTime: Date.now(),
        lastArchivedStatesByThread: {
          ...state.lastArchivedStatesByThread,
          [id]: { archived: Boolean(archived), timestamp: Date.now() },
        },
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
        
        runtime.set((state) => {
          const nextMutated = { ...state.lastArchivedStatesByThread };
          delete nextMutated[id];
          const originalThread = originalThreads.find((t) => threadMatchesIdentifier(t, id));
          return {
            activeThreadId: state.activeThreadId === '' ? originalActiveThreadId : state.activeThreadId,
            threads: state.threads.map((thread) => (threadMatchesIdentifier(thread, id) && originalThread ? {
              ...thread,
              archived: Boolean(originalThread.archived),
              archivedAt: originalThread.archivedAt || 0,
              status: originalThread.status,
            } : thread)),
            threadArchiveLoadingByThread: {
              ...state.threadArchiveLoadingByThread,
              [id]: false,
            },
            lastArchivedStatesByThread: nextMutated,
            actionNotice: actionNotice(`${action}会话失败：${message}`, 'error'),
          };
        });
        
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
  const runtime = createClientStoreRuntime(set, get);
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
    ...createComposerSlice(runtime, composerActionDeps),
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
