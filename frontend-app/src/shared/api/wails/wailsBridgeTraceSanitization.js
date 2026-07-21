import {
  FRONTEND_PERFORMANCE_TRACE_PHASES,
  FRONTEND_RUNTIME_TRACE_DEFAULT_PHASES,
  FRONTEND_TRACE_ALLOWED_METADATA_KEYS,
  FRONTEND_TRACE_ALLOWED_PHASES,
  FRONTEND_TRACE_ALLOWED_STATUSES,
  FRONTEND_TRACE_FORBIDDEN_KEYS,
  FRONTEND_TRACE_RPC_SLOW_MS,
  FRONTEND_TRACE_SENSITIVE_TEXT_PATTERNS,
} from "./wailsBridgeConstants.js";
import { optionalDiagnosticString } from "./wailsBridgeLogRuntime.js";
import { createFrontendTraceTimestamp } from "./wailsBridgeTraceContext.js";
import { requiredAppStoragePort } from "../browser/browserStorage.js";

/** @typedef {Record<string, unknown>} TraceRecord */
/** @typedef {string | number | boolean | null} TraceJSONScalar */
/** @typedef {Record<string, TraceJSONScalar>} TraceMetadata */
/**
 * @typedef {
 *   | "trace_id"
 *   | "span_id"
 *   | "parent_span_id"
 *   | "method"
 *   | "thread_id"
 *   | "agent_id"
 *   | "turn_id"
 *   | "call_id"
 *   | "client_kind"
 *   | "client_route"
 * } TraceStringField
 */
/**
 * @typedef {{
 *   ts: string,
 *   phase: string,
 *   status: string,
 *   duration_ms?: number,
 *   error?: string,
 *   metadata?: TraceMetadata,
 * } & Partial<Record<TraceStringField, string>>} SanitizedFrontendTraceEvent
 */

function isUITestMCPTraceSuppressed() {
  const env = /** @type {ImportMeta & { env: Record<string, unknown> }} */ (
    import.meta
  ).env;
  return env.PROD !== true && env.VITE_SUPER_DOLPHIN_UI_TEST_MCP === "1";
}

function isFrontendTraceDebugEnabled() {
  if (typeof window === "undefined") return false;
  if (
    /** @type {Window & { __AO_FRONTEND_TRACE_DEBUG__?: boolean }} */ (window)
      .__AO_FRONTEND_TRACE_DEBUG__ === true
  )
    return true;
  try {
    return (
      requiredAppStoragePort("frontend trace storage").get(
        "observability.frontend.debug",
      ) === "true"
    );
  } catch {
    return false;
  }
}

/** @param {unknown} value @param {number} [limit] */
function safeTraceString(value, limit = 160) {
  const text = optionalDiagnosticString(value).trim();
  if (!text) return "";
  return text.length > limit ? `${text.slice(0, limit)}…` : text;
}

/** @param {string} text */
function containsForbiddenTraceText(text) {
  const value = safeTraceString(text, 512);
  const normalized = value.toLowerCase();
  if (!normalized) return false;
  if (
    FRONTEND_TRACE_SENSITIVE_TEXT_PATTERNS.some((pattern) =>
      pattern.test(value),
    )
  )
    return true;
  for (const key of FRONTEND_TRACE_FORBIDDEN_KEYS) {
    const token = key.toLowerCase();
    if (
      normalized.includes(token) ||
      normalized.includes(token.replaceAll("_", " "))
    )
      return true;
  }
  return false;
}

/** @param {unknown} value @param {number} [limit] */
function safeTraceDiagnosticToken(value, limit = 80) {
  const text = safeTraceString(value, limit);
  return !text || containsForbiddenTraceText(text) ? "" : text;
}

/** @param {unknown} error */
function safeTraceErrorMessage(error) {
  const value =
    error && (typeof error === "object" || typeof error === "function")
      ? /** @type {TraceRecord} */ (error)
      : {};
  const code = safeTraceDiagnosticToken(value.code, 80);
  const name = safeTraceDiagnosticToken(value.name, 80);
  const message = safeTraceString(value.message, 240);
  const safeMessage = containsForbiddenTraceText(message) ? "" : message;
  if (code && safeMessage) return `${code}: ${safeMessage}`;
  if (safeMessage) return safeMessage;
  return code || name || "Error";
}

