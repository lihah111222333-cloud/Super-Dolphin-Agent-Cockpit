// @ts-check

import { RUNTIME_TOOL_TERMINAL_STATUSES } from './runtimeResults.js';

const RUNTIME_ASSISTANT_PREFIX_DUPLICATE_MIN_CHARS = 24;
const RUNTIME_ASSISTANT_LOOSE_DUPLICATE_MIN_CHARS = 80;
const RUNTIME_ASSISTANT_LOOSE_DUPLICATE_SHINGLE_SIZE = 12;
const RUNTIME_ASSISTANT_LOOSE_DUPLICATE_MIN_MATCHES = 4;
const RUNTIME_ASSISTANT_LOOSE_DUPLICATE_MIN_RATIO = 0.65;
const IMAGE_PLACEHOLDER_RE = /<image\s[^>]*><\/image>/gi;
const TIMELINE_KIND_KEYS = Object.freeze(['kind', 'type', 'eventType', 'event_type', 'role']);
const TIMELINE_ROLE_KEYS = Object.freeze(['role', 'kind', 'type', 'eventType', 'event_type']);
const TIMELINE_TEXT_KEYS = Object.freeze(['text', 'content', 'message', 'delta', 'output', 'result', 'answer', 'response', 'summary', 'preview', 'error']);
const TIMELINE_ID_KEYS = Object.freeze(['id', 'messageId', 'message_id']);
const TIMELINE_TITLE_KEYS = Object.freeze(['title', 'label', 'name', 'tool', 'toolName', 'command']);
const TIMELINE_TIME_KEYS = Object.freeze(['time', 'startedAt', 'started_at', 'ts', 'createdAt', 'created_at']);
const TIMELINE_COMPLETED_KEYS = Object.freeze(['completedAt', 'completed_at', 'finishedAt', 'finished_at']);
const TIMELINE_CALL_ID_KEYS = Object.freeze(['callId', 'call_id', 'toolCallId', 'tool_call_id']);
const TIMELINE_LIFECYCLE_KINDS = new Set(['tool', 'command', 'process']);
const TIMELINE_TERMINAL_STATUSES = new Set([...RUNTIME_TOOL_TERMINAL_STATUSES, 'skipped', 'cancelled', 'canceled', 'aborted']);
const MESSAGE_LIFECYCLE_ITEM_TYPES = new Set(['message', 'usermessage', 'user_message', 'assistantmessage', 'assistant_message']);
const GENERIC_COMMAND_TITLES = new Set(['command', 'execute command', 'running command', '执行命令', '命令', '终端命令']);

function normalizeString(value) {
  return (value || '').toString().trim();
}

function objectRecord(value) {
  return value && typeof value === 'object' && !Array.isArray(value) ? value : {};
}

/**
 * @param {Record<string, unknown>} source
 * @param {readonly string[]} keys
 */
function firstFieldValue(source, keys = []) {
  const record = objectRecord(source);
  for (const key of keys) {
    const value = record[key];
    if (value !== undefined && value !== null && value !== '') return value;
  }
  return undefined;
}

/**
 * @param {Record<string, unknown>} source
 * @param {readonly string[]} keys
 */
function positiveNumberFromFields(source, keys = []) {
  const numeric = Number(firstFieldValue(source, keys));
  return Math.max(0, Number.isFinite(numeric) ? numeric : 0);
}

function normalizeTimestamp(value) {
  if (typeof value === 'boolean' || value === null || value === undefined) return 0;
  if (typeof value === 'number') return Number.isFinite(value) && value > 0 ? value : 0;
  const text = normalizeString(value);
  if (!text) return 0;
  const asNumber = Number(text);
  if (Number.isFinite(asNumber) && asNumber > 0) return asNumber;
  const sanitized = text.replace(/(\.\d{3})\d+/g, '$1');
  const parsed = Date.parse(sanitized);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : 0;
}

