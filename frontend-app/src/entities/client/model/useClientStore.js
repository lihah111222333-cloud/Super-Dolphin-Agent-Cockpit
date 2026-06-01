import { create } from 'zustand';
import {
  addProject as addProjectRPC,
  archiveThread as archiveThreadRPC,
  compactThread,
  deleteThread as deleteThreadRPC,
  getProjects,
  getSidebarState,
  getThreadConfig,
  getThreadMessages,
  getThreadState,
  getWindowBootstrap,
  getPreference,
  interruptTurn,
  onBridgeEvent,
  openNewWindow as openNewWindowRPC,
  readConfig,
  recoverThread,
  registerBridgeLogStore,
  renameThread as renameThreadRPC,
  resolveThreadIdentity,
  saveClipboardImage,
  beginTextClipboardWrite,
  copyTextToClipboard,
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

const DEFAULT_PROVIDER = 'codex';
const MAX_WARNING_ENTRIES = 300;
const MAX_RUNTIME_RESULT_ENTRIES = 120;
const RUNTIME_RESULT_DETAIL_LIMIT = 1600;
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
  tasks: 'workflows',
  commands: 'workflows',
  'memory-center': 'memory',
  memory: 'files',
});
const APP_PAGE_IDS = new Set(['chat', 'prompts', 'workflows', 'skills', 'memory', 'files', 'settings']);
const IMAGE_ATTACHMENT_RE = /\.(png|jpe?g|gif|webp|bmp|svg)$/i;

