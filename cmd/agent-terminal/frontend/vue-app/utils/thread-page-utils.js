import { logInfo, logWarn } from '../services/log.js';
import { perfNow } from '../stores/thread-actions-helpers.js';

/**
 * @typedef {import('./thread-page-types').BuildVisibleChatThreadCardsOptions} BuildVisibleChatThreadCardsOptions
 * @typedef {import('./thread-page-types').ThreadCardSource} ThreadCardSource
 * @typedef {import('./thread-page-types').ThreadSelectionFreshness} ThreadSelectionFreshness
 * @typedef {import('./thread-page-types').ThreadSelectionFreshnessOptions} ThreadSelectionFreshnessOptions
 * @typedef {import('./thread-page-types').ThreadStoreLike} ThreadStoreLike
 * @typedef {import('./thread-page-types').VisibleChatThreadCardState} VisibleChatThreadCardState
 */




/**
 * @param {ThreadStoreLike | null | undefined} threadStore
 * @param {string | null | undefined} threadId
 * @param {{ syncRuntime?: boolean, force?: boolean } | null | undefined} [loadOptions]
 * @returns {Promise<boolean>}
 */
export async function requestHistoryLoad(threadStore, threadId, loadOptions = undefined) {
  const id = (threadId || '').toString().trim();
  const hasLoader = typeof threadStore?.loadMessages === 'function';
  if (!id || !hasLoader) {
    logInfo('ui', 'chat.historyLoad.skipped.invalid', {
      thread_id: id,
      has_loader: hasLoader,
    });
    return false;
  }

  const existing = typeof threadStore?.getThreadTimeline === 'function'
    ? threadStore.getThreadTimeline(id)
    : [];
  const hasDialogHistory = Array.isArray(existing) && existing.some((item) => {
    const kind = (item?.kind || '').toString().trim();
    return kind === 'assistant' || kind === 'user';
  });
  const forceReload = loadOptions?.force === true;
  const historyLooksStale = !forceReload
    && typeof threadStore?.shouldReloadThreadHistory === 'function'
    && threadStore.shouldReloadThreadHistory(id);
  if (hasDialogHistory && !forceReload && !historyLooksStale) {
    logInfo('ui', 'chat.historyLoad.skipped.cached', {
      thread_id: id,
      timeline_len: existing.length,
    });
    return false;
  }
  if (hasDialogHistory && historyLooksStale) {
    logInfo('ui', 'chat.historyLoad.cached_but_stale', {
      thread_id: id,
      timeline_len: existing.length,
    });
  } else if (!forceReload && Array.isArray(existing) && existing.length > 0) {
    logInfo('ui', 'chat.historyLoad.resume_from_transient_timeline', {
      thread_id: id,
      timeline_len: existing.length,
      item_kinds: existing
        .slice(0, 8)
        .map((item) => (item?.kind || '').toString().trim())
        .filter(Boolean),
    });
  }



  logInfo('ui', 'chat.historyLoad.start', {
    thread_id: id,
    sync_runtime: loadOptions?.syncRuntime !== false,
    force_reload: forceReload,
    cache_stale: historyLooksStale,
  });
  const loadStart = perfNow();
  try {
    await threadStore.loadMessages(id, undefined, loadOptions || undefined);
    const loadDuration = Math.round(perfNow() - loadStart);
    logWarn('ui', 'chat.historyLoad.done_timed', {
      thread_id: id,
      duration_ms: loadDuration,
      force_reload: forceReload,
    });
    return true;
  } catch (error) {
    logWarn('ui', 'chat.historyLoad.failed', {
      thread_id: id,
      error,
      duration_ms: Math.round(perfNow() - loadStart),
    });
    throw error;
  }
}


/**
 * @param {ThreadStoreLike | null | undefined} threadStore
 * @param {string | null | undefined} threadId
 * @param {ThreadSelectionFreshnessOptions | null | undefined} [options]
 * @returns {Promise<ThreadSelectionFreshness>}
 */
