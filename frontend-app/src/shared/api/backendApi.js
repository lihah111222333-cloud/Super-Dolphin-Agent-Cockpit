// @ts-check

import {
  callAPI as callWailsAPI,
  getBuildInfo as getWailsBuildInfo,
  onBridgeEvent as subscribeBridgeEvent,
  onAgentEvent as subscribeAgentEvent,
  onFilesDropped as subscribeFilesDropped,
  onRuntimeReconnect as subscribeRuntimeReconnect,
  readDroppedTextFiles as readDroppedTextFilesViaBridge,
  saveClipboardImage as saveClipboardImageViaBridge,
  registerBridgeLogStore,
  saveTextFile as saveTextFileViaBridge,
  openSharedFile as openSharedFileViaBridge,
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
  CONFIG_LSP_PROMPT_HINT_READ: 'config/lspPromptHint/read',
  CONFIG_LSP_PROMPT_HINT_WRITE: 'config/lspPromptHint/write',
  CONFIG_BUILTIN_TOOLS_READ: 'config/builtinTools/read',
  CONFIG_BUILTIN_TOOLS_WRITE: 'config/builtinTools/write',

  APP_UPDATE_CHECK: 'app/update/check',
  APP_UPDATE_DOWNLOAD: 'app/update/download',
  APP_UPDATE_INSTALL: 'app/update/install',
  APP_UPDATE_INSTALL_LATEST: 'app/update/installLatest',

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
  UI_CODE_LOCATE: 'ui/code/locate',
  UI_CODE_OPEN: 'ui/code/open',
  UI_CODE_SAVE: 'ui/code/save',
  UI_PATH_OPEN: 'ui/path/open',

  UI_PROJECTS_GET: 'ui/projects/get',
  UI_PROJECTS_SET_ACTIVE: 'ui/projects/setActive',
  UI_PROJECTS_ADD: 'ui/projects/add',
  UI_PROJECTS_REMOVE: 'ui/projects/remove',

  UI_PREFERENCES_GET: 'ui/preferences/get',
  UI_PREFERENCES_GET_ALL: 'ui/preferences/getAll',
  UI_PREFERENCES_SET: 'ui/preferences/set',

  UI_DASHBOARD_GET: 'ui/dashboard/get',
  UI_VIDEO_GET_API_KEY: 'ui/video/getApiKey',
  UI_VIDEO_SET_API_KEY: 'ui/video/setApiKey',
  DASHBOARD_LOGS: 'dashboard/logs',
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
  PERSONALIZATION_PROFILE_GET: 'personalization/profile/get',
  PERSONALIZATION_PROFILE_SAVE: 'personalization/profile/save',
  PROMPT_SECTIONS_LIST: 'prompt-sections/list',
  PROMPT_SECTIONS_WRITE: 'prompt-sections/write',
  PROMPT_SECTIONS_DELETE: 'prompt-sections/delete',

  DASHBOARD_DAGS: 'dashboard/dags',
  DASHBOARD_DAG_DETAIL: 'dashboard/dagDetail',
  DASHBOARD_DAG_RUNS: 'dashboard/dagRuns',
  DASHBOARD_DAG_RUN: 'dashboard/dagRun',
  DASHBOARD_DAG_START: 'dashboard/dagStart',
  DASHBOARD_DAG_DISPATCH_NODE: 'dashboard/dagDispatchNode',
  DASHBOARD_DAG_TERMINATE: 'dashboard/dagTerminate',
  DASHBOARD_DAG_DELETE: 'dashboard/dagDelete',
  DASHBOARD_DAG_APPLY_OPS: 'dashboard/dagApplyOps',

  WORKFLOW_TEMPLATES_LIST: 'workflowTemplates/list',
  WORKFLOW_TEMPLATES_GET: 'workflowTemplates/get',
  WORKFLOW_TEMPLATES_RENDER_DAG: 'workflowTemplates/renderDag',

  CRONJOB_LIST: 'cronjob/list',
  CRONJOB_GET: 'cronjob/get',
  CRONJOB_CREATE: 'cronjob/create',
  CRONJOB_UPDATE: 'cronjob/update',
  CRONJOB_DELETE: 'cronjob/delete',
  CRONJOB_RUN_ONCE: 'cronjob/runOnce',
  CRONJOB_SET_ENABLED: 'cronjob/setEnabled',
  CRONJOB_LIST_RUNS: 'cronjob/listRuns',

  SKILLS_LOCAL_DELETE: 'skills/local/delete',
  SKILLS_LOCAL_READ: 'skills/local/read',
  SKILLS_LOCAL_LIST_FILES: 'skills/local/listFiles',
  SKILLS_LOCAL_WRITE: 'skills/local/write',
  SKILLS_LOCAL_IMPORT_DIR: 'skills/local/importDir',
  SKILLS_CREATE: 'skills/create',
  SKILLS_SUMMARY_SUGGEST: 'skills/summary/suggest',
  SKILLS_RESOLUTION_LIST: 'skills/resolution_list',
  SKILLS_RESOLUTION_PREVIEW: 'skills/resolution_preview',
  SKILLS_RESOLUTION_APPLY: 'skills/resolution_apply',

  DATASOURCE_V2_CREATE: 'datasourceV2/create',
  DATASOURCE_V2_LIST: 'datasourceV2/list',
  DATASOURCE_V2_GET: 'datasourceV2/get',
  DATASOURCE_V2_UPDATE: 'datasourceV2/update',
  DATASOURCE_V2_DELETE: 'datasourceV2/delete',

  MCP_SERVER_LIST: 'mcpServer/list',
  MCP_SERVER_SQLITE_START: 'mcpServer/sqlite/start',
  MCP_SERVER_SQLITE_STOP: 'mcpServer/sqlite/stop',
  MCP_SERVER_PLAYWRIGHT_START: 'mcpServer/playwright/start',
  MCP_SERVER_PLAYWRIGHT_STOP: 'mcpServer/playwright/stop',

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
  TURN_FORCE_COMPLETE: 'turn/forceComplete',
  APPROVAL_RESPOND: 'approval/respond',
});

const objectPrototype = Object.prototype;
const TOOL_SURFACE_MODES = new Set(['chat', 'auto', 'agent']);
/**
 * Fields accepted by the React thread/start facade before canonicalization.
 * Keep this list intentionally narrower than arbitrary objects so Go-side
 * strict decoders are not the first layer that catches UI payload drift.
 */
