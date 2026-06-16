// @ts-check

import {
  normalizeThreadId,
} from './threadIdentity.js';

function normalizeString(value) {
  return (value || '').toString().trim();
}

function normalizePath(value) {
  const path = normalizeString(value);
  if (!path) return '';
  if (path !== '/' && !/^[a-zA-Z]:[\\/]?$/.test(path)) {
    return path.replace(/[\\/]+$/, '');
  }
  return path;
}

export function forkSourceTitle(thread, threadId) {
  const name = normalizeString(thread?.name);
  if (name) return `继承自会话：${name}`;
  const id = normalizeThreadId(threadId || thread?.id);
  return id ? `继承自会话：${id}` : '继承自前一个对话';
}

export function normalizeForkSharedFiles(response) {
  const files = Array.isArray(response?.files) ? response.files : [];
  const seen = new Set();
  const normalized = [];
  for (const file of files) {
    const path = normalizeString(typeof file === 'string' ? file : file?.path);
    if (!path || seen.has(path)) continue;
    seen.add(path);
    normalized.push({ path });
  }
  return normalized;
}

export function cachedForkSharedFiles(state) {
  const cwd = normalizePath(state.activeProject || state.cwd);
  const cache = cwd ? state.sharedFilesPageCacheByCwd?.[cwd] : null;
  return normalizeForkSharedFiles(cache || {});
}

export function initialForkSharedFilePaths(state, availableSharedFiles = [], seedPath = '') {
  const available = new Set(availableSharedFiles.map((file) => file.path));
  const selected = [];
  const add = (path, requireAvailable) => {
    const value = normalizeString(path);
    if (!value || selected.includes(value)) return;
    if (requireAvailable && !available.has(value)) return;
    selected.push(value);
  };
  (state.attachments || []).forEach((item) => add(item?.path, true));
  add(seedPath, false);
  return selected;
}

export function mergeForkSharedFilesWithSelected(availableSharedFiles = [], selectedPaths = []) {
  const seen = new Set();
  const merged = [];
  for (const file of availableSharedFiles) {
    const path = normalizeString(file?.path);
    if (!path || seen.has(path)) continue;
    seen.add(path);
    merged.push({ path });
  }
  for (const path of selectedPaths) {
    const value = normalizeString(path);
    if (!value || seen.has(value)) continue;
    seen.add(value);
    merged.push({ path: value });
  }
  return merged;
}

export function createLoadForkSharedFiles({ readSharedFile } = {}) {
  if (typeof readSharedFile !== 'function') throw new Error('readSharedFile is required');
  return async function loadForkSharedFiles(paths = []) {
    const selected = paths.map(normalizeString).filter(Boolean);
    if (selected.length === 0) return [];
    return Promise.all(selected.map(async (path) => {
      const detail = await readSharedFile({ path });
      if (!detail || typeof detail !== 'object' || Array.isArray(detail)) {
        throw new Error(`shared file ${path} returned empty response`);
      }
      return {
        path: normalizeString(detail.path) || path,
        content: (detail.content || '').toString(),
      };
    }));
  };
}

export function buildForkThreadState(state, threadId, identity, launchPreferences, name, kickoffText, deps = {}) {
  const {
    actionNotice,
    defaultProvider = '',
    emptyForkDraft,
    nowISO = () => new Date().toISOString(),
    nowMillis = () => Date.now(),
    threadActivityTimestamp = () => Date.now(),
    threadMatchesIdentifier = (thread, id) => normalizeThreadId(thread?.id) === normalizeThreadId(id),
  } = deps;
  const provider = launchPreferences.modelProvider || launchPreferences.provider || state.provider || defaultProvider;
  return {
    activePage: 'chat',
    activeThreadId: threadId,
    provider,
    activityThreadAtById: {
      ...state.activityThreadAtById,
      [threadId]: threadActivityTimestamp(),
    },
    forkDraft: emptyForkDraft(),
    actionNotice: actionNotice('已创建继承对话', 'success'),
    threads: [
      {
        id: threadId,
        agentId: identity.agentId,
        providerThreadId: identity.providerThreadId,
        sessionId: identity.sessionId,
        name,
        provider,
        status: '工作中',
      },
      ...state.threads.filter((item) => !threadMatchesIdentifier(item, threadId)),
    ],
    timelinesByThread: {
      ...state.timelinesByThread,
      [threadId]: [{
        id: `fork-kickoff-${nowMillis()}`,
        role: 'user',
        text: kickoffText,
        time: nowISO(),
        done: true,
        optimistic: true,
      }],
    },
  };
}
