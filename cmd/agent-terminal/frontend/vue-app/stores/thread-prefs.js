// @ts-nocheck
import { callAPI } from '../services/api.js';
import { logDebug, logInfo } from '../services/log.js';
import { normalizeChatLayout, normalizeCmdLayout } from './thread-view.model.js';
import {
  normalizePreferenceScopeCwd,
  normalizeSplitRatio,
  normalizeThreadRailWidth,
  normalizeCmdCardCols,
} from './thread-ui-normalize.js';
import {
  PREF_ACTIVE_THREAD_ID,
  PREF_ACTIVE_CMD_THREAD_ID,

  PREF_VIEW_CHAT,
  PREF_VIEW_CMD,
  PREF_PINNED_THREADS_CHAT,
  PREF_ARCHIVED_THREADS_CHAT,
  normalizeChatPrefs as normalizeChatPrefsModel,
  normalizeCmdPrefs as normalizeCmdPrefsModel,
} from './thread-preference.model.js';

export {
  PREF_ACTIVE_THREAD_ID,
  PREF_ACTIVE_CMD_THREAD_ID,

  PREF_VIEW_CHAT,
  PREF_VIEW_CMD,
  PREF_PINNED_THREADS_CHAT,
  PREF_ARCHIVED_THREADS_CHAT,
  normalizeChatPrefsModel as normalizeChatPrefs,
  normalizeCmdPrefsModel as normalizeCmdPrefs,
};



const preferenceWriteQueueByKey = new Map();
let preferenceScopeCwd = '';

export function shouldSyncAfterPreferencePersist(prefKey) {
  const key = (prefKey || '').toString().trim();
  return key !== PREF_ACTIVE_THREAD_ID && key !== PREF_ACTIVE_CMD_THREAD_ID;
}

export function withPreferenceScope(payload = {}) {
  const next = payload && typeof payload === 'object' && !Array.isArray(payload)
    ? { ...payload }
    : {};
  if (preferenceScopeCwd) next.cwd = preferenceScopeCwd;
  return next;
}

function persistRemote(key, value) {
  return callAPI('ui/preferences/set', withPreferenceScope({ key, value }));
}


export function createPreferenceManager(state, { syncRuntimeState } = {}) {
  const runSyncRuntimeState = typeof syncRuntimeState === 'function'
    ? syncRuntimeState
    : () => Promise.resolve();

  function setPreferenceScopeCwd(cwd) {
    preferenceScopeCwd = normalizePreferenceScopeCwd(cwd);
    logDebug('thread', 'prefs.scope.changed', { cwd: preferenceScopeCwd });
  }

  function getPreferenceScopeCwd() {
    return preferenceScopeCwd;
  }

  function persistPreferenceAndSync(prefKey, value, logMeta = {}, options = {}) {
    const queueKey = (prefKey || '').toString();
    const syncAfterPersist = Object.prototype.hasOwnProperty.call(options, 'syncAfterPersist')
      ? Boolean(options.syncAfterPersist)
      : shouldSyncAfterPreferencePersist(queueKey);
    logInfo('thread', 'prefs.persist.queued', {
      key: queueKey,
      sync_after_persist: syncAfterPersist,
      cwd: preferenceScopeCwd,
      ...logMeta,
    });
    const prev = preferenceWriteQueueByKey.get(queueKey) || Promise.resolve();
    const current = prev
      .catch(() => {})
      .then(() => {
        logInfo('thread', 'prefs.persist.remote.start', {
          key: queueKey,
          sync_after_persist: syncAfterPersist,
          cwd: preferenceScopeCwd,
          ...logMeta,
        });
        return persistRemote(queueKey, value);
      })
      .then(() => {
        logInfo('thread', 'prefs.persist.remote.done', {
          key: queueKey,
          sync_after_persist: syncAfterPersist,
          cwd: preferenceScopeCwd,
          ...logMeta,
        });
        if (!syncAfterPersist) return;
        logInfo('thread', 'prefs.persist.sync.schedule', {
          key: queueKey,
          cwd: preferenceScopeCwd,
          ...logMeta,
        });
        runSyncRuntimeState().catch(() => {});
      })
      .catch((error) => {
        logDebug('thread', 'prefs.persist.failed', { key: prefKey, error, ...logMeta });
      });
    preferenceWriteQueueByKey.set(queueKey, current);
    current.finally(() => {
      if (preferenceWriteQueueByKey.get(queueKey) === current) preferenceWriteQueueByKey.delete(queueKey);
    });
    return current;
  }
  function readChatPrefs() {
    return normalizeChatPrefsModel(state.viewPrefsChat);
  }

  function readCmdPrefs() {
    return normalizeCmdPrefsModel(state.viewPrefsCmd);
  }


  function getLayout(mode) {
    return mode === 'cmd' ? readCmdPrefs().layout : readChatPrefs().layout;
  }

  function setLayout(mode, layout) {
    if (mode === 'cmd') {
      const current = readCmdPrefs();
      persistPreferenceAndSync(PREF_VIEW_CMD, { ...current, layout: normalizeCmdLayout(layout) }, { mode: 'cmd', field: 'layout' });
      return;
    }
    const current = readChatPrefs();
    persistPreferenceAndSync(PREF_VIEW_CHAT, { ...current, layout: normalizeChatLayout(layout) }, { mode: 'chat', field: 'layout' });
  }

  function getSplitRatio(mode) {
    return mode === 'cmd' ? readCmdPrefs().splitRatio : readChatPrefs().splitRatio;
  }

  function setSplitRatio(mode, ratio) {
    const next = normalizeSplitRatio(ratio);
    if (mode === 'cmd') {
      const current = readCmdPrefs();
      persistPreferenceAndSync(PREF_VIEW_CMD, { ...current, splitRatio: next }, { mode: 'cmd', field: 'splitRatio' });
      return;
    }
    const current = readChatPrefs();
    persistPreferenceAndSync(PREF_VIEW_CHAT, { ...current, splitRatio: next }, { mode: 'chat', field: 'splitRatio' });
  }

  function getThreadRailWidth() {
    return readChatPrefs().threadRailWidth;
  }

  function setThreadRailWidth(width) {
    const next = normalizeThreadRailWidth(width);
    const current = readChatPrefs();
    if (current.threadRailWidth === next) return;
    persistPreferenceAndSync(PREF_VIEW_CHAT, { ...current, threadRailWidth: next }, { mode: 'chat', field: 'threadRailWidth' });
  }

  function getCmdCardCols() {
    return readCmdPrefs().cardCols;
  }

  function setCmdCardCols(cols) {
    const current = readCmdPrefs();
    persistPreferenceAndSync(PREF_VIEW_CMD, { ...current, cardCols: normalizeCmdCardCols(cols) }, { mode: 'cmd', field: 'cardCols' });
  }

  return {
    setPreferenceScopeCwd,
    getPreferenceScopeCwd,
    persistPreferenceAndSync,
    getLayout,
    setLayout,
    getSplitRatio,
    setSplitRatio,
    getThreadRailWidth,
    setThreadRailWidth,
    getCmdCardCols,
    setCmdCardCols,
  };
}
