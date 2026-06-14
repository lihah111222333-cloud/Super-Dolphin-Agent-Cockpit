import React from 'react';
import { CancelledError, QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { WorkflowPage } from './WorkflowPage.jsx';

const backend = vi.hoisted(() => ({
  applyDagOps: vi.fn(),
  deleteDag: vi.fn(),
  dispatchDagNode: vi.fn(),
  getDashboardPage: vi.fn(),
  getDagDetail: vi.fn(),
  getDagRun: vi.fn(),
  getDagRuns: vi.fn(),
  openSharedFile: vi.fn(),
  readSharedFile: vi.fn(),
  startDag: vi.fn(),
  startTurn: vi.fn(),
  startThread: vi.fn(),
  terminateDagRun: vi.fn(),
}));

vi.mock('../../shared/api/backendApi.js', () => backend);

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((promiseResolve, promiseReject) => {
    resolve = promiseResolve;
    reject = promiseReject;
  });
  return { promise, resolve, reject };
}

function workflowPageElement(queryClient, store = {}, pageProps = {}) {
  return (
    <QueryClientProvider client={queryClient}>
      <WorkflowPage projectPath="/repo/app" store={store} {...pageProps} />
    </QueryClientProvider>
  );
}

function renderWorkflowPage(store = {}, pageProps = {}) {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  });

  const result = render(workflowPageElement(queryClient, store, pageProps));
  return {
    ...result,
    rerenderWorkflowPage: (nextPageProps = {}) => {
      result.rerender(workflowPageElement(queryClient, store, nextPageProps));
    },
  };
}

function mockWorkflowDag() {
  const dag = {
    dag_key: 'daily-brief',
    title: 'Daily Brief',
    status: 'ready',
    trigger: 'manual',
    version: 7,
  };

  backend.getDashboardPage.mockResolvedValue({ dags: [dag] });
  backend.getDagDetail.mockResolvedValue({
    dag,
    nodes: [
      {
        node_key: 'draft',
        title: 'Draft',
        node_type: 'agent',
        assigned_to: 'codex',
        depends_on: [],
      },
    ],
  });
  backend.getDagRuns.mockResolvedValue({ runs: [] });
  backend.getDagRun.mockResolvedValue({ run: null, nodes: [] });
  backend.startDag.mockResolvedValue({ run_key: 'run-live' });
  backend.dispatchDagNode.mockResolvedValue({ enqueued: true });
}

