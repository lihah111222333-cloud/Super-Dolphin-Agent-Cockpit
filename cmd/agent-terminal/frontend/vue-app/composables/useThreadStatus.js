import {
  ref,
  computed,
  watch,
} from '../../lib/vue.esm-browser.prod.js';
import { isThreadActiveStatus, normalizeStatus } from '../services/status.js';
import { logInfo } from '../services/log.js';
import {
  formatTokenInline,
  formatTokenTooltip,
  formatElapsedCompact,
  getTokenLevel,
} from '../utils/format-utils.js';
import { useContextUsageThresholds } from './useContextUsageThresholds.js';

const TOKEN_LEVEL_RANK = Object.freeze({ normal: 0, warn: 1, danger: 2, critical: 3 });

/**
 * @param {object} props
 * @param {import('../../lib/vue.esm-browser.prod.js').Ref<string>} selectedThreadId
 * @param {import('../../lib/vue.esm-browser.prod.js').ComputedRef<string>} activeStatus
 * @param {import('../../lib/vue.esm-browser.prod.js').Ref<boolean>|null} [showPathChoiceModal]
 */
export function useThreadStatus(props, selectedThreadId, activeStatus, showPathChoiceModal = null) {
  function isThreadInterruptible(threadId) {
    if (!threadId) return false;
    if (typeof props.threadStore.getThreadInterruptible !== 'function') return false;
    return Boolean(props.threadStore.getThreadInterruptible(threadId));
  }

  function getThreadCapabilities(threadId) {
    if (!threadId) return [];
    const capabilities = props.threadStore?.state?.agentRuntimeById?.[threadId]?.capabilities;
    return Array.isArray(capabilities)
      ? capabilities.map((capability) => (capability || '').toString().trim().toLowerCase()).filter(Boolean)
      : [];
  }

  function getThreadStatusHeader(threadId) {
    if (!threadId) return '';
    if (typeof props.threadStore.getThreadStatusHeader !== 'function') return '';
    const header = (props.threadStore.getThreadStatusHeader(threadId) || '').toString().trim();
    if (header) return header;
    return '等待指示';
  }

  const activeStatusHeader = computed(() => getThreadStatusHeader(selectedThreadId.value));
  const activeStatusDetails = computed(() => {
    if (typeof props.threadStore.getThreadStatusDetails !== 'function') return '';
    return (props.threadStore.getThreadStatusDetails(selectedThreadId.value) || '').toString().trim();
  });
  const activeTokenUsage = computed(() => {
    if (typeof props.threadStore.getThreadTokenUsage !== 'function') return null;
    return props.threadStore.getThreadTokenUsage(selectedThreadId.value);
  });
  const canInterrupt = computed(() => isThreadInterruptible(selectedThreadId.value));
  const canCompact = computed(() => getThreadCapabilities(selectedThreadId.value).includes('context_compact'));
  const compacting = computed(() => {
    if (typeof props.threadStore.getThreadCompacting !== 'function') return false;
    return props.threadStore.getThreadCompacting(selectedThreadId.value);
  });
  const activeCompactResult = computed(() => {
    if (typeof props.threadStore.getThreadCompactResult !== 'function') return null;
    return props.threadStore.getThreadCompactResult(selectedThreadId.value);
  });
  const compactResultText = computed(() => (activeCompactResult.value?.message || '').toString().trim());
  const compactResultTone = computed(() => {
    const status = (activeCompactResult.value?.status || '').toString().trim().toLowerCase();
    if (status === 'failed') return 'error';
    if (status === 'success') return 'success';
    return '';
  });
  const compactSuccessCount = computed(() => {
    if (typeof props.threadStore.getThreadCompactSuccessCount !== 'function') return 0;
    return props.threadStore.getThreadCompactSuccessCount(selectedThreadId.value);
  });
  const displayStatusText = computed(() => {
    if (!selectedThreadId.value) return '未选择会话';
    return activeStatusHeader.value || '等待指示';
  });
  const activeTokenInline = computed(() => formatTokenInline(activeTokenUsage.value));
  const activeTokenTooltip = computed(() => formatTokenTooltip(activeTokenUsage.value));
  // Phase 1 settings: 阈值从用户偏好读，改变后下一次 computed 重算自动生效。
  const tokenThresholds = useContextUsageThresholds();
  const activeTokenLevel = computed(() => getTokenLevel(activeTokenUsage.value, tokenThresholds.value));

  // 记录每个 thread 最后一次上报过的告警等级，避免同一个阈值重复刷屏。
  // 不走 store state——这只是一次性跨阈值信号，不需要跨会话持久化。
  // 上限 64 条，防止长时间运行时 Map 无限增长。
  const lastReportedLevelByThread = new Map();
  const LEVEL_MAP_CAP = 64;
  watch(
    () => ({
      threadId: (selectedThreadId.value || '').toString().trim(),
      level: activeTokenLevel.value,
      usage: activeTokenUsage.value,
    }),
    ({ threadId, level, usage }) => {
      if (!threadId) return;
      const prev = lastReportedLevelByThread.get(threadId) || 'normal';
      if (prev === level) return;
      lastReportedLevelByThread.set(threadId, level);
      // 超过上限时淘汰最早的条目
      if (lastReportedLevelByThread.size > LEVEL_MAP_CAP) {
        const oldest = lastReportedLevelByThread.keys().next().value;
        if (oldest !== undefined) lastReportedLevelByThread.delete(oldest);
      }
      const prevRank = TOKEN_LEVEL_RANK[prev] || 0;
      const nextRank = TOKEN_LEVEL_RANK[level] || 0;
      if (nextRank <= prevRank) return; // 仅在“跳高”时上报，“退低”（例如 compact 后）静默。
      logInfo('thread', 'context_usage.level_crossed', {
        thread_id: threadId,
        level,
        prev_level: prev,
        used_percent: Number(usage?.usedPercent) || 0,
        used_tokens: Number(usage?.usedTokens) || 0,
        context_window: Number(usage?.contextWindowTokens) || 0,
      });
    },
    { immediate: true },
  );
  const activeActivityStats = computed(() => {
    if (typeof props.threadStore.getThreadActivityStats !== 'function') return {};
    return props.threadStore.getThreadActivityStats(selectedThreadId.value);
  });
  const activeAlerts = computed(() => {
    if (typeof props.threadStore.getThreadAlerts !== 'function') return [];
    return props.threadStore.getThreadAlerts(selectedThreadId.value);
  });
  const isStatusTimerModalPaused = computed(() => (
    Boolean(props.projectStore?.state?.showModal) || Boolean(showPathChoiceModal?.value)
  ));
  const statusSinceByThread = /** @type {{ value: Record<string, number> }} */ (ref({}));
  const statusPausedAtByThread = /** @type {{ value: Record<string, number> }} */ (ref({}));
  const statusTick = ref(Date.now());
  let statusTickTimer = 0;
  const activeStatusMeta = computed(() => {
    const threadId = selectedThreadId.value;
    if (!threadId) return '';
    const state = normalizeStatus(activeStatus.value);
    if (!isThreadActiveStatus(state)) return '';
    const since = Number(statusSinceByThread.value[threadId]) || Date.now();
    const elapsedSeconds = Math.max(0, Math.floor((statusTick.value - since) / 1000));
    const elapsed = formatElapsedCompact(elapsedSeconds);
    const hint = canInterrupt.value ? ' • Esc 可中断' : '';
    const detail = activeStatusDetails.value;
    if (detail) {
      return `(${elapsed}${hint}) · ${detail}`;
    }
    return `(${elapsed}${hint})`;
  });

  function ensureStatusTickTimer() {
    if (statusTickTimer) return;
    if (typeof window === 'undefined' || typeof window.setInterval !== 'function') return;
    statusTickTimer = window.setInterval(() => {
      statusTick.value = Date.now();
    }, 1000);
  }

  function stopStatusTickTimer() {
    if (!statusTickTimer) return;
    if (typeof window !== 'undefined' && typeof window.clearInterval === 'function') {
      window.clearInterval(statusTickTimer);
    }
    statusTickTimer = 0;
  }

  watch(
    () => [
      selectedThreadId.value,
      activeStatus.value,
      canInterrupt.value,
      isStatusTimerModalPaused.value,
    ],
    ([threadId, status, interruptible, modalPaused]) => {
      const now = Date.now();
      statusTick.value = now;
      if (!threadId) {
        stopStatusTickTimer();
        return;
      }
      const state = normalizeStatus(status);
      if (!isThreadActiveStatus(state)) {
        statusSinceByThread.value[threadId] = 0;
        statusPausedAtByThread.value[threadId] = 0;
        stopStatusTickTimer();
        return;
      }
      if (!statusSinceByThread.value[threadId]) {
        statusSinceByThread.value[threadId] = now;
      }
      const shouldTick = Boolean(interruptible) && !modalPaused;
      const pausedAt = Number(statusPausedAtByThread.value[threadId]) || 0;
      if (shouldTick) {
        if (pausedAt > 0) {
          const since = Number(statusSinceByThread.value[threadId]) || now;
          statusSinceByThread.value[threadId] = since + Math.max(0, now - pausedAt);
          statusPausedAtByThread.value[threadId] = 0;
        }
        ensureStatusTickTimer();
        return;
      }
      if (!pausedAt) {
        statusPausedAtByThread.value[threadId] = now;
      }
      stopStatusTickTimer();
    },
    { immediate: true },
  );

  watch(
    () => ({
      threadId: (selectedThreadId.value || '').toString().trim(),
      status: normalizeStatus(activeStatus.value),
      header: (activeStatusHeader.value || '').toString().trim(),
      details: (activeStatusDetails.value || '').toString().trim(),
      display: (displayStatusText.value || '').toString().trim(),
      interruptible: Boolean(canInterrupt.value),
    }),
    (next, prev) => {
      if (!next.threadId) return;
      if (prev
        && next.threadId === prev.threadId
        && next.status === prev.status
        && next.header === prev.header
        && next.details === prev.details
        && next.display === prev.display
        && next.interruptible === prev.interruptible) {
        return;
      }
      logInfo('ui', 'chat.display_status.resolved', {
        thread_id: next.threadId,
        status: next.status,
        status_header: next.header,
        status_details: next.details,
        display_text: next.display,
        interruptible: next.interruptible,
      });
    },
    { immediate: true, deep: false },
  );

  return {
    isThreadInterruptible,
    getThreadStatusHeader,
    activeStatusHeader,
    activeStatusDetails,
    activeTokenUsage,
    canInterrupt,
    canCompact,
    compacting,
    activeCompactResult,
    compactResultText,
    compactResultTone,
    compactSuccessCount,
    displayStatusText,
    activeTokenInline,
    activeTokenTooltip,
    activeTokenLevel,
    activeActivityStats,
    activeAlerts,
    activeStatusMeta,
    isStatusTimerModalPaused,
    ensureStatusTickTimer,
    stopStatusTickTimer,
  };
}
