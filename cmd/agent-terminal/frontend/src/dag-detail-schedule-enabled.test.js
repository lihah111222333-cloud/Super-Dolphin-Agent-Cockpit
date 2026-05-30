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

describe('useDagDetail scheduled enablement', () => {
  beforeEach(() => {
    apiMock.callAPI.mockReset();
    logMock.logWarn.mockReset();
  });

  it('toggles scheduled DAG enablement through apply ops without changing cron frequency', async () => {
    const applyOpsCalls = [];
    apiMock.callAPI.mockImplementation(async (method, params) => {
      if (method === 'dashboard/dagDetail') {
        return {
          dag: {
            dag_key: 'dag-1',
            title: 'Daily Brief',
            version: 7,
            trigger: 'scheduled',
            cron_expr: '0 8 * * *',
            next_run_at: null,
          },
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
    const result = await detail.setScheduleEnabled({ enabled: true });

    expect(result).toEqual({ ok: true });
    expect(detail.state.scheduleError).toBeNull();
    expect(applyOpsCalls).toEqual([{
      dagKey: 'dag-1',
      baseVersion: 7,
      ops: [{
        op: 'update_dag',
        patch: { schedule_enabled: true },
      }],
    }]);
  });
});