function extractText(value) {
  if (value === null || value === undefined) return '';
  if (typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean') {
    return normalizeString(value);
  }
  if (Array.isArray(value)) {
    return value.map((item) => extractText(item)).filter(Boolean).join('\n');
  }
  if (typeof value === 'object') {
    return extractText(value.text || value.content || value.message || value.delta || value.output || value.result || value.answer || value.response);
  }
  return '';
}

function normalizeTimelineKindFromRaw(rawRole, rawKind) {
  if (rawRole.includes('user')) return 'user';
  if (rawKind.includes('approval')) return 'approval';
  if (rawKind.includes('thinking') || rawKind.includes('reasoning')) return 'thinking';
  if (rawKind.includes('command') || rawKind.includes('exec')) return 'command';
  if (rawKind.includes('tool')) return 'tool';
  if (rawKind.includes('plan')) return 'plan';
  return 'assistant';
}

function normalizeTimelineElapsedMs(item) {
  if (item?.elapsedMs !== undefined) return Number(item.elapsedMs);
  if (item?.elapsed_ms !== undefined) return Number(item.elapsed_ms);
  if (item?.durationMs !== undefined) return Number(item.durationMs);
  if (item?.duration_ms !== undefined) return Number(item.duration_ms);
  return undefined;
}

function normalizeTimelineDone(item, status) {
  const normalizedStatus = normalizeString(status).toLowerCase();
  if (TIMELINE_TERMINAL_STATUSES.has(normalizedStatus)) return true;
  if (typeof item?.done === 'boolean') return item.done;
  if (!normalizedStatus) return true;
  return false;
}

function normalizeUserTimelineText(text) {
  let processed = text;
  if (processed) {
    processed = processed.replace(IMAGE_PLACEHOLDER_RE, '').trim();
  }
  const trimmed = normalizeString(processed).trim();
  const closeTag = '</turn_aborted>';
  const lower = trimmed.toLowerCase();
  if (!lower.startsWith('<turn_aborted>')) {
    return { text: processed, controlOnly: false };
  }
  const closeIndex = lower.indexOf(closeTag);
  const remaining = closeIndex >= 0 ? trimmed.slice(closeIndex + closeTag.length).trimStart() : '';
  return {
    text: remaining,
    controlOnly: !remaining,
  };
}

export function normalizeTimelineItem(item) {
  const rawKind = normalizeString(firstFieldValue(item, TIMELINE_KIND_KEYS)).toLowerCase();
  const rawRole = normalizeString(firstFieldValue(item, TIMELINE_ROLE_KEYS)).toLowerCase();
  const normalizedRole = rawRole.includes('user') ? 'user' : 'assistant';
  const normalizedKind = normalizeTimelineKindFromRaw(rawRole, rawKind);
  const rawText = extractText(firstFieldValue(item, TIMELINE_TEXT_KEYS));
  const userText = normalizedRole === 'user' ? normalizeUserTimelineText(rawText) : { text: rawText, controlOnly: false };
  const status = normalizeString(item?.status);
  return {
    id: normalizeString(firstFieldValue(item, TIMELINE_ID_KEYS)) || `${normalizedRole}-${Date.now()}`,
    role: normalizedRole,
    kind: normalizedKind,
    text: userText.text,
    controlOnly: userText.controlOnly,
    title: normalizeString(firstFieldValue(item, TIMELINE_TITLE_KEYS)),
    callId: normalizeString(firstFieldValue(item, TIMELINE_CALL_ID_KEYS)),
    requestId: positiveNumberFromFields(item, ['requestId', 'request_id']),
    command: normalizeString(item?.command),
    toolName: normalizeString(item?.tool || item?.toolName || item?.tool_name),
    status,
    time: normalizeString(firstFieldValue(item, TIMELINE_TIME_KEYS)) || new Date().toISOString(),
    completedAt: normalizeString(firstFieldValue(item, TIMELINE_COMPLETED_KEYS)),
    done: normalizeTimelineDone(item, status),
    optimistic: Boolean(item?.optimistic),
    elapsedMs: normalizeTimelineElapsedMs(item),
  };
}

