// @ts-check

import {
  parseMemorySnapshotResponse,
  parseModelProviderRegistryResponse,
  parseObservabilityResultResponse,
  parseSharedFileDetailResponse,
  parseSharedFilesDashboardResponse,
} from './backendSchemas.js';

const objectPrototype = Object.prototype;

/** @param {any} value */
function normalizeString(value) {
  return typeof value === 'string' ? value.trim() : '';
}

/**
 * @param {any} value
 * @param {string} key
 */
function hasOwn(value, key) {
  return objectPrototype.hasOwnProperty.call(value, key);
}

/**
 * @param {string} method
 * @param {any} response
 */
function assertBackendResponseObject(method, response) {
  if (!response || typeof response !== 'object' || Array.isArray(response)) {
    throw new TypeError(`${method} response must be an object`);
  }
  return response;
}

/**
 * @param {string} method
 * @param {any} response
 * @param {readonly string[]} keys
 */
function requireResponseKey(method, response, keys) {
  for (const key of keys) {
    if (normalizeString(response[key])) return;
  }
  throw new Error(`${method} response missing ${keys.join(' or ')}`);
}

/**
 * @param {string} method
 * @param {any} response
 * @param {string} key
 * @returns {Record<string, any> | undefined}
 */
function optionalUIStateThreadMap(method, response, key) {
  if (!hasOwn(response, key)) return undefined;
  const value = response[key];
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new TypeError(`${method} response ${key} must be an object`);
  }
  for (const threadId of Object.keys(value)) {
    if (!normalizeString(threadId)) {
      throw new TypeError(`${method} response ${key} thread id must be non-empty`);
    }
  }
  return value;
}

/**
 * @param {string} method
 * @param {Record<string, any>} value
 * @param {string} key
 * @param {'string' | 'boolean'} expectedType
 */
function validateUIStateScalarMap(method, value, key, expectedType) {
  const scalarMap = optionalUIStateThreadMap(method, value, key);
  if (!scalarMap) return;
  for (const [threadId, scalar] of Object.entries(scalarMap)) {
    if (typeof scalar !== expectedType) {
      throw new TypeError(`${method} response ${key}.${threadId} must be a ${expectedType}`);
    }
  }
}

/**
 * @param {string} method
 * @param {string} path
 * @param {any} value
 */
function assertUIStateInteger(method, path, value) {
  if (!Number.isInteger(value)) {
    throw new TypeError(`${method} response ${path} must be an integer`);
  }
  if (value < 0) {
    throw new TypeError(`${method} response ${path} must be a non-negative integer`);
  }
}

/**
 * @param {string} method
 * @param {string} threadId
 * @param {any} activity
 */
function validateUIStateActivity(method, threadId, activity) {
  const path = `activityStatsByThread.${threadId}`;
  if (!activity || typeof activity !== 'object' || Array.isArray(activity)) {
    throw new TypeError(`${method} response ${path} must be an object`);
  }
  for (const key of ['lspCalls', 'commands', 'fileEdits']) {
    assertUIStateInteger(method, `${path}.${key}`, activity[key]);
  }
  if (!hasOwn(activity, 'toolCalls')) return;
  const toolCalls = activity.toolCalls;
  if (!toolCalls || typeof toolCalls !== 'object' || Array.isArray(toolCalls)) {
    throw new TypeError(`${method} response ${path}.toolCalls must be an object`);
  }
  for (const [toolName, count] of Object.entries(toolCalls)) {
    if (!normalizeString(toolName)) {
      throw new TypeError(`${method} response ${path}.toolCalls tool name must be non-blank`);
    }
    assertUIStateInteger(method, `${path}.toolCalls.${toolName}`, count);
  }
}

/**
 * @param {string} method
 * @param {Record<string, any>} value
 */
function validateUIStateActivityMap(method, value) {
  const activityMap = optionalUIStateThreadMap(method, value, 'activityStatsByThread');
  if (!activityMap) return;
  for (const [threadId, activity] of Object.entries(activityMap)) {
    validateUIStateActivity(method, threadId, activity);
  }
}

/**
 * @param {string} method
 * @param {Record<string, any>} value
 */
