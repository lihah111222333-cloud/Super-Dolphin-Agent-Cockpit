import { BarChart3, Bell, ClipboardList, FileText, Presentation, ShieldCheck, Video } from 'lucide-react';
import { errorMessage, firstText, objectValue, parseStrictJsonValue, textValue } from '../../shared/pageShared.js';

const DAG_SCHEDULE_TIMEZONE = 'Asia/Shanghai';
const IDEMPOTENCY_RANDOM_RADIX = 16;

const ENTERPRISE_TEMPLATE_ICON_BY_ID = Object.freeze({
  'government-enterprise/promo-video': Video,
  'government-enterprise/daily-weekly-report': FileText,
  'government-enterprise/project-briefing': Presentation,
  'government-enterprise/meeting-minutes': Bell,
  'government-enterprise/data-analysis-brief': BarChart3,
  'government-enterprise/approval-material': ShieldCheck,
});

const ENTERPRISE_TEMPLATE_DEFAULTS = Object.freeze({
  timezone: DAG_SCHEDULE_TIMEZONE,
  output_format: 'markdown',
});

const ENTERPRISE_DESIGN_PHASES = Object.freeze([
  '场景理解',
  '阶段拆分',
  '资源/skill 发现',
  'DAG 生成',
  '等待运行',
]);

const DAG_DESIGNER_ENABLED_TOOLS = Object.freeze([
  'list_models',
  'prompt_list',
  'command_list',
  'shared_file_list',
  'workflow_template_list',
  'workflow_template_get',
  'workflow_template_render_dag',
  'task_create_dag',
  'task_get_dag',
  'task_get_run',
  'task_list_runs',
  'task_dag_apply_ops',
  'task_dispatch_node',
  'task_start_dag',
]);

const ENTERPRISE_REQUIRED_TEMPLATE_FIELDS = Object.freeze([
  'template_id',
  'template_version',
  'ui_schema',
  'dag_template',
  'review_node',
  'final_node_key',
  'outputs.to_sharedfile',
  'outputs.to_artifact',
  'config.ui',
]);

function firstPresent(...values) {
  return values.find((value) => value !== undefined && value !== null);
}

function selectCompatField(label, ...values) {
  const value = firstPresent(...values);
  if (value === undefined) throw new Error(`${label} is required`);
  return value;
}

function optionalArrayField(value, label) {
  if (value === undefined || value === null) return [];
  if (!Array.isArray(value)) throw new Error(`${label} must be an array`);
  return value;
}

function requireArrayField(value, label) {
  if (!Array.isArray(value)) throw new Error(`${label} must be an array`);
  return value;
}

function requireObjectField(value, label) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error(`${label} must be an object`);
  return value;
}

function parseJsonObject(value, label) {
  try {
    const parsed = parseStrictJsonValue(value, label);
    return requireObjectField(parsed, label);
  } catch (error) {
    throw new Error(`${label} JSON object parse failed: ${errorMessage(error)}`, { cause: error });
  }
}

function enterpriseNodeDependencies(node) {
  return optionalArrayField(firstPresent(node?.depends_on, node?.dependsOn), 'workflow node depends_on');
}

function uniqueWorkflowActionKey(prefix) {
  if (typeof crypto?.randomUUID === 'function') return `${prefix}-${crypto.randomUUID()}`;
  return `${prefix}-${Math.random().toString(IDEMPOTENCY_RANDOM_RADIX).slice(2)}`;
}

function workflowMonotonicTimestamp() {
  return Math.trunc(performance.timeOrigin + performance.now());
}

function uniqueWorkflowMaterialStamp() {
  return Math.trunc(performance.timeOrigin + performance.now()).toString(IDEMPOTENCY_RANDOM_RADIX);
}

function enterpriseTemplateCompat(template) {
  return {
    id: selectCompatField('workflow template id', template?.id, template?.key, template?.template_id, template?.templateId),
    businessFlow: firstPresent(template?.business_flow, template?.businessFlow),
    outputTypes: firstPresent(template?.output_types, template?.outputTypes, template?.outputFormats),
    uiSchema: firstPresent(template?.ui_schema, template?.uiSchema),
    dagTemplate: firstPresent(template?.dag_template, template?.dagTemplate),
    finalOutput: firstPresent(template?.final_output, template?.finalOutput),
    finalNodeKey: firstPresent(template?.final_node_key, template?.finalNodeKey),
    supportsSchedule: firstPresent(template?.supports_schedule, template?.supportsSchedule),
    requiresReview: firstPresent(template?.requires_review, template?.requiresReview),
    availableVersions: firstPresent(template?.available_versions, template?.availableVersions),
    estimatedNodes: firstPresent(template?.estimated_nodes, template?.estimatedNodes),
  };
}

