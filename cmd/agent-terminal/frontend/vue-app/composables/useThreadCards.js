import {
  computed,
} from '../../lib/vue.esm-browser.prod.js';
import { normalizeStatus } from '../services/status.js';
import {
  formatTimelineTime,
  normalizeActivityOutput,
  summarizeToolActivity,
  toolActivityDetail,
} from '../utils/format-utils.js';

import { buildVisibleChatThreadCards } from '../utils/thread-page-utils.js';
import { getThreadRouting, getThreadPendingLaunch } from '../stores/thread-actions-helpers.js';
import { createPinnedPlanState } from './useThreadCards.pinned-plan.js';

/**
 * @typedef {import('../utils/thread-page-types').ProcessActivityItem} ProcessActivityItem
 */

const CARD_RENDER_WARN_MS = 16;
const CARD_RENDER_DEBUG_MS = 4;
const CARD_RENDER_SAMPLE_LIMIT = 8;

function roundPerfMs(value) {
  return Math.round(Number(value || 0) * 100) / 100;
}

function createCardPerfCollector() {
  const marks = [];
  return {
    marks,
    mark(stage, durationMs, fields = {}) {
      marks.push({
        stage,
        duration_ms: roundPerfMs(durationMs),
        ...fields,
      });
    },
  };
}

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
  const rawTool = (item.tool || item.toolName || item.name || '').toString().trim();
  const tool = summarizeToolActivity(rawTool, item);
  const detail = tool.status === 'active'
    ? normalizeActivityOutput(toolActivityDetail(item)).replace(/\n/g, ' ')
    : '';
  const message = detail ? `${tool.name} · ${tool.summary} · ${detail}` : `${tool.name} · ${tool.summary}`;
  return {
    id: processActivityId(item, 'tool', index),
    time: formatTimelineTime(item.ts),
    message,
    kind: 'tool',
    status: tool.status,
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
    status: approvalActivityStatus(status, failed),
    multiline: Boolean(command),
  };
}

function approvalActivityStatus(status, failed) {
  if (failed) return 'failed';
  if (['approved', 'rejected', 'resolved', 'submitted'].includes(status)) return 'done';
  return 'active';
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
    status: fileActivityStatus(failed, active),
  };
}

function fileActivityStatus(failed, active) {
  if (failed) return 'failed';
  if (active) return 'active';
  return 'done';
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
    // Skip command entries that are actually tool calls — the backend emits
    // both ItemStarted (kind:command) and ToolCallBegin (kind:tool) for the
    // same MCP call; the kind:tool branch below handles them correctly.
    if ((item.tool || item.toolName || '').toString().trim()) return null;
    // Skip ghost command entries with no command text — these render as
    // "终端命令" with zero useful information for the user.
    if (!(item.command || '').toString().trim()) return null;
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
    const buildStart = performance.now();
    const perf = createCardPerfCollector();
    const raw = buildVisibleChatThreadCards({
      threads: chatThreadOptions.value,
      selectedThreadId: selectedThreadId.value,
      pinnedMap: props.threadStore.state.pinnedThreadAtById,
      archivedMap: props.threadStore.state.archivedThreadAtById,
      runtimeById: props.threadStore.state.agentRuntimeById,
      showArchived: showArchivedThreadList.value,
      displayNameOf: (thread) => props.threadStore.displayName(thread),
      routingOf: (threadId) => getThreadRouting(threadId),
      pendingLaunchOf: (threadId) => getThreadPendingLaunch(threadId),
      statusOf: (threadId) => normalizeStatus(props.threadStore.getThreadStatus(threadId)),
      statusHeaderOf: (threadId) => getThreadStatusHeader(threadId),
      interruptibleOf: (threadId) => isThreadInterruptible(threadId),
      perf,
    });
    const buildDuration = performance.now() - buildStart;

    let recycleCount = 0;
    let newCardCount = 0;
    let changedCardCount = 0;
    let fieldCompareCount = 0;
    const changedSamples = [];
    const recycleStart = performance.now();
    const cacheSizeBefore = threadCardCache.size;
    const recycledCards = raw.cards.map((card) => {
      const cached = threadCardCache.get(card.id);
      if (cached) {
        let isSame = true;
        let changedKey = '';
        for (const key in card) {
          fieldCompareCount += 1;
          if (card[key] !== cached[key]) {
            isSame = false;
            changedKey = key;
            break;
          }
        }
        if (isSame) {
          recycleCount += 1;
          return cached;
        }
        changedCardCount += 1;
        if (changedSamples.length < CARD_RENDER_SAMPLE_LIMIT) {
          changedSamples.push({ id: card.id, key: changedKey || 'unknown' });
        }
      } else {
        newCardCount += 1;
        if (changedSamples.length < CARD_RENDER_SAMPLE_LIMIT) {
          changedSamples.push({ id: card.id, key: 'new' });
        }
      }
      threadCardCache.set(card.id, card);
      return card;
    });
    const recycleDuration = performance.now() - recycleStart;

    const elapsed = performance.now() - start;
    if (elapsed > CARD_RENDER_DEBUG_MS) {
      import('../services/log.js').then((m) => {
        const log = elapsed > CARD_RENDER_WARN_MS ? m.logWarn : m.logDebug;
        log('ui', 'chat.render.cards.perf', {
          duration_ms: roundPerfMs(elapsed),
          warn_threshold_ms: CARD_RENDER_WARN_MS,
          build_duration_ms: roundPerfMs(buildDuration),
          recycle_duration_ms: roundPerfMs(recycleDuration),
          phase_marks: perf.marks,
          source_threads: Array.isArray(chatThreadOptions.value) ? chatThreadOptions.value.length : 0,
          total_cards: raw.cards.length,
          active_count: raw.activeCount,
          archived_count: raw.archivedCount,
          recycled_cards: recycleCount,
          new_cards: newCardCount,
          changed_cards: changedCardCount,
          field_compares: fieldCompareCount,
          cache_size_before: cacheSizeBefore,
          cache_size_after: threadCardCache.size,
          changed_samples: changedSamples,
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
    const seenIds = new Set();
    return threads.value.map((thread) => {
      const threadId = (thread?.id || '').toString();
      if (seenIds.has(threadId)) {
        import('../services/log.js').then((m) => m.logWarn('ui', 'chat.cards.duplicate_thread', { thread_id: threadId, thread_name: thread?.name }));
      }
      seenIds.add(threadId);

      const selected = threadId === selId;
      const runtime = props.threadStore.state.agentRuntimeById?.[threadId];
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