describe('WorkflowPage module', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('exports the workflow page component', () => {
    expect(WorkflowPage).toBeTypeOf('function');
  });

  it('loads DAG dashboard data and fetches selected DAG detail', async () => {
    mockWorkflowDag();

    renderWorkflowPage();

    expect((await screen.findAllByText('Daily Brief')).length).toBeGreaterThanOrEqual(2);
    expect(screen.getByRole('tab', { name: '历史记录 1' })).toHaveAttribute('aria-selected', 'true');

    await waitFor(() => {
      expect(backend.getDashboardPage).toHaveBeenCalledWith({ cwd: '/repo/app', page: 'dags' });
      expect(backend.getDagDetail).toHaveBeenCalledWith({ dagKey: 'daily-brief' });
      expect(backend.getDagRuns).toHaveBeenCalledWith({ dagKey: 'daily-brief', limit: 30 });
      expect(backend.getDagRuns).toHaveBeenCalledWith({ dagKey: 'daily-brief', status: 'running', limit: 1 });
    });
  });

  it('shows automation overview metrics from the DAG dashboard data', async () => {
    const dags = [
      {
        dag_key: 'live-flow',
        title: 'Live Flow',
        status: 'ready',
        trigger: 'scheduled',
        cron_expr: 'CRON_TZ=Asia/Shanghai 0 8 * * *',
        latest_run: {
          run_key: 'run-live',
          status: 'running',
          metadata: { final_output: { path: 'reports/live.md' } },
        },
      },
      {
        dag_key: 'scheduled-flow',
        title: 'Scheduled Flow',
        status: 'ready',
        trigger: 'scheduled',
        cron_expr: 'CRON_TZ=Asia/Shanghai 0 9 * * 1-5',
        schedule_enabled: true,
        hasFinalOutput: false,
        latest_run: {
          run_key: 'run-empty',
          status: 'succeeded',
          metadata: { final_output: {} },
        },
      },
      {
        dag_key: 'manual-flow',
        title: 'Manual Flow',
        status: 'ready',
        trigger: 'manual',
      },
      {
        dag_key: 'done-flow',
        title: 'Done Flow',
        status: 'done',
        trigger: 'manual',
        hasFinalOutput: true,
        latest_run: {
          run_key: 'run-done',
          status: 'succeeded',
        },
      },
    ];
    backend.getDashboardPage.mockResolvedValue({ dags });
    backend.getDagDetail.mockImplementation(({ dagKey }) => Promise.resolve({
      dag: dags.find((item) => item.dag_key === dagKey) || dags[0],
      nodes: [],
    }));
    backend.getDagRuns.mockResolvedValue({ runs: [] });
    backend.getDagRun.mockResolvedValue({ run: null, nodes: [] });

    renderWorkflowPage();

    await screen.findByLabelText('自动化资产');
    const metricValue = (label) => {
      const overview = screen.getByLabelText('自动化资产');
      const term = Array.from(overview.querySelectorAll('dt')).find((node) => node.textContent === label);
      expect(term).toBeTruthy();
      return term.nextElementSibling;
    };
    await waitFor(() => expect(metricValue('全部自动化')).toHaveTextContent('4'));
    expect(metricValue('运行中')).toHaveTextContent('1');
    expect(metricValue('定时任务')).toHaveTextContent('1');
    expect(metricValue('可启动')).toHaveTextContent('2');
    expect(metricValue('最终产物')).toHaveTextContent('2');
  });

  it('coalesces pending DAG list refreshes without surfacing TanStack cancellation', async () => {
    const dag = {
      dag_key: 'daily-brief',
      title: 'Daily Brief',
      status: 'ready',
      trigger: 'manual',
      version: 7,
    };
    const pendingRefresh = deferred();
    let dashboardCalls = 0;
    backend.getDashboardPage.mockImplementation(() => {
      dashboardCalls += 1;
      if (dashboardCalls === 1) return Promise.resolve({ dags: [dag] });
      if (dashboardCalls === 2) return pendingRefresh.promise;
      return Promise.reject(new CancelledError());
    });
    backend.getDagDetail.mockResolvedValue({
      dag,
      nodes: [{ node_key: 'draft', title: 'Draft', node_type: 'agent', assigned_to: 'codex', depends_on: [] }],
    });
    backend.getDagRuns.mockResolvedValue({ runs: [] });
    backend.getDagRun.mockResolvedValue({ run: null, nodes: [] });

    const { rerenderWorkflowPage } = renderWorkflowPage({}, { refreshKey: 0 });

    expect((await screen.findAllByText('Daily Brief')).length).toBeGreaterThanOrEqual(2);
    expect(backend.getDashboardPage).toHaveBeenCalledTimes(1);

    await act(async () => {
      rerenderWorkflowPage({ refreshKey: 1 });
      await Promise.resolve();
    });
    await waitFor(() => expect(backend.getDashboardPage).toHaveBeenCalledTimes(2));

    await act(async () => {
      rerenderWorkflowPage({ refreshKey: 2 });
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(backend.getDashboardPage).toHaveBeenCalledTimes(2);
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();

    await act(async () => {
      pendingRefresh.resolve({ dags: [dag] });
      await pendingRefresh.promise;
    });
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('still reports real backend errors during DAG list refresh', async () => {
    const dag = {
      dag_key: 'daily-brief',
      title: 'Daily Brief',
      status: 'ready',
      trigger: 'manual',
      version: 7,
    };
    backend.getDashboardPage
      .mockResolvedValueOnce({ dags: [dag] })
      .mockRejectedValueOnce(new Error('workflow backend offline'));
    backend.getDagDetail.mockResolvedValue({ dag, nodes: [] });
    backend.getDagRuns.mockResolvedValue({ runs: [] });
    backend.getDagRun.mockResolvedValue({ run: null, nodes: [] });

    const { rerenderWorkflowPage } = renderWorkflowPage({}, { refreshKey: 0 });

    expect((await screen.findAllByText('Daily Brief')).length).toBeGreaterThanOrEqual(2);
    await act(async () => {
      rerenderWorkflowPage({ refreshKey: 1 });
      await Promise.resolve();
    });

    expect(await screen.findByRole('alert')).toHaveTextContent('同步失败，显示的是上次成功的数据：workflow backend offline');
  });

  it('starts the selected DAG with the manual trigger payload', async () => {
    mockWorkflowDag();

    renderWorkflowPage();

    fireEvent.click(await screen.findByRole('button', { name: '运行' }));

    await waitFor(() => {
      expect(backend.startDag).toHaveBeenCalledWith(expect.objectContaining({
        dagKey: 'daily-brief',
        triggerSource: 'manual',
      }));
    });

    const payload = backend.startDag.mock.calls[0][0];
    expect(payload.idempotencyKey).toMatch(/^ui-/);
  });

  it('shows blocked ready-node diagnostics and dispatches the runtime node with an assignee', async () => {
    mockWorkflowDag();
    backend.getDagRuns.mockImplementation((params = {}) => Promise.resolve({
      runs: params.status === 'running'
        ? [{ id: 88, run_key: 'run-waiting', status: 'waiting_for_assignee' }]
        : [{ id: 88, run_key: 'run-waiting', status: 'waiting_for_assignee' }],
    }));
    backend.getDagRun.mockResolvedValue({
      run: { id: 88, run_key: 'run-waiting', status: 'waiting_for_assignee' },
      nodes: [{
        node_key: 'draft',
        title: 'Draft',
        node_type: 'agent',
        status: 'ready',
        assigned_to: '',
        active_wakeup_id: null,
        depends_on: [],
        config: { exec: { agent_key: 'writer', cwd: '/repo/app' } },
      }],
    });

    renderWorkflowPage();

    expect(await screen.findByText(/缺少 assigned_to/)).toHaveTextContent('Draft');
    expect(screen.getByText(/ready 但没有 active_wakeup_id/)).toBeInTheDocument();

    const assigneeInput = screen.getByRole('textbox', { name: /恢复执行者/ });
    fireEvent.change(assigneeInput, { target: { value: 'codex-runner' } });
    fireEvent.click(screen.getByRole('button', { name: '指派并派发' }));

    await waitFor(() => {
      expect(backend.dispatchDagNode).toHaveBeenCalledWith({
        dagKey: 'daily-brief',
        runId: 88,
        nodeKey: 'draft',
        assignedTo: 'codex-runner',
      });
    });
  });

  it('restores the run action and shows an error when startDag times out', async () => {
    mockWorkflowDag();
    backend.startDag.mockReturnValue(new Promise(() => {}));

    renderWorkflowPage();

    const runButton = await screen.findByRole('button', { name: '运行' });
    vi.useFakeTimers();
    fireEvent.click(runButton);

    expect(screen.getByRole('button', { name: '启动中...' })).toBeDisabled();

    await act(async () => {
      await vi.advanceTimersByTimeAsync(8000);
    });

    expect(screen.getByRole('button', { name: '运行' })).not.toBeDisabled();
    expect(screen.getByRole('alert')).toHaveTextContent(
      '启动自动化失败：自动化操作超时，请检查任务数据或后端状态。',
    );
  });

  it('formats the active run row time as a readable date and time', async () => {
    mockWorkflowDag();
    backend.getDagRuns.mockImplementation((params = {}) => Promise.resolve({
      runs: params.status === 'running'
        ? []
        : [{
            run_key: 'run-readable-time',
            status: 'succeeded',
            started_at: '2026-05-30T08:09:10Z',
          }],
    }));
    backend.getDagRun.mockResolvedValue({
      run: { run_key: 'run-readable-time', status: 'succeeded' },
      nodes: [],
    });

    renderWorkflowPage();

    const runRow = await screen.findByRole('button', { name: /第 1 次运行/ });
    await waitFor(() => expect(runRow).toHaveClass('active'));
    const time = runRow.querySelector('time');
    expect(time).toHaveTextContent('2026-05-30 08:09:10');
    expect(time).toHaveAttribute('dateTime', '2026-05-30T08:09:10Z');
    expect(time).toHaveAttribute('title', '2026-05-30T08:09:10Z');
  });

  it('numbers run rows by chronological run order even when the backend returns newest first', async () => {
    mockWorkflowDag();
    backend.getDagRuns.mockImplementation((params = {}) => Promise.resolve({
      runs: params.status === 'running'
        ? []
        : [
            { run_key: 'run-later', status: 'running', started_at: '2026-06-02T20:12:43.303129+08:00' },
            { run_key: 'run-earlier', status: 'cancelled', started_at: '2026-06-02T17:22:56.112233+08:00' },
          ],
    }));
    backend.getDagRun.mockResolvedValue({ run: { run_key: 'run-later', status: 'running' }, nodes: [] });

    renderWorkflowPage();

    const rows = await screen.findAllByRole('button', { name: /第 \d+ 次运行/ });
    expect(rows[0]).toHaveTextContent('第 1 次运行');
    expect(rows[0]).toHaveTextContent('2026-06-02 17:22:56');
    expect(rows[1]).toHaveTextContent('第 2 次运行');
    expect(rows[1]).toHaveTextContent('2026-06-02 20:12:43');
  });

  it('shows a stable open action for mp4 final output without reading it as text', async () => {
    const dag = {
      dag_key: 'douyin-video',
      title: 'Douyin Video',
      status: 'ready',
      trigger: 'manual',
      version: 1,
    };
    const finalPath = 'dag/douyin/daily-video/run-video-21/final.mp4';
    backend.getDashboardPage.mockResolvedValue({ dags: [dag] });
    backend.getDagDetail.mockResolvedValue({ dag, nodes: [] });
    backend.getDagRuns.mockImplementation((params = {}) => Promise.resolve({
      runs: params.status === 'running'
        ? []
        : [{
            id: 21,
            run_key: 'run-video-21',
            status: 'succeeded',
            started_at: '2026-06-06T13:25:55+08:00',
            metadata: {
              final_output: {
                kind: 'file',
                role: 'final_output',
                path: finalPath,
                source_node_key: 'generate_video_mp4',
              },
            },
          }],
    }));
    backend.getDagRun.mockResolvedValue({
      run: {
        id: 21,
        run_key: 'run-video-21',
        status: 'succeeded',
        metadata: {
          final_output: {
            kind: 'file',
            role: 'final_output',
            path: finalPath,
            source_node_key: 'generate_video_mp4',
          },
        },
      },
      nodes: [],
    });
    backend.openSharedFile.mockResolvedValue({ opened: true });

    renderWorkflowPage();

    expect(await screen.findByText(finalPath)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '页内播放' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '系统打开' }));

    await waitFor(() => expect(backend.openSharedFile).toHaveBeenCalledWith({ path: finalPath }));
    expect(backend.readSharedFile).not.toHaveBeenCalled();
  });

  it('orders runtime workflow steps and topology by dependencies instead of backend row order', async () => {
    const dag = {
      dag_key: 'douyin-video',
      title: 'Douyin Video',
      status: 'ready',
      trigger: 'manual',
      version: 1,
    };
    const unorderedNodes = [
      {
        node_key: 'collect_signals',
        title: '收集视频方向信号',
        node_type: 'agent',
        assigned_to: 'collector',
        depends_on: [],
        status: 'done',
      },
      {
        node_key: 'generate_video_mp4',
        title: '调用 video_with_audio 生成 MP4',
        node_type: 'agent',
        assigned_to: 'video-runner',
        depends_on: ['write_script'],
        status: 'done',
      },
      {
        node_key: 'write_script',
        title: '生成成片脚本',
        node_type: 'agent',
        assigned_to: 'writer',
        depends_on: ['collect_signals'],
        status: 'done',
      },
    ];
    backend.getDashboardPage.mockResolvedValue({ dags: [dag] });
    backend.getDagDetail.mockResolvedValue({ dag, nodes: unorderedNodes });
    backend.getDagRuns.mockImplementation((params = {}) => Promise.resolve({
      runs: params.status === 'running'
        ? []
        : [{ id: 21, run_key: 'run-video', status: 'succeeded', started_at: '2026-06-06T13:25:55+08:00' }],
    }));
    backend.getDagRun.mockResolvedValue({
      run: { id: 21, run_key: 'run-video', status: 'succeeded' },
      nodes: unorderedNodes,
    });

    const { container } = renderWorkflowPage();

    await waitFor(() => {
      const stepTitles = Array.from(container.querySelectorAll('.dag-node-list strong')).map((node) => node.textContent);
      expect(stepTitles).toEqual([
        '收集视频方向信号',
        '生成成片脚本',
        '调用 video_with_audio 生成 MP4',
      ]);
    });
    expect(container.querySelector('.workflow-topology-source')?.textContent).toBe(
      '收集视频方向信号 --> 生成成片脚本\n'
      + '生成成片脚本 --> 调用 video_with_audio 生成 MP4',
    );
  });

  it('keeps the newest ten runs visible and collapses earlier runs behind a themed expand button', async () => {
    mockWorkflowDag();
    const runs = Array.from({ length: 12 }, (_, index) => ({
      run_key: `run-${String(index + 1).padStart(2, '0')}`,
      status: index === 11 ? 'running' : 'succeeded',
      started_at: `2026-06-${String(index + 1).padStart(2, '0')}T08:00:00Z`,
    })).reverse();
    backend.getDagRuns.mockImplementation((params = {}) => Promise.resolve({
      runs: params.status === 'running' ? [] : runs,
    }));
    backend.getDagRun.mockResolvedValue({ run: { run_key: 'run-12', status: 'running' }, nodes: [] });

    renderWorkflowPage();

    expect(await screen.findByRole('button', { name: /第 3 次运行/ })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /第 12 次运行/ })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /第 1 次运行/ })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /第 2 次运行/ })).not.toBeInTheDocument();
    const expand = screen.getByRole('button', { name: '展开较早 2 次运行' });
    expect(expand).toHaveClass('dag-run-list-toggle');
    expect(expand).toHaveAttribute('aria-expanded', 'false');

    fireEvent.click(expand);

    expect(await screen.findByRole('button', { name: /第 1 次运行/ })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /第 2 次运行/ })).toBeInTheDocument();
    const collapse = screen.getByRole('button', { name: '收起较早运行记录' });
    expect(collapse).toHaveAttribute('aria-expanded', 'true');
  });

  it('reads and saves agent node settings through config.exec and outputs schema', async () => {
    const dag = {
      dag_key: 'daily-brief',
      title: 'Daily Brief',
      status: 'ready',
      trigger: 'manual',
      version: 7,
    };
    backend.getDashboardPage.mockResolvedValue({ dags: [dag] });
    backend.getDagDetail.mockResolvedValue({
      dag,
      nodes: [{
        node_key: 'draft',
        title: 'Draft',
        node_type: 'agent',
        assigned_to: 'codex-runner',
        depends_on: [],
        config: {
          exec: {
            provider: 'codex',
            model: 'gpt-5.5',
            agent_key: 'writer',
            prompt_key: 'main/writer',
            cwd: '/repo/app',
          },
          first_turn: 'Write a daily brief',
          outputs: {
            to_sharedfile: { path: 'reports/brief.md', lock_mode: 'exclusive' },
            to_node_result: true,
          },
        },
      }],
    });
    backend.getDagRuns.mockResolvedValue({ runs: [] });
    backend.getDagRun.mockResolvedValue({ run: null, nodes: [] });
    backend.applyDagOps.mockResolvedValue({ newVersion: 8 });

    renderWorkflowPage();

    expect(await screen.findByDisplayValue('/repo/app')).toBeInTheDocument();
    expect(screen.getByDisplayValue('writer')).toBeInTheDocument();
    expect(screen.getByDisplayValue('reports/brief.md')).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText('执行 cwd'), { target: { value: '/repo/review' } });
    fireEvent.change(screen.getByLabelText('Agent Key'), { target: { value: 'reviewer' } });
    fireEvent.change(screen.getByLabelText('Prompt Key'), { target: { value: 'main/reviewer' } });
    fireEvent.change(screen.getByLabelText('输出 sharedfile'), { target: { value: 'reports/review.md' } });
    fireEvent.change(screen.getByLabelText('写入模式'), { target: { value: 'append' } });
    fireEvent.click(screen.getByRole('button', { name: '保存步骤' }));

    await waitFor(() => expect(backend.applyDagOps).toHaveBeenCalled());
    const patch = backend.applyDagOps.mock.calls[0][0].ops[0].patch;
    expect(patch.config).toMatchObject({
      exec: {
        provider: 'codex',
        model: 'gpt-5.5',
        agent_key: 'reviewer',
        prompt_key: 'main/reviewer',
        cwd: '/repo/review',
      },
      first_turn: 'Write a daily brief',
      outputs: {
        to_sharedfile: { path: 'reports/review.md', lock_mode: 'append' },
        to_node_result: true,
      },
    });
    expect(patch.config).not.toHaveProperty('provider');
    expect(patch.config).not.toHaveProperty('output_file');
  });

  it('renders automation and hybrid nodes with their real exec schema fields', async () => {
    const dag = {
      dag_key: 'daily-brief',
      title: 'Daily Brief',
      status: 'ready',
      trigger: 'manual',
      version: 7,
    };
    backend.getDashboardPage.mockResolvedValue({ dags: [dag] });
    backend.getDagDetail.mockResolvedValue({
      dag,
      nodes: [
        {
          node_key: 'build',
          title: 'Build',
          node_type: 'automation',
          assigned_to: 'worker',
          depends_on: [],
          config: { exec: { kind: 'command_card', command_ref: 'build_app' }, outputs: { to_node_result: true } },
        },
        {
          node_key: 'verify',
          title: 'Verify',
          node_type: 'hybrid',
          assigned_to: 'worker',
          depends_on: ['build'],
          config: {
            exec: {
              automation: { kind: 'command_card', command_ref: 'test_app' },
              verifier: { provider: 'claude', model: 'opus', agent_key: 'reviewer', prompt_key: 'main/reviewer', cwd: '/repo/app' },
            },
            outputs: { to_sharedfile: { path: 'reports/verify.md' } },
          },
        },
      ],
    });
    backend.getDagRuns.mockResolvedValue({ runs: [] });
    backend.getDagRun.mockResolvedValue({ run: null, nodes: [] });

    renderWorkflowPage();

    expect(await screen.findByDisplayValue('build_app')).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText('步骤'), { target: { value: 'verify' } });

    expect(screen.getByDisplayValue('test_app')).toBeInTheDocument();
    expect(screen.getByDisplayValue('reviewer')).toBeInTheDocument();
    expect(screen.getByDisplayValue('reports/verify.md')).toBeInTheDocument();
  });

  it('saves automation node settings through config.exec and outputs schema', async () => {
    const dag = {
      dag_key: 'daily-brief',
      title: 'Daily Brief',
      status: 'ready',
      trigger: 'manual',
      version: 7,
    };
    backend.getDashboardPage.mockResolvedValue({ dags: [dag] });
    backend.getDagDetail.mockResolvedValue({
      dag,
      nodes: [{
        node_key: 'build',
        title: 'Build',
        node_type: 'automation',
        assigned_to: 'worker',
        depends_on: [],
        config: { exec: { kind: 'command_card', command_ref: 'build_app' }, outputs: { to_node_result: false } },
      }],
    });
    backend.getDagRuns.mockResolvedValue({ runs: [] });
    backend.getDagRun.mockResolvedValue({ run: null, nodes: [] });
    backend.applyDagOps.mockResolvedValue({ newVersion: 8 });

    renderWorkflowPage();

    expect(await screen.findByDisplayValue('build_app')).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText('命令卡片'), { target: { value: 'build_app_v2' } });
    fireEvent.change(screen.getByLabelText('输出 sharedfile'), { target: { value: 'reports/build.log' } });
    fireEvent.click(screen.getByLabelText('结果写入节点摘要'));
    fireEvent.click(screen.getByRole('button', { name: '保存步骤' }));

    await waitFor(() => expect(backend.applyDagOps).toHaveBeenCalled());
    const patch = backend.applyDagOps.mock.calls[0][0].ops[0].patch;
    expect(patch.config).toMatchObject({
      exec: {
        kind: 'command_card',
        command_ref: 'build_app_v2',
      },
      outputs: {
        to_sharedfile: { path: 'reports/build.log', lock_mode: 'exclusive' },
        to_node_result: true,
      },
    });
    expect(JSON.stringify(patch.config)).not.toContain('agent_key');
  });

  it('saves hybrid node verifier settings with the backend-accepted nested schema', async () => {
    const dag = {
      dag_key: 'daily-brief',
      title: 'Daily Brief',
      status: 'ready',
      trigger: 'manual',
      version: 7,
    };
    backend.getDashboardPage.mockResolvedValue({ dags: [dag] });
    backend.getDagDetail.mockResolvedValue({
      dag,
      nodes: [{
        node_key: 'verify',
        title: 'Verify',
        node_type: 'hybrid',
        assigned_to: 'worker',
        depends_on: [],
        config: {
          exec: {
            automation: { kind: 'command_card', command_ref: 'test_app' },
            verifier: { provider: 'claude', model: 'opus', agent_key: 'reviewer', prompt_key: 'main/reviewer', cwd: '/repo/app' },
          },
          outputs: { to_sharedfile: { path: 'reports/verify.md', lock_mode: 'exclusive' } },
        },
      }],
    });
    backend.getDagRuns.mockResolvedValue({ runs: [] });
    backend.getDagRun.mockResolvedValue({ run: null, nodes: [] });
    backend.applyDagOps.mockResolvedValue({ newVersion: 8 });

    renderWorkflowPage();

    expect(await screen.findByDisplayValue('test_app')).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText('命令卡片'), { target: { value: 'test_app_v2' } });
    fireEvent.change(screen.getByLabelText('Agent Key'), { target: { value: 'verifier_v2' } });
    fireEvent.change(screen.getByLabelText('Prompt Key'), { target: { value: 'main/verifier_v2' } });
    fireEvent.change(screen.getByLabelText('执行 cwd'), { target: { value: '/repo/review' } });
    fireEvent.change(screen.getByLabelText('输出 sharedfile'), { target: { value: 'reports/review.md' } });
    fireEvent.click(screen.getByRole('button', { name: '保存步骤' }));

    await waitFor(() => expect(backend.applyDagOps).toHaveBeenCalled());
    const patch = backend.applyDagOps.mock.calls[0][0].ops[0].patch;
    expect(patch.config).toMatchObject({
      exec: {
        automation: {
          kind: 'command_card',
          command_ref: 'test_app_v2',
        },
        verifier: {
          provider: 'claude',
          model: 'opus',
          agent_key: 'verifier_v2',
          prompt_key: 'main/verifier_v2',
          cwd: '/repo/review',
        },
      },
      outputs: {
        to_sharedfile: { path: 'reports/review.md', lock_mode: 'exclusive' },
      },
    });
    expect(patch.config.exec).not.toHaveProperty('agent_key');
  });

  it('fails fast before saving invalid node schema settings', async () => {
    const dag = {
      dag_key: 'daily-brief',
      title: 'Daily Brief',
      status: 'ready',
      trigger: 'manual',
      version: 7,
    };
    backend.getDashboardPage.mockResolvedValue({ dags: [dag] });
    backend.getDagDetail.mockResolvedValue({
      dag,
      nodes: [{
        node_key: 'verify',
        title: 'Verify',
        node_type: 'hybrid',
        assigned_to: 'worker',
        depends_on: [],
        config: {
          exec: {
            automation: { kind: 'command_card', command_ref: 'test_app' },
            verifier: { agent_key: 'reviewer', cwd: '/repo/app' },
          },
        },
      }],
    });
    backend.getDagRuns.mockResolvedValue({ runs: [] });
    backend.getDagRun.mockResolvedValue({ run: null, nodes: [] });
    backend.applyDagOps.mockResolvedValue({ newVersion: 8 });

    renderWorkflowPage();

    expect(await screen.findByDisplayValue('test_app')).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText('命令卡片'), { target: { value: '' } });
    fireEvent.click(screen.getByRole('button', { name: '保存步骤' }));

    expect(await screen.findByRole('alert')).toHaveTextContent('config.exec.command_ref 不能为空');
    expect(backend.applyDagOps).not.toHaveBeenCalled();
  });

  it('saves schedule cron expressions with the backend timezone prefix', async () => {
    mockWorkflowDag();
    backend.applyDagOps.mockResolvedValue({ newVersion: 8 });

    renderWorkflowPage();

    fireEvent.click(await screen.findByRole('button', { name: '创建定时任务' }));
    fireEvent.change(screen.getByLabelText('运行时间'), { target: { value: '05:00' } });
    fireEvent.click(screen.getAllByRole('button', { name: '创建定时任务' }).at(-1));

    await waitFor(() => expect(backend.applyDagOps).toHaveBeenCalled());
    expect(backend.applyDagOps.mock.calls[0][0].ops[0].patch).toMatchObject({
      trigger: 'scheduled',
      cron_expr: 'CRON_TZ=Asia/Shanghai 0 5 * * *',
    });
  });

  it('starts the generic AI designer flow without the Douyin template action', async () => {
    mockWorkflowDag();
    backend.startThread.mockResolvedValue({ thread_id: 'thread-design' });
    const store = {
      resolveLaunchPreferences: vi.fn().mockResolvedValue({
        modelProvider: 'codex',
        model: 'gpt-5.5',
        effort: 'high',
      }),
      setActiveThread: vi.fn(),
      setActivePage: vi.fn(),
    };

    renderWorkflowPage(store);

    expect(await screen.findByRole('button', { name: '通过聊天创建' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '抖音 5 点模板' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '通过聊天创建' }));

    await waitFor(() => {
      expect(backend.startThread).toHaveBeenCalledWith(expect.objectContaining({
        cwd: '/repo/app',
        name: 'AI 设计流程',
        agentKey: 'dag_designer',
        promptKey: 'main/dag_designer_zh',
        deferSpawn: true,
      }));
    });
    expect(backend.startTurn).not.toHaveBeenCalled();
    expect(store.setActiveThread).toHaveBeenCalledWith('thread-design');
    expect(store.setActivePage).toHaveBeenCalledWith('chat');
  });

  it('opens a DAG node child conversation through the explicit thread opener', async () => {
    const dag = {
      dag_key: 'daily-brief',
      title: 'Daily Brief',
      status: 'ready',
      trigger: 'manual',
      version: 7,
    };
    backend.getDashboardPage.mockResolvedValue({ dags: [dag] });
    backend.getDagDetail.mockResolvedValue({
      dag,
      nodes: [{
        node_key: 'review',
        title: 'Review',
        node_type: 'agent',
        status: 'done',
        assigned_to: 'codex',
        spawning_thread_id: 'agent_child_1',
        config: { prompt: '请评审这个方案' },
        result: '评审完成：可以继续。',
      }],
    });
    backend.getDagRuns.mockResolvedValue({ runs: [] });
    backend.getDagRun.mockResolvedValue({ run: null, nodes: [] });
    const store = {
      openThreadById: vi.fn().mockResolvedValue(true),
      setActiveThread: vi.fn(),
      setActivePage: vi.fn(),
    };

    renderWorkflowPage(store);

    fireEvent.click(await screen.findByRole('button', { name: '查看对话' }));

    await waitFor(() => {
      expect(store.openThreadById).toHaveBeenCalledWith('agent_child_1', {
        source: 'dag-node',
        dagNode: expect.objectContaining({
          nodeKey: 'review',
          title: 'Review',
          config: { prompt: '请评审这个方案' },
          result: '评审完成：可以继续。',
        }),
      });
    });
    expect(store.setActiveThread).not.toHaveBeenCalled();
    await waitFor(() => {
      expect(store.setActivePage).toHaveBeenCalledWith('chat');
    });
  });

  it('keeps the workflow page active when a DAG node child conversation cannot be opened', async () => {
    const dag = {
      dag_key: 'daily-brief',
      title: 'Daily Brief',
      status: 'ready',
      trigger: 'manual',
      version: 7,
    };
    backend.getDashboardPage.mockResolvedValue({ dags: [dag] });
    backend.getDagDetail.mockResolvedValue({
      dag,
      nodes: [{
        node_key: 'review',
        title: 'Review',
        node_type: 'agent',
        status: 'failed',
        assigned_to: 'codex',
        spawning_thread_id: 'agent_child_1',
      }],
    });
    backend.getDagRuns.mockResolvedValue({ runs: [] });
    backend.getDagRun.mockResolvedValue({ run: null, nodes: [] });
    const store = {
      openThreadById: vi.fn().mockResolvedValue(false),
      setActiveThread: vi.fn(),
      setActivePage: vi.fn(),
    };

    renderWorkflowPage(store);

    fireEvent.click(await screen.findByRole('button', { name: '查看对话' }));

    await waitFor(() => {
      expect(store.openThreadById).toHaveBeenCalledWith('agent_child_1', {
        source: 'dag-node',
        dagNode: expect.objectContaining({ nodeKey: 'review', title: 'Review' }),
      });
    });
    expect(store.setActiveThread).not.toHaveBeenCalled();
    expect(store.setActivePage).not.toHaveBeenCalled();
  });
});
