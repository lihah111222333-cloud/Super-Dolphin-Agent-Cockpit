// @ts-nocheck
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const cronApiMock = vi.hoisted(() => ({
  listJobs: vi.fn(),
  getJob: vi.fn(),
  createJob: vi.fn(),
  updateJob: vi.fn(),
  deleteJob: vi.fn(),
  setJobEnabled: vi.fn(),
  listJobRuns: vi.fn(),
  runOnce: vi.fn(),
  mapCronRpcError: vi.fn((err) => ({
    code: 0,
    kind: 'unknown',
    message: (err && err.message) || String(err || ''),
  })),
}));

const bridgeMock = vi.hoisted(() => ({
  onBridgeEvent: vi.fn(),
  unsubscribe: vi.fn(),
}));

vi.mock('./services/cron-api.js', () => cronApiMock);

vi.mock('./services/api.js', () => ({
  onBridgeEvent: bridgeMock.onBridgeEvent,
}));

vi.mock('./services/log.js', () => ({
  logDebug: vi.fn(),
  logInfo: vi.fn(),
  logWarn: vi.fn(),
}));

import { useCronStore, CRON_BRIDGE_EVENT_NAME } from './stores/cron.js';

function resetStoreState() {
  const store = useCronStore();
  store.state.jobs = [];
  store.state.runsByJob = {};
  store.state.loading = { list: false, runs: {} };
  store.state.error = { list: '', runs: {} };
}

