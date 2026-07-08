import {
  activeThreadForStore,
  activeThreadIdentifiers,
  normalizedThreadIdentity,
  threadScopedBooleanValue,
  threadScopedMapValue,
} from '../adapters/threadStateAdapter.js';
import { requireTimestampMillis } from '../../shared/pageShared.js';

const GENERIC_TIMELINE_COMMAND_TITLES = new Set([
  'command',
  'execute command',
  'running command',
  '执行命令',
  '命令',
  '终端命令',
]);

function threadDataTextValue(value) {
  if (value === null || value === undefined) return '';
  return value.toString();
}

function firstThreadDataText(values) {
  for (const value of values) {
    const text = threadDataTextValue(value).trim();
    if (text) return text;
  }
  return '';
}

function requiredThreadMap(value, name) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new Error(`chat thread data requires ${name} object`);
  }
  return value;
}

function optionalThreadArray(value, name) {
  if (value === undefined || value === null) return [];
  if (!Array.isArray(value)) throw new Error(`chat thread data requires ${name} array`);
  return value;
}

function timelineItemKindCandidates(item) {
  return [item?.kind, item?.type, item?.eventType, item?.event_type];
}

function timelineItemTextCandidates(item) {
  return [item?.text, item?.content, item?.message, item?.output, item?.result, item?.error];
}

function timelineItemToolCandidates(item) {
  return [item?.tool, item?.toolName, item?.tool_name];
}

function timelineItemOrderCandidates(item) {
  return [item?.time, item?.ts, item?.createdAt, item?.created_at, item?.completedAt, item?.completed_at];
}

function activityEntryFields(entry) {
  return entry?.fields && typeof entry.fields === 'object' && !Array.isArray(entry.fields)
    ? entry.fields
    : null;
}

function activityEntryThreadPatch(fields) {
  const patch = fields?._threadPatch === undefined ? fields?._thread_patch : fields._threadPatch;
  return patch && typeof patch === 'object' && !Array.isArray(patch) ? patch : null;
}

function activityEntryThreadIdCandidates(entry) {
  const fields = activityEntryFields(entry);
  const patch = activityEntryThreadPatch(fields);
  return [
    entry?.threadId,
    entry?.thread_id,
    entry?.agentId,
    entry?.agent_id,
    fields?.threadId,
    fields?.thread_id,
    fields?.agentId,
    fields?.agent_id,
    patch?.threadId,
    patch?.thread_id,
    patch?.agentId,
    patch?.agent_id,
  ];
}

function positiveTimestampNumber(value) {
  if (!Number.isFinite(value) || value <= 0) return 0;
  return value < 1_000_000_000_000 ? value * 1000 : value;
}

function numericTextTimestampMs(text) {
  if (!/^\d+(?:\.\d+)?$/.test(text)) return 0;
  return positiveTimestampNumber(Number(text));
}

function parsedDateTimestampMs(text) {
  try {
    return requireTimestampMillis(text, 'thread data timestamp');
  } catch {
    return 0;
  }
}

function timestampMs(value) {
  if (typeof value === 'number') return positiveTimestampNumber(value);
  const text = threadDataTextValue(value).trim();
  return numericTextTimestampMs(text) || parsedDateTimestampMs(text);
}

function timelineItemKind(item = {}) {
  return firstThreadDataText(timelineItemKindCandidates(item)).toLowerCase();
}

function timelineItemTextValue(item = {}) {
  return firstThreadDataText(timelineItemTextCandidates(item));
}

function hasRenderableTimelineCommand(item = {}) {
  if (threadDataTextValue(item.command).trim()) return true;
  if (timelineItemTextValue(item)) return true;
  const title = threadDataTextValue(item.title).trim();
  return Boolean(title.startsWith('$ ') && !GENERIC_TIMELINE_COMMAND_TITLES.has(title.toLowerCase()));
}

function isRenderableThreadScopedTimelineItem(item = {}) {
  if (timelineItemKind(item) !== 'command') return true;
  if (firstThreadDataText(timelineItemToolCandidates(item))) return false;
  return hasRenderableTimelineCommand(item);
}

function timelineItemOrderTime(item = {}) {
  return timestampMs(firstThreadDataText(timelineItemOrderCandidates(item)));
}

