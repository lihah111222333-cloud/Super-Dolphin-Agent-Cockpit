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

function dagVersionFromItem(item) {
  const value = item?.version ?? item?.Version;
  if (value === undefined || value === null || value === '') return null;
  const version = Number(value);
  return Number.isFinite(version) && version >= 0 ? version : null;
}

function runKeyFromItem(item) {
  return (item?.run_key || item?.runKey || item?.key || item?.id || '').toString().trim();
}

function makeStartIdempotencyKey(dagKey) {
  return `dag-start:${dagKey}:${Date.now()}:${Math.random().toString(36).slice(2)}`;
}

function normalizeNodeSavePayload(payload = {}) {
  const nodeKey = (payload.nodeKey || payload.node_key || '').toString().trim();
  if (!nodeKey) throw new Error('缺少 node key');
  const patch = {};
  if (Object.prototype.hasOwnProperty.call(payload, 'title')) {
    patch.title = (payload.title || '').toString().trim();
  }
  if (Object.prototype.hasOwnProperty.call(payload, 'dependsOn')) {
    patch.depends_on = Array.isArray(payload.dependsOn)
      ? payload.dependsOn.map((item) => (item || '').toString().trim()).filter(Boolean)
      : [];
  }
  if (Object.prototype.hasOwnProperty.call(payload, 'config')) {
    patch.config = payload.config && typeof payload.config === 'object' ? payload.config : {};
  }
  return {
    op: 'update_node',
    node_key: nodeKey,
    patch,
  };
}

