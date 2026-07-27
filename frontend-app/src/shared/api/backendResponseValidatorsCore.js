// @ts-check

import {
  assertBackendResponseObject,
  assertOnlyResponseKeys,
  assertResponseRecord,
  hasOwn,
  normalizeString,
  validateStringFields,
} from './backendResponseValidatorShared.js';

const RUNTIME_CONFIG_RESPONSE_KEYS = new Set(['model', 'modelProvider', 'cwd', 'approvalPolicy', 'sandbox', 'config', 'baseInstructions', 'developerInstructions', 'personality', 'toolRouting']);
const RUNTIME_TOOL_ROUTING_KEYS = new Set(['mode', 'routerModel', 'routerProvider', 'routerBaseURL', 'routerHasAPIKey', 'confidenceThreshold', 'timeoutSec']);
const BUILTIN_TOOLS_RESPONSE_KEYS = new Set(['tools']);
const BUILTIN_TOOL_KEYS = new Set(['id', 'label', 'description', 'enabled', 'provider', 'replacedBy', 'filterMode', 'enforcement']);
const WINDOW_BOOTSTRAP_RESPONSE_KEYS = new Set(['snapshot']);
const SIDEBAR_RESPONSE_KEYS = new Set([
  'threads', 'agents', 'active_turn', 'recent_turns', 'workspace', 'token_usage',
  'statuses', 'interruptibleByThread', 'statusHeadersByThread', 'statusDetailsByThread',
  'agentRuntimeById', 'activeThreadId', 'activeCmdThreadId', 'mainAgentId',
  'viewPrefs.chat', 'viewPrefs.cmd', 'threadPins.chat', 'threadArchives.chat', 'groups',
]);
const THREAD_SUMMARY_KEYS = new Set(['id', 'name', 'agent_id', 'createdAt', 'updatedAt', 'lifecycleStatus', 'state', 'threadStatus', 'agentState', 'lastMessage', 'overlayText', 'overlayType', 'overlayPriority']);
const AGENT_SUMMARY_KEYS = new Set(['id', 'name', 'thread_id', 'provider_thread_id', 'parent_id', 'state', 'provider', 'model', 'cwd', 'port', 'logPath', 'createdAt', 'updatedAt', 'last_report', 'agentState', 'threadStatus', 'lastMessage']);
const TURN_SUMMARY_KEYS = new Set(['id', 'agent_id', 'thread_id', 'status', 'success', 'error', 'reason', 'started_at', 'completed_at']);
const WORKSPACE_PANEL_KEYS = new Set(['runs']);
const WORKSPACE_RUN_KEYS = new Set(['run_key', 'dag_key', 'status', 'source_root', 'workspace_path', 'created_by', 'updated_by', 'merged_file_count', 'conflicts', 'errors', 'message', 'updated_at']);
const TOKEN_USAGE_KEYS = new Set(['inputTokens', 'outputTokens', 'totalTokens', 'usedTokens', 'contextWindowTokens', 'usedPercent']);
const THREAD_GROUP_KEYS = new Set(['key', 'title', 'threads']);
const FRONTEND_INGEST_RESPONSE_KEYS = new Set(['enabled', 'recorded', 'dropped', 'disabled_reason']);
const OPEN_WINDOW_RESPONSE_KEYS = new Set(['ok', 'windowId', 'cwd']);
const CODE_SAVE_RESPONSE_KEYS = new Set(['ok', 'filePath', 'relative', 'totalLines', 'contentVersion']);
const PROJECTS_STATE_RESPONSE_KEYS = new Set(['projects', 'active']);
const OK_RESPONSE_KEYS = new Set(['ok']);
const DASHBOARD_PAGE_RESPONSE_KEYS = new Set(['agents', 'dags', 'skills', 'commandCards', 'prompts', 'memory', 'finalOutputRefs', 'sharedFileRetention']);
const SHARED_FILE_RETENTION_KEYS = new Set(['items', 'protectedCount', 'cleanupCandidateCount']);
const VIDEO_API_KEY_STATUS_RESPONSE_KEYS = new Set(['configured', 'masked']);
const DASHBOARD_LOGS_RESPONSE_KEYS = new Set(['logs']);
const DASHBOARD_LOG_ENTRY_KEYS = new Set(['source', 'id', 'timestamp', 'level', 'logger', 'message', 'raw', 'component', 'agent_id', 'thread_id', 'trace_id', 'span_id', 'parent_span_id', 'event_type', 'tool_name', 'duration_ms']);
const THREAD_CONFIG_RESPONSE_KEYS = new Set(['threadId', 'provider', 'supportsThreadOverride', 'override', 'effective']);
const THREAD_CONFIG_VALUES_KEYS = new Set(['model', 'effort', 'approvals']);
const THREAD_COMPACT_RESPONSE_KEYS = new Set(['threadId', 'command', 'beforeTokens', 'afterTokens', 'compacted', 'estimated']);
const THREAD_RECOVER_RESPONSE_KEYS = new Set(['thread', 'recovered', 'mode']);
const THREAD_RECOVER_THREAD_KEYS = new Set(['id', 'status']);

