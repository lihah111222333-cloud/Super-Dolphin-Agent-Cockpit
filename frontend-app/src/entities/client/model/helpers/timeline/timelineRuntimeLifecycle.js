import {
  firstOptionalPresent,
  normalizeString,
  normalizeTimelineDone,
  normalizeTimelineKind,
  normalizeTimestamp,
} from "./timelineRuntimeFields.js";

const TIMELINE_LIFECYCLE_KINDS = new Set(["tool", "command", "process"]);

function lifecycleTimelineKey(item) {
  const kind = normalizeTimelineKind(item);
  const callId = normalizeString(item?.callId);
  if (
    item?.role !== "assistant" ||
    !callId ||
    !TIMELINE_LIFECYCLE_KINDS.has(kind)
  )
    return "";
  return `${kind}:${callId}`;
}

function isTerminalTimelineItem(item) {
  return normalizeTimelineDone(
    item,
    normalizeString(item?.status).toLowerCase(),
  );
}

function preferredLifecycleTimelineItem(existingItem, incomingItem) {
  const existingTerminal = isTerminalTimelineItem(existingItem);
  const incomingTerminal = isTerminalTimelineItem(incomingItem);
  if (existingTerminal !== incomingTerminal)
    return incomingTerminal ? incomingItem : existingItem;
  const existingTextLength = normalizeString(existingItem?.text).length;
  const incomingTextLength = normalizeString(incomingItem?.text).length;
  if (existingTextLength !== incomingTextLength)
    return incomingTextLength > existingTextLength
      ? incomingItem
      : existingItem;
  const existingTime = normalizeTimestamp(
    firstOptionalPresent(existingItem?.completedAt, existingItem?.time),
  );
  const incomingTime = normalizeTimestamp(
    firstOptionalPresent(incomingItem?.completedAt, incomingItem?.time),
  );
  if (existingTime !== incomingTime)
    return incomingTime > existingTime ? incomingItem : existingItem;
  return incomingItem;
}

function earlierTimelineTime(left, right) {
  const leftTime = normalizeString(left?.time);
  const rightTime = normalizeString(right?.time);
  const leftTimestamp = normalizeTimestamp(leftTime);
  const rightTimestamp = normalizeTimestamp(rightTime);
  if (leftTimestamp > 0 && rightTimestamp > 0)
    return leftTimestamp <= rightTimestamp ? leftTime : rightTime;
  return leftTime || rightTime;
}

function mergeLifecycleTimelineItem(existingItem, incomingItem) {
  const preferred = preferredLifecycleTimelineItem(existingItem, incomingItem);
  const fallback = preferred === incomingItem ? existingItem : incomingItem;
  const status =
    normalizeString(preferred?.status) || normalizeString(fallback?.status);
  const text = normalizeString(preferred?.text)
    ? preferred.text
    : fallback.text;
  return {
    ...fallback,
    ...preferred,
    id: normalizeString(preferred?.id) || normalizeString(fallback?.id),
    callId:
      normalizeString(preferred?.callId) || normalizeString(fallback?.callId),
    text,
    title:
      normalizeString(preferred?.title) || normalizeString(fallback?.title),
    status,
    time: earlierTimelineTime(existingItem, incomingItem),
    completedAt:
      normalizeString(preferred?.completedAt) ||
      normalizeString(fallback?.completedAt),
    done: normalizeTimelineDone(preferred, status),
    elapsedMs: firstOptionalPresent(preferred?.elapsedMs, fallback?.elapsedMs),
  };
}

export function coalesceTimelineLifecycleItems(items = []) {
  const output = [];
  const indexByKey = new Map();
  for (const item of items) {
    const key = lifecycleTimelineKey(item);
    if (!key) {
      output.push(item);
      continue;
    }
    const existingIndex = indexByKey.get(key);
    if (existingIndex === undefined) {
      indexByKey.set(key, output.length);
      output.push(item);
      continue;
    }
    output[existingIndex] = mergeLifecycleTimelineItem(
      output[existingIndex],
      item,
    );
  }
  return output;
}
