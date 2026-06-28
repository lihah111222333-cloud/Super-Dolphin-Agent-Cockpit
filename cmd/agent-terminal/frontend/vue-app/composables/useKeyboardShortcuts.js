/**
 * @param {object} deps
 * @param {import('../../lib/vue.esm-browser.prod.js').Ref<string>} deps.selectedThreadId
 * @param {import('../../lib/vue.esm-browser.prod.js').ComputedRef<boolean>} deps.canInterrupt
 * @param {import('../../lib/vue.esm-browser.prod.js').ComputedRef<boolean>} deps.isStatusTimerModalPaused
 * @param {() => void} deps.stopSelected
 */
export function useKeyboardShortcuts(deps) {
  const {
    selectedThreadId,
    canInterrupt,
    isStatusTimerModalPaused,
    stopSelected,
  } = deps;

  function isEditableElement(node) {
    if (!node || typeof node !== 'object') return false;
    const tag = (node.tagName || '').toString().toLowerCase();
    if (tag === 'input' || tag === 'textarea' || tag === 'select') return true;
    if (Boolean(node.isContentEditable)) return true;
    if (typeof node.closest === 'function') {
      const editableRoot = node.closest('[contenteditable], [contenteditable="true"]');
      if (editableRoot) return true;
    }
    return false;
  }

  function isComposerTextarea(node) {
    if (!node || typeof node !== 'object') return false;
    const tag = (node.tagName || '').toString().toLowerCase();
    if (tag !== 'textarea') return false;
    const id = (node.id || '').toString().trim();
    if (id === 'chatInput') return true;
    if (typeof node.closest === 'function') {
      return Boolean(node.closest('#chat-input-bar'));
    }
    return false;
  }

  function isEscapeKeyEvent(event) {
    const key = (event?.key || '').toString();
    if (key === 'Escape' || key === 'Esc') return true;
    const code = (event?.code || '').toString();
    if (code === 'Escape') return true;
    const keyCode = Number(event?.keyCode || event?.which || 0);
    return keyCode === 27;
  }

  function onGlobalEscape(event) {
    if (!isEscapeKeyEvent(event)) return;
    if (event?.repeat) return;
    if (!selectedThreadId.value) return;
    if (!canInterrupt.value) return;
    if (isStatusTimerModalPaused.value) return;
    const activeEl = typeof document !== 'undefined' ? document.activeElement : null;
    const inComposerTextarea = isComposerTextarea(event?.target) || isComposerTextarea(activeEl);
    if (!inComposerTextarea && (isEditableElement(event?.target) || isEditableElement(activeEl))) return;
    if (event && event.__aoGlobalEscapeHandled) return;
    if (event) event.__aoGlobalEscapeHandled = true;
    if (typeof event?.preventDefault === 'function') event.preventDefault();
    stopSelected();
  }

  return { onGlobalEscape };
}
