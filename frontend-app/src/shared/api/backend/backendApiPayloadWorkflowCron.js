import { RPC_METHODS } from './backendRpcMethods.js';
import {
  assertPlainObject,
  cleanObject,
  hasOwn,
  normalizeOptionalLimit,
  normalizeString,
  requireBoolean,
  requireCwd,
  requireKey,
} from './backendApiCommon.js';

/** @typedef {Record<string, unknown>} WorkflowPayload */

/** @param {string} method @param {unknown} params */
function cronIdPayload(method, params) {
  return {
    id: requireKey(method, assertPlainObject(method, params), 'id').id,
  };
}

/** @param {unknown} params */
function cronSetEnabledPayload(params) {
  const method = RPC_METHODS.CRONJOB_SET_ENABLED;
  const payload = requireBoolean(
    method,
    requireKey(method, assertPlainObject(method, params), 'id'),
    'enabled',
  );
  return { id: payload.id, enabled: payload.enabled };
}

/** @param {unknown} params */
function cronListRunsPayload(params) {
  const method = RPC_METHODS.CRONJOB_LIST_RUNS;
  const payload = assertPlainObject(method, params);
  const jobID = normalizeString(payload.job_id || payload.jobId);
  if (!jobID) throw new Error(`${method}: job_id is required`);
  return cleanObject({
    job_id: jobID,
    limit: normalizeOptionalLimit(method, payload),
  });
}

/** @param {unknown} params */
function cronListPayload(params) {
  const method = RPC_METHODS.CRONJOB_LIST;
  const payload = assertPlainObject(method, params);
  if (!hasOwn(payload, 'limit') || !hasOwn(payload, 'cursor')) {
    throw new Error(`${method}: limit and cursor are required`);
  }
  if (Object.keys(payload).some((key) => key !== 'limit' && key !== 'cursor')) {
    throw new Error(`${method}: unexpected payload field`);
  }
  if (
    typeof payload.limit !== 'number'
    || !Number.isInteger(payload.limit)
    || payload.limit < 1
    || payload.limit > 100
  ) {
    throw new Error(`${method}: limit must be an integer within range`);
  }
  if (typeof payload.cursor !== 'string') throw new Error(`${method}: cursor must be a string`);
  return { limit: payload.limit, cursor: payload.cursor };
}

/** @param {string} method @param {WorkflowPayload} payload */
function cronJobConfigPayload(method, payload) {
  if (!hasOwn(payload, 'config') || payload.config == null) return undefined;
  if (typeof payload.config !== 'object' || Array.isArray(payload.config)) {
    throw new Error(`${method}: config must be an object`);
  }
  return payload.config;
}

/** @param {string} method @param {WorkflowPayload} payload */
function cronJobSkillsPayload(method, payload) {
  if (!hasOwn(payload, 'skills') || payload.skills == null) return undefined;
  if (!Array.isArray(payload.skills)) throw new Error(`${method}: skills must be an array`);
  return payload.skills.map(normalizeString).filter(Boolean);
}

/** @param {string} method @param {WorkflowPayload} payload */
function cronJobEnabledPayload(method, payload) {
  if (!hasOwn(payload, 'enabled') || payload.enabled == null) return undefined;
  if (typeof payload.enabled !== 'boolean') throw new Error(`${method}: enabled must be boolean`);
  return payload.enabled;
}

/** @param {string} method @param {WorkflowPayload} payload */
function cronJobMaxAttemptsPayload(method, payload) {
  const raw = payload.max_attempts ?? payload.maxAttempts;
  if (raw === undefined || raw === null || raw === '') return undefined;
  const value = Number(raw);
  if (!Number.isInteger(value) || value < 0) {
    throw new Error(`${method}: max_attempts must be a non-negative integer`);
  }
  return value;
}

/** @param {string} method @param {unknown} params @param {{ requireId?: boolean }} options */
function cronJobMutationPayload(method, params, options = {}) {
  const payload = /** @type {WorkflowPayload & { cwd: string }} */ (requireCwd(method, params));
  const name = normalizeString(payload.name);
  const prompt = normalizeString(payload.prompt);
  const scheduleExpr = normalizeString(payload.schedule_expr ?? payload.scheduleExpr);
  if (!name) throw new Error(`${method}: name is required`);
  if (!prompt) throw new Error(`${method}: prompt is required`);
  if (!scheduleExpr) throw new Error(`${method}: schedule_expr is required`);
  return cleanObject({
    id: options.requireId ? requireKey(method, payload, 'id').id : undefined,
    cwd: payload.cwd,
    name,
    prompt,
    schedule_type: normalizeString(payload.schedule_type ?? payload.scheduleType),
    schedule_expr: scheduleExpr,
    timezone: normalizeString(payload.timezone),
    provider: normalizeString(payload.provider),
    model: normalizeString(payload.model),
    config: cronJobConfigPayload(method, payload),
    skills: cronJobSkillsPayload(method, payload),
    notify_channel: normalizeString(payload.notify_channel ?? payload.notifyChannel),
    enabled: cronJobEnabledPayload(method, payload),
    next_run_at: normalizeString(payload.next_run_at ?? payload.nextRunAt),
    max_attempts: cronJobMaxAttemptsPayload(method, payload),
  });
}

/** @param {string} method @param {WorkflowPayload} payload */
function codeProjectsPayload(method, payload) {
  if (!hasOwn(payload, 'projects') || payload.projects == null) return undefined;
  if (!Array.isArray(payload.projects)) throw new Error(`${method}: projects must be an array`);
  const projects = payload.projects.map(normalizeString).filter(Boolean);
  return projects.length > 0 ? projects : undefined;
}

/** @param {string} method @param {WorkflowPayload} payload @param {string} key */
function optionalCodeInteger(method, payload, key) {
  if (!hasOwn(payload, key) || payload[key] === undefined || payload[key] === null || payload[key] === '') {
    return undefined;
  }
  const value = Number(payload[key]);
  if (!Number.isFinite(value)) throw new Error(`${method}: ${key} must be a number`);
  return Math.trunc(value);
}

/**
 * @param {string} method
 * @param {unknown} params
 * @param {{ includePosition?: boolean, includeContent?: boolean }} options
 */
function codeFilePayload(method, params, options = {}) {
  const payload = requireKey(method, assertPlainObject(method, params), 'filePath');
  /** @type {WorkflowPayload} */
  const request = {
    filePath: payload.filePath,
    project: normalizeString(payload.project),
    projects: codeProjectsPayload(method, payload),
  };
  if (options.includePosition) {
    request.line = optionalCodeInteger(method, payload, 'line');
    request.column = optionalCodeInteger(method, payload, 'column');
  }
  if (options.includeContent) {
    if (!hasOwn(payload, 'content')) throw new Error(`${method}: content is required`);
    if (typeof payload.content !== 'string') throw new Error(`${method}: content must be a string`);
    request.content = payload.content;
  }
  return cleanObject(request);
}

export {
  cronIdPayload,
  cronSetEnabledPayload,
  cronListRunsPayload,
  cronListPayload,
  cronJobMutationPayload,
  cronJobConfigPayload,
  cronJobSkillsPayload,
  cronJobEnabledPayload,
  cronJobMaxAttemptsPayload,
  codeProjectsPayload,
  optionalCodeInteger,
  codeFilePayload,
};
