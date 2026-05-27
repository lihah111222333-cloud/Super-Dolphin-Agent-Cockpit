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
          nodes: [{ node_key: 'report', title: 'Report template', status: 'pending' }],
        };
      }
      if (method === 'dashboard/dagRuns') {
        if (params.status === 'running') {
          expect(params).toEqual({ dagKey: 'dag-1', status: 'running', limit: 1 });
          return { runs: [] };
        }
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
      if (method === 'dashboard/dagRun') {
        expect(params).toEqual({ runKey: 'run-1' });
        return {
          run: {
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
          },
          nodes: [{
            node_key: 'report',
            title: 'Report runtime',
            status: 'succeeded',
            spawning_thread_id: 'thread-child',
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
    expect(detail.state.nodes[0]).toMatchObject({
      title: 'Report runtime',
      status: 'succeeded',
      spawning_thread_id: 'thread-child',
    });
    expect(detail.state.run.run_key).toBe('run-1');
    expect(detail.state.finalOutput).toEqual({
      kind: 'file',
      role: 'final_output',
      path: 'reports/daily-brief.pptx',
      source_node_key: 'report',
    });

    detail.handleStatusEvent({ run_key: 'run-1', node_key: 'report', status: 'done' });
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

  it('loads a running run separately so active gates are not limited to recent history', async () => {
    apiMock.callAPI.mockImplementation(async (method, params) => {
      if (method === 'dashboard/dagDetail') {
        return { dag: { dag_key: 'dag-1', title: 'Daily Brief' }, nodes: [] };
      }
      if (method === 'dashboard/dagRuns') {
        if (params.status === 'running') {
          expect(params).toEqual({ dagKey: 'dag-1', status: 'running', limit: 1 });
          return { runs: [{ run_key: 'run-hidden', status: 'running' }] };
        }
        expect(params).toEqual({ dagKey: 'dag-1', limit: 5 });
        return { runs: [{ run_key: 'run-done', status: 'succeeded' }] };
      }
      if (method === 'dashboard/dagRun') {
        expect(params).toEqual({ runKey: 'run-done' });
        return { run: { run_key: 'run-done', status: 'succeeded' }, nodes: [] };
      }
      throw new Error(`unexpected method ${method}`);
    });

    const detail = useDagDetail();
    await detail.open({ dag_key: 'dag-1' });

    expect(detail.state.runs.map((run) => run.run_key)).toEqual(['run-done']);
    expect(detail.state.activeRun).toMatchObject({ run_key: 'run-hidden', status: 'running' });
  });

  it('deletes the open DAG and clears detail state', async () => {
    apiMock.callAPI.mockImplementation(async (method, params) => {
      if (method === 'dashboard/dagDelete') {
        expect(params).toEqual({ dagKey: 'dag-1' });
        return {};
      }
      throw new Error(`unexpected method ${method}`);
    });

    const detail = useDagDetail();
    detail.state.dag = { dag_key: 'dag-1', title: 'Daily Brief' };
    detail.state.show = true;

    const result = await detail.deleteDAG();

    expect(result).toEqual({ ok: true });
    expect(detail.state.deleting).toBe(false);
    expect(detail.state.deleteError).toBeNull();
    expect(detail.state.show).toBe(false);
    expect(apiMock.callAPI).toHaveBeenCalledWith('dashboard/dagDelete', { dagKey: 'dag-1' });
  });

  it('keeps the current DAG visible when delete fails', async () => {
    const deleteError = new Error('active run');
    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'dashboard/dagDelete') throw deleteError;
      throw new Error(`unexpected method ${method}`);
    });

    const detail = useDagDetail();
    detail.state.dag = { dag_key: 'dag-1', title: 'Daily Brief' };
    detail.state.show = true;

    const result = await detail.deleteDAG();

    expect(result).toEqual({ ok: false });
    expect(detail.state.deleteError).toBe(deleteError);
    expect(detail.state.show).toBe(true);
    expect(logMock.logWarn).toHaveBeenCalledWith('ui', 'useDagDetail.delete.failed', expect.objectContaining({ dagKey: 'dag-1', error: deleteError }));
  });

  it('selects a run and exposes that run final_output', async () => {
    apiMock.callAPI.mockImplementation(async (method, params) => {
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
      if (method === 'dashboard/dagRun') {
        if (params.runKey === 'run-new') {
          return {
            run: { run_key: 'run-new', metadata: { final_output: { kind: 'text', text: 'new result' } } },
            nodes: [{ node_key: 'report', status: 'running' }],
          };
        }
        if (params.runKey === 'run-old') {
          return {
            run: { run_key: 'run-old', metadata: { final_output: { kind: 'text', text: 'old result' } } },
            nodes: [{ node_key: 'report', status: 'done', spawning_thread_id: 'thread-old' }],
          };
        }
      }
      throw new Error(`unexpected method ${method}`);
    });

    const detail = useDagDetail();
    await detail.open({ dag_key: 'dag-1' });

    expect(detail.state.selectedRunKey).toBe('run-new');
    await detail.selectRun('run-old');

    expect(detail.state.selectedRunKey).toBe('run-old');
    expect(detail.state.run.run_key).toBe('run-old');
    expect(detail.state.finalOutput).toEqual({ kind: 'text', text: 'old result' });
    expect(detail.state.nodes[0]).toMatchObject({ status: 'done', spawning_thread_id: 'thread-old' });
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
      if (method === 'dashboard/dagRun') {
        const text = params.runKey === 'run-started' ? 'started result' : 'existing result';
        return {
          run: {
            run_key: params.runKey,
            metadata: { final_output: { kind: 'text', text } },
          },
          nodes: [{ node_key: 'report', status: params.runKey === 'run-started' ? 'done' : 'running' }],
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

  it('terminates the active run and refreshes run state', async () => {
    let terminated = false;
    apiMock.callAPI.mockImplementation(async (method, params) => {
      if (method === 'dashboard/dagDetail') {
        return { dag: { dag_key: 'dag-1', title: 'Daily Brief' }, nodes: [] };
      }
      if (method === 'dashboard/dagRuns') {
        if (params.status === 'running') {
          return { runs: terminated ? [] : [{ run_key: 'run-active', status: 'running' }] };
        }
        return { runs: [{ run_key: 'run-active', status: terminated ? 'cancelled' : 'running' }] };
      }
      if (method === 'dashboard/dagRun') {
        return { run: { run_key: params.runKey, status: terminated ? 'cancelled' : 'running' }, nodes: [] };
      }
      if (method === 'dashboard/dagTerminate') {
        expect(params).toEqual({ dagKey: 'dag-1', runKey: 'run-active', reason: 'user_requested' });
        terminated = true;
        return {};
      }
      throw new Error(`unexpected method ${method}`);
    });

    const detail = useDagDetail();
    await detail.open({ dag_key: 'dag-1' });
    await detail.terminateActiveRun();

    expect(detail.state.terminating).toBe(false);
    expect(detail.state.terminateError).toBeNull();
    expect(detail.state.activeRun).toBeNull();
    expect(detail.state.run.status).toBe('cancelled');
    expect(apiMock.callAPI.mock.calls.filter(([method]) => method === 'dashboard/dagTerminate')).toHaveLength(1);
  });

  it('keeps terminate error after failure refresh still shows a non-terminal run', async () => {
    const terminateError = new Error('terminate refused');
    apiMock.callAPI.mockImplementation(async (method, params) => {
      if (method === 'dashboard/dagDetail') {
        return { dag: { dag_key: 'dag-1', title: 'Daily Brief' }, nodes: [] };
      }
      if (method === 'dashboard/dagRuns') {
        if (params.status === 'running') {
          return { runs: [{ run_key: 'run-active', status: 'running' }] };
        }
        return { runs: [{ run_key: 'run-active', status: 'running' }] };
      }
      if (method === 'dashboard/dagRun') {
        return { run: { run_key: params.runKey, status: 'running' }, nodes: [] };
      }
      if (method === 'dashboard/dagTerminate') {
        throw terminateError;
      }
      throw new Error(`unexpected method ${method}`);
    });

    const detail = useDagDetail();
    await detail.open({ dag_key: 'dag-1' });
    const detailCallsBefore = apiMock.callAPI.mock.calls.filter(([method]) => method === 'dashboard/dagDetail').length;
    const runsCallsBefore = apiMock.callAPI.mock.calls.filter(([method]) => method === 'dashboard/dagRuns').length;
    const runCallsBefore = apiMock.callAPI.mock.calls.filter(([method]) => method === 'dashboard/dagRun').length;

    const result = await detail.terminateActiveRun();

    expect(result).toEqual({ ok: false, refreshed: true });
    expect(detail.state.terminating).toBe(false);
    expect(detail.state.terminateError).toBe(terminateError);
    expect(detail.state.terminateWarning).toBe(null);
    expect(detail.state.activeRun).toEqual({ run_key: 'run-active', status: 'running' });
    expect(detail.state.run).toEqual({ run_key: 'run-active', status: 'running' });
    expect(apiMock.callAPI.mock.calls.filter(([method]) => method === 'dashboard/dagDetail')).toHaveLength(detailCallsBefore + 1);
    expect(apiMock.callAPI.mock.calls.filter(([method]) => method === 'dashboard/dagRuns')).toHaveLength(runsCallsBefore + 2);
    expect(apiMock.callAPI.mock.calls.filter(([method]) => method === 'dashboard/dagRun')).toHaveLength(runCallsBefore + 1);
  });

  it('refreshes cancelled state and stores a warning when terminate reports post-cancel stop failure', async () => {
    const terminateError = new Error('stop spawned agent failed');
    apiMock.callAPI.mockImplementation(async (method, params) => {
      if (method === 'dashboard/dagDetail') {
        return { dag: { dag_key: 'dag-1', title: 'Daily Brief' }, nodes: [] };
      }
      if (method === 'dashboard/dagRuns') {
        if (params.status === 'running') {
          return { runs: [{ run_key: 'run-active', status: 'running' }] };
        }
        return { runs: [{ run_key: 'run-active', status: 'running' }] };
      }
      if (method === 'dashboard/dagRun') {
        return { run: { run_key: params.runKey, status: 'running' }, nodes: [] };
      }
      if (method === 'dashboard/dagTerminate') {
        apiMock.callAPI.mockImplementation(async (refreshMethod, refreshParams) => {
          if (refreshMethod === 'dashboard/dagDetail') {
            return { dag: { dag_key: 'dag-1', title: 'Daily Brief' }, nodes: [] };
          }
          if (refreshMethod === 'dashboard/dagRuns') {
            if (refreshParams.status === 'running') return { runs: [] };
            return { runs: [{ run_key: 'run-active', status: 'cancelled' }] };
          }
          if (refreshMethod === 'dashboard/dagRun') {
            return { run: { run_key: refreshParams.runKey, status: 'cancelled' }, nodes: [] };
          }
          throw new Error(`unexpected refresh method ${refreshMethod}`);
        });
        throw terminateError;
      }
      throw new Error(`unexpected method ${method}`);
    });

    const detail = useDagDetail();
    await detail.open({ dag_key: 'dag-1' });
    const result = await detail.terminateActiveRun();

    expect(result.ok).toBe(true);
    expect(detail.state.terminating).toBe(false);
    expect(detail.state.terminateError).toBeNull();
    expect(detail.state.terminateWarning).toBe(terminateError);
    expect(detail.state.activeRun).toBeNull();
    expect(detail.state.run.status).toBe('cancelled');
  });

  it('clears stale terminate warnings before starting a new run', async () => {
    apiMock.callAPI.mockImplementation(async (method, params) => {
      if (method === 'dashboard/dagDetail') {
        return { dag: { dag_key: 'dag-1', title: 'Daily Brief' }, nodes: [] };
      }
      if (method === 'dashboard/dagRuns') {
        if (params.status === 'running') return { runs: [] };
        return { runs: [{ run_key: 'run-started', status: 'running' }] };
      }
      if (method === 'dashboard/dagRun') {
        return { run: { run_key: params.runKey, status: 'running' }, nodes: [] };
      }
      if (method === 'dashboard/dagStart') {
        return { run_key: 'run-started' };
      }
      throw new Error(`unexpected method ${method}`);
    });

    const detail = useDagDetail();
    await detail.open({ dag_key: 'dag-1' });
    detail.state.terminateWarning = new Error('old stop warning');
    await detail.start();

    expect(detail.state.terminateWarning).toBeNull();
    expect(detail.state.terminateError).toBeNull();
  });

  it('does not terminate a non-running selected run when no active running run is loaded', async () => {
    apiMock.callAPI.mockImplementation(async (method, params) => {
      if (method === 'dashboard/dagDetail') {
        return { dag: { dag_key: 'dag-1', title: 'Daily Brief' }, nodes: [] };
      }
      if (method === 'dashboard/dagRuns') {
        if (params.status === 'running') return { runs: [] };
        return { runs: [{ run_key: 'run-queued', status: 'queued' }] };
      }
      if (method === 'dashboard/dagRun') {
        return { run: { run_key: params.runKey, status: 'queued' }, nodes: [] };
      }
      throw new Error(`unexpected method ${method}`);
    });

    const detail = useDagDetail();
    await detail.open({ dag_key: 'dag-1' });
    await detail.terminateActiveRun();

    expect(detail.state.terminateError?.message).toBe('缺少运行中的 run');
    expect(detail.state.run.status).toBe('queued');
    expect(apiMock.callAPI.mock.calls.filter(([method]) => method === 'dashboard/dagTerminate')).toHaveLength(0);
  });

  it('terminates a list-sourced active run when detail has no run loaded', async () => {
    let terminated = false;
    apiMock.callAPI.mockImplementation(async (method, params) => {
      if (method === 'dashboard/dagDetail') return { dag: { dag_key: 'dag-1', title: 'Daily Brief' }, nodes: [] };
      if (method === 'dashboard/dagRuns') {
        if (params.status === 'running') return { runs: [] };
        return { runs: terminated ? [{ run_key: 'run-list', status: 'cancelled' }] : [] };
      }
      if (method === 'dashboard/dagRun') return { run: { run_key: params.runKey, status: 'cancelled' }, nodes: [] };
      if (method === 'dashboard/dagTerminate') {
        expect(params).toEqual({ dagKey: 'dag-1', runKey: 'run-list', reason: 'user_requested' });
        terminated = true;
        return {};
      }
      throw new Error(`unexpected method ${method}`);
    });

    const detail = useDagDetail();
    await detail.open({ dag_key: 'dag-1' });
    await detail.terminateActiveRun({ run_key: 'run-list', status: 'running' });

    expect(detail.state.terminateError).toBeNull();
    expect(detail.state.run.status).toBe('cancelled');
  });

  it('keeps terminating true until the post-terminate refresh finishes', async () => {
    let terminated = false;
    let detailCalls = 0;
    let resolveRefreshDetail;
    const refreshDetail = new Promise((resolve) => { resolveRefreshDetail = resolve; });
    apiMock.callAPI.mockImplementation(async (method, params) => {
      if (method === 'dashboard/dagDetail') {
        detailCalls += 1;
        if (detailCalls === 1) return { dag: { dag_key: 'dag-1', title: 'Daily Brief' }, nodes: [] };
        return refreshDetail;
      }
      if (method === 'dashboard/dagRuns') {
        if (params.status === 'running') {
          return { runs: terminated ? [] : [{ run_key: 'run-active', status: 'running' }] };
        }
        return { runs: [{ run_key: 'run-active', status: terminated ? 'cancelled' : 'running' }] };
      }
      if (method === 'dashboard/dagRun') {
        return { run: { run_key: params.runKey, status: terminated ? 'cancelled' : 'running' }, nodes: [] };
      }
      if (method === 'dashboard/dagTerminate') {
        terminated = true;
        return {};
      }
      throw new Error(`unexpected method ${method}`);
    });

    const detail = useDagDetail();
    await detail.open({ dag_key: 'dag-1' });
    const terminatePromise = detail.terminateActiveRun();
    await Promise.resolve();
    await Promise.resolve();

    expect(detail.state.terminating).toBe(true);

    resolveRefreshDetail({ dag: { dag_key: 'dag-1', title: 'Daily Brief' }, nodes: [] });
    await terminatePromise;
    expect(detail.state.terminating).toBe(false);
  });

  it('saves an agent node through dashboard apply ops and refreshes the same dag', async () => {
    const applyOpsCalls = [];
    apiMock.callAPI.mockImplementation(async (method, params) => {
      if (method === 'dashboard/dagDetail') {
        return {
          dag: { dag_key: 'dag-1', title: 'Daily Brief', version: params.dagKey === 'dag-1' ? 7 : 0 },
          nodes: [{
            node_key: 'draft',
            title: 'Draft report',
            node_type: 'agent',
            depends_on: ['collect'],
            config: {
              exec: { provider: 'claude', model: 'sonnet', prompt_key: 'main/writer' },
              first_turn: 'old prompt',
            },
          }],
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
    await detail.saveAgentNode({
      nodeKey: 'draft',
      title: 'Draft report v2',
      dependsOn: ['collect'],
      config: {
        exec: { provider: 'claude', model: 'opus', prompt_key: 'main/writer' },
        first_turn: 'new prompt',
      },
    });

    expect(detail.state.saveError).toBeNull();
    expect(detail.state.savingNodeKey).toBe('');
    expect(applyOpsCalls).toHaveLength(1);
    expect(applyOpsCalls[0]).toEqual({
      dagKey: 'dag-1',
      baseVersion: 7,
      ops: [{
        op: 'update_node',
        node_key: 'draft',
        patch: {
          title: 'Draft report v2',
          depends_on: ['collect'],
          config: {
            exec: { provider: 'claude', model: 'opus', prompt_key: 'main/writer' },
            first_turn: 'new prompt',
          },
        },
      }],
    });
    expect(apiMock.callAPI.mock.calls.filter(([method]) => method === 'dashboard/dagDetail')).toHaveLength(2);
  });

  it('allows node saves with initial DAG version zero', async () => {
    const applyOpsCalls = [];
    apiMock.callAPI.mockImplementation(async (method, params) => {
      if (method === 'dashboard/dagDetail') {
        return {
          dag: { dag_key: 'dag-1', title: 'Daily Brief', version: 0 },
          nodes: [{
            node_key: 'draft',
            title: 'Draft report',
            node_type: 'agent',
            depends_on: [],
            config: {
              exec: { provider: 'claude', model: 'sonnet', prompt_key: 'main/writer' },
              first_turn: 'old prompt',
            },
          }],
        };
      }
      if (method === 'dashboard/dagRuns') return { runs: [] };
      if (method === 'dashboard/dagApplyOps') {
        applyOpsCalls.push(params);
        return { newVersion: 1 };
      }
      throw new Error(`unexpected method ${method}`);
    });

    const detail = useDagDetail();
    await detail.open({ dag_key: 'dag-1' });
    await detail.saveAgentNode({
      nodeKey: 'draft',
      title: 'Draft report v2',
      dependsOn: [],
      config: {
        exec: { provider: 'claude', model: 'opus', prompt_key: 'main/writer' },
        first_turn: 'new prompt',
      },
    });

    expect(detail.state.saveError).toBeNull();
    expect(applyOpsCalls).toHaveLength(1);
    expect(applyOpsCalls[0].baseVersion).toBe(0);
  });

  it('rejects node saves before calling apply ops when DAG version is missing', async () => {
    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'dashboard/dagDetail') {
        return { dag: { dag_key: 'dag-1', title: 'Daily Brief' }, nodes: [] };
      }
      if (method === 'dashboard/dagRuns') return { runs: [] };
      throw new Error(`unexpected method ${method}`);
    });

    const detail = useDagDetail();
    await detail.open({ dag_key: 'dag-1' });
    await detail.saveAgentNode({
      nodeKey: 'draft',
      title: 'Draft',
      dependsOn: [],
      config: { exec: { provider: 'claude', model: 'sonnet', prompt_key: 'main/writer' } },
    });

    expect(detail.state.saveError).toBeInstanceOf(Error);
    expect(detail.state.saveError.message).toContain('DAG version');
    expect(apiMock.callAPI.mock.calls.some(([method]) => method === 'dashboard/dagApplyOps')).toBe(false);
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
    apiMock.callAPI.mockImplementation(async (method, params) => {
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
    apiMock.callAPI.mockImplementation(async (method, params) => {
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
      if (method === 'dashboard/dagRun') {
        expect(params).toEqual({ runKey: 'run-1' });
        return {
          run: {
            run_key: 'run-1',
            metadata: '{"final_output":{"kind":"json","result":{"verdict":"pass"}}}',
          },
          nodes: [{ node_key: 'report', status: 'done' }],
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
    expect(detail.state.runsError).toBeNull();
    expect(detail.state.nodes[0]).toMatchObject({ node_key: 'report', status: 'done' });
  });

  it('surfaces malformed dag run detail without mixing stale run data', async () => {
    apiMock.callAPI.mockImplementation(async (method) => {
      if (method === 'dashboard/dagDetail') {
        return {
          dag: { dag_key: 'dag-1', title: 'Daily Brief' },
          nodes: [{ node_key: 'report', status: 'pending' }],
        };
      }
      if (method === 'dashboard/dagRuns') {
        return {
          runs: [{
            run_key: 'run-1',
            metadata: { final_output: { kind: 'text', text: 'list output' } },
          }],
        };
      }
      if (method === 'dashboard/dagRun') {
        return { nodes: [{ node_key: 'report', status: 'done' }] };
      }
      throw new Error(`unexpected method ${method}`);
    });

    const detail = useDagDetail();
    await detail.open({ dag_key: 'dag-1' });

    expect(detail.state.selectedRunKey).toBe('run-1');
    expect(detail.state.run).toBeNull();
    expect(detail.state.finalOutput).toBeNull();
    expect(detail.state.nodes).toEqual([]);
    expect(detail.state.runsError).toBeInstanceOf(Error);
    expect(detail.state.runsError.message).toContain('missing run');
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
