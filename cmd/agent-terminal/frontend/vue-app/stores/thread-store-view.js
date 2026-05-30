// @ts-nocheck
import { deriveChatAgents, deriveCmdAgents } from './thread-view.model.js';
import { parseEpochMillis, parseThreadCreatedAtFromID } from './thread-time-utils.js';

export function createThreadViewHelpers(state) {
  function resolveMaxTimestamp(source, keys) {
    if (!source || typeof source !== 'object') return 0;
    let max = 0;
    for (const key of keys) {
      const ts = parseEpochMillis(source[key]);
      if (ts > max) max = ts;
    }
    return max;
  }

  function resolveThreadActivityAt(thread) {
    const id = (thread?.id || thread || '').toString().trim();
    if (!id) return 0;
    const meta = state.agentMetaById?.[id];
    const threadActivityAt = resolveMaxTimestamp(thread, ['updatedAt', 'updated_at']);
    let activityAt = threadActivityAt;
    if (!isThreadWorking(thread, id)) {
      const recentTurnAt = resolveMaxTimestamp(meta, ['lastActiveAt', 'last_active_at']);
      if (recentTurnAt > activityAt) activityAt = recentTurnAt;
    }
    if (activityAt > 0) return activityAt;
    const createdKeys = ['createdAt', 'created_at', 'startedAt', 'started_at'];
    const createdAt = Math.max(resolveMaxTimestamp(thread, createdKeys), resolveMaxTimestamp(meta, createdKeys));
    if (createdAt > 0) return createdAt;
    return parseThreadCreatedAtFromID(id);
  }

  function isThreadWorking(thread, id) {
    const raw = (state.statuses?.[id] || thread?.threadStatus || thread?.state || thread?.status || '').toString().trim().toLowerCase();
    return raw === 'starting'
      || raw === 'thinking'
      || raw === 'responding'
      || raw === 'running'
      || raw === 'editing'
      || raw === 'waiting'
      || raw === 'syncing';
  }

  function displayName(thread) {
    if (!thread?.id) return '';
    const threadName = (thread.name || '').toString().trim();
    if (threadName && threadName !== thread.id) return threadName;
    const alias = (state.agentMetaById[thread.id]?.alias || '').toString().trim();
    return alias || threadName || '新对话';
  }

  function sortChatThreadsByPinned(threads) {
    const list = Array.isArray(threads) ? threads.slice() : [];
    if (list.length <= 1) return list;
    const indexByID = new Map();
    const activityAtByID = new Map();
    for (let i = 0; i < list.length; i += 1) {
      const id = (list[i]?.id || '').toString().trim();
      indexByID.set(id, i);
      activityAtByID.set(id, resolveThreadActivityAt(list[i]));
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
        const leftActivityAt = activityAtByID.get(leftID) ?? 0;
        const rightActivityAt = activityAtByID.get(rightID) ?? 0;
        if (leftActivityAt !== rightActivityAt) return rightActivityAt - leftActivityAt;
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
    return all.filter((t) => {
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
