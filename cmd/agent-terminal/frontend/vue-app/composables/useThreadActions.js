import { ref } from '../../lib/vue.esm-browser.prod.js';
import { callAPI } from '../services/api.js';
import { logInfo, logWarn } from '../services/log.js';

function getThreadConfigFromStore(threadStore, threadId) {
  if (typeof threadStore?.getThreadConfig !== 'function') return Promise.resolve(null);
  return threadStore.getThreadConfig(threadId);
}

function setThreadConfigFromStore(threadStore, threadId, config) {
  if (typeof threadStore?.setThreadConfig !== 'function') return Promise.resolve(null);
  return threadStore.setThreadConfig(threadId, config);
}

function getDisplayNameFromStore(threadStore, thread) {
  return threadStore.displayName(thread);
}

function resolveThreadDisplayNameFromStore(threadStore, threadId) {
  const id = (threadId || '').toString().trim();
  if (!id) return '';
  if (id.toLowerCase() === 'system') return '系统';
  return threadStore.displayName({ id, name: id, state: '' });
}

function setCmdLayoutValue(layoutMode, value) {
  layoutMode.value = value;
}

function setCmdCardColsValue(cmdCardCols, value) {
  cmdCardCols.value = value;
}

async function performSend({
  selectedThreadId,
  composer,
  modeKey,
  threadStore,
  projectStore,
  resolveComposerSkillSelectionForSend,
  resetSelectedComposerSkills,
  scheduleScrollToBottom,
}) {
  let threadId = (selectedThreadId.value || '').toString().trim();
  const text = composer.state.text;
  const attachments = [...composer.state.attachments];
  if (!text.trim() && attachments.length === 0) return;

  if (!threadId) {
    threadId = await threadStore.startThread(projectStore?.state?.active || '.', {
      focusMode: modeKey.value,
    });
    if (!threadId) return;
    selectedThreadId.value = threadId;
  }

  const {
    selectedSkills,
    manualSkillSelection,
  } = await resolveComposerSkillSelectionForSend(threadId, text);

  const savedText = text;
  const savedAttachments = attachments;
  composer.clearComposer();
  resetSelectedComposerSkills();
  try {
    await threadStore.sendMessage(threadId, text, attachments, {
      selectedSkills,
      manualSkillSelection,
      cwd: projectStore?.state?.active || '',
    });
    scheduleScrollToBottom(true);
  } catch (err) {
    logWarn('ui', 'chat.send.error_restore', {
      thread_id: threadId,
      error: err?.message || String(err),
    });
    // Restore composer content so the user doesn't lose their message
    composer.state.text = savedText;
    savedAttachments.forEach((a) => composer.addAttachment(a));
  }
}

/**
 * @param {object} props
 * @param {object} deps
 */
