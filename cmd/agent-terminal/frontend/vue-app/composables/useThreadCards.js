import {
  computed,
} from '../../lib/vue.esm-browser.prod.js';
import { normalizeStatus } from '../services/status.js';
import {
  formatTimelineTime,
  normalizeActivityOutput,
} from '../utils/format-utils.js';
import { buildVisibleChatThreadCards } from '../utils/thread-page-utils.js';
import { createPinnedPlanState } from './useThreadCards.pinned-plan.js';

/**
 * @typedef {import('../utils/thread-page-types').ProcessActivityItem} ProcessActivityItem
 */

function processActivityId(item, kind, index) {
  return (item.id || `${kind}-${index}`).toString();
}

function isFailedActivity(item) {
  const status = (item?.status || '').toString().trim().toLowerCase();
  if (status === 'failed' || status === 'error' || status === 'rejected') return true;
  if (item?.success === false) return true;
  if (Number.isFinite(Number(item?.exitCode)) && Math.trunc(Number(item.exitCode)) !== 0) return true;
  return Boolean((item?.error || '').toString().trim());
}

function toToolProcessActivityItem(item, index) {
  const status = (item.status || '').toString().trim().toLowerCase();
  const failed = isFailedActivity(item);
  const tool = (item.tool || '').toString().trim() || '未知工具';
  const detail = (item.preview || item.file || '').toString().trim();
  return {
    id: processActivityId(item, 'tool', index),
    time: formatTimelineTime(item.ts),
    message: detail ? `${tool} · ${detail}` : tool,
    kind: 'tool',
    status: failed ? 'failed' : status === 'running' ? 'active' : 'done',
  };
}

function toApprovalProcessActivityItem(item, index) {
  const status = (item.status || '').toString().trim().toLowerCase();
  const failed = isFailedActivity(item);
  const command = (item.command || item.tool || item.file || item.text || '').toString().trim();
  return {
    id: processActivityId(item, 'approval', index),
    time: formatTimelineTime(item.ts),
    message: command ? `审批确认 · ${command}` : '等待审批确认',
    kind: 'approval',
    status: failed ? 'failed' : ['approved', 'rejected', 'resolved', 'submitted'].includes(status) ? 'done' : 'active',
    multiline: Boolean(command),
  };
}

