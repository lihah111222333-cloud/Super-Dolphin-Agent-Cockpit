import { firstOptionalPresent, normalizeOptionalTextField, optionalTextField } from './contractStoreModel.js';

const THREAD_ACTION_LABELS = Object.freeze({ 'thread.interrupt': '中断当前执行', 'thread.force_complete': '强制完成当前执行', 'thread.compact': '压缩上下文', 'thread.recover': '恢复连接' });
const THREAD_ACTION_SUCCESS_MESSAGES = Object.freeze({ 'thread.interrupt': '已发送中断请求', 'thread.force_complete': '已发送强制完成请求', 'thread.compact': '已发送压缩请求', 'thread.recover': '已发送恢复请求' });
export const INTERRUPT_RPC_TIMEOUT_MS = 15_000;
const INTERRUPT_RPC_TIMEOUT_CODE = 'THREAD_INTERRUPT_RPC_TIMEOUT';
const INTERRUPT_PENDING_MESSAGE = '正在请求停止，尚未确认，任务可能仍在运行';
const INTERRUPT_UNCONFIRMED_MESSAGE = '停止未确认，任务可能仍在运行';
function threadActionRequiresActiveTurn(action) { return action === 'thread.interrupt' || action === 'thread.force_complete'; }

export function createStopRequestId() {
  const randomUUID = globalThis.crypto?.randomUUID;
  if (typeof randomUUID !== 'function') throw new Error('thread.interrupt: secure request id generator is required');
  const requestId = normalizeOptionalTextField(randomUUID.call(globalThis.crypto));
  if (!requestId) throw new Error('thread.interrupt: request id generator returned an empty value');
  return requestId;
}

function interruptResponseContractError(field) {
  const error = new TypeError(`thread.interrupt response contract violation: ${field}`);
  error.code = 'THREAD_INTERRUPT_RESPONSE_CONTRACT';
  return error;
}

function requireInterruptResponseText(result, field, expected) {
  const actual = result[field];
  if (typeof actual !== 'string' || actual === '' || actual !== expected) {
    throw interruptResponseContractError(field);
  }
}

function requireInterruptResponseNonEmptyText(result, field) {
  if (typeof result[field] !== 'string' || result[field] === '') throw interruptResponseContractError(field);
}

const INTERRUPT_RESPONSE_FIELDS = new Set(['ok', 'accepted', 'requestId', 'expectedTurnId', 'errorCode', 'turnId', 'status', 'confirmed', 'mode', 'interruptSent', 'stateBefore', 'stateAfter', 'waitedMs', 'activeObserved']);

function requireInterruptResponseBoolean(result, field, expected) {
  if (result[field] !== expected) throw interruptResponseContractError(field);
}

function requireInterruptResponseInteger(result, field) {
  if (!Number.isInteger(result[field]) || result[field] < 0) throw interruptResponseContractError(field);
}

function requireInterruptResponseObservation(result) {
  requireInterruptResponseInteger(result, 'waitedMs');
  requireInterruptResponseBoolean(result, 'activeObserved', true);
}

function rejectInterruptResponseObservation(result) {
  if (Object.hasOwn(result, 'waitedMs') || Object.hasOwn(result, 'activeObserved')) throw interruptResponseContractError('observation');
}

function assertInterruptResponseFields(result) {
  for (const field of Object.keys(result)) {
    if (!INTERRUPT_RESPONSE_FIELDS.has(field)) throw interruptResponseContractError(`unknown ${field}`);
  }
}

function requireAcceptedInterruptIdentity(result, request) {
  requireInterruptResponseText(result, 'expectedTurnId', request.expectedTurnId);
  requireInterruptResponseText(result, 'requestId', request.requestId);
  requireInterruptResponseText(result, 'turnId', request.expectedTurnId);
}

