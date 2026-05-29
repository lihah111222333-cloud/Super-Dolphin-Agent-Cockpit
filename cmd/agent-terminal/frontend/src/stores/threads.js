// @ts-nocheck
import { reactive, watch } from '../../lib/vue.esm-browser.prod.js';
import { createStore } from 'zustand/vanilla';
import { useStore } from 'zustand';
import * as React from 'react';
import { callAPI } from '../services/api.js';
import { logDebug, logInfo, logWarn } from '../services/log.js';
import { assertThreadStoreStateWhitelist, THREAD_STORE_UI_LOCAL_STATE_WHITELIST, THREAD_STORE_RUNTIME_STATE_KEYS } from './thread-state-whitelist.js';
import { createPreferenceManager, withPreferenceScope, shouldSyncAfterPreferencePersist } from './thread-prefs.js';
import { createSyncManager } from './thread-sync.js';
import { createThreadActions } from './thread-actions.js';
import { createThreadViewHelpers } from './thread-store-view.js';
import {
  COMPACT_COMPLETION_TIMEOUT_MS,
  compactPendingByThread,
  compactWaitersByThread,
  getCompactWaiter,
  getThreadCompacting,
  getThreadCompactResult,
  getThreadCompactSuccessCount,
  setCompactResult,
  settleCompactWaiter,
  waitForCompactCompletion,
  cancelCompactWaiter,
} from './thread-compact.js';
import {
  applyRuntimeSnapshot,
  freezeTimelineItemsAtomic,
  markLocalActiveCmdThreadDirty,
  markLocalActiveThreadDirty,
  normalizeProviderThreadID,
} from './thread-snapshot.js';
import { clearThreadSendHoldNoticesInState } from './thread-send-block.js';

export { normalizeThreadID, toNormalizedEventString, getBridgeEventThreadId, getBridgeEventMethod, getBridgeEventType, getBridgeEventCommand, collectBridgeEventItemKinds, isContextCompactionItemKind, isCompactCommand } from './bridge-event-parser.js';
export { normalizeEpochMillis, parseEpochMillis, parseThreadCreatedAtFromID, ensureThreadOrderIndex, sortThreadsByStableFirstSeen } from './thread-time-utils.js';
export { normalizePreferenceScopeCwd, normalizeSplitRatio, normalizeThreadRailWidth, normalizeCmdCardCols, normalizeThread, normalizeThreadTimestampMap } from './thread-ui-normalize.js';
export { withPreferenceScope, shouldSyncAfterPreferencePersist } from './thread-prefs.js';

const state = reactive({ activeThreadId: '', activeCmdThreadId: '', pinnedThreadAtById: {}, archivedThreadAtById: {}, promptStaleNotice: '', sendBlockedNoticesByThread: {}, sendHoldNoticesByThread: {} });
const runtimeRootState = reactive({
  threads: [], statuses: {}, interruptibleByThread: {}, viewPrefsChat: null, viewPrefsCmd: null,
  statusHeadersByThread: {}, statusDetailsByThread: {}, overlayTextByThread: {}, overlayTypeByThread: {}, overlayPriorityByThread: {},
  timelinesByThread: {}, diffTextByThread: {}, diffRevisionByThread: {},
  tokenUsageByThread: {}, agentMetaById: {}, agentRuntimeById: {}, mainAgentId: '', mainAgentState: '', partial: false,
  activityStatsByThread: {}, alertsByThread: {}, skillRevision: 0,
  kickoffByThread: {},
});

const vueStateProxy = new Proxy(state, {
  get(target, key) {
    if (THREAD_STORE_RUNTIME_STATE_KEYS.includes(key)) {
      return runtimeRootState[key];
    }
    return target[key];
  },
  set(target, key, value) {
    if (THREAD_STORE_RUNTIME_STATE_KEYS.includes(key)) {
      runtimeRootState[key] = value;
      return true;
    }
    target[key] = value;
    return true;
  },
  ownKeys(target) {
    return [...Reflect.ownKeys(target), ...THREAD_STORE_RUNTIME_STATE_KEYS];
  },
  getOwnPropertyDescriptor(target, key) {
    if (THREAD_STORE_RUNTIME_STATE_KEYS.includes(key)) {
      return {
        enumerable: true,
        configurable: true,
        writable: true,
      };
    }
    return Reflect.getOwnPropertyDescriptor(target, key);
  }
});

assertThreadStoreStateWhitelist(state, 'thread-store.init');
logInfo('thread', 'state.whitelist.applied', { ui_local_keys: THREAD_STORE_UI_LOCAL_STATE_WHITELIST.length, runtime_accessor_keys: THREAD_STORE_RUNTIME_STATE_KEYS.length });

