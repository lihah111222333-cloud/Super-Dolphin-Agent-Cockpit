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
  previewSharedFile as previewSharedFileViaBridge,
  beginTextClipboardWrite as beginTextClipboardWriteViaBridge,
  copyTextToClipboard as copyTextToClipboardViaBridge,
  selectDatasourceImportFile as selectDatasourceImportFileViaBridge,
  selectFiles as selectFilesViaBridge,
  selectProjectDir as selectProjectDirViaBridge,
  selectProjectDirs as selectProjectDirsViaBridge,
  sendFrontendLogBatch,
  emitFrontendTraceEvent,
} from './wailsBridge';
import { positiveApprovalRequestIdFromFields } from './approvalRequestId.js';
import {
  parseMemorySnapshotResponse,
  parseModelProviderRegistryResponse,
  parseObservabilityResultResponse,
  parseSharedFileDetailResponse,
  parseSharedFilesDashboardResponse,
} from './backendSchemas.js';

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
  MODEL_PROVIDERS_LIST: 'modelProviders/list',
  MODEL_PROVIDERS_SAVE: 'modelProviders/save',
  MODEL_PROVIDERS_APPLY: 'modelProviders/apply',

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
  DASHBOARD_WORKFLOW_MATERIAL_WRITE: 'dashboard/workflowMaterialWrite',

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
  DASHBOARD_DAG_CREATE_AND_START: 'dashboard/dagCreateAndStart',
  DASHBOARD_DAG_DISPATCH_NODE: 'dashboard/dagDispatchNode',
  DASHBOARD_DAG_TERMINATE: 'dashboard/dagTerminate',
  DASHBOARD_DAG_DELETE: 'dashboard/dagDelete',
  DASHBOARD_DAG_APPLY_OPS: 'dashboard/dagApplyOps',

  WORKFLOW_TEMPLATES_LIST: 'workflowTemplates/list',
  WORKFLOW_TEMPLATES_GET: 'workflowTemplates/get',
  WORKFLOW_TEMPLATES_RENDER_DAG: 'workflowTemplates/renderDag',
  WORKFLOW_TEMPLATES_SAVE: 'workflowTemplates/save',
  WORKFLOW_TEMPLATES_ROLLBACK: 'workflowTemplates/rollback',

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
  SKILL_TOOLS_CREATE: 'skills/tools/create',
  SKILL_TOOLS_LIST: 'skills/tools/list',
  SKILL_TOOLS_GET: 'skills/tools/get',
  SKILL_TOOLS_UPDATE: 'skills/tools/update',
  SKILL_TOOLS_DELETE: 'skills/tools/delete',

  DATASOURCE_V2_CREATE: 'datasourceV2/create',
  DATASOURCE_V2_IMPORT_LOCAL_FILE: 'datasourceV2/importLocalFile',
  DATASOURCE_V2_LIST: 'datasourceV2/list',
  DATASOURCE_V2_GET: 'datasourceV2/get',
  DATASOURCE_V2_LIST_CHUNKS: 'datasourceV2/list_chunks',
  DATASOURCE_V2_UPDATE: 'datasourceV2/update',
  DATASOURCE_V2_DELETE: 'datasourceV2/delete',

  MCP_SERVER_LIST: 'mcpServer/list',
  MCP_SERVER_SQLITE_START: 'mcpServer/sqlite/start',
  MCP_SERVER_SQLITE_STOP: 'mcpServer/sqlite/stop',
  MCP_SERVER_PLAYWRIGHT_START: 'mcpServer/playwright/start',
  MCP_SERVER_PLAYWRIGHT_STOP: 'mcpServer/playwright/stop',
  MCP_TOOL_LIFECYCLE_SET: 'mcpServer/toolLifecycle/set',
  MCP_TOOL_LIFECYCLE_LIST: 'mcpServer/toolLifecycle/list',
  MCP_TOOL_LIFECYCLE_EXPORT: 'mcpServer/toolLifecycle/export',

  THREAD_START: 'thread/start',
  THREAD_LIST_PAGE: 'thread/listPage',
  THREAD_LOADED_LIST_PAGE: 'thread/loaded/listPage',
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
const MCP_TOOL_LIFECYCLE_STATES = new Set(['enabled', 'disabled', 'suspended', 'removed']);
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

function assertStrictPlainObject(method, params) {
  const value = assertPlainObject(method, params);
  const prototype = Object.getPrototypeOf(value);
  if (prototype !== objectPrototype && prototype !== null) {
    throw new TypeError(`${method} params must be a plain object`);
  }
  return value;
}

function assertNoExtraPayloadFields(method, payload) {
  const [key] = Object.keys(payload);
  if (key) {
    throw new Error(`${method}: unsupported payload field ${key}`);
  }
}

function takePayloadField(payload, key) {
  const value = payload[key];
  delete payload[key];
  return value;
}

function takePayloadFields(payload, keys) {
  const out = {};
  for (const key of keys) {
    if (hasOwn(payload, key)) out[key] = payload[key];
    delete payload[key];
  }
  return out;
}

function normalizeString(value) {
  if (value === undefined || value === null) return '';
  return String(value).trim();
}

function normalizeRequiredString(method, value, key) {
  const normalized = normalizeString(value);
  if (!normalized) throw new Error(`${method}: ${key} is required`);
  return normalized;
}

function normalizeOptionalString(value) {
  if (value === undefined || value === null) return '';
  return String(value);
}

