import { isCancelledError, useQuery, useQueryClient } from '@tanstack/react-query';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { dashboardQueryKey, errorMessage, firstText, numberOrNull, objectValue, optionalSettingsCwd, queryHasSnapshot, textValue } from '../../shared/pageShared.js';
import { useWorkflowActions } from './useWorkflowActions.js';
import { getDagDetail, getDagRun, getDagRuns, listWorkflowTemplates } from '../services/workflowPageService.js';
import {
  categoryCounts,
  CONFIGURABLE_NODE_TYPES,
  DAG_CATEGORIES,
  dagCategoryOf,
  dagStatusLabel,
  dagVersionOf,
  fetchDagsDashboard,
  fetchWorkflowQuery,
  firstAvailableCategory,
  invalidateWorkflowQuery,
  isScheduledTrigger,
  normalizeDashboardDag,
  normalizeDagNode,
  normalizeDagRun,
  STARTABLE_DAG_STATUSES,
  STARTABLE_DAG_TRIGGERS,
  workflowDashboardQueryErrorState,
  workflowOverviewStats,
} from '../services/workflowDagModel.js';
import { finalOutputDescriptor, finalOutputPreviewText, rootAssigneeWarning, workflowDiagnosticNodes, workflowNodeDiagnostics } from '../services/workflowNodeModel.js';
import { enterpriseTemplateId, firstPresent, requireArrayField } from '../services/workflowEnterpriseTemplateModel.js';

const DAG_RECENT_RUN_LIMIT = 30;

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
  const selection = useWorkflowSelection(list.items);
  const detail = useWorkflowDetailQuery({ items: list.items, selectedDag: selection.selectedDag, selectedDagKey: selection.selectedDagKey, workflowCwd });
  const run = useWorkflowRunDetail({ activeRun: detail.activeRun, runs: detail.runs, workflowCwd });
  const notices = useWorkflowNotice(selection.selectedDagKey);
  const refresh = useWorkflowRefresh({ refreshDags, refreshKey, reportRefreshError: list.reportSyncFailure, selectedDagKeyRef: selection.selectedDagKeyRef, selectedRunKey: run.selectedRunKey, setSelectedRunKey: run.setSelectedRunKey, workflowCwd });
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
  const reportSyncFailure = useCallback((err) => {
    if (!workflowCwd || isCancelledError(err)) return;
    const key = dashboardQueryKey(workflowCwd, 'dags');
    setWorkflowSyncFailureState({
      dataUpdatedAt: queryClient.getQueryState(key)?.dataUpdatedAt || 0,
      message: '同步失败，显示的是上次成功的数据：' + errorMessage(err),
    });
  }, [queryClient, workflowCwd]);
  const refreshDags = useCallback(async () => {
    if (!workflowCwd) return [];
    const key = dashboardQueryKey(workflowCwd, 'dags');
    if (refreshPromiseRef.current?.workflowCwd === workflowCwd) return refreshPromiseRef.current.promise;
    const refreshPromise = (async () => {
      try {
        await queryClient.invalidateQueries(workflowQueryKeyFilter(key), workflowRefreshInvalidateOptions());
        setWorkflowSyncFailureState(workflowSyncClearState(queryClient.getQueryState(key)?.dataUpdatedAt || 0));
      } catch (err) {
        reportSyncFailure(err);
      }
      return requireArrayField(queryClient.getQueryData(key), 'workflow query dags cache');
    })();
    refreshPromiseRef.current = { promise: refreshPromise, workflowCwd };
    refreshPromise.finally(() => {
      if (refreshPromiseRef.current?.promise === refreshPromise) refreshPromiseRef.current = null;
    });
    return refreshPromise;
  }, [queryClient, reportSyncFailure, workflowCwd]);
  return { errorState, items, loading, refreshDags, reportSyncFailure, syncFailure: workflowSyncFailure };
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
    : firstText(visibleItems[0]?.dagKey);
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
  return requireArrayField(response?.nodes, 'workflow dag detail nodes').map((node, index) => normalizeDagNode(node, index));
}

function workflowRunsFromResponse(response) {
  return requireArrayField(response?.runs, 'workflow dag runs').map((run, index) => normalizeDagRun(run, index));
}

function workflowActiveRunFromResponse(response) {
  const runs = requireArrayField(response?.runs, 'workflow active runs');
  return runs.length > 0 ? normalizeDagRun(runs[0]) : null;
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
  const detailData = objectValue(dagDetailData);
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
  const fallbackRunKey = firstText(activeRun?.runKey, runs[0]?.runKey);
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

function workflowRefreshInvalidateOptions() {
  return { cancelRefetch: false, throwOnError: true };
}

function workflowQueryKeyFilter(queryKey) {
  return { queryKey };
}

function workflowSyncClearState(dataUpdatedAt) {
  return { dataUpdatedAt, message: '' };
}

function useWorkflowRefresh(options) {
  const { refreshDags, refreshKey, reportRefreshError, selectedDagKeyRef, selectedRunKey, setSelectedRunKey, workflowCwd } = options;
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
    void refreshWorkflowSurface().catch(reportRefreshError);
  }, [refreshWorkflowSurface, reportRefreshError, workflowRefreshKey]);
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
  const activeRunKey = firstText(detail.activeRun?.runKey);
  const dagKey = firstText(activeDetailDag?.dagKey, selection.selectedDag?.dagKey);
  const startDisabledReason = useMemo(() => workflowStartDisabledReason({ activeDetailDag, activeRunKey, dagKey, detail, list, missingRootAssigneeWarning }), [activeDetailDag, activeRunKey, dagKey, detail, list, missingRootAssigneeWarning]);
  return workflowDerivedSnapshot({ activeDetailDag, activeRunKey, dagKey, detail, list, missingRootAssigneeWarning, run, selection, startDisabledReason });
}

function workflowStartDisabledReason(options) {
  const { activeDetailDag, activeRunKey, dagKey, detail, list, missingRootAssigneeWarning } = options;
  if (!dagKey) return '未选择自动化';
  if (list.loading || detail.detailLoading) return '自动化详情加载中';
  if (activeRunKey) return '已有运行正在进行';
  if (!STARTABLE_DAG_STATUSES.has(textValue(activeDetailDag?.status).toLowerCase())) return '当前流程状态不可运行';
  if (!STARTABLE_DAG_TRIGGERS.has(textValue(activeDetailDag?.trigger).toLowerCase())) return '当前触发方式不可运行';
  return textValue(missingRootAssigneeWarning);
}

function workflowDerivedSnapshot(options) {
  const { activeDetailDag, activeRunKey, dagKey, detail, list, missingRootAssigneeWarning, run, selection, startDisabledReason } = options;
  const messages = workflowLoadMessages(list.errorState, list.syncFailure, detail.detailErrorState);
  const baseVersion = dagVersionOf(activeDetailDag);
  const finalOutput = finalOutputDescriptor(run.selectedRun?.run) || finalOutputDescriptor(detail.activeRun) || finalOutputDescriptor(selection.selectedDag?.latestRun);
  const finalText = finalOutputPreviewText(finalOutput);
  const recentRunPanelLabel = dagStatusLabel(firstText(detail.activeRun?.status, detail.runs[0]?.status, activeDetailDag?.latestRun?.status));
  const diagnosticNodes = workflowDiagnosticNodes(detail, run);
  const diagnostics = workflowNodeDiagnostics(diagnosticNodes);
  const runId = numberOrNull(firstPresent(run.selectedRun?.run?.runId, run.selectedRun?.run?.raw?.id, detail.activeRun?.runId, detail.activeRun?.raw?.id));
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

export { useWorkflowPageController };
