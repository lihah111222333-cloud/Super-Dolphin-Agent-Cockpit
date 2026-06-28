// Cron API thin wrapper.
// Single canonical caller for cronjob/* host RPC. Components must NOT call
// callAPI('cronjob/...') directly; they go through this module so error
// kinds and shapes stay consistent.
//
// Error mapping: internal/module/cron/rpc.go::mapRPCError folds all
// validation sentinels into jrpc2.InvalidParams, so the only reliable
// signal at this layer is the message text. All cron service errors are
// prefixed with "cron: " (see internal/module/cron/contract.go:31-40).
import { callAPI } from './api.js';

const ERROR_KIND_BY_PREFIX = Object.freeze([
  ['cron: cwd is required', 'cwd_required'],
  ['cron: name is required', 'name_required'],
  ['cron: prompt is required', 'prompt_required'],
  ['cron: schedule_expr is required', 'schedule_required'],
  ['cron: max_attempts must be', 'invalid_max_attempts'],
  ['cron: config is invalid for provider', 'invalid_config'],
  ['cron: provider not supported', 'provider_unsupported'],
  ['cron: job not found', 'not_found'],
  ['cron: cannot trigger disabled job', 'job_disabled'],
]);

export function mapCronRpcError(err) {
  const message = (err && typeof err === 'object' && typeof err.message === 'string')
    ? err.message
    : String(err || '');
  const code = (err && typeof err === 'object' && typeof err.code === 'number') ? err.code : 0;
  for (const [prefix, kind] of ERROR_KIND_BY_PREFIX) {
    if (message.includes(prefix)) {
      return { code, kind, message };
    }
  }
  return { code, kind: 'unknown', message };
}

function ensureObject(value, label) {
  if (value && typeof value === 'object' && !Array.isArray(value)) return value;
  throw new TypeError(`cron-api: ${label} must be an object`);
}

function ensureNonEmptyString(value, label) {
  if (typeof value !== 'string' || value === '') {
    throw new TypeError(`cron-api: ${label} must be a non-empty string`);
  }
  return value;
}

export async function listJobs() {
  const res = await callAPI('cronjob/list', {});
  const jobs = Array.isArray(res?.jobs) ? res.jobs : [];
  return jobs;
}

export async function getJob(id) {
  ensureNonEmptyString(id, 'getJob.id');
  const res = await callAPI('cronjob/get', { id });
  return res || null;
}

export async function createJob(input) {
  ensureObject(input, 'createJob.input');
  return callAPI('cronjob/create', input);
}

export async function updateJob(id, input) {
  ensureNonEmptyString(id, 'updateJob.id');
  ensureObject(input, 'updateJob.input');
  return callAPI('cronjob/update', { id, ...input });
}

export async function deleteJob(id) {
  ensureNonEmptyString(id, 'deleteJob.id');
  await callAPI('cronjob/delete', { id });
}

export async function runOnce(id) {
  ensureNonEmptyString(id, 'runOnce.id');
  return callAPI('cronjob/runOnce', { id });
}

export async function setJobEnabled(id, enabled) {
  ensureNonEmptyString(id, 'setJobEnabled.id');
  if (typeof enabled !== 'boolean') {
    throw new TypeError('cron-api: setJobEnabled.enabled must be a boolean');
  }
  await callAPI('cronjob/setEnabled', { id, enabled });
}

export async function listJobRuns(jobId, limit = 0) {
  ensureNonEmptyString(jobId, 'listJobRuns.jobId');
  const params = { job_id: jobId };
  if (Number.isInteger(limit) && limit > 0) {
    params.limit = limit;
  }
  const res = await callAPI('cronjob/listRuns', params);
  return Array.isArray(res?.runs) ? res.runs : [];
}
