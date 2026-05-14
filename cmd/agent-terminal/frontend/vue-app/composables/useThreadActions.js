import { ref } from '../../lib/vue.esm-browser.prod.js';
import { callAPI } from '../services/api.js';
import { logInfo, logWarn } from '../services/log.js';

export function resolveProjectActionCwd(projectStore, windowCwd = '') {
  const active = (projectStore?.state?.active || '').toString().trim();
  if (active && active !== '.') return active;
  return (windowCwd || '').toString().trim() || '.';
}

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

function getThreadRuntimeFromStore(threadStore, threadId) {
  const id = (threadId || '').toString().trim();
  if (!id) return {};
  const runtime = threadStore?.state?.agentRuntimeById?.[id];
  return runtime && typeof runtime === 'object' ? runtime : {};
}

function getThreadCapabilitiesFromStore(threadStore, threadId) {
  const capabilities = getThreadRuntimeFromStore(threadStore, threadId).capabilities;
  return Array.isArray(capabilities)
    ? capabilities.map((capability) => (capability || '').toString().trim().toLowerCase()).filter(Boolean)
    : [];
}

function formatProviderLabel(provider) {
  const normalized = (provider || '').toString().trim().toLowerCase();
  if (normalized === 'claude') return 'Claude';
  if (normalized === 'codex') return 'Codex';
  return (provider || '').toString().trim();
}

function setThreadCompactMessage(threadStore, threadId, status, message, extra = {}) {
  const id = (threadId || '').toString().trim();
  const detail = (message || '').toString().trim();
  if (!id || !detail) return false;
  if (typeof threadStore?.setThreadCompactResult !== 'function') return false;
  threadStore.setThreadCompactResult(id, status, detail, extra);
  return true;
}

function warnUserMessage(message, extra = {}) {
  const detail = (message || '').toString().trim();
  if (!detail) return;
  console.warn(detail, extra);
}

function alertOrWarnUserMessage(message, extra = {}) {
  const detail = (message || '').toString().trim();
  if (!detail) return;
  if (typeof window !== 'undefined' && typeof window.alert === 'function') {
    window.alert(detail);
    return;
  }
  warnUserMessage(detail, extra);
}

function formatCompactErrorMessage(error) {
  const code = (error?.code || '').toString().trim().toLowerCase();
  if (code === 'compact_timeout') return '压缩超时：未收到完成信号，请重试。';
  const detail = (error?.message || '').toString().trim();
  if (!detail || detail.startsWith('compact_')) return '压缩失败，请重试。';
  return `压缩失败: ${detail}`;
}

function setCmdLayoutValue(layoutMode, value) {
  layoutMode.value = value;
}

function setCmdCardColsValue(cmdCardCols, value) {
  cmdCardCols.value = value;
}

const EMPTY_SKILL_SELECTION = Object.freeze({
  enabled: false,
  selectedSkills: [],
  manualSkillSelection: false,
});

function normalizeSelectedSkillNames(rawSelectedSkills) {
  return Array.isArray(rawSelectedSkills)
    ? rawSelectedSkills.map((item) => (item || '').toString().trim()).filter(Boolean)
    : [];
}

async function resolveLaunchStartPayload(text, focusMode, resolveLaunchSkillSelectionForStart) {
  const rawSelection = typeof resolveLaunchSkillSelectionForStart === 'function'
    ? await resolveLaunchSkillSelectionForStart(text)
    : EMPTY_SKILL_SELECTION;
  const selectedSkills = normalizeSelectedSkillNames(rawSelection?.selectedSkills);
  const manualSkillSelection = rawSelection?.manualSkillSelection === true;
  const enabled = rawSelection?.enabled === true;
  const startOptions = { focusMode };
  if (enabled && (selectedSkills.length > 0 || manualSkillSelection)) {
    startOptions.selectedSkills = selectedSkills;
    startOptions.manualSkillSelection = manualSkillSelection;
  }
  // Forward the user's first message so the backend router has input to
  // classify against prompt_templates tags. Without this the router gets
  // an empty string and falls back to no injection.
  if (typeof text === 'string' && text.trim()) {
    startOptions.prompt = text;
  } else {
    // C1 opt-in: empty composer means the user clicked “启动 Agent” without
    // typing yet. Tell the backend to create a pending_launch row instead
    // of forking Claude CLI immediately — the real spawn happens on the
    // first turn/start once router has real user input to classify.
    startOptions.deferSpawn = true;
  }
  return {
    enabled,
    selectedSkills,
    manualSkillSelection,
    startOptions,
  };
}

