import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import {
  backend,
  renderWorkflowPage,
  mockWorkflowDag,
  mockEnterpriseTemplates,
  workflowRunMetadataWithFilePath,
  commandCardRef,
  verifierExecFixture,
  verifySharedFileOutputs,
  workflowSharedFileOutputs,
  openEnterpriseTemplateCatalog,
} from './WorkflowPage.testSupport.js';

  beforeEach(() => {
  vi.clearAllMocks();
  mockEnterpriseTemplates();
  backend.writeWorkflowMaterial.mockImplementation(({ path }) => Promise.resolve({ path }));
});

afterEach(() => {
  vi.useRealTimers();
});

function draftNodeExecFixture() {
  return {
    agent_key: 'writer',
    cwd: '/repo/app',
    model: 'gpt-5.5',
    prompt_key: 'main/writer',
    provider: 'codex',
  };
}

function nodeResultOutput() {
  return { to_node_result: true };
}

function commandCardExec(commandRef) {
  return { kind: 'command_card', command_ref: commandRef };
}

function commandCardConfig(commandRef, outputs) {
  return { exec: commandCardExec(commandRef), outputs };
}

function hybridExecFixture() {
  return {
    automation: commandCardRef('test_app'),
    verifier: verifierExecFixture(),
  };
}

function hybridConfigFixture(outputPath) {
  return {
    exec: hybridExecFixture(),
    outputs: verifySharedFileOutputs(outputPath),
  };
}

it('opens office and pdf final outputs through the system without reading binary files as text', async () => {
  const dag = {
    dag_key: 'enterprise-report',
    title: 'Enterprise Report',
    status: 'ready',
    trigger: 'manual',
    version: 1,
  };
  const finalPath = 'reports/workflows/data_report_release/run-1/final.pptx';
  backend.getDashboardPage.mockResolvedValue({ dags: [dag] });
  backend.getDagDetail.mockResolvedValue({ dag, nodes: [] });
  backend.getDagRuns.mockImplementation((params = {}) => Promise.resolve({
    runs: params.status === 'running'
      ? []
      : [{
          id: 31,
          run_key: 'run-report-31',
          status: 'succeeded',
          metadata: workflowRunMetadataWithFilePath(finalPath),
        }],
  }));
  backend.getDagRun.mockResolvedValue({
    run: {
      id: 31,
      run_key: 'run-report-31',
      status: 'succeeded',
      metadata: workflowRunMetadataWithFilePath(finalPath),
    },
    nodes: [],
  });
  backend.openSharedFile.mockResolvedValue({ opened: true });

  renderWorkflowPage();

  expect(await screen.findByText(finalPath)).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: '系统打开' }));

  await waitFor(() => expect(backend.openSharedFile).toHaveBeenCalledWith({ path: finalPath }));
  expect(backend.readSharedFile).not.toHaveBeenCalled();
});

