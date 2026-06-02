import {
  callAPI as callWailsAPI,
  getBuildInfo as getWailsBuildInfo,
  onBridgeEvent as subscribeBridgeEvent,
  onAgentEvent as subscribeAgentEvent,
  onFilesDropped as subscribeFilesDropped,
  readDroppedTextFiles as readDroppedTextFilesViaBridge,
  saveClipboardImage as saveClipboardImageViaBridge,
  registerBridgeLogStore,
  saveTextFile as saveTextFileViaBridge,
  beginTextClipboardWrite as beginTextClipboardWriteViaBridge,
  copyTextToClipboard as copyTextToClipboardViaBridge,
  selectFiles as selectFilesViaBridge,
  selectProjectDir as selectProjectDirViaBridge,
  selectProjectDirs as selectProjectDirsViaBridge,
  sendFrontendLogBatch,
  emitFrontendTraceEvent,
} from './wailsBridge';

export const RPC_METHODS = Object.freeze({
  CONFIG_READ: 'config/read',

  UI_WINDOW_BOOTSTRAP_GET: 'ui/windowBootstrap/get',
  UI_STATE_GET: 'ui/state/get',
  UI_SIDEBAR_GET: 'ui/sidebar/get',
  UI_LOG: 'ui/log',
  OBSERVABILITY_TRACE_GET: 'observability/trace/get',
  OBSERVABILITY_THREAD_RECENT: 'observability/thread/recent',
  OBSERVABILITY_RECENT_LIST: 'observability/recent/list',
  OBSERVABILITY_SLOW_LIST: 'observability/slow/list',
  OBSERVABILITY_ERROR_LIST: 'observability/error/list',
  OBSERVABILITY_STATUS: 'observability/status',
  OBSERVABILITY_FRONTEND_INGEST: 'observability/frontend/ingest',
  UI_OPEN_NEW_WINDOW: 'ui/openNewWindow',

  UI_PROJECTS_GET: 'ui/projects/get',
  UI_PROJECTS_SET_ACTIVE: 'ui/projects/setActive',
  UI_PROJECTS_ADD: 'ui/projects/add',
  UI_PROJECTS_REMOVE: 'ui/projects/remove',

  UI_PREFERENCES_GET: 'ui/preferences/get',
  UI_PREFERENCES_GET_ALL: 'ui/preferences/getAll',
  UI_PREFERENCES_SET: 'ui/preferences/set',

  UI_DASHBOARD_GET: 'ui/dashboard/get',
  UI_MEMORY_GET: 'ui/memory/get',
  UI_MEMORY_ENTRY_GET: 'ui/memory/entry/get',
  UI_MEMORY_ENTRY_UPSERT: 'ui/memory/entry/upsert',
  UI_MEMORY_ENTRY_DELETE: 'ui/memory/entry/delete',
  UI_MEMORY_AUTO_DREAM_SET_INTENT: 'ui/memory/auto-dream/set-intent',
  UI_MEMORY_ENTRY_MERGE: 'ui/memory/entry/merge',
  UI_MEMORY_SIMILARITY_IGNORE: 'ui/memory/similarity/ignore',
  UI_MEMORY_SIMILARITY_CONSOLIDATE_ALL: 'ui/memory/similarity/consolidate-all',
  UI_MEMORY_SIMILARITY_CONSOLIDATE_ALL_START: 'ui/memory/similarity/consolidate-all/start',
  UI_MEMORY_SIMILARITY_CONSOLIDATE_ALL_STATUS: 'ui/memory/similarity/consolidate-all/status',
  UI_SHARED_FILE_GET: 'ui/memory/shared-file/get',
  UI_SHARED_FILE_DELETE: 'ui/memory/shared-file/delete',
  DASHBOARD_SHARED_FILES: 'dashboard/sharedFiles',

  PROMPT_ASSETS_LIST: 'prompt-assets/list',
  DASHBOARD_PROMPTS: 'dashboard/prompts',
  PROMPTS_GET: 'prompts/get',
  PROMPTS_WRITE: 'prompts/write',
  PROMPTS_DELETE: 'prompts/delete',
  PROMPT_INTENTS_DRAFT: 'prompt-intents/draft',
  PROMPT_INTENTS_COMMIT: 'prompt-intents/commit',
  PROMPT_INTENTS_DISCARD: 'prompt-intents/discard',
  PROMPT_INTENTS_DRY_RUN: 'prompt-intents/dry-run',
  PROMPT_SECTIONS_LIST: 'prompt-sections/list',
  PROMPT_SECTIONS_WRITE: 'prompt-sections/write',
  PROMPT_SECTIONS_DELETE: 'prompt-sections/delete',

  DASHBOARD_DAGS: 'dashboard/dags',
  DASHBOARD_DAG_DETAIL: 'dashboard/dagDetail',
  DASHBOARD_DAG_RUNS: 'dashboard/dagRuns',
  DASHBOARD_DAG_RUN: 'dashboard/dagRun',
  DASHBOARD_DAG_START: 'dashboard/dagStart',
  DASHBOARD_DAG_TERMINATE: 'dashboard/dagTerminate',
  DASHBOARD_DAG_DELETE: 'dashboard/dagDelete',
  DASHBOARD_DAG_APPLY_OPS: 'dashboard/dagApplyOps',

  SKILLS_LOCAL_DELETE: 'skills/local/delete',
  SKILLS_LOCAL_READ: 'skills/local/read',
  SKILLS_LOCAL_LIST_FILES: 'skills/local/listFiles',
  SKILLS_LOCAL_WRITE: 'skills/local/write',
  SKILLS_LOCAL_IMPORT_DIR: 'skills/local/importDir',
  SKILLS_SUMMARY_SUGGEST: 'skills/summary/suggest',
  SKILLS_RESOLUTION_LIST: 'skills/resolution_list',
  SKILLS_RESOLUTION_PREVIEW: 'skills/resolution_preview',
  SKILLS_RESOLUTION_APPLY: 'skills/resolution_apply',

  THREAD_START: 'thread/start',
  THREAD_MESSAGES: 'thread/messages',
  THREAD_RESOLVE: 'thread/resolve',
  THREAD_ARCHIVE: 'thread/archive',
  THREAD_UNARCHIVE: 'thread/unarchive',
  THREAD_DELETE: 'thread/delete',
  THREAD_CONFIG_GET: 'thread/config/get',
  THREAD_CONFIG_SET: 'thread/config/set',
  THREAD_COMPACT_START: 'thread/compact/start',
  THREAD_RECOVER: 'thread/recover',
  THREAD_NAME_SET: 'thread/name/set',

  TURN_START: 'turn/start',
  TURN_INTERRUPT: 'turn/interrupt',
});

