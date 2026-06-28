// @ts-nocheck
import {
  collectBridgeEventItemKinds,
  getBridgeEventCommand,
  getBridgeEventMethod,
  getBridgeEventThreadId,
  getBridgeEventType,
  isCompactCommand,
  isContextCompactionItemKind,
  normalizeThreadID,
  toNormalizedEventString,
} from './bridge-event-parser.js';
import { applyImmediateTimelineFromMessages } from './thread-history-ui.js';
import { applyRuntimeThreadPatch, shouldSkipThreadSyncFromPatch, THREAD_PATCH_METHOD } from './thread-live-patch.js';
import { getLoadedDiffRevision, normalizeDiffRevision } from './thread-diff-sync.js';
import {
  getThreadActivityStats,
  getThreadAlerts,
  getThreadDiff,
  getThreadInterruptible,
  getThreadStatus,
  getThreadStatusDetails,
  getThreadStatusHeader,
  getThreadTimeline,
  getThreadTokenUsage,
  shouldReloadThreadHistory,
} from './thread-sync-selectors.js';

export function perfNow() {
  return typeof performance !== 'undefined' && typeof performance.now === 'function' ? performance.now() : Date.now();
}

function runtimeSnapshotScopeKey(threadId) {
  const id = normalizeThreadID(threadId);
  return id ? 'thread:' + id : 'global';
}

export function beginRuntimeSnapshotRequest(ctx, threadId) {
  const scope = runtimeSnapshotScopeKey(threadId);
  ctx.runtimeSnapshotRequestSeq += 1;
  ctx.latestRuntimeSnapshotRequestSeqByScope.set(scope, ctx.runtimeSnapshotRequestSeq);
  return { seq: ctx.runtimeSnapshotRequestSeq, scope, threadId: normalizeThreadID(threadId) };
}

export function isLatestRuntimeSnapshotRequest(ctx, meta) {
  if (!meta || typeof meta !== 'object') return true;
  return ctx.latestRuntimeSnapshotRequestSeqByScope.get(meta.scope) === meta.seq;
}

export function loadedDiffRevision(ctx, threadId) {
  return getLoadedDiffRevision({
    threadId,
    diffTextByThread: ctx.state.diffTextByThread,
    loadedRevisionByThread: ctx.threadDiffLoadedRevisionByThread,
    normalizeThreadID,
  });
}
const DIRECT_THREAD_SYNC_METHODS = new Set([
  'item/agentmessage/delta',
  'item/reasoning/textdelta',
  'item/reasoning/summarytextdelta',
  'item/reasoning/summarypartadded',
  'item/commandexecution/outputdelta',
  'item/filechange/outputdelta',
  'item/plan/delta',
  'turn/output/delta',
]);

function isDirectThreadSyncSignal(methodLower, sourceLower) {
  if (DIRECT_THREAD_SYNC_METHODS.has(methodLower)) return true;
  return (methodLower === 'ui/thread/changed' || methodLower === THREAD_PATCH_METHOD) && DIRECT_THREAD_SYNC_METHODS.has(sourceLower);
}

const REGRESSION_GUARD_THROTTLE_MS = 30_000;
const _regressionGuardLastWarnByThread = new Map();

function applyRegressionGuardToSnapshot(ctx, res, threadId) {
  if (!res || !res.timelinesByThread || !res.timelinesByThread[threadId]) return false;
  
  const timeline = res.timelinesByThread[threadId];
  const localTimeline = Array.isArray(ctx.state.timelinesByThread?.[threadId]) ? ctx.state.timelinesByThread[threadId] : [];
  
  // Dialog items are primarily sourced from thread/messages history, but they
  // still count as local context that a tiny runtime snapshot must not dilute.
  const localNonDialogLen = localTimeline.filter((it) => it?.kind !== 'user' && it?.kind !== 'assistant').length;
  const localComparableLen = Math.max(localTimeline.length, localNonDialogLen);

  const regressionByCount = localComparableLen > 5 && timeline.length > 0
    && timeline.length < localComparableLen * 0.3
    && (localComparableLen - timeline.length) > 10;

  if (regressionByCount) {
    let guardType = 'count_mismatch';
    
    // Throttle WARN to at most once per 30s per thread to avoid log flooding
    // during high-frequency streaming events. The guard behavior (discarding
    // the regressed snapshot) still applies on every hit.
    const now = Date.now();
    const lastWarn = _regressionGuardLastWarnByThread.get(threadId) || 0;
    if (now - lastWarn >= REGRESSION_GUARD_THROTTLE_MS) {
      _regressionGuardLastWarnByThread.set(threadId, now);
      ctx.logWarn('thread', 'sync.thread_state_regression_guard', {
        thread_id: threadId,
        remote_len: timeline.length,
        local_non_dialog_len: localNonDialogLen,
        guard_type: guardType,
        remote_kinds: timeline.slice(0, 5).map((it) => it?.kind),
      });
    }
    
    delete res.timelinesByThread[threadId];
    return guardType;
  }
  return null;
}

