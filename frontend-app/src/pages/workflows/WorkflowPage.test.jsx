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
  getWorkflowTemplate: vi.fn(),
  listWorkflowTemplates: vi.fn(),
  openSharedFile: vi.fn(),
  readSharedFile: vi.fn(),
  renderWorkflowTemplateDraft: vi.fn(),
  rollbackWorkflowTemplate: vi.fn(),
  saveWorkflowTemplate: vi.fn(),
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

function workflowDesignStore() {
  return {
    resolveLaunchPreferences: vi.fn().mockResolvedValue({
      modelProvider: 'codex',
      model: 'gpt-5.5',
      effort: 'high',
    }),
    setActiveThread: vi.fn(),
    setActivePage: vi.fn(),
  };
}

const enterpriseTemplateDetails = {
  'government-enterprise/promo-video': enterpriseTemplateDetail({
    id: 'government-enterprise/promo-video',
    title: '宣传视频',
    description: '生成宣传脚本、审稿意见和 MP4 成片。',
    outputTypes: ['video', 'markdown', 'json'],
    finalNodeKey: 'final_video',
  }),
  'government-enterprise/daily-weekly-report': enterpriseTemplateDetail({
    id: 'government-enterprise/daily-weekly-report',
    title: '日报/周报',
    description: '汇总周期工作并生成复核后报告。',
    outputTypes: ['markdown', 'docx', 'pdf', 'json'],
    finalNodeKey: 'final_report',
    supportsSchedule: true,
  }),
  'government-enterprise/project-briefing': enterpriseTemplateDetail({
    id: 'government-enterprise/project-briefing',
    title: '项目汇报',
    description: '生成项目汇报材料。',
    outputTypes: ['pptx', 'pdf', 'markdown'],
    finalNodeKey: 'final_briefing',
  }),
  'government-enterprise/meeting-minutes': enterpriseTemplateDetail({
    id: 'government-enterprise/meeting-minutes',
    title: '会议纪要',
    description: '提取会议纪要和督办清单。',
    outputTypes: ['docx', 'markdown', 'pdf', 'json'],
    finalNodeKey: 'final_minutes',
  }),
  'government-enterprise/data-analysis-brief': enterpriseTemplateDetail({
    id: 'government-enterprise/data-analysis-brief',
    title: '数据分析简报',
    description: '生成指标解释和数据简报。',
    outputTypes: ['pptx', 'markdown', 'pdf', 'json'],
    finalNodeKey: 'final_brief',
    supportsSchedule: true,
  }),
  'government-enterprise/approval-material': enterpriseTemplateDetail({
    id: 'government-enterprise/approval-material',
    title: '审批材料',
    description: '生成审批依据、风险提示和材料包。',
    outputTypes: ['docx', 'pdf', 'markdown', 'json'],
    finalNodeKey: 'final_pack',
  }),
};

