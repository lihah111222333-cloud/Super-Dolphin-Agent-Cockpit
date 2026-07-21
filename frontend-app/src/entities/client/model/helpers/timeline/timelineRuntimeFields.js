import {
  firstOptionalPresent,
  normalizeOptionalTextField,
  optionalTextField,
  parseOptionalTimestamp,
} from "../../contractStoreModel.js";
import { RUNTIME_TOOL_TERMINAL_STATUSES } from "../../runtimeResults.js";

export const RUNTIME_ASSISTANT_PREFIX_DUPLICATE_MIN_CHARS = 24;
export const RUNTIME_ASSISTANT_LOOSE_DUPLICATE_MIN_CHARS = 80;
export const RUNTIME_ASSISTANT_LOOSE_DUPLICATE_SHINGLE_SIZE = 12;
export const RUNTIME_ASSISTANT_LOOSE_DUPLICATE_MIN_MATCHES = 4;
export const RUNTIME_ASSISTANT_LOOSE_DUPLICATE_MIN_RATIO = 0.65;

const TIMELINE_TERMINAL_STATUSES = new Set([
  ...RUNTIME_TOOL_TERMINAL_STATUSES,
  "skipped",
  "cancelled",
  "canceled",
  "aborted",
]);

export function normalizeString(value) {
  return normalizeOptionalTextField(value);
}

export function objectRecord(value) {
  return value && typeof value === "object" && !Array.isArray(value)
    ? value
    : {};
}

/**
 * @param {Record<string, unknown>} source
 * @param {readonly string[]} keys
 */
export function firstFieldValue(source, keys = []) {
  const record = objectRecord(source);
  for (const key of keys) {
    const value = record[key];
    if (value !== undefined && value !== null && value !== "") return value;
  }
  return undefined;
}

/**
 * @param {Record<string, unknown>} source
 * @param {readonly string[]} keys
 */
export function positiveNumberFromFields(source, keys = []) {
  const numeric = Number(firstFieldValue(source, keys));
  return Math.max(0, Number.isFinite(numeric) ? numeric : 0);
}

export function normalizeTimestamp(value) {
  if (typeof value === "boolean" || value === null || value === undefined)
    return 0;
  if (typeof value === "number")
    return Number.isFinite(value) && value > 0 ? value : 0;
  const text = normalizeString(value);
  if (!text) return 0;
  const asNumber = Number(text);
  if (Number.isFinite(asNumber) && asNumber > 0) return asNumber;
  const sanitized = text.replace(/(\.\d{3})\d+/g, "$1");
  const parsed = parseOptionalTimestamp(sanitized);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : 0;
}

export function extractText(value) {
  if (value === null || value === undefined) return "";
  if (
    typeof value === "string" ||
    typeof value === "number" ||
    typeof value === "boolean"
  )
    return normalizeString(value);
  if (Array.isArray(value))
    return value
      .map((item) => extractText(item))
      .filter(Boolean)
      .join("\n");
  if (typeof value === "object")
    return extractText(
      value.text ||
        value.content ||
        value.message ||
        value.delta ||
        value.output ||
        value.result ||
        value.answer ||
        value.response,
    );
  return "";
}

export function normalizeTimelineDone(item, status) {
  const normalizedStatus = normalizeString(status).toLowerCase();
  if (TIMELINE_TERMINAL_STATUSES.has(normalizedStatus)) return true;
  if (typeof item?.done === "boolean") return item.done;
  if (!normalizedStatus) return true;
  return false;
}

export function normalizeTimelineKind(item) {
  const kind = normalizeString(item?.kind).toLowerCase();
  if (kind) return kind;
  return item?.role === "user" ? "user" : "assistant";
}

export function compactTimelineText(value) {
  return normalizeString(value).replace(/\s+/g, "");
}

export function optionalTimelineText(value) {
  return optionalTextField(value).trim();
}

export { firstOptionalPresent };
