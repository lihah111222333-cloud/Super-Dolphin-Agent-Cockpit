
import { getThreadConfig } from '../../../../../shared/api/backendApi.js';
import { normalizeActiveProviderName } from '../providerRuntimeConfig.js';
import { DEFAULT_PROVIDER, normalizePath, normalizeString, normalizeThreadConfig, optionalUiObject, resolveLaunchPreferences } from './clientStoreUtils.js';
import { activeProviderLockedThreadId, threadConfigTargetIdForState } from './clientStoreRuntimeThreadModel.js';
import { actionNotice } from './clientStoreSendModel.js';

function threadConfigLoadedPatch(state, id, config) {
  return {
    threadConfigByThread: {
      ...state.threadConfigByThread,
      [id]: config,
    },
    threadConfigLoadingByThread: {
      ...state.threadConfigLoadingByThread,
      [id]: false,
    },
    threadConfigFailedByThread: {
      ...state.threadConfigFailedByThread,
      [id]: false,
    },
  };
}

function threadConfigFailedPatch(state, id) {
  return {
    threadConfigLoadingByThread: {
      ...state.threadConfigLoadingByThread,
      [id]: false,
    },
    threadConfigFailedByThread: {
      ...state.threadConfigFailedByThread,
      [id]: true,
    },
  };
}

function threadConfigLoadingPatch(state, id) {
  return {
    threadConfigLoadingByThread: {
      ...state.threadConfigLoadingByThread,
      [id]: true,
    },
  };
}

async function loadThreadConfigAction(runtime, threadId) {
  const id = threadConfigTargetIdForState(runtime.get(), threadId);
  if (!id) return null;
  runtime.set((state) => threadConfigLoadingPatch(state, id));
  try {
    const raw = await getThreadConfig({ threadId: id });
    const config = normalizeThreadConfig(raw, id, runtime.get().provider);
    runtime.set((state) => threadConfigLoadedPatch(state, id, config));
    return config;
  }
  catch (error) {
    runtime.set((state) => threadConfigFailedPatch(state, id));
    runtime.addWarning('error', 'thread.config.get.failed', { threadId: id, error: error.message });
    return null;
  }
}

async function toggleProviderModeAction(runtime) {
  const lockedThreadId = activeProviderLockedThreadId(runtime.get());
  if (lockedThreadId) {
    runtime.notifyAction('已开启的聊天不能更改 provider，请新建对话后切换', 'warning', { threadId: lockedThreadId });
    return false;
  }
  runtime.set({
    provider: DEFAULT_PROVIDER,
    actionNotice: actionNotice('当前桌面仅支持 Codex provider', 'warning'),
  });
  runtime.addWarning('warn', 'provider.toggle.unsupported', { requestedProvider: 'claude' });
  return false;
}

function createNavigationActions(runtime) {
  return {
    setActivePage: (activePage) => runtime.set({ activePage }),
    resolveLaunchPreferences: (cwdArg) => {
      const cwd = normalizePath(cwdArg) || runtime.requireCwd('thread.launchPreferences');
      return resolveLaunchPreferences(cwd, runtime.addWarning, runtime.getPreference);
    },

  };
}

function scopedPageCacheEntry(state, cacheName, key, defaults, patch) {
  return {
    ...defaults,
    ...(state[cacheName]?.[key] || optionalUiObject()),
    ...patch,
  };
}

function scopedPageCachePatch(state, cacheName, key, defaults, patch) {
  return {
    [cacheName]: {
      ...state[cacheName],
      [key]: scopedPageCacheEntry(state, cacheName, key, defaults, patch),
    },
  };
}

function setScopedPageCache(runtime, cacheName, cwd, defaults, patch) {
  const key = normalizeString(cwd);
  if (!key) return;
  runtime.set((state) => scopedPageCachePatch(state, cacheName, key, defaults, patch));
}

function defaultPromptPageCache() {
  return {
    items: [],
    activePromptId: '',
    fallbackMode: false,
    hasLoadedPrompts: false,
  };
}

function defaultWorkflowPageCache() {
  return {
    items: [],
    selectedDagKey: '',
    detailsByDagKey: {},
    hasLoadedDags: false,
  };
}

function defaultSkillPageCache() {
  return {
    items: [],
    resolutionConflicts: [],
    hasLoadedSkills: false,
  };
}

function defaultSharedFileRetention() {
  return {
    items: [],
    protectedCount: 0,
    cleanupCandidateCount: 0,
  };
}

function defaultSharedFilesPageCache() {
  return {
    files: [],
    finalOutputRefs: [],
    retention: defaultSharedFileRetention(),
    hasLoadedFiles: false,
  };
}

function defaultMemorySnapshot() {
  return {
    overview: {},
    entries: [],
  };
}

function defaultMemoryPageCache() {
  return {
    snapshot: defaultMemorySnapshot(),
    hasLoadedMemory: false,
  };
}

function createPromptWorkflowCacheActions(runtime) {
  return {
    setPromptPageCache: (cwd, patch = {}) => setScopedPageCache(
      runtime,
      'promptPageCacheByCwd',
      cwd,
      defaultPromptPageCache(),
      patch,
    ),
    setWorkflowPageCache: (cwd, patch = {}) => setScopedPageCache(
      runtime,
      'workflowPageCacheByCwd',
      cwd,
      defaultWorkflowPageCache(),
      patch,
    ),

  };
}

function createResourcePageCacheActions(runtime) {
  return {
    setSkillPageCache: (cwd, patch = {}) => setScopedPageCache(
      runtime,
      'skillPageCacheByCwd',
      cwd,
      defaultSkillPageCache(),
      patch,
    ),
    setSharedFilesPageCache: (cwd, patch = {}) => setScopedPageCache(
      runtime,
      'sharedFilesPageCacheByCwd',
      cwd,
      defaultSharedFilesPageCache(),
      patch,
    ),
    setMemoryPageCache: (cwd, patch = {}) => setScopedPageCache(
      runtime,
      'memoryPageCacheByCwd',
      cwd,
      defaultMemoryPageCache(),
      patch,
    ),
    setRightPanelWidth: (rightPanelWidth) => runtime.set({ rightPanelWidth }),


  };
}

function createProviderConfigActions(runtime) {
  return {
    refreshProviderConfig: () => {
      const cwd = runtime.requireCwd('provider.config');
      const provider = normalizeActiveProviderName(runtime.get().provider, 'provider.config') || DEFAULT_PROVIDER;
      return runtime.loadProviderConfig(cwd, provider);
    },

    loadThreadConfig: (threadId) => loadThreadConfigAction(runtime, threadId),


  };
}

function createProviderActions(runtime) {
  return {
    toggleProviderMode: () => toggleProviderModeAction(runtime),


  };
}


export {
  createNavigationActions,
  createPromptWorkflowCacheActions,
  createProviderActions,
  createProviderConfigActions,
  createResourcePageCacheActions,
  defaultMemoryPageCache,
  defaultMemorySnapshot,
  defaultPromptPageCache,
  defaultSharedFileRetention,
  defaultSharedFilesPageCache,
  defaultSkillPageCache,
  defaultWorkflowPageCache,
  scopedPageCacheEntry,
  scopedPageCachePatch,
  setScopedPageCache,
  loadThreadConfigAction,
  threadConfigLoadingPatch,
  toggleProviderModeAction,
};
