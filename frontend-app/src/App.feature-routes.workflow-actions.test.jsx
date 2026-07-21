import { installAppTestHooks, testEnv } from "./test-utils/appTestHarness.jsx";

installAppTestHooks();
const {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
  expect,
  it,
  App,
  backend,
  waitForBackendThreadHeading,
  mockWorkflowDagLifecycle,
  openWorkflowDashboard,
  runAndStopWorkflowDag,
  createWorkflowSchedule,
  editWorkflowStep,
  deleteWorkflowDag,
  designWorkflowWithAi,
} = testEnv;

it('allows selecting an empty DAG category and shows an empty state', async () => {
  const scheduledDag = {
    dag_key: 'weekly-report',
    title: 'Weekly Report',
    description: '每周报告',
    status: 'ready',
    trigger: 'scheduled',
    cron_expr: '0 8 * * 1',
    version: 3,
  };
  backend.getDashboardPage.mockImplementation(({ page }) => Promise.resolve(
    page === 'dags' ? { dags: [scheduledDag] } : { skills: [] },
  ));
  backend.getDagDetail.mockResolvedValue({ dag: scheduledDag, nodes: [] });
  backend.getDagRuns.mockResolvedValue({ runs: [] });

  render(<App />);
  await waitForBackendThreadHeading();
  fireEvent.click(screen.getByLabelText('自动化'));

  await waitFor(() => {
    expect(screen.getByRole('tab', { name: '定时任务 1' })).toHaveAttribute('aria-selected', 'true');
  });
  fireEvent.click(screen.getByRole('tab', { name: '进行中 0' }));

  await waitFor(() => {
    expect(screen.getByRole('tab', { name: '进行中 0' })).toHaveAttribute('aria-selected', 'true');
  });
  expect(screen.getByText('无任务')).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: /Weekly Report/ })).not.toBeInTheDocument();
  });

it('presents workflow schedules without raw cron or DAG internals', async () => {
  const scheduledDag = {
    dag_key: 'daily_remote_main_pr_review',
    title: '每日远程 main PR 审核',
    status: 'ready',
    trigger: 'scheduled',
    cron_expr: '0 1 * * *',
    next_run_at: '2026-06-01T01:00:00Z',
    version: 7,
  };
  backend.getDashboardPage.mockImplementation(({ page }) => Promise.resolve(
    page === 'dags' ? { dags: [scheduledDag] } : { skills: [] },
  ));
  backend.getDagDetail.mockResolvedValue({ dag: scheduledDag, nodes: [] });
  backend.getDagRuns.mockResolvedValue({ runs: [] });

  render(<App />);
  await waitForBackendThreadHeading();
  fireEvent.click(screen.getByLabelText('自动化'));

  await waitFor(() => {
    expect(screen.getByRole('tab', { name: '定时任务 1' })).toHaveAttribute('aria-selected', 'true');
  });
  expect(screen.getAllByText('每天 01:00').length).toBeGreaterThanOrEqual(1);
  expect(screen.getByText('已启用')).toBeInTheDocument();
  expect(screen.queryByText('0 1 * * *')).not.toBeInTheDocument();
  expect(screen.queryByText('daily_remote_main_pr_review')).not.toBeInTheDocument();

  fireEvent.click(screen.getByRole('button', { name: '修改计划' }));
  const dialog = await screen.findByRole('dialog', { name: '修改计划' });
  expect(within(dialog).queryByLabelText('Cron 表达式')).not.toBeInTheDocument();
  expect(within(dialog).getByLabelText('运行频率')).toHaveValue('daily');
  expect(within(dialog).getByLabelText('运行时间')).toHaveValue('01:00');
  });

it('runs, stops, deletes, schedules, edits and designs DAGs through the old RPC surface', async () => {
  mockWorkflowDagLifecycle();

  await openWorkflowDashboard();
  await runAndStopWorkflowDag();
  await createWorkflowSchedule();

  await editWorkflowStep();
  await deleteWorkflowDag();
  await designWorkflowWithAi();
  });

it('uses the active-run query, not stale selected run detail, to unlock DAG controls after stop', async () => {
  const dag = {
    dag_key: 'daily-brief',
    title: 'Daily Brief',
    status: 'ready',
    trigger: 'manual',
    version: 7,
  };
  const agentNode = {
    node_key: 'draft',
    title: '起草',
    node_type: 'agent',
    assigned_to: 'agent-a',
    depends_on: [],
    config: {},
  };
  let hasActiveRun = true;
  backend.getDashboardPage.mockImplementation(({ page }) => Promise.resolve(
    page === 'dags' ? { dags: [dag] } : { skills: [] },
  ));
  backend.getDagDetail.mockResolvedValue({ dag, nodes: [agentNode] });
  backend.getDagRuns.mockImplementation(({ status }) => {
    if (status === 'running') {
      return Promise.resolve({ runs: hasActiveRun ? [{ run_key: 'run-live', status: 'running' }] : [] });
    }
    return Promise.resolve({ runs: [{ run_key: 'run-live', status: hasActiveRun ? 'running' : 'cancelled' }] });
  });
  backend.getDagRun.mockResolvedValue({ run: { run_key: 'run-live', status: 'running' }, nodes: [agentNode] });
  backend.terminateDagRun.mockImplementation(() => {
    hasActiveRun = false;
    return Promise.resolve({ ok: true });
  });

  render(<App />);
  await waitForBackendThreadHeading();
  fireEvent.click(screen.getByLabelText('自动化'));
  expect(await screen.findByRole('button', { name: '停止运行' })).toBeInTheDocument();

  fireEvent.click(screen.getByRole('button', { name: '停止运行' }));
  await waitFor(() => {
    expect(backend.terminateDagRun).toHaveBeenCalledWith({
      dagKey: 'daily-brief',
      runKey: 'run-live',
      reason: 'user_requested',
    });
  });

  await waitFor(() => {
    expect(screen.queryByRole('button', { name: '停止运行' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: '运行' })).not.toBeDisabled();
  });
  });