function validateUIStateRuntimeMap(method, value) {
  const runtimeMap = optionalUIStateThreadMap(method, value, 'agentRuntimeById');
  if (!runtimeMap) return;
  for (const [threadId, runtime] of Object.entries(runtimeMap)) {
    if (!runtime || typeof runtime !== 'object' || Array.isArray(runtime)) {
      throw new TypeError(`${method} response agentRuntimeById.${threadId} must be an object`);
    }
  }
}

/**
 * @param {string} method
 * @param {Record<string, any>} value
 */
function validateUIStateStatusMaps(method, value) {
  validateUIStateScalarMap(method, value, 'statuses', 'string');
  validateUIStateScalarMap(method, value, 'statusHeadersByThread', 'string');
  validateUIStateScalarMap(method, value, 'statusDetailsByThread', 'string');
  validateUIStateScalarMap(method, value, 'interruptibleByThread', 'boolean');
  validateUIStateActivityMap(method, value);
  validateUIStateRuntimeMap(method, value);
}

/**
 * @param {string} method
 * @param {Record<string, any>} value
 */
function validateUIStateWireFields(method, value) {
  if (hasOwn(value, 'threads') && value.threads !== null && !Array.isArray(value.threads)) {
    throw new TypeError(`${method} response threads must be an array or null`);
  }
  if (hasOwn(value, 'agents') && value.agents !== null && !Array.isArray(value.agents)) {
    throw new TypeError(`${method} response agents must be an array or null`);
  }
  validateUIStateStatusMaps(method, value);
}

/**
 * @param {string} method
 * @param {any} response
 */
function validateUISidebarResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  validateUIStateWireFields(method, value);
  return value;
}

/**
 * @param {string} method
 * @param {any} response
 */
function validateUIStateResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  const requiredSnapshotFields = [
    ['threads'],
    ['agents'],
    ['token_usage', 'tokenUsage'],
  ];
  const missingFields = requiredSnapshotFields
    .filter((aliases) => !aliases.some((key) => hasOwn(value, key)))
    .map((aliases) => aliases.join(' or '));
  validateUIStateWireFields(method, value);
  if (missingFields.length > 0) {
    throw new Error(`${method} response missing UI state snapshot fields; required: ${missingFields.join(', ')}`);
  }
  return value;
}

/**
 * @param {string} method
 * @param {any} response
 */
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

/**
 * @param {string} method
 * @param {any} response
 */
function validateThreadStartResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  if (value.thread && (typeof value.thread !== 'object' || Array.isArray(value.thread))) {
    throw new TypeError(`${method} response thread must be an object`);
  }
  if (value.thread && normalizeString(value.thread.id)) return value;
  requireResponseKey(method, value, ['threadId', 'thread_id']);
  return value;
}

/**
 * @param {string} method
 * @param {any} response
 */
function validateThreadForkResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  const thread = value.thread;
  if (!thread || typeof thread !== 'object' || Array.isArray(thread)) {
    throw new TypeError(`${method} response thread must be an object`);
  }
  if (!normalizeString(thread.id)) {
    throw new Error(`${method} response thread.id is required`);
  }
  const forkedFromCamel = normalizeString(thread.forkedFrom);
  const forkedFromSnake = normalizeString(thread.forked_from);
  if (forkedFromCamel && forkedFromSnake && forkedFromCamel !== forkedFromSnake) {
    throw new Error(`${method} response thread.forkedFrom fields conflict`);
  }
  if (!forkedFromCamel && !forkedFromSnake) {
    throw new Error(`${method} response thread.forkedFrom is required`);
  }
  const kickoffSnake = normalizeString(value.kickoff_state);
  const kickoffCamel = normalizeString(value.kickoffState);
  if (!kickoffSnake && !kickoffCamel) {
    throw new Error(`${method} response kickoff state is required`);
  }
  if (kickoffSnake && kickoffCamel && kickoffSnake !== kickoffCamel) {
    throw new Error(`${method} response kickoff state fields conflict`);
  }
  const kickoffState = kickoffCamel || kickoffSnake;
  if (kickoffState !== 'created_only') {
    throw new Error(`${method} response unsupported kickoff state ${kickoffState}`);
  }
  return value;
}

/**
 * @param {string} method
 * @param {any} response
 */
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

/**
 * @param {string} method
 * @param {any} response
 */
function validateThreadResolveResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  requireResponseKey(method, value, ['id', 'threadId', 'thread_id']);
  return value;
}

/**
 * @param {string} method
 * @param {any} response
 */
function validateTurnStartResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  requireResponseKey(method, value, ['turn_id', 'turnId']);
  return value;
}

/** @param {any} value */
function hasTurnForceCompleteFailureDiagnostic(value) {
  return ['errorCode', 'error', 'message'].some((key) => (
    typeof value[key] === 'string' && value[key].trim() !== ''
  ));
}

/**
 * @param {string} method
 * @param {any} response
 */
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

/**
 * @param {string} method
 * @param {any} response
 */
function validateDashboardDagStartResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  requireResponseKey(method, value, ['runKey', 'run_key']);
  return value;
}

/**
 * @param {string} method
 * @param {any} response
 */
function validateDashboardDagCreateAndStartResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  requireResponseKey(method, value, ['dagKey', 'dag_key']);
  requireResponseKey(method, value, ['runKey', 'run_key']);
  return value;
}

/**
 * @param {string} method
 * @param {any} response
 */
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

/**
 * @param {string} method
 * @param {any} response
 */
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

const MCP_SERVER_LIST_RESPONSE_KEYS = new Set(['configPath', 'config_path', 'mcpServers', 'mcp_servers']);
const MCP_SERVER_STATUS_RESPONSE_KEYS = new Set(['enabled']);
const MCP_SERVER_CONTROL_RESPONSE_KEYS = new Set(['configPath', 'config_path', 'serverName', 'server_name', 'added', 'enabled']);
const THREAD_RECOVER_RESPONSE_KEYS = new Set(['thread', 'recovered', 'mode']);
const THREAD_RECOVER_THREAD_KEYS = new Set(['id', 'status']);

/**
 * @param {string} method
 * @param {Record<string, any>} value
 * @param {ReadonlySet<string>} allowedKeys
 * @param {string} label
 */
function assertOnlyResponseKeys(method, value, allowedKeys, label) {
  for (const key of Object.keys(value)) {
    if (!allowedKeys.has(key)) {
      throw new TypeError(`${method} response ${label} must not include ${key}`);
    }
  }
}

/**
 * @param {string} method
 * @param {any} response
 */
function validateThreadRecoverResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, THREAD_RECOVER_RESPONSE_KEYS, 'body');
  const thread = value.thread;
  if (!thread || typeof thread !== 'object' || Array.isArray(thread)) {
    throw new TypeError(`${method} response thread must be an object`);
  }
  assertOnlyResponseKeys(method, thread, THREAD_RECOVER_THREAD_KEYS, 'thread');
  if (!normalizeString(thread.id)) {
    throw new TypeError(`${method} response thread.id must be a non-empty string`);
  }
  if (thread.status !== 'recovering') {
    throw new TypeError(`${method} response thread.status must be recovering`);
  }
  if (typeof value.recovered !== 'boolean') {
    throw new TypeError(`${method} response recovered must be a boolean`);
  }
  if (!normalizeString(value.mode)) {
    throw new TypeError(`${method} response mode must be a non-empty string`);
  }
  return value;
}

/**
 * @param {string} method
 * @param {any} response
 */
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

/**
 * @param {string} method
 * @param {any} response
 * @param {Record<string, { serverName: string, enabled: boolean }>} controlSpecs
 */
function validateMCPServerControlResponse(method, response, controlSpecs) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, MCP_SERVER_CONTROL_RESPONSE_KEYS, 'body');
  const configPath = normalizeString(value.configPath || value.config_path);
  if (!configPath) {
    throw new Error(`${method} response configPath must be a non-empty string`);
  }
  const spec = controlSpecs[method];
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

/**
 * @param {string} method
 * @param {any} response
 * @param {(response: unknown) => unknown} parser
 */
function validateSchemaResponse(method, response, parser) {
  try {
    return parser(response);
  }
  catch (error) {
    throw new TypeError(`${method} response ${error.message || 'schema is invalid'}`, { cause: error });
  }
}

