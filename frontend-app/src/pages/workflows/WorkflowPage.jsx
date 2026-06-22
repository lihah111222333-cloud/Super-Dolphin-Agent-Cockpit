import React, { useCallback, useEffect, useMemo, useReducer, useRef, useState } from 'react';
import { isCancelledError, useQuery, useQueryClient } from '@tanstack/react-query';
import { Workflow, ArrowLeft, Clock, Bell, BarChart3, ClipboardList, FileText, Presentation, ShieldCheck, Video } from 'lucide-react';
import { APP_COPY } from '../../shared/i18n/appI18n.js';
import { FocusTrapDialog } from '../../shared/ui/FocusTrapDialog.jsx';
import { appendCurrentModelOption, dashboardQueryErrorState, dashboardQueryKey, errorMessage, firstText, listToText, numberOrNull, objectValue, optionalSettingsCwd, queryHasSnapshot, SKILLS_REQUEST_TIMEOUT_MS, textValue, withTimeout, wordListFromText } from '../shared/pageShared.js';
import { PageHeader, Panel, RetryableSyncError } from '../shared/pageComponents.jsx';
import { finalOutputPath, workflowOrderedNodes } from './adapters/workflowDisplayAdapter.js';
import { WorkflowDiagnostics } from './components/WorkflowDiagnostics.jsx';
import { WorkflowFinalOutputPanel } from './components/WorkflowFinalOutputPanel.jsx';
import { applyDagOps, deleteDag, dispatchDagNode, getDashboardPage, getDagDetail, getDagRun, getDagRuns, getWorkflowTemplate, listWorkflowTemplates, openSharedFile, readSharedFile, renderWorkflowTemplateDraft, rollbackWorkflowTemplate, saveWorkflowTemplate, startDag, startThread, startTurn, terminateDagRun } from './services/workflowPageService.js';
import './WorkflowPage.css';

const DAG_RECENT_RUN_LIMIT = 30;
const DAG_RUN_HISTORY_VISIBLE_LIMIT = 10;
const MAX_SCHEDULE_HOUR = 23;
const MAX_SCHEDULE_MINUTE = 59;
const DAYS_IN_MONTH = 31;
const IDEMPOTENCY_RANDOM_RADIX = 16;
const EMPTY_STATE_ICON_SIZE = 34;
const DAG_SCHEDULE_TIMEZONE = 'Asia/Shanghai';
const DAG_SCHEDULE_CRON_TZ_PREFIX = `CRON_TZ=${DAG_SCHEDULE_TIMEZONE}`;
const ENTERPRISE_OUTPUT_FORMATS = Object.freeze(['markdown', 'json', 'pdf', 'docx', 'pptx', 'xlsx', 'video']);
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

const ENTERPRISE_REQUIRED_TEMPLATE_FIELDS = Object.freeze([
  'template_id',
  'template_version',
  'ui_schema',
  'dag_template',
  'review_node',
  'final_node_key',
  'outputs.to_sharedfile',
  'config.ui',
]);

// 生成给 DAG 设计器的首轮模板需求，约束它按模板库参数先发现资源再创建可运行 DAG。
function buildEnterpriseWorkflowTemplateBrief(template) {
  const values = objectValue(template.templateValues);
  const templateId = enterpriseTemplateId(template);
  const outputFormat = textValue(values.output_format || template.selectedOutputFormat || firstEnterpriseOutputType(template)) || 'markdown';
  const outputPath = textValue(values.output_path) || enterpriseTemplateDefaultOutputPath(templateId);
  const outputTypes = enterpriseOutputTypes(template);
  const dagTemplate = objectValue(template.dag_template || template.dagTemplate);
  const nodes = Array.isArray(dagTemplate.nodes) ? dagTemplate.nodes : [];
  const finalNodeKey = textValue(dagTemplate.final_node_key || dagTemplate.finalNodeKey || template.finalNodeKey);
  const reviewNode = nodes.find((node) => enterpriseNodeKey(node).includes('review')) || nodes.find((node) => enterpriseNodeTitle(node).includes('复核')) || null;
  const draftPreview = template.draftPreview || renderEnterpriseTemplatePreview(template, values);
  return [
    `请基于政企工作流模板库中的“${enterpriseTemplateTitle(template)}”创建可运行 DAG。`,
    `template_id: ${templateId}`,
    `template_version: ${enterpriseTemplateVersion(template)}`,
    `business_flow: ${textValue(template.business_flow || template.businessFlow)}`,
    `场景说明: ${enterpriseTemplateDescription(template)}`,
    `目标输出格式: ${outputFormat}`,
    `可选输出格式: ${outputTypes.join(', ')}`,
    `默认输出路径: ${outputPath}`,
    `final_node_key: ${finalNodeKey}`,
    `review_node: ${reviewNode ? enterpriseNodeKey(reviewNode) : '必须从模板节点中识别复核节点'}`,
    `用户参数: ${JSON.stringify(values, null, 2)}`,
    `ui_schema: ${JSON.stringify(template.ui_schema || template.uiSchema || [], null, 2)}`,
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
    '大结果必须写 outputs.to_sharedfile；视频成片使用 outputs.to_artifact，并保留 video_with_audio 的结构化成功/失败 JSON 契约。',
    '每个节点必须保留或补全 config.ui：stage_key、stage_title、execution_mode、operation_summary、model_action、skills、input_sources、expected_outputs。',
    'config.ui.operation_summary 用来给用户悬停节点时展示该节点计划执行的大模型操作；不要输出或要求暴露隐藏思维链。',
    '如果 pptx、docx、xlsx、pdf、mp4 等目标格式需要的生成工具或 command_card 未发现，必须显式提示能力缺口，不能伪造二进制产物、外部发布动作或静默降级。',
    '最终交付必须使用唯一 final_node_key，并说明该节点如何提升为 run-level final_output。',
    '首版只创建可运行 DAG 草案；外部发布、OA/IM/网盘/审批系统流转和真实人工审批都需要用户确认后另行配置。',
  ].join('\n');
}

function enterpriseTemplateId(template) {
  return textValue(template?.id || template?.key || template?.template_id || template?.templateId);
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
  const raw = template?.available_versions || template?.availableVersions || [];
  if (!Array.isArray(raw)) return [];
  return [...new Set(raw.map((item) => Number(item)).filter((item) => Number.isInteger(item) && item > 0))]
    .sort((left, right) => left - right);
}

function enterpriseRollbackVersion(template) {
  const current = enterpriseTemplateVersionNumber(template);
  const versions = enterpriseAvailableVersions(template).filter((version) => version < current);
  return versions.at(-1) || 0;
}

function enterpriseTemplateTrustLevel(template) {
  return textValue(template?.trust?.level || template?.trustLevel);
}

function enterpriseTemplateCompatibilityRuntime(template) {
  return textValue(template?.compatibility?.runtime || template?.runtime);
}

function enterpriseTemplateNodeTypes(template) {
  const nodeTypes = template?.compatibility?.node_types || template?.compatibility?.nodeTypes || [];
  if (!Array.isArray(nodeTypes)) return '';
  return nodeTypes.map((item) => textValue(item)).filter(Boolean).join(', ');
}

function enterpriseTemplateSearchText(template) {
  return [
    enterpriseTemplateId(template),
    enterpriseTemplateTitle(template),
    enterpriseTemplateDescription(template),
    textValue(template?.business_flow || template?.businessFlow),
    ...(Array.isArray(template?.tags) ? template.tags : []),
  ].map((item) => textValue(item).toLowerCase()).filter(Boolean).join(' ');
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
  return textValue(title?.zh || title?.en || template?.name || enterpriseTemplateId(template));
}

function enterpriseTemplateDescription(template) {
  const description = template?.description;
  if (typeof description === 'string') return textValue(description);
  return textValue(description?.zh || description?.en || template?.summary);
}

function enterpriseOutputTypes(template) {
  const raw = template?.output_types || template?.outputTypes || template?.outputFormats;
  if (!Array.isArray(raw) || raw.length === 0) return ENTERPRISE_OUTPUT_FORMATS;
  const outputTypes = raw.flatMap((item) => {
    const value = textValue(item);
    return value ? [value] : [];
  });
  return outputTypes.length > 0 ? outputTypes : ENTERPRISE_OUTPUT_FORMATS;
}

function firstEnterpriseOutputType(template) {
  return enterpriseOutputTypes(template)[0] || 'markdown';
}

function enterpriseNodeKey(node) {
  return textValue(node?.node_key || node?.nodeKey || node?.key);
}

function enterpriseNodeTitle(node) {
  return textValue(node?.title || node?.name || enterpriseNodeKey(node));
}

function enterpriseTemplateIcon(template) {
  return ENTERPRISE_TEMPLATE_ICON_BY_ID[enterpriseTemplateId(template)] || ClipboardList;
}

function enterpriseTemplateFields(template) {
  const fields = template?.ui_schema || template?.uiSchema;
  return Array.isArray(fields) ? fields : [];
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
  return textValue(field?.label?.zh || field?.key);
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
  return Array.isArray(field?.options) ? field.options : [];
}

function enterpriseOptionLabel(option) {
  if (typeof option?.label === 'string') return textValue(option.label);
  return textValue(option?.label?.zh || option?.value);
}

