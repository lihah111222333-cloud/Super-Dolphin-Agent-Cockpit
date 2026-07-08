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

export function attachActiveThreadRpcRuntime(runtime, deps) {
  const { activeThreadInterruptTarget, backendThreadIdForState, cleanObject } = deps;
  const { get, requireCwd, notifyAction, addWarning } = runtime;

  const activeThreadRPC = async (action, rpc) => {
    const currentState = get();
    const requiresActiveTurn = threadActionRequiresActiveTurn(action);
    const activeTurnTarget = requiresActiveTurn ? activeThreadInterruptTarget(currentState) : null;
    const threadId = activeTurnTarget?.threadId || backendThreadIdForState(currentState, currentState.activeThreadId);
    if (!threadId) {
      notifyAction('当前没有可操作的后端线程', 'warning');
      return false;
    }
    try {
      const cwd = requireCwd(action);
      const payload = threadActionPayload({ action, activeThreadInterruptTarget, activeTurnTarget, cleanObject, currentState, cwd, notifyAction, threadId });
      if (!payload) return false;
      const result = await rpc(cleanObject(payload));
      if (notifyThreadActionFailure({ action, addWarning, notifyAction, result, threadId })) return false;
      notifyAction(THREAD_ACTION_SUCCESS_MESSAGES[action] || '线程操作已提交', 'success', { threadId });
      return true;
    }
    catch (error) {
      const message = error?.message || String(error);
      notifyAction(`${THREAD_ACTION_LABELS[action] || '线程操作'}失败：${message}`, 'error', { threadId });
      addWarning('error', `${action}.failed`, { threadId, error: message });
      return false;
    }
  };

  Object.assign(runtime, { activeThreadRPC });
}
