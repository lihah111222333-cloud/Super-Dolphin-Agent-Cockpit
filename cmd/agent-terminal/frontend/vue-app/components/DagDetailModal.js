import { computed, ref } from '../../lib/vue.esm-browser.prod.js';

const NODE_STATUS_OPTIONS = Object.freeze(['pending', 'running', 'done', 'failed']);

function formatTimestamp(value) {
  if (!value) return '-';
  try {
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return String(value);
    return date.toLocaleString();
  } catch (_err) {
    return String(value);
  }
}

function formatDependsOn(value) {
  if (!Array.isArray(value) || value.length === 0) return '-';
  return value.join(', ');
}

export const DagDetailModal = {
  name: 'DagDetailModal',
  props: {
    show: { type: Boolean, default: false },
    loading: { type: Boolean, default: false },
    error: { type: String, default: '' },
    dag: { type: Object, default: null },
    nodes: { type: Array, default: () => [] },
    savingNodeKey: { type: String, default: '' },
    saveError: { type: String, default: '' },
  },
  emits: ['close', 'update-node-status', 'open-chat'],
  setup(props, ctx = {}) {
    const emit = typeof ctx.emit === 'function' ? ctx.emit : () => {};
    const dagTitle = computed(() => {
      if (!props.dag) return 'DAG 详情';
      return props.dag.title || props.dag.dag_key || 'DAG 详情';
    });
    const hasNodes = computed(() => Array.isArray(props.nodes) && props.nodes.length > 0);
    const pendingStatus = ref({});

    function close() {
      emit('close');
    }

    function selectedStatus(node) {
      const key = (node?.node_key || '').toString();
      return pendingStatus.value[key] ?? (node?.status || '');
    }

    function onStatusChange(node, value) {
      const key = (node?.node_key || '').toString();
      if (!key) return;
      pendingStatus.value = { ...pendingStatus.value, [key]: value };
    }

    function isStatusDirty(node) {
      const selected = selectedStatus(node);
      const current = (node?.status || '').toString();
      return selected !== '' && selected !== current;
    }

    function saveStatus(node) {
      const key = (node?.node_key || '').toString();
      if (!key) return;
      const selected = selectedStatus(node);
      if (!selected) return;
      emit('update-node-status', { nodeKey: key, status: selected });
    }

    function openChat(node) {
      emit('open-chat', {
        turnId: (node?.active_turn_id || '').toString(),
        assignedTo: (node?.assigned_to || '').toString(),
      });
    }

    return {
      dagTitle,
      hasNodes,
      close,
      formatTimestamp,
      formatDependsOn,
      statusOptions: NODE_STATUS_OPTIONS,
      selectedStatus,
      onStatusChange,
      isStatusDirty,
      saveStatus,
      openChat,
    };
  },
  template: `
    <div
      v-if="show"
      class="modal-overlay"
      role="dialog"
      aria-modal="true"
      data-testid="dag-detail-modal"
      @click.self="close"
      @keydown.esc.prevent="close"
      tabindex="-1"
    >
      <div class="modal-box" style="min-width:560px;max-width:960px;max-height:80vh;overflow:auto;">
        <div class="modal-title" data-testid="dag-detail-title">{{ dagTitle }}</div>

        <div v-if="loading" data-testid="dag-detail-loading" style="padding:16px 0;color:var(--text-muted);">
          加载中…
        </div>
        <div
          v-else-if="error"
          data-testid="dag-detail-error"
          style="padding:12px 0;color:var(--danger, #d32f2f);"
        >
          {{ error }}
        </div>
        <template v-else-if="dag">
          <section style="display:flex;flex-direction:column;gap:6px;margin-bottom:16px;">
            <div class="data-row-vue"><strong>DAG Key</strong><span>{{ dag.dag_key || '-' }}</span></div>
            <div class="data-row-vue"><strong>状态</strong><span>{{ dag.status || '-' }}</span></div>
            <div v-if="dag.description" class="data-row-vue">
              <strong>描述</strong><span>{{ dag.description }}</span>
            </div>
            <div class="data-row-vue"><strong>创建者</strong><span>{{ dag.created_by || '-' }}</span></div>
            <div class="data-row-vue">
              <strong>开始时间</strong><span>{{ formatTimestamp(dag.started_at) }}</span>
            </div>
            <div class="data-row-vue">
              <strong>结束时间</strong><span>{{ formatTimestamp(dag.finished_at) }}</span>
            </div>
            <div class="data-row-vue">
              <strong>创建时间</strong><span>{{ formatTimestamp(dag.created_at) }}</span>
            </div>
            <div class="data-row-vue">
              <strong>更新时间</strong><span>{{ formatTimestamp(dag.updated_at) }}</span>
            </div>
          </section>

          <section>
            <div style="font-weight:600;margin-bottom:8px;">节点 ({{ nodes.length }})</div>
            <div
              v-if="!hasNodes"
              data-testid="dag-detail-nodes-empty"
              style="color:var(--text-muted);padding:8px 0;"
            >
              该 DAG 暂无节点
            </div>
            <article
              v-for="(node, idx) in nodes"
              :key="node.node_key || idx"
              class="data-card-vue"
              :data-testid="'dag-detail-node-' + idx"
              style="margin-bottom:8px;"
            >
              <div class="data-row-vue"><strong>Node Key</strong><span>{{ node.node_key || '-' }}</span></div>
              <div class="data-row-vue"><strong>标题</strong><span>{{ node.title || '-' }}</span></div>
              <div class="data-row-vue"><strong>状态</strong><span>{{ node.status || '-' }}</span></div>
              <div v-if="node.assigned_to" class="data-row-vue">
                <strong>执行者</strong><span>{{ node.assigned_to }}</span>
              </div>
              <div v-if="node.command_ref" class="data-row-vue">
                <strong>命令</strong><span>{{ node.command_ref }}</span>
              </div>
              <div class="data-row-vue">
                <strong>依赖</strong><span>{{ formatDependsOn(node.depends_on) }}</span>
              </div>
              <div v-if="node.active_turn_id" class="data-row-vue">
                <strong>Turn</strong><span>{{ node.active_turn_id }}</span>
              </div>
              <div v-if="node.last_event_at" class="data-row-vue">
                <strong>最近事件</strong><span>{{ formatTimestamp(node.last_event_at) }}</span>
              </div>
              <div
                class="data-actions-vue"
                style="display:flex;gap:8px;align-items:center;flex-wrap:wrap;justify-content:flex-start;"
              >
                <label style="display:flex;align-items:center;gap:6px;font-size:12px;">
                  状态
                  <select
                    :data-testid="'dag-detail-node-status-' + idx"
                    :value="selectedStatus(node)"
                    :disabled="savingNodeKey === node.node_key"
                    @change="onStatusChange(node, $event.target.value)"
                  >
                    <option v-for="opt in statusOptions" :key="opt" :value="opt">{{ opt }}</option>
                  </select>
                </label>
                <button
                  class="btn btn-ghost"
                  type="button"
                  :data-testid="'dag-detail-node-save-' + idx"
                  :disabled="!isStatusDirty(node) || savingNodeKey === node.node_key"
                  @click="saveStatus(node)"
                >{{ savingNodeKey === node.node_key ? '保存中…' : '保存状态' }}</button>
                <button
                  v-if="node.active_turn_id"
                  class="btn btn-ghost"
                  type="button"
                  :data-testid="'dag-detail-node-open-chat-' + idx"
                  @click="openChat(node)"
                >打开对话</button>
              </div>
            </article>
          </section>
        </template>
        <div
          v-else
          data-testid="dag-detail-empty"
          style="padding:12px 0;color:var(--text-muted);"
        >
          暂无 DAG 信息
        </div>

        <div
          v-if="saveError"
          data-testid="dag-detail-save-error"
          style="margin-top:12px;color:var(--danger, #d32f2f);font-size:12px;"
        >{{ saveError }}</div>

        <div class="modal-btns">
          <button
            class="btn btn-ghost"
            type="button"
            data-testid="dag-detail-close"
            @click="close"
          >关闭</button>
        </div>
      </div>
    </div>
  `,
};
