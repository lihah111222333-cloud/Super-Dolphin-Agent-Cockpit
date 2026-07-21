import { RPC_METHODS } from './backendRpcMethods.js';
import {
  MCP_TOOL_LIFECYCLE_STATES,
  assertPlainObject,
  assertStrictPlainObject,
  assertNoExtraPayloadFields,
  takePayloadField,
  normalizeString,
  normalizeOptionalString,
  optionalPayloadObject,
  requireCwd,
  requireKey,
  cleanObject,
  requireSkillScope,
  requireContent,
  requirePaths,
  normalizeOptionalLimit,
} from './backendApiCommon.js';
import {
  skillPersonalType,
  normalizeSkillSummarySuggestion,
  skillResolutionPayload,
  requireNumber,
  cronIdPayload,
  cronSetEnabledPayload,
  cronListRunsPayload,
  cronListPayload,
  cronJobMutationPayload,
  codeFilePayload,
} from './backendApiPayloads.js';

/** @typedef {(method: string, payload: Record<string, unknown>) => Promise<unknown>} BackendCaller */
/** @typedef {Record<string, unknown>} OpsPayload */

/** @param {string} method @param {unknown} params @param {string} key */
function requirePositiveInteger(method, params, key) {
  const payload = requireNumber(method, params, key);
  const value = payload[key];
  if (typeof value !== 'number' || !Number.isInteger(value) || value <= 0) {
    throw new Error(`${method}: ${key} must be a positive integer`);
  }
  return payload;
}

/** @param {string} method @param {OpsPayload} payload @param {string} key */
function requireObjectField(method, payload, key) {
  if (payload[key] == null || typeof payload[key] !== 'object' || Array.isArray(payload[key])) {
    throw new Error(`${method}: ${key} must be an object`);
  }
}

/** @param {unknown} params */
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

/** @param {unknown} params */
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

/** @param {unknown} params */
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

/** @param {unknown} params */
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

/** @param {BackendCaller} callBackend */
function createCronApi(callBackend) {
  return {
    listCronJobs: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.CRONJOB_LIST, cronListPayload(params)),
    getCronJob: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.CRONJOB_GET, cronIdPayload(RPC_METHODS.CRONJOB_GET, params)),
    createCronJob: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.CRONJOB_CREATE, cronJobMutationPayload(RPC_METHODS.CRONJOB_CREATE, params)),
    updateCronJob: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.CRONJOB_UPDATE, cronJobMutationPayload(RPC_METHODS.CRONJOB_UPDATE, params, { requireId: true })),
    deleteCronJob: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.CRONJOB_DELETE, cronIdPayload(RPC_METHODS.CRONJOB_DELETE, params)),
    runCronJobOnce: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.CRONJOB_RUN_ONCE, cronIdPayload(RPC_METHODS.CRONJOB_RUN_ONCE, params)),
    setCronJobEnabled: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.CRONJOB_SET_ENABLED, cronSetEnabledPayload(params)),
    listCronJobRuns: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.CRONJOB_LIST_RUNS, cronListRunsPayload(params)),
  };
}

/** @param {BackendCaller} callBackend */
function createCodeApi(callBackend) {
  return {
    locateCodeFile: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.UI_CODE_LOCATE, codeFilePayload(RPC_METHODS.UI_CODE_LOCATE, params)),
    openCodeFile: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.UI_CODE_OPEN, codeFilePayload(RPC_METHODS.UI_CODE_OPEN, params, { includePosition: true })),
    openPath: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.UI_PATH_OPEN, codeFilePayload(RPC_METHODS.UI_PATH_OPEN, params, { includePosition: true })),
    saveCodeFile: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.UI_CODE_SAVE, codeFilePayload(RPC_METHODS.UI_CODE_SAVE, params, { includeContent: true })),
  };
}

