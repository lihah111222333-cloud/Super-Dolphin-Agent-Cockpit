import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Workflow } from 'lucide-react';
import { FocusTrapDialog } from '../../shared/ui/FocusTrapDialog.jsx';
import { applyDagOps, deleteDag, getDashboardPage, getDagDetail, getDagRun, getDagRuns, startDag, startThread, terminateDagRun } from '../../shared/api/backendApi.js';
import { appendCurrentModelOption, dashboardQueryErrorState, dashboardQueryKey, errorMessage, firstText, listToText, numberOrNull, objectValue, optionalSettingsCwd, queryHasSnapshot, SKILLS_REQUEST_TIMEOUT_MS, textValue, useDashboardFocusInvalidation, withTimeout, wordListFromText } from '../shared/pageShared.js';
import { PageHeader, Panel, RetryableSyncError } from '../shared/pageComponents.jsx';

const DAG_RECENT_RUN_LIMIT = 5;

const DAG_DESIGNER_ENABLED_TOOLS = Object.freeze([
  'list_models',
  'prompt_list',
  'command_list',
  'shared_file_list',
  'task_create_dag',
  'task_get_dag',
  'task_dag_apply_ops',
  'task_start_dag',
]);

const DAG_CATEGORIES = Object.freeze([
  { key: 'running', label: '进行中' },
  { key: 'scheduled', label: '定时任务' },
  { key: 'history', label: '历史记录' },
]);

const STARTABLE_DAG_STATUSES = new Set(['draft', 'ready']);

const STARTABLE_DAG_TRIGGERS = new Set(['manual', 'scheduled', 'schedule', 'cron', '']);

const RUNNING_RUN_STATUSES = new Set(['running', 'pending', 'dispatching', 'waiting_for_assignee']);

async function fetchDagsDashboard(cwd) {
  const response = await withTimeout(
    getDashboardPage({ cwd, page: 'dags' }),
    SKILLS_REQUEST_TIMEOUT_MS,
    '自动化加载超时，请检查任务数据或后端状态。',
  );
  return normalizeDagsResponse(response);
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
  const runKey = runKeyOf(raw);
  return {
    id: runKey || `run:${index}`,
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
    config,
    raw,
  };
}

function normalizeDashboardDag(raw = {}, index = 0) {
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
    scheduleEnabled: scheduleEnabledFromDagItem(raw),
    raw,
  };
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

