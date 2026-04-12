// @ts-nocheck
import { deriveChatAgents, deriveCmdAgents } from './thread-view.model.js';
import { parseEpochMillis, parseThreadCreatedAtFromID } from './thread-time-utils.js';

export function createThreadViewHelpers(state) {
  function resolveThreadCreatedAt(threadId) {
    const id = (threadId || '').toString().trim();
    if (!id) return 0;
    const meta = state.agentMetaById?.[id];
    if (meta && typeof meta === 'object') {
      for (const key of ['createdAt', 'created_at', 'startedAt', 'started_at']) {
        const ts = parseEpochMillis(meta[key]);
        if (ts > 0) return ts;
      }
    }
    return parseThreadCreatedAtFromID(id);
  }

  function displayName(thread) {
    if (!thread?.id) return '';
    const threadName = (thread.name || '').toString().trim();
    if (threadName && threadName !== thread.id) return threadName;
    const alias = (state.agentMetaById[thread.id]?.alias || '').toString().trim();
    return alias || threadName || thread.id;
  }

  function sortChatThreadsByPinned(threads) {
    const list = Array.isArray(threads) ? threads.slice() : [];
    if (list.length <= 1) return list;
    const indexByID = new Map();
    const createdAtByID = new Map();
    for (let i = 0; i < list.length; i += 1) {
      const id = (list[i]?.id || '').toString().trim();
      indexByID.set(id, i);
      createdAtByID.set(id, resolveThreadCreatedAt(id));
    }
    list.sort((left, right) => {
      const leftID = (left?.id || '').toString().trim();
      const rightID = (right?.id || '').toString().trim();
      const leftPinnedAt = Number(state.pinnedThreadAtById?.[leftID]);
      const rightPinnedAt = Number(state.pinnedThreadAtById?.[rightID]);
      const leftPinned = Number.isFinite(leftPinnedAt) && leftPinnedAt > 0;
      const rightPinned = Number.isFinite(rightPinnedAt) && rightPinnedAt > 0;
      if (leftPinned !== rightPinned) return leftPinned ? -1 : 1;
      if (leftPinned && rightPinned && leftPinnedAt !== rightPinnedAt) return rightPinnedAt - leftPinnedAt;
      if (!leftPinned && !rightPinned) {
        const leftCreatedAt = createdAtByID.get(leftID) ?? 0;
        const rightCreatedAt = createdAtByID.get(rightID) ?? 0;
        if (leftCreatedAt !== rightCreatedAt) return rightCreatedAt - leftCreatedAt;
      }
      return (indexByID.get(leftID) ?? 0) - (indexByID.get(rightID) ?? 0);
    });
    return list;
  }

  function getThreadsByMode(mode, activeProjectPath = '') {
    const all = mode === 'cmd'
      ? deriveCmdAgents({ threads: state.threads })
      : sortChatThreadsByPinned(deriveChatAgents({ threads: state.threads }));
    if (!activeProjectPath || activeProjectPath === '.') return all;
    const activeId = mode === 'cmd' ? state.activeCmdThreadId : state.activeThreadId;
    return all.filter((t) => {
      if (t.id && t.id === activeId) return true;
      const cwd = (state.agentRuntimeById[t.id]?.cwd || '').toString();
      if (!cwd) return true;
      return cwd === activeProjectPath || cwd.startsWith(activeProjectPath + '/') || cwd.endsWith('/' + activeProjectPath);
    });
  }

  function getCurrentThreadId(mode) {
    return mode === 'cmd' ? (state.activeCmdThreadId || '') : state.activeThreadId;
  }

  return { displayName, getThreadsByMode, getCurrentThreadId };
}
