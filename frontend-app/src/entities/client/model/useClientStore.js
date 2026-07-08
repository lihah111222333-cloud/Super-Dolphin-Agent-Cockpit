import { create } from 'zustand';
import {
  addProject as addProjectRPC,
  getPreference,
  getProjects,
  getSidebarState,
  getThreadState,
  getWindowBootstrap,
  onBridgeEvent,
  onRuntimeReconnect,
  openNewWindow as openNewWindowRPC,
  readConfig,
  registerBridgeLogStore,
  removeProject as removeProjectRPC,
  selectProjectDir,
  setActiveProject as setActiveProjectRPC,
} from '../../../shared/api/backendApi.js';
import { createComposerSlice } from './composerSlice.js';
import { createForkSlice } from './forkSlice.js';
import { createProjectSlice } from './projectSlice.js';
import { createRuntimeSlice } from './runtimeSlice.js';
import {
  PROVIDER_ACTIVE_PREF_KEY,
  requireActiveProviderPreference,
} from './helpers/providerRuntimeConfig.js';
import { isDagNodeStatusBridgeEvent } from './helpers/bridgeRevision.js';
import { createThreadSelectionActions } from './helpers/threadSelectionActions.js';
import {
  baseState,
  clockNowMillis,
  normalizeBootstrapPage,
  normalizeBootstrapSnapshot,
  normalizePath,
  normalizeString,
  optionalUiObject,
  projectShortLabel,
  resetClientStoreClockMillisForTests,
  resolveLaunchPreferences,
  setClientStoreClockMillisForTestsValue,
  stateWithPatch,
} from './helpers/a1/clientStoreUtils.js';
import { normalizeThreadId } from './helpers/threadIdentity.js';
import {
  backendThreadIdForState,
  shouldAutoLoadThreadConfig,
  threadConfigTargetIdForState,
  threadMatchesIdentifier,
  upsertExplicitThread,
} from './helpers/a1/clientStoreRuntimeThreadModel.js';
import {
  normalizeThread,
} from './helpers/a1/clientStoreThreadModel.js';
import {
  actionNotice,
  composerActionDeps,
  forkActionDeps,
} from './helpers/a1/clientStoreSendModel.js';
import { createClientStoreRuntime } from './helpers/a1/clientStoreRuntimeCore.js';
import {
  createNavigationActions,
  createPromptWorkflowCacheActions,
  createProviderActions,
  createProviderConfigActions,
  createResourcePageCacheActions,
} from './helpers/a1/clientStorePageActions.js';
import {
  createActiveThreadActions,
  createDashboardCommandActions,
  createThreadArchiveActions,
  createThreadCopyActions,
  createThreadDeleteActions,
  createThreadRenamePinActions,
} from './helpers/a1/clientStoreThreadActions.js';

const projectActionDeps = {
  addProject: (payload) => addProjectRPC(payload),
  normalizePath,
  openNewWindow: (payload) => openNewWindowRPC(payload),
  projectShortLabel,
  removeProject: (payload) => removeProjectRPC(payload),
  selectProjectDir: (seed) => selectProjectDir(seed),
  setActiveProject: (payload) => setActiveProjectRPC(payload),
};

const runtimeActionDeps = {
  backendThreadIdForState,
  getPreference: (payload) => getPreference(payload),
  getProjects: (payload) => getProjects(payload),
  getSidebarState: (payload) => getSidebarState(payload),
  getThreadState: (payload) => getThreadState(payload),
  getWindowBootstrap: () => getWindowBootstrap(),
  isDagNodeStatusBridgeEvent,
  normalizeBootstrapPage,
  normalizeBootstrapSnapshot,
  normalizePath,
  normalizeThreadId,
  onBridgeEvent: (callback, options) => onBridgeEvent(callback, options),
  onRuntimeReconnect: (callback) => onRuntimeReconnect(callback),
  providerActivePreferenceKey: PROVIDER_ACTIVE_PREF_KEY,
  readConfig: () => readConfig(),
  requireActiveProviderPreference,
  shouldAutoLoadThreadConfig,
};

function createClientStore(set, get) {
  const runtime = createClientStoreRuntime(set, get);
  const composerDeps = {
    ...composerActionDeps,
    send: {
      ...composerActionDeps.send,
      resolveLaunchPreferences: (cwd) => resolveLaunchPreferences(cwd, runtime.addWarning),
    },
  };
  return {
    ...baseState,
    ...createRuntimeSlice(runtime, runtimeActionDeps),
    ...createNavigationActions(runtime),
    ...createPromptWorkflowCacheActions(runtime),
    ...createResourcePageCacheActions(runtime),
    ...createProviderConfigActions(runtime),
    ...createProjectSlice(runtime, projectActionDeps),
    ...createProviderActions(runtime),
    ...createThreadSelectionActions(runtime, {
      actionNotice,
      backendThreadIdForState,
      clockNowMillis,
      normalizeThread,
      normalizeString,
      optionalUiObject,
      threadConfigTargetIdForState,
      threadMatchesIdentifier,
      upsertExplicitThread,
    }),
    ...createForkSlice(runtime, forkActionDeps),
    ...createComposerSlice(runtime, composerDeps),
    ...createDashboardCommandActions(runtime),
    ...createActiveThreadActions(runtime),
    ...createThreadCopyActions(runtime),
    ...createThreadRenamePinActions(runtime),
    ...createThreadArchiveActions(runtime),
    ...createThreadDeleteActions(runtime),
    addWarning: runtime.addWarning,
    addLog: runtime.addLog,
    setLogLevel: runtime.setLogLevel,
    toggleSmoothStreaming: () => {
      runtime.set((state) => ({ smoothStreaming: !state.smoothStreaming }));
    },
  };
}

export const useClientStore = create(createClientStore);

export function resetClientStoreForTests(patch = {}) {
  resetClientStoreClockMillisForTests();
  useClientStore.getState().destroy();
  useClientStore.setState(stateWithPatch(patch));
}

export function setClientStoreClockMillisForTests(clock) {
  setClientStoreClockMillisForTestsValue(clock);
}

registerBridgeLogStore({
  info: (event, fields) => useClientStore.getState().addLog('info', event, fields),
  debug: (event, fields) => useClientStore.getState().addLog('debug', event, fields),
  warn: (event, fields) => useClientStore.getState().addLog('warn', event, fields),
  error: (event, fields) => useClientStore.getState().addLog('error', event, fields),
});