it('opens xlsx final outputs through the system without reading binary files as text', async () => {
  const dag = {
    dag_key: 'enterprise-data',
    title: 'Enterprise Data',
    status: 'ready',
    trigger: 'manual',
    version: 1,
  };
  const finalPath = 'reports/workflows/data_analysis_brief/run-1/final.xlsx';
  backend.getDashboardPage.mockResolvedValue({ dags: [dag] });
  backend.getDagDetail.mockResolvedValue({ dag, nodes: [] });
  backend.getDagRuns.mockImplementation((params = {}) => Promise.resolve({
    runs: params.status === 'running'
      ? []
      : [{
          id: 32,
          run_key: 'run-data-32',
          status: 'succeeded',
          metadata: workflowRunMetadataWithFilePath(finalPath),
        }],
  }));
  backend.getDagRun.mockResolvedValue({
    run: {
      id: 32,
      run_key: 'run-data-32',
      status: 'succeeded',
      metadata: workflowRunMetadataWithFilePath(finalPath),
    },
    nodes: [],
  });
  backend.openSharedFile.mockResolvedValue({ opened: true });

  renderWorkflowPage();

  expect(await screen.findByText(finalPath)).toBeInTheDocument();
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
        exec: draftNodeExecFixture(),
        first_turn: 'Write a daily brief',
        outputs: workflowSharedFileOutputs('reports/brief.md'),
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

it('renders historical hybrid nodes without exposing them in node config editing', async () => {
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
        config: commandCardConfig('build_app', nodeResultOutput()),
      },
      {
        node_key: 'verify',
        title: 'Verify',
        node_type: 'hybrid',
        assigned_to: 'worker',
        depends_on: ['build'],
        config: hybridConfigFixture('reports/verify.md'),
      },
    ],
  });
  backend.getDagRuns.mockResolvedValue({ runs: [] });
  backend.getDagRun.mockResolvedValue({ run: null, nodes: [] });

  renderWorkflowPage();

  expect(await screen.findByDisplayValue('build_app')).toBeInTheDocument();
  expect(screen.getAllByText('Verify').length).toBeGreaterThan(0);
  expect(screen.getByLabelText('步骤')).toHaveDisplayValue('Build');
  expect(screen.queryByRole('option', { name: 'Verify' })).not.toBeInTheDocument();
  expect(screen.queryByDisplayValue('test_app')).not.toBeInTheDocument();
  expect(screen.queryByDisplayValue('reviewer')).not.toBeInTheDocument();
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
      config: commandCardConfig('build_app', { to_node_result: false }),
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

it('does not expose hybrid-only DAGs as configurable runtime nodes', async () => {
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
      config: hybridConfigFixture('reports/verify.md'),
    }],
  });
  backend.getDagRuns.mockResolvedValue({ runs: [] });
  backend.getDagRun.mockResolvedValue({ run: null, nodes: [] });
  backend.applyDagOps.mockResolvedValue({ newVersion: 8 });

  renderWorkflowPage();

  expect(await screen.findByText('暂无可配置步骤')).toBeInTheDocument();
  expect(screen.queryByDisplayValue('test_app')).not.toBeInTheDocument();
  expect(screen.queryByRole('button', { name: '保存步骤' })).not.toBeInTheDocument();
  expect(backend.applyDagOps).not.toHaveBeenCalled();
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
      node_type: 'automation',
      assigned_to: 'worker',
      depends_on: [],
      config: { exec: commandCardExec('test_app') },
    }],
  });
  backend.getDagRuns.mockResolvedValue({ runs: [] });
  backend.getDagRun.mockResolvedValue({ run: null, nodes: [] });
  backend.applyDagOps.mockResolvedValue({ newVersion: 8 });

  renderWorkflowPage();

  expect(await screen.findByDisplayValue('test_app')).toBeInTheDocument();
  fireEvent.change(screen.getByLabelText('命令卡片'), { target: { value: '' } });
  fireEvent.click(screen.getByRole('button', { name: '保存步骤' }));

  expect(await screen.findByRole('alert')).toHaveTextContent('保存步骤失败，请重试。');
  expect(screen.queryByText(/config\.exec\.command_ref/)).not.toBeInTheDocument();
  expect(backend.applyDagOps).not.toHaveBeenCalled();
});

it('saves schedule cron expressions with the backend timezone prefix', async () => {
  mockWorkflowDag();
  backend.applyDagOps.mockResolvedValue({ newVersion: 8 });

  renderWorkflowPage();

  fireEvent.click(await screen.findByRole('button', { name: '创建定时任务' }));
  const scheduleDialog = await screen.findByRole('dialog', { name: '创建定时任务' });
  fireEvent.change(screen.getByLabelText('运行时间'), { target: { value: '05:00' } });
  fireEvent.click(within(scheduleDialog).getByRole('button', { name: '创建定时任务' }));

  await waitFor(() => expect(backend.applyDagOps).toHaveBeenCalled());
  expect(backend.applyDagOps.mock.calls[0][0].ops[0].patch).toMatchObject({
    trigger: 'scheduled',
    cron_expr: 'CRON_TZ=Asia/Shanghai 0 5 * * *',
  });
});