function renderEnterpriseTemplatePreview(template, values = {}) {
  const dagTemplate = objectValue(template?.dag_template || template?.dagTemplate);
  const finalOutput = objectValue(template?.final_output || template?.finalOutput);
  const nodes = Array.isArray(dagTemplate.nodes) ? dagTemplate.nodes : [];
  return {
    dag_key: renderEnterprisePlaceholders(dagTemplate.dag_key_template || dagTemplate.dagKeyTemplate || enterpriseTemplateId(template), values),
    title: renderEnterprisePlaceholders(dagTemplate.title_template || dagTemplate.titleTemplate || enterpriseTemplateTitle(template), values),
    description: renderEnterprisePlaceholders(dagTemplate.description_template || dagTemplate.descriptionTemplate || enterpriseTemplateDescription(template), values),
    trigger: textValue(dagTemplate.trigger || 'manual'),
    final_node_key: textValue(dagTemplate.final_node_key || dagTemplate.finalNodeKey),
    final_output: renderEnterpriseValue(finalOutput, values),
    nodes: nodes.map((node) => ({
      node_key: enterpriseNodeKey(node),
      title: renderEnterprisePlaceholders(enterpriseNodeTitle(node), values),
      node_type: textValue(node.node_type || node.nodeType),
      assigned_to: textValue(node.assigned_to || node.assignedTo),
      depends_on: Array.isArray(node.depends_on || node.dependsOn) ? (node.depends_on || node.dependsOn) : [],
      config: renderEnterpriseValue(node.config || {}, values),
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
  for (const [key, raw] of Object.entries(values || {})) {
    output = output.replaceAll(`{{${key}}}`, textValue(raw));
  }
  return output;
}

const DAG_CATEGORIES = Object.freeze([
  { key: 'running', label: '进行中' },
  { key: 'scheduled', label: '定时任务' },
  { key: 'history', label: '历史记录' },
]);

const STARTABLE_DAG_STATUSES = new Set(['draft', 'ready']);

const STARTABLE_DAG_TRIGGERS = new Set(['manual', 'scheduled', 'schedule', 'cron', '']);

const RUNNING_RUN_STATUSES = new Set(['running', 'pending', 'dispatching', 'waiting_for_assignee']);

const CONFIGURABLE_NODE_TYPES = new Set(['agent', 'automation']);

const WORKFLOW_ACTION_TIMEOUT_MESSAGE = '自动化操作超时，请检查任务数据或后端状态。';

/*
 * WorkflowPage 把后端 DAG 数据整理成页面 model。
 * 列表、详情、运行记录先归一化，组件只读整理后的字段。
 */

function withWorkflowActionTimeout(promise) {
  return withTimeout(promise, SKILLS_REQUEST_TIMEOUT_MS, WORKFLOW_ACTION_TIMEOUT_MESSAGE);
}

function workflowDashboardQueryErrorState(query, hasSnapshot = queryHasSnapshot(query)) {
  if (isCancelledError(query?.error)) return { cachedSyncError: '', blockingError: '' };
  return dashboardQueryErrorState(query, hasSnapshot);
}

async function fetchDagsDashboard(cwd) {
  const response = await withTimeout(
    getDashboardPage({ cwd, page: 'dags' }),
    SKILLS_REQUEST_TIMEOUT_MS,
    '自动化加载超时，请检查任务数据或后端状态。',
  );
  return normalizeDagsResponse(response);
}

const workflowQueryOperations = new Map();

function coalesceWorkflowQueryOperation(queryKey, operation) {
  const operationKey = JSON.stringify(queryKey);
  const activeOperation = workflowQueryOperations.get(operationKey);
  if (activeOperation) return activeOperation;
  const operationPromise = Promise.resolve().then(operation);
  const trackedOperation = operationPromise.finally(() => {
    if (workflowQueryOperations.get(operationKey) === trackedOperation) {
      workflowQueryOperations.delete(operationKey);
    }
  });
  workflowQueryOperations.set(operationKey, trackedOperation);
  return trackedOperation;
}

function invalidateWorkflowQuery(queryClient, queryKey) {
  return coalesceWorkflowQueryOperation(queryKey, () => (
    queryClient.invalidateQueries({ queryKey }, { cancelRefetch: false, throwOnError: true })
  ));
}

function fetchWorkflowQuery(queryClient, queryKey, queryFn) {
  return coalesceWorkflowQueryOperation(queryKey, () => queryClient.fetchQuery({ queryKey, queryFn }));
}

function cleanObject(payload) {
  return Object.fromEntries(
    Object.entries(payload).filter(([, value]) => value !== undefined && value !== ''),
  );
}

function dagKeyOf(raw) {
  return firstText(raw?.dag_key, raw?.dagKey, raw?.key, raw?.id);
}

function runKeyOf(raw) {
  return firstText(raw?.run_key, raw?.runKey, raw?.key, raw?.id);
}

function nodeKeyOf(raw) {
  return firstText(raw?.node_key, raw?.nodeKey, raw?.key, raw?.id);
}

function dagVersionOf(item) {
  return numberOrNull(item?.version ?? item?.dag_version ?? item?.dagVersion ?? item?.raw?.version);
}

function normalizeDagRun(raw = {}, index = 0) {
  /*
   * runKey 用来选中运行记录；数字 runId 只在派发节点时用。
   */
  const runKey = runKeyOf(raw);
  return {
    id: runKey || `run:${index}`,
    runId: numberOrNull(raw.id ?? raw.run_id ?? raw.runId),
    runKey,
    status: firstText(raw.status, raw.state),
    triggerSource: firstText(raw.trigger_source, raw.triggerSource),
    startedAt: firstText(raw.started_at, raw.startedAt, raw.created_at, raw.createdAt),
    finishedAt: firstText(raw.finished_at, raw.finishedAt),
    metadata: objectValue(raw.metadata),
    raw,
  };
}

function normalizedDagNodeDependencies(raw = {}) {
  if (Array.isArray(raw.depends_on)) return raw.depends_on;
  if (Array.isArray(raw.dependsOn)) return raw.dependsOn;
  return wordListFromText(raw.depends_on || raw.dependsOn || '');
}

function normalizeDagNode(raw = {}, index = 0) {
  /*
   * node 展示读整理后的字段，raw/config/result 留给高级设置和保存。
   */
  const nodeKey = nodeKeyOf(raw);
  const config = objectValue(raw.config);
  const dependsOn = normalizedDagNodeDependencies(raw);
  return {
    id: nodeKey || `node:${index}`,
    nodeKey,
    title: firstText(raw.title, raw.name, nodeKey, `节点 ${index + 1}`),
    nodeType: firstText(raw.node_type, raw.nodeType, raw.type),
    assignedTo: firstText(raw.assigned_to, raw.assignedTo),
    dependsOn,
    status: firstText(raw.status, raw.state),
    threadId: firstText(raw.spawning_thread_id, raw.spawningThreadId, raw.threadId, raw.thread_id),
    activeWakeupId: numberOrNull(raw.active_wakeup_id ?? raw.activeWakeupId),
    config,
    result: parsedDagConfig(raw.result),
    raw,
  };
}

function normalizeDashboardDag(raw = {}, index = 0) {
  /*
   * 列表里的 DAG 只有摘要和最近运行。
   * 节点详情和完整历史要从详情接口取。
   */
  const dagKey = dagKeyOf(raw);
  const latestRun = raw.latest_run || raw.latestRun || null;
  const cronExpr = cronExprFromDagItem(raw);
  return {
    id: dagKey || `dag:${index}`,
    dagKey,
    title: firstText(raw.title, raw.name, dagKey, `自动化 ${index + 1}`),
    description: firstText(raw.description, raw.summary),
    status: firstText(raw.status, raw.state),
    trigger: dagTriggerValue(raw),
    cronExpr,
    nextRunAt: firstText(raw.next_run_at, raw.nextRunAt),
    startedAt: firstText(raw.started_at, raw.startedAt, raw.created_at, raw.createdAt),
    finishedAt: firstText(raw.finished_at, raw.finishedAt),
    version: dagVersionOf(raw),
    latestRun: latestRun ? normalizeDagRun(latestRun) : null,
    hasFinalOutput: dashboardDagFinalOutputFlag(raw),
    scheduleEnabled: scheduleEnabledFromDagItem(raw),
    raw,
  };
}

function dashboardDagFinalOutputFlag(raw = {}) {
  if (typeof raw.hasFinalOutput === 'boolean') return raw.hasFinalOutput;
  if (typeof raw.has_final_output === 'boolean') return raw.has_final_output;
  return undefined;
}

function normalizeDagsResponse(response) {
  if (!response || typeof response !== 'object' || Array.isArray(response)) {
    throw new Error('dags dashboard response must be an object');
  }
  if (!Array.isArray(response.dags)) {
    throw new Error('dags dashboard response dags must be an array');
  }
  return response.dags.map((item, index) => normalizeDashboardDag(item, index));
}

function dagStatusLabel(value) {
  const status = textValue(value).toLowerCase();
  const labels = {
    draft: '草稿',
    ready: '可运行',
    running: '运行中',
    waiting_for_assignee: '等待执行者',
    blocked: '已阻塞',
    succeeded: '成功',
    done: '成功',
    success: '成功',
    failed: '失败',
    cancelled: '已取消',
    canceled: '已取消',
    pending: '待开始',
    queued: '排队中',
    starting: '启动中',
    awaiting_verify: '待确认',
    skipped: '已跳过',
    idle: '空闲',
  };
  return labels[status] || textValue(value) || '-';
}

function dagTriggerValue(raw = {}) {
  const trigger = raw.trigger || raw.trigger_config || raw.triggerConfig;
  if (trigger && typeof trigger === 'object' && !Array.isArray(trigger)) {
    return firstText(trigger.type, trigger.kind, raw.trigger_type, raw.triggerType);
  }
  return firstText(trigger, raw.trigger_type, raw.triggerType);
}

function cronExprFromDagItem(item = {}) {
  const trigger = item.trigger || item.trigger_config || item.triggerConfig;
  if (trigger && typeof trigger === 'object' && !Array.isArray(trigger)) {
    return firstText(trigger.schedule, trigger.cron, trigger.expression, item.schedule, item.cron, item.cron_expr, item.cronExpr);
  }
  return firstText(item.schedule, item.cron, item.cron_expr, item.cronExpr);
}

function scheduleEnabledFromDagItem(item = {}) {
  if (typeof item.schedule_enabled === 'boolean') return item.schedule_enabled;
  if (typeof item.scheduleEnabled === 'boolean') return item.scheduleEnabled;
  const trigger = item.trigger || item.trigger_config || item.triggerConfig;
  if (trigger && typeof trigger === 'object' && !Array.isArray(trigger)) {
    return Boolean(firstText(trigger.next_run_at, trigger.nextRunAt, item.next_run_at, item.nextRunAt));
  }
  return Boolean(firstText(item.next_run_at, item.nextRunAt));
}

function isScheduledTrigger(value) {
  return ['scheduled', 'schedule', 'cron'].includes(textValue(value).toLowerCase());
}

function triggerLabel(value) {
  const trigger = textValue(value).toLowerCase();
  const labels = {
    manual: '手动',
    scheduled: '定时',
    schedule: '定时',
    cron: '定时',
  };
  return labels[trigger] || textValue(value) || '-';
}

const DEFAULT_DAG_SCHEDULE = Object.freeze({ preset: 'daily', time: '08:00', weekday: '1', monthDay: '1' });

const DAG_SCHEDULE_FORMAT_WARNING = '已有计划格式无法识别，请重新选择运行频率和时间。';

const DAG_SCHEDULE_RANGE_WARNING = '已有计划超出简化设置范围，请重新选择运行频率和时间。';

const DAG_WEEKDAY_OPTIONS = Object.freeze([
  { value: '1', label: '周一' },
  { value: '2', label: '周二' },
  { value: '3', label: '周三' },
  { value: '4', label: '周四' },
  { value: '5', label: '周五' },
  { value: '6', label: '周六' },
  { value: '7', label: '周日' },
]);

const DAG_WEEKDAY_LABELS = Object.freeze(Object.fromEntries(DAG_WEEKDAY_OPTIONS.map((item) => [item.value, item.label])));

function twoDigits(value) {
  return value.toString().padStart(2, '0');
}

function formatDagRunStartedAt(value) {
  const text = textValue(value);
  if (!text) return '时间未记录';
  const matched = text.match(/^(\d{4})-(\d{2})-(\d{2})[T\s](\d{2}):(\d{2}):(\d{2})/);
  if (matched) {
    const [, year, month, day, hour, minute, second] = matched;
    return `${year}-${month}-${day} ${hour}:${minute}:${second}`;
  }
  const parsed = new Date(text);
  if (Number.isNaN(parsed.getTime())) return text;
  return [
    parsed.getFullYear().toString().padStart(4, '0'),
    twoDigits(parsed.getMonth() + 1),
    twoDigits(parsed.getDate()),
  ].join('-') + ` ${twoDigits(parsed.getHours())}:${twoDigits(parsed.getMinutes())}:${twoDigits(parsed.getSeconds())}`;
}

function dagRunStartedAtMillis(run) {
  const parsed = Date.parse(textValue(run?.startedAt));
  return Number.isFinite(parsed) ? parsed : Number.POSITIVE_INFINITY;
}

function chronologicalWorkflowRuns(runs) {
  const source = Array.isArray(runs) ? runs : [];
  return source
    .map((run, index) => ({ index, run }))
    .sort((left, right) => {
      const leftTime = dagRunStartedAtMillis(left.run);
      const rightTime = dagRunStartedAtMillis(right.run);
      if (leftTime !== rightTime) return leftTime - rightTime;
      return left.index - right.index;
    })
    .map((entry) => entry.run);
}

function parseScheduleTime(value) {
  const text = textValue(value);
  const match = /^(\d{1,2}):(\d{2})$/.exec(text);
  if (!match) return null;
  const hour = Number(match[1]);
  const minute = Number(match[2]);
  if (!Number.isInteger(hour) || !Number.isInteger(minute) || hour < 0 || hour > MAX_SCHEDULE_HOUR || minute < 0 || minute > MAX_SCHEDULE_MINUTE) {
    return null;
  }
  return { hour, minute, label: `${twoDigits(hour)}:${twoDigits(minute)}` };
}

function dagScheduleState(warning = '', patch = {}) {
  return { ...DEFAULT_DAG_SCHEDULE, ...patch, warning };
}

function isDagWeekday(value) {
  return Object.prototype.hasOwnProperty.call(DAG_WEEKDAY_LABELS, value);
}

function isMonthDayText(value) {
  const day = Number(value);
  return Number.isInteger(day) && day >= 1 && day <= DAYS_IN_MONTH;
}

function cronSchedulePartsWithTimezone(cronExpr) {
  const text = textValue(cronExpr);
  if (!text) return { cronText: '', timezone: DAG_SCHEDULE_TIMEZONE };
  const parts = text.split(/\s+/);
  const first = parts[0] || '';
  if (first.startsWith('CRON_TZ=')) {
    return {
      cronText: parts.slice(1).join(' '),
      timezone: first.slice('CRON_TZ='.length) || DAG_SCHEDULE_TIMEZONE,
    };
  }
  return { cronText: text, timezone: DAG_SCHEDULE_TIMEZONE };
}

function parseCronScheduleParts(cronExpr) {
  const { cronText: text, timezone } = cronSchedulePartsWithTimezone(cronExpr);
  if (!text) return { empty: true };
  const parts = text.split(/\s+/);
  if (parts.length !== 5) return { error: DAG_SCHEDULE_FORMAT_WARNING };
  const [minuteText, hourText, dayOfMonth, month, dayOfWeek] = parts;
  const hour = Number(hourText);
  const minute = Number(minuteText);
  if (!Number.isInteger(hour) || !Number.isInteger(minute) || hour < 0 || hour > MAX_SCHEDULE_HOUR || minute < 0 || minute > MAX_SCHEDULE_MINUTE) {
    return { error: DAG_SCHEDULE_FORMAT_WARNING };
  }
  return { minute, hour, dayOfMonth, month, dayOfWeek, time: `${twoDigits(hour)}:${twoDigits(minute)}`, timezone };
}

function cronFieldMatches(expected, actual) {
  return typeof expected === 'function' ? expected(actual) : actual === expected;
}

function cronScheduleRuleMatches(rule, parsed) {
  return (
    cronFieldMatches(rule.dayOfMonth, parsed.dayOfMonth)
    && cronFieldMatches(rule.month, parsed.month)
    && cronFieldMatches(rule.dayOfWeek, parsed.dayOfWeek)
  );
}

const DAG_CRON_SCHEDULE_RULES = Object.freeze([
  { preset: 'weekdays', dayOfMonth: '*', month: '*', dayOfWeek: '1-5' },
  { preset: 'weekly', dayOfMonth: '*', month: '*', dayOfWeek: isDagWeekday, patch: (parsed) => ({ weekday: parsed.dayOfWeek }) },
  { preset: 'monthly', dayOfMonth: isMonthDayText, month: '*', dayOfWeek: '*', patch: (parsed) => ({ monthDay: Number(parsed.dayOfMonth).toString() }) },
  { preset: 'daily', dayOfMonth: '*', month: '*', dayOfWeek: '*' },
]);

function scheduleStateForCronRule(rule, parsed) {
  return dagScheduleState('', { preset: rule.preset, time: parsed.time, ...(rule.patch?.(parsed) || {}) });
}

function scheduleStateFromCron(cronExpr) {
  const parsed = parseCronScheduleParts(cronExpr);
  if (parsed.empty) return dagScheduleState();
  if (parsed.error) return dagScheduleState(parsed.error);
  const rule = DAG_CRON_SCHEDULE_RULES.find((item) => cronScheduleRuleMatches(item, parsed));
  return rule ? scheduleStateForCronRule(rule, parsed) : dagScheduleState(DAG_SCHEDULE_RANGE_WARNING);
}

function scheduleLabelFromState(schedule) {
  const parsed = parseScheduleTime(schedule?.time);
  if (!parsed) return '';
  if (schedule?.preset === 'daily') return `每天 ${parsed.label}`;
  if (schedule?.preset === 'weekdays') return `工作日 ${parsed.label}`;
  if (schedule?.preset === 'weekly') return `${DAG_WEEKDAY_LABELS[schedule.weekday] ? `每${DAG_WEEKDAY_LABELS[schedule.weekday]}` : '每周'} ${parsed.label}`;
  if (schedule?.preset === 'monthly') return `每月 ${schedule.monthDay || DEFAULT_DAG_SCHEDULE.monthDay} 日 ${parsed.label}`;
  return '';
}

function scheduleLabelFromCron(cronExpr) {
  if (!textValue(cronExpr)) return '';
  const state = scheduleStateFromCron(cronExpr);
  if (state.warning) return '';
  return scheduleLabelFromState(state);
}

function scheduleLabelFromDag(item) {
  return scheduleLabelFromCron(cronExprFromDagItem(item));
}

function cronExprFromSchedule(preset, time, weekday, monthDay) {
  const parsed = parseScheduleTime(time);
  if (!parsed) return { cronExpr: '', error: '请选择运行时间' };
  const minute = parsed.minute.toString();
  const hour = parsed.hour.toString();
  if (preset === 'daily') return { cronExpr: `${DAG_SCHEDULE_CRON_TZ_PREFIX} ${minute} ${hour} * * *`, error: '' };
  if (preset === 'weekdays') return { cronExpr: `${DAG_SCHEDULE_CRON_TZ_PREFIX} ${minute} ${hour} * * 1-5`, error: '' };
  if (preset === 'weekly') {
    if (!Object.prototype.hasOwnProperty.call(DAG_WEEKDAY_LABELS, weekday)) return { cronExpr: '', error: '请选择星期几' };
    return { cronExpr: `${DAG_SCHEDULE_CRON_TZ_PREFIX} ${minute} ${hour} * * ${weekday}`, error: '' };
  }
  if (preset === 'monthly') {
    const day = Number(monthDay);
    if (!Number.isInteger(day) || day < 1 || day > DAYS_IN_MONTH) return { cronExpr: '', error: '请选择每月几号' };
    return { cronExpr: `${DAG_SCHEDULE_CRON_TZ_PREFIX} ${minute} ${hour} ${day} * *`, error: '' };
  }
  return { cronExpr: '', error: '请选择运行频率' };
}

function schedulePlanLabel(item) {
  const readable = scheduleLabelFromDag(item);
  if (readable) return readable;
  return triggerLabel(item?.trigger);
}

function latestDagRunLabel(item) {
  const status = firstText(item?.latestRun?.status, item?.latest_run_status, item?.latestRunStatus);
  if (status) return dagStatusLabel(status);
  if (item?.latestRun?.runKey) return '有运行记录';
  if (isScheduledTrigger(item?.trigger)) return '未运行';
  return textValue(item?.status).toLowerCase() === 'draft' || textValue(item?.status).toLowerCase() === 'ready' ? '未启动' : '-';
}

function displayDagStatusLabel(item) {
  const trigger = textValue(item?.trigger).toLowerCase();
  const status = textValue(item?.status).toLowerCase();
  if (isScheduledTrigger(trigger) && cronExprFromDagItem(item)) {
    if (status === 'running') return '运行中';
    return scheduleEnabledFromDagItem(item) ? '已启用' : '已暂停';
  }
  return dagStatusLabel(item?.status);
}

function isRunningStatus(value) {
  return RUNNING_RUN_STATUSES.has(textValue(value).toLowerCase());
}

function dagHasActiveRun(item) {
  return isRunningStatus(item?.latestRun?.status) || isRunningStatus(item?.status);
}

function isScheduledDag(item) {
  const trigger = textValue(item?.trigger).toLowerCase();
  return ['scheduled', 'schedule', 'cron'].includes(trigger) || Boolean(item?.cronExpr || item?.nextRunAt);
}

function dagCategoryOf(item) {
  if (dagHasActiveRun(item)) return 'running';
  if (isScheduledDag(item)) return 'scheduled';
  return 'history';
}

function categoryCounts(items) {
  return DAG_CATEGORIES.reduce((acc, category) => {
    acc[category.key] = items.filter((item) => dagCategoryOf(item) === category.key).length;
    return acc;
  }, {});
}

function workflowOverviewStats(items) {
  const source = Array.isArray(items) ? items : [];
  const counts = categoryCounts(source);
  return {
    total: source.length,
    running: counts.running || 0,
    scheduled: counts.scheduled || 0,
    startable: source.filter((item) => (
      !dagHasActiveRun(item)
      && STARTABLE_DAG_STATUSES.has(textValue(item?.status).toLowerCase())
      && STARTABLE_DAG_TRIGGERS.has(textValue(item?.trigger).toLowerCase())
    )).length,
    finalOutputs: source.filter((item) => workflowDagHasFinalOutput(item)).length,
  };
}

function workflowDagHasFinalOutput(item) {
  if (typeof item?.hasFinalOutput === 'boolean') return item.hasFinalOutput;
  if (typeof item?.raw?.hasFinalOutput === 'boolean') return item.raw.hasFinalOutput;
  if (typeof item?.raw?.has_final_output === 'boolean') return item.raw.has_final_output;
  return isPresentFinalOutput(finalOutputDescriptor(item?.latestRun) || finalOutputDescriptor(item?.latestRun?.raw));
}

function isPresentFinalOutput(value) {
  if (!value) return false;
  if (typeof value === 'string') return value.trim().length > 0;
  if (Array.isArray(value)) return value.length > 0;
  if (typeof value === 'object') return Object.keys(value).length > 0;
  return true;
}

function firstAvailableCategory(items) {
  const counts = categoryCounts(items);
  const found = DAG_CATEGORIES.find((category) => counts[category.key] > 0);
  return found?.key || DAG_CATEGORIES[0].key;
}

function finalOutputDescriptor(raw) {
  const source = raw?.run || raw || {};
  const metadata = objectValue(source.metadata);
  return source.final_output || source.finalOutput || metadata.final_output || metadata.finalOutput || null;
}

function finalOutputPreviewText(value) {
  if (typeof value === 'string') return value.trim();
  if (value && typeof value === 'object') {
    return firstText(value.text, value.content, value.message, value.output, value.summary) || JSON.stringify(value);
  }
  return '';
}

function parsedDagConfig(value) {
  if (!value) return {};
  if (typeof value === 'object' && !Array.isArray(value)) return value;
  if (typeof value !== 'string') return {};
  try {
    const parsed = JSON.parse(value);
    return parsed && typeof parsed === 'object' && !Array.isArray(parsed) ? parsed : {};
  }
  catch {
    return {};
  }
}

function validThreadIdText(value) {
  const text = textValue(value);
  if (!text || /^launch[_-]/i.test(text)) return '';
  return text;
}

function firstValidThreadId(...values) {
  for (const value of values) {
    const text = validThreadIdText(value);
    if (text) return text;
  }
  return '';
}

function threadIdFromStartResponse(value) {
  return firstValidThreadId(
    value?.threadId,
    value?.thread_id,
    value?.thread?.threadId,
    value?.thread?.thread_id,
    value?.id,
    value?.thread?.id,
    value?.agentId,
    value?.agent_id,
    value?.thread?.agentId,
    value?.thread?.agent_id,
  );
}

function dagNodeFormFromNode(node) {
  const nodeType = textValue(node?.nodeType).toLowerCase();
  const config = parsedDagConfig(node?.config);
  const exec = objectValue(config.exec);
  const agentExec = nodeType === 'hybrid' ? objectValue(exec.verifier) : exec;
  const automationExec = nodeType === 'hybrid' ? objectValue(exec.automation) : exec;
  const outputs = objectValue(config.outputs);
  const toSharedfile = objectValue(outputs.to_sharedfile);
  return {
    nodeKey: textValue(node?.nodeKey),
    nodeType,
    title: textValue(node?.title),
    assignedTo: textValue(node?.assignedTo),
    execProvider: firstText(agentExec.provider, config.provider),
    execModel: firstText(agentExec.model, config.model),
    execAgentKey: firstText(agentExec.agent_key, agentExec.agentKey, config.agent_key, config.agentKey),
    execPromptKey: firstText(agentExec.prompt_key, agentExec.promptKey, config.prompt_key, config.promptKey),
    execCwd: firstText(agentExec.cwd, agentExec.CWD, config.cwd),
    automationKind: firstText(automationExec.kind, 'command_card'),
    commandRef: firstText(automationExec.command_ref, automationExec.commandRef, node?.commandRef),
    dependsOn: listToText(node?.dependsOn || []),
    firstTurn: firstText(config.first_turn, config.firstTurn, config.prompt),
    outputSharedfilePath: firstText(toSharedfile.path, config.output_file, config.outputFile),
    outputSharedfileLockMode: firstText(toSharedfile.lock_mode, toSharedfile.lockMode, 'exclusive'),
    outputToNodeResult: outputs.to_node_result === true || outputs.toNodeResult === true,
  };
}

function dagNodePatchFromForm(form, node) {
  const nodeType = textValue(form.nodeType || node?.nodeType).toLowerCase();
  const baseConfig = stripLegacyDagNodeConfig(parsedDagConfig(node?.config));
  const config = {
    ...baseConfig,
    exec: dagNodeExecPatchFromForm(form, baseConfig, nodeType),
    outputs: dagNodeOutputsPatchFromForm(form, baseConfig),
  };
  if (nodeType === 'agent') {
    config.first_turn = textValue(form.firstTurn);
  }
  validateDagNodePatchForm(form, nodeType);
  return {
    title: textValue(form.title),
    assigned_to: textValue(form.assignedTo),
    depends_on: wordListFromText(form.dependsOn),
    config: cleanObject(config),
  };
}

function stripLegacyDagNodeConfig(config) {
  const {
    provider: _provider,
    model: _model,
    agent_key: _agentKeySnake,
    agentKey: _agentKey,
    prompt_key: _promptKeySnake,
    promptKey: _promptKey,
    output_file: _outputFileSnake,
    outputFile: _outputFile,
    prompt: _prompt,
    cwd: _cwd,
    ...rest
  } = objectValue(config);
  return rest;
}

function dagNodeExecPatchFromForm(form, config, nodeType) {
  const exec = objectValue(config.exec);
  if (nodeType === 'automation') {
    return cleanObject({
      ...exec,
      kind: textValue(form.automationKind) || 'command_card',
      command_ref: textValue(form.commandRef),
    });
  }
  if (nodeType === 'hybrid') {
    return cleanObject({
      ...exec,
      automation: cleanObject({
        ...objectValue(exec.automation),
        kind: textValue(form.automationKind) || 'command_card',
        command_ref: textValue(form.commandRef),
      }),
      verifier: dagNodeAgentExecPatchFromForm(form, objectValue(exec.verifier)),
    });
  }
  return dagNodeAgentExecPatchFromForm(form, exec);
}

function dagNodeAgentExecPatchFromForm(form, exec) {
  return cleanObject({
    ...objectValue(exec),
    provider: textValue(form.execProvider),
    model: textValue(form.execModel),
    agent_key: textValue(form.execAgentKey),
    prompt_key: textValue(form.execPromptKey),
    cwd: textValue(form.execCwd),
  });
}

function dagNodeOutputsPatchFromForm(form, config) {
  const outputs = { ...objectValue(config.outputs) };
  const path = textValue(form.outputSharedfilePath);
  if (path) {
    outputs.to_sharedfile = cleanObject({
      ...objectValue(outputs.to_sharedfile),
      path,
      lock_mode: textValue(form.outputSharedfileLockMode),
    });
  } else {
    delete outputs.to_sharedfile;
  }
  outputs.to_node_result = Boolean(form.outputToNodeResult);
  return cleanObject(outputs);
}

function validateDagNodePatchForm(form, nodeType) {
  const provider = textValue(form.execProvider);
  if (provider && provider !== 'claude' && provider !== 'codex') throw new Error('config.exec.provider 只能是 claude 或 codex');
  const lockMode = textValue(form.outputSharedfileLockMode);
  if (lockMode && !['exclusive', 'append', 'shared'].includes(lockMode)) throw new Error('outputs.to_sharedfile.lock_mode 只能是 exclusive、append 或 shared');
  if (!textValue(form.outputSharedfilePath) && lockMode && lockMode !== 'exclusive') throw new Error('outputs.to_sharedfile.path 为空时不能设置写入模式');
  if (nodeType === 'automation' || nodeType === 'hybrid') validateAutomationExecForm(form);
  if (nodeType === 'agent' || nodeType === 'hybrid') validateAgentExecForm(form);
}

function validateAutomationExecForm(form) {
  if ((textValue(form.automationKind) || 'command_card') !== 'command_card') throw new Error('config.exec.kind 当前只能是 command_card');
  if (!textValue(form.commandRef)) throw new Error('config.exec.command_ref 不能为空');
}

function validateAgentExecForm(form) {
  if (!dagNodeAgentExecFormHasValues(form)) return;
  if (!textValue(form.execAgentKey) && !textValue(form.execPromptKey)) throw new Error('config.exec.agent_key 或 config.exec.prompt_key 至少填写一个');
  const cwd = textValue(form.execCwd);
  if (cwd && !cwd.startsWith('/')) throw new Error('config.exec.cwd 必须是绝对路径');
}

function dagNodeAgentExecFormHasValues(form) {
  return Boolean(
    textValue(form.execProvider)
    || textValue(form.execModel)
    || textValue(form.execAgentKey)
    || textValue(form.execPromptKey)
    || textValue(form.execCwd),
  );
}

function workflowDiagnosticNodes(detail, run) {
  const runtimeNodes = Array.isArray(run?.selectedRun?.nodes) ? run.selectedRun.nodes : [];
  return workflowOrderedNodes(runtimeNodes.length > 0 ? runtimeNodes : detail.nodes);
}

function workflowNodeDiagnostics(nodes = []) {
  return nodes.flatMap((node) => {
    const status = textValue(node?.status).toLowerCase();
    const diagnostics = [];
    if ((status === 'ready' || status === 'pending') && !textValue(node?.assignedTo)) {
      diagnostics.push({
        key: `${node.nodeKey}:waiting_for_assignee`,
        node,
        severity: 'blocked',
        title: '等待执行者',
        message: `${node.title || node.nodeKey} 缺少 assigned_to，后端不会自动 enqueue wakeup。`,
        recovery: 'dispatch',
      });
    }
    if (status === 'ready' && !node?.activeWakeupId) {
      diagnostics.push({
        key: `${node.nodeKey}:ready_no_wakeup`,
        node,
        severity: 'blocked',
        title: 'ready-no-wakeup',
        message: `${node.title || node.nodeKey} 已 ready 但没有 active_wakeup_id，请指派执行者后手动派发。`,
        recovery: textValue(node?.assignedTo) ? 'dispatch' : '',
      });
    }
    if (status === 'blocked') {
      diagnostics.push({
        key: `${node.nodeKey}:blocked`,
        node,
        severity: 'blocked',
        title: 'blocked',
        message: workflowNodeFailureText(node) || `${node.title || node.nodeKey} 被后端标记为 blocked。`,
      });
    }
    if (status === 'failed') {
      diagnostics.push({
        key: `${node.nodeKey}:failed`,
        node,
        severity: 'failed',
        title: 'failed',
        message: workflowNodeFailureText(node) || `${node.title || node.nodeKey} 执行失败，请查看节点结果或对话。`,
      });
    }
    return diagnostics;
  });
}

function workflowNodeFailureText(node) {
  const result = parsedDagConfig(node?.result || node?.raw?.result);
  return firstText(
    result.error_summary,
    result.errorSummary,
    result.reason,
    result.message,
    result.failure_class,
    result.failureClass,
  );
}

function rootNodesMissingAssignee(nodes = []) {
  if (!Array.isArray(nodes) || nodes.length === 0) return [];
  return nodes.filter((node) => {
    const dependsOn = Array.isArray(node?.dependsOn) ? node.dependsOn : [];
    return dependsOn.length === 0 && !textValue(node?.assignedTo);
  });
}

function rootAssigneeWarning(nodes = []) {
  const missing = rootNodesMissingAssignee(nodes);
  if (missing.length === 0) return '';
  const names = missing.flatMap((node) => {
    const name = node.title || node.nodeKey;
    return name ? [name] : [];
  }).join('、');
  return names ? `首个步骤「${names}」缺少执行者，请先在高级设置中填写执行者。` : '首个步骤缺少执行者，请先在高级设置中填写执行者。';
}

function WorkflowPage({ copy = APP_COPY.zh.workflow, onWorkflowViewChange, projectPath, store, refreshKey = 0 }) {
  const model = useWorkflowPageController({ projectPath, refreshKey, store });
  return <WorkflowPageView copy={copy} model={model} onWorkflowViewChange={onWorkflowViewChange} />;
}

function useWorkflowPageController({ projectPath, refreshKey, store }) {
  /*
   * controller 把项目 cwd、查询结果、选择态和按钮动作放进 model。
   * 子组件只用 model，不直接调用后端。
   */
  const workflowCwd = optionalSettingsCwd(projectPath);
  const isProjectPending = !workflowCwd;
  const list = useWorkflowListQuery(workflowCwd);
  const { refreshDags } = list;
  useWorkflowListFocusRefresh(workflowCwd, refreshDags);
  const activePage = store?.activePage;
  const mountedRef = useRef(false);
  useEffect(() => {
    if (!mountedRef.current) { mountedRef.current = true; return; }
    if (activePage === 'workflows' && workflowCwd) void refreshDags();
  }, [activePage, refreshDags, workflowCwd]);
  const selection = useWorkflowSelection(list.items);
  const detail = useWorkflowDetailQuery({ items: list.items, selectedDag: selection.selectedDag, selectedDagKey: selection.selectedDagKey, workflowCwd });
  const run = useWorkflowRunDetail({ activeRun: detail.activeRun, runs: detail.runs, workflowCwd });
  const notices = useWorkflowNotice(selection.selectedDagKey);
  const refresh = useWorkflowRefresh({ refreshDags, refreshKey, selectedDagKeyRef: selection.selectedDagKeyRef, selectedRunKey: run.selectedRunKey, setSelectedRunKey: run.setSelectedRunKey, workflowCwd });
  const actionState = useWorkflowActionState(detail.activeDetailDag);
  const [designSession, setDesignSession] = useState(null);
  const templates = useWorkflowTemplatesQuery();
  const derived = useWorkflowDerivedState({ detail, list, run, selection });
  const actions = useWorkflowActions({ actionState, derived, detail, list, notices, refresh, run, selection, setDesignSession, store, workflowCwd });
  return { actions, actionState, derived, designSession, detail, isProjectPending, list, notices, refresh, run, selection, store, templates, workflowCwd };
}

function useWorkflowTemplatesQuery() {
  const { data, error, isPending } = useQuery({
    queryKey: ['workflow-templates', 'government-enterprise'],
    queryFn: () => listWorkflowTemplates({ category: 'government-enterprise' }),
  });
  const items = useMemo(() => {
    const templates = Array.isArray(data?.templates) ? data.templates : [];
    return templates.filter((item) => enterpriseTemplateId(item));
  }, [data]);
  return {
    items,
    loading: isPending,
    error: error ? errorMessage(error) : '',
  };
}

function useWorkflowListQuery(workflowCwd) {
  /*
   * 列表刷新失败时保留上次成功数据。
   * syncFailure 只提示同步失败，不把页面清空。
   */
  const queryClient = useQueryClient();
  const refreshPromiseRef = useRef(null);
  const [workflowSyncFailureState, setWorkflowSyncFailureState] = useState({ dataUpdatedAt: 0, message: '' });
  const {
    data: dagsData,
    error: dagsError,
    isPending: dagsPending,
    dataUpdatedAt: dagsDataUpdatedAt,
  } = useQuery({
    queryKey: dashboardQueryKey(workflowCwd, 'dags'),
    queryFn: () => fetchDagsDashboard(workflowCwd),
    enabled: Boolean(workflowCwd),
  });
  const dagsQuery = { data: dagsData, error: dagsError, isPending: dagsPending };
  const hasSnapshot = queryHasSnapshot(dagsQuery);
  const items = useMemo(() => (Array.isArray(dagsData) ? dagsData : []), [dagsData]);
  const loading = Boolean(workflowCwd) && dagsQuery.isPending && !hasSnapshot;
  const errorState = workflowDashboardQueryErrorState(dagsQuery, hasSnapshot);
  if (workflowSyncFailureState.message && dagsDataUpdatedAt > workflowSyncFailureState.dataUpdatedAt) {
    setWorkflowSyncFailureState({ dataUpdatedAt: dagsDataUpdatedAt, message: '' });
  }
  const workflowSyncFailure = workflowSyncFailureState.message && dagsDataUpdatedAt <= workflowSyncFailureState.dataUpdatedAt
    ? workflowSyncFailureState.message
    : '';
  const refreshDags = useCallback(async () => {
    if (!workflowCwd) return [];
    const key = dashboardQueryKey(workflowCwd, 'dags');
    if (refreshPromiseRef.current?.workflowCwd === workflowCwd) return refreshPromiseRef.current.promise;
    const refreshPromise = (async () => {
      try {
        await queryClient.invalidateQueries({ queryKey: key }, { throwOnError: true, cancelRefetch: false });
        setWorkflowSyncFailureState({ dataUpdatedAt: queryClient.getQueryState(key)?.dataUpdatedAt || 0, message: '' });
      } catch (err) {
        if (!isCancelledError(err)) {
          setWorkflowSyncFailureState({
            dataUpdatedAt: queryClient.getQueryState(key)?.dataUpdatedAt || 0,
            message: '同步失败，显示的是上次成功的数据：' + errorMessage(err),
          });
        }
      }
      return queryClient.getQueryData(key) || [];
    })();
    refreshPromiseRef.current = { promise: refreshPromise, workflowCwd };
    refreshPromise.finally(() => {
      if (refreshPromiseRef.current?.promise === refreshPromise) refreshPromiseRef.current = null;
    });
    return refreshPromise;
  }, [queryClient, workflowCwd]);
  return { errorState, items, loading, refreshDags, syncFailure: workflowSyncFailure };
}

function useWorkflowListFocusRefresh(workflowCwd, refreshDags) {
  useEffect(() => {
    if (!workflowCwd) return undefined;
    const refresh = () => {
      if (typeof document !== 'undefined' && document.visibilityState === 'hidden') return;
      void refreshDags();
    };
    window.addEventListener('focus', refresh);
    document.addEventListener('visibilitychange', refresh);
    return () => {
      window.removeEventListener('focus', refresh);
      document.removeEventListener('visibilitychange', refresh);
    };
  }, [refreshDags, workflowCwd]);
}

function useWorkflowSelection(items) {
  const [activeCategory, setActiveCategory] = useState(DAG_CATEGORIES[0].key);
  const [categoryManuallySelected, setCategoryManuallySelected] = useState(false);
  const [selectedDagKey, setSelectedDagKey] = useState('');
  const counts = useMemo(() => categoryCounts(items), [items]);
  const preferredCategory = useMemo(() => firstAvailableCategory(items), [items]);
  const activeCategoryHasItems = items.some((item) => dagCategoryOf(item) === activeCategory);
  const effectiveActiveCategory = !categoryManuallySelected && items.length > 0 && !activeCategoryHasItems && activeCategory !== preferredCategory
    ? preferredCategory
    : activeCategory;
  if (effectiveActiveCategory !== activeCategory) {
    setActiveCategory(effectiveActiveCategory);
  }
  const visibleItems = useMemo(() => items.filter((item) => dagCategoryOf(item) === effectiveActiveCategory), [effectiveActiveCategory, items]);
  const effectiveSelectedDagKey = visibleItems.some((item) => item.dagKey === selectedDagKey)
    ? selectedDagKey
    : visibleItems[0]?.dagKey || '';
  if (effectiveSelectedDagKey !== selectedDagKey) {
    setSelectedDagKey(effectiveSelectedDagKey);
  }
  const selectedDagKeyRef = useRef(effectiveSelectedDagKey);
  useEffect(() => {
    selectedDagKeyRef.current = effectiveSelectedDagKey;
  }, [effectiveSelectedDagKey]);
  const selectedDag = useMemo(() => items.find((item) => item.dagKey === effectiveSelectedDagKey) || visibleItems[0] || null, [effectiveSelectedDagKey, items, visibleItems]);

  const chooseCategory = useCallback((categoryKey) => {
    setCategoryManuallySelected(true);
    setActiveCategory(categoryKey);
  }, []);
  return { activeCategory: effectiveActiveCategory, chooseCategory, counts, selectedDag, selectedDagKey: effectiveSelectedDagKey, selectedDagKeyRef, setSelectedDagKey, visibleItems };
}

async function fetchWorkflowRunDetail(runKey) {
  const key = textValue(runKey);
  if (!key) return null;
  const response = await getDagRun({ runKey: key });
  const run = response?.run ? normalizeDagRun(response.run) : null;
  const nodes = Array.isArray(response?.nodes) ? response.nodes.map((node, index) => normalizeDagNode(node, index)) : [];
  return { run, nodes };
}

async function fetchWorkflowDagDetail(selectedDagKey, items) {
  const key = textValue(selectedDagKey);
  const [detailResponse, runsResponse, activeResponse] = await Promise.all([
    getDagDetail({ dagKey: key }),
    getDagRuns({ dagKey: key, limit: DAG_RECENT_RUN_LIMIT }),
    getDagRuns({ dagKey: key, status: 'running', limit: 1 }),
  ]);
  const listDag = items.find((item) => item.dagKey === key);
  const dag = normalizeDashboardDag({ ...objectValue(listDag?.raw), ...objectValue(detailResponse?.dag) });
  return {
    activeRun: workflowActiveRunFromResponse(activeResponse),
    dag,
    nodes: workflowNodesFromResponse(detailResponse),
    runs: workflowRunsFromResponse(runsResponse),
  };
}

function workflowNodesFromResponse(response) {
  return Array.isArray(response?.nodes) ? response.nodes.map((node, index) => normalizeDagNode(node, index)) : [];
}

function workflowRunsFromResponse(response) {
  return Array.isArray(response?.runs) ? response.runs.map((run, index) => normalizeDagRun(run, index)) : [];
}

function workflowActiveRunFromResponse(response) {
  return Array.isArray(response?.runs) && response.runs.length > 0 ? normalizeDagRun(response.runs[0]) : null;
}

function useWorkflowDetailQuery({ items, selectedDag, selectedDagKey, workflowCwd }) {
  /*
   * 详情加载中时先用列表项兜底。
   * 这样右侧标题和状态不会闪成空白。
   */
  const {
    data: dagDetailData,
    error: dagDetailError,
    isPending: dagDetailPending,
  } = useQuery({
    queryKey: dashboardQueryKey(workflowCwd, 'dag-detail', selectedDagKey),
    queryFn: () => fetchWorkflowDagDetail(selectedDagKey, items),
    enabled: Boolean(workflowCwd && selectedDagKey),
  });
  const dagDetailQuery = { data: dagDetailData, error: dagDetailError, isPending: dagDetailPending };
  const hasSnapshot = queryHasSnapshot(dagDetailQuery);
  const detailData = dagDetailData || {};
  const nodes = useMemo(() => (Array.isArray(detailData.nodes) ? detailData.nodes : []), [detailData.nodes]);
  const runs = useMemo(() => (Array.isArray(detailData.runs) ? detailData.runs : []), [detailData.runs]);
  return {
    activeDetailDag: detailData.dag || selectedDag || null,
    activeRun: detailData.activeRun || null,
    detailDag: detailData.dag || null,
    detailErrorState: workflowDashboardQueryErrorState(dagDetailQuery, hasSnapshot),
    detailLoading: Boolean(selectedDagKey) && dagDetailQuery.isPending && !hasSnapshot && !selectedDag,
    nodes,
    runs,
  };
}

function useWorkflowRunDetail({ activeRun, runs, workflowCwd }) {
  /*
   * 运行详情优先看正在运行的 run，其次看最近历史。
   * run 的节点回来得晚时，诊断会先用 DAG 详情里的节点。
   */
  const queryClient = useQueryClient();
  const [selectedRunKey, setSelectedRunKey] = useState('');
  const fallbackRunKey = activeRun?.runKey || runs[0]?.runKey || '';
  const effectiveSelectedRunKey = selectedRunKey && runs.some((run) => run.runKey === selectedRunKey)
    ? selectedRunKey
    : fallbackRunKey;
  if (effectiveSelectedRunKey !== selectedRunKey) {
    setSelectedRunKey(effectiveSelectedRunKey);
  }
  const { data: runDetailData } = useQuery({
    queryKey: dashboardQueryKey(workflowCwd, 'dag-run', effectiveSelectedRunKey),
    queryFn: () => fetchWorkflowRunDetail(effectiveSelectedRunKey),
    enabled: Boolean(workflowCwd && effectiveSelectedRunKey),
  });
  const loadRunDetail = useCallback((runKey) => {
    const key = textValue(runKey);
    if (!key) { setSelectedRunKey(''); return null; }
    setSelectedRunKey(key);
    const queryKey = dashboardQueryKey(workflowCwd, 'dag-run', key);
    return fetchWorkflowQuery(queryClient, queryKey, () => fetchWorkflowRunDetail(key));
  }, [queryClient, workflowCwd]);
  return { loadRunDetail, selectedRun: runDetailData || null, selectedRunKey: effectiveSelectedRunKey, setSelectedRunKey };
}

function useWorkflowNotice(selectedDagKey) {
  const [noticeState, setNoticeState] = useState({ selectedDagKey, notice: null });
  if (noticeState.selectedDagKey !== selectedDagKey) {
    setNoticeState({ selectedDagKey, notice: null });
  }
  const clearNotice = useCallback(() => setNoticeState((current) => ({ ...current, notice: null })), []);
  const showTaskNotice = useCallback((message, taskKey = selectedDagKey) => {
    const key = textValue(taskKey);
    if (!message || !key || selectedDagKey !== key) return;
    setNoticeState({ selectedDagKey: key, notice: { dagKey: key, message } });
  }, [selectedDagKey]);
  const notice = noticeState.selectedDagKey === selectedDagKey ? noticeState.notice : null;
  return { clearNotice, notice, showTaskNotice };
}

function useWorkflowRefresh({ refreshDags, refreshKey, selectedDagKeyRef, selectedRunKey, setSelectedRunKey, workflowCwd }) {
  const queryClient = useQueryClient();
  const handledWorkflowRefreshRef = useRef(0);
  const workflowRefreshKey = Number(refreshKey || 0);
  const refreshDetail = useCallback(async (dagKey, preferredRunKey = '') => {
    const key = textValue(dagKey);
    if (!key) return;
    const runKey = textValue(preferredRunKey) || textValue(selectedRunKey);
    if (preferredRunKey) setSelectedRunKey(textValue(preferredRunKey));
    const operations = [
      invalidateWorkflowQuery(queryClient, dashboardQueryKey(workflowCwd, 'dag-detail', key)),
    ];
    if (runKey) {
      operations.push(invalidateWorkflowQuery(queryClient, dashboardQueryKey(workflowCwd, 'dag-run', runKey)));
    }
    await Promise.all(operations);
  }, [queryClient, selectedRunKey, setSelectedRunKey, workflowCwd]);
  const refreshWorkflowSurface = useCallback(async () => {
    if (!workflowCwd) return;
    await refreshDags();
    if (selectedDagKeyRef.current) await refreshDetail(selectedDagKeyRef.current, selectedRunKey);
  }, [refreshDags, refreshDetail, selectedDagKeyRef, selectedRunKey, workflowCwd]);
  useEffect(() => {
    if (workflowRefreshKey <= 0 || handledWorkflowRefreshRef.current === workflowRefreshKey) return;
    handledWorkflowRefreshRef.current = workflowRefreshKey;
    void refreshWorkflowSurface().catch(() => {});
  }, [refreshWorkflowSurface, workflowRefreshKey]);
  return { refreshDetail, refreshWorkflowSurface };
}

function useWorkflowActionState(activeDetailDag) {
  const [actioning, setActioning] = useState('');
  const [savingNodeKey, setSavingNodeKey] = useState('');
  const [dispatchingNodeKey, setDispatchingNodeKey] = useState('');
  const [error, setError] = useState('');
  const [deleteTarget, setDeleteTarget] = useState(null);
  const [scheduleOpen, setScheduleOpen] = useState(false);
  const [scheduleCron, setScheduleCron] = useState('0 8 * * *');
  const openScheduleModal = useCallback(() => {
    setScheduleCron(activeDetailDag?.cronExpr || '0 8 * * *');
    setScheduleOpen(true);
  }, [activeDetailDag]);
  return { actioning, deleteTarget, dispatchingNodeKey, error, openScheduleModal, savingNodeKey, scheduleCron, scheduleOpen, setActioning, setDeleteTarget, setDispatchingNodeKey, setError, setSavingNodeKey, setScheduleCron, setScheduleOpen };
}

function useWorkflowDerivedState({ detail, list, run, selection }) {
  /*
   * derived 统一算按钮禁用原因、诊断、最终输出和可编辑节点。
   * 展示组件不要重复猜这些状态。
   */
  const missingRootAssigneeWarning = useMemo(() => rootAssigneeWarning(detail.nodes), [detail.nodes]);
  const activeDetailDag = detail.activeDetailDag;
  const activeRunKey = detail.activeRun?.runKey || '';
  const dagKey = activeDetailDag?.dagKey || selection.selectedDag?.dagKey || '';
  const startDisabledReason = useMemo(() => workflowStartDisabledReason({ activeDetailDag, activeRunKey, dagKey, detail, list, missingRootAssigneeWarning }), [activeDetailDag, activeRunKey, dagKey, detail, list, missingRootAssigneeWarning]);
  return workflowDerivedSnapshot({ activeDetailDag, activeRunKey, dagKey, detail, list, missingRootAssigneeWarning, run, selection, startDisabledReason });
}

function workflowStartDisabledReason({ activeDetailDag, activeRunKey, dagKey, detail, list, missingRootAssigneeWarning }) {
  if (!dagKey) return '未选择自动化';
  if (list.loading || detail.detailLoading) return '自动化详情加载中';
  if (activeRunKey) return '已有运行正在进行';
  if (!STARTABLE_DAG_STATUSES.has(textValue(activeDetailDag?.status).toLowerCase())) return '当前流程状态不可运行';
  if (!STARTABLE_DAG_TRIGGERS.has(textValue(activeDetailDag?.trigger).toLowerCase())) return '当前触发方式不可运行';
  return missingRootAssigneeWarning || '';
}

function workflowDerivedSnapshot({ activeDetailDag, activeRunKey, dagKey, detail, list, missingRootAssigneeWarning, run, selection, startDisabledReason }) {
  const messages = workflowLoadMessages(list.errorState, list.syncFailure, detail.detailErrorState);
  const baseVersion = dagVersionOf(activeDetailDag);
  const finalOutput = finalOutputDescriptor(run.selectedRun?.run) || finalOutputDescriptor(detail.activeRun) || finalOutputDescriptor(selection.selectedDag?.latestRun);
  const finalText = finalOutputPreviewText(finalOutput);
  const recentRunPanelLabel = dagStatusLabel(detail.activeRun?.status || detail.runs[0]?.status || activeDetailDag?.latestRun?.status);
  const diagnosticNodes = workflowDiagnosticNodes(detail, run);
  const diagnostics = workflowNodeDiagnostics(diagnosticNodes);
  const runId = numberOrNull(run.selectedRun?.run?.runId ?? run.selectedRun?.run?.raw?.id ?? detail.activeRun?.runId ?? detail.activeRun?.raw?.id);
  return {
    activeDetailDag,
    activeRunKey,
    baseVersion,
    blockingLoadError: messages.blockingLoadError,
    dagKey,
    configurableNodes: detail.nodes.filter((node) => CONFIGURABLE_NODE_TYPES.has(textValue(node.nodeType).toLowerCase())),
    deleteDisabledReason: activeRunKey ? '已有运行正在进行，请先停止运行后再删除。' : '',
    finalOutput,
    finalText,
    diagnostics,
    diagnosticNodes,
    missingRootAssigneeWarning,
    overviewStats: workflowOverviewStats(list.items),
    recentRunPanelLabel,
    runId,
    scheduleActionLabel: isScheduledTrigger(activeDetailDag?.trigger) || activeDetailDag?.cronExpr ? '修改计划' : '创建定时任务',
    scheduleToggleVisible: isScheduledTrigger(activeDetailDag?.trigger) && Boolean(activeDetailDag?.cronExpr),
    startDisabledReason,
    syncError: messages.syncError,
  };
}

function workflowLoadMessages(listErrorState, syncFailure, detailErrorState) {
  let blockingLoadError = '';
  if (listErrorState.blockingError) blockingLoadError = '加载自动化失败：' + listErrorState.blockingError;
  else if (detailErrorState.blockingError) blockingLoadError = '加载自动化详情失败：' + detailErrorState.blockingError;
  return {
    blockingLoadError,
    syncError: syncFailure || listErrorState.cachedSyncError || detailErrorState.cachedSyncError,
  };
}

function useWorkflowActions({ actionState, derived, list, notices, refresh, selection, setDesignSession, store, workflowCwd }) {
  /*
   * workflow actions 只提交操作并刷新数据。
   * DAG 的真实状态以后端刷新结果为准，本地只放按钮和提示状态。
   */
  const runSelectedDag = useRunSelectedDagAction({ actionState, derived, list, notices, refresh });
  const stopSelectedDag = useStopSelectedDagAction({ actionState, derived, list, notices, refresh });
  const confirmDeleteDAG = useDeleteDagAction({ actionState, derived, list, notices, selection });
  const saveSchedule = useSaveScheduleAction({ actionState, derived, list, notices, refresh });
  const toggleScheduleEnabled = useToggleScheduleAction({ actionState, derived, list, notices, refresh });
  const saveAgentNode = useSaveAgentNodeAction({ actionState, derived, notices, refresh });
  const dispatchNode = useDispatchDagNodeAction({ actionState, derived, list, notices, refresh });
  const startDesignFlow = useStartDesignFlowAction({ actionState, notices, setDesignSession, store, workflowCwd });
  return { confirmDeleteDAG, dispatchNode, runSelectedDag, saveAgentNode, saveSchedule, startDesignFlow, stopSelectedDag, toggleScheduleEnabled };
}

function useRunSelectedDagAction({ actionState, derived, list, notices, refresh }) {
  return useCallback(async () => {
    if (derived.startDisabledReason) return;
    const targetDagKey = derived.dagKey;
    actionState.setActioning('start');
    actionState.setError('');
    notices.clearNotice();
    try {
      const result = await withWorkflowActionTimeout(startDag({ dagKey: targetDagKey, triggerSource: 'manual', idempotencyKey: 'ui-' + Date.now() + '-' + Math.random().toString(IDEMPOTENCY_RANDOM_RADIX).slice(2) }));
      const runKey = runKeyOf(result);
      await list.refreshDags().catch(() => []);
      await refresh.refreshDetail(targetDagKey, runKey).catch(() => {});
      const warning = textValue(result?.warning);
      notices.showTaskNotice(warning ? '已启动，后端提示：' + warning : '已启动自动化', targetDagKey);
    } catch (err) {
      actionState.setError('启动自动化失败：' + errorMessage(err));
    } finally {
      actionState.setActioning('');
    }
  }, [actionState, derived, list, notices, refresh]);
}

function useStopSelectedDagAction({ actionState, derived, list, notices, refresh }) {
  return useCallback(async () => {
    if (!derived.dagKey || !derived.activeRunKey) return;
    const targetDagKey = derived.dagKey;
    actionState.setActioning('stop');
    actionState.setError('');
    notices.clearNotice();
    try {
      await withWorkflowActionTimeout(terminateDagRun({ dagKey: targetDagKey, runKey: derived.activeRunKey, reason: 'user_requested' }));
      notices.showTaskNotice('已停止运行', targetDagKey);
    } catch (err) {
      actionState.setError('停止运行失败：' + errorMessage(err));
    } finally {
      await list.refreshDags().catch(() => []);
      await refresh.refreshDetail(targetDagKey).catch(() => {});
      actionState.setActioning('');
    }
  }, [actionState, derived, list, notices, refresh]);
}

function useDeleteDagAction({ actionState, derived, list, notices, selection }) {
  return useCallback(async () => {
    const target = actionState.deleteTarget;
    const targetKey = target?.dagKey || derived.dagKey;
    if (!targetKey) return;
    if (derived.activeRunKey) {
      actionState.setDeleteTarget(null);
      actionState.setError('删除自动化失败：已有运行正在进行，请先停止运行后再删除。');
      return;
    }
    actionState.setActioning('delete');
    actionState.setError('');
    notices.clearNotice();
    try {
      await withWorkflowActionTimeout(deleteDag({ dagKey: targetKey }));
      actionState.setDeleteTarget(null);
      const fallback = list.items.filter((item) => item.dagKey !== targetKey);
      const nextItems = await list.refreshDags().catch(() => fallback);
      selection.setSelectedDagKey(nextWorkflowSelectionKey(nextItems, selection.activeCategory));
      notices.showTaskNotice('已删除 ' + (target?.title || targetKey), targetKey);
    } catch (err) {
      actionState.setError('删除自动化失败：' + errorMessage(err));
    } finally {
      actionState.setActioning('');
    }
  }, [actionState, derived, list, notices, selection]);
}

function nextWorkflowSelectionKey(items, activeCategory) {
  return items.find((item) => dagCategoryOf(item) === activeCategory)?.dagKey || items[0]?.dagKey || '';
}

function useSaveScheduleAction({ actionState, derived, list, notices, refresh }) {
  return useCallback(async (nextCronExpr = '') => {
    const cronExpr = textValue(nextCronExpr) || textValue(actionState.scheduleCron);
    if (!derived.dagKey || !cronExpr) return;
    if (derived.baseVersion === null) { actionState.setError('自动化详情不完整，无法保存定时任务'); return; }
    if (derived.missingRootAssigneeWarning) { actionState.setError('保存定时任务失败：' + derived.missingRootAssigneeWarning); return; }
    const targetDagKey = derived.dagKey;
    actionState.setActioning('schedule');
    actionState.setError('');
    notices.clearNotice();
    try {
      await withWorkflowActionTimeout(applyDagOps({ dagKey: targetDagKey, baseVersion: derived.baseVersion, ops: [{ op: 'update_dag', patch: { trigger: 'scheduled', cron_expr: cronExpr } }] }));
      actionState.setScheduleOpen(false);
      await Promise.all([
        list.refreshDags().catch(() => []),
        refresh.refreshDetail(targetDagKey).catch(() => {}),
      ]);
      notices.showTaskNotice('已保存定时任务', targetDagKey);
    } catch (err) {
      actionState.setError('保存定时任务失败：' + errorMessage(err));
    } finally {
      actionState.setActioning('');
    }
  }, [actionState, derived, list, notices, refresh]);
}

function useToggleScheduleAction({ actionState, derived, list, notices, refresh }) {
  return useCallback(async () => {
    if (!derived.dagKey) return;
    if (derived.baseVersion === null) { actionState.setError('自动化详情不完整，无法切换自动运行'); return; }
    const targetDagKey = derived.dagKey;
    const enabled = !derived.activeDetailDag?.scheduleEnabled;
    actionState.setActioning('schedule-toggle');
    actionState.setError('');
    notices.clearNotice();
    try {
      await withWorkflowActionTimeout(applyDagOps({ dagKey: targetDagKey, baseVersion: derived.baseVersion, ops: [{ op: 'update_dag', patch: { schedule_enabled: enabled } }] }));
      await Promise.all([
        list.refreshDags().catch(() => []),
        refresh.refreshDetail(targetDagKey).catch(() => {}),
      ]);
      notices.showTaskNotice(enabled ? '已启用自动运行' : '已暂停自动运行', targetDagKey);
    } catch (err) {
      actionState.setError('切换自动运行失败：' + errorMessage(err));
    } finally {
      actionState.setActioning('');
    }
  }, [actionState, derived, list, notices, refresh]);
}

function useSaveAgentNodeAction({ actionState, derived, notices, refresh }) {
  return useCallback(async (form, node) => {
    if (!derived.dagKey || !node?.nodeKey) return;
    if (derived.baseVersion === null) { actionState.setError('自动化详情不完整，无法保存步骤'); return; }
    const targetDagKey = derived.dagKey;
    actionState.setSavingNodeKey(node.nodeKey);
    actionState.setError('');
    notices.clearNotice();
    try {
      await withWorkflowActionTimeout(applyDagOps({ dagKey: targetDagKey, baseVersion: derived.baseVersion, ops: [{ op: 'update_node', node_key: node.nodeKey, patch: dagNodePatchFromForm(form, node) }] }));
      await refresh.refreshDetail(targetDagKey).catch(() => {});
      notices.showTaskNotice('已保存步骤 ' + node.title, targetDagKey);
    } catch (err) {
      actionState.setError('保存步骤失败：' + errorMessage(err));
    } finally {
      actionState.setSavingNodeKey('');
    }
  }, [actionState, derived, notices, refresh]);
}

function useDispatchDagNodeAction({ actionState, derived, list, notices, refresh }) {
  return useCallback(async (node, assignedTo) => {
    const assignee = textValue(assignedTo);
    if (!derived.dagKey || !node?.nodeKey) return;
    if (!derived.runId) { actionState.setError('派发节点失败：当前运行缺少 runId，无法定位 runtime node'); return; }
    if (!assignee) { actionState.setError('派发节点失败：请填写执行者 assigned_to'); return; }
    const targetDagKey = derived.dagKey;
    actionState.setDispatchingNodeKey(node.nodeKey);
    actionState.setError('');
    notices.clearNotice();
    try {
      await withWorkflowActionTimeout(dispatchDagNode({
        dagKey: targetDagKey,
        runId: derived.runId,
        nodeKey: node.nodeKey,
        assignedTo: assignee,
      }));
      await Promise.all([
        list.refreshDags().catch(() => []),
        refresh.refreshDetail(targetDagKey).catch(() => {}),
      ]);
      notices.showTaskNotice(`已派发步骤 ${node.title || node.nodeKey}`, targetDagKey);
    } catch (err) {
      actionState.setError('派发节点失败：' + errorMessage(err));
    } finally {
      actionState.setDispatchingNodeKey('');
    }
  }, [actionState, derived, list, notices, refresh]);
}

function useStartDesignFlowAction({ actionState, notices, setDesignSession, store, workflowCwd }) {
  return useCallback(async (template = null, options = {}) => {
    if (!workflowCwd) return;
    const isEnterpriseTemplate = Boolean(template);
    const stayOnWorkflow = Boolean(options.stayOnWorkflow);
    actionState.setActioning('design');
    actionState.setError('');
    notices.clearNotice();
    if (isEnterpriseTemplate) {
      setDesignSession?.(enterpriseDesignSessionSnapshot(template, {
        phase: 'starting',
        message: '正在启动 DAG 设计器',
      }));
    } else if (stayOnWorkflow) {
      setDesignSession?.(freeDesignSessionSnapshot({
        phase: 'starting',
        message: '正在启动 AI 设计流程',
      }));
    }
    try {
      if (typeof store?.resolveLaunchPreferences !== 'function') throw new Error('自动化启动配置不可用');
      const launchPreferences = await store.resolveLaunchPreferences(workflowCwd);
      const { config: launchConfig = {}, ...launchPayload } = launchPreferences || {};
      const response = await withWorkflowActionTimeout(startThread(workflowDesignThreadPayload(workflowCwd, launchConfig, launchPayload)));
      const threadId = threadIdFromStartResponse(response);
      if (template) {
        if (!threadId) throw new Error('thread/start 未返回可用 threadId，无法发送模板需求');
        setDesignSession?.(enterpriseDesignSessionSnapshot(template, {
          threadId,
          phase: 'sending',
          message: '正在发送阶段拆分需求',
        }));
        try {
          await withWorkflowActionTimeout(startTurn({
            cwd: workflowCwd,
            threadId,
            input: buildEnterpriseWorkflowTemplateBrief(template),
          }));
          setDesignSession?.(enterpriseDesignSessionSnapshot(template, {
            threadId,
            phase: 'submitted',
            message: 'DAG 设计器已接收，正在评估阶段拆分和可用资源。',
          }));
        } catch (err) {
          actionState.setError('发送政企模板需求失败：' + errorMessage(err));
          setDesignSession?.(enterpriseDesignSessionSnapshot(template, {
            threadId,
            phase: 'failed',
            message: '发送政企模板需求失败：' + errorMessage(err),
          }));
          return;
        }
      }
      if (isEnterpriseTemplate) return;
      if (stayOnWorkflow) {
        setDesignSession?.(freeDesignSessionSnapshot({
          threadId,
          phase: 'submitted',
          message: threadId ? 'AI 设计流程已创建，可进入对话继续描述需求。' : 'AI 设计流程已创建。',
        }));
        return;
      }
      if (threadId && typeof store?.setActiveThread === 'function') await store.setActiveThread(threadId);
      if (typeof store?.setActivePage === 'function') store.setActivePage('chat');
    } catch (err) {
      actionState.setError((template ? '启动政企模板失败：' : '启动 AI 设计流程失败：') + errorMessage(err));
      if (isEnterpriseTemplate) {
        setDesignSession?.(enterpriseDesignSessionSnapshot(template, {
          phase: 'failed',
          message: '启动政企模板失败：' + errorMessage(err),
        }));
      } else if (stayOnWorkflow) {
        setDesignSession?.(freeDesignSessionSnapshot({
          phase: 'failed',
          message: '启动 AI 设计流程失败：' + errorMessage(err),
        }));
      }
    } finally {
      actionState.setActioning('');
    }
  }, [actionState, notices, setDesignSession, store, workflowCwd]);
}

function enterpriseDesignSessionSnapshot(template, patch = {}) {
  const values = objectValue(template?.templateValues);
  const outputFormat = textValue(values.output_format || template?.selectedOutputFormat || firstEnterpriseOutputType(template)) || 'markdown';
  return {
    templateKey: enterpriseTemplateId(template),
    templateTitle: enterpriseTemplateTitle(template),
    outputFormat,
    phases: ENTERPRISE_DESIGN_PHASES,
    phase: 'starting',
    threadId: '',
    message: '',
    startedAt: Date.now(),
    ...patch,
  };
}

function freeDesignSessionSnapshot(patch = {}) {
  return {
    templateKey: 'free-design',
    templateTitle: '自由设计',
    outputFormat: 'dag',
    phases: ['启动设计器', '创建对话', '描述需求', '生成方案', '创建自动化'],
    phase: 'starting',
    threadId: '',
    message: '',
    startedAt: Date.now(),
    ...patch,
  };
}

function workflowDesignThreadPayload(cwd, launchConfig, launchPayload) {
  return {
    cwd,
    ...launchPayload,
    provider: textValue(launchPayload.provider || launchPayload.modelProvider),
    name: 'AI 设计流程',
    agentKey: 'dag_designer',
    promptKey: 'main/dag_designer_zh',
    deferSpawn: true,
    config: { ...launchConfig, enabledTools: [...DAG_DESIGNER_ENABLED_TOOLS], providerNativeSkills: false },
  };
}

function AutomationActionButtons({ copy, onStartChat, onViewTemplates }) {
  return (
    <div className="automation-presets-row">
      <button type="button" className="preset-pill" onClick={onStartChat}>
        <Workflow size={16} className="preset-icon" />
        <span>{copy.freeDesignPageTitle}</span>
      </button>
      <button type="button" className="preset-pill" onClick={onViewTemplates}>
        <ClipboardList size={16} className="preset-icon" />
        <span>{copy.viewTemplates}</span>
      </button>
    </div>
  );
}

function AutomationEmptyState({ copy, onStartChat, onViewTemplates }) {
  return (
    <div className="automation-empty-state">
      <div className="empty-clock-wrapper">
        <Clock size={40} className="empty-clock-icon" />
      </div>
      <h2>{copy.createFirst}</h2>
      <AutomationActionButtons copy={copy} onStartChat={onStartChat} onViewTemplates={onViewTemplates} />
    </div>
  );
}

function EnterpriseWorkflowTemplates({ onSelectTemplate, sectionRef, selectedTemplateId, templatesState }) {
  const queryClient = useQueryClient();
  const templates = templatesState.items;
  const [filters, setFilters] = useState({ businessFlow: '', outputType: '', schedule: '', keyword: '' });
  const [rollbackState, setRollbackState] = useState({ target: '', error: '' });
  const businessFlowOptions = useMemo(() => Array.from(new Set(templates.flatMap((template) => {
    const businessFlow = textValue(template.business_flow || template.businessFlow);
    return businessFlow ? [businessFlow] : [];
  }))), [templates]);
  const outputTypeOptions = useMemo(() => Array.from(new Set(templates.flatMap((template) => enterpriseOutputTypes(template).filter(Boolean)))), [templates]);
  const visibleTemplates = useMemo(() => templates.filter((template) => {
    const keyword = textValue(filters.keyword).toLowerCase();
    if (keyword && !enterpriseTemplateSearchText(template).includes(keyword)) return false;
    if (filters.businessFlow && textValue(template.business_flow || template.businessFlow) !== filters.businessFlow) return false;
    if (filters.outputType && !enterpriseOutputTypes(template).includes(filters.outputType)) return false;
    if (filters.schedule === 'scheduled' && !(template.supports_schedule || template.supportsSchedule)) return false;
    if (filters.schedule === 'manual' && (template.supports_schedule || template.supportsSchedule)) return false;
    return true;
  }), [filters, templates]);
  const updateFilter = (key, value) => setFilters((current) => ({ ...current, [key]: value }));
  const rollbackTemplate = async (template) => {
    const templateId = enterpriseTemplateId(template);
    const version = enterpriseRollbackVersion(template);
    if (!templateId || !version) return;
    setRollbackState({ target: `${templateId}:${version}`, error: '' });
    try {
      await rollbackWorkflowTemplate({ templateId, version });
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ['workflow-templates', 'government-enterprise'] }),
        queryClient.invalidateQueries({ queryKey: ['workflow-template-detail', templateId] }),
      ]);
    } catch (err) {
      setRollbackState({ target: '', error: '回滚模板失败：' + errorMessage(err) });
      return;
    }
    setRollbackState({ target: '', error: '' });
  };
  return (
    <section
      ref={sectionRef}
      className="enterprise-workflow-templates"
      aria-labelledby="enterprise-workflow-templates-title"
      tabIndex={-1}
    >
      <div className="enterprise-template-heading">
        <div>
          <h2 id="enterprise-workflow-templates-title">政企工作流模板库</h2>
          <p>按业务流程选择模板，先填写关键参数并预览 DAG 草案，再交给 DAG 设计器发现资源和创建工作流。</p>
        </div>
        <div className="enterprise-template-capabilities" aria-label="政企自动化能力">
          <span>复核节点</span>
          <span>DAG 草案</span>
          <span>目标输出格式</span>
        </div>
      </div>
      {templatesState.loading ? <p className="enterprise-template-muted">正在加载模板库。</p> : null}
      {templatesState.error ? <p className="danger-text" role="alert">加载模板库失败：{templatesState.error}</p> : null}
      {rollbackState.error ? <p className="danger-text" role="alert">{rollbackState.error}</p> : null}
      <div className="enterprise-template-filters" aria-label="政企模板筛选">
        <label>
          <span>搜索模板</span>
          <input aria-label="搜索模板" value={filters.keyword} onChange={(event) => updateFilter('keyword', event.target.value)} />
        </label>
        <label>
          <span>业务流</span>
          <select value={filters.businessFlow} onChange={(event) => updateFilter('businessFlow', event.target.value)}>
            <option value="">全部</option>
            {businessFlowOptions.map((item) => <option key={item} value={item}>{item}</option>)}
          </select>
        </label>
        <label>
          <span>输出类型</span>
          <select value={filters.outputType} onChange={(event) => updateFilter('outputType', event.target.value)}>
            <option value="">全部</option>
            {outputTypeOptions.map((item) => <option key={item} value={item}>{item.toUpperCase()}</option>)}
          </select>
        </label>
        <label>
          <span>定时</span>
          <select value={filters.schedule} onChange={(event) => updateFilter('schedule', event.target.value)}>
            <option value="">全部</option>
            <option value="scheduled">支持定时</option>
            <option value="manual">手动触发</option>
          </select>
        </label>
      </div>
      <div className="enterprise-template-grid">
        {visibleTemplates.map((template) => {
          const templateId = enterpriseTemplateId(template);
          const TemplateIcon = enterpriseTemplateIcon(template);
          const selected = templateId === selectedTemplateId;
          const version = enterpriseTemplateVersion(template);
          const rollbackVersion = enterpriseRollbackVersion(template);
          const rollbackTarget = `${templateId}:${rollbackVersion}`;
          return (
            <article key={templateId} className={'enterprise-template-card' + (selected ? ' selected' : '')}>
              <div className="enterprise-template-card-top">
                <span className="enterprise-template-icon" aria-hidden="true">
                  <TemplateIcon size={18} />
                </span>
                <div>
                  <h3>{enterpriseTemplateTitle(template)}</h3>
                  <p>{enterpriseTemplateDescription(template)}</p>
                </div>
              </div>
              <div className="enterprise-template-chip-group" aria-label={`${enterpriseTemplateTitle(template)}输出格式`}>
                {enterpriseOutputTypes(template).map((item) => <span key={item}>{item.toUpperCase()}</span>)}
              </div>
              <dl className="enterprise-template-meta">
                <div>
                  <dt>业务流</dt>
                  <dd>{textValue(template.business_flow || template.businessFlow)}</dd>
                </div>
                <div>
                  <dt>节点</dt>
                  <dd>{Number(template.estimated_nodes || template.estimatedNodes || 0) || '-'} 个</dd>
                </div>
                <div>
                  <dt>复核</dt>
                  <dd>{template.requires_review || template.requiresReview ? '默认包含' : '未配置'}</dd>
                </div>
                <div>
                  <dt>版本</dt>
                  <dd>{version ? `v${version}` : '-'}</dd>
                </div>
                <div>
                  <dt>信任</dt>
                  <dd>{enterpriseTemplateTrustLevel(template) || '-'}</dd>
                </div>
                <div>
                  <dt>运行时</dt>
                  <dd>{enterpriseTemplateCompatibilityRuntime(template) || '-'}</dd>
                </div>
                <div>
                  <dt>节点类型</dt>
                  <dd>{enterpriseTemplateNodeTypes(template) || '-'}</dd>
                </div>
                <div>
                  <dt>定时</dt>
                  <dd>{template.supports_schedule || template.supportsSchedule ? '支持' : '手动'}</dd>
                </div>
              </dl>
              {rollbackVersion ? (
                <button
                  type="button"
                  className="btn-outline enterprise-template-action"
                  disabled={rollbackState.target === rollbackTarget}
                  onClick={() => { void rollbackTemplate(template); }}
                >
                  {rollbackState.target === rollbackTarget ? '回滚中...' : `回滚到 v${rollbackVersion}`}
                </button>
              ) : null}
              <button
                type="button"
                className={selected ? 'btn-dark enterprise-template-action' : 'btn-outline enterprise-template-action'}
                onClick={() => onSelectTemplate(templateId)}
                aria-label={`选择${enterpriseTemplateTitle(template)}模板`}
              >
                {selected ? '已选择' : '选择模板'}
              </button>
            </article>
          );
        })}
      </div>
    </section>
  );
}