export function mergeThreadScopedTimelineItems(items = []) {
  const merged = [];
  const indexById = new Map();

  for (const item of items) {
    const id = threadDataTextValue(item?.id).trim();
    if (!id) {
      merged.push(item);
      continue;
    }
    const existingIndex = indexById.get(id);
    if (existingIndex === undefined) {
      indexById.set(id, merged.length);
      merged.push(item);
      continue;
    }
    merged[existingIndex] = { ...merged[existingIndex], ...item };
  }

  return merged
    .map((item, index) => ({ item, index, orderTime: timelineItemOrderTime(item) }))
    .sort((left, right) => {
      if (left.orderTime && right.orderTime && left.orderTime !== right.orderTime) {
        return left.orderTime - right.orderTime;
      }
      if (left.orderTime && !right.orderTime) return -1;
      if (!left.orderTime && right.orderTime) return 1;
      return left.index - right.index;
    })
    .map(({ item }) => item);
}

export function threadScopedTimelineValue(map = {}, activeThreadId, activeThread, fallback = []) {
  /*
   * 同一会话可能有 threadId、agentId、sessionId 等多个名字。
   * 这里把这些名字下的 timeline 合成当前页面要显示的消息。
   */
  const ids = activeThreadIdentifiers(activeThreadId, activeThread);
  const items = [];
  for (const id of ids) {
    if (!map || typeof map !== 'object' || Array.isArray(map)) continue;
    if (!Object.prototype.hasOwnProperty.call(map, id)) continue;
    const value = map[id];
    if (Array.isArray(value)) items.push(...value.filter(isRenderableThreadScopedTimelineItem));
  }
  return items.length > 0 ? mergeThreadScopedTimelineItems(items) : fallback;
}

function firstNormalizedIdentity(values = []) {
  for (const value of values) {
    const id = normalizedThreadIdentity(value);
    if (id) return id;
  }
  return '';
}

export function activityEntryThreadIdentifier(entry = {}) {
  return firstNormalizedIdentity(activityEntryThreadIdCandidates(entry));
}

export function scopedActivityEntries(entries = [], activeThreadId, activeThread, options = {}) {
  const ids = activeThreadIdentifiers(activeThreadId, activeThread);
  if (ids.size === 0) return [];
  return optionalThreadArray(entries, 'activity entries').filter((entry) => {
    const entryThreadId = activityEntryThreadIdentifier(entry);
    if (!entryThreadId) return Boolean(options.includeUnscoped);
    return ids.has(entryThreadId);
  });
}

export function useChatThreadData(store, activeThreadId) {
  /*
   * ChatPage 在这里从 store 取当前线程数据。
   * timeline、diff、token、活动日志都按当前线程名字集合读取。
   */
  const activeThread = activeThreadForStore(store);
  const timelineBlocked = Boolean(activeThreadId && threadScopedBooleanValue(requiredThreadMap(store.threadStateLoadingByThread, 'threadStateLoadingByThread'), activeThreadId, activeThread, false));
  const cachedTimeline = threadScopedTimelineValue(requiredThreadMap(store.timelinesByThread, 'timelinesByThread'), activeThreadId, activeThread, []);
  const timelineReadyFlag = threadScopedBooleanValue(requiredThreadMap(store.threadTimelineReadyByThread, 'threadTimelineReadyByThread'), activeThreadId, activeThread, false);
  const timelineReady = Boolean(
    activeThreadId &&
    timelineReadyFlag &&
    (!timelineBlocked || cachedTimeline.length > 0),
  );
  const timelineContentBlocked = timelineBlocked && !timelineReady;
  return {
    activeThread,
    activeTurn: threadScopedMapValue(requiredThreadMap(store.activeTurnByThread, 'activeTurnByThread'), activeThreadId, activeThread, null),
    activityStats: threadScopedMapValue(requiredThreadMap(store.activityStatsByThread, 'activityStatsByThread'), activeThreadId, activeThread, null),
    diffText: threadDataTextValue(threadScopedMapValue(requiredThreadMap(store.diffTextByThread, 'diffTextByThread'), activeThreadId, activeThread, '')),
    messagePagination: threadScopedMapValue(requiredThreadMap(store.threadMessagePaginationByThread, 'threadMessagePaginationByThread'), activeThreadId, activeThread, null),
    messages: timelineContentBlocked ? [] : cachedTimeline,
    runtimeResults: scopedActivityEntries(optionalThreadArray(store.runtimeResultEntries, 'runtimeResultEntries'), activeThreadId, activeThread, { includeUnscoped: true }),
    statusEntry: activeThreadId ? requiredThreadMap(store.statuses, 'statuses')[activeThreadId] : null,
    timelineBlocked,
    timelineContentBlocked,
    tokenUsage: threadScopedMapValue(requiredThreadMap(store.tokenUsageByThread, 'tokenUsageByThread'), activeThreadId, activeThread, null),
    warnings: scopedActivityEntries(optionalThreadArray(store.warningEntries, 'warningEntries'), activeThreadId, activeThread, { includeUnscoped: true }),
  };
}
