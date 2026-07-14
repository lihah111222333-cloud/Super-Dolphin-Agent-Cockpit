import { act, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import {
  WorkflowPage,
  backend,
  CancelledError,
  deferred,
  renderWorkflowPage,
  mockWorkflowDag,
  mockEnterpriseTemplates,
  enterpriseTemplateDetails,
  fillEnterpriseTemplateForm,
  openTemplateCatalog,
  workflowRunMetadataWithFinalOutput,
  workflowRunMetadataWithFinalFile,
  workflowRunMetadataWithoutFinalOutput,
  agentExecFixture,
} from './WorkflowPage.testSupport.jsx';

  beforeEach(() => {
  vi.clearAllMocks();
  mockEnterpriseTemplates();
  backend.writeWorkflowMaterial.mockImplementation(({ path }) => Promise.resolve({ path }));
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

it.each(['detail', 'runs'])('keeps DAG detail unpublished when the %s response validator rejects malformed data', async (wire) => {
  mockWorkflowDag();
  const message = `dashboard/${wire} response is malformed`;
  if (wire === 'detail') backend.getDagDetail.mockRejectedValue(new TypeError(message));
  else backend.getDagRuns.mockRejectedValue(new TypeError(message));

  renderWorkflowPage();

  expect(await screen.findByRole('alert')).toHaveTextContent(`加载自动化详情失败：${message}`);
  expect(screen.queryByText('Draft')).not.toBeInTheDocument();
  expect(screen.queryByText('run-malformed')).not.toBeInTheDocument();
});

it('keeps DAG run state unpublished when the run response validator rejects malformed data', async () => {
  mockWorkflowDag();
  backend.getDagRuns.mockImplementation((params = {}) => Promise.resolve({
    runs: params.status === 'running'
      ? [{ id: 88, run_key: 'run-malformed', status: 'running' }]
      : [{ id: 88, run_key: 'run-malformed', status: 'running' }],
  }));
  backend.getDagRun.mockRejectedValue(new TypeError('dashboard/dagRun response is malformed'));

  renderWorkflowPage();

  expect(await screen.findByRole('alert')).toHaveTextContent('加载自动化详情失败：dashboard/dagRun response is malformed');
  expect(screen.queryByText('Corrupted Runtime Node')).not.toBeInTheDocument();
});

it.each(['render', 'save'])('keeps template state unpublished when the %s response validator rejects malformed data', async (wire) => {
  mockWorkflowDag();
  const template = enterpriseTemplateDetails['government-enterprise/meeting-minutes'];
  const message = `workflowTemplates/${wire} response is malformed`;
  if (wire === 'render') {
    backend.renderWorkflowTemplateDraft.mockRejectedValue(new TypeError(message));
  } else {
    backend.renderWorkflowTemplateDraft.mockResolvedValue({
      draft: {
        template_id: template.id,
        template_version: template.version,
        dag_key: 'government_enterprise_meeting_minutes_run',
        title: '模板主题 - 会议纪要',
        description: '提取会议要点。',
        trigger: 'manual',
        final_node_key: 'final_minutes',
        nodes: template.dag_template.nodes,
        final_output: template.final_output,
      },
    });
    backend.saveWorkflowTemplate.mockRejectedValue(new TypeError(message));
  }

  renderWorkflowPage();
  await openTemplateCatalog();
  fireEvent.click(await screen.findByRole('button', { name: '选择会议纪要模板' }));
  await fillEnterpriseTemplateForm('模板主题', 'materials/source.md', '复核负责人');
  fireEvent.click(screen.getByRole('button', { name: '保存为模板' }));

  expect(await screen.findByRole('alert')).toHaveTextContent(`保存模板失败：${message}`);
  expect(backend.saveWorkflowTemplate).toHaveBeenCalledTimes(wire === 'save' ? 1 : 0);
  expect(screen.queryByText(/模板已保存为/)).not.toBeInTheDocument();
});

it('does not publish an uploaded material path when its response validator rejects', async () => {
  mockWorkflowDag();
  backend.writeWorkflowMaterial.mockRejectedValueOnce(new TypeError(
    'dashboard/workflowMaterialWrite response workflow material write response.path Expected string, received number',
  ));

  renderWorkflowPage();
  await openTemplateCatalog();
  fireEvent.click(await screen.findByRole('button', { name: '选择审批材料模板' }));
  const input = await screen.findByLabelText('输入材料');
  const dropTarget = input.closest('.enterprise-template-file-ref');
  const file = new File(['审批说明正文'], '审批材料.txt', { type: 'text/plain' });
  file.text = vi.fn().mockResolvedValue('审批说明正文');

  fireEvent.drop(dropTarget, { dataTransfer: { files: [file] } });

  expect(await screen.findByRole('alert')).toHaveTextContent('dashboard/workflowMaterialWrite');
  expect(input).toHaveValue('');
  expect(screen.queryByText('已上传 1 个材料文件')).not.toBeInTheDocument();
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
        metadata: workflowRunMetadataWithFinalOutput('reports/live.md'),
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
        metadata: workflowRunMetadataWithoutFinalOutput(),
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

it('shows refresh failure after a successful DAG start', async () => {
  mockWorkflowDag();
  backend.getDagDetail
    .mockResolvedValueOnce({
      dag: { dag_key: 'daily-brief', title: 'Daily Brief', status: 'ready', trigger: 'manual', version: 7 },
      nodes: [{ node_key: 'draft', title: 'Draft', node_type: 'agent', assigned_to: 'codex', depends_on: [] }],
    })
    .mockRejectedValueOnce(new Error('detail refresh offline'));

  renderWorkflowPage();

  fireEvent.click(await screen.findByRole('button', { name: '运行' }));

  const notice = await screen.findByText(/已启动自动化/);
  expect(notice).toHaveTextContent('但刷新状态失败：detail refresh offline');
});

it('shows blocked ready-node diagnostics and dispatches the runtime node with an assignee', async () => {
  mockWorkflowDag();
  backend.dispatchDagNode.mockResolvedValue({ malformed: ['ignored-response-body'] });
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
      config: { exec: agentExecFixture('writer', '/repo/app') },
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
  expect(await screen.findByText('已派发步骤 Draft')).toBeInTheDocument();
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
});

it('ignores the malformed terminate response body after stopping the active DAG run', async () => {
  mockWorkflowDag();
  backend.getDagRuns.mockImplementation((params = {}) => Promise.resolve({
    runs: params.status === 'running'
      ? [{ id: 91, run_key: 'run-active', status: 'running' }]
      : [{ id: 91, run_key: 'run-active', status: 'running' }],
  }));
  backend.getDagRun.mockResolvedValue({ run: { id: 91, run_key: 'run-active', status: 'running' }, nodes: [] });
  backend.terminateDagRun.mockResolvedValue({ malformed: ['ignored-response-body'] });

  renderWorkflowPage();
  fireEvent.click(await screen.findByRole('button', { name: '停止运行' }));

  await waitFor(() => expect(backend.terminateDagRun).toHaveBeenCalledWith({
    dagKey: 'daily-brief',
    runKey: 'run-active',
    reason: 'user_requested',
  }));
  expect(await screen.findByText('已停止运行')).toBeInTheDocument();
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
});

it('ignores the malformed delete response body after deleting a DAG', async () => {
  mockWorkflowDag();
  backend.getDashboardPage
    .mockResolvedValueOnce({ dags: [{ dag_key: 'daily-brief', title: 'Daily Brief', status: 'ready', trigger: 'manual', version: 7 }] })
    .mockResolvedValue({ dags: [] });
  backend.deleteDag.mockResolvedValue({ malformed: ['ignored-response-body'] });

  renderWorkflowPage();
  fireEvent.click(await screen.findByRole('button', { name: '删除' }));
  fireEvent.click(screen.getByRole('button', { name: '确认删除' }));

  await waitFor(() => expect(backend.deleteDag).toHaveBeenCalledWith({ dagKey: 'daily-brief' }));
  await waitFor(() => expect(screen.queryByRole('dialog', { name: '删除自动化' })).not.toBeInTheDocument());
  expect(await screen.findByText('创建首个自动化')).toBeInTheDocument();
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
});

it('keeps one run intent pending when startDag outlives the former timeout', async () => {
  mockWorkflowDag();
  const pendingStart = deferred();
  backend.startDag.mockReturnValue(pendingStart.promise);

  renderWorkflowPage();

  const runButton = await screen.findByRole('button', { name: '运行' });
  vi.useFakeTimers();
  fireEvent.click(runButton);

  expect(screen.getByRole('button', { name: '启动中...' })).toBeDisabled();
  expect(backend.startDag).toHaveBeenCalledTimes(1);
  const idempotencyKeys = backend.startDag.mock.calls.map(([payload]) => payload.idempotencyKey);

  await act(async () => {
    await vi.advanceTimersByTimeAsync(8000);
  });

  const pendingRunButton = screen.getByRole('button', { name: '启动中...' });
  expect(pendingRunButton).toBeDisabled();
  fireEvent.click(pendingRunButton);
  expect(backend.startDag).toHaveBeenCalledTimes(1);
  expect(idempotencyKeys).toHaveLength(1);

  vi.useRealTimers();
  await act(async () => {
    pendingStart.resolve({ run_key: 'run-deferred' });
    await pendingStart.promise;
  });

  expect(await screen.findByText('已启动自动化')).toBeInTheDocument();
  expect(screen.getByRole('button', { name: '运行' })).not.toBeDisabled();
});

it('reuses the run intent key when an uncertain transport failure is retried explicitly', async () => {
  mockWorkflowDag();
  const deliveredRuns = new Map();
  backend.startDag.mockImplementation(({ idempotencyKey }) => {
    if (deliveredRuns.size === 0) {
      deliveredRuns.set(idempotencyKey, { run_key: 'run-created-before-disconnect', execution_state: 'running' });
      return Promise.reject(new Error('transport disconnected after request delivery'));
    }
    const existingRun = deliveredRuns.get(idempotencyKey);
    return existingRun
      ? Promise.resolve(existingRun)
      : Promise.reject(new Error('duplicate run would be created with a different idempotency key'));
  });

  renderWorkflowPage();

  fireEvent.click(await screen.findByRole('button', { name: '运行' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('transport disconnected after request delivery');

  fireEvent.click(screen.getByRole('button', { name: '运行' }));

  expect(await screen.findByText('已启动自动化')).toBeInTheDocument();
  expect(backend.startDag).toHaveBeenCalledTimes(2);
  const [firstPayload, retryPayload] = backend.startDag.mock.calls.map(([payload]) => payload);
  expect(retryPayload.idempotencyKey).toBe(firstPayload.idempotencyKey);
  expect(retryPayload.idempotencyKey).toMatch(/^ui-/);
  expect(deliveredRuns.get(retryPayload.idempotencyKey)?.run_key).toBe('run-created-before-disconnect');
});

it('rotates the run intent key after the backend reports idempotency exhaustion', async () => {
  mockWorkflowDag();
  backend.startDag
    .mockRejectedValueOnce(new Error(
      'idempotency key exhausted: previous run is in terminal state, use a new key to retry (run_key=run-failed, status=failed)',
    ))
    .mockResolvedValueOnce({ run_key: 'run-after-exhaustion', execution_state: 'running' });

  renderWorkflowPage();

  fireEvent.click(await screen.findByRole('button', { name: '运行' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('idempotency key exhausted:');
  const firstKey = backend.startDag.mock.calls[0][0].idempotencyKey;

  fireEvent.click(screen.getByRole('button', { name: '运行' }));

  expect(await screen.findByText('已启动自动化')).toBeInTheDocument();
  expect(backend.startDag).toHaveBeenCalledTimes(2);
  expect(backend.startDag.mock.calls[1][0].idempotencyKey).not.toBe(firstKey);
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

it('renders mp4 final output through a backend-tokenized preview URL without file URLs', async () => {
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
          metadata: workflowRunMetadataWithFinalFile(finalPath),
        }],
  }));
  backend.getDagRun.mockResolvedValue({
    run: {
      id: 21,
      run_key: 'run-video-21',
      status: 'succeeded',
      metadata: workflowRunMetadataWithFinalFile(finalPath),
    },
    nodes: [],
  });
  backend.openSharedFile.mockImplementation((params = {}) => Promise.resolve(params.preview ? {
    url: 'http://127.0.0.1:4511/shared-file-preview?id=sf_video_123',
    path: finalPath,
    contentType: 'video/mp4',
    sizeBytes: 24,
  } : { opened: true }));

  const { container } = renderWorkflowPage();

  expect(await screen.findByText(finalPath)).toBeInTheDocument();
  await waitFor(() => expect(backend.openSharedFile).toHaveBeenCalledWith({ path: finalPath, preview: true }));
  const video = container.querySelector('video.workflow-final-media');
  expect(video).not.toBeNull();
  expect(video.getAttribute('src')).toBe('http://127.0.0.1:4511/shared-file-preview?id=sf_video_123');
  expect(video.getAttribute('src')).not.toContain('file://');

  fireEvent.click(screen.getByRole('button', { name: '系统打开' }));

  await waitFor(() => expect(backend.openSharedFile).toHaveBeenCalledWith({ path: finalPath }));
  expect(backend.readSharedFile).not.toHaveBeenCalled();
});
