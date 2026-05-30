import { logDebug, logInfo, logWarn } from '../services/log.js';
import { perfNow } from '../stores/thread-actions-helpers.js';

/**
 * @typedef {import('./thread-page-types').BuildVisibleChatThreadCardsOptions} BuildVisibleChatThreadCardsOptions
 * @typedef {import('./thread-page-types').ThreadCardSource} ThreadCardSource
 * @typedef {import('./thread-page-types').ThreadSelectionFreshness} ThreadSelectionFreshness
 * @typedef {import('./thread-page-types').ThreadSelectionFreshnessOptions} ThreadSelectionFreshnessOptions
 * @typedef {import('./thread-page-types').ThreadStoreLike} ThreadStoreLike
 * @typedef {import('./thread-page-types').VisibleChatThreadCardState} VisibleChatThreadCardState
 */

const HISTORY_LOAD_WARN_MS = 500;
const SELECTION_FLOW_WARN_MS = 500;

export function isStaleThreadSelectionError(error) {
  const text = [
    typeof error === 'string' ? error : '',
    error?.message || '',
    error?.cause?.message || '',
  ].join('\n').toLowerCase();
  if (text.includes('session not found') || text.includes('session is not available')) return true;
  if (text.includes('thread "') && text.includes('not found: store: not found')) return true;
  return text.includes('resolve session: thread') && text.includes('context deadline exceeded');
}