it('blocks scheduling when root DAG steps have no assignee', async () => {
  const dag = {
    dag_key: 'daily-brief',
    title: 'Daily Brief',
    status: 'ready',
    trigger: 'manual',
    version: 7,
  };
  const unassignedRoot = {
    node_key: 'draft',
    title: '起草',
    node_type: 'agent',
    assigned_to: '',
    depends_on: [],
    config: { provider: 'codex', model: 'gpt-5', first_turn: '请起草简报' },
  };
  backend.getDashboardPage.mockImplementation(({ page }) => Promise.resolve(
    page === 'dags' ? { dags: [dag] } : { skills: [] },
  ));
  backend.getDagDetail.mockResolvedValue({ dag, nodes: [unassignedRoot] });
  backend.getDagRuns.mockResolvedValue({ runs: [] });

  render(<App />);
  await waitForBackendThreadHeading();
  fireEvent.click(screen.getByLabelText('自动化'));
  expect((await screen.findAllByText('Daily Brief')).length).toBeGreaterThanOrEqual(1);

  expect(await screen.findByText('首个步骤「起草」缺少执行者，请先在高级设置中填写执行者。')).toBeInTheDocument();
  expect(screen.getByRole('button', { name: '运行' })).toBeDisabled();

  fireEvent.click(screen.getByRole('button', { name: '创建定时任务' }));
  const scheduleDialog = await screen.findByRole('dialog', { name: '创建定时任务' });
  fireEvent.click(within(scheduleDialog).getByRole('button', { name: '创建定时任务' }));

  expect(await screen.findByRole('alert')).toHaveTextContent('保存定时任务失败：首个步骤「起草」缺少执行者');
  expect(backend.applyDagOps).not.toHaveBeenCalled();
  });

it('keeps workflow action notices scoped to the selected task', async () => {
  const firstDag = {
    dag_key: 'flow-a',
    title: '流程 A',
    status: 'ready',
    trigger: 'manual',
    version: 7,
  };
  const secondDag = {
    dag_key: 'flow-b',
    title: '流程 B',
    status: 'ready',
    trigger: 'manual',
    version: 8,
  };
  backend.getDashboardPage.mockImplementation(({ page }) => Promise.resolve(
    page === 'dags' ? { dags: [firstDag, secondDag] } : { skills: [] },
  ));
  backend.getDagDetail.mockImplementation(({ dagKey }) => Promise.resolve({
    dag: dagKey === 'flow-a' ? firstDag : secondDag,
    nodes: [{
      node_key: 'draft',
      title: dagKey === 'flow-a' ? '步骤 A' : '步骤 B',
      node_type: 'agent',
      status: 'pending',
      depends_on: [],
      config: {},
    }],
  }));
  backend.getDagRuns.mockResolvedValue({ runs: [] });
  backend.applyDagOps.mockResolvedValue({ newVersion: 8 });

  render(<App />);
  await waitForBackendThreadHeading();
  fireEvent.click(screen.getByLabelText('自动化'));
  expect((await screen.findAllByText('流程 A')).length).toBeGreaterThanOrEqual(2);
  expect((await screen.findAllByText('步骤 A')).length).toBeGreaterThanOrEqual(1);

  fireEvent.click(screen.getByText('高级设置'));
  fireEvent.click(screen.getByRole('button', { name: '保存步骤' }));
  await waitFor(() => {
    expect(screen.getByText('已保存步骤 步骤 A')).toBeInTheDocument();
  });

  fireEvent.click(screen.getByRole('button', { name: /流程 B/ }));
  await waitFor(() => {
    expect(screen.getByRole('heading', { name: '流程 B' })).toBeInTheDocument();
  });
  expect((await screen.findAllByText('步骤 B')).length).toBeGreaterThanOrEqual(1);
  await waitFor(() => {
    expect(screen.queryByText('已保存步骤 步骤 A')).not.toBeInTheDocument();
  });

  fireEvent.click(screen.getByRole('button', { name: /流程 A/ }));
  await waitFor(() => {
    expect(screen.getByRole('heading', { name: '流程 A' })).toBeInTheDocument();
  });
  expect((await screen.findAllByText('步骤 A')).length).toBeGreaterThanOrEqual(1);
  await waitFor(() => {
    expect(screen.queryByText('已保存步骤 步骤 A')).not.toBeInTheDocument();
  });
  });