function enterpriseDagTemplateCompat(dagTemplate) {
  return {
    dagKeyTemplate: firstPresent(dagTemplate?.dag_key_template, dagTemplate?.dagKeyTemplate),
    titleTemplate: firstPresent(dagTemplate?.title_template, dagTemplate?.titleTemplate),
    descriptionTemplate: firstPresent(dagTemplate?.description_template, dagTemplate?.descriptionTemplate),
    finalNodeKey: firstPresent(dagTemplate?.final_node_key, dagTemplate?.finalNodeKey),
  };
}

// 生成给 DAG 设计器的首轮模板需求，约束它按模板库参数先发现资源再创建可运行 DAG。
function buildEnterpriseWorkflowTemplateBrief(template) {
  const values = objectValue(template.templateValues);
  const templateId = enterpriseTemplateId(template);
  const compat = enterpriseTemplateCompat(template);
  const outputFormat = firstText(values.output_format, template.selectedOutputFormat, firstEnterpriseOutputType(template), 'markdown');
  const outputPath = firstText(values.output_path, enterpriseTemplateDefaultOutputPath(templateId));
  const outputTypes = enterpriseOutputTypes(template);
  const dagTemplate = requireObjectField(compat.dagTemplate, 'workflow template dag_template');
  const dagCompat = enterpriseDagTemplateCompat(dagTemplate);
  const nodes = requireArrayField(dagTemplate.nodes, 'workflow template dag_template.nodes');
  const finalNodeKey = firstText(dagCompat.finalNodeKey, compat.finalNodeKey);
  const reviewNode = nodes.find((node) => enterpriseNodeKey(node).includes('review')) || nodes.find((node) => enterpriseNodeTitle(node).includes('复核')) || null;
  const draftPreview = template.draftPreview || renderEnterpriseTemplatePreview(template, values);
  return [
    `请基于政企工作流模板库中的“${enterpriseTemplateTitle(template)}”创建可运行 DAG。`,
    `template_id: ${templateId}`,
    `template_version: ${enterpriseTemplateVersion(template)}`,
    `business_flow: ${textValue(compat.businessFlow)}`,
    `场景说明: ${enterpriseTemplateDescription(template)}`,
    `目标输出格式: ${outputFormat}`,
    `可选输出格式: ${outputTypes.join(', ')}`,
    'Only use output_format values explicitly listed by this template. Do not add pdf/docx/pptx/xlsx unless the template lists that exact value and a real artifact or conversion tool is discovered.',
    `默认输出路径: ${outputPath}`,
    `final_node_key: ${finalNodeKey}`,
    `review_node: ${reviewNode ? enterpriseNodeKey(reviewNode) : '必须从模板节点中识别复核节点'}`,
    `用户参数: ${JSON.stringify(values, null, 2)}`,
    `ui_schema: ${JSON.stringify(enterpriseTemplateFields(template), null, 2)}`,
    `dag_template: ${JSON.stringify(dagTemplate, null, 2)}`,
    `dag_preview: ${JSON.stringify(draftPreview, null, 2)}`,
    'workflow_template tools: first call workflow_template_list/get/render_dag for the same built-in template library; workflow_template_render_dag only renders a DAG draft and must not persist or start a DAG.',
    `模板必备字段: ${ENTERPRISE_REQUIRED_TEMPLATE_FIELDS.join(', ')}`,
    `DAG 设计器工具白名单: ${DAG_DESIGNER_ENABLED_TOOLS.join(', ')}。`,
    '创建 DAG 前先说明阶段数、依赖关系、顺序/并行关系、每阶段 skill/prompt/command_card 选择和最终材料路径。',
    '必须先调用 list_models、prompt_list、command_list、shared_file_list 发现当前资源，再通过 task_create_dag 落库。',
    '不得硬编码 provider、model、prompt_key、agent_key、command_ref 或 sharedfile path。',
    '所有可运行节点必须设置顶层 assigned_to，执行配置必须放在 node.config.exec。',
    'automation 只允许使用 command_list 发现到的 command_card；未发现合适 command_card 时，改用 agent 节点说明需要用户提供数据或人工处理。',
    '所有模板必须包含复核节点；最终交付只能来自复核后的唯一 final_node_key，不要宣称已有 DAG 级审批阻断。',
    '支持定时的模板默认使用 CRON_TZ=Asia/Shanghai；用户未填写具体 cron 或执行时间时必须先确认，不要替用户猜测。',
    '中间大文本必须写 outputs.to_sharedfile；文档型阶段（报告、审批材料、纪要、草稿、复核意见）必须设置 outputs.to_node_result=false，不能把正文写入 node.result。',
    '下游节点需要读取上游正文时，必须在 config.inputs.from_sharedfiles 引用上游 outputs.to_sharedfile.path；depends_on 只控制调度顺序，config.ui.input_sources 只供界面展示。',
    '文档最终交付使用 outputs.to_artifact，source_tool=document_renderer，source_text_field=document_text，path_template 指向 final.{{output_format}} 或已渲染的 final.docx/final.pdf。',
    '视频成片使用 outputs.to_artifact，并保留 video_with_audio 的结构化成功/失败 JSON 契约。',
    '每个节点必须保留或补全 config.ui：stage_key、stage_title、execution_mode、operation_summary、model_action、skills、input_sources、expected_outputs。',
    'config.ui.operation_summary 用来给用户悬停节点时展示该节点计划执行的大模型操作；不要输出或要求暴露隐藏思维链。',
    '如果 pptx、docx、xlsx、pdf、mp4 等目标格式需要的生成工具或 command_card 未发现，必须显式提示能力缺口，不能伪造二进制产物、外部发布动作或静默降级。',
    '最终交付必须使用唯一 final_node_key，并说明该节点如何提升为 run-level final_output。',
    '首版只创建可运行 DAG 草案；外部发布、OA/IM/网盘/审批系统流转和真实人工审批都需要用户确认后另行配置。',
  ].join('\n');
}

