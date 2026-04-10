function parseHistoryMetadata(raw) {
  if (!raw) return null;
  if (typeof raw === 'string') {
    try {
      return JSON.parse(raw);
    } catch {
      return null;
    }
  }
  return typeof raw === 'object' ? raw : null;
}

function extractFirstString(source, keys) {
  if (!source || typeof source !== 'object') return '';
  for (const key of keys) {
    const value = source[key];
    if (typeof value === 'string' && value.trim()) return value.trim();
  }
  return '';
}

function toCreatedAtMillis(value) {
  if (value instanceof Date) return value.getTime();
  const parsed = Date.parse((value || '').toString());
  return Number.isFinite(parsed) ? parsed : Number.NaN;
}

function toNumericMessageID(value) {
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : Number.NaN;
}

function sortHistoryMessagesChronologically(messages) {
  return messages
    .map((message, index) => ({
      message,
      index,
      createdAt: toCreatedAtMillis(message?.createdAt),
      numericId: toNumericMessageID(message?.id),
    }))
    .sort((left, right) => {
      if (Number.isFinite(left.createdAt) && Number.isFinite(right.createdAt) && left.createdAt !== right.createdAt) {
        return left.createdAt - right.createdAt;
      }
      if (Number.isFinite(left.numericId) && Number.isFinite(right.numericId) && left.numericId !== right.numericId) {
        return left.numericId - right.numericId;
      }
      return left.index - right.index;
    })
    .map((entry) => entry.message);
}

function countDialogTimelineItems(items) {
  if (!Array.isArray(items)) return 0;
  return items.reduce((count, item) => {
    const kind = (item?.kind || '').toString().trim();
    return count + ((kind === 'assistant' || kind === 'user') ? 1 : 0);
  }, 0);
}

function getLatestDialogTimestamp(items) {
  if (!Array.isArray(items)) return Number.NaN;
  for (let index = items.length - 1; index >= 0; index -= 1) {
    const item = items[index];
    const kind = (item?.kind || '').toString().trim();
    if (kind !== 'assistant' && kind !== 'user') continue;
    return toCreatedAtMillis(item?.ts);
  }
  return Number.NaN;
}

function historyMessageToTimelineItem(threadId, message, index) {
  const role = (message?.role || '').toString().trim().toLowerCase();
  const kind = role === 'assistant' ? 'assistant' : (role === 'user' ? 'user' : '');
  if (!kind) return null;
  const rawCreatedAt = message?.createdAt;
  const ts = rawCreatedAt instanceof Date ? rawCreatedAt.toISOString() : (rawCreatedAt || '').toString();
  const rawID = Number(message?.id);
  const itemID = Number.isFinite(rawID) && rawID > 0 ? `${threadId}-history-${Math.floor(rawID)}` : `${threadId}-history-${index + 1}`;
  const item = { id: itemID, kind, text: (message?.content || '').toString(), ts };
  if (kind !== 'user') return item;
  const metadata = parseHistoryMetadata(message?.metadata);
  if (!metadata) return item;
  if (metadata.internal === true) item.internal = true;
  const sourceKind = extractFirstString(metadata, ['sourceKind', 'source_kind']);
  if (sourceKind) item.sourceKind = sourceKind;
  const fromThreadId = extractFirstString(metadata, ['fromThreadId', 'from_thread_id', 'from']);
  if (fromThreadId) item.fromThreadId = fromThreadId;
  const toThreadId = extractFirstString(metadata, ['toThreadId', 'to_thread_id', 'to']);
  if (toThreadId) item.toThreadId = toThreadId;
  const fromDisplay = extractFirstString(metadata, ['fromDisplay', 'from_display']);
  if (fromDisplay) item.fromDisplay = fromDisplay;
  const toDisplay = extractFirstString(metadata, ['toDisplay', 'to_display']);
  if (toDisplay) item.toDisplay = toDisplay;
  return item;
}

