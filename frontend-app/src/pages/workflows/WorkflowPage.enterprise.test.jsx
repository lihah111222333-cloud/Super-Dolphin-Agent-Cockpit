import { act, fireEvent, renderHook, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import {
  backend,
  deferred,
  renderWorkflowPage,
  mockWorkflowDag,
  workflowDesignStore,
  enterpriseTemplateDetails,
  fillEnterpriseTemplateForm,
  mockEnterpriseTemplates,
  openTemplateCatalog,
} from './WorkflowPage.testSupport.jsx';
import { useWorkflowActions } from './hooks/useWorkflowActions.js';

  beforeEach(() => {
  vi.clearAllMocks();
  mockEnterpriseTemplates();
  backend.writeWorkflowMaterial.mockImplementation(({ path }) => Promise.resolve({ path }));
});

afterEach(() => {
  vi.useRealTimers();
});


it('filters templates by search and shows version trust compatibility and rollback', async () => {
  mockWorkflowDag();
  backend.rollbackWorkflowTemplate.mockResolvedValue({ malformed: ['ignored-response-body'] });
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
  await waitFor(() => expect(screen.queryByRole('button', { name: '回滚中...' })).not.toBeInTheDocument());
  expect(screen.getByRole('button', { name: '回滚到 v1' })).toBeEnabled();
  await waitFor(() => expect(backend.listWorkflowTemplates.mock.calls.length).toBeGreaterThan(1));
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
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

it('creates and starts the selected enterprise workflow template without opening chat', async () => {
  mockWorkflowDag();
  const template = enterpriseTemplateDetails['government-enterprise/approval-material'];
  backend.renderWorkflowTemplateDraft.mockResolvedValue({
    draft: {
      template_id: template.id,
      template_version: template.version,
      dag_key: 'government_enterprise_approval_material_run',
      title: '模板主题 - 审批材料',
      description: '生成审批材料包。',
      trigger: 'manual',
      final_node_key: 'final_pack',
      metadata: { source: 'ui-template' },
      nodes: template.dag_template.nodes,
      final_output: template.final_output,
    },
  });

  renderWorkflowPage(workflowDesignStore());

  await openTemplateCatalog();
  fireEvent.click(await screen.findByRole('button', { name: '选择审批材料模板' }));
  await fillEnterpriseTemplateForm('模板主题', 'materials/source.md', '复核负责人');
  fireEvent.click(screen.getByRole('button', { name: '创建工作流' }));

  await waitFor(() => {
    expect(backend.renderWorkflowTemplateDraft).toHaveBeenCalledWith(expect.objectContaining({
      templateId: 'government-enterprise/approval-material',
      version: 1,
      values: expect.objectContaining({
        title: '模板主题',
        source_materials: 'materials/source.md',
        reviewer: '复核负责人',
      }),
      runtime_context: { cwd: '/repo/app' },
    }));
  });
  await waitFor(() => {
    expect(backend.createAndStartDag).toHaveBeenCalledWith(expect.objectContaining({
      dagKey: 'government_enterprise_approval_material_run',
      title: '模板主题 - 审批材料',
      finalNodeKey: 'final_pack',
      metadata: { source: 'ui-template' },
      nodes: expect.arrayContaining([
        expect.objectContaining({
          nodeKey: 'intake',
          nodeType: 'agent',
          assignedTo: 'government-enterprise/approval-material_intake_runner',
        }),
      ]),
    }));
  });
  expect(backend.startThread).not.toHaveBeenCalled();
  expect(backend.startTurn).not.toHaveBeenCalled();
  expect(await screen.findByRole('status')).toHaveTextContent('已创建并启动自动化');
});

it('uploads dropped material files and stores sharedfile paths in the enterprise template material field', async () => {
  mockWorkflowDag();

  renderWorkflowPage();

  await openTemplateCatalog();
  fireEvent.click(await screen.findByRole('button', { name: '选择审批材料模板' }));
  const input = await screen.findByLabelText('输入材料');
  const dropTarget = input.closest('.enterprise-template-file-ref');
  expect(dropTarget).toBeTruthy();
  const file = new File(['审批说明正文'], '审批材料.txt', { type: 'text/plain' });
  file.text = vi.fn().mockResolvedValue('审批说明正文');

  fireEvent.drop(dropTarget, {
    dataTransfer: {
      files: [file],
    },
  });

  await waitFor(() => {
    expect(backend.writeWorkflowMaterial).toHaveBeenCalledWith(expect.objectContaining({
      path: expect.stringContaining('reports/workflows/uploads/government-enterprise-approval-material/source_materials/'),
      content: expect.stringContaining('审批说明正文'),
    }));
    expect(input.value).toContain('reports/workflows/uploads/government-enterprise-approval-material/source_materials/');
    expect(input.value).not.toContain('审批说明正文');
  });
  expect(await screen.findByText('已上传 1 个材料文件')).toBeInTheDocument();
});

it('rejects binary material drops instead of writing unreadable content', async () => {
  mockWorkflowDag();

  renderWorkflowPage();

  await openTemplateCatalog();
  fireEvent.click(await screen.findByRole('button', { name: '选择审批材料模板' }));
  const input = await screen.findByLabelText('输入材料');
  const dropTarget = input.closest('.enterprise-template-file-ref');
  const file = new File(['%PDF-1.4'], 'approval.pdf', { type: 'application/pdf' });

  fireEvent.drop(dropTarget, {
    dataTransfer: {
      files: [file],
    },
  });

  await screen.findByRole('alert');
  expect(backend.writeWorkflowMaterial).not.toHaveBeenCalled();
  expect(input).toHaveValue('');
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
    outputFormat: 'docx',
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
  fireEvent.click(screen.getByRole('button', { name: '用聊天调整' }));

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
  expect(brief).toContain('outputs.to_artifact');
  if (outputFormat === 'docx') {
    expect(brief).toContain('document_renderer');
    expect(brief).toContain('source_text_field');
  }
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
  fireEvent.click(screen.getByRole('button', { name: '用聊天调整' }));

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
  fireEvent.click(screen.getByRole('button', { name: '用聊天调整' }));

  expect(await screen.findByRole('alert')).toHaveTextContent('发送政企模板需求失败：turn offline');
  expect(backend.startTurn).toHaveBeenCalled();
  expect(store.setActiveThread).not.toHaveBeenCalled();
});

it('keeps a newer user thread active when a generic workflow continuation returns late', async () => {
  const started = deferred();
  backend.startThread.mockReturnValue(started.promise);
  let selectionGeneration = Object.freeze({});
  let activeThreadId = 'thread-initial';
  const setActivePage = vi.fn();
  const store = {
    ...workflowDesignStore(),
    captureThreadSelection: vi.fn(() => selectionGeneration),
    setActivePage,
    setActiveThread: vi.fn(async (threadId, options = {}) => {
      if (Object.prototype.hasOwnProperty.call(options, 'selectionSnapshot')) {
        if (options.selectionSnapshot !== selectionGeneration) return false;
      } else {
        selectionGeneration = Object.freeze({});
      }
      activeThreadId = threadId;
      return true;
    }),
  };
  const actionState = { setActioning: vi.fn(), setError: vi.fn() };
  const notices = { clearNotice: vi.fn() };
  const { result } = renderHook(() => useWorkflowActions({
    actionState,
    derived: {},
    list: {},
    notices,
    refresh: {},
    selection: {},
    setDesignSession: vi.fn(),
    store,
    workflowCwd: '/repo/app',
  }));

  let workflowAction;
  act(() => {
    workflowAction = result.current.startDesignFlow();
  });
  await waitFor(() => expect(backend.startThread).toHaveBeenCalledTimes(1));

  act(() => {
    selectionGeneration = Object.freeze({});
    activeThreadId = 'thread-user';
  });
  await act(async () => {
    started.resolve({ thread_id: 'thread-workflow' });
    await workflowAction;
  });

  expect(activeThreadId).toBe('thread-user');
  expect(setActivePage).not.toHaveBeenCalled();
});

it.each([
  ['captureThreadSelection', '自动化会话选择快照能力不可用'],
  ['setActiveThread', '自动化会话选择能力不可用'],
  ['setActivePage', '自动化页面导航能力不可用'],
])('fails fast when the generic workflow store omits %s', async (missingAction, message) => {
  const store = {
    ...workflowDesignStore(),
    captureThreadSelection: vi.fn(() => Object.freeze({})),
  };
  delete store[missingAction];
  const actionState = { setActioning: vi.fn(), setError: vi.fn() };
  const { result } = renderHook(() => useWorkflowActions({
    actionState,
    derived: {},
    list: {},
    notices: { clearNotice: vi.fn() },
    refresh: {},
    selection: {},
    setDesignSession: vi.fn(),
    store,
    workflowCwd: '/repo/app',
  }));

  await act(async () => {
    await result.current.startDesignFlow();
  });

  expect(actionState.setError).toHaveBeenCalledWith(`启动 AI 设计流程失败：${message}`);
  expect(backend.startThread).not.toHaveBeenCalled();
});
