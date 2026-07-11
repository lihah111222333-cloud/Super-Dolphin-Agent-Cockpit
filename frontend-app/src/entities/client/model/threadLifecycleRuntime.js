import { firstOptionalPresent, normalizeOptionalTextField, optionalTextField } from './contractStoreModel.js';

const THREAD_ACTION_LABELS = Object.freeze({ 'thread.interrupt': '中断当前执行', 'thread.force_complete': '强制完成当前执行', 'thread.compact': '压缩上下文', 'thread.recover': '恢复连接' });
const THREAD_ACTION_SUCCESS_MESSAGES = Object.freeze({ 'thread.interrupt': '已发送中断请求', 'thread.force_complete': '已发送强制完成请求', 'thread.compact': '已发送压缩请求', 'thread.recover': '已发送恢复请求' });
function threadActionRequiresActiveTurn(action) { return action === 'thread.interrupt' || action === 'thread.force_complete'; }

function interruptFailureMessage(result) {
  for (const value of [result?.error, result?.message, result?.reason, result?.status, result?.mode]) {
    const message = normalizeOptionalTextField(value); if (message) return message;
  }
  throw new Error('thread.interrupt ok:false response message is required');
}

function forceCompleteFailureMessage(result) {
  const message = optionalTextField(firstOptionalPresent(result?.error, result?.message, result?.errorCode, 'force complete target not found')).trim();
  if (!message) throw new Error('thread.force_complete ok:false response message is required');
  return message;
}

function threadActionPayload(params) {
  const { action, activeThreadInterruptTarget, activeTurnTarget, cleanObject, currentState, cwd, notifyAction, threadId } = params;
  if (!threadActionRequiresActiveTurn(action)) return { cwd, threadId };

  const target = activeTurnTarget || activeThreadInterruptTarget(currentState);
  if (!target.interruptible) {
    notifyAction(action === 'thread.interrupt' ? '当前没有可中断任务' : '当前没有可强制完成任务', 'warning', { threadId });
    return null;
  }
  if (action === 'thread.interrupt') return cleanObject({ cwd, threadId: target.threadId, source: 'ui_stop' });
  return cleanObject({ cwd, threadId: target.threadId });
}

function notifyThreadActionFailure(params) {
  const { action, addWarning, notifyAction, result, threadId } = params;
  if (action === 'thread.interrupt' && result?.ok === false) {
    const message = interruptFailureMessage(result);
    notifyAction(`${THREAD_ACTION_LABELS[action]}失败：${message}`, 'warning', { threadId });
    addWarning('warn', `${action}.failed`, { threadId, error: message });
    return true;
  }
  if (action === 'thread.force_complete' && (result?.ok === false || result?.forceCompleted === false)) {
    const message = forceCompleteFailureMessage(result);
    notifyAction(`${THREAD_ACTION_LABELS[action]}失败：${message}`, 'warning', { threadId, error: message });
    addWarning('warn', `${action}.failed`, { threadId, error: message });
    return true;
  }
  return false;
}

function setThreadRecoveryPending(set, threadId, pending) {
  set((state) => {
    const next = { ...state.threadRecoveryPendingByThread };
    if (pending) next[threadId] = true;
    else delete next[threadId];
    return { threadRecoveryPendingByThread: next };
  });
}

function notifyRecoveryResult(params) {
  const { addWarning, noticeGate, notifyAction, recovered, threadId } = params;
  if (!noticeGate(threadId)) return;
  if (recovered) {
    notifyAction('恢复请求已接受，正在恢复', 'success', { threadId });
    return;
  }
  notifyAction('恢复请求失败', 'warning', { threadId });
  addWarning('warn', 'thread.recover.failed', { threadId });
}

function notifyThreadTransportFailure(params) {
  const { action, addWarning, error, noticeGate, notifyAction, threadId } = params;
  if (noticeGate && !noticeGate(threadId)) return;
  const message = error?.message || String(error);
  notifyAction(`${THREAD_ACTION_LABELS[action] || '线程操作'}失败：${message}`, 'error', { threadId });
  addWarning('error', `${action}.failed`, { threadId, error: message });
}

export function attachActiveThreadRpcRuntime(runtime, deps) {
  const { activeThreadInterruptTarget, backendThreadIdForState, cleanObject } = deps;
  const { get, set, requireCwd, notifyAction, addWarning } = runtime;

  const runActiveThreadRPC = async (action, rpc, options = {}) => {
    const currentState = get();
    const requiresActiveTurn = threadActionRequiresActiveTurn(action);
    const activeTurnTarget = requiresActiveTurn ? activeThreadInterruptTarget(currentState) : null;
    const threadId = options.threadId
      || activeTurnTarget?.threadId
      || backendThreadIdForState(currentState, currentState.activeThreadId);
    if (!threadId) {
      notifyAction('当前没有可操作的后端线程', 'warning');
      return { ok: false, threadId: '', result: null };
    }
    try {
      const cwd = requireCwd(action);
      const payload = threadActionPayload({ action, activeThreadInterruptTarget, activeTurnTarget, cleanObject, currentState, cwd, notifyAction, threadId });
      if (!payload) return { ok: false, threadId, result: null };
      const result = await rpc(cleanObject(payload));
      if (notifyThreadActionFailure({ action, addWarning, notifyAction, result, threadId })) return { ok: false, threadId, result };
      return { ok: true, threadId, result };
    }
    catch (error) {
      notifyThreadTransportFailure({ action, addWarning, error, noticeGate: options.noticeGate, notifyAction, threadId });
      return { ok: false, threadId, result: null };
    }
  };

  const activeThreadRPC = async (action, rpc) => {
    const outcome = await runActiveThreadRPC(action, rpc);
    if (!outcome.ok) return false;
    notifyAction(THREAD_ACTION_SUCCESS_MESSAGES[action] || '线程操作已提交', 'success', { threadId: outcome.threadId });
    return true;
  };

  const recoverActiveThreadRPC = async (rpc) => {
    const currentState = get();
    const threadId = backendThreadIdForState(currentState, currentState.activeThreadId);
    if (!threadId) return activeThreadRPC('thread.recover', rpc);
    if (currentState.threadRecoveryPendingByThread[threadId]) return false;

    setThreadRecoveryPending(set, threadId, true);
    const noticeGate = (targetThreadId) => {
      const state = get();
      return backendThreadIdForState(state, state.activeThreadId) === targetThreadId;
    };
    try {
      const outcome = await runActiveThreadRPC('thread.recover', rpc, { threadId, noticeGate });
      if (!outcome.ok) return false;
      const recovered = outcome.result.recovered === true;
      notifyRecoveryResult({ addWarning, noticeGate, notifyAction, recovered, threadId });
      return recovered;
    }
    finally {
      setThreadRecoveryPending(set, threadId, false);
    }
  };

  Object.assign(runtime, { activeThreadRPC, recoverActiveThreadRPC });
}
