import {
  ref,
  computed,
} from '../../lib/vue.esm-browser.prod.js';
import { normalizeStatus } from '../services/status.js';
import {
  formatTimelineTime,
  normalizeActivityOutput,
} from '../utils/format-utils.js';
import { resolvePlanItemKey } from '../utils/plan-utils.js';
import { buildVisibleChatThreadCards } from '../utils/thread-page-utils.js';

/**
 * @typedef {import('../utils/thread-page-types').ProcessActivityItem} ProcessActivityItem
 */


/**
 * @param {object} props
 * @param {object} deps
 */
export function useThreadCards(props, deps) {
  const {
    threads,
    chatThreadOptions,
    selectedThreadId,

    showArchivedThreadList,
    activeTimeline,
    isCmd,
    layoutMode,
    timelinePreview,
    diffPreview,
    getThreadStatusHeader,
    isThreadInterruptible,
  } = deps;

  const __threadCardCache = new Map();
  const visibleChatThreadCardState = computed(() => {
    const raw = buildVisibleChatThreadCards({
      threads: chatThreadOptions.value,
      selectedThreadId: selectedThreadId.value,

      pinnedMap: props.threadStore.state.pinnedThreadAtById,
      archivedMap: props.threadStore.state.archivedThreadAtById,
      runtimeById: props.threadStore.state.agentRuntimeById,
      showArchived: showArchivedThreadList.value,
      displayNameOf: (thread) => props.threadStore.displayName(thread),
      statusOf: (threadId) => normalizeStatus(props.threadStore.getThreadStatus(threadId)),
      statusHeaderOf: (threadId) => getThreadStatusHeader(threadId),
      interruptibleOf: (threadId) => isThreadInterruptible(threadId),
    });

    const recycledCards = raw.cards.map((card) => {
      const cached = __threadCardCache.get(card.id);
      if (cached) {
        let isSame = true;
        for (const key in card) {
          if (card[key] !== cached[key]) {
            isSame = false;
            break;
          }
        }
        if (isSame) return cached;
      }
      __threadCardCache.set(card.id, card);
      return card;
    });

    return {
      activeCount: raw.activeCount,
      archivedCount: raw.archivedCount,
      cards: recycledCards,
    };
  });
  const chatActiveThreadCards = computed(() => (
    showArchivedThreadList.value ? [] : visibleChatThreadCardState.value.cards
  ));
  const chatArchivedThreadCards = computed(() => (
    showArchivedThreadList.value ? visibleChatThreadCardState.value.cards : []
  ));
  const visibleChatThreadCards = computed(() => visibleChatThreadCardState.value.cards);
  const activeChatThreadCount = computed(() => visibleChatThreadCardState.value.activeCount);
  const archivedChatThreadCount = computed(() => visibleChatThreadCardState.value.archivedCount);

  /**
   * @param {any} item
   * @param {number} index
   * @returns {ProcessActivityItem | null}
   */
  function toProcessActivityItem(item, index) {
    if (!item || typeof item !== 'object') return null;
    const kind = (item.kind || '').toString().trim();
    if (!kind) return null;
    if (kind === 'thinking') {
      const done = Boolean(item.done);
      return {
        id: (item.id || `${kind}-${index}`).toString(),
        time: formatTimelineTime(item.ts),
        message: done ? '思考完成' : '思考中',
        kind: 'thinking',
        status: done ? 'done' : 'active',
      };
    }
    if (kind === 'command') {
      const status = (item.status || '').toString().trim().toLowerCase();
      const commandText = (item.command || '').toString().trim();
      const title = commandText ? `$ ${commandText}` : '终端命令';
      const output = normalizeActivityOutput(item.output);
      const rawExitCode = Number(item.exitCode);
      const hasExitCode = Number.isFinite(rawExitCode);
      const exitCode = hasExitCode ? Math.trunc(rawExitCode) : undefined;
      if (status === 'running') {
        return {
          id: (item.id || `${kind}-${index}`).toString(),
          time: formatTimelineTime(item.ts),
          message: title,
          kind: 'command',
          title,
          command: commandText,
          output,
          status: 'active',
          multiline: Boolean(commandText || output),
        };
      }
      if (status === 'failed') {
        return {
          id: (item.id || `${kind}-${index}`).toString(),
          time: formatTimelineTime(item.ts),
          message: title,
          kind: 'command',
          title,
          command: commandText,
          output,
          status: 'failed',
          exitCode,
          multiline: Boolean(output),
        };
      }
      return {
        id: (item.id || `${kind}-${index}`).toString(),
        time: formatTimelineTime(item.ts),
        message: title,
        kind: 'command',
        title,
        command: commandText,
        output,
        status: 'done',
        exitCode,
        multiline: Boolean(output),
      };
    }
    return null;
  }

  const activeProcessActivity = computed(() => {
    const list = Array.isArray(activeTimeline.value) ? activeTimeline.value : [];
    /** @type {any[]} */
    const items = [];
    let lastSignature = '';
    for (let index = 0; index < list.length; index += 1) {
      const entry = toProcessActivityItem(list[index], index);
      if (!entry) continue;
      const signature = `${entry.message}|${entry.status}`;
      if (signature === lastSignature) continue;
      lastSignature = signature;
      items.push(/** @type {any} */ (entry));
    }
    return items.slice(-12).reverse();
  });

  const activeRuntime = computed(() => {
    const map = props.threadStore.state.agentRuntimeById || {};
    return map[selectedThreadId.value] || null;
  });
  const noActiveThread = computed(() => !selectedThreadId.value);
  const showOverview = computed(() => {
    if (isCmd.value) return false;
    return layoutMode.value === 'mix';
  });

  const latestPlanItem = computed(() => {
    if (isCmd.value) return null;
    const list = activeTimeline.value || [];
    for (let index = list.length - 1; index >= 0; index -= 1) {
      const item = list[index];
      if (item?.kind !== 'plan') continue;
      const text = (item.text || '').toString().trim();
      if (!text) continue;
      return item;
    }
    return null;
  });
  const DISMISS_STORAGE_KEY = '__plan_dismissed_v2__';
  function loadDismissed() {
    try {
      const raw = sessionStorage.getItem(DISMISS_STORAGE_KEY);
      return raw ? JSON.parse(raw) : {};
    } catch { return {}; }
  }
  function saveDismissed(map) {
    try { sessionStorage.setItem(DISMISS_STORAGE_KEY, JSON.stringify(map)); } catch {}
  }
  const dismissedPlanKeyByThread = ref(loadDismissed());
  const activePinnedPlan = computed(() => {
    const threadId = (selectedThreadId.value || '').toString().trim();
    if (!threadId) return null;
    const item = latestPlanItem.value;
    if (!item) return null;
    const key = resolvePlanItemKey(item);
    if (!key) return null;
    if (dismissedPlanKeyByThread.value?.[threadId] === key) return null;
    const text = (item.text || '').toString().trim();
    if (!text) return null;
    return {
      id: ((item?.id ?? '') || key).toString(),
      key,
      threadId,
      done: Boolean(item.done),
      statusText: item.done ? '完成' : '进行中',
      text,
    };
  });
  function dismissPinnedPlan() {
    const plan = activePinnedPlan.value;
    if (!plan) return;
    const next = {
      ...dismissedPlanKeyByThread.value,
      [plan.threadId]: plan.key,
    };
    dismissedPlanKeyByThread.value = next;
    saveDismissed(next);
  }

  let _lastStatsKey = '';
  let _lastStats = { total: 0, running: 0, thinking: 0, editing: 0, error: 0 };
  const stats = computed(() => {
    const ids = threads.value.map((t) => t.id);
    const key = ids.map((id) => `${id}:${normalizeStatus(props.threadStore.getThreadStatus(id))}`).join(',');
    if (key === _lastStatsKey) return _lastStats;
    _lastStatsKey = key;
    const summary = { total: ids.length, running: 0, thinking: 0, editing: 0, error: 0 };
    for (const id of ids) {
      const status = normalizeStatus(props.threadStore.getThreadStatus(id));
      if (status === 'running') summary.running += 1;
      if (status === 'thinking' || status === 'responding' || status === 'waiting') summary.thinking += 1;
      if (status === 'editing') summary.editing += 1;
      if (status === 'error') summary.error += 1;
    }
    _lastStats = summary;
    return summary;
  });

  const recentThreads = computed(() => {
    const meta = props.threadStore.state.agentMetaById || {};
    return [...threads.value]
      .sort((a, b) => {
        const aTs = Date.parse(meta[a.id]?.lastActiveAt || '') || 0;
        const bTs = Date.parse(meta[b.id]?.lastActiveAt || '') || 0;
        return bTs - aTs;
      })
      .slice(0, 6);
  });

  const cmdCards = computed(() => {
    if (!isCmd.value) return [];
    const selId = selectedThreadId.value;
    const layout = layoutMode.value;
    // 显式触发响应式追踪，确保选中卡片的 timeline 流式更新能使 cmdCards 失效重算。
    // eslint-disable-next-line no-unused-expressions
    activeTimeline.value;
    return threads.value.map((thread) => {
      const selected = thread.id === selId;
      const runtime = props.threadStore.state.agentRuntimeById?.[thread.id];
      const cwdMismatch = Boolean(runtime?.cwdMismatch);
      const card = {
        id: thread.id,
        name: props.threadStore.displayName(thread),
        status: props.threadStore.getThreadStatus(thread.id),
        statusHeader: getThreadStatusHeader(thread.id) || '等待指示',
        interruptible: isThreadInterruptible(thread.id),
        selected,
        preview: [],
        diff: '',
        cwdMismatch,
        cwdMismatchReason: cwdMismatch ? ((runtime?.cwdMismatchReason || '').toString()) : '',
        provider: (runtime?.provider || '').toString().trim(),
      };
      if (selected) {
        if (layout !== 'overview') card.preview = timelinePreview(thread.id);
        if (layout === 'mix') card.diff = diffPreview(thread.id);
      }
      return card;
    });
  });

  return {
    chatActiveThreadCards,
    chatArchivedThreadCards,
    visibleChatThreadCards,
    activeChatThreadCount,
    archivedChatThreadCount,
    activeProcessActivity,
    activeRuntime,
    noActiveThread,
    showOverview,
    activePinnedPlan,
    dismissPinnedPlan,
    stats,
    recentThreads,
    cmdCards,
  };
}
