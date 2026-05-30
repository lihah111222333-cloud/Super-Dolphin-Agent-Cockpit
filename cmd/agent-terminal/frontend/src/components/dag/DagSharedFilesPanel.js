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

const LOCK_MODE_LABELS = {
  exclusive: '独占写入',
  append: '追加写入',
  shared: '共享读取',
};

function lockModeLabel(value) {
  const mode = textValue(value);
  if (!mode) return '-';
  return LOCK_MODE_LABELS[mode.toLowerCase()] || mode;
}

function accessLabel(mode, lockMode) {
  return lockMode && lockMode !== '-' ? `${mode} · ${lockMode}` : mode;
}

function sharedFileRows(nodes) {
  const rows = [];
  for (const [index, node] of nodes.entries()) {
    const key = nodeKey(node, index);
    const stepLabel = `第 ${index + 1} 步`;
    const config = parseObject(node?.config);
    const inputs = parseObject(config.inputs);
    const outputs = parseObject(config.outputs);
    const reads = Array.isArray(inputs.from_sharedfiles) ? inputs.from_sharedfiles : [];
    for (const path of reads) {
      rows.push({
        nodeKey: key,
        stepLabel,
        path: textValue(path),
        mode: '读取',
        lockMode: '-',
        accessLabel: '读取',
      });
    }
    const target = parseObject(outputs.to_sharedfile);
    if (target.path) {
      const mode = '写入';
      const lockMode = lockModeLabel(target.lock_mode || target.lockMode);
      rows.push({
        nodeKey: key,
        stepLabel,
        path: textValue(target.path),
        mode,
        lockMode,
        accessLabel: accessLabel(mode, lockMode),
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
      <div class="dag-section-title">工作文件</div>
      <div v-if="rows.length === 0" class="dag-console-muted" data-testid="dag-sharedfiles-empty">暂无工作文件读写</div>
      <div v-else class="dag-sharedfile-list">
        <div v-for="row in rows" :key="row.nodeKey + ':' + row.mode + ':' + row.path" class="dag-sharedfile-row">
          <span>{{ row.stepLabel }}</span>
          <strong>{{ row.path }}</strong>
          <small>{{ row.accessLabel }}</small>
        </div>
      </div>
    </section>
  `,
};
