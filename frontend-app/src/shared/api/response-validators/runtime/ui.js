// @ts-check

import {
  assertBackendResponseObject,
  assertOnlyResponseKeys,
  assertResponseRecord,
  hasOwn,
  normalizeString,
} from '../shared.js';
import { validateUIStateResponse as validateCoreUIStateResponse } from '../core/config.js';

const ACTIVITY_STATS_RESPONSE_KEYS = new Set(['lspCalls', 'commands', 'fileEdits', 'toolCalls']);
const UI_STATE_RESPONSE_KEYS = new Set([
  'threads',
  'agents',
  'active_turn',
  'recent_turns',
  'token_usage',
  'tokenUsage',
  'statuses',
  'interruptibleByThread',
  'statusHeadersByThread',
  'statusDetailsByThread',
  'tokenUsageByThread',
  'agentMetaById',
  'agentRuntimeById',
  'diffTextByThread',
  'diffRevisionByThread',
  'timelinesByThread',
  'activityStatsByThread',
  'alertsByThread',
  'unchanged',
  'activeThreadId',
  'activeCmdThreadId',
  'mainAgentId',
  'mainAgentState',
  'settings.showInjectedPromptInChat',
  'viewPrefs.chat',
  'viewPrefs.cmd',
  'threadPins.chat',
  'threadArchives.chat',
  'groups',
]);
const SIDEBAR_STATE_RESPONSE_KEYS = new Set([...UI_STATE_RESPONSE_KEYS, 'workspace']);

/** @param {string} method @param {unknown} value @param {string} path */
function assertUIStateNonNegativeInteger(method, value, path) {
  if (!Number.isInteger(value)) {
    throw new TypeError(`${method} response ${path} must be an integer`);
  }
  if (/** @type {number} */ (value) < 0) {
    throw new TypeError(`${method} response ${path} must be a non-negative integer`);
  }
}

/** @param {string} method @param {unknown} value @param {string} label @param {'string' | 'boolean'} valueType */
function validateUIStateTypedMap(method, value, label, valueType) {
  const map = assertResponseRecord(method, value, label);
  for (const [threadId, candidate] of Object.entries(map)) {
    if (!normalizeString(threadId)) {
      throw new TypeError(`${method} response ${label} thread id must be non-empty`);
    }
    if (typeof candidate !== valueType) {
      throw new TypeError(`${method} response ${label}.${threadId} must be a ${valueType}`);
    }
  }
}

/** @param {string} method @param {unknown} value */
function validateUIStateActivityMap(method, value) {
  const label = 'activityStatsByThread';
  const activityMap = assertResponseRecord(method, value, label);
  for (const [threadId, candidate] of Object.entries(activityMap)) {
    if (!normalizeString(threadId)) {
      throw new TypeError(`${method} response ${label} thread id must be non-empty`);
    }
    const activity = assertResponseRecord(method, candidate, `${label}.${threadId}`);
    assertOnlyResponseKeys(method, activity, ACTIVITY_STATS_RESPONSE_KEYS, `${label}.${threadId}`);
    for (const key of ['lspCalls', 'commands', 'fileEdits']) {
      assertUIStateNonNegativeInteger(method, activity[key], `${label}.${threadId}.${key}`);
    }
    if (!hasOwn(activity, 'toolCalls')) continue;
    const toolCalls = assertResponseRecord(method, activity.toolCalls, `${label}.${threadId}.toolCalls`);
    for (const [toolName, count] of Object.entries(toolCalls)) {
      if (!normalizeString(toolName)) {
        throw new TypeError(`${method} response ${label}.${threadId}.toolCalls tool name must be non-blank`);
      }
      assertUIStateNonNegativeInteger(method, count, `${label}.${threadId}.toolCalls.${toolName}`);
    }
  }
}

/** @param {string} method @param {unknown} value */
function validateUIStateAgentRuntimeMap(method, value) {
  const label = 'agentRuntimeById';
  const runtimeMap = assertResponseRecord(method, value, label);
  for (const [runtimeId, candidate] of Object.entries(runtimeMap)) {
    if (!normalizeString(runtimeId)) {
      throw new TypeError(`${method} response ${label} runtime id must be non-empty`);
    }
    assertResponseRecord(method, candidate, `${label}.${runtimeId}`);
  }
}

/** @param {string} method @param {Record<string, unknown>} value */
function validateStateMaps(method, value) {
  for (const key of ['statuses', 'statusHeadersByThread', 'statusDetailsByThread']) {
    if (hasOwn(value, key)) validateUIStateTypedMap(method, value[key], key, 'string');
  }
  if (hasOwn(value, 'interruptibleByThread')) {
    validateUIStateTypedMap(method, value.interruptibleByThread, 'interruptibleByThread', 'boolean');
  }
  if (hasOwn(value, 'activityStatsByThread')) {
    validateUIStateActivityMap(method, value.activityStatsByThread);
  }
  if (hasOwn(value, 'agentRuntimeById')) {
    validateUIStateAgentRuntimeMap(method, value.agentRuntimeById);
  }
}

/** @param {string} method @param {unknown} response */
function validateRuntimeUIStateResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, UI_STATE_RESPONSE_KEYS, 'body');
  const requiredSnapshotFields = [
    ['threads'],
    ['agents'],
    ['token_usage', 'tokenUsage'],
  ];
  const missingFields = requiredSnapshotFields
    .filter((aliases) => !aliases.some((key) => hasOwn(value, key)))
    .map((aliases) => aliases.join(' or '));
  validateCoreUIStateResponse(method, value);
  validateStateMaps(method, value);
  if (missingFields.length > 0) {
    throw new Error(`${method} response missing UI state snapshot fields; required: ${missingFields.join(', ')}`);
  }
  return value;
}

/** @param {string} method @param {unknown} response */

function validateUIStateResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  let runtimeValue = value;
  if (!hasOwn(value, 'token_usage') && hasOwn(value, 'tokenUsage')) {
    const { tokenUsage, ...rest } = value;
    runtimeValue = { ...rest, token_usage: tokenUsage };
  }
  validateRuntimeUIStateResponse(method, runtimeValue);
  const requiredSnapshotFields = [
    ['threads'],
    ['agents'],
    ['token_usage', 'tokenUsage'],
  ];
  const missingFields = requiredSnapshotFields
    .filter((aliases) => !aliases.some((key) => hasOwn(value, key)))
    .map((aliases) => aliases.join(' or '));
  if (missingFields.length > 0) {
    throw new Error(`${method} response missing UI state snapshot fields; required: ${missingFields.join(', ')}`);
  }
  return value;
}


export {
  SIDEBAR_STATE_RESPONSE_KEYS,
  validateRuntimeUIStateResponse,
  validateStateMaps,
  validateUIStateResponse,
};