const THREAD_START_ALLOWED_KEYS = new Set([
  'cwd',
  'name',
  'provider',
  'modelProvider',
  'model_provider',
  'model',
  'effort',
  'promptKey',
  'prompt_key',
  'agentKey',
  'agent_key',
  'toolSurfaceMode',
  'tool_surface_mode',
  'deferSpawn',
  'defer_spawn',
  'codexModelProvider',
  'codex_model_provider',
  'config',
  'launchIntentId',
  'launch_intent_id',
  'baseInstructions',
  'base_instructions',
  'optimisticUserMessage',
  'optimistic_user_message',
  'skipInitialRuntimeSync',
  'skip_initial_runtime_sync',
]);
const DEFAULT_PROMPT_INTENT_KIND = 'expert';
const DEFAULT_PROMPT_SOURCE_TYPE = 'user_input';

function assertPlainObject(method, params) {
  // 误判防护：assertPlainObject 是 React RPC facade 的对象参数守卫。
  const value = params == null ? {} : params;
  if (typeof value !== 'object' || Array.isArray(value)) {
    throw new TypeError(`${method} params must be an object`);
  }
  return value;
}

function assertAllowedPayloadFields(method, payload, allowedKeys) {
  for (const key of Object.keys(payload)) {
    if (!allowedKeys.has(key)) {
      throw new Error(`${method}: unsupported payload field ${key}`);
    }
  }
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

function normalizeToolSurfaceMode(value) {
  const mode = normalizeString(value).toLowerCase();
  if (!mode) return '';
  if (!TOOL_SURFACE_MODES.has(mode)) throw new Error(`${RPC_METHODS.THREAD_START}: toolSurfaceMode must be chat, auto, or agent`);
  return mode;
}

function normalizeProvider(params) {
  return normalizeString(params.modelProvider || params.model_provider || params.provider);
}

function requireCwd(method, params) {
  // 误判防护：requireCwd 阻断缺 cwd 的 backend RPC 参数。
  const payload = assertPlainObject(method, params);
  const cwd = normalizeString(payload.cwd);
  if (!cwd || cwd === '.') {
    throw new Error(`${method}: cwd is required`);
  }
  return { ...payload, cwd };
}

function requireThreadId(method, params) {
  // 误判防护：requireThreadId 阻断缺 threadId 的 backend RPC 参数。
  const payload = assertPlainObject(method, params);
  const threadId = normalizeString(payload.threadId || payload.thread_id);
  if (!threadId) {
    throw new Error(`${method}: threadId is required`);
  }
  return { ...payload, threadId };
}

function requireKey(method, params, key) {
  // 误判防护：requireKey 阻断缺关键字段的 backend RPC 参数。
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
    includeTail: payload.includeTail,
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

function hasAttachmentInputContent(attachments) {
  return Array.isArray(attachments) && attachments.some((item) => normalizeAttachmentInputItem(item));
}

function normalizeTurnInput(input, attachments = []) {
  const extraItems = Array.isArray(attachments)
    ? attachments.map(normalizeAttachmentInputItem).filter(Boolean)
    : [];

  if (Array.isArray(input)) {
    if (input.length > 0 && extraItems.length > 0) {
      throw new Error(`${RPC_METHODS.TURN_START}: input and attachments cannot both contain content`);
    }
    if (input.length === 0 && extraItems.length === 0) {
      throw new Error(`${RPC_METHODS.TURN_START}: input is required`);
    }
    return { input: [...input, ...extraItems] };
  }

  const text = normalizeString(input);
  if (text && extraItems.length > 0) {
    throw new Error(`${RPC_METHODS.TURN_START}: input and attachments cannot both contain content`);
  }
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
  // 误判防护：dashboardDagStartPayload 要求 dagKey，避免 DAG start 空目标。
  const payload = requireKey(RPC_METHODS.DASHBOARD_DAG_START, assertPlainObject(RPC_METHODS.DASHBOARD_DAG_START, params), 'dagKey');
  return cleanObject({
    dagKey: payload.dagKey,
    triggerSource: normalizeString(payload.triggerSource),
    idempotencyKey: normalizeString(payload.idempotencyKey),
  });
}

function dashboardDagDispatchNodePayload(params) {
  const payload = requireNumber(
    RPC_METHODS.DASHBOARD_DAG_DISPATCH_NODE,
    requireKey(
      RPC_METHODS.DASHBOARD_DAG_DISPATCH_NODE,
      requireKey(RPC_METHODS.DASHBOARD_DAG_DISPATCH_NODE, assertPlainObject(RPC_METHODS.DASHBOARD_DAG_DISPATCH_NODE, params), 'dagKey'),
      'nodeKey',
    ),
    'runId',
  );
  const assignedTo = normalizeString(payload.assignedTo || payload.assigned_to);
  if (!assignedTo) throw new Error(`${RPC_METHODS.DASHBOARD_DAG_DISPATCH_NODE}: assignedTo is required`);
  return {
    dagKey: payload.dagKey,
    runId: payload.runId,
    nodeKey: payload.nodeKey,
    assignedTo,
  };
}

function optionalInteger(value) {
  if (value === undefined || value === null || value === '') return undefined;
  const parsed = Number(value);
  if (!Number.isFinite(parsed)) return undefined;
  return Math.trunc(parsed);
}

function requireNumber(method, params, key) {
  // 误判防护：requireNumber 阻断缺失或非数字的 backend RPC 参数。
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
  const payload = requireKey(
    RPC_METHODS.DASHBOARD_DAG_TERMINATE,
    requireKey(RPC_METHODS.DASHBOARD_DAG_TERMINATE, assertPlainObject(RPC_METHODS.DASHBOARD_DAG_TERMINATE, params), 'dagKey'),
    'runKey',
  );
  return cleanObject({
    dagKey: payload.dagKey,
    runKey: payload.runKey,
    reason: normalizeString(payload.reason),
  });
}

function dashboardDagApplyOpsPayload(params) {
  // 误判防护：dashboardDagApplyOpsPayload 要求 dagKey/baseVersion/ops 数组。
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

function cronIdPayload(method, params) {
  return {
    id: requireKey(method, assertPlainObject(method, params), 'id').id,
  };
}

function cronUpdatePayload(params) {
  const payload = requireKey(RPC_METHODS.CRONJOB_UPDATE, assertPlainObject(RPC_METHODS.CRONJOB_UPDATE, params), 'id');
  return { ...payload, id: payload.id };
}

function cronSetEnabledPayload(params) {
  const payload = requireBoolean(
    RPC_METHODS.CRONJOB_SET_ENABLED,
    requireKey(RPC_METHODS.CRONJOB_SET_ENABLED, assertPlainObject(RPC_METHODS.CRONJOB_SET_ENABLED, params), 'id'),
    'enabled',
  );
  return { id: payload.id, enabled: payload.enabled };
}

function cronListRunsPayload(params) {
  const payload = assertPlainObject(RPC_METHODS.CRONJOB_LIST_RUNS, params);
  const jobID = normalizeString(payload.job_id || payload.jobId);
  if (!jobID) throw new Error(`${RPC_METHODS.CRONJOB_LIST_RUNS}: job_id is required`);
  return cleanObject({
    job_id: jobID,
    limit: normalizeOptionalLimit(RPC_METHODS.CRONJOB_LIST_RUNS, payload),
  });
}

function codeProjectsPayload(method, payload) {
  if (!hasOwn(payload, 'projects') || payload.projects == null) return undefined;
  if (!Array.isArray(payload.projects)) throw new Error(`${method}: projects must be an array`);
  const projects = payload.projects.map(normalizeString).filter(Boolean);
  return projects.length > 0 ? projects : undefined;
}

function optionalCodeInteger(method, payload, key) {
  if (!hasOwn(payload, key) || payload[key] === undefined || payload[key] === null || payload[key] === '') return undefined;
  const value = Number(payload[key]);
  if (!Number.isFinite(value)) throw new Error(`${method}: ${key} must be a number`);
  return Math.trunc(value);
}

function codeFilePayload(method, params, options = {}) {
  const payload = requireKey(method, assertPlainObject(method, params), 'filePath');
  const request = {
    filePath: payload.filePath,
    project: normalizeString(payload.project),
    projects: codeProjectsPayload(method, payload),
  };
  if (options.includePosition) {
    request.line = optionalCodeInteger(method, payload, 'line');
    request.column = optionalCodeInteger(method, payload, 'column');
  }
  if (options.includeContent) {
    if (!hasOwn(payload, 'content')) throw new Error(`${method}: content is required`);
    request.content = (payload.content ?? '').toString();
  }
  return cleanObject(request);
}


function promptWritePayload(params) {
  const payload = requireKey(
    RPC_METHODS.PROMPTS_WRITE,
    requireCwd(RPC_METHODS.PROMPTS_WRITE, params),
    'name',
  );
  const priority = optionalInteger(payload.priority);
  const matchWhen = promptMatchWhen(payload);
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
    match_when: matchWhen,
  });
}

function promptMatchWhen(payload) {
  if (hasOwn(payload, 'match_when')) return payload.match_when;
  if (hasOwn(payload, 'matchWhen')) return payload.matchWhen;
  return undefined;
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
  const rawInput = promptIntentRawInput(payload);
  return cleanObject({
    cwd: payload.cwd,
    kind: normalizeString(payload.kind) || DEFAULT_PROMPT_INTENT_KIND,
    raw_input: rawInput,
    ...promptIntentSourceFields(payload),
    enable_global: promptIntentEnableGlobal(payload),
    ...promptProviderFields(payload),
  });
}

function promptIntentRawInput(payload) {
  const rawInput = normalizeString(payload.raw_input ?? payload.rawInput);
  if (!rawInput) throw new Error(`${RPC_METHODS.PROMPT_INTENTS_DRAFT}: raw_input is required`);
  return rawInput;
}

function promptIntentEnableGlobal(payload) {
  const scope = normalizeString(payload.scope);
  return payload.enable_global ?? payload.enableGlobal ?? (scope === 'global' ? true : undefined);
}

function promptIntentSourceFields(payload) {
  return {
    source_type: normalizeString(payload.source_type ?? payload.sourceType) || DEFAULT_PROMPT_SOURCE_TYPE,
    source_url: normalizeString(payload.source_url ?? payload.sourceUrl),
    license_hint: normalizeString(payload.license_hint ?? payload.licenseHint),
  };
}

function promptProviderFields(payload) {
  return {
    provider: normalizeString(payload.provider ?? payload.modelProvider),
    model: normalizeString(payload.model),
    model_provider: normalizeString(payload.model_provider ?? payload.codexModelProvider),
  };
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

function personalizationProfilePayload(method, params) {
  const payload = requireCwd(method, params);
  if (method === RPC_METHODS.PERSONALIZATION_PROFILE_GET) return { cwd: payload.cwd };
  if (!payload.profile || typeof payload.profile !== 'object' || Array.isArray(payload.profile)) {
    throw new Error(`${method}: profile must be an object`);
  }
  return { cwd: payload.cwd, profile: payload.profile };
}

function promptSectionPayload(method, params) {
  return requireKey(method, requireCwd(method, params), 'prompt_id');
}

function lspPromptHintWritePayload(params) {
  const payload = requireCwd(RPC_METHODS.CONFIG_LSP_PROMPT_HINT_WRITE, params);
  if (!hasOwn(payload, 'hint')) throw new Error(`${RPC_METHODS.CONFIG_LSP_PROMPT_HINT_WRITE}: hint is required`);
  return { cwd: payload.cwd, hint: (payload.hint ?? '').toString() };
}

function videoApiKeyPayload(params) {
  const payload = assertPlainObject(RPC_METHODS.UI_VIDEO_SET_API_KEY, params);
  const apiKey = normalizeString(payload.apiKey);
  if (!apiKey) throw new Error(`${RPC_METHODS.UI_VIDEO_SET_API_KEY}: apiKey is required`);
  return { apiKey };
}

function builtinToolWritePayload(params) {
  const payload = requireBoolean(
    RPC_METHODS.CONFIG_BUILTIN_TOOLS_WRITE,
    requireKey(RPC_METHODS.CONFIG_BUILTIN_TOOLS_WRITE, requireCwd(RPC_METHODS.CONFIG_BUILTIN_TOOLS_WRITE, params), 'id'),
    'enabled',
  );
  return { cwd: payload.cwd, id: payload.id, enabled: payload.enabled };
}

function dashboardLogsPayload(params = {}) {
  const payload = assertPlainObject(RPC_METHODS.DASHBOARD_LOGS, params);
  return cleanObject({
    source: normalizeString(payload.source),
    category: normalizeString(payload.category),
    keyword: normalizeString(payload.keyword),
    level: normalizeString(payload.level),
    logger: normalizeString(payload.logger),
    component: normalizeString(payload.component),
    agentId: normalizeString(payload.agentId || payload.agent_id),
    threadId: normalizeString(payload.threadId || payload.thread_id),
    eventType: normalizeString(payload.eventType || payload.event_type),
    toolName: normalizeString(payload.toolName || payload.tool_name),
    limit: normalizeOptionalLimit(RPC_METHODS.DASHBOARD_LOGS, payload),
  });
}

function hasOwn(value, key) {
  return objectPrototype.hasOwnProperty.call(value, key);
}

/** @type {ReadonlyArray<readonly [string, (...args: any[]) => any]>} */
const NATIVE_DEP_FALLBACKS = Object.freeze([
  ['getBuildInfo', getWailsBuildInfo],
  ['onAgentEvent', subscribeAgentEvent],
  ['onBridgeEvent', subscribeBridgeEvent],
  ['onFilesDropped', subscribeFilesDropped],
  ['onRuntimeReconnect', subscribeRuntimeReconnect],
  ['readDroppedTextFiles', readDroppedTextFilesViaBridge],
  ['saveClipboardImage', saveClipboardImageViaBridge],
  ['saveTextFile', saveTextFileViaBridge],
  ['openSharedFile', openSharedFileViaBridge],
  ['beginTextClipboardWrite', beginTextClipboardWriteViaBridge],
  ['copyTextToClipboard', copyTextToClipboardViaBridge],
  ['selectFiles', selectFilesViaBridge],
  ['selectProjectDir', selectProjectDirViaBridge],
  ['selectProjectDirs', selectProjectDirsViaBridge],
]);

/** @param {Record<string, any>} deps */
function resolveNativeDeps(deps) {
  return Object.fromEntries(NATIVE_DEP_FALLBACKS.map(([key, fallback]) => [key, deps[key] || fallback]));
}

function createBackendCaller(callAPI) {
  return (method, params = {}) => {
    const rpcMethod = normalizeString(method);
    if (!rpcMethod) throw new Error('backend RPC method is required');
    return callAPI(rpcMethod, assertPlainObject(rpcMethod, params));
  };
}

function createConfigProjectApi(callBackend) {
  return {
    readConfig: () => callBackend(RPC_METHODS.CONFIG_READ, {}),
    readLspPromptHint: (params) => callBackend(RPC_METHODS.CONFIG_LSP_PROMPT_HINT_READ, requireCwd(RPC_METHODS.CONFIG_LSP_PROMPT_HINT_READ, params)),
    writeLspPromptHint: (params) => callBackend(RPC_METHODS.CONFIG_LSP_PROMPT_HINT_WRITE, lspPromptHintWritePayload(params)),
    readBuiltinTools: (params) => callBackend(RPC_METHODS.CONFIG_BUILTIN_TOOLS_READ, requireCwd(RPC_METHODS.CONFIG_BUILTIN_TOOLS_READ, params)),
    writeBuiltinTool: (params) => callBackend(RPC_METHODS.CONFIG_BUILTIN_TOOLS_WRITE, builtinToolWritePayload(params)),
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
    getVideoApiKey: () => callBackend(RPC_METHODS.UI_VIDEO_GET_API_KEY, {}),
    setVideoApiKey: (params) => callBackend(
      RPC_METHODS.UI_VIDEO_SET_API_KEY,
      videoApiKeyPayload(params),
    ),
    listDashboardLogs: (params = {}) => callBackend(RPC_METHODS.DASHBOARD_LOGS, dashboardLogsPayload(params)),
  };
}

function createAppUpdateApi(callBackend) {
  return {
    checkAppUpdate: () => callBackend(RPC_METHODS.APP_UPDATE_CHECK, {}),
    downloadAppUpdate: () => callBackend(RPC_METHODS.APP_UPDATE_DOWNLOAD, {}),
    installAppUpdate: () => callBackend(RPC_METHODS.APP_UPDATE_INSTALL, {}),
    installLatestAppUpdate: () => callBackend(RPC_METHODS.APP_UPDATE_INSTALL_LATEST, {}),
  };
}

function createObservabilityMemoryApi(callBackend) {
  return {
    ...createObservabilityApi(callBackend),
    ...createMemoryApi(callBackend),
  };
}

function datasourceCreatePayload(method, params) {
  const payload = assertPlainObject(method, params);
  const sourcePath = normalizeString(payload.sourcePath || payload.source_path);
  if (!sourcePath) throw new Error(`${method}: sourcePath is required`);
  return { sourcePath };
}

function datasourceListPayload(params = {}) {
  const method = RPC_METHODS.DATASOURCE_V2_LIST;
  const payload = assertPlainObject(method, params);
  const limit = normalizeOptionalLimit(method, payload);
  if (!limit) throw new Error(`${method}: limit must be a positive integer`);
  return cleanObject({ keyword: normalizeString(payload.keyword), limit });
}

function datasourceDocumentIDPayload(method, params) {
  const payload = assertPlainObject(method, params);
  const documentID = Number(payload.documentId ?? payload.document_id ?? payload.id);
  if (!Number.isInteger(documentID) || documentID <= 0) {
    throw new Error(`${method}: documentId is required`);
  }
  return { documentId: documentID };
}

function datasourceUpdatePayload(params) {
  const method = RPC_METHODS.DATASOURCE_V2_UPDATE;
  const payload = assertPlainObject(method, params);
  const { documentId } = datasourceDocumentIDPayload(method, payload);
  const sourcePath = normalizeString(payload.sourcePath || payload.source_path);
  const fileName = normalizeString(payload.fileName || payload.file_name);
  if (!sourcePath) throw new Error(`${method}: sourcePath is required`);
  if (!fileName) throw new Error(`${method}: fileName is required`);
  if (!hasOwn(payload, 'sizeBytes') && !hasOwn(payload, 'size_bytes')) {
    throw new Error(`${method}: sizeBytes is required`);
  }
  const sizeBytes = Number(payload.sizeBytes ?? payload.size_bytes);
  if (!Number.isInteger(sizeBytes) || sizeBytes < 0) {
    throw new Error(`${method}: sizeBytes must be a non-negative integer`);
  }
  return cleanObject({
    documentId,
    sourcePath,
    fileName,
    extension: normalizeString(payload.extension),
    sizeBytes,
  });
}

function createDatasourceApi(callBackend) {
  return {
    createDatasourceDocument: (params) => callBackend(
      RPC_METHODS.DATASOURCE_V2_CREATE,
      datasourceCreatePayload(RPC_METHODS.DATASOURCE_V2_CREATE, params),
    ),
    listDatasourceDocuments: (params = {}) => callBackend(
      RPC_METHODS.DATASOURCE_V2_LIST,
      datasourceListPayload(params),
    ),
    getDatasourceDocument: (params) => callBackend(
      RPC_METHODS.DATASOURCE_V2_GET,
      datasourceDocumentIDPayload(RPC_METHODS.DATASOURCE_V2_GET, params),
    ),
    updateDatasourceDocument: (params) => callBackend(
      RPC_METHODS.DATASOURCE_V2_UPDATE,
      datasourceUpdatePayload(params),
    ),
    deleteDatasourceDocument: (params) => callBackend(
      RPC_METHODS.DATASOURCE_V2_DELETE,
      datasourceDocumentIDPayload(RPC_METHODS.DATASOURCE_V2_DELETE, params),
    ),
  };
}

function createObservabilityApi(callBackend) {
  return {
    getObservabilityTrace: (params) => callBackend(RPC_METHODS.OBSERVABILITY_TRACE_GET, observabilityTracePayload(RPC_METHODS.OBSERVABILITY_TRACE_GET, params)),
    getObservabilityThreadRecent: (params) => callBackend(RPC_METHODS.OBSERVABILITY_THREAD_RECENT, observabilityThreadPayload(RPC_METHODS.OBSERVABILITY_THREAD_RECENT, params)),
    listObservabilityRecent: (params = {}) => callBackend(RPC_METHODS.OBSERVABILITY_RECENT_LIST, observabilityRecentPayload(RPC_METHODS.OBSERVABILITY_RECENT_LIST, params)),
    listObservabilitySlow: (params = {}) => callBackend(RPC_METHODS.OBSERVABILITY_SLOW_LIST, observabilityListPayload(RPC_METHODS.OBSERVABILITY_SLOW_LIST, params)),
    listObservabilityErrors: (params = {}) => callBackend(RPC_METHODS.OBSERVABILITY_ERROR_LIST, observabilityListPayload(RPC_METHODS.OBSERVABILITY_ERROR_LIST, params)),
    getObservabilityStatus: () => callBackend(RPC_METHODS.OBSERVABILITY_STATUS, {}),
  };
}

function createMemoryApi(callBackend) {
  return {
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
      requireKey(RPC_METHODS.UI_MEMORY_SIMILARITY_CONSOLIDATE_ALL_STATUS, requireCwd(RPC_METHODS.UI_MEMORY_SIMILARITY_CONSOLIDATE_ALL_STATUS, params), 'jobId'),
    ),
    listSharedFiles: (params = {}) => {
      const payload = assertPlainObject(RPC_METHODS.DASHBOARD_SHARED_FILES, params);
      if (Object.keys(payload).length > 0) throw new Error(`${RPC_METHODS.DASHBOARD_SHARED_FILES}: params are not supported`);
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
  };
}

function createPromptDagApi(callBackend) {
  return {
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
    getPersonalizationProfile: (params) => callBackend(
      RPC_METHODS.PERSONALIZATION_PROFILE_GET,
      personalizationProfilePayload(RPC_METHODS.PERSONALIZATION_PROFILE_GET, params),
    ),
    savePersonalizationProfile: (params) => callBackend(
      RPC_METHODS.PERSONALIZATION_PROFILE_SAVE,
      personalizationProfilePayload(RPC_METHODS.PERSONALIZATION_PROFILE_SAVE, params),
    ),
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
    dispatchDagNode: (params) => callBackend(RPC_METHODS.DASHBOARD_DAG_DISPATCH_NODE, dashboardDagDispatchNodePayload(params)),
    terminateDagRun: (params) => callBackend(RPC_METHODS.DASHBOARD_DAG_TERMINATE, dashboardDagTerminatePayload(params)),
    terminateDag: (params) => callBackend(RPC_METHODS.DASHBOARD_DAG_TERMINATE, dashboardDagTerminatePayload(params)),
    deleteDag: (params) => callBackend(
      RPC_METHODS.DASHBOARD_DAG_DELETE,
      requireKey(RPC_METHODS.DASHBOARD_DAG_DELETE, assertPlainObject(RPC_METHODS.DASHBOARD_DAG_DELETE, params), 'dagKey'),
    ),
    applyDagOps: (params) => callBackend(RPC_METHODS.DASHBOARD_DAG_APPLY_OPS, dashboardDagApplyOpsPayload(params)),
    listWorkflowTemplates: (params = {}) => callBackend(
      RPC_METHODS.WORKFLOW_TEMPLATES_LIST,
      workflowTemplateListPayload(params),
    ),
    getWorkflowTemplate: (params) => callBackend(
      RPC_METHODS.WORKFLOW_TEMPLATES_GET,
      requireKey(RPC_METHODS.WORKFLOW_TEMPLATES_GET, assertPlainObject(RPC_METHODS.WORKFLOW_TEMPLATES_GET, params), 'templateId'),
    ),
    renderWorkflowTemplateDraft: (params) => callBackend(
      RPC_METHODS.WORKFLOW_TEMPLATES_RENDER_DAG,
      workflowTemplateRenderPayload(params),
    ),
  };
}

function workflowTemplateRenderPayload(params) {
  const payload = requireKey(
    RPC_METHODS.WORKFLOW_TEMPLATES_RENDER_DAG,
    assertPlainObject(RPC_METHODS.WORKFLOW_TEMPLATES_RENDER_DAG, params),
    'templateId',
  );
  if (payload.values != null && (typeof payload.values !== 'object' || Array.isArray(payload.values))) {
    throw new Error(`${RPC_METHODS.WORKFLOW_TEMPLATES_RENDER_DAG}: values must be an object`);
  }
  if (payload.user_inputs != null && (typeof payload.user_inputs !== 'object' || Array.isArray(payload.user_inputs))) {
    throw new Error(`${RPC_METHODS.WORKFLOW_TEMPLATES_RENDER_DAG}: user_inputs must be an object`);
  }
  if (payload.runtime_context != null && (typeof payload.runtime_context !== 'object' || Array.isArray(payload.runtime_context))) {
    throw new Error(`${RPC_METHODS.WORKFLOW_TEMPLATES_RENDER_DAG}: runtime_context must be an object`);
  }
  return {
    templateId: payload.templateId,
    version: payload.version,
    values: payload.values || {},
    user_inputs: payload.user_inputs,
    runtime_context: payload.runtime_context,
    locale: payload.locale,
  };
}

function workflowTemplateListPayload(params) {
  const payload = assertPlainObject(RPC_METHODS.WORKFLOW_TEMPLATES_LIST, params);
  return {
    category: payload.category,
    business_flow: payload.business_flow,
    output_type: payload.output_type,
    supports_schedule: payload.supports_schedule,
    locale: payload.locale,
  };
}

function createCronApi(callBackend) {
  return {
    listCronJobs: () => callBackend(RPC_METHODS.CRONJOB_LIST, {}),
    getCronJob: (params) => callBackend(RPC_METHODS.CRONJOB_GET, cronIdPayload(RPC_METHODS.CRONJOB_GET, params)),
    createCronJob: (params) => callBackend(RPC_METHODS.CRONJOB_CREATE, assertPlainObject(RPC_METHODS.CRONJOB_CREATE, params)),
    updateCronJob: (params) => callBackend(RPC_METHODS.CRONJOB_UPDATE, cronUpdatePayload(params)),
    deleteCronJob: (params) => callBackend(RPC_METHODS.CRONJOB_DELETE, cronIdPayload(RPC_METHODS.CRONJOB_DELETE, params)),
    runCronJobOnce: (params) => callBackend(RPC_METHODS.CRONJOB_RUN_ONCE, cronIdPayload(RPC_METHODS.CRONJOB_RUN_ONCE, params)),
    setCronJobEnabled: (params) => callBackend(RPC_METHODS.CRONJOB_SET_ENABLED, cronSetEnabledPayload(params)),
    listCronJobRuns: (params) => callBackend(RPC_METHODS.CRONJOB_LIST_RUNS, cronListRunsPayload(params)),
  };
}

function createCodeApi(callBackend) {
  return {
    locateCodeFile: (params) => callBackend(RPC_METHODS.UI_CODE_LOCATE, codeFilePayload(RPC_METHODS.UI_CODE_LOCATE, params)),
    openCodeFile: (params) => callBackend(RPC_METHODS.UI_CODE_OPEN, codeFilePayload(RPC_METHODS.UI_CODE_OPEN, params, { includePosition: true })),
    openPath: (params) => callBackend(RPC_METHODS.UI_PATH_OPEN, codeFilePayload(RPC_METHODS.UI_PATH_OPEN, params, { includePosition: true })),
    saveCodeFile: (params) => callBackend(RPC_METHODS.UI_CODE_SAVE, codeFilePayload(RPC_METHODS.UI_CODE_SAVE, params, { includeContent: true })),
  };
}

function createSkillApi(callBackend) {
  return {
    readSkill: (params) => callBackend(
      RPC_METHODS.SKILLS_LOCAL_READ,
      requireKey(RPC_METHODS.SKILLS_LOCAL_READ, requireCwd(RPC_METHODS.SKILLS_LOCAL_READ, params), 'path'),
    ),
    listSkillFiles: (params) => callBackend(
      RPC_METHODS.SKILLS_LOCAL_LIST_FILES,
      requireKey(RPC_METHODS.SKILLS_LOCAL_LIST_FILES, requireCwd(RPC_METHODS.SKILLS_LOCAL_LIST_FILES, params), 'dir'),
    ),
    writeSkill: (params) => writeSkillPayload(callBackend, params),
    createSkill: (params) => createSkillPayload(callBackend, params),
    importSkillDirectories: (params) => importSkillDirectoriesPayload(callBackend, params),
    suggestSkillSummary: (params) => suggestSkillSummaryPayload(callBackend, params),
    listSkillResolutions: (params) => callBackend(RPC_METHODS.SKILLS_RESOLUTION_LIST, requireCwd(RPC_METHODS.SKILLS_RESOLUTION_LIST, params)),
    previewSkillResolution: (params) => callBackend(RPC_METHODS.SKILLS_RESOLUTION_PREVIEW, {
      cwd: requireCwd(RPC_METHODS.SKILLS_RESOLUTION_PREVIEW, params).cwd,
      ...skillResolutionPayload(params),
    }),
    applySkillResolution: (params) => applySkillResolutionPayload(callBackend, params),
    deleteSkill: (params) => deleteSkillPayload(callBackend, params),
  };
}

function createSkillPayload(callBackend, params) {
  const payload = requireContent(
    RPC_METHODS.SKILLS_CREATE,
    requireKey(RPC_METHODS.SKILLS_CREATE, requireCwd(RPC_METHODS.SKILLS_CREATE, params), 'name'),
  );
  if (!payload.content.trim()) throw new Error(`${RPC_METHODS.SKILLS_CREATE}: content is required`);
  return callBackend(RPC_METHODS.SKILLS_CREATE, {
    cwd: payload.cwd,
    name: payload.name,
    content: payload.content,
  });
}

function writeSkillPayload(callBackend, params) {
  const payload = requireSkillScope(
    RPC_METHODS.SKILLS_LOCAL_WRITE,
    requireContent(RPC_METHODS.SKILLS_LOCAL_WRITE, requireKey(RPC_METHODS.SKILLS_LOCAL_WRITE, requireCwd(RPC_METHODS.SKILLS_LOCAL_WRITE, params), 'path')),
  );
  return callBackend(RPC_METHODS.SKILLS_LOCAL_WRITE, cleanObject({
    cwd: payload.cwd,
    path: payload.path,
    content: payload.content,
    scope: payload.scope,
    personal_type: skillPersonalType(payload),
  }));
}

function importSkillDirectoriesPayload(callBackend, params) {
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
}

async function suggestSkillSummaryPayload(callBackend, params) {
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
}

function applySkillResolutionPayload(callBackend, params) {
  const payload = assertPlainObject(RPC_METHODS.SKILLS_RESOLUTION_APPLY, params);
  return callBackend(RPC_METHODS.SKILLS_RESOLUTION_APPLY, cleanObject({
    cwd: requireCwd(RPC_METHODS.SKILLS_RESOLUTION_APPLY, payload).cwd,
    ...skillResolutionPayload(payload),
    preview_id: normalizeString(payload.preview_id ?? payload.previewId),
    preview_hash: normalizeString(payload.preview_hash ?? payload.previewHash),
  }));
}

function deleteSkillPayload(callBackend, params) {
  const payload = requireKey(RPC_METHODS.SKILLS_LOCAL_DELETE, requireCwd(RPC_METHODS.SKILLS_LOCAL_DELETE, params), 'name');
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
}

function emptyStrictPayload(method, params = {}) {
  const payload = assertPlainObject(method, params);
  if (Object.keys(payload).length > 0) throw new Error(`${method}: params are not supported`);
  return {};
}

function createMCPServerApi(callBackend) {
  return {
    listMCPServers: (params = {}) => callBackend(
      RPC_METHODS.MCP_SERVER_LIST,
      emptyStrictPayload(RPC_METHODS.MCP_SERVER_LIST, params),
    ),
    startSQLiteMCPServer: (params = {}) => callBackend(
      RPC_METHODS.MCP_SERVER_SQLITE_START,
      emptyStrictPayload(RPC_METHODS.MCP_SERVER_SQLITE_START, params),
    ),
    stopSQLiteMCPServer: (params = {}) => callBackend(
      RPC_METHODS.MCP_SERVER_SQLITE_STOP,
      emptyStrictPayload(RPC_METHODS.MCP_SERVER_SQLITE_STOP, params),
    ),
    startPlaywrightMCPServer: (params = {}) => callBackend(
      RPC_METHODS.MCP_SERVER_PLAYWRIGHT_START,
      emptyStrictPayload(RPC_METHODS.MCP_SERVER_PLAYWRIGHT_START, params),
    ),
    stopPlaywrightMCPServer: (params = {}) => callBackend(
      RPC_METHODS.MCP_SERVER_PLAYWRIGHT_STOP,
      emptyStrictPayload(RPC_METHODS.MCP_SERVER_PLAYWRIGHT_STOP, params),
    ),
  };
}

function createThreadApi(callBackend) {
  return {
    getThreadMessages: (params) => callBackend(RPC_METHODS.THREAD_MESSAGES, requireThreadId(RPC_METHODS.THREAD_MESSAGES, assertPlainObject(RPC_METHODS.THREAD_MESSAGES, params))),
    resolveThreadIdentity: (params) => callBackend(RPC_METHODS.THREAD_RESOLVE, requireThreadId(RPC_METHODS.THREAD_RESOLVE, assertPlainObject(RPC_METHODS.THREAD_RESOLVE, params))),
    archiveThread: (params) => callBackend(RPC_METHODS.THREAD_ARCHIVE, threadIdOnlyPayload(RPC_METHODS.THREAD_ARCHIVE, params)),
    unarchiveThread: (params) => callBackend(RPC_METHODS.THREAD_UNARCHIVE, threadIdOnlyPayload(RPC_METHODS.THREAD_UNARCHIVE, params)),
    deleteThread: (params) => callBackend(RPC_METHODS.THREAD_DELETE, threadIdOnlyPayload(RPC_METHODS.THREAD_DELETE, params)),
    getThreadConfig: (params) => callBackend(RPC_METHODS.THREAD_CONFIG_GET, threadIdOnlyPayload(RPC_METHODS.THREAD_CONFIG_GET, params)),
    setThreadConfig: (params) => callBackend(RPC_METHODS.THREAD_CONFIG_SET, threadConfigPayload(params)),
    startThread: (params) => callBackend(RPC_METHODS.THREAD_START, threadStartPayload(params)),
    startTurn: (params) => callBackend(RPC_METHODS.TURN_START, turnStartPayload(params)),
    interruptTurn: (params) => callBackend(RPC_METHODS.TURN_INTERRUPT, turnInterruptPayload(params)),
    forceCompleteTurn: (params) => callBackend(RPC_METHODS.TURN_FORCE_COMPLETE, forceCompleteTurnPayload(params)),
    respondApproval: (params) => callBackend(RPC_METHODS.APPROVAL_RESPOND, approvalRespondPayload(params)),
    compactThread: (params) => callBackend(RPC_METHODS.THREAD_COMPACT_START, compactThreadPayload(params)),
    recoverThread: (params) => callBackend(RPC_METHODS.THREAD_RECOVER, threadIdOnlyPayload(RPC_METHODS.THREAD_RECOVER, requireCwd(RPC_METHODS.THREAD_RECOVER, params))),
    renameThread: (params) => callBackend(RPC_METHODS.THREAD_NAME_SET, legacyThreadNamePayload(RPC_METHODS.THREAD_NAME_SET, params)),
  };
}

function threadIdOnlyPayload(method, params) {
  const payload = requireThreadId(method, assertPlainObject(method, params));
  return { threadId: payload.threadId };
}

function threadConfigPayload(params) {
  const payload = requireThreadId(RPC_METHODS.THREAD_CONFIG_SET, assertPlainObject(RPC_METHODS.THREAD_CONFIG_SET, params));
  return {
    threadId: payload.threadId,
    model: normalizeProviderConfigValue(payload.model),
    effort: normalizeProviderConfigValue(payload.effort),
  };
}

function threadStartPayload(params) {
  const payload = requireCwd(RPC_METHODS.THREAD_START, params);
  assertAllowedPayloadFields(RPC_METHODS.THREAD_START, payload, THREAD_START_ALLOWED_KEYS);
  const provider = normalizeProvider(payload);
  if (!provider) throw new Error(`${RPC_METHODS.THREAD_START}: provider is required`);
  const rest = { ...payload };
  const promptKey = normalizeString(rest.promptKey || rest.prompt_key);
  const agentKey = normalizeString(rest.agentKey || rest.agent_key);
  const deferSpawn = rest.deferSpawn ?? rest.defer_spawn;
  const toolSurfaceMode = normalizeToolSurfaceMode(rest.toolSurfaceMode || rest.tool_surface_mode);
  stripThreadStartInternalKeys(rest);
  const request = cleanObject({ ...rest, provider, prompt_key: promptKey, agent_key: agentKey, toolSurfaceMode });
  if (deferSpawn === true) request.defer_spawn = true;
  return request;
}

function stripThreadStartInternalKeys(rest) {
  delete rest.provider;
  delete rest.modelProvider;
  delete rest.model_provider;
  delete rest.codexModelProvider;
  delete rest.codex_model_provider;
  delete rest.promptKey;
  delete rest.prompt_key;
  delete rest.agentKey;
  delete rest.agent_key;
  delete rest.deferSpawn;
  delete rest.defer_spawn;
  delete rest.toolSurfaceMode;
  delete rest.tool_surface_mode;
  delete rest.optimisticUserMessage;
  delete rest.optimistic_user_message;
  delete rest.skipInitialRuntimeSync;
  delete rest.skip_initial_runtime_sync;
}

function turnStartPayload(params) {
  const payload = requireThreadId(RPC_METHODS.TURN_START, requireCwd(RPC_METHODS.TURN_START, params));
  const { input, attachments, ...rest } = payload;
  if (normalizeString(rest.prompt) && hasAttachmentInputContent(attachments)) {
    throw new Error(`${RPC_METHODS.TURN_START}: prompt and attachments cannot both contain content`);
  }
  return { ...rest, ...normalizeTurnInput(input, attachments) };
}

function turnInterruptPayload(params) {
  const payload = requireThreadId(RPC_METHODS.TURN_INTERRUPT, requireCwd(RPC_METHODS.TURN_INTERRUPT, params));
  return cleanObject({ thread_id: payload.threadId, source: normalizeString(payload.source) });
}

function forceCompleteTurnPayload(params) {
  const payload = requireThreadId(RPC_METHODS.TURN_FORCE_COMPLETE, requireCwd(RPC_METHODS.TURN_FORCE_COMPLETE, params));
  return { threadId: payload.threadId };
}

function approvalRespondPayload(params) {
  const payload = assertPlainObject(RPC_METHODS.APPROVAL_RESPOND, params);
  const rawRequestId = Number(payload.requestId || payload.request_id);
  const requestId = Number.isFinite(rawRequestId) ? Math.trunc(rawRequestId) : 0;
  if (requestId <= 0) throw new Error(`${RPC_METHODS.APPROVAL_RESPOND}: requestId is required`);
  if (!hasOwn(payload, 'approved')) throw new Error(`${RPC_METHODS.APPROVAL_RESPOND}: approved is required`);
  if (typeof payload.approved !== 'boolean') throw new Error(`${RPC_METHODS.APPROVAL_RESPOND}: approved must be boolean`);
  return { requestId, approved: payload.approved };
}

function compactThreadPayload(params) {
  const payload = requireThreadId(RPC_METHODS.THREAD_COMPACT_START, requireCwd(RPC_METHODS.THREAD_COMPACT_START, params));
  return cleanObject({ threadId: payload.threadId, args: payload.args });
}

function createNativeApi(native) {
  return {
    getBuildInfo: native.getBuildInfo,
    onAgentEvent: native.onAgentEvent,
    onBridgeEvent: native.onBridgeEvent,
    onFilesDropped: native.onFilesDropped,
    onRuntimeReconnect: native.onRuntimeReconnect,
    readDroppedTextFiles: native.readDroppedTextFiles,
    saveClipboardImage: native.saveClipboardImage,
    saveTextFile: native.saveTextFile,
    openSharedFile: native.openSharedFile,
    beginTextClipboardWrite: native.beginTextClipboardWrite,
    copyTextToClipboard: native.copyTextToClipboard,
    selectFiles: native.selectFiles,
    selectProjectDir: native.selectProjectDir,
    selectProjectDirs: native.selectProjectDirs,
  };
}

export function createBackendApi(deps = {}) {
  const callBackend = createBackendCaller(deps.callAPI || callWailsAPI);
  return {
    callBackend,
    ...createConfigProjectApi(callBackend),
    ...createAppUpdateApi(callBackend),
    ...createObservabilityMemoryApi(callBackend),
    ...createPromptDagApi(callBackend),
    ...createCronApi(callBackend),
    ...createCodeApi(callBackend),
    ...createSkillApi(callBackend),
    ...createDatasourceApi(callBackend),
    ...createMCPServerApi(callBackend),
    ...createThreadApi(callBackend),
    ...createNativeApi(resolveNativeDeps(deps)),
  };
}

const backendApi = createBackendApi();

export const callBackend = backendApi.callBackend;
export const readConfig = backendApi.readConfig;
export const readLspPromptHint = backendApi.readLspPromptHint;
export const writeLspPromptHint = backendApi.writeLspPromptHint;
export const readBuiltinTools = backendApi.readBuiltinTools;
export const writeBuiltinTool = backendApi.writeBuiltinTool;
export const checkAppUpdate = backendApi.checkAppUpdate;
export const downloadAppUpdate = backendApi.downloadAppUpdate;
export const installAppUpdate = backendApi.installAppUpdate;
export const installLatestAppUpdate = backendApi.installLatestAppUpdate;
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
export const getVideoApiKey = backendApi.getVideoApiKey;
export const setVideoApiKey = backendApi.setVideoApiKey;
export const listDashboardLogs = backendApi.listDashboardLogs;
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
export const getPersonalizationProfile = backendApi.getPersonalizationProfile;
export const savePersonalizationProfile = backendApi.savePersonalizationProfile;
export const listPromptSections = backendApi.listPromptSections;
export const writePromptSection = backendApi.writePromptSection;
export const deletePromptSection = backendApi.deletePromptSection;
export const listDags = backendApi.listDags;
export const getDagDetail = backendApi.getDagDetail;
export const getDagRuns = backendApi.getDagRuns;
export const getDagRun = backendApi.getDagRun;
export const startDag = backendApi.startDag;
export const dispatchDagNode = backendApi.dispatchDagNode;
export const terminateDagRun = backendApi.terminateDagRun;
export const terminateDag = backendApi.terminateDag;
export const deleteDag = backendApi.deleteDag;
export const applyDagOps = backendApi.applyDagOps;
export const listWorkflowTemplates = backendApi.listWorkflowTemplates;
export const getWorkflowTemplate = backendApi.getWorkflowTemplate;
export const renderWorkflowTemplateDraft = backendApi.renderWorkflowTemplateDraft;
export const listCronJobs = backendApi.listCronJobs;
export const getCronJob = backendApi.getCronJob;
export const createCronJob = backendApi.createCronJob;
export const updateCronJob = backendApi.updateCronJob;
export const deleteCronJob = backendApi.deleteCronJob;
export const runCronJobOnce = backendApi.runCronJobOnce;
export const setCronJobEnabled = backendApi.setCronJobEnabled;
export const listCronJobRuns = backendApi.listCronJobRuns;
export const locateCodeFile = backendApi.locateCodeFile;
export const openCodeFile = backendApi.openCodeFile;
export const openPath = backendApi.openPath;
export const saveCodeFile = backendApi.saveCodeFile;
export const readSkill = backendApi.readSkill;
export const listSkillFiles = backendApi.listSkillFiles;
export const writeSkill = backendApi.writeSkill;
export const createSkill = backendApi.createSkill;
export const importSkillDirectories = backendApi.importSkillDirectories;
export const suggestSkillSummary = backendApi.suggestSkillSummary;
export const listSkillResolutions = backendApi.listSkillResolutions;
export const previewSkillResolution = backendApi.previewSkillResolution;
export const applySkillResolution = backendApi.applySkillResolution;
export const deleteSkill = backendApi.deleteSkill;
export const createDatasourceDocument = backendApi.createDatasourceDocument;
export const listDatasourceDocuments = backendApi.listDatasourceDocuments;
export const getDatasourceDocument = backendApi.getDatasourceDocument;
export const updateDatasourceDocument = backendApi.updateDatasourceDocument;
export const deleteDatasourceDocument = backendApi.deleteDatasourceDocument;
export const listMCPServers = backendApi.listMCPServers;
export const startSQLiteMCPServer = backendApi.startSQLiteMCPServer;
export const stopSQLiteMCPServer = backendApi.stopSQLiteMCPServer;
export const startPlaywrightMCPServer = backendApi.startPlaywrightMCPServer;
export const stopPlaywrightMCPServer = backendApi.stopPlaywrightMCPServer;
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
export const forceCompleteTurn = backendApi.forceCompleteTurn;
export const respondApproval = backendApi.respondApproval;
export const compactThread = backendApi.compactThread;
export const recoverThread = backendApi.recoverThread;
export const renameThread = backendApi.renameThread;
export const getBuildInfo = backendApi.getBuildInfo;
export const onAgentEvent = backendApi.onAgentEvent;
export const onBridgeEvent = backendApi.onBridgeEvent;
export const onFilesDropped = backendApi.onFilesDropped;
export const onRuntimeReconnect = backendApi.onRuntimeReconnect;
export const readDroppedTextFiles = backendApi.readDroppedTextFiles;
export const saveClipboardImage = backendApi.saveClipboardImage;
export const saveTextFile = backendApi.saveTextFile;
export const openSharedFile = backendApi.openSharedFile;
export const beginTextClipboardWrite = backendApi.beginTextClipboardWrite;
export const copyTextToClipboard = backendApi.copyTextToClipboard;
export const selectFiles = backendApi.selectFiles;
export const selectProjectDir = backendApi.selectProjectDir;
export const selectProjectDirs = backendApi.selectProjectDirs;
export { registerBridgeLogStore, sendFrontendLogBatch, emitFrontendTraceEvent };