function EnterpriseTemplateWorkbench({ onStartTemplate, selectedTemplateId, starting, workflowCwd }) {
  const queryClient = useQueryClient();
  const {
    data,
    error,
    isPending,
  } = useQuery({
    queryKey: ['workflow-template-detail', selectedTemplateId],
    queryFn: () => getWorkflowTemplate({ templateId: selectedTemplateId }),
    enabled: Boolean(selectedTemplateId),
  });
  const template = data?.template || null;

  if (!selectedTemplateId) return null;
  if (isPending) return <section className="enterprise-template-workbench"><p>正在加载模板详情。</p></section>;
  if (error) return <p className="danger-text" role="alert">加载模板详情失败：{errorMessage(error)}</p>;
  if (!template) return null;

  const refreshTemplateQueries = async (templateId) => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ['workflow-templates', 'government-enterprise'] }),
      queryClient.invalidateQueries({ queryKey: ['workflow-template-detail', templateId] }),
    ]);
  };

  return (
    <EnterpriseTemplateForm
      key={enterpriseTemplateId(template)}
      onStartTemplate={onStartTemplate}
      onTemplateChanged={refreshTemplateQueries}
      starting={starting}
      template={template}
      workflowCwd={workflowCwd}
    />
  );
}

function EnterpriseTemplateForm({ onStartTemplate, onTemplateChanged, starting, template, workflowCwd }) {
  const [formValues, setFormValues] = useState(() => enterpriseTemplateDefaultValues(template));
  const [formError, setFormError] = useState('');
  const [saveState, setSaveState] = useState({ saving: false, status: '' });
  const fields = enterpriseTemplateFields(template);
  const draftPreview = renderEnterpriseTemplatePreview(template, formValues);
  const validateForm = () => {
    const missing = missingEnterpriseRequiredFields(fields, formValues);
    if (missing.length > 0) {
      setFormError(`请先填写必填参数：${missing.join('、')}`);
      return false;
    }
    setFormError('');
    return true;
  };
  const startTemplate = () => {
    if (!validateForm()) return;
    void onStartTemplate({
      ...template,
      templateValues: formValues,
      selectedOutputFormat: formValues.output_format,
      draftPreview,
    });
  };
  const saveTemplate = async () => {
    if (!validateForm()) return;
    const templateId = enterpriseTemplateId(template);
    if (!workflowCwd) {
      setFormError('保存模板失败：项目路径不可用，无法渲染 DAG 草案。');
      return;
    }
    setSaveState({ saving: true, status: '' });
    try {
      const rendered = await renderWorkflowTemplateDraft({
        templateId,
        version: enterpriseTemplateVersionNumber(template),
        values: formValues,
        runtime_context: { cwd: workflowCwd },
      });
      const draft = rendered?.draft;
      if (!draft) throw new Error('workflowTemplates/renderDag 未返回 DAG 草案');
      const saved = await saveWorkflowTemplate(enterpriseSaveTemplatePayload(template, draft));
      await onTemplateChanged?.(templateId);
      const savedVersion = Number(saved?.template?.version) || enterpriseTemplateVersionNumber(template) + 1;
      setSaveState({ saving: false, status: `模板已保存为 v${savedVersion}` });
    } catch (err) {
      setFormError('保存模板失败：' + errorMessage(err));
      setSaveState({ saving: false, status: '' });
    }
  };

  return (
    <section className="enterprise-template-workbench" aria-labelledby="enterprise-template-workbench-title">
      <div className="enterprise-workbench-heading">
        <div>
          <h2 id="enterprise-template-workbench-title">{enterpriseTemplateTitle(template)}</h2>
          <p>{enterpriseTemplateDescription(template)}</p>
        </div>
        <div className="enterprise-workbench-badges" aria-label="模板能力">
          <span>{textValue(template.business_flow || template.businessFlow)}</span>
          <span>{template.requires_review || template.requiresReview ? '含复核节点' : '无复核节点'}</span>
          <span>{template.supports_schedule || template.supportsSchedule ? '支持定时' : '手动触发'}</span>
        </div>
      </div>
      {formError ? <p className="danger-text" role="alert">{formError}</p> : null}
      {saveState.status ? <p role="status" className="settings-status">{saveState.status}</p> : null}
      <div className="enterprise-workbench-layout">
        <form className="enterprise-template-form" onSubmit={(event) => { event.preventDefault(); startTemplate(); }}>
          <h3>模板参数</h3>
          {fields.map((field) => (
            <EnterpriseTemplateField
              field={field}
              key={field.key}
              onChange={(value) => setFormValues((current) => ({ ...current, [field.key]: value }))}
              outputTypes={enterpriseOutputTypes(template)}
              value={formValues[field.key]}
            />
          ))}
          <div className="enterprise-template-form-actions">
            <button type="submit" className="btn-dark" disabled={starting}>
              {starting ? '正在创建' : '创建工作流'}
            </button>
            <button type="button" className="btn-outline" disabled={starting} onClick={startTemplate}>
              用聊天调整
            </button>
            <button type="button" className="btn-outline" disabled={starting || saveState.saving} onClick={() => { void saveTemplate(); }}>
              {saveState.saving ? '保存中...' : '保存为模板'}
            </button>
          </div>
        </form>
        <EnterpriseTemplatePreview draft={draftPreview} template={template} />
      </div>
    </section>
  );
}

