
import { optionalTextField } from '../../contractStoreModel.js';
import { createRuntimeResultHelpers } from '../../runtimeResults.js';
import { isVisibleTimelineItem, mergeTimelineItems, normalizeTimelineItem } from '../../timelineRuntime.js';
import { knownProviderName } from '../providerRuntimeConfig.js';
import { activeTurnPayload, isInterruptibleTurnSummary, normalizeActivityStats, normalizeTokenUsage, normalizeTurnSummary } from '../threadActivityMetrics.js';
import {
  normalizePath,
  normalizeString,
  normalizeTimestamp,
  objectRecord,
  optionalUiArray,
  optionalUiObject,
  sidebarThreadsByProjectWith,
} from './clientStoreUtils.js';
import {
  archiveMapFromPayload,
  archiveMapFromThreads,
  hasArchiveMapPayload,
  hasOwn,
  hasPinMapPayload,
  normalizeThread,
  pinMapFromPayload,
} from './clientStoreThreadModel.js';
import {
  backendThreadIdFromThreads,
  canonicalizeActiveTurnByThread,
  canonicalizeThreadKey,
  explicitThreadReplacementIds,
  runtimeMapFromPayload,
  runtimeProviderForThread,
  runtimeThreadIdentifier,
  snapshotThreadCwd,
  threadMatchesCwdScope,
  threadMatchesIdentifier,
} from './clientStoreRuntimeThreadModel.js';
import { composerScopeCwd } from '../../composerAttachments.js';
import { normalizeBackendThreadId, normalizeThreadId } from '../threadIdentity.js';

const runtimeResultHelpers = createRuntimeResultHelpers({
  normalizeString,
  normalizeTimestamp,
  normalizeThreadId,
  runtimeThreadIdentifier,
});
const {
  mergeRuntimeResultEntries,
  runtimeResultEntriesFromTimelineItems,
  runtimeResultEntryFromRPCDone,
} = runtimeResultHelpers;

function snapshotArchiveMap(state, payload) {
  return hasArchiveMapPayload(payload) ? archiveMapFromPayload(payload) : archiveMapFromThreads(state.threads);
}

function snapshotPinMap(state, payload) {
  return hasPinMapPayload(payload) ? pinMapFromPayload(payload) : state.pinnedThreadAtById;
}

function normalizeSnapshotThreadList(payload, state, options, maps) {
  if (!Array.isArray(payload.threads)) return state.threads;
  const threads = payload.threads
    .filter((thread) => threadMatchesCwdScope(thread, maps.scopeCwd, maps.runtimeById))
    .map((thread) => normalizeThread(thread, {
      state,
      archivedAtById: maps.archivedAtById,
      pinnedAtById: maps.pinnedAtById,
      fallbackProvider: snapshotThreadFallbackProvider(thread, state, maps.runtimeById),
      fallbackCwd: snapshotThreadCwd(thread, maps.runtimeById),
      lastArchivedStatesByThread: state.lastArchivedStatesByThread,
      threadArchiveLoadingByThread: state.threadArchiveLoadingByThread,
    }))
    .map((thread) => (
      options.preserveLiveBusyStatus === true
        ? preserveLiveBusyStatusForSnapshotThread(state, thread)
        : thread
    ))
    .filter((thread) => thread.id);
  return threads;
}

function snapshotThreadFallbackProvider(thread, state, runtimeById) {
  const runtimeProvider = knownProviderName(runtimeProviderForThread(thread, runtimeById));
  if (runtimeProvider) return runtimeProvider;
  const existing = state.threads.find((candidate) => threadMatchesIdentifier(candidate, thread?.id));
  const existingProvider = knownProviderName(existing?.provider);
  if (existingProvider) return existingProvider;
  if (threadMatchesIdentifier(thread, state.activeThreadId)) return knownProviderName(state.provider);
  return '';
}

function shouldPreserveSnapshotThread(state, thread, nextThreads) {
  const hasTimeline = (state.timelinesByThread[thread.id] || optionalUiArray()).length > 0;
  const alreadyIncluded = nextThreads.some((nextThread) => threadMatchesIdentifier(nextThread, thread.id));
  return !alreadyIncluded && (thread.id === state.activeThreadId || hasTimeline);
}

const LIVE_BUSY_THREAD_STATUS_KEYS = new Set([
  'starting',
  'preparing',
  'thinking',
  'running',
  'editing',
  'waiting',
  'syncing',
  'responding',
  'force_completing',
  'interrupting',
]);

