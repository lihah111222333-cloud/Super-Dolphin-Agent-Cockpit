import { installAppTestHooks, testEnv } from "./test-utils/appTestHarness.jsx";

installAppTestHooks();
const {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
  expect,
  it,
  vi,
  frontendHealthSnapshot,
  App,
  backend,
  deferred,
  waitForBackendThreadHeading,
} = testEnv;

it('keeps cached workflow data visible and exposes retry when a background sync fails', async () => {
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

  backend.getDashboardPage.mockRejectedValueOnce(new Error('workflow backend offline'));
  await act(async () => {
    backend.__bridgeCallback?.({ type: 'task/node/statusChanged', payload: { dag_key: 'flow-a', run_key: 'run-a', node_key: 'step', new_status: 'running' } });
    await Promise.resolve();
  });

  expect(screen.getAllByText('流程 A').length).toBeGreaterThanOrEqual(1);
  const alert = await screen.findByRole('alert');
  expect(alert).toHaveTextContent('同步自动化失败，当前显示上次成功数据。');
  expect(alert).not.toHaveTextContent('workflow backend offline');
  expect(frontendHealthSnapshot()).toEqual(expect.arrayContaining([
    expect.objectContaining({ actionId: 'workflow.dashboard.load', diagnosticId: expect.any(String) }),
  ]));

  dags = [{
    dag_key: 'flow-b',
    title: '流程 B',
    status: 'running',
    trigger: 'manual',
    version: 2,
    latest_run: { run_key: 'run-b', status: 'running' },
  }];
  fireEvent.click(screen.getByRole('button', { name: '重试同步' }));

  expect((await screen.findAllByText('流程 B')).length).toBeGreaterThanOrEqual(1);
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

it('clears a stale workflow sync alert after focus refresh succeeds', async () => {
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

  backend.getDashboardPage.mockRejectedValueOnce(new Error('workflow backend offline'));
  await act(async () => {
    backend.__bridgeCallback?.({ type: 'task/node/statusChanged', payload: { dag_key: 'flow-a', run_key: 'run-a', node_key: 'step', new_status: 'running' } });
    await Promise.resolve();
  });
  const alert = await screen.findByRole('alert');
  expect(alert).toHaveTextContent('同步自动化失败，当前显示上次成功数据。');
  expect(alert).not.toHaveTextContent('workflow backend offline');

  dags = [{
    dag_key: 'flow-b',
    title: '流程 B',
    status: 'running',
    trigger: 'manual',
    version: 2,
    latest_run: { run_key: 'run-b', status: 'running' },
  }];
  await act(async () => {
    window.dispatchEvent(new Event('focus'));
  });

  expect((await screen.findAllByText('流程 B')).length).toBeGreaterThanOrEqual(1);
  await waitFor(() => {
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });
  });

it('coalesces selected workflow detail and run refreshes when events and retry overlap', async () => {
  const runningDag = {
    dag_key: 'flow-a',
    title: '流程 A',
    status: 'running',
    trigger: 'manual',
    version: 1,
    latest_run: { run_key: 'run-a', status: 'running' },
  };
  const node = { node_key: 'step', title: '步骤 A', node_type: 'agent', status: 'running', depends_on: [], config: {} };
  backend.getDashboardPage.mockImplementation(({ page }) => Promise.resolve(
    page === 'dags' ? { dags: [runningDag] } : { skills: [] },
  ));
  backend.getDagDetail.mockResolvedValue({ dag: runningDag, nodes: [node] });
  backend.getDagRuns.mockImplementation(({ status }) => Promise.resolve({
    runs: status === 'running' ? [runningDag.latest_run] : [runningDag.latest_run],
  }));
  backend.getDagRun.mockResolvedValue({ run: runningDag.latest_run, nodes: [node] });

  render(<App />);
  await waitForBackendThreadHeading();
  fireEvent.click(screen.getByLabelText('自动化'));
  expect((await screen.findAllByText('流程 A')).length).toBeGreaterThanOrEqual(1);
  expect((await screen.findAllByText('步骤 A')).length).toBeGreaterThanOrEqual(1);

  vi.clearAllMocks();
  const detailRefresh = deferred();
  const recentRunsRefresh = deferred();
  const activeRunsRefresh = deferred();
  const runRefresh = deferred();
  backend.getDashboardPage
    .mockImplementationOnce(({ page }) => (
      page === 'dags' ? Promise.reject(new Error('workflow backend offline')) : Promise.resolve({ skills: [] })
    ))
    .mockImplementation(({ page }) => Promise.resolve(
      page === 'dags' ? { dags: [runningDag] } : { skills: [] },
    ));
  backend.getDagDetail.mockImplementation(() => detailRefresh.promise);
  backend.getDagRuns.mockImplementation(({ status }) => (
    status === 'running' ? activeRunsRefresh.promise : recentRunsRefresh.promise
  ));
  backend.getDagRun.mockImplementation(() => runRefresh.promise);

  await act(async () => {
    backend.__bridgeCallback?.({ type: 'task/node/statusChanged', payload: { dag_key: 'flow-a', run_key: 'run-a', node_key: 'step', new_status: 'running' } });
    await Promise.resolve();
  });
  const alert = await screen.findByRole('alert');
  expect(alert).toHaveTextContent('同步自动化失败，当前显示上次成功数据。');
  expect(alert).not.toHaveTextContent('workflow backend offline');
  await waitFor(() => expect(backend.getDagDetail).toHaveBeenCalledTimes(1));

  fireEvent.click(within(alert).getByRole('button', { name: '重试同步' }));
  await act(async () => {
    backend.__bridgeCallback?.({ type: 'task/node/statusChanged', payload: { dag_key: 'flow-a', run_key: 'run-a', node_key: 'step', new_status: 'running' } });
    await Promise.resolve();
    await Promise.resolve();
  });
  await waitFor(() => expect(backend.getDashboardPage.mock.calls.length).toBeGreaterThanOrEqual(2));

  expect(backend.getDagDetail).toHaveBeenCalledTimes(1);
  expect(backend.getDagRuns).toHaveBeenCalledTimes(2);
  expect(backend.getDagRun).toHaveBeenCalledTimes(1);

  await act(async () => {
    detailRefresh.reject(new Error('detail backend offline'));
    recentRunsRefresh.resolve({ runs: [runningDag.latest_run] });
    activeRunsRefresh.resolve({ runs: [runningDag.latest_run] });
    runRefresh.resolve({ run: runningDag.latest_run, nodes: [node] });
  });

  const detailAlert = await screen.findByRole('alert');
  expect(detailAlert).toHaveTextContent('同步自动化失败，当前显示上次成功数据。');
  expect(detailAlert).not.toHaveTextContent('detail backend offline');
  });

it('shows a retryable blocking error instead of an empty workflow state on initial load failure', async () => {
  const flow = {
    dag_key: 'flow-a',
    title: '流程 A',
    status: 'running',
    trigger: 'manual',
    version: 1,
    latest_run: { run_key: 'run-a', status: 'running' },
  };
  backend.getDashboardPage.mockImplementation(({ page }) => (
    page === 'dags'
      ? Promise.reject(new Error('workflow backend offline'))
      : Promise.resolve({ skills: [] })
  ));

  render(<App />);
  await waitForBackendThreadHeading();
  fireEvent.click(screen.getByLabelText('自动化'));

  const alert = await screen.findByRole('alert');
  expect(alert).toHaveTextContent('加载自动化失败，请重试。');
  expect(alert).not.toHaveTextContent('workflow backend offline');
  expect(frontendHealthSnapshot()).toEqual(expect.arrayContaining([
    expect.objectContaining({ actionId: 'workflow.dashboard.load', diagnosticId: expect.any(String) }),
  ]));
  expect(screen.queryByText('无任务')).not.toBeInTheDocument();

  backend.getDashboardPage.mockImplementation(({ page }) => (
    page === 'dags' ? Promise.resolve({ dags: [flow] }) : Promise.resolve({ skills: [] })
  ));
  backend.getDagDetail.mockResolvedValue({
    dag: flow,
    nodes: [{ node_key: 'step', title: '步骤 A', node_type: 'agent', status: 'running', depends_on: [], config: {} }],
  });
  backend.getDagRuns.mockResolvedValue({ runs: [{ run_key: 'run-a', status: 'running' }] });
  backend.getDagRun.mockResolvedValue({ run: { run_key: 'run-a', status: 'running' }, nodes: [] });
  fireEvent.click(within(alert).getByRole('button', { name: '重试同步' }));

  expect((await screen.findAllByText('流程 A')).length).toBeGreaterThanOrEqual(1);
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

it('keeps cached workflow data visible when navigating back and refreshes silently', async () => {
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
  backend.getDagRuns.mockImplementation(({ dagKey }) => {
    const dag = dags.find((item) => item.dag_key === dagKey) || dags[0];
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

  fireEvent.click(screen.getByLabelText('新对话'));
  dags = [{
    dag_key: 'flow-b',
    title: '流程 B',
    status: 'running',
    trigger: 'manual',
    version: 2,
    latest_run: { run_key: 'run-b', status: 'running' },
  }];
  fireEvent.click(screen.getByLabelText('自动化'));

  expect(screen.queryByText('正在加载自动化...')).not.toBeInTheDocument();
  expect(screen.queryByText('正在加载详情...')).not.toBeInTheDocument();
  expect(screen.getAllByText('流程 A').length).toBeGreaterThanOrEqual(1);
  expect((await screen.findAllByText('流程 B')).length).toBeGreaterThanOrEqual(1);
  expect(screen.queryByText('流程 A')).not.toBeInTheDocument();
  });
