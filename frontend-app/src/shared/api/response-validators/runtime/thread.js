// @ts-check

import {
  assertBackendResponseObject,
  assertOnlyResponseKeys,
  assertResponseRecord,
  normalizeString,
  validateStringFields,
} from '../shared.js';
import { validateThreadRecoverResponse as validateCoreThreadRecoverResponse } from '../core/thread.js';

const PROMPT_HISTORY_RESPONSE_KEYS = new Set(['entries', 'nextCursor', 'hasMore', 'nonce']);
const PROMPT_HISTORY_ENTRY_KEYS = new Set(['threadId', 'messageId', 'text', 'createdAt']);

/** @param {string} method @param {unknown} response */
function validateThreadRecoverResponse(method, response) {
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
function validateThreadPromptHistoryResponse(method, response) {
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
  if (typeof value.nonce !== 'string' || !normalizeString(value.nonce)) throw new TypeError(`${method} response nonce must be a non-empty string`);
  if (new TextEncoder().encode(value.nonce).byteLength > 2048) {
    throw new TypeError(`${method} response nonce exceeds 2048 bytes`);
  }
  return value;
}

/** @param {string} method @param {unknown} response */

export { validateThreadRecoverResponse, validateThreadPromptHistoryResponse };