function normalizeLiveThreadStatusKey(value) {
  const raw = normalizeString(value);
  if (raw === '工作中') return 'running';
  if (raw === '发送中') return 'preparing';
  return raw.toLowerCase().replace(/-/g, '_');
}

function isLiveBusyThreadStatus(value) {
  return LIVE_BUSY_THREAD_STATUS_KEYS.has(normalizeLiveThreadStatusKey(value));
}

function snapshotThreadStatusIds(snapshotThread, existingThread) {
  return [
    ...explicitThreadReplacementIds(snapshotThread),
    ...explicitThreadReplacementIds(existingThread),
  ].filter((id, index, ids) => id && ids.indexOf(id) === index);
}

function existingThreadForSnapshotStatus(state, snapshotThread) {
  const ids = snapshotThreadStatusIds(snapshotThread);
  return state.threads.find((thread) => ids.some((id) => threadMatchesIdentifier(thread, id)));
}

function liveStatusEntryForSnapshotThread(state, ids) {
  for (const id of ids) {
    const entry = state.statuses?.[id];
    if (isLiveBusyThreadStatus(entry?.status)) return entry.status;
  }
  return '';
}

function liveActiveTurnForSnapshotThread(state, ids) {
  for (const [threadId, turn] of Object.entries(state.activeTurnByThread || optionalUiArray())) {
    const normalized = normalizeTurnSummary(turn);
    if (!isInterruptibleTurnSummary(normalized)) continue;
    const turnThreadId = normalizeThreadId(normalized.threadId || threadId);
    if (ids.includes(normalizeThreadId(threadId)) || ids.includes(turnThreadId)) return normalized;
  }
  return null;
}

function liveBusyStatusForSnapshotThread(state, snapshotThread) {
  const existingThread = existingThreadForSnapshotStatus(state, snapshotThread);
  const ids = snapshotThreadStatusIds(snapshotThread, existingThread);
  const statusEntry = liveStatusEntryForSnapshotThread(state, ids);
  if (statusEntry) return statusEntry;
  const activeTurn = liveActiveTurnForSnapshotThread(state, ids);
  if (activeTurn && isLiveBusyThreadStatus(activeTurn.status)) return activeTurn.status;
  if (isLiveBusyThreadStatus(existingThread?.status)) return existingThread.status;
  return '';
}

function preserveLiveBusyStatusForSnapshotThread(state, snapshotThread) {
  /*
   * sidebar 快照是列表投影，可能落后于实时 ui/thread/patch。
   * 本地仍在运行时保留 live 状态，避免左侧项目树运行中图标被 stale idle 快照刷掉。
   */
  if (isLiveBusyThreadStatus(snapshotThread?.status)) return snapshotThread;
  const liveBusyStatus = liveBusyStatusForSnapshotThread(state, snapshotThread);
  return liveBusyStatus ? { ...snapshotThread, status: liveBusyStatus } : snapshotThread;
}

function snapshotThreadList(payload, state, options, maps) {
  const nextThreads = [...normalizeSnapshotThreadList(payload, state, options, maps)];
  if (!options.preserveActiveThreadId) return nextThreads;
  for (const thread of state.threads) {
    if (shouldPreserveSnapshotThread(state, thread, nextThreads)) nextThreads.push(thread);
  }
  return nextThreads;
}

function snapshotActiveThreadId(state, payload, nextThreads, options) {
  const preferredActiveThreadId = normalizeThreadId(options.preferredActiveThreadId);
  const autoSelectThread = options.autoSelectThread !== false;
  const activeLookupOptions = options.includeArchivedActiveThread ? { includeArchived: true } : {};

  if (options.preserveActiveThreadId) {
    return (
      backendThreadIdFromThreads(state.activeThreadId, nextThreads, { includeArchived: true }) ||
      (!nextThreads.some((thread) => threadMatchesIdentifier(thread, state.activeThreadId))
        ? normalizeBackendThreadId(state.activeThreadId)
        : '')
    );
  }

  const explicitActiveThreadId = backendThreadIdFromThreads(preferredActiveThreadId, nextThreads, activeLookupOptions);
  if (!autoSelectThread) return explicitActiveThreadId;
  const snapshotActive = normalizeThreadId(payload.activeThreadId || payload.active_thread_id);
  const selectableThreadId = nextThreads.find((thread) => !thread.archived)?.id || optionalTextField();
  return (
    explicitActiveThreadId ||
    backendThreadIdFromThreads(snapshotActive, nextThreads, activeLookupOptions) ||
    backendThreadIdFromThreads(state.activeThreadId, nextThreads, activeLookupOptions) ||
    selectableThreadId
  );
}