function optionalPayloadObject(value) {
  if (value === undefined || value === null) return {};
  return value;
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
  return { ...payload, content: normalizeOptionalString(payload.content) };
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

function normalizeOptionalCursorInteger(method, payload, camelKey, snakeKey) {
  const raw = payload[camelKey] ?? payload[snakeKey];
  if (raw === undefined || raw === null || raw === '') return undefined;
  const value = Number(raw);
  if (!Number.isInteger(value) || value < 0) throw new Error(`${method}: ${camelKey} must be a non-negative integer`);
  return value;
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
  const { unused, threadId } = threadScopedPayload(method, params);
  const name = takePayloadField(unused, 'name');
  if (!normalizeString(name)) throw new Error(`${method}: name is required`);
  assertNoExtraPayloadFields(method, unused);
  return { threadId, name };
}

function memoryTargetPayload(method, value, field = 'target') {
  const target = normalizeString(value);
  if (target !== 'private' && target !== 'team') {
    throw new Error(`${method}: ${field} must be private or team`);
  }
  return target;
}

function memoryEntryGetPayload(method, params) {
  const payload = requireKey(method, requireCwd(method, params), 'path');
  return {
    ...payload,
    target: memoryTargetPayload(method, payload.target),
  };
}

function memoryEntryUpsertPayload(method, params) {
  const payload = requireCwd(method, params);
  for (const key of ['name', 'description', 'type', 'content']) {
    if (!normalizeString(payload[key])) throw new Error(`${method}: ${key} is required`);
  }
  return cleanObject({
    cwd: payload.cwd,
    target: memoryTargetPayload(method, payload.target),
    existingPath: normalizeString(payload.existingPath),
    name: normalizeString(payload.name),
    description: normalizeString(payload.description),
    type: normalizeString(payload.type),
    content: normalizeRequiredString(method, payload.content, 'content'),
    title: normalizeString(payload.title),
  });
}

function memoryPairPayload(method, params) {
  const payload = requireCwd(method, params);
  for (const key of ['pathA', 'pathB']) {
    if (!normalizeString(payload[key])) throw new Error(`${method}: ${key} is required`);
  }
  return {
    cwd: payload.cwd,
    targetA: memoryTargetPayload(method, payload.targetA, 'targetA'),
    pathA: normalizeString(payload.pathA),
    targetB: memoryTargetPayload(method, payload.targetB, 'targetB'),
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

function skillResolutionPayload(method, params = {}) {
  const payload = assertPlainObject(method, params);
  const conflictID = normalizeString(payload.conflict_id ?? payload.conflictId);
  const action = normalizeString(payload.action);
  if (!conflictID) throw new Error(`${method}: conflict_id is required`);
  if (!action) throw new Error(`${method}: action is required`);
  const entries = [
    ['conflict_id', conflictID],
    ['action', action],
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

function dashboardDagCreateAndStartPayload(params) {
  const method = RPC_METHODS.DASHBOARD_DAG_CREATE_AND_START;
  const payload = requireKey(method, requireKey(method, assertPlainObject(method, params), 'dagKey'), 'title');
  if (!Array.isArray(payload.nodes) || payload.nodes.length === 0) {
    throw new Error(`${method}: nodes must be a non-empty array`);
  }
  if (payload.metadata != null && (typeof payload.metadata !== 'object' || Array.isArray(payload.metadata))) {
    throw new Error(`${method}: metadata must be an object`);
  }
  return cleanObject({
    dagKey: payload.dagKey,
    title: payload.title,
    description: normalizeString(payload.description),
    finalNodeKey: normalizeString(payload.finalNodeKey || payload.final_node_key),
    metadata: optionalPayloadObject(payload.metadata),
    nodes: payload.nodes,
    idempotencyKey: normalizeString(payload.idempotencyKey),
  });
}

function dashboardWorkflowMaterialWritePayload(params) {
  const method = RPC_METHODS.DASHBOARD_WORKFLOW_MATERIAL_WRITE;
  const payload = assertPlainObject(method, params);
  const path = normalizeString(payload.path);
  const content = typeof payload.content === 'string' ? payload.content : '';
  if (!path) throw new Error(`${method}: path is required`);
  if (!content.trim()) throw new Error(`${method}: content is required`);
  return { path, content };
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

function cronJobMutationPayload(method, params, options = {}) {
  const payload = requireCwd(method, params);
  const name = normalizeString(payload.name);
  const prompt = normalizeString(payload.prompt);
  const scheduleExpr = normalizeString(payload.schedule_expr ?? payload.scheduleExpr);
  if (!name) throw new Error(`${method}: name is required`);
  if (!prompt) throw new Error(`${method}: prompt is required`);
  if (!scheduleExpr) throw new Error(`${method}: schedule_expr is required`);
  return cleanObject({
    id: options.requireId ? requireKey(method, payload, 'id').id : undefined,
    cwd: payload.cwd,
    name,
    prompt,
    schedule_type: normalizeString(payload.schedule_type ?? payload.scheduleType),
    schedule_expr: scheduleExpr,
    timezone: normalizeString(payload.timezone),
    provider: normalizeString(payload.provider),
    model: normalizeString(payload.model),
    config: cronJobConfigPayload(method, payload),
    skills: cronJobSkillsPayload(method, payload),
    notify_channel: normalizeString(payload.notify_channel ?? payload.notifyChannel),
    enabled: cronJobEnabledPayload(method, payload),
    next_run_at: normalizeString(payload.next_run_at ?? payload.nextRunAt),
    max_attempts: cronJobMaxAttemptsPayload(method, payload),
  });
}

function cronJobConfigPayload(method, payload) {
  if (!hasOwn(payload, 'config') || payload.config == null) return undefined;
  if (typeof payload.config !== 'object' || Array.isArray(payload.config)) {
    throw new Error(`${method}: config must be an object`);
  }
  return payload.config;
}

function cronJobSkillsPayload(method, payload) {
  if (!hasOwn(payload, 'skills') || payload.skills == null) return undefined;
  if (!Array.isArray(payload.skills)) throw new Error(`${method}: skills must be an array`);
  return payload.skills.map(normalizeString).filter(Boolean);
}

function cronJobEnabledPayload(method, payload) {
  if (!hasOwn(payload, 'enabled') || payload.enabled == null) return undefined;
  if (typeof payload.enabled !== 'boolean') throw new Error(`${method}: enabled must be boolean`);
  return payload.enabled;
}

function cronJobMaxAttemptsPayload(method, payload) {
  const raw = payload.max_attempts ?? payload.maxAttempts;
  if (raw === undefined || raw === null || raw === '') return undefined;
  const value = Number(raw);
  if (!Number.isInteger(value) || value < 0) {
    throw new Error(`${method}: max_attempts must be a non-negative integer`);
  }
  return value;
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
    if (typeof payload.content !== 'string') throw new Error(`${method}: content must be a string`);
    request.content = payload.content;
  }
  return cleanObject(request);
}


function promptWritePayload(params) {
  const payload = requireKey(
    RPC_METHODS.PROMPTS_WRITE,
    requireCwd(RPC_METHODS.PROMPTS_WRITE, params),
    'name',
  );
  const promptID = normalizeString(payload.id) || normalizeString(payload.key);
  if (!promptID) {
    throw new Error(`${RPC_METHODS.PROMPTS_WRITE}: id or key is required`);
  }
  const priority = optionalInteger(payload.priority);
  const matchWhen = promptMatchWhen(payload);
  return cleanObject({
    cwd: payload.cwd,
    id: promptID,
    name: payload.name,
    description: normalizeString(payload.description),
    agentType: normalizeString(payload.agentType || payload.agent_key || payload.agentKey) || 'main',
    priority,
    when_to_use: normalizeString(payload.when_to_use ?? payload.whenToUse),
    content: hasOwn(payload, 'content') ? normalizeOptionalString(payload.content) : undefined,
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
  return { cwd: payload.cwd, hint: normalizeOptionalString(payload.hint) };
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

function assertBackendResponseObject(method, response) {
  if (!response || typeof response !== 'object' || Array.isArray(response)) {
    throw new TypeError(`${method} response must be an object`);
  }
  return response;
}

function requireResponseKey(method, response, keys) {
  for (const key of keys) {
    if (normalizeString(response[key])) return;
  }
  throw new Error(`${method} response missing ${keys.join(' or ')}`);
}

function validateUIStateResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  const snapshotKeys = [
    'threads',
    'agents',
    'active_turn',
    'recent_turns',
    'token_usage',
    'statuses',
    'unchanged',
    'activeThreadId',
    'mainAgentId',
  ];
  if (!snapshotKeys.some((key) => hasOwn(value, key))) {
    throw new Error(`${method} response missing UI state snapshot fields`);
  }
  if (hasOwn(value, 'threads') && value.threads !== null && !Array.isArray(value.threads)) {
    throw new TypeError(`${method} response threads must be an array or null`);
  }
  if (hasOwn(value, 'agents') && value.agents !== null && !Array.isArray(value.agents)) {
    throw new TypeError(`${method} response agents must be an array or null`);
  }
  return value;
}

function validateLspPromptHintResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  for (const key of ['hint', 'defaultHint', 'overrideHint']) {
    if (typeof value[key] !== 'string') {
      throw new TypeError(`${method} response ${key} must be a string`);
    }
  }
  if (typeof value.usingDefault !== 'boolean') {
    throw new TypeError(`${method} response usingDefault must be a boolean`);
  }
  return value;
}

function validateThreadStartResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  if (value.thread && (typeof value.thread !== 'object' || Array.isArray(value.thread))) {
    throw new TypeError(`${method} response thread must be an object`);
  }
  if (value.thread && normalizeString(value.thread.id)) return value;
  requireResponseKey(method, value, ['threadId', 'thread_id']);
  return value;
}

function validateThreadMessagesResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  if (!Array.isArray(value.messages)) {
    throw new TypeError(`${method} response messages must be an array`);
  }
  if (hasOwn(value, 'total') && (typeof value.total !== 'number' || !Number.isFinite(value.total))) {
    throw new TypeError(`${method} response total must be a number`);
  }
  if (hasOwn(value, 'hasMore') && typeof value.hasMore !== 'boolean') {
    throw new TypeError(`${method} response hasMore must be a boolean`);
  }
  if (hasOwn(value, 'nextBefore') && typeof value.nextBefore !== 'string') {
    throw new TypeError(`${method} response nextBefore must be a string`);
  }
  return value;
}

function validateThreadResolveResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  requireResponseKey(method, value, ['id', 'threadId', 'thread_id']);
  return value;
}

function validateTurnStartResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  requireResponseKey(method, value, ['turn_id', 'turnId']);
  return value;
}

function hasTurnForceCompleteFailureDiagnostic(value) {
  return ['errorCode', 'error', 'message'].some((key) => (
    typeof value[key] === 'string' && value[key].trim() !== ''
  ));
}

function validateTurnForceCompleteResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  if (typeof value.forceCompleted !== 'boolean') {
    throw new TypeError(`${method} response forceCompleted must be a boolean`);
  }
  if (value.forceCompleted) {
    if (value.ok !== true) {
      throw new TypeError(`${method} response ok must be true when forceCompleted is true`);
    }
    return value;
  }
  if (value.ok === true) {
    throw new TypeError(`${method} response ok true cannot have forceCompleted false`);
  }
  if (value.ok !== false) {
    throw new TypeError(`${method} response ok must be false when forceCompleted is false`);
  }
  if (!hasTurnForceCompleteFailureDiagnostic(value)) {
    throw new TypeError(`${method} response failure must include errorCode, error, or message`);
  }
  return value;
}

function validateDashboardDagStartResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  requireResponseKey(method, value, ['runKey', 'run_key']);
  return value;
}

function validateDashboardDagCreateAndStartResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  requireResponseKey(method, value, ['dagKey', 'dag_key']);
  requireResponseKey(method, value, ['runKey', 'run_key']);
  return value;
}

function validateSkillReadResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  const skill = value.skill;
  if (!skill || typeof skill !== 'object' || Array.isArray(skill)) {
    throw new TypeError(`${method} response skill must be an object`);
  }
  requireResponseKey(method, skill, ['path']);
  if (!hasOwn(skill, 'content') || typeof skill.content !== 'string') {
    throw new TypeError(`${method} response skill.content must be a string`);
  }
  return value;
}

function validateAppUpdateInstallResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  if (value.started !== true) {
    throw new TypeError(`${method} response started must be true`);
  }
  if (typeof value.helper !== 'string' || !normalizeString(value.helper)) {
    throw new TypeError(`${method} response helper must be a non-empty string`);
  }
  return value;
}

const MCP_SERVER_CONTROL_RESPONSE_SPECS = Object.freeze({
  [RPC_METHODS.MCP_SERVER_SQLITE_START]: { serverName: 'sqlite', enabled: true },
  [RPC_METHODS.MCP_SERVER_SQLITE_STOP]: { serverName: 'sqlite', enabled: false },
  [RPC_METHODS.MCP_SERVER_PLAYWRIGHT_START]: { serverName: 'playwright', enabled: true },
  [RPC_METHODS.MCP_SERVER_PLAYWRIGHT_STOP]: { serverName: 'playwright', enabled: false },
});
const MCP_SERVER_LIST_RESPONSE_KEYS = new Set(['configPath', 'config_path', 'mcpServers', 'mcp_servers']);
const MCP_SERVER_STATUS_RESPONSE_KEYS = new Set(['enabled']);
const MCP_SERVER_CONTROL_RESPONSE_KEYS = new Set(['configPath', 'config_path', 'serverName', 'server_name', 'added', 'enabled']);

function assertOnlyResponseKeys(method, value, allowedKeys, label) {
  for (const key of Object.keys(value)) {
    if (!allowedKeys.has(key)) {
      throw new TypeError(`${method} response ${label} must not include ${key}`);
    }
  }
}

function validateMCPServerListResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, MCP_SERVER_LIST_RESPONSE_KEYS, 'body');
  const configPath = normalizeString(value.configPath || value.config_path);
  if (!configPath) {
    throw new Error(`${method} response configPath must be a non-empty string`);
  }
  const servers = value.mcpServers || value.mcp_servers;
  if (!servers || typeof servers !== 'object' || Array.isArray(servers)) {
    throw new TypeError(`${method} response mcpServers must be an object`);
  }
  for (const [serverName, server] of Object.entries(servers)) {
    const normalizedName = normalizeString(serverName);
    if (!normalizedName) {
      throw new Error(`${method} response mcpServers must not include an empty server name`);
    }
    if (!server || typeof server !== 'object' || Array.isArray(server)) {
      throw new TypeError(`${method} response mcpServers.${normalizedName} must be an object`);
    }
    assertOnlyResponseKeys(method, server, MCP_SERVER_STATUS_RESPONSE_KEYS, `mcpServers.${normalizedName}`);
    if (typeof server.enabled !== 'boolean') {
      throw new TypeError(`${method} response mcpServers.${normalizedName}.enabled must be a boolean`);
    }
  }
  return value;
}

function validateMCPServerControlResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, MCP_SERVER_CONTROL_RESPONSE_KEYS, 'body');
  const configPath = normalizeString(value.configPath || value.config_path);
  if (!configPath) {
    throw new Error(`${method} response configPath must be a non-empty string`);
  }
  const spec = MCP_SERVER_CONTROL_RESPONSE_SPECS[method];
  const serverName = normalizeString(value.serverName || value.server_name);
  if (!spec || serverName !== spec.serverName) {
    throw new Error(`${method} response serverName must be ${spec?.serverName || 'a known MCP server'}`);
  }
  if (value.enabled !== spec.enabled) {
    throw new TypeError(`${method} response enabled must be ${spec.enabled}`);
  }
  if (hasOwn(value, 'added') && typeof value.added !== 'boolean') {
    throw new TypeError(`${method} response added must be a boolean`);
  }
  return value;
}

