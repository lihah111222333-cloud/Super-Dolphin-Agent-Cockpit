import { ref, computed, nextTick } from '../../lib/vue.esm-browser.prod.js';
import { EFFORT_MODES, MODEL_OPTIONS } from '../provider-config-options.js';

function normalizeThreadConfigValue(value) {
  return (value || '').toString().trim();
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
  const effectiveModel = computed(() => normalizeThreadConfigValue(props.threadConfigMeta?.effective?.model));
  const effectiveEffort = computed(() => normalizeThreadConfigValue(props.threadConfigMeta?.effective?.effort));
  const overrideModel = computed(() => normalizeThreadConfigValue(props.threadConfigMeta?.override?.model));
  const overrideEffort = computed(() => normalizeThreadConfigValue(props.threadConfigMeta?.override?.effort));

  const threadConfigVisible = computed(() => !props.isCmd && Boolean(props.threadId) && normalizedThreadConfigProvider.value === 'codex');
  const threadConfigEditable = computed(() => threadConfigVisible.value && Boolean(props.threadConfigSupportsOverride));
  const threadConfigInherited = computed(() => !overrideModel.value && !overrideEffort.value);

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
    emit('update-thread-config-model', value);
    nextTick(() => emit('save-thread-config'));
  }

  function onEffortSelectChange(value) {
    emit('update-thread-config-effort', value);
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
    threadConfigModelOptions: MODEL_OPTIONS,
    threadConfigEffortOptions: EFFORT_MODES,
  };
}
