import { logInfo, logWarn } from '../services/log.js';

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
  try {
    await threadStore.loadMessages(id, undefined, loadOptions || undefined);
    logInfo('ui', 'chat.historyLoad.done', {
      thread_id: id,
    });
    return true;
  } catch (error) {
    logWarn('ui', 'chat.historyLoad.failed', {
      thread_id: id,
      error,
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
  if (shouldRefreshVisibleThread) {
    logInfo('ui', 'chat.threadState.refresh_visible_thread', {
      reason,
      previous_thread_id: previousThreadId,
      thread_id: id,
    });
    await threadStore.syncThreadState(id);
    const requestedHistory = await requestHistoryLoad(threadStore, id, { syncRuntime: false, force: true });
    return {
      requestedHistory,
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
    await threadStore.loadMessages(id);
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
    await threadStore.loadMessages(id);
    return {
      requestedHistory: true,
      syncedThreadState: false,
      forcedHistoryReload: false,
    };
  }

  const requestedHistory = await requestHistoryLoad(threadStore, id);
  if (requestedHistory) {
    return {
      requestedHistory,
      syncedThreadState: false,
      forcedHistoryReload: false,
    };
  }
  if (!id || typeof threadStore?.syncThreadState !== 'function') {
    return {
      requestedHistory,
      syncedThreadState: false,
      forcedHistoryReload: false,
    };
  }
  await threadStore.syncThreadState(id);
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
    };
  });

  return {
    cards,
    activeCount: activeThreads.length,
    archivedCount: archivedThreads.length,
  };
}
