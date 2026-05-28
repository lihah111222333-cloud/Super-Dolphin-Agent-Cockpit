// @ts-nocheck
import { isThreadActiveStatus, normalizeStatus } from '../services/status.js';
import { normalizeThreadID } from './bridge-event-parser.js';

// Backend timeline projector lifecycle kinds that are not renderable chat content.
const STRUCTURAL_TIMELINE_KINDS = new Set([
  'turn_start', 'turn_end', 'turn_interrupted',
]);

export function getThreadTimeline(ctx, threadId) {
  if (!threadId) return [];
  const items = ctx.state.timelinesByThread[threadId] || [];
  // Ensure we don't render truncated snapshot data before the full history is loaded.
  // Live thread patches are already authoritative push data, so keep them visible
  // while background history hydration catches up.
  const hasHistory = ctx.threadHistoryLoadedAtByThread ? ctx.threadHistoryLoadedAtByThread.has(threadId) : false;
  const hasLivePatch = ctx.threadPatchMetaByThread ? ctx.threadPatchMetaByThread.has(threadId) : false;
  if (!hasHistory && !hasLivePatch) {
    if (typeof ctx.logDebug === 'function') {
      ctx.logDebug('ui', 'chat.timeline.shielded_empty', { thread_id: threadId, items_len: items.length });
    }
    return [];
  }
  if (items.length === 0) return items;
  // fork 继承对话 kickoff：当 kickoffByThread 记录这个 thread 的 kickoff text 时，
  // 过滤匹配 text 的 user 消息（让 agent 看起来主动开场）；text 转 trim 比对避免空白差异。
  const kickoffText = ((ctx.state.kickoffByThread || {})[threadId] || '').toString().trim();
  const hasStructural = items.some((it) => STRUCTURAL_TIMELINE_KINDS.has(it?.kind));
  if (!hasStructural && !kickoffText) return items;
  return items.filter((it) => {
    if (STRUCTURAL_TIMELINE_KINDS.has(it?.kind)) return false;
    if (kickoffText && it?.kind === 'user' && (it?.text || '').toString().trim() === kickoffText) return false;
    return true;
  });
}

export function getThreadDiff(ctx, threadId) {
  if (!threadId) return '';
  return ctx.state.diffTextByThread[threadId] || '';
}

export function getThreadStatus(ctx, threadId) {
  if (!threadId) return 'idle';
  return ctx.state.statuses[threadId] || 'idle';
}

export function getThreadStatusHeader(ctx, threadId) {
  if (!threadId) return '';
  return (ctx.state.statusHeadersByThread?.[threadId] || '').toString();
}

export function getThreadStatusDetails(ctx, threadId) {
  if (!threadId) return '';
  return (ctx.state.statusDetailsByThread?.[threadId] || '').toString();
}

export function getThreadTokenUsage(ctx, threadId) {
  if (!threadId) return null;
  const value = ctx.state.tokenUsageByThread?.[threadId];
  return value && typeof value === 'object' ? value : null;
}

export function getThreadActivityStats(ctx, threadId) {
  if (!threadId) return {};
  const value = ctx.state.activityStatsByThread?.[threadId];
  return value && typeof value === 'object' ? value : {};
}

export function getThreadAlerts(ctx, threadId) {
  if (!threadId) return [];
  const value = ctx.state.alertsByThread?.[threadId];
  return Array.isArray(value) ? value : [];
}

export function getThreadInterruptible(ctx, threadId) {
  if (!threadId) return false;
  return Boolean(ctx.state.interruptibleByThread[threadId]);
}

export function shouldReloadThreadHistory(ctx, threadId) {
  const id = normalizeThreadID(threadId);
  if (!id) return false;
  const status = normalizeStatus(getThreadStatus(ctx, id));
  const providerThreadID = ctx.normalizeProviderThreadID(ctx.state.agentRuntimeById?.[id]?.providerThreadId || ctx.state.agentRuntimeById?.[id]?.provider_thread_id);
  const loadedProviderThreadID = ctx.normalizeProviderThreadID(ctx.threadHistoryProviderThreadIDByThread.get(id));
  if (providerThreadID && loadedProviderThreadID && providerThreadID !== loadedProviderThreadID) {
    ctx.logWarn('thread', 'shouldReloadThreadHistory.true.provider_mismatch', { thread_id: id, provider_thread_id: providerThreadID, loaded_provider_thread_id: loadedProviderThreadID });
    return true;
  }
  const loadedAt = Number(ctx.threadHistoryLoadedAtByThread.get(id) || 0);
  if (!Number.isFinite(loadedAt) || loadedAt <= 0) {
    if (typeof ctx.logDebug === 'function') {
      ctx.logDebug('thread', 'shouldReloadThreadHistory.true.not_loaded', { thread_id: id });
    }
    return true;
  }
  if (isThreadActiveStatus(status)) {
    const elapsed = Date.now() - loadedAt;
    const streamingTtl = 1000; // Poll history every 1s during streaming
    const shouldReload = elapsed > streamingTtl;
    if (typeof ctx.logDebug === 'function') {
      ctx.logDebug('thread', 'shouldReloadThreadHistory.streaming_check', { thread_id: id, elapsed, ttl: streamingTtl, should_reload: shouldReload });
    }
    return shouldReload;
  }
  const elapsed = Date.now() - loadedAt;
  const ttl = ctx.THREAD_HISTORY_FRESH_TTL_MS;
  const shouldReload = elapsed > ttl;
  if (typeof ctx.logDebug === 'function') {
    ctx.logDebug('thread', 'shouldReloadThreadHistory.ttl_check', { thread_id: id, elapsed, ttl, should_reload: shouldReload });
  }
  return shouldReload;
}
