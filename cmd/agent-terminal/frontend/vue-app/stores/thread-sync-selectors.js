// @ts-nocheck
import { normalizeStatus } from '../services/status.js';
import { normalizeThreadID } from './bridge-event-parser.js';

// Backend timeline projector lifecycle kinds that are not renderable chat content.
const STRUCTURAL_TIMELINE_KINDS = new Set([
  'turn_start', 'turn_end', 'turn_interrupted',
]);

export function getThreadTimeline(ctx, threadId) {
  if (!threadId) return [];
  const items = ctx.state.timelinesByThread[threadId] || [];
  // Ensure we don't render truncated snapshot data before the full history is loaded
  const hasHistory = ctx.threadHistoryLoadedAtByThread ? ctx.threadHistoryLoadedAtByThread.has(threadId) : false;
  if (!hasHistory) {
    ctx.logWarn('ui', 'chat.timeline.shielded_empty', { thread_id: threadId, items_len: items.length });
    return [];
  }
  if (items.length === 0) return items;
  // Fast path: if no structural items, return as-is to avoid allocation
  if (!items.some((it) => STRUCTURAL_TIMELINE_KINDS.has(it?.kind))) return items;
  return items.filter((it) => !STRUCTURAL_TIMELINE_KINDS.has(it?.kind));
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
  if (providerThreadID && loadedProviderThreadID && providerThreadID !== loadedProviderThreadID) return true;
  const loadedAt = Number(ctx.threadHistoryLoadedAtByThread.get(id) || 0);
  if (!Number.isFinite(loadedAt) || loadedAt <= 0) return true;
  if (status !== 'idle') return false;
  return (Date.now() - loadedAt) > ctx.THREAD_HISTORY_FRESH_TTL_MS;
}