function toFileProcessActivityItem(item, index) {
  const status = (item.status || '').toString().trim().toLowerCase();
  const failed = isFailedActivity(item);
  const file = (item.file || '').toString().trim() || '未知文件';
  const active = status === 'editing' || status === 'running';
  let prefix = '已修改';
  if (failed) prefix = '修改失败';
  else if (active) prefix = '修改中';
  else if (status === 'saved') prefix = '已保存';
  return {
    id: processActivityId(item, 'file', index),
    time: formatTimelineTime(item.ts),
    message: `${prefix} · ${file}`,
    kind: 'file',
    status: failed ? 'failed' : active ? 'active' : 'done',
  };
}

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
    const failed = isFailedActivity(item);
    const commandText = (item.command || '').toString().trim();
    const title = commandText ? `$ ${commandText}` : '终端命令';
    const output = normalizeActivityOutput(item.output);
    const rawExitCode = Number(item.exitCode);
    const hasExitCode = Number.isFinite(rawExitCode);
    const exitCode = hasExitCode ? Math.trunc(rawExitCode) : undefined;
    if (status === 'running' && !failed) {
      return {
        id: processActivityId(item, kind, index),
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
    if (failed) {
      return {
        id: processActivityId(item, kind, index),
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
      id: processActivityId(item, kind, index),
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
  if (kind === 'tool') return toToolProcessActivityItem(item, index);
  if (kind === 'approval') return toApprovalProcessActivityItem(item, index);
  if (kind === 'file') return toFileProcessActivityItem(item, index);
  return null;
}

function createVisibleChatThreadCardState(props, deps) {
  const {
    chatThreadOptions,
    selectedThreadId,
    showArchivedThreadList,
    getThreadStatusHeader,
    isThreadInterruptible,
  } = deps;
  const threadCardCache = new Map();
  const visibleChatThreadCardState = computed(() => {
    const start = performance.now();
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

    let recycleCount = 0;
    const recycledCards = raw.cards.map((card) => {
      const cached = threadCardCache.get(card.id);
      if (cached) {
        let isSame = true;
        for (const key in card) {
          if (card[key] !== cached[key]) {
            isSame = false;
            break;
          }
        }
        if (isSame) {
          recycleCount += 1;
          return cached;
        }
      }
      threadCardCache.set(card.id, card);
      return card;
    });

    const elapsed = performance.now() - start;
    if (elapsed > 1.5 || raw.cards.length > 0) {
      import('../services/log.js').then((m) => {
        m.logWarn('ui', 'chat.render.cards.perf', {
          duration_ms: Math.round(elapsed * 100) / 100,
          total_cards: raw.cards.length,
          recycled_cards: recycleCount,
        });
      });
    }
    return { activeCount: raw.activeCount, archivedCount: raw.archivedCount, cards: recycledCards };
  });

  return {
    chatActiveThreadCards: computed(() => (showArchivedThreadList.value ? [] : visibleChatThreadCardState.value.cards)),
    chatArchivedThreadCards: computed(() => (showArchivedThreadList.value ? visibleChatThreadCardState.value.cards : [])),
    visibleChatThreadCards: computed(() => visibleChatThreadCardState.value.cards),
    activeChatThreadCount: computed(() => visibleChatThreadCardState.value.activeCount),
    archivedChatThreadCount: computed(() => visibleChatThreadCardState.value.archivedCount),
  };
}

function createActiveProcessActivity(activeTimeline) {
  return computed(() => {
    const list = Array.isArray(activeTimeline.value) ? activeTimeline.value : [];
    let lastSignature = '';
    const items = list.flatMap((rawItem, index) => {
      const entry = toProcessActivityItem(rawItem, index);
      if (!entry) return [];
      const signature = `${entry.message}|${entry.status}`;
      if (signature === lastSignature) return [];
      lastSignature = signature;
      return [entry];
    });
    return /** @type {ProcessActivityItem[]} */ (items.slice(-12).reverse());
  });
}

function createStatsState(threads, props) {
  let lastStatsKey = '';
  let lastStats = { total: 0, running: 0, thinking: 0, editing: 0, error: 0 };
  return computed(() => {
    const ids = threads.value.map((t) => t.id);
    const key = ids.map((id) => `${id}:${normalizeStatus(props.threadStore.getThreadStatus(id))}`).join(',');
    if (key === lastStatsKey) return lastStats;
    lastStatsKey = key;
    const summary = { total: ids.length, running: 0, thinking: 0, editing: 0, error: 0 };
    for (const id of ids) {
      const status = normalizeStatus(props.threadStore.getThreadStatus(id));
      if (status === 'running') summary.running += 1;
      if (status === 'thinking' || status === 'responding' || status === 'waiting') summary.thinking += 1;
      if (status === 'editing') summary.editing += 1;
      if (status === 'error') summary.error += 1;
    }
    lastStats = summary;
    return summary;
  });
}

function createRecentThreads(threads, props) {
  return computed(() => {
    const meta = props.threadStore.state.agentMetaById || {};
    return [...threads.value]
      .sort((a, b) => {
        const aTs = Date.parse(meta[a.id]?.lastActiveAt || '') || 0;
        const bTs = Date.parse(meta[b.id]?.lastActiveAt || '') || 0;
        return bTs - aTs;
      })
      .slice(0, 6);
  });
}

function createCmdCards(props, deps) {
  const {
    threads,
    selectedThreadId,
    isCmd,
    layoutMode,
    activeTimeline,
    timelinePreview,
    diffPreview,
    getThreadStatusHeader,
    isThreadInterruptible,
  } = deps;
  return computed(() => {
    if (!isCmd.value) return [];
    const selId = selectedThreadId.value;
    const layout = layoutMode.value;
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
}

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
  const {
    chatActiveThreadCards,
    chatArchivedThreadCards,
    visibleChatThreadCards,
    activeChatThreadCount,
    archivedChatThreadCount,
  } = createVisibleChatThreadCardState(props, {
    chatThreadOptions,
    selectedThreadId,
    showArchivedThreadList,
    getThreadStatusHeader,
    isThreadInterruptible,
  });
  const activeProcessActivity = createActiveProcessActivity(activeTimeline);
  const activeRuntime = computed(() => {
    const map = props.threadStore.state.agentRuntimeById || {};
    return map[selectedThreadId.value] || null;
  });
  const noActiveThread = computed(() => !selectedThreadId.value);
  const showOverview = computed(() => {
    if (isCmd.value) return false;
    return layoutMode.value === 'mix';
  });
  const { activePinnedPlan, dismissPinnedPlan } = createPinnedPlanState(isCmd, selectedThreadId, activeTimeline);
  const stats = createStatsState(threads, props);
  const recentThreads = createRecentThreads(threads, props);
  const cmdCards = createCmdCards(props, {
    threads,
    selectedThreadId,
    isCmd,
    layoutMode,
    activeTimeline,
    timelinePreview,
    diffPreview,
    getThreadStatusHeader,
    isThreadInterruptible,
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
