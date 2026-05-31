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
  readConfig,
  recoverThread,
  registerBridgeLogStore,
  renameThread as renameThreadRPC,
  resolveThreadIdentity,
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
const PROVIDER_ACTIVE_PREF_KEY = 'settings.provider.active';
const ACTIVE_PROMPT_PREF_KEY = 'settings.activePromptKey';
const CODEX_IDENTITY_DEFAULTS = Object.freeze({
  codexHome: '~/.codex',
  codexInstanceKey: 'default',
  codexModelProvider: 'openai',
});
const PROVIDER_DEFAULT_CONFIGS = Object.freeze({
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
  const parsed = Date.parse(text);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : 0;
}

function normalizeProviderName(value) {
  const provider = normalizeProviderConfigValue(value).toLowerCase();
  if (!provider) return '';
  if (provider === 'codex' || provider === 'claude') return provider;
  throw new Error(`invalid provider preference: ${normalizeProviderConfigValue(value)}`);
}

function providerPreferenceScope(provider) {
  return provider === 'codex' ? 'codex' : 'claude';
}

function providerPreferenceKey(provider, suffix) {
  return `settings.provider.${provider}.${suffix}`;
}

function normalizeCodexIdentityValue(value, fallback) {
  if (typeof value === 'boolean') return fallback;
  return normalizeProviderConfigValue(value) || fallback;
}

function defaultProviderConfig(provider) {
  return PROVIDER_DEFAULT_CONFIGS[provider] || PROVIDER_DEFAULT_CONFIGS[DEFAULT_PROVIDER];
}

function normalizeProviderRuntimeConfig(raw = {}, providerValue = DEFAULT_PROVIDER) {
  const provider = normalizeProviderName(providerValue) || DEFAULT_PROVIDER;
  const defaults = defaultProviderConfig(provider);
  return {
    provider,
    model: normalizeProviderConfigValue(raw.model) || defaults.model,
    effort: normalizeProviderConfigValue(raw.effort) || defaults.effort,
    codexModelProvider: normalizeCodexIdentityValue(raw.codexModelProvider, CODEX_IDENTITY_DEFAULTS.codexModelProvider),
  };
}

function normalizeThreadConfig(raw = {}, fallbackThreadId = '', fallbackProvider = DEFAULT_PROVIDER) {
  const source = raw && typeof raw === 'object' && !Array.isArray(raw) ? raw : {};
  const provider = normalizeProviderName(source.provider || fallbackProvider) || DEFAULT_PROVIDER;
  const defaults = defaultProviderConfig(provider);
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
    launch.config = {
      codexHome: normalizeCodexIdentityValue(codexHome, CODEX_IDENTITY_DEFAULTS.codexHome),
      codexInstanceKey: normalizeCodexIdentityValue(codexInstanceKey, CODEX_IDENTITY_DEFAULTS.codexInstanceKey),
      codexModelProvider: normalizeCodexIdentityValue(codexModelProvider, CODEX_IDENTITY_DEFAULTS.codexModelProvider),
    };
  }
  return launch;
}

