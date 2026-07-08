import { optionalTextField, firstOptionalPresent, normalizeOptionalTextField, systemClockMillis, currentIsoTimestamp } from './contractStoreModel.js';
import {
  isVisibleTimelineItem,
  mergeTimelineItems,
  normalizeTimelineItem } from './timelineRuntime.js';
import {
  activeTurnPayload,
  isInterruptibleTurnSummary,
  normalizeActivityStats,
  normalizeTokenUsage,
  normalizeTurnSummary,
  threadActivityTimestamp as defaultThreadActivityTimestamp } from './threadActivityMetrics.js';
import { firstThreadCopyText } from './threadCopyPayload.js';

function optionalUiArray() {
  return [];
}

function optionalUiObject() {
  return {};
}

const MAX_BRIDGE_PATCH_WARNING_ENTRIES = 300;

function normalizeString(value) {
  return normalizeOptionalTextField(value);
}

function positiveApprovalRequestId(item) {
  const parsed = Number(firstOptionalPresent(item?.requestId, item?.request_id));
  return Math.max(0, Number.isFinite(parsed) ? parsed : 0);
}

function cleanObject(payload) {
  return Object.fromEntries(
    Object.entries(payload).filter(([, value]) => value !== undefined && value !== ''),
  );
}

function bridgePatchRuntime(payload) {
  return payload.agentRuntime || payload.agent_runtime || optionalUiObject();
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
  const alerts = payload.alerts || payload.Alerts;
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
    alerts: Array.isArray(alerts) ? alerts : [],
    runtimeResultEntries: deps.runtimeResultEntriesFromTimelineItems?.(timelineItems, threadId) || optionalUiArray(),
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
      timelinesByThread[patch.threadId] || optionalUiArray(),
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

function pendingApprovalCannotRender(item) {
  return (
    normalizeString(item?.kind).toLowerCase() === 'approval' &&
    normalizeString(item?.status).toLowerCase() === 'pending' &&
    !isVisibleTimelineItem(item)
  );
}

function bridgePatchWarningSignature(level, event, threadId, fields = {}) {
  return [
    level,
    event,
    threadId,
    normalizeString(fields.itemId),
    normalizeString(fields.status),
    normalizeString(fields.alertId),
    normalizeString(fields.message),
  ].join('|');
}

function mergeBridgePatchWarningEntries(existingEntries = [], incomingEntries = []) {
  if (incomingEntries.length === 0) return existingEntries;
  let merged = Array.isArray(existingEntries) ? existingEntries : [];
  for (const entry of incomingEntries) {
    const existingIndex = merged.findIndex((item) => item.signature === entry.signature);
    if (existingIndex < 0) {
      merged = [entry, ...merged].slice(0, MAX_BRIDGE_PATCH_WARNING_ENTRIES);
      continue;
    }
    const existing = merged[existingIndex];
    const updated = {
      ...existing,
      id: entry.id,
      timestamp: entry.timestamp,
      fields: entry.fields,
      occurrenceCount: (Number(existing.occurrenceCount) || 1) + 1,
    };
    merged = [
      updated,
      ...merged.slice(0, existingIndex),
      ...merged.slice(existingIndex + 1),
    ].slice(0, MAX_BRIDGE_PATCH_WARNING_ENTRIES);
  }
  return merged;
}

function bridgePatchAlertLevel(alert) {
  const level = normalizeString(alert.level || alert.Level).toLowerCase();
  if (level === 'warning') return 'warn';
  if (level === 'warn' || level === 'error' || level === 'info') return level;
  return 'warn';
}

function bridgePatchAlertEvent(alert, patch) {
  return normalizeString(
    alert.event || alert.Event ||
    alert.code || alert.Code ||
    alert.source || alert.Source ||
    patch.payload.source || patch.payload.Source,
  ) || 'thread.patch.alert';
}

function bridgePatchAlertEntries(patch, deps = {}) {
  if (!Array.isArray(patch.alerts) || patch.alerts.length === 0) return [];
  const nowISO = deps.nowISO || (() => currentIsoTimestamp());
  const nowMillis = deps.nowMillis || (() => systemClockMillis());
  return patch.alerts
    .filter((alert) => alert && typeof alert === 'object')
    .map((alert) => {
      const level = bridgePatchAlertLevel(alert);
      const event = bridgePatchAlertEvent(alert, patch);
      const alertId = normalizeString(alert.id || alert.ID);
      const message = normalizeString(alert.message || alert.Message);
      const fields = cleanObject({
        threadId: patch.threadId,
        alertId,
        message,
        source: normalizeString(patch.payload.source || patch.payload.Source),
      });
      return {
        id: alertId || `${event}-${nowMillis()}`,
        timestamp: normalizeString(alert.time || alert.Time || alert.timestamp || alert.Timestamp) || nowISO(),
        level,
        event,
        threadId: patch.threadId,
        fields,
        occurrenceCount: 1,
        signature: bridgePatchWarningSignature(level, event, patch.threadId, fields),
      };
    });
}

function bridgePatchApprovalWarningEntries(state, patch, deps = {}) {
  const nowISO = deps.nowISO || (() => currentIsoTimestamp());
  const nowMillis = deps.nowMillis || (() => systemClockMillis());
  const approvalEntries = Array.isArray(patch.timelineItems) ? patch.timelineItems
    .map(normalizeTimelineItem)
    .filter(pendingApprovalCannotRender)
    .map((item) => {
      const fields = {
        threadId: patch.threadId,
        itemId: normalizeString(item.id),
        requestId: positiveApprovalRequestId(item),
        status: normalizeString(item.status),
      };
      const event = 'timeline.approval.render_missing';
      const signature = bridgePatchWarningSignature('warn', event, patch.threadId, fields);
      return {
        id: `${event}-${nowMillis()}`,
        timestamp: nowISO(),
        level: 'warn',
        event,
        threadId: patch.threadId,
        fields,
        occurrenceCount: 1,
        signature,
      };
    }) : [];
  const entries = [
    ...approvalEntries,
    ...bridgePatchAlertEntries(patch, deps),
  ];
  return mergeBridgePatchWarningEntries(state.warningEntries, entries);
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
  const nowMillis = deps.nowMillis || (() => systemClockMillis());
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
    ...(existingThread || optionalUiObject()),
    ...patch.patchedThread,
    name: bridgePatchThreadName(existingThread, patch.patchedThread),
    provider: patch.patchProvider || existingThread?.provider || patch.patchedThread.provider,
    cwd: patch.patchedThread.cwd || existingThread?.cwd || optionalTextField(),
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

function bridgePatchMatchesThread(thread, patch, mergedThread, threadMatchesIdentifier) {
  return [
    patch.threadId,
    mergedThread.id,
    mergedThread.agentId,
    mergedThread.providerThreadId,
    mergedThread.sessionId,
  ].filter(Boolean).some((id) => threadMatchesIdentifier(thread, id));
}

function bridgePatchSidebarThread(thread, mergedThread) {
  const next = {
    ...thread,
    ...mergedThread,
  };
  if (!mergedThread.cwd && thread?.cwd) next.cwd = thread.cwd;
  return next;
}

function bridgePatchSidebarThreadsByProject(state, patch, deps = {}, nextThreads = []) {
  const current = state.sidebarThreadsByProject;
  if (!current || typeof current !== 'object' || Array.isArray(current)) return current || optionalUiObject();
  const threadMatchesIdentifier = deps.threadMatchesIdentifier || (() => false);
  const mergedThread = nextThreads.find((thread) => bridgePatchMatchesThread(thread, patch, patch.patchedThread, threadMatchesIdentifier));
  if (!mergedThread) return current;

  let changed = false;
  const next = {};
  for (const [projectKey, threads] of Object.entries(current)) {
    if (!Array.isArray(threads)) {
      next[projectKey] = threads;
      continue;
    }
    next[projectKey] = threads.map((thread) => {
      if (!bridgePatchMatchesThread(thread, patch, mergedThread, threadMatchesIdentifier)) return thread;
      changed = true;
      return bridgePatchSidebarThread(thread, mergedThread);
    });
  }
  return changed ? next : current;
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
  const nowMillis = deps.nowMillis || (() => systemClockMillis());
  const nowISO = deps.nowISO || (() => currentIsoTimestamp());
  return [{
    id: `${patch.method}-${nowMillis()}`,
    method: patch.method,
    threadId: patch.threadId,
    timestamp: nowISO(),
  }, ...state.activityEntries].slice(0, 120);
}

function bridgePatchState(state, patch, deps = {}) {
  const threads = bridgePatchThreads(state, patch, deps);
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
    threads,
    sidebarThreadsByProject: bridgePatchSidebarThreadsByProject(state, patch, deps, threads),
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
    warningEntries: bridgePatchApprovalWarningEntries(state, patch, deps),
  };
}

export { bridgePatchData, bridgePatchState };