function EnterpriseTemplateField({ field, onChange, outputTypes, value }) {
  const label = enterpriseFieldLabel(field);
  const help = enterpriseFieldHelp(field);
  const commonProps = {
    'aria-label': label,
    id: `enterprise-template-field-${field.key}`,
    value: value ?? '',
    onChange: (event) => onChange(event.target.value),
  };
  return (
    <label className="enterprise-template-field" htmlFor={commonProps.id}>
      <span>{label}{field.required ? <em>必填</em> : null}</span>
      <EnterpriseTemplateInput
        commonProps={commonProps}
        field={field}
        onChange={onChange}
        outputTypes={outputTypes}
        value={value}
      />
      {help ? <small>{help}</small> : null}
    </label>
  );
}

function EnterpriseTemplateInput({ commonProps, field, onChange, outputTypes, value }) {
  if (field.type === 'textarea') {
    return <textarea {...commonProps} placeholder={enterpriseFieldPlaceholder(field)} rows={4} />;
  }
  if (field.type === 'select' || field.type === 'multi_select') {
    const options = enterpriseFieldOptions(field);
    const selectOptions = options.length > 0 ? options : outputTypes.map((format) => ({ value: format, label: { zh: format.toUpperCase() } }));
    return (
      <select {...commonProps}>
        {selectOptions.map((option) => (
          <option key={option.value} value={option.value}>{enterpriseOptionLabel(option)}</option>
        ))}
      </select>
    );
  }
  if (field.type === 'boolean') {
    return (
      <input
        aria-label={commonProps['aria-label']}
        checked={Boolean(value)}
        id={commonProps.id}
        onChange={(event) => onChange(event.target.checked)}
        type="checkbox"
      />
    );
  }
  if (field.type === 'number') {
    return <input {...commonProps} placeholder={enterpriseFieldPlaceholder(field)} type="number" />;
  }
  return <input {...commonProps} placeholder={enterpriseFieldPlaceholder(field)} type="text" />;
}

