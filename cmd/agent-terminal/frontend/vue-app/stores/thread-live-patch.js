// @ts-nocheck
import { normalizeThreadID, toNormalizedEventString } from './bridge-event-parser.js';
import { normalizeThread } from './thread-ui-normalize.js';
import { normalizeStatus } from '../services/status.js';
import { freezeTimelineItemsAtomic } from './thread-snapshot.js';

export const THREAD_PATCH_METHOD = 'ui/thread/patch';

function getBridgeEventPayloadObject(evt) {
  const candidates = [evt?.payload, evt?.params?.payload, evt?.params, evt?.data, evt];
  for (const candidate of candidates) {
    if (candidate && typeof candidate === 'object' && !Array.isArray(candidate)) return candidate;
  }
  return {};
}

function cloneTimelineItem(item) {
  if (!item || typeof item !== 'object') return null;
  const next = { ...item };
  if (Array.isArray(item.attachments)) next.attachments = item.attachments.map((attachment) => ({ ...attachment }));
  if (Array.isArray(item.Attachments)) next.Attachments = item.Attachments.map((attachment) => ({ ...attachment }));
  Object.freeze(next);
  return next;
}

function upsertThreadEntry(state, threadPatch, statusPatch) {
  const base = Array.isArray(state.threads) ? state.threads : [];
  const nextStatus = typeof statusPatch === 'string' && statusPatch ? normalizeStatus(statusPatch) : '';
  const normalized = threadPatch
    ? normalizeThread({ ...threadPatch, state: threadPatch?.state || nextStatus || threadPatch?.state })
    : null;
  if (!normalized?.id && !nextStatus) return;
  const threadId = normalized?.id || normalizeThreadID(threadPatch?.id);
  if (!threadId) return;
  const existingIndex = base.findIndex((item) => item?.id === threadId);
  const current = existingIndex >= 0 ? base[existingIndex] : null;
  const nextEntry = normalized || normalizeThread({ ...(current || { id: threadId, name: threadId }), state: nextStatus || current?.state || 'idle' });
  if (nextStatus && !threadPatch) nextEntry.state = nextStatus;
  if (current && current.id === nextEntry.id && current.name === nextEntry.name && current.state === nextEntry.state) return;
  const nextThreads = base.slice();
  if (existingIndex >= 0) nextThreads[existingIndex] = nextEntry;
  else nextThreads.push(nextEntry);
  state.threads = nextThreads;
}

function setSingleMapEntry(state, key, threadId, value, options = {}) {
  const current = state[key] && typeof state[key] === 'object' ? state[key] : {};
  const hasValue = Object.prototype.hasOwnProperty.call(options, 'hasValue') ? options.hasValue : true;
  if (!hasValue) return;
  if (options.removeWhenNil && (value == null || (options.removeWhenEmptyString && value === ''))) {
    if (!Object.prototype.hasOwnProperty.call(current, threadId)) return;
    const next = { ...current };
    delete next[threadId];
    state[key] = next;
    return;
  }
  if (current[threadId] === value) return;
  state[key] = { ...current, [threadId]: value };
}

function setObjectMapEntry(state, key, threadId, value, hasOwnValue) {
  if (!hasOwnValue) return;
  const current = state[key] && typeof state[key] === 'object' ? state[key] : {};
  if (value == null) {
    // 保护 tokenUsageByThread：一旦设置，不因 null patch 而删除
    if (key === 'tokenUsageByThread' && Object.prototype.hasOwnProperty.call(current, threadId)) return;
    if (!Object.prototype.hasOwnProperty.call(current, threadId)) return;
    const next = { ...current };
    delete next[threadId];
    state[key] = next;
    return;
  }
  const normalized = value && typeof value === 'object' ? { ...value } : {};
  Object.freeze(normalized);
  if (JSON.stringify(current[threadId]) === JSON.stringify(normalized)) return;
  state[key] = { ...current, [threadId]: normalized };
}

function setAlertsEntry(state, threadId, value, hasOwnValue) {
  if (!hasOwnValue) return;
  const current = state.alertsByThread && typeof state.alertsByThread === 'object' ? state.alertsByThread : {};
  const normalized = Array.isArray(value) ? value.map((entry) => ({ ...entry })) : [];
  if (JSON.stringify(current[threadId] || []) === JSON.stringify(normalized)) return;
  state.alertsByThread = { ...current, [threadId]: Object.freeze(normalized) };
}

