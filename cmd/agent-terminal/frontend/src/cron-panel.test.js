// @ts-nocheck
import { describe, expect, it, vi } from 'vitest';

vi.mock('./services/log.js', () => ({
  logDebug: vi.fn(),
  logInfo: vi.fn(),
  logWarn: vi.fn(),
  logError: vi.fn(),
  registerLogBridgeSink: vi.fn(),
}));

vi.mock('./services/api.js', () => ({
  selectProjectDir: vi.fn().mockResolvedValue(''),
  onBridgeEvent: vi.fn(() => () => {}),
}));

const cronApiMock = vi.hoisted(() => ({
  mapCronRpcError: vi.fn((err) => ({
    code: 0,
    kind: 'unknown',
    message: (err && err.message) || String(err || ''),
  })),
}));

vi.mock('./services/cron-api.js', () => cronApiMock);

const storeMock = vi.hoisted(() => ({
  state: {
    jobs: [],
    runsByJob: {},
    loading: { list: false, runs: {} },
    error: { list: '', runs: {} },
  },
  loadJobs: vi.fn(),
  setJobEnabled: vi.fn(),
  deleteJob: vi.fn(),
  attachBridge: vi.fn(),
  detachBridge: vi.fn(),
}));

vi.mock('./stores/cron.js', () => ({
  useCronStore: () => storeMock,
  CRON_BRIDGE_EVENT_NAME: 'cron/job/runStateChanged',
}));

import { CronPanel } from './pages/CronPanel.js';
import { TasksPage } from './pages/TasksPage.js';

describe('CronPanel contract', () => {
  it('exports the panel name + testid hooks', () => {
    expect(CronPanel.name).toBe('CronPanel');
    expect(typeof CronPanel.setup).toBe('function');
    expect(CronPanel.template).toContain('cron-panel');
    expect(CronPanel.template).toContain('cron-empty-state');
    expect(CronPanel.template).toContain('cron-list');
    expect(CronPanel.template).toContain('cron-refresh-button');
    expect(CronPanel.template).toContain('cron-new-button');
    expect(CronPanel.template).toContain("'cron-view-' + idx");
    expect(CronPanel.template).toContain("'cron-runonce-' + idx");
    expect(CronPanel.template).toContain("'cron-edit-' + idx");
    expect(CronPanel.template).toContain('<CronJobDetail');
    expect(CronPanel.components).toHaveProperty('CronJobForm');
    expect(CronPanel.components).toHaveProperty('CronJobDetail');
  });

  it('exposes view + form + detail action helpers from setup()', () => {
    const vm = CronPanel.setup();
    expect(vm.view.value).toBe('list');
    expect(vm.editingJob.value).toBeNull();
    expect(vm.viewingJobId.value).toBe('');

    vm.openCreate();
    expect(vm.view.value).toBe('form');
    expect(vm.editingJob.value).toBeNull();

    vm.closeForm();
    expect(vm.view.value).toBe('list');

    vm.openEdit({ id: 'j1', name: 'demo' });
    expect(vm.view.value).toBe('form');
    expect(vm.editingJob.value).toEqual({ id: 'j1', name: 'demo' });

    vm.openDetail({ id: 'j2' });
    expect(vm.view.value).toBe('detail');
    expect(vm.viewingJobId.value).toBe('j2');

    vm.editFromDetail({ id: 'j2', name: 'two' });
    expect(vm.view.value).toBe('form');
    expect(vm.editingJob.value).toEqual({ id: 'j2', name: 'two' });

    vm.backToList();
    expect(vm.view.value).toBe('list');
    expect(vm.viewingJobId.value).toBe('');
    expect(vm.editingJob.value).toBeNull();
  });

  it('formatSchedule / formatRetryBudget / formatLastRun handle empty + populated jobs', () => {
    const vm = CronPanel.setup();
    expect(vm.formatSchedule({})).toBe('-');
    expect(vm.formatSchedule({ schedule_expr: '0 9 * * *', timezone: 'Asia/Seoul' }))
      .toBe('0 9 * * * (Asia/Seoul)');
    expect(vm.formatSchedule({ schedule_expr: '*/5 * * * *' })).toBe('*/5 * * * *');

    expect(vm.formatRetryBudget({ max_attempts: 0 })).toBe('不重试');
    expect(vm.formatRetryBudget({ max_attempts: 3, failure_count: 1 })).toBe('1 / 3');

    expect(vm.formatLastRun({})).toBe('从未运行');
    expect(vm.formatLastRun({ last_status: 'finished' })).toBe('finished');
    expect(vm.formatLastRun({ last_status: 'failed', last_run_at: '2026-04-28T10:00:00Z' }))
      .toBe('failed · 2026-04-28T10:00:00Z');
  });
});

describe('TasksPage cron sub-tab integration', () => {
  it('registers CronPanel and exposes the cron sub-tab control', () => {
    expect(TasksPage.components).toBeTruthy();
    expect(TasksPage.components.CronPanel).toBe(CronPanel);
    expect(TasksPage.template).toContain('tasks-subtab-cron');
    expect(TasksPage.template).toContain("tasksSubTab === 'cron'");
    expect(TasksPage.template).toContain('<CronPanel');
  });
});