function enterpriseTemplateDetail({ id, title, description, outputTypes, finalNodeKey, supportsSchedule = false }) {
  const outputSlug = id.replace(/[^a-z0-9]+/gi, '_').replace(/^_+|_+$/g, '').toLowerCase();
  return {
    id,
    version: 1,
    title: { zh: title },
    description: { zh: description },
    category: 'government-enterprise',
    business_flow: '政企流程',
    output_types: outputTypes,
    tags: ['政企'],
    estimated_nodes: 5,
    requires_review: true,
    supports_schedule: supportsSchedule,
    available_versions: [1],
    trust: { level: 'built_in', source: 'bundled' },
    compatibility: { runtime: 'dag-v2', node_types: ['agent'], required_capabilities: ['workflow.node.agent', 'workflow.output.sharedfile', 'workflow.final_output'] },
    ui_schema: [
      { key: 'title', type: 'text', required: true, label: { zh: '主题名称' }, placeholder: { zh: '请输入主题' }, help: { zh: '用于 DAG 标题。' } },
      { key: 'source_materials', type: 'file_ref', required: true, label: { zh: '输入材料' }, placeholder: { zh: 'sharedfile 路径或材料说明' }, help: { zh: '必须提供材料来源。' } },
      {
        key: 'output_format',
        type: 'select',
        required: true,
        label: { zh: '输出格式' },
        help: { zh: '二进制格式必须发现可用工具。' },
        options: outputTypes.map((format) => ({ value: format, label: { zh: format.toUpperCase() } })),
      },
      { key: 'reviewer', type: 'reviewer', required: true, label: { zh: '复核人' }, placeholder: { zh: '负责人' }, help: { zh: '复核后才进入最终节点。' } },
      { key: 'output_path', type: 'path', required: true, label: { zh: '保存目录' }, placeholder: { zh: `reports/workflows/${outputSlug}/{{run_id}}/` }, help: { zh: '必须位于 reports/workflows/ 或 dag/。' } },
    ],
    dag_template: {
      dag_key_template: `${id}_{{run_id}}`,
      title_template: '{{title}} - ' + title,
      description_template: `基于 {{source_materials}} 创建${title}工作流。`,
      trigger: 'manual',
      final_node_key: finalNodeKey,
      nodes: [
        {
          node_key: 'intake',
          title: '材料理解',
          node_type: 'agent',
          assigned_to: `${id}_intake_runner`,
          depends_on: [],
          config: {
            ui: {
              stage_key: 'intake',
              stage_title: '材料理解',
              execution_mode: 'sequential',
              operation_summary: '读取材料并提取关键事实。',
              model_action: '抽取事实',
              skills: ['prompt_list'],
              input_sources: ['{{source_materials}}'],
              expected_outputs: [`reports/workflows/${outputSlug}/{{run_id}}/intake.md`],
            },
            outputs: { to_sharedfile: { path: `reports/workflows/${outputSlug}/{{run_id}}/intake.md` } },
          },
        },
        {
          node_key: 'review',
          title: '复核意见',
          node_type: 'agent',
          assigned_to: `${id}_review_runner`,
          depends_on: ['intake'],
          config: { ui: { operation_summary: '生成复核清单。' }, outputs: { to_sharedfile: { path: `reports/workflows/${outputSlug}/{{run_id}}/review.md` } } },
        },
        {
          node_key: finalNodeKey,
          title: '最终交付',
          node_type: 'agent',
          assigned_to: `${id}_final_runner`,
          depends_on: ['review'],
          config: { ui: { operation_summary: '生成最终材料。' }, outputs: { to_sharedfile: { path: `reports/workflows/${outputSlug}/{{run_id}}/final.{{output_format}}` } } },
        },
      ],
    },
    validation: { sharedfile_prefixes: ['reports/workflows/', 'dag/'], require_review_before_final: true, require_final_node_key: true },
    final_output: { node_key: finalNodeKey, kind: 'sharedfile', path_template: `reports/workflows/${outputSlug}/{{run_id}}/final.{{output_format}}` },
  };
}

function mockEnterpriseTemplates() {
  const templates = Object.values(enterpriseTemplateDetails).map((template) => ({
    id: template.id,
    version: template.version,
    title: template.title,
    description: template.description,
    category: template.category,
    business_flow: template.business_flow,
    output_types: template.output_types,
    estimated_nodes: template.estimated_nodes,
    requires_review: template.requires_review,
    supports_schedule: template.supports_schedule,
    final_node_key: template.dag_template.final_node_key,
    available_versions: template.available_versions,
    trust: template.trust,
    compatibility: template.compatibility,
  }));
  backend.listWorkflowTemplates.mockResolvedValue({ templates });
  backend.getWorkflowTemplate.mockImplementation(({ templateId }) => Promise.resolve({
    template: enterpriseTemplateDetails[templateId],
  }));
}

async function fillEnterpriseTemplateForm(title, sourceMaterials, reviewer) {
  fireEvent.change(await screen.findByLabelText('主题名称'), { target: { value: title } });
  fireEvent.change(screen.getByLabelText('输入材料'), { target: { value: sourceMaterials } });
  fireEvent.change(screen.getByLabelText('复核人'), { target: { value: reviewer } });
}

async function openTemplateCatalog() {
  fireEvent.click(await screen.findByRole('button', { name: '查看模板' }));
  return screen.findByRole('heading', { name: '政企工作流模板库' });
}