/**
 * @param {string} method
 * @param {Record<string, unknown>} response
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
 * @param {unknown} response
 */
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

/**
 * @param {string} method
 * @param {unknown} response
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
 * @param {unknown} response
 */
function validateRuntimeConfigResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, RUNTIME_CONFIG_RESPONSE_KEYS, 'body');
  for (const key of ['model', 'cwd', 'approvalPolicy']) {
    if (typeof value[key] !== 'string') {
      throw new TypeError(`${method} response ${key} must be a string`);
    }
  }
  for (const key of ['modelProvider', 'sandbox', 'config', 'baseInstructions', 'developerInstructions', 'personality']) {
    if (!hasOwn(value, key)) {
      throw new TypeError(`${method} response ${key} is required`);
    }
  }
  const toolRouting = value.toolRouting;
  if (!toolRouting || typeof toolRouting !== 'object' || Array.isArray(toolRouting)) {
    throw new TypeError(`${method} response toolRouting must be an object`);
  }
  const routing = /** @type {Record<string, unknown>} */ (toolRouting);
  assertOnlyResponseKeys(method, routing, RUNTIME_TOOL_ROUTING_KEYS, 'toolRouting');
  for (const key of ['mode', 'routerModel', 'routerProvider', 'routerBaseURL']) {
    if (typeof routing[key] !== 'string') {
      throw new TypeError(`${method} response toolRouting.${key} must be a string`);
    }
  }
  if (typeof routing.routerHasAPIKey !== 'boolean') {
    throw new TypeError(`${method} response toolRouting.routerHasAPIKey must be a boolean`);
  }
  if (typeof routing.confidenceThreshold !== 'number' || !Number.isFinite(routing.confidenceThreshold)) {
    throw new TypeError(`${method} response toolRouting.confidenceThreshold must be a finite number`);
  }
  if (!Number.isInteger(routing.timeoutSec)) {
    throw new TypeError(`${method} response toolRouting.timeoutSec must be an integer`);
  }
  return value;
}

/**
 * @param {string} method
 * @param {unknown} response
 */
function validateBuiltinToolsResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, BUILTIN_TOOLS_RESPONSE_KEYS, 'body');
  if (!Array.isArray(value.tools)) {
    throw new TypeError(`${method} response tools must be an array`);
  }
  /** @type {unknown[]} */
  const tools = value.tools;
  tools.forEach((tool, index) => {
    if (!tool || typeof tool !== 'object' || Array.isArray(tool)) {
      throw new TypeError(`${method} response tools[${index}] must be an object`);
    }
    const toolRecord = /** @type {Record<string, unknown>} */ (tool);
    assertOnlyResponseKeys(method, toolRecord, BUILTIN_TOOL_KEYS, `tools[${index}]`);
    for (const key of ['id', 'label']) {
      if (typeof toolRecord[key] !== 'string') {
        throw new TypeError(`${method} response tools[${index}].${key} must be a string`);
      }
    }
    if (typeof toolRecord.enabled !== 'boolean') {
      throw new TypeError(`${method} response tools[${index}].enabled must be a boolean`);
    }
    for (const key of ['description', 'provider', 'replacedBy', 'filterMode', 'enforcement']) {
      if (hasOwn(toolRecord, key) && typeof toolRecord[key] !== 'string') {
        throw new TypeError(`${method} response tools[${index}].${key} must be a string`);
      }
    }
  });
  return value;
}

