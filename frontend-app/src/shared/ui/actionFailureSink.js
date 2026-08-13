// @ts-check

/** @typedef {{ actionId: string, threadId?: string, publicError: { code: string, title: string, message: string, diagnosticId: string, retryable: boolean, recoveryActions: readonly string[] }, retry?: () => unknown }} VisibleActionFailure */

/** @type {VisibleActionFailure | null} */
let activeFailure = null;
/** @type {Set<() => void>} */
const listeners = new Set();

function emit() {
  listeners.forEach((listener) => listener());
}

/** @param {VisibleActionFailure} failure */
export function publishVisibleActionFailure(failure) {
  activeFailure = failure;
  emit();
}

export function clearVisibleActionFailure() {
  activeFailure = null;
  emit();
}

/** @param {readonly string[]} actionIds @param {string | undefined} threadId */
export function clearVisibleActionFailureForActions(actionIds, threadId) {
  if (!activeFailure || activeFailure.threadId !== threadId || !actionIds.includes(activeFailure.actionId)) return false;
  clearVisibleActionFailure();
  return true;
}

/** @param {VisibleActionFailure | null} expectedFailure @param {readonly string[]} actionIds @param {string | undefined} threadId */
export function clearVisibleActionFailureIfCurrent(expectedFailure, actionIds, threadId) {
  if (!expectedFailure || activeFailure !== expectedFailure) return false;
  return clearVisibleActionFailureForActions(actionIds, threadId);
}

/** @param {() => void} listener */
export function subscribeVisibleActionFailure(listener) {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

export function visibleActionFailureSnapshot() {
  return activeFailure;
}

export function resetVisibleActionFailureForTest() {
  clearVisibleActionFailure();
}