function applyTimelineDelta(state, threadId, payload) {
  const hasItems = Array.isArray(payload.timelineItems);
  const hasRemovedItems = Array.isArray(payload.removedItemIds);
  const hasTimelineOrder = Array.isArray(payload.timelineOrder);
  if (!hasItems && !hasRemovedItems && !hasTimelineOrder) return { changed: false, needsRecovery: false };

  const current = Array.isArray(state.timelinesByThread?.[threadId]) ? state.timelinesByThread[threadId] : [];
  const itemById = new Map();
  for (const item of current) {
    if (item?.id) itemById.set(item.id, item);
  }

  if (hasRemovedItems) {
    for (const itemId of payload.removedItemIds) itemById.delete(itemId);
  }
  if (hasItems) {
    for (const rawItem of payload.timelineItems) {
      const nextItem = cloneTimelineItem(rawItem);
      if (!nextItem?.id) continue;
      itemById.set(nextItem.id, nextItem);
    }
  }

  let needsRecovery = false;
  let nextTimeline = current;
  if (hasTimelineOrder) {
    const ordered = [];
    for (const itemId of payload.timelineOrder) {
      const item = itemById.get(itemId);
      if (!item) {
        needsRecovery = true;
        continue;
      }
      ordered.push(item);
    }
    nextTimeline = ordered;
  } else {
    const seen = new Set();
    const merged = [];
    for (const item of current) {
      if (!item?.id) continue;
      const nextItem = itemById.get(item.id);
      merged.push(nextItem || item);
      seen.add(item.id);
    }
    for (const [itemId, item] of itemById.entries()) {
      if (seen.has(itemId)) continue;
      merged.push(item);
    }
    nextTimeline = merged;
  }

  const frozenTimeline = freezeTimelineItemsAtomic(nextTimeline, current);
  if (!frozenTimeline.changed) return { changed: false, needsRecovery };
  state.timelinesByThread = { ...state.timelinesByThread, [threadId]: frozenTimeline.items };
  return { changed: true, needsRecovery };
}

function recordThreadPatchMeta(ctx, threadId, source, sequence, now) {
  ctx.threadPatchSeqByThread.set(threadId, sequence);
  ctx.threadPatchMetaByThread.set(threadId, {
    at: now,
    source: toNormalizedEventString(source),
    sequence,
  });
}

export function shouldSkipThreadSyncFromPatch(ctx, threadId, methodLower, sourceLower, now = Date.now()) {
  const id = normalizeThreadID(threadId);
  if (!id) return false;
  const meta = ctx.threadPatchMetaByThread.get(id);
  if (!meta) return false;
  if (!Number.isFinite(meta.at) || (now - meta.at) > ctx.THREAD_PATCH_RECENT_WINDOW_MS) return false;
  const candidate = toNormalizedEventString(sourceLower || methodLower);
  if (!candidate) return false;
  return candidate === meta.source;
}

