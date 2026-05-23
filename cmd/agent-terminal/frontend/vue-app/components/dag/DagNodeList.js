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

function parseObject(value) {
  if (!value) return {};
  if (typeof value === 'object') return value;
  if (typeof value !== 'string') return {};
  try {
    const parsed = JSON.parse(value);
    return parsed && typeof parsed === 'object' ? parsed : {};
  } catch {
    return {};
  }
}

function nodeProviderLabel(node) {
  const config = parseObject(node?.config);
  const exec = parseObject(config.exec || node?.exec);
  const provider = textValue(node?.provider, node?.provider_id, node?.providerId, exec.provider);
  const model = textValue(node?.model, node?.model_id, node?.modelId, exec.model);
  const agent = textValue(node?.agent_key, node?.agentKey, node?.agent, exec.agent_key, exec.agentKey);
  return [provider, model, agent].filter((item) => item !== '-').join(' / ') || '-';
}

function spawningThreadId(node) {
  return textValue(node?.spawning_thread_id, node?.spawningThreadId, node?.thread_id, node?.threadId);
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
      title: textValue(node?.title, node?.name, nodeKey(node)),
      status: textValue(node?.status, node?.state),
      nodeType: textValue(node?.node_type, node?.nodeType, node?.type),
      providerLabel: nodeProviderLabel(node),
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
      <div class="dag-section-title">Nodes</div>
      <div v-if="rows.length === 0" class="dag-console-muted" data-testid="dag-node-list-empty">暂无节点</div>
      <div v-else class="dag-node-list-grid">
        <article v-for="row in rows" :key="row.key" class="dag-node-card">
          <div class="dag-node-card-head">
            <strong>{{ row.title }}</strong>
            <span>{{ row.status }}</span>
          </div>
          <div class="dag-node-meta">
            <span>{{ row.nodeType }}</span>
            <span>{{ row.providerLabel }}</span>
          </div>
          <button
            v-if="row.spawningThreadId !== '-'"
            type="button"
            class="dag-link-button"
            data-testid="dag-node-open-chat"
            @click="openChat(row)"
          >{{ row.spawningThreadId }}</button>
        </article>
      </div>
    </section>
  `,
};