const objectPrototype = Object.prototype;

function assertPlainObject(method, params) {
  const value = params == null ? {} : params;
  if (typeof value !== 'object' || Array.isArray(value)) {
    throw new TypeError(`${method} params must be an object`);
  }
  return value;
}

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

function normalizeProvider(params) {
  return normalizeString(params.modelProvider || params.model_provider || params.provider);
}

function requireCwd(method, params) {
  const payload = assertPlainObject(method, params);
  const cwd = normalizeString(payload.cwd);
  if (!cwd || cwd === '.') {
    throw new Error(`${method}: cwd is required`);
  }
  return { ...payload, cwd };
}

function requireThreadId(method, params) {
  const payload = assertPlainObject(method, params);
  const threadId = normalizeString(payload.threadId || payload.thread_id);
  if (!threadId) {
    throw new Error(`${method}: threadId is required`);
  }
  return { ...payload, threadId };
}

function requireKey(method, params, key) {
  const payload = assertPlainObject(method, params);
  const value = normalizeString(payload[key]);
  if (!value) {
    throw new Error(`${method}: ${key} is required`);
  }
  return { ...payload, [key]: value };
}

function cleanObject(payload) {
  return Object.fromEntries(
    Object.entries(payload).filter(([, value]) => value !== undefined && value !== ''),
  );
}

function requireSkillScope(method, params) {
  const payload = assertPlainObject(method, params);
  const scope = normalizeString(payload.scope);
  if (scope !== 'project' && scope !== 'personal') {
    throw new Error(`${method}: scope must be project or personal`);
  }
  return { ...payload, scope };
}

function requireContent(method, params) {
  const payload = assertPlainObject(method, params);
  if (!hasOwn(payload, 'content')) throw new Error(`${method}: content is required`);
  return { ...payload, content: (payload.content || '').toString() };
}

function requirePaths(method, params) {
  const payload = assertPlainObject(method, params);
  if (!Array.isArray(payload.paths)) throw new Error(`${method}: paths must be an array`);
  return payload;
}

function requireBoolean(method, params, key) {
  const payload = assertPlainObject(method, params);
  if (!hasOwn(payload, key)) throw new Error(`${method}: ${key} is required`);
  if (typeof payload[key] !== 'boolean') throw new Error(`${method}: ${key} must be boolean`);
  return { ...payload, [key]: payload[key] };
}

function normalizeOptionalLimit(method, payload) {
  if (!hasOwn(payload, 'limit') || payload.limit === undefined || payload.limit === '') return undefined;
  const limit = Number(payload.limit);
  if (!Number.isInteger(limit) || limit <= 0) throw new Error(`${method}: limit must be a positive integer`);
  return limit;
}

function observabilityTracePayload(method, params) {
  const payload = assertPlainObject(method, params);
  const traceId = normalizeString(payload.traceId || payload.trace_id);
  if (!traceId) throw new Error(`${method}: traceId is required`);
  return cleanObject({ traceId, limit: normalizeOptionalLimit(method, payload), includeTail: payload.includeTail });
}

function observabilityThreadPayload(method, params) {
  const payload = assertPlainObject(method, params);
  const threadId = normalizeString(payload.threadId || payload.thread_id);
  if (!threadId) throw new Error(`${method}: threadId is required`);
  return cleanObject({ threadId, limit: normalizeOptionalLimit(method, payload), includeTail: payload.includeTail });
}

function observabilityListPayload(method, params = {}) {
  const payload = assertPlainObject(method, params);
  return cleanObject({ limit: normalizeOptionalLimit(method, payload), component: normalizeString(payload.component) });
}

function observabilityRecentPayload(method, params = {}) {
  const payload = assertPlainObject(method, params);
  return cleanObject({
    limit: normalizeOptionalLimit(method, payload),
    status: normalizeString(payload.status),
    component: normalizeString(payload.component),
    method: normalizeString(payload.method),
    traceId: normalizeString(payload.traceId || payload.trace_id),
    threadId: normalizeString(payload.threadId || payload.thread_id),
    agentId: normalizeString(payload.agentId || payload.agent_id),
    keyword: normalizeString(payload.keyword),
  });
}

function legacyThreadNamePayload(method, params) {
  const payload = requireKey(method, requireThreadId(method, params), 'name');
  return { threadId: payload.threadId, name: payload.name };
}

function memoryEntryGetPayload(method, params) {
  return requireKey(method, requireCwd(method, params), 'path');
}

function memoryEntryUpsertPayload(method, params) {
  const payload = requireCwd(method, params);
  for (const key of ['name', 'description', 'type', 'content']) {
    if (!normalizeString(payload[key])) throw new Error(`${method}: ${key} is required`);
  }
  return cleanObject({
    cwd: payload.cwd,
    target: normalizeString(payload.target),
    existingPath: normalizeString(payload.existingPath),
    name: normalizeString(payload.name),
    description: normalizeString(payload.description),
    type: normalizeString(payload.type),
    content: (payload.content || '').toString().trim(),
    title: normalizeString(payload.title),
  });
}

function memoryPairPayload(method, params) {
  const payload = requireCwd(method, params);
  for (const key of ['targetA', 'pathA', 'targetB', 'pathB']) {
    if (!normalizeString(payload[key])) throw new Error(`${method}: ${key} is required`);
  }
  return {
    cwd: payload.cwd,
    targetA: normalizeString(payload.targetA),
    pathA: normalizeString(payload.pathA),
    targetB: normalizeString(payload.targetB),
    pathB: normalizeString(payload.pathB),
  };
}

function skillPersonalType(payload) {
  return normalizeString(payload.personal_type || payload.personalType);
}

function normalizeSkillSummarySuggestion(raw) {
  if (typeof raw === 'string') return normalizeString(raw);
  if (raw && typeof raw === 'object' && !Array.isArray(raw) && hasOwn(raw, 'description')) {
    return normalizeString(raw.description);
  }
  throw new Error(`${RPC_METHODS.SKILLS_SUMMARY_SUGGEST}: description is required`);
}

function skillResolutionPayload(params = {}) {
  const payload = assertPlainObject(RPC_METHODS.SKILLS_RESOLUTION_PREVIEW, params);
  const entries = [
    ['conflict_id', payload.conflict_id ?? payload.conflictId],
    ['action', payload.action],
    ['name', payload.name],
    ['scope', payload.scope],
    ['personal_type', payload.personal_type ?? payload.personalType],
    ['provider', payload.provider],
    ['source_provider', payload.source_provider ?? payload.sourceProvider],
    ['source_path_id', payload.source_path_id ?? payload.sourcePathId],
    ['new_name', payload.new_name ?? payload.newName],
    ['keep_source_id', payload.keep_source_id ?? payload.keepSourceID],
    ['merge_content_hash', payload.merge_content_hash ?? payload.mergeContentHash],
    ['disable_policy_target', payload.disable_policy_target ?? payload.disablePolicyTarget],
  ];
  return cleanObject(Object.fromEntries(entries.map(([key, value]) => [key, normalizeString(value)])));
}

