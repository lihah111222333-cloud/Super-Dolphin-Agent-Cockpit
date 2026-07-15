// @ts-check

import {
  validateSidebarStateResponse as validateCoreSidebarStateResponse,
  validateThreadRecoverResponse as validateCoreThreadRecoverResponse,
  validateUIStateResponse as validateCoreUIStateResponse,
} from './backendResponseValidatorsCore.js';
import {
  assertBackendResponseObject,
  assertOnlyResponseKeys,
  assertResponseRecord,
  hasOwn,
  normalizeString,
  validateStringFields,
} from './backendResponseValidatorShared.js';

const PROMPT_HISTORY_RESPONSE_KEYS = new Set(['entries', 'nextCursor', 'hasMore', 'nonce']);
const PROMPT_HISTORY_ENTRY_KEYS = new Set(['threadId', 'messageId', 'text', 'createdAt']);
const TOOLBRIDGE_TOOLS_RESPONSE_KEYS = new Set(['tools']);
const TOOLBRIDGE_TOOL_RESPONSE_KEYS = new Set(['serverName', 'toolName', 'displayName', 'description', 'enabled', 'disabledReason']);
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

/** @param {string} method @param {Record<string, any>} value */
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
export function validateUIStateResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  const requiredSnapshotFields = [
    ['threads'],
    ['agents'],
    ['token_usage', 'tokenUsage'],
  ];
  const missingFields = requiredSnapshotFields
    .filter((aliases) => !aliases.some((key) => hasOwn(value, key)))
    .map((aliases) => aliases.join(' or '));
  assertOnlyResponseKeys(method, value, UI_STATE_RESPONSE_KEYS, 'body');
  validateCoreUIStateResponse(method, value);
  validateStateMaps(method, value);
  if (missingFields.length > 0) {
    throw new Error(`${method} response missing UI state snapshot fields; required: ${missingFields.join(', ')}`);
  }
  return value;
}

/** @param {string} method @param {unknown} response */
export function validateSidebarStateResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  const { activityStatsByThread, ...coreValue } = value;
  const hasFullSidebarContract = (
    hasOwn(value, 'threads')
    && hasOwn(value, 'agents')
    && hasOwn(value, 'workspace')
    && hasOwn(value, 'token_usage')
  );
  if (hasFullSidebarContract) {
    validateCoreSidebarStateResponse(method, coreValue);
  } else {
    if (hasOwn(value, 'threads') && value.threads !== null && !Array.isArray(value.threads)) {
      throw new TypeError(`${method} response threads must be an array or null`);
    }
    if (hasOwn(value, 'agents') && value.agents !== null && !Array.isArray(value.agents)) {
      throw new TypeError(`${method} response agents must be an array or null`);
    }
  }
  validateStateMaps(method, value);
  return value;
}

/** @param {string} method @param {unknown} response */
export function validateThreadRecoverResponse(method, response) {
  const envelope = assertBackendResponseObject(method, response);
  const responseThread = assertResponseRecord(method, envelope.thread, 'thread');
  if (!normalizeString(responseThread.id)) {
    throw new TypeError(`${method} response thread.id must be a non-empty string`);
  }
  const value = validateCoreThreadRecoverResponse(method, response);
  const thread = assertResponseRecord(method, value.thread, 'thread');
  if (thread.status !== 'recovering') {
    throw new TypeError(`${method} response thread.status must be recovering`);
  }
  if (!normalizeString(value.mode)) {
    throw new TypeError(`${method} response mode must be a non-empty string`);
  }
  return value;
}

/** @param {string} method @param {unknown} response */
export function validateThreadPromptHistoryResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, PROMPT_HISTORY_RESPONSE_KEYS, 'body');
  if (!Array.isArray(value.entries)) throw new TypeError(`${method} response entries must be an array`);
  if (value.entries.length > 50) throw new TypeError(`${method} response entries must not exceed 50`);
  /** @type {unknown[]} */ (value.entries).forEach((candidate, index) => {
    const label = `entries[${index}]`;
    const entry = assertResponseRecord(method, candidate, label);
    assertOnlyResponseKeys(method, entry, PROMPT_HISTORY_ENTRY_KEYS, label);
    validateStringFields(method, entry, label, ['threadId', 'messageId', 'createdAt'], ['text']);
  });
  if (typeof value.nextCursor !== 'string') throw new TypeError(`${method} response nextCursor must be a string`);
  if (new TextEncoder().encode(value.nextCursor).byteLength > 2048) {
    throw new TypeError(`${method} response nextCursor exceeds 2048 bytes`);
  }
  if (typeof value.hasMore !== 'boolean') throw new TypeError(`${method} response hasMore must be a boolean`);
  if (value.hasMore && !value.nextCursor) {
    throw new TypeError(`${method} response nextCursor must be non-empty when hasMore is true`);
  }
  if (!value.hasMore && value.nextCursor !== '') {
    throw new TypeError(`${method} response nextCursor must be empty when hasMore is false`);
  }
  if (!normalizeString(value.nonce)) throw new TypeError(`${method} response nonce must be a non-empty string`);
  if (new TextEncoder().encode(value.nonce).byteLength > 2048) {
    throw new TypeError(`${method} response nonce exceeds 2048 bytes`);
  }
  return value;
}

/** @param {string} method @param {unknown} response */
export function validateToolbridgeToolsListResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, TOOLBRIDGE_TOOLS_RESPONSE_KEYS, 'body');
  if (!Array.isArray(value.tools)) throw new TypeError(`${method} response tools must be an array`);
  /** @type {unknown[]} */ (value.tools).forEach((candidate, index) => {
    const label = `tools[${index}]`;
    const tool = assertResponseRecord(method, candidate, label);
    assertOnlyResponseKeys(method, tool, TOOLBRIDGE_TOOL_RESPONSE_KEYS, label);
    for (const key of ['serverName', 'toolName', 'displayName']) {
      if (!normalizeString(tool[key])) {
        throw new TypeError(`${method} response ${label}.${key} must be a non-empty string`);
      }
    }
    validateStringFields(method, tool, label, [], ['description', 'disabledReason']);
    if (typeof tool.enabled !== 'boolean') {
      throw new TypeError(`${method} response ${label}.enabled must be a boolean`);
    }
  });
  return value;
}

/** @param {string} method @param {unknown} response */
export function validateCronListResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  const keys = new Set(['jobs', 'next_cursor', 'has_more']);
  if (Object.keys(value).length !== keys.size || [...keys].some((key) => !hasOwn(value, key))) {
    throw new Error(`${method} response must contain exactly jobs, next_cursor, has_more`);
  }
  assertOnlyResponseKeys(method, value, keys, 'body');
  if (!Array.isArray(value.jobs)) throw new TypeError(`${method} response jobs must be an array`);
  if (typeof value.next_cursor !== 'string') throw new TypeError(`${method} response next_cursor must be a string`);
  if (typeof value.has_more !== 'boolean') throw new TypeError(`${method} response has_more must be a boolean`);
  if (!value.has_more && value.next_cursor !== '') {
    throw new Error(`${method} response final page next_cursor must be empty`);
  }
  if (value.has_more && !value.next_cursor) {
    throw new Error(`${method} response next_cursor is required when has_more`);
  }
  return value;
}