export async function ensureThreadSelectionFresh(threadStore, threadId, options = {}) {
  const id = (threadId || '').toString().trim();
  const opts = options && typeof options === 'object' ? options : {};
  const reason = (Reflect.get(opts, 'reason') || '').toString().trim();
  const previousThreadId = (Reflect.get(opts, 'previousThreadId') || '').toString().trim();
  const shouldRefreshOnThreadSwitch = reason === 'selection' && Boolean(previousThreadId) && previousThreadId !== id;
  const shouldRefreshOnPageEnter = reason === 'page-enter' && Boolean(id);
  const shouldRefreshVisibleThread = (shouldRefreshOnThreadSwitch || shouldRefreshOnPageEnter) && typeof threadStore?.syncThreadState === 'function';
  const selectionStart = perfNow();
  logWarn('ui', 'chat.selection.fresh.start', {
    thread_id: id,
    reason,
    previous_thread_id: previousThreadId,
    should_refresh_visible: shouldRefreshVisibleThread,
    should_refresh_switch: shouldRefreshOnThreadSwitch,
    should_refresh_page_enter: shouldRefreshOnPageEnter,
  });
  if (shouldRefreshVisibleThread) {
    logInfo('ui', 'chat.threadState.refresh_visible_thread', {
      reason,
      previous_thread_id: previousThreadId,
      thread_id: id,
    });
    const concurrentStart = perfNow();
    const requestedHistory = await requestHistoryLoad(threadStore, id, { syncRuntime: false, force: true }).catch(err => {
      logWarn('ui', 'chat.selection.requestHistoryLoad.failed', { thread_id: id, error: err?.message || String(err) });
      return false;
    });
    await threadStore.syncThreadState(id).catch(err => {
      logWarn('ui', 'chat.selection.syncThreadState.failed', { thread_id: id, error: err?.message || String(err) });
    });
    const concurrentDuration = Math.round(perfNow() - concurrentStart);
    logWarn('ui', 'chat.selection.concurrentLoad.done', {
      thread_id: id,
      requested_history: requestedHistory,
      duration_ms: concurrentDuration,
      total_ms: Math.round(perfNow() - selectionStart),
    });
    return {
      requestedHistory: Boolean(requestedHistory),
      syncedThreadState: true,
      forcedHistoryReload: false,
    };
  }

  const shouldForceHistoryReload = Boolean(id)
    && typeof threadStore?.shouldReloadThreadHistory === 'function'
    && threadStore.shouldReloadThreadHistory(id)
    && typeof threadStore?.loadMessages === 'function';
  if (shouldForceHistoryReload) {
    logInfo('ui', 'chat.historyLoad.force_reload', {
      thread_id: id,
    });
    const forceStart = perfNow();
    await threadStore.loadMessages(id);
    logWarn('ui', 'chat.selection.forceReload.done', {
      thread_id: id,
      duration_ms: Math.round(perfNow() - forceStart),
      total_ms: Math.round(perfNow() - selectionStart),
    });
    return {
      requestedHistory: true,
      syncedThreadState: false,
      forcedHistoryReload: true,
    };
  }

  if ((shouldRefreshOnThreadSwitch || shouldRefreshOnPageEnter) && typeof threadStore?.loadMessages === 'function') {
    logInfo('ui', 'chat.historyLoad.refresh_visible_thread', {
      reason,
      previous_thread_id: previousThreadId,
      thread_id: id,
    });
    const refreshStart = perfNow();
    await threadStore.loadMessages(id);
    logWarn('ui', 'chat.selection.refreshLoadMessages.done', {
      thread_id: id,
      duration_ms: Math.round(perfNow() - refreshStart),
      total_ms: Math.round(perfNow() - selectionStart),
    });
    return {
      requestedHistory: true,
      syncedThreadState: false,
      forcedHistoryReload: false,
    };
  }

  const fallbackStart = perfNow();
  const requestedHistory = await requestHistoryLoad(threadStore, id);
  if (requestedHistory) {
    logWarn('ui', 'chat.selection.fallbackHistory.done', {
      thread_id: id,
      duration_ms: Math.round(perfNow() - fallbackStart),
      total_ms: Math.round(perfNow() - selectionStart),
    });
    return {
      requestedHistory,
      syncedThreadState: false,
      forcedHistoryReload: false,
    };
  }
  if (!id || typeof threadStore?.syncThreadState !== 'function') {
    logWarn('ui', 'chat.selection.fresh.no_action', {
      thread_id: id,
      total_ms: Math.round(perfNow() - selectionStart),
    });
    return {
      requestedHistory,
      syncedThreadState: false,
      forcedHistoryReload: false,
    };
  }
  const fallbackSyncStart = perfNow();
  await threadStore.syncThreadState(id);
  logWarn('ui', 'chat.selection.fallbackSync.done', {
    thread_id: id,
    duration_ms: Math.round(perfNow() - fallbackSyncStart),
    total_ms: Math.round(perfNow() - selectionStart),
  });
  return {
    requestedHistory,
    syncedThreadState: true,
    forcedHistoryReload: false,
  };
}




/**
 * @param {ThreadSelectionFreshness | null | undefined} freshness
 * @param {boolean} [pendingApplied=false]
 * @returns {boolean}
 */
export function shouldForceThreadSelectionScroll(freshness, pendingApplied = false) {
  if (pendingApplied) return false;
  return Boolean(freshness?.requestedHistory || freshness?.forcedHistoryReload);
}

/**
 * @param {BuildVisibleChatThreadCardsOptions | null | undefined} opts
 * @returns {VisibleChatThreadCardState}
 */