function validateSchemaResponse(method, response, parser) {
  try {
    return parser(response);
  }
  catch (error) {
    throw new TypeError(`${method} response ${error.message || 'schema is invalid'}`, { cause: error });
  }
}

const validateObservabilityResultResponse = (method, response) => validateSchemaResponse(method, response, parseObservabilityResultResponse);
const validateMemorySnapshotResponse = (method, response) => validateSchemaResponse(method, response, parseMemorySnapshotResponse);
const validateSharedFilesDashboardResponse = (method, response) => validateSchemaResponse(method, response, parseSharedFilesDashboardResponse);
const validateSharedFileDetailResponse = (method, response) => validateSchemaResponse(method, response, parseSharedFileDetailResponse);
const validateModelProviderRegistryResponse = (method, response) => validateSchemaResponse(method, response, parseModelProviderRegistryResponse);

const BACKEND_RESPONSE_VALIDATORS = Object.freeze({
  [RPC_METHODS.APP_UPDATE_INSTALL]: validateAppUpdateInstallResponse,
  [RPC_METHODS.APP_UPDATE_INSTALL_LATEST]: validateAppUpdateInstallResponse,
  [RPC_METHODS.CONFIG_LSP_PROMPT_HINT_READ]: validateLspPromptHintResponse,
  [RPC_METHODS.CONFIG_LSP_PROMPT_HINT_WRITE]: validateLspPromptHintResponse,
  [RPC_METHODS.DASHBOARD_SHARED_FILES]: validateSharedFilesDashboardResponse,
  [RPC_METHODS.MCP_SERVER_LIST]: validateMCPServerListResponse,
  [RPC_METHODS.MCP_SERVER_SQLITE_START]: validateMCPServerControlResponse,
  [RPC_METHODS.MCP_SERVER_SQLITE_STOP]: validateMCPServerControlResponse,
  [RPC_METHODS.MCP_SERVER_PLAYWRIGHT_START]: validateMCPServerControlResponse,
  [RPC_METHODS.MCP_SERVER_PLAYWRIGHT_STOP]: validateMCPServerControlResponse,
  [RPC_METHODS.MODEL_PROVIDERS_APPLY]: validateModelProviderRegistryResponse,
  [RPC_METHODS.MODEL_PROVIDERS_LIST]: validateModelProviderRegistryResponse,
  [RPC_METHODS.OBSERVABILITY_ERROR_LIST]: validateObservabilityResultResponse,
  [RPC_METHODS.OBSERVABILITY_RECENT_LIST]: validateObservabilityResultResponse,
  [RPC_METHODS.OBSERVABILITY_SLOW_LIST]: validateObservabilityResultResponse,
  [RPC_METHODS.OBSERVABILITY_THREAD_RECENT]: validateObservabilityResultResponse,
  [RPC_METHODS.OBSERVABILITY_TRACE_GET]: validateObservabilityResultResponse,
  [RPC_METHODS.UI_STATE_GET]: validateUIStateResponse,
  [RPC_METHODS.UI_MEMORY_GET]: validateMemorySnapshotResponse,
  [RPC_METHODS.UI_SHARED_FILE_GET]: validateSharedFileDetailResponse,
  [RPC_METHODS.SKILLS_LOCAL_READ]: validateSkillReadResponse,
  [RPC_METHODS.THREAD_START]: validateThreadStartResponse,
  [RPC_METHODS.THREAD_MESSAGES]: validateThreadMessagesResponse,
  [RPC_METHODS.THREAD_RESOLVE]: validateThreadResolveResponse,
  [RPC_METHODS.TURN_START]: validateTurnStartResponse,
  [RPC_METHODS.TURN_FORCE_COMPLETE]: validateTurnForceCompleteResponse,
  [RPC_METHODS.DASHBOARD_DAG_START]: validateDashboardDagStartResponse,
  [RPC_METHODS.DASHBOARD_DAG_CREATE_AND_START]: validateDashboardDagCreateAndStartResponse,
});

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
  ['previewSharedFile', previewSharedFileViaBridge],
  ['beginTextClipboardWrite', beginTextClipboardWriteViaBridge],
  ['copyTextToClipboard', copyTextToClipboardViaBridge],
  ['selectDatasourceImportFile', selectDatasourceImportFileViaBridge],
  ['selectFiles', selectFilesViaBridge],
  ['selectProjectDir', selectProjectDirViaBridge],
  ['selectProjectDirs', selectProjectDirsViaBridge],
]);

/** @param {Record<string, any>} deps */
function resolveNativeDeps(deps) {
  return Object.fromEntries(NATIVE_DEP_FALLBACKS.map(([key, fallback]) => [key, deps[key] || fallback]));
}