function enterpriseTemplateId(template) {
  return textValue(enterpriseTemplateCompat(template).id);
}

function enterpriseTemplateVersion(template) {
  if (template?.version == null) return '';
  return String(template.version).trim();
}

function enterpriseTemplateVersionNumber(template) {
  const value = Number(template?.version);
  return Number.isInteger(value) && value > 0 ? value : 1;
}

function enterpriseAvailableVersions(template) {
  const raw = optionalArrayField(enterpriseTemplateCompat(template).availableVersions, 'workflow template available_versions');
  const versions = new Set();
  for (const item of raw) {
    const version = Number(item);
    if (Number.isInteger(version) && version > 0) versions.add(version);
  }
  const sortedVersions = Array.from(versions);
  sortedVersions.sort((left, right) => left - right);
  return sortedVersions;
}

function enterpriseRollbackVersion(template) {
  const current = enterpriseTemplateVersionNumber(template);
  const versions = enterpriseAvailableVersions(template).filter((version) => version < current);
  return versions.at(-1) ?? 0;
}

function enterpriseTemplateTrustLevel(template) {
  return firstText(template?.trust?.level, template?.trustLevel);
}

function enterpriseTemplateCompatibilityRuntime(template) {
  return firstText(template?.compatibility?.runtime, template?.runtime);
}

function enterpriseTemplateNodeTypes(template) {
  const nodeTypes = optionalArrayField(firstPresent(template?.compatibility?.node_types, template?.compatibility?.nodeTypes), 'workflow template compatibility.node_types');
  return nodeTypes.flatMap((item) => {
    const nodeType = textValue(item);
    return nodeType ? [nodeType] : [];
  }).join(', ');
}

function enterpriseTemplateSearchText(template) {
  const terms = [];
  for (const item of [
    enterpriseTemplateId(template),
    enterpriseTemplateTitle(template),
    enterpriseTemplateDescription(template),
    firstText(template?.business_flow, template?.businessFlow),
    ...(Array.isArray(template?.tags) ? template.tags : []),
  ]) {
    const term = textValue(item).toLowerCase();
    if (term) terms.push(term);
  }
  return terms.join(' ');
}

function enterpriseTemplateOutputSlug(templateId) {
  return textValue(templateId).replace(/[^a-z0-9]+/gi, '_').replace(/^_+|_+$/g, '').toLowerCase();
}

function enterpriseTemplateDefaultOutputPath(templateId) {
  const slug = enterpriseTemplateOutputSlug(templateId) || 'government_enterprise_workflow';
  return `reports/workflows/${slug}/{{run_id}}/`;
}

function enterpriseTemplateTitle(template) {
  const title = template?.title;
  if (typeof title === 'string') return textValue(title);
  return firstText(title?.zh, title?.en, template?.name, enterpriseTemplateId(template));
}