export function buildVisibleChatThreadCards(opts) {
  const {
    threads = [],
    selectedThreadId = '',
    pinnedMap = {},
    archivedMap = {},
    runtimeById = {},
    showArchived = false,
    displayNameOf,
    statusOf,
    statusHeaderOf,
    interruptibleOf,
    routingOf,
    pendingLaunchOf,
  } = opts || {};

  const safeThreads = Array.isArray(threads) ? threads : [];
  const safePinnedMap = pinnedMap && typeof pinnedMap === 'object' ? pinnedMap : {};
  const safeArchivedMap = archivedMap && typeof archivedMap === 'object' ? archivedMap : {};
  const safeRuntimeById = runtimeById && typeof runtimeById === 'object' ? runtimeById : {};

  /**
   * @param {ThreadCardSource} thread
   * @returns {boolean}
   */
  function isArchivedThread(thread) {
    const threadId = (thread?.id || '').toString();
    if (!threadId) return false;
    const archivedAt = Number(safeArchivedMap[threadId]) || 0;
    return Number.isFinite(archivedAt) && archivedAt > 0;
  }

  const activeThreads = safeThreads.filter((thread) => !isArchivedThread(thread));
  const archivedThreads = safeThreads.filter((thread) => isArchivedThread(thread));
  const visibleThreads = showArchived ? archivedThreads : activeThreads;

  const cards = visibleThreads.map((thread) => {
    const threadId = (thread?.id || '').toString();
    const displayName = (typeof displayNameOf === 'function' ? displayNameOf(thread) : '') || threadId;
    const pinnedAt = Number(safePinnedMap[threadId]) || 0;
    const archivedAt = Number(safeArchivedMap[threadId]) || 0;
    const isArchived = Number.isFinite(archivedAt) && archivedAt > 0;
    const runtime = safeRuntimeById[threadId];
    const cwdMismatch = Boolean(runtime?.cwdMismatch);
    const cwdMismatchReason = cwdMismatch ? ((runtime?.cwdMismatchReason || '').toString()) : '';
    return {
      id: threadId,
      name: displayName,
      showId: displayName === threadId,
      status: isArchived ? 'idle' : (typeof statusOf === 'function' ? statusOf(threadId) : 'idle'),
      statusHeader: isArchived ? '已归档' : ((typeof statusHeaderOf === 'function' ? statusHeaderOf(threadId) : '') || '等待指示'),
      interruptible: isArchived ? false : (typeof interruptibleOf === 'function' ? interruptibleOf(threadId) : false),
      pinnedAt,
      archivedAt,
      isArchived,
      isPinned: Number.isFinite(pinnedAt) && pinnedAt > 0,
      selected: threadId === selectedThreadId,

      cwdMismatch,
      cwdMismatchReason,
      provider: (runtime?.provider || '').toString().trim(),
      // Routing metadata captured by startThread (see stores/thread-actions-
      // helpers.js getThreadRouting). Empty when: thread started before the
      // router shipped, router did not match, or caller omitted routingOf.
      agentKey: (typeof routingOf === 'function'
        ? ((routingOf(threadId) || {}).agentKey || '')
        : '').toString().trim(),
      // Human-readable persona label ("SQL 与数据建模专家" or "候选池 · N 条")
      // surfaced by thread/start or turn/start; the badge shows this rather
      // than the opaque slug so users recognize which prompt is active.
      agentTitle: (typeof routingOf === 'function'
        ? ((routingOf(threadId) || {}).agentTitle || '')
        : '').toString().trim(),
      promptKey: (typeof routingOf === 'function'
        ? ((routingOf(threadId) || {}).promptKey || '')
        : '').toString().trim(),
      // P21 pool-merge: `thread.mergedCandidateKeys.length > 0` drives the
      // “候选池 · N 条” badge in ThreadRailSidePanel. Must be materialized
      // here (not left as undefined) because v-if touches .length on it.
      mergedCandidateKeys: (typeof routingOf === 'function'
        ? (Array.isArray((routingOf(threadId) || {}).mergedCandidateKeys)
            ? (routingOf(threadId) || {}).mergedCandidateKeys
            : [])
        : []),
      // Friendly titles aligned by index with mergedCandidateKeys, used as
      // the tooltip so users see "通用助手、SQL 与数据建模专家…" instead of
      // "main/default、main/sql…".
      mergedCandidateTitles: (typeof routingOf === 'function'
        ? (Array.isArray((routingOf(threadId) || {}).mergedCandidateTitles)
            ? (routingOf(threadId) || {}).mergedCandidateTitles
            : [])
        : []),
      // C1 pending-launch: thread row exists but provider CLI has not been
      // forked yet (awaiting first turn). Card renders a "待启动" marker and
      // the send button shows a "启动中…" state on the first send.
      pendingLaunch: typeof pendingLaunchOf === 'function'
        ? Boolean(pendingLaunchOf(threadId))
        : false,
    };
  });

  return {
    cards,
    activeCount: activeThreads.length,
    archivedCount: archivedThreads.length,
  };
}
