import { reactive } from '../../lib/vue.esm-browser.prod.js';
import { callAPI } from '../services/api.js';

export function useDagDetail() {
  const state = reactive({
    show: false,
    loading: false,
    error: '',
    dag: null,
    nodes: [],
    savingNodeKey: '',
    saveError: '',
  });

  async function fetchDetail(dagKey) {
    const res = await callAPI('dashboard/dagDetail', { dagKey });
    state.dag = res?.dag || null;
    state.nodes = Array.isArray(res?.nodes) ? res.nodes : [];
  }

  async function open(/** @type {{ dag_key?: string } | null | undefined} */ item) {
    const dagKey = (item?.dag_key || '').toString().trim();
    if (!dagKey) return;
    state.show = true;
    state.loading = true;
    state.error = '';
    state.saveError = '';
    state.dag = null;
    state.nodes = [];
    try {
      await fetchDetail(dagKey);
    } catch (err) {
      state.error = err?.message || 'DAG 详情加载失败';
    } finally {
      state.loading = false;
    }
  }

  async function refresh() {
    const dagKey = (state.dag?.dag_key || '').toString().trim();
    if (!dagKey) return;
    try {
      await fetchDetail(dagKey);
    } catch (err) {
      state.error = err?.message || 'DAG 详情加载失败';
    }
  }

  async function updateNodeStatus(/** @type {string} */ nodeKey, /** @type {string} */ status) {
    const dagKey = (state.dag?.dag_key || '').toString().trim();
    const trimmedNodeKey = (nodeKey || '').toString().trim();
    const trimmedStatus = (status || '').toString().trim();
    if (!dagKey || !trimmedNodeKey || !trimmedStatus) return false;
    state.savingNodeKey = trimmedNodeKey;
    state.saveError = '';
    try {
      await callAPI('task/node/update', {
        dag_key: dagKey,
        node_key: trimmedNodeKey,
        status: trimmedStatus,
      });
      await fetchDetail(dagKey);
      return true;
    } catch (err) {
      state.saveError = err?.message || '节点状态更新失败';
      return false;
    } finally {
      state.savingNodeKey = '';
    }
  }

  function matchesOpenDag(/** @type {string} */ dagKey) {
    const key = (dagKey || '').toString().trim();
    if (!key) return false;
    return state.show && (state.dag?.dag_key || '').toString().trim() === key;
  }

  function handleStatusEvent(/** @type {{ dag_key?: string }} */ payload) {
    if (!matchesOpenDag(payload?.dag_key || '')) return false;
    refresh().catch((err) => { console.warn('refresh dag detail failed', err); });
    return true;
  }

  function close() {
    state.show = false;
  }

  return { state, open, close, refresh, updateNodeStatus, matchesOpenDag, handleStatusEvent };
}
