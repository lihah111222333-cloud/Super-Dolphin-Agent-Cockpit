// useDagDetail.js — STUB（恢复黑屏前的最小可运行实现）
//
// 历史：c9f267a `feat(handoff): add task handoff continuity flow` commit 引入
//       了 app.js 对 useDagDetail / DagDetailModal 的引用，但实际文件未 commit，
//       导致 Vite import 解析 500 → webview 黑屏。
//
// 该 stub 提供 app.js 期望的接口形状（state + open/close/updateNodeStatus/
// handleStatusEvent），实际加载逻辑留待后续真实实现替换。
import { reactive } from '../../lib/vue.esm-browser.prod.js';
import { logWarn } from '../services/log.js';

export function useDagDetail() {
  const state = reactive({
    show: false,
    loading: false,
    error: null,
    dag: null,
    nodes: [],
    savingNodeKey: '',
    saveError: null,
  });

  function resetTransient() {
    state.error = null;
    state.loading = false;
    state.savingNodeKey = '';
    state.saveError = null;
  }

  function open(item) {
    resetTransient();
    state.dag = item || null;
    state.nodes = [];
    state.show = true;
    logWarn('ui', 'useDagDetail.open.stub', {
      hint: 'DagDetail 当前为占位实现，未加载真实节点数据',
      dagKey: (item && (item.id || item.key)) || '',
    });
  }

  function close() {
    state.show = false;
  }

  function updateNodeStatus(nodeKey, status) {
    logWarn('ui', 'useDagDetail.updateNodeStatus.stub', { nodeKey, status });
  }

  function handleStatusEvent(/* payload */) {
    // task/node/statuschanged 事件入口；stub 不持有真实 dag/nodes，故无需处理。
  }

  return { state, open, close, updateNodeStatus, handleStatusEvent };
}