function canonicalizeThreadValues(source = {}, nextThreads = [], normalizer = (value) => value) {
  const output = {};
  for (const [threadId, value] of Object.entries(source || optionalUiArray())) {
    output[canonicalizeThreadKey(threadId, nextThreads)] = normalizer(value);
  }
  return output;
}

function snapshotTimelineBase(state, nextThreads) {
  return {
    timelinesByThread: canonicalizeThreadValues(state.timelinesByThread, nextThreads),
    threadTimelineReadyByThread: canonicalizeThreadValues(
      state.threadTimelineReadyByThread || optionalUiObject(),
      nextThreads,
      Boolean,
    ),
    threadMessagePaginationByThread: canonicalizeThreadValues(
      state.threadMessagePaginationByThread || optionalUiObject(),
      nextThreads,
    ),
  };
}

function mergeSnapshotTimelineItems(existingTimeline, ready, items = []) {
  const visibleExistingTimeline = existingTimeline.filter(isVisibleTimelineItem);
  const normalizedItems = items.map(normalizeTimelineItem);
  const visibleItems = normalizedItems.filter(isVisibleTimelineItem);
  if (visibleItems.length === 0 && ready) return visibleExistingTimeline;
  return mergeTimelineItems(visibleExistingTimeline, visibleItems, { preserveExistingVisible: true });
}

function snapshotTimelines(state, payload, nextThreads) {
  /*
   * 快照只补充后端看到的消息，不清空前端正在显示的 timeline。
   * thread id 变成真实 id 时，也要保住已加载历史和乐观消息。
   */
  const next = snapshotTimelineBase(state, nextThreads);
  const runtimeResultEntries = [];
  for (const [threadId, items] of Object.entries(objectRecord(payload.timelinesByThread || payload.timelines_by_thread))) {
    if (!Array.isArray(items)) continue;
    const canonicalId = canonicalizeThreadKey(threadId, nextThreads);
    runtimeResultEntries.push(...runtimeResultEntriesFromTimelineItems(items, canonicalId));
    next.timelinesByThread[canonicalId] = mergeSnapshotTimelineItems(
      next.timelinesByThread[canonicalId] || optionalUiArray(),
      next.threadTimelineReadyByThread[canonicalId],
      items,
    );
    next.threadTimelineReadyByThread[canonicalId] = true;
  }
  return { ...next, runtimeResultEntries };
}

function snapshotNormalizedThreadMap(stateMap, payloadMap, nextThreads, normalizer) {
  const output = canonicalizeThreadValues(stateMap, nextThreads);
  for (const [threadId, value] of Object.entries(objectRecord(payloadMap))) {
    const normalized = normalizer(value);
    if (normalized) output[canonicalizeThreadKey(threadId, nextThreads)] = normalized;
  }
  return output;
}

function snapshotPayloadThreadMap(payload, camelKey, snakeKey) {
  if (hasOwn(payload, camelKey)) return payload[camelKey];
  if (hasOwn(payload, snakeKey)) return payload[snakeKey];
  return undefined;
}

function snapshotThreadMetrics(state, payload, nextThreads, activeThreadId) {
  const tokenUsagePayloadMap = snapshotPayloadThreadMap(payload, 'tokenUsageByThread', 'token_usage_by_thread');
  const activityStatsPayloadMap = snapshotPayloadThreadMap(payload, 'activityStatsByThread', 'activity_stats_by_thread');
  const tokenUsageByThread = snapshotNormalizedThreadMap(
    state.tokenUsageByThread,
    tokenUsagePayloadMap,
    nextThreads,
    normalizeTokenUsage,
  );
  const activityStatsByThread = snapshotNormalizedThreadMap(
    state.activityStatsByThread,
    activityStatsPayloadMap,
    nextThreads,
    normalizeActivityStats,
  );
  const activeTokenUsage = tokenUsagePayloadMap === undefined ? normalizeTokenUsage(payload.tokenUsage || payload.token_usage) : null;
  const activeActivityStats = activityStatsPayloadMap === undefined ? normalizeActivityStats(payload.activityStats || payload.activity_stats) : null;
  if (activeTokenUsage && activeThreadId) tokenUsageByThread[activeThreadId] = activeTokenUsage;
  if (activeActivityStats && activeThreadId) activityStatsByThread[activeThreadId] = activeActivityStats;
  return { tokenUsageByThread, activityStatsByThread };
}

