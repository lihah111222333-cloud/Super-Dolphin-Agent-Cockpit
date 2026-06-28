// @ts-nocheck
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { reactive } from '../lib/vue.esm-browser.prod.js';

const detailMock = vi.hoisted(() => ({
  state: {
    loading: false,
    error: null,
    runsError: null,
    show: false,
    dag: null,
    nodes: [],
    runs: [],
    activeRun: null,
    run: null,
    selectedRunKey: '',
    finalOutput: null,
    starting: false,
    startError: null,
    terminating: false,
    terminateError: null,
    terminateWarning: null,
    deleting: false,
    deleteError: null,
    scheduling: false,
    scheduleError: null,
    savingNodeKey: '',
    saveError: null,
  },
  open: vi.fn(),
  start: vi.fn(),
  terminateActiveRun: vi.fn(),
  deleteDAG: vi.fn(),
  selectRun: vi.fn(),
  saveAgentNode: vi.fn(),
  handleStatusEvent: vi.fn(),
  setSchedule: vi.fn(),
  setScheduleEnabled: vi.fn(),
}));

vi.mock('./composables/useDagDetail.js', () => ({
  useDagDetail: () => detailMock,
}));

import { DagsPage } from './pages/DagsPage.js';

function resetDetailMockState() {
  Object.assign(detailMock.state, {
    loading: false,
    error: null,
    runsError: null,
    show: false,
    dag: null,
    nodes: [],
    runs: [],
    activeRun: null,
    run: null,
    selectedRunKey: '',
    finalOutput: null,
    starting: false,
    startError: null,
    terminating: false,
    terminateError: null,
    terminateWarning: null,
    deleting: false,
    deleteError: null,
    scheduling: false,
    scheduleError: null,
    savingNodeKey: '',
    saveError: null,
  });
  detailMock.open.mockReset();
  detailMock.start.mockReset();
  detailMock.terminateActiveRun.mockReset();
  detailMock.deleteDAG.mockReset();
  detailMock.selectRun.mockReset();
  detailMock.saveAgentNode.mockReset();
  detailMock.handleStatusEvent.mockReset();
  detailMock.setSchedule.mockReset();
  detailMock.setScheduleEnabled.mockReset();
}