async function performSend({
  selectedThreadId,
  composer,
  modeKey,
  threadStore,
  projectStore,
  windowCwd,
  resolveComposerSkillSelectionForSend,
  resolveLaunchSkillSelectionForStart,
  clearLaunchSkillSelection,
  resetSelectedComposerSkills,
  scheduleScrollToBottom,
}) {
  let threadId = (selectedThreadId.value || '').toString().trim();
  const text = composer.state.text;
  const attachments = [...composer.state.attachments];
  if (!text.trim() && attachments.length === 0) return;

  let skillSelection = EMPTY_SKILL_SELECTION;
  const actionCwd = resolveProjectActionCwd(projectStore, windowCwd);
  if (!threadId) {
    skillSelection = await resolveLaunchStartPayload(text, modeKey.value, resolveLaunchSkillSelectionForStart);
    threadId = await threadStore.startThread(actionCwd, skillSelection.startOptions);
    if (!threadId) return;
    selectedThreadId.value = threadId;
    if (typeof clearLaunchSkillSelection === 'function') {
      clearLaunchSkillSelection();
    }
  } else {
    skillSelection = await resolveComposerSkillSelectionForSend(threadId, text);
  }

  const selectedSkills = normalizeSelectedSkillNames(skillSelection?.selectedSkills);
  const manualSkillSelection = skillSelection?.manualSkillSelection === true;

  const savedText = text;
  const savedAttachments = attachments;
  composer.clearComposer();
  resetSelectedComposerSkills();
  try {
    await threadStore.sendMessage(threadId, text, attachments, {
      selectedSkills,
      manualSkillSelection,
      cwd: actionCwd,
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
    resolveLaunchSkillSelectionForStart,
    clearLaunchSkillSelection,
    resetSelectedComposerSkills,
    showArchivedThreadList,
  } = deps;

  const recoveringSelected = ref(false);

  const launchOne = () => resolveLaunchStartPayload(composer?.state?.text || '', modeKey.value, resolveLaunchSkillSelectionForStart)
    .then(({ startOptions }) => props.threadStore.startThread(resolveProjectActionCwd(props.projectStore, props.windowCwd), startOptions))
    .then((id) => {
      if (!id) return;
      if (typeof clearLaunchSkillSelection === 'function') clearLaunchSkillSelection();
      selectedThreadId.value = id;
    });

  const getThreadConfig = (threadId) => getThreadConfigFromStore(props.threadStore, threadId);
  const setThreadConfig = (threadId, config) => setThreadConfigFromStore(props.threadStore, threadId, config);

  const send = () => performSend({
    selectedThreadId,
    composer,
    modeKey,
    threadStore: props.threadStore, projectStore: props.projectStore,
    windowCwd: props.windowCwd,
    resolveComposerSkillSelectionForSend,
    resolveLaunchSkillSelectionForStart,
    clearLaunchSkillSelection,
    resetSelectedComposerSkills,
    scheduleScrollToBottom,
  });

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
    // Phase 1.8a：在 stopThread 之前先标抑制位，确保 F2 watcher 在 status='error'
    // 跳变时已能识别为用户主动 stop。
    if (typeof props.markManualAbort === 'function') {
      try { props.markManualAbort(threadId, 'ui_stop'); } catch (_) { /* never break interrupt */ }
    }
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
    if (!threadId) return { ok: false, code: 'no_thread', threadId };
    if (deps.compacting?.value) return { ok: false, code: 'compact_in_progress', threadId };
    const runtime = getThreadRuntimeFromStore(props.threadStore, threadId);
    const capabilities = getThreadCapabilitiesFromStore(props.threadStore, threadId);
    if (!capabilities.includes('context_compact')) {
      const providerLabel = formatProviderLabel(runtime.provider);
      const message = providerLabel
        ? `${providerLabel} 当前不支持上下文压缩。`
        : '当前线程不支持上下文压缩。';
      logInfo('ui', 'chat.compact.unsupported', {
        thread_id: threadId,
        provider: (runtime.provider || '').toString(),
        capabilities,
      });
      if (!setThreadCompactMessage(props.threadStore, threadId, 'failed', message, { code: 'compact_unsupported' })) {
        warnUserMessage(message, { threadId, code: 'compact_unsupported' });
      }
      return { ok: false, code: 'compact_unsupported', threadId, message };
    }
    try {
      await props.threadStore.compactThread(threadId);
      return { ok: true, threadId };
    } catch (error) {
      const message = formatCompactErrorMessage(error);
      const code = (error?.code || 'compact_failed').toString().trim() || 'compact_failed';
      logWarn('ui', 'chat.compact.failed', {
        thread_id: threadId,
        error,
      });
      if (!setThreadCompactMessage(props.threadStore, threadId, 'failed', message, { code })) {
        warnUserMessage(message, { threadId, code });
      }
      return { ok: false, code, threadId, message, error };
    }
  }

  async function recoverSelected() {
    const threadId = (selectedThreadId.value || '').toString().trim();
    if (!threadId || recoveringSelected.value) return { ok: false, code: 'recover_unavailable', threadId };
    recoveringSelected.value = true;
    logInfo('ui', 'chat.recover.request', { thread_id: threadId });
    try {
      if (typeof props.threadStore.recoverThread === 'function') {
        await props.threadStore.recoverThread(threadId);
      } else {
        await callAPI('thread/recover', { threadId });
      }
      const message = '已触发进程恢复，请等待连接重建。';
      logInfo('ui', 'chat.recover.done', { thread_id: threadId, message });
      alertOrWarnUserMessage(message);
      return { ok: true, threadId, message };
    } catch (error) {
      const detail = error && typeof error === 'object' && error.message ? error.message : String(error || 'unknown error');
      const message = `进程恢复失败: ${detail}`;
      logWarn('ui', 'chat.recover.failed', {
        thread_id: threadId,
        error,
      });
      alertOrWarnUserMessage(message, { threadId, action: 'recover' });
      return { ok: false, code: 'recover_failed', threadId, message, error };

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
    logWarn('ui', 'chat.select.card_history.refresh', { card_id: cardId, sync_runtime: false });
    props.threadStore.loadMessages(cardId, 300, { syncRuntime: false });
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
