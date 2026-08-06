import { assertOnlyResponseKeys } from '../../../shared/api/backendResponseValidatorShared.js';
import { firstOptionalPresent, normalizeOptionalTextField, optionalTextField } from './contractStoreModel.js';

const THREAD_ACTION_LABELS = Object.freeze({ 'thread.interrupt': '中断当前执行', 'thread.force_complete': '强制完成当前执行', 'thread.compact': '压缩上下文', 'thread.recover': '恢复连接' });
const THREAD_ACTION_SUCCESS_MESSAGES = Object.freeze({ 'thread.interrupt': '已发送中断请求', 'thread.force_complete': '已发送强制完成请求', 'thread.compact': '已发送压缩请求', 'thread.recover': '已发送恢复请求' });
export const INTERRUPT_RPC_TIMEOUT_MS = 15_000;
const INTERRUPT_RPC_TIMEOUT_CODE = 'THREAD_INTERRUPT_RPC_TIMEOUT';
const INTERRUPT_PENDING_MESSAGE = '正在请求停止，尚未确认，任务可能仍在运行';
const INTERRUPT_UNCONFIRMED_MESSAGE = '停止未确认，任务可能仍在运行';
const INTERRUPT_SUCCESS_RESPONSE_KEYS = new Set(['ok', 'accepted', 'requestId', 'expectedTurnId', 'turnId', 'status', 'confirmed', 'mode', 'interruptSent', 'stateBefore', 'stateAfter', 'waitedMs', 'activeObserved']);
const INTERRUPT_FAILURE_RESPONSE_KEYS = new Set([...INTERRUPT_SUCCESS_RESPONSE_KEYS, 'errorCode', 'error', 'message', 'reason']);
function threadActionRequiresActiveTurn(action) { return action === 'thread.interrupt' || action === 'thread.force_complete'; }

function interruptSingleFlightKey(target) {
  if (!target?.interruptible) return '';
  const threadId = normalizeOptionalTextField(target.threadId);
  const turnId = normalizeOptionalTextField(target.turnId);
  return threadId && turnId ? `${threadId}\u0000${turnId}` : '';
}

export function createStopRequestId() {
  const randomUUID = globalThis.crypto?.randomUUID;
  if (typeof randomUUID !== 'function') throw new Error('thread.interrupt: secure request id generator is required');
  const requestId = normalizeOptionalTextField(randomUUID.call(globalThis.crypto));
  if (!requestId) throw new Error('thread.interrupt: request id generator returned an empty value');
  return requestId;
}

function interruptFailureMessage(result) {
  for (const value of [result?.error, result?.message, result?.reason, result?.status, result?.mode]) {
    const message = normalizeOptionalTextField(value); if (message) return message;
  }
  throw new Error('thread.interrupt ok:false response message is required');
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

function validateInterruptFailureResponse(result) {
  if (result == null || typeof result !== 'object' || Array.isArray(result)) {
    throw interruptResponseContractError('object');
  }
  if (result.ok !== false) throw interruptResponseContractError('ok');
  assertOnlyResponseKeys('thread.interrupt', result, INTERRUPT_FAILURE_RESPONSE_KEYS, 'body');
}

function validateInterruptSuccessResponse(result, request) {
  if (result == null || typeof result !== 'object' || Array.isArray(result)) {
    throw interruptResponseContractError('object');
  }
  assertOnlyResponseKeys('thread.interrupt', result, INTERRUPT_SUCCESS_RESPONSE_KEYS, 'body');
  if (result.ok !== true) throw interruptResponseContractError('ok');
  if (result.accepted !== true) throw interruptResponseContractError('accepted');
  requireInterruptResponseText(result, 'requestId', request.requestId);
  requireInterruptResponseText(result, 'expectedTurnId', request.expectedTurnId);
  requireInterruptResponseText(result, 'turnId', request.expectedTurnId);
  requireInterruptResponseText(result, 'status', 'interrupted');
  requireInterruptResponseText(result, 'mode', 'interrupt_confirmed');
  requireInterruptResponseText(result, 'stateBefore', 'running');
  requireInterruptResponseText(result, 'stateAfter', 'idle');
  if (result.confirmed !== true) throw interruptResponseContractError('confirmed');
  if (result.interruptSent !== true) throw interruptResponseContractError('interruptSent');
  if (result.activeObserved !== true) throw interruptResponseContractError('activeObserved');
  if (!Number.isInteger(result.waitedMs) || result.waitedMs < 0) {
    throw interruptResponseContractError('waitedMs');
  }
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
    validateInterruptFailureResponse(result);
    interruptFailureMessage(result);
    if (result.mode === 'interrupt_timeout') {
      notifyAction(INTERRUPT_UNCONFIRMED_MESSAGE, 'warning', { threadId });
      addWarning('warn', `${action}.unconfirmed`, { threadId, error: 'stop confirmation timed out; see Health diagnostic ID' });
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
  const interruptFlights = new Map();

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
      if (notifyThreadActionFailure({ action, addWarning, notifyAction, result, threadId })) return { ok: false, threadId, result };
      if (action === 'thread.interrupt') validateInterruptSuccessResponse(result, request);
      return { ok: true, threadId, result };
    }
    catch (error) {
      notifyThreadTransportFailure({ action, addWarning, error, noticeGate: options.noticeGate, notifyAction, threadId });
      throw error;
    }
  };

  const executeActiveThreadRPC = async (action, rpc) => {
    const outcome = await runActiveThreadRPC(action, rpc);
    if (!outcome.ok) return false;
    notifyAction(THREAD_ACTION_SUCCESS_MESSAGES[action] || '线程操作已提交', 'success', { threadId: outcome.threadId });
    return true;
  };

  const activeThreadRPC = (action, rpc) => {
    if (action !== 'thread.interrupt') return executeActiveThreadRPC(action, rpc);
    const key = interruptSingleFlightKey(activeThreadInterruptTarget(get()));
    if (!key) return executeActiveThreadRPC(action, rpc);
    const existing = interruptFlights.get(key);
    if (existing) return existing.actionPromise;

    const actionPromise = executeActiveThreadRPC(action, rpc);
    const flight = { actionPromise };
    interruptFlights.set(key, flight);
    const releaseSettledFlight = () => {
      if (interruptFlights.get(key) === flight) interruptFlights.delete(key);
    };
    void actionPromise.then(
      releaseSettledFlight,
      releaseSettledFlight,
    );
    return actionPromise;
  };

  const recoverActiveThreadRPC = async (rpc) => {
    const currentState = get();
    const threadId = backendThreadIdForState(currentState, currentState.activeThreadId);
    if (!threadId) return executeActiveThreadRPC('thread.recover', rpc);
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