function snapshotDiffText(state, payload, nextThreads, activeThreadId) {
  const diffTextByThread = canonicalizeThreadValues(state.diffTextByThread, nextThreads);
  const threadDiffReadyByThread = canonicalizeThreadValues(state.threadDiffReadyByThread || optionalUiObject(), nextThreads, Boolean);
  for (const [threadId, text] of Object.entries(objectRecord(payload.diffTextByThread || payload.diff_text_by_thread))) {
    const canonicalId = canonicalizeThreadKey(threadId, nextThreads);
    diffTextByThread[canonicalId] = text;
    threadDiffReadyByThread[canonicalId] = true;
  }
  if (activeThreadId && typeof payload.diffText === 'string') {
    diffTextByThread[activeThreadId] = payload.diffText;
    threadDiffReadyByThread[activeThreadId] = true;
  }
  return { diffTextByThread, threadDiffReadyByThread };
}

function snapshotActiveTurnByThread(state, payload, nextThreads) {
  const activeTurn = activeTurnPayload(payload);
  if (activeTurn === undefined) return canonicalizeActiveTurnByThread(state.activeTurnByThread, nextThreads);
  const normalizedActiveTurn = normalizeTurnSummary(activeTurn);
  if (!isInterruptibleTurnSummary(normalizedActiveTurn) || !normalizedActiveTurn.threadId) return {};
  const canonicalThreadId = canonicalizeThreadKey(normalizedActiveTurn.threadId, nextThreads);
  return { [canonicalThreadId]: { ...normalizedActiveTurn, threadId: canonicalThreadId } };
}

function snapshotStatusMapValue(payload, mapKey, threadId, canonicalThreadId) {
  const values = payload[mapKey];
  if (values === undefined) return undefined;
  if (hasOwn(values, threadId)) return values[threadId];
  if (canonicalThreadId !== threadId && hasOwn(values, canonicalThreadId)) return values[canonicalThreadId];
  return undefined;
}

function snapshotStatusRuntime(payload, runtimeById, threadId, canonicalThreadId, nextThreads) {
  const candidates = [threadId, canonicalThreadId];
  const thread = nextThreads.find((entry) => (
    threadMatchesIdentifier(entry, threadId) || threadMatchesIdentifier(entry, canonicalThreadId)
  ));
  const threadAgentId = normalizeThreadId(thread?.agentId);
  if (threadAgentId) candidates.push(threadAgentId);
  for (const agent of Array.isArray(payload.agents) ? payload.agents : optionalUiArray()) {
    const agentId = normalizeThreadId(agent?.id);
    const agentThreadId = normalizeThreadId(agent?.thread_id);
    if (agentThreadId === threadId || agentThreadId === canonicalThreadId || (threadAgentId && agentId === threadAgentId)) {
      candidates.push(agentThreadId, agentId);
    }
  }
  for (const candidate of candidates.filter((id, index, ids) => id && ids.indexOf(id) === index)) {
    if (hasOwn(runtimeById, candidate)) return runtimeById[candidate];
  }
  return undefined;
}

function snapshotStatusEntry(value) {
  if (typeof value === 'string') return { status: value };
  if (value && typeof value === 'object' && !Array.isArray(value)) return value;
  throw new TypeError('stored thread status entry must be a string or object');
}

