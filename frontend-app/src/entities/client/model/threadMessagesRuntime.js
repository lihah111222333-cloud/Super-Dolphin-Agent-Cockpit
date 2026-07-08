import { optionalTextField, normalizeOptionalTextField, systemClockMillis } from './contractStoreModel.js';
function optionalUiArray() {
  return [];
}

function optionalUiObject() {
  return {};
}

// @ts-check

import {
  isVisibleTimelineItem,
  mergeTimelineItems } from './timelineRuntime.js';
import {
  normalizeThreadMessageItems } from './threadHistoryTimeline.js';
import {
  messagePageParams,
  normalizeThreadMessagesPageMeta,
  threadMessagesPaginationPatch,
  THREAD_MESSAGES_PAGE_SIZE } from './threadMessagesPagination.js';

/**
 * @typedef {{
 *   nowMillis?: () => number,
 *   getThreadMessages?: (params: Record<string, unknown>) => Promise<Record<string, unknown>>,
 * }} ThreadMessageFetcherDeps
 *
 * @typedef {{
 *   includeArchived?: boolean,
 *   historyFallback?: unknown[],
 * }} ThreadMessageLoadOptions
 */

function normalizeString(value) {
  return normalizeOptionalTextField(value);
}

function cleanObject(payload) {
  return Object.fromEntries(
    Object.entries(payload).filter(([, value]) => value !== undefined && value !== ''),
  );
}

export function markThreadMessagesReadyPatch(state, id) {
  const timelineWasReady = Boolean(state.threadTimelineReadyByThread?.[id]);
  const currentItems = state.timelinesByThread[id] || optionalUiArray();
  const hasActiveItems = currentItems.some((item) => item.done === false || item.optimistic);
  const preserve = timelineWasReady || hasActiveItems;
  return {
    timelinesByThread: {
      ...state.timelinesByThread,
      [id]: mergeTimelineItems(state.timelinesByThread[id] || optionalUiArray(), [], { preserveExistingVisible: preserve }),
    },
    threadTimelineReadyByThread: {
      ...state.threadTimelineReadyByThread,
      [id]: true,
    },
  };
}

export function applyThreadHistoryFallbackPatch(state, id, fallbackItems) {
  const items = Array.isArray(fallbackItems) ? fallbackItems.filter(isVisibleTimelineItem) : [];
  if (items.length === 0) return null;
  const existing = state.timelinesByThread[id] || optionalUiArray();
  const nextTimeline = existing.some(isVisibleTimelineItem)
    ? existing
    : mergeTimelineItems(existing, items, { preserveExistingVisible: true });
  return {
    timelinesByThread: {
      ...state.timelinesByThread,
      [id]: nextTimeline,
    },
    threadTimelineReadyByThread: {
      ...state.threadTimelineReadyByThread,
      [id]: true,
    },
    ...threadMessagesPaginationPatch(state, id, {
      hasMore: false,
      nextBefore: '',
      loading: false,
    }),
  };
}

export function threadHistoryInitialPageTracePayload(id, page, status, error) {
  return cleanObject({
    phase: 'frontend.thread_history.initial_page.load',
    thread_id: id,
    page_size: THREAD_MESSAGES_PAGE_SIZE,
    message_count: page?.messages?.length || 0,
    has_more: Boolean(page?.meta?.hasMore),
    next_before: page?.meta?.nextBefore ? 'present' : '',
    duration_ms: page?.durationMs,
    status,
    error_name: error?.name || optionalTextField(),
  });
}

export function applyThreadMessageItemsPatch(state, id, pageItems, pageMeta = {}) {
  return {
    timelinesByThread: {
      ...state.timelinesByThread,
      [id]: mergeTimelineItems(state.timelinesByThread[id] || optionalUiArray(), pageItems, { preserveExistingVisible: true }),
    },
    threadTimelineReadyByThread: {
      ...state.threadTimelineReadyByThread,
      [id]: true,
    },
    ...threadMessagesPaginationPatch(state, id, {
      hasMore: Boolean(pageMeta.hasMore),
      nextBefore: normalizeString(pageMeta.nextBefore),
      loading: false,
    }),
  };
}

/**
 * @param {ThreadMessageFetcherDeps} [deps]
 */
export function createThreadMessagePageFetcher({ getThreadMessages, nowMillis = () => systemClockMillis() } = {}) {
  if (typeof getThreadMessages !== 'function') throw new Error('getThreadMessages is required');
  return async function fetchThreadMessagePage(id, before = '') {
    const startedAt = nowMillis();
    const res = await getThreadMessages(messagePageParams(id, before));
    const page = Array.isArray(res?.messages) ? res.messages : [];
    return {
      messages: page,
      items: normalizeThreadMessageItems(page),
      meta: normalizeThreadMessagesPageMeta(res, page),
      durationMs: nowMillis() - startedAt,
    };
  };
}

function markThreadMessagesReady(set, id) {
  set((state) => markThreadMessagesReadyPatch(state, id));
}

function applyThreadHistoryFallback(set, id, fallbackItems) {
  const items = Array.isArray(fallbackItems) ? fallbackItems.filter(isVisibleTimelineItem) : [];
  if (items.length === 0) return false;
  set((state) => applyThreadHistoryFallbackPatch(state, id, fallbackItems));
  return true;
}

