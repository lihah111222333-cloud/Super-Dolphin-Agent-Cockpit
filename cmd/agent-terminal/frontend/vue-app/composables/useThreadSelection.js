import { watch } from '../../lib/vue.esm-browser.prod.js';
import { logInfo, logWarn } from '../services/log.js';
import {
  ensureThreadSelectionFresh,
  isStaleThreadSelectionError,
} from '../utils/thread-page-utils.js';

/**
 * @typedef {import('../utils/thread-page-types').ThreadSelectionFreshness} ThreadSelectionFreshness
 * @typedef {import('../utils/thread-page-types').ThreadSelectionOptions} ThreadSelectionOptions
 */

/**
 * @param {ThreadSelectionOptions} opts
 * @returns {ReturnType<typeof watch>}
 */
export function useThreadSelection(opts) {
  const {
    selectedThreadId,
    threadStore,
    focusedDiffPath,
    focusedDiffLine,
    fallbackDiffText,
    fallbackMediaPreview,
    fallbackMarkdownPreview,
    scheduleScrollToBottom,
    resetScrollState,
  } = opts;

  return watch(
    () => selectedThreadId.value,
    /**
     * @param {string | null | undefined} id
     * @param {string | null | undefined} prevId
     * @returns {Promise<void>}
     */
    async (id, prevId) => {
      const nextId = (id || '').toString().trim();
      const previousId = (prevId || '').toString().trim();
      focusedDiffPath.value = '';
      focusedDiffLine.value = 0;
      fallbackDiffText.value = '';
      fallbackMediaPreview.value = null;
      fallbackMarkdownPreview.value = null;
      logInfo('ui', 'chat.selection.watch', {
        previous_thread_id: previousId,
        thread_id: nextId,
      });
      if (!nextId) return;

      const changedFromExistingThread = nextId !== previousId && Boolean(previousId);
      // [FIX] 仅在线程 ID 真正变化且 prevId 已有值时才重置:
      // - immediate 首次触发 (prevId 为 undefined → '') 时不 reset，让 scheduleScrollToBottom 处理
      // - 同 ID 重复写入（snapshot 覆写）不 reset
      if (resetScrollState && changedFromExistingThread) {
        resetScrollState();
        scheduleScrollToBottom(true);
      }

      /** @type {ThreadSelectionFreshness} */
      let freshness = {
        requestedHistory: false,
        syncedThreadState: false,
        forcedHistoryReload: false,
      };
      try {
        freshness = await ensureThreadSelectionFresh(threadStore, nextId, {
          reason: 'selection',
          previousThreadId: previousId,
        });
        logInfo('ui', 'chat.selection.history.checked', {
          previous_thread_id: previousId,
          thread_id: nextId,
          requested: freshness.requestedHistory,
          synced: freshness.syncedThreadState,
          forced: freshness.forcedHistoryReload,
        });
      } catch (error) {
        logWarn('ui', 'chat.selection.history.failed', {
          previous_thread_id: previousId,
          thread_id: nextId,
          error,
        });
        if (isStaleThreadSelectionError(error) && (selectedThreadId.value || '').toString().trim() === nextId) {
          selectedThreadId.value = '';
          logWarn('ui', 'chat.selection.stale_cleared', {
            previous_thread_id: previousId,
            thread_id: nextId,
            error: error?.message || String(error),
          });
          return;
        }
      }
      if ((selectedThreadId.value || '').toString().trim() !== nextId) return;
      if (!changedFromExistingThread) {
        scheduleScrollToBottom(true);
      }
      logInfo('ui', 'chat.selection.watch.done', {
        previous_thread_id: previousId,
        thread_id: nextId,
        forced_scroll: true,
      });
    },
    { immediate: true },
  );
}
