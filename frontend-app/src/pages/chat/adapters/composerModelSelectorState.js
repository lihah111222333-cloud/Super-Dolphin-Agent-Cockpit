import { normalizeConfigText, normalizeProviderKey } from '../../shared/pageShared.js';
import { MODEL_DEFAULTS_BY_PROVIDER, firstConfigText, isClaudeOpusFamilyModel, modelSelectorDerivedState } from './composerModelSelectorDerived.js';

function activeThreadComposerConfig(store, activeThreadId) {
  const id = normalizeConfigText(activeThreadId);
  if (!id) return null;
  const configs = store.threadConfigByThread;
  if (!configs) return null;
  if (configs[id]) return configs[id];
  if (!Array.isArray(store.threads)) return null;
  const thread = store.threads.find((candidate) => (
    [candidate?.id, candidate?.agentId, candidate?.providerThreadId, candidate?.sessionId]
      .map(normalizeConfigText)
      .includes(id)
  ));
  if (!thread) return null;
  for (const candidate of [thread.id, thread.agentId, thread.providerThreadId, thread.sessionId]) {
    const key = normalizeConfigText(candidate);
    if (key && configs[key]) return configs[key];
  }
  return null;
}

function providerCatalogConfig(store, activeThreadConfig) {
  if (activeThreadConfig) return activeThreadConfig;
  if (!store.threadConfigByThread) return null;
  const provider = normalizeProviderKey(firstConfigText(store.providerConfig?.provider, store.provider));
  return Object.values(store.threadConfigByThread).find((config) => (
    normalizeProviderKey(config?.provider) === provider
    && Array.isArray(config?.availableModels)
    && config.availableModels.length > 0
  )) || null;
}

function modelSnapshotValue(canOverrideThread, activeThreadConfig, providerValue, defaultValue, key) {
  if (canOverrideThread) {
    return firstConfigText(activeThreadConfig?.override?.[key], activeThreadConfig?.effective?.[key], defaultValue);
  }
  return firstConfigText(providerValue, defaultValue);
}

function modelSelectorSnapshot(store, activeThreadId) {
  const threadConfig = activeThreadComposerConfig(store, activeThreadId);
  const activeThreadConfig = providerCatalogConfig(store, threadConfig);
  const providerKey = normalizeProviderKey(firstConfigText(activeThreadConfig?.provider, store.providerConfig?.provider, store.provider));
  const providerDefaults = MODEL_DEFAULTS_BY_PROVIDER[providerKey] || MODEL_DEFAULTS_BY_PROVIDER.codex;
  const canOverrideThread = Boolean(activeThreadId && threadConfig?.supportsThreadOverride);
  const activeModel = modelSnapshotValue(canOverrideThread, threadConfig, store.providerConfig?.model, providerDefaults.model, 'model');
  const activeEffort = modelSnapshotValue(canOverrideThread, threadConfig, store.providerConfig?.effort, providerDefaults.effort, 'effort');
  return {
    activeEffort,
    activeModel,
    activeThreadConfig,
    canOverrideThread,
    draftEffort: canOverrideThread ? normalizeConfigText(threadConfig?.override?.effort) : activeEffort,
    draftModel: canOverrideThread ? normalizeConfigText(threadConfig?.override?.model) : activeModel,
    providerKey,
  };
}

function nextModelDraft(providerKey, draft, patch, activeModel) {
  const next = { ...draft, ...patch };
  const nextEffort = normalizeConfigText(next.effort).toLowerCase();
  if (providerKey === 'claude' && nextEffort === 'max' && !isClaudeOpusFamilyModel(next.model || activeModel)) {
    return { ...next, effort: 'high' };
  }
  return next;
}

function loadedModelDraft(loaded, activeModel, activeEffort) {
  const loadedCanOverride = Boolean(loaded?.supportsThreadOverride);
  return {
    model: loadedCanOverride ? normalizeConfigText(loaded.override?.model) : activeModel,
    effort: loadedCanOverride ? normalizeConfigText(loaded.override?.effort) : activeEffort,
  };
}

export {
  loadedModelDraft,
  modelSelectorDerivedState,
  modelSelectorSnapshot,
  nextModelDraft,
};