function parseScheduleTime(value) {
  const text = textValue(value);
  const match = /^(\d{1,2}):(\d{2})$/.exec(text);
  if (!match) return null;
  const hour = Number(match[1]);
  const minute = Number(match[2]);
  if (!Number.isInteger(hour) || !Number.isInteger(minute) || hour < 0 || hour > 23 || minute < 0 || minute > 59) {
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
  return Number.isInteger(day) && day >= 1 && day <= 31;
}

function parseCronScheduleParts(cronExpr) {
  const text = textValue(cronExpr);
  if (!text) return { empty: true };
  const parts = text.split(/\s+/);
  if (parts.length !== 5) return { error: DAG_SCHEDULE_FORMAT_WARNING };
  const [minuteText, hourText, dayOfMonth, month, dayOfWeek] = parts;
  const hour = Number(hourText);
  const minute = Number(minuteText);
  if (!Number.isInteger(hour) || !Number.isInteger(minute) || hour < 0 || hour > 23 || minute < 0 || minute > 59) {
    return { error: DAG_SCHEDULE_FORMAT_WARNING };
  }
  return { minute, hour, dayOfMonth, month, dayOfWeek, time: `${twoDigits(hour)}:${twoDigits(minute)}` };
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
  if (preset === 'daily') return { cronExpr: `${minute} ${hour} * * *`, error: '' };
  if (preset === 'weekdays') return { cronExpr: `${minute} ${hour} * * 1-5`, error: '' };
  if (preset === 'weekly') {
    if (!Object.prototype.hasOwnProperty.call(DAG_WEEKDAY_LABELS, weekday)) return { cronExpr: '', error: '请选择星期几' };
    return { cronExpr: `${minute} ${hour} * * ${weekday}`, error: '' };
  }
  if (preset === 'monthly') {
    const day = Number(monthDay);
    if (!Number.isInteger(day) || day < 1 || day > 31) return { cronExpr: '', error: '请选择每月几号' };
    return { cronExpr: `${minute} ${hour} ${day} * *`, error: '' };
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

function firstAvailableCategory(items) {
  const counts = categoryCounts(items);
  const found = DAG_CATEGORIES.find((category) => counts[category.key] > 0);
  return found?.key || DAG_CATEGORIES[0].key;
}

function finalOutputText(raw) {
  const source = raw?.run || raw || {};
  const metadata = objectValue(source.metadata);
  const value = source.final_output || source.finalOutput || metadata.final_output || metadata.finalOutput;
  if (typeof value === 'string') return value.trim();
  if (value && typeof value === 'object') {
    return firstText(value.text, value.content, value.message, value.output, value.summary) || JSON.stringify(value);
  }
  return '';
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
  const config = objectValue(node?.config);
  return {
    nodeKey: textValue(node?.nodeKey),
    title: textValue(node?.title),
    assignedTo: textValue(node?.assignedTo),
    provider: firstText(config.provider, config.agentKey, config.agent_key),
    model: textValue(config.model),
    promptKey: firstText(config.prompt_key, config.promptKey),
    dependsOn: listToText(node?.dependsOn || []),
    firstTurn: firstText(config.first_turn, config.firstTurn, config.prompt),
    outputFile: firstText(config.output_file, config.outputFile),
  };
}

function dagNodePatchFromForm(form, node) {
  const config = {
    ...objectValue(node?.config),
    provider: textValue(form.provider),
    model: textValue(form.model),
    prompt_key: textValue(form.promptKey),
    first_turn: textValue(form.firstTurn),
    output_file: textValue(form.outputFile),
  };
  return {
    title: textValue(form.title),
    assigned_to: textValue(form.assignedTo),
    depends_on: wordListFromText(form.dependsOn),
    config: cleanObject(config),
  };
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
  const names = missing.map((node) => node.title || node.nodeKey).filter(Boolean).join('、');
  return names ? `首个步骤「${names}」缺少执行者，请先在高级设置中填写执行者。` : '首个步骤缺少执行者，请先在高级设置中填写执行者。';
}

function WorkflowPage({ projectPath, store, refreshKey = 0 }) {
  const model = useWorkflowPageController({ projectPath, refreshKey, store });
  return <WorkflowPageView model={model} />;
}

function useWorkflowPageController({ projectPath, refreshKey, store }) {
  const workflowCwd = optionalSettingsCwd(projectPath);
  const isProjectPending = !workflowCwd;
  useDashboardFocusInvalidation(workflowCwd, 'dags');
  const list = useWorkflowListQuery(workflowCwd);
  const selection = useWorkflowSelection(list.items);
  const detail = useWorkflowDetailQuery({ items: list.items, selectedDag: selection.selectedDag, selectedDagKey: selection.selectedDagKey, workflowCwd });
  const run = useWorkflowRunDetail({ activeRun: detail.activeRun, runs: detail.runs, workflowCwd });
  const notices = useWorkflowNotice(selection.selectedDagKey);
  const refresh = useWorkflowRefresh({ refreshDags: list.refreshDags, refreshKey, selectedDagKeyRef: selection.selectedDagKeyRef, selectedRunKey: run.selectedRunKey, setSelectedRunKey: run.setSelectedRunKey, workflowCwd });
  const actionState = useWorkflowActionState(detail.activeDetailDag);
  const derived = useWorkflowDerivedState({ detail, list, run, selection });
  const actions = useWorkflowActions({ actionState, derived, detail, list, notices, refresh, run, selection, store, workflowCwd });
  return { actions, actionState, derived, detail, isProjectPending, list, notices, refresh, run, selection, store, workflowCwd };
}

function useWorkflowListQuery(workflowCwd) {
  const queryClient = useQueryClient();
  const [workflowSyncFailure, setWorkflowSyncFailure] = useState('');
  const dagsQuery = useQuery({
    queryKey: dashboardQueryKey(workflowCwd, 'dags'),
    queryFn: () => fetchDagsDashboard(workflowCwd),
    enabled: Boolean(workflowCwd),
  });
  const hasSnapshot = queryHasSnapshot(dagsQuery);
  const items = useMemo(() => (Array.isArray(dagsQuery.data) ? dagsQuery.data : []), [dagsQuery.data]);
  const loading = Boolean(workflowCwd) && dagsQuery.isPending && !hasSnapshot;
  const errorState = dashboardQueryErrorState(dagsQuery, hasSnapshot);
  const refreshDags = useCallback(async () => {
    if (!workflowCwd) return [];
    const key = dashboardQueryKey(workflowCwd, 'dags');
    try {
      await queryClient.invalidateQueries({ queryKey: key }, { throwOnError: true });
      setWorkflowSyncFailure('');
    } catch (err) {
      setWorkflowSyncFailure('同步失败，显示的是上次成功的数据：' + errorMessage(err));
    }
    return queryClient.getQueryData(key) || [];
  }, [queryClient, workflowCwd]);
  return { errorState, items, loading, refreshDags, syncFailure: workflowSyncFailure };
}

function useWorkflowSelection(items) {
  const [activeCategory, setActiveCategory] = useState(DAG_CATEGORIES[0].key);
  const [categoryManuallySelected, setCategoryManuallySelected] = useState(false);
  const [selectedDagKey, setSelectedDagKey] = useState('');
  const selectedDagKeyRef = useRef(selectedDagKey);
  const counts = useMemo(() => categoryCounts(items), [items]);
  const preferredCategory = useMemo(() => firstAvailableCategory(items), [items]);
  const visibleItems = useMemo(() => items.filter((item) => dagCategoryOf(item) === activeCategory), [activeCategory, items]);
  const selectedDag = useMemo(() => items.find((item) => item.dagKey === selectedDagKey) || visibleItems[0] || null, [items, selectedDagKey, visibleItems]);

  useEffect(() => {
    selectedDagKeyRef.current = selectedDagKey;
  }, [selectedDagKey]);
  useEffect(() => {
    if (!categoryManuallySelected && items.length > 0 && visibleItems.length === 0 && activeCategory !== preferredCategory) setActiveCategory(preferredCategory);
  }, [activeCategory, categoryManuallySelected, items.length, preferredCategory, visibleItems.length]);
  useEffect(() => {
    if (visibleItems.length === 0) setSelectedDagKey('');
    else if (!visibleItems.some((item) => item.dagKey === selectedDagKey)) setSelectedDagKey(visibleItems[0].dagKey);
  }, [selectedDagKey, visibleItems]);

  const chooseCategory = useCallback((categoryKey) => {
    setCategoryManuallySelected(true);
    setActiveCategory(categoryKey);
  }, []);
  return { activeCategory, chooseCategory, counts, selectedDag, selectedDagKey, selectedDagKeyRef, setSelectedDagKey, visibleItems };
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
  const dagDetailQuery = useQuery({
    queryKey: dashboardQueryKey(workflowCwd, 'dag-detail', selectedDagKey),
    queryFn: () => fetchWorkflowDagDetail(selectedDagKey, items),
    enabled: Boolean(workflowCwd && selectedDagKey),
  });
  const hasSnapshot = queryHasSnapshot(dagDetailQuery);
  const detailData = dagDetailQuery.data || {};
  const nodes = useMemo(() => (Array.isArray(detailData.nodes) ? detailData.nodes : []), [detailData.nodes]);
  const runs = useMemo(() => (Array.isArray(detailData.runs) ? detailData.runs : []), [detailData.runs]);
  return {
    activeDetailDag: detailData.dag || selectedDag || null,
    activeRun: detailData.activeRun || null,
    detailDag: detailData.dag || null,
    detailErrorState: dashboardQueryErrorState(dagDetailQuery, hasSnapshot),
    detailLoading: Boolean(selectedDagKey) && dagDetailQuery.isPending && !hasSnapshot && !selectedDag,
    nodes,
    runs,
  };
}

function useWorkflowRunDetail({ activeRun, runs, workflowCwd }) {
  const queryClient = useQueryClient();
  const [selectedRunKey, setSelectedRunKey] = useState('');
  useEffect(() => {
    const nextRunKey = activeRun?.runKey || runs[0]?.runKey || '';
    if (!nextRunKey) {
      if (selectedRunKey) setSelectedRunKey('');
      return;
    }
    if (!selectedRunKey || !runs.some((run) => run.runKey === selectedRunKey)) setSelectedRunKey(nextRunKey);
  }, [activeRun, runs, selectedRunKey]);
  const runDetailQuery = useQuery({
    queryKey: dashboardQueryKey(workflowCwd, 'dag-run', selectedRunKey),
    queryFn: () => fetchWorkflowRunDetail(selectedRunKey),
    enabled: Boolean(workflowCwd && selectedRunKey),
  });
  const loadRunDetail = useCallback((runKey) => {
    const key = textValue(runKey);
    if (!key) { setSelectedRunKey(''); return null; }
    setSelectedRunKey(key);
    return queryClient.fetchQuery({ queryKey: dashboardQueryKey(workflowCwd, 'dag-run', key), queryFn: () => fetchWorkflowRunDetail(key) });
  }, [queryClient, workflowCwd]);
  return { loadRunDetail, selectedRun: runDetailQuery.data || null, selectedRunKey, setSelectedRunKey };
}

function useWorkflowNotice(selectedDagKey) {
  const [notice, setNotice] = useState(null);
  const selectedDagKeyRef = useRef(selectedDagKey);
  useEffect(() => {
    selectedDagKeyRef.current = selectedDagKey;
    setNotice(null);
  }, [selectedDagKey]);
  const clearNotice = useCallback(() => setNotice(null), []);
  const showTaskNotice = useCallback((message, taskKey = selectedDagKeyRef.current) => {
    const key = textValue(taskKey);
    if (!message || !key || selectedDagKeyRef.current !== key) return;
    setNotice({ dagKey: key, message });
  }, []);
  return { clearNotice, notice, showTaskNotice };
}

function useWorkflowRefresh({ refreshDags, refreshKey, selectedDagKeyRef, selectedRunKey, setSelectedRunKey, workflowCwd }) {
  const queryClient = useQueryClient();
  const handledWorkflowRefreshRef = useRef(0);
  const workflowRefreshKey = Number(refreshKey || 0);
  const refreshDetail = useCallback(async (dagKey, preferredRunKey = '') => {
    const key = textValue(dagKey);
    if (!key) return;
    if (preferredRunKey) setSelectedRunKey(textValue(preferredRunKey));
    await queryClient.invalidateQueries({ queryKey: dashboardQueryKey(workflowCwd, 'dag-detail', key) });
    await queryClient.invalidateQueries({ queryKey: dashboardQueryKey(workflowCwd, 'dag-run') });
  }, [queryClient, setSelectedRunKey, workflowCwd]);
  const refreshWorkflowSurface = useCallback(async () => {
    if (!workflowCwd) return;
    await refreshDags();
    if (selectedDagKeyRef.current) await refreshDetail(selectedDagKeyRef.current, selectedRunKey);
  }, [refreshDags, refreshDetail, selectedDagKeyRef, selectedRunKey, workflowCwd]);
  useEffect(() => {
    if (workflowRefreshKey <= 0 || handledWorkflowRefreshRef.current === workflowRefreshKey) return;
    handledWorkflowRefreshRef.current = workflowRefreshKey;
    void refreshWorkflowSurface();
  }, [refreshWorkflowSurface, workflowRefreshKey]);
  return { refreshDetail, refreshWorkflowSurface };
}

function useWorkflowActionState(activeDetailDag) {
  const [actioning, setActioning] = useState('');
  const [savingNodeKey, setSavingNodeKey] = useState('');
  const [error, setError] = useState('');
  const [deleteTarget, setDeleteTarget] = useState(null);
  const [scheduleOpen, setScheduleOpen] = useState(false);
  const [scheduleCron, setScheduleCron] = useState('0 8 * * *');
  const openScheduleModal = useCallback(() => {
    setScheduleCron(activeDetailDag?.cronExpr || '0 8 * * *');
    setScheduleOpen(true);
  }, [activeDetailDag]);
  return { actioning, deleteTarget, error, openScheduleModal, savingNodeKey, scheduleCron, scheduleOpen, setActioning, setDeleteTarget, setError, setSavingNodeKey, setScheduleCron, setScheduleOpen };
}

function useWorkflowDerivedState({ detail, list, run, selection }) {
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
  const finalText = finalOutputText(run.selectedRun?.run) || finalOutputText(detail.activeRun) || finalOutputText(selection.selectedDag?.latestRun);
  const recentRunPanelLabel = dagStatusLabel(detail.activeRun?.status || detail.runs[0]?.status || activeDetailDag?.latestRun?.status);
  return {
    activeDetailDag,
    activeRunKey,
    agentNodes: detail.nodes.filter((node) => textValue(node.nodeType).toLowerCase() === 'agent'),
    baseVersion,
    blockingLoadError: messages.blockingLoadError,
    dagKey,
    deleteDisabledReason: activeRunKey ? '已有运行正在进行，请先停止运行后再删除。' : '',
    finalText,
    missingRootAssigneeWarning,
    recentRunPanelLabel,
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

function useWorkflowActions({ actionState, derived, list, notices, refresh, selection, store, workflowCwd }) {
  const runSelectedDag = useRunSelectedDagAction({ actionState, derived, list, notices, refresh });
  const stopSelectedDag = useStopSelectedDagAction({ actionState, derived, list, notices, refresh });
  const confirmDeleteDAG = useDeleteDagAction({ actionState, derived, list, notices, selection });
  const saveSchedule = useSaveScheduleAction({ actionState, derived, list, notices, refresh });
  const toggleScheduleEnabled = useToggleScheduleAction({ actionState, derived, list, notices, refresh });
  const saveAgentNode = useSaveAgentNodeAction({ actionState, derived, notices, refresh });
  const startDesignFlow = useStartDesignFlowAction({ actionState, notices, store, workflowCwd });
  return { confirmDeleteDAG, runSelectedDag, saveAgentNode, saveSchedule, startDesignFlow, stopSelectedDag, toggleScheduleEnabled };
}

function useRunSelectedDagAction({ actionState, derived, list, notices, refresh }) {
  return useCallback(async () => {
    if (derived.startDisabledReason) return;
    const targetDagKey = derived.dagKey;
    actionState.setActioning('start');
    actionState.setError('');
    notices.clearNotice();
    try {
      const result = await startDag({ dagKey: targetDagKey, triggerSource: 'manual', idempotencyKey: 'ui-' + Date.now() + '-' + Math.random().toString(16).slice(2) });
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
      await terminateDagRun({ dagKey: targetDagKey, runKey: derived.activeRunKey, reason: 'user_requested' });
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
      await deleteDag({ dagKey: targetKey });
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
      await applyDagOps({ dagKey: targetDagKey, baseVersion: derived.baseVersion, ops: [{ op: 'update_dag', patch: { trigger: 'scheduled', cron_expr: cronExpr } }] });
      actionState.setScheduleOpen(false);
      await list.refreshDags().catch(() => []);
      await refresh.refreshDetail(targetDagKey).catch(() => {});
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
      await applyDagOps({ dagKey: targetDagKey, baseVersion: derived.baseVersion, ops: [{ op: 'update_dag', patch: { schedule_enabled: enabled } }] });
      await list.refreshDags().catch(() => []);
      await refresh.refreshDetail(targetDagKey).catch(() => {});
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
      await applyDagOps({ dagKey: targetDagKey, baseVersion: derived.baseVersion, ops: [{ op: 'update_node', node_key: node.nodeKey, patch: dagNodePatchFromForm(form, node) }] });
      await refresh.refreshDetail(targetDagKey).catch(() => {});
      notices.showTaskNotice('已保存步骤 ' + node.title, targetDagKey);
    } catch (err) {
      actionState.setError('保存步骤失败：' + errorMessage(err));
    } finally {
      actionState.setSavingNodeKey('');
    }
  }, [actionState, derived, notices, refresh]);
}

function useStartDesignFlowAction({ actionState, notices, store, workflowCwd }) {
  return useCallback(async () => {
    if (!workflowCwd) return;
    actionState.setActioning('design');
    actionState.setError('');
    notices.clearNotice();
    try {
      if (typeof store?.resolveLaunchPreferences !== 'function') throw new Error('自动化启动配置不可用');
      const launchPreferences = await store.resolveLaunchPreferences(workflowCwd);
      const { config: launchConfig = {}, ...launchPayload } = launchPreferences || {};
      const response = await startThread(workflowDesignThreadPayload(workflowCwd, launchConfig, launchPayload));
      const threadId = threadIdFromStartResponse(response);
      if (threadId && typeof store?.setActiveThread === 'function') await store.setActiveThread(threadId);
      if (typeof store?.setActivePage === 'function') store.setActivePage('chat');
    } catch (err) {
      actionState.setError('启动 AI 设计流程失败：' + errorMessage(err));
    } finally {
      actionState.setActioning('');
    }
  }, [actionState, notices, store, workflowCwd]);
}

function workflowDesignThreadPayload(cwd, launchConfig, launchPayload) {
  return {
    cwd,
    ...launchPayload,
    name: 'AI 设计流程',
    agentKey: 'dag_designer',
    promptKey: 'main/dag_designer_zh',
    deferSpawn: true,
    config: { ...launchConfig, enabledTools: [...DAG_DESIGNER_ENABLED_TOOLS], providerNativeSkills: false },
  };
}

function WorkflowPageView({ model }) {
  return (
    <section className="workflow-page">
      <WorkflowHeader model={model} />
      <WorkflowMessages model={model} />
      <WorkflowGrid model={model} />
      <WorkflowModals model={model} />
    </section>
  );
}

function WorkflowHeader({ model }) {
  const { actionState, actions, isProjectPending, workflowCwd } = model;
  return (
    <PageHeader
      icon={Workflow}
      title="自动化"
      subtitle={workflowCwd ? '当前项目：' + workflowCwd : '正在连接本地项目...'}
      actions={(
        <button type="button" onClick={() => { void actions.startDesignFlow(); }} disabled={isProjectPending || actionState.actioning === 'design'}>
          {actionState.actioning === 'design' ? '启动中...' : 'AI 设计流程'}
        </button>
      )}
    />
  );
}

function WorkflowMessages({ model }) {
  const { actionState, derived, refresh } = model;
  return (
    <>
      {derived.syncError ? <WorkflowSyncAlert message={derived.syncError} onRetry={refresh.refreshWorkflowSurface} /> : null}
      {actionState.error ? <p className="danger-text" role="alert">{actionState.error}</p> : null}
      <RetryableSyncError className="danger-text workflow-sync-alert" message={derived.blockingLoadError} onRetry={refresh.refreshWorkflowSurface} />
    </>
  );
}

function WorkflowSyncAlert({ message, onRetry }) {
  return (
    <div className="danger-text workflow-sync-alert" role="alert">
      <span>{message}</span>
      <button type="button" className="ghost" onClick={() => { void onRetry(); }}>重试同步</button>
    </div>
  );
}

function WorkflowGrid({ model }) {
  return (
    <div className="workflow-grid">
      <WorkflowList model={model} />
      <WorkflowDetail model={model} />
    </div>
  );
}

function WorkflowList({ model }) {
  const { derived, isProjectPending, list, selection } = model;
  return (
    <aside className="workflow-list">
      <WorkflowCategoryTabs selection={selection} />
      {!isProjectPending && list.loading ? <p className="console-message">正在加载自动化...</p> : null}
      {!isProjectPending && !derived.blockingLoadError && !list.loading && selection.visibleItems.length === 0 ? <p className="console-message">无任务</p> : null}
      {selection.visibleItems.map((item) => <WorkflowListItem item={item} key={item.id} selection={selection} />)}
    </aside>
  );
}

function WorkflowCategoryTabs({ selection }) {
  return (
    <div className="tabs" role="tablist" aria-label="自动化分类">
      {DAG_CATEGORIES.map((category) => (
        <button
          key={category.key}
          type="button"
          role="tab"
          aria-selected={selection.activeCategory === category.key ? 'true' : 'false'}
          className={selection.activeCategory === category.key ? 'active' : ''}
          onClick={() => selection.chooseCategory(category.key)}
        >
          {category.label} {selection.counts[category.key] || 0}
        </button>
      ))}
    </div>
  );
}

function WorkflowListItem({ item, selection }) {
  const recentLabel = latestDagRunLabel(item);
  return (
    <button type="button" className={item.dagKey === selection.selectedDagKey ? 'active' : ''} onClick={() => selection.setSelectedDagKey(item.dagKey)}>
      <strong>{item.title}</strong>
      <span>{recentLabel === '-' ? '暂无运行' : '最近运行：' + recentLabel}</span>
      <em>{displayDagStatusLabel(item)} · {schedulePlanLabel(item)} · {recentLabel}</em>
    </button>
  );
}

function WorkflowDetail({ model }) {
  if (!model.derived.activeDetailDag) {
    return (
      <section className="workflow-detail">
        <EmptyState icon={Workflow} title="暂无自动化" text="左侧选择自动化后查看详情。" />
      </section>
    );
  }
  return <WorkflowDetailContent model={model} />;
}

function WorkflowDetailContent({ model }) {
  const { derived, detail, notices, selection } = model;
  return (
    <section className="workflow-detail">
      <WorkflowDetailTop model={model} />
      {detail.detailLoading ? <p className="console-message">正在加载详情...</p> : null}
      {notices.notice?.message && notices.notice.dagKey === selection.selectedDagKey ? <p className="settings-status">{notices.notice.message}</p> : null}
      {derived.startDisabledReason ? <p className="console-message">{derived.startDisabledReason}</p> : null}
      <Panel title="最终结果">{derived.finalText || '当前运行尚未标记最终结果。'}</Panel>
      <WorkflowStatGrid derived={derived} selection={selection} />
      <WorkflowRunHistory model={model} />
      <WorkflowNodeList model={model} />
      <WorkflowAdvanced model={model} />
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

function WorkflowRunHistory({ model }) {
  const { detail, run } = model;
  return (
    <Panel title="运行历史">
      <div className="dag-run-list">
        {detail.runs.length === 0 ? <p>暂无运行记录</p> : null}
        {detail.runs.map((item, index) => <WorkflowRunRow index={index} key={item.id} run={item} runState={run} />)}
      </div>
    </Panel>
  );
}

function WorkflowRunRow({ index, run, runState }) {
  const active = run.runKey === runState.selectedRunKey;
  return (
    <button type="button" className={'run-row ' + (active ? 'active' : '')} onClick={() => { void runState.loadRunDetail(run.runKey); }}>
      <span>{'第 ' + (index + 1) + ' 次运行'}</span>
      <em>{dagStatusLabel(run.status)}</em>
      <time>{run.startedAt || '时间未记录'}</time>
    </button>
  );
}

function WorkflowNodeList({ model }) {
  const { detail, store } = model;
  return (
    <Panel title="执行步骤">
      <div className="dag-node-list">
        {detail.nodes.length === 0 ? <p>暂无步骤</p> : null}
        {detail.nodes.map((node) => <WorkflowNodeRow key={node.id} node={node} store={store} />)}
      </div>
    </Panel>
  );
}

function WorkflowNodeRow({ node, store }) {
  return (
    <article className="dag-node-row">
      <strong>{node.title}</strong>
      <em>{dagStatusLabel(node.status)}</em>
      {node.threadId ? <button type="button" onClick={() => openWorkflowNodeThread(store, node.threadId)}>查看对话</button> : null}
    </article>
  );
}

function openWorkflowNodeThread(store, threadId) {
  void store?.setActiveThread?.(threadId);
  store?.setActivePage?.('chat');
}

function WorkflowAdvanced({ model }) {
  const { actionState, actions, derived } = model;
  return (
    <details className="workflow-advanced">
      <summary>高级设置</summary>
      {derived.agentNodes.length > 0 ? <DagNodeEditor nodes={derived.agentNodes} savingNodeKey={actionState.savingNodeKey} onSave={actions.saveAgentNode} /> : <p className="console-message">暂无可配置步骤</p>}
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
  const [activeNodeKey, setActiveNodeKey] = useState(nodes[0]?.nodeKey || '');
  const activeNode = useMemo(
    () => nodes.find((node) => node.nodeKey === activeNodeKey) || nodes[0] || null,
    [activeNodeKey, nodes],
  );
  const [form, setForm] = useState(() => dagNodeFormFromNode(activeNode));
  const [formNodeKey, setFormNodeKey] = useState(activeNode?.nodeKey || '');

  useEffect(() => {
    if (!activeNode || nodes.some((node) => node.nodeKey === activeNodeKey)) return;
    setActiveNodeKey(nodes[0]?.nodeKey || '');
  }, [activeNode, activeNodeKey, nodes]);

  useEffect(() => {
    const nextNodeKey = activeNode?.nodeKey || '';
    if (nextNodeKey === formNodeKey) return;
    setFormNodeKey(nextNodeKey);
    setForm(dagNodeFormFromNode(activeNode));
  }, [activeNode, formNodeKey]);

  if (!activeNode) return null;
  const update = (key) => (event) => setForm((current) => ({ ...current, [key]: event.target.value }));
  const modelOptions = form.provider ? appendCurrentModelOption(form.provider, form.model) : [];

  return (
    <Panel title="步骤设置">
      <div className="dag-node-editor">
        <label>
          步骤
          <select value={activeNode.nodeKey} onChange={(event) => setActiveNodeKey(event.target.value)}>
            {nodes.map((node) => <option key={node.nodeKey} value={node.nodeKey}>{node.title}</option>)}
          </select>
        </label>
        <label>名称<input value={form.title} onChange={update('title')} aria-label="名称" /></label>
        <label>执行者<input value={form.assignedTo} onChange={update('assignedTo')} aria-label="执行者" /></label>
        <label>
          执行引擎
          <select value={form.provider} onChange={update('provider')} aria-label="执行引擎">
            <option value="">默认</option>
            <option value="claude">claude</option>
            <option value="codex">codex</option>
          </select>
        </label>
        <label>
          模型
          <select value={form.model} onChange={update('model')} aria-label="模型">
            <option value="">默认</option>
            {modelOptions.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
          </select>
        </label>
        <label>提示词<input value={form.promptKey} onChange={update('promptKey')} aria-label="提示词" /></label>
        <label>依赖步骤<input value={form.dependsOn} onChange={update('dependsOn')} aria-label="依赖步骤" /></label>
        <label className="wide">指令<textarea value={form.firstTurn} onChange={update('firstTurn')} aria-label="指令" /></label>
        <label>输出文件<input value={form.outputFile} onChange={update('outputFile')} aria-label="输出文件" /></label>
        <div className="dag-node-editor-actions">
          <button type="button" onClick={() => { void onSave(form, activeNode); }} disabled={savingNodeKey === activeNode.nodeKey}>
            {savingNodeKey === activeNode.nodeKey ? '保存中...' : '保存步骤'}
          </button>
        </div>
      </div>
    </Panel>
  );
}

function DagScheduleModal({ cron, actionLabel, saving, onClose, onSave }) {
  const initialSchedule = useMemo(() => scheduleStateFromCron(cron), [cron]);
  const [preset, setPreset] = useState(initialSchedule.preset);
  const [time, setTime] = useState(initialSchedule.time);
  const [weekday, setWeekday] = useState(initialSchedule.weekday);
  const [monthDay, setMonthDay] = useState(initialSchedule.monthDay);
  const [inputError, setInputError] = useState(initialSchedule.warning || '');
  const monthDays = useMemo(() => Array.from({ length: 31 }, (_item, index) => (index + 1).toString()), []);
  const previewText = scheduleLabelFromState({ preset, time, weekday, monthDay });

  useEffect(() => {
    setPreset(initialSchedule.preset);
    setTime(initialSchedule.time);
    setWeekday(initialSchedule.weekday);
    setMonthDay(initialSchedule.monthDay);
    setInputError(initialSchedule.warning || '');
  }, [initialSchedule]);

  const choose = (setter) => (event) => {
    setter(event.target.value);
    setInputError('');
  };

  const confirm = () => {
    const { cronExpr, error } = cronExprFromSchedule(preset, time, weekday, monthDay);
    if (error) {
      setInputError(error);
      return;
    }
    void onSave(cronExpr);
  };

  return (
    <FocusTrapDialog ariaLabel={actionLabel} closeDisabled={saving} onClose={onClose}>
        <header><h2>{actionLabel}</h2><button type="button" className="ghost" onClick={onClose} disabled={saving}>关闭</button></header>
        <div className="dag-node-editor">
          <label>
            运行频率
            <select value={preset} onChange={choose(setPreset)} disabled={saving} aria-label="运行频率">
              <option value="daily">每天</option>
              <option value="weekdays">工作日</option>
              <option value="weekly">每周</option>
              <option value="monthly">每月</option>
            </select>
          </label>
          {preset === 'weekly' ? (
            <label>
              星期几
              <select value={weekday} onChange={choose(setWeekday)} disabled={saving} aria-label="星期几">
                {DAG_WEEKDAY_OPTIONS.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}
              </select>
            </label>
          ) : null}
          {preset === 'monthly' ? (
            <label>
              每月几号
              <select value={monthDay} onChange={choose(setMonthDay)} disabled={saving} aria-label="每月几号">
                {monthDays.map((day) => <option key={day} value={day}>{day} 日</option>)}
              </select>
            </label>
          ) : null}
          <label>
            运行时间
            <input value={time} type="time" onChange={choose(setTime)} disabled={saving} aria-label="运行时间" />
          </label>
        </div>
        {previewText ? <p className="settings-status">{previewText} 自动运行</p> : null}
        {inputError ? <p className="danger-text" role="alert">{inputError}</p> : null}
        <footer>
          <button type="button" className="ghost" onClick={onClose} disabled={saving}>取消</button>
          <button type="button" onClick={confirm} disabled={saving}>{saving ? '保存中...' : actionLabel}</button>
        </footer>
    </FocusTrapDialog>
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
      <span><Icon size={34} /></span>
      <h2>{title}</h2>
      <p>{text}</p>
    </div>
  );
}

export { WorkflowPage };