export function sortTimelineChronologically(items = []) {
  return (
    [...items]
    .map((item, index) => ({ item, index, timestamp: normalizeTimestamp(item?.time) }))
    .sort((left, right) => {
      if (left.timestamp !== right.timestamp) return left.timestamp - right.timestamp;
      return left.index - right.index;
    })
    .map(({ item }) => item)
  );
}

function normalizeTimelineKind(item) {
  const kind = normalizeString(item?.kind).toLowerCase();
  if (kind) return kind;
  return item?.role === 'user' ? 'user' : 'assistant';
}

function lifecycleTimelineKey(item) {
  const kind = normalizeTimelineKind(item);
  const callId = normalizeString(item?.callId);
  if (item?.role !== 'assistant' || !callId || !TIMELINE_LIFECYCLE_KINDS.has(kind)) return '';
  return `${kind}:${callId}`;
}

function isTerminalTimelineItem(item) {
  const status = normalizeString(item?.status).toLowerCase();
  return normalizeTimelineDone(item, status);
}

function timelineItemTextLength(item) {
  return normalizeString(item?.text).length;
}

function timelineItemSortTime(item) {
  return normalizeTimestamp(item?.completedAt || item?.time);
}

function preferredLifecycleTimelineItem(existingItem, incomingItem) {
  const existingTerminal = isTerminalTimelineItem(existingItem);
  const incomingTerminal = isTerminalTimelineItem(incomingItem);
  if (existingTerminal !== incomingTerminal) return incomingTerminal ? incomingItem : existingItem;

  const existingTextLength = timelineItemTextLength(existingItem);
  const incomingTextLength = timelineItemTextLength(incomingItem);
  if (existingTextLength !== incomingTextLength) return incomingTextLength > existingTextLength ? incomingItem : existingItem;

  const existingTime = timelineItemSortTime(existingItem);
  const incomingTime = timelineItemSortTime(incomingItem);
  if (existingTime !== incomingTime) return incomingTime > existingTime ? incomingItem : existingItem;

  return incomingItem;
}

function earlierTimelineTime(left, right) {
  const leftTime = normalizeString(left?.time);
  const rightTime = normalizeString(right?.time);
  const leftTimestamp = normalizeTimestamp(leftTime);
  const rightTimestamp = normalizeTimestamp(rightTime);
  if (leftTimestamp > 0 && rightTimestamp > 0) return leftTimestamp <= rightTimestamp ? leftTime : rightTime;
  return leftTime || rightTime;
}

function mergeLifecycleTimelineItem(existingItem, incomingItem) {
  const preferred = preferredLifecycleTimelineItem(existingItem, incomingItem);
  const fallback = preferred === incomingItem ? existingItem : incomingItem;
  const status = normalizeString(preferred?.status) || normalizeString(fallback?.status);
  const text = normalizeString(preferred?.text) ? preferred.text : fallback.text;

  return {
    ...fallback,
    ...preferred,
    id: normalizeString(preferred?.id) || normalizeString(fallback?.id),
    callId: normalizeString(preferred?.callId) || normalizeString(fallback?.callId),
    text,
    title: normalizeString(preferred?.title) || normalizeString(fallback?.title),
    status,
    time: earlierTimelineTime(existingItem, incomingItem),
    completedAt: normalizeString(preferred?.completedAt) || normalizeString(fallback?.completedAt),
    done: normalizeTimelineDone(preferred, status),
    elapsedMs: preferred?.elapsedMs ?? fallback?.elapsedMs,
  };
}

function coalesceTimelineLifecycleItems(items = []) {
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

    output[existingIndex] = mergeLifecycleTimelineItem(output[existingIndex], item);
  }

  return output;
}

export function sameTimelineContent(left, right) {
  return left?.role === right?.role && normalizeTimelineKind(left) === normalizeTimelineKind(right) && normalizeString(left?.text) === normalizeString(right?.text);
}

