import { fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import {
  backend,
  renderWorkflowPage,
  mockWorkflowDag,
  enterpriseTemplateDetail,
  mockEnterpriseTemplates,
  openTemplateCatalog,
} from './WorkflowPage.testSupport.js';

  beforeEach(() => {
  vi.clearAllMocks();
  mockEnterpriseTemplates();
  backend.writeWorkflowMaterial.mockImplementation(({ path }) => Promise.resolve({ path }));
});

afterEach(() => {
  vi.useRealTimers();
});

function expectedStageOutput(path) {
  return [{ format: 'json', path }];
}

function sharedfileOutput(path) {
  return { to_sharedfile: { path } };
}

function reviewNodeConfig() {
  return { prompt: '请评审这个方案' };
}

function malformedFinalOutputMetadata() {
  return { final_output: { text: '{"broken":' } };
}

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
          expected_outputs: expectedStageOutput('reports/workflows/document_review_archive/{{run_id}}/risks.json'),
        },
        outputs: sharedfileOutput('reports/workflows/document_review_archive/{{run_id}}/risks.json'),
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
        config: reviewNodeConfig(),
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

it('fails fast when the workflow list response is missing the dags array', async () => {
  backend.getDashboardPage.mockResolvedValue({ items: [] });

  renderWorkflowPage();

  const alert = await screen.findByRole('alert');
  expect(alert).toHaveTextContent('加载自动化失败，请重试。');
  expect(alert).not.toHaveTextContent('dags dashboard response dags must be an array');
  expect(backend.getDagDetail).not.toHaveBeenCalled();
});

it('shows a schedule warning for malformed cron data instead of guessing defaults', async () => {
  const dag = {
    dag_key: 'bad-schedule',
    title: 'Bad Schedule',
    status: 'ready',
    trigger: 'scheduled',
    cron_expr: 'CRON_TZ=Asia/Shanghai invalid cron',
    version: 3,
  };
  backend.getDashboardPage.mockResolvedValue({ dags: [dag] });
  backend.getDagDetail.mockResolvedValue({ dag, nodes: [] });
  backend.getDagRuns.mockResolvedValue({ runs: [] });
  backend.getDagRun.mockResolvedValue({ run: null, nodes: [] });

  renderWorkflowPage();

  fireEvent.click(await screen.findByRole('button', { name: '修改计划' }));

  expect(await screen.findByRole('alert')).toHaveTextContent('已有计划格式无法识别，请重新选择运行频率和时间。');
});

it('keeps malformed JSON final output text visible instead of parsing through a fallback', async () => {
  const dag = {
    dag_key: 'text-output',
    title: 'Text Output',
    status: 'done',
    trigger: 'manual',
    version: 2,
  };
  backend.getDashboardPage.mockResolvedValue({ dags: [dag] });
  backend.getDagDetail.mockResolvedValue({ dag, nodes: [] });
  backend.getDagRuns.mockImplementation((params = {}) => Promise.resolve({
    runs: params.status === 'running'
      ? []
      : [{ id: 41, run_key: 'run-text', status: 'succeeded', metadata: malformedFinalOutputMetadata() }],
  }));
  backend.getDagRun.mockResolvedValue({
    run: {
      id: 41,
      run_key: 'run-text',
      status: 'succeeded',
      metadata: malformedFinalOutputMetadata(),
    },
    nodes: [],
  });

  renderWorkflowPage();

  expect(await screen.findByText('{"broken":')).toBeInTheDocument();
});

it('accepts camelCase workflow template contract fields through the template adapter', async () => {
  mockWorkflowDag();
  const template = enterpriseTemplateDetail({
    id: 'government-enterprise/camel-template',
    title: '驼峰模板',
    description: '使用 camelCase 字段的兼容模板。',
    outputTypes: ['docx'],
    finalNodeKey: 'final_doc',
  });
  const camelTemplate = {
    id: template.id,
    version: template.version,
    title: template.title,
    description: template.description,
    category: template.category,
    businessFlow: template.business_flow,
    outputTypes: template.output_types,
    tags: template.tags,
    estimatedNodes: template.estimated_nodes,
    requiresReview: template.requires_review,
    supportsSchedule: template.supports_schedule,
    availableVersions: template.available_versions,
    trust: template.trust,
    compatibility: { runtime: template.compatibility.runtime, nodeTypes: template.compatibility.node_types },
    uiSchema: template.ui_schema,
    dagTemplate: {
      dagKeyTemplate: template.dag_template.dag_key_template,
      titleTemplate: template.dag_template.title_template,
      descriptionTemplate: template.dag_template.description_template,
      trigger: template.dag_template.trigger,
      finalNodeKey: template.dag_template.final_node_key,
      nodes: template.dag_template.nodes.map((node) => ({
        nodeKey: node.node_key,
        title: node.title,
        nodeType: node.node_type,
        assignedTo: node.assigned_to,
        dependsOn: node.depends_on,
        config: node.config,
      })),
    },
    finalOutput: {
      nodeKey: template.final_output.node_key,
      kind: template.final_output.kind,
      pathTemplate: template.final_output.path_template,
    },
  };
  backend.listWorkflowTemplates.mockResolvedValue({
    templates: [{
      ...camelTemplate,
      dagTemplate: undefined,
      uiSchema: undefined,
      finalOutput: undefined,
    }],
  });
  backend.getWorkflowTemplate.mockResolvedValue({ template: camelTemplate });

  renderWorkflowPage();
  await openTemplateCatalog();
  fireEvent.click(await screen.findByRole('button', { name: '选择驼峰模板模板' }));

  expect(await screen.findByLabelText('主题名称')).toBeInTheDocument();
  expect(screen.getAllByText('DOCX').length).toBeGreaterThan(0);
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
});

it('rejects workflow template details that omit required output arrays', async () => {
  mockWorkflowDag();
  const template = enterpriseTemplateDetail({
    id: 'government-enterprise/missing-output-types',
    title: '缺失输出类型',
    description: '缺失 outputTypes。',
    outputTypes: ['docx'],
    finalNodeKey: 'final_doc',
  });
  const brokenTemplate = { ...template };
  delete brokenTemplate.output_types;
  backend.listWorkflowTemplates.mockResolvedValue({
    templates: [{
      id: template.id,
      version: template.version,
      title: template.title,
      description: template.description,
      business_flow: template.business_flow,
      output_types: template.output_types,
      estimated_nodes: template.estimated_nodes,
      requires_review: template.requires_review,
      supports_schedule: template.supports_schedule,
      available_versions: template.available_versions,
      trust: template.trust,
      compatibility: template.compatibility,
    }],
  });
  backend.getWorkflowTemplate.mockResolvedValue({ template: brokenTemplate });

  renderWorkflowPage();
  await openTemplateCatalog();
  fireEvent.click(await screen.findByRole('button', { name: '选择缺失输出类型模板' }));

expect(await screen.findByRole('alert')).toHaveTextContent('模板契约错误：workflow template output_types must be an array');
  expect(screen.queryByLabelText('主题名称')).not.toBeInTheDocument();
});
