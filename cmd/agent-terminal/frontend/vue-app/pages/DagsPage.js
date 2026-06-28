import { computed, ref, watch } from '../../lib/vue.esm-browser.prod.js';
import { DagFinalOutputPanel } from '../components/dag/DagFinalOutputPanel.js';
import { DagNodeEditForm } from '../components/dag/DagNodeEditForm.js';
import { DagNodeList } from '../components/dag/DagNodeList.js';
import { DagRunHistoryPanel } from '../components/dag/DagRunHistoryPanel.js';
import { DagScheduleModal } from '../components/dag/DagScheduleModal.js';
import { DagSharedFilesPanel } from '../components/dag/DagSharedFilesPanel.js';
import { DagTopologyPanel } from '../components/dag/DagTopologyPanel.js';
import { useDagDetail } from '../composables/useDagDetail.js';
import { useDagStatusEventBridge } from '../composables/useDagStatusEventBridge.js';
import {
  isScheduledTrigger,
  scheduleLabelFromDagItem,
  scheduledTaskStatusLabel,
  useDagScheduleAction,
} from '../composables/useDagScheduleAction.js';

function textValue(...values) {
  for (const value of values) {
    if (value === null || value === undefined) continue;
    const text = value.toString().trim();
    if (text) return text;
  }
  return '-';
}

function normalizedValue(...values) {
  const text = textValue(...values);
  return text === '-' ? '' : text.toLowerCase();
}

function finalOutputPresent(value) {
  if (value === true) return true;
  if (!value) return false;
  if (typeof value === 'string') return value.trim() !== '';
  if (typeof value === 'object') return Object.keys(value).length > 0;
  return false;
}

function userErrorText(error, fallback) {
  return error ? fallback : '';
}

