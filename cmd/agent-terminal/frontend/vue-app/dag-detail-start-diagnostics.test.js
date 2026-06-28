// @ts-nocheck
import { beforeEach, describe, expect, it, vi } from 'vitest';

const apiMock = vi.hoisted(() => ({
  callAPI: vi.fn(),
}));
const logMock = vi.hoisted(() => ({
  logWarn: vi.fn(),
}));

vi.mock('./services/api.js', () => ({
  callAPI: apiMock.callAPI,
}));
vi.mock('./services/log.js', () => ({
  logWarn: logMock.logWarn,
}));

import { useDagDetail } from './composables/useDagDetail.js';

describe('useDagDetail start diagnostics', () => {
  beforeEach(() => {
    apiMock.callAPI.mockReset();
    logMock.logWarn.mockReset();
  });

  it('surfaces start diagnostics when a run is waiting for node dispatch', async () => {
    apiMock.callAPI.mockImplementation(async (method, params) => {
      if (method === 'dashboard/dagDetail') {
        return { dag: { dag_key: 'dag-1', title: 'Daily Brief' }, nodes: [] };
      }
      if (method === 'dashboard/dagRuns') return { runs: [] };
      if (method === 'dashboard/dagStart') {
        expect(params).toMatchObject({ dagKey: 'dag-1', triggerSource: 'manual' });
        return {
          runKey: 'run-waiting',
          runId: 55,
          readyRootNodes: 1,
          scheduledWakeups: 0,
          executionState: 'waiting_for_assignee',
          warning: '已启动，但根节点缺少执行代理；请使用 task_dispatch_node 指派。',
        };
      }
      throw new Error(`unexpected method ${method}`);
    });

    const detail = useDagDetail();
    await detail.open({ dag_key: 'dag-1' });
    await detail.start();

    expect(detail.state.startError).toBeNull();
    expect(detail.state.startExecutionState).toBe('waiting_for_assignee');
    expect(detail.state.startWarning).toContain('task_dispatch_node');
  });
});
