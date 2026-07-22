import { normalizeConfigText, normalizeProviderKey } from '../../shared/pageShared.js';
import { MODEL_DEFAULTS_BY_PROVIDER, firstConfigText, isClaudeOpusFamilyModel, modelSelectorDerivedState } from './composerModelSelectorDerived.js';

function activeThreadComposerConfig(store, activeThreadId) {
  return activeThreadId ? store.threadConfigByThread?.[activeThreadId] : null;
}

function modelSnapshotValue(canOverrideThread, activeThreadConfig, providerValue, defaultValue, key) {
  if (canOverrideThread) {
    return firstConfigText(activeThreadConfig?.override?.[key], activeThreadConfig?.effective?.[key], defaultValue);
  }
  return firstConfigText(providerValue, defaultValue);
}

function modelSelectorSnapshot(store, activeThreadId) {
  const activeThreadConfig = activeThreadComposerConfig(store, activeThreadId);
  const providerKey = normalizeProviderKey(firstConfigText(activeThreadConfig?.provider, store.providerConfig?.provider, store.provider));
  const providerDefaults = MODEL_DEFAULTS_BY_PROVIDER[providerKey] || MODEL_DEFAULTS_BY_PROVIDER.codex;
  const canOverrideThread = Boolean(activeThreadId && activeThreadConfig?.supportsThreadOverride);
  const activeModel = modelSnapshotValue(canOverrideThread, activeThreadConfig, store.providerConfig?.model, providerDefaults.model, 'model');
  const activeEffort = modelSnapshotValue(canOverrideThread, activeThreadConfig, store.providerConfig?.effort, providerDefaults.effort, 'effort');
  return {
    activeEffort,
    activeModel,
    activeThreadConfig,
    canOverrideThread,
    draftEffort: canOverrideThread ? normalizeConfigText(activeThreadConfig?.override?.effort) : activeEffort,
    draftModel: canOverrideThread ? normalizeConfigText(activeThreadConfig?.override?.model) : activeModel,
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
