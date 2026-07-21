import React from 'react';
import { CancelledError, QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen } from '@testing-library/react';
import { vi } from 'vitest';
import { WorkflowPage as workflowPage } from './WorkflowPage.jsx';

const backend = vi.hoisted(() => ({
  applyDagOps: vi.fn(),
  createAndStartDag: vi.fn(),
  deleteDag: vi.fn(),
  dispatchDagNode: vi.fn(),
  getDashboardPage: vi.fn(),
  getDagDetail: vi.fn(),
  getDagRun: vi.fn(),
  getDagRuns: vi.fn(),
  getWorkflowTemplate: vi.fn(),
  listWorkflowTemplates: vi.fn(),
  openSharedFile: vi.fn(),
  previewSharedFile: vi.fn(),
  readSharedFile: vi.fn(),
  renderWorkflowTemplateDraft: vi.fn(),
  rollbackWorkflowTemplate: vi.fn(),
  saveWorkflowTemplate: vi.fn(),
  startDag: vi.fn(),
  startTurn: vi.fn(),
  startThread: vi.fn(),
  terminateDagRun: vi.fn(),
  writeWorkflowMaterial: vi.fn(),
}));

vi.mock('./services/workflowPageService.js', () => backend);

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
  return React.createElement(
    QueryClientProvider,
    { client: queryClient },
    React.createElement(workflowPage, { projectPath: '/repo/app', store, ...pageProps }),
  );
}

function renderWorkflowPage(store = {}, pageProps = {}) {
  const queryClient = new QueryClient(workflowTestQueryClientOptions());

  const result = render(workflowPageElement(queryClient, store, pageProps));
  return {
    ...result,
    rerenderWorkflowPage: (nextPageProps = {}) => {
      result.rerender(workflowPageElement(queryClient, store, nextPageProps));
    },
  };
}

function workflowTestQueryClientOptions() {
  return {
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
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
  backend.createAndStartDag.mockResolvedValue({ dagKey: 'template-dag', runKey: 'template-run', version: 1, executionState: 'queued' });
  backend.dispatchDagNode.mockResolvedValue({
    node: {
      id: 1,
      dag_key: 'daily-brief',
      node_key: 'draft',
      title: 'Draft',
      status: 'ready',
      assigned_to: 'codex',
      created_at: '2026-07-21T00:00:00Z',
      updated_at: '2026-07-21T00:00:00Z',
    },
    wakeup_id: 1,
    enqueued: true,
  });
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
    outputTypes: ['docx', 'pdf'],
    finalNodeKey: 'final_report',
    supportsSchedule: true,
  }),
  'government-enterprise/project-briefing': enterpriseTemplateDetail({
    id: 'government-enterprise/project-briefing',
    title: '项目汇报',
    description: '生成项目汇报材料。',
    outputTypes: ['docx', 'pdf'],
    finalNodeKey: 'final_briefing',
  }),
  'government-enterprise/meeting-minutes': enterpriseTemplateDetail({
    id: 'government-enterprise/meeting-minutes',
    title: '会议纪要',
    description: '提取会议纪要和督办清单。',
    outputTypes: ['docx', 'pdf'],
    finalNodeKey: 'final_minutes',
  }),
  'government-enterprise/data-analysis-brief': enterpriseTemplateDetail({
    id: 'government-enterprise/data-analysis-brief',
    title: '数据分析简报',
    description: '生成指标解释和数据简报。',
    outputTypes: ['docx', 'pdf'],
    finalNodeKey: 'final_brief',
    supportsSchedule: true,
  }),
  'government-enterprise/approval-material': enterpriseTemplateDetail({
    id: 'government-enterprise/approval-material',
    title: '审批材料',
    description: '生成审批依据、风险提示和材料包。',
    outputTypes: ['docx', 'pdf'],
    finalNodeKey: 'final_pack',
  }),
};

function enterpriseTemplateDetail(options) {
  const { id, title, description, outputTypes, finalNodeKey, supportsSchedule = false } = options;
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
    compatibility: { runtime: 'dag-v2', node_types: ['agent'], required_capabilities: ['workflow.node.agent', 'workflow.output.sharedfile', 'workflow.output.artifact', 'workflow.final_output'] },
    ui_schema: [
      { key: 'title', type: 'text', required: true, label: { zh: '主题名称' }, placeholder: { zh: '请输入主题' }, help: { zh: '用于 DAG 标题。' } },
      { key: 'source_materials', type: 'file_ref', required: true, label: { zh: '输入材料' }, placeholder: { zh: 'sharedfile 路径或材料说明' }, help: { zh: '必须提供材料来源。' } },
      {
        key: 'output_format',
        type: 'select',
        required: true,
        label: { zh: '输出格式' },
        help: { zh: '二进制格式必须发现可用工具。' },
        options: outputTypes.map(enterpriseFieldOption),
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
      nodes: enterpriseTemplateNodes(id, outputSlug, finalNodeKey),
    },
    validation: { sharedfile_prefixes: ['reports/workflows/', 'dag/'], require_review_before_final: true, require_final_node_key: true },
    final_output: { node_key: finalNodeKey, kind: 'artifact', path_template: `reports/workflows/${outputSlug}/{{run_id}}/final.{{output_format}}` },
  };
}