/**
 * @param {string} method
 * @param {unknown} response
 */
function validateWindowBootstrapResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, WINDOW_BOOTSTRAP_RESPONSE_KEYS, 'body');
  // snapshot 为一次性消费：桌面宿主首次加载后即被取走，此后页面加载（浏览器直开、
  // 刷新、HMR）都会得到 null。null 交给 normalizeBootstrapSnapshot 回退为空快照，
  // 不能在这里 fail，否则 bootstrap 永久失败、全部控件被禁用。
  if (value.snapshot === null) return value;
  if (!value.snapshot || typeof value.snapshot !== 'object' || Array.isArray(value.snapshot)) {
    throw new TypeError(`${method} response snapshot must be an object`);
  }
  return value;
}

/**
 * @param {string} method
 * @param {unknown} response
 * @param {string} label
 */

/** @param {string} method @param {unknown} value @param {string} label */
function validateThreadSummary(method, value, label) {
  const thread = assertResponseRecord(method, value, label);
  assertOnlyResponseKeys(method, thread, THREAD_SUMMARY_KEYS, label);
  validateStringFields(method, thread, label, ['id'], [
    'name', 'agent_id', 'createdAt', 'updatedAt', 'lifecycleStatus', 'state',
    'threadStatus', 'agentState', 'lastMessage', 'overlayText', 'overlayType',
  ]);
  if (hasOwn(thread, 'overlayPriority') && !Number.isInteger(thread.overlayPriority)) {
    throw new TypeError(`${method} response ${label}.overlayPriority must be an integer`);
  }
}

/** @param {string} method @param {unknown} value @param {string} label */
function validateAgentSummary(method, value, label) {
  const agent = assertResponseRecord(method, value, label);
  assertOnlyResponseKeys(method, agent, AGENT_SUMMARY_KEYS, label);
  validateStringFields(method, agent, label, ['id'], [
    'name', 'thread_id', 'provider_thread_id', 'parent_id', 'state', 'provider',
    'model', 'cwd', 'logPath', 'createdAt', 'updatedAt', 'last_report',
    'agentState', 'threadStatus', 'lastMessage',
  ]);
  if (hasOwn(agent, 'port') && !Number.isInteger(agent.port)) {
    throw new TypeError(`${method} response ${label}.port must be an integer`);
  }
}

/** @param {string} method @param {unknown} value @param {string} label */
function validateTurnSummary(method, value, label) {
  const turn = assertResponseRecord(method, value, label);
  assertOnlyResponseKeys(method, turn, TURN_SUMMARY_KEYS, label);
  validateStringFields(method, turn, label, ['id', 'agent_id', 'status'], [
    'thread_id', 'error', 'reason', 'started_at', 'completed_at',
  ]);
  if (hasOwn(turn, 'success') && typeof turn.success !== 'boolean') {
    throw new TypeError(`${method} response ${label}.success must be a boolean`);
  }
}

/** @param {string} method @param {unknown} value @param {string} label */
function validateWorkspaceRun(method, value, label) {
  const run = assertResponseRecord(method, value, label);
  assertOnlyResponseKeys(method, run, WORKSPACE_RUN_KEYS, label);
  validateStringFields(method, run, label, ['run_key'], [
    'dag_key', 'status', 'source_root', 'workspace_path', 'created_by',
    'updated_by', 'message', 'updated_at',
  ]);
  for (const key of ['merged_file_count', 'conflicts', 'errors']) {
    if (hasOwn(run, key) && !Number.isInteger(run[key])) {
      throw new TypeError(`${method} response ${label}.${key} must be an integer`);
    }
  }
}

/**
 * @param {string} method
 * @param {unknown} value
 * @param {string} label
 * @param {'string' | 'boolean' | 'integer'} valueType
 */
function validateTypedMap(method, value, label, valueType) {
  const map = assertResponseRecord(method, value, label);
  for (const item of Object.values(map)) {
    const valid = valueType === 'integer'
      ? Number.isInteger(item)
      : typeof item === valueType;
    if (!valid) {
      const plural = valueType === 'string' ? 'strings' : valueType === 'boolean' ? 'booleans' : 'integers';
      throw new TypeError(`${method} response ${label} values must be ${plural}`);
    }
  }
}

