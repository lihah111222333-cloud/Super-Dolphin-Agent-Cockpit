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
import { DagDetailModal } from './components/DagDetailModal.js';

describe('useDagDetail', () => {
  beforeEach(() => {
    apiMock.callAPI.mockReset();
    logMock.logWarn.mockReset();
  });

  it('loads dag detail, recent runs, and exposes run final_output', async () => {
    apiMock.callAPI.mockImplementation(async (method, params) => {
      if (method === 'dashboard/dagDetail') {
        expect(params).toEqual({ dagKey: 'dag-1' });
        return {
          dag: { dag_key: 'dag-1', title: 'Daily Brief' },
          nodes: [{ node_key: 'report', title: 'Report' }],
        };
      }
      if (method === 'dashboard/dagRuns') {
        expect(params).toEqual({ dagKey: 'dag-1', limit: 5 });
        return {
          runs: [{
            run_key: 'run-1',
            dag_key: 'dag-1',
            status: 'succeeded',
            metadata: {
              final_output: {
                kind: 'file',
                role: 'final_output',
                path: 'reports/daily-brief.pptx',
                source_node_key: 'report',
              },
            },
          }],
        };
      }
      throw new Error(`unexpected method ${method}`);
    });

    const detail = useDagDetail();
    await detail.open({ dag_key: 'dag-1' });

    expect(detail.state.show).toBe(true);
    expect(detail.state.loading).toBe(false);
    expect(detail.state.dag.title).toBe('Daily Brief');
    expect(detail.state.nodes).toHaveLength(1);
    expect(detail.state.run.run_key).toBe('run-1');
    expect(detail.state.finalOutput).toEqual({
      kind: 'file',
      role: 'final_output',
      path: 'reports/daily-brief.pptx',
      source_node_key: 'report',
    });

    detail.handleStatusEvent({ node_key: 'report', status: 'done' });
    expect(detail.state.nodes[0].status).toBe('done');
  });

  it('keeps dag detail visible when run loading fails', async () => {
    const runsError = new Error('runs unavailable');
    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'dashboard/dagDetail') {
        return { dag: { dag_key: 'dag-1', title: 'Daily Brief' }, nodes: [] };
      }
      if (method === 'dashboard/dagRuns') {
        throw runsError;
      }
      throw new Error(`unexpected method ${method}`);
    });

    const detail = useDagDetail();
    await detail.open({ dag_key: 'dag-1' });

    expect(detail.state.error).toBeNull();
    expect(detail.state.dag.title).toBe('Daily Brief');
    expect(detail.state.runsError).toBe(runsError);
    expect(detail.state.runs).toEqual([]);
    expect(detail.state.finalOutput).toBeNull();
  });

  it('selects a run and exposes that run final_output', async () => {
    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'dashboard/dagDetail') {
        return { dag: { dag_key: 'dag-1', title: 'Daily Brief' }, nodes: [] };
      }
      if (method === 'dashboard/dagRuns') {
        return {
          runs: [
            {
              run_key: 'run-new',
              metadata: { final_output: { kind: 'text', text: 'new result' } },
            },
            {
              run_key: 'run-old',
              metadata: { final_output: { kind: 'text', text: 'old result' } },
            },
          ],
        };
      }
      throw new Error(`unexpected method ${method}`);
    });

    const detail = useDagDetail();
    await detail.open({ dag_key: 'dag-1' });

    expect(detail.state.selectedRunKey).toBe('run-new');
    detail.selectRun('run-old');

    expect(detail.state.selectedRunKey).toBe('run-old');
    expect(detail.state.run.run_key).toBe('run-old');
    expect(detail.state.finalOutput).toEqual({ kind: 'text', text: 'old result' });
  });

  it('starts a dag, refreshes detail and runs, and selects the returned run', async () => {
    apiMock.callAPI.mockImplementation(async (method, params) => {
      if (method === 'dashboard/dagDetail') {
        expect(params).toEqual({ dagKey: 'dag-1' });
        return { dag: { dag_key: 'dag-1', title: 'Daily Brief' }, nodes: [] };
      }
      if (method === 'dashboard/dagRuns') {
        return {
          runs: [
            {
              run_key: 'run-existing',
              metadata: { final_output: { kind: 'text', text: 'existing result' } },
            },
            {
              run_key: 'run-started',
              metadata: { final_output: { kind: 'text', text: 'started result' } },
            },
          ],
        };
      }
      if (method === 'dashboard/dagStart') {
        expect(params).toMatchObject({
          dagKey: 'dag-1',
          triggerSource: 'manual',
        });
        expect(params.idempotencyKey).toEqual(expect.any(String));
        return { runKey: 'run-started' };
      }
      throw new Error(`unexpected method ${method}`);
    });

    const detail = useDagDetail();
    await detail.open({ dag_key: 'dag-1' });
    await detail.start();

    expect(detail.state.starting).toBe(false);
    expect(detail.state.startError).toBeNull();
    expect(detail.state.selectedRunKey).toBe('run-started');
    expect(detail.state.run.run_key).toBe('run-started');
    expect(detail.state.finalOutput).toEqual({ kind: 'text', text: 'started result' });
  });

  it('ignores duplicate start calls while a start is already running', async () => {
    let resolveStart;
    const startPromise = new Promise((resolve) => { resolveStart = resolve; });
    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'dashboard/dagDetail') {
        return { dag: { dag_key: 'dag-1', title: 'Daily Brief' }, nodes: [] };
      }
      if (method === 'dashboard/dagRuns') return { runs: [] };
      if (method === 'dashboard/dagStart') return startPromise;
      throw new Error(`unexpected method ${method}`);
    });

    const detail = useDagDetail();
    await detail.open({ dag_key: 'dag-1' });

    const firstStart = detail.start();
    const secondStart = detail.start();

    expect(apiMock.callAPI.mock.calls.filter(([method]) => method === 'dashboard/dagStart')).toHaveLength(1);

    resolveStart({ runKey: 'run-started' });
    await Promise.all([firstStart, secondStart]);
  });

  it('does not expose a start error when post-start detail refresh fails', async () => {
    const refreshError = new Error('detail refresh failed');
    let detailCalls = 0;
    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'dashboard/dagDetail') {
        detailCalls += 1;
        if (detailCalls === 1) return { dag: { dag_key: 'dag-1', title: 'Daily Brief' }, nodes: [] };
        throw refreshError;
      }
      if (method === 'dashboard/dagRuns') return { runs: [] };
      if (method === 'dashboard/dagStart') return { runKey: 'run-started' };
      throw new Error(`unexpected method ${method}`);
    });

    const detail = useDagDetail();
    await detail.open({ dag_key: 'dag-1' });
    await detail.start();

    expect(detail.state.starting).toBe(false);
    expect(detail.state.startError).toBeNull();
    expect(detail.state.error).toBe(refreshError);
  });

  it('clears starting state when closing during an in-flight start', async () => {
    let resolveStart;
    const startPromise = new Promise((resolve) => { resolveStart = resolve; });
    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'dashboard/dagDetail') {
        return { dag: { dag_key: 'dag-1', title: 'Daily Brief' }, nodes: [] };
      }
      if (method === 'dashboard/dagRuns') return { runs: [] };
      if (method === 'dashboard/dagStart') return startPromise;
      throw new Error(`unexpected method ${method}`);
    });

    const detail = useDagDetail();
    await detail.open({ dag_key: 'dag-1' });

    const pendingStart = detail.start();
    expect(detail.state.starting).toBe(true);

    detail.close();
    expect(detail.state.starting).toBe(false);

    resolveStart({ runKey: 'run-started' });
    await pendingStart;
    expect(detail.state.starting).toBe(false);
  });

  it('exposes start failures without replacing the detail error', async () => {
    const startError = new Error('start failed');
    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'dashboard/dagDetail') {
        return { dag: { dag_key: 'dag-1', title: 'Daily Brief' }, nodes: [] };
      }
      if (method === 'dashboard/dagRuns') return { runs: [] };
      if (method === 'dashboard/dagStart') throw startError;
      throw new Error(`unexpected method ${method}`);
    });

    const detail = useDagDetail();
    await detail.open({ dag_key: 'dag-1' });
    await detail.start();

    expect(detail.state.error).toBeNull();
    expect(detail.state.starting).toBe(false);
    expect(detail.state.startError).toBe(startError);
    expect(detail.state.dag.title).toBe('Daily Brief');
  });

  it('normalizes final_output from a JSON metadata string', async () => {
    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'dashboard/dagDetail') {
        return { dag: { dag_key: 'dag-1', title: 'Daily Brief' }, nodes: [] };
      }
      if (method === 'dashboard/dagRuns') {
        return {
          runs: [{
            run_key: 'run-1',
            metadata: '{"final_output":{"kind":"json","result":{"verdict":"pass"}}}',
          }],
        };
      }
      throw new Error(`unexpected method ${method}`);
    });

    const detail = useDagDetail();
    await detail.open({ dag_key: 'dag-1' });

    expect(detail.state.finalOutput).toEqual({
      kind: 'json',
      result: { verdict: 'pass' },
    });
  });

  it('ignores stale detail responses from earlier opens', async () => {
    let resolveFirstDetail;
    let resolveSecondDetail;
    const firstDetail = new Promise((resolve) => { resolveFirstDetail = resolve; });
    const secondDetail = new Promise((resolve) => { resolveSecondDetail = resolve; });

    apiMock.callAPI.mockImplementation((method, params) => {
      if (method === 'dashboard/dagDetail' && params.dagKey === 'dag-a') return firstDetail;
      if (method === 'dashboard/dagDetail' && params.dagKey === 'dag-b') return secondDetail;
      if (method === 'dashboard/dagRuns') return Promise.resolve({ runs: [] });
      throw new Error(`unexpected method ${method}`);
    });

    const detail = useDagDetail();
    const firstOpen = detail.open({ dag_key: 'dag-a', title: 'A' });
    const secondOpen = detail.open({ dag_key: 'dag-b', title: 'B' });

    resolveSecondDetail({ dag: { dag_key: 'dag-b', title: 'B loaded' }, nodes: [] });
    await secondOpen;
    expect(detail.state.dag.title).toBe('B loaded');

    resolveFirstDetail({ dag: { dag_key: 'dag-a', title: 'A stale' }, nodes: [] });
    await firstOpen;
    expect(detail.state.dag.title).toBe('B loaded');
  });

  it('applies real statusChanged payloads only for the current dag', async () => {
    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'dashboard/dagDetail') {
        return {
          dag: { dag_key: 'dag-1', title: 'Daily Brief' },
          nodes: [{ node_key: 'report', status: 'running' }],
        };
      }
      if (method === 'dashboard/dagRuns') return { runs: [] };
      throw new Error(`unexpected method ${method}`);
    });

    const detail = useDagDetail();
    await detail.open({ dag_key: 'dag-1' });

    detail.handleStatusEvent({ dag_key: 'other-dag', node_key: 'report', new_status: 'failed' });
    expect(detail.state.nodes[0].status).toBe('running');

    detail.handleStatusEvent({ dag_key: 'dag-1', node_key: 'report', new_status: 'done' });
    expect(detail.state.nodes[0].status).toBe('done');
  });
});

describe('DagDetailModal', () => {
  it('renders a final output section', () => {
    expect(DagDetailModal.template).toContain('dag-final-output');
    expect(DagDetailModal.template).toContain('最终产物');
  });

  it('formats JSON final output objects as JSON text', () => {
    const vm = DagDetailModal.setup({
      finalOutput: { kind: 'json', result: { score: 0.91, verdict: 'pass' } },
      run: { run_key: 'run-1' },
      dag: {},
      nodes: [],
    }, { emit: vi.fn() });

    expect(vm.finalOutputText.value).toContain('"score": 0.91');
    expect(vm.finalOutputText.value).toContain('"verdict": "pass"');
  });
});