function createBackendCaller(callAPI) {
  return async (method, params = {}) => {
    const rpcMethod = normalizeString(method);
    if (!rpcMethod) throw new Error('backend RPC method is required');
    const response = await callAPI(rpcMethod, assertPlainObject(rpcMethod, params));
    const validator = BACKEND_RESPONSE_VALIDATORS[rpcMethod];
    return validator ? validator(rpcMethod, response) : response;
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
    listModelProviders: (params) => callBackend(
      RPC_METHODS.MODEL_PROVIDERS_LIST,
      requireCwd(RPC_METHODS.MODEL_PROVIDERS_LIST, params),
    ),
    saveModelProviders: (params) => {
      const payload = requireCwd(RPC_METHODS.MODEL_PROVIDERS_SAVE, params);
      if (!payload.registry || typeof payload.registry !== 'object' || Array.isArray(payload.registry)) {
        throw new Error(`${RPC_METHODS.MODEL_PROVIDERS_SAVE}: registry is required`);
      }
      return callBackend(RPC_METHODS.MODEL_PROVIDERS_SAVE, payload);
    },
    applyModelProvider: (params) => callBackend(
      RPC_METHODS.MODEL_PROVIDERS_APPLY,
      requireKey(RPC_METHODS.MODEL_PROVIDERS_APPLY, requireCwd(RPC_METHODS.MODEL_PROVIDERS_APPLY, params), 'vendorId'),
    ),
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

function datasourceImportLocalFilePayload(params) {
  const method = RPC_METHODS.DATASOURCE_V2_IMPORT_LOCAL_FILE;
  const payload = datasourceCreatePayload(method, params);
  const pickerTokenValue = params.pickerToken !== undefined ? params.pickerToken : params.picker_token;
  const pickerToken = normalizeString(pickerTokenValue);
  return cleanObject({ ...payload, pickerToken });
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

function datasourceChunksPayload(params) {
  const method = RPC_METHODS.DATASOURCE_V2_LIST_CHUNKS;
  const payload = assertPlainObject(method, params);
  const { documentId } = datasourceDocumentIDPayload(method, payload);
  const limit = normalizeOptionalLimit(method, payload);
  if (!limit) throw new Error(`${method}: limit must be a positive integer`);
  if (!hasOwn(payload, 'cursor')) throw new Error(`${method}: cursor is required`);
  const cursor = Number(payload.cursor);
  if (!Number.isInteger(cursor) || cursor < -1) {
    throw new Error(`${method}: cursor must be -1 or greater`);
  }
  return { documentId, limit, cursor };
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
    importDatasourceLocalFile: (params) => callBackend(
      RPC_METHODS.DATASOURCE_V2_IMPORT_LOCAL_FILE,
      datasourceImportLocalFilePayload(params),
    ),
    listDatasourceDocuments: (params = {}) => callBackend(
      RPC_METHODS.DATASOURCE_V2_LIST,
      datasourceListPayload(params),
    ),
    getDatasourceDocument: (params) => callBackend(
      RPC_METHODS.DATASOURCE_V2_GET,
      datasourceDocumentIDPayload(RPC_METHODS.DATASOURCE_V2_GET, params),
    ),
    listDatasourceChunks: (params) => callBackend(
      RPC_METHODS.DATASOURCE_V2_LIST_CHUNKS,
      datasourceChunksPayload(params),
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
      requireCwd(
        RPC_METHODS.UI_MEMORY_AUTO_DREAM_SET_INTENT,
        requireBoolean(RPC_METHODS.UI_MEMORY_AUTO_DREAM_SET_INTENT, params, 'enabled'),
      ),
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
    writeWorkflowMaterial: (params) => callBackend(RPC_METHODS.DASHBOARD_WORKFLOW_MATERIAL_WRITE, dashboardWorkflowMaterialWritePayload(params)),
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
    createAndStartDag: (params) => callBackend(RPC_METHODS.DASHBOARD_DAG_CREATE_AND_START, dashboardDagCreateAndStartPayload(params)),
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
    saveWorkflowTemplate: (params) => callBackend(
      RPC_METHODS.WORKFLOW_TEMPLATES_SAVE,
      workflowTemplateSavePayload(params),
    ),
    rollbackWorkflowTemplate: (params) => callBackend(
      RPC_METHODS.WORKFLOW_TEMPLATES_ROLLBACK,
      workflowTemplateRollbackPayload(params),
    ),
  };
}

function requirePositiveInteger(method, params, key) {
  const payload = requireNumber(method, params, key);
  if (!Number.isInteger(payload[key]) || payload[key] <= 0) {
    throw new Error(`${method}: ${key} must be a positive integer`);
  }
  return payload;
}

function requireObjectField(method, payload, key) {
  if (payload[key] == null || typeof payload[key] !== 'object' || Array.isArray(payload[key])) {
    throw new Error(`${method}: ${key} must be an object`);
  }
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
    values: optionalPayloadObject(payload.values),
    user_inputs: payload.user_inputs,
    runtime_context: payload.runtime_context,
    locale: payload.locale,
  };
}

function workflowTemplateSavePayload(params) {
  const method = RPC_METHODS.WORKFLOW_TEMPLATES_SAVE;
  const payload = requirePositiveInteger(
    method,
    requireKey(method, requireKey(method, assertPlainObject(method, params), 'templateId'), 'category'),
    'version',
  );
  requireObjectField(method, payload, 'trust');
  requireObjectField(method, payload, 'compatibility');
  requireObjectField(method, payload, 'draft');
  return cleanObject({
    templateId: payload.templateId,
    version: payload.version,
    title: payload.title,
    description: payload.description,
    category: payload.category,
    business_flow: payload.business_flow,
    output_types: payload.output_types,
    tags: payload.tags,
    requires_review: payload.requires_review,
    supports_schedule: payload.supports_schedule,
    trust: payload.trust,
    compatibility: payload.compatibility,
    ui_schema: payload.ui_schema,
    validation: payload.validation,
    draft: payload.draft,
  });
}

function workflowTemplateRollbackPayload(params) {
  const method = RPC_METHODS.WORKFLOW_TEMPLATES_ROLLBACK;
  const payload = requirePositiveInteger(
    method,
    requireKey(method, assertPlainObject(method, params), 'templateId'),
    'version',
  );
  return {
    templateId: payload.templateId,
    version: payload.version,
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
    createCronJob: (params) => callBackend(RPC_METHODS.CRONJOB_CREATE, cronJobMutationPayload(RPC_METHODS.CRONJOB_CREATE, params)),
    updateCronJob: (params) => callBackend(RPC_METHODS.CRONJOB_UPDATE, cronJobMutationPayload(RPC_METHODS.CRONJOB_UPDATE, params, { requireId: true })),
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
      ...skillResolutionPayload(RPC_METHODS.SKILLS_RESOLUTION_PREVIEW, params),
    }),
    applySkillResolution: (params) => applySkillResolutionPayload(callBackend, params),
    deleteSkill: (params) => deleteSkillPayload(callBackend, params),
    createSkillTool: (params) => callBackend(RPC_METHODS.SKILL_TOOLS_CREATE, skillToolMutationPayload(RPC_METHODS.SKILL_TOOLS_CREATE, params)),
    listSkillTools: (params) => callBackend(RPC_METHODS.SKILL_TOOLS_LIST, skillToolListPayload(params)),
    getSkillTool: (params) => callBackend(RPC_METHODS.SKILL_TOOLS_GET, skillToolIDPayload(RPC_METHODS.SKILL_TOOLS_GET, params)),
    updateSkillTool: (params) => callBackend(RPC_METHODS.SKILL_TOOLS_UPDATE, skillToolUpdatePayload(params)),
    deleteSkillTool: (params) => callBackend(RPC_METHODS.SKILL_TOOLS_DELETE, skillToolIDPayload(RPC_METHODS.SKILL_TOOLS_DELETE, params)),
  };
}

function skillToolListPayload(params = {}) {
  const method = RPC_METHODS.SKILL_TOOLS_LIST;
  const payload = requireCwd(method, params);
  const limit = normalizeOptionalLimit(method, payload);
  if (!limit) throw new Error(`${method}: limit must be a positive integer`);
  return cleanObject({ cwd: payload.cwd, keyword: normalizeString(payload.keyword), limit });
}

function skillToolIDPayload(method, params) {
  const payload = requireCwd(method, params);
  const id = Number(payload.id);
  if (!Number.isInteger(id) || id <= 0) throw new Error(`${method}: id is required`);
  return { cwd: payload.cwd, id };
}

function skillToolMutationPayload(method, params) {
  const payload = requireCwd(method, params);
  const methodName = normalizeString(payload.methodName || payload.method_name || payload.name);
  const description = normalizeString(payload.description);
  if (!methodName) throw new Error(`${method}: methodName is required`);
  if (!description) throw new Error(`${method}: description is required`);
  if (typeof payload.enabled !== 'boolean') throw new Error(`${method}: enabled is required`);
  return { cwd: payload.cwd, methodName, description, enabled: payload.enabled };
}

