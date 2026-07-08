import { currentTimestampMillis, requireTimestampMillis } from '../../shared/pageShared.js';

const STALE_ARCHIVE_MS = 7 * 24 * 60 * 60 * 1000;

function normalizedThreadIdentity(value) {
  if (value === null || value === undefined) return '';
  return value.toString().trim();
}

function isInternalThreadIdentifier(value) {
  const text = normalizedThreadIdentity(value);
  if (!text) return false;
  return /^agent_[a-z0-9_-]+$/i.test(text) || /^thread[-_][a-z0-9_-]+$/i.test(text);
}

function threadSortTimestamp(value) {
  if (typeof value === 'number') return Number.isFinite(value) && value > 0 ? value : 0;
  const text = normalizedThreadIdentity(value);
  if (!text) return 0;
  const asNumber = Number(text);
  if (Number.isFinite(asNumber) && asNumber > 0) return asNumber;
  try {
    return requireTimestampMillis(text, 'thread sort timestamp');
  } catch {
    return 0;
  }
}

function threadMatchesActiveId(thread, activeThreadId) {
  const id = normalizedThreadIdentity(activeThreadId);
  if (!id || !thread) return false;
  return [
    thread.id,
    thread.threadId,
    thread.thread_id,
    thread.agentId,
    thread.agent_id,
    thread.providerThreadId,
    thread.provider_thread_id,
  ].some((value) => normalizedThreadIdentity(value) === id);
}

function threadIdentityList(thread) {
  return [
    thread?.id,
    thread?.threadId,
    thread?.thread_id,
    thread?.agentId,
    thread?.agent_id,
    thread?.providerThreadId,
    thread?.provider_thread_id,
    thread?.sessionId,
    thread?.session_id,
  ];
}

function activeThreadIdentifiers(activeThreadId, activeThread) {
  const ids = [activeThreadId, ...threadIdentityList(activeThread)];
  const result = new Set();
  for (const id of ids) {
    const normalized = normalizedThreadIdentity(id);
    if (normalized) result.add(normalized);
  }
  return result;
}

function hasThreadScopedMapValue(map, id) {
  if (!map || typeof map !== 'object' || Array.isArray(map)) return false;
  return Object.prototype.hasOwnProperty.call(map, id);
}

function threadScopedMapValue(map = {}, activeThreadId, activeThread, fallback = null) {
  const ids = activeThreadIdentifiers(activeThreadId, activeThread);
  for (const id of ids) {
    if (hasThreadScopedMapValue(map, id)) return map[id];
  }
  return fallback;
}

function threadScopedBooleanValue(map = {}, activeThreadId, activeThread, fallback = false) {
  const ids = activeThreadIdentifiers(activeThreadId, activeThread);
  let found = false;
  for (const id of ids) {
    if (!hasThreadScopedMapValue(map, id)) continue;
    found = true;
    if (map[id]) return true;
  }
  return found ? false : fallback;
}

function activeThreadForStore(store) {
  const activeThreadId = normalizedThreadIdentity(store?.activeThreadId);
  if (!activeThreadId) return null;
  const threads = Array.isArray(store?.threads) ? store.threads : [];
  return threads.find((thread) => threadMatchesActiveId(thread, activeThreadId)) ?? null;
}

function displayThreadName(thread, fallback = '新对话') {
  const ids = activeThreadIdentifiers(thread?.id, thread);
  for (const value of [thread?.name, thread?.title, thread?.displayName, thread?.display_name]) {
    const text = normalizedThreadIdentity(value);
    if (!text) continue;
    if (ids.has(text) || isInternalThreadIdentifier(text)) continue;
    return text;
  }
  return fallback;
}

function archivedStaleReason(thread) {
  if (!thread?.archived) return '';
  const archivedAt = Number(thread.archivedAt);
  if (Number.isFinite(archivedAt) && archivedAt > STALE_ARCHIVE_MS && currentTimestampMillis('thread archive stale now') - archivedAt > STALE_ARCHIVE_MS) {
    return 'expired';
  }
  if (normalizedThreadIdentity(thread.name) === normalizedThreadIdentity(thread.id)) {
    return 'empty';
  }
  return '';
}

export {
  activeThreadForStore,
  activeThreadIdentifiers,
  archivedStaleReason,
  displayThreadName,
  normalizedThreadIdentity,
  threadMatchesActiveId,
  threadScopedBooleanValue,
  threadScopedMapValue,
  threadSortTimestamp,
};
