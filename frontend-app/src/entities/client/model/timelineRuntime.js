// @ts-check

import { approvalIdentityFromFields } from "../../../shared/api/support/approvalRequestId.js";
import {
  currentIsoTimestamp as currentIsoTimestampContract,
  systemClockMillis as systemClockMillisContract,
} from "./contractStoreModel.js";
import {
  dedupeAssistantTimelineItems,
  sameRuntimeAssistantContentLoose,
  sameTimelineContent,
  sameTimelineContentCompact,
  sameTimelineContentPrefix,
  sameTimelineDuplicateContent,
} from "./helpers/timeline/timelineRuntimeContent.js";
import { coalesceTimelineLifecycleItems } from "./helpers/timeline/timelineRuntimeLifecycle.js";
import {
  compactTimelineText,
  extractText,
  firstFieldValue,
  firstOptionalPresent,
  normalizeString,
  normalizeTimelineDone,
  normalizeTimestamp,
  positiveNumberFromFields,
} from "./helpers/timeline/timelineRuntimeFields.js";
import { isVisibleTimelineItem } from "./helpers/timeline/timelineRuntimeVisibility.js";

const currentIsoTimestamp = /** @type {(label?: string) => string} */ (
  currentIsoTimestampContract
);
const systemClockMillis = /** @type {(label?: string) => number} */ (
  systemClockMillisContract
);

const IMAGE_PLACEHOLDER_RE = /<image\s[^>]*><\/image>/gi;
const TIMELINE_KIND_KEYS = Object.freeze([
  "kind",
  "type",
  "eventType",
  "event_type",
  "role",
]);
const TIMELINE_ROLE_KEYS = Object.freeze([
  "role",
  "kind",
  "type",
  "eventType",
  "event_type",
]);
const TIMELINE_TEXT_KEYS = Object.freeze([
  "text",
  "content",
  "message",
  "delta",
  "output",
  "result",
  "answer",
  "response",
  "summary",
  "preview",
  "error",
]);
const TIMELINE_ID_KEYS = Object.freeze(["id", "messageId", "message_id"]);
const TIMELINE_TITLE_KEYS = Object.freeze([
  "title",
  "label",
  "name",
  "tool",
  "toolName",
  "command",
]);
const TIMELINE_TIME_KEYS = Object.freeze([
  "time",
  "startedAt",
  "started_at",
  "ts",
  "createdAt",
  "created_at",
]);
const TIMELINE_COMPLETED_KEYS = Object.freeze([
  "completedAt",
  "completed_at",
  "finishedAt",
  "finished_at",
]);
const TIMELINE_CALL_ID_KEYS = Object.freeze([
  "callId",
  "call_id",
  "toolCallId",
  "tool_call_id",
]);
const USER_CONTROL_ENVELOPE_TAGS = Object.freeze([
  "turn_aborted",
  "hook_prompt",
]);

/** @param {string} rawRole @param {string} rawKind */
function normalizeTimelineKindFromRaw(rawRole, rawKind) {
  if (rawRole.includes("user")) return "user";
  if (rawKind.includes("approval")) return "approval";
  if (rawKind.includes("thinking") || rawKind.includes("reasoning"))
    return "thinking";
  if (rawKind.includes("command") || rawKind.includes("exec")) return "command";
  if (rawKind.includes("tool")) return "tool";
  if (rawKind.includes("plan")) return "plan";
  return "assistant";
}

/** @param {Record<string, unknown>} item */
function normalizeTimelineElapsedMs(item) {
  if (item?.elapsedMs !== undefined) return Number(item.elapsedMs);
  if (item?.elapsed_ms !== undefined) return Number(item.elapsed_ms);
  if (item?.durationMs !== undefined) return Number(item.durationMs);
  if (item?.duration_ms !== undefined) return Number(item.duration_ms);
  return undefined;
}