function skillToolUpdatePayload(params) {
  const method = RPC_METHODS.SKILL_TOOLS_UPDATE;
  return { ...skillToolMutationPayload(method, params), id: skillToolIDPayload(method, params).id };
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
    content: normalizeOptionalString(payload.content),
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
  const previewID = normalizeString(payload.preview_id ?? payload.previewId);
  const previewHash = normalizeString(payload.preview_hash ?? payload.previewHash);
  if (!previewID) throw new Error(`${RPC_METHODS.SKILLS_RESOLUTION_APPLY}: preview_id is required`);
  if (!previewHash) throw new Error(`${RPC_METHODS.SKILLS_RESOLUTION_APPLY}: preview_hash is required`);
  return callBackend(RPC_METHODS.SKILLS_RESOLUTION_APPLY, cleanObject({
    cwd: requireCwd(RPC_METHODS.SKILLS_RESOLUTION_APPLY, payload).cwd,
    ...skillResolutionPayload(RPC_METHODS.SKILLS_RESOLUTION_APPLY, payload),
    preview_id: previewID,
    preview_hash: previewHash,
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

function rejectUnsupportedParamsPayload(method, params = {}) {
  const payload = assertPlainObject(method, params);
  if (Object.keys(payload).length > 0) throw new Error(`${method}: params are not supported`);
  return {};
}

function normalizeMCPToolLifecycleString(method, value, key) {
  if (value === undefined || value === null) return '';
  if (typeof value !== 'string') throw new Error(`${method}: ${key} must be a string`);
  return value.trim();
}

function mcpToolLifecycleString(method, payload, camelKey, snakeKey = camelKey) {
  const camelValue = takePayloadField(payload, camelKey);
  const snakeValue = snakeKey === camelKey ? undefined : takePayloadField(payload, snakeKey);
  const value = camelValue === undefined || camelValue === null || camelValue === '' ? snakeValue : camelValue;
  return normalizeMCPToolLifecycleString(method, value, camelKey);
}

function mcpToolLifecycleSetPayload(params) {
  const method = RPC_METHODS.MCP_TOOL_LIFECYCLE_SET;
  const payload = { ...assertStrictPlainObject(method, params) };
  const serverName = mcpToolLifecycleString(method, payload, 'serverName', 'server_name');
  const toolName = mcpToolLifecycleString(method, payload, 'toolName', 'tool_name');
  const state = mcpToolLifecycleString(method, payload, 'state');
  const workspaceRoot = mcpToolLifecycleString(method, payload, 'workspaceRoot', 'workspace_root');
  const manifestName = mcpToolLifecycleString(method, payload, 'manifestName', 'manifest_name');
  const reason = mcpToolLifecycleString(method, payload, 'reason');
  const replacementTool = mcpToolLifecycleString(method, payload, 'replacementTool', 'replacement_tool');
  assertNoExtraPayloadFields(method, payload);
  if (!serverName) throw new Error(`${method}: serverName is required`);
  if (!toolName) throw new Error(`${method}: toolName is required`);
  if (!state) throw new Error(`${method}: state is required`);
  if (!MCP_TOOL_LIFECYCLE_STATES.has(state)) {
    throw new Error(`${method}: state must be enabled, disabled, suspended, or removed`);
  }
  return cleanObject({
    workspaceRoot,
    serverName,
    manifestName,
    toolName,
    state,
    reason,
    replacementTool,
  });
}

function mcpToolLifecycleListPayload(params) {
  const method = RPC_METHODS.MCP_TOOL_LIFECYCLE_LIST;
  const payload = { ...assertStrictPlainObject(method, params) };
  const serverName = mcpToolLifecycleString(method, payload, 'serverName', 'server_name');
  const workspaceRoot = mcpToolLifecycleString(method, payload, 'workspaceRoot', 'workspace_root');
  assertNoExtraPayloadFields(method, payload);
  if (!serverName) throw new Error(`${method}: serverName is required`);
  return cleanObject({
    workspaceRoot,
    serverName,
  });
}

function mcpToolLifecycleExportPayload(params = {}) {
  const method = RPC_METHODS.MCP_TOOL_LIFECYCLE_EXPORT;
  const payload = { ...assertStrictPlainObject(method, params) };
  const workspaceRoot = mcpToolLifecycleString(method, payload, 'workspaceRoot', 'workspace_root');
  assertNoExtraPayloadFields(method, payload);
  return cleanObject({
    workspaceRoot,
  });
}

function createMCPServerApi(callBackend) {
  return {
    listMCPServers: (params = {}) => callBackend(
      RPC_METHODS.MCP_SERVER_LIST,
      rejectUnsupportedParamsPayload(RPC_METHODS.MCP_SERVER_LIST, params),
    ),
    startSQLiteMCPServer: (params = {}) => callBackend(
      RPC_METHODS.MCP_SERVER_SQLITE_START,
      rejectUnsupportedParamsPayload(RPC_METHODS.MCP_SERVER_SQLITE_START, params),
    ),
    stopSQLiteMCPServer: (params = {}) => callBackend(
      RPC_METHODS.MCP_SERVER_SQLITE_STOP,
      rejectUnsupportedParamsPayload(RPC_METHODS.MCP_SERVER_SQLITE_STOP, params),
    ),
    startPlaywrightMCPServer: (params = {}) => callBackend(
      RPC_METHODS.MCP_SERVER_PLAYWRIGHT_START,
      rejectUnsupportedParamsPayload(RPC_METHODS.MCP_SERVER_PLAYWRIGHT_START, params),
    ),
    stopPlaywrightMCPServer: (params = {}) => callBackend(
      RPC_METHODS.MCP_SERVER_PLAYWRIGHT_STOP,
      rejectUnsupportedParamsPayload(RPC_METHODS.MCP_SERVER_PLAYWRIGHT_STOP, params),
    ),
    setMCPToolLifecycle: (params) => callBackend(
      RPC_METHODS.MCP_TOOL_LIFECYCLE_SET,
      mcpToolLifecycleSetPayload(params),
    ),
    listMCPToolLifecycle: (params) => callBackend(
      RPC_METHODS.MCP_TOOL_LIFECYCLE_LIST,
      mcpToolLifecycleListPayload(params),
    ),
    exportMCPToolLifecycle: (params = {}) => callBackend(
      RPC_METHODS.MCP_TOOL_LIFECYCLE_EXPORT,
      mcpToolLifecycleExportPayload(params),
    ),
  };
}

function createThreadApi(callBackend) {
  return {
    listThreadsPage: (params) => callBackend(RPC_METHODS.THREAD_LIST_PAGE, threadListPagePayload(RPC_METHODS.THREAD_LIST_PAGE, params)),
    listLoadedThreadsPage: (params) => callBackend(RPC_METHODS.THREAD_LOADED_LIST_PAGE, threadListPagePayload(RPC_METHODS.THREAD_LOADED_LIST_PAGE, params)),
    getThreadMessages: (params) => callBackend(RPC_METHODS.THREAD_MESSAGES, threadMessagesPayload(params)),
    resolveThreadIdentity: (params) => callBackend(RPC_METHODS.THREAD_RESOLVE, threadIdOnlyPayload(RPC_METHODS.THREAD_RESOLVE, params)),
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

function threadListPagePayload(method, params = {}) {
  const payload = assertPlainObject(method, params);
  const limit = normalizeOptionalLimit(method, payload);
  if (!limit) throw new Error(`${method}: limit is required`);
  return cleanObject({
    limit,
    cursor_created_at: normalizeOptionalCursorInteger(method, payload, 'cursorCreatedAt', 'cursor_created_at'),
    cursor_thread_id: normalizeString(payload.cursorThreadId || payload.cursor_thread_id),
  });
}

function threadIdOnlyPayload(method, params) {
  const { unused, threadId } = threadScopedPayload(method, params);
  assertNoExtraPayloadFields(method, unused);
  return { threadId };
}

function threadMessagesPayload(params) {
  const { unused, threadId } = threadScopedPayload(RPC_METHODS.THREAD_MESSAGES, params);
  const limit = takePayloadField(unused, 'limit');
  const before = takePayloadField(unused, 'before');
  assertNoExtraPayloadFields(RPC_METHODS.THREAD_MESSAGES, unused);
  return cleanObject({ threadId, limit, before });
}

function threadConfigPayload(params) {
  const { unused, threadId } = threadScopedPayload(RPC_METHODS.THREAD_CONFIG_SET, params);
  const model = normalizeProviderConfigValue(takePayloadField(unused, 'model'));
  const effort = normalizeProviderConfigValue(takePayloadField(unused, 'effort'));
  assertNoExtraPayloadFields(RPC_METHODS.THREAD_CONFIG_SET, unused);
  return {
    threadId,
    model,
    effort,
  };
}

function threadScopedPayload(method, params) {
  const payload = assertPlainObject(method, params);
  const threadId = resolveThreadIdAliases(method, payload);
  const unused = { ...payload };
  takePayloadField(unused, 'threadId');
  takePayloadField(unused, 'thread_id');
  takePayloadField(unused, 'cwd');
  return { unused, threadId };
}

function resolveThreadIdAliases(method, payload) {
  const camel = hasOwn(payload, 'threadId') ? normalizeString(payload.threadId) : '';
  const snake = hasOwn(payload, 'thread_id') ? normalizeString(payload.thread_id) : '';
  if (camel && snake && camel !== snake) {
    throw new Error(`${method}: conflicting threadId values for threadId and thread_id`);
  }
  const threadId = camel || snake;
  if (!threadId) {
    throw new Error(`${method}: threadId is required`);
  }
  return threadId;
}

function threadStartPayload(params) {
  const payload = requireCwd(RPC_METHODS.THREAD_START, params);
  const unused = { ...payload };
  const providerRaw = takePayloadField(unused, 'provider');
  const modelProvider = takePayloadField(unused, 'modelProvider');
  const modelProviderSnake = takePayloadField(unused, 'model_provider');
  takePayloadField(unused, 'codexModelProvider');
  takePayloadField(unused, 'codex_model_provider');
  const promptKey = normalizeString(takePayloadField(unused, 'promptKey') || takePayloadField(unused, 'prompt_key'));
  const agentKey = normalizeString(takePayloadField(unused, 'agentKey') || takePayloadField(unused, 'agent_key'));
  const deferSpawn = takePayloadField(unused, 'deferSpawn') ?? takePayloadField(unused, 'defer_spawn');
  const toolSurfaceModeRaw = takePayloadField(unused, 'toolSurfaceMode') || takePayloadField(unused, 'tool_surface_mode');
  takePayloadField(unused, 'optimisticUserMessage');
  takePayloadField(unused, 'optimistic_user_message');
  takePayloadField(unused, 'skipInitialRuntimeSync');
  takePayloadField(unused, 'skip_initial_runtime_sync');
  const request = cleanObject({
    cwd: takePayloadField(unused, 'cwd'),
    agentId: takePayloadField(unused, 'agentId'),
    agent_id: takePayloadField(unused, 'agent_id'),
    agent_type: takePayloadField(unused, 'agent_type'),
    agentMemoryScope: takePayloadField(unused, 'agentMemoryScope'),
    agentType: takePayloadField(unused, 'agentType'),
    agent_memory_scope: takePayloadField(unused, 'agent_memory_scope'),
    approvalPolicy: takePayloadField(unused, 'approvalPolicy'),
    approval_policy: takePayloadField(unused, 'approval_policy'),
    baseInstructions: takePayloadField(unused, 'baseInstructions'),
    base_instructions: takePayloadField(unused, 'base_instructions'),
    config: takePayloadField(unused, 'config'),
    developerInstructions: takePayloadField(unused, 'developerInstructions'),
    developer_instructions: takePayloadField(unused, 'developer_instructions'),
    effort: takePayloadField(unused, 'effort'),
    instructions: takePayloadField(unused, 'instructions'),
    language: takePayloadField(unused, 'language'),
    launchIntentId: takePayloadField(unused, 'launchIntentId'),
    launch_intent_id: takePayloadField(unused, 'launch_intent_id'),
    manualSkillSelection: takePayloadField(unused, 'manualSkillSelection'),
    manual_skill_selection: takePayloadField(unused, 'manual_skill_selection'),
    memoryScope: takePayloadField(unused, 'memoryScope'),
    memory_scope: takePayloadField(unused, 'memory_scope'),
    model: takePayloadField(unused, 'model'),
    name: takePayloadField(unused, 'name'),
    parentAgentId: takePayloadField(unused, 'parentAgentId'),
    parentID: takePayloadField(unused, 'parentID'),
    parentId: takePayloadField(unused, 'parentId'),
    parent_agent_id: takePayloadField(unused, 'parent_agent_id'),
    personality: takePayloadField(unused, 'personality'),
    prompt: takePayloadField(unused, 'prompt'),
    sandbox: takePayloadField(unused, 'sandbox'),
    selectedSkillRefs: takePayloadField(unused, 'selectedSkillRefs'),
    selectedSkills: takePayloadField(unused, 'selectedSkills'),
    selected_skill_refs: takePayloadField(unused, 'selected_skill_refs'),
    selected_skills: takePayloadField(unused, 'selected_skills'),
    summary: takePayloadField(unused, 'summary'),
  });
  assertNoExtraPayloadFields(RPC_METHODS.THREAD_START, unused);
  const provider = normalizeString(modelProvider || modelProviderSnake || providerRaw);
  if (!provider) throw new Error(`${RPC_METHODS.THREAD_START}: provider is required`);
  request.provider = provider;
  const toolSurfaceMode = normalizeToolSurfaceMode(toolSurfaceModeRaw);
  if (promptKey) request.prompt_key = promptKey;
  if (agentKey) request.agent_key = agentKey;
  if (toolSurfaceMode) request.toolSurfaceMode = toolSurfaceMode;
  if (deferSpawn === true) request.defer_spawn = true;
  return request;
}

function turnStartPayload(params) {
  const payload = requireThreadId(RPC_METHODS.TURN_START, requireCwd(RPC_METHODS.TURN_START, params));
  const unused = { ...payload };
  const input = takePayloadField(unused, 'input');
  const attachments = takePayloadField(unused, 'attachments');
  const request = takePayloadFields(unused, [
    'additionalWorkingDirectories',
    'additional_working_directories',
    'approvalPolicy',
    'approval_policy',
    'cwd',
    'effort',
    'enabledTools',
    'enabled_tools',
    'files',
    'gitRoot',
    'git_root',
    'images',
    'isWorktree',
    'is_worktree',
    'language',
    'manualSkillSelection',
    'manual_skill_selection',
    'mcpSnapshot',
    'mcp_snapshot',
    'model',
    'outputSchema',
    'output_schema',
    'prompt',
    'provider',
    'selectedSkillRefs',
    'selectedSkills',
    'selected_skill_refs',
    'selected_skills',
    'sessionFlags',
    'session_flags',
    'threadID',
    'threadId',
    'thread_id',
  ]);
  assertNoExtraPayloadFields(RPC_METHODS.TURN_START, unused);
  if (normalizeString(request.prompt) && hasAttachmentInputContent(attachments)) {
    throw new Error(`${RPC_METHODS.TURN_START}: prompt and attachments cannot both contain content`);
  }
  return { ...request, ...normalizeTurnInput(input, attachments) };
}

function turnInterruptPayload(params) {
  const { unused, threadId } = threadScopedPayload(RPC_METHODS.TURN_INTERRUPT, requireCwd(RPC_METHODS.TURN_INTERRUPT, params));
  const source = normalizeString(takePayloadField(unused, 'source'));
  takePayloadField(unused, 'turnId');
  takePayloadField(unused, 'turn_id');
  assertNoExtraPayloadFields(RPC_METHODS.TURN_INTERRUPT, unused);
  return cleanObject({ thread_id: threadId, source });
}

function forceCompleteTurnPayload(params) {
  const payload = requireThreadId(RPC_METHODS.TURN_FORCE_COMPLETE, requireCwd(RPC_METHODS.TURN_FORCE_COMPLETE, params));
  const unused = { ...payload };
  delete unused.cwd;
  delete unused.threadId;
  delete unused.thread_id;
  assertNoExtraPayloadFields(RPC_METHODS.TURN_FORCE_COMPLETE, unused);
  return { threadId: payload.threadId };
}

function approvalRespondPayload(params) {
  const payload = assertPlainObject(RPC_METHODS.APPROVAL_RESPOND, params);
  const {
    approved,
    requestId,
    request_id: requestIdAlias,
    ...unused
  } = payload;
  assertNoExtraPayloadFields(RPC_METHODS.APPROVAL_RESPOND, unused);
  const normalizedRequestId = positiveApprovalRequestIdFromFields(payload);
  if (normalizedRequestId <= 0) {
    const hasRequestId = hasOwn(payload, 'requestId') || hasOwn(payload, 'request_id');
    const rawRequestId = hasOwn(payload, 'requestId') ? requestId : requestIdAlias;
    if (!hasRequestId || rawRequestId === undefined || rawRequestId === null || rawRequestId === '' || rawRequestId === 0) {
      throw new Error(`${RPC_METHODS.APPROVAL_RESPOND}: requestId is required`);
    }
    throw new Error(`${RPC_METHODS.APPROVAL_RESPOND}: requestId must be a positive integer`);
  }
  if (!hasOwn(payload, 'approved')) throw new Error(`${RPC_METHODS.APPROVAL_RESPOND}: approved is required`);
  if (typeof approved !== 'boolean') throw new Error(`${RPC_METHODS.APPROVAL_RESPOND}: approved must be boolean`);
  return { requestId: normalizedRequestId, approved };
}

function compactThreadPayload(params) {
  const { unused, threadId } = threadScopedPayload(RPC_METHODS.THREAD_COMPACT_START, requireCwd(RPC_METHODS.THREAD_COMPACT_START, params));
  const args = takePayloadField(unused, 'args');
  assertNoExtraPayloadFields(RPC_METHODS.THREAD_COMPACT_START, unused);
  return cleanObject({ threadId, args });
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
    openSharedFile: (params) => {
      const payload = requireKey('openSharedFile', assertPlainObject('openSharedFile', params), 'path');
      return payload.preview === true
        ? native.previewSharedFile({ path: payload.path })
        : native.openSharedFile({ path: payload.path });
    },
    previewSharedFile: (params) => native.previewSharedFile(requireKey('previewSharedFile', assertPlainObject('previewSharedFile', params), 'path')),
    beginTextClipboardWrite: native.beginTextClipboardWrite,
    copyTextToClipboard: native.copyTextToClipboard,
    selectDatasourceImportFile: native.selectDatasourceImportFile,
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
export const listModelProviders = backendApi.listModelProviders;
export const saveModelProviders = backendApi.saveModelProviders;
export const applyModelProvider = backendApi.applyModelProvider;
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
export const writeWorkflowMaterial = backendApi.writeWorkflowMaterial;
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
export const createAndStartDag = backendApi.createAndStartDag;
export const dispatchDagNode = backendApi.dispatchDagNode;
export const terminateDagRun = backendApi.terminateDagRun;
export const terminateDag = backendApi.terminateDag;
export const deleteDag = backendApi.deleteDag;
export const applyDagOps = backendApi.applyDagOps;
export const listWorkflowTemplates = backendApi.listWorkflowTemplates;
export const getWorkflowTemplate = backendApi.getWorkflowTemplate;
export const renderWorkflowTemplateDraft = backendApi.renderWorkflowTemplateDraft;
export const saveWorkflowTemplate = backendApi.saveWorkflowTemplate;
export const rollbackWorkflowTemplate = backendApi.rollbackWorkflowTemplate;
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
export const createSkillTool = backendApi.createSkillTool;
export const listSkillTools = backendApi.listSkillTools;
export const getSkillTool = backendApi.getSkillTool;
export const updateSkillTool = backendApi.updateSkillTool;
export const deleteSkillTool = backendApi.deleteSkillTool;
export const createDatasourceDocument = backendApi.createDatasourceDocument;
export const importDatasourceLocalFile = backendApi.importDatasourceLocalFile;
export const listDatasourceDocuments = backendApi.listDatasourceDocuments;
export const getDatasourceDocument = backendApi.getDatasourceDocument;
export const listDatasourceChunks = backendApi.listDatasourceChunks;
export const updateDatasourceDocument = backendApi.updateDatasourceDocument;
export const deleteDatasourceDocument = backendApi.deleteDatasourceDocument;
export const listMCPServers = backendApi.listMCPServers;
export const startSQLiteMCPServer = backendApi.startSQLiteMCPServer;
export const stopSQLiteMCPServer = backendApi.stopSQLiteMCPServer;
export const startPlaywrightMCPServer = backendApi.startPlaywrightMCPServer;
export const stopPlaywrightMCPServer = backendApi.stopPlaywrightMCPServer;
export const setMCPToolLifecycle = backendApi.setMCPToolLifecycle;
export const listMCPToolLifecycle = backendApi.listMCPToolLifecycle;
export const exportMCPToolLifecycle = backendApi.exportMCPToolLifecycle;
export const listThreadsPage = backendApi.listThreadsPage;
export const listLoadedThreadsPage = backendApi.listLoadedThreadsPage;
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
export const previewSharedFile = backendApi.previewSharedFile;
export const beginTextClipboardWrite = backendApi.beginTextClipboardWrite;
export const copyTextToClipboard = backendApi.copyTextToClipboard;
export const selectFiles = backendApi.selectFiles;
export const selectDatasourceImportFile = backendApi.selectDatasourceImportFile;
export const selectProjectDir = backendApi.selectProjectDir;
export const selectProjectDirs = backendApi.selectProjectDirs;
export { registerBridgeLogStore, sendFrontendLogBatch, emitFrontendTraceEvent };