/** @param {string} method @param {unknown} value @param {string} label */
function validateThreadGroup(method, value, label) {
  const group = assertResponseRecord(method, value, label);
  assertOnlyResponseKeys(method, group, THREAD_GROUP_KEYS, label);
  validateStringFields(method, group, label, ['key', 'title'], []);
  if (!Array.isArray(group.threads)) {
    throw new TypeError(`${method} response ${label}.threads must be an array`);
  }
  for (let index = 0; index < group.threads.length; index += 1) {
    validateThreadSummary(method, group.threads[index], `${label}.threads[${index}]`);
  }
}

/** @param {string} method @param {unknown} response */
function validateSidebarStateResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, SIDEBAR_RESPONSE_KEYS, 'body');

  if (hasOwn(value, 'threads')) {
    if (!Array.isArray(value.threads)) {
      throw new TypeError(`${method} response threads must be an array`);
    }
    for (let index = 0; index < value.threads.length; index += 1) {
      validateThreadSummary(method, value.threads[index], `threads[${index}]`);
    }
  }
  if (hasOwn(value, 'agents')) {
    if (!Array.isArray(value.agents)) {
      throw new TypeError(`${method} response agents must be an array`);
    }
    for (let index = 0; index < value.agents.length; index += 1) {
      validateAgentSummary(method, value.agents[index], `agents[${index}]`);
    }
  }
  if (hasOwn(value, 'active_turn')) {
    validateTurnSummary(method, value.active_turn, 'active_turn');
  }
  if (hasOwn(value, 'recent_turns')) {
    if (!Array.isArray(value.recent_turns)) {
      throw new TypeError(`${method} response recent_turns must be an array`);
    }
    for (let index = 0; index < value.recent_turns.length; index += 1) {
      validateTurnSummary(method, value.recent_turns[index], `recent_turns[${index}]`);
    }
  }

  if (hasOwn(value, 'workspace')) {
    const workspace = assertResponseRecord(method, value.workspace, 'workspace');
    assertOnlyResponseKeys(method, workspace, WORKSPACE_PANEL_KEYS, 'workspace');
    if (!Array.isArray(workspace.runs)) {
      throw new TypeError(`${method} response workspace.runs must be an array`);
    }
    for (let index = 0; index < workspace.runs.length; index += 1) {
      validateWorkspaceRun(method, workspace.runs[index], `workspace.runs[${index}]`);
    }
  }

  if (hasOwn(value, 'token_usage')) {
    const tokenUsage = assertResponseRecord(method, value.token_usage, 'token_usage');
    assertOnlyResponseKeys(method, tokenUsage, TOKEN_USAGE_KEYS, 'token_usage');
    for (const key of ['inputTokens', 'outputTokens', 'totalTokens', 'usedTokens']) {
      if (!Number.isInteger(tokenUsage[key])) {
        throw new TypeError(`${method} response token_usage.${key} must be an integer`);
      }
    }
    if (hasOwn(tokenUsage, 'contextWindowTokens') && !Number.isInteger(tokenUsage.contextWindowTokens)) {
      throw new TypeError(`${method} response token_usage.contextWindowTokens must be an integer`);
    }
    if (hasOwn(tokenUsage, 'usedPercent') && (typeof tokenUsage.usedPercent !== 'number' || !Number.isFinite(tokenUsage.usedPercent))) {
      throw new TypeError(`${method} response token_usage.usedPercent must be a finite number`);
    }
  }

  for (const key of ['activeThreadId', 'activeCmdThreadId', 'mainAgentId']) {
    if (hasOwn(value, key) && typeof value[key] !== 'string') {
      throw new TypeError(`${method} response ${key} must be a string`);
    }
  }
  for (const key of ['viewPrefs.chat', 'viewPrefs.cmd']) {
    if (hasOwn(value, key)) assertResponseRecord(method, value[key], key);
  }
  for (const key of ['threadPins.chat', 'threadArchives.chat']) {
    if (hasOwn(value, key)) validateTypedMap(method, value[key], key, 'integer');
  }
  if (hasOwn(value, 'groups')) {
    if (!Array.isArray(value.groups)) {
      throw new TypeError(`${method} response groups must be an array`);
    }
    for (let index = 0; index < value.groups.length; index += 1) {
      validateThreadGroup(method, value.groups[index], `groups[${index}]`);
    }
  }
  return value;
}

