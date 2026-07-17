// @ts-check

/** @typedef {{ actionId: string, publicError: { code: string, title: string, message: string, diagnosticId: string, retryable: boolean, recoveryActions: readonly string[] }, retry?: () => unknown }} VisibleActionFailure */

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
