import * as Vue from '../../lib/vue.esm-browser.prod.js';
import {
  appendCurrentOption,
  canonicalizeModelValue,
  EFFORT_MODES,
  EFFORT_MODES_BY_PROVIDER,
  isClaudeOpusFamilyModel,
  MODEL_OPTIONS,
  MODEL_OPTIONS_BY_PROVIDER,
  normalizeProviderConfigValue,
  getProviderDefaultConfig,
} from '../provider-config-options.js';

function normalizeThreadConfigValue(value) {
  return normalizeProviderConfigValue(value);
}

export function useComposerThreadConfigVue(props, emit) {
  const threadConfigOpen = Vue.ref(false);
  const threadConfigWrapRef = Vue.ref(null);
  const threadConfigTriggerRef = Vue.ref(null);
  const threadConfigDropdownStyle = Vue.ref({});

  const normalizedThreadConfigProvider = Vue.computed(() => normalizeThreadConfigValue(props.threadConfigProvider));
  const draftModel = Vue.computed(() => normalizeThreadConfigValue(props.threadConfigDraftModel));
  const draftEffort = Vue.computed(() => normalizeThreadConfigValue(props.threadConfigDraftEffort));
  const effectiveModel = Vue.computed(() => normalizeThreadConfigValue(props.threadConfigMeta?.effective?.model));
  const effectiveEffort = Vue.computed(() => normalizeThreadConfigValue(props.threadConfigMeta?.effective?.effort));
  const overrideModel = Vue.computed(() => normalizeThreadConfigValue(props.threadConfigMeta?.override?.model));
  const overrideEffort = Vue.computed(() => normalizeThreadConfigValue(props.threadConfigMeta?.override?.effort));
  const selectedThreadConfigModel = Vue.computed(() => draftModel.value || overrideModel.value || effectiveModel.value);
  const selectedThreadConfigEffort = Vue.computed(() => draftEffort.value || overrideEffort.value || effectiveEffort.value);
  const normalizedSelectedThreadConfigEffort = Vue.computed(() => {
    const currentEffort = normalizeThreadConfigValue(selectedThreadConfigEffort.value);
    const provider = normalizedThreadConfigProvider.value;
    if (!currentEffort) {
      return getProviderDefaultConfig(provider).effort;
    }
    if (
      provider === 'claude' &&
      currentEffort.toLowerCase() === 'max' &&
      !isClaudeOpusFamilyModel(selectedThreadConfigModel.value)
    ) {
      return 'high';
    }
    return currentEffort;
  });

  const threadConfigVisible = Vue.computed(() =>
    !props.isCmd &&
    Boolean(props.threadId) &&
    ['codex', 'claude'].includes(normalizedThreadConfigProvider.value)
  );
  const threadConfigEditable = Vue.computed(() => threadConfigVisible.value && Boolean(props.threadConfigSupportsOverride));
  const threadConfigInherited = Vue.computed(() => !overrideModel.value && !overrideEffort.value);

  const threadConfigModelOptions = Vue.computed(() => {
    const providerKey = normalizedThreadConfigProvider.value;
    const matched = MODEL_OPTIONS_BY_PROVIDER[providerKey];
    const canonicalModel = canonicalizeModelValue(providerKey, selectedThreadConfigModel.value);
    return appendCurrentOption(
      matched || MODEL_OPTIONS,
      canonicalModel,
    );
  });
  const threadConfigEffortBaseOptions = Vue.computed(() =>
    EFFORT_MODES_BY_PROVIDER[normalizedThreadConfigProvider.value] || EFFORT_MODES
  );
  const threadConfigEffortOptions = Vue.computed(() => {
    const baseOptions = threadConfigEffortBaseOptions.value;
    if (normalizedThreadConfigProvider.value !== 'claude') {
      return appendCurrentOption(baseOptions, normalizedSelectedThreadConfigEffort.value);
    }
    const filteredOptions = isClaudeOpusFamilyModel(selectedThreadConfigModel.value)
      ? baseOptions
      : baseOptions.filter((item) => item.value !== 'max');
    const normalizedDraftEffort = normalizedSelectedThreadConfigEffort.value;
    return appendCurrentOption(filteredOptions, normalizedDraftEffort);
  });

  function shortModelLabel(rawModel) {
    const provider = normalizedThreadConfigProvider.value;
    const canonical = canonicalizeModelValue(provider, rawModel);
    if (!canonical) return '';
    const options = MODEL_OPTIONS_BY_PROVIDER[provider];
    const match = options?.find((o) => normalizeProviderConfigValue(o.value) === canonical);
    return match?.label || canonical;
  }

  const threadConfigSummaryLabel = Vue.computed(() => {
    if (threadConfigInherited.value) {
      const model = effectiveModel.value || selectedThreadConfigModel.value;
      const effort = effectiveEffort.value || normalizedSelectedThreadConfigEffort.value;
      const parts = [shortModelLabel(model), effort].filter(Boolean);
      return parts.length > 0 ? parts.join(' · ') : '';
    }
    const model = overrideModel.value || effectiveModel.value || '';
    const effort = overrideEffort.value || effectiveEffort.value || '';
    const parts = [shortModelLabel(model), effort].filter(Boolean);
    return parts.length > 0 ? parts.join(' · ') : '已覆盖';
  });

  const threadConfigInheritModelLabel = Vue.computed(() => {
    const currentModel = effectiveModel.value || selectedThreadConfigModel.value;
    return currentModel ? `默认（当前：${currentModel}）` : '默认';
  });
  const threadConfigInheritEffortLabel = Vue.computed(() => {
    const currentEffort = effectiveEffort.value || normalizedSelectedThreadConfigEffort.value;
    return currentEffort ? `默认（当前：${currentEffort}）` : '默认';
  });

  function toggleThreadConfig() {
    if (!threadConfigOpen.value) {
      threadConfigDropdownStyle.value = {
        position: 'absolute',
        bottom: 'calc(100% + 8px)',
        right: '0',
        minWidth: '240px',
        zIndex: '101',
        overflow: 'visible',
      };
    }
    threadConfigOpen.value = !threadConfigOpen.value;
  }

  function onThreadConfigClickOutside(ev) {
    if (!threadConfigOpen.value) return;
    if (threadConfigWrapRef.value && threadConfigWrapRef.value.contains(ev.target)) {
      return;
    }
    if (ev.target && ev.target.tagName && ev.target.tagName.toLowerCase() === 'option') {
      return;
    }
    threadConfigOpen.value = false;
  }

  function saveThreadConfig() {
    emit('save-thread-config');
  }

  function restoreThreadConfig() {
    if (!threadConfigInherited.value) {
      emit('restore-thread-config-inherit');
      threadConfigOpen.value = false;
    }
  }

  function onModelSelectChange(value) {
    const normalizedValue = normalizeThreadConfigValue(value);
    emit('update-thread-config-model', normalizedValue);
    if (
      normalizedThreadConfigProvider.value === 'claude' &&
      !isClaudeOpusFamilyModel(normalizedValue) &&
      normalizeThreadConfigValue(selectedThreadConfigEffort.value).toLowerCase() === 'max'
    ) {
      emit('update-thread-config-effort', 'high');
    }
    Vue.nextTick(() => emit('save-thread-config'));
  }

  function onEffortSelectChange(value) {
    const normalizedValue = normalizeThreadConfigValue(value);
    const nextEffort = normalizedThreadConfigProvider.value === 'claude' &&
      normalizedValue.toLowerCase() === 'max' &&
      !isClaudeOpusFamilyModel(selectedThreadConfigModel.value)
      ? 'high'
      : normalizedValue;
    emit('update-thread-config-effort', nextEffort);
    Vue.nextTick(() => emit('save-thread-config'));
  }

  return {
    threadConfigOpen,
    threadConfigWrapRef,
    threadConfigTriggerRef,
    threadConfigDropdownStyle,
    threadConfigVisible,
    threadConfigEditable,
    threadConfigInherited,
    threadConfigSummaryLabel,
    threadConfigInheritModelLabel,
    threadConfigInheritEffortLabel,
    toggleThreadConfig,
    onThreadConfigClickOutside,
    saveThreadConfig,
    restoreThreadConfig,
    onModelSelectChange,
    onEffortSelectChange,
    threadConfigModelOptions,
    threadConfigEffortOptions,
  };
}
