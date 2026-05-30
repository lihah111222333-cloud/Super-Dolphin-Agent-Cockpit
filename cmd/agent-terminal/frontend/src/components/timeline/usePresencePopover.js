import { computed, onBeforeUnmount, ref, watch } from '../../../lib/vue.esm-browser.prod.js';

function resolvePresenceTargetValue(target) {
  if (typeof target === 'string') return target || 'body';
  if (target && typeof target === 'object' && 'value' in target) {
    return target.value || 'body';
  }
  return target || 'body';
}

function hasPresenceTargetValue(target) {
  if (typeof target === 'string') return Boolean(target);
  if (target && typeof target === 'object' && 'value' in target) {
    return Boolean(target.value);
  }
  return Boolean(target);
}

function clearPresencePopoverCloseTimer(state) {
  if (!state.presencePopoverCloseTimer || typeof clearTimeout !== 'function') return;
  clearTimeout(state.presencePopoverCloseTimer);
  state.presencePopoverCloseTimer = 0;
}

export function usePresencePopover(props, {
  trailingProcessItems,
  latestPresenceItems,
  formatTime,
  toolSummaryKindLabel,
  toolSummaryText,
  toolTickerText,
  translateThinkingBody,
}) {
  const presencePopoverVisible = ref(false);
  const state = { presencePopoverCloseTimer: 0 };
  const resolvedPresenceTarget = computed(() => resolvePresenceTargetValue(props.presenceTarget));
  const hasPresenceTarget = computed(() => hasPresenceTargetValue(props.presenceTarget));
  const sharedStatusText = computed(() => (props.activeStatusText || '').toString().trim());
  const showAgentPresence = computed(() => {
    const text = sharedStatusText.value;
    if (!text || text === '未选择会话') return false;
    return true;
  });
  const pendingProcessItems = computed(() => trailingProcessItems(props.items));
  const presencePopoverItems = computed(() => latestPresenceItems(props.items));
  const presenceLabel = computed(() => sharedStatusText.value);
  const sharedStatusMeta = computed(() => (props.activeStatusMeta || '').toString().trim());
  const thinkingPopoverText = computed(() => {
    const recent = presencePopoverItems.value;
    for (let index = recent.length - 1; index >= 0; index -= 1) {
      const item = recent[index];
      if (!item || typeof item !== 'object') continue;
      const text = (item.text || '').toString().trim();
      if (!text) continue;
      if (item.kind === 'thinking') return translateThinkingBody(text);
    }
    return '';
  });
  const thinkingToolSummaries = computed(() => {
    const recent = presencePopoverItems.value;
    const entries = [];
    const seen = new Set();
    for (let index = recent.length - 1; index >= 0 && entries.length < 6; index -= 1) {
      const item = recent[index];
      if (!item || typeof item !== 'object') continue;
      if (!['tool', 'command', 'file'].includes(item.kind)) continue;
      const text = toolSummaryText(item);
      if (!text) continue;
      const time = formatTime(item.ts);
      const key = `${item.kind}|${text}|${time}|${item.status || ''}`;
      if (seen.has(key)) continue;
      seen.add(key);
      entries.push({
        id: (item.id || `${item.kind}-${index}`).toString(),
        time,
        kindLabel: toolSummaryKindLabel(item),
        text,
      });
    }
    return entries;
  });
  const collapsedToolCount = computed(() => pendingProcessItems.value.filter((item) => item?.kind === 'tool').length);
  const collapsedToolTickerText = computed(() => {
    const recent = pendingProcessItems.value;
    const entries = [];
    const seen = new Set();
    for (let index = recent.length - 1; index >= 0 && entries.length < 8; index -= 1) {
      const item = recent[index];
      if (item?.kind !== 'tool') continue;
      const text = toolTickerText(item);
      if (!text) continue;
      const key = `${item.tool || ''}|${text}|${item.status || ''}|${item.ts || ''}`;
      if (seen.has(key)) continue;
      seen.add(key);
      entries.push(text);
    }
    return entries.reverse().join('   •   ');
  });
  const showToolTicker = computed(() => collapsedToolCount.value > 0 && Boolean(collapsedToolTickerText.value));
  const showThinkingPopover = computed(() => Boolean(thinkingPopoverText.value) || thinkingToolSummaries.value.length > 0);
  const showPresencePopover = computed(() => showThinkingPopover.value && presencePopoverVisible.value);
  const presencePopoverTitle = computed(() => {
    if (!showThinkingPopover.value) return '';
    if (collapsedToolCount.value > 0) {
      return `悬浮查看思考过程与工具摘要（已收起 ${collapsedToolCount.value} 个工具调用）`;
    }
    return '悬浮查看思考过程与工具摘要';
  });

  function openPresencePopover() {
    clearPresencePopoverCloseTimer(state);
    if (!showThinkingPopover.value) return;
    presencePopoverVisible.value = true;
  }

  function closePresencePopover() {
    clearPresencePopoverCloseTimer(state);
    presencePopoverVisible.value = false;
  }

  function schedulePresencePopoverClose() {
    clearPresencePopoverCloseTimer(state);
    if (typeof setTimeout !== 'function') {
      presencePopoverVisible.value = false;
      return;
    }
    state.presencePopoverCloseTimer = setTimeout(() => {
      presencePopoverVisible.value = false;
      state.presencePopoverCloseTimer = 0;
    }, 120);
  }

  watch(
    () => showThinkingPopover.value,
    (visible) => {
      if (!visible) closePresencePopover();
    },
    { immediate: true },
  );

  onBeforeUnmount(() => {
    clearPresencePopoverCloseTimer(state);
  });

  return {
    resolvedPresenceTarget,
    hasPresenceTarget,
    showAgentPresence,
    presenceLabel,
    sharedStatusText,
    sharedStatusMeta,
    thinkingPopoverText,
    thinkingToolSummaries,
    collapsedToolCount,
    collapsedToolTickerText,
    showToolTicker,
    showPresencePopover,
    openPresencePopover,
    closePresencePopover,
    schedulePresencePopoverClose,
    presencePopoverTitle,
    showThinkingPopover,
  };
}
