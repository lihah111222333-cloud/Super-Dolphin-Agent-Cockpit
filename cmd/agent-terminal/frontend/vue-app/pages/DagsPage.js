import { computed, ref, watch } from '../../lib/vue.esm-browser.prod.js';
import { AutoContinuePrefCard } from '../components/AutoContinuePrefCard.js';

function textValue(...values) {
  for (const value of values) {
    if (value === null || value === undefined) continue;
    const text = value.toString().trim();
    if (text) return text;
  }
  return '-';
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
  components: { AutoContinuePrefCard },
  props: {
    items: { type: Array, default: () => [] },
    emptyText: { type: String, default: '暂无 DAG' },
    loading: { type: Boolean, default: false },
    error: { type: String, default: '' },
  },
  setup(props) {
    const selectedKey = ref('');
    const rows = computed(() => props.items.map((item, index) => normalizeDag(item, index)));
    const selectedRow = computed(() => rows.value.find((row) => row.listKey === selectedKey.value) || rows.value[0] || null);

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

    return {
      rows,
      selectedRow,
      selectDag,
    };
  },
  template: `
    <section id="page-dags" class="page active dag-console-page" data-testid="dag-console">
      <AutoContinuePrefCard />
      <div class="panel-header" data-testid="dag-console-header">
        <div class="ph-bar"></div>
        <div class="ph-text"><h2>DAG Console</h2></div>
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
              <h3>{{ selectedRow.title }}</h3>
              <span>{{ selectedRow.key }}</span>
            </div>
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