export function applyImmediateTimelineFromMessages({ threadId, response, state, normalizeThreadID, freezeTimelineItemsAtomic, logInfo, logWarn: logWarnParam }) {
  const logWarnFn = typeof logWarnParam === 'function' ? logWarnParam : (typeof logInfo === 'function' ? logInfo : () => {});
  const id = typeof normalizeThreadID === 'function' ? normalizeThreadID(threadId) : (threadId || '').toString().trim();
  const messages = Array.isArray(response?.messages) ? response.messages : [];
  if (!id || messages.length === 0) return false;
  const orderedMessages = sortHistoryMessagesChronologically(messages);
  const timeline = orderedMessages.map((message, index) => historyMessageToTimelineItem(id, message, index)).filter(Boolean);
  if (timeline.length === 0) return false;
  const existing = Array.isArray(state?.timelinesByThread?.[id]) ? state.timelinesByThread[id] : [];
  const existingDialogCount = countDialogTimelineItems(existing);
  const incomingDialogCount = countDialogTimelineItems(timeline);
  const existingLatestDialogTs = getLatestDialogTimestamp(existing);
  const incomingLatestDialogTs = getLatestDialogTimestamp(timeline);
  const existingTsValid = Number.isFinite(existingLatestDialogTs);
  const incomingTsValid = Number.isFinite(incomingLatestDialogTs);
  // 跳过条件核心判断：本地是否比 incoming "更新或相等"
  // - 两边都有有效 ts → 直接比时间戳
  // - incoming 有有效 ts 而本地没有 → 本地数据不完整（live-patch 无 ts），强制接受 incoming
  // - existing 有有效 ts 而 incoming 没有 → 保守保留已有本地数据
  // - 两边都无有效 ts → fallback 比消息数量
  let sameOrNewerExistingDialog = false;
  if (existingTsValid && incomingTsValid) sameOrNewerExistingDialog = existingLatestDialogTs >= incomingLatestDialogTs;
  else if (!existingTsValid && incomingTsValid) sameOrNewerExistingDialog = false;
  else if (existingTsValid && !incomingTsValid) sameOrNewerExistingDialog = true;
  else sameOrNewerExistingDialog = existing.length >= timeline.length;
  if (existingDialogCount > 0 && existingDialogCount >= incomingDialogCount && sameOrNewerExistingDialog) {
    if (typeof logInfo === 'function') logInfo('thread', 'messages.load.local_timeline.skipped_stale', {
      thread_id: id,
      existing_count: existing.length,
      incoming_count: timeline.length,
      existing_dialog_count: existingDialogCount,
      incoming_dialog_count: incomingDialogCount,
      existing_latest_dialog_ts: existingTsValid ? existingLatestDialogTs : null,
      incoming_latest_dialog_ts: incomingTsValid ? incomingLatestDialogTs : null,
    });
    return false;
  }
  // Check if local has optimistic user messages that would be lost
  const hasOptimistic = existing.some((it) => (it?.id || '').toString().includes('-optimistic-'));
  logWarnFn('thread', 'messages.load.local_timeline.overwrite_decision', {
    thread_id: id,
    existing_total: existing.length,
    incoming_total: timeline.length,
    existing_dialog_count: existingDialogCount,
    incoming_dialog_count: incomingDialogCount,
    existing_ts: existingTsValid ? existingLatestDialogTs : null,
    incoming_ts: incomingTsValid ? incomingLatestDialogTs : null,
    same_or_newer: sameOrNewerExistingDialog,
    has_optimistic: hasOptimistic,
  });
  const frozenTimeline = freezeTimelineItemsAtomic(timeline, existing);
  if (!frozenTimeline.changed) return false;
  // Preserve optimistic user messages that are not yet in the incoming timeline
  if (hasOptimistic) {
    const optimisticItems = existing.filter((it) => (it?.id || '').toString().includes('-optimistic-'));
    const mergedItems = [...frozenTimeline.items, ...optimisticItems];
    state.timelinesByThread = { ...state.timelinesByThread, [id]: mergedItems };
    logWarnFn('thread', 'messages.load.local_timeline.applied_with_optimistic', {
      thread_id: id,
      history_count: frozenTimeline.items.length,
      optimistic_count: optimisticItems.length,
      merged_count: mergedItems.length,
    });
    return true;
  }
  state.timelinesByThread = { ...state.timelinesByThread, [id]: frozenTimeline.items };
  if (typeof logInfo === 'function') logInfo('thread', 'messages.load.local_timeline.applied', { thread_id: id, count: timeline.length });
  return true;
}