export async function syncRuntimeState(ctx) {
  const { callAPI, logInfo, logWarn } = ctx;
  if (ctx.runtimeSyncPromise) {
    ctx.runtimeSyncPending = true;
    logInfo('thread', 'state.sync.join_existing', {
      active_thread_id: (ctx.state.activeThreadId || '').toString().trim(),
      active_cmd_thread_id: (ctx.state.activeCmdThreadId || '').toString().trim(),
      cwd: ctx.getPreferenceScopeCwd(),
    });
    await ctx.runtimeSyncPromise;
    if (ctx.runtimeSyncPromise) return ctx.runtimeSyncPromise;
    return;
  }
  ctx.runtimeSyncPromise = (async () => {
    try {
      const activeThreadId = (ctx.state.activeThreadId || '').toString().trim();
      const activeCmdThreadId = (ctx.state.activeCmdThreadId || '').toString().trim();
      const snapshotRequest = beginRuntimeSnapshotRequest(ctx, activeThreadId);
      logInfo('thread', 'state.sync.request', {
        active_thread_id: activeThreadId,
        active_cmd_thread_id: activeCmdThreadId,
        cwd: ctx.getPreferenceScopeCwd(),
        loaded_diff_revision: loadedDiffRevision(ctx, activeThreadId),
      });
      const res = await callAPI('ui/state/get', ctx.withPreferenceScope({ threadId: activeThreadId, includeDiff: false }));
      applyRegressionGuardToSnapshot(ctx, res, activeThreadId);

      const timelines = res && typeof res.timelinesByThread === 'object' && res.timelinesByThread ? res.timelinesByThread : {};
      logInfo('thread', 'state.sync.response', {
        active_thread_id: activeThreadId,
        active_cmd_thread_id: activeCmdThreadId,
        timeline_threads: Object.keys(timelines).length,
        diff_revision: normalizeDiffRevision(res?.diffRevisionByThread?.[activeThreadId]),
      });
      if (!isLatestRuntimeSnapshotRequest(ctx, snapshotRequest)) {
        logInfo('thread', 'state.sync.stale.skipped', { active_thread_id: activeThreadId, scope: snapshotRequest.scope, request_seq: snapshotRequest.seq });
        return res || {};
      }
      if (typeof ctx.saveScrollPosition === 'function') ctx.saveScrollPosition();
      ctx.applyRuntimeSnapshot(ctx.state, res || {}, {
        requestedThreadId: activeThreadId,
        allowActiveSelectionPatch: true,
        loadedRevisionByThread: ctx.threadDiffLoadedRevisionByThread,
      });
      if (typeof ctx.restoreScrollPosition === 'function') ctx.restoreScrollPosition();
      if (activeThreadId && normalizeDiffRevision(ctx.state.diffRevisionByThread?.[activeThreadId]) !== loadedDiffRevision(ctx, activeThreadId)) {
        void ctx.syncThreadDiffState(activeThreadId).catch((error) => {
          logWarn('thread', 'state.sync.diff.background_failed', { thread_id: activeThreadId, reason: 'runtime_sync', error });
        });
      }
      logInfo('thread', 'state.sync.applied', {
        active_thread_id: (ctx.state.activeThreadId || '').toString().trim(),
        active_cmd_thread_id: (ctx.state.activeCmdThreadId || '').toString().trim(),
        diff_revision: normalizeDiffRevision(ctx.state.diffRevisionByThread?.[activeThreadId]),
      });
      return res || {};
    } finally {
      ctx.runtimeSyncPromise = null;
      if (ctx.runtimeSyncPending) {
        ctx.runtimeSyncPending = false;
        logInfo('thread', 'state.sync.replay_pending', {
          active_thread_id: (ctx.state.activeThreadId || '').toString().trim(),
          active_cmd_thread_id: (ctx.state.activeCmdThreadId || '').toString().trim(),
          cwd: ctx.getPreferenceScopeCwd(),
        });
        await syncRuntimeState(ctx).catch(() => {});
      }
    }
  })();
  return ctx.runtimeSyncPromise;
}

