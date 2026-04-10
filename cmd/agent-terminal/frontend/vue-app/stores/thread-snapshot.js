// @ts-nocheck
import { logDebug, logInfo, logWarn } from '../services/log.js';
import { normalizeStatus } from '../services/status.js';
import { normalizeThreadID } from './bridge-event-parser.js';
import { ensureThreadOrderIndex, sortThreadsByStableFirstSeen } from './thread-time-utils.js';
import { normalizeThread, normalizeThreadTimestampMap } from './thread-ui-normalize.js';
import {
  PREF_ACTIVE_THREAD_ID,
  PREF_ACTIVE_CMD_THREAD_ID,

  PREF_VIEW_CHAT,
  PREF_VIEW_CMD,
  PREF_PINNED_THREADS_CHAT,
  PREF_ARCHIVED_THREADS_CHAT,
  normalizeChatPrefs,
  normalizeCmdPrefs,
} from './thread-preference.model.js';
import { mergeDiffRuntimePatch, markLoadedDiffRevisions } from './thread-diff-sync.js';
import { _optimisticThreadIds } from './thread-optimistic.js';
import {
  freezeTimelineItemsAtomic,
  mergeObjectMapAtomic,
  mergeStringMapAtomic,
  normalizeAgentRuntimeMap,
  normalizeProviderThreadID,
} from './thread-snapshot-utils.js';


let _localActiveThreadIdDirty = false;
let _localActiveCmdThreadIdDirty = false;
const THREAD_PAYLOAD_CACHE_LIMIT = 6;
let _threadPayloadTouchSeq = 0;
const _threadPayloadTouchedAt = new Map();

function normalizePinnedThreadMap(value) {
  return normalizeThreadTimestampMap(value);
}

function normalizeArchivedThreadMap(value) {
  return normalizeThreadTimestampMap(value);
}

function hasThreadInSnapshot(threadList, threadId) {
  const current = normalizeThreadID(threadId);
  return current
    ? Array.isArray(threadList) && threadList.some((thread) => normalizeThreadID(thread?.id) === current)
    : false;
}

function shouldPreserveLocalSelection(localId, remoteId, threadList) {
  const current = normalizeThreadID(localId);
  return current ? hasThreadInSnapshot(threadList, current) && current !== normalizeThreadID(remoteId) : false;
}

export function markLocalActiveThreadDirty(dirty = true) {
  _localActiveThreadIdDirty = dirty !== false;
}

export function markLocalActiveCmdThreadDirty(dirty = true) {
  _localActiveCmdThreadIdDirty = dirty !== false;
}

export { freezeTimelineItemsAtomic, normalizeProviderThreadID } from './thread-snapshot-utils.js';

function touchThreadPayload(threadId) {
  const id = normalizeThreadID(threadId);
  if (!id) return;
  _threadPayloadTouchSeq += 1;
  _threadPayloadTouchedAt.set(id, _threadPayloadTouchSeq);
}

function touchThreadPayloadMap(source) {
  if (!source || typeof source !== 'object') return;
  Object.keys(source).forEach((key) => touchThreadPayload(key));
}

function buildPayloadKeepThreadIDs({ state, patch, data, requestedThreadId }) {
  const keep = new Set();
  const remember = (threadId) => {
    const id = normalizeThreadID(threadId);
    if (!id) return;
    keep.add(id);
    touchThreadPayload(id);
  };

  remember(requestedThreadId);
  remember(patch.activeThreadId);
  remember(patch.activeCmdThreadId);
  remember(state.activeThreadId);
  remember(state.activeCmdThreadId);

  touchThreadPayloadMap(data.timelinesByThread);
  touchThreadPayloadMap(data.diffTextByThread);
  touchThreadPayloadMap(data.diffRevisionByThread);
  touchThreadPayloadMap(data.statusHeadersByThread);
  touchThreadPayloadMap(data.statusDetailsByThread);
  touchThreadPayloadMap(data.tokenUsageByThread);
  touchThreadPayloadMap(data.agentMetaById);
  touchThreadPayloadMap(data.agentRuntimeById);
  touchThreadPayloadMap(data.activityStatsByThread);
  touchThreadPayloadMap(data.alertsByThread);

  const recentIDs = Array.from(_threadPayloadTouchedAt.entries())
    .sort((left, right) => right[1] - left[1])
    .slice(0, THREAD_PAYLOAD_CACHE_LIMIT)
    .map(([threadId]) => threadId);
  recentIDs.forEach((threadId) => keep.add(threadId));
  return keep;
}