function normalizeString(value) {
  return (value || '').toString().trim();
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
  if (provider === 'codex' || provider === 'claude') return provider;
  throw new Error(`invalid provider preference: ${normalizeProviderConfigValue(value)}`);
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
  const modelKey = providerPreferenceKey(providerScope, 'model');
  const effortKey = providerPreferenceKey(providerScope, 'effort');

  const launch = cleanObject({
    modelProvider: provider,
    model: requireProviderPreferenceValue(model, modelKey, 'startThread'),
    effort: requireProviderPreferenceValue(effort, effortKey, 'startThread'),
    prompt_key: normalizeProviderConfigValue(activePromptKey),
  });
  if (providerScope === 'codex') {
    const codexHomeKey = providerPreferenceKey('codex', 'codexHome');
    const codexInstanceKeyKey = providerPreferenceKey('codex', 'codexInstanceKey');
    const codexModelProviderKey = providerPreferenceKey('codex', 'codexModelProvider');
    launch.config = {
      codexHome: requireProviderPreferenceValue(codexHome, codexHomeKey, 'startThread'),
      codexInstanceKey: requireProviderPreferenceValue(codexInstanceKey, codexInstanceKeyKey, 'startThread'),
      codexModelProvider: requireProviderPreferenceValue(codexModelProvider, codexModelProviderKey, 'startThread'),
    };
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
    if (archivedAt > 0) {
      entries.push([id, archivedAt]);
    } else if (thread?.archived) {
      entries.push([id, 1]);
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

function normalizeThread(raw, options = {}) {
  const identity = normalizeThreadIdentity(raw);
  const status = normalizeString(raw?.status || raw?.state || raw?.lifecycleStatus || raw?.lifecycle_status || raw?.threadStatus || raw?.thread_status) || '等待指示';
  const sourceThread = raw?.thread && typeof raw.thread === 'object' ? raw.thread : {};
  const cwd = normalizePath(
    raw?.cwd ||
    raw?.CWD ||
    raw?.workdir ||
    raw?.workDir ||
    raw?.work_dir ||
    sourceThread?.cwd ||
    sourceThread?.CWD ||
    sourceThread?.workdir ||
    sourceThread?.workDir ||
    sourceThread?.work_dir ||
    options.fallbackCwd,
  );
  const provider = normalizeString(
    raw?.provider ||
    raw?.modelProvider ||
    raw?.model_provider ||
    raw?.agentKey ||
    raw?.agent_key ||
    options.fallbackProvider,
  );
  const pinnedAt = normalizeTimestamp(
    raw?.pinnedAt ||
    raw?.pinned_at ||
    raw?.pinnedAtMs ||
    raw?.pinned_at_ms ||
    (typeof raw?.pinned === 'boolean' ? 0 : raw?.pinned) ||
    options.pinnedAtById?.[identity.threadId],
  );
  const archivedAt = normalizeTimestamp(
    raw?.archivedAt ||
    raw?.archived_at ||
    raw?.archivedAtMs ||
    raw?.archived_at_ms ||
    (typeof raw?.archived === 'boolean' ? 0 : raw?.archived) ||
    options.archivedAtById?.[identity.threadId],
  );
  const lifecycleStatus = normalizeString(raw?.lifecycleStatus || raw?.lifecycle_status || raw?.threadStatus || raw?.thread_status);
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
    archived: Boolean(raw?.archived || raw?.isArchived || archivedAt > 0 || isArchivedStatus(status) || isArchivedStatus(lifecycleStatus)),
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

function activeProviderLockedThreadId(state) {
  const id = normalizeThreadId(state?.activeThreadId);
  if (!id) return '';
  const matchedThread = (state?.threads || []).find((thread) => threadMatchesIdentifier(thread, id));
  return normalizeBackendThreadId(matchedThread?.id || id);
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

function canonicalizeActiveTurnByThread(activeTurnByThread = {}, threads = []) {
  const next = {};
  for (const [threadId, turn] of Object.entries(activeTurnByThread || {})) {
    const normalized = normalizeTurnSummary(turn);
    if (!normalized) continue;
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

function normalizeTokenUsage(value) {
  if (!value || typeof value !== 'object') return null;
  const usedTokens = Number(value.usedTokens ?? value.used_tokens ?? value.totalTokens ?? value.total_tokens ?? 0) || 0;
  const contextWindowTokens = Number(value.contextWindowTokens ?? value.context_window_tokens ?? value.contextWindow ?? value.context_window ?? 0) || 0;
  const usedPercent = Number(value.usedPercent ?? value.used_percent ?? (contextWindowTokens > 0 ? (usedTokens / contextWindowTokens) * 100 : 0)) || 0;
  return { usedTokens, contextWindowTokens, usedPercent };
}

function normalizeActivityStats(value) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null;
  const rawToolCalls = value.toolCalls || value.tool_calls || {};
  const toolCalls = {};
  if (rawToolCalls && typeof rawToolCalls === 'object' && !Array.isArray(rawToolCalls)) {
    for (const [name, count] of Object.entries(rawToolCalls)) {
      const key = normalizeString(name);
      const numeric = Number(count);
      if (key && Number.isFinite(numeric) && numeric > 0) toolCalls[key] = numeric;
    }
  }
  return {
    lspCalls: Math.max(0, Number(value.lspCalls ?? value.lsp_calls ?? 0) || 0),
    commands: Math.max(0, Number(value.commands ?? 0) || 0),
    fileEdits: Math.max(0, Number(value.fileEdits ?? value.file_edits ?? 0) || 0),
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

function normalizeTimelineItem(item) {
  const rawKind = normalizeString(item?.kind || item?.type || item?.eventType || item?.event_type || item?.role).toLowerCase();
  const rawRole = normalizeString(item?.role || item?.kind || item?.type || item?.eventType || item?.event_type).toLowerCase();
  const normalizedRole = rawRole.includes('user') ? 'user' : 'assistant';
  const normalizedKind = rawRole.includes('user')
    ? 'user'
    : (
      rawKind.includes('thinking') || rawKind.includes('reasoning') ? 'thinking'
        : rawKind.includes('command') || rawKind.includes('exec') ? 'command'
          : rawKind.includes('tool') ? 'tool'
            : rawKind.includes('assistant') || rawKind.includes('agent_message') || rawKind.includes('agentmessage') || rawKind === 'final_answer' ? 'assistant'
              : 'assistant'
    );
  const text = extractText(item?.text || item?.content || item?.message || item?.delta || item?.output || item?.result || item?.answer || item?.response || item?.summary || item?.preview);
  return {
    id: normalizeString(item?.id || item?.messageId || item?.message_id) || `${normalizedRole}-${Date.now()}`,
    role: normalizedRole,
    kind: normalizedKind,
    text,
    title: normalizeString(item?.title || item?.label || item?.name || item?.tool || item?.toolName || item?.command),
    status: normalizeString(item?.status),
    time: normalizeString(item?.time || item?.startedAt || item?.started_at || item?.ts || item?.createdAt || item?.created_at) || new Date().toISOString(),
    completedAt: normalizeString(item?.completedAt || item?.completed_at || item?.finishedAt || item?.finished_at),
    done: item?.done !== false,
    optimistic: Boolean(item?.optimistic),
    elapsedMs: item?.elapsedMs !== undefined
      ? Number(item.elapsedMs)
      : (item?.elapsed_ms !== undefined
        ? Number(item.elapsed_ms)
        : (item?.durationMs !== undefined ? Number(item.durationMs) : (item?.duration_ms !== undefined ? Number(item.duration_ms) : undefined))),
  };
}

function normalizeThreadMessagesTotal(value) {
  if (value === null || value === undefined || value === '') return null;
  const total = Number(value);
  return Number.isFinite(total) && total >= 0 ? total : null;
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
  return withoutMCPServer
    .replace(/[./:-]+/g, '_')
    .replace(/^functions_+/, '')
    .replace(/^function_+/, '')
    .replace(/^tools_+/, '')
    .replace(/^tool_+/, '')
    .replace(/^lsp_+/, '')
    .replace(/_+/g, '_')
    .replace(/^_+|_+$/g, '');
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
  const failed = status === 'failed' || status === 'error' || item.success === false || Boolean(normalizeString(item.error));
  const detail = runtimeToolResultDetail(item);
  const terminal = ['completed', 'complete', 'done', 'ok', 'success', 'succeeded', 'failed', 'error'].includes(status);
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
  return items
    .map((item, index) => runtimeToolResultEntry(item, threadId, index))
    .filter(Boolean);
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
  return [...nextById.values()]
    .sort((left, right) => {
      const leftTime = normalizeTimestamp(left.timestamp);
      const rightTime = normalizeTimestamp(right.timestamp);
      return rightTime - leftTime;
    })
    .slice(0, MAX_RUNTIME_RESULT_ENTRIES);
}

function sortTimelineChronologically(items = []) {
  return [...items]
    .map((item, index) => ({ item, index, timestamp: normalizeTimestamp(item?.time) }))
    .sort((left, right) => {
      if (left.timestamp !== right.timestamp) return left.timestamp - right.timestamp;
      return left.index - right.index;
    })
    .map(({ item }) => item);
}

function sameTimelineContent(left, right) {
  return left?.role === right?.role && normalizeTimelineKind(left) === normalizeTimelineKind(right) && normalizeString(left?.text) === normalizeString(right?.text);
}

function compactTimelineText(value) {
  return normalizeString(value).replace(/\s+/g, '');
}

function sameTimelineContentCompact(left, right) {
  return left?.role === right?.role &&
    normalizeTimelineKind(left) === normalizeTimelineKind(right) &&
    compactTimelineText(left?.text) &&
    compactTimelineText(left?.text) === compactTimelineText(right?.text);
}

function normalizeTimelineKind(item) {
  const kind = normalizeString(item?.kind).toLowerCase();
  if (kind) return kind;
  return item?.role === 'user' ? 'user' : 'assistant';
}

function isVisibleTimelineItem(item) {
  if (item?.role === 'user') return true;
  if (normalizeString(item?.text)) return true;
  const kind = normalizeTimelineKind(item);
  return kind === 'thinking' || kind === 'reasoning' || kind === 'tool' || kind === 'command' || kind === 'process';
}

function preferredAssistantTimelineItem(existingItem, incomingItem) {
  if (existingItem?.runtime !== incomingItem?.runtime) {
    return incomingItem?.runtime ? existingItem : incomingItem;
  }
  return normalizeString(incomingItem?.text).length > normalizeString(existingItem?.text).length
    ? incomingItem
    : existingItem;
}

function dedupeAssistantTimelineItems(items = []) {
  const output = [];
  let lastUserIndex = -1;

  for (const item of items) {
    if (item?.role === 'user') {
      output.push(item);
      lastUserIndex = output.length - 1;
      continue;
    }

    if (item?.role !== 'assistant' || item.done === false || !compactTimelineText(item.text)) {
      output.push(item);
      continue;
    }

    let duplicateIndex = -1;
    for (let index = output.length - 1; index > lastUserIndex; index -= 1) {
      const candidate = output[index];
      if (candidate?.role === 'assistant' && candidate.done !== false && sameTimelineContentCompact(candidate, item)) {
        duplicateIndex = index;
        break;
      }
    }

    if (duplicateIndex >= 0) {
      output[duplicateIndex] = preferredAssistantTimelineItem(output[duplicateIndex], item);
      continue;
    }

    output.push(item);
  }

  return output;
}

function mergeTimelineItems(existingItems = [], incomingItems = [], options = {}) {
  const preserveExistingVisible = options?.preserveExistingVisible === true;
  const incomingById = new Map(incomingItems.map((item) => [item.id, item]));
  const incomingIds = new Set(incomingById.keys());
  const consumedIncomingIds = new Set();
  const merged = [];

  for (const existingItem of existingItems) {
    const replacement = incomingById.get(existingItem.id);
    if (replacement) {
      merged.push(replacement);
      consumedIncomingIds.add(replacement.id);
      continue;
    }

    const shouldPreserveExistingMessage = (
      ((preserveExistingVisible && isVisibleTimelineItem(existingItem)) || existingItem.role === 'user' || existingItem.optimistic || existingItem.runtime) &&
      !incomingIds.has(existingItem.id) &&
      !incomingItems.some((incomingItem) => (
        sameTimelineContent(existingItem, incomingItem) ||
        sameTimelineContentCompact(existingItem, incomingItem)
      ))
    );
    if (shouldPreserveExistingMessage) {
      merged.push(existingItem);
    }
  }

  for (const incomingItem of incomingItems) {
    if (!consumedIncomingIds.has(incomingItem.id)) {
      merged.push(incomingItem);
    }
  }

  return dedupeAssistantTimelineItems(sortTimelineChronologically(merged));
}

function runtimeThreadIdentifier(payload = {}) {
  const patch = payload._threadPatch || payload._thread_patch || {};
  const thread = payload.thread && typeof payload.thread === 'object' ? payload.thread : {};
  const patchThread = patch.thread && typeof patch.thread === 'object' ? patch.thread : {};
  const runtime = payload.agentRuntime || payload.agent_runtime || {};
  const patchRuntime = patch.agentRuntime || patch.agent_runtime || {};
  return payload.threadId ||
    payload.thread_id ||
    payload.codexThreadId ||
    payload.codex_thread_id ||
    thread.threadId ||
    thread.thread_id ||
    thread.codexThreadId ||
    thread.codex_thread_id ||
    thread.id ||
    runtime.threadId ||
    runtime.thread_id ||
    patch.threadId ||
    patch.thread_id ||
    patch.codexThreadId ||
    patch.codex_thread_id ||
    patchThread.threadId ||
    patchThread.thread_id ||
    patchThread.codexThreadId ||
    patchThread.codex_thread_id ||
    patchThread.id ||
    patchRuntime.threadId ||
    patchRuntime.thread_id ||
    payload.agentId ||
    payload.agent_id ||
    thread.agentId ||
    thread.agent_id ||
    runtime.agentId ||
    runtime.agent_id ||
    patch.agentId ||
    patch.agent_id ||
    patchThread.agentId ||
    patchThread.agent_id ||
    patchRuntime.agentId ||
    patchRuntime.agent_id;
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
  return runtimeAssistantStreamId(payload) ||
    `assistant-stream-${normalizeThreadId(runtimeThreadIdentifier(payload)) || Date.now()}`;
}

function isRuntimeAssistantItem(item) {
  const type = normalizeString(item?.type || item?.kind || item?.role).toLowerCase();
  return type.includes('agentmessage') ||
    type.includes('agent_message') ||
    type.includes('assistant') ||
    type === 'final_answer';
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

function mergeRuntimeAssistantCompletion(existingItems = [], completion) {
  if (!completion?.item) return existingItems;
  const finalItem = completion.item;
  const dropIds = new Set([finalItem.id, completion.streamId].filter(Boolean));
  const withoutReplaced = existingItems.filter((item) => !dropIds.has(item.id));
  let lastUserIndex = -1;
  for (let index = withoutReplaced.length - 1; index >= 0; index -= 1) {
    if (withoutReplaced[index]?.role === 'user') {
      lastUserIndex = index;
      break;
    }
  }
  const duplicate = withoutReplaced.find((item, index) => (
    item.role === 'assistant' &&
    item.done !== false &&
    (
      sameTimelineContent(item, finalItem) ||
      (index > lastUserIndex && sameTimelineContentCompact(item, finalItem))
    )
  ));
  if (duplicate && (!completion.explicitId || duplicate.runtime || withoutReplaced.indexOf(duplicate) > lastUserIndex)) {
    return dedupeAssistantTimelineItems(withoutReplaced);
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
  return Array.isArray(attachments)
    ? attachments.map((item) => ({ ...item })).map(normalizeAttachment).filter(Boolean)
    : [];
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

async function startTurnWithRecover(payload) {
  return startTurn(payload);
}

function createLaunchIntentId() {
  const id = globalThis.crypto?.randomUUID?.() || `${Date.now()}-${Math.random().toString(16).slice(2)}`;
  return `launch_${id}`;
}

function compareSequence(left, right) {
  try {
    const a = BigInt(normalizeString(left) || '0');
    const b = BigInt(normalizeString(right) || '0');
    if (a === b) return 0;
    return a < b ? -1 : 1;
  } catch {
    return 0;
  }
}

function resolveInitialLevel() {
  try {
    if (typeof localStorage !== 'undefined') {
      return localStorage.getItem('agent-orchestrator.log.level') || 'info';
    }
  } catch (error) {
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
  permission: '完全访问权限',
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
  threadConfigSaving: false,
  timelinesByThread: {},
  threadTimelineReadyByThread: {},
  tokenUsageByThread: {},
  activityStatsByThread: {},
  diffTextByThread: {},
  activityEntries: [],
  runtimeResultEntries: [],
  warningEntries: [],
  draft: '',
  attachments: [],
  sending: false,
  rightPanelWidth: 380,
  logLevel: resolveInitialLevel(),
  logEntries: [],
  actionNotice: null,
};

function stateWithPatch(patch = {}) {
  return {
    ...baseState,
    ...patch,
  };
}

export const useClientStore = create((set, get) => {
  let bridgeUnsubscribe = null;
  const sequencesByThread = new Map();
  const composerDrafts = new Map();
  const sidebarSnapshotsByCwd = new Map();
  let sidebarRefreshSeq = 0;

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
      warningEntries: (() => {
        const existingIndex = state.warningEntries.findIndex((item) => item.signature === signature);
        if (existingIndex < 0) return [entry, ...state.warningEntries].slice(0, MAX_WARNING_ENTRIES);
        const existing = state.warningEntries[existingIndex];
        const updated = {
          ...existing,
          id: entry.id,
          timestamp: entry.timestamp,
          fields,
          occurrenceCount: (Number(existing.occurrenceCount) || 1) + 1,
        };
        return [
          updated,
          ...state.warningEntries.slice(0, existingIndex),
          ...state.warningEntries.slice(existingIndex + 1),
        ].slice(0, MAX_WARNING_ENTRIES);
      })(),
    }));
  };

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
    } catch (error) {
      void error;
    }
    set({ logLevel: level });
  };

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
      pendingActiveThreadId: '',
      timelinesByThread: {},
      threadTimelineReadyByThread: {},
      tokenUsageByThread: {},
      activityStatsByThread: {},
      diffTextByThread: {},
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
      model: requireProviderPreferenceValue(model, modelKey, 'provider.config'),
      effort: requireProviderPreferenceValue(effort, effortKey, 'provider.config'),
      codexModelProvider: providerScope === 'codex'
        ? requireProviderPreferenceValue(codexModelProvider, codexModelProviderKey, 'provider.config')
        : '',
    }, provider);
    set({ provider, providerConfig });
    return providerConfig;
  };

  const applySnapshot = (payload = {}, options = {}) => {
    const preferredActiveThreadId = normalizeThreadId(options.preferredActiveThreadId);
    const autoSelectThread = options.autoSelectThread !== false;
    set((state) => {
      const archivedAtById = hasArchiveMapPayload(payload) ? archiveMapFromPayload(payload) : archiveMapFromThreads(state.threads);
      const pinnedAtById = hasPinMapPayload(payload) ? pinMapFromPayload(payload) : state.pinnedThreadAtById;
      const runtimeById = runtimeMapFromPayload(payload);
      const scopeCwd = normalizePath(options.scopeCwd) || composerScopeCwd(state);
      const incomingThreads = Array.isArray(payload.threads)
        ? payload.threads.filter((thread) => threadMatchesCwdScope(thread, scopeCwd, runtimeById)).map((thread) => normalizeThread(thread, {
          archivedAtById,
          pinnedAtById,
          fallbackProvider: runtimeProviderForThread(thread, runtimeById),
          fallbackCwd: snapshotThreadCwd(thread, runtimeById),
        })).filter((thread) => thread.id)
        : state.threads;
      const nextThreads = [...incomingThreads];
      if (options.preserveActiveThreadId) {
        for (const thread of state.threads) {
          const shouldPreserve = thread.id === state.activeThreadId || (state.timelinesByThread[thread.id] || []).length > 0;
          const alreadyIncluded = nextThreads.some((nextThread) => threadMatchesIdentifier(nextThread, thread.id));
          if (shouldPreserve && !alreadyIncluded) nextThreads.push(thread);
        }
      }
      const snapshotActive = normalizeThreadId(payload.activeThreadId || payload.active_thread_id);
      const selectableThreads = nextThreads.filter((thread) => !thread.archived);
      const preservedActiveThreadId = options.preserveActiveThreadId ? (
        backendThreadIdFromThreads(state.activeThreadId, nextThreads) ||
        (!nextThreads.some((thread) => threadMatchesIdentifier(thread, state.activeThreadId)) ? normalizeBackendThreadId(state.activeThreadId) : '')
      ) : '';
      const activeLookupOptions = options.includeArchivedActiveThread ? { includeArchived: true } : {};
      const explicitActiveThreadId = (
        preservedActiveThreadId ||
        backendThreadIdFromThreads(preferredActiveThreadId, nextThreads, activeLookupOptions)
      );
      const activeThreadId = autoSelectThread
        ? (
          explicitActiveThreadId ||
          backendThreadIdFromThreads(snapshotActive, nextThreads, activeLookupOptions) ||
          backendThreadIdFromThreads(state.activeThreadId, nextThreads, activeLookupOptions) ||
          selectableThreads[0]?.id ||
          ''
        )
        : explicitActiveThreadId;

      const timelinesByThread = {};
      const threadTimelineReadyByThread = {};
      for (const [threadId, items] of Object.entries(state.timelinesByThread)) {
        const canonicalId = canonicalizeThreadKey(threadId, nextThreads);
        timelinesByThread[canonicalId] = items;
      }
      for (const [threadId, ready] of Object.entries(state.threadTimelineReadyByThread || {})) {
        const canonicalId = canonicalizeThreadKey(threadId, nextThreads);
        threadTimelineReadyByThread[canonicalId] = Boolean(ready);
      }
      const runtimeResultEntries = [];
      const incomingTimelines = payload.timelinesByThread || payload.timelines_by_thread;
      if (incomingTimelines && typeof incomingTimelines === 'object') {
        for (const [threadId, items] of Object.entries(incomingTimelines)) {
          if (Array.isArray(items)) {
            const canonicalId = canonicalizeThreadKey(threadId, nextThreads);
            const existingTimeline = timelinesByThread[canonicalId] || [];
            const normalizedItems = items.map(normalizeTimelineItem);
            runtimeResultEntries.push(...runtimeResultEntriesFromTimelineItems(items, canonicalId));
            const visibleItems = normalizedItems.filter(isVisibleTimelineItem);
            timelinesByThread[canonicalId] = visibleItems.length === 0 && threadTimelineReadyByThread[canonicalId]
              ? existingTimeline
              : mergeTimelineItems(existingTimeline, visibleItems, { preserveExistingVisible: true });
            threadTimelineReadyByThread[canonicalId] = true;
          }
        }
      }

      const tokenUsageByThread = {};
      for (const [threadId, usage] of Object.entries(state.tokenUsageByThread)) {
        const canonicalId = canonicalizeThreadKey(threadId, nextThreads);
        tokenUsageByThread[canonicalId] = usage;
      }
      const incomingTokens = payload.tokenUsageByThread || payload.token_usage_by_thread;
      if (incomingTokens && typeof incomingTokens === 'object') {
        for (const [threadId, usage] of Object.entries(incomingTokens)) {
          const normalized = normalizeTokenUsage(usage);
          if (normalized) {
            const canonicalId = canonicalizeThreadKey(threadId, nextThreads);
            tokenUsageByThread[canonicalId] = normalized;
          }
        }
      }
      const activeTokenUsage = normalizeTokenUsage(payload.tokenUsage || payload.token_usage);
      if (activeTokenUsage && activeThreadId) {
        tokenUsageByThread[activeThreadId] = activeTokenUsage;
      }

      const activityStatsByThread = {};
      for (const [threadId, stats] of Object.entries(state.activityStatsByThread)) {
        const canonicalId = canonicalizeThreadKey(threadId, nextThreads);
        activityStatsByThread[canonicalId] = stats;
      }
      const incomingActivityStats = payload.activityStatsByThread || payload.activity_stats_by_thread;
      if (incomingActivityStats && typeof incomingActivityStats === 'object') {
        for (const [threadId, stats] of Object.entries(incomingActivityStats)) {
          const normalized = normalizeActivityStats(stats);
          if (normalized) {
            const canonicalId = canonicalizeThreadKey(threadId, nextThreads);
            activityStatsByThread[canonicalId] = normalized;
          }
        }
      }
      const activeActivityStats = normalizeActivityStats(payload.activityStats || payload.activity_stats);
      if (activeActivityStats && activeThreadId) {
        activityStatsByThread[activeThreadId] = activeActivityStats;
      }

      const diffTextByThread = {};
      for (const [threadId, text] of Object.entries(state.diffTextByThread)) {
        const canonicalId = canonicalizeThreadKey(threadId, nextThreads);
        diffTextByThread[canonicalId] = text;
      }
      const incomingDiff = payload.diffTextByThread || payload.diff_text_by_thread;
      if (incomingDiff && typeof incomingDiff === 'object') {
        for (const [threadId, text] of Object.entries(incomingDiff)) {
          const canonicalId = canonicalizeThreadKey(threadId, nextThreads);
          diffTextByThread[canonicalId] = text;
        }
      }
      if (activeThreadId && typeof payload.diffText === 'string') {
        diffTextByThread[activeThreadId] = payload.diffText;
      }

      let activeTurnByThread = canonicalizeActiveTurnByThread(state.activeTurnByThread, nextThreads);
      const activeTurn = activeTurnPayload(payload);
      if (activeTurn !== undefined) {
        activeTurnByThread = {};
        const normalizedActiveTurn = normalizeTurnSummary(activeTurn);
        if (normalizedActiveTurn?.threadId) {
          const canonicalThreadId = canonicalizeThreadKey(normalizedActiveTurn.threadId, nextThreads);
          activeTurnByThread[canonicalThreadId] = { ...normalizedActiveTurn, threadId: canonicalThreadId };
        }
      }

      return {
        activeThreadId,
        threads: nextThreads,
        pinnedThreadAtById: pinnedAtById,
        timelinesByThread,
        threadTimelineReadyByThread,
        tokenUsageByThread,
        activityStatsByThread,
        diffTextByThread,
        runtimeResultEntries: mergeRuntimeResultEntries(state.runtimeResultEntries, runtimeResultEntries),
        activeTurnByThread,
        statuses: {
          ...state.statuses,
          ...(payload.statuses || {}),
        },
      };
    });
  };

  const refreshChatSurfaceForCwdInBackground = (cwdValue) => {
    const cwd = normalizePath(cwdValue);
    if (!cwd || cwd === '.') {
      throw new Error('frontend-app: cwd is required for project chat refresh');
    }
    const seq = ++sidebarRefreshSeq;
    const cachedSidebar = sidebarSnapshotsByCwd.get(cwd);
    clearChatSurfaceForCwdSwitch(cwd);
    if (cachedSidebar) {
      applySnapshot(cachedSidebar, { autoSelectThread: false, scopeCwd: cwd });
    }
    getSidebarState({ cwd })
      .then((sidebar) => {
        cacheSidebarSnapshot(cwd, sidebar);
        if (seq !== sidebarRefreshSeq || normalizePath(currentChatCwd()) !== cwd) return;
        applySnapshot(sidebar, { autoSelectThread: false, scopeCwd: cwd });
        set((state) => ({
          chatSurfaceLoadingCwd: state.chatSurfaceLoadingCwd === cwd ? '' : state.chatSurfaceLoadingCwd,
        }));
      })
      .catch((error) => {
        if (seq !== sidebarRefreshSeq || normalizePath(currentChatCwd()) !== cwd) return;
        set((state) => ({
          chatSurfaceLoadingCwd: state.chatSurfaceLoadingCwd === cwd ? '' : state.chatSurfaceLoadingCwd,
          actionNotice: actionNotice(`刷新会话列表失败：${error.message}`, 'error'),
        }));
        addWarning('error', 'thread.sidebar.refresh.failed', { cwd, error: error.message });
      });
  };

  const loadThreadMessages = async (threadId, options = {}) => {
    const loadOptions = options && typeof options === 'object' ? options : {};
    const id = backendThreadIdForState(get(), threadId, { includeArchived: loadOptions.includeArchived === true });
    if (!id) return;
    try {
      const allMessages = [];
      const seenCursors = new Set();
      let before = '';
      let expectedTotal = null;

      while (true) {
        const params = before
          ? { threadId: id, limit: THREAD_MESSAGES_PAGE_SIZE, before }
          : { threadId: id, limit: THREAD_MESSAGES_PAGE_SIZE };
        const res = await getThreadMessages(params);
        const page = Array.isArray(res?.messages) ? res.messages : [];
        expectedTotal = normalizeThreadMessagesTotal(res?.total) ?? expectedTotal;
        if (page.length === 0) {
          if (expectedTotal !== null && allMessages.length < expectedTotal) {
            throw new Error(`thread/messages returned ${allMessages.length}/${expectedTotal} messages before history was complete`);
          }
          break;
        }

        allMessages.push(...page);
        const shouldLoadMore = expectedTotal !== null
          ? allMessages.length < expectedTotal
          : page.length >= THREAD_MESSAGES_PAGE_SIZE;
        if (!shouldLoadMore) break;

        const nextBefore = oldestThreadMessageCursor(page);
        if (!nextBefore) {
          throw new Error('thread/messages cannot continue pagination without an id or createdAt cursor');
        }
        if (seenCursors.has(nextBefore)) {
          throw new Error(`thread/messages pagination cursor repeated: ${nextBefore}`);
        }
        seenCursors.add(nextBefore);
        before = nextBefore;
      }

      if (allMessages.length === 0) {
        set((state) => ({
          timelinesByThread: state.threadTimelineReadyByThread?.[id]
            ? state.timelinesByThread
            : {
              ...state.timelinesByThread,
              [id]: mergeTimelineItems(state.timelinesByThread[id] || [], []),
            },
          threadTimelineReadyByThread: {
            ...state.threadTimelineReadyByThread,
            [id]: true,
          },
        }));
        return;
      }
      const pageItems = sortTimelineChronologically(allMessages.map((message) => normalizeTimelineItem({
        id: message.id || message.messageId || message.message_id,
        role: message.role,
        kind: message.kind || message.type || message.eventType || message.event_type,
        text: message.content || message.text || message.message || message.delta || message.output || message.result || message.answer || message.response,
        createdAt: message.createdAt || message.created_at,
        completedAt: message.completedAt || message.completed_at || message.finishedAt || message.finished_at,
      })).filter(isVisibleTimelineItem));
      set((state) => ({
        timelinesByThread: {
          ...state.timelinesByThread,
          [id]: mergeTimelineItems(state.timelinesByThread[id] || [], pageItems, { preserveExistingVisible: true }),
        },
        threadTimelineReadyByThread: {
          ...state.threadTimelineReadyByThread,
          [id]: true,
        },
      }));
    } catch (error) {
      addWarning('error', 'thread.messages.failed', { threadId: id, error: error.message });
    }
  };

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
    if (isAgentRuntimeId(id)) return '';

    const fallback = normalizeBackendThreadId(id);
    if (!fallback) return '';
    if (fallback === normalizeBackendThreadId(state.activeThreadId)) return fallback;
    if (payloadCwd && (!activeCwd || payloadCwd === activeCwd)) return fallback;

    addWarning('warn', 'thread.patch.unknown_thread', { threadId: fallback, activeCwd });
    return '';
  };

  const applyAssistantDelta = (method, payload) => {
    const threadId = bridgeThreadIdForPayload(payload);
    const delta = extractText(payload.delta || payload.text || payload.content);
    if (!threadId || !delta) return false;
    const itemId = normalizeString(payload.itemId || payload.item_id || payload.messageId || payload.message_id) ||
      runtimeAssistantFallbackId(payload);
    set((state) => {
      const timeline = state.timelinesByThread[threadId] || [];
      let found = false;
      const nextTimeline = timeline.map((item) => {
        if (item.id !== itemId) return item;
        found = true;
        return {
          ...item,
          role: 'assistant',
          text: `${normalizeString(item.text)}${delta}`,
          done: false,
        };
      });
      if (!found) {
        nextTimeline.push({
          id: itemId,
          role: 'assistant',
          kind: 'assistant',
          text: delta,
          time: normalizeString(payload.timestamp) || new Date().toISOString(),
          done: false,
          optimistic: false,
          runtime: true,
        });
      }
      return {
        timelinesByThread: {
          ...state.timelinesByThread,
          [threadId]: nextTimeline,
        },
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

    const timelineItems = payload.timelineItems || payload.timeline_items;
    const runtimeResultEntries = runtimeResultEntriesFromTimelineItems(timelineItems, threadId);
    const tokenUsage = normalizeTokenUsage(payload.tokenUsage || payload.token_usage);
    const activityStats = normalizeActivityStats(payload.activityStats || payload.activity_stats);
    const diffText = typeof payload.diffText === 'string' ? payload.diffText : payload.diff_text;
    const rawRuntime = payload.agentRuntime || payload.agent_runtime || {};
    const rawThread = payload.thread && typeof payload.thread === 'object' ? payload.thread : {};
    const patchProvider = normalizeString(
      rawRuntime.provider ||
      rawRuntime.modelProvider ||
      rawRuntime.model_provider ||
      rawThread.provider ||
      rawThread.modelProvider ||
      rawThread.model_provider,
    );
    const statusText = normalizeString(payload.statusHeader || payload.status || rawThread.state || rawThread.status);
    const patchedThread = normalizeThread({
      ...rawThread,
      threadId,
      agentId: rawRuntime.agentId || rawRuntime.agent_id || rawThread.agentId || rawThread.agent_id,
      providerThreadId: rawRuntime.providerThreadId || rawRuntime.provider_thread_id || rawThread.providerThreadId || rawThread.provider_thread_id,
      provider: patchProvider,
      lastMessage: rawRuntime.lastMessage || rawRuntime.last_message || payload.statusDetails || payload.status_details || rawThread.lastMessage,
      status: statusText || rawThread.status,
    });

    set((state) => {
      const timelinesByThread = { ...state.timelinesByThread };
      if (Array.isArray(timelineItems)) {
        timelinesByThread[threadId] = mergeTimelineItems(
          timelinesByThread[threadId] || [],
          timelineItems.map(normalizeTimelineItem).filter(isVisibleTimelineItem),
          { preserveExistingVisible: true },
        );
      }

      const tokenUsageByThread = { ...state.tokenUsageByThread };
      if (tokenUsage) tokenUsageByThread[threadId] = tokenUsage;

      const activityStatsByThread = { ...state.activityStatsByThread };
      if (activityStats) activityStatsByThread[threadId] = activityStats;

      const diffTextByThread = { ...state.diffTextByThread };
      if (typeof diffText === 'string') diffTextByThread[threadId] = diffText;
      const activeTurnByThread = { ...state.activeTurnByThread };
      const patchActiveTurn = activeTurnPayload(payload);
      if (patchActiveTurn !== undefined) {
        delete activeTurnByThread[threadId];
        const normalizedActiveTurn = normalizeTurnSummary(patchActiveTurn);
        if (normalizedActiveTurn?.id) activeTurnByThread[threadId] = { ...normalizedActiveTurn, threadId };
      } else if (payload.interruptible === false || statusText === 'idle' || statusText === 'interrupted' || statusText === 'completed') {
        delete activeTurnByThread[threadId];
      }

      const existingThread = state.threads.find((thread) => threadMatchesIdentifier(thread, threadId));
      const promoteForActivity = shouldFloatThreadPatch(payload);
      let threads = state.threads;
      if (patchedThread.id) {
        const mergedThread = {
          ...(existingThread || {}),
          ...patchedThread,
          name: patchedThread.name === '新对话' ? (existingThread?.name || patchedThread.name) : patchedThread.name,
          provider: patchProvider || existingThread?.provider || patchedThread.provider,
          status: statusText || patchedThread.status || existingThread?.status || '等待指示',
          archived: Boolean(existingThread?.archived || patchedThread.archived),
        };
        if (!existingThread || shouldFloatThreadPatch(payload)) {
          threads = [
            mergedThread,
            ...state.threads.filter((thread) => !threadMatchesIdentifier(thread, threadId)),
          ];
        } else {
          threads = state.threads.map((thread) => (threadMatchesIdentifier(thread, threadId) ? mergedThread : thread));
        }
      }

      return {
        threads,
        activityThreadAtById: promoteForActivity ? {
          ...state.activityThreadAtById,
          [threadId]: threadActivityTimestamp(),
        } : state.activityThreadAtById,
        timelinesByThread,
        tokenUsageByThread,
        activityStatsByThread,
        diffTextByThread,
        runtimeResultEntries: mergeRuntimeResultEntries(state.runtimeResultEntries, runtimeResultEntries),
        activeTurnByThread,
        statuses: {
          ...state.statuses,
          [threadId]: cleanObject({
            status: payload.status,
            statusHeader: payload.statusHeader,
            statusDetails: payload.statusDetails || payload.status_details,
            interruptible: payload.interruptible,
            activityStats,
            agentRuntime: rawRuntime,
          }),
        },
        activityEntries: [{
          id: `${method}-${Date.now()}`,
          method,
          threadId,
          timestamp: new Date().toISOString(),
        }, ...state.activityEntries].slice(0, 120),
      };
    });
  };

  const handleBridgeEvent = (evt) => {
    const method = normalizeString(evt?.method || evt?.type);
    const eventName = method.toLowerCase();
    const payload = evt?.payload || evt?.params || evt?.data || {};
    if (!method) return;

    if (eventName === 'skills/changed') {
      set((state) => ({ skillRevision: state.skillRevision + 1 }));
      return;
    }
    if (
      eventName === 'ui/shared-files/changed'
      || eventName === 'shared-files/changed'
      || eventName === 'shared_file/changed'
    ) {
      set((state) => ({ sharedFilesRevision: state.sharedFilesRevision + 1 }));
      return;
    }
    if (eventName === 'ui/memory/changed' || eventName === 'memory/changed') {
      set((state) => ({ memoryRevision: state.memoryRevision + 1 }));
      return;
    }
    if (
      eventName === 'prompts/changed'
      || eventName === 'prompt-assets/changed'
      || eventName === 'ui/prompts/changed'
      || (eventName === 'ui/preferences/changed' && normalizeString(payload.key) === ACTIVE_PROMPT_PREF_KEY)
    ) {
      set((state) => ({ promptRevision: state.promptRevision + 1 }));
      return;
    }
    if (
      eventName === 'task/node/statuschanged'
      || eventName === 'cron/job/runstatechanged'
      || eventName === 'task/dag/changed'
      || eventName === 'dags/changed'
    ) {
      set((state) => ({ workflowRevision: state.workflowRevision + 1 }));
      return;
    }
    if (method === 'ui/thread/patch') {
      applyBridgePatch(method, payload);
      return;
    }
    if (isAssistantMessageDeltaEvent(eventName, payload)) {
      applyAssistantDelta(method, payload);
      return;
    }
    if (eventName === 'item/completed') {
      applyAssistantCompletion(method, payload);
      return;
    }
    if (eventName === 'turn/completed') {
      if (payload._threadPatch) applyBridgePatch('ui/thread/patch', payload._threadPatch);
      applyAssistantCompletion(method, payload);
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

  const activeThreadRPC = async (action, rpc) => {
    const threadId = backendThreadIdForState(get(), get().activeThreadId);
    if (!threadId) {
      notifyAction('当前没有可操作的后端线程', 'warning');
      return false;
    }
    const cwd = requireCwd(action);
    const payload = action === 'thread.interrupt'
      ? { cwd, threadId, source: 'ui_stop' }
      : { cwd, threadId };
    try {
      await rpc(cleanObject(payload));
      notifyAction({
        'thread.interrupt': '已发送中断请求',
        'thread.compact': '已发送压缩请求',
        'thread.recover': '已发送恢复请求',
      }[action] || '线程操作已提交', 'success', { threadId });
      return true;
    } catch (error) {
      notifyAction(`${action} 失败：${error.message}`, 'error', { threadId });
      addWarning('error', `${action}.failed`, { threadId, error: error.message });
      throw error;
    }
  };

  return {
    ...baseState,

    initializeEvents: () => {
      if (bridgeUnsubscribe) return;
      bridgeUnsubscribe = onBridgeEvent(handleBridgeEvent);
    },

    destroy: () => {
      if (bridgeUnsubscribe) {
        bridgeUnsubscribe();
        bridgeUnsubscribe = null;
      }
      sequencesByThread.clear();
      composerDrafts.clear();
      sidebarSnapshotsByCwd.clear();
      sidebarRefreshSeq += 1;
    },

    bootstrap: async () => {
      set({ bootstrapStatus: 'loading', error: '' });
      get().initializeEvents();
      try {
        const config = await readConfig();
        const cwd = normalizePath(config?.cwd);
        if (!cwd || cwd === '.') {
          throw new Error('frontend-app bootstrap cwd is required');
        }
        const windowSnapshot = normalizeBootstrapSnapshot(await getWindowBootstrap());
        const windowCwd = normalizePath(windowSnapshot.cwd);
        const scopedCwd = windowCwd || cwd;
        const bootstrapPage = normalizeBootstrapPage(windowSnapshot.page);
        const activeProvider = requireActiveProviderPreference(
          await getPreference({ cwd: scopedCwd, key: PROVIDER_ACTIVE_PREF_KEY }),
          'frontend-app bootstrap',
        );
        set({
          cwd,
          projectScopeCwd: scopedCwd,
          activeProject: scopedCwd,
          provider: activeProvider,
          ...(bootstrapPage ? { activePage: bootstrapPage } : {}),
        });
        await loadProviderConfig(scopedCwd, activeProvider);
        const projects = await getProjects({ cwd: scopedCwd });
        applyProjects(projects, scopedCwd);
        const sidebar = await getSidebarState({ cwd: scopedCwd });
        cacheSidebarSnapshot(scopedCwd, sidebar);
        applySnapshot(sidebar);
        const activeThreadId = backendThreadIdForState(useClientStore.getState(), useClientStore.getState().activeThreadId);
        if (activeThreadId) {
          await get().syncThreadState(activeThreadId);
        }
        set({ bootstrapStatus: 'ready' });
      } catch (error) {
        set({ bootstrapStatus: 'failed', error: error.message });
        addWarning('error', 'app.bootstrap.failed', { error: error.message });
        throw error;
      }
    },

    syncThreadState: async (threadId, options = {}) => {
      const syncOptions = options && typeof options === 'object' ? options : {};
      const id = backendThreadIdForState(get(), threadId, { includeArchived: syncOptions.includeArchived === true });
      if (!id) return false;
      const cwd = requireCwd('thread.sync');
      const activeAtRequest = get().activeThreadId;
      const includeDiff = syncOptions.includeDiff !== false;
      const shouldLoadMessages = syncOptions.loadMessages !== false;
      const shouldLoadDiffAfter = syncOptions.loadDiffAfter === true && !includeDiff;
      set((state) => ({
        threadStateLoadingByThread: {
          ...state.threadStateLoadingByThread,
          [id]: true,
        },
      }));
      try {
        const snapshotPromise = getThreadState({ cwd, threadId: id, includeDiff });
        const messagesPromise = shouldLoadMessages
          ? loadThreadMessages(id, { includeArchived: syncOptions.includeArchived === true })
          : Promise.resolve();
        const snapshot = await snapshotPromise;
        const activeChanged = normalizeThreadId(get().activeThreadId) !== normalizeThreadId(activeAtRequest);
        applySnapshot(snapshot, {
          preferredActiveThreadId: id,
          preserveActiveThreadId: activeChanged || syncOptions.preserveActiveThreadId === true,
          includeArchivedActiveThread: syncOptions.includeArchived === true,
        });
        await messagesPromise;
        if (!activeChanged && shouldAutoLoadThreadConfig(get(), id)) await get().loadThreadConfig(id);
        if (shouldLoadDiffAfter && normalizeThreadId(get().activeThreadId) === normalizeThreadId(id)) {
          void get().syncThreadState(id, {
            includeArchived: syncOptions.includeArchived === true,
            includeDiff: true,
            loadMessages: false,
            preserveActiveThreadId: true,
          }).catch((error) => {
            addWarning('error', 'thread.diff.refresh.failed', { threadId: id, error: error.message });
          });
        }
        return true;
      } finally {
        set((state) => ({
          threadStateLoadingByThread: {
            ...state.threadStateLoadingByThread,
            [id]: false,
          },
        }));
      }
    },

    setActivePage: (activePage) => set({ activePage }),
    resolveLaunchPreferences: async (cwdArg) => {
      const cwd = normalizePath(cwdArg) || requireCwd('thread.launchPreferences');
      return resolveLaunchPreferences(cwd);
    },
    setPromptPageCache: (cwd, patch = {}) => {
      const key = normalizeString(cwd);
      if (!key) return;
      set((state) => ({
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
      set((state) => ({
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
    setSkillPageCache: (cwd, patch = {}) => {
      const key = normalizeString(cwd);
      if (!key) return;
      set((state) => ({
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
      set((state) => ({
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
      set((state) => ({
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
    setDraft: (draft) => set({ draft }),
    setPermission: (permission) => set({ permission }),
    setRightPanelWidth: (rightPanelWidth) => set({ rightPanelWidth }),

    refreshProviderConfig: async () => {
      const cwd = requireCwd('provider.config');
      const provider = normalizeProviderName(get().provider) || DEFAULT_PROVIDER;
      return loadProviderConfig(cwd, provider);
    },

    loadThreadConfig: async (threadId) => {
      const id = threadConfigTargetIdForState(get(), threadId);
      if (!id) return null;
      set((state) => ({
        threadConfigLoadingByThread: {
          ...state.threadConfigLoadingByThread,
          [id]: true,
        },
      }));
      try {
        const raw = await getThreadConfig({ threadId: id });
        const config = normalizeThreadConfig(raw, id, get().provider);
        set((state) => ({
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
      } catch (error) {
        set((state) => ({
          threadConfigLoadingByThread: {
            ...state.threadConfigLoadingByThread,
            [id]: false,
          },
          threadConfigFailedByThread: {
            ...state.threadConfigFailedByThread,
            [id]: true,
          },
        }));
        addWarning('error', 'thread.config.get.failed', { threadId: id, error: error.message });
        return null;
      }
    },

    saveComposerModelConfig: async (config = {}) => {
      const hasModel = Object.prototype.hasOwnProperty.call(config, 'model');
      const hasEffort = Object.prototype.hasOwnProperty.call(config, 'effort');
      const hasThreadTarget = Object.prototype.hasOwnProperty.call(config, 'threadId') ||
        Object.prototype.hasOwnProperty.call(config, 'thread_id');
      const cwd = requireCwd('composer.model.save');
      const state = get();
      const requestedThreadId = hasThreadTarget ? (config.threadId || config.thread_id) : state.activeThreadId;
      const threadId = threadConfigTargetIdForState(state, requestedThreadId);
      const existingConfig = threadId ? state.threadConfigByThread[threadId] : null;
      const threadConfig = existingConfig || (threadId ? await get().loadThreadConfig(threadId) : null);
      const nextModel = hasModel ? normalizeProviderConfigValue(config.model) : '';
      const nextEffort = hasEffort ? normalizeProviderConfigValue(config.effort) : '';
      if (threadId && !threadConfig) {
        notifyAction('线程配置加载失败，无法保存模型配置', 'error', { threadId });
        return false;
      }
      if (threadId && threadConfig?.supportsThreadOverride) {
        const provider = normalizeProviderName(threadConfig.provider) || DEFAULT_PROVIDER;
        set({ threadConfigSaving: true });
        try {
          const saved = await setThreadConfig({
            threadId,
            model: hasModel ? nextModel : normalizeProviderConfigValue(threadConfig.override.model),
            effort: hasEffort ? nextEffort : normalizeProviderConfigValue(threadConfig.override.effort),
          });
          const normalized = normalizeThreadConfig(saved, threadId, provider);
          set((current) => ({
            threadConfigByThread: {
              ...current.threadConfigByThread,
              [threadId]: normalized,
            },
            threadConfigSaving: false,
            actionNotice: actionNotice('线程配置已保存，下次发送生效。', 'success'),
          }));
          return true;
        } catch (error) {
          set({
            threadConfigSaving: false,
            actionNotice: actionNotice(`线程配置保存失败：${error.message}`, 'error'),
          });
          addWarning('error', 'thread.config.set.failed', { threadId, error: error.message });
          throw error;
        }
      }

      const provider = normalizeProviderName(state.provider) || DEFAULT_PROVIDER;
      const current = state.providerConfig || normalizeProviderRuntimeConfig({}, provider);
      const value = normalizeProviderRuntimeConfig({
        model: hasModel ? nextModel || current.model : current.model,
        effort: hasEffort ? nextEffort || current.effort : current.effort,
        codexModelProvider: current.codexModelProvider,
      }, provider);
      await setPreference({ cwd, key: providerPreferenceKey(provider, 'model'), value: value.model });
      await setPreference({ cwd, key: providerPreferenceKey(provider, 'effort'), value: value.effort });
      set({
        providerConfig: value,
        actionNotice: actionNotice('全局模型配置已保存', 'success'),
      });
      return true;
    },

    restoreComposerModelInheritance: async (config = {}) => {
      const state = get();
      const requestedThreadId = Object.prototype.hasOwnProperty.call(config, 'threadId') ||
        Object.prototype.hasOwnProperty.call(config, 'thread_id')
        ? (config.threadId || config.thread_id)
        : state.activeThreadId;
      const threadId = threadConfigTargetIdForState(state, requestedThreadId);
      if (!threadId) return false;
      const existingConfig = state.threadConfigByThread[threadId] || await get().loadThreadConfig(threadId);
      if (!existingConfig?.supportsThreadOverride) return false;
      const saved = await setThreadConfig({ threadId, model: '', effort: '' });
      const normalized = normalizeThreadConfig(saved, threadId, existingConfig.provider || state.provider);
      set((current) => ({
        threadConfigByThread: {
          ...current.threadConfigByThread,
          [threadId]: normalized,
        },
        actionNotice: actionNotice('已恢复继承全局默认', 'success'),
      }));
      return true;
    },

    saveComposerModelProvider: async (codexModelProvider) => {
      const key = providerPreferenceKey('codex', 'codexModelProvider');
      const value = requireProviderPreferenceValue(codexModelProvider, key, 'composer.modelProvider.save');
      const cwd = requireCwd('composer.modelProvider.save');
      await setPreference({ cwd, key, value });
      set((state) => ({
        providerConfig: normalizeProviderRuntimeConfig({
          ...state.providerConfig,
          codexModelProvider: value,
        }, state.provider || DEFAULT_PROVIDER),
        actionNotice: actionNotice('模型渠道已保存', 'success'),
      }));
      return true;
    },

    setActiveProjectPath: async (path) => {
      const target = normalizePath(path);
      if (!target) return false;
      const cwd = requireProjectScopeCwd('project.setActive');
      const previousActiveProject = normalizePath(get().activeProject);
      const previousProjects = Array.isArray(get().projects) ? [...get().projects] : [];
      try {
        saveActiveComposerDraft();
        const visibleProjects = Array.isArray(get().projects) ? get().projects.map(normalizePath).filter(Boolean) : [];
        if (target !== '.' && (previousActiveProject === '.' || !visibleProjects.includes(target))) {
          const addedProjects = await addProjectRPC({ cwd, path: target });
          applyProjects(addedProjects, cwd);
        }
        const optimisticProjects = target === '.' || visibleProjects.includes(target)
          ? previousProjects
          : [...new Set([...previousProjects, target])];
        set({
          projects: optimisticProjects,
          activeProject: target,
        });
        const optimisticCwd = target && target !== '.' ? target : cwd;
        refreshChatSurfaceForCwdInBackground(optimisticCwd);
        const projects = await setActiveProjectRPC({ cwd, path: target });
        applyProjects(projects, cwd);
        const selectedProject = normalizePath(get().activeProject);
        const selectedCwd = selectedProject && selectedProject !== '.' ? selectedProject : cwd;
        if (selectedCwd !== optimisticCwd) {
          refreshChatSurfaceForCwdInBackground(selectedCwd);
        }
        notifyAction(`已切换项目：${projectShortLabel(target)}`, 'success');
        return true;
      } catch (error) {
        set({
          activeProject: previousActiveProject,
          projects: previousProjects,
          chatSurfaceLoadingCwd: '',
        });
        notifyAction(`切换项目失败：${error.message}`, 'error');
        addWarning('error', 'project.set_active.failed', { path: target, error: error.message });
        throw error;
      }
    },

    addProjectFromPicker: async () => {
      const scopeCwd = requireProjectScopeCwd('project.add');
      const activeProject = normalizePath(get().activeProject);
      const seed = activeProject && activeProject !== '.' ? activeProject : scopeCwd;
      let selected = '';
      try {
        selected = normalizePath(await selectProjectDir(seed));
        if (!selected) {
          notifyAction('未选择项目', 'info');
          return false;
        }
        const projects = await addProjectRPC({ cwd: scopeCwd, path: selected });
        applyProjects(projects, scopeCwd);
        notifyAction(`已添加项目：${projectShortLabel(selected)}`, 'success');
        return true;
      } catch (error) {
        notifyAction(`添加项目失败：${error.message}`, 'error');
        addWarning('error', 'project.add.failed', { path: selected, error: error.message });
        throw error;
      }
    },

    openNewWindow: async () => {
      const scopeCwd = requireProjectScopeCwd('ui.open_new_window');
      const activeProject = normalizePath(get().activeProject);
      const seed = activeProject && activeProject !== '.' ? activeProject : scopeCwd;
      let selected = '';
      try {
        selected = normalizePath(await selectProjectDir(seed));
        if (!selected) {
          notifyAction('未选择新窗口目录', 'info');
          return false;
        }
        await openNewWindowRPC({ cwd: selected });
        notifyAction(`已打开新窗口：${projectShortLabel(selected)}`, 'success');
        return true;
      } catch (error) {
        notifyAction(`打开新窗口失败：${error.message}`, 'error');
        addWarning('error', 'ui.open_new_window.failed', { path: selected, error: error.message });
        throw error;
      }
    },

    removeProjectPath: async (path) => {
      const target = normalizePath(path);
      if (!target) return false;
      const cwd = requireProjectScopeCwd('project.remove');
      try {
        const projects = await removeProjectRPC({ cwd, path: target });
        applyProjects(projects, cwd);
        notifyAction(`已移除项目：${projectShortLabel(target)}`, 'success');
        return true;
      } catch (error) {
        notifyAction(`移除项目失败：${error.message}`, 'error');
        addWarning('error', 'project.remove.failed', { path: target, error: error.message });
        throw error;
      }
    },

    toggleProviderMode: async () => {
      const lockedThreadId = activeProviderLockedThreadId(get());
      if (lockedThreadId) {
        notifyAction('已开启的聊天不能更改 provider，请新建对话后切换', 'warning', { threadId: lockedThreadId });
        return false;
      }
      const current = normalizeProviderName(get().provider) || DEFAULT_PROVIDER;
      const next = current === 'claude' ? 'codex' : 'claude';
      const cwd = requireCwd('provider.toggle');
      await setPreference({ cwd, key: PROVIDER_ACTIVE_PREF_KEY, value: next });
      await loadProviderConfig(cwd, next);
      set({
        provider: next,
        actionNotice: actionNotice(`已切换为 ${next === 'claude' ? 'Claude' : 'Codex'}`, 'success'),
      });
      return true;
    },

    setActiveThread: async (threadId) => {
      const id = backendThreadIdForState(get(), threadId, { includeArchived: true });
      const current = get();
      saveActiveComposerDraft(current);
      const restored = restoreComposerDraft(current, id);
      if (!id) {
        set({
          activeThreadId: '',
          pendingActiveThreadId: '',
          draft: restored.draft,
          attachments: restored.attachments,
        });
        return;
      }
      const currentThreadId = backendThreadIdForState(current, current.activeThreadId);
      if (currentThreadId === id) {
        set({
          pendingActiveThreadId: '',
          draft: restored.draft,
          attachments: restored.attachments,
        });
        await get().syncThreadState(id, { includeArchived: true, includeDiff: false, loadDiffAfter: true });
        return;
      }
      set((state) => ({
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
        await get().syncThreadState(id, { includeArchived: true, includeDiff: false, loadDiffAfter: true });
      } catch (error) {
        set((state) => ({
          threadStateLoadingByThread: {
            ...state.threadStateLoadingByThread,
            [id]: false,
          },
        }));
        throw error;
      }
    },

    newThread: () => {
      const current = get();
      saveActiveComposerDraft(current);
      const restored = restoreComposerDraft(current, '');
      set({ activeThreadId: '', draft: restored.draft, attachments: restored.attachments, actionNotice: actionNotice('已创建新对话草稿', 'info') });
    },

    continueWithSharedFile: (path) => {
      const target = normalizeString(path);
      if (!target) return false;
      const attachment = { path: target, name: basename(target) };
      saveActiveComposerDraft();
      set((state) => ({
        activePage: 'chat',
        activeThreadId: '',
        draft: `请基于共享文件 ${target} 继续对话。`,
        attachments: state.attachments.some((item) => item.path === target)
          ? state.attachments
          : [attachment],
      }));
      return true;
    },

    selectFilesForComposer: async () => {
      try {
        const picked = await selectFiles();
        const attachments = (Array.isArray(picked) ? picked : [])
          .map(normalizeAttachment)
          .filter(Boolean);
        set((state) => ({
          attachments: appendUniqueAttachments(state.attachments, attachments),
          actionNotice: actionNotice(attachments.length > 0 ? `已添加 ${attachments.length} 个附件` : '未选择附件', attachments.length > 0 ? 'success' : 'info'),
        }));
        return attachments;
      } catch (error) {
        addWarning('error', 'attachments.select.failed', { error: error.message });
        throw error;
      }
    },

    attachPathsForComposer: (paths) => {
      const attachments = (Array.isArray(paths) ? paths : [])
        .map(normalizeAttachment)
        .filter(Boolean);
      set((state) => ({
        attachments: appendUniqueAttachments(state.attachments, attachments),
        actionNotice: actionNotice(
          attachments.length > 0 ? `已添加 ${attachments.length} 个附件` : '未找到可添加的附件路径',
          attachments.length > 0 ? 'success' : 'info',
        ),
      }));
      return attachments.length;
    },

    attachDroppedFilesForComposer: async (files) => {
      const list = fileListOf(files);
      if (list.length === 0) return 0;
      const attachments = [];
      const rejected = [];
      for (let index = 0; index < list.length; index += 1) {
        const file = list[index];
        const path = droppedFilePath(file);
        if (path) {
          attachments.push(normalizeFileAttachment(path));
          continue;
        }
        if (fileLooksImage(file)) {
          attachments.push(await imageFileAttachment(file, index, 'dropped-image'));
          continue;
        }
        rejected.push(normalizeString(file?.name) || `file-${index + 1}`);
      }
      set((state) => ({
        attachments: appendUniqueAttachments(state.attachments, attachments),
        actionNotice: actionNotice(
          attachments.length > 0
            ? `已添加 ${attachments.length} 个附件`
            : '无法添加无路径的非图片文件',
          attachments.length > 0 ? 'success' : 'error',
        ),
      }));
      if (rejected.length > 0) {
        addWarning('warn', 'attachments.drop.rejected_no_path', { files: rejected });
      }
      return attachments.length;
    },

    attachPastedImagesForComposer: async (files) => {
      const images = fileListOf(files).filter(fileLooksImage);
      if (images.length === 0) return 0;
      const attachments = [];
      for (let index = 0; index < images.length; index += 1) {
        attachments.push(await imageFileAttachment(images[index], index, 'pasted-image'));
      }
      set((state) => ({
        attachments: appendUniqueAttachments(state.attachments, attachments),
        actionNotice: actionNotice(`已添加 ${attachments.length} 张图片`, 'success'),
      }));
      return attachments.length;
    },

    removeAttachment: (path) => {
      const target = normalizeString(path);
      set((state) => ({
        attachments: state.attachments.filter((item) => attachmentKey(item) !== target && item.path !== target),
      }));
    },

    sendDraft: async () => {
      const cwd = requireCwd('send message');
      const text = normalizeString(get().draft);
      const attachments = get().attachments.map(normalizeAttachment).filter(Boolean);
      const input = buildTurnInput(text, attachments);
      if (input.length === 0) return false;

      const previousDraft = get().draft;
      const previousAttachments = get().attachments;
      const previousActiveThreadId = get().activeThreadId;
      const previousThreadId = reusableThreadIdForSend(get(), previousActiveThreadId);
      const launchIntentId = createLaunchIntentId();
      const provisionalThreadId = previousThreadId || launchIntentId;
      const optimisticItem = {
        id: `user-${launchIntentId}`,
        role: 'user',
        text,
        attachments,
        time: new Date().toISOString(),
        done: true,
        optimistic: true,
      };

      set((state) => ({
        sending: true,
        error: '',
        draft: '',
        attachments: [],
        actionNotice: actionNotice('消息已发送，等待回复', 'info'),
        activeThreadId: provisionalThreadId,
        activityThreadAtById: {
          ...state.activityThreadAtById,
          [provisionalThreadId]: threadActivityTimestamp(),
        },
        threads: previousThreadId && state.threads.some((thread) => threadMatchesIdentifier(thread, previousThreadId))
          ? [
            state.threads.find((thread) => threadMatchesIdentifier(thread, previousThreadId)),
            ...state.threads.filter((thread) => !threadMatchesIdentifier(thread, previousThreadId)),
          ]
          : state.threads,
        timelinesByThread: {
          ...state.timelinesByThread,
          [provisionalThreadId]: [
            ...(state.timelinesByThread[provisionalThreadId] || []),
            optimisticItem,
          ],
        },
      }));

      try {
        let threadId = previousThreadId;
        if (!threadId) {
          const launchPreferences = await resolveLaunchPreferences(cwd);
          const thread = await startThread({
            cwd,
            name: text.slice(0, 40),
            ...launchPreferences,
            deferSpawn: true,
            launchIntentId,
          });
          const identity = normalizeThreadIdentity(thread);
          threadId = identity.threadId;
          if (!threadId) throw new Error('thread/start response missing threadId');
          set((state) => {
            const provisionalTimeline = state.timelinesByThread[provisionalThreadId] || [];
            const timelinesByThread = { ...state.timelinesByThread };
            delete timelinesByThread[provisionalThreadId];
            timelinesByThread[threadId] = provisionalTimeline;
            const activityThreadAtById = { ...state.activityThreadAtById };
            if (activityThreadAtById[provisionalThreadId]) {
              activityThreadAtById[threadId] = activityThreadAtById[provisionalThreadId];
              delete activityThreadAtById[provisionalThreadId];
            }
            return {
              activeThreadId: threadId,
              provider: launchPreferences.modelProvider || launchPreferences.provider || DEFAULT_PROVIDER,
              activityThreadAtById,
              timelinesByThread,
              threads: [
                { id: threadId, agentId: identity.agentId, providerThreadId: identity.providerThreadId, sessionId: identity.sessionId, name: text.slice(0, 40) || '新对话', provider: launchPreferences.modelProvider || launchPreferences.provider || DEFAULT_PROVIDER, status: '工作中' },
                ...state.threads.filter((item) => item.id !== threadId),
              ],
            };
          });
        }

        await startTurnWithRecover({
          cwd,
          threadId,
          input,
          manualSkillSelection: false,
        });
        clearComposerDraft({ ...get(), activeThreadId: previousActiveThreadId }, previousActiveThreadId);
        clearComposerDraft(get(), provisionalThreadId);
        clearComposerDraft(get(), threadId);
        set({ sending: false });
        return true;
      } catch (error) {
        const stateBeforeRollback = get();
        const createdThreadId = !previousThreadId && stateBeforeRollback.activeThreadId !== provisionalThreadId
          ? backendThreadIdForState(stateBeforeRollback, stateBeforeRollback.activeThreadId)
          : '';
        set((state) => {
          const timelinesByThread = { ...state.timelinesByThread };
          const activeTimeline = timelinesByThread[state.activeThreadId] || [];
          timelinesByThread[state.activeThreadId] = activeTimeline.filter((item) => item.id !== optimisticItem.id);
          if (!previousThreadId) {
            delete timelinesByThread[provisionalThreadId];
          }
          return {
            sending: false,
            draft: previousDraft,
            attachments: previousAttachments,
            activeThreadId: previousActiveThreadId,
            timelinesByThread,
            error: error.message,
            actionNotice: actionNotice(`发送失败：${error.message}`, 'error'),
          };
        });
        if (createdThreadId) {
          try {
            await deleteThreadRPC({ threadId: createdThreadId });
          } catch (cleanupError) {
            addWarning('warn', 'thread.provisional.delete.failed', {
              threadId: createdThreadId,
              error: cleanupError.message || String(cleanupError),
            });
          }
        }
        addWarning('error', 'thread.send.failed', { error: error.message });
        throw error;
      }
    },

    interruptActiveThread: () => activeThreadRPC('thread.interrupt', interruptTurn),
    compactActiveThread: () => activeThreadRPC('thread.compact', compactThread),
    recoverActiveThread: () => activeThreadRPC('thread.recover', recoverThread),

    hasActiveThreadActions: () => Boolean(backendThreadIdForState(get(), get().activeThreadId)),

    refreshActiveThreadStatus: async () => {
      const threadId = backendThreadIdForState(get(), get().activeThreadId);
      if (!threadId) return false;
      await get().syncThreadState(threadId);
      notifyAction('线程状态已刷新', 'success', { threadId });
      return true;
    },

    copyActiveThreadInfo: async () => {
      const state = get();
      const threadId = backendThreadIdForState(state, state.activeThreadId);
      if (!threadId) {
        notifyAction('当前没有可复制的后端线程', 'warning');
        return false;
      }
      const preparedClipboardWrite = beginTextClipboardWrite();
      const thread = state.threads.find((item) => item.id === threadId) || {};
      const cwd = requireCwd('thread.copy');
      let identity;
      try {
        identity = await resolveThreadIdentity({ cwd, threadId });
      } catch (error) {
        preparedClipboardWrite?.cancel?.(error);
        notifyAction(`复制失败：线程信息接口调用失败：${error.message || String(error)}`, 'warning', { threadId });
        addWarning('warn', 'thread.identity.resolve.failed', { threadId, error: error.message || String(error) });
        return false;
      }
      if (!identity || typeof identity !== 'object' || Array.isArray(identity)) {
        preparedClipboardWrite?.cancel?.();
        notifyAction('复制失败：线程信息接口返回值不是 JSON 对象', 'warning', { threadId });
        addWarning('warn', 'thread.identity.resolve.invalid', { threadId });
        return false;
      }
      const threadConfig = state.threadConfigByThread[threadId] || await get().loadThreadConfig(threadId);
      const payload = buildThreadCopyPayload({ state: get(), threadId, thread, identity, threadConfig });
      try {
        const text = JSON.stringify(payload, null, 2);
        const copyFailures = [];
        if (preparedClipboardWrite?.commit) {
          try {
            await preparedClipboardWrite.commit(text);
            notifyAction('线程信息已复制', 'success', { threadId });
            return true;
          } catch (error) {
            copyFailures.push(`prepared clipboard write failed: ${error.message || String(error)}`);
            addWarning('warn', 'thread.copy.prepared_clipboard.failed', { threadId, error: error.message || String(error) });
          }
        }
        try {
          await copyTextToClipboard(text);
        } catch (error) {
          if (copyFailures.length > 0) {
            throw new Error(`${copyFailures.join('; ')}; fallback copy failed: ${error.message || String(error)}`, { cause: error });
          }
          throw error;
        }
        notifyAction('线程信息已复制', 'success', { threadId });
        return true;
      } catch (error) {
        notifyAction(`复制失败：${error.message || String(error)}`, 'warning', { threadId });
        addWarning('warn', 'thread.copy.clipboard.failed', { threadId, error: error.message || String(error) });
        return false;
      }
    },

    renameThread: async (threadId, name) => {
      const id = backendThreadIdForState(get(), threadId);
      const nextName = normalizeString(name);
      if (!id || !nextName) return false;
      await renameThreadRPC({ threadId: id, name: nextName });
      set((state) => ({
        threads: state.threads.map((thread) => (thread.id === id ? { ...thread, name: nextName } : thread)),
        actionNotice: actionNotice('线程已重命名', 'success'),
      }));
      return true;
    },

    toggleThreadPin: async (threadId) => {
      const id = backendThreadIdForArchiveState(get(), threadId);
      if (!id) return false;
      const cwd = requireCwd('thread.pin');
      const currentMap = normalizeTimestampMap(get().pinnedThreadAtById);
      const pinned = currentMap[id] > 0;
      const nextMap = { ...currentMap };
      if (pinned) {
        delete nextMap[id];
      } else {
        nextMap[id] = Date.now();
      }
      await setPreference({
        cwd,
        key: THREAD_PINS_CHAT_PREF_KEY,
        value: nextMap,
      });
      set((state) => ({
        pinnedThreadAtById: nextMap,
        threads: state.threads.map((thread) => (thread.id === id ? {
          ...thread,
          pinned: !pinned,
          pinnedAt: nextMap[id] || 0,
        } : thread)),
        actionNotice: actionNotice(pinned ? '会话已取消置顶' : '会话已置顶', 'success'),
      }));
      return true;
    },

    archiveThread: async (threadId, archived) => {
      const id = backendThreadIdForArchiveState(get(), threadId);
      if (!id) return false;
      const cwd = requireCwd('thread.archive');
      if (archived) {
        await archiveThreadRPC({ threadId: id });
      } else {
        await unarchiveThreadRPC({ threadId: id });
      }
      const archivedAt = archived ? Date.now() : 0;
      await setPreference({
        cwd,
        key: `archivedThreadAtById.${id}`,
        value: archivedAt > 0 ? archivedAt : null,
      });
      set((state) => ({
        activeThreadId: archived && normalizeThreadId(state.activeThreadId) === id ? '' : state.activeThreadId,
        threads: state.threads.map((thread) => (thread.id === id ? {
          ...thread,
          archived: Boolean(archived),
          archivedAt,
          status: archived ? 'archived' : (isArchivedStatus(thread.status) ? 'created' : thread.status),
        } : thread)),
        actionNotice: actionNotice(archived ? '线程已归档' : '线程已恢复到列表', 'success'),
      }));
      return true;
    },

    deleteStaleThreads: async (threadIds) => {
      const ids = [...new Set((Array.isArray(threadIds) ? threadIds : [])
        .map((threadId) => backendThreadIdForArchiveState(get(), threadId))
        .filter(Boolean))];
      if (ids.length === 0) return { deleted: 0, failed: 0 };
      const cwd = requireCwd('thread.delete');
      const deletedIds = [];
      const failedIds = [];
      for (const id of ids) {
        try {
          await deleteThreadRPC({ threadId: id });
          deletedIds.push(id);
        } catch (error) {
          failedIds.push(id);
          addWarning('warn', 'thread.delete.failed', { threadId: id, error: error.message || String(error) });
        }
      }
      if (deletedIds.length > 0) {
        await Promise.all(deletedIds.map((id) => setPreference({
          cwd,
          key: `archivedThreadAtById.${id}`,
          value: null,
        })));
        const deletedSet = new Set(deletedIds);
        set((state) => ({
          activeThreadId: deletedSet.has(state.activeThreadId) ? '' : state.activeThreadId,
          threads: state.threads.filter((thread) => !deletedSet.has(thread.id)),
          actionNotice: actionNotice(
            failedIds.length > 0
              ? `已删除 ${deletedIds.length} 个无用会话，${failedIds.length} 个失败`
              : `已删除 ${deletedIds.length} 个无用会话`,
            failedIds.length > 0 ? 'warning' : 'success',
          ),
        }));
      } else {
        set({
          actionNotice: actionNotice(`删除无用会话失败：${failedIds.length} 个失败`, 'error'),
        });
      }
      return { deleted: deletedIds.length, failed: failedIds.length };
    },

    addWarning,
    addLog,
    setLogLevel,
  };
});

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
