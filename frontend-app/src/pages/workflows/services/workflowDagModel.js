import { isCancelledError } from '@tanstack/react-query';
import { dashboardQueryErrorState, firstText, queryHasSnapshot, SKILLS_REQUEST_TIMEOUT_MS, numberOrNull, objectValue, textValue, withTimeout, wordListFromText } from '../../shared/pageShared.js';
import { getDashboardPage } from './workflowPageService.js';
import { firstPresent } from './workflowEnterpriseTemplateModel.js';
import { scheduleLabelFromCron } from './workflowScheduleModel.js';

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
  return numberOrNull(firstPresent(item?.version, item?.dag_version, item?.dagVersion, item?.raw?.version));
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
  return wordListFromText(firstText(raw.depends_on, raw.dependsOn));
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
    result: raw.result,
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
  if (isScheduledTrigger(trigger) && cronExprFromDagItem(item)) return true;
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

function formatDagRunStartedAt(value) {
  const text = textValue(value);
  if (!text) return '时间未记录';
  const matched = text.match(/^(\d{4})-(\d{2})-(\d{2})[T\s](\d{2}):(\d{2}):(\d{2})/);
  if (matched) {
    const [, year, month, day, hour, minute, second] = matched;
    return `${year}-${month}-${day} ${hour}:${minute}:${second}`;
  }
  return text;
}

function dagRunStartedAtSortText(run) {
  return textValue(run?.startedAt);
}

function scheduleLabelFromDag(item) {
  if (!isScheduledDag(item)) return '';
  const cronLabel = scheduleLabelFromCron(item?.cronExpr);
  if (cronLabel) return cronLabel;
  const label = textValue(item?.nextRunAt);
  return label ? label : '定时计划';
}

function schedulePlanLabel(item) {
  if (!isScheduledDag(item)) return '';
  return scheduleLabelFromDag(item);
}

function latestDagRunLabel(item) {
  const latestRun = item?.latestRun;
  if (!latestRun) return '暂无运行';
  return `${dagStatusLabel(latestRun.status)} · ${formatDagRunStartedAt(latestRun.startedAt)}`;
}

function displayDagStatusLabel(item) {
  if (dagHasActiveRun(item)) return '运行中';
  if (isScheduledDag(item) && item?.cronExpr) {
    return item?.scheduleEnabled ? '已启用' : '已暂停';
  }
  return dagStatusLabel(item?.status);
}

function isRunningStatus(value) {
  return RUNNING_RUN_STATUSES.has(textValue(value).toLowerCase());
}

function dagHasActiveRun(item) {
  return isRunningStatus(item?.latestRun?.status);
}

function isScheduledDag(item) {
  const trigger = textValue(item?.trigger).toLowerCase();
  return isScheduledTrigger(trigger) || Boolean(firstText(item?.cronExpr, item?.nextRunAt));
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
    running: numberOrNull(counts.running),
    scheduled: numberOrNull(counts.scheduled),
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
  return isPresentFinalOutput(finalOutputDescriptorFromRun(item?.latestRun));
}

function finalOutputDescriptorFromRun(raw) {
  const source = objectValue(raw);
  const metadata = objectValue(source.metadata);
  if (source.final_output) return source.final_output;
  if (metadata.final_output) return metadata.final_output;
  return null;
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
  return found?.key ? found.key : DAG_CATEGORIES[0].key;
}

export {
  categoryCounts,
  CONFIGURABLE_NODE_TYPES,
  cronExprFromDagItem,
  dagCategoryOf,
  dagHasActiveRun,
  DAG_CATEGORIES,
  dagKeyOf,
  dagRunStartedAtSortText,
  dagStatusLabel,
  dagTriggerValue,
  dagVersionOf,
  displayDagStatusLabel,
  fetchDagsDashboard,
  fetchWorkflowQuery,
  firstAvailableCategory,
  formatDagRunStartedAt,
  invalidateWorkflowQuery,
  isRunningStatus,
  isScheduledDag,
  isScheduledTrigger,
  latestDagRunLabel,
  normalizeDagNode,
  normalizeDagRun,
  normalizeDashboardDag,
  normalizeDagsResponse,
  nodeKeyOf,
  runKeyOf,
  RUNNING_RUN_STATUSES,
  scheduleEnabledFromDagItem,
  scheduleLabelFromDag,
  schedulePlanLabel,
  STARTABLE_DAG_STATUSES,
  STARTABLE_DAG_TRIGGERS,
  triggerLabel,
  withWorkflowActionTimeout,
  workflowDagHasFinalOutput,
  workflowDashboardQueryErrorState,
  workflowOverviewStats,
};
