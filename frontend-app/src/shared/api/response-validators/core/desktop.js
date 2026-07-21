// @ts-check

import {
  assertBackendResponseObject,
  assertOnlyResponseKeys,
  hasOwn,
} from '../shared.js';

const FRONTEND_INGEST_RESPONSE_KEYS = new Set(['enabled', 'recorded', 'dropped', 'disabled_reason']);
const OPEN_WINDOW_RESPONSE_KEYS = new Set(['ok', 'windowId', 'cwd']);
const CODE_SAVE_RESPONSE_KEYS = new Set(['ok', 'filePath', 'relative', 'totalLines']);
const PROJECTS_STATE_RESPONSE_KEYS = new Set(['projects', 'active']);
const OK_RESPONSE_KEYS = new Set(['ok']);
const DASHBOARD_PAGE_RESPONSE_KEYS = new Set(['agents', 'dags', 'skills', 'commandCards', 'prompts', 'memory', 'finalOutputRefs', 'sharedFileRetention']);
const SHARED_FILE_RETENTION_KEYS = new Set(['items', 'protectedCount', 'cleanupCandidateCount']);
const VIDEO_API_KEY_STATUS_RESPONSE_KEYS = new Set(['configured', 'masked']);
const DASHBOARD_LOGS_RESPONSE_KEYS = new Set(['logs']);
const DASHBOARD_LOG_ENTRY_KEYS = new Set(['source', 'id', 'timestamp', 'level', 'logger', 'message', 'raw', 'component', 'agent_id', 'thread_id', 'trace_id', 'span_id', 'parent_span_id', 'event_type', 'tool_name', 'duration_ms']);

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
  for (const key of ['filePath', 'relative']) {
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

export { validateCodeSaveResponse, validateDashboardLogsResponse, validateDashboardPageResponse, validateFrontendIngestResponse, validateNullResponse, validateOKResponse, validateOpenWindowResponse, validateProjectsStateResponse, validateVideoAPIKeyStatusResponse };
