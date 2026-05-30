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

describe('useDagDetail status events', () => {
  beforeEach(() => {
    apiMock.callAPI.mockReset();
    logMock.logWarn.mockReset();
  });

  it('applies real statusChanged payloads only for the current dag', async () => {
    apiMock.callAPI.mockImplementation(async (method, params = {}) => {
      if (method === 'dashboard/dagDetail') return { dag: { dag_key: 'dag-1', title: 'Daily Brief' }, nodes: [] };
      if (method === 'dashboard/dagRuns') return { runs: [{ id: 11, run_key: 'run-1', status: 'running' }] };
      if (method === 'dashboard/dagRun') {
        return {
          run: { id: 11, run_key: params.runKey, status: 'running' },
          nodes: [{ node_key: 'report', status: 'running' }],
        };
      }
      throw new Error(`unexpected method ${method}`);
    });

    const detail = useDagDetail();
    await detail.open({ dag_key: 'dag-1' });

    detail.handleStatusEvent({ dag_key: 'other-dag', run_key: 'run-1', node_key: 'report', new_status: 'failed' });
    expect(detail.state.nodes[0].status).toBe('running');

    detail.handleStatusEvent({ dag_key: 'dag-1', run_key: 'run-1', node_key: 'report', new_status: 'done' });
    expect(detail.state.nodes[0].status).toBe('done');
  });

  it('ignores same-DAG node status events from other runs', async () => {
    apiMock.callAPI.mockImplementation(async (method, params = {}) => {
      if (method === 'dashboard/dagDetail') return { dag: { dag_key: 'dag-1' }, nodes: [] };
      if (method === 'dashboard/dagRuns' && !params.status) {
        return { runs: [{ id: 11, run_key: 'run-1', status: 'running' }, { id: 12, run_key: 'run-2', status: 'running' }] };
      }
      if (method === 'dashboard/dagRuns' && params.status === 'running') return { runs: [{ id: 11, run_key: 'run-1', status: 'running' }] };
      if (method === 'dashboard/dagRun') {
        return {
          run: { id: 11, run_key: params.runKey, status: 'running' },
          nodes: [{ node_key: 'report', status: 'running' }],
        };
      }
      throw new Error(`unexpected method ${method}`);
    });

    const detail = useDagDetail();
    await detail.open({ dag_key: 'dag-1' });

    detail.handleStatusEvent({ dag_key: 'dag-1', run_key: 'run-2', node_key: 'report', new_status: 'failed' });
    expect(detail.state.nodes[0].status).toBe('running');

    detail.handleStatusEvent({ dag_key: 'dag-1', run_id: 11, node_key: 'report', new_status: 'done' });
    expect(detail.state.nodes[0].status).toBe('done');
  });
});