function enterpriseTemplateDescription(template) {
  const description = template?.description;
  if (typeof description === 'string') return textValue(description);
  return firstText(description?.zh, description?.en, template?.summary);
}

function enterpriseOutputTypes(template) {
  const raw = requireArrayField(enterpriseTemplateCompat(template).outputTypes, 'workflow template output_types');
  if (raw.length === 0) throw new Error('workflow template output_types must not be empty');
  const outputTypes = raw.flatMap((item) => {
    const value = textValue(item);
    return value ? [value] : [];
  });
  if (outputTypes.length === 0) throw new Error('workflow template output_types has no usable values');
  return outputTypes;
}

function firstEnterpriseOutputType(template) {
  return enterpriseOutputTypes(template).at(0) ?? 'markdown';
}

function enterpriseNodeKey(node) {
  return firstText(node?.node_key, node?.nodeKey, node?.key);
}

function enterpriseNodeTitle(node) {
  return firstText(node?.title, node?.name, enterpriseNodeKey(node));
}

function enterpriseTemplateIcon(template) {
  return ENTERPRISE_TEMPLATE_ICON_BY_ID[enterpriseTemplateId(template)] ?? ClipboardList;
}

function enterpriseTemplateFields(template) {
  return requireArrayField(enterpriseTemplateCompat(template).uiSchema, 'workflow template ui_schema');
}

function enterpriseTemplateDefaultValues(template) {
  const defaults = { ...ENTERPRISE_TEMPLATE_DEFAULTS };
  for (const field of enterpriseTemplateFields(template)) {
    if (field.key === 'output_path') {
      defaults[field.key] = enterpriseTemplateDefaultOutputPath(enterpriseTemplateId(template));
    } else if (field.key === 'output_format') {
      defaults[field.key] = firstEnterpriseOutputType(template);
    } else if (field.key === 'timezone') {
      defaults[field.key] = DAG_SCHEDULE_TIMEZONE;
    } else if (field.type === 'boolean') {
      defaults[field.key] = false;
    } else {
      defaults[field.key] = '';
    }
  }
  return defaults;
}

function enterpriseFieldLabel(field) {
  if (typeof field?.label === 'string') return textValue(field.label);
  return firstText(field?.label?.zh, field?.key);
}

function enterpriseFieldHelp(field) {
  if (typeof field?.help === 'string') return textValue(field.help);
  return textValue(field?.help?.zh);
}

function enterpriseFieldPlaceholder(field) {
  if (typeof field?.placeholder === 'string') return textValue(field.placeholder);
  return textValue(field?.placeholder?.zh);
}

function enterpriseFieldOptions(field) {
  return optionalArrayField(field?.options, `workflow template field ${textValue(field?.key)} options`);
}

function enterpriseOptionLabel(option) {
  if (typeof option?.label === 'string') return textValue(option.label);
  return firstText(option?.label?.zh, option?.value);
}

function renderEnterpriseTemplatePreview(template, values = {}) {
  const compat = enterpriseTemplateCompat(template);
  const dagTemplate = requireObjectField(compat.dagTemplate, 'workflow template dag_template');
  const dagCompat = enterpriseDagTemplateCompat(dagTemplate);
  const finalOutput = requireObjectField(compat.finalOutput, 'workflow template final_output');
  const nodes = requireArrayField(dagTemplate.nodes, 'workflow template dag_template.nodes');
  return {
    dag_key: renderEnterprisePlaceholders(firstText(dagCompat.dagKeyTemplate, enterpriseTemplateId(template)), values),
    title: renderEnterprisePlaceholders(firstText(dagCompat.titleTemplate, enterpriseTemplateTitle(template)), values),
    description: renderEnterprisePlaceholders(firstText(dagCompat.descriptionTemplate, enterpriseTemplateDescription(template)), values),
    trigger: textValue(dagTemplate.trigger || 'manual'),
    final_node_key: textValue(dagCompat.finalNodeKey),
    final_output: renderEnterpriseValue(finalOutput, values),
    nodes: nodes.map((node) => ({
      node_key: enterpriseNodeKey(node),
      title: renderEnterprisePlaceholders(enterpriseNodeTitle(node), values),
      node_type: textValue(node.node_type || node.nodeType),
      assigned_to: textValue(node.assigned_to || node.assignedTo),
      depends_on: enterpriseNodeDependencies(node),
      config: renderEnterpriseValue(objectValue(node.config), values),
    })),
  };
}