function basename(path) {
  const value = normalizeString(path);
  return value.split(/[\\/]/).filter(Boolean).pop() || value;
}

function normalizeAttachmentPath(item) {
  if (typeof item === 'string') return normalizeString(item);
  if (item && typeof item === 'object') return normalizeString(item.path || item.url);
  return '';
}

function normalizeAttachmentInputItem(item) {
  if (item && typeof item === 'object' && normalizeString(item.kind) === 'image') {
    const path = normalizeString(item.path);
    const previewUrl = normalizeString(item.previewUrl || item.url);
    if (path) {
      const payload = { type: 'localImage', path };
      if (previewUrl.toLowerCase().startsWith('data:image/')) payload.url = previewUrl;
      return payload;
    }
    if (previewUrl) return { type: 'image', url: previewUrl };
    return null;
  }

  const path = normalizeAttachmentPath(item);
  if (!path) return null;
  return { type: 'mention', name: basename(path), path };
}

function normalizeTurnInput(input, attachments = []) {
  const extraItems = Array.isArray(attachments)
    ? attachments.map(normalizeAttachmentInputItem).filter(Boolean)
    : [];

  if (Array.isArray(input)) {
    if (input.length === 0 && extraItems.length === 0) {
      throw new Error(`${RPC_METHODS.TURN_START}: input is required`);
    }
    return { input: [...input, ...extraItems] };
  }

  const text = normalizeString(input);
  if (!text && extraItems.length === 0) {
    throw new Error(`${RPC_METHODS.TURN_START}: input is required`);
  }
  if (extraItems.length > 0) {
    return {
      input: [
        ...(text ? [{ type: 'text', text }] : []),
        ...extraItems,
      ],
    };
  }
  return { prompt: text };
}

function dashboardDagStartPayload(params) {
  const payload = requireKey(RPC_METHODS.DASHBOARD_DAG_START, assertPlainObject(RPC_METHODS.DASHBOARD_DAG_START, params), 'dagKey');
  return cleanObject({
    dagKey: payload.dagKey,
    triggerSource: normalizeString(payload.triggerSource),
    idempotencyKey: normalizeString(payload.idempotencyKey),
  });
}

function optionalInteger(value) {
  if (value === undefined || value === null || value === '') return undefined;
  const parsed = Number(value);
  if (!Number.isFinite(parsed)) return undefined;
  return Math.trunc(parsed);
}

function requireNumber(method, params, key) {
  const payload = assertPlainObject(method, params);
  if (!hasOwn(payload, key) || payload[key] === null || payload[key] === '') {
    throw new Error(`${method}: ${key} is required`);
  }
  const value = Number(payload[key]);
  if (!Number.isFinite(value)) {
    throw new Error(`${method}: ${key} must be a number`);
  }
  return { ...payload, [key]: value };
}

function dashboardDagsPayload(params = {}) {
  const payload = assertPlainObject(RPC_METHODS.DASHBOARD_DAGS, params);
  return cleanObject({
    keyword: normalizeString(payload.keyword),
    status: normalizeString(payload.status),
    limit: optionalInteger(payload.limit),
  });
}

function dashboardDagRunsPayload(params) {
  const payload = requireKey(RPC_METHODS.DASHBOARD_DAG_RUNS, assertPlainObject(RPC_METHODS.DASHBOARD_DAG_RUNS, params), 'dagKey');
  return cleanObject({
    dagKey: payload.dagKey,
    status: normalizeString(payload.status),
    limit: optionalInteger(payload.limit),
  });
}

function dashboardDagTerminatePayload(params) {
  const payload = requireKey(RPC_METHODS.DASHBOARD_DAG_TERMINATE, assertPlainObject(RPC_METHODS.DASHBOARD_DAG_TERMINATE, params), 'dagKey');
  return cleanObject({
    dagKey: payload.dagKey,
    runKey: normalizeString(payload.runKey),
    reason: normalizeString(payload.reason),
  });
}

function dashboardDagApplyOpsPayload(params) {
  const payload = requireNumber(
    RPC_METHODS.DASHBOARD_DAG_APPLY_OPS,
    requireKey(RPC_METHODS.DASHBOARD_DAG_APPLY_OPS, assertPlainObject(RPC_METHODS.DASHBOARD_DAG_APPLY_OPS, params), 'dagKey'),
    'baseVersion',
  );
  if (!Array.isArray(payload.ops)) {
    throw new Error(`${RPC_METHODS.DASHBOARD_DAG_APPLY_OPS}: ops must be an array`);
  }
  return {
    dagKey: payload.dagKey,
    baseVersion: payload.baseVersion,
    ops: payload.ops,
  };
}


function promptWritePayload(params) {
  const payload = requireKey(
    RPC_METHODS.PROMPTS_WRITE,
    requireCwd(RPC_METHODS.PROMPTS_WRITE, params),
    'name',
  );
  const priority = optionalInteger(payload.priority);
  return cleanObject({
    cwd: payload.cwd,
    id: normalizeString(payload.id),
    name: payload.name,
    description: normalizeString(payload.description),
    agentType: normalizeString(payload.agentType || payload.agent_key || payload.agentKey) || 'main',
    priority,
    when_to_use: normalizeString(payload.when_to_use ?? payload.whenToUse),
    content: hasOwn(payload, 'content') ? (payload.content || '').toString() : undefined,
    tags: Array.isArray(payload.tags) ? payload.tags : [],
    enabled: hasOwn(payload, 'enabled') ? Boolean(payload.enabled) : undefined,
    scope: normalizeString(payload.scope) || 'project',
    match_when: hasOwn(payload, 'match_when')
      ? payload.match_when
      : (hasOwn(payload, 'matchWhen') ? payload.matchWhen : undefined),
  });
}

function promptDeletePayload(params) {
  const payload = requireKey(
    RPC_METHODS.PROMPTS_DELETE,
    requireCwd(RPC_METHODS.PROMPTS_DELETE, params),
    'id',
  );
  return cleanObject({
    cwd: payload.cwd,
    id: payload.id,
    scope: normalizeString(payload.scope) || 'project',
  });
}

