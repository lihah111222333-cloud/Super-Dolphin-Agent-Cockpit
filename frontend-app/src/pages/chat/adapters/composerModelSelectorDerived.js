import { canonicalizeModelValue, modelOptionFor, normalizeConfigText, normalizeProviderKey } from '../../shared/pageShared.js';

const EFFORT_OPTIONS_BY_PROVIDER = Object.freeze({
  codex: Object.freeze([
    { value: 'xhigh', label: '超高' },
    { value: 'high', label: '高' },
    { value: 'medium', label: '中' },
    { value: 'low', label: '低' },
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

function localizedEffortOption(option, copy) {
  const label = copy?.modelEffortLabels?.[option.value];
  return label ? { ...option, label } : option;
}

function effortOptionFor(provider, value, copy) {
  const normalized = normalizeConfigText(value);
  const options = EFFORT_OPTIONS_BY_PROVIDER[normalizeProviderKey(provider)] || EFFORT_OPTIONS_BY_PROVIDER.codex;
  const option = options.find((item) => item.value === normalized);
  if (option) return localizedEffortOption(option, copy);
  return normalized ? { value: normalized, label: normalized } : null;
}

function appendCurrentEffortOption(provider, value, model = '', copy) {
  const providerKey = normalizeProviderKey(provider);
  const baseOptions = EFFORT_OPTIONS_BY_PROVIDER[providerKey] || EFFORT_OPTIONS_BY_PROVIDER.codex;
  const options = providerKey === 'claude' && !isClaudeOpusFamilyModel(model)
    ? baseOptions.filter((item) => item.value !== 'max')
    : baseOptions;
  const localizedOptions = options.map((item) => localizedEffortOption(item, copy));
  const current = effortOptionFor(provider, value, copy);
  if (!current || localizedOptions.some((item) => item.value === current.value)) return localizedOptions;
  return [...localizedOptions, current];
}

function composerModelLabel(provider, model, effort, copy) {
  const providerKey = normalizeProviderKey(provider);
  const modelValue = normalizeConfigText(model) || MODEL_DEFAULTS_BY_PROVIDER[providerKey].model;
  const effortValue = normalizeConfigText(effort) || MODEL_DEFAULTS_BY_PROVIDER[providerKey].effort;
  const modelLabel = modelOptionFor(providerKey, modelValue)?.label || modelValue;
  const effortLabel = effortOptionFor(providerKey, effortValue, copy)?.label || effortValue;
  if (providerKey === 'codex') return `${modelLabel.replace(/^GPT-/i, '')} ${effortLabel}`.trim();
  return `${modelLabel} · ${effortLabel}`.trim();
}

function firstConfigText(...values) {
  for (const value of values) {
    const text = normalizeConfigText(value);
    if (text) return text;
  }
  return '';
}

function inheritedConfigLabel(activeValue, activeLabel, copy) {
  if (!activeValue) return copy?.inheritDefault || '默认';
  return `${copy?.inheritCurrentPrefix || '默认（当前：'}${activeLabel}${copy?.inheritCurrentSuffix || '）'}`;
}

function modelSelectorTitle(disabled, canOverrideThread, copy) {
  if (disabled) return copy?.projectActionBlocked || '请先连接后端并选择项目';
  return canOverrideThread
    ? copy?.threadModelConfig || '线程执行配置'
    : copy?.globalModelConfig || '全局模型配置';
}

function providerModelOptions(activeThreadConfig) {
  if (!activeThreadConfig) return [];
  if (!Array.isArray(activeThreadConfig.availableModels)) {
    throw new Error('thread config availableModels is required');
  }
  return activeThreadConfig.availableModels.map((model) => ({ value: model, label: model }));
}

function modelSelectorDerivedState(options) {
  const { activeEffort, activeModel, activeThreadConfig, canOverrideThread, copy, disabled, draft, providerKey, store, activeThreadId } = options;
  const selectedModel = canonicalizeModelValue(providerKey, draft.model || activeModel);
  const selectedEffort = draft.effort || activeEffort;
  const activeEffortLabel = effortOptionFor(providerKey, activeEffort, copy)?.label || activeEffort;
  const activeModelLabel = modelOptionFor(providerKey, activeModel)?.label || activeModel;
  return {
    canOverrideThread,
    disabled,
    providerKey,
    effortOptions: appendCurrentEffortOption(providerKey, selectedEffort, selectedModel, copy),
    inheritEffortLabel: inheritedConfigLabel(activeEffort, activeEffortLabel, copy),
    inheritModelLabel: inheritedConfigLabel(activeModel, activeModelLabel, copy),
    inherited: canOverrideThread && !activeThreadConfig?.override?.model && !activeThreadConfig?.override?.effort,
    label: composerModelLabel(providerKey, activeModel, activeEffort, copy),
    modelOptions: providerModelOptions(activeThreadConfig),
    selectEffortValue: canOverrideThread ? draft.effort : draft.effort || activeEffort,
    selectModelValue: canOverrideThread
      ? canonicalizeModelValue(providerKey, draft.model)
      : canonicalizeModelValue(providerKey, draft.model || activeModel),
    selectorBusy: Boolean(store.threadConfigSaving || (activeThreadId && store.threadConfigLoadingByThread?.[activeThreadId])),
    selectorTitle: modelSelectorTitle(disabled, canOverrideThread, copy),
  };
}

export {
  MODEL_DEFAULTS_BY_PROVIDER,
  firstConfigText,
  isClaudeOpusFamilyModel,
  modelSelectorDerivedState,
};
