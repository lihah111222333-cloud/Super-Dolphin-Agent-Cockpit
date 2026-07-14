
import { normalizeOptionalTextField, currentIsoTimestamp, parseRequiredTimestamp } from '../../contractStoreModel.js';
import {
  emitFrontendTraceEvent,
} from '../../../../../shared/api/backendApi.js';
import {
  ACTIVE_PROMPT_PREF_KEY,
} from '../bridgeRevision.js';
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
} from '../providerRuntimeConfig.js';
import {
  RUNTIME_PROVIDER,
  normalizeKnownProviderName,
  providerPreferenceKey,
} from '../providerPreferences.js';
import { normalizeThreadId } from '../threadIdentity.js';

function optionalUiArray() {
  return [];
}

function optionalUiObject() {
  return {};
}

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
let clientStoreClockMillisForTests = null;
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
  return normalizeOptionalTextField(value);
}

function clockNowMillis() {
  if (clientStoreClockMillisForTests) return clientStoreClockMillisForTests();
  return performance.timeOrigin + performance.now();
}

function clockNowISO() {
  return currentIsoTimestamp();
}

function parseTimestampMillis(value) {
  return parseRequiredTimestamp(value);
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
  if (!key) return state.sidebarThreadsByProject || optionalUiObject();
  return {
    ...(state.sidebarThreadsByProject || optionalUiObject()),
    [key]: Array.isArray(threads) ? threads : [],
  };
}

function sidebarThreadsByProjectUpsert(state, projectPath, thread) {
  const key = sidebarProjectKey(projectPath);
  if (!key || !thread?.id) return state.sidebarThreadsByProject || optionalUiObject();
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
  const parsed = parseTimestampMillis(sanitized);
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

async function getScopedPreference(getPreference, cwd, key) {
  const scope = normalizeString(cwd);
  if (scope) {
    const scoped = await getPreference(
      { cwd: scope, key },
      { allowTombstone: true },
    );
    if (isPreferenceTombstone(scoped)) return '';
    if (!isPreferenceAbsent(scoped)) return scoped;
  }
  const globalValue = await getPreference(
    { key },
    { allowTombstone: true },
  );
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

async function resolveLaunchPreferences(cwd, addWarning = null, getPreference) {
  const activeProviderValue = await getPreference({
    cwd,
    key: PROVIDER_ACTIVE_PREF_KEY,
  });
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
    getScopedPreference(getPreference, cwd, providerPreferenceKey('codex', 'codexHome')),
    getScopedPreference(getPreference, cwd, providerPreferenceKey('codex', 'codexInstanceKey')),
    getScopedPreference(getPreference, cwd, providerPreferenceKey('codex', 'codexModelProvider')),
    getScopedPreference(getPreference, cwd, providerPreferenceKey(provider, 'sandbox')),
    getScopedPreference(getPreference, cwd, providerPreferenceKey(provider, 'approvalPolicy')),
    getScopedPreference(getPreference, cwd, providerPreferenceKey(provider, 'personality')),
    getScopedPreference(getPreference, cwd, providerPreferenceKey(provider, 'summary')),
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

function resolveInitialLevel() {
  try {
    if (typeof localStorage !== 'undefined') {
      return localStorage.getItem('agent-orchestrator.log.level') || 'info';
    }
  }
  catch (error) {
    emitFrontendTraceEvent({
      phase: 'frontend.log_level.preference_read.failed',
      status: 'error',
      error: error?.message || String(error),
    });
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


export function setClientStoreClockMillisForTestsValue(clock) {
  clientStoreClockMillisForTests = clock;
}

export function resetClientStoreClockMillisForTests() {
  clientStoreClockMillisForTests = null;
}

export {
  AGENT_IDENTITY_KEYS,
  APP_PAGE_IDS,
  ASSISTANT_DELTA_FLUSH_MS,
  BOOTSTRAP_PAGE_ALIASES,
  BRIDGE_PATCH_SLOW_MS,
  DEFAULT_PROVIDER,
  ROOT_THREAD_IDENTITY_KEYS,
  THREAD_IDENTITY_KEYS,
  THREAD_PINS_CHAT_PREF_KEY,
  baseState,
  cleanObject,
  clockNowISO,
  clockNowMillis,
  emptyForkDraft,
  firstFieldValue,
  firstValueFromSources,
  getScopedPreference,
  normalizeBootstrapPage,
  normalizeBootstrapSnapshot,
  normalizePath,
  normalizeProviderName,
  normalizeString,
  normalizeThreadConfig,
  normalizeTimestamp,
  normalizeTimestampMap,
  objectPrototype,
  objectRecord,
  optionalUiArray,
  optionalUiObject,
  projectShortLabel,
  resolveInitialLevel,
  resolveLaunchPreferences,
  sanitizeLaunchSandboxPreference,
  sidebarProjectKey,
  sidebarThreadsByProjectUpsert,
  sidebarThreadsByProjectWith,
  stateWithPatch,
  mapSidebarThreadCache,
};
