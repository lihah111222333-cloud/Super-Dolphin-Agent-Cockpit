// @ts-nocheck
import { reactive } from '../../lib/vue.esm-browser.prod.js';
import { normalizeThreadID } from './bridge-event-parser.js';

export const compactPendingByThread = reactive({});
export const compactResultByThread = reactive({});
export const compactSuccessCountByThread = reactive({});
export const compactSuccessHideTimerByThread = new Map();
export const compactWaitersByThread = new Map();
export const COMPACT_COMPLETION_TIMEOUT_MS = 180000;
export const COMPACT_SUCCESS_MESSAGE_TTL_MS = 2200;

export function createCompactError(code, threadId, message, extra = {}) {
  const error = new Error(message);
  error.code = code;
  error.threadId = threadId;
  if (extra && typeof extra === 'object') Object.assign(error, extra);
  return error;
}

export function clearCompactSuccessHideTimer(threadId) {
  const id = normalizeThreadID(threadId);
  if (!id) return;
  const timerId = compactSuccessHideTimerByThread.get(id);
  if (timerId == null) return;
  if (typeof window !== 'undefined' && typeof window.clearTimeout === 'function') {
    window.clearTimeout(timerId);
  } else {
    clearTimeout(timerId);
  }
  compactSuccessHideTimerByThread.delete(id);
}

export function scheduleCompactSuccessAutoHide(threadId) {
  const id = normalizeThreadID(threadId);
  if (!id) return;
  clearCompactSuccessHideTimer(id);
  const setTimer = (typeof window !== 'undefined' && typeof window.setTimeout === 'function')
    ? window.setTimeout.bind(window)
    : setTimeout;
  const timerId = setTimer(() => {
    const current = compactResultByThread[id];
    const currentStatus = (current?.status || '').toString().trim().toLowerCase();
    if (current && currentStatus === 'success') {
      compactResultByThread[id] = {
        ...current,
        message: '',
        hidden: true,
        updatedAt: Date.now(),
      };
    }
    compactSuccessHideTimerByThread.delete(id);
  }, COMPACT_SUCCESS_MESSAGE_TTL_MS);
  compactSuccessHideTimerByThread.set(id, timerId);
}

export function setCompactResult(threadId, status, message, extra = {}) {
  const id = normalizeThreadID(threadId);
  if (!id) return;
  clearCompactSuccessHideTimer(id);
  const normalizedStatus = (status || '').toString().trim().toLowerCase();
  const previousStatus = (compactResultByThread[id]?.status || '').toString().trim().toLowerCase();
  const shouldBumpSuccessCount = normalizedStatus === 'success' && previousStatus !== 'success';
  if (shouldBumpSuccessCount) {
    const currentCount = Number(compactSuccessCountByThread[id]);
    compactSuccessCountByThread[id] = Number.isFinite(currentCount) && currentCount > 0
      ? Math.floor(currentCount) + 1
      : 1;
  }
  compactResultByThread[id] = {
    status: normalizedStatus,
    message: (message || '').toString(),
    updatedAt: Date.now(),
    ...extra,
  };
  if (normalizedStatus === 'success') scheduleCompactSuccessAutoHide(id);
}

export function getCompactWaiter(threadId) {
  const id = normalizeThreadID(threadId);
  if (!id) return null;
  return compactWaitersByThread.get(id) || null;
}

export function settleCompactWaiter(threadId, outcome, payload) {
  const id = normalizeThreadID(threadId);
  if (!id) return false;
  const waiter = compactWaitersByThread.get(id);
  if (!waiter) return false;
  compactWaitersByThread.delete(id);
  if (waiter.timeoutID) {
    globalThis.clearTimeout(waiter.timeoutID);
    waiter.timeoutID = 0;
  }
  if (outcome === 'resolve') {
    waiter.resolve(payload || {});
    return true;
  }
  waiter.reject(payload);
  return true;
}

export function cancelCompactWaiter(threadId, reason = 'cancelled') {
  const id = normalizeThreadID(threadId);
  if (!id) return false;
  return settleCompactWaiter(
    id,
    'reject',
    createCompactError('compact_wait_cancelled', id, 'compact_wait_cancelled:' + reason, { reason }),
  );
}

export function waitForCompactCompletion(threadId, timeoutMs = COMPACT_COMPLETION_TIMEOUT_MS) {
  const id = normalizeThreadID(threadId);
  if (!id) {
    return Promise.reject(createCompactError('compact_wait_invalid_thread', '', 'compact_wait_invalid_thread'));
  }
  if (compactWaitersByThread.has(id)) cancelCompactWaiter(id, 'replaced');
  const timeout = Math.max(1000, Number(timeoutMs) || COMPACT_COMPLETION_TIMEOUT_MS);
  return new Promise((resolve, reject) => {
    const timeoutID = globalThis.setTimeout(() => {
      settleCompactWaiter(
        id,
        'reject',
        createCompactError('compact_timeout', id, 'compact_timeout:' + id + ':' + timeout, { timeoutMs: timeout }),
      );
    }, timeout);
    compactWaitersByThread.set(id, {
      resolve,
      reject,
      timeoutID,
      createdAt: Date.now(),
      timeoutMs: timeout,
      compactLifecycleStarted: false,
      compactCommandObserved: false,
    });
  });
}

export function getThreadCompacting(threadId) {
  if (!threadId) return false;
  return Boolean(compactPendingByThread[threadId]);
}

export function getThreadCompactResult(threadId) {
  if (!threadId) return null;
  const value = compactResultByThread[threadId];
  if (!value || typeof value !== 'object') return null;
  return value;
}

export function getThreadCompactSuccessCount(threadId) {
  if (!threadId) return 0;
  const value = Number(compactSuccessCountByThread[threadId]);
  if (!Number.isFinite(value) || value <= 0) return 0;
  return Math.max(0, Math.floor(value));
}
