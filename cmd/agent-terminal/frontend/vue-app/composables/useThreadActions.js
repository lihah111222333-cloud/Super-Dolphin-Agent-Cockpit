import { ref, watch } from '../../lib/vue.esm-browser.prod.js';
import { callAPI } from '../services/api.js';
import { logInfo, logWarn } from '../services/log.js';
import { isThreadErrorStatus } from '../services/status.js';
import { isStaleThreadSelectionError } from '../utils/thread-page-utils.js';

export function resolveProjectActionCwd(projectStore, windowCwd = '') {
  const active = (projectStore?.state?.active || '').toString().trim();
  if (active && active !== '.') return active;
  const scoped = (windowCwd || '').toString().trim();
  if (scoped) return scoped;
  throw new Error('project action cwd is required');
}

export function resolveProjectViewCwd(projectStore, windowCwd = '') {
  const active = (projectStore?.state?.active || '').toString().trim();
  if (active && active !== '.') return active;
  return (windowCwd || '').toString().trim();
}

function readWindowCwd(windowCwd) {
  const value = typeof windowCwd === 'function' ? windowCwd() : windowCwd;
  return (value || '').toString();
}

function normalizeOptionalCwd(value) {
  return (value || '').toString().trim();
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

function getThreadModelFromStore(threadStore, threadId) {
  const id = (threadId || '').toString().trim();
  if (!id) return {};
  const threads = threadStore?.state?.threads;
  if (!Array.isArray(threads)) return {};
  const thread = threads.find((candidate) => (candidate?.id || '').toString().trim() === id);
  return thread && typeof thread === 'object' ? thread : {};
}

function resolveExistingThreadActionCwd(threadStore, threadId) {
  const runtimeCwd = normalizeOptionalCwd(getThreadRuntimeFromStore(threadStore, threadId).cwd);
  if (runtimeCwd) return runtimeCwd;
  return normalizeOptionalCwd(getThreadModelFromStore(threadStore, threadId).cwd);
}

function createSendOptions(cwd) {
  const options = { manualSkillSelection: false };
  const cwdValue = normalizeOptionalCwd(cwd);
  if (cwdValue) options.cwd = cwdValue;
  return options;
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

function errorTextCandidates(error) {
  return [
    error?.message,
    error?.cause?.message,
    typeof error === 'string' ? error : '',
  ].map((item) => (item || '').toString()).filter(Boolean);
}

function normalizeErrorDetailText(text) {
  const raw = (text || '').toString().trim();
  if (!raw) return '';
  const unescaped = raw.replace(/\\"/g, '"');
  try {
    const parsed = JSON.parse(unescaped);
    const detail = (
      parsed?.message
      || parsed?.error?.message
      || parsed?.data?.message
      || ''
    ).toString().trim();
    if (detail) return detail;
  } catch {
    // Non-JSON Error.message values are already the backend detail.
  }
  return unescaped;
}

function formatErrorDetail(error) {
  for (const candidate of errorTextCandidates(error)) {
    const detail = normalizeErrorDetailText(candidate);
    if (detail) return detail;
  }
  return String(error || '').trim();
}

function missingWorkDirFailure(error) {
  const text = errorTextCandidates(error).join('\n').replace(/\\"/g, '"');
  const lower = text.toLowerCase();
  const reportsMissingWorkdir = lower.includes('pool work dir stat')
    || lower.includes('cwd stat')
    || lower.includes('resolve provider project cwd realpath');
  if (!reportsMissingWorkdir || !lower.includes('no such file or directory')) {
    return null;
  }
  const quoted = text.match(/(?:pool work dir stat|cwd stat) "([^"]+)"/i);
  const realpath = text.match(/resolve provider project cwd realpath:\s*lstat\s+([^:\n]+):/i);
  const lstat = text.match(/lstat\s+([^:\n]+):\s+no such file or directory/i);
  const path = (quoted?.[1] || realpath?.[1] || lstat?.[1] || '').toString().trim();
  return { path };
}

function formatMissingWorkDirNotice(error, actionLabel) {
  const failure = missingWorkDirFailure(error);
  if (!failure) return '';
  const target = failure.path || '后端未返回具体路径';
  return `${actionLabel}失败：该会话的工作目录已不存在。\n\n${target}\n\n请恢复该目录，或新建/重新绑定会话后继续。`;
}

function formatSkillConflictNotice(error, actionLabel) {
  const text = errorTextCandidates(error).join('\n').replace(/\\"/g, '"');
  const lower = text.toLowerCase();
  const hasSkillConflict = lower.includes('skill mirror conflicts') || lower.includes('skill same-name conflict');
  if (!hasSkillConflict) return '';
  return `${actionLabel}失败：当前项目有技能冲突，请到技能页面处理后再试。`;
}

function formatActionFailureNotice(error, actionLabel) {
  const detail = formatErrorDetail(error);
  return formatMissingWorkDirNotice(error, actionLabel)
    || formatSkillConflictNotice(error, actionLabel)
    || `${actionLabel}失败：${detail || '后端未返回错误详情'}`;
}

function formatSendFailureNotice(error) {
  return formatActionFailureNotice(error, '发送');
}

function formatLaunchSendFailureNotice(error, provider) {
  const detail = formatErrorDetail(error);
  const providerLabel = formatProviderLabel(provider);
  const launchLabel = providerLabel ? `${providerLabel} 启动失败` : 'Provider 启动失败';
  return formatMissingWorkDirNotice(error, '发送')
    || formatSkillConflictNotice(error, '发送')
    || `发送失败：${launchLabel}：${detail || '后端未返回错误详情'}`;
}

function formatThreadErrorSendBlockedNotice() {
  return '当前会话已报错，不能继续发送。请先恢复会话，或新建/继承一个会话后继续。';
}

function isSelectedThreadSendBlocked(threadStore, threadId) {
  const id = (threadId || '').toString().trim();
  if (!id || typeof threadStore?.getThreadStatus !== 'function') return false;
  return isThreadErrorStatus(threadStore.getThreadStatus(id));
}

function getThreadSendBlockedNotice(sendBlockedNoticesByThread, threadId) {
  const id = (threadId || '').toString().trim();
  if (!id) return '';
  const map = sendBlockedNoticesByThread?.value;
  return map instanceof Map ? (map.get(id) || '').toString() : '';
}

function getThreadSendHoldNotice(sendHoldNoticesByThread, threadId) {
  const id = (threadId || '').toString().trim();
  if (!id) return '';
  const map = sendHoldNoticesByThread?.value;
  return map instanceof Map ? (map.get(id) || '').toString() : '';
}

function getStoreThreadSendBlockedNotice(threadStore, threadId) {
  const id = (threadId || '').toString().trim();
  if (!id) return '';
  if (typeof threadStore?.getThreadSendBlockedNotice === 'function') {
    return (threadStore.getThreadSendBlockedNotice(id) || '').toString();
  }
  const notices = threadStore?.state?.sendBlockedNoticesByThread;
  return notices && typeof notices === 'object' ? (notices[id] || '').toString() : '';
}

function getStoreThreadSendHoldNotice(threadStore, threadId) {
  const id = (threadId || '').toString().trim();
  if (!id) return '';
  const notices = threadStore?.state?.sendHoldNoticesByThread;
  return notices && typeof notices === 'object' ? (notices[id] || '').toString() : '';
}

function isStoreThreadSendBlocked(threadStore, threadId) {
  const id = (threadId || '').toString().trim();
  if (!id) return false;
  if (typeof threadStore?.isThreadSendBlocked === 'function') {
    return Boolean(threadStore.isThreadSendBlocked(id));
  }
  return Boolean(getStoreThreadSendBlockedNotice(threadStore, id));
}

function setThreadSendBlockedNotice(sendBlockedNoticesByThread, threadId, notice) {
  const id = (threadId || '').toString().trim();
  const detail = (notice || '').toString().trim();
  if (!id || !detail || !sendBlockedNoticesByThread) return;
  const next = new Map(sendBlockedNoticesByThread.value instanceof Map ? sendBlockedNoticesByThread.value : []);
  next.set(id, detail);
  sendBlockedNoticesByThread.value = next;
}

function setThreadSendHoldNotice(sendHoldNoticesByThread, threadId, notice) {
  const id = (threadId || '').toString().trim();
  const detail = (notice || '').toString().trim();
  if (!id || !detail || !sendHoldNoticesByThread) return;
  const next = new Map(sendHoldNoticesByThread.value instanceof Map ? sendHoldNoticesByThread.value : []);
  next.set(id, detail);
  sendHoldNoticesByThread.value = next;
}

function clearThreadSendBlockedNotice(sendBlockedNoticesByThread, threadId) {
  const id = (threadId || '').toString().trim();
  const map = sendBlockedNoticesByThread?.value;
  if (!id || !(map instanceof Map) || !map.has(id)) return;
  const next = new Map(map);
  next.delete(id);
  sendBlockedNoticesByThread.value = next;
}

function clearThreadSendHoldNotice(sendHoldNoticesByThread, threadId) {
  const id = (threadId || '').toString().trim();
  const map = sendHoldNoticesByThread?.value;
  if (!id || !(map instanceof Map) || !map.has(id)) return;
  const next = new Map(map);
  next.delete(id);
  sendHoldNoticesByThread.value = next;
}

function clearStoreThreadSendBlockedNotice(threadStore, threadId) {
  const id = (threadId || '').toString().trim();
  if (!id) return;
  if (typeof threadStore?.clearThreadSendBlockedNotice === 'function') {
    threadStore.clearThreadSendBlockedNotice(id);
    return;
  }
  const notices = threadStore?.state?.sendBlockedNoticesByThread;
  if (!notices || typeof notices !== 'object' || !Object.prototype.hasOwnProperty.call(notices, id)) return;
  const next = { ...notices };
  delete next[id];
  threadStore.state.sendBlockedNoticesByThread = next;
}

function clearStoreThreadSendHoldNotice(threadStore, threadId) {
  const id = (threadId || '').toString().trim();
  const notices = threadStore?.state?.sendHoldNoticesByThread;
  if (!id || !notices || typeof notices !== 'object' || !Object.prototype.hasOwnProperty.call(notices, id)) return;
  const next = { ...notices };
  delete next[id];
  threadStore.state.sendHoldNoticesByThread = next;
}

function shouldApplySendFailureToCurrentSelection(selectedThreadId, sourceThreadId, options = {}) {
  const currentThreadId = (selectedThreadId?.value || '').toString().trim();
  const failedThreadId = (sourceThreadId || '').toString().trim();
  const allowEmptySelection = Boolean(options.allowEmptySelection);
  if (!failedThreadId) return allowEmptySelection && !currentThreadId;
  if (!currentThreadId) return allowEmptySelection;
  return currentThreadId === failedThreadId;
}

function getSelectedThreadSendNotice(threadStore, sendBlockedNoticesByThread, sendHoldNoticesByThread, threadId) {
  return getThreadSendBlockedNotice(sendBlockedNoticesByThread, threadId) || getThreadSendHoldNotice(sendHoldNoticesByThread, threadId)
    || getStoreThreadSendBlockedNotice(threadStore, threadId) || getStoreThreadSendHoldNotice(threadStore, threadId)
    || (isSelectedThreadSendBlocked(threadStore, threadId) || isStoreThreadSendBlocked(threadStore, threadId) ? formatThreadErrorSendBlockedNotice() : '');
}

function formatLaunchFailureNotice(error) {
  return formatActionFailureNotice(error, '启动');
}

function isProviderPreferenceReady(providerPreferenceReady) {
  return providerPreferenceReady?.value !== false;
}

function formatProviderPreferencePendingNotice(providerPreferenceError) {
  const detail = (providerPreferenceError?.value || '').toString().trim();
  if (detail) return `Provider 初始化失败：${detail}`;
  return 'Provider 正在初始化，请稍后再试。';
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

function createLaunchIntentId() {
  const randomUUID = globalThis.crypto?.randomUUID;
  if (typeof randomUUID !== 'function') {
    throw new Error('crypto.randomUUID is required to create a launch intent id');
  }
  return `launch_${randomUUID.call(globalThis.crypto)}`;
}

function createLaunchIntentState() {
  let intentId = '';
  let threadId = '';
  return {
    current() {
      if (!intentId) intentId = createLaunchIntentId();
      return intentId;
    },
    bindThread(id) {
      threadId = (id || '').toString().trim();
    },
    reset() {
      intentId = '';
      threadId = '';
    },
    resetIfThread(id) {
      const target = (id || '').toString().trim();
      if (target && target === threadId) this.reset();
    },
  };
}

async function resolveStartOptions(focusMode, launchIntentId = '') {
  const startOptions = { focusMode, deferSpawn: true };
  if (typeof launchIntentId === 'string' && launchIntentId.trim()) {
    startOptions.launchIntentId = launchIntentId.trim();
  }
  // The first user message is sent by sendMessage/turn/start immediately after
  // this pending thread exists, so thread/start must not launch the provider.
  return startOptions;
}

async function performSend({
  selectedThreadId,
  composer,
  modeKey,
  threadStore,
  projectStore,
  windowCwd,
  scheduleScrollToBottom,
  sendFailureNotice,
  sendBlockedNoticesByThread,
  sendHoldNoticesByThread,
  providerPreferenceReady,
  providerPreferenceError,
  launchIntent,
}) {
  let threadId = (selectedThreadId.value || '').toString().trim();
  let startedNewThread = false;
  let startedNewThreadCwd = '';
  const text = composer.state.text;
  const attachments = [...composer.state.attachments];
  if (!text.trim() && attachments.length === 0) return;
  sendFailureNotice.value = '';
  const blockedNotice = getThreadSendBlockedNotice(sendBlockedNoticesByThread, threadId);
  if (blockedNotice) {
    sendFailureNotice.value = blockedNotice;
    return;
  }
  const holdNotice = getThreadSendHoldNotice(sendHoldNoticesByThread, threadId);
  if (holdNotice) {
    sendFailureNotice.value = holdNotice;
    return;
  }
  const storeBlockedNotice = getStoreThreadSendBlockedNotice(threadStore, threadId);
  if (storeBlockedNotice) {
    sendFailureNotice.value = storeBlockedNotice;
    return;
  }
  const storeHoldNotice = getStoreThreadSendHoldNotice(threadStore, threadId);
  if (storeHoldNotice) {
    sendFailureNotice.value = storeHoldNotice;
    return;
  }
  if (isSelectedThreadSendBlocked(threadStore, threadId) || isStoreThreadSendBlocked(threadStore, threadId)) {
    sendFailureNotice.value = formatThreadErrorSendBlockedNotice();
    return;
  }

  if (!threadId) {
    if (!isProviderPreferenceReady(providerPreferenceReady)) {
      sendFailureNotice.value = formatProviderPreferencePendingNotice(providerPreferenceError);
      return;
    }
    try {
      const actionCwd = resolveProjectActionCwd(projectStore, readWindowCwd(windowCwd));
      const launchIntentId = launchIntent?.current?.() || '';
      const startOptions = await resolveStartOptions(modeKey.value, launchIntentId);
      startOptions.optimisticUserMessage = { text, attachments };
      startOptions.skipInitialRuntimeSync = true;
      threadId = await threadStore.startThread(actionCwd, startOptions);
      startedNewThreadCwd = actionCwd;
      launchIntent?.bindThread?.(threadId);
    } catch (err) {
      logWarn('ui', 'chat.start.error', {
        error: err?.message || String(err),
      });
      if (shouldApplySendFailureToCurrentSelection(selectedThreadId, threadId, { allowEmptySelection: true })) {
        sendFailureNotice.value = formatSendFailureNotice(err);
      }
      throw err;
    }
    if (!threadId) return;
    startedNewThread = true;
    selectedThreadId.value = threadId;
    if (typeof composer.clearDraft === 'function') {
      composer.clearDraft('', modeKey.value);
    }
  }

  const actionCwd = startedNewThread
    ? startedNewThreadCwd
    : resolveExistingThreadActionCwd(threadStore, threadId);
  const savedText = text;
  const savedAttachments = attachments;
  composer.clearComposer();
  try {
    const sendOptions = createSendOptions(actionCwd);
    await threadStore.sendMessage(threadId, text, attachments, sendOptions);
    launchIntent?.resetIfThread?.(threadId);
    scheduleScrollToBottom(true);
  } catch (err) {
    logWarn('ui', 'chat.send.error_restore', {
      thread_id: threadId,
      error: err?.message || String(err),
    });
    let clearedStaleSelection = false;
    if (isStaleThreadSelectionError(err) && (selectedThreadId.value || '').toString().trim() === threadId) {
      selectedThreadId.value = '';
      clearedStaleSelection = true;
      logWarn('ui', 'chat.send.stale_thread_cleared', {
        thread_id: threadId,
        error: err?.message || String(err),
      });
    }
    const notice = startedNewThread
      ? formatLaunchSendFailureNotice(err, getThreadRuntimeFromStore(threadStore, threadId).provider)
      : formatSendFailureNotice(err);
    const localBlocked = getStoreThreadSendBlockedNotice(threadStore, threadId) || isStoreThreadSendBlocked(threadStore, threadId);
    if (localBlocked) setThreadSendBlockedNotice(sendBlockedNoticesByThread, threadId, notice);
    else setThreadSendHoldNotice(sendHoldNoticesByThread, threadId, notice);
    if (typeof composer.restoreDraft === 'function') {
      composer.restoreDraft(threadId, modeKey.value, { text: savedText, attachments: savedAttachments });
    }
    let clearedLaunchFailureSelection = false;
    if (startedNewThread && (selectedThreadId.value || '').toString().trim() === threadId) {
      selectedThreadId.value = '';
      launchIntent?.resetIfThread?.(threadId);
      clearedLaunchFailureSelection = true;
      if (typeof composer.restoreDraft === 'function') {
        composer.restoreDraft('', modeKey.value, { text: savedText, attachments: savedAttachments });
      }
      logWarn('ui', 'chat.send.launch_thread_cleared', {
        thread_id: threadId,
        error: err?.message || String(err),
      });
    }
    const applyFailureToCurrentSelection = shouldApplySendFailureToCurrentSelection(selectedThreadId, threadId, {
      allowEmptySelection: clearedStaleSelection || clearedLaunchFailureSelection,
    });
    if (applyFailureToCurrentSelection) {
      // Restore composer content so the user doesn't lose their message.
      composer.state.text = savedText;
      composer.state.attachments = [...savedAttachments];
      sendFailureNotice.value = notice;
    }
    throw err;
  }
}

function createLaunchOneAction({
  selectedThreadId,
  sendFailureNotice,
  launchIntent,
}) {
  return () => {
    sendFailureNotice.value = '';
    launchIntent?.reset?.();
    selectedThreadId.value = '';
    return Promise.resolve();
  };
}

function createSendAction({
  selectedThreadId,
  composer,
  modeKey,
  threadStore,
  projectStore,
  windowCwd,
  scheduleScrollToBottom,
  sendFailureNotice,
  sendBlockedNoticesByThread,
  sendHoldNoticesByThread,
  providerPreferenceReady,
  providerPreferenceError,
  launchIntent,
}) {
  let sendInFlightPromise = null;
  return () => {
    if (sendInFlightPromise) {
      logInfo('ui', 'chat.send.skipped.in_flight', {
        thread_id: (selectedThreadId.value || '').toString().trim(),
      });
      return sendInFlightPromise;
    }
    sendInFlightPromise = performSend({
      selectedThreadId,
      composer,
      modeKey,
      threadStore,
      projectStore,
      windowCwd,
      scheduleScrollToBottom,
      sendFailureNotice,
      sendBlockedNoticesByThread,
      sendHoldNoticesByThread,
      providerPreferenceReady,
      providerPreferenceError,
      launchIntent,
    })
      .finally(() => {
        sendInFlightPromise = null;
      });
    return sendInFlightPromise;
  };
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
    composer, layoutMode, cmdCardCols, isThreadInterruptible, beginInlineRename,
    scheduleScrollToBottom, showArchivedThreadList, providerPreferenceReady, providerPreferenceError,
    sendBlockedNoticesByThread: providedSendBlockedNoticesByThread,
    sendHoldNoticesByThread: providedSendHoldNoticesByThread,
  } = deps;

  const recoveringSelected = ref(false); const sendFailureNotice = ref('');
  const sendBlockedNoticesByThread = providedSendBlockedNoticesByThread || ref(new Map()); const sendHoldNoticesByThread = providedSendHoldNoticesByThread || ref(new Map());
  const launchIntent = createLaunchIntentState();

  watch(
    () => selectedThreadId.value,
    (id, prevId) => {
      const nextThreadId = (id || '').toString().trim();
      const previousThreadId = (prevId || '').toString().trim();
      if (nextThreadId === previousThreadId) return;
      sendFailureNotice.value = getSelectedThreadSendNotice(props.threadStore, sendBlockedNoticesByThread, sendHoldNoticesByThread, nextThreadId);
    },
    { flush: 'sync', immediate: true },
  );

  const launchOne = createLaunchOneAction({
    selectedThreadId, sendFailureNotice,
    launchIntent,
  });

  const getThreadConfig = (threadId) => getThreadConfigFromStore(props.threadStore, threadId);
  const setThreadConfig = (threadId, config) => setThreadConfigFromStore(props.threadStore, threadId, config);

  const send = createSendAction({
    selectedThreadId,
    composer,
    modeKey,
    threadStore: props.threadStore, projectStore: props.projectStore, windowCwd: () => props.windowCwd,
    scheduleScrollToBottom, sendFailureNotice, sendBlockedNoticesByThread, sendHoldNoticesByThread, providerPreferenceReady, providerPreferenceError, launchIntent,
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
      props.markManualAbort(threadId, 'ui_stop');
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
        launchIntent.resetIfThread(threadId);
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
      throw error;
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
      clearThreadSendBlockedNotice(sendBlockedNoticesByThread, threadId);
      clearThreadSendHoldNotice(sendHoldNoticesByThread, threadId);
      clearStoreThreadSendBlockedNotice(props.threadStore, threadId);
      clearStoreThreadSendHoldNotice(props.threadStore, threadId);
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
    return interruptCurrent({ threadId });
  }

  function toggleThreadPin(threadId) {
    if (typeof props.threadStore.toggleThreadPin !== 'function') return;
    props.threadStore.toggleThreadPin(threadId);
  }

  async function toggleThreadArchive(threadId) {
    if (typeof props.threadStore.toggleThreadArchive !== 'function') return;
    await props.threadStore.toggleThreadArchive(threadId);
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
      throw error;
    }
  }

  const getDisplayName = (thread) => getDisplayNameFromStore(props.threadStore, thread);
  const resolveThreadDisplayName = (threadId) => resolveThreadDisplayNameFromStore(props.threadStore, threadId);
  const setCmdLayout = (value) => setCmdLayoutValue(layoutMode, value);
  const setCmdCardCols = (value) => setCmdCardColsValue(cmdCardCols, value);

  return {
    recoveringSelected, sendFailureNotice,
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