/** @param {string} method @param {unknown} response */
function validateFrontendIngestResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, FRONTEND_INGEST_RESPONSE_KEYS, 'body');
  if (typeof value.enabled !== 'boolean') {
    throw new TypeError(`${method} response enabled must be a boolean`);
  }
  for (const key of ['recorded', 'dropped']) {
    if (!Number.isInteger(value[key])) {
      throw new TypeError(`${method} response ${key} must be an integer`);
    }
  }
  if (hasOwn(value, 'disabled_reason') && typeof value.disabled_reason !== 'string') {
    throw new TypeError(`${method} response disabled_reason must be a string`);
  }
  return value;
}

/** @param {string} method @param {unknown} response */
function validateOpenWindowResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, OPEN_WINDOW_RESPONSE_KEYS, 'body');
  if (typeof value.ok !== 'boolean') {
    throw new TypeError(`${method} response ok must be a boolean`);
  }
  if (value.ok !== true) {
    throw new TypeError(`${method} response ok must be true`);
  }
  for (const key of ['windowId', 'cwd']) {
    if (typeof value[key] !== 'string') {
      throw new TypeError(`${method} response ${key} must be a string`);
    }
  }
  return value;
}

/** @param {string} method @param {unknown} response */
function validateCodeSaveResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, CODE_SAVE_RESPONSE_KEYS, 'body');
  if (typeof value.ok !== 'boolean') {
    throw new TypeError(`${method} response ok must be a boolean`);
  }
  if (value.ok !== true) {
    throw new TypeError(`${method} response ok must be true`);
  }
  for (const key of ['filePath', 'relative', 'contentVersion']) {
    if (typeof value[key] !== 'string' || !value[key].trim()) {
      throw new TypeError(`${method} response ${key} must be a non-empty string`);
    }
  }
  if (!Number.isInteger(value.totalLines)) {
    throw new TypeError(`${method} response totalLines must be an integer`);
  }
  return value;
}

/** @param {string} method @param {unknown} response */
function validateProjectsStateResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, PROJECTS_STATE_RESPONSE_KEYS, 'body');
  const projects = value.projects;
  if (!Array.isArray(projects) || projects.some((project) => typeof project !== 'string')) {
    throw new TypeError(`${method} response projects must be an array of strings`);
  }
  if (typeof value.active !== 'string') {
    throw new TypeError(`${method} response active must be a string`);
  }
  return value;
}

/** @param {string} method @param {unknown} response */
function validateOKResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, OK_RESPONSE_KEYS, 'body');
  if (value.ok !== true) {
    throw new TypeError(`${method} response ok must be true`);
  }
  return value;
}

/** @param {string} method @param {unknown} response */
function validateNullResponse(method, response) {
  if (response !== null) {
    throw new TypeError(`${method} response must be null`);
  }
  return response;
}

/** @param {string} method @param {unknown} response @param {string} label */
function validateThreadConfigValues(method, response, label) {
  const value = assertResponseRecord(method, response, label);
  assertOnlyResponseKeys(method, value, THREAD_CONFIG_VALUES_KEYS, label);
  validateStringFields(method, value, label, [], ['model', 'effort', 'approvals']);
}

/** @param {string} method @param {unknown} response */
function validateThreadConfigResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, THREAD_CONFIG_RESPONSE_KEYS, 'body');
  validateStringFields(method, value, 'body', ['threadId'], ['provider']);
  if (typeof value.supportsThreadOverride !== 'boolean') {
    throw new TypeError(`${method} response supportsThreadOverride must be a boolean`);
  }
  validateThreadConfigValues(method, value.override, 'override');
  validateThreadConfigValues(method, value.effective, 'effective');
  return value;
}

