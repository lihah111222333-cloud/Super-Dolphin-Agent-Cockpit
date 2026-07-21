import { installAppTestHooks, testEnv } from "./test-utils/appTestHarness.jsx";

installAppTestHooks();
const {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  expect,
  it,
  vi,
  App,
  backend,
  waitForBackendThreadHeading,
} = testEnv;

it('loads DAG list, detail, runs and selected run through legacy dashboard RPCs', async () => {
  const runningDag = {
    dag_key: 'daily-brief',
    title: 'Daily Brief',
    description: '每日简报',
    status: 'ready',
    trigger: 'manual',
    version: 7,
    latest_run: { run_key: 'run-1', status: 'running', metadata: { final_output: '正在汇总' } },
  };
  backend.getDashboardPage.mockImplementation(({ page }) => Promise.resolve(
    page === 'dags'
      ? {
        dags: [
          runningDag,
          { dag_key: 'weekly-report', title: 'Weekly Report', status: 'ready', trigger: 'scheduled', cron_expr: '0 8 * * 1', next_run_at: '2026-06-01T00:00:00Z' },
          { dag_key: 'done-flow', title: 'Done Flow', status: 'done', trigger: 'manual', latest_run: { run_key: 'run-done', status: 'done' } },
        ],
      }
      : { skills: [] },
  ));
  backend.getDagDetail.mockResolvedValue({
    dag: runningDag,
    nodes: [
      { node_key: 'draft', title: '起草', node_type: 'agent', status: 'running', depends_on: [], config: { provider: 'codex', model: 'gpt-5' } },
    ],
  });
  backend.getDagRuns.mockImplementation(({ status }) => Promise.resolve({
    runs: status === 'running'
      ? [{ run_key: 'run-1', status: 'running', metadata: { final_output: '正在汇总' } }]
      : [
        { run_key: 'run-1', status: 'running', metadata: { final_output: '正在汇总' } },
        { run_key: 'run-0', status: 'done' },
      ],
  }));
  backend.getDagRun.mockResolvedValue({
    run: { run_key: 'run-1', status: 'running', metadata: { final_output: { text: '最终简报完成' } } },
    nodes: [{ node_key: 'draft', status: 'running' }],
  });

  render(<App />);
  await waitForBackendThreadHeading();
  fireEvent.click(screen.getByLabelText('自动化'));

  expect((await screen.findAllByText('Daily Brief')).length).toBeGreaterThanOrEqual(2);
  expect(screen.getByRole('tab', { name: '进行中 1' })).toBeInTheDocument();
  expect(screen.getByRole('tab', { name: '定时任务 1' })).toBeInTheDocument();
  expect(screen.getByRole('tab', { name: '历史记录 1' })).toBeInTheDocument();
  expect(await screen.findByText('最终简报完成')).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: /刷新/ })).not.toBeInTheDocument();

  await waitFor(() => {
    expect(backend.getDashboardPage).toHaveBeenCalledWith({ cwd: '/repo/app', page: 'dags' });
    expect(backend.getDagDetail).toHaveBeenCalledWith({ dagKey: 'daily-brief' });
    expect(backend.getDagRuns).toHaveBeenCalledWith({ dagKey: 'daily-brief', limit: 30 });
    expect(backend.getDagRuns).toHaveBeenCalledWith({ dagKey: 'daily-brief', status: 'running', limit: 1 });
    expect(backend.getDagRun).toHaveBeenCalledWith({ runKey: 'run-1' });
  });
  });

it('renders workflow topology, shared files, and readable final output file panels', async () => {
  const dag = {
    dag_key: 'report-flow',
    title: '报告流水线',
    status: 'succeeded',
    trigger: 'manual',
    version: 3,
    latest_run: { run_key: 'run-report', status: 'succeeded' },
  };
  const nodes = [{
    node_key: 'collect',
    title: '收集资料',
    status: 'succeeded',
    config: { outputs: { to_sharedfile: { path: 'brief/raw.md', lock_mode: 'exclusive' } } },
  }, {
    node_key: 'write',
    title: '撰写报告',
    status: 'succeeded',
    depends_on: ['collect', 'external-input'],
    config: {
      inputs: { from_sharedfiles: ['brief/raw.md'] },
      outputs: { to_sharedfile: { path: 'reports/final.md', lock_mode: 'append' } },
    },
  }];
  backend.getDashboardPage.mockImplementation(({ page }) => (
    page === 'dags' ? Promise.resolve({ dags: [dag] }) : Promise.resolve({ skills: [] })
  ));
  backend.getDagDetail.mockResolvedValue({ dag, nodes });
  backend.getDagRuns.mockImplementation(({ status }) => Promise.resolve({
    runs: status === 'running' ? [] : [{ run_key: 'run-report', status: 'succeeded', metadata: { final_output: { kind: 'sharedfile', path: 'reports/final.md' } } }],
  }));
  backend.getDagRun.mockResolvedValue({
    run: { run_key: 'run-report', status: 'succeeded', metadata: { final_output: { kind: 'sharedfile', path: 'reports/final.md' } } },
    nodes,
  });
  backend.readSharedFile.mockResolvedValue({ path: 'reports/final.md', content: '最终报告正文' });

  render(<App />);
  await waitForBackendThreadHeading();
  fireEvent.click(screen.getByLabelText('自动化'));

  expect((await screen.findAllByText('报告流水线')).length).toBeGreaterThanOrEqual(2);
  expect(await screen.findByText('流程图')).toBeInTheDocument();
  expect((await screen.findAllByText(/收集资料/)).length).toBeGreaterThanOrEqual(1);
  expect(await screen.findByText(/收集资料 --> 撰写报告/)).toBeInTheDocument();
  expect(await screen.findByText(/外部依赖 1 --> 撰写报告/)).toBeInTheDocument();

  expect(screen.getByText('工作文件')).toBeInTheDocument();
  expect(screen.getAllByText('brief/raw.md').length).toBeGreaterThanOrEqual(1);
  expect(screen.getAllByText('reports/final.md').length).toBeGreaterThanOrEqual(2);
  expect(screen.getByText('读取')).toBeInTheDocument();
  expect(screen.getByText('写入 · 追加写入')).toBeInTheDocument();

  fireEvent.click(screen.getByRole('button', { name: '读取最终结果' }));
  await waitFor(() => {
    expect(backend.readSharedFile).toHaveBeenCalledWith({ path: 'reports/final.md' });
  });
  expect(await screen.findByText('最终报告正文')).toBeInTheDocument();
  });