function promptIntentDraftPayload(params) {
  const payload = requireCwd(RPC_METHODS.PROMPT_INTENTS_DRAFT, params);
  const rawInput = normalizeString(payload.raw_input ?? payload.rawInput);
  if (!rawInput) throw new Error(`${RPC_METHODS.PROMPT_INTENTS_DRAFT}: raw_input is required`);
  const scope = normalizeString(payload.scope);
  const enableGlobal = payload.enable_global ?? payload.enableGlobal ?? (scope === 'global' ? true : undefined);
  return cleanObject({
    cwd: payload.cwd,
    kind: normalizeString(payload.kind) || 'expert',
    raw_input: rawInput,
    source_type: normalizeString(payload.source_type ?? payload.sourceType) || 'user_input',
    source_url: normalizeString(payload.source_url ?? payload.sourceUrl),
    license_hint: normalizeString(payload.license_hint ?? payload.licenseHint),
    enable_global: enableGlobal,
    provider: normalizeString(payload.provider ?? payload.modelProvider),
    model: normalizeString(payload.model),
    model_provider: normalizeString(payload.model_provider ?? payload.codexModelProvider),
  });
}

function memoryConsolidationPayload(method, params) {
  const payload = requireCwd(method, params);
  return cleanObject({
    cwd: payload.cwd,
    provider: normalizeString(payload.provider ?? payload.modelProvider),
    model: normalizeString(payload.model),
    model_provider: normalizeString(payload.model_provider ?? payload.codexModelProvider),
  });
}

function promptDraftKeyPayload(method, params) {
  const payload = requireCwd(method, params);
  const draftKey = normalizeString(payload.draft_key ?? payload.draftKey);
  if (!draftKey) throw new Error(`${method}: draft_key is required`);
  return { ...payload, draft_key: draftKey };
}

function promptIntentCommitPayload(params) {
  const payload = promptDraftKeyPayload(RPC_METHODS.PROMPT_INTENTS_COMMIT, params);
  const scope = normalizeString(payload.scope);
  const enableGlobal = payload.enable_global ?? payload.enableGlobal ?? (scope === 'global' ? true : undefined);
  return cleanObject({
    cwd: payload.cwd,
    draft_key: payload.draft_key,
    confirm_risk: payload.confirm_risk ?? payload.confirmRisk,
    enable_global: enableGlobal,
    confirm_global: payload.confirm_global ?? payload.confirmGlobal,
  });
}

function promptIntentDiscardPayload(params) {
  const payload = promptDraftKeyPayload(RPC_METHODS.PROMPT_INTENTS_DISCARD, params);
  return { cwd: payload.cwd, draft_key: payload.draft_key };
}

function promptIntentDryRunPayload(params) {
  const payload = promptDraftKeyPayload(RPC_METHODS.PROMPT_INTENTS_DRY_RUN, params);
  const question = normalizeString(payload.question);
  if (!question) throw new Error(`${RPC_METHODS.PROMPT_INTENTS_DRY_RUN}: question is required`);
  return cleanObject({
    cwd: payload.cwd,
    draft_key: payload.draft_key,
    kind: normalizeString(payload.kind),
    card: payload.card,
    question,
  });
}

function promptSectionPayload(method, params) {
  return requireKey(method, requireCwd(method, params), 'prompt_id');
}

function hasOwn(value, key) {
  return objectPrototype.hasOwnProperty.call(value, key);
}

