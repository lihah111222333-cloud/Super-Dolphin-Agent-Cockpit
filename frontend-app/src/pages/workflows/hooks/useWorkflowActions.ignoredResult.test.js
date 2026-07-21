import { describe, expect, it, vi } from 'vitest';
import { RPC_METHODS } from '../../../shared/api/backendApi.js';
import { createBackendResponseValidators } from '../../../shared/api/backendResponseValidators.js';
import { stopSelectedDagAction } from './useWorkflowLifecycleActions.js';
import { dispatchDagNodeAction } from './useWorkflowNodeActions.js';
import { saveScheduleAction } from './useWorkflowScheduleActions.js';

const validators = createBackendResponseValidators(RPC_METHODS);

function validatedFacade(method, name, response) {
  const validator = validators[method];
  return {
    [name]: vi.fn((request) => Promise.resolve().then(() => validator(method, response, request))),
  };
}

function dispatchNode(overrides = {}) {
  return {
    id: 7,
    dag_key: 'daily-brief',
    node_key: 'review',
    title: '人工复核',
    status: 'ready',
    assigned_to: 'agent-reviewer',
    created_at: '2026-07-21T00:00:00Z',
    updated_at: '2026-07-21T00:00:00Z',
    ...overrides,
  };
}

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

describe('workflow mutation response failures', () => {
  it('rejects malformed apply-ops before closing, refreshing, or publishing success', async () => {
    const facade = validatedFacade(RPC_METHODS.DASHBOARD_DAG_APPLY_OPS, 'applyDagOps', { newVersion: 7 });
    const ctx = workflowActionContext({ facade });

    await expect(
      saveScheduleAction(ctx, 'CRON_TZ=Asia/Shanghai 0 8 * * *'),
    ).rejects.toThrow('greater than request.baseVersion');

    expect(facade.applyDagOps).toHaveBeenCalledWith({
      baseVersion: 7,
      dagKey: 'daily-brief',
      ops: [{ op: 'update_dag', patch: { cron_expr: 'CRON_TZ=Asia/Shanghai 0 8 * * *', trigger: 'scheduled' } }],
    });
    expect(ctx.actionState.setScheduleOpen).not.toHaveBeenCalled();
    expect(ctx.list.refreshDags).not.toHaveBeenCalled();
    expect(ctx.refresh.refreshDetail).not.toHaveBeenCalled();
    expect(ctx.notices.showTaskNotice).not.toHaveBeenCalled();
    expect(ctx.actionState.setActioning).toHaveBeenNthCalledWith(1, 'schedule');
    expect(ctx.actionState.setActioning).toHaveBeenLastCalledWith('');
  });

  it.each([
    [
      'wrong node',
      { node: dispatchNode({ node_key: 'other' }), wakeup_id: 9, enqueued: true },
      'must match request.nodeKey',
    ],
    [
      'wrong assignee',
      { node: dispatchNode({ assigned_to: 'other' }), wakeup_id: 9, enqueued: true },
      'must match request.assignedTo',
    ],
    ['missing wakeup', { node: dispatchNode(), enqueued: true }, 'wakeup_id must be an integer'],
    ['non-positive wakeup', { node: dispatchNode(), wakeup_id: 0, enqueued: true }, 'must be a positive integer'],
  ])('rejects %s dispatch before refreshing or publishing success', async (_caseName, response, message) => {
    const facade = validatedFacade(RPC_METHODS.DASHBOARD_DAG_DISPATCH_NODE, 'dispatchDagNode', response);
    const ctx = workflowActionContext({ facade });
    const node = { nodeKey: 'review', title: '人工复核' };

    await expect(dispatchDagNodeAction(ctx, node, 'agent-reviewer')).rejects.toThrow(message);

    expect(facade.dispatchDagNode).toHaveBeenCalledWith({
      assignedTo: 'agent-reviewer',
      dagKey: 'daily-brief',
      nodeKey: 'review',
      runId: 42,
    });
    expect(ctx.list.refreshDags).not.toHaveBeenCalled();
    expect(ctx.refresh.refreshDetail).not.toHaveBeenCalled();
    expect(ctx.notices.showTaskNotice).not.toHaveBeenCalled();
    expect(ctx.actionState.setDispatchingNodeKey).toHaveBeenNthCalledWith(1, 'review');
    expect(ctx.actionState.setDispatchingNodeKey).toHaveBeenLastCalledWith('');
  });

  it('rejects malformed terminate before refreshing or publishing success', async () => {
    const facade = validatedFacade(RPC_METHODS.DASHBOARD_DAG_TERMINATE, 'terminateDagRun', 'malformed');
    const ctx = workflowActionContext({ facade });

    await expect(stopSelectedDagAction(ctx)).rejects.toThrow('must be an object');

    expect(facade.terminateDagRun).toHaveBeenCalledWith({
      dagKey: 'daily-brief',
      reason: 'user_requested',
      runKey: 'run-7',
    });
    expect(ctx.list.refreshDags).not.toHaveBeenCalled();
    expect(ctx.refresh.refreshDetail).not.toHaveBeenCalled();
    expect(ctx.notices.showTaskNotice).not.toHaveBeenCalled();
    expect(ctx.actionState.setActioning).toHaveBeenNthCalledWith(1, 'stop');
    expect(ctx.actionState.setActioning).toHaveBeenLastCalledWith('');
  });
});
