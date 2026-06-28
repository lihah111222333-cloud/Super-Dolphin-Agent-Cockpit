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

describe('useDagDetail schedule updates', () => {
  beforeEach(() => {
    apiMock.callAPI.mockReset();
    logMock.logWarn.mockReset();
  });

  it('sets a manual DAG schedule through dashboard apply ops and refreshes detail', async () => {
    const applyOpsCalls = [];
    apiMock.callAPI.mockImplementation(async (method, params) => {
      if (method === 'dashboard/dagDetail') {
        return {
          dag: { dag_key: 'dag-1', title: 'Daily Brief', version: params.dagKey === 'dag-1' ? 7 : 0 },
          nodes: [],
        };
      }
      if (method === 'dashboard/dagRuns') return { runs: [] };
      if (method === 'dashboard/dagApplyOps') {
        applyOpsCalls.push(params);
        return { newVersion: 8 };
      }
      throw new Error(`unexpected method ${method}`);
    });

    const detail = useDagDetail();
    await detail.open({ dag_key: 'dag-1' });
    const result = await detail.setSchedule({ cronExpr: ' 0 8 * * * ' });

    expect(result).toEqual({ ok: true });
    expect(detail.state.scheduleError).toBeNull();
    expect(detail.state.scheduling).toBe(false);
    expect(applyOpsCalls).toHaveLength(1);
    expect(applyOpsCalls[0]).toEqual({
      dagKey: 'dag-1',
      baseVersion: 7,
      ops: [{
        op: 'update_dag',
        patch: {
          trigger: 'scheduled',
          cron_expr: '0 8 * * *',
        },
      }],
    });
    expect(apiMock.callAPI.mock.calls.filter(([method]) => method === 'dashboard/dagDetail')).toHaveLength(2);
  });
});