export async function syncThreadState(ctx, threadId, options = {}) {
  const { callAPI, logInfo, logWarn } = ctx;
  const id = normalizeThreadID(threadId);
  if (!id) return null;
  const inFlight = ctx.threadStateSyncPromiseByThread.get(id);
  if (inFlight) {
    ctx.threadStateSyncPendingByThread.set(id, true);
    logInfo('thread', 'state.sync.thread.join_existing', { thread_id: id, cwd: ctx.getPreferenceScopeCwd() });
    return inFlight;
  }
  logInfo('thread', 'state.sync.thread.request', { thread_id: id, cwd: ctx.getPreferenceScopeCwd() });
  const request = (async () => {
    const start = perfNow();
    const snapshotRequest = beginRuntimeSnapshotRequest(ctx, id);
    try {
      const res = await callAPI('ui/state/get', ctx.withPreferenceScope({ threadId: id, includeDiff: false }));
      applyRegressionGuardToSnapshot(ctx, res, id);
      const timeline = Array.isArray(res?.timelinesByThread?.[id]) ? res.timelinesByThread[id] : [];
      const diffRevision = normalizeDiffRevision(res?.diffRevisionByThread?.[id]);
      const localTimelineLen = Array.isArray(ctx.state.timelinesByThread?.[id]) ? ctx.state.timelinesByThread[id].length : 0;
      logInfo('thread', 'state.sync.thread.response', { thread_id: id, timeline_len: timeline.length, diff_revision: diffRevision, local_timeline_len: localTimelineLen, duration_ms: Math.round(perfNow() - start) });

      if (!isLatestRuntimeSnapshotRequest(ctx, snapshotRequest)) {
        logInfo('thread', 'state.sync.thread.stale.skipped', { thread_id: id, scope: snapshotRequest.scope, request_seq: snapshotRequest.seq, duration_ms: Math.round(perfNow() - start) });
        return res || {};
      }
      if (typeof ctx.saveScrollPosition === 'function') ctx.saveScrollPosition();
      ctx.applyRuntimeSnapshot(ctx.state, res || {}, {
        requestedThreadId: id,
        allowActiveSelectionPatch: false,
        loadedRevisionByThread: ctx.threadDiffLoadedRevisionByThread,
      });
      if (typeof ctx.restoreScrollPosition === 'function') ctx.restoreScrollPosition();
      const activeThreadId = normalizeThreadID(ctx.state.activeThreadId);
      const activeCmdThreadId = normalizeThreadID(ctx.state.activeCmdThreadId);
      if ((id === activeThreadId || id === activeCmdThreadId) && shouldReloadThreadHistory(ctx, id)) {
        logInfo('thread', 'history.reload.after_sync', { thread_id: id, sync_runtime: false });
        await loadMessages(ctx, id, 300, { syncRuntime: false });
      }
      if (id === normalizeThreadID(ctx.state.activeThreadId) && normalizeDiffRevision(ctx.state.diffRevisionByThread?.[id]) !== loadedDiffRevision(ctx, id)) {
        void ctx.syncThreadDiffState(id).catch((error) => {
          logWarn('thread', 'state.sync.diff.background_failed', { thread_id: id, reason: 'thread_sync', error });
        });
      }
      if (options?.markHistoryLoaded !== false && Array.isArray(res?.timelinesByThread?.[id]) && res.timelinesByThread[id].length > 0) {
        ctx.threadHistoryLoadedAtByThread.set(id, Date.now());
      }
      logInfo('thread', 'state.sync.thread.applied', { thread_id: id, diff_revision: normalizeDiffRevision(ctx.state.diffRevisionByThread?.[id]), duration_ms: Math.round(perfNow() - start) });
      return res || {};
    } catch (error) {
      logWarn('thread', 'state.sync.thread.failed', { thread_id: id, error, duration_ms: Math.round(perfNow() - start) });
      throw error;
    } finally {
      ctx.threadStateSyncPromiseByThread.delete(id);
      if (ctx.threadStateSyncPendingByThread.get(id)) {
        ctx.threadStateSyncPendingByThread.delete(id);
        void syncThreadState(ctx, id).catch(() => {});
      }
    }
  })();
  ctx.threadStateSyncPromiseByThread.set(id, request);
  return request;
}