function enterpriseSaveTemplatePayload(template, draft) {
  return {
    templateId: enterpriseTemplateId(template),
    version: enterpriseTemplateVersionNumber(template) + 1,
    title: template.title,
    description: template.description,
    category: textValue(template.category),
    business_flow: textValue(template.business_flow || template.businessFlow),
    output_types: enterpriseOutputTypes(template),
    tags: Array.isArray(template.tags) ? template.tags : [],
    requires_review: Boolean(template.requires_review || template.requiresReview),
    supports_schedule: Boolean(template.supports_schedule || template.supportsSchedule),
    trust: { level: 'user', source: 'save_as_template' },
    compatibility: objectValue(template.compatibility),
    ui_schema: enterpriseTemplateFields(template),
    validation: objectValue(template.validation),
    draft,
  };
}

function enterpriseCreateAndStartDAGPayload(draft) {
  const normalized = objectValue(draft);
  return {
    dagKey: textValue(normalized.dag_key || normalized.dagKey),
    title: textValue(normalized.title),
    description: textValue(normalized.description),
    finalNodeKey: textValue(normalized.final_node_key || normalized.finalNodeKey),
    metadata: objectValue(normalized.metadata),
    nodes: enterpriseCreateDAGNodes(normalized.nodes),
    idempotencyKey: uniqueWorkflowActionKey('ui-template'),
  };
}

function enterpriseCreateDAGNodes(nodes) {
  if (!Array.isArray(nodes)) return [];
  return nodes.map((node) => ({
    nodeKey: enterpriseNodeKey(node),
    title: enterpriseNodeTitle(node),
    nodeType: textValue(node.node_type || node.nodeType),
    assignedTo: textValue(node.assigned_to || node.assignedTo),
    dependsOn: Array.isArray(node.depends_on || node.dependsOn) ? (node.depends_on || node.dependsOn) : [],
    commandRef: textValue(node.command_ref || node.commandRef),
    config: objectValue(node.config),
  }));
}

function renderEnterpriseValue(value, values) {
  if (typeof value === 'string') return renderEnterprisePlaceholders(value, values);
  if (Array.isArray(value)) return value.map((item) => renderEnterpriseValue(item, values));
  if (value && typeof value === 'object') {
    return Object.fromEntries(Object.entries(value).map(([key, item]) => [key, renderEnterpriseValue(item, values)]));
  }
  return value;
}

function renderEnterprisePlaceholders(value, values) {
  let output = textValue(value);
  for (const [key, raw] of Object.entries(objectValue(values))) {
    output = output.replaceAll(`{{${key}}}`, textValue(raw));
  }
  return output;
}

export {
  buildEnterpriseWorkflowTemplateBrief,
  ENTERPRISE_DESIGN_PHASES,
  ENTERPRISE_REQUIRED_TEMPLATE_FIELDS,
  ENTERPRISE_TEMPLATE_DEFAULTS,
  enterpriseAvailableVersions,
  enterpriseCreateAndStartDAGPayload,
  enterpriseDagTemplateCompat,
  enterpriseFieldHelp,
  enterpriseFieldLabel,
  enterpriseFieldOptions,
  enterpriseFieldPlaceholder,
  enterpriseNodeDependencies,
  enterpriseNodeKey,
  enterpriseNodeTitle,
  enterpriseOptionLabel,
  enterpriseOutputTypes,
  enterpriseRollbackVersion,
  enterpriseSaveTemplatePayload,
  enterpriseTemplateCompat,
  enterpriseTemplateCompatibilityRuntime,
  enterpriseTemplateDefaultOutputPath,
  enterpriseTemplateDefaultValues,
  enterpriseTemplateDescription,
  enterpriseTemplateFields,
  enterpriseTemplateIcon,
  enterpriseTemplateId,
  enterpriseTemplateNodeTypes,
  enterpriseTemplateOutputSlug,
  enterpriseTemplateSearchText,
  enterpriseTemplateTitle,
  enterpriseTemplateTrustLevel,
  enterpriseTemplateVersion,
  enterpriseTemplateVersionNumber,
  firstEnterpriseOutputType,
  firstPresent,
  optionalArrayField,
  parseJsonObject,
  renderEnterprisePlaceholders,
  renderEnterpriseTemplatePreview,
  renderEnterpriseValue,
  requireArrayField,
  requireObjectField,
  selectCompatField,
  DAG_DESIGNER_ENABLED_TOOLS,
  uniqueWorkflowActionKey,
  uniqueWorkflowMaterialStamp,
  workflowMonotonicTimestamp,
};