function logTimedDebugOrWarn(event, fields, durationMs, warnThresholdMs) {
  const log = durationMs > warnThresholdMs ? logWarn : logDebug;
  log('ui', event, {
    ...fields,
    warn_threshold_ms: warnThresholdMs,
  });
}


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
    logTimedDebugOrWarn('chat.historyLoad.done_timed', {
      thread_id: id,
      duration_ms: loadDuration,
      force_reload: forceReload,
    }, loadDuration, HISTORY_LOAD_WARN_MS);
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
  logDebug('ui', 'chat.selection.fresh.start', {
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
    const requestedHistory = await requestHistoryLoad(threadStore, id, { syncRuntime: false, force: true }).catch((err) => {
      logWarn('ui', 'chat.selection.requestHistoryLoad.failed', { thread_id: id, error: err?.message || String(err) });
      if (isStaleThreadSelectionError(err)) {
        throw err;
      }
      return false;
    });
    await threadStore.syncThreadState(id).catch((err) => {
      logWarn('ui', 'chat.selection.syncThreadState.failed', { thread_id: id, error: err?.message || String(err) });
      if (isStaleThreadSelectionError(err)) {
        throw err;
      }
    });
    const concurrentDuration = Math.round(perfNow() - concurrentStart);
    const totalDuration = Math.round(perfNow() - selectionStart);
    logTimedDebugOrWarn('chat.selection.concurrentLoad.done', {
      thread_id: id,
      requested_history: requestedHistory,
      duration_ms: concurrentDuration,
      total_ms: totalDuration,
    }, totalDuration, SELECTION_FLOW_WARN_MS);
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
    const totalDuration = Math.round(perfNow() - selectionStart);
    logTimedDebugOrWarn('chat.selection.forceReload.done', {
      thread_id: id,
      duration_ms: Math.round(perfNow() - forceStart),
      total_ms: totalDuration,
    }, totalDuration, SELECTION_FLOW_WARN_MS);
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
    const totalDuration = Math.round(perfNow() - selectionStart);
    logTimedDebugOrWarn('chat.selection.refreshLoadMessages.done', {
      thread_id: id,
      duration_ms: Math.round(perfNow() - refreshStart),
      total_ms: totalDuration,
    }, totalDuration, SELECTION_FLOW_WARN_MS);
    return {
      requestedHistory: true,
      syncedThreadState: false,
      forcedHistoryReload: false,
    };
  }

  const fallbackStart = perfNow();
  const requestedHistory = await requestHistoryLoad(threadStore, id);
  if (requestedHistory) {
    const totalDuration = Math.round(perfNow() - selectionStart);
    logTimedDebugOrWarn('chat.selection.fallbackHistory.done', {
      thread_id: id,
      duration_ms: Math.round(perfNow() - fallbackStart),
      total_ms: totalDuration,
    }, totalDuration, SELECTION_FLOW_WARN_MS);
    return {
      requestedHistory,
      syncedThreadState: false,
      forcedHistoryReload: false,
    };
  }
  if (!id || typeof threadStore?.syncThreadState !== 'function') {
    logDebug('ui', 'chat.selection.fresh.no_action', {
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
  const totalDuration = Math.round(perfNow() - selectionStart);
  logTimedDebugOrWarn('chat.selection.fallbackSync.done', {
    thread_id: id,
    duration_ms: Math.round(perfNow() - fallbackSyncStart),
    total_ms: totalDuration,
  }, totalDuration, SELECTION_FLOW_WARN_MS);
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

function nowMs() {
  return (typeof performance !== 'undefined' && typeof performance.now === 'function')
    ? performance.now()
    : Date.now();
}

function markCardPerf(perf, stage, startedAt, fields = {}) {
  if (!perf || typeof perf.mark !== 'function') return;
  perf.mark(stage, nowMs() - startedAt, fields);
}

/**
 * @param {BuildVisibleChatThreadCardsOptions | null | undefined} opts
 * @returns {VisibleChatThreadCardState}
 */
function multiAgentOrderKey(name) {
  const match = (name || '').toString().trim().match(/^agent\s*(\d+)\b/i);
  if (!match) return 0;
  const value = Number.parseInt(match[1], 10);
  return Number.isFinite(value) && value > 0 ? value : 0;
}

function decorateMultiAgentCards(cards) {
  let group = 0;
  let seenInGroup = new Set();
  return cards.map((card, index) => {
    const order = multiAgentOrderKey(card?.name);
    if (order <= 0) return { card, index, order: 0, group: Number.MAX_SAFE_INTEGER };
    if (seenInGroup.has(order)) {
      group += 1;
      seenInGroup = new Set();
    }
    seenInGroup.add(order);
    return { card, index, order, group };
  });
}

function sortMultiAgentCardsInPlace(cards) {
  if (!Array.isArray(cards) || cards.length <= 1) return cards;
  const decorated = decorateMultiAgentCards(cards);
  const hasMultiAgent = decorated.some((entry) => entry.order > 0);
  if (!hasMultiAgent) return cards;
  decorated.sort((left, right) => {
    if (left.group !== right.group) return left.group - right.group;
    const leftOrder = left.order || Number.MAX_SAFE_INTEGER;
    const rightOrder = right.order || Number.MAX_SAFE_INTEGER;
    if (leftOrder !== rightOrder) return leftOrder - rightOrder;
    return left.index - right.index;
  });
  for (let index = 0; index < decorated.length; index += 1) cards[index] = decorated[index].card;
  return cards;
}

export function buildVisibleChatThreadCards(opts) {
  const totalStart = nowMs();
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
    perf,
  } = opts || {};

  const normalizeStart = nowMs();
  const safeThreads = Array.isArray(threads) ? threads : [];
  const safePinnedMap = pinnedMap && typeof pinnedMap === 'object' ? pinnedMap : {};
  const safeArchivedMap = archivedMap && typeof archivedMap === 'object' ? archivedMap : {};
  const safeRuntimeById = runtimeById && typeof runtimeById === 'object' ? runtimeById : {};
  markCardPerf(perf, 'normalize_inputs', normalizeStart, { source_count: safeThreads.length });

  /**
   * @param {ThreadCardSource} thread
   * @returns {boolean}
   */
  function threadLifecycleState(thread) {
    return (thread?.lifecycleStatus || thread?.state || thread?.status || thread?.threadStatus || '').toString().trim().toLowerCase();
  }

  function isDeletedThread(thread) {
    return threadLifecycleState(thread) === 'deleted';
  }

  function isArchivedThread(thread) {
    const threadId = (thread?.id || '').toString();
    if (!threadId || isDeletedThread(thread)) return false;
    const rawState = threadLifecycleState(thread);
    if (rawState === 'archived') return true;
    const archivedAt = Number(safeArchivedMap[threadId]) || 0;
    return Number.isFinite(archivedAt) && archivedAt > 0;
  }

  const STALE_EXPIRY_MS = 7 * 24 * 60 * 60 * 1000;
  function detectStaleReason(archivedAt, showId) {
    const hasRealArchivedAt = Number.isFinite(archivedAt) && archivedAt > STALE_EXPIRY_MS;
    if (hasRealArchivedAt && (Date.now() - archivedAt) > STALE_EXPIRY_MS) return 'expired';
    if (showId) return 'empty';
    return '';
  }

  const partitionStart = nowMs();
  const activeThreads = [];
  const archivedThreads = [];
  for (const thread of safeThreads) {
    if (isDeletedThread(thread)) continue;
    if (isArchivedThread(thread)) archivedThreads.push(thread);
    else activeThreads.push(thread);
  }

  const visibleThreads = showArchived ? archivedThreads : activeThreads;
  markCardPerf(perf, 'partition_threads', partitionStart, {
    source_count: safeThreads.length,
    active_count: activeThreads.length,
    archived_count: archivedThreads.length,
    visible_count: visibleThreads.length,
    show_archived: Boolean(showArchived),
  });

  let displayNameCalls = 0;
  let statusCalls = 0;
  let statusHeaderCalls = 0;
  let interruptibleCalls = 0;
  let routingCalls = 0;
  let pendingLaunchCalls = 0;
  let runtimeHits = 0;
  const cardStart = nowMs();
  const seenIds = new Set();
  const cards = visibleThreads.map((thread) => {
    const threadId = (thread?.id || '').toString();
    if (seenIds.has(threadId)) {
      logWarn('ui', 'chat.cards.duplicate_thread', { thread_id: threadId, thread_name: thread?.name });
    }
    seenIds.add(threadId);

    let displayName = '';
    if (typeof displayNameOf === 'function') {
      displayNameCalls += 1;
      displayName = displayNameOf(thread);
    }
    displayName = displayName || '新对话';
    const pinnedAt = Number(safePinnedMap[threadId]) || 0;
    const archivedAt = Number(safeArchivedMap[threadId]) || 0;
    const isArchived = isArchivedThread(thread);
    const runtime = safeRuntimeById[threadId];
    if (runtime) runtimeHits += 1;
    const cwdMismatch = Boolean(runtime?.cwdMismatch);
    const cwdMismatchReason = cwdMismatch ? ((runtime?.cwdMismatchReason || '').toString()) : '';
    let routing = {};
    if (typeof routingOf === 'function') {
      routingCalls += 1;
      routing = routingOf(threadId) || {};
    }
    let pendingLaunch = false;
    if (typeof pendingLaunchOf === 'function') {
      pendingLaunchCalls += 1;
      pendingLaunch = Boolean(pendingLaunchOf(threadId));
    }
    const staleReason = isArchived ? detectStaleReason(archivedAt, displayName === threadId) : '';
    let status = 'idle';
    let statusHeader = '已归档';
    let interruptible = false;
    if (!isArchived) {
      if (typeof statusOf === 'function') {
        statusCalls += 1;
        status = statusOf(threadId);
      }
      if (typeof statusHeaderOf === 'function') {
        statusHeaderCalls += 1;
        statusHeader = statusHeaderOf(threadId) || '等待指示';
      } else {
        statusHeader = '等待指示';
      }
      if (typeof interruptibleOf === 'function') {
        interruptibleCalls += 1;
        interruptible = interruptibleOf(threadId);
      }
    }
    return {
      id: threadId,
      name: displayName,
      showId: displayName === threadId,
      status,
      statusHeader,
      interruptible,
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
      agentKey: ((routing.agentKey || '')).toString().trim(),
      // Human-readable persona label ("SQL 与数据建模专家" or "候选池 · N 条")
      // surfaced by thread/start or turn/start; the badge shows this rather
      // than the opaque slug so users recognize which prompt is active.
      agentTitle: ((routing.agentTitle || '')).toString().trim(),
      promptKey: ((routing.promptKey || '')).toString().trim(),
      // C1 pending-launch: thread row exists but provider CLI has not been
      // forked yet (awaiting first turn). Card renders a "待启动" marker and
      // the send button shows a "启动中…" state on the first send.
      pendingLaunch,
      isStale: Boolean(staleReason),
      staleReason,
    };
  });
  if (showArchived && cards.length > 1) {
    cards.sort((a, b) => (b.archivedAt || 0) - (a.archivedAt || 0));
  } else {
    sortMultiAgentCardsInPlace(cards);
  }
  markCardPerf(perf, 'build_cards', cardStart, {
    visible_count: visibleThreads.length,
    card_count: cards.length,
    runtime_hits: runtimeHits,
    display_name_calls: displayNameCalls,
    status_calls: statusCalls,
    status_header_calls: statusHeaderCalls,
    interruptible_calls: interruptibleCalls,
    routing_calls: routingCalls,
    pending_launch_calls: pendingLaunchCalls,
  });
  markCardPerf(perf, 'total_build_visible_cards', totalStart, {
    source_count: safeThreads.length,
    card_count: cards.length,
  });

  return {
    cards,
    activeCount: activeThreads.length,
    archivedCount: archivedThreads.length,
  };
}