/** @param {string} method @param {unknown} response */
function validateThreadCompactResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, THREAD_COMPACT_RESPONSE_KEYS, 'body');
  validateStringFields(method, value, 'body', ['threadId', 'command'], []);
  for (const key of ['beforeTokens', 'afterTokens']) {
    if (!Number.isInteger(value[key])) {
      throw new TypeError(`${method} response ${key} must be an integer`);
    }
  }
  if (typeof value.compacted !== 'boolean') {
    throw new TypeError(`${method} response compacted must be a boolean`);
  }
  if (hasOwn(value, 'estimated') && typeof value.estimated !== 'boolean') {
    throw new TypeError(`${method} response estimated must be a boolean`);
  }
  return value;
}

/** @param {string} method @param {unknown} response */
function validateThreadRecoverResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, THREAD_RECOVER_RESPONSE_KEYS, 'body');
  const thread = assertResponseRecord(method, value.thread, 'thread');
  assertOnlyResponseKeys(method, thread, THREAD_RECOVER_THREAD_KEYS, 'thread');
  validateStringFields(method, thread, 'thread', ['id'], ['status']);
  if (typeof value.recovered !== 'boolean') {
    throw new TypeError(`${method} response recovered must be a boolean`);
  }
  if (typeof value.mode !== 'string') {
    throw new TypeError(`${method} response mode must be a string`);
  }
  return value;
}

/** @param {string} method @param {unknown} response */
function validateDashboardPageResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, DASHBOARD_PAGE_RESPONSE_KEYS, 'body');
  for (const key of ['agents', 'dags', 'skills', 'commandCards', 'prompts', 'memory', 'finalOutputRefs']) {
    if (!Array.isArray(value[key])) {
      throw new TypeError(`${method} response ${key} must be an array`);
    }
  }
  const retention = value.sharedFileRetention;
  if (!retention || typeof retention !== 'object' || Array.isArray(retention)) {
    throw new TypeError(`${method} response sharedFileRetention must be an object`);
  }
  const retentionValue = /** @type {Record<string, unknown>} */ (retention);
  assertOnlyResponseKeys(method, retentionValue, SHARED_FILE_RETENTION_KEYS, 'sharedFileRetention');
  if (!Array.isArray(retentionValue.items)) {
    throw new TypeError(`${method} response sharedFileRetention.items must be an array`);
  }
  for (const key of ['protectedCount', 'cleanupCandidateCount']) {
    if (!Number.isInteger(retentionValue[key])) {
      throw new TypeError(`${method} response sharedFileRetention.${key} must be an integer`);
    }
  }
  return value;
}

/** @param {string} method @param {unknown} response */
function validateVideoAPIKeyStatusResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, VIDEO_API_KEY_STATUS_RESPONSE_KEYS, 'body');
  if (typeof value.configured !== 'boolean') {
    throw new TypeError(`${method} response configured must be a boolean`);
  }
  if (typeof value.masked !== 'string') {
    throw new TypeError(`${method} response masked must be a string`);
  }
  return value;
}

/** @param {string} method @param {unknown} response */
function validateDashboardLogsResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, DASHBOARD_LOGS_RESPONSE_KEYS, 'body');
  if (!Array.isArray(value.logs)) {
    throw new TypeError(`${method} response logs must be an array`);
  }
  /** @type {unknown[]} */
  const logs = value.logs;
  logs.forEach((entry, index) => {
    if (!entry || typeof entry !== 'object' || Array.isArray(entry)) {
      throw new TypeError(`${method} response logs[${index}] must be an object`);
    }
    const entryRecord = /** @type {Record<string, unknown>} */ (entry);
    assertOnlyResponseKeys(method, entryRecord, DASHBOARD_LOG_ENTRY_KEYS, `logs[${index}]`);
    if (typeof entryRecord.source !== 'string') {
      throw new TypeError(`${method} response logs[${index}].source must be a string`);
    }
    if (!Number.isInteger(entryRecord.id)) {
      throw new TypeError(`${method} response logs[${index}].id must be an integer`);
    }
    if (typeof entryRecord.timestamp !== 'string') {
      throw new TypeError(`${method} response logs[${index}].timestamp must be a string`);
    }
    for (const key of ['level', 'logger', 'message', 'raw', 'component', 'agent_id', 'thread_id', 'trace_id', 'span_id', 'parent_span_id', 'event_type', 'tool_name']) {
      if (hasOwn(entryRecord, key) && typeof entryRecord[key] !== 'string') {
        throw new TypeError(`${method} response logs[${index}].${key} must be a string`);
      }
    }
    if (hasOwn(entryRecord, 'duration_ms') && !Number.isInteger(entryRecord.duration_ms)) {
      throw new TypeError(`${method} response logs[${index}].duration_ms must be an integer`);
    }
  });
  return value;
}