export function useDagDetail() {
  let openSeq = 0;
  const state = reactive({
    show: false,
    loading: false,
    error: null,
    runsError: null,
    dag: null,
    templateNodes: [],
    nodes: [],
    runs: [],
    run: null,
    selectedRunKey: '',
    finalOutput: null,
    starting: false,
    startError: null,
    savingNodeKey: '',
    saveError: null,
  });

  function resetTransient() {
    state.error = null;
    state.loading = false;
    state.runsError = null;
    state.starting = false;
    state.startError = null;
    state.savingNodeKey = '';
    state.saveError = null;
  }

  async function loadRun(seq, runKey) {
    const result = await callAPI('dashboard/dagRun', { runKey });
    if (seq !== openSeq || state.selectedRunKey !== runKey) return;
    const run = result?.run;
    if (!run || typeof run !== 'object') {
      throw new Error('DAG run detail missing run');
    }
    if (!Array.isArray(result?.nodes)) {
      throw new Error('DAG run detail missing nodes');
    }
    state.run = run;
    state.nodes = result.nodes;
    state.finalOutput = normalizeFinalOutput(state.run);
  }

  async function selectRun(runKey, seq = openSeq) {
    if (seq !== openSeq) return;
    const selectedKey = (runKey || '').toString().trim();
    const run = state.runs.find((item) => runKeyFromItem(item) === selectedKey) || null;
    state.selectedRunKey = run ? runKeyFromItem(run) : '';
    state.run = run;
    state.finalOutput = normalizeFinalOutput(run);
    state.runsError = null;
    if (!run) {
      state.nodes = Array.isArray(state.templateNodes) ? state.templateNodes : [];
      return;
    }
    const targetRunKey = state.selectedRunKey;
    state.nodes = [];
    try {
      await loadRun(seq, targetRunKey);
    } catch (error) {
      if (seq !== openSeq || state.selectedRunKey !== targetRunKey) return;
      state.run = null;
      state.finalOutput = null;
      state.nodes = [];
      state.runsError = error;
      logWarn('ui', 'useDagDetail.run.failed', { runKey: targetRunKey, error });
    }
  }

  async function loadRuns(seq, dagKey, preferredRunKey = '') {
    state.runsError = null;
    try {
      const runsResult = await callAPI('dashboard/dagRuns', { dagKey, limit: RECENT_RUN_LIMIT });
      if (seq !== openSeq) return;
      state.runs = Array.isArray(runsResult?.runs) ? runsResult.runs : [];
      const preferredKey = (preferredRunKey || '').toString().trim();
      const selected = preferredKey && state.runs.some((item) => runKeyFromItem(item) === preferredKey)
        ? preferredKey
        : runKeyFromItem(state.runs[0]);
      await selectRun(selected, seq);
    } catch (error) {
      if (seq === openSeq) {
        state.runs = [];
        state.run = null;
        state.selectedRunKey = '';
        state.finalOutput = null;
        state.runsError = error;
        logWarn('ui', 'useDagDetail.runs.failed', { dagKey, error });
      }
    }
  }

  async function loadDetail(seq, dagKey, item, preferredRunKey = '') {
    const detail = await callAPI('dashboard/dagDetail', { dagKey });
    if (seq !== openSeq) return;
    state.dag = detail?.dag || item || null;
    state.templateNodes = Array.isArray(detail?.nodes) ? detail.nodes : [];
    state.nodes = state.templateNodes;
    await loadRuns(seq, dagKey, preferredRunKey);
  }

  async function open(item) {
    const seq = ++openSeq;
    resetTransient();
    state.dag = item || null;
    state.templateNodes = [];
    state.nodes = [];
    state.runs = [];
    state.run = null;
    state.selectedRunKey = '';
    state.finalOutput = null;
    state.show = true;
    const dagKey = dagKeyFromItem(item);
    if (!dagKey) {
      state.error = '缺少 DAG key';
      return;
    }
    state.loading = true;
    try {
      await loadDetail(seq, dagKey, item);
    } catch (error) {
      if (seq === openSeq) {
        state.error = error;
        logWarn('ui', 'useDagDetail.open.failed', { dagKey, error });
      }
    } finally {
      if (seq === openSeq) state.loading = false;
    }
  }

  async function start() {
    if (state.starting) return;
    const seq = openSeq;
    const dagKey = dagKeyFromItem(state.dag);
    state.startError = null;
    if (!dagKey) {
      state.startError = new Error('缺少 DAG key');
      return;
    }
    state.starting = true;
    let result;
    try {
      result = await callAPI('dashboard/dagStart', {
        dagKey,
        triggerSource: 'manual',
        idempotencyKey: makeStartIdempotencyKey(dagKey),
      });
    } catch (error) {
      if (seq === openSeq) {
        state.startError = error;
        state.starting = false;
        logWarn('ui', 'useDagDetail.start.failed', { dagKey, error });
      }
      return;
    }
    if (seq !== openSeq) return;
    const runKey = runKeyFromItem(result);
    try {
      await loadDetail(seq, dagKey, state.dag, runKey);
    } catch (error) {
      if (seq === openSeq) {
        state.error = error;
        logWarn('ui', 'useDagDetail.start.refresh_failed', { dagKey, error });
      }
    } finally {
      if (seq === openSeq) state.starting = false;
    }
  }

  async function saveAgentNode(payload) {
    const op = normalizeNodeSavePayload(payload);
    const seq = openSeq;
    const dagKey = dagKeyFromItem(state.dag);
    state.saveError = null;
    if (!dagKey) {
      state.saveError = new Error('缺少 DAG key');
      return;
    }
    const baseVersion = dagVersionFromItem(state.dag);
    if (baseVersion === null) {
      state.saveError = new Error('缺少 DAG version，无法执行 apply_ops');
      return;
    }
    state.savingNodeKey = op.node_key;
    try {
      await callAPI('dashboard/dagApplyOps', {
        dagKey,
        baseVersion,
        ops: [op],
      });
      if (seq !== openSeq) return;
      await loadDetail(seq, dagKey, state.dag, state.selectedRunKey);
    } catch (error) {
      if (seq === openSeq) {
        state.saveError = error;
        logWarn('ui', 'useDagDetail.save_agent_node.failed', { dagKey, nodeKey: op.node_key, error });
      }
    } finally {
      if (seq === openSeq) state.savingNodeKey = '';
    }
  }

  function close() {
    openSeq += 1;
    state.show = false;
    state.starting = false;
  }

  function updateNodeStatus(nodeKey, status) {
    logWarn('ui', 'useDagDetail.updateNodeStatus.not_implemented', { nodeKey, status });
  }

  function handleStatusEvent(payload = {}) {
    const nodeKey = (payload.node_key || payload.nodeKey || '').toString().trim();
    const eventDagKey = (payload.dag_key || payload.dagKey || '').toString().trim();
    const currentDagKey = dagKeyFromItem(state.dag);
    if (eventDagKey && currentDagKey && eventDagKey !== currentDagKey) return;
    const status = (payload.new_status || payload.newStatus || payload.status || '').toString().trim();
    if (!nodeKey || !status || !Array.isArray(state.nodes)) return;
    const node = state.nodes.find((item) => (item?.node_key || item?.nodeKey || '').toString().trim() === nodeKey);
    if (node) node.status = status;
  }

  return { state, open, close, selectRun, start, saveAgentNode, updateNodeStatus, handleStatusEvent };
}
