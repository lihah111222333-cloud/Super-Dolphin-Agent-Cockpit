import { computed, ref, watch } from '../../lib/vue.esm-browser.prod.js';
import { AutoContinuePrefCard } from '../components/AutoContinuePrefCard.js';
import { DagFinalOutputPanel } from '../components/dag/DagFinalOutputPanel.js';
import { DagNodeEditForm } from '../components/dag/DagNodeEditForm.js';
import { DagNodeList } from '../components/dag/DagNodeList.js';
import { DagRunHistoryPanel } from '../components/dag/DagRunHistoryPanel.js';
import { DagSharedFilesPanel } from '../components/dag/DagSharedFilesPanel.js';
import { DagTopologyPanel } from '../components/dag/DagTopologyPanel.js';
import { useDagDetail } from '../composables/useDagDetail.js';

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

function triggerLabel(item) {
  const trigger = item.trigger || item.trigger_config || item.triggerConfig;
  const schedule = textValue(
    trigger?.schedule,
    trigger?.cron,
    trigger?.expression,
    item.schedule,
    item.cron,
    item.cron_expr,
    item.cronExpr,
  );
  const triggerType = typeof trigger === 'string'
    ? textValue(trigger)
    : textValue(
      trigger?.type,
      trigger?.kind,
      item.trigger_type,
      item.triggerType,
    );
  if (triggerType === '-' && schedule === '-') return '-';
  if (triggerType === '-') return schedule;
  if (schedule === '-') return triggerType;
  return `${triggerType} ${schedule}`;
}

