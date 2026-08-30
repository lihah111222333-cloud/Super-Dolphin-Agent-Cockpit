import { RPC_METHODS } from './backendRpcMethods.js';
import {
  MCP_TOOL_LIFECYCLE_STATES,
  assertPlainObject,
  assertStrictPlainObject,
  assertNoExtraPayloadFields,
  takePayloadField,
  normalizeString,
  optionalPayloadObject,
  requireKey,
  cleanObject,
} from './backendApiCommon.js';
import {
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
  createCronApi, createCodeApi, rejectUnsupportedParamsPayload, normalizeMCPToolLifecycleString, mcpToolLifecycleString, mcpToolLifecycleSetPayload, mcpToolLifecycleListPayload,
  mcpToolLifecycleExportPayload, createMCPServerApi,
};
