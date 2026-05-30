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
import { _optimisticThreadIds, _optimisticPreferenceMapTaints } from './thread-optimistic.js';
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

function normalizeOverlayPriority(value) {
  const num = Number(value);
  return Number.isFinite(num) ? Math.max(0, Math.floor(num)) : 0;
}

function buildThreadOverlayRuntimePatch(state, threadList) {
  if (!Array.isArray(threadList)) return {};
  const overlayTextByThread = {};
  const overlayTypeByThread = {};
  const overlayPriorityByThread = {};
  for (const item of threadList) {
    const id = normalizeThreadID(item?.id);
    if (!id) continue;
    overlayTextByThread[id] = (item?.overlayText || '').toString();
    overlayTypeByThread[id] = (item?.overlayType || '').toString();
    overlayPriorityByThread[id] = normalizeOverlayPriority(item?.overlayPriority);
  }
  const patch = {};
  const currentOverlayText = state.overlayTextByThread && typeof state.overlayTextByThread === 'object'
    ? state.overlayTextByThread
    : {};
  const mergedOverlayText = mergeStringMapAtomic(currentOverlayText, overlayTextByThread);
  if (mergedOverlayText.changed) patch.overlayTextByThread = mergedOverlayText.next;
  const currentOverlayType = state.overlayTypeByThread && typeof state.overlayTypeByThread === 'object'
    ? state.overlayTypeByThread
    : {};
  const mergedOverlayType = mergeStringMapAtomic(currentOverlayType, overlayTypeByThread);
  if (mergedOverlayType.changed) patch.overlayTypeByThread = mergedOverlayType.next;
  const currentOverlayPriority = state.overlayPriorityByThread && typeof state.overlayPriorityByThread === 'object'
    ? state.overlayPriorityByThread
    : {};
  let nextOverlayPriority = currentOverlayPriority;
  let overlayPriorityChanged = false;
  for (const [key, value] of Object.entries(overlayPriorityByThread)) {
    if (nextOverlayPriority[key] === value) continue;
    if (!overlayPriorityChanged) {
      nextOverlayPriority = { ...currentOverlayPriority };
      overlayPriorityChanged = true;
    }
    nextOverlayPriority[key] = value;
  }
  if (overlayPriorityChanged) patch.overlayPriorityByThread = nextOverlayPriority;
  return patch;
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
 * Merge remote timeline items with local runtime items.
 * - Strip local optimistic dialogs when remote has a new user/assistant dialog.
 * - Preserve ALL missing local items (tools, reasoning, files) to prevent UI flicker
 *   caused by delayed backend DB flushes racing with live websocket stream patches.
 */
function mergeTimelineWithLocalItems(newItems, oldItems, threadId, requestedThreadId, logWarn) {
  if (!Array.isArray(oldItems) || oldItems.length === 0 || newItems.length === 0) return newItems;
  const newIds = new Set(newItems.map((i) => i?.id).filter(Boolean));
  // Dialog items (user/assistant) are now sourced exclusively from the
  // thread/messages history RPC path. The uistate snapshot never carries
  // dialog items, so mergeTimelineWithLocalItems only has to reconcile
  // non-dialog items (tools, plan, turn markers) plus strip optimistic
  // holders once any remote dialog is visible on the thread.
  const remoteHasDialog = newItems.some((i) => i?.kind === 'user' || i?.kind === 'assistant');

  const localItems = oldItems.filter((i) => {
    if (newIds.has(i?.id)) return false;
    // Strip old optimistic items when incoming has actual new messages.
    if (remoteHasDialog && (i?.id || '').toString().includes('-optimistic-')) return false;
    return true;
  });

  if (localItems.length === 0) {
    if (remoteHasDialog) {
      const oldOptimistic = oldItems.filter((i) => (i?.id || '').toString().includes('-optimistic-'));
      if (oldOptimistic.length > 0) {
        logDebug('thread', 'snapshot.timeline.optimistic_stripped', {
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

  logDebug('thread', 'snapshot.timeline.local_items_preserved', {
    thread_id: threadId,
    requested_thread_id: requestedThreadId,
    preserved_count: localItems.length,
    preserved_ids: localItems.map((i) => (i?.id || '').toString()).slice(0, 8),
    preserved_kinds: localItems.map((i) => i?.kind).slice(0, 8),
    has_optimistic: localItems.some((i) => (i?.id || '').toString().includes('-optimistic-')),
    new_timeline_len: newItems.length,
    old_timeline_len: oldItems.length,
  });

  const mergedItems = [...newItems, ...localItems];
  mergedItems.sort((a, b) => {
    const tsA = (a && a.ts) ? String(a.ts) : '';
    const tsB = (b && b.ts) ? String(b.ts) : '';

    if (tsA && tsB) {
      if (tsA < tsB) return -1;
      if (tsA > tsB) return 1;
    } else if (tsA) {
      return -1;
    } else if (tsB) {
      return 1;
    }

    const idA = (a && a.id) ? String(a.id) : '';
    const idB = (b && b.id) ? String(b.id) : '';
    const partsA = idA.split('-');
    const partsB = idB.split('-');

    if (partsA.length > 1 && partsB.length > 1) {
      const prefixA = partsA.slice(0, -1).join('-');
      const prefixB = partsB.slice(0, -1).join('-');
      if (prefixA === prefixB && prefixA.length > 0) {
        const numA = Number(partsA[partsA.length - 1]);
        const numB = Number(partsB[partsB.length - 1]);
        if (!Number.isNaN(numA) && !Number.isNaN(numB) && numA !== numB) return numA - numB;
      }
    }

    if (idA < idB) return -1;
    if (idA > idB) return 1;
    return 0;
  });

  return mergedItems;
}

function mergeOptimisticThreads(nextThreads, state) {
  if (_optimisticThreadIds.size === 0) return nextThreads;
  const backendIds = new Set(nextThreads.map((t) => t.id));
  const now = Date.now();
  let hasOptimisticMutations = false;
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
    if (local) {
      nextThreads.push(local);
      hasOptimisticMutations = true;
    }
  }
  return hasOptimisticMutations ? sortThreadsByStableFirstSeen(nextThreads) : nextThreads;
}

function patchThreadListIdentitySafe(state, nextThreads, patch) {
  const existingThreadById = new Map();
  if (Array.isArray(state.threads)) {
    for (const t of state.threads) {
      if (t && t.id) existingThreadById.set(t.id, t);
    }
  }
  const identitySafeThreads = nextThreads.map((t) => {
    const existing = existingThreadById.get(t.id);
    return existing
      && existing.name === t.name
      && existing.state === t.state
      && existing.createdAt === t.createdAt
      && existing.updatedAt === t.updatedAt
      ? existing
      : t;
  });
  const oldThreadsStr = state.threads.map((t) => t.id).join(',');
  const newThreadsStr = identitySafeThreads.map((t) => t.id).join(',');
  if (oldThreadsStr !== newThreadsStr) {
    logDebug('thread', 'snapshot.threads.changed', {
      old_len: state.threads.length, new_len: identitySafeThreads.length,
      old_ids: oldThreadsStr, new_ids: newThreadsStr,
    });
    patch.threads = identitySafeThreads;
  } else {
    let needsPatch = false;
    for (let i = 0; i < identitySafeThreads.length; i++) {
      if (identitySafeThreads[i] !== state.threads[i]) { needsPatch = true; break; }
    }
    if (needsPatch) patch.threads = identitySafeThreads;
  }
}

function patchStatuses(state, data, nextThreads, patch, requestedThreadId) {
  let nextStatuses = state.statuses;
  let changed = false;
  if (data.statuses && typeof data.statuses === 'object') {
    for (const [key, value] of Object.entries(data.statuses)) {
      const normalized = normalizeStatus(value);
      if (nextStatuses[key] === normalized) continue;
      if (!changed) { nextStatuses = { ...nextStatuses }; changed = true; }
      nextStatuses[key] = normalized;
    }
  }
  for (const thread of nextThreads) {
    if (nextStatuses[thread.id]) continue;
    if (!changed) { nextStatuses = { ...nextStatuses }; changed = true; }
    nextStatuses[thread.id] = normalizeStatus(thread.state || 'idle');
  }
  if (changed) {
    const changedKeys = Object.keys(nextStatuses).filter((key) => nextStatuses[key] !== state.statuses[key]);
    if (changedKeys.length > 0) {
      logDebug('thread', 'snapshot.statuses.changed', {
        requested_thread_id: requestedThreadId,
        changed_thread_ids: changedKeys, changed_count: changedKeys.length,
        changes: changedKeys.slice(0, 6).map((key) => ({
          thread_id: key, old: state.statuses[key] || 'undefined', new: nextStatuses[key],
        })),
      });
    }
    patch.statuses = nextStatuses;
  }
}

function patchInterruptible(state, data, patch) {
  let next = state.interruptibleByThread;
  let changed = false;
  if (data.interruptibleByThread && typeof data.interruptibleByThread === 'object') {
    for (const [key, value] of Object.entries(data.interruptibleByThread)) {
      const normalized = Boolean(value);
      if (next[key] === normalized) continue;
      if (!changed) { next = { ...next }; changed = true; }
      next[key] = normalized;
    }
  }
  if (changed) patch.interruptibleByThread = next;
}

function patchTimelines(state, data, patch, requestedThreadId, allowActiveSelectionPatch) {
  let nextTimelinesByThread = state.timelinesByThread;
  let changed = false;
  if (data.timelinesByThread && typeof data.timelinesByThread === 'object') {
    for (const [key, value] of Object.entries(data.timelinesByThread)) {
      const newItems = Array.isArray(value) ? value : [];
      const oldItems = nextTimelinesByThread[key];
      if (Array.isArray(oldItems) && oldItems.length > 0 && newItems.length === 0) {
        logWarn('thread', 'snapshot.timeline.skip_empty_remote', {
          thread_id: key, requested_thread_id: requestedThreadId,
          active_thread_id: state.activeThreadId, active_cmd_thread_id: state.activeCmdThreadId,
          allow_active_selection_patch: allowActiveSelectionPatch,
          old_timeline_len: oldItems.length, new_timeline_len: newItems.length,
        });
        continue;
      }
      if (newItems.some(i => i?.kind === 'user')) {
        const uItems = newItems.filter(i => i?.kind === 'user');
        logDebug('thread', 'snapshot.timeline.user_items', {
          thread_id: key, count: uItems.length,
          preview_ids: uItems.map(i => i?.id).join(', '),
          preview_texts: uItems.map(i => (i?.text || '').substring(0, 30)).join(' | '),
        });
      }

      const mergedItems = mergeTimelineWithLocalItems(newItems, oldItems, key, requestedThreadId, logWarn);
      const frozenTimeline = freezeTimelineItemsAtomic(mergedItems, oldItems);
      if (!frozenTimeline.changed) continue;
      if (!changed) { nextTimelinesByThread = { ...nextTimelinesByThread }; changed = true; }
      nextTimelinesByThread[key] = frozenTimeline.items;
    }
  }
  if (changed) {
    logTimelineReplacements(state.timelinesByThread, nextTimelinesByThread);
    patch.timelinesByThread = nextTimelinesByThread;
  }
}

function logTimelineReplacements(oldMap, newMap) {
  for (const key of Object.keys(newMap)) {
    const oldItems = oldMap[key];
    const newItems = newMap[key];
    if (oldItems === newItems) continue;
    const oldLen = Array.isArray(oldItems) ? oldItems.length : 0;
    const newLen = Array.isArray(newItems) ? newItems.length : 0;
    let reusedCount = 0;
    let toolReusedCount = 0;
    let toolTotalCount = 0;
    let diffToolIds = [];
    if (Array.isArray(oldItems) && Array.isArray(newItems)) {
      for (let i = 0; i < Math.min(oldLen, newLen); i++) {
        const isTool = newItems[i]?.kind === 'tool' || oldItems[i]?.kind === 'tool';
        if (isTool) toolTotalCount++;
        
        if (oldItems[i] === newItems[i]) {
          reusedCount++;
          if (isTool) toolReusedCount++;
        } else if (isTool) {
          diffToolIds.push(newItems[i]?.id || oldItems[i]?.id);
        }
      }
    }
    logInfo('thread', 'snapshot.timeline.replaced', {
      thread_id: key, old_len: oldLen, new_len: newLen,
      reused_items: reusedCount, all_reused: reusedCount === newLen && oldLen === newLen,
      stack: new Error('[diag]').stack,
    });

    if (toolTotalCount > 0 && toolReusedCount < toolTotalCount) {
      logDebug('thread', 'snapshot.timeline.tool_flicker', {
        thread_id: key, 
        toolTotalCount, 
        toolReusedCount, 
        diffToolIds,
      });
    }
  }
}

export function applyRuntimeSnapshot(state, snapshot, options = {}) {
  const data = snapshot && typeof snapshot === 'object' ? snapshot : {};
  const patch = {};
  const allowActiveSelectionPatch = options.allowActiveSelectionPatch !== false;
  const requestedThreadId = normalizeThreadID(options.requestedThreadId);
  const loadedRevisionByThread = options.loadedRevisionByThread;

  const unorderedThreads = Array.isArray(data.threads) ? data.threads.map(normalizeThread) : [];
  const nextThreads = mergeOptimisticThreads(sortThreadsByStableFirstSeen(unorderedThreads), state);

  patchThreadListIdentitySafe(state, nextThreads, patch);
  patchStatuses(state, data, nextThreads, patch, requestedThreadId);
  patchInterruptible(state, data, patch);
  patchTimelines(state, data, patch, requestedThreadId, allowActiveSelectionPatch);
  applyRuntimeSnapshotMapPatches(state, data, patch);
  applyRuntimeSnapshotSelectionPatches({
    state, data, patch, nextThreads, requestedThreadId, allowActiveSelectionPatch,
  });
  applyRuntimeSnapshotPreferencePatches(state, data, patch);
  finalizeRuntimeSnapshotPatch({
    state, data, patch, requestedThreadId, loadedRevisionByThread,
  });
}

function applyRuntimeSnapshotMapPatches(state, data, patch) {
  Object.assign(patch, mergeDiffRuntimePatch(data, state));
  Object.assign(patch, buildThreadOverlayRuntimePatch(state, data.threads));

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
  if (Object.prototype.hasOwnProperty.call(data, 'mainAgentId')) {
    const nextMainAgentId = (data.mainAgentId || '').toString().trim();
    if (state.mainAgentId !== nextMainAgentId) patch.mainAgentId = nextMainAgentId;
    if (state.mainAgentId !== nextMainAgentId && state.mainAgentState !== '') patch.mainAgentState = '';
  }
  if (state.partial !== false) patch.partial = false;
  const mergedActivityStats = mergeObjectMapAtomic(state.activityStatsByThread, data.activityStatsByThread);
  if (mergedActivityStats.changed) patch.activityStatsByThread = mergedActivityStats.next;
  const mergedAlerts = mergeObjectMapAtomic(state.alertsByThread, data.alertsByThread);
  if (mergedAlerts.changed) patch.alertsByThread = mergedAlerts.next;
}

function applyRuntimeSnapshotSelectionPatches({
  state,
  data,
  patch,
  nextThreads,
  requestedThreadId,
  allowActiveSelectionPatch,
}) {
  applyRuntimeSnapshotSelectionPatch({
    data,
    patch,
    currentValue: state.activeThreadId,
    dirtyFlag: () => _localActiveThreadIdDirty,
    clearDirty: () => { _localActiveThreadIdDirty = false; },
    patchKey: 'activeThreadId',
    preserveThreads: nextThreads,
    preferenceKey: PREF_ACTIVE_THREAD_ID,
    scopedEvent: 'snapshot.activeThreadId.skipped_scoped',
    dirtyEvent: 'snapshot.activeThreadId.skipped_dirty',
    localEvent: 'snapshot.activeThreadId.skipped_local_selection',
    requestedThreadId,
    allowActiveSelectionPatch,
  });
  applyRuntimeSnapshotSelectionPatch({
    data,
    patch,
    currentValue: state.activeCmdThreadId,
    dirtyFlag: () => _localActiveCmdThreadIdDirty,
    clearDirty: () => { _localActiveCmdThreadIdDirty = false; },
    patchKey: 'activeCmdThreadId',
    preserveThreads: nextThreads,
    preferenceKey: PREF_ACTIVE_CMD_THREAD_ID,
    scopedEvent: 'snapshot.activeCmdThreadId.skipped_scoped',
    dirtyEvent: 'snapshot.activeCmdThreadId.skipped_dirty',
    localEvent: 'snapshot.activeCmdThreadId.skipped_local_selection',
    requestedThreadId,
    allowActiveSelectionPatch,
  });
}

function applyRuntimeSnapshotSelectionPatch({
  data,
  patch,
  currentValue,
  dirtyFlag,
  clearDirty,
  patchKey,
  preserveThreads,
  preferenceKey,
  scopedEvent,
  dirtyEvent,
  localEvent,
  requestedThreadId,
  allowActiveSelectionPatch,
}) {
  if (!Object.prototype.hasOwnProperty.call(data, preferenceKey)) return;
  const next = (data[preferenceKey] || '').toString();
  if (!allowActiveSelectionPatch) {
    if (currentValue !== next) {
      logDebug('thread', scopedEvent, { local: currentValue, remote: next, requested_thread_id: requestedThreadId });
    }
    return;
  }
  if (currentValue === next) {
    clearDirty();
    return;
  }
  if (dirtyFlag()) {
    logDebug('thread', dirtyEvent, { local: currentValue, remote: next });
    return;
  }
  if (shouldPreserveLocalSelection(currentValue, next, preserveThreads)) {
    logDebug('thread', localEvent, { local: currentValue, remote: next, requested_thread_id: requestedThreadId });
    return;
  }
  patch[patchKey] = next;
}

function applyRuntimeSnapshotPreferencePatches(state, data, patch) {
  const nowTaint = Date.now();
  applyRuntimeSnapshotThreadCollectionPatch({
    state,
    data,
    patch,
    preferenceKey: PREF_PINNED_THREADS_CHAT,
    patchKey: 'pinnedThreadAtById',
    normalizer: normalizePinnedThreadMap,
  });
  applyRuntimeSnapshotThreadCollectionPatch({
    state,
    data,
    patch,
    preferenceKey: PREF_ARCHIVED_THREADS_CHAT,
    patchKey: 'archivedThreadAtById',
    normalizer: normalizeArchivedThreadMap,
    nowTaint,
  });
  const nextViewPrefsChat = normalizeChatPrefs(data[PREF_VIEW_CHAT]);
  if (JSON.stringify(nextViewPrefsChat) !== JSON.stringify(state.viewPrefsChat)) patch.viewPrefsChat = nextViewPrefsChat;
  const nextViewPrefsCmd = normalizeCmdPrefs(data[PREF_VIEW_CMD]);
  if (JSON.stringify(nextViewPrefsCmd) !== JSON.stringify(state.viewPrefsCmd)) patch.viewPrefsCmd = nextViewPrefsCmd;
}

function applyRuntimeSnapshotThreadCollectionPatch({
  state,
  data,
  patch,
  preferenceKey,
  patchKey,
  normalizer,
  nowTaint = Date.now(),
}) {
  if (!Object.prototype.hasOwnProperty.call(data, preferenceKey)) return;
  const nextMap = normalizer(data[preferenceKey]);
  for (const id of Object.keys(nextMap)) ensureThreadOrderIndex(id);
  const expire = _optimisticPreferenceMapTaints.get(patchKey) || 0;
  if (nowTaint <= expire) {
    logDebug('thread', `snapshot.${patchKey}.skipped_optimistic`, {});
    return;
  }
  if (JSON.stringify(nextMap) !== JSON.stringify(state[patchKey])) {
    patch[patchKey] = nextMap;
  }
}

function finalizeRuntimeSnapshotPatch({
  state,
  data,
  patch,
  requestedThreadId,
  loadedRevisionByThread,
}) {
  const keepThreadIDs = buildPayloadKeepThreadIDs({
    state,
    patch,
    data,
    requestedThreadId,
  });
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