describe('DagsPage schedule action', () => {
  beforeEach(() => {
    resetDetailMockState();
  });

  it('lets a reusable manual history DAG be scheduled', async () => {
    const props = reactive({
      items: [{
        dag_key: 'dag-a',
        title: 'Dag A',
        status: 'ready',
        trigger: 'manual',
        latest_run: { run_key: 'run-done', status: 'succeeded' },
      }],
    });
    detailMock.state.dag = { ...props.items[0], version: 7 };
    detailMock.setSchedule.mockResolvedValueOnce({ ok: true });
    const emit = vi.fn();

    const vm = DagsPage.setup(props, { emit });
    vm.openScheduleModal();
    vm.schedulePreset.value = 'daily';
    vm.scheduleTime.value = '08:00';
    await vm.confirmScheduleSelectedDag();

    expect(vm.scheduleActionVisible.value).toBe(true);
    expect(vm.scheduleDisabledReason.value).toBe('');
    expect(vm.scheduleActionLabel.value).toBe('创建定时任务');
    expect(detailMock.setSchedule).toHaveBeenCalledWith({ cronExpr: '0 8 * * *' });
    expect(vm.scheduleConfirmOpen.value).toBe(false);
    expect(emit).toHaveBeenCalledWith('refresh-dags');
    expect(DagsPage.template).toContain('data-testid="dag-schedule-button"');
    expect(DagsPage.components.DagScheduleModal.template).toContain('data-testid="dag-schedule-preset"');
    expect(DagsPage.components.DagScheduleModal.template).toContain('data-testid="dag-schedule-time-input"');
    expect(DagsPage.components.DagScheduleModal.template).toContain('<option value="weekly">每周</option>');
    expect(DagsPage.components.DagScheduleModal.template).toContain('<option value="monthly">每月</option>');
    expect(DagsPage.components.DagScheduleModal.template).not.toContain('<option value="weekly">每周一</option>');
    expect(DagsPage.components.DagScheduleModal.template).not.toContain('<option value="monthly">每月 1 日</option>');
    expect(DagsPage.components.DagScheduleModal.template).toContain('data-testid="dag-schedule-weekday"');
    expect(DagsPage.components.DagScheduleModal.template).toContain('data-testid="dag-schedule-month-day"');
  });

  it('converts user-friendly weekdays schedule settings into cron', async () => {
    const props = reactive({
      items: [{
        dag_key: 'dag-a',
        title: 'Dag A',
        status: 'ready',
        trigger: 'manual',
        latest_run: { run_key: 'run-done', status: 'succeeded' },
      }],
    });
    detailMock.state.dag = { ...props.items[0], version: 7 };
    detailMock.setSchedule.mockResolvedValueOnce({ ok: true });
    const emit = vi.fn();

    const vm = DagsPage.setup(props, { emit });
    vm.openScheduleModal();
    vm.updateSchedulePreset('weekdays');
    vm.updateScheduleTime('09:30');
    await vm.confirmScheduleSelectedDag();

    expect(vm.schedulePreviewText.value).toBe('工作日 09:30 自动运行');
    expect(detailMock.setSchedule).toHaveBeenCalledWith({ cronExpr: '30 9 * * 1-5' });
  });

  it('lets an existing scheduled task edit its run plan', async () => {
    const props = reactive({
      items: [{
        dag_key: 'dag-a',
        title: 'Dag A',
        status: 'ready',
        trigger: 'scheduled',
        cron_expr: '30 9 * * 3',
        latest_run: { run_key: 'run-done', status: 'succeeded' },
      }],
    });
    detailMock.state.dag = { ...props.items[0], version: 7 };
    detailMock.setSchedule.mockResolvedValueOnce({ ok: true });
    const emit = vi.fn();

    const vm = DagsPage.setup(props, { emit });
    vm.openScheduleModal();
    expect(vm.scheduleActionVisible.value).toBe(true);
    expect(vm.scheduleActionLabel.value).toBe('修改计划');
    expect(vm.schedulePreset.value).toBe('weekly');
    expect(vm.scheduleWeekday.value).toBe('3');
    expect(vm.scheduleTime.value).toBe('09:30');

    vm.updateScheduleWeekday('5');
    await vm.confirmScheduleSelectedDag();

    expect(vm.schedulePreviewText.value).toBe('每周五 09:30 自动运行');
    expect(detailMock.setSchedule).toHaveBeenCalledWith({ cronExpr: '30 9 * * 5' });
    expect(emit).toHaveBeenCalledWith('refresh-dags');
  });

  it('presents scheduled tasks with product labels instead of DAG internals', () => {
    const props = reactive({
      items: [{
        dag_key: 'scheduled-review',
        title: 'Provider 自注册原生工具 - 代码审查',
        status: 'ready',
        trigger: 'scheduled',
        cron_expr: '0 8 * * *',
        next_run_at: '2026-05-27T02:00:00Z',
        latest_run: null,
      }],
    });

    const vm = DagsPage.setup(props, { emit: vi.fn() });

    expect(vm.rows.value[0]).toMatchObject({
      status: '已启用',
      triggerLabel: '每天 08:00',
      latestRunLabel: '未运行',
    });
    expect(DagsPage.template).toContain('运行计划');
    expect(DagsPage.template).toContain('执行步骤');
    expect(DagsPage.template).toContain('scheduleActionLabel');
    expect(DagsPage.template).not.toContain('<dt>流程状态</dt>');
    expect(DagsPage.template).not.toContain('<dt>触发</dt>');
  });

  it('uses next_run_at as the real scheduled task enablement state and toggles it', async () => {
    const props = reactive({
      items: [{
        dag_key: 'scheduled-review',
        title: 'Provider 自注册原生工具 - 代码审查',
        status: 'ready',
        trigger: 'scheduled',
        cron_expr: '0 8 * * *',
        next_run_at: null,
        latest_run: null,
      }],
    });
    detailMock.state.dag = { ...props.items[0], version: 7 };
    detailMock.setScheduleEnabled.mockResolvedValueOnce({ ok: true });
    const emit = vi.fn();

    const vm = DagsPage.setup(props, { emit });

    expect(vm.rows.value[0].status).toBe('已暂停');
    expect(vm.scheduleToggleVisible.value).toBe(true);
    expect(vm.scheduleToggleLabel.value).toBe('启用自动运行');
    await vm.toggleScheduleEnabled();

    expect(detailMock.setScheduleEnabled).toHaveBeenCalledWith({ enabled: true });
    expect(emit).toHaveBeenCalledWith('refresh-dags');
    expect(DagsPage.template).toContain('data-testid="dag-schedule-toggle-button"');
  });
});