export async function refreshSidebarState(ctx, options = {}) {
  const { callAPI, logDebug, logWarn } = ctx;
  if (ctx.sidebarRefreshPromise && !options.force) {
    ctx.sidebarRefreshPending = true;
    logDebug('thread', 'sidebar.refresh.pending_join', { ts: Date.now() });
    return ctx.sidebarRefreshPromise;
  }
  const start = perfNow();
  logDebug('thread', 'sidebar.refresh.start', { ts: Date.now(), force: options.force });
  
  ctx.sidebarRefreshPending = false;
  
  const currentPromise = (async () => {
    try {
      const snapshotRequest = beginRuntimeSnapshotRequest(ctx, (ctx.state.activeThreadId || '').toString().trim() || '__sidebar__');
      logDebug('thread', 'sidebar.refresh.api_call_start', { ts: Date.now() });
      const sidebar = await callAPI('ui/sidebar/get', ctx.withPreferenceScope({}));
      logDebug('thread', 'sidebar.refresh.api_call_done', { duration_ms: perfNow() - start, ts: Date.now() });
      if (!isLatestRuntimeSnapshotRequest(ctx, snapshotRequest)) return;
      if (typeof ctx.saveScrollPosition === 'function') ctx.saveScrollPosition();
      ctx.applyRuntimeSnapshot(ctx.state, sidebar || {}, {
        requestedThreadId: '',
        allowActiveSelectionPatch: true,
        loadedRevisionByThread: ctx.threadDiffLoadedRevisionByThread,
      });
      if (typeof ctx.restoreScrollPosition === 'function') ctx.restoreScrollPosition();
      logDebug('thread', 'sidebar.refreshed', { count: ctx.state.threads.length, active_chat: ctx.state.activeThreadId, active_cmd: ctx.state.activeCmdThreadId, duration_ms: Math.round(perfNow() - start) });
    } catch (error) {
      logWarn('thread', 'sidebar.refresh.failed', { error, duration_ms: Math.round(perfNow() - start) });
    } finally {
      const isLatest = (ctx.sidebarRefreshPromise === currentPromise);
      if (isLatest) ctx.sidebarRefreshPromise = null;
      if (isLatest && ctx.sidebarRefreshPending) {
        ctx.sidebarRefreshPending = false;
        await refreshSidebarState(ctx).catch((error) => {
          logWarn('thread', 'sidebar.refresh.replay_failed', { error });
        });
      }
    }
  })();
  ctx.sidebarRefreshPromise = currentPromise;
  return currentPromise;
}

export async function loadMessages(ctx, threadId, limit = 300, options = {}) {
  const { callAPI, logDebug, logInfo, logWarn } = ctx;
  const id = normalizeThreadID(threadId);
  if (!id) return;
  const syncRuntime = options?.syncRuntime !== false;
  const inFlight = ctx.messageLoadPromiseByThread.get(id);
  if (inFlight) {
    logInfo('thread', 'messages.load.join_existing', { thread_id: id, limit, sync_runtime: syncRuntime });
    return inFlight;
  }
  logInfo('thread', 'messages.load.start', { thread_id: id, limit, sync_runtime: syncRuntime });
  const request = (async () => {
    const start = perfNow();
    try {
      const res = await callAPI('thread/messages', { threadId: id, limit });
      const immediateTimelineApplied = applyImmediateTimelineFromMessages({ threadId: id, response: res, state: ctx.state, normalizeThreadID, freezeTimelineItemsAtomic: ctx.freezeTimelineItemsAtomic, logDebug, logInfo, logWarn });
      const loadedAt = Date.now();
      if (immediateTimelineApplied) ctx.threadHistoryLoadedAtByThread.set(id, loadedAt);
      if (syncRuntime) {
        logInfo('thread', 'messages.load.sync.start', { thread_id: id, limit, immediate_timeline_applied: immediateTimelineApplied });
        await syncRuntimeState(ctx);
      }
      if (!immediateTimelineApplied) ctx.threadHistoryLoadedAtByThread.set(id, loadedAt);
      ctx.threadHistoryProviderThreadIDByThread.set(id, ctx.normalizeProviderThreadID(ctx.state.agentRuntimeById?.[id]?.providerThreadId || ctx.state.agentRuntimeById?.[id]?.provider_thread_id));
      logInfo('thread', 'messages.loaded', { thread_id: id, count: Array.isArray(res?.messages) ? res.messages.length : 0, duration_ms: Math.round(perfNow() - start) });
      return res;
    } catch (error) {
      logWarn('thread', 'messages.load.failed', { thread_id: id, error, duration_ms: Math.round(perfNow() - start) });
      throw error;
    } finally {
      if (ctx.messageLoadPromiseByThread.get(id) === request) ctx.messageLoadPromiseByThread.delete(id);
    }
  })();
  ctx.messageLoadPromiseByThread.set(id, request);
  return request;
}

async function syncThreadHistoryAtomic(ctx, threadId) {
  const id = normalizeThreadID(threadId);
  if (!id) return null;
  const loadedAtBefore = Number(ctx.threadHistoryLoadedAtByThread.get(id) || 0);
  if (typeof ctx.logDebug === 'function') {
    ctx.logDebug('thread', 'syncThreadHistoryAtomic.start', { thread_id: id, loaded_at_before: loadedAtBefore });
  }
  await syncThreadState(ctx, id, { markHistoryLoaded: false });
  const loadedAtAfter = Number(ctx.threadHistoryLoadedAtByThread.get(id) || 0);
  if (loadedAtAfter > loadedAtBefore) {
    if (typeof ctx.logDebug === 'function') {
      ctx.logDebug('thread', 'syncThreadHistoryAtomic.skipped', { thread_id: id, loaded_at_after: loadedAtAfter });
    }
    return null;
  }
  if (typeof ctx.logDebug === 'function') {
    ctx.logDebug('thread', 'syncThreadHistoryAtomic.loading_messages', { thread_id: id });
  }
  return loadMessages(ctx, id, 300, { syncRuntime: false });
}

