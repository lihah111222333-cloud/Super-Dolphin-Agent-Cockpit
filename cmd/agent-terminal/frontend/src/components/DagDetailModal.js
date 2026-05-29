import { computed } from '../../lib/vue.esm-browser.prod.js';

function previewValue(value) {
  if (value === null || value === undefined) return '';
  if (typeof value === 'string') {
    const text = value.trim();
    return text.length > 320 ? `${text.slice(0, 320)}…` : text;
  }
  if (typeof value === 'number' || typeof value === 'boolean') return String(value);
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

export const DagDetailModal = {
  name: 'DagDetailModal',
  props: {
    show: { type: Boolean, default: false },
    loading: { type: Boolean, default: false },
    error: { type: [String, Object], default: null },
    dag: { type: Object, default: null },
    nodes: { type: Array, default: () => [] },
    runs: { type: Array, default: () => [] },
    run: { type: Object, default: null },
    finalOutput: { type: Object, default: null },
    savingNodeKey: { type: String, default: '' },
    saveError: { type: [String, Object], default: null },
  },
  emits: ['close', 'update-node-status', 'open-chat'],
  setup(props, { emit }) {
    const dagLabel = computed(() => {
      const d = props.dag || {};
      return (d.title || d.name || d.id || d.key || '').toString().trim() || '(未命名 DAG)';
    });
    const nodeCount = computed(() => Array.isArray(props.nodes) ? props.nodes.length : 0);
    const runLabel = computed(() => {
      const run = props.run || {};
      return (run.run_key || run.runKey || '').toString().trim();
    });
    const finalOutputKind = computed(() => (props.finalOutput?.kind || '').toString().trim() || '-');
    const finalOutputPath = computed(() => {
      const output = props.finalOutput || {};
      return (output.path || output?.sharedfile?.path || '').toString().trim();
    });
    const finalOutputText = computed(() => {
      const output = props.finalOutput || {};
      const text = previewValue(output.text);
      if (text) return text;
      const result = previewValue(output.result);
      if (result) return result;
      if (finalOutputPath.value) return finalOutputPath.value;
      if (!props.finalOutput) return '';
      return JSON.stringify(props.finalOutput, null, 2);
    });
    const errorText = computed(() => {
      const err = props.error;
      if (!err) return '';
      if (typeof err === 'string') return err;
      return err.message || JSON.stringify(err);
    });
    const saveErrorText = computed(() => {
      const err = props.saveError;
      if (!err) return '';
      if (typeof err === 'string') return err;
      return err.message || JSON.stringify(err);
    });
    function close() { emit('close'); }
    return { dagLabel, nodeCount, runLabel, finalOutputKind, finalOutputPath, finalOutputText, errorText, saveErrorText, close };
  },
  template: `
    <div v-if="show" class="modal-overlay" @click.self="close">
      <div class="modal-box">
        <div class="modal-title">DAG 详情</div>
        <div v-if="loading" class="modal-body">加载中…</div>
        <div v-else-if="errorText" class="modal-body modal-error">{{ errorText }}</div>
        <div v-else class="modal-body">
          <div>名称: {{ dagLabel }}</div>
          <div>节点数: {{ nodeCount }}</div>
          <div v-if="runLabel">最近运行: {{ runLabel }}</div>
          <div v-if="finalOutput" class="data-card-vue dag-final-output" data-testid="dag-final-output">
            <div class="data-row-vue"><strong>最终产物</strong><span>{{ finalOutputKind }}</span></div>
            <div v-if="finalOutputPath" class="data-row-vue"><strong>文件</strong><span>{{ finalOutputPath }}</span></div>
            <pre v-else class="memory-entry-preview">{{ finalOutputText }}</pre>
          </div>
          <div v-else-if="runLabel" class="modal-hint" data-testid="dag-final-output-empty" style="margin-top:8px;opacity:.7;font-size:12px;">
            最近运行尚未标记最终产物。
          </div>
          <div v-if="savingNodeKey">正在保存节点: {{ savingNodeKey }}</div>
          <div v-if="saveErrorText" class="modal-error">保存失败: {{ saveErrorText }}</div>
        </div>
        <div class="modal-btns">
          <button class="btn btn-ghost" @click="close">关闭</button>
        </div>
      </div>
    </div>
  `,
};