export function createBackendApi(deps = {}) {
  const callAPI = deps.callAPI || callWailsAPI;
  const native = {
    getBuildInfo: deps.getBuildInfo || getWailsBuildInfo,
    onAgentEvent: deps.onAgentEvent || subscribeAgentEvent,
    onBridgeEvent: deps.onBridgeEvent || subscribeBridgeEvent,
    onFilesDropped: deps.onFilesDropped || subscribeFilesDropped,
    readDroppedTextFiles: deps.readDroppedTextFiles || readDroppedTextFilesViaBridge,
    saveClipboardImage: deps.saveClipboardImage || saveClipboardImageViaBridge,
    saveTextFile: deps.saveTextFile || saveTextFileViaBridge,
    beginTextClipboardWrite: deps.beginTextClipboardWrite || beginTextClipboardWriteViaBridge,
    copyTextToClipboard: deps.copyTextToClipboard || copyTextToClipboardViaBridge,
    selectFiles: deps.selectFiles || selectFilesViaBridge,
    selectProjectDir: deps.selectProjectDir || selectProjectDirViaBridge,
    selectProjectDirs: deps.selectProjectDirs || selectProjectDirsViaBridge,
  };

  const callBackend = (method, params = {}) => {
    const rpcMethod = normalizeString(method);
    if (!rpcMethod) throw new Error('backend RPC method is required');
    return callAPI(rpcMethod, assertPlainObject(rpcMethod, params));
  };

  return {
    callBackend,

    readConfig: () => callBackend(RPC_METHODS.CONFIG_READ, {}),
    getWindowBootstrap: () => callBackend(RPC_METHODS.UI_WINDOW_BOOTSTRAP_GET, {}),

    getSidebarState: (params) => callBackend(RPC_METHODS.UI_SIDEBAR_GET, requireCwd(RPC_METHODS.UI_SIDEBAR_GET, params)),
    openNewWindow: (params) => callBackend(RPC_METHODS.UI_OPEN_NEW_WINDOW, requireCwd(RPC_METHODS.UI_OPEN_NEW_WINDOW, params)),
    getThreadState: (params) => callBackend(
      RPC_METHODS.UI_STATE_GET,
      requireThreadId(RPC_METHODS.UI_STATE_GET, requireCwd(RPC_METHODS.UI_STATE_GET, params)),
    ),

    getProjects: (params) => callBackend(RPC_METHODS.UI_PROJECTS_GET, requireCwd(RPC_METHODS.UI_PROJECTS_GET, params)),
    setActiveProject: (params) => callBackend(
      RPC_METHODS.UI_PROJECTS_SET_ACTIVE,
      requireKey(RPC_METHODS.UI_PROJECTS_SET_ACTIVE, requireCwd(RPC_METHODS.UI_PROJECTS_SET_ACTIVE, params), 'path'),
    ),
    addProject: (params) => callBackend(
      RPC_METHODS.UI_PROJECTS_ADD,
      requireKey(RPC_METHODS.UI_PROJECTS_ADD, requireCwd(RPC_METHODS.UI_PROJECTS_ADD, params), 'path'),
    ),
    removeProject: (params) => callBackend(
      RPC_METHODS.UI_PROJECTS_REMOVE,
      requireKey(RPC_METHODS.UI_PROJECTS_REMOVE, requireCwd(RPC_METHODS.UI_PROJECTS_REMOVE, params), 'path'),
    ),

    getPreference: (params) => callBackend(RPC_METHODS.UI_PREFERENCES_GET, assertPlainObject(RPC_METHODS.UI_PREFERENCES_GET, params)),
    getAllPreferences: (params = {}) => callBackend(RPC_METHODS.UI_PREFERENCES_GET_ALL, assertPlainObject(RPC_METHODS.UI_PREFERENCES_GET_ALL, params)),
    setPreference: (params) => {
      const payload = assertPlainObject(RPC_METHODS.UI_PREFERENCES_SET, params);
      if (!normalizeString(payload.key)) throw new Error(`${RPC_METHODS.UI_PREFERENCES_SET}: key is required`);
      if (!hasOwn(payload, 'value')) throw new Error(`${RPC_METHODS.UI_PREFERENCES_SET}: value is required`);
      return callBackend(RPC_METHODS.UI_PREFERENCES_SET, payload);
    },

    getDashboardPage: (params) => callBackend(
      RPC_METHODS.UI_DASHBOARD_GET,
      requireKey(RPC_METHODS.UI_DASHBOARD_GET, requireCwd(RPC_METHODS.UI_DASHBOARD_GET, params), 'page'),
    ),
    getObservabilityTrace: (params) => callBackend(RPC_METHODS.OBSERVABILITY_TRACE_GET, observabilityTracePayload(RPC_METHODS.OBSERVABILITY_TRACE_GET, params)),
    getObservabilityThreadRecent: (params) => callBackend(RPC_METHODS.OBSERVABILITY_THREAD_RECENT, observabilityThreadPayload(RPC_METHODS.OBSERVABILITY_THREAD_RECENT, params)),
    listObservabilityRecent: (params = {}) => callBackend(RPC_METHODS.OBSERVABILITY_RECENT_LIST, observabilityRecentPayload(RPC_METHODS.OBSERVABILITY_RECENT_LIST, params)),
    listObservabilitySlow: (params = {}) => callBackend(RPC_METHODS.OBSERVABILITY_SLOW_LIST, observabilityListPayload(RPC_METHODS.OBSERVABILITY_SLOW_LIST, params)),
    listObservabilityErrors: (params = {}) => callBackend(RPC_METHODS.OBSERVABILITY_ERROR_LIST, observabilityListPayload(RPC_METHODS.OBSERVABILITY_ERROR_LIST, params)),
    getObservabilityStatus: () => callBackend(RPC_METHODS.OBSERVABILITY_STATUS, {}),
    getMemorySnapshot: (params) => callBackend(RPC_METHODS.UI_MEMORY_GET, requireCwd(RPC_METHODS.UI_MEMORY_GET, params)),
    getMemoryEntry: (params) => callBackend(RPC_METHODS.UI_MEMORY_ENTRY_GET, memoryEntryGetPayload(RPC_METHODS.UI_MEMORY_ENTRY_GET, params)),
    upsertMemoryEntry: (params) => callBackend(RPC_METHODS.UI_MEMORY_ENTRY_UPSERT, memoryEntryUpsertPayload(RPC_METHODS.UI_MEMORY_ENTRY_UPSERT, params)),
    deleteMemoryEntry: (params) => callBackend(RPC_METHODS.UI_MEMORY_ENTRY_DELETE, memoryEntryGetPayload(RPC_METHODS.UI_MEMORY_ENTRY_DELETE, params)),
    setMemoryAutoDreamIntent: (params) => callBackend(
      RPC_METHODS.UI_MEMORY_AUTO_DREAM_SET_INTENT,
      requireBoolean(RPC_METHODS.UI_MEMORY_AUTO_DREAM_SET_INTENT, params, 'enabled'),
    ),
    mergeMemoryEntries: (params) => callBackend(RPC_METHODS.UI_MEMORY_ENTRY_MERGE, memoryPairPayload(RPC_METHODS.UI_MEMORY_ENTRY_MERGE, params)),
    ignoreMemorySimilarity: (params) => callBackend(RPC_METHODS.UI_MEMORY_SIMILARITY_IGNORE, memoryPairPayload(RPC_METHODS.UI_MEMORY_SIMILARITY_IGNORE, params)),
    consolidateMemorySimilarities: (params) => callBackend(RPC_METHODS.UI_MEMORY_SIMILARITY_CONSOLIDATE_ALL, requireCwd(RPC_METHODS.UI_MEMORY_SIMILARITY_CONSOLIDATE_ALL, params)),
    startConsolidateMemorySimilarities: (params) => callBackend(
      RPC_METHODS.UI_MEMORY_SIMILARITY_CONSOLIDATE_ALL_START,
      memoryConsolidationPayload(RPC_METHODS.UI_MEMORY_SIMILARITY_CONSOLIDATE_ALL_START, params),
    ),
    getMemoryConsolidationStatus: (params) => callBackend(
      RPC_METHODS.UI_MEMORY_SIMILARITY_CONSOLIDATE_ALL_STATUS,
      requireKey(
        RPC_METHODS.UI_MEMORY_SIMILARITY_CONSOLIDATE_ALL_STATUS,
        requireCwd(RPC_METHODS.UI_MEMORY_SIMILARITY_CONSOLIDATE_ALL_STATUS, params),
        'jobId',
      ),
    ),
    listSharedFiles: (params = {}) => {
      const payload = assertPlainObject(RPC_METHODS.DASHBOARD_SHARED_FILES, params);
      if (Object.keys(payload).length > 0) {
        throw new Error(`${RPC_METHODS.DASHBOARD_SHARED_FILES}: params are not supported`);
      }
      return callBackend(RPC_METHODS.DASHBOARD_SHARED_FILES, {});
    },
    readSharedFile: (params) => callBackend(
      RPC_METHODS.UI_SHARED_FILE_GET,
      requireKey(RPC_METHODS.UI_SHARED_FILE_GET, assertPlainObject(RPC_METHODS.UI_SHARED_FILE_GET, params), 'path'),
    ),
    deleteSharedFile: (params) => callBackend(
      RPC_METHODS.UI_SHARED_FILE_DELETE,
      requireKey(RPC_METHODS.UI_SHARED_FILE_DELETE, assertPlainObject(RPC_METHODS.UI_SHARED_FILE_DELETE, params), 'path'),
    ),
    listPromptAssets: (params) => callBackend(RPC_METHODS.PROMPT_ASSETS_LIST, requireCwd(RPC_METHODS.PROMPT_ASSETS_LIST, params)),
    getDashboardPrompts: (params) => callBackend(RPC_METHODS.DASHBOARD_PROMPTS, requireCwd(RPC_METHODS.DASHBOARD_PROMPTS, params)),
    getPrompt: (params) => callBackend(
      RPC_METHODS.PROMPTS_GET,
      requireKey(RPC_METHODS.PROMPTS_GET, requireCwd(RPC_METHODS.PROMPTS_GET, params), 'id'),
    ),
    writePrompt: (params) => callBackend(RPC_METHODS.PROMPTS_WRITE, promptWritePayload(params)),
    deletePrompt: (params) => callBackend(RPC_METHODS.PROMPTS_DELETE, promptDeletePayload(params)),
    draftPromptIntent: (params) => callBackend(RPC_METHODS.PROMPT_INTENTS_DRAFT, promptIntentDraftPayload(params)),
    commitPromptIntent: (params) => callBackend(RPC_METHODS.PROMPT_INTENTS_COMMIT, promptIntentCommitPayload(params)),
    discardPromptIntent: (params) => callBackend(RPC_METHODS.PROMPT_INTENTS_DISCARD, promptIntentDiscardPayload(params)),
    dryRunPromptIntent: (params) => callBackend(RPC_METHODS.PROMPT_INTENTS_DRY_RUN, promptIntentDryRunPayload(params)),
    listPromptSections: (params) => callBackend(RPC_METHODS.PROMPT_SECTIONS_LIST, promptSectionPayload(RPC_METHODS.PROMPT_SECTIONS_LIST, params)),
    writePromptSection: (params) => callBackend(RPC_METHODS.PROMPT_SECTIONS_WRITE, promptSectionPayload(RPC_METHODS.PROMPT_SECTIONS_WRITE, params)),
    deletePromptSection: (params) => callBackend(RPC_METHODS.PROMPT_SECTIONS_DELETE, promptSectionPayload(RPC_METHODS.PROMPT_SECTIONS_DELETE, params)),
    listDags: (params) => callBackend(RPC_METHODS.DASHBOARD_DAGS, dashboardDagsPayload(params)),
    getDagDetail: (params) => callBackend(
      RPC_METHODS.DASHBOARD_DAG_DETAIL,
      requireKey(RPC_METHODS.DASHBOARD_DAG_DETAIL, assertPlainObject(RPC_METHODS.DASHBOARD_DAG_DETAIL, params), 'dagKey'),
    ),
    getDagRuns: (params) => callBackend(RPC_METHODS.DASHBOARD_DAG_RUNS, dashboardDagRunsPayload(params)),
    getDagRun: (params) => callBackend(
      RPC_METHODS.DASHBOARD_DAG_RUN,
      requireKey(RPC_METHODS.DASHBOARD_DAG_RUN, assertPlainObject(RPC_METHODS.DASHBOARD_DAG_RUN, params), 'runKey'),
    ),
    startDag: (params) => callBackend(RPC_METHODS.DASHBOARD_DAG_START, dashboardDagStartPayload(params)),
    terminateDagRun: (params) => callBackend(RPC_METHODS.DASHBOARD_DAG_TERMINATE, dashboardDagTerminatePayload(params)),
    terminateDag: (params) => callBackend(RPC_METHODS.DASHBOARD_DAG_TERMINATE, dashboardDagTerminatePayload(params)),
    deleteDag: (params) => callBackend(
      RPC_METHODS.DASHBOARD_DAG_DELETE,
      requireKey(RPC_METHODS.DASHBOARD_DAG_DELETE, assertPlainObject(RPC_METHODS.DASHBOARD_DAG_DELETE, params), 'dagKey'),
    ),
    applyDagOps: (params) => callBackend(RPC_METHODS.DASHBOARD_DAG_APPLY_OPS, dashboardDagApplyOpsPayload(params)),
    readSkill: (params) => callBackend(
      RPC_METHODS.SKILLS_LOCAL_READ,
      requireKey(RPC_METHODS.SKILLS_LOCAL_READ, requireCwd(RPC_METHODS.SKILLS_LOCAL_READ, params), 'path'),
    ),
    listSkillFiles: (params) => callBackend(
      RPC_METHODS.SKILLS_LOCAL_LIST_FILES,
      requireKey(RPC_METHODS.SKILLS_LOCAL_LIST_FILES, requireCwd(RPC_METHODS.SKILLS_LOCAL_LIST_FILES, params), 'dir'),
    ),
    writeSkill: (params) => {
      const payload = requireSkillScope(
        RPC_METHODS.SKILLS_LOCAL_WRITE,
        requireContent(
          RPC_METHODS.SKILLS_LOCAL_WRITE,
          requireKey(RPC_METHODS.SKILLS_LOCAL_WRITE, requireCwd(RPC_METHODS.SKILLS_LOCAL_WRITE, params), 'path'),
        ),
      );
      return callBackend(RPC_METHODS.SKILLS_LOCAL_WRITE, cleanObject({
        cwd: payload.cwd,
        path: payload.path,
        content: payload.content,
        scope: payload.scope,
        personal_type: skillPersonalType(payload),
      }));
    },
    importSkillDirectories: (params) => {
      const payload = requireSkillScope(
        RPC_METHODS.SKILLS_LOCAL_IMPORT_DIR,
        requirePaths(RPC_METHODS.SKILLS_LOCAL_IMPORT_DIR, requireCwd(RPC_METHODS.SKILLS_LOCAL_IMPORT_DIR, params)),
      );
      return callBackend(RPC_METHODS.SKILLS_LOCAL_IMPORT_DIR, cleanObject({
        cwd: payload.cwd,
        paths: payload.paths,
        scope: payload.scope,
        personal_type: skillPersonalType(payload),
      }));
    },
    suggestSkillSummary: async (params) => {
      const payload = requireCwd(RPC_METHODS.SKILLS_SUMMARY_SUGGEST, params);
      const summaryPayload = {
        cwd: payload.cwd,
        name: normalizeString(payload.name),
        description: normalizeString(payload.description),
        content: (payload.content || '').toString(),
        scenario_words: Array.isArray(payload.scenario_words) ? payload.scenario_words : [],
        scope: normalizeString(payload.scope),
      };
      const provider = normalizeString(payload.provider ?? payload.modelProvider);
      const model = normalizeString(payload.model);
      const modelProvider = normalizeString(payload.model_provider ?? payload.codexModelProvider);
      if (provider) summaryPayload.provider = provider;
      if (model) summaryPayload.model = model;
      if (modelProvider) summaryPayload.model_provider = modelProvider;
      const raw = await callBackend(RPC_METHODS.SKILLS_SUMMARY_SUGGEST, summaryPayload);
      return normalizeSkillSummarySuggestion(raw);
    },
    listSkillResolutions: (params) => callBackend(
      RPC_METHODS.SKILLS_RESOLUTION_LIST,
      requireCwd(RPC_METHODS.SKILLS_RESOLUTION_LIST, params),
    ),
    previewSkillResolution: (params) => callBackend(
      RPC_METHODS.SKILLS_RESOLUTION_PREVIEW,
      {
        cwd: requireCwd(RPC_METHODS.SKILLS_RESOLUTION_PREVIEW, params).cwd,
        ...skillResolutionPayload(params),
      },
    ),
    applySkillResolution: (params) => {
      const payload = assertPlainObject(RPC_METHODS.SKILLS_RESOLUTION_APPLY, params);
      return callBackend(RPC_METHODS.SKILLS_RESOLUTION_APPLY, cleanObject({
        cwd: requireCwd(RPC_METHODS.SKILLS_RESOLUTION_APPLY, payload).cwd,
        ...skillResolutionPayload(payload),
        preview_id: normalizeString(payload.preview_id ?? payload.previewId),
        preview_hash: normalizeString(payload.preview_hash ?? payload.previewHash),
      }));
    },
    deleteSkill: (params) => {
      const payload = requireKey(
        RPC_METHODS.SKILLS_LOCAL_DELETE,
        requireCwd(RPC_METHODS.SKILLS_LOCAL_DELETE, params),
        'name',
      );
      const scope = normalizeString(payload.scope);
      if (scope !== 'project' && scope !== 'personal') {
        throw new Error(`${RPC_METHODS.SKILLS_LOCAL_DELETE}: scope must be project or personal`);
      }
      return callBackend(RPC_METHODS.SKILLS_LOCAL_DELETE, cleanObject({
        cwd: payload.cwd,
        name: payload.name,
        scope,
        personal_type: normalizeString(payload.personal_type || payload.personalType),
      }));
    },
    runDashboardCommand: () => {
      throw new Error('dashboard command execution backend RPC is not registered');
    },

    getThreadMessages: (params) => callBackend(
      RPC_METHODS.THREAD_MESSAGES,
      requireThreadId(RPC_METHODS.THREAD_MESSAGES, assertPlainObject(RPC_METHODS.THREAD_MESSAGES, params)),
    ),
    resolveThreadIdentity: (params) => callBackend(
      RPC_METHODS.THREAD_RESOLVE,
      requireThreadId(RPC_METHODS.THREAD_RESOLVE, assertPlainObject(RPC_METHODS.THREAD_RESOLVE, params)),
    ),
    archiveThread: (params) => {
      const payload = requireThreadId(RPC_METHODS.THREAD_ARCHIVE, assertPlainObject(RPC_METHODS.THREAD_ARCHIVE, params));
      return callBackend(RPC_METHODS.THREAD_ARCHIVE, { threadId: payload.threadId });
    },
    unarchiveThread: (params) => {
      const payload = requireThreadId(RPC_METHODS.THREAD_UNARCHIVE, assertPlainObject(RPC_METHODS.THREAD_UNARCHIVE, params));
      return callBackend(RPC_METHODS.THREAD_UNARCHIVE, { threadId: payload.threadId });
    },
    deleteThread: (params) => {
      const payload = requireThreadId(RPC_METHODS.THREAD_DELETE, assertPlainObject(RPC_METHODS.THREAD_DELETE, params));
      return callBackend(RPC_METHODS.THREAD_DELETE, { threadId: payload.threadId });
    },
    getThreadConfig: (params) => {
      const payload = requireThreadId(RPC_METHODS.THREAD_CONFIG_GET, assertPlainObject(RPC_METHODS.THREAD_CONFIG_GET, params));
      return callBackend(RPC_METHODS.THREAD_CONFIG_GET, { threadId: payload.threadId });
    },
    setThreadConfig: (params) => {
      const payload = requireThreadId(RPC_METHODS.THREAD_CONFIG_SET, assertPlainObject(RPC_METHODS.THREAD_CONFIG_SET, params));
      return callBackend(RPC_METHODS.THREAD_CONFIG_SET, {
        threadId: payload.threadId,
        model: normalizeProviderConfigValue(payload.model),
        effort: normalizeProviderConfigValue(payload.effort),
      });
    },
    startThread: (params) => {
      const payload = requireCwd(RPC_METHODS.THREAD_START, params);
      const provider = normalizeProvider(payload);
      if (!provider) {
        throw new Error(`${RPC_METHODS.THREAD_START}: provider is required`);
      }
      const rest = { ...payload };
      const promptKey = normalizeString(rest.promptKey || rest.prompt_key);
      const agentKey = normalizeString(rest.agentKey || rest.agent_key);
      const deferSpawn = rest.deferSpawn ?? rest.defer_spawn;
      delete rest.provider;
      delete rest.modelProvider;
      delete rest.model_provider;
      delete rest.promptKey;
      delete rest.prompt_key;
      delete rest.agentKey;
      delete rest.agent_key;
      delete rest.deferSpawn;
      delete rest.defer_spawn;
      delete rest.optimisticUserMessage;
      delete rest.optimistic_user_message;
      delete rest.skipInitialRuntimeSync;
      delete rest.skip_initial_runtime_sync;
      const request = cleanObject({
        ...rest,
        modelProvider: provider,
        prompt_key: promptKey,
        agent_key: agentKey,
      });
      if (deferSpawn === true) {
        request.defer_spawn = true;
      }
      return callBackend(RPC_METHODS.THREAD_START, request);
    },
    startTurn: (params) => {
      const payload = requireThreadId(RPC_METHODS.TURN_START, requireCwd(RPC_METHODS.TURN_START, params));
      const { input, attachments, ...rest } = payload;
      return callBackend(RPC_METHODS.TURN_START, {
        ...rest,
        ...normalizeTurnInput(input, attachments),
      });
    },
    interruptTurn: (params) => {
      const payload = requireThreadId(RPC_METHODS.TURN_INTERRUPT, requireCwd(RPC_METHODS.TURN_INTERRUPT, params));
      const turnId = normalizeString(payload.turnId || payload.turn_id);
      if (!turnId) {
        throw new Error(`${RPC_METHODS.TURN_INTERRUPT}: turnId is required`);
      }
      return callBackend(RPC_METHODS.TURN_INTERRUPT, cleanObject({
        threadId: payload.threadId,
        turnId,
        source: normalizeString(payload.source),
      }));
    },
    compactThread: (params) => {
      const payload = requireThreadId(RPC_METHODS.THREAD_COMPACT_START, requireCwd(RPC_METHODS.THREAD_COMPACT_START, params));
      return callBackend(RPC_METHODS.THREAD_COMPACT_START, cleanObject({
        threadId: payload.threadId,
        args: payload.args,
      }));
    },
    recoverThread: (params) => {
      const payload = requireThreadId(RPC_METHODS.THREAD_RECOVER, requireCwd(RPC_METHODS.THREAD_RECOVER, params));
      return callBackend(RPC_METHODS.THREAD_RECOVER, {
        threadId: payload.threadId,
      });
    },
    renameThread: (params) => callBackend(
      RPC_METHODS.THREAD_NAME_SET,
      legacyThreadNamePayload(RPC_METHODS.THREAD_NAME_SET, params),
    ),

    getBuildInfo: native.getBuildInfo,
    onAgentEvent: native.onAgentEvent,
    onBridgeEvent: native.onBridgeEvent,
    onFilesDropped: native.onFilesDropped,
    readDroppedTextFiles: native.readDroppedTextFiles,
    saveClipboardImage: native.saveClipboardImage,
    saveTextFile: native.saveTextFile,
    beginTextClipboardWrite: native.beginTextClipboardWrite,
    copyTextToClipboard: native.copyTextToClipboard,
    selectFiles: native.selectFiles,
    selectProjectDir: native.selectProjectDir,
    selectProjectDirs: native.selectProjectDirs,
  };
}

