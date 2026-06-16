import {
  isVisibleTimelineItem,
  mergeTimelineItems,
  normalizeTimelineItem,
} from './timelineRuntime.js';
import {
  activeTurnPayload,
  isInterruptibleTurnSummary,
  normalizeActivityStats,
  normalizeTokenUsage,
  normalizeTurnSummary,
  threadActivityTimestamp as defaultThreadActivityTimestamp,
} from './threadActivityMetrics.js';
import { firstThreadCopyText } from './threadCopyPayload.js';

function cleanObject(payload) {
  return Object.fromEntries(
    Object.entries(payload).filter(([, value]) => value !== undefined && value !== ''),
  );
}

function bridgePatchRuntime(payload) {
  return payload.agentRuntime || payload.agent_runtime || {};
}

function bridgePatchRawThread(payload) {
  return payload.thread && typeof payload.thread === 'object' ? payload.thread : {};
}

function bridgePatchProvider(rawRuntime, rawThread) {
  return firstThreadCopyText(
    rawRuntime.provider,
    rawRuntime.modelProvider,
    rawRuntime.model_provider,
    rawThread.provider,
    rawThread.modelProvider,
    rawThread.model_provider,
  );
}

function bridgePatchStatusText(payload, rawThread) {
  return firstThreadCopyText(payload.statusHeader, payload.status, rawThread.state, rawThread.status);
}

function bridgePatchedThread({ payload, threadId, rawRuntime, rawThread, patchProvider, statusText, normalizeThread }) {
  if (typeof normalizeThread !== 'function') throw new Error('bridgePatchData requires normalizeThread');
  return normalizeThread({
    ...rawThread,
    threadId,
    agentId: firstThreadCopyText(rawRuntime.agentId, rawRuntime.agent_id, rawThread.agentId, rawThread.agent_id),
    providerThreadId: firstThreadCopyText(rawRuntime.providerThreadId, rawRuntime.provider_thread_id, rawThread.providerThreadId, rawThread.provider_thread_id),
    provider: patchProvider,
    lastMessage: firstThreadCopyText(rawRuntime.lastMessage, rawRuntime.last_message, payload.statusDetails, payload.status_details, rawThread.lastMessage),
    status: statusText || rawThread.status,
  });
}

function bridgePatchData(method, payload, threadId, deps = {}) {
  const timelineItems = payload.timelineItems || payload.timeline_items;
  const rawRuntime = bridgePatchRuntime(payload);
  const rawThread = bridgePatchRawThread(payload);
  const patchProvider = bridgePatchProvider(rawRuntime, rawThread);
  const statusText = bridgePatchStatusText(payload, rawThread);
  const patchedThread = bridgePatchedThread({ payload, threadId, rawRuntime, rawThread, patchProvider, statusText, normalizeThread: deps.normalizeThread });
  return {
    method,
    payload,
    threadId,
    timelineItems,
    runtimeResultEntries: deps.runtimeResultEntriesFromTimelineItems?.(timelineItems, threadId) || [],
    tokenUsage: normalizeTokenUsage(payload.tokenUsage || payload.token_usage),
    activityStats: normalizeActivityStats(payload.activityStats || payload.activity_stats),
    diffText: typeof payload.diffText === 'string' ? payload.diffText : payload.diff_text,
    rawRuntime,
    patchProvider,
    statusText,
    patchedThread,
  };
}

function bridgePatchTimeline(state, patch) {
  const timelinesByThread = { ...state.timelinesByThread };
  if (Array.isArray(patch.timelineItems)) {
    const visibleItems = patch.timelineItems.map(normalizeTimelineItem).filter(isVisibleTimelineItem);
    timelinesByThread[patch.threadId] = mergeTimelineItems(
      timelinesByThread[patch.threadId] || [],
      visibleItems,
      { preserveExistingVisible: true },
    );
  }
  return timelinesByThread;
}

function bridgePatchHasVisibleTimelineItems(patch) {
  if (!Array.isArray(patch.timelineItems)) return false;
  return patch.timelineItems
    .map(normalizeTimelineItem)
    .some(isVisibleTimelineItem);
}

function bridgePatchActiveTurn(state, patch) {
  const activeTurnByThread = { ...state.activeTurnByThread };
  const patchActiveTurn = activeTurnPayload(patch.payload);
  if (patchActiveTurn !== undefined) {
    delete activeTurnByThread[patch.threadId];
    const normalizedActiveTurn = normalizeTurnSummary(patchActiveTurn);
    if (isInterruptibleTurnSummary(normalizedActiveTurn)) activeTurnByThread[patch.threadId] = { ...normalizedActiveTurn, threadId: patch.threadId };
    return activeTurnByThread;
  }
  if (patch.payload.interruptible === false || patch.statusText === 'idle' || patch.statusText === 'interrupted' || patch.statusText === 'completed') {
    delete activeTurnByThread[patch.threadId];
  }
  return activeTurnByThread;
}

function bridgePatchThreadName(existingThread, patchedThread) {
  if (patchedThread.name !== '新对话') return patchedThread.name;
  return existingThread?.name || patchedThread.name;
}