function validateAcceptedInterruptResult(result, request) {
  requireInterruptResponseBoolean(result, 'accepted', true);
  if (Object.hasOwn(result, 'errorCode')) throw interruptResponseContractError('errorCode');
  requireAcceptedInterruptIdentity(result, request);
  if (result.mode === 'interrupt_confirmed') {
    requireInterruptResponseBoolean(result, 'ok', true);
    requireInterruptResponseText(result, 'status', 'interrupted');
    requireInterruptResponseBoolean(result, 'confirmed', true);
    requireInterruptResponseBoolean(result, 'interruptSent', true);
    requireInterruptResponseText(result, 'stateBefore', 'running');
    requireInterruptResponseText(result, 'stateAfter', 'idle');
    return requireInterruptResponseObservation(result);
  }
  if (result.mode === 'interrupt_terminal_completed') {
    requireInterruptResponseBoolean(result, 'ok', true);
    requireInterruptResponseText(result, 'status', 'completed');
    requireInterruptResponseBoolean(result, 'confirmed', false);
    requireInterruptResponseBoolean(result, 'interruptSent', true);
    requireInterruptResponseText(result, 'stateBefore', 'running');
    requireInterruptResponseText(result, 'stateAfter', 'idle');
    return requireInterruptResponseObservation(result);
  }
  if (result.mode === 'interrupt_terminal_failed') {
    requireInterruptResponseBoolean(result, 'ok', true);
    if (!['failed', 'stalled'].includes(result.status)) throw interruptResponseContractError('status');
    requireInterruptResponseBoolean(result, 'confirmed', false);
    requireInterruptResponseBoolean(result, 'interruptSent', true);
    requireInterruptResponseText(result, 'stateBefore', 'running');
    requireInterruptResponseText(result, 'stateAfter', 'error');
    return requireInterruptResponseObservation(result);
  }
  if (result.mode === 'interrupt_timeout') {
    requireInterruptResponseBoolean(result, 'ok', false);
    if (!['running', 'interrupting'].includes(result.status)) throw interruptResponseContractError('status');
    requireInterruptResponseBoolean(result, 'confirmed', true);
    requireInterruptResponseBoolean(result, 'interruptSent', true);
    requireInterruptResponseText(result, 'stateBefore', 'running');
    requireInterruptResponseText(result, 'stateAfter', 'running');
    return requireInterruptResponseObservation(result);
  }
  if (result.mode === 'no_active_turn') {
    requireInterruptResponseBoolean(result, 'ok', true);
    requireInterruptResponseNonEmptyText(result, 'status');
    requireInterruptResponseBoolean(result, 'confirmed', false);
    requireInterruptResponseBoolean(result, 'interruptSent', false);
    if (result.stateBefore !== result.stateAfter || !['running', 'idle', 'error'].includes(result.stateBefore)) throw interruptResponseContractError('state');
    return rejectInterruptResponseObservation(result);
  }
  throw interruptResponseContractError('mode');
}

function validateInterruptRejectedResult(result, request) {
  requireInterruptResponseBoolean(result, 'accepted', false);
  requireInterruptResponseText(result, 'expectedTurnId', request.expectedTurnId);
  requireInterruptResponseBoolean(result, 'confirmed', false);
  requireInterruptResponseBoolean(result, 'interruptSent', false);
  rejectInterruptResponseObservation(result);
  if (result.mode === 'not_applied') {
    requireInterruptResponseBoolean(result, 'ok', false);
    requireInterruptResponseText(result, 'errorCode', 'NOT_APPLIED');
    if (Object.hasOwn(result, 'requestId')) requireInterruptResponseNonEmptyText(result, 'requestId');
    requireInterruptResponseText(result, 'turnId', request.expectedTurnId);
    requireInterruptResponseNonEmptyText(result, 'status');
    requireInterruptResponseText(result, 'stateBefore', 'running');
    return requireInterruptResponseText(result, 'stateAfter', 'running');
  }
  if (result.errorCode !== 'TARGET_CHANGED') throw interruptResponseContractError('errorCode');
  requireInterruptResponseBoolean(result, 'ok', false);
  requireInterruptResponseText(result, 'requestId', request.requestId);
  if (result.mode !== '' || result.stateBefore !== '' || result.stateAfter !== '') throw interruptResponseContractError('target changed state');
  const hasTurn = Object.hasOwn(result, 'turnId');
  const hasStatus = Object.hasOwn(result, 'status');
  if (hasTurn !== hasStatus) throw interruptResponseContractError('target snapshot');
  if (hasTurn && (typeof result.turnId !== 'string' || result.turnId === '' || result.turnId === request.expectedTurnId || typeof result.status !== 'string' || result.status === '')) throw interruptResponseContractError('target snapshot');
}

function validateInterruptResponse(result, request) {
  if (result == null || typeof result !== 'object' || Array.isArray(result)) {
    throw interruptResponseContractError('object');
  }
  assertInterruptResponseFields(result);
  if (result.accepted === true) return validateAcceptedInterruptResult(result, request);
  return validateInterruptRejectedResult(result, request);
}

function interruptTimeoutError() {
  const error = new Error('thread.interrupt RPC timed out before confirmation');
  error.code = INTERRUPT_RPC_TIMEOUT_CODE;
  return error;
}

async function interruptWithinTimeoutForPublisher(publishAction, rpc, payload) {
  const rpcPromise = Promise.resolve(rpc(payload));
  let outcome;
  let fulfilled = false;
  let settled = false;
  void rpcPromise.then(
    (result) => {
      fulfilled = true;
      outcome = result;
      settled = true;
    },
    (error) => {
      outcome = error;
      settled = true;
    },
  );
  await Promise.resolve();
  if (settled) {
    if (!fulfilled) throw outcome;
    return outcome;
  }

  let timeoutId;
  const pendingTimer = globalThis.setTimeout(
    publishAction,
    0,
    INTERRUPT_PENDING_MESSAGE,
    'info',
    { threadId: payload.threadId },
  );
  try {
    return await Promise.race([
      rpcPromise,
      new Promise((_, reject) => {
        timeoutId = globalThis.setTimeout(() => reject(interruptTimeoutError()), INTERRUPT_RPC_TIMEOUT_MS);
      }),
    ]);
  }
  finally {
    globalThis.clearTimeout(timeoutId);
    globalThis.clearTimeout(pendingTimer);
  }
}

