// useDagDetail.js — DAG detail loader for dashboard modal.
import { reactive } from '../../lib/vue.esm-browser.prod.js';
import { callAPI } from '../services/api.js';
import { logWarn } from '../services/log.js';

const RECENT_RUN_LIMIT = 5;
const TERMINABLE_RUN_STATUSES = new Set(['running']);
const TERMINAL_RUN_STATUSES = new Set(['succeeded', 'failed', 'cancelled']);

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

function runIDFromItem(item) {
  const value = item?.run_id ?? item?.runId ?? item?.id;
  if (value === undefined || value === null || value === '') return null;
  const id = Number(value);
  return Number.isFinite(id) && id > 0 ? id : null;
}

function runStatusFromItem(item) {
  return (item?.status || item?.run_status || item?.runStatus || '').toString().trim().toLowerCase();
}

function startExecutionStateFromItem(item) {
  return (item?.execution_state || item?.executionState || '').toString().trim().toLowerCase();
}

function numberFromItem(...values) {
  for (const value of values) {
    if (value === undefined || value === null || value === '') continue;
    const number = Number(value);
    if (Number.isFinite(number)) return number;
  }
  return null;
}

function startWarningFromResult(result) {
  const explicit = (result?.warning || result?.Warning || '').toString().trim();
  if (explicit) return explicit;
  const executionState = startExecutionStateFromItem(result);
  const readyRootNodes = numberFromItem(result?.readyRootNodes, result?.ready_root_nodes);
  const scheduledWakeups = numberFromItem(result?.scheduledWakeups, result?.scheduled_wakeups);
  if (executionState === 'waiting_for_assignee' || (readyRootNodes > 0 && scheduledWakeups === 0)) {
    return '已启动，但根节点还未指派执行代理，等待指派后继续执行。';
  }
  if (executionState === 'no_ready_roots') {
    return '已启动，但暂时没有可调度的根节点。';
  }
  return null;
}

function terminableRunKey(activeRun, selectedRun, candidateRun) {
  for (const run of [activeRun, selectedRun, candidateRun]) {
    if (!TERMINABLE_RUN_STATUSES.has(runStatusFromItem(run))) continue;
    const key = runKeyFromItem(run);
    if (key) return key;
  }
  return '';
}