function applyThreadMessageItems(set, id, pageItems, pageMeta = {}) {
  set((state) => applyThreadMessageItemsPatch(state, id, pageItems, pageMeta));
}

export function attachThreadMessagesRuntime(runtime, deps = {}) {
  /*
   * 历史分页用 generation 防止慢请求写回到新线程。
   * 初始页和“更早消息”都会追加到同一条 timeline。
   */
  const {
    backendThreadIdForState,
    emitFrontendTraceEvent,
    getThreadMessages,
  } = deps;
  const { set, get, addWarning } = runtime;
  const { threadMessageGenerations } = runtime;
  const fetchThreadMessagePage = createThreadMessagePageFetcher({ getThreadMessages });
  const emitThreadHistoryInitialPageTrace = (id, page, status, error) => {
    emitFrontendTraceEvent(threadHistoryInitialPageTracePayload(id, page, status, error));
  };

  const nextThreadMessageGeneration = (id) => {
    const nextGeneration = (threadMessageGenerations.get(id) || 0) + 1;
    threadMessageGenerations.set(id, nextGeneration);
    return nextGeneration;
  };

  const isCurrentThreadMessageGeneration = (id, generation) => threadMessageGenerations.get(id) === generation;

  const setThreadMessagesLoading = (id, generation, loading) => {
    set((state) => {
      if (!isCurrentThreadMessageGeneration(id, generation)) return {};
      return threadMessagesPaginationPatch(state, id, { loading });
    });
  };

  /**
   * @param {string} threadId
   * @param {ThreadMessageLoadOptions} [options]
   */
  const loadThreadMessages = async (threadId, options = {}) => {
    /** @type {ThreadMessageLoadOptions} */
    const loadOptions = options && typeof options === 'object' ? options : {};
    const id = backendThreadIdForState(get(), threadId, { includeArchived: loadOptions.includeArchived === true });
    if (!id) return;
    const generation = nextThreadMessageGeneration(id);
    setThreadMessagesLoading(id, generation, true);
    try {
      const page = await fetchThreadMessagePage(id);
      emitThreadHistoryInitialPageTrace(id, page, 'ok');
      if (!isCurrentThreadMessageGeneration(id, generation)) return;
      if (page.messages.length === 0) {
        if (!applyThreadHistoryFallback(set, id, loadOptions.historyFallback)) {
          markThreadMessagesReady(set, id);
        }
        setThreadMessagesLoading(id, generation, false);
        return;
      }
      applyThreadMessageItems(set, id, page.items, page.meta);
    }
    catch (error) {
      emitThreadHistoryInitialPageTrace(id, null, 'error', error);
      addWarning('error', 'thread.messages.failed', { threadId: id, error: error.message });
    }
    finally {
      setThreadMessagesLoading(id, generation, false);
    }
  };

  /**
   * @param {string} threadId
   * @param {ThreadMessageLoadOptions & { loadMessages?: boolean }} syncOptions
   */
  const startThreadMessagesLoad = async (threadId, syncOptions) => {
    if (syncOptions.loadMessages === false) return;
    await loadThreadMessages(threadId, {
      includeArchived: syncOptions.includeArchived === true,
      historyFallback: syncOptions.historyFallback,
    });
  };

  /**
   * @param {string} threadId
   * @param {ThreadMessageLoadOptions} [options]
   */
  const loadOlderThreadMessages = async (threadId, options = {}) => {
    /** @type {ThreadMessageLoadOptions} */
    const loadOptions = options && typeof options === 'object' ? options : {};
    const id = backendThreadIdForState(get(), threadId, { includeArchived: loadOptions.includeArchived === true });
    if (!id) return false;
    const pagination = get().threadMessagePaginationByThread?.[id] || optionalUiObject();
    if (pagination.loading) return false;
    if (!pagination.hasMore) return false;
    const before = normalizeString(pagination.nextBefore);
    if (!before) {
      addWarning('error', 'thread.messages.pagination.missing_cursor', { threadId: id });
      return false;
    }
    const generation = threadMessageGenerations.get(id) || nextThreadMessageGeneration(id);
    setThreadMessagesLoading(id, generation, true);
    try {
      const page = await fetchThreadMessagePage(id, before);
      if (!isCurrentThreadMessageGeneration(id, generation)) return false;
      if (page.messages.length === 0) {
        markThreadMessagesReady(set, id);
        set((state) => threadMessagesPaginationPatch(state, id, {
          hasMore: false,
          nextBefore: '',
          loading: false,
        }));
        return true;
      }
      applyThreadMessageItems(set, id, page.items, page.meta);
      return true;
    }
    catch (error) {
      if (isCurrentThreadMessageGeneration(id, generation)) {
        addWarning('error', 'thread.messages.failed', { threadId: id, error: error.message });
      }
      return false;
    }
    finally {
      setThreadMessagesLoading(id, generation, false);
    }
  };

  Object.assign(runtime, { loadThreadMessages, startThreadMessagesLoad, loadOlderThreadMessages });
}