/** @param {BackendCaller} callBackend */
function createSkillApi(callBackend) {
  return {
    readSkill: (/** @type {unknown} */ params) => callBackend(
      RPC_METHODS.SKILLS_LOCAL_READ,
      requireKey(RPC_METHODS.SKILLS_LOCAL_READ, requireCwd(RPC_METHODS.SKILLS_LOCAL_READ, params), 'path'),
    ),
    listSkillFiles: (/** @type {unknown} */ params) => callBackend(
      RPC_METHODS.SKILLS_LOCAL_LIST_FILES,
      requireKey(RPC_METHODS.SKILLS_LOCAL_LIST_FILES, requireCwd(RPC_METHODS.SKILLS_LOCAL_LIST_FILES, params), 'dir'),
    ),
    writeSkill: (/** @type {unknown} */ params) => writeSkillPayload(callBackend, params),
    createSkill: (/** @type {unknown} */ params) => createSkillPayload(callBackend, params),
    importSkillDirectories: (/** @type {unknown} */ params) => importSkillDirectoriesPayload(callBackend, params),
    suggestSkillSummary: (/** @type {unknown} */ params) => suggestSkillSummaryPayload(callBackend, params),
    listSkillResolutions: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.SKILLS_RESOLUTION_LIST, requireCwd(RPC_METHODS.SKILLS_RESOLUTION_LIST, params)),
    previewSkillResolution: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.SKILLS_RESOLUTION_PREVIEW, {
      cwd: requireCwd(RPC_METHODS.SKILLS_RESOLUTION_PREVIEW, params).cwd,
      ...skillResolutionPayload(RPC_METHODS.SKILLS_RESOLUTION_PREVIEW, params),
    }),
    applySkillResolution: (/** @type {unknown} */ params) => applySkillResolutionPayload(callBackend, params),
    deleteSkill: (/** @type {unknown} */ params) => deleteSkillPayload(callBackend, params),
    createSkillTool: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.SKILL_TOOLS_CREATE, skillToolMutationPayload(RPC_METHODS.SKILL_TOOLS_CREATE, params)),
    listSkillTools: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.SKILL_TOOLS_LIST, skillToolListPayload(params)),
    getSkillTool: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.SKILL_TOOLS_GET, skillToolIDPayload(RPC_METHODS.SKILL_TOOLS_GET, params)),
    updateSkillTool: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.SKILL_TOOLS_UPDATE, skillToolUpdatePayload(params)),
    deleteSkillTool: (/** @type {unknown} */ params) => callBackend(RPC_METHODS.SKILL_TOOLS_DELETE, skillToolIDPayload(RPC_METHODS.SKILL_TOOLS_DELETE, params)),
  };
}

/** @param {unknown} params */
function skillToolListPayload(params = {}) {
  const method = RPC_METHODS.SKILL_TOOLS_LIST;
  const payload = /** @type {OpsPayload & { cwd: string }} */ (requireCwd(method, params));
  const limit = normalizeOptionalLimit(method, payload);
  if (!limit) throw new Error(`${method}: limit must be a positive integer`);
  return cleanObject({ cwd: payload.cwd, keyword: normalizeString(payload.keyword), limit });
}

/** @param {string} method @param {unknown} params */
function skillToolIDPayload(method, params) {
  const payload = /** @type {OpsPayload & { cwd: string }} */ (requireCwd(method, params));
  const id = Number(payload.id);
  if (!Number.isInteger(id) || id <= 0) throw new Error(`${method}: id is required`);
  return { cwd: payload.cwd, id };
}

/** @param {string} method @param {unknown} params */
function skillToolMutationPayload(method, params) {
  const payload = /** @type {OpsPayload & { cwd: string }} */ (requireCwd(method, params));
  const methodName = normalizeString(payload.methodName || payload.method_name || payload.name);
  const description = normalizeString(payload.description);
  if (!methodName) throw new Error(`${method}: methodName is required`);
  if (!description) throw new Error(`${method}: description is required`);
  if (typeof payload.enabled !== 'boolean') throw new Error(`${method}: enabled is required`);
  return { cwd: payload.cwd, methodName, description, enabled: payload.enabled };
}

/** @param {unknown} params */
function skillToolUpdatePayload(params) {
  const method = RPC_METHODS.SKILL_TOOLS_UPDATE;
  return { ...skillToolMutationPayload(method, params), id: skillToolIDPayload(method, params).id };
}

