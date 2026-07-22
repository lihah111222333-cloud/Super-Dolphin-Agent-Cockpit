
import { firstOptionalPresent, optionalTextField } from '../../contractStoreModel.js';
import { getThreadMessages, emitFrontendTraceEvent } from '../../../../../shared/api/backendApi.js';
import { attachActiveThreadRpcRuntime, createStopRequestId } from '../../threadLifecycleRuntime.js';
import { attachThreadMessagesRuntime } from '../../threadMessagesRuntime.js';
import { attachAssistantEventRuntime } from '../assistantEventRuntime.js';
import { attachWarningRuntime } from '../warningRuntime.js';
import { normalizeActiveProviderName, normalizeCodexIdentityValue, normalizeProviderConfigValue, normalizeProviderRuntimeConfig } from '../providerRuntimeConfig.js';
import { providerPreferenceKey } from '../providerPreferences.js';
import { normalizeThreadId, normalizeBackendThreadId } from '../threadIdentity.js';
import { composerDraftKey, normalizeComposerDraftSnapshot, isEmptyComposerDraftSnapshot } from '../../composerAttachments.js';
import { restoreComposerCapabilities } from '../../capabilities/composerCapabilities.js';
import {
  ASSISTANT_DELTA_FLUSH_MS,
  DEFAULT_PROVIDER,
  cleanObject,
  clockNowISO,
  clockNowMillis,
  normalizePath,
  normalizeString,
  optionalUiArray,
} from './clientStoreUtils.js';
import {
  activeTurnIdForThread,
  activeThreadInterruptTarget,
  backendThreadIdForState,
  extractDeltaText,
  pickThreadScopedEntry,
  runtimeThreadIdentifier,
  threadMatchesIdentifier,
} from './clientStoreRuntimeThreadModel.js';
import { hasOwn } from './clientStoreThreadModel.js';
import { buildSnapshotState, mergeRuntimeResultEntries, runtimeResultEntryFromRPCDone } from './clientStoreSnapshotModel.js';
import { actionNotice, actionNoticeRuntimeFields } from './clientStoreSendModel.js';
import { attachBridgeEventRuntime, attachBridgeIdentityRuntime, attachBridgeLifecycleRuntime, attachBridgePatchRuntime } from './clientStoreBridgeRuntime.js';
import { attachSidebarRuntime } from './clientStoreSidebarRuntime.js';

function clearedChatSurfaceState(state, activeThreadId, cwd) {
  const scopedEntry = (map) => pickThreadScopedEntry(map, activeThreadId);
  return {
    activeThreadId,
    threads: activeThreadId
      ? state.threads.filter((thread) => threadMatchesIdentifier(thread, activeThreadId))
      : [],
    pinnedThreadAtById: {},
    statuses: scopedEntry(state.statuses),
    activeTurnByThread: scopedEntry(state.activeTurnByThread),
    threadConfigByThread: scopedEntry(state.threadConfigByThread),
    threadConfigLoadingByThread: scopedEntry(state.threadConfigLoadingByThread),
    threadConfigFailedByThread: scopedEntry(state.threadConfigFailedByThread),
    threadStateLoadingByThread: activeThreadId ? { [activeThreadId]: true } : {},
    threadArchiveLoadingByThread: scopedEntry(state.threadArchiveLoadingByThread),
    pendingActiveThreadId: activeThreadId ? (state.pendingActiveThreadId || activeThreadId) : '',
    timelinesByThread: scopedEntry(state.timelinesByThread),
    threadTimelineReadyByThread: scopedEntry(state.threadTimelineReadyByThread),
    threadMessagePaginationByThread: scopedEntry(state.threadMessagePaginationByThread),
    tokenUsageByThread: scopedEntry(state.tokenUsageByThread),
    activityStatsByThread: scopedEntry(state.activityStatsByThread),
    diffTextByThread: scopedEntry(state.diffTextByThread),
    threadDiffReadyByThread: scopedEntry(state.threadDiffReadyByThread),
    runtimeResultEntries: [],
    draft: activeThreadId ? state.draft : '',
    attachments: activeThreadId ? state.attachments : [],
    chatSurfaceLoadingCwd: cwd,
  };
}