function hasSubstantiveDeltaText(value) {
  return typeof value === 'string' ? value.length > 0 : Boolean(value);
}

function appendStreamingDeltaText(existingText, delta) {
  const base = (existingText || '').toString();
  const incoming = (delta || '').toString();
  if (!incoming) return base;
  if (!base) return incoming;
  if (base.endsWith(incoming)) return base;
  if (incoming.endsWith(base)) return incoming;
  const maxOverlap = Math.min(base.length, incoming.length);
  for (let overlap = maxOverlap; overlap > 0; overlap -= 1) {
    if (base.slice(-overlap) === incoming.slice(0, overlap)) {
      return base + incoming.slice(overlap);
    }
  }
  return base + incoming;
}

export function handleBridgeEvent(ctx, evt) {
  const { logInfo, logWarn } = ctx;
  const eventMethod = getBridgeEventMethod(evt);
  const eventBridgeType = getBridgeEventType(evt);
  const methodLower = toNormalizedEventString(eventMethod);
  const typeLower = toNormalizedEventString(eventBridgeType);
  const isSkillsChangedSignal = methodLower === 'skills/changed' || typeLower === 'skills/changed';
  const eventName = eventMethod || eventBridgeType || '';
  const sourceLower = toNormalizedEventString(evt?.source || evt?.params?.source || evt?.payload?.source || evt?.data?.source);
  let eventThreadId = getBridgeEventThreadId(evt);
  if (isSkillsChangedSignal) {
    ctx.state.skillRevision = Number(ctx.state.skillRevision || 0) + 1;
    logInfo('thread', 'skills.changed', { revision: ctx.state.skillRevision, skills_dir: (evt?.skillsDir || evt?.payload?.skillsDir || evt?.params?.skillsDir || '').toString() });
  }

  // 直接推送 token 数据到 store，无需 round-trip syncThreadState 调用。
  if (methodLower === 'thread/tokenusage/updated' && eventThreadId) {
    const payload = evt?.payload || evt?.params || evt?.data || evt || {};
    const tid = normalizeThreadID(eventThreadId);
    if (tid) {
      const input = Number(payload.input) || Number(payload.input_tokens) || Number(payload.inputTokens) || 0;
      const output = Number(payload.output) || Number(payload.output_tokens) || Number(payload.outputTokens) || 0;
      const totalTokens = Number(payload.total_tokens) || Number(payload.totalTokens) || (input + output);
      const contextWindow = Number(payload.context_window) || Number(payload.contextWindow) || Number(payload.contextWindowTokens) || 0;
      const prev = (ctx.state.tokenUsageByThread && ctx.state.tokenUsageByThread[tid]) || {};
      // 保留已有的 contextWindowTokens（由 system:init 设置），除非新值更大
      const resolvedWindow = contextWindow > 0 ? contextWindow : (Number(prev.contextWindowTokens) || 0);
      // 新值 > 0 时直接使用（允许修正），为 0 时保留旧值（如 system:init 初始化）
      const usedTokens = totalTokens > 0 ? totalTokens : (Number(prev.usedTokens) || 0);
      const usedPercent = resolvedWindow > 0 ? Math.min(100, Math.max(0, (usedTokens / resolvedWindow) * 100)) : 0;
      const next = Object.freeze({ usedTokens, contextWindowTokens: resolvedWindow, usedPercent, updatedAt: Date.now() });
      ctx.state.tokenUsageByThread = { ...(ctx.state.tokenUsageByThread || {}), [tid]: next };
      logInfo('thread', 'tokenUsage.push', { thread_id: tid, used: usedTokens, window: resolvedWindow, pct: Math.round(usedPercent) });
    }
  }

  const itemKinds = collectBridgeEventItemKinds(evt);
  const compactItemSignal = itemKinds.some((value) => isContextCompactionItemKind(value));
  const compactCommandSignal = isCompactCommand(getBridgeEventCommand(evt));
  const compactDoneSignal = methodLower === 'thread/compacted' || typeLower === 'context_compacted' || (methodLower === 'item/completed' && (compactItemSignal || compactCommandSignal));
  const compactStartSignal = methodLower === 'item/started' && (compactItemSignal || compactCommandSignal);
  if (!eventThreadId && (compactDoneSignal || compactStartSignal) && ctx.compactWaitersByThread.size === 1) {
    const onlyEntry = ctx.compactWaitersByThread.entries().next();
    if (!onlyEntry.done && Array.isArray(onlyEntry.value)) eventThreadId = normalizeThreadID(onlyEntry.value[0]);
  }

  const compactWaiter = ctx.getCompactWaiter(eventThreadId);
  if (compactWaiter && (compactStartSignal || compactCommandSignal)) {
    compactWaiter.compactCommandObserved = compactWaiter.compactCommandObserved || compactCommandSignal;
    compactWaiter.compactLifecycleStarted = compactWaiter.compactLifecycleStarted || compactStartSignal || compactItemSignal;
    logInfo('thread', 'compact.signal.progress', { thread_id: eventThreadId, method: eventMethod, type: eventBridgeType, compact_item_signal: compactItemSignal, compact_command_signal: compactCommandSignal, lifecycle_started: compactWaiter.compactLifecycleStarted });
  }

  const compactTurnDoneSignal = methodLower === 'turn/completed' && Boolean(compactWaiter?.compactLifecycleStarted);
  if (compactDoneSignal || compactTurnDoneSignal) {
    const settled = ctx.settleCompactWaiter(eventThreadId, 'resolve', { evt });
    if (!settled && eventThreadId) ctx.setCompactResult(eventThreadId, 'success', '上下文压缩完成');
    logInfo('thread', 'compact.signal.received', { thread_id: eventThreadId, method: eventMethod, type: eventBridgeType, compact_item_signal: compactItemSignal, compact_command_signal: compactCommandSignal, turn_completed_after_lifecycle: compactTurnDoneSignal, waiter_settled: settled });
  }

  const sidebarSyncSignal = methodLower === 'ui/sidebar/changed';
  const directThreadSyncSignal = isDirectThreadSyncSignal(methodLower, sourceLower);
  const turnCompletedSignal = methodLower === 'turn/completed';
  // turnTerminalSignal：所有让本地 streaming bubble 应该 finalize 的 method。
  // claudecli 在 stream EOF / 进程退出 / 中断时不一定发 turn/completed，
  // 但 turn/interrupted、thread/stopped、agent/stopped、agent/failed 任一会到。
  // 任一终结信号都让 <pre> 占位切到真 markdown 分支，避免回归 <pre> 卡住。
  const turnTerminalSignal = turnCompletedSignal
    || methodLower === 'turn/interrupted'
    || methodLower === 'agent/stopped'
    || methodLower === 'thread/stopped'
    || methodLower === 'agent/failed';
  const historyPageSignal = sourceLower === 'thread/messages/page';
  const threadSyncSignal = methodLower === 'ui/thread/changed' || directThreadSyncSignal || turnCompletedSignal || methodLower === 'item/completed';
  const eventThreadTarget = normalizeThreadID(eventThreadId);
  const activeThreadTarget = eventThreadTarget && (eventThreadTarget === normalizeThreadID(ctx.state.activeThreadId) || eventThreadTarget === normalizeThreadID(ctx.state.activeCmdThreadId)) ? eventThreadTarget : '';
  const historyHydrationSignal = turnCompletedSignal;

  if ((turnTerminalSignal && activeThreadTarget) || (turnCompletedSignal && activeThreadTarget)) {
    const existing = ctx.state.timelinesByThread?.[activeThreadTarget];
    if (Array.isArray(existing) && existing.length > 0) {
      let mutated = false;
      const next = existing.map((it) => {
        if (it?.kind === 'assistant' && it?.done === false && !it?.streamingFinalized) {
          mutated = true;
          return { ...it, streamingFinalized: true };
        }
        return it;
      });
      if (mutated) {
        ctx.state.timelinesByThread = { ...ctx.state.timelinesByThread, [activeThreadTarget]: next };
      }
    }
  }

  if (directThreadSyncSignal) {
    logInfo('thread', 'bridge.streaming_delta_received', {
      method: eventMethod, source: sourceLower, thread_id: eventThreadTarget,
      is_active: Boolean(activeThreadTarget),
    });
  }

  if (methodLower === 'item/agentmessage/delta' && activeThreadTarget) {
    const delta = evt?.payload?.delta;
    if (hasSubstantiveDeltaText(delta)) {
      const existing = ctx.state.timelinesByThread?.[activeThreadTarget] || [];
      const isStreamingItem = (it) => it?.kind === 'assistant' && it?.done === false && !it?.streamingFinalized;
      let lastIndex = -1;
      for (let i = existing.length - 1; i >= 0; i--) {
        if (isStreamingItem(existing[i])) { lastIndex = i; break; }
      }
      const nextTimelines = [...existing];
      if (lastIndex >= 0) {
        const prevItem = nextTimelines[lastIndex];
        nextTimelines[lastIndex] = {
          ...prevItem,
          text: appendStreamingDeltaText(prevItem?.text, delta),
        };
      } else {
        const ts = new Date().toISOString();
        const turnId = (evt?.payload?.turnId || evt?.payload?.turn_id || '').toString().trim();
        const streamingId = turnId
          ? `${activeThreadTarget}-${turnId}-streaming`
          : `${activeThreadTarget}-stream-${Date.now()}-streaming`;
        nextTimelines.push({ id: streamingId, kind: 'assistant', text: (delta || '').toString(), done: false, ts });
      }
      ctx.state.timelinesByThread = { ...ctx.state.timelinesByThread, [activeThreadTarget]: nextTimelines };
    }
  }
  if (methodLower === THREAD_PATCH_METHOD && eventThreadTarget) {
    const patchPayload = evt?.payload || evt?.params?.payload || evt?.params || evt?.data || evt || {};
    const hasTimelineItems = Array.isArray(patchPayload?.timelineItems) && patchPayload.timelineItems.length > 0;
    logInfo('thread', 'bridge.thread_patch_received', {
      thread_id: eventThreadTarget,
      source: (patchPayload?.source || '').toString(),
      sequence: patchPayload?.sequence,
      has_timeline_items: hasTimelineItems,
      is_active: Boolean(activeThreadTarget),
    });
    const patchResult = applyRuntimeThreadPatch(ctx, evt, eventThreadTarget, {
      perfNow,
      allowGlobalSelectionPatch: Boolean(activeThreadTarget),
    });
    if (patchResult?.handled) {
      if (patchResult.needsRecovery) {
        syncThreadState(ctx, eventThreadTarget).catch((error) => logWarn('thread', 'state.patch.recovery.failed', { error, by_event: eventName, reason: patchResult.reason || 'patch_gap' }));
      }
      return;
    }
  }
  if (activeThreadTarget && shouldSkipThreadSyncFromPatch(ctx, activeThreadTarget, methodLower, sourceLower, perfNow())) {
    if (!historyPageSignal) {
      logInfo('thread', 'bridge.event_skipped_by_patch_dedup', { method: eventMethod, source: sourceLower, thread_id: activeThreadTarget });
      return;
    }
  }

  if (sidebarSyncSignal) {
    const debounceMs = sourceLower === 'thread/started' ? 120 : 320;
    const now = typeof performance !== 'undefined' ? performance.now() : Date.now();
    if (now - ctx.sidebarSyncThrottleLastRun >= ctx.SIDEBAR_SYNC_THROTTLE_MS) {
      ctx.sidebarSyncThrottleLastRun = now;
      refreshSidebarState(ctx).catch((error) => logWarn('thread', 'sidebar.sync.failed', { error, by_event: eventName }));
    }
    clearTimeout(ctx.sidebarSyncDebounceTimer);
    ctx.sidebarSyncDebounceTimer = setTimeout(() => {
      ctx.sidebarSyncThrottleLastRun = typeof performance !== 'undefined' ? performance.now() : Date.now();
      refreshSidebarState(ctx).catch((error) => logWarn('thread', 'sidebar.sync.debounce.failed', { error, by_event: eventName }));
    }, debounceMs);
  }

  if (threadSyncSignal && historyHydrationSignal && eventThreadTarget && !activeThreadTarget) {
    const request = syncThreadHistoryAtomic(ctx, eventThreadTarget);
    logInfo('thread', 'state.sync.background_history.signal', { thread_id: eventThreadTarget, method: eventMethod, source: sourceLower || eventName });
    request.catch((error) => logWarn('thread', 'state.sync.background_history.failed', { error, by_event: eventName }));
    return;
  }

  // Streaming deltas: throttle + trailing debounce instead of full syncThreadState.
  // syncThreadState returns partial timeline during active turns and would overwrite local history.
  // Throttle: fire immediately on first delta, then at most once per STREAMING_THROTTLE_MS.
  // Trailing debounce: ensure a final sync fires after deltas stop.
  if (directThreadSyncSignal && activeThreadTarget && !turnCompletedSignal && !historyPageSignal) {
    const now = perfNow();
    const throttleMs = ctx.STREAMING_SYNC_THROTTLE_MS || 800;
    const elapsed = now - (ctx.streamingSyncLastRun || 0);
    if (elapsed >= throttleMs) {
      ctx.streamingSyncLastRun = now;
      syncThreadHistoryAtomic(ctx, activeThreadTarget)
        .catch((error) => logWarn('thread', 'state.sync.streaming_throttle.failed', { error, by_event: eventName }));
    }
    // Trailing debounce to catch final state after streaming stops
    clearTimeout(ctx.streamingSyncDebounceTimer);
    ctx.streamingSyncDebounceTimer = setTimeout(() => {
      ctx.streamingSyncLastRun = perfNow();
      syncThreadHistoryAtomic(ctx, activeThreadTarget)
        .catch((error) => logWarn('thread', 'state.sync.streaming_trailing.failed', { error, by_event: eventName }));
    }, 500);
    return;
  }

  // LIVE PATCHING DEBOUNCE PRESERVED FOR OTHER EVENTS; historyPageSignal removed to prevent infinite fetch loop

  if (threadSyncSignal && activeThreadTarget && (turnCompletedSignal || methodLower === 'item/completed')) {
    clearTimeout(ctx.syncDebounceTimer);
    const snapshotRequest = beginRuntimeSnapshotRequest(ctx, activeThreadTarget);
    const syncKind = turnCompletedSignal ? 'syncThreadHistoryAtomic' : 'syncThreadState';
    logInfo('thread', 'bridge.direct_sync_triggered', { thread_id: activeThreadTarget, sync_kind: syncKind, turn_completed: turnCompletedSignal });
    const request = turnCompletedSignal ? syncThreadHistoryAtomic(ctx, activeThreadTarget) : syncThreadState(ctx, activeThreadTarget);
    logInfo('thread', 'state.sync.direct.signal', { thread_id: activeThreadTarget, method: eventMethod, source: sourceLower || eventName, request_seq: snapshotRequest.seq });
    request.catch((error) => logWarn('thread', 'state.sync.direct.failed', { error, by_event: eventName }));
    return;
  }

  if (threadSyncSignal && activeThreadTarget) {
    const isHighPriority = sourceLower === 'thread/started' || shouldReloadThreadHistory(ctx, activeThreadTarget);
    const isMediumPriority = sourceLower === 'thread/compacted'
      || sourceLower === 'thread/tokenusage/updated'
      || sourceLower === 'turn/completed'
      || sourceLower === 'turn/aborted'
      || typeLower === 'context_compacted';
    let debounceMs = 200;
    if (isHighPriority) debounceMs = 0;
    else if (isMediumPriority) debounceMs = 80;
    const now = typeof performance !== 'undefined' ? performance.now() : Date.now();
    if (isHighPriority || now - ctx.syncThrottleLastRun >= ctx.SYNC_THROTTLE_MS) {
      ctx.syncThrottleLastRun = now;
      syncThreadState(ctx, activeThreadTarget).catch((error) => logWarn('thread', 'state.sync.throttle.failed', { error, by_event: eventName }));
    }
    clearTimeout(ctx.syncDebounceTimer);
    if (!isHighPriority) {
      ctx.syncDebounceTimer = setTimeout(() => {
        ctx.syncThrottleLastRun = typeof performance !== 'undefined' ? performance.now() : Date.now();
        syncThreadState(ctx, activeThreadTarget).catch((error) => logWarn('thread', 'state.sync.failed', { error, by_event: eventName }));
      }, debounceMs);
    }
  }
}

