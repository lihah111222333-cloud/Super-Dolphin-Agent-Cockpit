import { ref, computed, nextTick } from '../../lib/vue.esm-browser.prod.js';
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

/**
 * 管理 ComposerBar 线程配置弹窗（model / effort 覆盖）。
 *
 * @param {object} props - ComposerBar props
 * @param {(event: string, ...args: any[]) => void} emit
 */
export function useComposerThreadConfig(props, emit) {
  const threadConfigOpen = ref(false);
  const threadConfigWrapRef = ref(null);
  const threadConfigTriggerRef = ref(null);
  const threadConfigDropdownStyle = ref({});

  const normalizedThreadConfigProvider = computed(() => normalizeThreadConfigValue(props.threadConfigProvider));
  const draftModel = computed(() => normalizeThreadConfigValue(props.threadConfigDraftModel));
  const draftEffort = computed(() => normalizeThreadConfigValue(props.threadConfigDraftEffort));
  const effectiveModel = computed(() => normalizeThreadConfigValue(props.threadConfigMeta?.effective?.model));
  const effectiveEffort = computed(() => normalizeThreadConfigValue(props.threadConfigMeta?.effective?.effort));
  const overrideModel = computed(() => normalizeThreadConfigValue(props.threadConfigMeta?.override?.model));
  const overrideEffort = computed(() => normalizeThreadConfigValue(props.threadConfigMeta?.override?.effort));
  const selectedThreadConfigModel = computed(() => draftModel.value || overrideModel.value || effectiveModel.value);
  const selectedThreadConfigEffort = computed(() => draftEffort.value || overrideEffort.value || effectiveEffort.value);
  const normalizedSelectedThreadConfigEffort = computed(() => {
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

  const threadConfigVisible = computed(() =>
    !props.isCmd &&
    Boolean(props.threadId) &&
    ['codex', 'claude'].includes(normalizedThreadConfigProvider.value)
  );
  const threadConfigEditable = computed(() => threadConfigVisible.value && Boolean(props.threadConfigSupportsOverride));
  const threadConfigInherited = computed(() => !overrideModel.value && !overrideEffort.value);

  const threadConfigModelOptions = computed(() => {
    const providerKey = normalizedThreadConfigProvider.value;
    const matched = MODEL_OPTIONS_BY_PROVIDER[providerKey];
    // Canonicalize long slugs (e.g. claude-opus-4-7[1m]) back to the short
    // alias (opus[1m]) so the dropdown highlights the correct existing option
    // instead of appending a raw long slug at the bottom.
    const canonicalModel = canonicalizeModelValue(providerKey, selectedThreadConfigModel.value);
    return appendCurrentOption(
      matched || MODEL_OPTIONS,
      canonicalModel,
    );
  });
  const threadConfigEffortBaseOptions = computed(() =>
    EFFORT_MODES_BY_PROVIDER[normalizedThreadConfigProvider.value] || EFFORT_MODES
  );
  const threadConfigEffortOptions = computed(() => {
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

  // Convert a raw model slug to its short, human-readable label.
  // e.g. "claude-opus-4-7[1m]" → "Opus 4.7 [1M]", "sonnet" → "Sonnet 4.7"
  function shortModelLabel(rawModel) {
    const provider = normalizedThreadConfigProvider.value;
    const canonical = canonicalizeModelValue(provider, rawModel);
    if (!canonical) return '';
    const options = MODEL_OPTIONS_BY_PROVIDER[provider];
    const match = options?.find((o) => normalizeProviderConfigValue(o.value) === canonical);
    return match?.label || canonical;
  }

  const threadConfigSummaryLabel = computed(() => {
    if (threadConfigInherited.value) {
      // Show model name + effort directly without "(继承全局)" suffix.
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

  const threadConfigInheritModelLabel = computed(() => {
    const currentModel = effectiveModel.value || selectedThreadConfigModel.value;
    return currentModel ? `默认（当前：${currentModel}）` : '默认';
  });
  const threadConfigInheritEffortLabel = computed(() => {
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
    nextTick(() => emit('save-thread-config'));
  }

  function onEffortSelectChange(value) {
    const normalizedValue = normalizeThreadConfigValue(value);
    const nextEffort = normalizedThreadConfigProvider.value === 'claude' &&
      normalizedValue.toLowerCase() === 'max' &&
      !isClaudeOpusFamilyModel(selectedThreadConfigModel.value)
      ? 'high'
      : normalizedValue;
    emit('update-thread-config-effort', nextEffort);
    nextTick(() => emit('save-thread-config'));
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