function createClientStoreRuntime(set, get, { getPreference }) {
  /*
   * runtime 放前端临时工具：sequence、分页 generation、delta buffer、sidebar cache。
   * 这些不是可持久化状态，destroy/reset 时要一起清掉。
   */
  const runtime = {
    set,
    get,
    getPreference,
    bridgeUnsubscribe: null,
    reconnectUnsubscribe: null,
    eventInitializationPromise: null,
    eventInitializationGeneration: 0,
    eventInitializationState: 'idle',
    pendingRuntimeSubscriptions: new Set(),
    bridgeScopeRebindGeneration: 0,
    pendingBridgeScopeRebind: null,
    sequencesByThread: new Map(),
    patchGenerationsByThread: new Map(),
    composerDrafts: new Map(),
    sidebarSnapshotsByCwd: new Map(),
    sidebarRefreshesByCwd: new Map(),
    threadMessageGenerations: new Map(),
    threadSyncGenerations: new Map(),
    assistantDeltaBuffers: new Map(),
    turnTerminalStates: new Map(),
    observedTurnByThread: new Map(),
    retiredTurnRefs: new Map(),
    retiredTurnFilter: new Uint32Array(256),
    assistantEventLedgersByScope: new Map(),
    assistantEventScope: '',
    bridgeEventScopeGeneration: 0,
    assistantDeltaFlushTimer: null,
    assistantEventScopeEpoch: 0,
    sidebarRefreshSeq: 0,
    bootstrapRetryAfterReconnect: false,
  };
  attachComposerDraftRuntime(runtime);
  attachWarningRuntime(runtime, {
    cleanObject,
    emitFrontendTraceEvent,
    normalizeString,
    normalizeThreadId,
    runtimeThreadIdentifier,
  });
  attachLogRuntime(runtime);
  attachScopeRuntime(runtime);
  attachProviderRuntime(runtime);
  attachSidebarRuntime(runtime);
  attachThreadMessagesRuntime(runtime, {
    backendThreadIdForState,
    emitFrontendTraceEvent,
    getThreadMessages,
  });
  attachNotificationRuntime(runtime);
  attachBridgeIdentityRuntime(runtime);
  attachBridgeLifecycleRuntime(runtime);
  attachAssistantEventRuntime(runtime, {
    ASSISTANT_DELTA_FLUSH_MS,
    activeTurnIdForThread,
    actionNotice,
    clockNowISO,
    clockNowMillis,
    emitFrontendTraceEvent,
    extractDeltaText,
    hasOwn,
    normalizeString,
    optionalUiArray,
    runtimeThreadIdentifier,
    threadMatchesIdentifier,
  });
  attachBridgePatchRuntime(runtime);
  attachBridgeEventRuntime(runtime);
  attachActiveThreadRpcRuntime(runtime, {
    activeThreadInterruptTarget,
    backendThreadIdForState,
    cleanObject,
    createRequestId: createStopRequestId,
  });
  return runtime;
}

function attachComposerDraftRuntime(runtime) {
  const { get } = runtime;
  const { composerDrafts } = runtime;

  const saveActiveComposerDraft = (state = get()) => {
    const key = composerDraftKey(state);
    const snapshot = normalizeComposerDraftSnapshot(state);
    if (isEmptyComposerDraftSnapshot(snapshot)) {
      composerDrafts.delete(key);
      return;
    }
    composerDrafts.set(key, snapshot);
  };

  const saveComposerDraftSnapshot = (state = get(), threadId = state.activeThreadId, snapshot = {}) => {
    const key = composerDraftKey(state, threadId);
    const normalized = normalizeComposerDraftSnapshot(snapshot);
    if (isEmptyComposerDraftSnapshot(normalized)) {
      composerDrafts.delete(key);
      return;
    }
    composerDrafts.set(key, normalized);
  };

  const restoreComposerDraft = (state, threadId) => {
    const key = composerDraftKey(state, threadId);
    const restored = normalizeComposerDraftSnapshot(composerDrafts.get(key));
    return {
      ...restored,
      composerCapabilities: restoreComposerCapabilities(restored.composerCapabilities),
    };
  };

  const clearComposerDraft = (state, threadId) => {
    composerDrafts.delete(composerDraftKey(state, threadId));
  };


  Object.assign(runtime, { saveActiveComposerDraft, saveComposerDraftSnapshot, restoreComposerDraft, clearComposerDraft });
}