export function compactTimelineText(value) {
  return normalizeString(value).replace(/\s+/g, '');
}

function looseTimelineText(value) {
  return compactTimelineText(value).toLowerCase().replace(/[^\p{L}\p{N}]+/gu, '');
}

export function sameTimelineContentCompact(left, right) {
  return (
    left?.role === right?.role &&
    normalizeTimelineKind(left) === normalizeTimelineKind(right) &&
    compactTimelineText(left?.text) &&
    compactTimelineText(left?.text) === compactTimelineText(right?.text)
  );
}

function looseTimelineShingleMatch(shorterText, longerText) {
  if (shorterText.length < RUNTIME_ASSISTANT_LOOSE_DUPLICATE_MIN_CHARS) return false;
  const shingleSize = Math.min(RUNTIME_ASSISTANT_LOOSE_DUPLICATE_SHINGLE_SIZE, Math.floor(shorterText.length / RUNTIME_ASSISTANT_LOOSE_DUPLICATE_MIN_MATCHES));
  if (shingleSize <= 0) return false;
  const shingles = new Set();
  for (let index = 0; index <= shorterText.length - shingleSize; index += shingleSize) {
    shingles.add(shorterText.slice(index, index + shingleSize));
  }
  shingles.add(shorterText.slice(-shingleSize));
  const candidates = [...shingles].filter(Boolean);
  if (candidates.length < RUNTIME_ASSISTANT_LOOSE_DUPLICATE_MIN_MATCHES) return false;
  const matches = candidates.filter((candidate) => longerText.includes(candidate)).length;
  return matches >= RUNTIME_ASSISTANT_LOOSE_DUPLICATE_MIN_MATCHES && (matches / candidates.length) >= RUNTIME_ASSISTANT_LOOSE_DUPLICATE_MIN_RATIO;
}

export function sameRuntimeAssistantContentLoose(left, right) {
  if (!left?.runtime && !right?.runtime) return false;
  if (left?.role !== right?.role || normalizeTimelineKind(left) !== normalizeTimelineKind(right)) return false;
  const leftText = looseTimelineText(left?.text);
  const rightText = looseTimelineText(right?.text);
  if (!leftText || !rightText) return false;
  const shorterText = leftText.length <= rightText.length ? leftText : rightText;
  const longerText = leftText.length > rightText.length ? leftText : rightText;
  if (shorterText.length < RUNTIME_ASSISTANT_LOOSE_DUPLICATE_MIN_CHARS) return false;
  return longerText.includes(shorterText) || looseTimelineShingleMatch(shorterText, longerText);
}

export function sameTimelineContentPrefix(left, right) {
  if (left?.role !== right?.role || normalizeTimelineKind(left) !== normalizeTimelineKind(right)) return false;
  const leftText = compactTimelineText(left?.text);
  const rightText = compactTimelineText(right?.text);
  const shorterLength = Math.min(leftText.length, rightText.length);
  if (shorterLength < RUNTIME_ASSISTANT_PREFIX_DUPLICATE_MIN_CHARS) return false;
  return leftText.startsWith(rightText) || rightText.startsWith(leftText);
}

function sameTimelineSubstring(left, right) {
  if (left?.role !== right?.role || normalizeTimelineKind(left) !== normalizeTimelineKind(right)) return false;
  const leftText = compactTimelineText(left?.text);
  const rightText = compactTimelineText(right?.text);
  if (!leftText || !rightText) return false;
  const shorterLength = Math.min(leftText.length, rightText.length);
  if (shorterLength < 15) return false;
  return leftText.includes(rightText) || rightText.includes(leftText);
}

function sameTimelineDuplicateContent(left, right) {
  return sameTimelineContent(left, right) || sameTimelineContentCompact(left, right) || sameRuntimeAssistantContentLoose(left, right) || sameTimelineSubstring(left, right);
}

