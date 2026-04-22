// DagDetailModal.js — STUB（恢复黑屏前的最小可运行实现）
//
// 历史：c9f267a `feat(handoff): add task handoff continuity flow` commit 引入
//       了 app.js 对该组件的引用，但实际文件未 commit。这里提供能正常 mount 的
//       占位实现（show=false 时不渲染，show=true 时弹出极简信息框）。
import { computed } from '../../lib/vue.esm-browser.prod.js';

export const DagDetailModal = {
  name: 'DagDetailModal',
  props: {
    show: { type: Boolean, default: false },
    loading: { type: Boolean, default: false },
    error: { type: [String, Object], default: null },
    dag: { type: Object, default: null },
    nodes: { type: Array, default: () => [] },
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
    return { dagLabel, nodeCount, errorText, saveErrorText, close };
  },
  template: `
    <div v-if="show" class="modal-overlay" @click.self="close">
      <div class="modal-box">
        <div class="modal-title">DAG 详情（占位）</div>
        <div v-if="loading" class="modal-body">加载中…</div>
        <div v-else-if="errorText" class="modal-body modal-error">{{ errorText }}</div>
        <div v-else class="modal-body">
          <div>名称: {{ dagLabel }}</div>
          <div>节点数: {{ nodeCount }}</div>
          <div v-if="savingNodeKey">正在保存节点: {{ savingNodeKey }}</div>
          <div v-if="saveErrorText" class="modal-error">保存失败: {{ saveErrorText }}</div>
          <div class="modal-hint" style="margin-top:8px;opacity:.7;font-size:12px;">
            DagDetailModal 当前为占位实现，详情面板尚未接入真实数据。
          </div>
        </div>
        <div class="modal-btns">
          <button class="btn btn-ghost" @click="close">关闭</button>
        </div>
      </div>
    </div>
  `,
};
