import { computed } from '../../../lib/vue.esm-browser.prod.js';

function textValue(...values) {
  for (const value of values) {
    if (value === null || value === undefined) continue;
    const text = value.toString().trim();
    if (text) return text;
  }
  return '-';
}

function nodeKey(node) {
  return textValue(node?.node_key, node?.nodeKey, node?.key, node?.id);
}

function spawningThreadId(node) {
  return textValue(node?.spawning_thread_id, node?.spawningThreadId, node?.thread_id, node?.threadId);
}

const STEP_STATUS_LABELS = {
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

function statusLabel(value) {
  const status = textValue(value);
  if (status === '-') return '-';
  return STEP_STATUS_LABELS[status.toLowerCase()] || status;
}

function nodeTitle(node, index) {
  const title = textValue(node?.title, node?.name);
  return title === '-' ? `步骤 ${index + 1}` : title;
}

export const DagNodeList = {
  name: 'DagNodeList',
  props: {
    nodes: { type: Array, default: () => [] },
  },
  emits: ['open-chat'],
  setup(props, { emit }) {
    const rows = computed(() => (Array.isArray(props.nodes) ? props.nodes : []).map((node, index) => ({
      key: nodeKey(node) === '-' ? `node-${index}` : nodeKey(node),
      title: nodeTitle(node, index),
      status: statusLabel(node?.status || node?.state),
      chatLabel: '查看对话',
      spawningThreadId: spawningThreadId(node),
      raw: node,
    })));

    function openChat(row) {
      if (!row || row.spawningThreadId === '-') return;
      emit('open-chat', row.spawningThreadId);
    }

    return { rows, openChat };
  },
  template: `
    <section class="dag-detail-section dag-node-list" data-testid="dag-node-list">
      <div class="dag-section-title">步骤</div>
      <div v-if="rows.length === 0" class="dag-console-muted" data-testid="dag-node-list-empty">暂无步骤</div>
      <div v-else class="dag-node-list-grid">
        <article v-for="row in rows" :key="row.key" class="dag-node-card">
          <div class="dag-node-card-head">
            <strong>{{ row.title }}</strong>
            <span>{{ row.status }}</span>
          </div>
          <button
            v-if="row.spawningThreadId !== '-'"
            type="button"
            class="dag-link-button"
            data-testid="dag-node-open-chat"
            @click="openChat(row)"
          >{{ row.chatLabel }}</button>
        </article>
      </div>
    </section>
  `,
};
