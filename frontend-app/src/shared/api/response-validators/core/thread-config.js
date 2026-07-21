// @ts-check

import { assertBackendResponseObject, assertOnlyResponseKeys, assertResponseRecord, hasOwn, validateStringFields } from '../shared.js';

const THREAD_CONFIG_RESPONSE_KEYS = new Set(['threadId', 'provider', 'supportsThreadOverride', 'override', 'effective']);
const THREAD_CONFIG_VALUES_KEYS = new Set(['model', 'effort', 'approvals']);
const THREAD_COMPACT_RESPONSE_KEYS = new Set(['threadId', 'command', 'beforeTokens', 'afterTokens', 'compacted', 'estimated']);
const THREAD_RECOVER_RESPONSE_KEYS = new Set(['thread', 'recovered', 'mode']);
const THREAD_RECOVER_THREAD_KEYS = new Set(['id', 'status']);

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

export { validateThreadCompactResponse, validateThreadConfigResponse, validateThreadRecoverResponse };