describe('WorkflowPage module', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockEnterpriseTemplates();
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
    fireEvent.click(screen.getByRole('button', { name: '页内播放' }));
    const previewVideo = document.querySelector('.workflow-media-preview video');
    expect(previewVideo).toBeInTheDocument();
    expect(previewVideo.querySelector('track[kind="captions"]')).toHaveAttribute('label', '无字幕');

    fireEvent.click(screen.getByRole('button', { name: '系统打开' }));

    await waitFor(() => expect(backend.openSharedFile).toHaveBeenCalledWith({ path: finalPath }));
    expect(backend.readSharedFile).not.toHaveBeenCalled();
  });

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
            metadata: { final_output: { kind: 'file', path: finalPath } },
          }],
    }));
    backend.getDagRun.mockResolvedValue({
      run: {
        id: 31,
        run_key: 'run-report-31',
        status: 'succeeded',
        metadata: { final_output: { kind: 'file', path: finalPath } },
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
            metadata: { final_output: { kind: 'file', path: finalPath } },
          }],
    }));
    backend.getDagRun.mockResolvedValue({
      run: {
        id: 32,
        run_key: 'run-data-32',
        status: 'succeeded',
        metadata: { final_output: { kind: 'file', path: finalPath } },
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
        config: { exec: { kind: 'command_card', command_ref: 'test_app' } },
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

  it('renders the government enterprise workflow template catalog with DAG data', async () => {
    mockWorkflowDag();

    renderWorkflowPage();

    expect((await screen.findAllByText('Daily Brief')).length).toBeGreaterThanOrEqual(2);
    expect(screen.queryByRole('heading', { name: '政企工作流模板库' })).not.toBeInTheDocument();
    expect(await openTemplateCatalog()).toBeInTheDocument();
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

  it('filters templates by search and shows version trust compatibility and rollback', async () => {
    mockWorkflowDag();
    backend.rollbackWorkflowTemplate.mockResolvedValue({
      template: { ...enterpriseTemplateDetails['government-enterprise/meeting-minutes'], version: 1 },
    });
    backend.listWorkflowTemplates.mockResolvedValue({
      templates: [{
        ...enterpriseTemplateDetails['government-enterprise/meeting-minutes'],
        version: 2,
        available_versions: [1, 2],
        final_node_key: 'final_minutes',
      }, {
        ...enterpriseTemplateDetails['government-enterprise/promo-video'],
        final_node_key: 'final_video',
      }],
    });

    renderWorkflowPage();

    await openTemplateCatalog();
    fireEvent.change(screen.getByLabelText('搜索模板'), { target: { value: '会议' } });

    expect(await screen.findByRole('button', { name: '选择会议纪要模板' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '选择宣传视频模板' })).not.toBeInTheDocument();
    expect(screen.getByText('v2')).toBeInTheDocument();
    expect(screen.getByText('built_in')).toBeInTheDocument();
    expect(screen.getByText('dag-v2')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '回滚到 v1' }));

    await waitFor(() => {
      expect(backend.rollbackWorkflowTemplate).toHaveBeenCalledWith({
        templateId: 'government-enterprise/meeting-minutes',
        version: 1,
      });
    });
  });

  it('renders the template catalog in the empty state', async () => {
    backend.getDashboardPage.mockResolvedValue({ dags: [] });

    renderWorkflowPage();

    expect(await screen.findByText('创建首个自动化')).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: '政企工作流模板库' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '每日简报' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '每周回顾' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '项目监控' })).not.toBeInTheDocument();
    expect(await openTemplateCatalog()).toBeInTheDocument();
    expect(screen.queryByText('创建首个自动化')).not.toBeInTheDocument();
    expect(await screen.findByRole('button', { name: '选择日报/周报模板' })).toBeInTheDocument();
    fireEvent.click(await screen.findByRole('button', { name: '选择审批材料模板' }));
    expect(await screen.findByRole('heading', { name: '审批材料' })).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '返回自动化' }));
    expect(await screen.findByText('创建首个自动化')).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: '政企工作流模板库' })).not.toBeInTheDocument();
  });

  it('starts the generic AI designer flow in a returnable free-design view', async () => {
    backend.getDashboardPage.mockResolvedValue({ dags: [] });
    backend.startThread.mockResolvedValue({ thread_id: 'thread-design' });
    const store = workflowDesignStore();

    renderWorkflowPage(store);

    expect(await screen.findByText('创建首个自动化')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '通过聊天创建' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '抖音 5 点模板' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '自由设计' }));

    expect(await screen.findByRole('heading', { name: '自由设计' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '返回自动化' })).toBeInTheDocument();
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
    expect(store.setActiveThread).not.toHaveBeenCalled();
    expect(store.setActivePage).not.toHaveBeenCalled();
    expect(await screen.findByRole('status')).toHaveTextContent('AI 设计流程已创建');
    expect(screen.getByRole('button', { name: '查看设计对话' })).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '返回自动化' }));
    expect(await screen.findByText('创建首个自动化')).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: '自由设计' })).not.toBeInTheDocument();
  });

  it('selects a template before showing the parameter form and DAG preview', async () => {
    mockWorkflowDag();

    renderWorkflowPage();

    expect(screen.queryByRole('heading', { name: 'DAG 草案预览' })).not.toBeInTheDocument();
    await openTemplateCatalog();
    fireEvent.click(await screen.findByRole('button', { name: '选择会议纪要模板' }));

    expect(await screen.findByRole('heading', { name: '会议纪要' })).toBeInTheDocument();
    expect(await screen.findByLabelText('主题名称')).toBeInTheDocument();
    expect(screen.getByLabelText('输入材料')).toBeInTheDocument();
    expect(screen.getByLabelText('输出格式')).toHaveDisplayValue('DOCX');
    expect(screen.getByLabelText('复核人')).toBeInTheDocument();
    expect(screen.getByLabelText('保存目录')).toHaveValue('reports/workflows/government_enterprise_meeting_minutes/{{run_id}}/');
    expect(screen.getByRole('heading', { name: 'DAG 草案预览' })).toBeInTheDocument();
    expect(screen.getByText('reports/workflows/government_enterprise_meeting_minutes/{{run_id}}/final.docx')).toBeInTheDocument();
    expect(screen.queryByText('reports/workflows/government_enterprise_meeting_minutes/{{run_id}}/final.{{output_format}}')).not.toBeInTheDocument();
    expect(screen.getAllByText('复核').length).toBeGreaterThan(0);
    expect(screen.getAllByText('最终').length).toBeGreaterThan(0);
    expect(backend.startThread).not.toHaveBeenCalled();
  });

  it('saves the selected validated DAG draft as a new template version', async () => {
    mockWorkflowDag();
    const template = enterpriseTemplateDetails['government-enterprise/meeting-minutes'];
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
    backend.saveWorkflowTemplate.mockResolvedValue({
      template: { ...template, version: 2, available_versions: [1, 2] },
    });

    renderWorkflowPage();

    await openTemplateCatalog();
    fireEvent.click(await screen.findByRole('button', { name: '选择会议纪要模板' }));
    await fillEnterpriseTemplateForm('模板主题', 'materials/source.md', '复核负责人');
    fireEvent.click(screen.getByRole('button', { name: '保存为模板' }));

    await waitFor(() => {
      expect(backend.renderWorkflowTemplateDraft).toHaveBeenCalledWith(expect.objectContaining({
        templateId: 'government-enterprise/meeting-minutes',
        version: 1,
        values: expect.objectContaining({
          title: '模板主题',
          source_materials: 'materials/source.md',
          reviewer: '复核负责人',
        }),
        runtime_context: { cwd: '/repo/app' },
      }));
    });
    expect(backend.saveWorkflowTemplate).toHaveBeenCalledWith(expect.objectContaining({
      templateId: 'government-enterprise/meeting-minutes',
      version: 2,
      category: 'government-enterprise',
      trust: { level: 'user', source: 'save_as_template' },
      compatibility: expect.objectContaining({ runtime: 'dag-v2', node_types: ['agent'] }),
      draft: expect.objectContaining({ final_node_key: 'final_minutes' }),
    }));
    expect(await screen.findByRole('status')).toHaveTextContent('模板已保存为 v2');
  });

  it.each([
    {
      button: '选择宣传视频模板',
      key: 'government-enterprise/promo-video',
      outputPrefix: 'reports/workflows/government_enterprise_promo_video/{{run_id}}/',
      extra: 'video_with_audio',
      outputFormat: 'video',
    },
    {
      button: '选择日报/周报模板',
      key: 'government-enterprise/daily-weekly-report',
      outputPrefix: 'reports/workflows/government_enterprise_daily_weekly_report/{{run_id}}/',
      extra: 'CRON_TZ=Asia/Shanghai',
      outputFormat: 'markdown',
    },
    {
      button: '选择审批材料模板',
      key: 'government-enterprise/approval-material',
      outputPrefix: 'reports/workflows/government_enterprise_approval_material/{{run_id}}/',
      extra: '审批材料',
      outputFormat: 'docx',
    },
  ])('starts the enterprise workflow template designer flow for $key', async ({ button, key, outputPrefix, extra, outputFormat }) => {
    mockWorkflowDag();
    backend.startThread.mockResolvedValue({ thread_id: 'thread-template' });
    backend.startTurn.mockResolvedValue({ ok: true });
    const store = workflowDesignStore();

    renderWorkflowPage(store);

    await openTemplateCatalog();
    fireEvent.click(await screen.findByRole('button', { name: button }));
    await fillEnterpriseTemplateForm('模板主题', 'materials/source.md', '复核负责人');
    fireEvent.click(screen.getByRole('button', { name: '创建工作流' }));

    await waitFor(() => expect(backend.startTurn).toHaveBeenCalledWith(expect.objectContaining({
      cwd: '/repo/app',
      threadId: 'thread-template',
    })));
    expect(backend.startThread).toHaveBeenCalledWith(expect.objectContaining({
      cwd: '/repo/app',
      name: 'AI 设计流程',
      agentKey: 'dag_designer',
      promptKey: 'main/dag_designer_zh',
      deferSpawn: true,
      config: expect.objectContaining({
        enabledTools: expect.arrayContaining(['workflow_template_list', 'workflow_template_get', 'workflow_template_render_dag']),
      }),
    }));
    expect(backend.startThread.mock.invocationCallOrder[0]).toBeLessThan(backend.startTurn.mock.invocationCallOrder[0]);
    const brief = backend.startTurn.mock.calls[0][0].input;
    expect(brief).toContain(`template_id: ${key}`);
    expect(brief).toContain('template_version: 1');
    expect(brief).toContain(outputPrefix);
    expect(brief).toContain(`目标输出格式: ${outputFormat}`);
    expect(brief).toContain('ui_schema');
    expect(brief).toContain('dag_template');
    expect(brief).toContain('dag_preview');
    expect(brief).toContain('workflow_template_list');
    expect(brief).toContain('workflow_template_get');
    expect(brief).toContain('workflow_template_render_dag');
    expect(brief).toContain('review_node');
    expect(brief).toContain('顺序/并行');
    expect(brief).toContain('config.ui');
    expect(brief).toContain('operation_summary');
    expect(brief).toContain('skills');
    expect(brief).toContain('审批');
    expect(brief).toContain('command_card');
    expect(brief).toContain('outputs.to_sharedfile');
    expect(brief).toContain('final_node_key');
    expect(brief).toContain('node.config.exec');
    expect(brief).toContain(extra);
    expect(await screen.findByRole('status')).toHaveTextContent('DAG 设计器已接收');
    expect(screen.getByRole('button', { name: '查看设计对话' })).toBeInTheDocument();
    expect(store.setActiveThread).not.toHaveBeenCalled();
    expect(store.setActivePage).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole('button', { name: '查看设计对话' }));

    expect(store.setActiveThread).toHaveBeenCalledWith('thread-template');
    await waitFor(() => expect(store.setActivePage).toHaveBeenCalledWith('chat'));
  });

  it('does not send a template brief when the designer thread fails to start', async () => {
    mockWorkflowDag();
    backend.startThread.mockRejectedValue(new Error('thread offline'));
    const store = workflowDesignStore();

    renderWorkflowPage(store);

    await openTemplateCatalog();
    fireEvent.click(await screen.findByRole('button', { name: '选择审批材料模板' }));
    await fillEnterpriseTemplateForm('模板主题', 'materials/source.md', '复核负责人');
    fireEvent.click(screen.getByRole('button', { name: '创建工作流' }));

    expect(await screen.findByRole('alert')).toHaveTextContent('启动政企模板失败：thread offline');
    expect(backend.startTurn).not.toHaveBeenCalled();
    expect(store.setActiveThread).not.toHaveBeenCalled();
  });

  it('shows an explicit error when sending the enterprise template brief fails', async () => {
    mockWorkflowDag();
    backend.startThread.mockResolvedValue({ thread_id: 'thread-template' });
    backend.startTurn.mockRejectedValue(new Error('turn offline'));
    const store = workflowDesignStore();

    renderWorkflowPage(store);

    await openTemplateCatalog();
    fireEvent.click(await screen.findByRole('button', { name: '选择审批材料模板' }));
    await fillEnterpriseTemplateForm('模板主题', 'materials/source.md', '复核负责人');
    fireEvent.click(screen.getByRole('button', { name: '创建工作流' }));

    expect(await screen.findByRole('alert')).toHaveTextContent('发送政企模板需求失败：turn offline');
    expect(backend.startTurn).toHaveBeenCalled();
    expect(store.setActiveThread).not.toHaveBeenCalled();
  });

  it('renders workflow stages from node dependencies and shows planned model operations on hover', async () => {
    const dag = {
      dag_key: 'enterprise_doc_review',
      title: '文档审查归档',
      status: 'ready',
      trigger: 'manual',
      version: 1,
    };
    const nodes = [
      {
        node_key: 'risk_review',
        title: '风险分析',
        node_type: 'agent',
        assigned_to: 'risk-runner',
        depends_on: ['classify_docs'],
        status: 'ready',
        config: {
          ui: {
            stage_title: '风险分析',
            execution_mode: 'parallel',
            operation_summary: '大模型比对制度要求并标注风险等级。',
            model_action: '读取分类结果，输出风险说明。',
            skills: ['文档审查', '风险识别'],
            input_sources: ['classify_docs'],
            expected_outputs: [{ format: 'json', path: 'reports/workflows/document_review_archive/{{run_id}}/risks.json' }],
          },
          outputs: {
            to_sharedfile: { path: 'reports/workflows/document_review_archive/{{run_id}}/risks.json' },
          },
        },
      },
      {
        node_key: 'classify_docs',
        title: '抽取要点与文档分类',
        node_type: 'agent',
        assigned_to: 'classify-runner',
        depends_on: ['collect_materials'],
        status: 'done',
        config: {
          ui: {
            operation_summary: '大模型抽取文件主题、效力层级和关键词。',
            skills: ['信息抽取'],
          },
        },
      },
      {
        node_key: 'collect_materials',
        title: '接收材料与审查要求',
        node_type: 'agent',
        assigned_to: 'collector',
        depends_on: [],
        status: 'done',
      },
    ];
    backend.getDashboardPage.mockResolvedValue({ dags: [dag] });
    backend.getDagDetail.mockResolvedValue({ dag, nodes });
    backend.getDagRuns.mockResolvedValue({ runs: [] });
    backend.getDagRun.mockResolvedValue({ run: null, nodes: [] });

    renderWorkflowPage();

    expect(await screen.findByRole('heading', { name: '阶段进度' })).toBeInTheDocument();
    expect(screen.getByText('第 1 阶段')).toBeInTheDocument();
    expect(screen.getAllByText('顺序执行').length).toBeGreaterThan(0);
    expect(screen.getByText('并行执行')).toBeInTheDocument();
    const riskStage = screen.getByRole('button', { name: /风险分析/ });

    fireEvent.mouseEnter(riskStage);

    expect(await screen.findByText('大模型比对制度要求并标注风险等级。')).toBeInTheDocument();
    expect(screen.getByText('文档审查、风险识别')).toBeInTheDocument();
    expect(screen.getAllByText('reports/workflows/document_review_archive/{{run_id}}/risks.json').length).toBeGreaterThan(0);
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
