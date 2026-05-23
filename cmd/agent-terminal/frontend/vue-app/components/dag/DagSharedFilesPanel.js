import { computed } from '../../../lib/vue.esm-browser.prod.js';

function textValue(...values) {
  for (const value of values) {
    if (value === null || value === undefined) continue;
    const text = value.toString().trim();
    if (text) return text;
  }
  return '';
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

function nodeKey(node, index) {
  return textValue(node?.node_key, node?.nodeKey, node?.key, node?.id) || `node_${index + 1}`;
}

function sharedFileRows(nodes) {
  const rows = [];
  for (const [index, node] of nodes.entries()) {
    const key = nodeKey(node, index);
    const config = parseObject(node?.config);
    const inputs = parseObject(config.inputs);
    const outputs = parseObject(config.outputs);
    const reads = Array.isArray(inputs.from_sharedfiles) ? inputs.from_sharedfiles : [];
    for (const path of reads) rows.push({ nodeKey: key, path: textValue(path), mode: 'read', lockMode: '-' });
    const target = parseObject(outputs.to_sharedfile);
    if (target.path) {
      rows.push({
        nodeKey: key,
        path: textValue(target.path),
        mode: 'write',
        lockMode: textValue(target.lock_mode, target.lockMode) || '-',
      });
    }
  }
  return rows.filter((row) => row.path);
}

export const DagSharedFilesPanel = {
  name: 'DagSharedFilesPanel',
  props: {
    nodes: { type: Array, default: () => [] },
  },
  setup(props) {
    const rows = computed(() => sharedFileRows(Array.isArray(props.nodes) ? props.nodes : []));
    return { rows };
  },
  template: `
    <section class="dag-detail-section dag-sharedfiles-panel" data-testid="dag-sharedfiles-panel">
      <div class="dag-section-title">Shared Files</div>
      <div v-if="rows.length === 0" class="dag-console-muted" data-testid="dag-sharedfiles-empty">暂无 sharedfile 读写</div>
      <div v-else class="dag-sharedfile-list">
        <div v-for="row in rows" :key="row.nodeKey + ':' + row.mode + ':' + row.path" class="dag-sharedfile-row">
          <span>{{ row.nodeKey }}</span>
          <strong>{{ row.path }}</strong>
          <small>{{ row.mode }} · {{ row.lockMode }}</small>
        </div>
      </div>
    </section>
  `,
};
