// @ts-check

import { assertBackendResponseObject, assertOnlyResponseKeys, hasOwn, normalizeString } from '../shared.js';
import { requireResponseKey } from './config.js';

const THREAD_MESSAGES_RESPONSE_KEYS = new Set(['messages', 'total', 'hasMore', 'nextBefore']);

const THREAD_MESSAGE_KEYS = new Set(['id', 'agentId', 'role', 'eventType', 'method', 'content', 'createdAt', 'metadata']);

/** @param {string} method @param {unknown} response */
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
  assertOnlyResponseKeys(method, value, THREAD_MESSAGES_RESPONSE_KEYS, 'body');
  if (!Array.isArray(value.messages)) {
    throw new TypeError(`${method} response messages must be an array`);
  }
  if (typeof value.total !== 'number' || !Number.isSafeInteger(value.total) || value.total < 0) {
    throw new TypeError(`${method} response total must be a non-negative integer`);
  }
  if (typeof value.hasMore !== 'boolean') {
    throw new TypeError(`${method} response hasMore must be a boolean`);
  }
  if (typeof value.nextBefore !== 'string') {
    throw new TypeError(`${method} response nextBefore must be a string`);
  }
  value.messages.forEach((message, index) => {
    if (!message || typeof message !== 'object' || Array.isArray(message)) {
      throw new TypeError(`${method} response messages[${index}] must be an object`);
    }
    const record = /** @type {Record<string, unknown>} */ (message);
    assertOnlyResponseKeys(method, record, THREAD_MESSAGE_KEYS, `messages[${index}]`);
    if (typeof record.id !== 'number' || !Number.isSafeInteger(record.id) || record.id < 0) throw new TypeError(`${method} response messages[${index}].id must be a non-negative integer`);
    for (const key of ['agentId', 'role', 'eventType', 'method', 'content', 'createdAt']) {
      if (typeof record[key] !== 'string') throw new TypeError(`${method} response messages[${index}].${key} must be a string`);
    }
    if (hasOwn(record, 'metadata') && (!record.metadata || typeof record.metadata !== 'object' || Array.isArray(record.metadata))) {
      throw new TypeError(`${method} response messages[${index}].metadata must be an object`);
    }
  });
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

export { validateThreadForkResponse, validateThreadMessagesResponse, validateThreadResolveResponse, validateThreadStartResponse, validateTurnForceCompleteResponse, validateTurnStartResponse };