function attachLogRuntime(runtime) {
  const { set, addWarning } = runtime;

  const addLog = (level, event, fields = {}) => {
    const parts = (event || optionalTextField()).split('.');
    const scope = parts.length > 1 ? parts[0] : 'terminal';
    const eventName = parts.length > 1 ? parts.slice(1).join('.') : event;

    const entry = {
      id: `${event}-${clockNowMillis()}-${Math.random().toString(16).slice(2)}`,
      ts: clockNowISO(),
      level,
      scope,
      event: eventName,
      fields,
    };
    set((state) => ({
      logEntries: [entry, ...state.logEntries].slice(0, 600),
      runtimeResultEntries: mergeRuntimeResultEntries(
        state.runtimeResultEntries,
        [runtimeResultEntryFromRPCDone(event, fields)].filter(Boolean),
      ),
    }));

    if (level === 'warn' || level === 'error') {
      addWarning(level, event, fields);
    }
  };

  const setLogLevel = (level) => {
    try {
      if (typeof localStorage !== 'undefined') {
        localStorage.setItem('agent-orchestrator.log.level', level);
      }
    }
    catch (error) {
      addWarning('error', 'log_level.preference_save.failed', {
        status: 'storage_write_failed',
        error: 'action failure; see Health diagnostic ID',
      });
      throw error;
    }
    set({ logLevel: level });
  };

  Object.assign(runtime, { addLog, setLogLevel });
}

function attachScopeRuntime(runtime) {
  const { set, get, addWarning } = runtime;
  const { sequencesByThread, patchGenerationsByThread, sidebarSnapshotsByCwd, threadMessageGenerations, threadSyncGenerations } = runtime;

  const requireCwd = (reason) => {
    const activeProject = normalizePath(get().activeProject);
    const cwd = activeProject && activeProject !== '.' ? activeProject : normalizePath(get().cwd);
    if (!cwd || cwd === '.') {
      const error = new Error(`frontend-app: cwd is required for ${reason}`);
      addWarning('error', 'missing.cwd', { reason });
      throw error;
    }
    return cwd;
  };

  const requireProjectScopeCwd = (reason) => {
    const cwd = normalizePath(get().projectScopeCwd) || normalizePath(get().cwd);
    if (!cwd || cwd === '.') {
      const error = new Error(`frontend-app: project scope cwd is required for ${reason}`);
      addWarning('error', 'missing.project_scope_cwd', { reason });
      throw error;
    }
    return cwd;
  };

  const currentChatCwd = () => {
    const activeProject = normalizePath(get().activeProject);
    return activeProject && activeProject !== '.' ? activeProject : normalizePath(get().cwd);
  };

  const clearChatSurfaceForCwdSwitch = (cwdValue = '', options = {}) => {
    const cwd = normalizePath(cwdValue);
    const preserveActiveThreadId = options.preserveActiveThreadId === true;
    if (runtime.assistantEventScope !== cwd) {
      throw new Error('frontend-app: bridge scope must be rebound before clearing the chat surface');
    }
    sequencesByThread.clear();
    patchGenerationsByThread.clear();
    threadMessageGenerations.clear();
    threadSyncGenerations.clear();
    set((state) => {
      const activeThreadId = preserveActiveThreadId ? normalizeBackendThreadId(state.activeThreadId) : '';
      const targetScope = { ...state, activeProject: cwd, cwd };
      const restored = runtime.restoreComposerDraft(targetScope, activeThreadId);
      return {
        ...clearedChatSurfaceState(state, activeThreadId, cwd),
        draft: restored.draft,
        attachments: restored.attachments,
        composerCapabilities: restored.composerCapabilities,
      };
    });
  };

  const applyProjects = (payload, fallbackCwd) => {
    const projects = Array.isArray(payload?.projects)
      ? payload.projects.map(normalizePath).filter(Boolean)
      : [];
    const active = normalizePath(firstOptionalPresent(payload?.active, payload?.activeProject, fallbackCwd));
    set({
      projects,
      activeProject: active || normalizePath(fallbackCwd),
    });
  };

  const cacheSidebarSnapshot = (cwdValue, snapshot) => {
    const cwd = normalizePath(cwdValue);
    if (!cwd || cwd === '.' || !snapshot || typeof snapshot !== 'object' || Array.isArray(snapshot)) return;
    sidebarSnapshotsByCwd.set(cwd, snapshot);
  };


  Object.assign(runtime, { requireCwd, requireProjectScopeCwd, currentChatCwd, clearChatSurfaceForCwdSwitch, applyProjects, cacheSidebarSnapshot });
}

