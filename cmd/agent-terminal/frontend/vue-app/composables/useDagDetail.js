// useDagDetail.js — DAG detail loader for dashboard modal.
import { reactive } from '../../lib/vue.esm-browser.prod.js';
import { callAPI } from '../services/api.js';
import { logWarn } from '../services/log.js';

const RECENT_RUN_LIMIT = 5;

function parseJSONLike(value) {
  if (!value) return null;
  if (typeof value === 'object') return value;
  if (typeof value !== 'string') return null;
  const text = value.trim();
  if (!text) return null;
  try {
    return JSON.parse(text);
  } catch {
    return null;
  }
}

function normalizeFinalOutput(run) {
  const metadata = parseJSONLike(run?.metadata);
  const output = metadata?.final_output;
  if (!output) return null;
  if (typeof output === 'string') {
    return { kind: 'text', role: 'final_output', text: output };
  }
  if (typeof output !== 'object') return null;
  return { ...output };
}

function dagKeyFromItem(item) {
  return (item?.dag_key || item?.dagKey || item?.key || item?.id || '').toString().trim();
}

export function useDagDetail() {
  let openSeq = 0;
  const state = reactive({
    show: false,
    loading: false,
    error: null,
    dag: null,
    nodes: [],
    runs: [],
    run: null,
    finalOutput: null,
    savingNodeKey: '',
    saveError: null,
  });

  function resetTransient() {
    state.error = null;
    state.loading = false;
    state.savingNodeKey = '';
    state.saveError = null;
  }

  async function open(item) {
    const seq = ++openSeq;
    resetTransient();
    state.dag = item || null;
    state.nodes = [];
    state.runs = [];
    state.run = null;
    state.finalOutput = null;
    state.show = true;
    const dagKey = dagKeyFromItem(item);
    if (!dagKey) {
      state.error = '缺少 DAG key';
      return;
    }
    state.loading = true;
    try {
      const detail = await callAPI('dashboard/dagDetail', { dagKey });
      if (seq !== openSeq) return;
      state.dag = detail?.dag || item || null;
      state.nodes = Array.isArray(detail?.nodes) ? detail.nodes : [];
      try {
        const runsResult = await callAPI('dashboard/dagRuns', { dagKey, limit: RECENT_RUN_LIMIT });
        if (seq !== openSeq) return;
        state.runs = Array.isArray(runsResult?.runs) ? runsResult.runs : [];
        state.run = state.runs[0] || null;
        state.finalOutput = normalizeFinalOutput(state.run);
      } catch (error) {
        if (seq === openSeq) {
          state.runs = [];
          state.run = null;
          state.finalOutput = null;
          logWarn('ui', 'useDagDetail.runs.failed', { dagKey, error });
        }
      }
    } catch (error) {
      if (seq === openSeq) {
        state.error = error;
        logWarn('ui', 'useDagDetail.open.failed', { dagKey, error });
      }
    } finally {
      if (seq === openSeq) state.loading = false;
    }
  }

  function close() {
    openSeq += 1;
    state.show = false;
  }

  function updateNodeStatus(nodeKey, status) {
    logWarn('ui', 'useDagDetail.updateNodeStatus.not_implemented', { nodeKey, status });
  }

  function handleStatusEvent(payload = {}) {
    const nodeKey = (payload.node_key || payload.nodeKey || '').toString().trim();
    const status = (payload.status || '').toString().trim();
    if (!nodeKey || !status || !Array.isArray(state.nodes)) return;
    const node = state.nodes.find((item) => (item?.node_key || item?.nodeKey || '').toString().trim() === nodeKey);
    if (node) node.status = status;
  }

  return { state, open, close, updateNodeStatus, handleStatusEvent };
}
