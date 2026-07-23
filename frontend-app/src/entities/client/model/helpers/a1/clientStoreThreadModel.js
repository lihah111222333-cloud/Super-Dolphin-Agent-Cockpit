
import { firstOptionalPresent } from '../../contractStoreModel.js';
import { firstThreadCopyText } from '../threadCopyPayload.js';
import { isArchivedStatus } from '../threadListMutations.js';
import { normalizeBackendThreadId, normalizeThreadId, normalizeThreadIdentity } from '../threadIdentity.js';
import { normalizeThreadTimestamp } from '../../../../../shared/time/threadTimestamp.js';
import {
  clockNowMillis,
  normalizePath,
  normalizeString,
  normalizeTimestamp,
  normalizeTimestampMap,
  objectPrototype,
  optionalUiArray,
  optionalUiObject,
} from './clientStoreUtils.js';

function hasOwn(value, key) {
  return Boolean(value && typeof value === 'object' && objectPrototype.hasOwnProperty.call(value, key));
}

function threadMatchesIdentifier(thread, identifier) {
  const target = normalizeThreadId(identifier);
  if (!target) return false;
  const identity = normalizeThreadIdentity(thread);
  return [
    identity.threadId,
    identity.agentId,
    identity.providerThreadId,
    identity.sessionId,
    thread?.id,
  ].map(normalizeThreadId).includes(target);
}

function archiveMapFromPayload(payload = {}) {
  const direct = payload['threadArchives.chat'] || payload.threadArchivesChat || payload.archivedThreadAtById;
  const nested = firstOptionalPresent(payload.threadArchives?.chat, payload.thread_archives?.chat);
  return normalizeTimestampMap(direct || nested || optionalUiObject());
}

function archiveMapFromThreads(threads = []) {
  const entries = [];
  for (const thread of threads || optionalUiArray()) {
    const id = normalizeThreadId(thread?.id);
    if (!id) continue;
    const archivedAt = normalizeTimestamp(thread?.archivedAt);
    const value = archivedAt > 0 ? archivedAt : (thread?.archived ? 1 : 0);
    if (value > 0) {
      entries.push([id, value]);
      const agentId = normalizeThreadId(thread?.agentId);
      if (agentId && agentId !== id) {
        entries.push([agentId, value]);
      }
    }
  }
  return Object.fromEntries(entries);
}

function hasArchiveMapPayload(payload = {}) {
  return Boolean(payload && typeof payload === 'object' && (
    Object.prototype.hasOwnProperty.call(payload, 'threadArchives.chat') ||
    Object.prototype.hasOwnProperty.call(payload, 'threadArchivesChat') ||
    Object.prototype.hasOwnProperty.call(payload, 'archivedThreadAtById') ||
    Object.prototype.hasOwnProperty.call(payload.threadArchives || optionalUiObject(), 'chat') ||
    Object.prototype.hasOwnProperty.call(payload.thread_archives || optionalUiObject(), 'chat')
  ));
}

function pinMapFromPayload(payload = {}) {
  const direct = payload['threadPins.chat'] || payload.threadPinsChat || payload.pinnedThreadAtById;
  const nested = firstOptionalPresent(payload.threadPins?.chat, payload.thread_pins?.chat);
  return normalizeTimestampMap(direct || nested || optionalUiObject());
}

function hasPinMapPayload(payload = {}) {
  return Boolean(payload && typeof payload === 'object' && (
    Object.prototype.hasOwnProperty.call(payload, 'threadPins.chat') ||
    Object.prototype.hasOwnProperty.call(payload, 'threadPinsChat') ||
    Object.prototype.hasOwnProperty.call(payload, 'pinnedThreadAtById') ||
    Object.prototype.hasOwnProperty.call(payload.threadPins || optionalUiObject(), 'chat') ||
    Object.prototype.hasOwnProperty.call(payload.thread_pins || optionalUiObject(), 'chat')
  ));
}

function sourceThreadObject(raw) {
  return raw?.thread && typeof raw.thread === 'object' ? raw.thread : {};
}

function normalizeThreadStatusText(raw) {
  return firstThreadCopyText(
    raw?.status,
    raw?.state,
    raw?.lifecycleStatus,
    raw?.lifecycle_status,
    raw?.threadStatus,
    raw?.thread_status,
  ) || '等待指示';
}

function normalizeThreadCwdValue(raw, sourceThread, options = {}) {
  return normalizePath(firstThreadCopyText(
    raw?.cwd,
    raw?.CWD,
    raw?.workdir,
    raw?.workDir,
    raw?.work_dir,
    sourceThread?.cwd,
    sourceThread?.CWD,
    sourceThread?.workdir,
    sourceThread?.workDir,
    sourceThread?.work_dir,
    options.fallbackCwd,
  ));
}

function normalizeThreadProviderValue(raw, options = {}) {
  return firstThreadCopyText(
    raw?.provider,
    raw?.modelProvider,
    raw?.model_provider,
    options.fallbackProvider,
  );
}

function normalizeThreadPinnedAt(raw, identity, options = {}) {
  const threadId = identity && typeof identity === 'object' ? identity.threadId : normalizeThreadId(identity);
  const agentId = identity && typeof identity === 'object' ? identity.agentId : '';
  let resolvedDbId = '';
  if (options.state?.threads) {
    const existing = options.state.threads.find((t) =>
      (threadId && threadMatchesIdentifier(t, threadId)) ||
      (agentId && threadMatchesIdentifier(t, agentId))
    );
    if (existing) {
      resolvedDbId = normalizeBackendThreadId(existing.id);
    }
  }
  return normalizeTimestamp(firstThreadCopyText(
    raw?.pinnedAt,
    raw?.pinned_at,
    raw?.pinnedAtMs,
    raw?.pinned_at_ms,
    typeof raw?.pinned === 'boolean' ? 0 : raw?.pinned,
    (threadId && options.pinnedAtById?.[threadId]) ||
    (agentId && options.pinnedAtById?.[agentId]) ||
    (resolvedDbId && options.pinnedAtById?.[resolvedDbId]),
  ));
}

