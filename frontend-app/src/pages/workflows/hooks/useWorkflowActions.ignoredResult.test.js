import { describe, expect, it, vi } from 'vitest';
import {
  dispatchDagNodeAction,
  saveScheduleAction,
  stopSelectedDagAction,
} from './useWorkflowActions.js';

function workflowActionContext(overrides = {}) {
  const actionState = {
    scheduleCron: '',
    setActioning: vi.fn(),
    setDispatchingNodeKey: vi.fn(),
    setError: vi.fn(),
    setScheduleOpen: vi.fn(),
  };
  const derived = {
    activeDetailDag: null,
    activeRunKey: 'run-7',
    baseVersion: 7,
    dagKey: 'daily-brief',
    missingRootAssigneeWarning: '',
    runId: 42,
  };
  const list = { refreshDags: vi.fn().mockResolvedValue([]) };
  const notices = { clearNotice: vi.fn(), showTaskNotice: vi.fn() };
  const refresh = { refreshDetail: vi.fn().mockResolvedValue(undefined) };
  return { actionState, derived, list, notices, refresh, refreshContext: { list, refresh }, ...overrides };
}

describe('workflow ignored-result actions', () => {
  it('ignores malformed apply-ops body and publishes schedule success', async () => {
    const facade = { applyDagOps: vi.fn().mockResolvedValue({ malformed: 'apply-ops-sentinel' }) };
    const ctx = workflowActionContext({ facade });

    const result = await saveScheduleAction(ctx, 'CRON_TZ=Asia/Shanghai 0 8 * * *');

    expect(result).toBeUndefined();
    expect(facade.applyDagOps).toHaveBeenCalledWith({
      baseVersion: 7,
      dagKey: 'daily-brief',
      ops: [{ op: 'update_dag', patch: { cron_expr: 'CRON_TZ=Asia/Shanghai 0 8 * * *', trigger: 'scheduled' } }],
    });
    expect(ctx.actionState.setScheduleOpen).toHaveBeenCalledWith(false);
    expect(ctx.list.refreshDags).toHaveBeenCalledWith();
    expect(ctx.refresh.refreshDetail).toHaveBeenCalledWith('daily-brief', '');
    expect(ctx.notices.showTaskNotice).toHaveBeenLastCalledWith('已保存定时任务', 'daily-brief');
    expect(ctx.actionState.setScheduleOpen.mock.invocationCallOrder[0]).toBeLessThan(ctx.notices.showTaskNotice.mock.invocationCallOrder[0]);
    expect(ctx.actionState.setActioning).toHaveBeenNthCalledWith(1, 'schedule');
    expect(ctx.actionState.setActioning).toHaveBeenLastCalledWith('');
  });

  it('ignores malformed dispatch body and publishes dispatch success', async () => {
    const facade = { dispatchDagNode: vi.fn().mockResolvedValue(['dispatch-malformed-sentinel']) };
    const ctx = workflowActionContext({ facade });
    const node = { nodeKey: 'review', title: '人工复核' };

    const result = await dispatchDagNodeAction(ctx, node, 'agent-reviewer');

    expect(result).toBeUndefined();
    expect(facade.dispatchDagNode).toHaveBeenCalledWith({
      assignedTo: 'agent-reviewer',
      dagKey: 'daily-brief',
      nodeKey: 'review',
      runId: 42,
    });
    expect(ctx.list.refreshDags).toHaveBeenCalledWith();
    expect(ctx.refresh.refreshDetail).toHaveBeenCalledWith('daily-brief', '');
    expect(ctx.notices.showTaskNotice).toHaveBeenLastCalledWith('已派发步骤 人工复核', 'daily-brief');
    expect(ctx.refresh.refreshDetail.mock.invocationCallOrder[0]).toBeLessThan(ctx.notices.showTaskNotice.mock.invocationCallOrder[0]);
    expect(ctx.actionState.setDispatchingNodeKey).toHaveBeenNthCalledWith(1, 'review');
    expect(ctx.actionState.setDispatchingNodeKey).toHaveBeenLastCalledWith('');
  });

  it('ignores malformed terminate body and publishes stop success', async () => {
    const facade = { terminateDagRun: vi.fn().mockResolvedValue('terminate-malformed-sentinel') };
    const ctx = workflowActionContext({ facade });

    const result = await stopSelectedDagAction(ctx);

    expect(result).toBeUndefined();
    expect(facade.terminateDagRun).toHaveBeenCalledWith({
      dagKey: 'daily-brief',
      reason: 'user_requested',
      runKey: 'run-7',
    });
    expect(ctx.list.refreshDags).toHaveBeenCalledWith();
    expect(ctx.refresh.refreshDetail).toHaveBeenCalledWith('daily-brief', '');
    expect(ctx.notices.showTaskNotice).toHaveBeenLastCalledWith('已停止运行', 'daily-brief');
    expect(ctx.refresh.refreshDetail.mock.invocationCallOrder[0]).toBeLessThan(ctx.notices.showTaskNotice.mock.invocationCallOrder[0]);
    expect(ctx.actionState.setActioning).toHaveBeenNthCalledWith(1, 'stop');
    expect(ctx.actionState.setActioning).toHaveBeenLastCalledWith('');
  });
});
