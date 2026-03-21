import {
  ref,
  nextTick,
} from '../../lib/vue.esm-browser.prod.js';

/**
 * @param {object} props
 * @param {import('../../lib/vue.esm-browser.prod.js').ComputedRef<any[]>} visibleChatThreadCards
 * @param {(threadId: string) => void} selectThread
 */
export function useInlineRename(props, visibleChatThreadCards, selectThread) {
  const editingThreadId = ref('');
  const editingAlias = ref('');
  const renamingThreadId = ref('');
  const renameInputRefByThread = new Map();

  function setRenameInputRef(threadId, el) {
    const id = (threadId || '').toString().trim();
    if (!id) return;
    if (!el) {
      renameInputRefByThread.delete(id);
      return;
    }
    renameInputRefByThread.set(id, el);
  }


  function beginInlineRename(threadId) {
    const id = (threadId || '').toString().trim();
    if (!id) return;
    const target = visibleChatThreadCards.value.find((item) => item.id === id);
    const current = (target?.name || id).toString().trim() || id;
    editingThreadId.value = id;
    editingAlias.value = current;
    renamingThreadId.value = '';
    selectThread(id);
    nextTick(() => {
      const input = renameInputRefByThread.get(id);
      if (!input) return;
      input.focus();
      input.select();
    });
  }

  function cancelInlineRename(threadId = '') {
    const id = (threadId || editingThreadId.value || '').toString().trim();
    if (!id || editingThreadId.value !== id) return;
    editingThreadId.value = '';
    editingAlias.value = '';
    renamingThreadId.value = '';
  }


  function handleInlineRenameEnter(event, threadId) {
    const id = (threadId || editingThreadId.value || '').toString().trim();
    if (!id || editingThreadId.value !== id) return;
    const isComposing = Boolean(event?.isComposing || event?.keyCode === 229 || event?.which === 229);
    if (isComposing) return;
    if (typeof event?.preventDefault === 'function') {
      event.preventDefault();
    }
    submitInlineRename(id);
  }


  async function submitInlineRename(threadId) {
    const id = (threadId || editingThreadId.value || '').toString().trim();
    if (!id || editingThreadId.value !== id || renamingThreadId.value === id) return;

    const target = visibleChatThreadCards.value.find((item) => item.id === id);
    const current = (target?.name || id).toString().trim() || id;
    const nextName = (editingAlias.value || '').toString().trim();
    if (!nextName || nextName === current) {
      cancelInlineRename(id);
      return;
    }

    renamingThreadId.value = id;
    try {
      if (typeof props.threadStore.renameThread === 'function') {
        await props.threadStore.renameThread(id, nextName);
      } else if (typeof props.threadStore.promptRenameThread === 'function') {
        props.threadStore.promptRenameThread(id);
      }
      cancelInlineRename(id);
    } catch {
      renamingThreadId.value = '';
      nextTick(() => {
        const input = renameInputRefByThread.get(id);
        if (!input) return;
        input.focus();
        input.select();
      });
    }
  }

  function handleInlineRenameBlur(event, threadId) {
    const id = (threadId || '').toString().trim();
    if (!id || editingThreadId.value !== id) return;
    const related = event?.relatedTarget;
    const keepForSaveButton = ((related?.dataset?.renameSaveButtonFor || '').toString().trim() === id);
    if (keepForSaveButton) return;
    cancelInlineRename(id);
  }

  return {
    editingThreadId,
    editingAlias,
    renamingThreadId,
    setRenameInputRef,
    beginInlineRename,
    submitInlineRename,
    handleInlineRenameEnter,
    cancelInlineRename,
    handleInlineRenameBlur,
  };
}