/** @param {BackendCaller} callBackend @param {unknown} params */
function createSkillPayload(callBackend, params) {
  const payload = /** @type {OpsPayload & { content: string, cwd: string, name: string }} */ (requireContent(
    RPC_METHODS.SKILLS_CREATE,
    requireKey(RPC_METHODS.SKILLS_CREATE, requireCwd(RPC_METHODS.SKILLS_CREATE, params), 'name'),
  ));
  if (!payload.content.trim()) throw new Error(`${RPC_METHODS.SKILLS_CREATE}: content is required`);
  return callBackend(RPC_METHODS.SKILLS_CREATE, {
    cwd: payload.cwd,
    name: payload.name,
    content: payload.content,
  });
}

/** @param {BackendCaller} callBackend @param {unknown} params */
function writeSkillPayload(callBackend, params) {
  const payload = /** @type {OpsPayload & { content: string, cwd: string, path: string, scope: string }} */ (requireSkillScope(
    RPC_METHODS.SKILLS_LOCAL_WRITE,
    requireContent(RPC_METHODS.SKILLS_LOCAL_WRITE, requireKey(RPC_METHODS.SKILLS_LOCAL_WRITE, requireCwd(RPC_METHODS.SKILLS_LOCAL_WRITE, params), 'path')),
  ));
  return callBackend(RPC_METHODS.SKILLS_LOCAL_WRITE, cleanObject({
    cwd: payload.cwd,
    path: payload.path,
    content: payload.content,
    scope: payload.scope,
    personal_type: skillPersonalType(payload),
  }));
}

/** @param {BackendCaller} callBackend @param {unknown} params */
function importSkillDirectoriesPayload(callBackend, params) {
  const payload = /** @type {OpsPayload & { cwd: string, paths: unknown[], scope: string }} */ (requireSkillScope(
    RPC_METHODS.SKILLS_LOCAL_IMPORT_DIR,
    requirePaths(RPC_METHODS.SKILLS_LOCAL_IMPORT_DIR, requireCwd(RPC_METHODS.SKILLS_LOCAL_IMPORT_DIR, params)),
  ));
  return callBackend(RPC_METHODS.SKILLS_LOCAL_IMPORT_DIR, cleanObject({
    cwd: payload.cwd,
    paths: payload.paths,
    scope: payload.scope,
    personal_type: skillPersonalType(payload),
  }));
}

