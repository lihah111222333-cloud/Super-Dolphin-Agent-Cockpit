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

function runKeyFromItem(item) {
  return (item?.run_key || item?.runKey || item?.key || item?.id || '').toString().trim();
}

function makeStartIdempotencyKey(dagKey) {
  return `dag-start:${dagKey}:${Date.now()}:${Math.random().toString(36).slice(2)}`;
}

export function useDagDetail() {
  let openSeq = 0;
  const state = reactive({
    show: false,
    loading: false,
    error: null,
    runsError: null,
    dag: null,
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

  function selectRun(runKey) {
    const selectedKey = (runKey || '').toString().trim();
    const run = state.runs.find((item) => runKeyFromItem(item) === selectedKey) || null;
    state.selectedRunKey = run ? runKeyFromItem(run) : '';
    state.run = run;
    state.finalOutput = normalizeFinalOutput(run);
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
      selectRun(selected);
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
    state.nodes = Array.isArray(detail?.nodes) ? detail.nodes : [];
    await loadRuns(seq, dagKey, preferredRunKey);
  }

  async function open(item) {
    const seq = ++openSeq;
    resetTransient();
    state.dag = item || null;
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

  return { state, open, close, selectRun, start, updateNodeStatus, handleStatusEvent };
}
