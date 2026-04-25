import {
  ref,
  nextTick,
} from '../../lib/vue.esm-browser.prod.js';
import { logDebug, logWarn } from '../services/log.js';

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
    logDebug('ui', 'rename.inline.submit.triggered', { thread_id: id, raw_thread_id: threadId, renaming_thread_id: renamingThreadId.value });
    if (!id || editingThreadId.value !== id || renamingThreadId.value === id) {
      logDebug('ui', 'rename.inline.submit.skipped_state', { thread_id: id, editing_thread_id: editingThreadId.value, renaming_thread_id: renamingThreadId.value });
      return;
    }

    const target = visibleChatThreadCards.value.find((item) => item.id === id);
    const current = (target?.name || id).toString().trim() || id;
    const nextName = (editingAlias.value || '').toString().trim();
    logDebug('ui', 'rename.inline.submit.payload', { thread_id: id, current, next_name: nextName });
    if (!nextName || nextName === current) {
      logDebug('ui', 'rename.inline.submit.noop', { thread_id: id });
      cancelInlineRename(id);
      return;
    }

    renamingThreadId.value = id;
    try {
      logDebug('ui', 'rename.inline.submit.start', { thread_id: id, has_store_rename: typeof props.threadStore.renameThread === 'function' });
      if (typeof props.threadStore.renameThread === 'function') {
        await props.threadStore.renameThread(id, nextName);
      } else if (typeof props.threadStore.promptRenameThread === 'function') {
        props.threadStore.promptRenameThread(id);
      }
      logDebug('ui', 'rename.inline.submit.done', { thread_id: id });
      cancelInlineRename(id);
    } catch (error) {
      logWarn('ui', 'rename.inline.submit.failed', { thread_id: id, error });
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
