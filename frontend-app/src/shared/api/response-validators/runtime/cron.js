// @ts-check

import {
  assertBackendResponseObject,
  assertOnlyResponseKeys,
  assertResponseRecord,
  hasOwn,
  normalizeString,
  validateStringFields,
} from '../shared.js';

/** @param {string} method @param {unknown} response */
function validateCronListResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  const keys = new Set(['jobs', 'next_cursor', 'has_more']);
  if (Object.keys(value).length !== keys.size || [...keys].some((key) => !hasOwn(value, key))) {
    throw new Error(`${method} response must contain exactly jobs, next_cursor, has_more`);
  }
  assertOnlyResponseKeys(method, value, keys, 'body');
  if (!Array.isArray(value.jobs)) throw new TypeError(`${method} response jobs must be an array`);
  value.jobs.forEach((job, index) => validateCronJob(method, job, `jobs[${index}]`));
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

const CRON_JOB_RESPONSE_KEYS = new Set([
  'id', 'name', 'prompt', 'schedule_type', 'schedule_expr', 'timezone', 'provider', 'model', 'cwd',
  'config', 'skills', 'notify_channel', 'enabled', 'next_run_at', 'last_scheduled_at', 'last_run_at',
  'thread_id', 'agent_id', 'active_turn_id', 'last_turn_id', 'failure_count', 'max_attempts',
  'last_status', 'last_error', 'last_error_at', 'created_at', 'updated_at',
]);
const CRON_JOB_REQUIRED_STRING_KEYS = ['id', 'name', 'prompt', 'schedule_type', 'schedule_expr', 'provider', 'cwd'];
const CRON_JOB_OPTIONAL_STRING_KEYS = [
  'timezone', 'model', 'notify_channel', 'next_run_at', 'last_scheduled_at', 'last_run_at', 'thread_id',
  'agent_id', 'active_turn_id', 'last_turn_id', 'last_status', 'last_error', 'last_error_at', 'created_at', 'updated_at',
];
const CRON_RUN_RESPONSE_KEYS = new Set([
  'id', 'job_id', 'scheduled_at', 'idempotency_key', 'dedupe_key', 'thread_id', 'agent_id', 'turn_id',
  'submitted_at', 'status', 'error', 'created_at', 'updated_at',
]);
const CRON_RUN_REQUIRED_STRING_KEYS = ['id', 'job_id', 'status'];
const CRON_RUN_OPTIONAL_STRING_KEYS = [
  'scheduled_at', 'idempotency_key', 'dedupe_key', 'thread_id', 'agent_id', 'turn_id', 'submitted_at',
  'error', 'created_at', 'updated_at',
];

/** @param {string} method @param {unknown} response @param {string} label */
function validateCronJob(method, response, label) {
  const job = assertResponseRecord(method, response, label);
  assertOnlyResponseKeys(method, job, CRON_JOB_RESPONSE_KEYS, label);
  validateStringFields(method, job, label, CRON_JOB_REQUIRED_STRING_KEYS, CRON_JOB_OPTIONAL_STRING_KEYS);
  if (typeof job.enabled !== 'boolean') throw new TypeError(`${method} response ${label}.enabled must be a boolean`);
  for (const key of ['failure_count', 'max_attempts']) {
    if (!Number.isInteger(job[key])) throw new TypeError(`${method} response ${label}.${key} must be an integer`);
  }
  if (hasOwn(job, 'skills')) {
    if (!Array.isArray(job.skills) || job.skills.some((skill) => typeof skill !== 'string')) {
      throw new TypeError(`${method} response ${label}.skills must be an array of strings`);
    }
  }
  return job;
}

/** @param {string} method @param {unknown} response */
function validateCronJobResponse(method, response) {
  return validateCronJob(method, response, 'body');
}

/** @param {string} method @param {unknown} response @param {unknown} request */
function validateCronDeleteResponse(method, response, request) {
  const value = assertBackendResponseObject(method, response);
  const keys = new Set(['deleted', 'id']);
  assertOnlyResponseKeys(method, value, keys, 'body');
  if (value.deleted !== true) throw new TypeError(`${method} response body.deleted must be true`);
  if (typeof value.id !== 'string') throw new TypeError(`${method} response body.id must be a string`);
  const payload = assertResponseRecord(method, request, 'request');
  const expectedID = normalizeString(payload.id);
  if (!expectedID) throw new TypeError(`${method} request id must be a non-empty string for response correlation`);
  if (value.id !== expectedID) throw new TypeError(`${method} response id must equal request id`);
  return value;
}

/** @param {string} method @param {unknown} response @param {unknown} request */
function validateCronSetEnabledResponse(method, response, request) {
  const value = assertBackendResponseObject(method, response);
  const keys = new Set(['id', 'enabled']);
  assertOnlyResponseKeys(method, value, keys, 'body');
  if (typeof value.id !== 'string') throw new TypeError(`${method} response body.id must be a string`);
  if (typeof value.enabled !== 'boolean') throw new TypeError(`${method} response body.enabled must be a boolean`);
  const payload = assertResponseRecord(method, request, 'request');
  const expectedID = normalizeString(payload.id);
  if (!expectedID) throw new TypeError(`${method} request id must be a non-empty string for response correlation`);
  if (typeof payload.enabled !== 'boolean') throw new TypeError(`${method} request enabled must be a boolean for response correlation`);
  if (value.id !== expectedID) throw new TypeError(`${method} response id must equal request id`);
  if (value.enabled !== payload.enabled) throw new TypeError(`${method} response enabled must equal request enabled`);
  return value;
}

/** @param {string} method @param {unknown} response */
function validateCronListRunsResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  const keys = new Set(['runs']);
  assertOnlyResponseKeys(method, value, keys, 'body');
  if (!Array.isArray(value.runs)) throw new TypeError(`${method} response body.runs must be an array`);
  value.runs.forEach((candidate, index) => {
    const run = assertResponseRecord(method, candidate, `runs[${index}]`);
    assertOnlyResponseKeys(method, run, CRON_RUN_RESPONSE_KEYS, `runs[${index}]`);
    validateStringFields(method, run, `runs[${index}]`, CRON_RUN_REQUIRED_STRING_KEYS, CRON_RUN_OPTIONAL_STRING_KEYS);
  });
  return value;
}


export { validateCronDeleteResponse, validateCronJobResponse, validateCronListRunsResponse, validateCronListResponse, validateCronSetEnabledResponse };