/**
 * @param {string} method
 * @param {unknown} response
 */
function validateThreadStartResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  if (value.thread && (typeof value.thread !== 'object' || Array.isArray(value.thread))) {
    throw new TypeError(`${method} response thread must be an object`);
  }
  if (value.thread && normalizeString(/** @type {Record<string, unknown>} */ (value.thread).id)) return value;
  requireResponseKey(method, value, ['threadId', 'thread_id']);
  return value;
}

/**
 * @param {string} method
 * @param {unknown} response
 */
function validateThreadForkResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  const thread = value.thread;
  if (!thread || typeof thread !== 'object' || Array.isArray(thread)) {
    throw new TypeError(`${method} response thread must be an object`);
  }
  const threadValue = /** @type {Record<string, unknown>} */ (thread);
  if (!normalizeString(threadValue.id)) {
    throw new Error(`${method} response thread.id is required`);
  }
  const forkedFromCamel = normalizeString(threadValue.forkedFrom);
  const forkedFromSnake = normalizeString(threadValue.forked_from);
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
 * @param {unknown} response
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
 * @param {unknown} response
 */
function validateThreadResolveResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  requireResponseKey(method, value, ['id', 'threadId', 'thread_id']);
  return value;
}

/**
 * @param {string} method
 * @param {unknown} response
 */
function validateTurnStartResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  requireResponseKey(method, value, ['turn_id', 'turnId']);
  return value;
}

/** @param {Record<string, unknown>} value */
function hasTurnForceCompleteFailureDiagnostic(value) {
  return ['errorCode', 'error', 'message'].some((key) => (
    typeof value[key] === 'string' && value[key].trim() !== ''
  ));
}

/**
 * @param {string} method
 * @param {unknown} response
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
 * @param {unknown} response
 */
function validateDashboardDagStartResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  requireResponseKey(method, value, ['runKey', 'run_key']);
  return value;
}

/**
 * @param {string} method
 * @param {unknown} response
 */
function validateDashboardDagCreateAndStartResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  requireResponseKey(method, value, ['dagKey', 'dag_key']);
  requireResponseKey(method, value, ['runKey', 'run_key']);
  return value;
}

/**
 * @param {string} method
 * @param {unknown} response
 */
function validateSkillReadResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  const skill = value.skill;
  if (!skill || typeof skill !== 'object' || Array.isArray(skill)) {
    throw new TypeError(`${method} response skill must be an object`);
  }
  const skillValue = /** @type {Record<string, unknown>} */ (skill);
  requireResponseKey(method, skillValue, ['path']);
  if (!hasOwn(skillValue, 'content') || typeof skillValue.content !== 'string') {
    throw new TypeError(`${method} response skill.content must be a string`);
  }
  return value;
}

/**
 * @param {string} method
 * @param {unknown} response
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

export {
  validateAppUpdateInstallResponse,
  validateBuiltinToolsResponse,
  validateCodeSaveResponse,
  validateDashboardDagCreateAndStartResponse,
  validateDashboardDagStartResponse,
  validateDashboardLogsResponse,
  validateDashboardPageResponse,
  validateFrontendIngestResponse,
  validateLspPromptHintResponse,
  validateNullResponse,
  validateOKResponse,
  validateOpenWindowResponse,
  validateProjectsStateResponse,
  validateRuntimeConfigResponse,
  validateSidebarStateResponse,
  validateSkillReadResponse,
  validateThreadCompactResponse,
  validateThreadConfigResponse,
  validateThreadForkResponse,
  validateThreadMessagesResponse,
  validateThreadRecoverResponse,
  validateThreadResolveResponse,
  validateThreadStartResponse,
  validateTurnForceCompleteResponse,
  validateTurnStartResponse,
  validateUIStateResponse,
  validateVideoAPIKeyStatusResponse,
  validateWindowBootstrapResponse,
};