export const threadStoreVanilla = createStore((set) => ({
  activeThreadId: '',
  activeCmdThreadId: '',
  pinnedThreadAtById: {},
  archivedThreadAtById: {},
  promptStaleNotice: '',
  sendBlockedNoticesByThread: {},
  sendHoldNoticesByThread: {},
  threads: [],
  statuses: {},
  interruptibleByThread: {},
  viewPrefsChat: null,
  viewPrefsCmd: null,
  statusHeadersByThread: {},
  statusDetailsByThread: {},
  overlayTextByThread: {},
  overlayTypeByThread: {},
  overlayPriorityByThread: {},
  timelinesByThread: {},
  diffTextByThread: {},
  diffRevisionByThread: {},
  tokenUsageByThread: {},
  agentMetaById: {},
  agentRuntimeById: {},
  mainAgentId: '',
  mainAgentState: '',
  partial: false,
  activityStatsByThread: {},
  alertsByThread: {},
  skillRevision: 0,
  kickoffByThread: {},
}));

// Sync from Vue reactive states to Zustand store
watch(
  () => [state, runtimeRootState],
  () => {
    const nextState = {};
    for (const key of Object.keys(state)) {
      nextState[key] = state[key];
    }
    for (const key of THREAD_STORE_RUNTIME_STATE_KEYS) {
      nextState[key] = runtimeRootState[key];
    }
    threadStoreVanilla.setState(nextState);
  },
  { deep: true, flush: 'sync' }
);

// Sync from Zustand store to Vue reactive states
threadStoreVanilla.subscribe((newState) => {
  for (const key of Object.keys(newState)) {
    if (THREAD_STORE_RUNTIME_STATE_KEYS.includes(key)) {
      if (runtimeRootState[key] !== newState[key]) {
        runtimeRootState[key] = newState[key];
      }
    } else {
      if (state[key] !== newState[key]) {
        state[key] = newState[key];
      }
    }
  }
});

const viewHelpers = createThreadViewHelpers(vueStateProxy);
const serviceDeps = { callAPI, logDebug, logInfo, logWarn };
let syncManagerRef = null;
const preferenceManager = createPreferenceManager(vueStateProxy, {
  syncRuntimeState: (...args) => syncManagerRef?.syncRuntimeState?.(...args) || Promise.resolve(),
});
const syncManager = createSyncManager(vueStateProxy, {
  ...serviceDeps,
  applyRuntimeSnapshot,
  withPreferenceScope,
  getPreferenceScopeCwd: preferenceManager.getPreferenceScopeCwd,
  getCompactWaiter,
  settleCompactWaiter,
  setCompactResult,
  compactWaitersByThread,
  freezeTimelineItemsAtomic,
  normalizeProviderThreadID,
});
syncManagerRef = syncManager;
async function syncRuntimeStateAndClearSendHolds(...args) {
  const result = await syncManager.syncRuntimeState(...args);
  clearThreadSendHoldNoticesInState(vueStateProxy);
  return result;
}
async function refreshSidebarStateAndClearSendHolds(...args) {
  const result = await syncManager.refreshSidebarState(...args);
  clearThreadSendHoldNoticesInState(vueStateProxy);
  return result;
}
const actionManager = createThreadActions(vueStateProxy, {
  ...serviceDeps,
  syncRuntimeState: syncRuntimeStateAndClearSendHolds,
  syncThreadState: syncManager.syncThreadState,
  loadMessages: syncManager.loadMessages,
  markHistoryLoaded: syncManager.markHistoryLoaded,
  refreshSidebarState: refreshSidebarStateAndClearSendHolds,
  persistPreferenceAndSync: preferenceManager.persistPreferenceAndSync,
  withPreferenceScope,
  getPreferenceScopeCwd: preferenceManager.getPreferenceScopeCwd,
  markLocalActiveThreadDirty,
  markLocalActiveCmdThreadDirty,
  setCompactResult,
  waitForCompactCompletion,
  cancelCompactWaiter,
  compactPendingByThread,
  COMPACT_COMPLETION_TIMEOUT_MS,
  getThreadInterruptible: syncManager.getThreadInterruptible,
  displayName: viewHelpers.displayName,
});