/** @type {(method: string, response: unknown) => unknown} */
const validateObservabilityResultResponse = (method, response) => validateSchemaResponse(method, response, parseObservabilityResultResponse);
/** @type {(method: string, response: unknown) => unknown} */
const validateMemorySnapshotResponse = (method, response) => validateSchemaResponse(method, response, parseMemorySnapshotResponse);
/** @type {(method: string, response: unknown) => unknown} */
const validateSharedFilesDashboardResponse = (method, response) => validateSchemaResponse(method, response, parseSharedFilesDashboardResponse);
/** @type {(method: string, response: unknown) => unknown} */
const validateSharedFileDetailResponse = (method, response) => validateSchemaResponse(method, response, parseSharedFileDetailResponse);
/** @type {(method: string, response: unknown) => unknown} */
const validateModelProviderRegistryResponse = (method, response) => validateSchemaResponse(method, response, parseModelProviderRegistryResponse);

/** @param {Record<string, string>} methods */
export function createBackendResponseValidators(methods) {
  const controlSpecs = Object.freeze({
    [methods.MCP_SERVER_SQLITE_START]: { serverName: 'sqlite', enabled: true },
    [methods.MCP_SERVER_SQLITE_STOP]: { serverName: 'sqlite', enabled: false },
    [methods.MCP_SERVER_PLAYWRIGHT_START]: { serverName: 'playwright', enabled: true },
    [methods.MCP_SERVER_PLAYWRIGHT_STOP]: { serverName: 'playwright', enabled: false },
  });
  /** @type {(method: string, response: unknown) => unknown} */
  const validateControlResponse = (method, response) => validateMCPServerControlResponse(method, response, controlSpecs);

  return Object.freeze({
    [methods.APP_UPDATE_INSTALL]: validateAppUpdateInstallResponse,
    [methods.APP_UPDATE_INSTALL_LATEST]: validateAppUpdateInstallResponse,
    [methods.CONFIG_LSP_PROMPT_HINT_READ]: validateLspPromptHintResponse,
    [methods.CONFIG_LSP_PROMPT_HINT_WRITE]: validateLspPromptHintResponse,
    [methods.DASHBOARD_SHARED_FILES]: validateSharedFilesDashboardResponse,
    [methods.MCP_SERVER_LIST]: validateMCPServerListResponse,
    [methods.MCP_SERVER_SQLITE_START]: validateControlResponse,
    [methods.MCP_SERVER_SQLITE_STOP]: validateControlResponse,
    [methods.MCP_SERVER_PLAYWRIGHT_START]: validateControlResponse,
    [methods.MCP_SERVER_PLAYWRIGHT_STOP]: validateControlResponse,
    [methods.MODEL_PROVIDERS_APPLY]: validateModelProviderRegistryResponse,
    [methods.MODEL_PROVIDERS_LIST]: validateModelProviderRegistryResponse,
    [methods.OBSERVABILITY_ERROR_LIST]: validateObservabilityResultResponse,
    [methods.OBSERVABILITY_RECENT_LIST]: validateObservabilityResultResponse,
    [methods.OBSERVABILITY_SLOW_LIST]: validateObservabilityResultResponse,
    [methods.OBSERVABILITY_THREAD_RECENT]: validateObservabilityResultResponse,
    [methods.OBSERVABILITY_TRACE_GET]: validateObservabilityResultResponse,
    [methods.UI_SIDEBAR_GET]: validateUISidebarResponse,
    [methods.UI_STATE_GET]: validateUIStateResponse,
    [methods.UI_MEMORY_GET]: validateMemorySnapshotResponse,
    [methods.UI_SHARED_FILE_GET]: validateSharedFileDetailResponse,
    [methods.SKILLS_LOCAL_READ]: validateSkillReadResponse,
    [methods.THREAD_FORK]: validateThreadForkResponse,
    [methods.THREAD_START]: validateThreadStartResponse,
    [methods.THREAD_MESSAGES]: validateThreadMessagesResponse,
    [methods.THREAD_RECOVER]: validateThreadRecoverResponse,
    [methods.THREAD_RESOLVE]: validateThreadResolveResponse,
    [methods.TURN_START]: validateTurnStartResponse,
    [methods.TURN_FORCE_COMPLETE]: validateTurnForceCompleteResponse,
    [methods.DASHBOARD_DAG_START]: validateDashboardDagStartResponse,
    [methods.DASHBOARD_DAG_CREATE_AND_START]: validateDashboardDagCreateAndStartResponse,
  });
}