export function useThreadActions(props, deps) {
  const {
    selectedThreadId,
    modeKey,
    isCmd,
    composer,
    layoutMode,
    cmdCardCols,
    isThreadInterruptible,
    beginInlineRename,
    scheduleScrollToBottom,
    resolveComposerSkillSelectionForSend,
    resetSelectedComposerSkills,
    showArchivedThreadList,
  } = deps;

  const recoveringSelected = ref(false);

  function launchOne() {
    return props.threadStore.startThread(props.projectStore.state.active || '.', {
      focusMode: modeKey.value,
    }).then((id) => {
      if (id) {
        selectedThreadId.value = id;
      }
    });
  }

  const getThreadConfig = (threadId) => getThreadConfigFromStore(props.threadStore, threadId);
  const setThreadConfig = (threadId, config) => setThreadConfigFromStore(props.threadStore, threadId, config);

  function send() {
    return performSend({
      selectedThreadId,
      composer,
      modeKey,
      threadStore: props.threadStore,
      projectStore: props.projectStore,
      resolveComposerSkillSelectionForSend,
      resetSelectedComposerSkills,
      scheduleScrollToBottom,
    });
  }

  async function interruptCurrent(control) {
    const threadId = (control?.threadId || selectedThreadId.value || '').toString();
    if (!threadId) {
      control?.reject?.({ reason: 'no_thread' });
      return;
    }
    logInfo('ui', 'chat.interrupt.request', {
      thread_id: threadId,
      source: 'ui_stop',
    });
    try {
      const result = await props.threadStore.stopThread(threadId, { source: 'ui_stop' });
      const confirmed = Boolean(result?.confirmed);
      const settled = Boolean(result?.settled || confirmed);
      const mode = (result?.mode || '').toString();
      logInfo('ui', 'chat.interrupt.result', {
        thread_id: threadId,
        source: 'ui_stop',
        confirmed,
        settled,
        mode,
      });
      if (settled) {
        control?.confirm?.({
          mode,
          threadId,
        });
      } else {
        control?.reject?.({
          reason: mode || 'not_confirmed',
          mode,
          threadId,
        });
      }
    } catch (error) {
      logWarn('ui', 'chat.interrupt.failed', {
        thread_id: threadId,
        source: 'ui_stop',
        error,
      });
      control?.reject?.({
        reason: 'error',
        threadId,
      });
    }
  }

  async function compactCurrent() {
    const threadId = (selectedThreadId.value || '').toString().trim();
    if (!threadId) return;
    if (deps.compacting?.value) return;
    try {
      await props.threadStore.compactThread(threadId);
    } catch (error) {
      logWarn('ui', 'chat.compact.failed', {
        thread_id: threadId,
        error,
      });
    }
  }

  async function forceCompleteCurrent() {
    const threadId = (selectedThreadId.value || '').toString().trim();
    if (!threadId) return;
    logInfo('ui', 'chat.forceComplete.request', { thread_id: threadId });
    try {
      await props.threadStore.forceCompleteThread(threadId);
    } catch (error) {
      logWarn('ui', 'chat.forceComplete.failed', {
        thread_id: threadId,
        error,
      });
    }
  }

  async function recoverSelected() {
    const threadId = (selectedThreadId.value || '').toString().trim();
    if (!threadId || recoveringSelected.value) return;
    recoveringSelected.value = true;
    logInfo('ui', 'chat.recover.request', { thread_id: threadId });
    try {
      if (typeof props.threadStore.recoverThread === 'function') {
        await props.threadStore.recoverThread(threadId);
      } else {
        await callAPI('thread/recover', { threadId });
      }
      logInfo('ui', 'chat.recover.done', { thread_id: threadId });
      if (typeof window !== 'undefined' && typeof window.alert === 'function') {
        window.alert('已触发进程恢复，请等待连接重建。');
      }
    } catch (error) {
      logWarn('ui', 'chat.recover.failed', {
        thread_id: threadId,
        error,
      });
      if (typeof window !== 'undefined' && typeof window.alert === 'function') {
        const detail = (error && typeof error === 'object' && error.message)
          ? error.message
          : String(error || 'unknown error');
        window.alert(`进程恢复失败: ${detail}`);
      }
    } finally {
      recoveringSelected.value = false;
    }
  }

  function stopSelected() {
    const threadId = (selectedThreadId.value || '').toString().trim();
    if (!threadId) return;
    if (!isThreadInterruptible(threadId)) {
      logInfo('ui', 'chat.interrupt.skipped.notInterruptible', {
        thread_id: threadId,
        source: 'toolbar',
      });
      return;
    }
    interruptCurrent({ threadId });
  }

  function renameSelected() {
    beginInlineRename(selectedThreadId.value);
  }



  function loadCardHistory(cardId) {
    props.threadStore.loadMessages(cardId, 300);
  }

  function renameCard(cardId) {
    if (isCmd.value && typeof props.threadStore.promptRenameThread === 'function') {
      props.threadStore.promptRenameThread(cardId);
      return;
    }
    beginInlineRename(cardId);
  }

  function stopCard(cardId) {
    const threadId = (cardId || '').toString().trim();
    if (!threadId) return;
    if (!isThreadInterruptible(threadId)) {
      logInfo('ui', 'chat.interrupt.skipped.notInterruptible', {
        thread_id: threadId,
        source: 'card',
      });
      return;
    }
    interruptCurrent({ threadId });
  }

  function toggleThreadPin(threadId) {
    if (typeof props.threadStore.toggleThreadPin !== 'function') return;
    props.threadStore.toggleThreadPin(threadId);
  }

  async function toggleThreadArchive(threadId) {
    if (typeof props.threadStore.toggleThreadArchive !== 'function') return;
    try {
      await props.threadStore.toggleThreadArchive(threadId);
    } catch (error) {
      logWarn('ui', 'thread.archive.toggle.failed', {
        thread_id: (threadId || '').toString(),
        error,
      });
    }
  }

  function toggleArchivedThreadList() {
    showArchivedThreadList.value = !showArchivedThreadList.value;
  }

  async function openNewWindow() {
    try {
      const dirResult = await callAPI('ui/selectProjectDir', { defaultPath: props.projectStore?.state?.active || '' });
      const cwd = dirResult?.path || '';
      if (!cwd) {
        logInfo('ui', 'openNewWindow.cancelled', {});
        return;
      }
      logInfo('ui', 'openNewWindow.start', { cwd });
      await callAPI('ui/openNewWindow', { cwd });
      logInfo('ui', 'openNewWindow.done', { cwd });
    } catch (error) {
      logWarn('ui', 'openNewWindow.failed', { error });
    }
  }

  const getDisplayName = (thread) => getDisplayNameFromStore(props.threadStore, thread);
  const resolveThreadDisplayName = (threadId) => resolveThreadDisplayNameFromStore(props.threadStore, threadId);
  const setCmdLayout = (value) => setCmdLayoutValue(layoutMode, value);
  const setCmdCardCols = (value) => setCmdCardColsValue(cmdCardCols, value);

  return {
    recoveringSelected,
    launchOne,
    getThreadConfig,
    setThreadConfig,
    send,
    interruptCurrent,
    compactCurrent,
    forceCompleteCurrent,
    recoverSelected,
    stopSelected,
    renameSelected,

    loadCardHistory,
    renameCard,
    stopCard,
    toggleThreadPin,
    toggleThreadArchive,
    toggleArchivedThreadList,
    openNewWindow,
    getDisplayName,
    resolveThreadDisplayName,
    setCmdLayout,
    setCmdCardCols,
  };
}