function basename(path) {
  const value = normalizeString(path);
  return value.split(/[\\/]/).filter(Boolean).pop() || value;
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
function isArchivedStatus(value) {
  const status = normalizeString(value).toLowerCase();
  return status === 'archived' || status === '归档' || status === '已归档';
}

function archiveMapFromPayload(payload = {}) {
  const direct = payload['threadArchives.chat'] || payload.threadArchivesChat || payload.archivedThreadAtById;
  const nested = payload.threadArchives?.chat || payload.thread_archives?.chat;
  const value = direct || nested || {};
  return value && typeof value === 'object' && !Array.isArray(value) ? value : {};
}

function normalizeThread(raw, options = {}) {
  const identity = normalizeThreadIdentity(raw);
  const status = normalizeString(raw?.status || raw?.state || raw?.lifecycleStatus || raw?.lifecycle_status || raw?.threadStatus || raw?.thread_status) || '等待指示';
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
    name: normalizeString(raw?.name || raw?.title || raw?.displayName || raw?.summary) || '新对话',
    provider: normalizeString(raw?.provider || raw?.agentKey || raw?.agent_key) || DEFAULT_PROVIDER,
    status,
    lastMessage: normalizeString(raw?.lastMessage || raw?.last_message || raw?.preview),
    updatedAt: normalizeString(raw?.updatedAt || raw?.updated_at || raw?.createdAt || raw?.created_at),
    pinned: Boolean(raw?.pinned || raw?.isPinned),
    archived: Boolean(raw?.archived || raw?.isArchived || archivedAt > 0 || isArchivedStatus(status) || isArchivedStatus(lifecycleStatus)),
    archivedAt,
  };
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

function backendThreadIdForState(state, value) {
  const id = normalizeThreadId(value);
  if (!id) return '';
  const matchedThread = state.threads.find((thread) => threadMatchesIdentifier(thread, id));
  if (matchedThread) return matchedThread.archived ? '' : normalizeBackendThreadId(matchedThread.id);
  if (isAgentRuntimeId(id)) return '';
  return normalizeBackendThreadId(id);
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

function normalizeTokenUsage(value) {
  if (!value || typeof value !== 'object') return null;
  const usedTokens = Number(value.usedTokens ?? value.used_tokens ?? value.totalTokens ?? value.total_tokens ?? 0) || 0;
  const contextWindowTokens = Number(value.contextWindowTokens ?? value.context_window_tokens ?? value.contextWindow ?? value.context_window ?? 0) || 0;
  const usedPercent = Number(value.usedPercent ?? value.used_percent ?? (contextWindowTokens > 0 ? (usedTokens / contextWindowTokens) * 100 : 0)) || 0;
  return { usedTokens, contextWindowTokens, usedPercent };
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
    return extractText(value.text || value.content || value.message || value.delta);
  }
  return '';
}

function normalizeTimelineItem(item) {
  const role = normalizeString(item?.role || item?.kind || item?.type).toLowerCase();
  const normalizedRole = role.includes('user') ? 'user' : 'assistant';
  return {
    id: normalizeString(item?.id || item?.messageId || item?.message_id) || `${normalizedRole}-${Date.now()}`,
    role: normalizedRole,
    text: extractText(item?.text || item?.content || item?.message || item?.delta),
    time: normalizeString(item?.time || item?.ts || item?.createdAt || item?.created_at) || new Date().toISOString(),
    done: item?.done !== false,
    optimistic: Boolean(item?.optimistic),
  };
}

function sameTimelineContent(left, right) {
  return left?.role === right?.role && normalizeString(left?.text) === normalizeString(right?.text);
}

function mergeTimelineItems(existingItems = [], incomingItems = []) {
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
      (existingItem.role === 'user' || existingItem.optimistic || existingItem.runtime) &&
      !incomingIds.has(existingItem.id) &&
      !incomingItems.some((incomingItem) => sameTimelineContent(existingItem, incomingItem))
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

  return merged;
}

function runtimeThreadIdentifier(payload = {}) {
  return payload.threadId ||
    payload.thread_id ||
    payload.agentId ||
    payload.agent_id ||
    payload._threadPatch?.threadId ||
    payload._threadPatch?.thread_id ||
    payload._threadPatch?.agentId ||
    payload._threadPatch?.agent_id;
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

function mergeRuntimeAssistantCompletion(existingItems = [], completion) {
  if (!completion?.item) return existingItems;
  const finalItem = completion.item;
  const dropIds = new Set([finalItem.id, completion.streamId].filter(Boolean));
  const withoutReplaced = existingItems.filter((item) => !dropIds.has(item.id));
  if (!completion.explicitId) {
    const duplicate = withoutReplaced.find((item) => (
      item.role === 'assistant' &&
      item.done !== false &&
      sameTimelineContent(item, finalItem)
    ));
    if (duplicate) return withoutReplaced;
  }
  return [...withoutReplaced, finalItem];
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
    const path = normalizeString(value);
    return path ? { path, name: basename(path) } : null;
  }
  if (!value || typeof value !== 'object') return null;
  const path = normalizeString(value.path || value.url);
  if (!path) return null;
  return {
    path,
    name: normalizeString(value.name) || basename(path),
    kind: normalizeString(value.kind),
    previewUrl: normalizeString(value.previewUrl || value.url),
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

function isRecoverableTurnStartError(error) {
  const message = normalizeString(error?.message || error).toLowerCase();
  return message.includes('session is not available') || message.includes('session not found');
}

async function startTurnWithRecover(payload) {
  try {
    return await startTurn(payload);
  } catch (error) {
    if (!isRecoverableTurnStartError(error)) throw error;
    await recoverThread({ cwd: payload.cwd, threadId: payload.threadId });
    return startTurn(payload);
  }
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

function isNextSequence(previous, next) {
  try {
    return BigInt(normalizeString(next)) === BigInt(normalizeString(previous)) + 1n;
  } catch {
    return true;
  }
}

function resolveInitialLevel() {
  try {
    if (typeof localStorage !== 'undefined') {
      return localStorage.getItem('agent-orchestrator.log.level') || 'info';
    }
  } catch {}
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
  skillRevision: 0,
  threads: [],
  statuses: {},
  activeThreadId: '',
  threadConfigByThread: {},
  threadConfigLoadingByThread: {},
  threadConfigSaving: false,
  timelinesByThread: {},
  tokenUsageByThread: {},
  diffTextByThread: {},
  activityEntries: [],
  warningEntries: [],
  draft: '',
  attachments: [],
  sending: false,
  rightPanelWidth: 520,
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

  const addWarning = (level, event, fields = {}) => {
    if (level !== 'warn' && level !== 'error') return;
    const entry = {
      id: `${event}-${Date.now()}-${Math.random().toString(16).slice(2)}`,
      timestamp: new Date().toISOString(),
      level,
      event,
      fields,
    };
    set((state) => ({
      warningEntries: [entry, ...state.warningEntries].slice(0, MAX_WARNING_ENTRIES),
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
    } catch {}
    set({ logLevel: level });
  };

  const requireCwd = (reason) => {
    const cwd = normalizePath(get().activeProject) || normalizePath(get().cwd);
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

  const loadProviderConfig = async (cwdValue, providerValue) => {
    const cwd = normalizePath(cwdValue) || requireCwd('provider.config');
    const provider = normalizeProviderName(providerValue || get().provider) || DEFAULT_PROVIDER;
    const providerScope = providerPreferenceScope(provider);
    const [model, effort, codexModelProvider] = await Promise.all([
      getPreference({ cwd, key: providerPreferenceKey(providerScope, 'model') }),
      getPreference({ cwd, key: providerPreferenceKey(providerScope, 'effort') }),
      providerScope === 'codex'
        ? getPreference({ cwd, key: providerPreferenceKey('codex', 'codexModelProvider') })
        : Promise.resolve(CODEX_IDENTITY_DEFAULTS.codexModelProvider),
    ]);
    const providerConfig = normalizeProviderRuntimeConfig({ model, effort, codexModelProvider }, provider);
    set({ provider, providerConfig });
    return providerConfig;
  };

  const applySnapshot = (payload = {}, options = {}) => {
    const preferredActiveThreadId = normalizeThreadId(options.preferredActiveThreadId);
    set((state) => {
      const archivedAtById = archiveMapFromPayload(payload);
      const incomingThreads = Array.isArray(payload.threads)
        ? payload.threads.map((thread) => normalizeThread(thread, { archivedAtById })).filter((thread) => thread.id)
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
      const activeThreadId = (
        preservedActiveThreadId ||
        backendThreadIdFromThreads(preferredActiveThreadId, nextThreads) ||
        backendThreadIdFromThreads(snapshotActive, nextThreads) ||
        backendThreadIdFromThreads(state.activeThreadId, nextThreads) ||
        (!nextThreads.some((thread) => threadMatchesIdentifier(thread, preferredActiveThreadId)) ? normalizeBackendThreadId(preferredActiveThreadId) : '') ||
        (!nextThreads.some((thread) => threadMatchesIdentifier(thread, snapshotActive)) ? normalizeBackendThreadId(snapshotActive) : '') ||
        (!nextThreads.some((thread) => threadMatchesIdentifier(thread, state.activeThreadId)) ? normalizeBackendThreadId(state.activeThreadId) : '') ||
        selectableThreads[0]?.id ||
        ''
      );

      const timelinesByThread = {};
      for (const [threadId, items] of Object.entries(state.timelinesByThread)) {
        const canonicalId = canonicalizeThreadKey(threadId, nextThreads);
        timelinesByThread[canonicalId] = items;
      }
      const incomingTimelines = payload.timelinesByThread || payload.timelines_by_thread;
      if (incomingTimelines && typeof incomingTimelines === 'object') {
        for (const [threadId, items] of Object.entries(incomingTimelines)) {
          if (Array.isArray(items)) {
            const canonicalId = canonicalizeThreadKey(threadId, nextThreads);
            timelinesByThread[canonicalId] = items.map(normalizeTimelineItem);
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

      return {
        activeThreadId,
        threads: nextThreads,
        timelinesByThread,
        tokenUsageByThread,
        diffTextByThread,
        statuses: {
          ...state.statuses,
          ...(payload.statuses || {}),
        },
      };
    });
  };

  const loadThreadMessages = async (threadId) => {
    const id = backendThreadIdForState(get(), threadId);
    if (!id) return;
    try {
      const res = await getThreadMessages({ threadId: id, limit: 300 });
      if (!Array.isArray(res?.messages) || res.messages.length === 0) return;
      set((state) => ({
        timelinesByThread: {
          ...state.timelinesByThread,
          [id]: res.messages.map((message) => normalizeTimelineItem({
            id: message.id,
            role: message.role,
            text: message.content,
            createdAt: message.createdAt || message.created_at,
          })),
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
    return backendThreadIdForState(get(), identifier) || normalizeBackendThreadId(identifier);
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
        addWarning('warn', 'thread.patch.stale', { threadId, sequence, previousSequence });
        return;
      }
      if (previousSequence && !isNextSequence(previousSequence, sequence)) {
        addWarning('warn', 'thread.patch.gap', { threadId, sequence, previousSequence });
      }
      sequencesByThread.set(threadId, sequence);
    }

    const timelineItems = payload.timelineItems || payload.timeline_items;
    const tokenUsage = normalizeTokenUsage(payload.tokenUsage || payload.token_usage);
    const diffText = typeof payload.diffText === 'string' ? payload.diffText : payload.diff_text;
    const rawRuntime = payload.agentRuntime || payload.agent_runtime || {};
    const rawThread = payload.thread && typeof payload.thread === 'object' ? payload.thread : {};
    const statusText = normalizeString(payload.statusHeader || payload.status || rawThread.state || rawThread.status);
    const patchedThread = normalizeThread({
      ...rawThread,
      threadId,
      agentId: rawRuntime.agentId || rawRuntime.agent_id || rawThread.agentId || rawThread.agent_id,
      providerThreadId: rawRuntime.providerThreadId || rawRuntime.provider_thread_id || rawThread.providerThreadId || rawThread.provider_thread_id,
      provider: rawRuntime.provider || rawThread.provider,
      lastMessage: rawRuntime.lastMessage || rawRuntime.last_message || payload.statusDetails || payload.status_details || rawThread.lastMessage,
      status: statusText || rawThread.status,
    });

    set((state) => {
      const timelinesByThread = { ...state.timelinesByThread };
      if (Array.isArray(timelineItems)) {
        timelinesByThread[threadId] = mergeTimelineItems(
          timelinesByThread[threadId] || [],
          timelineItems
            .map(normalizeTimelineItem)
            .filter((item) => item.role === 'user' || normalizeString(item.text)),
        );
      }

      const tokenUsageByThread = { ...state.tokenUsageByThread };
      if (tokenUsage) tokenUsageByThread[threadId] = tokenUsage;

      const diffTextByThread = { ...state.diffTextByThread };
      if (typeof diffText === 'string') diffTextByThread[threadId] = diffText;

      const existingThread = state.threads.find((thread) => threadMatchesIdentifier(thread, threadId));
      const threads = patchedThread.id ? [
        {
          ...(existingThread || {}),
          ...patchedThread,
          name: patchedThread.name === '新对话' ? (existingThread?.name || patchedThread.name) : patchedThread.name,
          status: statusText || patchedThread.status || existingThread?.status || '等待指示',
          archived: Boolean(existingThread?.archived || patchedThread.archived),
        },
        ...state.threads.filter((thread) => !threadMatchesIdentifier(thread, threadId)),
      ] : state.threads;

      return {
        threads,
        timelinesByThread,
        tokenUsageByThread,
        diffTextByThread,
        statuses: {
          ...state.statuses,
          [threadId]: cleanObject({
            status: payload.status,
            statusHeader: payload.statusHeader,
            statusDetails: payload.statusDetails || payload.status_details,
            interruptible: payload.interruptible,
            activityStats: payload.activityStats || payload.activity_stats,
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
    if (method === 'ui/thread/patch') {
      applyBridgePatch(method, payload);
      return;
    }
    if (eventName === 'item/agentmessage/delta' || eventName === 'item/agent_message/delta') {
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
    if (eventName === 'rpc.failed' || eventName.endsWith('/failed')) {
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
    try {
      await rpc({ cwd, threadId });
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
        const activeProvider = normalizeProviderName(await getPreference({ cwd: scopedCwd, key: PROVIDER_ACTIVE_PREF_KEY }));
        set({
          cwd,
          projectScopeCwd: scopedCwd,
          activeProject: scopedCwd,
          provider: activeProvider || DEFAULT_PROVIDER,
          ...(bootstrapPage ? { activePage: bootstrapPage } : {}),
        });
        await loadProviderConfig(scopedCwd, activeProvider || DEFAULT_PROVIDER);
        const projects = await getProjects({ cwd: scopedCwd });
        applyProjects(projects, scopedCwd);
        const sidebar = await getSidebarState({ cwd: scopedCwd });
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

    syncThreadState: async (threadId) => {
      const id = backendThreadIdForState(get(), threadId);
      if (!id) return false;
      const cwd = requireCwd('thread.sync');
      const activeAtRequest = get().activeThreadId;
      const snapshot = await getThreadState({ cwd, threadId: id, includeDiff: true });
      const activeChanged = normalizeThreadId(get().activeThreadId) !== normalizeThreadId(activeAtRequest);
      applySnapshot(snapshot, { preferredActiveThreadId: id, preserveActiveThreadId: activeChanged });
      await loadThreadMessages(id);
      if (!activeChanged) await get().loadThreadConfig(id);
      return true;
    },

    setActivePage: (activePage) => set({ activePage }),
    setDraft: (draft) => set({ draft }),
    setPermission: (permission) => set({ permission }),
    setRightPanelWidth: (rightPanelWidth) => set({ rightPanelWidth }),

    refreshProviderConfig: async () => {
      const cwd = requireCwd('provider.config');
      const provider = normalizeProviderName(get().provider) || DEFAULT_PROVIDER;
      return loadProviderConfig(cwd, provider);
    },

    loadThreadConfig: async (threadId) => {
      const id = backendThreadIdForState(get(), threadId);
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
        }));
        return config;
      } catch (error) {
        set((state) => ({
          threadConfigLoadingByThread: {
            ...state.threadConfigLoadingByThread,
            [id]: false,
          },
        }));
        addWarning('error', 'thread.config.get.failed', { threadId: id, error: error.message });
        return null;
      }
    },

    saveComposerModelConfig: async (config = {}) => {
      const hasModel = Object.prototype.hasOwnProperty.call(config, 'model');
      const hasEffort = Object.prototype.hasOwnProperty.call(config, 'effort');
      const cwd = requireCwd('composer.model.save');
      const state = get();
      const threadId = backendThreadIdForState(state, state.activeThreadId);
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

    restoreComposerModelInheritance: async () => {
      const state = get();
      const threadId = backendThreadIdForState(state, state.activeThreadId);
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
      const value = normalizeCodexIdentityValue(codexModelProvider, CODEX_IDENTITY_DEFAULTS.codexModelProvider);
      const cwd = requireCwd('composer.modelProvider.save');
      await setPreference({ cwd, key: providerPreferenceKey('codex', 'codexModelProvider'), value });
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
      try {
        const projects = await setActiveProjectRPC({ cwd, path: target });
        applyProjects(projects, cwd);
        notifyAction(`已切换项目：${projectShortLabel(target)}`, 'success');
        return true;
      } catch (error) {
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
      const id = backendThreadIdForState(get(), threadId);
      set({ activeThreadId: id });
      if (id) await get().syncThreadState(id);
    },

    newThread: () => {
      set({ activeThreadId: '', draft: '', attachments: [], actionNotice: actionNotice('已创建新对话草稿', 'info') });
    },

    continueWithSharedFile: (path) => {
      const target = normalizeString(path);
      if (!target) return false;
      const attachment = { path: target, name: basename(target) };
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
          attachments: [...state.attachments, ...attachments],
          actionNotice: actionNotice(attachments.length > 0 ? `已添加 ${attachments.length} 个附件` : '未选择附件', attachments.length > 0 ? 'success' : 'info'),
        }));
        return attachments;
      } catch (error) {
        addWarning('error', 'attachments.select.failed', { error: error.message });
        throw error;
      }
    },

    removeAttachment: (path) => {
      const target = normalizeString(path);
      set((state) => ({
        attachments: state.attachments.filter((item) => item.path !== target),
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
      const previousThreadId = backendThreadIdForState(get(), get().activeThreadId);
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
            return {
              activeThreadId: threadId,
              provider: launchPreferences.modelProvider || launchPreferences.provider || DEFAULT_PROVIDER,
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
        set({ sending: false });
        return true;
      } catch (error) {
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
            activeThreadId: previousThreadId,
            timelinesByThread,
            error: error.message,
            actionNotice: actionNotice(`发送失败：${error.message}`, 'error'),
          };
        });
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
      const thread = state.threads.find((item) => item.id === threadId) || {};
      let identity = {};
      try {
        identity = await resolveThreadIdentity({ cwd: requireCwd('thread.copy'), threadId });
      } catch (error) {
        addWarning('warn', 'thread.identity.resolve.failed', { threadId, error: error.message });
      }
      const lines = [
        `threadId: ${threadId}`,
        thread.agentId || identity.agentId || identity.agent_id ? `agentId: ${thread.agentId || identity.agentId || identity.agent_id}` : '',
        thread.providerThreadId || identity.providerThreadId || identity.provider_thread_id ? `providerThreadId: ${thread.providerThreadId || identity.providerThreadId || identity.provider_thread_id}` : '',
        thread.name ? `name: ${thread.name}` : '',
        thread.status ? `status: ${thread.status}` : '',
      ].filter(Boolean);
      if (!globalThis.navigator?.clipboard?.writeText) {
        notifyAction('复制失败：剪贴板不可用', 'warning', { threadId });
        addWarning('warn', 'thread.copy.clipboard.unavailable', { threadId });
        return false;
      }
      try {
        await globalThis.navigator.clipboard.writeText(lines.join('\n'));
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
      const cwd = requireCwd('thread.rename');
      await renameThreadRPC({ cwd, threadId: id, name: nextName });
      set((state) => ({
        threads: state.threads.map((thread) => (thread.id === id ? { ...thread, name: nextName } : thread)),
        actionNotice: actionNotice('线程已重命名', 'success'),
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