function runIsTerminal(run) {
  return TERMINAL_RUN_STATUSES.has(runStatusFromItem(run));
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

function scheduleCronExprFromPayload(payload = {}) {
  return (payload.cronExpr || payload.cron_expr || '').toString().trim();
}

function resetTransient(state) {
  state.error = null;
  state.loading = false;
  state.runsError = null;
  state.starting = false;
  state.startError = null;
  state.startWarning = null;
  state.startExecutionState = '';
  state.terminating = false;
  state.terminateError = null;
  state.terminateWarning = null;
  state.deleting = false;
  state.deleteError = null;
  state.scheduling = false;
  state.scheduleError = null;
  state.savingNodeKey = '';
  state.saveError = null;
}

function resetOpenState(state, item) {
  state.dag = item || null;
  state.templateNodes = [];
  state.nodes = [];
  state.runs = [];
  state.activeRun = null;
  state.run = null;
  state.selectedRunKey = '';
  state.finalOutput = null;
  state.show = true;
}

function initialDagDetailState() {
  return {
    show: false,
    loading: false,
    error: null,
    runsError: null,
    dag: null,
    templateNodes: [],
    nodes: [],
    runs: [],
    activeRun: null,
    run: null,
    selectedRunKey: '',
    finalOutput: null,
    starting: false,
    startError: null,
    startWarning: null,
    startExecutionState: '',
    terminating: false,
    terminateError: null,
    terminateWarning: null,
    deleting: false,
    deleteError: null,
    scheduling: false,
    scheduleError: null,
    savingNodeKey: '',
    saveError: null,
  };
}

async function terminateRunFlow(ctx, candidateRun) {
  const { state, getSeq, loadDetail } = ctx;
  if (state.terminating) return;
  const seq = getSeq();
  const dagKey = dagKeyFromItem(state.dag);
  const runKey = terminableRunKey(state.activeRun, state.run, candidateRun);
  state.terminateError = null;
  state.terminateWarning = null;
  if (!dagKey) {
    state.terminateError = new Error('缺少 DAG key');
    return { ok: false, refreshed: false };
  }
  if (!runKey) {
    state.terminateError = new Error('缺少运行中的 run');
    return { ok: false, refreshed: false };
  }
  state.terminating = true;
  try {
    await callAPI('dashboard/dagTerminate', { dagKey, runKey, reason: 'user_requested' });
  } catch (error) {
    return handleTerminateFailure(ctx, seq, dagKey, runKey, error);
  }
  if (seq !== getSeq()) return { ok: false, refreshed: false };
  try {
    await loadDetail(seq, dagKey, state.dag, runKey);
  } catch (error) {
    if (seq === getSeq()) {
      state.error = error;
      logWarn('ui', 'useDagDetail.terminate.refresh_failed', { dagKey, runKey, error });
    }
  } finally {
    if (seq === getSeq()) state.terminating = false;
  }
  return { ok: true, refreshed: true };
}

async function handleTerminateFailure(ctx, seq, dagKey, runKey, error) {
  const { state, getSeq, loadDetail } = ctx;
  let refreshed = false;
  if (seq === getSeq()) {
    logWarn('ui', 'useDagDetail.terminate.failed', { dagKey, runKey, error });
    try {
      await loadDetail(seq, dagKey, state.dag, runKey);
      refreshed = true;
    } catch (refreshError) {
      if (seq === getSeq()) {
        state.error = refreshError;
        logWarn('ui', 'useDagDetail.terminate.error_refresh_failed', { dagKey, runKey, error: refreshError });
      }
    }
    if (seq === getSeq()) {
      if (runIsTerminal(state.run)) {
        state.terminateWarning = error;
        state.terminateError = null;
      } else {
        state.terminateError = error;
      }
      state.terminating = false;
    }
  }
  return { ok: seq === getSeq() && runIsTerminal(state.run), refreshed };
}

function scheduleApplyOps(cronExpr) {
  return [{
    op: 'update_dag',
    patch: {
      trigger: 'scheduled',
      cron_expr: cronExpr,
    },
  }];
}

function scheduleEnabledApplyOps(enabled) {
  return [{
    op: 'update_dag',
    patch: {
      schedule_enabled: Boolean(enabled),
    },
  }];
}

function applyOpsBaseVersion(state) {
  const baseVersion = dagVersionFromItem(state.dag);
  if (baseVersion === null) {
    return { baseVersion: null, error: new Error('缺少 DAG version，无法执行 apply_ops') };
  }
  return { baseVersion, error: null };
}

async function setScheduleFlow(ctx, payload = {}) {
  const { state, getSeq, loadDetail } = ctx;
  if (state.scheduling) return { ok: false };
  const seq = getSeq();
  const dagKey = dagKeyFromItem(state.dag);
  const cronExpr = scheduleCronExprFromPayload(payload);
  state.scheduleError = null;
  if (!dagKey) {
    state.scheduleError = new Error('缺少 DAG key');
    return { ok: false };
  }
  if (!cronExpr) {
    state.scheduleError = new Error('缺少 cron 表达式');
    return { ok: false };
  }
  const { baseVersion, error: versionError } = applyOpsBaseVersion(state);
  if (versionError) {
    state.scheduleError = versionError;
    return { ok: false };
  }
  state.scheduling = true;
  try {
    await callAPI('dashboard/dagApplyOps', { dagKey, baseVersion, ops: scheduleApplyOps(cronExpr) });
    if (seq !== getSeq()) return { ok: false };
    await loadDetail(seq, dagKey, state.dag, state.selectedRunKey);
    return { ok: true };
  } catch (error) {
    if (seq === getSeq()) {
      state.scheduleError = error;
      logWarn('ui', 'useDagDetail.set_schedule.failed', { dagKey, error });
    }
    return { ok: false };
  } finally {
    if (seq === getSeq()) state.scheduling = false;
  }
}

async function setScheduleEnabledFlow(ctx, payload = {}) {
  const { state, getSeq, loadDetail } = ctx;
  if (state.scheduling) return { ok: false };
  const seq = getSeq();
  const dagKey = dagKeyFromItem(state.dag);
  state.scheduleError = null;
  if (!dagKey) {
    state.scheduleError = new Error('缺少 DAG key');
    return { ok: false };
  }
  const { baseVersion, error: versionError } = applyOpsBaseVersion(state);
  if (versionError) {
    state.scheduleError = versionError;
    return { ok: false };
  }
  state.scheduling = true;
  try {
    await callAPI('dashboard/dagApplyOps', { dagKey, baseVersion, ops: scheduleEnabledApplyOps(payload.enabled) });
    if (seq !== getSeq()) return { ok: false };
    await loadDetail(seq, dagKey, state.dag, state.selectedRunKey);
    return { ok: true };
  } catch (error) {
    if (seq === getSeq()) {
      state.scheduleError = error;
      logWarn('ui', 'useDagDetail.set_schedule_enabled.failed', { dagKey, error });
    }
    return { ok: false };
  } finally {
    if (seq === getSeq()) state.scheduling = false;
  }
}

export function useDagDetail() {
  let openSeq = 0;
  const state = reactive(initialDagDetailState());

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
    state.activeRun = null;
    try {
      const runsResult = await callAPI('dashboard/dagRuns', { dagKey, limit: RECENT_RUN_LIMIT });
      if (seq !== openSeq) return;
      state.runs = Array.isArray(runsResult?.runs) ? runsResult.runs : [];
      const activeResult = await callAPI('dashboard/dagRuns', { dagKey, status: 'running', limit: 1 });
      if (seq !== openSeq) return;
      const activeRuns = Array.isArray(activeResult?.runs) ? activeResult.runs : [];
      state.activeRun = activeRuns[0] || null;
      const preferredKey = (preferredRunKey || '').toString().trim();
      const selected = preferredKey && state.runs.some((item) => runKeyFromItem(item) === preferredKey)
        ? preferredKey
        : runKeyFromItem(state.runs[0]);
      await selectRun(selected, seq);
    } catch (error) {
      if (seq === openSeq) {
        state.runs = [];
        state.activeRun = null;
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
    resetTransient(state);
    resetOpenState(state, item);
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
    state.startWarning = null;
    state.startExecutionState = '';
    state.terminateError = null;
    state.terminateWarning = null;
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
    state.startExecutionState = startExecutionStateFromItem(result);
    state.startWarning = startWarningFromResult(result);
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

  async function terminateActiveRun(candidateRun = null) {
    return terminateRunFlow({ state, getSeq: () => openSeq, loadDetail }, candidateRun);
  }

  async function deleteDAG() {
    if (state.deleting) return { ok: false };
    const seq = openSeq;
    const dagKey = dagKeyFromItem(state.dag);
    state.deleteError = null;
    if (!dagKey) {
      state.deleteError = new Error('缺少 DAG key');
      return { ok: false };
    }
    state.deleting = true;
    try {
      await callAPI('dashboard/dagDelete', { dagKey });
      if (seq !== openSeq) return { ok: false };
      close();
      return { ok: true };
    } catch (error) {
      if (seq === openSeq) {
        state.deleteError = error;
        logWarn('ui', 'useDagDetail.delete.failed', { dagKey, error });
      }
      return { ok: false };
    } finally {
      if (seq === openSeq) state.deleting = false;
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

  async function setSchedule(payload = {}) {
    return setScheduleFlow({ state, getSeq: () => openSeq, loadDetail }, payload);
  }

  async function setScheduleEnabled(payload = {}) {
    return setScheduleEnabledFlow({ state, getSeq: () => openSeq, loadDetail }, payload);
  }

  function close() {
    openSeq += 1;
    state.show = false;
    state.starting = false;
    state.terminating = false;
    state.deleting = false;
    state.scheduling = false;
  }

  function updateNodeStatus(nodeKey, status) {
    logWarn('ui', 'useDagDetail.updateNodeStatus.not_implemented', { nodeKey, status });
  }

  function handleStatusEvent(payload = {}) {
    const nodeKey = (payload.node_key || payload.nodeKey || '').toString().trim();
    const eventDagKey = (payload.dag_key || payload.dagKey || '').toString().trim();
    const currentDagKey = dagKeyFromItem(state.dag);
    if (eventDagKey && currentDagKey && eventDagKey !== currentDagKey) return;
    const eventRunKey = runKeyFromItem(payload);
    const currentRunKey = runKeyFromItem(state.run) || state.selectedRunKey;
    const eventRunID = runIDFromItem(payload);
    const currentRunID = runIDFromItem(state.run);
    const canCompareRunKey = Boolean(eventRunKey && currentRunKey);
    const canCompareRunID = eventRunID !== null && currentRunID !== null;
    if (!canCompareRunKey && !canCompareRunID) return;
    if (canCompareRunKey && eventRunKey !== currentRunKey) return;
    if (canCompareRunID && eventRunID !== currentRunID) return;
    const status = (payload.new_status || payload.newStatus || payload.status || '').toString().trim();
    if (!nodeKey || !status || !Array.isArray(state.nodes)) return;
    const node = state.nodes.find((item) => (item?.node_key || item?.nodeKey || '').toString().trim() === nodeKey);
    if (node) node.status = status;
  }

  return { state, open, close, selectRun, start, terminateActiveRun, deleteDAG, saveAgentNode, setSchedule, setScheduleEnabled, updateNodeStatus, handleStatusEvent };
}
