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
  };
}
