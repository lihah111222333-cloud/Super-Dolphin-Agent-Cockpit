// @ts-nocheck
import { describe, expect, it, vi } from 'vitest';

vi.mock('./services/log.js', () => ({
  logDebug: vi.fn(),
  logInfo: vi.fn(),
  logWarn: vi.fn(),
}));

const storeMock = vi.hoisted(() => ({
  state: {
    jobs: [],
    runsByJob: {},
    loading: { list: false, runs: {} },
    error: { list: '', runs: {} },
  },
  loadRuns: vi.fn(),
}));

vi.mock('./stores/cron.js', () => ({
  useCronStore: () => storeMock,
  CRON_BRIDGE_EVENT_NAME: 'cron/job/runStateChanged',
}));

import { CronJobDetail, runStatusColor } from './components/cron/CronJobDetail.js';

describe('runStatusColor mapping', () => {
  it('returns the canonical 7 statuses + unknown fallback', () => {
    expect(runStatusColor('pending').tone).toBe('gray');
    expect(runStatusColor('submitting').tone).toBe('blue');
    expect(runStatusColor('submitted').tone).toBe('blue');
    expect(runStatusColor('running').tone).toBe('purple');
    expect(runStatusColor('finished').tone).toBe('green');
    expect(runStatusColor('failed').tone).toBe('red');
    expect(runStatusColor('observe_lost').tone).toBe('orange');
    expect(runStatusColor('unrecognized').tone).toBe('gray');
    expect(runStatusColor('unrecognized').label).toBe('unrecognized');
    expect(runStatusColor('').label).toBe('未知');
  });
});

describe('CronJobDetail contract', () => {
  it('exports the expected component shape + testid hooks', () => {
    expect(CronJobDetail.name).toBe('CronJobDetail');
    expect(typeof CronJobDetail.setup).toBe('function');
    expect(CronJobDetail.template).toContain('cron-job-detail');
    expect(CronJobDetail.template).toContain('cron-detail-back');
    expect(CronJobDetail.template).toContain('cron-detail-edit');
    expect(CronJobDetail.template).toContain('cron-detail-refresh-runs');
    expect(CronJobDetail.template).toContain('cron-detail-runs-list');
    expect(CronJobDetail.template).toContain('cron-run-observe-lost-hint');
  });

  it('looks up the job from store by id and exposes runs/loading helpers', () => {
    storeMock.state.jobs = [
      { id: 'j1', name: 'demo', schedule_expr: '0 9 * * *', timezone: 'UTC' },
    ];
    storeMock.state.runsByJob = { j1: [{ id: 'r1', status: 'finished' }] };
    storeMock.state.loading = { list: false, runs: { j1: false } };
    storeMock.state.error = { list: '', runs: { j1: '' } };
    const vm = CronJobDetail.setup({ jobId: 'j1' }, { emit: vi.fn() });
    expect(vm.job.value).toEqual(storeMock.state.jobs[0]);
    expect(vm.runs.value).toEqual([{ id: 'r1', status: 'finished' }]);
    expect(vm.loadingRuns.value).toBe(false);
    expect(vm.runsError.value).toBe('');
  });

  it('returns null job when id is unknown', () => {
    storeMock.state.jobs = [{ id: 'j1' }];
    const vm = CronJobDetail.setup({ jobId: 'missing' }, { emit: vi.fn() });
    expect(vm.job.value).toBeNull();
  });

  it('refreshRuns calls store.loadRuns with the prop id', async () => {
    storeMock.loadRuns.mockReset();
    const vm = CronJobDetail.setup({ jobId: 'j1' }, { emit: vi.fn() });
    await vm.refreshRuns();
    expect(storeMock.loadRuns).toHaveBeenCalledWith('j1');
  });

  it('emits edit with the job payload, back without payload', () => {
    storeMock.state.jobs = [{ id: 'j1', name: 'demo' }];
    const emit = vi.fn();
    const vm = CronJobDetail.setup({ jobId: 'j1' }, { emit });
    vm.onEdit();
    expect(emit).toHaveBeenCalledWith('edit', storeMock.state.jobs[0]);
    vm.onBack();
    expect(emit).toHaveBeenCalledWith('back');
  });
});