function EnterpriseTemplatePreview({ draft, template }) {
  const nodes = Array.isArray(draft.nodes) ? draft.nodes : [];
  const finalOutput = objectValue(draft.final_output || draft.finalOutput || template?.final_output || template?.finalOutput);
  return (
    <div className="enterprise-template-preview">
      <div className="enterprise-template-preview-head">
        <div>
          <h3>DAG 草案预览</h3>
          <p>{draft.title || enterpriseTemplateTitle(template)}</p>
        </div>
        <span>{draft.final_node_key || 'final_node_key'}</span>
      </div>
      <dl className="enterprise-template-preview-meta">
        <div>
          <dt>触发</dt>
          <dd>{draft.trigger || 'manual'}</dd>
        </div>
        <div>
          <dt>最终输出</dt>
          <dd>{textValue(finalOutput.path_template || finalOutput.pathTemplate)}</dd>
        </div>
      </dl>
      <ol className="enterprise-template-preview-nodes">
        {nodes.map((node) => {
          const key = enterpriseNodeKey(node);
          const isFinal = key === draft.final_node_key;
          const isReview = key.includes('review') || enterpriseNodeTitle(node).includes('复核');
          return (
            <li key={key}>
              <div>
                <strong>{enterpriseNodeTitle(node)}</strong>
                <span>{textValue(node.node_type || node.nodeType)} · {textValue(node.assigned_to || node.assignedTo)}</span>
              </div>
              <em>{(node.depends_on || node.dependsOn || []).length ? `依赖 ${(node.depends_on || node.dependsOn).join('、')}` : '起始节点'}</em>
              {isReview ? <b>复核</b> : null}
              {isFinal ? <b>最终</b> : null}
            </li>
          );
        })}
      </ol>
    </div>
  );
}

