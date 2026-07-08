import { normalizeOptionalTextField, optionalTextField, systemClockMillis, currentIsoTimestamp } from './contractStoreModel.js';
function optionalUiArray() {
  return [];
}

function optionalUiObject() {
  return {};
}

// @ts-check

import {
  normalizeThreadId } from './helpers/threadIdentity.js';

/**
 * @typedef {{
 *   readSharedFile?: (request: { path: string }) => Promise<{ path?: unknown, content?: unknown }>,
 * }} ForkSharedFileDeps
 */

function normalizeString(value) {
  return normalizeOptionalTextField(value);
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
  return normalizeForkSharedFiles(cache || optionalUiObject());
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
  (state.attachments || optionalUiArray()).forEach((item) => add(item?.path, true));
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

/**
 * @param {ForkSharedFileDeps} [deps]
 */
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
        content: optionalTextField(detail.content),
      };
    }));
  };
}

export function buildForkThreadState(options) { const { state, threadId, identity, launchPreferences, name, kickoffText, deps = {} } = options;
  const {
    actionNotice,
    defaultProvider = '',
    emptyForkDraft,
    nowISO = () => currentIsoTimestamp(),
    nowMillis = () => systemClockMillis(),
    threadActivityTimestamp = () => systemClockMillis(),
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

function isForkKickoffTimelineItem(item) {
  return Boolean(item?.optimistic && normalizeString(item?.id).startsWith('fork-kickoff-'));
}

// markForkKickoffFailedState 把已创建但开场消息失败的 fork 标成需要用户处理，避免继续显示为工作中。
export function markForkKickoffFailedState(state, threadId, errorMessage) {
  const id = normalizeThreadId(threadId);
  const timelinesByThread = { ...state.timelinesByThread };
  const timeline = Array.isArray(timelinesByThread[id]) ? timelinesByThread[id] : [];
  timelinesByThread[id] = timeline.filter((item) => !isForkKickoffTimelineItem(item));
  return {
    threads: state.threads.map((thread) => (
      normalizeThreadId(thread?.id) === id
        ? {
            ...thread,
            status: '需要操作',
            forkKickoffStatus: 'failed',
            forkKickoffError: normalizeString(errorMessage),
          }
        : thread
    )),
    timelinesByThread,
  };
}
