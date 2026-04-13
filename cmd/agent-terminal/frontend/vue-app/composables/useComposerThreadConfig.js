import { ref, computed, nextTick } from '../../lib/vue.esm-browser.prod.js';
import {
  appendCurrentOption,
  EFFORT_MODES,
  EFFORT_MODES_BY_PROVIDER,
  isClaudeOpusFamilyModel,
  MODEL_OPTIONS,
  MODEL_OPTIONS_BY_PROVIDER,
  normalizeProviderConfigValue,
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

  const threadConfigVisible = computed(() =>
    !props.isCmd &&
    Boolean(props.threadId) &&
    ['codex', 'claude'].includes(normalizedThreadConfigProvider.value)
  );
  const threadConfigEditable = computed(() => threadConfigVisible.value && Boolean(props.threadConfigSupportsOverride));
  const threadConfigInherited = computed(() => !overrideModel.value && !overrideEffort.value);

  const threadConfigModelOptions = computed(() =>
    appendCurrentOption(
      MODEL_OPTIONS_BY_PROVIDER[normalizedThreadConfigProvider.value] || MODEL_OPTIONS,
      draftModel.value,
    )
  );
  const threadConfigEffortBaseOptions = computed(() =>
    EFFORT_MODES_BY_PROVIDER[normalizedThreadConfigProvider.value] || EFFORT_MODES
  );
  const threadConfigEffortOptions = computed(() => {
    const baseOptions = threadConfigEffortBaseOptions.value;
    if (normalizedThreadConfigProvider.value !== 'claude') {
      return appendCurrentOption(baseOptions, draftEffort.value);
    }
    const filteredOptions = isClaudeOpusFamilyModel(selectedThreadConfigModel.value)
      ? baseOptions
      : baseOptions.filter((item) => item.value !== 'max');
    const normalizedDraftEffort = draftEffort.value === 'max' && !isClaudeOpusFamilyModel(selectedThreadConfigModel.value)
      ? 'high'
      : draftEffort.value;
    return appendCurrentOption(filteredOptions, normalizedDraftEffort);
  });

  const threadConfigSummaryLabel = computed(() => {
    if (threadConfigInherited.value) {
      const parts = [effectiveModel.value, effectiveEffort.value].filter(Boolean);
      return parts.length > 0 ? parts.join('-') : '继承全局';
    }
    const model = overrideModel.value || effectiveModel.value || '';
    const effort = overrideEffort.value || effectiveEffort.value || '';
    const parts = [model.split('/').pop(), effort].filter(Boolean);
    return parts.length > 0 ? parts.join('-') : '已覆盖';
  });

  const threadConfigInheritModelLabel = computed(() => '默认');
  const threadConfigInheritEffortLabel = computed(() => '默认');

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