function normalizeThreadArchivedAt(raw, identity, options = {}) {
  const threadId = identity && typeof identity === 'object' ? identity.threadId : normalizeThreadId(identity);
  const agentId = identity && typeof identity === 'object' ? identity.agentId : '';
  let resolvedDbId = '';
  if (options.state?.threads) {
    const existing = options.state.threads.find((t) =>
      (threadId && threadMatchesIdentifier(t, threadId)) ||
      (agentId && threadMatchesIdentifier(t, agentId))
    );
    if (existing) {
      resolvedDbId = normalizeBackendThreadId(existing.id);
    }
  }
  return normalizeTimestamp(firstThreadCopyText(
    raw?.archivedAt,
    raw?.archived_at,
    raw?.archivedAtMs,
    raw?.archived_at_ms,
    typeof raw?.archived === 'boolean' ? 0 : raw?.archived,
    (threadId && options.archivedAtById?.[threadId]) ||
    (agentId && options.archivedAtById?.[agentId]) ||
    (resolvedDbId && options.archivedAtById?.[resolvedDbId]),
  ));
}

function normalizeThreadLifecycleStatus(raw) {
  return firstThreadCopyText(raw?.lifecycleStatus, raw?.lifecycle_status, raw?.threadStatus, raw?.thread_status);
}

function isThreadArchived(raw, status, lifecycleStatus, archivedAt) {
  return Boolean(firstOptionalPresent(raw?.archived, raw?.isArchived, archivedAt > 0, isArchivedStatus(status), isArchivedStatus(lifecycleStatus)));
}

function normalizeThread(raw, options = {}) {
  const identity = normalizeThreadIdentity(raw);
  const sourceThread = sourceThreadObject(raw);
  const status = normalizeThreadStatusText(raw);
  const cwd = normalizeThreadCwdValue(raw, sourceThread, options);
  const provider = normalizeThreadProviderValue(raw, options);
  const pinnedAt = normalizeThreadPinnedAt(raw, identity, options);
  const archivedAt = normalizeThreadArchivedAt(raw, identity, options);
  const lifecycleStatus = normalizeThreadLifecycleStatus(raw);
  let archived = isThreadArchived(raw, status, lifecycleStatus, archivedAt);
  const recentOverride =
    (identity.threadId && options.lastArchivedStatesByThread?.[identity.threadId]) ||
    (identity.agentId && options.lastArchivedStatesByThread?.[identity.agentId]);
  const isLoading = Boolean(
    (identity.threadId && options.threadArchiveLoadingByThread?.[identity.threadId]) ||
    (identity.agentId && options.threadArchiveLoadingByThread?.[identity.agentId])
  );
  if (isLoading && recentOverride) {
    archived = recentOverride.archived;
  } else if (recentOverride && clockNowMillis() - recentOverride.timestamp < 8000) {
    archived = recentOverride.archived;
  }
  return {
    id: identity.threadId,
    agentId: identity.agentId,
    providerThreadId: identity.providerThreadId,
    sessionId: identity.sessionId,
    cwd,
    name: normalizeString(firstOptionalPresent(raw?.name, raw?.title, raw?.displayName, raw?.summary)) || '新对话',
    provider,
    status,
    agentKey: normalizeString(firstOptionalPresent(raw?.agentKey, raw?.agent_key, sourceThread?.agentKey, sourceThread?.agent_key)),
    dagKey: normalizeString(firstOptionalPresent(raw?.dagKey, raw?.dag_key, sourceThread?.dagKey, sourceThread?.dag_key)),
    workflowKey: normalizeString(firstOptionalPresent(raw?.workflowKey, raw?.workflow_key, sourceThread?.workflowKey, sourceThread?.workflow_key)),
    runKey: normalizeString(firstOptionalPresent(raw?.runKey, raw?.run_key, sourceThread?.runKey, sourceThread?.run_key)),
    taskId: normalizeString(firstOptionalPresent(raw?.taskId, raw?.task_id, sourceThread?.taskId, sourceThread?.task_id)),
    source: normalizeString(firstOptionalPresent(raw?.source, raw?.origin, sourceThread?.source, sourceThread?.origin)),
    lastMessage: normalizeString(firstOptionalPresent(raw?.lastMessage, raw?.last_message, raw?.preview)),
    updatedAt: normalizeThreadTimestamp(firstOptionalPresent(raw?.updatedAt, raw?.updated_at, raw?.createdAt, raw?.created_at)),
    pinned: Boolean(firstOptionalPresent(raw?.pinned, raw?.isPinned, pinnedAt > 0)),
    pinnedAt,
    archived,
    archivedAt,
  };
}

export {
  archiveMapFromPayload,
  archiveMapFromThreads,
  hasArchiveMapPayload,
  hasOwn,
  hasPinMapPayload,
  isThreadArchived,
  normalizeThread,
  normalizeThreadArchivedAt,
  normalizeThreadCwdValue,
  normalizeThreadLifecycleStatus,
  normalizeThreadPinnedAt,
  normalizeThreadProviderValue,
  normalizeThreadStatusText,
  pinMapFromPayload,
  sourceThreadObject,
};