/** @param {string} text */
function normalizeUserTimelineText(text) {
  let processed = text;
  if (processed) processed = processed.replace(IMAGE_PLACEHOLDER_RE, "").trim();
  let remaining = normalizeString(processed).trim();
  let strippedControlEnvelope = false;
  while (remaining) {
    const lower = remaining.toLowerCase();
    const tagName = USER_CONTROL_ENVELOPE_TAGS.find((tag) =>
      lower.startsWith(`<${tag}`),
    );
    if (!tagName) break;
    strippedControlEnvelope = true;
    const closeTag = `</${tagName}>`;
    const closeIndex = lower.indexOf(closeTag);
    remaining =
      closeIndex >= 0
        ? remaining.slice(closeIndex + closeTag.length).trimStart()
        : "";
  }
  return {
    text: strippedControlEnvelope ? remaining : processed,
    controlOnly: strippedControlEnvelope && !remaining,
  };
}

/** @param {Record<string, unknown>} item */
export function normalizeTimelineItem(item) {
  const rawKind = normalizeString(
    firstFieldValue(item, TIMELINE_KIND_KEYS),
  ).toLowerCase();
  const rawRole = normalizeString(
    firstFieldValue(item, TIMELINE_ROLE_KEYS),
  ).toLowerCase();
  const normalizedRole = rawRole.includes("user") ? "user" : "assistant";
  const normalizedKind = normalizeTimelineKindFromRaw(rawRole, rawKind);
  const rawText = extractText(firstFieldValue(item, TIMELINE_TEXT_KEYS));
  const userText =
    normalizedRole === "user"
      ? normalizeUserTimelineText(rawText)
      : { text: rawText, controlOnly: false };
  const status = normalizeString(item?.status);
  const approvalIdentity =
    normalizedKind === "approval"
      ? approvalIdentityFromFields(item, "timeline approval")
      : null;
  const turnId = normalizeString(item?.turnId);
  return {
    id:
      normalizeString(firstFieldValue(item, TIMELINE_ID_KEYS)) ||
      `${normalizedRole}-${systemClockMillis()}`,
    role: normalizedRole,
    kind: normalizedKind,
    text: userText.text,
    controlOnly: userText.controlOnly,
    title: normalizeString(firstFieldValue(item, TIMELINE_TITLE_KEYS)),
    sessionScope:
      approvalIdentity === null ? "" : approvalIdentity.sessionScope,
    callId: approvalIdentity
      ? approvalIdentity.callId
      : normalizeString(firstFieldValue(item, TIMELINE_CALL_ID_KEYS)),
    requestId: approvalIdentity
      ? approvalIdentity.requestId
      : positiveNumberFromFields(item, ["requestId", "request_id"]),
    command: normalizeString(item?.command),
    toolName: normalizeString(
      firstOptionalPresent(item?.tool, item?.toolName, item?.tool_name),
    ),
    status,
    time:
      normalizeString(firstFieldValue(item, TIMELINE_TIME_KEYS)) ||
      currentIsoTimestamp(),
    completedAt: normalizeString(
      firstFieldValue(item, TIMELINE_COMPLETED_KEYS),
    ),
    done: normalizeTimelineDone(item, status),
    optimistic: Boolean(item?.optimistic),
    elapsedMs: normalizeTimelineElapsedMs(item),
    ...(turnId ? { turnId } : {}),
  };
}

/** @param {Record<string, unknown>[]} items */
export function sortTimelineChronologically(items = []) {
  return [...items]
    .map((item, index) => ({
      item,
      index,
      timestamp: normalizeTimestamp(item?.time),
    }))
    .sort((left, right) =>
      left.timestamp !== right.timestamp
        ? left.timestamp - right.timestamp
        : left.index - right.index,
    )
    .map(({ item }) => item);
}