function missingEnterpriseRequiredFields(fields, values) {
  return fields.reduce((missing, field) => {
    if (field.required && !textValue(values?.[field.key])) missing.push(enterpriseFieldLabel(field));
    return missing;
  }, []);
}

function EnterpriseDesignProgress({ designSession, store }) {
  if (!designSession) return null;
  const activeIndex = enterpriseDesignPhaseIndex(designSession.phase);
  const canOpenThread = Boolean(designSession.threadId);
  const title = designSession.templateKey === 'free-design' ? '自由设计进度' : `${designSession.templateTitle}设计进度`;
  return (
    <section className={'enterprise-design-progress enterprise-design-progress-' + designSession.phase} aria-labelledby="enterprise-design-progress-title" role="status">
      <div className="enterprise-design-progress-heading">
        <div>
          <h2 id="enterprise-design-progress-title">{title}</h2>
          <p>{designSession.message || '正在准备政企自动化设计。'}</p>
        </div>
        <span>{designSession.outputFormat.toUpperCase()}</span>
      </div>
      <ol className="enterprise-design-steps">
        {designSession.phases.map((phase, index) => {
          const state = index < activeIndex ? 'done' : index === activeIndex ? 'active' : 'waiting';
          return (
            <li className={'enterprise-design-step ' + state} key={phase}>
              <span>{index + 1}</span>
              <strong>{phase}</strong>
            </li>
          );
        })}
      </ol>
      {canOpenThread ? (
        <button type="button" className="btn-outline enterprise-design-open" onClick={() => { void openEnterpriseDesignThread(store, designSession.threadId); }}>
          查看设计对话
        </button>
      ) : null}
    </section>
  );
}

function enterpriseDesignPhaseIndex(phase) {
  if (phase === 'starting') return 0;
  if (phase === 'sending') return 2;
  if (phase === 'submitted') return 4;
  if (phase === 'failed') return 1;
  return 0;
}

async function openEnterpriseDesignThread(store, threadId) {
  if (!threadId) return;
  if (typeof store?.setActiveThread === 'function') await store.setActiveThread(threadId);
  if (typeof store?.setActivePage === 'function') store.setActivePage('chat');
}

function WorkflowPageView({ copy, model, onWorkflowViewChange }) {
  const { derived, isProjectPending, list, actions, actionState } = model;
  const isEmpty = !isProjectPending && !derived.blockingLoadError && !list.loading && derived.overviewStats.total === 0;
  const templateSectionRef = useRef(null);
  const [activeWorkflowView, setActiveWorkflowView] = useState('automation');
  const [selectedTemplateId, setSelectedTemplateId] = useState('');
  const handleViewTemplates = useCallback(() => {
    setActiveWorkflowView('templates');
    onWorkflowViewChange?.('templates');
  }, [onWorkflowViewChange]);
  const handleSelectTemplate = useCallback((templateId) => {
    setActiveWorkflowView('templates');
    onWorkflowViewChange?.('templates');
    setSelectedTemplateId(templateId);
  }, [onWorkflowViewChange]);
  const handleStartFreeDesign = useCallback(() => {
    setActiveWorkflowView('freeDesign');
    onWorkflowViewChange?.('freeDesign');
    void actions.startDesignFlow(null, { stayOnWorkflow: true });
  }, [actions, onWorkflowViewChange]);
  const handleReturnAutomation = useCallback(() => {
    setActiveWorkflowView('automation');
    onWorkflowViewChange?.('automation');
    setSelectedTemplateId('');
  }, [onWorkflowViewChange]);

  useEffect(() => {
    if (activeWorkflowView !== 'templates') return;
    const section = templateSectionRef.current;
    if (!section) return;
    if (typeof section.scrollIntoView === 'function') section.scrollIntoView({ block: 'start' });
    section.focus();
  }, [activeWorkflowView, selectedTemplateId]);
  if (activeWorkflowView === 'templates') {
    return (
      <section className="workflow-page">
        <WorkflowSubpageHeader title={copy.templatePageTitle} onBack={handleReturnAutomation} />
        <WorkflowMessages copy={copy} model={model} />
        <EnterpriseWorkflowTemplates
          onSelectTemplate={handleSelectTemplate}
          sectionRef={templateSectionRef}
          selectedTemplateId={selectedTemplateId}
          templatesState={model.templates}
        />
        <EnterpriseTemplateWorkbench
          onStartTemplate={(template) => actions.startDesignFlow(template)}
          selectedTemplateId={selectedTemplateId}
          starting={actionState.actioning === 'design'}
          workflowCwd={model.workflowCwd}
        />
        <EnterpriseDesignProgress designSession={model.designSession} store={model.store} />
        <WorkflowModals model={model} />
      </section>
    );
  }

  if (activeWorkflowView === 'freeDesign') {
    return (
      <section className="workflow-page">
        <WorkflowSubpageHeader title={copy.freeDesignPageTitle} onBack={handleReturnAutomation} />
        <WorkflowMessages copy={copy} model={model} />
        <EnterpriseDesignProgress designSession={model.designSession} store={model.store} />
        <WorkflowModals model={model} />
      </section>
    );
  }

  return (
    <section className="workflow-page">
      <WorkflowHeader copy={copy} model={model} />
      <WorkflowMessages copy={copy} model={model} />
      {isEmpty ? (
        <AutomationEmptyState copy={copy} onStartChat={handleStartFreeDesign} onViewTemplates={handleViewTemplates} />
      ) : (
        <>
          <div className="automation-page-actions">
            <AutomationActionButtons copy={copy} onStartChat={handleStartFreeDesign} onViewTemplates={handleViewTemplates} />
          </div>
          <WorkflowGrid copy={copy} model={model} />
        </>
      )}
      <WorkflowModals model={model} />
    </section>
  );
}