it('ignores the malformed apply ops response body after saving a schedule', async () => {
  mockWorkflowDag();
  backend.applyDagOps.mockResolvedValue({ malformed: ['ignored-response-body'] });

  renderWorkflowPage();

  fireEvent.click(await screen.findByRole('button', { name: '创建定时任务' }));
  const scheduleDialog = await screen.findByRole('dialog', { name: '创建定时任务' });
  fireEvent.change(screen.getByLabelText('运行时间'), { target: { value: '05:00' } });
  fireEvent.click(within(scheduleDialog).getByRole('button', { name: '创建定时任务' }));

  await waitFor(() => expect(backend.applyDagOps).toHaveBeenCalled());
  expect(await screen.findByText('已保存定时任务')).toBeInTheDocument();
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
});

it('preserves paused scheduled DAGs when editing the schedule cron expression', async () => {
  const dag = {
    dag_key: 'paused-flow',
    title: 'Paused Flow',
    status: 'ready',
    trigger: 'scheduled',
    cron_expr: 'CRON_TZ=Asia/Shanghai 0 9 * * *',
    schedule_enabled: false,
    version: 7,
  };
  backend.getDashboardPage.mockResolvedValue({ dags: [dag] });
  backend.getDagDetail.mockResolvedValue({ dag, nodes: [] });
  backend.getDagRuns.mockResolvedValue({ runs: [] });
  backend.getDagRun.mockResolvedValue({ run: null, nodes: [] });
  backend.applyDagOps.mockResolvedValue({ newVersion: 8 });

  renderWorkflowPage();

  fireEvent.click(await screen.findByRole('button', { name: '修改计划' }));
  const scheduleDialog = await screen.findByRole('dialog', { name: '修改计划' });
  fireEvent.change(screen.getByLabelText('运行时间'), { target: { value: '06:30' } });
  fireEvent.click(within(scheduleDialog).getByRole('button', { name: '修改计划' }));

  await waitFor(() => expect(backend.applyDagOps).toHaveBeenCalled());
  expect(backend.applyDagOps.mock.calls[0][0].ops[0].patch).toMatchObject({
    trigger: 'scheduled',
    cron_expr: 'CRON_TZ=Asia/Shanghai 30 6 * * *',
    schedule_enabled: false,
  });
});

it('renders the government enterprise workflow template catalog with DAG data', async () => {
  mockWorkflowDag();

  renderWorkflowPage();

  expect((await screen.findAllByText('Daily Brief')).length).toBeGreaterThanOrEqual(2);
  expect(screen.queryByRole('heading', { name: '政企工作流模板库' })).not.toBeInTheDocument();
  expect(await openEnterpriseTemplateCatalog()).toBeInTheDocument();
  expect(screen.queryByText('Daily Brief')).not.toBeInTheDocument();
  expect(screen.getByRole('button', { name: '返回自动化' })).toBeInTheDocument();
  expect(backend.listWorkflowTemplates).toHaveBeenCalledWith({ category: 'government-enterprise' });
  expect(await screen.findByRole('button', { name: '选择宣传视频模板' })).toBeInTheDocument();
  expect(screen.getByRole('button', { name: '选择日报/周报模板' })).toBeInTheDocument();
  expect(screen.getByRole('button', { name: '选择项目汇报模板' })).toBeInTheDocument();
  expect(screen.getByRole('button', { name: '选择会议纪要模板' })).toBeInTheDocument();
  expect(screen.getByRole('button', { name: '选择数据分析简报模板' })).toBeInTheDocument();
  expect(screen.getByRole('button', { name: '选择审批材料模板' })).toBeInTheDocument();
  expect(screen.getByText('DAG 草案')).toBeInTheDocument();
  expect(screen.getByText('目标输出格式')).toBeInTheDocument();
});