const backendApi = createBackendApi();

export const callBackend = backendApi.callBackend;
export const readConfig = backendApi.readConfig;
export const getWindowBootstrap = backendApi.getWindowBootstrap;
export const getSidebarState = backendApi.getSidebarState;
export const openNewWindow = backendApi.openNewWindow;
export const getThreadState = backendApi.getThreadState;
export const getProjects = backendApi.getProjects;
export const setActiveProject = backendApi.setActiveProject;
export const addProject = backendApi.addProject;
export const removeProject = backendApi.removeProject;
export const getPreference = backendApi.getPreference;
export const getAllPreferences = backendApi.getAllPreferences;
export const setPreference = backendApi.setPreference;
export const getDashboardPage = backendApi.getDashboardPage;
export const getObservabilityTrace = backendApi.getObservabilityTrace;
export const getObservabilityThreadRecent = backendApi.getObservabilityThreadRecent;
export const listObservabilityRecent = backendApi.listObservabilityRecent;
export const listObservabilitySlow = backendApi.listObservabilitySlow;
export const listObservabilityErrors = backendApi.listObservabilityErrors;
export const getObservabilityStatus = backendApi.getObservabilityStatus;
export const getMemorySnapshot = backendApi.getMemorySnapshot;
export const getMemoryEntry = backendApi.getMemoryEntry;
export const upsertMemoryEntry = backendApi.upsertMemoryEntry;
export const deleteMemoryEntry = backendApi.deleteMemoryEntry;
export const setMemoryAutoDreamIntent = backendApi.setMemoryAutoDreamIntent;
export const mergeMemoryEntries = backendApi.mergeMemoryEntries;
export const ignoreMemorySimilarity = backendApi.ignoreMemorySimilarity;
export const consolidateMemorySimilarities = backendApi.consolidateMemorySimilarities;
export const startConsolidateMemorySimilarities = backendApi.startConsolidateMemorySimilarities;
export const getMemoryConsolidationStatus = backendApi.getMemoryConsolidationStatus;
export const listSharedFiles = backendApi.listSharedFiles;
export const readSharedFile = backendApi.readSharedFile;
export const deleteSharedFile = backendApi.deleteSharedFile;
export const listPromptAssets = backendApi.listPromptAssets;
export const getDashboardPrompts = backendApi.getDashboardPrompts;
export const getPrompt = backendApi.getPrompt;
export const writePrompt = backendApi.writePrompt;
export const deletePrompt = backendApi.deletePrompt;
export const draftPromptIntent = backendApi.draftPromptIntent;
export const commitPromptIntent = backendApi.commitPromptIntent;
export const discardPromptIntent = backendApi.discardPromptIntent;
export const dryRunPromptIntent = backendApi.dryRunPromptIntent;
export const listPromptSections = backendApi.listPromptSections;
export const writePromptSection = backendApi.writePromptSection;
export const deletePromptSection = backendApi.deletePromptSection;
export const listDags = backendApi.listDags;
export const getDagDetail = backendApi.getDagDetail;
export const getDagRuns = backendApi.getDagRuns;
export const getDagRun = backendApi.getDagRun;
export const startDag = backendApi.startDag;
export const terminateDagRun = backendApi.terminateDagRun;
export const terminateDag = backendApi.terminateDag;
export const deleteDag = backendApi.deleteDag;
export const applyDagOps = backendApi.applyDagOps;
export const readSkill = backendApi.readSkill;
export const listSkillFiles = backendApi.listSkillFiles;
export const writeSkill = backendApi.writeSkill;
export const importSkillDirectories = backendApi.importSkillDirectories;
export const suggestSkillSummary = backendApi.suggestSkillSummary;
export const listSkillResolutions = backendApi.listSkillResolutions;
export const previewSkillResolution = backendApi.previewSkillResolution;
export const applySkillResolution = backendApi.applySkillResolution;
export const deleteSkill = backendApi.deleteSkill;
export const runDashboardCommand = backendApi.runDashboardCommand;
export const getThreadMessages = backendApi.getThreadMessages;
export const resolveThreadIdentity = backendApi.resolveThreadIdentity;
export const archiveThread = backendApi.archiveThread;
export const unarchiveThread = backendApi.unarchiveThread;
export const deleteThread = backendApi.deleteThread;
export const getThreadConfig = backendApi.getThreadConfig;
export const setThreadConfig = backendApi.setThreadConfig;
export const startThread = backendApi.startThread;
export const startTurn = backendApi.startTurn;
export const interruptTurn = backendApi.interruptTurn;
export const compactThread = backendApi.compactThread;
export const recoverThread = backendApi.recoverThread;
export const renameThread = backendApi.renameThread;
export const getBuildInfo = backendApi.getBuildInfo;
export const onAgentEvent = backendApi.onAgentEvent;
export const onBridgeEvent = backendApi.onBridgeEvent;
export const onFilesDropped = backendApi.onFilesDropped;
export const readDroppedTextFiles = backendApi.readDroppedTextFiles;
export const saveClipboardImage = backendApi.saveClipboardImage;
export const saveTextFile = backendApi.saveTextFile;
export const beginTextClipboardWrite = backendApi.beginTextClipboardWrite;
export const copyTextToClipboard = backendApi.copyTextToClipboard;
export const selectFiles = backendApi.selectFiles;
export const selectProjectDir = backendApi.selectProjectDir;
export const selectProjectDirs = backendApi.selectProjectDirs;
export { registerBridgeLogStore, sendFrontendLogBatch, emitFrontendTraceEvent };