function pruneThreadPayloadMap(current, keepThreadIDs, mapName) {
  if (!current || typeof current !== 'object') return { next: current, changed: false };
  let changed = false;
  const next = {};
  for (const [key, value] of Object.entries(current)) {
    const id = normalizeThreadID(key);
    if (!id || !keepThreadIDs.has(id)) {
      if (['statusHeadersByThread', 'agentRuntimeById'].includes(mapName)) {
        logWarn('thread', 'snapshot.pruning_suspicious', { map_name: mapName, thread_id: key });
      }
      changed = true;
      continue;
    }
    next[key] = value;
  }
  return changed ? { next, changed } : { next: current, changed };
}

function pruneLoadedDiffRevisions(loadedRevisionByThread, keepThreadIDs) {
  if (!loadedRevisionByThread?.keys || !loadedRevisionByThread?.delete) return;
  for (const key of Array.from(loadedRevisionByThread.keys())) {
    if (!keepThreadIDs.has(normalizeThreadID(key))) {
      loadedRevisionByThread.delete(key);
    }
  }
}

function pruneTouchedThreadPayloadCache(keepThreadIDs) {
  for (const key of Array.from(_threadPayloadTouchedAt.keys())) {
    if (!keepThreadIDs.has(normalizeThreadID(key))) {
      _threadPayloadTouchedAt.delete(key);
    }
  }
}

/**
 * Merge remote timeline items with local dialog items.
 * - When remote has real dialog (user/assistant), strip local optimistic items.
 * - When remote has only structural items, preserve local dialog items.
 */
function mergeTimelineWithLocalDialog(newItems, oldItems, threadId, requestedThreadId, logWarn) {
  if (!Array.isArray(oldItems) || oldItems.length === 0 || newItems.length === 0) return newItems;
  const newIds = new Set(newItems.map((i) => i?.id).filter(Boolean));
  const remoteHasDialog = newItems.some((i) => i?.kind === 'user' || i?.kind === 'assistant');

  const localDialogItems = oldItems.filter((i) => {
    if (i?.kind !== 'user' && i?.kind !== 'assistant') return false;
    if (newIds.has(i?.id)) return false;
    // Strip old optimistic items when incoming has actual new messages.
    if (remoteHasDialog && (i?.id || '').toString().includes('-optimistic-')) return false;
    return true;
  });

  if (localDialogItems.length === 0) {
    if (remoteHasDialog) {
      const oldOptimistic = oldItems.filter((i) => (i?.id || '').toString().includes('-optimistic-'));
      if (oldOptimistic.length > 0 && typeof logWarn === 'function') {
        logWarn('thread', 'snapshot.timeline.optimistic_stripped', {
          thread_id: threadId,
          requested_thread_id: requestedThreadId,
          stripped_count: oldOptimistic.length,
          stripped_ids: oldOptimistic.map((i) => (i?.id || '').toString()).slice(0, 4),
          new_timeline_len: newItems.length,
        });
      }
    }
    return newItems;
  }

  if (!remoteHasDialog && typeof logWarn === 'function') {
    logWarn('thread', 'snapshot.timeline.local_dialog_preserved', {
      thread_id: threadId,
      requested_thread_id: requestedThreadId,
      preserved_count: localDialogItems.length,
      preserved_ids: localDialogItems.map((i) => (i?.id || '').toString()).slice(0, 8),
      preserved_kinds: localDialogItems.map((i) => i?.kind).slice(0, 8),
      has_optimistic: localDialogItems.some((i) => (i?.id || '').toString().includes('-optimistic-')),
      new_timeline_len: newItems.length,
      old_timeline_len: oldItems.length,
    });
  }

  const mergedItems = [...newItems, ...localDialogItems];
  mergedItems.sort((a, b) => {
    const tsA = Date.parse(a?.ts || '');
    const tsB = Date.parse(b?.ts || '');
    const valA = Number.isFinite(tsA) ? tsA : 0;
    const valB = Number.isFinite(tsB) ? tsB : 0;
    if (valA !== valB && valA > 0 && valB > 0) return valA - valB;
    const numA = Number((a?.id || '').toString().split('-').pop());
    const numB = Number((b?.id || '').toString().split('-').pop());
    if (Number.isFinite(numA) && Number.isFinite(numB) && numA !== numB) return numA - numB;
    return 0;
  });

  return mergedItems;
}