function WorkflowHeader({ copy, model }) {
  const { isProjectPending } = model;
  return (
    <PageHeader
      icon={Workflow}
      title={copy.title}
      subtitle={isProjectPending ? copy.connecting : ''}
      actions={null}
    />
  );
}

function WorkflowSubpageHeader({ onBack, title }) {
  return (
    <PageHeader
      icon={Workflow}
      title={title}
      actions={(
        <button type="button" className="btn-outline workflow-return-button" onClick={onBack}>
          <ArrowLeft size={16} />
          <span>返回自动化</span>
        </button>
      )}
    />
  );
}

function WorkflowMessages({ copy, model }) {
  const { actionState, derived, refresh } = model;
  return (
    <>
      {derived.syncError ? <WorkflowSyncAlert copy={copy} message={derived.syncError} onRetry={refresh.refreshWorkflowSurface} /> : null}
      {actionState.error ? <p className="danger-text" role="alert">{actionState.error}</p> : null}
      <RetryableSyncError className="danger-text workflow-sync-alert" message={derived.blockingLoadError} onRetry={refresh.refreshWorkflowSurface} />
    </>
  );
}

function WorkflowSyncAlert({ copy, message, onRetry }) {
  return (
    <div className="danger-text workflow-sync-alert" role="alert">
      <span>{message}</span>
      <button type="button" className="ghost" onClick={() => { void onRetry().catch(() => {}); }}>{copy.retrySync}</button>
    </div>
  );
}

function WorkflowGrid({ copy, model }) {
  return (
    <div className="workflow-grid">
      <WorkflowList copy={copy} model={model} />
      <WorkflowDetail copy={copy} model={model} />
    </div>
  );
}

function WorkflowList({ copy, model }) {
  const { derived, isProjectPending, list, selection } = model;
  return (
    <aside className="workflow-list">
      <WorkflowCategoryTabs copy={copy} selection={selection} />
      {!isProjectPending && list.loading ? <p className="console-message">{copy.loading}</p> : null}
      {!isProjectPending && !derived.blockingLoadError && !list.loading && selection.visibleItems.length === 0 ? <p className="console-message">{copy.noTasks}</p> : null}
      {selection.visibleItems.map((item) => <WorkflowListItem copy={copy} item={item} key={item.id} selection={selection} />)}
    </aside>
  );
}

function WorkflowCategoryTabs({ copy, selection }) {
  return (
    <div className="tabs" role="tablist" aria-label={copy.categoriesAria}>
      {DAG_CATEGORIES.map((category) => (
        <button
          key={category.key}
          type="button"
          role="tab"
          aria-selected={selection.activeCategory === category.key ? 'true' : 'false'}
          className={selection.activeCategory === category.key ? 'active' : ''}
          onClick={() => selection.chooseCategory(category.key)}
        >
          {copy.categories[category.key]} {selection.counts[category.key] || 0}
        </button>
      ))}
    </div>
  );
}

function WorkflowListItem({ copy, item, selection }) {
  const recentLabel = latestDagRunLabel(item);
  return (
    <button type="button" className={item.dagKey === selection.selectedDagKey ? 'active' : ''} onClick={() => selection.setSelectedDagKey(item.dagKey)}>
      <strong>{item.title}</strong>
      <span>{recentLabel === '-' ? copy.noRuns : copy.recentRunPrefix + recentLabel}</span>
      <em>{displayDagStatusLabel(item)} · {schedulePlanLabel(item)} · {recentLabel}</em>
    </button>
  );
}

function WorkflowDetail({ copy, model }) {
  if (!model.derived.activeDetailDag) {
    return (
      <section className="workflow-detail">
        <WorkflowOverview copy={copy} derived={model.derived} />
        <EmptyState icon={Workflow} title={copy.noAutomationTitle} text={copy.noAutomationText} />
      </section>
    );
  }
  return <WorkflowDetailContent copy={copy} model={model} />;
}

function WorkflowDetailContent({ copy, model }) {
  const { derived, detail, notices, selection } = model;
  return (
    <section className="workflow-detail">
      <WorkflowDetailTop model={model} />
      <WorkflowOverview copy={copy} derived={derived} />
      {detail.detailLoading ? <p className="console-message">正在加载详情...</p> : null}
      {notices.notice?.message && notices.notice.dagKey === selection.selectedDagKey ? <p className="settings-status">{notices.notice.message}</p> : null}
      {derived.startDisabledReason ? <p className="console-message">{derived.startDisabledReason}</p> : null}
      <WorkflowFinalOutputPanel
        key={finalOutputPath(derived.finalOutput) || 'inline-output'}
        finalOutput={derived.finalOutput}
        openFile={(payload) => openSharedFile(payload)}
        previewText={derived.finalText}
        readFile={(payload) => readSharedFile(payload)}
        workflowCwd={model.workflowCwd}
      />
      <WorkflowStageProgress model={model} />
      <WorkflowStatGrid derived={derived} selection={selection} />
      <WorkflowDiagnostics model={model} />
      <WorkflowRunHistory model={model} />
      <WorkflowNodeList model={model} />
      <WorkflowAdvanced model={model} />
    </section>
  );
}

function WorkflowOverview({ copy, derived }) {
  const stats = derived.overviewStats;
  const metrics = [
    [copy.metrics.total, stats.total],
    [copy.metrics.running, stats.running],
    [copy.metrics.scheduled, stats.scheduled],
    [copy.metrics.startable, stats.startable],
    [copy.metrics.finalOutputs, stats.finalOutputs],
  ];
  return (
    <section className="workflow-overview" aria-label={copy.overviewAria}>
      <div className="workflow-overview-copy">
        <span>{copy.currentAssets}</span>
        <h2>{copy.overviewTitle}</h2>
      </div>
      <dl>
        {metrics.map(([label, value]) => (
          <div key={label}>
            <dt>{label}</dt>
            <dd>{value}</dd>
          </div>
        ))}
      </dl>
    </section>
  );
}

function WorkflowDetailTop({ model }) {
  const { actionState, actions, derived } = model;
  return (
    <div className="detail-top">
      <h2>{derived.activeDetailDag.title}</h2>
      <button type="button" className="danger" onClick={() => actionState.setDeleteTarget(derived.activeDetailDag)} disabled={Boolean(derived.deleteDisabledReason) || actionState.actioning === 'delete'} title={derived.deleteDisabledReason}>删除</button>
      <button type="button" onClick={actionState.openScheduleModal} disabled={derived.baseVersion === null || actionState.actioning === 'schedule'}>{derived.scheduleActionLabel}</button>
      {derived.scheduleToggleVisible ? <WorkflowScheduleToggle model={model} /> : null}
      {derived.activeRunKey ? <WorkflowStopButton model={model} /> : null}
      <button type="button" onClick={() => { void actions.runSelectedDag(); }} disabled={Boolean(derived.startDisabledReason) || actionState.actioning === 'start'} title={derived.startDisabledReason}>{actionState.actioning === 'start' ? '启动中...' : '运行'}</button>
    </div>
  );
}

function WorkflowScheduleToggle({ model }) {
  const { actionState, actions, derived } = model;
  return (
    <button type="button" onClick={() => { void actions.toggleScheduleEnabled(); }} disabled={derived.baseVersion === null || actionState.actioning === 'schedule-toggle'}>
      {derived.activeDetailDag.scheduleEnabled ? '暂停自动运行' : '启用自动运行'}
    </button>
  );
}

function WorkflowStopButton({ model }) {
  const { actionState, actions } = model;
  return (
    <button type="button" className="danger" onClick={() => { void actions.stopSelectedDag(); }} disabled={actionState.actioning === 'stop'}>
      {actionState.actioning === 'stop' ? '停止中...' : '停止运行'}
    </button>
  );
}

function WorkflowStatGrid({ derived, selection }) {
  return (
    <div className="stat-grid">
      <Panel title="任务状态">{displayDagStatusLabel(derived.activeDetailDag)}</Panel>
      <Panel title="运行计划">{schedulePlanLabel(derived.activeDetailDag)}</Panel>
      <Panel title="最近运行">{derived.recentRunPanelLabel === '-' ? latestDagRunLabel(selection.selectedDag) : derived.recentRunPanelLabel}</Panel>
      <Panel title="最终结果">{derived.finalText ? '已生成' : '-'}</Panel>
    </div>
  );
}

function WorkflowStageProgress({ model }) {
  const groups = useMemo(() => workflowStageGroups(model.derived.diagnosticNodes), [model.derived.diagnosticNodes]);
  const [activeNodeKey, setActiveNodeKey] = useState('');
  const flatNodes = groups.flatMap((group) => group.nodes);
  const activeNode = flatNodes.find((node) => node.nodeKey === activeNodeKey) || flatNodes[0] || null;
  if (groups.length === 0) return null;
  return (
    <Panel title="阶段进度">
      <div className="workflow-stage-progress">
        <div className="workflow-stage-track" aria-label="工作流阶段">
          {groups.map((group) => (
            <section className="workflow-stage-group" key={group.key} aria-label={`第 ${group.index + 1} 阶段`}>
              <div className="workflow-stage-group-head">
                <span>第 {group.index + 1} 阶段</span>
                <em>{group.executionLabel}</em>
              </div>
              <div className="workflow-stage-node-grid">
                {group.nodes.map((node) => (
                  <button
                    type="button"
                    className={'workflow-stage-node workflow-stage-node-' + node.statusKind}
                    key={node.nodeKey}
                    onFocus={() => setActiveNodeKey(node.nodeKey)}
                    onMouseEnter={() => setActiveNodeKey(node.nodeKey)}
                    aria-label={`${node.title} ${dagStatusLabel(node.status)}`}
                  >
                    <strong>{node.title}</strong>
                    <span>{dagStatusLabel(node.status)}</span>
                  </button>
                ))}
              </div>
            </section>
          ))}
        </div>
        <WorkflowStageOperationPanel node={activeNode} />
      </div>
    </Panel>
  );
}

function WorkflowStageOperationPanel({ node }) {
  if (!node) return null;
  return (
    <aside className="workflow-stage-operation" aria-label="节点操作说明">
      <span>{node.executionLabel}</span>
      <h4>{node.stageTitle}</h4>
      <p>{node.operationSummary}</p>
      <dl>
        <div>
          <dt>模型操作</dt>
          <dd>{node.modelAction}</dd>
        </div>
        <div>
          <dt>Skill / 工具</dt>
          <dd>{node.skillsText}</dd>
        </div>
        <div>
          <dt>输入来源</dt>
          <dd>{node.inputSourcesText}</dd>
        </div>
        <div>
          <dt>输出文件</dt>
          <dd>{node.outputsText}</dd>
        </div>
      </dl>
    </aside>
  );
}

function workflowStageGroups(nodes = []) {
  const orderedNodes = workflowOrderedNodes(nodes);
  const byKey = new Map(orderedNodes.flatMap((node) => {
    const key = textValue(node?.nodeKey);
    return key ? [[key, node]] : [];
  }));
  const memo = new Map();
  const visiting = new Set();
  const depthFor = (node) => {
    const key = textValue(node?.nodeKey);
    if (!key) return 0;
    if (memo.has(key)) return memo.get(key);
    if (visiting.has(key)) return 0;
    visiting.add(key);
    const deps = Array.isArray(node.dependsOn) ? node.dependsOn : [];
    const depth = deps.reduce((max, depKey) => {
      const dependency = byKey.get(depKey);
      return dependency ? Math.max(max, depthFor(dependency) + 1) : max;
    }, 0);
    visiting.delete(key);
    memo.set(key, depth);
    return depth;
  };
  const groups = new Map();
  orderedNodes.forEach((node) => {
    const depth = depthFor(node);
    if (!groups.has(depth)) groups.set(depth, []);
    groups.get(depth).push(workflowStageNodeView(node, depth));
  });
  return Array.from(groups.entries())
    .sort(([left], [right]) => left - right)
    .map(([depth, groupNodes], index) => ({
      key: `stage:${depth}`,
      index,
      nodes: groupNodes,
      executionLabel: workflowStageGroupExecutionLabel(groupNodes),
    }));
}

function workflowStageNodeView(node, depth) {
  const config = parsedDagConfig(node?.config);
  const ui = objectValue(config.ui);
  const outputs = workflowStageOutputPaths(ui, config);
  const skills = listFromMaybe(ui.skills);
  const inputSources = listFromMaybe(ui.input_sources || ui.inputSources);
  const executionMode = textValue(ui.execution_mode || ui.executionMode).toLowerCase();
  return {
    nodeKey: textValue(node?.nodeKey) || `stage-node:${depth}`,
    title: textValue(node?.title || ui.stage_title || ui.stageTitle || node?.nodeKey) || `阶段 ${depth + 1}`,
    status: textValue(node?.status),
    statusKind: workflowStageStatusKind(node?.status),
    stageTitle: firstText(ui.stage_title, ui.stageTitle, node?.title, node?.nodeKey, `阶段 ${depth + 1}`),
    operationSummary: firstText(ui.operation_summary, ui.operationSummary, workflowStageFallbackOperation(node, config)),
    modelAction: firstText(ui.model_action, ui.modelAction, workflowStageFallbackModelAction(node, config)),
    skillsText: skills.length > 0 ? skills.join('、') : '未声明，等待 DAG 设计器补充',
    inputSourcesText: inputSources.length > 0 ? inputSources.join('、') : workflowStageDependencyText(node),
    outputsText: outputs.length > 0 ? outputs.join('、') : '未声明 sharedfile 输出',
    executionMode,
    executionLabel: executionMode === 'parallel' ? '并行执行' : '顺序执行',
  };
}

function workflowStageGroupExecutionLabel(nodes = []) {
  if (nodes.length > 1 || nodes.some((node) => node.executionMode === 'parallel')) return '并行执行';
  return '顺序执行';
}

function workflowStageStatusKind(status) {
  const value = textValue(status).toLowerCase();
  if (['done', 'succeeded', 'success', 'completed'].includes(value)) return 'done';
  if (['running', 'dispatching', 'active'].includes(value)) return 'active';
  if (['failed', 'error', 'blocked'].includes(value)) return 'failed';
  if (['waiting_for_assignee', 'ready', 'pending'].includes(value)) return 'attention';
  if (['cancelled', 'canceled', 'terminated'].includes(value)) return 'neutral';
  return 'waiting';
}

function workflowStageOutputPaths(ui, config) {
  const expected = Array.isArray(ui.expected_outputs) ? ui.expected_outputs : (Array.isArray(ui.expectedOutputs) ? ui.expectedOutputs : []);
  const paths = expected.flatMap((item) => {
    if (!item) return [];
    if (typeof item === 'string') return [item];
    const path = firstText(item.path, item.sharedfile, item.shared_file);
    return path ? [path] : [];
  });
  const outputs = objectValue(config.outputs);
  const toSharedfile = objectValue(outputs.to_sharedfile);
  const sharedfilePath = textValue(toSharedfile.path);
  if (sharedfilePath) paths.push(sharedfilePath);
  return [...new Set(paths)];
}

function workflowStageFallbackOperation(node, config) {
  const outputs = objectValue(config.outputs);
  const toSharedfile = objectValue(outputs.to_sharedfile);
  const outputPath = textValue(toSharedfile.path);
  if (outputPath) return `该节点按配置生成材料并写入 ${outputPath}。`;
  return '该节点尚未声明悬停说明，前端根据节点标题和状态展示保守说明。';
}