/** @param {Record<string, unknown>} left @param {Record<string, unknown>} right */
function areTimelineItemsEquivalent(left, right) {
  if (!left || !right) return left === right;
  if (
    left.id !== right.id ||
    left.role !== right.role ||
    left.kind !== right.kind
  )
    return false;
  for (const field of [
    "text",
    "status",
    "completedAt",
    "title",
    "command",
    "toolName",
    "sessionScope",
    "callId",
    "time",
  ]) {
    if (
      normalizeString(left[field]).trim() !==
      normalizeString(right[field]).trim()
    )
      return false;
  }
  return (
    Boolean(left.done) === Boolean(right.done) &&
    Boolean(left.optimistic) === Boolean(right.optimistic) &&
    Boolean(left.runtime) === Boolean(right.runtime) &&
    Boolean(left.controlOnly) === Boolean(right.controlOnly) &&
    (left.elapsedMs ?? 0) === (right.elapsedMs ?? 0) &&
    (left.requestId ?? 0) === (right.requestId ?? 0)
  );
}

/** @param {Record<string, unknown>} existingItem @param {Record<string, unknown>} replacement */
function mergedTimelineReplacement(existingItem, replacement) {
  return areTimelineItemsEquivalent(existingItem, replacement)
    ? existingItem
    : {
        ...replacement,
        attachments: replacement.attachments || existingItem.attachments,
      };
}

/**
 * @param {Record<string, unknown>[]} existingItems
 * @param {Record<string, unknown>[]} incomingItems
 * @param {{ preserveExistingVisible?: boolean }} options
 */
export function mergeTimelineItems(
  existingItems = [],
  incomingItems = [],
  options = {},
) {
  const preserveExistingVisible = options?.preserveExistingVisible === true;
  const visibleIncomingItems = incomingItems.filter(isVisibleTimelineItem);
  const incomingById = new Map(
    visibleIncomingItems.map((item) => [item.id, item]),
  );
  const uniqueIncomingItems = visibleIncomingItems.filter(
    (item) => incomingById.get(item.id) === item,
  );
  const incomingIds = new Set(incomingById.keys());
  const duplicateExistingByIncomingId = new Map();
  const duplicateExistingIds = new Set();
  for (const existingItem of existingItems) {
    if (incomingIds.has(existingItem.id)) continue;
    const duplicateIncoming = uniqueIncomingItems.find((incomingItem) =>
      sameTimelineDuplicateContent(existingItem, incomingItem),
    );
    if (duplicateIncoming) {
      duplicateExistingByIncomingId.set(duplicateIncoming.id, existingItem);
      duplicateExistingIds.add(existingItem.id);
    }
  }
  const consumedIncomingIds = new Set();
  const merged = [];
  for (const existingItem of existingItems) {
    if (duplicateExistingIds.has(existingItem.id)) continue;
    const replacement = incomingById.get(existingItem.id);
    if (replacement) {
      merged.push(mergedTimelineReplacement(existingItem, replacement));
      consumedIncomingIds.add(replacement.id);
    } else if (
      (preserveExistingVisible && isVisibleTimelineItem(existingItem)) ||
      existingItem.role === "user" ||
      existingItem.optimistic ||
      existingItem.runtime
    ) {
      merged.push(existingItem);
    }
  }
  for (const incomingItem of uniqueIncomingItems) {
    if (!consumedIncomingIds.has(incomingItem.id)) {
      const duplicateExisting = duplicateExistingByIncomingId.get(
        incomingItem.id,
      );
      merged.push(
        duplicateExisting?.attachments
          ? {
              ...incomingItem,
              attachments:
                incomingItem.attachments || duplicateExisting.attachments,
            }
          : incomingItem,
      );
    }
  }
  return dedupeAssistantTimelineItems(
    coalesceTimelineLifecycleItems(sortTimelineChronologically(merged)),
  );
}

export {
  compactTimelineText,
  dedupeAssistantTimelineItems,
  isVisibleTimelineItem,
  sameRuntimeAssistantContentLoose,
  sameTimelineContent,
  sameTimelineContentCompact,
  sameTimelineContentPrefix,
};