export function buildSyncContext(state, deps) {
  return {
    state,
    ...deps,
    runtimeSyncPromise: null,
    runtimeSyncPending: false,
    runtimeSnapshotRequestSeq: 0,
    latestRuntimeSnapshotRequestSeqByScope: new Map(),
    messageLoadPromiseByThread: new Map(),
    threadStateSyncPromiseByThread: new Map(),
    threadStateSyncPendingByThread: new Map(),
    threadDiffSyncPromiseByThread: new Map(),
    threadDiffLoadedRevisionByThread: new Map(),
    threadHistoryLoadedAtByThread: new Map(),
    threadHistoryProviderThreadIDByThread: new Map(),
    threadPatchSeqByThread: new Map(),
    threadPatchMetaByThread: new Map(),
    syncDebounceTimer: 0,
    syncThrottleLastRun: 0,
    streamingSyncLastRun: 0,
    streamingSyncDebounceTimer: 0,
    historyPageDebounceTimer: 0,
    sidebarRefreshPromise: null,
    sidebarRefreshPending: false,
    sidebarSyncDebounceTimer: 0,
    sidebarSyncThrottleLastRun: 0,
    THREAD_HISTORY_FRESH_TTL_MS: 30_000,
    THREAD_PATCH_RECENT_WINDOW_MS: 250,
    SYNC_THROTTLE_MS: 100,
    STREAMING_SYNC_THROTTLE_MS: 200,
    SIDEBAR_SYNC_THROTTLE_MS: 250,
  };
}