const DAG_STATUS_LABELS = {
  draft: '草稿',
  ready: '可运行',
  running: '运行中',
  succeeded: '成功',
  done: '成功',
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

const RUN_STATUS_LABELS = {
  ...DAG_STATUS_LABELS,
  ready: '等待运行',
};

const TRIGGER_LABELS = {
  manual: '手动',
  scheduled: '定时',
  schedule: '定时',
  cron: '定时',
};

function statusLabel(value, labels = DAG_STATUS_LABELS) {
  const normalized = normalizedValue(value);
  if (!normalized) return '-';
  return labels[normalized] || textValue(value);
}

function triggerTypeLabel(value) {
  const normalized = normalizedValue(value);
  if (!normalized) return '-';
  return TRIGGER_LABELS[normalized] || textValue(value);
}

function triggerLabel(item) {
  const trigger = item.trigger || item.trigger_config || item.triggerConfig;
  const readableSchedule = scheduleLabelFromDagItem(item);
  const triggerType = typeof trigger === 'string'
    ? textValue(trigger)
    : textValue(
      trigger?.type,
      trigger?.kind,
      item.trigger_type,
      item.triggerType,
    );
  if (triggerType === '-' && !readableSchedule) return '-';
  if (triggerType === '-') return readableSchedule;
  const label = triggerTypeLabel(triggerType);
  if (isScheduledTrigger(normalizedValue(triggerType)) && readableSchedule) return readableSchedule;
  return label;
}

function latestRunLabel(item) {
  const run = item.latest_run || item.latestRun || item.run;
  if (typeof run === 'string') {
    const mapped = statusLabel(run, RUN_STATUS_LABELS);
    return mapped === textValue(run) ? '有运行记录' : mapped;
  }
  const runKey = textValue(
    run?.run_key,
    run?.runKey,
    item.latest_run_key,
    item.latestRunKey,
  );
  const status = textValue(
    run?.status,
    run?.state,
    item.latest_run_status,
    item.latestRunStatus,
  );
  if (runKey === '-' && status === '-') return '-';
  if (status !== '-') return statusLabel(status, RUN_STATUS_LABELS);
  return '有运行记录';
}

function latestRunLabelFromRun(run) {
  return latestRunLabel({ latest_run: run });
}

function displayLatestRunLabel(latestLabel, dagStatus, trigger) {
  if (latestLabel !== '-') return latestLabel;
  if (isScheduledTrigger(trigger)) return '未运行';
  return dagStatus === 'draft' || dagStatus === 'ready' ? '未启动' : '-';
}

function hasFinalOutput(item) {
  const run = item.latest_run || item.latestRun || {};
  const metadata = item.metadata || {};
  return finalOutputPresent(item.has_final_output)
    || finalOutputPresent(item.hasFinalOutput)
    || finalOutputPresent(item.final_output)
    || finalOutputPresent(item.finalOutput)
    || finalOutputPresent(metadata.final_output)
    || finalOutputPresent(metadata.finalOutput)
    || finalOutputPresent(run.final_output)
    || finalOutputPresent(run.finalOutput)
    || finalOutputPresent(run.metadata?.final_output)
    || finalOutputPresent(run.metadata?.finalOutput);
}

const STARTABLE_DAG_STATUSES = new Set(['draft', 'ready']);
const STARTABLE_DAG_TRIGGERS = new Set(['manual', 'scheduled', 'schedule', 'cron']);
const ACTIVE_RUN_STATUSES = new Set(['running']);
const CATEGORY_ACTIVE_RUN_STATUSES = new Set(['running', 'starting', 'queued', 'awaiting_verify']);
const TERMINAL_DAG_STATUSES = new Set(['done', 'failed', 'cancelled', 'canceled', 'skipped']);
const DAG_CATEGORY_DEFS = [
  { key: 'running', label: '进行中' },
  { key: 'scheduled', label: '定时任务' },
  { key: 'history', label: '历史记录' },
];

function displayStatusLabel(status, trigger, item) {
  return scheduledTaskStatusLabel(status, trigger, item) || statusLabel(status);
}

function dagKeyFromItem(item) {
  const key = textValue(item?.dag_key, item?.dagKey, item?.key, item?.id);
  return key === '-' ? '' : key;
}

function dagCandidates(row, detailState, dagKey) {
  const items = [];
  const detailDag = detailState?.dag;
  if (detailDag && (!dagKey || dagKeyFromItem(detailDag) === dagKey)) {
    items.push(detailDag);
  }
  if (row?.raw) items.push(row.raw);
  return items;
}

function dagStatusOf(items) {
  for (const item of items) {
    const status = normalizedValue(item?.status, item?.state);
    if (status) return status;
  }
  return '';
}

function dagTriggerOf(items) {
  for (const item of items) {
    const trigger = item?.trigger || item?.trigger_config || item?.triggerConfig;
    const value = typeof trigger === 'string'
      ? normalizedValue(trigger)
      : normalizedValue(trigger?.type, trigger?.kind, item?.trigger_type, item?.triggerType);
    if (value) return value;
  }
  return '';
}

function runStatusOf(run) {
  return normalizedValue(run?.status, run?.state);
}

function runKeyOf(run) {
  const key = textValue(run?.run_key, run?.runKey, run?.key, run?.id);
  return key === '-' ? '' : key;
}

function detailRunsAreAuthoritative(detailState, dagKey) {
  const detailKey = dagKeyFromItem(detailState?.dag);
  const hasLoadedRuns = Array.isArray(detailState?.runs) && detailState.runs.length > 0;
  const hasRunEvidence = Boolean(
    detailState?.activeRun
      || detailState?.run
      || hasLoadedRuns,
  );
  return Boolean(
    detailKey
      && (!dagKey || detailKey === dagKey)
      && !detailState?.loading
      && !detailState?.error
      && !detailState?.runsError
      && hasRunEvidence,
  );
}

function collectDetailRuns(detailState) {
  const out = [];
  const seen = new Set();
  for (const run of [detailState?.run, detailState?.activeRun, ...(Array.isArray(detailState?.runs) ? detailState.runs : [])]) {
    const key = runKeyOf(run);
    if (key && seen.has(key)) continue;
    if (key) seen.add(key);
    out.push(run);
  }
  return out;
}

function collectListRuns(items) {
  const runs = [];
  for (const item of items) {
    runs.push(item?.latest_run || item?.latestRun || item?.run);
    runs.push({ status: item?.latest_run_status || item?.latestRunStatus });
  }
  return runs;
}

function detailLatestRunLabel(detailState, dagKey) {
  if (!detailRunsAreAuthoritative(detailState, dagKey)) return '';
  const runs = collectDetailRuns(detailState).filter(Boolean);
  if (runs.length) return latestRunLabelFromRun(runs[0]);
  return '-';
}

function hasActiveRun(items, detailState, dagKey) {
  const runs = detailRunsAreAuthoritative(detailState, dagKey)
    ? collectDetailRuns(detailState)
    : collectListRuns(items).concat(collectDetailRuns(detailState));
  return runs.some((run) => ACTIVE_RUN_STATUSES.has(runStatusOf(run)));
}

function terminableActiveRun(items, detailState, dagKey) {
  const runs = detailRunsAreAuthoritative(detailState, dagKey)
    ? collectDetailRuns(detailState)
    : collectListRuns(items).concat(collectDetailRuns(detailState));
  return runs.find((run) => ACTIVE_RUN_STATUSES.has(runStatusOf(run)) && Boolean(runKeyOf(run))) || null;
}

function dagTitle(item, index, key) {
  const title = textValue(item.title, item.name);
  return title === '-' ? `任务流程 ${index + 1}` : title;
}

function normalizeDag(item, index) {
  const key = textValue(item.dag_key, item.dagKey, item.key, item.id);
  const status = textValue(item.status, item.state);
  const normalizedStatus = normalizedValue(status);
  const trigger = dagTriggerOf([item]);
  const latestLabel = latestRunLabel(item);
  return {
    key,
    title: dagTitle(item, index, key),
    status: displayStatusLabel(status, trigger, item),
    triggerLabel: triggerLabel(item),
    scheduleLabel: scheduleLabelFromDagItem(item),
    latestRunLabel: displayLatestRunLabel(latestLabel, normalizedStatus, trigger),
    hasFinalOutput: hasFinalOutput(item),
    raw: item,
    listKey: key === '-' ? `dag-${index}` : key,
  };
}

function rowHasActiveRun(row) {
  return CATEGORY_ACTIVE_RUN_STATUSES.has(dagStatusOf([row?.raw]))
    || collectListRuns([row?.raw]).some((run) => CATEGORY_ACTIVE_RUN_STATUSES.has(runStatusOf(run)));
}

function rowIsScheduled(row) {
  const trigger = dagTriggerOf([row?.raw]);
  return trigger === 'scheduled' || trigger === 'schedule' || trigger === 'cron';
}

function rowMatchesCategory(row, category) {
  if (category === 'running') return rowHasActiveRun(row);
  if (category === 'scheduled') return rowIsScheduled(row);
  if (category === 'history') return !rowHasActiveRun(row) && !rowIsScheduled(row);
  return false;
}

function firstAvailableCategory(tabs) {
  return tabs.find((tab) => tab.count > 0)?.key || '';
}

export const DagsPage = {
  name: 'DagsPage',
  components: {
    DagFinalOutputPanel,
    DagNodeEditForm,
    DagNodeList,
    DagRunHistoryPanel,
    DagScheduleModal,
    DagSharedFilesPanel,
    DagTopologyPanel,
  },
  props: {
    items: { type: Array, default: () => [] },
    emptyText: { type: String, default: '暂无任务流程' },
    loading: { type: Boolean, default: false },
    error: { type: String, default: '' },
    statusEvents: { type: Array, default: () => [] },
  },
  emits: ['open-chat', 'design-flow', 'refresh-dags'],
  setup(props, ctx) {
    const emit = ctx?.emit || (() => {});
    const dagDetail = useDagDetail();
    const detailState = dagDetail.state;
    const selectedKey = ref('');
    const deleteSuccessText = ref('');
    const deleteConfirmTarget = ref(null);
    const rows = computed(() => props.items.map((item, index) => {
      const row = normalizeDag(item, index);
      const dagKey = row.key === '-' ? '' : row.key;
      const detailLabel = detailLatestRunLabel(detailState, dagKey);
      return detailLabel ? { ...row, latestRunLabel: detailLabel } : row;
    }));
    const activeCategory = ref('');
    const categoryManuallySelected = ref(false);
    const categoryTabs = computed(() => DAG_CATEGORY_DEFS.map((def) => ({
      ...def,
      count: rows.value.filter((row) => rowMatchesCategory(row, def.key)).length,
    })));
    const visibleRows = computed(() => (
      activeCategory.value
        ? rows.value.filter((row) => rowMatchesCategory(row, activeCategory.value))
        : []
    ));
    const selectedRow = computed(() => visibleRows.value.find((row) => row.listKey === selectedKey.value) || visibleRows.value[0] || null);
    const selectedDagKey = computed(() => selectedRow.value?.key === '-' ? '' : selectedRow.value?.key || '');
    const selectedDagItems = computed(() => dagCandidates(selectedRow.value, detailState, selectedDagKey.value));
    const selectedFinalOutput = computed(() => detailState.finalOutput);
    const pageErrorText = computed(() => userErrorText(props.error, '加载任务流程失败，请稍后重试。'));
    const designNodes = computed(() => (
      Array.isArray(detailState.templateNodes) && detailState.templateNodes.length
        ? detailState.templateNodes
        : detailState.nodes
    ));
    const startDisabledReason = computed(() => {
      if (!selectedRow.value || !selectedDagKey.value) return '未选择任务流程';
      if (props.loading || detailState.loading) return '任务流程详情加载中';
      if (detailState.error) return '任务流程详情不可用';
      if (detailState.runsError) return '运行历史不可用，无法确认是否正在运行';
      if (detailState.starting) return '启动中';
      if (hasActiveRun(selectedDagItems.value, detailState, selectedDagKey.value)) return '已有运行正在进行';
      const status = dagStatusOf(selectedDagItems.value);
      if (!STARTABLE_DAG_STATUSES.has(status)) return '当前流程状态不可运行';
      const trigger = dagTriggerOf(selectedDagItems.value);
      if (!STARTABLE_DAG_TRIGGERS.has(trigger)) return '当前触发方式不可运行';
      return '';
    });
    const selectedTerminableRun = computed(() => terminableActiveRun(selectedDagItems.value, detailState, selectedDagKey.value));
    const stopActionVisible = computed(() => Boolean(selectedTerminableRun.value));
    const stopDisabledReason = computed(() => {
      if (!selectedRow.value || !selectedDagKey.value) return '未选择任务流程';
      if (props.loading || detailState.loading) return '任务流程详情加载中';
      if (detailState.error) return '任务流程详情不可用';
      if (detailState.runsError) return '运行历史不可用，无法确认是否正在运行';
      if (detailState.terminating) return '停止中';
      if (!stopActionVisible.value) return '暂无运行中任务';
      return '';
    });
    const editDisabledReason = computed(() => {
      if (!selectedRow.value || !selectedDagKey.value) return '未选择任务流程';
      if (props.loading || detailState.loading) return '任务流程详情加载中';
      if (detailState.error) return '任务流程详情不可用，不能编辑步骤';
      if (detailState.runsError) return '运行历史不可用，不能编辑步骤';
      if (hasActiveRun(selectedDagItems.value, detailState, selectedDagKey.value)) return '已有运行正在进行，不能编辑步骤';
      const status = dagStatusOf(selectedDagItems.value);
      if (status === 'running') return '当前任务流程正在运行，不能编辑步骤';
      if (TERMINAL_DAG_STATUSES.has(status)) return '任务流程已结束，不能编辑步骤';
      return '';
    });
    const detailErrorText = computed(() => userErrorText(detailState.error, '任务流程详情不可用，请稍后重试。'));
    const startErrorText = computed(() => userErrorText(detailState.startError, '运行任务流程失败，请稍后重试。'));
    const terminateErrorText = computed(() => userErrorText(detailState.terminateError, '停止任务流程失败，请稍后重试。'));
    const startWarningText = computed(() => {
      if (detailState.startExecutionState === 'waiting_for_assignee') return '任务流程已启动，等待指派执行代理。';
      if (detailState.startExecutionState === 'no_ready_roots') return '任务流程已启动，但暂时没有可运行步骤。';
      return detailState.startWarning ? '任务流程已启动，等待下一步调度。' : '';
    });
    const terminateWarningText = computed(() => (detailState.terminateWarning ? '任务流程已停止，但部分子代理停止失败。' : ''));
    const deleteErrorText = computed(() => userErrorText(detailState.deleteError, '删除任务流程失败，请稍后重试。'));
    const runsErrorText = computed(() => userErrorText(detailState.runsError, '无法加载运行历史，请稍后重试。'));
    const deleteDisabledReason = computed(() => {
      if (!selectedRow.value || !selectedDagKey.value) return '未选择任务流程';
      if (props.loading || detailState.loading) return '任务流程详情加载中';
      if (detailState.error) return '任务流程详情不可用，不能删除';
      if (detailState.deleting) return '删除中';
      if (hasActiveRun(selectedDagItems.value, detailState, selectedDagKey.value)) return '已有运行正在进行，不能删除';
      return '';
    });
    const scheduleAction = useDagScheduleAction({
      props,
      detailState,
      selectedRow,
      selectedDagKey,
      selectedDagItems,
      dagDetail,
      emit,
      startableStatuses: STARTABLE_DAG_STATUSES,
      dagStatusOf,
      dagTriggerOf,
      hasActiveRun,
    });

    watch(
      () => categoryTabs.value.map((tab) => `${tab.key}:${tab.count}`).join('|'),
      () => {
        if (!rows.value.length) {
          activeCategory.value = '';
          categoryManuallySelected.value = false;
          return;
        }
        const activeTab = categoryTabs.value.find((tab) => tab.key === activeCategory.value);
        if (!activeTab || (!categoryManuallySelected.value && activeTab.count === 0)) {
          activeCategory.value = firstAvailableCategory(categoryTabs.value);
          categoryManuallySelected.value = false;
        }
      },
      { immediate: true, flush: 'sync' },
    );

    watch(
      () => visibleRows.value.map((row) => row.listKey).join('\n'),
      () => {
        if (!visibleRows.value.length) {
          selectedKey.value = '';
          return;
        }
        if (!visibleRows.value.some((row) => row.listKey === selectedKey.value)) {
          selectedKey.value = visibleRows.value[0].listKey;
        }
      },
      { immediate: true, flush: 'sync' },
    );

    function setCategory(category) {
      const tab = categoryTabs.value.find((item) => item.key === category);
      if (!tab) return;
      deleteSuccessText.value = '';
      deleteConfirmTarget.value = null;
      scheduleAction.cancelScheduleDAG();
      activeCategory.value = tab.key;
      categoryManuallySelected.value = true;
    }

    function selectDag(row) {
      if (!row) return;
      deleteSuccessText.value = '';
      deleteConfirmTarget.value = null;
      scheduleAction.cancelScheduleDAG();
      selectedKey.value = row.listKey;
    }

    watch(
      () => selectedDagKey.value,
      (dagKey) => {
        const row = selectedRow.value;
        if (dagKey && row?.raw) dagDetail.open(row.raw);
      },
      { immediate: true, flush: 'sync' },
    );
    useDagStatusEventBridge(props, dagDetail);

    async function startSelectedDag() {
      if (startDisabledReason.value) return;
      await dagDetail.start();
      if (!detailState.startError) emit('refresh-dags');
    }

    async function stopSelectedDag() {
      if (stopDisabledReason.value) return;
      await dagDetail.terminateActiveRun(selectedTerminableRun.value);
      if (!detailState.terminateError) emit('refresh-dags');
    }

    function deleteSelectedDag() {
      deleteSuccessText.value = '';
      if (deleteDisabledReason.value) return;
      deleteConfirmTarget.value = {
        dagKey: selectedDagKey.value,
        title: selectedRow.value.title,
      };
    }

    function cancelDeleteDAG() {
      if (detailState.deleting) return;
      deleteConfirmTarget.value = null;
    }

    async function confirmDeleteDAG() {
      const target = deleteConfirmTarget.value;
      if (!target || detailState.deleting) return;
      const result = await dagDetail.deleteDAG();
      if (result?.ok && !detailState.deleteError) {
        deleteSuccessText.value = `已删除「${target.title}」`;
        deleteConfirmTarget.value = null;
        emit('refresh-dags');
      }
    }

    function selectRun(runKey) {
      dagDetail.selectRun(runKey);
    }

    function openChat(threadId) {
      emit('open-chat', threadId);
    }

    function startDesignFlow() {
      emit('design-flow', {
        dagKey: selectedDagKey.value,
        title: selectedRow.value?.title || '',
      });
    }

    async function saveAgentNode(payload) {
      if (editDisabledReason.value) return;
      await dagDetail.saveAgentNode(payload);
    }

    return {
      activeCategory,
      ...scheduleAction,
      categoryTabs,
      dagDetail,
      cancelDeleteDAG,
      confirmDeleteDAG,
      deleteConfirmTarget,
      deleteDisabledReason,
      deleteErrorText,
      deleteSelectedDag,
      deleteSuccessText,
      detailErrorText,
      detailState,
      designNodes,
      editDisabledReason,
      openChat,
      pageErrorText,
      rows,
      runsErrorText,
      saveAgentNode,
      selectedRow,
      selectedFinalOutput,
      selectDag,
      selectRun,
      setCategory,
      stopActionVisible,
      stopDisabledReason,
      stopSelectedDag,
      startDisabledReason,
      startDesignFlow,
      startErrorText,
      startSelectedDag,
      startWarningText,
      terminateErrorText,
      terminateWarningText,
      visibleRows,
    };
  },
  template: `
    <section id="page-dags" class="page active dag-console-page" data-testid="dag-console">
      <div class="panel-header" data-testid="dag-console-header">
        <div class="ph-bar"></div>
        <div class="ph-text"><h2>任务流程</h2></div>
        <button
          type="button"
          class="btn"
          data-testid="dag-design-flow-button"
          @click="startDesignFlow"
        >AI 设计流程</button>
      </div>
      <div class="dag-console-shell">
        <aside class="dag-console-list-pane" data-testid="dag-console-list">
          <div v-if="loading" class="empty-state dag-console-empty" data-testid="dag-console-loading">
            <div class="es-icon">D</div>
            <h3>正在加载任务流程</h3>
          </div>
          <div v-else-if="pageErrorText" class="empty-state dag-console-empty dag-console-error" data-testid="dag-console-error">
            <div class="es-icon">D</div>
            <h3>加载任务流程失败</h3>
            <p>{{ pageErrorText }}</p>
          </div>
          <div v-else-if="rows.length === 0" class="empty-state dag-console-empty" data-testid="dag-console-empty">
            <div class="es-icon">D</div>
            <h3>{{ emptyText }}</h3>
          </div>
          <div v-else class="dag-console-list-wrap">
            <div class="dag-category-tabs" data-testid="dag-category-tabs" role="tablist" aria-label="任务流程分类">
              <button
                v-for="tab in categoryTabs"
                :key="tab.key"
                type="button"
                role="tab"
                class="dag-category-tab"
                :class="{ active: activeCategory === tab.key }"
                :aria-selected="activeCategory === tab.key ? 'true' : 'false'"
                :data-testid="'dag-category-tab-' + tab.key"
                @click="setCategory(tab.key)"
              >
                <span>{{ tab.label }}</span>
                <span class="dag-category-count">{{ tab.count }}</span>
              </button>
            </div>
            <div v-if="visibleRows.length === 0" class="dag-console-muted dag-console-category-empty" data-testid="dag-category-empty">当前分类暂无任务流程</div>
            <div v-else class="dag-console-list">
              <button
                v-for="(row, idx) in visibleRows"
                :key="row.listKey"
                type="button"
                class="dag-console-row"
                :class="{ active: selectedRow && selectedRow.listKey === row.listKey }"
                :data-testid="'dag-console-row-' + idx"
                @click="selectDag(row)"
              >
                <span class="dag-console-row-main">
                  <span class="dag-console-title">{{ row.title }}</span>
                  <span class="dag-console-key">{{ row.latestRunLabel === '-' ? '暂无运行' : '最近运行：' + row.latestRunLabel }}</span>
                </span>
                <span class="dag-console-row-meta">
                  <span class="dag-console-status">{{ row.status }}</span>
                  <span class="dag-console-trigger">{{ row.triggerLabel }}</span>
                  <span class="dag-console-run">{{ row.latestRunLabel }}</span>
                  <span v-if="row.hasFinalOutput" class="dag-console-final" data-testid="dag-console-final-marker">最终结果</span>
                </span>
              </button>
            </div>
          </div>
        </aside>

        <section class="dag-console-detail-pane" data-testid="dag-console-detail">
          <div v-if="loading" class="empty-state dag-console-empty" data-testid="dag-console-detail-loading">
            <div class="es-icon">D</div>
            <h3>正在加载任务流程</h3>
          </div>
          <div v-else-if="pageErrorText" class="empty-state dag-console-empty dag-console-error" data-testid="dag-console-detail-error">
            <div class="es-icon">D</div>
            <h3>加载任务流程失败</h3>
            <p>{{ pageErrorText }}</p>
          </div>
          <div v-else-if="selectedRow" class="dag-console-detail-grid">
            <div class="dag-console-detail-heading">
              <div>
                <h3>{{ selectedRow.title }}</h3>
                <span>{{ selectedRow.latestRunLabel === '-' ? '暂无运行' : '最近运行：' + selectedRow.latestRunLabel }}</span>
              </div>
              <div class="dag-console-actions">
                <button
                  type="button"
                  class="btn dag-delete-button"
                  data-testid="dag-delete-button"
                  :disabled="Boolean(deleteDisabledReason)"
                  :title="deleteDisabledReason"
                  @click="deleteSelectedDag"
                >{{ detailState.deleting ? '删除中' : '删除' }}</button>
                <button
                  v-if="scheduleToggleVisible"
                  type="button"
                  class="btn"
                  data-testid="dag-schedule-toggle-button"
                  :disabled="Boolean(scheduleToggleDisabledReason)"
                  :title="scheduleToggleDisabledReason"
                  @click="toggleScheduleEnabled"
                >{{ detailState.scheduling ? '保存中' : scheduleToggleLabel }}</button>
                <button
                  v-if="scheduleActionVisible"
                  type="button"
                  class="btn"
                  data-testid="dag-schedule-button"
                  :disabled="Boolean(scheduleDisabledReason)"
                  :title="scheduleDisabledReason"
                  @click="openScheduleModal"
                >{{ detailState.scheduling ? '保存中' : scheduleActionLabel }}</button>
                <button
                  v-if="stopActionVisible"
                  type="button"
                  class="btn dag-stop-button"
                  data-testid="dag-stop-button"
                  :disabled="Boolean(stopDisabledReason)"
                  :title="stopDisabledReason"
                  @click="stopSelectedDag"
                >{{ detailState.terminating ? '停止中' : '停止' }}</button>
                <button
                  type="button"
                  class="btn btn-primary"
                  data-testid="dag-start-button"
                  :disabled="Boolean(startDisabledReason)"
                  :title="startDisabledReason"
                  @click="startSelectedDag"
                >{{ detailState.starting ? '启动中' : '运行' }}</button>
              </div>
            </div>
            <div v-if="startDisabledReason" class="dag-console-muted" data-testid="dag-start-disabled-reason">{{ startDisabledReason }}</div>
            <div v-if="deleteDisabledReason" class="dag-console-muted" data-testid="dag-delete-disabled-reason">{{ deleteDisabledReason }}</div>
            <div v-if="startErrorText" class="dag-console-error-inline" data-testid="dag-start-error">{{ startErrorText }}</div>
            <div v-if="startWarningText" class="dag-console-warning-inline" data-testid="dag-start-warning">{{ startWarningText }}</div>
            <div v-if="terminateErrorText" class="dag-console-error-inline" data-testid="dag-terminate-error">{{ terminateErrorText }}</div>
            <div v-if="terminateWarningText" class="dag-console-warning-inline" data-testid="dag-terminate-warning">{{ terminateWarningText }}</div>
            <div v-if="deleteErrorText" class="dag-console-error-inline" data-testid="dag-delete-error">{{ deleteErrorText }}</div>
            <div v-if="deleteSuccessText" class="dag-console-success-inline" data-testid="dag-delete-success">{{ deleteSuccessText }}</div>
            <div v-if="scheduleErrorText" class="dag-console-error-inline" data-testid="dag-schedule-error">{{ scheduleErrorText }}</div>
            <DagFinalOutputPanel
              v-if="runsErrorText"
              data-testid="dag-runs-error"
              :final-output="null"
              :runs-error="detailState.runsError"
            />
            <DagFinalOutputPanel
              v-else
              :final-output="selectedFinalOutput"
              :runs-error="null"
            />
	            <dl class="dag-console-facts">
	              <div>
	                <dt>任务状态</dt>
	                <dd>{{ selectedRow.status }}</dd>
	              </div>
	              <div>
	                <dt>运行计划</dt>
	                <dd>{{ selectedRow.triggerLabel }}</dd>
	              </div>
              <div>
                <dt>最近运行</dt>
                <dd>{{ selectedRow.latestRunLabel }}</dd>
              </div>
              <div>
                <dt>最终结果</dt>
                <dd>{{ selectedRow.hasFinalOutput ? '已记录' : '-' }}</dd>
              </div>
            </dl>
            <div v-if="detailState.loading" class="dag-console-muted" data-testid="dag-detail-loading-inline">正在加载详情</div>
            <div v-if="detailErrorText" class="dag-console-error-inline" data-testid="dag-detail-load-error">
              {{ detailErrorText }}
            </div>
            <DagRunHistoryPanel
              :runs="detailState.runs"
              :selected-run-key="detailState.selectedRunKey"
              @select-run="selectRun"
            />
	            <details class="dag-steps-section">
	              <summary>执行步骤</summary>
	              <DagNodeList :nodes="detailState.nodes" @open-chat="openChat" />
	            </details>
	            <details class="dag-advanced-section">
              <summary>高级设置</summary>
              <DagTopologyPanel :nodes="designNodes" />
              <DagNodeEditForm
                :nodes="designNodes"
                :saving-node-key="detailState.savingNodeKey"
                :save-error="detailState.saveError"
                :disabled-reason="editDisabledReason"
                @save-agent-node="saveAgentNode"
              />
              <DagSharedFilesPanel :nodes="designNodes" />
            </details>
          </div>
          <div v-else class="empty-state dag-console-empty" data-testid="dag-console-detail-empty">
            <div class="es-icon">D</div>
            <h3>{{ emptyText }}</h3>
          </div>
        </section>
      </div>
      <DagScheduleModal
        :open="scheduleConfirmOpen"
        :title="selectedRow ? selectedRow.title : ''"
        :action-label="scheduleActionLabel"
        :preset="schedulePreset"
        :time="scheduleTime"
        :weekday="scheduleWeekday"
        :month-day="scheduleMonthDay"
        :preview-text="schedulePreviewText"
        :input-error="scheduleInputError"
        :schedule-error-text="scheduleErrorText"
        :saving="detailState.scheduling"
        @update-preset="updateSchedulePreset"
        @update-time="updateScheduleTime"
        @update-weekday="updateScheduleWeekday"
        @update-month-day="updateScheduleMonthDay"
        @cancel="cancelScheduleDAG"
        @confirm="confirmScheduleSelectedDag"
      />
      <div v-if="deleteConfirmTarget" class="modal-overlay dag-delete-overlay" data-testid="dag-delete-overlay" @click.self="cancelDeleteDAG">
        <div class="modal-box dag-delete-modal" role="dialog" aria-modal="true" data-testid="dag-delete-modal">
          <div class="dag-delete-modal-head">
            <div>
              <div class="dag-delete-modal-title">删除任务流程</div>
              <div class="dag-delete-modal-tip">{{ deleteConfirmTarget.title }}</div>
            </div>
            <button
              type="button"
              class="btn btn-ghost"
              data-testid="dag-delete-close"
              :disabled="detailState.deleting"
              @click="cancelDeleteDAG"
            >关闭</button>
          </div>
          <div class="dag-delete-modal-body">
            删除后会移除该流程及历史运行记录。该操作不可撤销。
          </div>
          <div class="dag-delete-modal-actions">
            <button
              type="button"
              class="btn btn-ghost"
              data-testid="dag-delete-cancel"
              :disabled="detailState.deleting"
              @click="cancelDeleteDAG"
            >取消</button>
            <button
              type="button"
              class="btn dag-delete-button"
              data-testid="dag-delete-confirm"
              :disabled="detailState.deleting"
              @click="confirmDeleteDAG"
            >{{ detailState.deleting ? '删除中' : '确认删除' }}</button>
          </div>
        </div>
      </div>
    </section>
  `,
};
