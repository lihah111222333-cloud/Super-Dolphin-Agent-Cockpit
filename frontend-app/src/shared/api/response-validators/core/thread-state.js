// @ts-check

import {
  assertBackendResponseObject,
  assertOnlyResponseKeys,
  assertResponseRecord,
  hasOwn,
  validateStringFields,
} from '../shared.js';

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


export { validateSidebarStateResponse, validateAgentSummary, validateThreadSummary, validateTurnSummary, validateTypedMap, validateWorkspaceRun };