function forceCompleteFailureMessage(result) {
  const message = optionalTextField(firstOptionalPresent(result?.error, result?.message, result?.errorCode, 'force complete target not found')).trim();
  if (!message) throw new Error('thread.force_complete ok:false response message is required');
  return message;
}

function threadActionPayload(params) {
  const { action, activeThreadInterruptTarget, activeTurnTarget, cleanObject, createRequestId, currentState, cwd, notifyAction, threadId } = params;
  if (!threadActionRequiresActiveTurn(action)) return { cwd, threadId };

  const target = activeTurnTarget || activeThreadInterruptTarget(currentState);
  if (!target.interruptible) {
    notifyAction(action === 'thread.interrupt' ? '当前没有可中断任务' : '当前没有可强制完成任务', 'warning', { threadId });
    return null;
  }
  if (action === 'thread.interrupt') {
    const expectedTurnId = normalizeOptionalTextField(target.turnId);
    if (!expectedTurnId) throw new Error('thread.interrupt: expectedTurnId is required');
    const requestId = normalizeOptionalTextField(createRequestId());
    if (!requestId) throw new Error('thread.interrupt: requestId is required');
    return { cwd, threadId: target.threadId, expectedTurnId, requestId, source: 'ui_stop' };
  }
  return cleanObject({ cwd, threadId: target.threadId });
}

function notifyThreadActionFailure(params) {
  const { action, addWarning, notifyAction, result, threadId } = params;
  if (action === 'thread.interrupt' && result?.ok === false) {
    if (result.mode === 'interrupt_timeout') {
      notifyAction(INTERRUPT_UNCONFIRMED_MESSAGE, 'warning', { threadId });
      addWarning('warn', `${action}.unconfirmed`, { threadId, error: 'stop confirmation timed out; see Health diagnostic ID' });
      return true;
    }
    if (result.mode === 'not_applied') {
      notifyAction('中断未应用，请重试。', 'warning', { threadId });
      addWarning('warn', `${action}.not_applied`, { threadId, error: 'stop was not applied; see Health diagnostic ID' });
      return true;
    }
    if (result.errorCode === 'TARGET_CHANGED') {
      notifyAction('当前任务已切换，请重试。', 'warning', { threadId });
      addWarning('warn', `${action}.target_changed`, { threadId, error: 'stop target changed; see Health diagnostic ID' });
      return true;
    }
    notifyAction('中断当前执行失败，请重试。', 'warning', { threadId });
    addWarning('warn', `${action}.failed`, { threadId, error: 'action failure; see Health diagnostic ID' });
    return true;
  }
  if (action === 'thread.force_complete' && (result?.ok === false || result?.forceCompleted === false)) {
    forceCompleteFailureMessage(result);
    notifyAction(`${THREAD_ACTION_LABELS[action]}失败，请重试。`, 'warning', { threadId });
    addWarning('warn', `${action}.failed`, { threadId, error: 'action failure; see Health diagnostic ID' });
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
  const { action, addWarning, error: _, noticeGate, notifyAction, threadId } = params;
  if (noticeGate && !noticeGate(threadId)) return;
  if (action === 'thread.interrupt') {
    notifyAction(INTERRUPT_UNCONFIRMED_MESSAGE, 'warning', { threadId });
    addWarning('error', `${action}.unconfirmed`, { threadId, error: 'stop confirmation unavailable; see Health diagnostic ID' });
    return;
  }
  notifyAction(`${THREAD_ACTION_LABELS[action] || '线程操作'}失败，请重试。`, 'error', { threadId });
  addWarning('error', `${action}.failed`, { threadId, error: 'action failure; see Health diagnostic ID' });
}

export function attachActiveThreadRpcRuntime(runtime, deps) {
  const { activeThreadInterruptTarget, backendThreadIdForState, cleanObject, createRequestId } = deps;
  const { get, set, requireCwd, notifyAction: publishAction, addWarning } = runtime;
  const interruptWithinTimeout = interruptWithinTimeoutForPublisher.bind(null, publishAction);
  const notifyAction = (message, tone, details) => {
    if (message === INTERRUPT_PENDING_MESSAGE && tone === 'info' && details?.request) {
      return;
    }
    publishAction(message, tone, details);
  };

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
      const payload = threadActionPayload({ action, activeThreadInterruptTarget, activeTurnTarget, cleanObject, createRequestId, currentState, cwd, notifyAction, threadId });
      if (!payload) return { ok: false, threadId, result: null };
      const request = cleanObject(payload);
      if (action === 'thread.interrupt') notifyAction(INTERRUPT_PENDING_MESSAGE, 'info', { request, threadId });
      const result = action === 'thread.interrupt'
        ? await interruptWithinTimeout(rpc, request)
        : await rpc(request);
      if (action === 'thread.interrupt') validateInterruptResponse(result, request);
      if (notifyThreadActionFailure({ action, addWarning, notifyAction, result, threadId })) return { ok: false, threadId, result };
      return { ok: true, threadId, result };
    }
    catch (error) {
      notifyThreadTransportFailure({ action, addWarning, error, noticeGate: options.noticeGate, notifyAction, threadId });
      throw error;
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
