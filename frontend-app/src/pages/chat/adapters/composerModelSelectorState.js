import { appendCurrentModelOption, canonicalizeModelValue, modelOptionFor, normalizeConfigText, normalizeProviderKey } from '../../shared/pageShared.js';

const EFFORT_OPTIONS_BY_PROVIDER = Object.freeze({
  codex: Object.freeze([
    { value: 'xhigh', label: '极高' },
    { value: 'high', label: '高' },
    { value: 'medium', label: '中' },
    { value: 'low', label: '低' },
    { value: 'minimal', label: '极低' },
    { value: 'none', label: '关闭' },
  ]),
  claude: Object.freeze([
    { value: 'max', label: 'max' },
    { value: 'high', label: 'high' },
    { value: 'medium', label: 'medium' },
    { value: 'low', label: 'low' },
  ]),
});

const MODEL_DEFAULTS_BY_PROVIDER = Object.freeze({
  codex: Object.freeze({ model: 'gpt-5.5', effort: 'xhigh' }),
  claude: Object.freeze({ model: 'sonnet', effort: 'high' }),
});

function isClaudeOpusFamilyModel(model) {
  const normalized = normalizeConfigText(model).toLowerCase();
  return normalized === 'best' || normalized.includes('opus');
}

function effortOptionFor(provider, value) {
  const normalized = normalizeConfigText(value);
  const options = EFFORT_OPTIONS_BY_PROVIDER[normalizeProviderKey(provider)] || EFFORT_OPTIONS_BY_PROVIDER.codex;
  return options.find((item) => item.value === normalized) || (normalized ? { value: normalized, label: normalized } : null);
}

function appendCurrentEffortOption(provider, value, model = '') {
  const providerKey = normalizeProviderKey(provider);
  const baseOptions = EFFORT_OPTIONS_BY_PROVIDER[providerKey] || EFFORT_OPTIONS_BY_PROVIDER.codex;
  const options = providerKey === 'claude' && !isClaudeOpusFamilyModel(model)
    ? baseOptions.filter((item) => item.value !== 'max')
    : baseOptions;
  const current = effortOptionFor(provider, value);
  if (!current || options.some((item) => item.value === current.value)) return options;
  return [...options, current];
}

function composerModelLabel(provider, model, effort) {
  const providerKey = normalizeProviderKey(provider);
  const modelValue = normalizeConfigText(model) || MODEL_DEFAULTS_BY_PROVIDER[providerKey].model;
  const effortValue = normalizeConfigText(effort) || MODEL_DEFAULTS_BY_PROVIDER[providerKey].effort;
  const modelLabel = modelOptionFor(providerKey, modelValue)?.label || modelValue;
  const effortLabel = effortOptionFor(providerKey, effortValue)?.label || effortValue;
  return `${modelLabel} · ${effortLabel}`.trim();
}

function firstConfigText(...values) {
  for (const value of values) {
    const text = normalizeConfigText(value);
    if (text) return text;
  }
  return '';
}

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

function modelSelectorTitle(disabled, canOverrideThread) {
  if (disabled) return '请先连接后端并选择项目';
  return canOverrideThread ? '线程执行配置' : '全局模型配置';
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

function modelSelectorDerivedState({ activeEffort, activeModel, activeThreadConfig, canOverrideThread, disabled, draft, providerKey, store, activeThreadId }) {
  const selectedModel = canonicalizeModelValue(providerKey, draft.model || activeModel);
  const selectedEffort = draft.effort || activeEffort;
  return {
    canOverrideThread,
    disabled,
    effortOptions: appendCurrentEffortOption(providerKey, selectedEffort, selectedModel),
    inheritEffortLabel: activeEffort ? `默认（当前：${effortOptionFor(providerKey, activeEffort)?.label || activeEffort}）` : '默认',
    inheritModelLabel: activeModel ? `默认（当前：${modelOptionFor(providerKey, activeModel)?.label || activeModel}）` : '默认',
    inherited: canOverrideThread && !activeThreadConfig?.override?.model && !activeThreadConfig?.override?.effort,
    label: composerModelLabel(providerKey, activeModel, activeEffort),
    modelOptions: appendCurrentModelOption(providerKey, selectedModel),
    selectEffortValue: canOverrideThread ? draft.effort : draft.effort || activeEffort,
    selectModelValue: canOverrideThread
      ? canonicalizeModelValue(providerKey, draft.model)
      : canonicalizeModelValue(providerKey, draft.model || activeModel),
    selectorBusy: Boolean(store.threadConfigSaving || (activeThreadId && store.threadConfigLoadingByThread?.[activeThreadId])),
    selectorTitle: modelSelectorTitle(disabled, canOverrideThread),
  };
}

export {
  loadedModelDraft,
  modelSelectorDerivedState,
  modelSelectorSnapshot,
  nextModelDraft,
};