it('auto-updates workflow page without a manual refresh button', async () => {
  let dags = [{
    dag_key: 'flow-a',
    title: '流程 A',
    status: 'running',
    trigger: 'manual',
    version: 1,
    latest_run: { run_key: 'run-a', status: 'running' },
  }];
  backend.getDashboardPage.mockImplementation(({ page }) => Promise.resolve(
    page === 'dags' ? { dags } : { skills: [] },
  ));
  backend.getDagDetail.mockImplementation(({ dagKey }) => {
    const dag = dags.find((item) => item.dag_key === dagKey) || dags[0];
    const suffix = (dag?.title || '').split(' ').pop() || '';
    return Promise.resolve({
      dag,
      nodes: [{ node_key: 'step', title: `步骤 ${suffix}`, node_type: 'agent', status: 'running', depends_on: [], config: {} }],
    });
  });
  backend.getDagRuns.mockImplementation(({ dagKey, status }) => {
    const dag = dags.find((item) => item.dag_key === dagKey) || dags[0];
    if (status === 'running') return Promise.resolve({ runs: dag?.latest_run ? [dag.latest_run] : [] });
    return Promise.resolve({ runs: dag?.latest_run ? [dag.latest_run] : [] });
  });
  backend.getDagRun.mockImplementation(({ runKey }) => Promise.resolve({
    run: { run_key: runKey, status: 'running' },
    nodes: [],
  }));

  render(<App />);
  await waitForBackendThreadHeading();
  fireEvent.click(screen.getByLabelText('自动化'));

  expect((await screen.findAllByText('流程 A')).length).toBeGreaterThanOrEqual(1);
  expect(screen.queryByRole('button', { name: /刷新/ })).not.toBeInTheDocument();

  dags = [{
    dag_key: 'flow-b',
    title: '流程 B',
    status: 'running',
    trigger: 'manual',
    version: 2,
    latest_run: { run_key: 'run-b', status: 'running' },
  }];
  await act(async () => {
    backend.__bridgeCallback?.({ type: 'task/node/statusChanged', payload: { dag_key: 'flow-b', run_key: 'run-b', node_key: 'step', new_status: 'running' } });
  });

  expect((await screen.findAllByText('流程 B')).length).toBeGreaterThanOrEqual(1);
  expect(screen.queryByText('流程 A')).not.toBeInTheDocument();

  dags = [{
    dag_key: 'flow-c',
    title: '流程 C',
    status: 'running',
    trigger: 'manual',
    version: 3,
    latest_run: { run_key: 'run-c', status: 'running' },
  }];
  await act(async () => {
    window.dispatchEvent(new Event('focus'));
  });

  expect((await screen.findAllByText('流程 C')).length).toBeGreaterThanOrEqual(1);
  expect(screen.queryByText('流程 B')).not.toBeInTheDocument();
  });

it('does not poll workflow data with a page interval', async () => {
  const intervalSpy = vi.spyOn(window, 'setInterval');
  try {
    const runningDag = {
      dag_key: 'flow-a',
      title: '流程 A',
      status: 'running',
      trigger: 'manual',
      version: 1,
      latest_run: { run_key: 'run-a', status: 'running' },
    };
    backend.getDashboardPage.mockImplementation(({ page }) => Promise.resolve(
      page === 'dags' ? { dags: [runningDag] } : { skills: [] },
    ));
    backend.getDagDetail.mockResolvedValue({
      dag: runningDag,
      nodes: [{ node_key: 'step', title: '步骤 A', node_type: 'agent', status: 'running', depends_on: [], config: {} }],
    });
    backend.getDagRuns.mockImplementation(({ status }) => Promise.resolve({
      runs: status === 'running' ? [{ run_key: 'run-a', status: 'running' }] : [{ run_key: 'run-a', status: 'running' }],
    }));
    backend.getDagRun.mockResolvedValue({
      run: { run_key: 'run-a', status: 'running' },
      nodes: [],
    });

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('自动化'));

    expect((await screen.findAllByText('流程 A')).length).toBeGreaterThanOrEqual(1);
    expect(intervalSpy.mock.calls.filter((call) => call[1] === 4000)).toHaveLength(0);
  }
  finally {
    intervalSpy.mockRestore();
  }
  });
