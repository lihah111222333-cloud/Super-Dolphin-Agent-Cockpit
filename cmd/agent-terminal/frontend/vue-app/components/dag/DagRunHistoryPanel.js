import { computed } from '../../../lib/vue.esm-browser.prod.js';

function textValue(...values) {
  for (const value of values) {
    if (value === null || value === undefined) continue;
    const text = value.toString().trim();
    if (text) return text;
  }
  return '-';
}

function runKey(run) {
  return textValue(run?.run_key, run?.runKey, run?.key, run?.id);
}

const RUN_STATUS_LABELS = {
  draft: '草稿',
  ready: '等待运行',
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

function statusLabel(value) {
  const status = textValue(value);
  if (status === '-') return '-';
  return RUN_STATUS_LABELS[status.toLowerCase()] || status;
}

export const DagRunHistoryPanel = {
  name: 'DagRunHistoryPanel',
  props: {
    runs: { type: Array, default: () => [] },
    selectedRunKey: { type: String, default: '' },
  },
  emits: ['select-run'],
  setup(props, { emit }) {
    const rows = computed(() => (Array.isArray(props.runs) ? props.runs : []).map((run, index) => {
      const key = runKey(run);
      return {
        key: key === '-' ? `run-${index}` : key,
        label: `第 ${index + 1} 次运行`,
        status: statusLabel(run?.status || run?.state),
        startedAt: textValue(run?.started_at, run?.startedAt, run?.created_at, run?.createdAt),
      };
    }));

    function selectRun(row) {
      if (!row || row.key === '-') return;
      emit('select-run', row.key);
    }

    return { rows, selectRun };
  },
  template: `
    <section class="dag-detail-section dag-run-history" data-testid="dag-run-history">
      <div class="dag-section-title">最近运行</div>
      <div v-if="rows.length === 0" class="dag-console-muted" data-testid="dag-run-history-empty">暂无运行记录</div>
      <div v-else class="dag-run-list">
        <button
          v-for="row in rows"
          :key="row.key"
          type="button"
          class="dag-run-row"
          :class="{ active: selectedRunKey === row.key }"
          data-testid="dag-run-history-row"
          @click="selectRun(row)"
        >
          <span>{{ row.label }}</span>
          <span>{{ row.status }}</span>
          <small>{{ row.startedAt === '-' ? '时间未记录' : row.startedAt }}</small>
        </button>
      </div>
    </section>
  `,
};