function snapshotStatuses(state, payload, nextThreads, runtimeById, options) {
  const output = canonicalizeThreadValues(state.statuses, nextThreads, snapshotStatusEntry);
  if (!hasOwn(payload, 'statuses')) return output;
  if (!payload.statuses || typeof payload.statuses !== 'object' || Array.isArray(payload.statuses)) {
    throw new TypeError('UI state statuses must be an object');
  }
  for (const [threadId, status] of Object.entries(payload.statuses)) {
    if (!normalizeThreadId(threadId)) throw new TypeError('UI state statuses thread id must be non-empty');
    if (typeof status !== 'string') throw new TypeError(`UI state statuses.${threadId} must be a string`);
    const canonicalThreadId = canonicalizeThreadKey(threadId, nextThreads);
    const entry = { status };
    const statusHeader = snapshotStatusMapValue(payload, 'statusHeadersByThread', threadId, canonicalThreadId);
    const statusDetails = snapshotStatusMapValue(payload, 'statusDetailsByThread', threadId, canonicalThreadId);
    const interruptible = snapshotStatusMapValue(payload, 'interruptibleByThread', threadId, canonicalThreadId);
    const activityStats = snapshotStatusMapValue(payload, 'activityStatsByThread', threadId, canonicalThreadId);
    const agentRuntime = snapshotStatusRuntime(payload, runtimeById, threadId, canonicalThreadId, nextThreads);
    if (statusHeader !== undefined && statusHeader !== '') entry.statusHeader = statusHeader;
    if (statusDetails !== undefined && statusDetails !== '') entry.statusDetails = statusDetails;
    if (interruptible !== undefined) entry.interruptible = interruptible;
    if (activityStats !== undefined) entry.activityStats = normalizeActivityStats(activityStats);
    if (agentRuntime !== undefined) entry.agentRuntime = agentRuntime;
    const existing = output[canonicalThreadId];
    if (
      options.preserveLiveBusyStatus === true &&
      isLiveBusyThreadStatus(existing?.status) &&
      !isLiveBusyThreadStatus(entry.status)
    ) continue;
    if (existing?.status === entry.status) {
      output[canonicalThreadId] = { ...existing, ...entry };
      continue;
    }
    output[canonicalThreadId] = entry;
  }
  return output;
}

function buildSnapshotState(state, payload = {}, options = {}) {
  /*
   * 线程快照用来刷新列表、状态、指标和 diff。
   * 空 timeline 不代表后端要求清空消息，要继续走合并逻辑。
   */
  const maps = {
    archivedAtById: snapshotArchiveMap(state, payload),
    pinnedAtById: snapshotPinMap(state, payload),
    runtimeById: runtimeMapFromPayload(payload),
    scopeCwd: normalizePath(options.scopeCwd) || composerScopeCwd(state),
  };
  const nextThreads = snapshotThreadList(payload, state, options, maps);
  const activeThreadId = snapshotActiveThreadId(state, payload, nextThreads, options);
  const timelineState = snapshotTimelines(state, payload, nextThreads);
  const metrics = snapshotThreadMetrics(state, payload, nextThreads, activeThreadId);
  const diffState = snapshotDiffText(state, payload, nextThreads, activeThreadId);
  const sidebarThreadsByProject = options.cacheSidebarThreads === false
    ? state.sidebarThreadsByProject
    : sidebarThreadsByProjectWith(state, maps.scopeCwd, nextThreads);
  return {
    activeThreadId,
    threads: nextThreads,
    sidebarThreadsByProject,
    pinnedThreadAtById: maps.pinnedAtById,
    timelinesByThread: timelineState.timelinesByThread,
    threadTimelineReadyByThread: timelineState.threadTimelineReadyByThread,
    threadMessagePaginationByThread: timelineState.threadMessagePaginationByThread,
    runtimeResultEntries: mergeRuntimeResultEntries(state.runtimeResultEntries, timelineState.runtimeResultEntries),
    activeTurnByThread: snapshotActiveTurnByThread(state, payload, nextThreads),
    statuses: snapshotStatuses(state, payload, nextThreads, maps.runtimeById, options),
    ...metrics,
    ...diffState,
  };
}


export {
  buildSnapshotState,
  existingThreadForSnapshotStatus,
  isLiveBusyThreadStatus,
  liveActiveTurnForSnapshotThread,
  liveBusyStatusForSnapshotThread,
  liveStatusEntryForSnapshotThread,
  mergeRuntimeResultEntries,
  mergeSnapshotTimelineItems,
  normalizeLiveThreadStatusKey,
  normalizeSnapshotThreadList,
  preserveLiveBusyStatusForSnapshotThread,
  runtimeResultEntriesFromTimelineItems,
  runtimeResultEntryFromRPCDone,
  shouldPreserveSnapshotThread,
  snapshotActiveThreadId,
  snapshotActiveTurnByThread,
  snapshotArchiveMap,
  snapshotDiffText,
  snapshotNormalizedThreadMap,
  snapshotPayloadThreadMap,
  snapshotPinMap,
  snapshotThreadFallbackProvider,
  snapshotThreadList,
  snapshotThreadMetrics,
  snapshotTimelineBase,
  snapshotTimelines,
};
