// @ts-nocheck
// Cron store. Single source of truth for cron jobs + runs in the UI.
// Components must not call cron-api directly; they go through this store
// so optimistic updates and runtime event subscriptions stay consistent.
import { reactive } from '../../lib/vue.esm-browser.prod.js';
import * as cronApi from '../services/cron-api.js';
import { onBridgeEvent } from '../services/api.js';
import { logDebug, logInfo, logWarn } from '../services/log.js';

export const CRON_BRIDGE_EVENT_NAME = 'cron/job/runStateChanged';

const state = reactive({
  jobs: [],
  runsByJob: {},
  loading: { list: false, runs: {} },
  error: { list: '', runs: {} },
});

function findJobIndex(id) {
  return state.jobs.findIndex((job) => job && job.id === id);
}

function upsertJob(job) {
  if (!job || typeof job !== 'object' || !job.id) return;
  const idx = findJobIndex(job.id);
  if (idx === -1) {
    state.jobs = [...state.jobs, job];
  } else {
    const next = state.jobs.slice();
    next[idx] = { ...next[idx], ...job };
    state.jobs = next;
  }
}

function removeJob(id) {
  state.jobs = state.jobs.filter((job) => job && job.id !== id);
  if (state.runsByJob[id]) {
    const next = { ...state.runsByJob };
    delete next[id];
    state.runsByJob = next;
  }
}

async function loadJobs() {
  state.loading.list = true;
  state.error.list = '';
  try {
    const jobs = await cronApi.listJobs();
    state.jobs = Array.isArray(jobs) ? jobs : [];
    logDebug('cron', 'jobs.loaded', { count: state.jobs.length });
    return state.jobs;
  } catch (err) {
    const mapped = cronApi.mapCronRpcError(err);
    state.error.list = mapped.message || 'failed to load cron jobs';
    logWarn('cron', 'jobs.load.failed', { kind: mapped.kind, message: mapped.message });
    throw err;
  } finally {
    state.loading.list = false;
  }
}

async function createJob(input) {
  const job = await cronApi.createJob(input);
  upsertJob(job);
  logInfo('cron', 'job.created', { id: job?.id });
  return job;
}

async function updateJob(id, input) {
  const prevIdx = findJobIndex(id);
  const prev = prevIdx === -1 ? null : { ...state.jobs[prevIdx] };
  if (prev) {
    upsertJob({ ...prev, ...input, id });
  }
  try {
    const job = await cronApi.updateJob(id, input);
    upsertJob(job);
    logInfo('cron', 'job.updated', { id });
    return job;
  } catch (err) {
    if (prev) upsertJob(prev);
    throw err;
  }
}

async function setJobEnabled(id, enabled) {
  const idx = findJobIndex(id);
  if (idx === -1) {
    await cronApi.setJobEnabled(id, enabled);
    return;
  }
  const prev = { ...state.jobs[idx] };
  upsertJob({ ...prev, enabled });
  try {
    await cronApi.setJobEnabled(id, enabled);
    logDebug('cron', 'job.enabled.set', { id, enabled });
  } catch (err) {
    upsertJob(prev);
    throw err;
  }
}

async function deleteJob(id) {
  const idx = findJobIndex(id);
  const prev = idx === -1 ? null : { ...state.jobs[idx] };
  if (prev) removeJob(id);
  try {
    await cronApi.deleteJob(id);
    logInfo('cron', 'job.deleted', { id });
  } catch (err) {
    if (prev) upsertJob(prev);
    throw err;
  }
}

async function runOnce(id) {
  const job = await cronApi.runOnce(id);
  upsertJob(job);
  logInfo('cron', 'job.runOnce', { id });
  return job;
}

async function loadRuns(jobId, limit = 0) {
  state.loading.runs = { ...state.loading.runs, [jobId]: true };
  state.error.runs = { ...state.error.runs, [jobId]: '' };
  try {
    const runs = await cronApi.listJobRuns(jobId, limit);
    state.runsByJob = { ...state.runsByJob, [jobId]: Array.isArray(runs) ? runs : [] };
    return state.runsByJob[jobId];
  } catch (err) {
    const mapped = cronApi.mapCronRpcError(err);
    state.error.runs = { ...state.error.runs, [jobId]: mapped.message || 'failed to load runs' };
    logWarn('cron', 'runs.load.failed', { job_id: jobId, kind: mapped.kind, message: mapped.message });
    throw err;
  } finally {
    const next = { ...state.loading.runs };
    delete next[jobId];
    state.loading.runs = next;
  }
}

// applyRunStateEvent merges a wails bridge event into runsByJob.
// Event payload shape: { job_id, run_id, status, turn_id?, error?, scheduled_at?, submitted_at? }.
function applyRunStateEvent(payload) {
  if (!payload || typeof payload !== 'object') return false;
  const jobId = payload.job_id;
  const runId = payload.run_id;
  if (typeof jobId !== 'string' || jobId === '' || typeof runId !== 'string' || runId === '') {
    return false;
  }
  const existing = Array.isArray(state.runsByJob[jobId]) ? state.runsByJob[jobId] : [];
  const idx = existing.findIndex((r) => r && r.id === runId);
  let nextList;
  if (idx === -1) {
    nextList = [{ id: runId, job_id: jobId, ...payload }, ...existing];
  } else {
    nextList = existing.slice();
    nextList[idx] = { ...nextList[idx], ...payload, id: runId, job_id: jobId };
  }
  state.runsByJob = { ...state.runsByJob, [jobId]: nextList };
  return true;
}

let bridgeUnsubscribe = null;
let bridgeRefCount = 0;

function attachBridge() {
  bridgeRefCount += 1;
  if (bridgeUnsubscribe) return bridgeUnsubscribe;
  bridgeUnsubscribe = onBridgeEvent((evt) => {
    if (!evt || typeof evt !== 'object') return;
    const name = evt.name || evt.method || evt.type;
    if (name !== CRON_BRIDGE_EVENT_NAME) return;
    const payload = evt.data || evt.payload || evt;
    const ok = applyRunStateEvent(payload);
    if (!ok) {
      logWarn('cron', 'bridge.event.dropped', { reason: 'invalid_payload' });
    }
  });
  return bridgeUnsubscribe;
}

function detachBridge() {
  bridgeRefCount = Math.max(0, bridgeRefCount - 1);
  if (bridgeRefCount > 0) return;
  if (typeof bridgeUnsubscribe === 'function') {
    try { bridgeUnsubscribe(); } catch { /* ignore */ }
  }
  bridgeUnsubscribe = null;
}

export function useCronStore() {
  return {
    state,
    loadJobs,
    createJob,
    updateJob,
    setJobEnabled,
    deleteJob,
    runOnce,
    loadRuns,
    attachBridge,
    detachBridge,
    // exported for tests
    _internal: { applyRunStateEvent, upsertJob, removeJob },
  };
}