function enterpriseFieldOption(format) {
  return { label: { zh: format.toUpperCase() }, value: format };
}

function enterpriseSharedFileOutput(path) {
  return { to_sharedfile: { path } };
}

function enterpriseArtifactOutput(pathTemplate) {
  return {
    to_artifact: {
      path_template: pathTemplate,
      source_text_field: 'document_text',
      source_tool: 'document_renderer',
    },
    to_node_result: false,
  };
}

function enterpriseTemplateNodes(id, outputSlug, finalNodeKey) {
  return [
    enterpriseIntakeNode(id, outputSlug),
    enterpriseReviewNode(id, outputSlug),
    enterpriseFinalNode(id, outputSlug, finalNodeKey),
  ];
}

function enterpriseIntakeNode(id, outputSlug) {
  const outputPath = `reports/workflows/${outputSlug}/{{run_id}}/intake.md`;
  const ui = {
    execution_mode: 'sequential',
    expected_outputs: [outputPath],
    input_sources: ['{{source_materials}}'],
    model_action: '抽取事实',
    operation_summary: '读取材料并提取关键事实。',
    skills: ['prompt_list'],
    stage_key: 'intake',
    stage_title: '材料理解',
  };
  return {
    assigned_to: `${id}_intake_runner`,
    config: { outputs: enterpriseSharedFileOutput(outputPath), ui },
    depends_on: [],
    node_key: 'intake',
    node_type: 'agent',
    title: '材料理解',
  };
}

function enterpriseReviewNode(id, outputSlug) {
  const outputPath = `reports/workflows/${outputSlug}/{{run_id}}/review.md`;
  return {
    assigned_to: `${id}_review_runner`,
    config: { outputs: enterpriseSharedFileOutput(outputPath), ui: { operation_summary: '生成复核清单。' } },
    depends_on: ['intake'],
    node_key: 'review',
    node_type: 'agent',
    title: '复核意见',
  };
}

function enterpriseFinalNode(id, outputSlug, finalNodeKey) {
  const outputPath = `reports/workflows/${outputSlug}/{{run_id}}/final.{{output_format}}`;
  return {
    assigned_to: `${id}_final_runner`,
    config: { outputs: enterpriseArtifactOutput(outputPath), ui: { operation_summary: '生成最终材料。' } },
    depends_on: ['review'],
    node_key: finalNodeKey,
    node_type: 'agent',
    title: '最终交付',
  };
}

function workflowRunMetadataWithFinalOutput(path) {
  return { final_output: { path } };
}

function workflowRunMetadataWithFilePath(path) {
  return { final_output: { kind: 'file', path } };
}

function workflowRunMetadataWithFinalFile(path) {
  return {
    final_output: {
      kind: 'file',
      path,
      role: 'final_output',
      source_node_key: 'generate_video_mp4',
    },
  };
}

function workflowRunMetadataWithoutFinalOutput() {
  return { final_output: {} };
}

function commandCardRef(commandRef) {
  return { command_ref: commandRef, kind: 'command_card' };
}

function verifierExecFixture() {
  return {
    agent_key: 'reviewer',
    cwd: '/repo/app',
    model: 'opus',
    prompt_key: 'main/reviewer',
    provider: 'claude',
  };
}

function agentExecFixture(agentKey, cwd) {
  return { agent_key: agentKey, cwd };
}

function verifySharedFileOutputs(path) {
  return { to_sharedfile: { path } };
}

function workflowSharedFileOutputs(path) {
  return {
    to_node_result: true,
    to_sharedfile: { lock_mode: 'exclusive', path },
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

export {
  CancelledError,
  backend,
  deferred,
  renderWorkflowPage,
  mockWorkflowDag,
  workflowDesignStore,
  enterpriseTemplateDetail,
  enterpriseTemplateDetails,
  fillEnterpriseTemplateForm,
  mockEnterpriseTemplates,
  workflowRunMetadataWithFinalOutput,
  workflowRunMetadataWithFilePath,
  workflowRunMetadataWithFinalFile,
  workflowRunMetadataWithoutFinalOutput,
  commandCardRef,
  verifierExecFixture,
  agentExecFixture,
  verifySharedFileOutputs,
  workflowSharedFileOutputs,
  openTemplateCatalog,
  openTemplateCatalog as openEnterpriseTemplateCatalog,
};