function shouldPromoteBridgePatchThread(existingThread, patch) {
  return !existingThread || patch.promoteForActivity;
}

function bridgePatchThreads(state, patch, deps = {}) {
  const threadMatchesIdentifier = deps.threadMatchesIdentifier || (() => false);
  const nowMillis = deps.nowMillis || (() => Date.now());
  const existingThread = state.threads.find((thread) => threadMatchesIdentifier(thread, patch.threadId));
  if (!patch.patchedThread.id) return state.threads;
  let archived = Boolean(existingThread?.archived || patch.patchedThread.archived);
  const recentOverride =
    state.lastArchivedStatesByThread?.[patch.threadId] ||
    (existingThread?.id && state.lastArchivedStatesByThread?.[existingThread.id]) ||
    (existingThread?.agentId && state.lastArchivedStatesByThread?.[existingThread.agentId]);
  const isLoading = Boolean(
    state.threadArchiveLoadingByThread?.[patch.threadId] ||
    (existingThread?.id && state.threadArchiveLoadingByThread?.[existingThread.id]) ||
    (existingThread?.agentId && state.threadArchiveLoadingByThread?.[existingThread.agentId])
  );
  if (isLoading && recentOverride) {
    archived = recentOverride.archived;
  } else if (recentOverride && nowMillis() - recentOverride.timestamp < 8000) {
    archived = recentOverride.archived;
  }
  const mergedThread = {
    ...(existingThread || {}),
    ...patch.patchedThread,
    name: bridgePatchThreadName(existingThread, patch.patchedThread),
    provider: patch.patchProvider || existingThread?.provider || patch.patchedThread.provider,
    status: patch.statusText || patch.patchedThread.status || existingThread?.status || '等待指示',
    pinned: Boolean(existingThread?.pinned || patch.patchedThread.pinned),
    pinnedAt: existingThread?.pinnedAt || patch.patchedThread.pinnedAt || 0,
    archived,
  };
  if (shouldPromoteBridgePatchThread(existingThread, patch)) {
    return [
      mergedThread,
      ...state.threads.filter((thread) => !threadMatchesIdentifier(thread, patch.threadId)),
    ];
  }
  return state.threads.map((thread) => (threadMatchesIdentifier(thread, patch.threadId) ? mergedThread : thread));
}

function bridgePatchActivityThreadAt(state, patch, deps = {}) {
  if (!patch.promoteForActivity) return state.activityThreadAtById;
  const threadActivityTimestamp = deps.threadActivityTimestamp || defaultThreadActivityTimestamp;
  return {
    ...state.activityThreadAtById,
    [patch.threadId]: threadActivityTimestamp(),
  };
}

function bridgePatchStatuses(state, patch) {
  return {
    ...state.statuses,
    [patch.threadId]: cleanObject({
      status: patch.payload.status,
      statusHeader: patch.payload.statusHeader,
      statusDetails: patch.payload.statusDetails || patch.payload.status_details,
      interruptible: patch.payload.interruptible,
      activityStats: patch.activityStats,
      agentRuntime: patch.rawRuntime,
    }),
  };
}

function bridgePatchActivityEntries(state, patch, deps = {}) {
  const nowMillis = deps.nowMillis || (() => Date.now());
  const nowISO = deps.nowISO || (() => new Date().toISOString());
  return [{
    id: `${patch.method}-${nowMillis()}`,
    method: patch.method,
    threadId: patch.threadId,
    timestamp: nowISO(),
  }, ...state.activityEntries].slice(0, 120);
}

function bridgePatchState(state, patch, deps = {}) {
  const tokenUsageByThread = { ...state.tokenUsageByThread };
  if (patch.tokenUsage) tokenUsageByThread[patch.threadId] = patch.tokenUsage;
  const activityStatsByThread = { ...state.activityStatsByThread };
  if (patch.activityStats) activityStatsByThread[patch.threadId] = patch.activityStats;
  const diffTextByThread = { ...state.diffTextByThread };
  const threadDiffReadyByThread = { ...state.threadDiffReadyByThread };
  if (typeof patch.diffText === 'string') {
    diffTextByThread[patch.threadId] = patch.diffText;
    threadDiffReadyByThread[patch.threadId] = true;
  }
  return {
    threads: bridgePatchThreads(state, patch, deps),
    activityThreadAtById: bridgePatchActivityThreadAt(state, patch, deps),
    timelinesByThread: bridgePatchTimeline(state, patch),
    threadTimelineReadyByThread: bridgePatchHasVisibleTimelineItems(patch)
      ? { ...state.threadTimelineReadyByThread, [patch.threadId]: true }
      : state.threadTimelineReadyByThread,
    tokenUsageByThread,
    activityStatsByThread,
    diffTextByThread,
    threadDiffReadyByThread,
    runtimeResultEntries: deps.mergeRuntimeResultEntries?.(state.runtimeResultEntries, patch.runtimeResultEntries) || state.runtimeResultEntries,
    activeTurnByThread: bridgePatchActiveTurn(state, patch),
    statuses: bridgePatchStatuses(state, patch),
    activityEntries: bridgePatchActivityEntries(state, patch, deps),
  };
}

export { bridgePatchData, bridgePatchState };
