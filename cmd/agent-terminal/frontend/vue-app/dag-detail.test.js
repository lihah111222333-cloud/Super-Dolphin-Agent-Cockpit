// @ts-nocheck
import { describe, expect, it, vi } from 'vitest';

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
    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'dashboard/dagDetail') {
        return { dag: { dag_key: 'dag-1', title: 'Daily Brief' }, nodes: [] };
      }
      if (method === 'dashboard/dagRuns') {
        throw new Error('runs unavailable');
      }
      throw new Error(`unexpected method ${method}`);
    });

    const detail = useDagDetail();
    await detail.open({ dag_key: 'dag-1' });

    expect(detail.state.error).toBeNull();
    expect(detail.state.dag.title).toBe('Daily Brief');
    expect(detail.state.runs).toEqual([]);
    expect(detail.state.finalOutput).toBeNull();
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
