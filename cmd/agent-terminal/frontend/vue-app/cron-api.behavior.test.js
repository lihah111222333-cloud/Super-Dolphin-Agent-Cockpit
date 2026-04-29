// @ts-nocheck
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const apiMock = vi.hoisted(() => ({
  callAPI: vi.fn(),
}));

vi.mock('./services/api.js', () => ({
  callAPI: apiMock.callAPI,
}));

import {
  mapCronRpcError,
  listJobs,
  getJob,
  createJob,
  updateJob,
  deleteJob,
  setJobEnabled,
  listJobRuns,
  runOnce,
} from './services/cron-api.js';

beforeEach(() => {
  apiMock.callAPI.mockReset();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('cron-api error mapping', () => {
  it('maps each cron sentinel message to its kind', () => {
    const cases = [
      ['cron: cwd is required', 'cwd_required'],
      ['cron: name is required', 'name_required'],
      ['cron: prompt is required', 'prompt_required'],
      ['cron: schedule_expr is required', 'schedule_required'],
      ['cron: max_attempts must be >= 0', 'invalid_max_attempts'],
      ['cron: config is invalid for provider', 'invalid_config'],
      ["cron: provider not supported in v1 (only 'codex')", 'provider_unsupported'],
      ['cron: job not found', 'not_found'],
    ];
    for (const [message, kind] of cases) {
      expect(mapCronRpcError(new Error(message)).kind).toBe(kind);
    }
  });

  it('falls back to unknown for unmatched messages', () => {
    expect(mapCronRpcError(new Error('something else')).kind).toBe('unknown');
    expect(mapCronRpcError(null).kind).toBe('unknown');
    expect(mapCronRpcError({ message: '', code: -32602 })).toEqual({
      code: -32602,
      kind: 'unknown',
      message: '',
    });
  });

  it('preserves jrpc2 numeric code on the mapped result', () => {
    const err = Object.assign(new Error('cron: cwd is required'), { code: -32602 });
    expect(mapCronRpcError(err)).toEqual({
      code: -32602,
      kind: 'cwd_required',
      message: 'cron: cwd is required',
    });
  });
});

describe('cron-api RPC wrappers', () => {
  it('listJobs returns array; defaults to [] when result has no jobs', async () => {
    apiMock.callAPI.mockResolvedValueOnce({ jobs: [{ id: 'a' }] });
    expect(await listJobs()).toEqual([{ id: 'a' }]);
    expect(apiMock.callAPI).toHaveBeenCalledWith('cronjob/list', {});

    apiMock.callAPI.mockResolvedValueOnce({});
    expect(await listJobs()).toEqual([]);
  });

  it('getJob requires non-empty id and forwards params', async () => {
    apiMock.callAPI.mockResolvedValueOnce({ id: 'j1', name: 'demo' });
    const job = await getJob('j1');
    expect(job).toEqual({ id: 'j1', name: 'demo' });
    expect(apiMock.callAPI).toHaveBeenCalledWith('cronjob/get', { id: 'j1' });

    await expect(getJob('')).rejects.toBeInstanceOf(TypeError);
    await expect(getJob(123)).rejects.toBeInstanceOf(TypeError);
  });

  it('createJob requires object input', async () => {
    apiMock.callAPI.mockResolvedValueOnce({ id: 'new' });
    const out = await createJob({ name: 'x' });
    expect(out).toEqual({ id: 'new' });
    expect(apiMock.callAPI).toHaveBeenCalledWith('cronjob/create', { name: 'x' });

    await expect(createJob(null)).rejects.toBeInstanceOf(TypeError);
    await expect(createJob([])).rejects.toBeInstanceOf(TypeError);
  });

  it('updateJob merges id into payload', async () => {
    apiMock.callAPI.mockResolvedValueOnce({ id: 'j1', name: 'after' });
    const out = await updateJob('j1', { name: 'after' });
    expect(out).toEqual({ id: 'j1', name: 'after' });
    expect(apiMock.callAPI).toHaveBeenCalledWith('cronjob/update', { id: 'j1', name: 'after' });
  });

  it('deleteJob and setJobEnabled call expected methods', async () => {
    apiMock.callAPI.mockResolvedValueOnce({ deleted: true });
    await deleteJob('j1');
    expect(apiMock.callAPI).toHaveBeenLastCalledWith('cronjob/delete', { id: 'j1' });

    apiMock.callAPI.mockResolvedValueOnce({});
    await setJobEnabled('j1', true);
    expect(apiMock.callAPI).toHaveBeenLastCalledWith('cronjob/setEnabled', { id: 'j1', enabled: true });

    await expect(setJobEnabled('j1', 'yes')).rejects.toBeInstanceOf(TypeError);
  });

  it('runOnce calls cronjob/runOnce with id', async () => {
    apiMock.callAPI.mockResolvedValueOnce({ id: 'j1', name: 'demo', next_run_at: 'now' });
    const out = await runOnce('j1');
    expect(out).toEqual({ id: 'j1', name: 'demo', next_run_at: 'now' });
    expect(apiMock.callAPI).toHaveBeenLastCalledWith('cronjob/runOnce', { id: 'j1' });

    await expect(runOnce('')).rejects.toBeInstanceOf(TypeError);
  });

  it('listJobRuns omits limit when not a positive int', async () => {
    apiMock.callAPI.mockResolvedValueOnce({ runs: [{ id: 'r1' }] });
    expect(await listJobRuns('j1')).toEqual([{ id: 'r1' }]);
    expect(apiMock.callAPI).toHaveBeenLastCalledWith('cronjob/listRuns', { job_id: 'j1' });

    apiMock.callAPI.mockResolvedValueOnce({ runs: [] });
    await listJobRuns('j1', 50);
    expect(apiMock.callAPI).toHaveBeenLastCalledWith('cronjob/listRuns', { job_id: 'j1', limit: 50 });

    apiMock.callAPI.mockResolvedValueOnce({});
    expect(await listJobRuns('j1', 0)).toEqual([]);
  });
});
