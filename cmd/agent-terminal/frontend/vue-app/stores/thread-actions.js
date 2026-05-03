// @ts-nocheck
 import {
   compactThread,
   forceCompleteThread,
   getThreadConfig,
   getThreadArchivedAt,
   getThreadPinnedAt,
   promptRenameThread,
   recoverThread,
   renameThread,
   saveActiveCmdThread,
   saveActiveThread,
   sendMessage,
   setThreadConfig,
 
   setThreadArchived,
   setThreadPinned,
   startThread,
   stopThread,
 } from './thread-actions-helpers.js';
import {
  PREF_ARCHIVED_THREADS_CHAT,
  PREF_PINNED_THREADS_CHAT,
} from './thread-preference.model.js';

async function batchDeleteStaleThreads(ctx, threadIds) {
  const ids = Array.isArray(threadIds) ? threadIds.filter(id => id) : [];
  if (!ids.length) return { deleted: 0, failed: 0 };
  const { logInfo, logWarn, callAPI } = ctx;
  logInfo('thread', 'batch_delete.start', { count: ids.length });
  const deletedIds = [];
  for (const id of ids) {
    try {
      await callAPI('thread/delete', { threadId: id });
      deletedIds.push(id);
    } catch (e) {
      logWarn('thread', 'batch_delete.item_failed', { thread_id: id, error: e });
    }
  }
  if (!deletedIds.length) return { deleted: 0, failed: ids.length };
  const deletedSet = new Set(deletedIds);
  ctx.state.threads = (ctx.state.threads || []).filter(t => !deletedSet.has(t?.id));
  const nextArchived = { ...(ctx.state.archivedThreadAtById || {}) };
  const nextPinned = { ...(ctx.state.pinnedThreadAtById || {}) };
  for (const id of deletedIds) { delete nextArchived[id]; delete nextPinned[id]; }
  ctx.state.archivedThreadAtById = nextArchived;
  ctx.state.pinnedThreadAtById = nextPinned;
  const { _optimisticPreferenceMapTaints, OPTIMISTIC_LEAK_GUARD_MS } = await import('./thread-optimistic.js');
  _optimisticPreferenceMapTaints.set('archivedThreadAtById', Date.now() + OPTIMISTIC_LEAK_GUARD_MS);
  _optimisticPreferenceMapTaints.set('pinnedThreadAtById', Date.now() + OPTIMISTIC_LEAK_GUARD_MS);
  await Promise.all([
    ctx.persistPreferenceAndSync(PREF_ARCHIVED_THREADS_CHAT, nextArchived, { batch_deleted: deletedIds.length }, { syncAfterPersist: false }),
    ctx.persistPreferenceAndSync(PREF_PINNED_THREADS_CHAT, nextPinned, { batch_deleted: deletedIds.length }, { syncAfterPersist: false }),
  ]);
  await ctx.refreshSidebarState();

  logInfo('thread', 'batch_delete.done', { deleted: deletedIds.length, failed: ids.length - deletedIds.length });
  return { deleted: deletedIds.length, failed: ids.length - deletedIds.length };
}

export function createThreadActions(state, deps) {
  const ctx = { state, ...deps };
  return {
    saveActiveThread: (id) => saveActiveThread(ctx, id),
    saveActiveCmdThread: (id) => saveActiveCmdThread(ctx, id),

    renameThread: (threadId, name) => renameThread(ctx, threadId, name),
    getThreadConfig: (threadId) => getThreadConfig(ctx, threadId),
    setThreadConfig: (threadId, config) => setThreadConfig(ctx, threadId, config),
    startThread: (cwd, options) => startThread(ctx, cwd, options),
    stopThread: (threadId, options) => stopThread(ctx, threadId, options),
    recoverThread: (threadId) => recoverThread(ctx, threadId),
    sendMessage: (threadId, prompt, attachments, options) => sendMessage(ctx, threadId, prompt, attachments, options),
    compactThread: (threadId) => compactThread(ctx, threadId),
    forceCompleteThread: (threadId) => forceCompleteThread(ctx, threadId),
    getThreadPinnedAt: (threadId) => getThreadPinnedAt(ctx, threadId),
    getThreadArchivedAt: (threadId) => getThreadArchivedAt(ctx, threadId),
    setThreadPinned: (threadId, pinned) => setThreadPinned(ctx, threadId, pinned),
    toggleThreadPin: (threadId) => setThreadPinned(ctx, threadId, !(getThreadPinnedAt(ctx, threadId) > 0)),
    setThreadArchived: (threadId, archived) => setThreadArchived(ctx, threadId, archived),
    toggleThreadArchive: (threadId) => {
      const id = (threadId || '').toString().trim();
      if (!id) return Promise.resolve();
      return setThreadArchived(ctx, id, !(getThreadArchivedAt(ctx, id) > 0));
    },
    promptRenameThread: (threadId) => promptRenameThread(ctx, threadId),
    batchDeleteStaleThreads: (threadIds) => batchDeleteStaleThreads(ctx, threadIds),
  };
}