export function applyRuntimeSnapshot(state, snapshot, options = {}) {
  const data = snapshot && typeof snapshot === 'object' ? snapshot : {};
  const patch = {};
  const allowActiveSelectionPatch = options.allowActiveSelectionPatch !== false;
  const requestedThreadId = normalizeThreadID(options.requestedThreadId);
  const loadedRevisionByThread = options.loadedRevisionByThread;

  const unorderedThreads = Array.isArray(data.threads) ? data.threads.map(normalizeThread) : [];
  const nextThreads = sortThreadsByStableFirstSeen(unorderedThreads);

  if (_optimisticThreadIds.size > 0) {
    const backendIds = new Set(nextThreads.map((t) => t.id));
    const now = Date.now();
    for (const [optimisticId, expiresAt] of _optimisticThreadIds) {
      if (backendIds.has(optimisticId)) {
        _optimisticThreadIds.delete(optimisticId);
        continue;
      }
      if (now > expiresAt) {
        logWarn('thread', 'optimistic.leak.expired', { thread_id: optimisticId });
        _optimisticThreadIds.delete(optimisticId);
        continue;
      }
      const local = state.threads.find((t) => t.id === optimisticId);
      if (local) nextThreads.push(local);
    }
  }

  if (state.threads.map((t) => t.id).join(',') !== nextThreads.map((t) => t.id).join(',')) patch.threads = nextThreads;

  let nextStatuses = state.statuses;
  let statusesChanged = false;
  if (data.statuses && typeof data.statuses === 'object') {
    for (const [key, value] of Object.entries(data.statuses)) {
      const normalized = normalizeStatus(value);
      if (nextStatuses[key] === normalized) continue;
      if (!statusesChanged) {
        nextStatuses = { ...nextStatuses };
        statusesChanged = true;
      }
      nextStatuses[key] = normalized;
    }
  }
  for (const thread of nextThreads) {
    if (nextStatuses[thread.id]) continue;
    if (!statusesChanged) {
      nextStatuses = { ...nextStatuses };
      statusesChanged = true;
    }
    nextStatuses[thread.id] = normalizeStatus(thread.state || 'idle');
  }
  if (statusesChanged) {
    const changedKeys = Object.keys(nextStatuses).filter((key) => nextStatuses[key] !== state.statuses[key]);
    if (changedKeys.length > 0) {
      logWarn('thread', 'snapshot.statuses.changed', {
        requested_thread_id: requestedThreadId,
        changed_thread_ids: changedKeys,
        changed_count: changedKeys.length,
        changes: changedKeys.slice(0, 6).map((key) => ({
          thread_id: key,
          old: state.statuses[key] || 'undefined',
          new: nextStatuses[key],
        })),
      });
    }
    patch.statuses = nextStatuses;
  }

  let nextInterruptibleByThread = state.interruptibleByThread;
  let interruptibleChanged = false;
  if (data.interruptibleByThread && typeof data.interruptibleByThread === 'object') {
    for (const [key, value] of Object.entries(data.interruptibleByThread)) {
      const normalized = Boolean(value);
      if (nextInterruptibleByThread[key] === normalized) continue;
      if (!interruptibleChanged) {
        nextInterruptibleByThread = { ...nextInterruptibleByThread };
        interruptibleChanged = true;
      }
      nextInterruptibleByThread[key] = normalized;
    }
  }
  if (interruptibleChanged) patch.interruptibleByThread = nextInterruptibleByThread;

  let nextTimelinesByThread = state.timelinesByThread;
  let timelinesChanged = false;
  if (data.timelinesByThread && typeof data.timelinesByThread === 'object') {
    for (const [key, value] of Object.entries(data.timelinesByThread)) {
      const newItems = Array.isArray(value) ? value : [];
      const oldItems = nextTimelinesByThread[key];
      if (Array.isArray(oldItems) && oldItems.length > 0 && newItems.length === 0) {
        logWarn('thread', 'snapshot.timeline.skip_empty_remote', {
          thread_id: key,
          requested_thread_id: requestedThreadId,
          active_thread_id: state.activeThreadId,
          active_cmd_thread_id: state.activeCmdThreadId,
          allow_active_selection_patch: allowActiveSelectionPatch,
          old_timeline_len: oldItems.length,
          new_timeline_len: newItems.length,
        });
        continue;
      }
      // Merge: preserve local dialog items (user/assistant from loadMessages
      // or optimistic insert) when remote timeline only has structural events.
      // [FIX] Only preserve for the requested thread — cross-thread snapshots
      // must not re-preserve stale optimistic items on unrelated threads.
      const isRequestedThread = requestedThreadId && key === requestedThreadId;
      const isDirectSync = !requestedThreadId;
      const mergedItems = (isRequestedThread || isDirectSync)
        ? mergeTimelineWithLocalDialog(newItems, oldItems, key, requestedThreadId, logWarn)
        : newItems;
      const frozenTimeline = freezeTimelineItemsAtomic(mergedItems, oldItems);
      if (!frozenTimeline.changed) continue;
      if (!timelinesChanged) {
        nextTimelinesByThread = { ...nextTimelinesByThread };
        timelinesChanged = true;
      }
      nextTimelinesByThread[key] = frozenTimeline.items;
    }
  }
  if (timelinesChanged) {
    for (const key of Object.keys(nextTimelinesByThread)) {
      const oldItems = state.timelinesByThread[key];
      const newItems = nextTimelinesByThread[key];
      if (oldItems === newItems) continue;
      const oldLen = Array.isArray(oldItems) ? oldItems.length : 0;
      const newLen = Array.isArray(newItems) ? newItems.length : 0;
      let reusedCount = 0;
      if (Array.isArray(oldItems) && Array.isArray(newItems)) {
        for (let i = 0; i < Math.min(oldLen, newLen); i++) {
          if (oldItems[i] === newItems[i]) reusedCount++;
        }
      }
      logInfo('thread', 'snapshot.timeline.replaced', {
        thread_id: key,
        old_len: oldLen,
        new_len: newLen,
        reused_items: reusedCount,
        all_reused: reusedCount === newLen && oldLen === newLen,
        stack: new Error('[diag]').stack,
      });
    }
    patch.timelinesByThread = nextTimelinesByThread;
  }

  Object.assign(patch, mergeDiffRuntimePatch(data, state));

  const mergedStatusHeaders = mergeStringMapAtomic(state.statusHeadersByThread, data.statusHeadersByThread);
  if (mergedStatusHeaders.changed) patch.statusHeadersByThread = mergedStatusHeaders.next;
  const mergedStatusDetails = mergeStringMapAtomic(state.statusDetailsByThread, data.statusDetailsByThread);
  if (mergedStatusDetails.changed) patch.statusDetailsByThread = mergedStatusDetails.next;
  const mergedTokenUsage = mergeObjectMapAtomic(state.tokenUsageByThread, data.tokenUsageByThread);
  if (mergedTokenUsage.changed) patch.tokenUsageByThread = mergedTokenUsage.next;
  const mergedAgentMeta = mergeObjectMapAtomic(state.agentMetaById, data.agentMetaById);
  if (mergedAgentMeta.changed) patch.agentMetaById = mergedAgentMeta.next;
  const mergedAgentRuntime = mergeObjectMapAtomic(state.agentRuntimeById, normalizeAgentRuntimeMap(data.agentRuntimeById));
  if (mergedAgentRuntime.changed) patch.agentRuntimeById = mergedAgentRuntime.next;
  const mergedActivityStats = mergeObjectMapAtomic(state.activityStatsByThread, data.activityStatsByThread);
  if (mergedActivityStats.changed) patch.activityStatsByThread = mergedActivityStats.next;
  const mergedAlerts = mergeObjectMapAtomic(state.alertsByThread, data.alertsByThread);
  if (mergedAlerts.changed) patch.alertsByThread = mergedAlerts.next;

  if (Object.prototype.hasOwnProperty.call(data, PREF_ACTIVE_THREAD_ID)) {
    const next = (data[PREF_ACTIVE_THREAD_ID] || '').toString();
    if (!allowActiveSelectionPatch) {
      if (state.activeThreadId !== next) logDebug('thread', 'snapshot.activeThreadId.skipped_scoped', { local: state.activeThreadId, remote: next, requested_thread_id: requestedThreadId });
    } else if (state.activeThreadId !== next) {
      if (_localActiveThreadIdDirty) {
        logDebug('thread', 'snapshot.activeThreadId.skipped_dirty', { local: state.activeThreadId, remote: next });
      } else if (shouldPreserveLocalSelection(state.activeThreadId, next, nextThreads)) {
        logDebug('thread', 'snapshot.activeThreadId.skipped_local_selection', { local: state.activeThreadId, remote: next, requested_thread_id: requestedThreadId });
      } else {
        patch.activeThreadId = next;
      }
    } else {
      _localActiveThreadIdDirty = false;
    }
  }

  if (Object.prototype.hasOwnProperty.call(data, PREF_ACTIVE_CMD_THREAD_ID)) {
    const next = (data[PREF_ACTIVE_CMD_THREAD_ID] || '').toString();
    if (!allowActiveSelectionPatch) {
      if (state.activeCmdThreadId !== next) logDebug('thread', 'snapshot.activeCmdThreadId.skipped_scoped', { local: state.activeCmdThreadId, remote: next, requested_thread_id: requestedThreadId });
    } else if (state.activeCmdThreadId !== next) {
      if (_localActiveCmdThreadIdDirty) {
        logDebug('thread', 'snapshot.activeCmdThreadId.skipped_dirty', { local: state.activeCmdThreadId, remote: next });
      } else if (shouldPreserveLocalSelection(state.activeCmdThreadId, next, nextThreads)) {
        logDebug('thread', 'snapshot.activeCmdThreadId.skipped_local_selection', { local: state.activeCmdThreadId, remote: next, requested_thread_id: requestedThreadId });
      } else {
        patch.activeCmdThreadId = next;
      }
    } else {
      _localActiveCmdThreadIdDirty = false;
    }
  }


  if (Object.prototype.hasOwnProperty.call(data, PREF_PINNED_THREADS_CHAT)) {
    const pinnedMap = normalizePinnedThreadMap(data[PREF_PINNED_THREADS_CHAT]);
    for (const id of Object.keys(pinnedMap)) ensureThreadOrderIndex(id);
    if (JSON.stringify(pinnedMap) !== JSON.stringify(state.pinnedThreadAtById)) {
      patch.pinnedThreadAtById = pinnedMap;
    }
  }
  if (Object.prototype.hasOwnProperty.call(data, PREF_ARCHIVED_THREADS_CHAT)) {
    const archivedMap = normalizeArchivedThreadMap(data[PREF_ARCHIVED_THREADS_CHAT]);
    for (const id of Object.keys(archivedMap)) ensureThreadOrderIndex(id);
    if (JSON.stringify(archivedMap) !== JSON.stringify(state.archivedThreadAtById)) {
      patch.archivedThreadAtById = archivedMap;
    }
  }
  const nextViewPrefsChat = normalizeChatPrefs(data[PREF_VIEW_CHAT]);
  if (JSON.stringify(nextViewPrefsChat) !== JSON.stringify(state.viewPrefsChat)) patch.viewPrefsChat = nextViewPrefsChat;
  const nextViewPrefsCmd = normalizeCmdPrefs(data[PREF_VIEW_CMD]);
  if (JSON.stringify(nextViewPrefsCmd) !== JSON.stringify(state.viewPrefsCmd)) patch.viewPrefsCmd = nextViewPrefsCmd;

  const keepThreadIDs = buildPayloadKeepThreadIDs({
    state,
    patch,
    data,
    requestedThreadId,
  });
  // Only prune heavy payload maps to prevent memory leaks from huge text or timelines.
  // We MUST NOT prune lightweight UI state maps like statusHeadersByThread or agentRuntimeById
  // because the sidebar needs them to render tags and '等待指示' statuses for all visible threads.
  const pruneCandidateMaps = [
    ['timelinesByThread', patch.timelinesByThread || state.timelinesByThread],
    ['diffTextByThread', patch.diffTextByThread || state.diffTextByThread],
    ['diffRevisionByThread', patch.diffRevisionByThread || state.diffRevisionByThread],
  ];
  for (const [key, value] of pruneCandidateMaps) {
    const pruned = pruneThreadPayloadMap(value, keepThreadIDs, key);
    if (pruned.changed) patch[key] = pruned.next;
  }

  if (Object.keys(patch).length > 0) {
    Object.assign(state, patch);
    if (requestedThreadId) {
      logInfo('thread', 'snapshot.applied', {
        requested_thread_id: requestedThreadId,
        patch_keys: Object.keys(patch),
        status: (state.statuses?.[requestedThreadId] || '').toString(),
        status_header: (state.statusHeadersByThread?.[requestedThreadId] || '').toString(),
        status_details: (state.statusDetailsByThread?.[requestedThreadId] || '').toString(),
        timeline_len: Array.isArray(state.timelinesByThread?.[requestedThreadId]) ? state.timelinesByThread[requestedThreadId].length : 0,
        diff_revision: Number(state.diffRevisionByThread?.[requestedThreadId] || 0),
        has_diff_text: Boolean((state.diffTextByThread?.[requestedThreadId] || '').toString()),
      });
    }
  }
  if (loadedRevisionByThread?.set) markLoadedDiffRevisions(data, state, loadedRevisionByThread);
  pruneLoadedDiffRevisions(loadedRevisionByThread, keepThreadIDs);
  pruneTouchedThreadPayloadCache(keepThreadIDs);
}