function isInjectedPromptTimelineItem(item) {
  if (item?.role !== 'user') return false;
  const text = normalizeString(item?.text).trim();
  if (!text) return false;
  return /^#\s+AGENTS\.md instructions for .+\n/i.test(text) && /<INSTRUCTIONS>[\s\S]*<\/INSTRUCTIONS>/i.test(text);
}

function isMessageLifecycleTimelineItem(item) {
  if (item?.role) return false;
  return MESSAGE_LIFECYCLE_ITEM_TYPES.has(normalizeString(item?.itemType).toLowerCase());
}

function isToolBackedCommandTimelineItem(item) {
  return Boolean(normalizeString(item?.tool || item?.toolName || item?.tool_name));
}

function isMeaningfulCommandTimelineItem(item) {
  if (normalizeString(item?.command)) return true;
  if (normalizeString(item?.text || item?.output || item?.error)) return true;
  const title = normalizeString(item?.title).trim();
  return Boolean(title.startsWith('$ ') && !GENERIC_COMMAND_TITLES.has(title.toLowerCase()));
}

function isVisibleApprovalTimelineItem(item) {
  const requestId = positiveNumberFromFields(item, ['requestId', 'request_id']);
  const status = normalizeString(item?.status);
  return requestId > 0 && Boolean(status);
}

export function isVisibleTimelineItem(item) {
  if (item?.controlOnly) return false;
  if (isInjectedPromptTimelineItem(item)) return false;
  if (isMessageLifecycleTimelineItem(item)) return false;
  if (item?.role === 'user') return true;
  const kind = normalizeTimelineKind(item);
  if (kind === 'approval') return isVisibleApprovalTimelineItem(item);
  if (normalizeString(item?.text)) return true;
  if (kind === 'command') return !isToolBackedCommandTimelineItem(item) && isMeaningfulCommandTimelineItem(item);
  return kind === 'thinking' || kind === 'reasoning' || kind === 'tool' || kind === 'process' || kind === 'plan';
}

export function preferredAssistantTimelineItem(existingItem, incomingItem) {
  const isRuntime = Boolean(existingItem?.runtime || incomingItem?.runtime);
  if (existingItem?.runtime !== incomingItem?.runtime) {
    const base = incomingItem?.runtime ? existingItem : incomingItem;
    return isRuntime ? { ...base, runtime: true } : base;
  }
  return (
    normalizeString(incomingItem?.text).length > normalizeString(existingItem?.text).length
    ? incomingItem
    : existingItem
  );
}

export function dedupeAssistantTimelineItems(items = []) {
  const output = [];
  let lastUserIndex = -1;
  const seenIds = new Set();

  for (const item of items) {
    if (item?.role === 'user') {
      output.push(item);
      lastUserIndex = output.length - 1;
      continue;
    }

    if (item?.role !== 'assistant' || !compactTimelineText(item.text)) {
      output.push(item);
      continue;
    }

    if (item.id && seenIds.has(item.id)) continue;

    const duplicateIndices = [];
    for (let index = output.length - 1; index > lastUserIndex; index -= 1) {
      const candidate = output[index];
      if (candidate?.role === 'assistant' && sameTimelineDuplicateContent(candidate, item)) {
        duplicateIndices.push(index);
      }
    }

    if (duplicateIndices.length > 0) {
      let mergedItem = item;
      let anyDone = Boolean(item.done);
      for (const index of duplicateIndices) {
        if (output[index].done) anyDone = true;
        mergedItem = preferredAssistantTimelineItem(output[index], mergedItem);
      }
      if (anyDone) {
        mergedItem = { ...mergedItem, done: true };
      }
      const primaryIndex = duplicateIndices[duplicateIndices.length - 1];
      output[primaryIndex] = mergedItem;

      const indicesToRemove = new Set(duplicateIndices.slice(0, -1));
      if (indicesToRemove.size > 0) {
        let writeIdx = lastUserIndex + 1;
        for (let readIdx = lastUserIndex + 1; readIdx < output.length; readIdx++) {
          if (!indicesToRemove.has(readIdx)) {
            output[writeIdx] = output[readIdx];
            writeIdx++;
          }
        }
        output.length = writeIdx;
      }
      continue;
    }

    output.push(item);
    if (item.id) seenIds.add(item.id);
  }

  return output;
}

