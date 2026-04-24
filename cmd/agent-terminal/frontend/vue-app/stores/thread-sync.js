// @ts-nocheck
import { normalizeThreadID } from './bridge-event-parser.js';
import { createSyncThreadDiffState } from './thread-diff-sync.js';
import {
  beginRuntimeSnapshotRequest,
  buildSyncContext,
  handleBridgeEvent,
  isLatestRuntimeSnapshotRequest,
  loadMessages,
  perfNow,
  refreshSidebarState,
  syncRuntimeState,
  syncThreadState,
} from './thread-sync-helpers.js';
import {
  getThreadActivityStats,
  getThreadAlerts,
  getThreadDiff,
  getThreadInterruptible,
  getThreadStatus,
  getThreadStatusDetails,
  getThreadStatusHeader,
  getThreadTimeline,
  getThreadTokenUsage,
  shouldReloadThreadHistory,
} from './thread-sync-selectors.js';

export function createSyncManager(state, deps) {
  const ctx = buildSyncContext(state, deps);
  ctx.syncThreadDiffState = createSyncThreadDiffState({
    state,
    threadDiffSyncPromiseByThread: ctx.threadDiffSyncPromiseByThread,
    threadDiffLoadedRevisionByThread: ctx.threadDiffLoadedRevisionByThread,
    normalizeThreadID,
    getPreferenceScopeCwd: ctx.getPreferenceScopeCwd,
    callAPI: ctx.callAPI,
    withPreferenceScope: ctx.withPreferenceScope,
    logInfo: ctx.logInfo,
    logWarn: ctx.logWarn,
    perfNow,
    applyRuntimeSnapshot: ctx.applyRuntimeSnapshot,
  });
  return {
    beginRuntimeSnapshotRequest: (threadId) => beginRuntimeSnapshotRequest(ctx, threadId),
    isLatestRuntimeSnapshotRequest: (meta) => isLatestRuntimeSnapshotRequest(ctx, meta),
    syncRuntimeState: () => syncRuntimeState(ctx),
    syncThreadState: (threadId) => syncThreadState(ctx, threadId),
    syncThreadDiffState: ctx.syncThreadDiffState,
    refreshSidebarState: () => refreshSidebarState(ctx),
    handleAgentEvent: (evt) => handleBridgeEvent(ctx, evt),
    handleBridgeEvent: (evt) => handleBridgeEvent(ctx, evt),
    loadMessages: (threadId, limit = 300, options = {}) => loadMessages(ctx, threadId, limit, options),
    shouldReloadThreadHistory: (threadId) => shouldReloadThreadHistory(ctx, threadId),
    getThreadInterruptible: (threadId) => getThreadInterruptible(ctx, threadId),
    getThreadTimeline: (threadId) => getThreadTimeline(ctx, threadId),
    getThreadDiff: (threadId) => getThreadDiff(ctx, threadId),
    getThreadStatus: (threadId) => getThreadStatus(ctx, threadId),
    getThreadStatusHeader: (threadId) => getThreadStatusHeader(ctx, threadId),
    getThreadStatusDetails: (threadId) => getThreadStatusDetails(ctx, threadId),
    getThreadTokenUsage: (threadId) => getThreadTokenUsage(ctx, threadId),
    getThreadActivityStats: (threadId) => getThreadActivityStats(ctx, threadId),
    getThreadAlerts: (threadId) => getThreadAlerts(ctx, threadId),
    markHistoryLoaded: (threadId) => { ctx.threadHistoryLoadedAtByThread.set(threadId, Date.now()); },
    /** 注入 scroll 保护函数：在 applyRuntimeSnapshot 前后保存/恢复 scrollTop */
    setScrollGuard: (save, restore) => {
      ctx.saveScrollPosition = typeof save === 'function' ? save : null;
      ctx.restoreScrollPosition = typeof restore === 'function' ? restore : null;
    },
  };
}
