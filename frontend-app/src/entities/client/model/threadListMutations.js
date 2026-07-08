import { normalizeOptionalTextField } from './contractStoreModel.js';
function optionalUiArray() {
  return [];
}

function normalizeString(value) {
  return normalizeOptionalTextField(value);
}

function normalizeThreadId(value) {
  return normalizeString(value);
}

function threadMatchesIdentifier(thread, value) {
  const id = normalizeThreadId(value);
  return Boolean(id && (
    normalizeThreadId(thread?.id) === id ||
    normalizeThreadId(thread?.agentId) === id ||
    normalizeThreadId(thread?.providerThreadId) === id ||
    normalizeThreadId(thread?.sessionId) === id
  ));
}

export function isArchivedStatus(value) {
  const status = normalizeString(value).toLowerCase();
  return status === 'archived' || status === '归档' || status === '已归档';
}

export function threadArchiveStatus(thread, archived) {
  if (archived) return 'archived';
  if (isArchivedStatus(thread?.status)) return 'created';
  return thread?.status;
}

export function applyThreadRename(threads = [], id, nextName) {
  return threads.map((thread) => (thread.id === id ? { ...thread, name: nextName } : thread));
}

export function archiveThreadOptimisticState(state, { id, archived, archivedAt, timestamp }) {
  const targetArchived = Boolean(archived);
  return {
    activeThreadId: targetArchived && normalizeThreadId(state.activeThreadId) === id ? '' : state.activeThreadId,
    threads: state.threads.map((thread) => (thread.id === id ? {
      ...thread,
      archived: targetArchived,
      archivedAt,
      status: threadArchiveStatus(thread, targetArchived),
    } : thread)),
    threadArchiveLoadingByThread: {
      ...state.threadArchiveLoadingByThread,
      [id]: true,
    },
    lastListMutationTime: timestamp,
    lastArchivedStatesByThread: {
      ...state.lastArchivedStatesByThread,
      [id]: { archived: targetArchived, timestamp },
    },
  };
}

export function archiveThreadFailureState(state, {
  id,
  originalThreads,
  originalActiveThreadId,
  actionNotice,
}) {
  const nextMutated = { ...state.lastArchivedStatesByThread };
  delete nextMutated[id];
  const originalThread = (originalThreads || optionalUiArray()).find((thread) => threadMatchesIdentifier(thread, id));
  return {
    activeThreadId: state.activeThreadId === '' ? originalActiveThreadId : state.activeThreadId,
    threads: state.threads.map((thread) => (threadMatchesIdentifier(thread, id) && originalThread ? {
      ...thread,
      archived: Boolean(originalThread.archived),
      archivedAt: originalThread.archivedAt || 0,
      status: originalThread.status,
    } : thread)),
    threadArchiveLoadingByThread: {
      ...state.threadArchiveLoadingByThread,
      [id]: false,
    },
    lastArchivedStatesByThread: nextMutated,
    actionNotice,
  };
}