export function applyRuntimeThreadPatch(ctx, evt, threadId, options = {}) {
  const payload = getBridgeEventPayloadObject(evt);
  const id = normalizeThreadID(threadId || payload.threadId || payload.thread_id || payload.agent_id);
  if (!id) return { handled: false, needsRecovery: false, reason: '' };
  const hasStatus = Object.prototype.hasOwnProperty.call(payload, 'status');
  const hasInterruptible = Object.prototype.hasOwnProperty.call(payload, 'interruptible');
  const hasStatusHeader = Object.prototype.hasOwnProperty.call(payload, 'statusHeader');
  const hasStatusDetails = Object.prototype.hasOwnProperty.call(payload, 'statusDetails');
  const hasDiffText = Object.prototype.hasOwnProperty.call(payload, 'diffText');
  const hasDiffRevision = Object.prototype.hasOwnProperty.call(payload, 'diffRevision');
  const diffTextValue = hasDiffText ? (payload.diffText || '').toString() : undefined;
  const diffRevisionValue = hasDiffRevision ? Number(payload.diffRevision || 0) : undefined;
  const currentDiffText = (ctx.state.diffTextByThread?.[id] || '').toString();
  const currentDiffRevision = Number(ctx.state.diffRevisionByThread?.[id] || 0);
  const normalizedSource = toNormalizedEventString(payload.source);
  const shouldIgnoreTransientDiffClear = Boolean(
    hasDiffText
      && diffTextValue === ''
      && currentDiffText
      && (!hasDiffRevision || !Number.isFinite(diffRevisionValue) || diffRevisionValue <= currentDiffRevision)
      && (normalizedSource.startsWith('item/') || normalizedSource.startsWith('turn/')),
  );
  const hasTokenUsage = Object.prototype.hasOwnProperty.call(payload, 'tokenUsage');
  const hasAgentMeta = Object.prototype.hasOwnProperty.call(payload, 'agentMeta');
  const hasActivityStats = Object.prototype.hasOwnProperty.call(payload, 'activityStats');
  const hasAlerts = Object.prototype.hasOwnProperty.call(payload, 'alerts');
  const hasRefreshRequired = Boolean(payload.refreshRequired);
  const hasRecover = Boolean(payload.recover);
  const hasPatchShape = Object.prototype.hasOwnProperty.call(payload, 'thread')
    || hasStatus
    || hasInterruptible
    || hasStatusHeader
    || hasStatusDetails
    || hasDiffText
    || hasDiffRevision
    || hasTokenUsage
    || hasAgentMeta
    || hasActivityStats
    || hasAlerts
    || Array.isArray(payload.timelineItems)
    || Array.isArray(payload.removedItemIds)
    || Array.isArray(payload.timelineOrder)
    || hasRefreshRequired
    || hasRecover;
  if (!hasPatchShape) return { handled: false, needsRecovery: false, reason: '' };

  const sequence = Number(payload.sequence || 0);
  const previousSequence = Number(ctx.threadPatchSeqByThread.get(id) || 0);
  if (Number.isFinite(sequence) && sequence > 0 && previousSequence > 0 && sequence <= previousSequence) {
    ctx.logInfo('thread', 'state.patch.stale.skipped', {
      thread_id: id,
      source: (payload.source || '').toString(),
      sequence,
      previous_sequence: previousSequence,
    });
    return { handled: true, needsRecovery: false, reason: 'stale_sequence' };
  }

  const now = typeof options.perfNow === 'function' ? options.perfNow() : Date.now();

  upsertThreadEntry(ctx.state, payload.thread, payload.status);
  setSingleMapEntry(ctx.state, 'statuses', id, hasStatus ? normalizeStatus(payload.status) : undefined, { hasValue: hasStatus });
  setSingleMapEntry(ctx.state, 'interruptibleByThread', id, Boolean(payload.interruptible), { hasValue: hasInterruptible });
  setSingleMapEntry(ctx.state, 'statusHeadersByThread', id, hasStatusHeader ? (payload.statusHeader || '').toString() : undefined, { hasValue: hasStatusHeader });
  setSingleMapEntry(ctx.state, 'statusDetailsByThread', id, hasStatusDetails ? (payload.statusDetails || '').toString() : undefined, { hasValue: hasStatusDetails });
  if (shouldIgnoreTransientDiffClear) {
    ctx.logInfo('thread', 'state.patch.diff_clear_ignored', {
      thread_id: id,
      source: normalizedSource,
      sequence: Number.isFinite(sequence) && sequence > 0 ? sequence : null,
      current_diff_revision: currentDiffRevision,
      incoming_diff_revision: Number.isFinite(diffRevisionValue) ? diffRevisionValue : null,
    });
  } else {
    setSingleMapEntry(ctx.state, 'diffTextByThread', id, diffTextValue, { hasValue: hasDiffText });
    setSingleMapEntry(ctx.state, 'diffRevisionByThread', id, diffRevisionValue, { hasValue: hasDiffRevision });
  }
  setObjectMapEntry(ctx.state, 'tokenUsageByThread', id, payload.tokenUsage, hasTokenUsage);
  setObjectMapEntry(ctx.state, 'agentMetaById', id, payload.agentMeta, hasAgentMeta);
  setObjectMapEntry(ctx.state, 'activityStatsByThread', id, payload.activityStats, hasActivityStats);
  setAlertsEntry(ctx.state, id, payload.alerts, hasAlerts);

  const timelineResult = applyTimelineDelta(ctx.state, id, payload);
  const sequenceGap = Number.isFinite(sequence) && sequence > 0 && previousSequence > 0 && sequence !== previousSequence + 1;
  const needsRecovery = hasRecover || hasRefreshRequired || sequenceGap || timelineResult.needsRecovery;
  if (Number.isFinite(sequence) && sequence > 0) recordThreadPatchMeta(ctx, id, payload.source, sequence, now);
  if (Array.isArray(payload.timelineItems) && payload.timelineItems.length > 0) {
    ctx.logWarn('thread', 'patch.timeline_items_applied', {
      thread_id: id,
      source: (payload.source || '').toString(),
      sequence,
      timeline_items: payload.timelineItems.length,
      timeline_changed: timelineResult.changed,
      needs_recovery: needsRecovery,
    });
  }
  ctx.logInfo('thread', 'state.patch.applied', {
    thread_id: id,
    source: (payload.source || '').toString(),
    sequence: Number.isFinite(sequence) && sequence > 0 ? sequence : null,
    sequence_gap: sequenceGap,
    timeline_items: Array.isArray(payload.timelineItems) ? payload.timelineItems.length : 0,
    timeline_removed: Array.isArray(payload.removedItemIds) ? payload.removedItemIds.length : 0,
    timeline_reordered: Array.isArray(payload.timelineOrder),
    refresh_required: hasRefreshRequired,
    fallback_reason: hasRefreshRequired ? (payload.fallbackReason || '').toString() : '',
  });
  return { handled: true, needsRecovery, reason: hasRefreshRequired ? ((payload.fallbackReason || '').toString() || 'refresh_required') : (sequenceGap ? 'sequence_gap' : (timelineResult.needsRecovery ? 'missing_timeline_item' : '')) };
}
