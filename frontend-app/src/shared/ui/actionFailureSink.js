let activeFailure = null;
const listeners = new Set();

function emit() {
  listeners.forEach((listener) => listener());
}

export function publishVisibleActionFailure(failure) {
  activeFailure = failure;
  emit();
}

export function clearVisibleActionFailure() {
  activeFailure = null;
  emit();
}

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