function workflowStageFallbackModelAction(node, config) {
  const exec = objectValue(config.exec);
  const promptKey = firstText(exec.prompt_key, exec.promptKey, exec.verifier?.prompt_key, exec.verifier?.promptKey);
  const commandRef = firstText(exec.command_ref, exec.commandRef, exec.automation?.command_ref, exec.automation?.commandRef);
  if (promptKey) return `使用已发现的 prompt ${promptKey} 处理该阶段输入。`;
  if (commandRef) return `调用已发现的 command_card ${commandRef} 执行该阶段自动化。`;
  return `处理 ${node?.title || node?.nodeKey || '当前阶段'} 的输入并产出阶段结果。`;
}

function workflowStageDependencyText(node) {
  const deps = Array.isArray(node?.dependsOn) ? node.dependsOn.filter(Boolean) : [];
  return deps.length > 0 ? deps.join('、') : '首阶段输入或用户提供材料';
}

function listFromMaybe(value) {
  if (Array.isArray(value)) return value.flatMap((item) => {
    const text = textValue(item);
    return text ? [text] : [];
  });
  const text = textValue(value);
  return text ? wordListFromText(text) : [];
}

function WorkflowRunHistory({ model }) {
  const { detail, run, selection } = model;
  const [expandedState, setExpandedState] = useState({ expanded: false, selectedDagKey: selection.selectedDagKey });
  if (expandedState.selectedDagKey !== selection.selectedDagKey) {
    setExpandedState({ expanded: false, selectedDagKey: selection.selectedDagKey });
  }
  const expanded = expandedState.selectedDagKey === selection.selectedDagKey ? expandedState.expanded : false;
  const orderedRuns = useMemo(() => chronologicalWorkflowRuns(detail.runs), [detail.runs]);
  const hiddenCount = Math.max(orderedRuns.length - DAG_RUN_HISTORY_VISIBLE_LIMIT, 0);
  const visibleRuns = expanded || hiddenCount === 0 ? orderedRuns : orderedRuns.slice(hiddenCount);
  return (
    <Panel title="运行历史">
      <div className="dag-run-list">
        {detail.runs.length === 0 ? <p>暂无运行记录</p> : null}
        {hiddenCount > 0 ? (
          <button
            type="button"
            className="dag-run-list-toggle"
            aria-expanded={expanded}
            onClick={() => setExpandedState((current) => ({
              expanded: !(current.selectedDagKey === selection.selectedDagKey ? current.expanded : false),
              selectedDagKey: selection.selectedDagKey,
            }))}
          >
            {expanded ? '收起较早运行记录' : `展开较早 ${hiddenCount} 次运行`}
          </button>
        ) : null}
        {visibleRuns.map((item, index) => (
          <WorkflowRunRow
            index={expanded || hiddenCount === 0 ? index : hiddenCount + index}
            key={item.id}
            run={item}
            runState={run}
          />
        ))}
      </div>
    </Panel>
  );
}

function WorkflowRunRow({ index, run, runState }) {
  const active = run.runKey === runState.selectedRunKey;
  const startedAt = textValue(run.startedAt);
  return (
    <button type="button" className={'run-row ' + (active ? 'active' : '')} onClick={() => { void runState.loadRunDetail(run.runKey); }}>
      <span>{'第 ' + (index + 1) + ' 次运行'}</span>
      <em>{dagStatusLabel(run.status)}</em>
      <time dateTime={startedAt || undefined} title={startedAt || undefined}>{formatDagRunStartedAt(startedAt)}</time>
    </button>
  );
}

function WorkflowNodeList({ model }) {
  const { derived, store } = model;
  return (
    <Panel title="执行步骤">
      <div className="dag-node-list">
        {derived.diagnosticNodes.length === 0 ? <p>暂无步骤</p> : null}
        {derived.diagnosticNodes.map((node) => <WorkflowNodeRow key={node.id} node={node} store={store} />)}
      </div>
    </Panel>
  );
}

function WorkflowNodeRow({ node, store }) {
  return (
    <article className="dag-node-row">
      <strong>{node.title}</strong>
      <em>{dagStatusLabel(node.status)}</em>
      {node.threadId ? <button type="button" onClick={() => openWorkflowNodeThread(store, node)}>查看对话</button> : null}
    </article>
  );
}

function openWorkflowNodeThread(store, nodeOrThreadId) {
  if (typeof store?.openThreadById !== 'function') return;
  const node = nodeOrThreadId && typeof nodeOrThreadId === 'object' ? nodeOrThreadId : null;
  const threadId = node ? node.threadId : nodeOrThreadId;
  const dagNode = node ? { ...node, result: node.raw?.result ?? node.result } : null;
  void store.openThreadById(threadId, { source: 'dag-node', ...(dagNode ? { dagNode } : {}) }).then((opened) => {
    if (opened) store?.setActivePage?.('chat');
  });
}

function WorkflowAdvanced({ model }) {
  const { actionState, actions, derived } = model;
  return (
    <details className="workflow-advanced">
      <summary>高级设置</summary>
      {derived.configurableNodes.length > 0 ? <DagNodeEditor nodes={derived.configurableNodes} savingNodeKey={actionState.savingNodeKey} onSave={actions.saveAgentNode} /> : <p className="console-message">暂无可配置步骤</p>}
    </details>
  );
}

function WorkflowModals({ model }) {
  const { actionState, actions, derived } = model;
  return (
    <>
      {actionState.scheduleOpen ? <DagScheduleModal cron={actionState.scheduleCron} actionLabel={derived.scheduleActionLabel} saving={actionState.actioning === 'schedule'} onClose={() => actionState.setScheduleOpen(false)} onSave={actions.saveSchedule} /> : null}
      {actionState.deleteTarget ? <ConfirmDagDeleteModal dag={actionState.deleteTarget} deleting={actionState.actioning === 'delete'} onClose={() => actionState.setDeleteTarget(null)} onConfirm={actions.confirmDeleteDAG} /> : null}
    </>
  );
}

function DagNodeEditor({ nodes, savingNodeKey, onSave }) {
  const { activeNode, form, modelOptions, setActiveNodeKey, update } = useDagNodeEditorState(nodes);
  if (!activeNode) return null;

  return (
    <Panel title="步骤设置">
      <div className="dag-node-editor">
        <DagNodeSelector activeNode={activeNode} nodes={nodes} setActiveNodeKey={setActiveNodeKey} />
        <DagNodeConfigFields form={form} modelOptions={modelOptions} update={update} />
        <DagNodeInstructionFields form={form} update={update} />
        <div className="dag-node-editor-actions">
          <button type="button" onClick={() => { void onSave(form, activeNode); }} disabled={savingNodeKey === activeNode.nodeKey}>
            {savingNodeKey === activeNode.nodeKey ? '保存中...' : '保存步骤'}
          </button>
        </div>
      </div>
    </Panel>
  );
}

function useDagNodeEditorState(nodes) {
  const [activeNodeKey, setActiveNodeKeyState] = useState(nodes[0]?.nodeKey || '');
  const effectiveActiveNodeKey = nodes.some((node) => node.nodeKey === activeNodeKey)
    ? activeNodeKey
    : nodes[0]?.nodeKey || '';
  const activeNode = useMemo(
    () => nodes.find((node) => node.nodeKey === effectiveActiveNodeKey) || nodes[0] || null,
    [effectiveActiveNodeKey, nodes],
  );
  const [form, setForm] = useState(() => dagNodeFormFromNode(activeNode));
  if (effectiveActiveNodeKey !== activeNodeKey) {
    setActiveNodeKeyState(effectiveActiveNodeKey);
    setForm(dagNodeFormFromNode(activeNode));
  }

  const update = useCallback((key) => (event) => {
    const value = event.target.type === 'checkbox' ? event.target.checked : event.target.value;
    setForm((current) => ({ ...current, [key]: value }));
  }, []);
  const setActiveNodeKey = useCallback((nodeKey) => {
    const nextNode = nodes.find((node) => node.nodeKey === nodeKey) || nodes[0] || null;
    setActiveNodeKeyState(nextNode?.nodeKey || '');
    setForm(dagNodeFormFromNode(nextNode));
  }, [nodes]);
  const modelOptions = form.execProvider ? appendCurrentModelOption(form.execProvider, form.execModel) : [];
  return { activeNode, form, modelOptions, setActiveNodeKey, update };
}

function DagNodeSelector({ nodes, activeNode, setActiveNodeKey }) {
  return (
    <label>
      步骤
      <select value={activeNode.nodeKey} onChange={(event) => setActiveNodeKey(event.target.value)} aria-label="步骤">
        {nodes.map((node) => <option key={node.nodeKey} value={node.nodeKey}>{node.title}</option>)}
      </select>
    </label>
  );
}

function DagNodeConfigFields({ form, update, modelOptions }) {
  const nodeType = textValue(form.nodeType);
  return (
    <>
      <label>名称<input value={form.title} onChange={update('title')} aria-label="名称" /></label>
      <label>执行者<input value={form.assignedTo} onChange={update('assignedTo')} aria-label="执行者" /></label>
      {(nodeType === 'agent' || nodeType === 'hybrid') ? <DagAgentExecFields form={form} modelOptions={modelOptions} update={update} /> : null}
      {(nodeType === 'automation' || nodeType === 'hybrid') ? <DagAutomationExecFields form={form} update={update} /> : null}
      <label>依赖步骤<input value={form.dependsOn} onChange={update('dependsOn')} aria-label="依赖步骤" /></label>
      <DagNodeOutputFields form={form} update={update} />
    </>
  );
}

function DagAgentExecFields({ form, update, modelOptions }) {
  return (
    <>
      <label>
        执行引擎
        <select value={form.execProvider} onChange={update('execProvider')} aria-label="执行引擎">
          <option value="">默认</option>
          <option value="claude">claude</option>
          <option value="codex">codex</option>
        </select>
      </label>
      <label>
        模型
        <select value={form.execModel} onChange={update('execModel')} aria-label="模型">
          <option value="">默认</option>
          {modelOptions.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
        </select>
      </label>
      <label>Agent Key<input value={form.execAgentKey} onChange={update('execAgentKey')} aria-label="Agent Key" /></label>
      <label>Prompt Key<input value={form.execPromptKey} onChange={update('execPromptKey')} aria-label="Prompt Key" /></label>
      <label>执行 cwd<input value={form.execCwd} onChange={update('execCwd')} aria-label="执行 cwd" /></label>
    </>
  );
}

function DagAutomationExecFields({ form, update }) {
  return (
    <>
      <label>
        自动化类型
        <select value={form.automationKind} onChange={update('automationKind')} aria-label="自动化类型">
          <option value="command_card">command_card</option>
        </select>
      </label>
      <label>命令卡片<input value={form.commandRef} onChange={update('commandRef')} aria-label="命令卡片" /></label>
    </>
  );
}

function DagNodeOutputFields({ form, update }) {
  return (
    <>
      <label>输出 sharedfile<input value={form.outputSharedfilePath} onChange={update('outputSharedfilePath')} aria-label="输出 sharedfile" /></label>
      <label>
        写入模式
        <select value={form.outputSharedfileLockMode} onChange={update('outputSharedfileLockMode')} aria-label="写入模式">
          <option value="exclusive">exclusive</option>
          <option value="append">append</option>
          <option value="shared">shared</option>
        </select>
      </label>
      <label className="inline-field">
        <input type="checkbox" checked={form.outputToNodeResult} onChange={update('outputToNodeResult')} aria-label="结果写入节点摘要" />
        结果写入节点摘要
      </label>
    </>
  );
}

function DagNodeInstructionFields({ form, update }) {
  if (textValue(form.nodeType) !== 'agent') return null;
  return (
    <>
      <label className="wide">指令<textarea value={form.firstTurn} onChange={update('firstTurn')} aria-label="指令" /></label>
    </>
  );
}

function DagScheduleModal({ cron, actionLabel, saving, onClose, onSave }) {
  const schedule = useDagScheduleForm(cron, onSave);
  return (
    <FocusTrapDialog ariaLabel={actionLabel} closeDisabled={saving} onClose={onClose}>
        <header><h2>{actionLabel}</h2><button type="button" className="ghost" onClick={onClose} disabled={saving}>关闭</button></header>
        <div className="dag-node-editor">
          <DagSchedulePresetField saving={saving} schedule={schedule} />
          <DagScheduleConditionalFields saving={saving} schedule={schedule} />
          <DagScheduleTimeField saving={saving} schedule={schedule} />
        </div>
        {schedule.previewText ? <p className="settings-status">{schedule.previewText} 自动运行</p> : null}
        {schedule.inputError ? <p className="danger-text" role="alert">{schedule.inputError}</p> : null}
        <footer>
          <button type="button" className="ghost" onClick={onClose} disabled={saving}>取消</button>
          <button type="button" onClick={schedule.confirm} disabled={saving}>{saving ? '保存中...' : actionLabel}</button>
        </footer>
    </FocusTrapDialog>
  );
}

function dagScheduleFields(schedule) {
  return {
    preset: schedule.preset,
    time: schedule.time,
    weekday: schedule.weekday,
    monthDay: schedule.monthDay,
    inputError: schedule.warning || '',
  };
}

function dagScheduleReducer(state, action) {
  if (action.type === 'reset') return dagScheduleFields(action.schedule);
  if (action.type === 'change') return { ...state, [action.key]: action.value, inputError: '' };
  if (action.type === 'error') return { ...state, inputError: action.error };
  throw new Error(`unknown dag schedule action: ${action.type}`);
}

function useDagScheduleForm(cron, onSave) {
  const initialSchedule = useMemo(() => scheduleStateFromCron(cron), [cron]);
  const [state, dispatch] = useReducer(dagScheduleReducer, initialSchedule, dagScheduleFields);
  const monthDays = useMemo(() => Array.from({ length: DAYS_IN_MONTH }, (_item, index) => (index + 1).toString()), []);
  const previewText = scheduleLabelFromState(state);

  useEffect(() => {
    dispatch({ type: 'reset', schedule: initialSchedule });
  }, [initialSchedule]);

  const choose = (key) => (event) => dispatch({ type: 'change', key, value: event.target.value });
  const setInputError = (error) => dispatch({ type: 'error', error });

  const confirm = () => confirmDagSchedule({ ...state, onSave, setInputError });
  return { ...state, choose, confirm, monthDays, previewText };
}

function confirmDagSchedule({ monthDay, onSave, preset, setInputError, time, weekday }) {
  const { cronExpr, error } = cronExprFromSchedule(preset, time, weekday, monthDay);
  if (error) {
    setInputError(error);
    return;
  }
  void onSave(cronExpr);
}

function DagSchedulePresetField({ schedule, saving }) {
  return (
    <label>
      运行频率
      <select value={schedule.preset} onChange={schedule.choose('preset')} disabled={saving} aria-label="运行频率">
        <option value="daily">每天</option>
        <option value="weekdays">工作日</option>
        <option value="weekly">每周</option>
        <option value="monthly">每月</option>
      </select>
    </label>
  );
}

function DagScheduleConditionalFields({ schedule, saving }) {
  if (schedule.preset === 'weekly') {
    return (
      <label>
        星期几
        <select value={schedule.weekday} onChange={schedule.choose('weekday')} disabled={saving} aria-label="星期几">
          {DAG_WEEKDAY_OPTIONS.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}
        </select>
      </label>
    );
  }
  if (schedule.preset === 'monthly') {
    return (
      <label>
        每月几号
        <select value={schedule.monthDay} onChange={schedule.choose('monthDay')} disabled={saving} aria-label="每月几号">
          {schedule.monthDays.map((day) => <option key={day} value={day}>{day} 日</option>)}
        </select>
      </label>
    );
  }
  return null;
}

function DagScheduleTimeField({ schedule, saving }) {
  return (
    <label>
      运行时间
      <input value={schedule.time} type="time" onChange={schedule.choose('time')} disabled={saving} aria-label="运行时间" />
    </label>
  );
}

function ConfirmDagDeleteModal({ dag, deleting, onClose, onConfirm }) {
  return (
    <FocusTrapDialog ariaLabel="删除自动化" closeDisabled={deleting} onClose={onClose}>
        <header><h2>删除自动化</h2><button type="button" className="ghost" onClick={onClose} disabled={deleting}>关闭</button></header>
        <p>确定删除自动化 “{dag.title}” 吗？该操作会删除配置和运行关联信息，无法恢复。</p>
        <p className="path">{dag.dagKey}</p>
        <footer>
          <button type="button" className="ghost" onClick={onClose} disabled={deleting}>取消</button>
          <button type="button" className="text-danger" onClick={() => { void onConfirm(); }} disabled={deleting}>{deleting ? '删除中...' : '确认删除'}</button>
        </footer>
    </FocusTrapDialog>
  );
}

function EmptyState({ icon: Icon, title, text }) {
  return (
    <div className="empty-state">
      <span><Icon size={EMPTY_STATE_ICON_SIZE} /></span>
      <h2>{title}</h2>
      <p>{text}</p>
    </div>
  );
}

export { WorkflowPage };