function latestRunLabel(item) {
  const run = item.latest_run || item.latestRun || item.run;
  if (typeof run === 'string') return textValue(run);
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
  if (runKey === '-') return status;
  if (status === '-') return runKey;
  return `${runKey} · ${status}`;
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
const STARTABLE_DAG_TRIGGERS = new Set(['manual', 'scheduled']);
const ACTIVE_RUN_STATUSES = new Set(['pending', 'ready', 'queued', 'starting', 'running', 'awaiting_verify']);
const TERMINAL_DAG_STATUSES = new Set(['done', 'failed', 'cancelled', 'canceled', 'skipped']);

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

function hasActiveRun(items, detailState) {
  const runs = [];
  for (const item of items) {
    runs.push(item?.latest_run || item?.latestRun || item?.run);
    runs.push({ status: item?.latest_run_status || item?.latestRunStatus });
  }
  runs.push(detailState?.run);
  runs.push(detailState?.activeRun);
  if (Array.isArray(detailState?.runs)) runs.push(...detailState.runs);
  return runs.some((run) => ACTIVE_RUN_STATUSES.has(runStatusOf(run)));
}

function normalizeDag(item, index) {
  const key = textValue(item.dag_key, item.dagKey, item.key, item.id);
  return {
    key,
    title: textValue(item.title, item.name, key),
    status: textValue(item.status, item.state),
    triggerLabel: triggerLabel(item),
    latestRunLabel: latestRunLabel(item),
    hasFinalOutput: hasFinalOutput(item),
    raw: item,
    listKey: key === '-' ? `dag-${index}` : key,
  };
}

export const DagsPage = {
  name: 'DagsPage',
  components: {
    AutoContinuePrefCard,
    DagFinalOutputPanel,
    DagNodeEditForm,
    DagNodeList,
    DagRunHistoryPanel,
    DagSharedFilesPanel,
    DagTopologyPanel,
  },
  props: {
    items: { type: Array, default: () => [] },
    emptyText: { type: String, default: '暂无 DAG' },
    loading: { type: Boolean, default: false },
    error: { type: String, default: '' },
  },
  setup(props, ctx) {
    const emit = ctx?.emit || (() => {});
    const dagDetail = useDagDetail();
    const detailState = dagDetail.state;
    const selectedKey = ref('');
    const rows = computed(() => props.items.map((item, index) => normalizeDag(item, index)));
    const selectedRow = computed(() => rows.value.find((row) => row.listKey === selectedKey.value) || rows.value[0] || null);
    const selectedDagKey = computed(() => selectedRow.value?.key === '-' ? '' : selectedRow.value?.key || '');
    const selectedDagItems = computed(() => dagCandidates(selectedRow.value, detailState, selectedDagKey.value));
    const selectedFinalOutput = computed(() => detailState.finalOutput);
    const designNodes = computed(() => (
      Array.isArray(detailState.templateNodes) && detailState.templateNodes.length
        ? detailState.templateNodes
        : detailState.nodes
    ));
    const startDisabledReason = computed(() => {
      if (!selectedRow.value || !selectedDagKey.value) return '未选择 DAG';
      if (props.loading || detailState.loading) return 'DAG 详情加载中';
      if (detailState.error) return 'DAG 详情不可用';
      if (detailState.runsError) return '运行历史不可用，无法确认是否有运行中 run';
      if (detailState.starting) return '启动中';
      if (hasActiveRun(selectedDagItems.value, detailState)) return '已有运行中 run';
      const status = dagStatusOf(selectedDagItems.value);
      if (!STARTABLE_DAG_STATUSES.has(status)) return '当前状态不可启动';
      const trigger = dagTriggerOf(selectedDagItems.value);
      if (!STARTABLE_DAG_TRIGGERS.has(trigger)) return '当前触发方式不可启动';
      return '';
    });
    const editDisabledReason = computed(() => {
      if (!selectedRow.value || !selectedDagKey.value) return '未选择 DAG';
      if (props.loading || detailState.loading) return 'DAG 详情加载中';
      if (detailState.error) return 'DAG 详情不可用，不能编辑节点';
      if (detailState.runsError) return '运行历史不可用，不能编辑节点';
      if (hasActiveRun(selectedDagItems.value, detailState)) return '已有运行中 run，不能编辑节点';
      const status = dagStatusOf(selectedDagItems.value);
      if (status === 'running') return '当前 DAG 正在运行，不能编辑节点';
      if (TERMINAL_DAG_STATUSES.has(status)) return 'DAG 已终态，不能编辑节点';
      return '';
    });
    const startErrorText = computed(() => {
      const err = detailState.startError;
      if (!err) return '';
      if (typeof err === 'string') return err;
      return err.message || JSON.stringify(err);
    });
    const runsErrorText = computed(() => {
      const err = detailState.runsError;
      if (!err) return '';
      if (typeof err === 'string') return err;
      return err.message || JSON.stringify(err);
    });

    watch(
      () => rows.value.map((row) => row.listKey).join('\n'),
      () => {
        if (!rows.value.length) {
          selectedKey.value = '';
          return;
        }
        if (!rows.value.some((row) => row.listKey === selectedKey.value)) {
          selectedKey.value = rows.value[0].listKey;
        }
      },
      { immediate: true },
    );

    function selectDag(row) {
      if (!row) return;
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

    function startSelectedDag() {
      if (startDisabledReason.value) return;
      dagDetail.start();
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
      dagDetail,
      detailState,
      designNodes,
      editDisabledReason,
      openChat,
      rows,
      runsErrorText,
      saveAgentNode,
      selectedRow,
      selectedFinalOutput,
      selectDag,
      selectRun,
      startDisabledReason,
      startDesignFlow,
      startErrorText,
      startSelectedDag,
    };
  },
  template: `
    <section id="page-dags" class="page active dag-console-page" data-testid="dag-console">
      <AutoContinuePrefCard />
      <div class="panel-header" data-testid="dag-console-header">
        <div class="ph-bar"></div>
        <div class="ph-text"><h2>DAG Console</h2></div>
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
            <h3>正在加载 DAG</h3>
          </div>
          <div v-else-if="error" class="empty-state dag-console-empty dag-console-error" data-testid="dag-console-error">
            <div class="es-icon">D</div>
            <h3>加载 DAG 失败</h3>
            <p>{{ error }}</p>
          </div>
          <div v-else-if="rows.length === 0" class="empty-state dag-console-empty" data-testid="dag-console-empty">
            <div class="es-icon">D</div>
            <h3>{{ emptyText }}</h3>
          </div>
          <div v-else class="dag-console-list">
            <button
              v-for="(row, idx) in rows"
              :key="row.listKey"
              type="button"
              class="dag-console-row"
              :class="{ active: selectedRow && selectedRow.listKey === row.listKey }"
              :data-testid="'dag-console-row-' + idx"
              @click="selectDag(row)"
            >
              <span class="dag-console-row-main">
                <span class="dag-console-title">{{ row.title }}</span>
                <span class="dag-console-key">{{ row.key }}</span>
              </span>
              <span class="dag-console-row-meta">
                <span class="dag-console-status">{{ row.status }}</span>
                <span class="dag-console-trigger">{{ row.triggerLabel }}</span>
                <span class="dag-console-run">{{ row.latestRunLabel }}</span>
                <span v-if="row.hasFinalOutput" class="dag-console-final" data-testid="dag-console-final-marker">Final</span>
              </span>
            </button>
          </div>
        </aside>

        <section class="dag-console-detail-pane" data-testid="dag-console-detail">
          <div v-if="loading" class="empty-state dag-console-empty" data-testid="dag-console-detail-loading">
            <div class="es-icon">D</div>
            <h3>正在加载 DAG</h3>
          </div>
          <div v-else-if="error" class="empty-state dag-console-empty dag-console-error" data-testid="dag-console-detail-error">
            <div class="es-icon">D</div>
            <h3>加载 DAG 失败</h3>
            <p>{{ error }}</p>
          </div>
          <div v-else-if="selectedRow" class="dag-console-detail-grid">
            <div class="dag-console-detail-heading">
              <div>
                <h3>{{ selectedRow.title }}</h3>
                <span>{{ selectedRow.key }}</span>
              </div>
              <button
                type="button"
                class="btn btn-primary"
                data-testid="dag-start-button"
                :disabled="Boolean(startDisabledReason)"
                :title="startDisabledReason"
                @click="startSelectedDag"
              >{{ detailState.starting ? '启动中' : 'Start' }}</button>
            </div>
            <div v-if="startDisabledReason" class="dag-console-muted" data-testid="dag-start-disabled-reason">{{ startDisabledReason }}</div>
            <div v-if="startErrorText" class="dag-console-error-inline" data-testid="dag-start-error">{{ startErrorText }}</div>
            <dl class="dag-console-facts">
              <div>
                <dt>状态</dt>
                <dd>{{ selectedRow.status }}</dd>
              </div>
              <div>
                <dt>触发</dt>
                <dd>{{ selectedRow.triggerLabel }}</dd>
              </div>
              <div>
                <dt>最近运行</dt>
                <dd>{{ selectedRow.latestRunLabel }}</dd>
              </div>
              <div>
                <dt>最终产物</dt>
                <dd>{{ selectedRow.hasFinalOutput ? '已记录' : '-' }}</dd>
              </div>
            </dl>
            <div v-if="detailState.loading" class="dag-console-muted" data-testid="dag-detail-loading-inline">正在加载详情</div>
            <div v-if="detailState.error" class="dag-console-error-inline" data-testid="dag-detail-load-error">
              {{ typeof detailState.error === 'string' ? detailState.error : detailState.error.message }}
            </div>
            <DagNodeList :nodes="detailState.nodes" @open-chat="openChat" />
            <DagTopologyPanel :nodes="designNodes" />
            <DagNodeEditForm
              :nodes="designNodes"
              :saving-node-key="detailState.savingNodeKey"
              :save-error="detailState.saveError"
              :disabled-reason="editDisabledReason"
              @save-agent-node="saveAgentNode"
            />
            <DagSharedFilesPanel :nodes="designNodes" />
            <DagRunHistoryPanel
              :runs="detailState.runs"
              :selected-run-key="detailState.selectedRunKey"
              @select-run="selectRun"
            />
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
          </div>
          <div v-else class="empty-state dag-console-empty" data-testid="dag-console-detail-empty">
            <div class="es-icon">D</div>
            <h3>{{ emptyText }}</h3>
          </div>
        </section>
      </div>
    </section>
  `,
};