function attachProviderRuntime(runtime) {
  const { set, get, getPreference, requireCwd } = runtime;

  const loadProviderConfig = async (cwdValue, providerValue) => {
    const cwd = normalizePath(cwdValue) || requireCwd('provider.config');
    const provider = normalizeActiveProviderName(providerValue || get().provider, 'provider.config') || DEFAULT_PROVIDER;
    const modelKey = providerPreferenceKey(provider, 'model');
    const effortKey = providerPreferenceKey(provider, 'effort');
    const codexModelProviderKey = providerPreferenceKey('codex', 'codexModelProvider');
    const [model, effort, codexModelProvider] = await Promise.all([
      getPreference({ cwd, key: modelKey }),
      getPreference({ cwd, key: effortKey }),
      getPreference({ cwd, key: codexModelProviderKey }),
    ]);
    const providerConfig = normalizeProviderRuntimeConfig({
      model: normalizeProviderConfigValue(model),
      effort: normalizeProviderConfigValue(effort),
      codexModelProvider: normalizeCodexIdentityValue(codexModelProvider),
    }, provider);
    set({ provider, providerConfig });
    return providerConfig;
  };

  const applySnapshot = (payload = {}, options = {}) => {
    set((state) => buildSnapshotState(state, payload, options));
  };


  Object.assign(runtime, { loadProviderConfig, applySnapshot });
}

function attachNotificationRuntime(runtime) {
  const { set, addWarning } = runtime;

  const notifyAction = (message, tone = 'info', fields = {}) => {
    const baseNotice = actionNotice(message, tone);
    const notice = baseNotice ? { ...baseNotice, ...actionNoticeRuntimeFields(fields) } : null;
    if (!notice) return;
    set((state) => ({
      actionNotice: notice,
      activityEntries: [{
        id: `action-${clockNowMillis()}-${Math.random().toString(16).slice(2)}`,
        method: 'ui/action',
        threadId: normalizeThreadId(fields.threadId),
        message: notice.message,
        timestamp: notice.timestamp,
      }, ...state.activityEntries].slice(0, 120),
    }));
  };

  const notifyRPCFailure = (messagePrefix, warningEvent, error, fields = {}) => {
    notifyAction(`${messagePrefix}失败，请重试。`, 'error', fields);
    addWarning('error', warningEvent, { ...fields, error: 'action failure; see Health diagnostic ID' });
    return false;
  };


  Object.assign(runtime, { notifyAction, notifyRPCFailure });
}


export {
  attachComposerDraftRuntime,
  attachLogRuntime,
  attachNotificationRuntime,
  attachProviderRuntime,
  attachScopeRuntime,
  attachSidebarRuntime,
  createClientStoreRuntime,
};