function areTimelineItemsEquivalent(left, right) {
  if (!left || !right) return left === right;
  if (left.id !== right.id) return false;
  if (left.role !== right.role) return false;
  if (left.kind !== right.kind) return false;

  const normText = (val) => (val || '').toString().trim();
  if (normText(left.text) !== normText(right.text)) return false;
  if (normText(left.status) !== normText(right.status)) return false;
  if (normText(left.completedAt) !== normText(right.completedAt)) return false;
  if (normText(left.title) !== normText(right.title)) return false;
  if (normText(left.command) !== normText(right.command)) return false;
  if (normText(left.toolName) !== normText(right.toolName)) return false;
  if (normText(left.callId) !== normText(right.callId)) return false;
  if (normText(left.time) !== normText(right.time)) return false;

  if (Boolean(left.done) !== Boolean(right.done)) return false;
  if (Boolean(left.optimistic) !== Boolean(right.optimistic)) return false;
  if (Boolean(left.runtime) !== Boolean(right.runtime)) return false;
  if (Boolean(left.controlOnly) !== Boolean(right.controlOnly)) return false;

  if ((left.elapsedMs ?? 0) !== (right.elapsedMs ?? 0)) return false;
  if ((left.requestId ?? 0) !== (right.requestId ?? 0)) return false;

  return true;
}

export function mergeTimelineItems(existingItems = [], incomingItems = [], options = {}) {
  const preserveExistingVisible = options?.preserveExistingVisible === true;
  const visibleIncomingItems = incomingItems.filter(isVisibleTimelineItem);
  const incomingById = new Map(visibleIncomingItems.map((item) => [item.id, item]));
  const uniqueIncomingItems = visibleIncomingItems.filter((item) => incomingById.get(item.id) === item);
  const incomingIds = new Set(incomingById.keys());

  const duplicateExistingByIncomingId = new Map();
  const duplicateExistingIds = new Set();

  for (const existingItem of existingItems) {
    if (incomingIds.has(existingItem.id)) continue;

    const duplicateIncoming = uniqueIncomingItems.find((incomingItem) =>
      sameTimelineDuplicateContent(existingItem, incomingItem)
    );

    if (duplicateIncoming) {
      duplicateExistingByIncomingId.set(duplicateIncoming.id, existingItem);
      duplicateExistingIds.add(existingItem.id);
    }
  }

  const consumedIncomingIds = new Set();
  const merged = [];

  for (const existingItem of existingItems) {
    if (duplicateExistingIds.has(existingItem.id)) {
      continue;
    }

    const replacement = incomingById.get(existingItem.id);
    if (replacement) {
      if (areTimelineItemsEquivalent(existingItem, replacement)) {
        merged.push(existingItem);
      } else {
        merged.push({
          ...replacement,
          attachments: replacement.attachments || existingItem.attachments,
        });
      }
      consumedIncomingIds.add(replacement.id);
      continue;
    }

    const shouldPreserveExistingMessage = (
      ((preserveExistingVisible && isVisibleTimelineItem(existingItem)) || existingItem.role === 'user' || existingItem.optimistic || existingItem.runtime)
    );
    if (shouldPreserveExistingMessage) {
      merged.push(existingItem);
    }
  }

  for (const incomingItem of uniqueIncomingItems) {
    if (!consumedIncomingIds.has(incomingItem.id)) {
      const duplicateExisting = duplicateExistingByIncomingId.get(incomingItem.id);
      const mergedIncoming = duplicateExisting && duplicateExisting.attachments
        ? { ...incomingItem, attachments: incomingItem.attachments || duplicateExisting.attachments }
        : incomingItem;

      merged.push(mergedIncoming);
    }
  }

  return dedupeAssistantTimelineItems(coalesceTimelineLifecycleItems(sortTimelineChronologically(merged)));
}