function isVueContext() {
  if (typeof window !== 'undefined' && window.__VUE_SETUP_ACTIVE__) return true;
  if (typeof window !== 'undefined' && window.__REACT_APP_ACTIVE__) return false;
  try {
    const dispatcher =
      React.__SECRET_INTERNALS_DO_NOT_USE_OR_YOU_WILL_BE_FIRED?.ReactCurrentDispatcher?.current ||
      React.__CLIENT_INTERNALS_DO_NOT_USE_OR_WARN_USERS_THEY_CANNOT_UPGRADE?.H;
    return !dispatcher;
  } catch (e) {
    return true;
  }
}

export function useThreadStore() {
  let stateSnapshot;
  let isReact = true;
  if (isVueContext()) {
    stateSnapshot = vueStateProxy;
    isReact = false;
  } else {
    try {
      stateSnapshot = useStore(threadStoreVanilla);
    } catch (e) {
      stateSnapshot = vueStateProxy;
      isReact = false;
    }
  }

  return {
    get state() {
      if (isVueContext() || !isReact) {
        return vueStateProxy;
      }
      const _ = stateSnapshot; // register React dependency
      return threadStoreVanilla.getState();
    },
    setPreferenceScopeCwd: preferenceManager.setPreferenceScopeCwd,
    getPreferenceScopeCwd: preferenceManager.getPreferenceScopeCwd,
    saveActiveThread: actionManager.saveActiveThread,
    refreshSidebarState: refreshSidebarStateAndClearSendHolds,
    syncThreadState: syncManager.syncThreadState,
    syncThreadDiffState: syncManager.syncThreadDiffState,
    getThreadConfig: actionManager.getThreadConfig,
    setThreadConfig: actionManager.setThreadConfig,
    startThread: actionManager.startThread,
    stopThread: actionManager.stopThread,
    recoverThread: actionManager.recoverThread,
    compactThread: actionManager.compactThread,
    forceCompleteThread: actionManager.forceCompleteThread,
    loadMessages: syncManager.loadMessages,
    markHistoryLoaded: syncManager.markHistoryLoaded,
    sendMessage: actionManager.sendMessage,
    getThreadSendBlockedNotice: actionManager.getThreadSendBlockedNotice,
    isThreadSendBlocked: actionManager.isThreadSendBlocked,
    clearThreadSendBlockedNotice: actionManager.clearThreadSendBlockedNotice,
    handleAgentEvent: syncManager.handleAgentEvent,
    handleBridgeEvent: syncManager.handleBridgeEvent,
    saveActiveCmdThread: actionManager.saveActiveCmdThread,

    renameThread: actionManager.renameThread,
    promptRenameThread: actionManager.promptRenameThread,
    getLayout: preferenceManager.getLayout,
    setLayout: preferenceManager.setLayout,
    getSplitRatio: preferenceManager.getSplitRatio,
    setSplitRatio: preferenceManager.setSplitRatio,
    getThreadRailWidth: preferenceManager.getThreadRailWidth,
    setThreadRailWidth: preferenceManager.setThreadRailWidth,
    getCmdCardCols: preferenceManager.getCmdCardCols,
    setCmdCardCols: preferenceManager.setCmdCardCols,
    getThreadsByMode: viewHelpers.getThreadsByMode,
    getCurrentThreadId: viewHelpers.getCurrentThreadId,
    getThreadTimeline: syncManager.getThreadTimeline,
    getThreadDiff: syncManager.getThreadDiff,
    getThreadStatus: syncManager.getThreadStatus,
    shouldReloadThreadHistory: syncManager.shouldReloadThreadHistory,
    getThreadInterruptible: syncManager.getThreadInterruptible,
    getThreadStatusHeader: syncManager.getThreadStatusHeader,
    getThreadStatusDetails: syncManager.getThreadStatusDetails,
    getThreadTokenUsage: syncManager.getThreadTokenUsage,
    getThreadCompacting,
    getThreadCompactResult,
    setThreadCompactResult: setCompactResult,
    getThreadCompactSuccessCount,
    getThreadActivityStats: syncManager.getThreadActivityStats,
    getThreadAlerts: syncManager.getThreadAlerts,
    setScrollGuard: syncManager.setScrollGuard,
    getThreadPinnedAt: actionManager.getThreadPinnedAt,
    getThreadArchivedAt: actionManager.getThreadArchivedAt,
    setThreadPinned: actionManager.setThreadPinned,
    toggleThreadPin: actionManager.toggleThreadPin,
    setThreadArchived: actionManager.setThreadArchived,
    toggleThreadArchive: actionManager.toggleThreadArchive,
    batchDeleteStaleThreads: actionManager.batchDeleteStaleThreads,
    displayName: viewHelpers.displayName,
  };
}