/** @param {BackendCaller} callBackend @param {unknown} params */
async function suggestSkillSummaryPayload(callBackend, params) {
  const payload = /** @type {OpsPayload & { cwd: string }} */ (requireCwd(RPC_METHODS.SKILLS_SUMMARY_SUGGEST, params));
  /** @type {OpsPayload} */
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

/** @param {BackendCaller} callBackend @param {unknown} params */
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

/** @param {BackendCaller} callBackend @param {unknown} params */
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

/** @param {string} method @param {unknown} params */
function rejectUnsupportedParamsPayload(method, params = {}) {
  const payload = assertPlainObject(method, params);
  if (Object.keys(payload).length > 0) throw new Error(`${method}: params are not supported`);
  return {};
}

/** @param {string} method @param {unknown} value @param {string} key @returns {string} */
function normalizeMCPToolLifecycleString(method, value, key) {
  if (value === undefined || value === null) return '';
  if (typeof value !== 'string') throw new Error(`${method}: ${key} must be a string`);
  return value.trim();
}

/** @param {string} method @param {OpsPayload} payload @param {string} camelKey @param {string} snakeKey */
function mcpToolLifecycleString(method, payload, camelKey, snakeKey = camelKey) {
  const camelValue = takePayloadField(payload, camelKey);
  const snakeValue = snakeKey === camelKey ? undefined : takePayloadField(payload, snakeKey);
  const value = camelValue === undefined || camelValue === null || camelValue === '' ? snakeValue : camelValue;
  return normalizeMCPToolLifecycleString(method, value, camelKey);
}

/** @param {unknown} params */
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

/** @param {unknown} params */
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

/** @param {unknown} params */
function mcpToolLifecycleExportPayload(params = {}) {
  const method = RPC_METHODS.MCP_TOOL_LIFECYCLE_EXPORT;
  const payload = { ...assertStrictPlainObject(method, params) };
  const workspaceRoot = mcpToolLifecycleString(method, payload, 'workspaceRoot', 'workspace_root');
  assertNoExtraPayloadFields(method, payload);
  return cleanObject({
    workspaceRoot,
  });
}

/** @param {BackendCaller} callBackend */
function createMCPServerApi(callBackend) {
  return {
    listToolbridgeTools: (/** @type {unknown} */ params) => callBackend(
      RPC_METHODS.TOOLBRIDGE_TOOLS_LIST,
      toolbridgeToolsListPayload(params),
    ),
    listMCPServers: (/** @type {unknown} */ params = {}) => callBackend(
      RPC_METHODS.MCP_SERVER_LIST,
      rejectUnsupportedParamsPayload(RPC_METHODS.MCP_SERVER_LIST, params),
    ),
    startSQLiteMCPServer: (/** @type {unknown} */ params = {}) => callBackend(
      RPC_METHODS.MCP_SERVER_SQLITE_START,
      rejectUnsupportedParamsPayload(RPC_METHODS.MCP_SERVER_SQLITE_START, params),
    ),
    stopSQLiteMCPServer: (/** @type {unknown} */ params = {}) => callBackend(
      RPC_METHODS.MCP_SERVER_SQLITE_STOP,
      rejectUnsupportedParamsPayload(RPC_METHODS.MCP_SERVER_SQLITE_STOP, params),
    ),
    startPlaywrightMCPServer: (/** @type {unknown} */ params = {}) => callBackend(
      RPC_METHODS.MCP_SERVER_PLAYWRIGHT_START,
      rejectUnsupportedParamsPayload(RPC_METHODS.MCP_SERVER_PLAYWRIGHT_START, params),
    ),
    stopPlaywrightMCPServer: (/** @type {unknown} */ params = {}) => callBackend(
      RPC_METHODS.MCP_SERVER_PLAYWRIGHT_STOP,
      rejectUnsupportedParamsPayload(RPC_METHODS.MCP_SERVER_PLAYWRIGHT_STOP, params),
    ),
    setMCPToolLifecycle: (/** @type {unknown} */ params) => callBackend(
      RPC_METHODS.MCP_TOOL_LIFECYCLE_SET,
      mcpToolLifecycleSetPayload(params),
    ),
    listMCPToolLifecycle: (/** @type {unknown} */ params) => callBackend(
      RPC_METHODS.MCP_TOOL_LIFECYCLE_LIST,
      mcpToolLifecycleListPayload(params),
    ),
    exportMCPToolLifecycle: (/** @type {unknown} */ params = {}) => callBackend(
      RPC_METHODS.MCP_TOOL_LIFECYCLE_EXPORT,
      mcpToolLifecycleExportPayload(params),
    ),
  };
}

/** @param {unknown} params */
function toolbridgeToolsListPayload(params) {
  const method = RPC_METHODS.TOOLBRIDGE_TOOLS_LIST;
  const payload = { ...assertStrictPlainObject(method, params) };
  const cwd = normalizeString(takePayloadField(payload, 'cwd'));
  assertNoExtraPayloadFields(method, payload);
  if (!cwd) throw new Error(`${method}: cwd is required`);
  return { cwd };
}


export {
  requirePositiveInteger, requireObjectField, workflowTemplateRenderPayload, workflowTemplateSavePayload, workflowTemplateRollbackPayload, workflowTemplateListPayload,
  createCronApi, createCodeApi, createSkillApi, skillToolListPayload, skillToolIDPayload, skillToolMutationPayload,
  skillToolUpdatePayload, createSkillPayload, writeSkillPayload, importSkillDirectoriesPayload, suggestSkillSummaryPayload, applySkillResolutionPayload,
  deleteSkillPayload, rejectUnsupportedParamsPayload, normalizeMCPToolLifecycleString, mcpToolLifecycleString, mcpToolLifecycleSetPayload, mcpToolLifecycleListPayload,
  mcpToolLifecycleExportPayload, createMCPServerApi,
};