/** @param {unknown} value */
function safeTraceErrorValue(value) {
  if (value instanceof Error || (value && typeof value === "object"))
    return safeTraceErrorMessage(value);
  const message = safeTraceString(value, 240);
  if (!message || containsForbiddenTraceText(message)) return "";
  return safeTraceString(message, 120);
}

/** @param {unknown} metadata @returns {TraceMetadata | undefined} */
function safeTraceMetadata(metadata) {
  if (!metadata || typeof metadata !== "object" || Array.isArray(metadata))
    return undefined;
  const out = /** @type {TraceMetadata} */ ({});
  for (const [key, value] of Object.entries(metadata)) {
    if (
      !FRONTEND_TRACE_ALLOWED_METADATA_KEYS.has(key) ||
      FRONTEND_TRACE_FORBIDDEN_KEYS.has(key)
    )
      continue;
    if (value === undefined || value === null || value === "") continue;
    if (
      typeof value === "string" ||
      typeof value === "number" ||
      typeof value === "boolean"
    )
      out[key] = typeof value === "string" ? safeTraceString(value) : value;
  }
  return Object.keys(out).length > 0 ? out : undefined;
}

/** @param {unknown} event @returns {SanitizedFrontendTraceEvent | null} */
function sanitizeFrontendTraceEvent(event) {
  if (!event || typeof event !== "object" || Array.isArray(event)) return null;
  const source = /** @type {TraceRecord} */ (event);
  const phase = safeTraceString(source.phase);
  if (!FRONTEND_TRACE_ALLOWED_PHASES.has(phase)) return null;
  const status = safeTraceString(source.status).toLowerCase();
  if (!FRONTEND_TRACE_ALLOWED_STATUSES.has(status)) return null;
  if (FRONTEND_PERFORMANCE_TRACE_PHASES.has(phase)) {
    const expectedStatus =
      phase === "frontend.performance.capability_absent" ? "ok" : "slow";
    if (status !== expectedStatus) return null;
  }
  const durationMS = Number(source.duration_ms);
  const out = /** @type {SanitizedFrontendTraceEvent} */ ({
    ts: createFrontendTraceTimestamp(),
    phase,
    status,
  });
  /** @type {Array<[TraceStringField, TraceStringField, number]>} */
  const traceStringFields = [
    ["trace_id", "trace_id", 64],
    ["span_id", "span_id", 32],
    ["parent_span_id", "parent_span_id", 32],
    ["method", "method", 160],
    ["thread_id", "thread_id", 160],
    ["agent_id", "agent_id", 160],
    ["turn_id", "turn_id", 160],
    ["call_id", "call_id", 160],
    ["client_kind", "client_kind", 80],
    ["client_route", "client_route", 240],
  ];
  for (const [target, sourceKey, limit] of traceStringFields) {
    const value = safeTraceString(source[sourceKey], limit);
    if (value) out[target] = value;
  }
  if (Number.isFinite(durationMS) && durationMS >= 0)
    out.duration_ms = Math.round(durationMS);
  if (out.status === "error") {
    const error = safeTraceErrorValue(source.error);
    if (error) out.error = error;
  }
  const metadata = safeTraceMetadata(source.metadata);
  if (metadata) out.metadata = metadata;
  return out;
}

/** @param {SanitizedFrontendTraceEvent | null} event */
function shouldRemoteFlushFrontendTrace(event) {
  if (!event || isUITestMCPTraceSuppressed()) return false;
  if (event.status === "error" || event.status === "slow") return true;
  if (FRONTEND_RUNTIME_TRACE_DEFAULT_PHASES.has(event.phase)) return true;
  if (
    event.phase === "frontend.patch.apply.slow" ||
    event.phase === "frontend.render.slow"
  )
    return true;
  if (FRONTEND_PERFORMANCE_TRACE_PHASES.has(event.phase)) return true;
  if (
    event.phase === "frontend.rpc.done" &&
    Number(event.duration_ms) >= FRONTEND_TRACE_RPC_SLOW_MS
  )
    return true;
  return isFrontendTraceDebugEnabled();
}

export {
  isUITestMCPTraceSuppressed,
  isFrontendTraceDebugEnabled,
  safeTraceErrorMessage,
  sanitizeFrontendTraceEvent,
  shouldRemoteFlushFrontendTrace,
};
