const STALE_ARCHIVE_MS = 7 * 24 * 60 * 60 * 1000;

function normalizedThreadIdentity(value) {
  return (value || '').toString().trim();
}

function isInternalThreadIdentifier(value) {
  const text = normalizedThreadIdentity(value);
  if (!text) return false;
  return /^agent_[a-z0-9_-]+$/i.test(text) || /^thread[-_][a-z0-9_-]+$/i.test(text);
}

function threadSortTimestamp(value) {
  if (typeof value === 'number') return Number.isFinite(value) && value > 0 ? value : 0;
  const text = (value || '').toString().trim();
  if (!text) return 0;
  const asNumber = Number(text);
  if (Number.isFinite(asNumber) && asNumber > 0) return asNumber;
  const parsed = Date.parse(text);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : 0;
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

function activeThreadIdentifiers(activeThreadId, activeThread) {
  const ids = [
    activeThreadId,
    activeThread?.id,
    activeThread?.threadId,
    activeThread?.thread_id,
    activeThread?.agentId,
    activeThread?.agent_id,
    activeThread?.providerThreadId,
    activeThread?.provider_thread_id,
    activeThread?.sessionId,
    activeThread?.session_id,
  ];
  const result = new Set();
  for (const id of ids) {
    const normalized = normalizedThreadIdentity(id);
    if (normalized) result.add(normalized);
  }
  return result;
}

function threadScopedMapValue(map = {}, activeThreadId, activeThread, fallback = null) {
  const ids = activeThreadIdentifiers(activeThreadId, activeThread);
  for (const id of ids) {
    if (Object.prototype.hasOwnProperty.call(map || {}, id)) return map[id];
  }
  return fallback;
}

function threadScopedBooleanValue(map = {}, activeThreadId, activeThread, fallback = false) {
  const ids = activeThreadIdentifiers(activeThreadId, activeThread);
  let found = false;
  for (const id of ids) {
    if (!Object.prototype.hasOwnProperty.call(map || {}, id)) continue;
    found = true;
    if (map[id]) return true;
  }
  return found ? false : fallback;
}

function activeThreadForStore(store) {
  const activeThreadId = normalizedThreadIdentity(store?.activeThreadId);
  if (!activeThreadId) return null;
  return (store?.threads || []).find((thread) => threadMatchesActiveId(thread, activeThreadId)) || null;
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
  const archivedAt = Number(thread.archivedAt || 0);
  if (Number.isFinite(archivedAt) && archivedAt > STALE_ARCHIVE_MS && Date.now() - archivedAt > STALE_ARCHIVE_MS) {
    return 'expired';
  }
  if ((thread.name || '').toString().trim() === (thread.id || '').toString().trim()) {
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