beforeEach(() => {
  for (const key of Object.keys(cronApiMock)) {
    if (typeof cronApiMock[key]?.mockReset === 'function') cronApiMock[key].mockReset();
  }
  cronApiMock.mapCronRpcError.mockImplementation((err) => ({
    code: 0,
    kind: 'unknown',
    message: (err && err.message) || String(err || ''),
  }));
  bridgeMock.onBridgeEvent.mockReset();
  bridgeMock.unsubscribe.mockReset();
  bridgeMock.onBridgeEvent.mockImplementation(() => bridgeMock.unsubscribe);
  resetStoreState();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('cron store CRUD + optimistic updates', () => {
  it('loadJobs populates state and toggles loading flag', async () => {
    cronApiMock.listJobs.mockResolvedValueOnce([{ id: 'a', name: 'A' }]);
    const store = useCronStore();
    const promise = store.loadJobs();
    expect(store.state.loading.list).toBe(true);
    const jobs = await promise;
    expect(jobs).toEqual([{ id: 'a', name: 'A' }]);
    expect(store.state.jobs).toEqual([{ id: 'a', name: 'A' }]);
    expect(store.state.loading.list).toBe(false);
  });

  it('createJob upserts the returned job', async () => {
    cronApiMock.createJob.mockResolvedValueOnce({ id: 'new', name: 'x' });
    const store = useCronStore();
    const job = await store.createJob({ name: 'x' });
    expect(job).toEqual({ id: 'new', name: 'x' });
    expect(store.state.jobs).toEqual([{ id: 'new', name: 'x' }]);
  });

  it('setJobEnabled applies optimistic flip then commits server result', async () => {
    const store = useCronStore();
    store.state.jobs = [{ id: 'j1', enabled: true, name: 'demo' }];
    cronApiMock.setJobEnabled.mockImplementationOnce(async () => {
      // mid-flight: optimistic state must already be flipped
      expect(store.state.jobs[0].enabled).toBe(false);
    });
    await store.setJobEnabled('j1', false);
    expect(store.state.jobs[0].enabled).toBe(false);
  });

  it('setJobEnabled rolls back on failure', async () => {
    const store = useCronStore();
    store.state.jobs = [{ id: 'j1', enabled: true, name: 'demo' }];
    cronApiMock.setJobEnabled.mockRejectedValueOnce(new Error('boom'));
    await expect(store.setJobEnabled('j1', false)).rejects.toThrow('boom');
    expect(store.state.jobs[0].enabled).toBe(true);
  });

  it('deleteJob removes optimistically and rolls back on failure', async () => {
    const store = useCronStore();
    store.state.jobs = [{ id: 'j1', name: 'demo' }];
    store.state.runsByJob = { j1: [{ id: 'r1' }] };

    cronApiMock.deleteJob.mockRejectedValueOnce(new Error('nope'));
    await expect(store.deleteJob('j1')).rejects.toThrow('nope');
    expect(store.state.jobs).toEqual([{ id: 'j1', name: 'demo' }]);

    cronApiMock.deleteJob.mockResolvedValueOnce(undefined);
    await store.deleteJob('j1');
    expect(store.state.jobs).toEqual([]);
    expect(store.state.runsByJob.j1).toBeUndefined();
  });

  it('updateJob rolls back on failure', async () => {
    const store = useCronStore();
    store.state.jobs = [{ id: 'j1', name: 'before', enabled: true }];
    cronApiMock.updateJob.mockRejectedValueOnce(new Error('rejected'));
    await expect(store.updateJob('j1', { name: 'after' })).rejects.toThrow('rejected');
    expect(store.state.jobs[0]).toEqual({ id: 'j1', name: 'before', enabled: true });
  });

  it('runOnce upserts the returned job', async () => {
    cronApiMock.runOnce.mockResolvedValueOnce({ id: 'j1', name: 'demo', next_run_at: 'now' });
    const store = useCronStore();
    store.state.jobs = [{ id: 'j1', name: 'demo', next_run_at: 'tomorrow' }];
    const job = await store.runOnce('j1');
    expect(job.next_run_at).toBe('now');
    expect(store.state.jobs[0].next_run_at).toBe('now');
    expect(cronApiMock.runOnce).toHaveBeenCalledWith('j1');
  });

  it('loadRuns stores runs and tracks per-job loading flag', async () => {
    cronApiMock.listJobRuns.mockResolvedValueOnce([{ id: 'r1', status: 'finished' }]);
    const store = useCronStore();
    const promise = store.loadRuns('j1');
    expect(store.state.loading.runs.j1).toBe(true);
    const runs = await promise;
    expect(runs).toEqual([{ id: 'r1', status: 'finished' }]);
    expect(store.state.runsByJob.j1).toEqual([{ id: 'r1', status: 'finished' }]);
    expect(store.state.loading.runs.j1).toBeUndefined();
  });
});

describe('cron store bridge event handling', () => {
  it('attachBridge subscribes and applies matching events to runsByJob', () => {
    const store = useCronStore();
    store.attachBridge();
    expect(bridgeMock.onBridgeEvent).toHaveBeenCalledTimes(1);
    const handler = bridgeMock.onBridgeEvent.mock.calls[0][0];

    handler({
      name: CRON_BRIDGE_EVENT_NAME,
      data: { job_id: 'j1', run_id: 'r1', status: 'submitted' },
    });
    expect(store.state.runsByJob.j1).toEqual([
      { id: 'r1', job_id: 'j1', run_id: 'r1', status: 'submitted' },
    ]);

    // second event for same run → merge in place
    handler({
      name: CRON_BRIDGE_EVENT_NAME,
      data: { job_id: 'j1', run_id: 'r1', status: 'running', turn_id: 't1' },
    });
    expect(store.state.runsByJob.j1).toHaveLength(1);
    expect(store.state.runsByJob.j1[0].status).toBe('running');
    expect(store.state.runsByJob.j1[0].turn_id).toBe('t1');

    store.detachBridge();
  });

  it('attachBridge ignores events with mismatched name', () => {
    const store = useCronStore();
    store.attachBridge();
    const handler = bridgeMock.onBridgeEvent.mock.calls[0][0];
    handler({ name: 'something.else', data: { job_id: 'j1', run_id: 'r1' } });
    expect(store.state.runsByJob.j1).toBeUndefined();
    store.detachBridge();
  });

  it('detachBridge unsubscribes only when refcount returns to 0', () => {
    const store = useCronStore();
    store.attachBridge();
    store.attachBridge();
    store.detachBridge();
    expect(bridgeMock.unsubscribe).not.toHaveBeenCalled();
    store.detachBridge();
    expect(bridgeMock.unsubscribe).toHaveBeenCalledTimes(1);
  });

  it('applyRunStateEvent rejects payloads missing required ids', () => {
    const store = useCronStore();
    expect(store._internal.applyRunStateEvent({ run_id: 'r1' })).toBe(false);
    expect(store._internal.applyRunStateEvent({ job_id: 'j1' })).toBe(false);
    expect(store._internal.applyRunStateEvent(null)).toBe(false);
    expect(store.state.runsByJob).toEqual({});
  });
});
