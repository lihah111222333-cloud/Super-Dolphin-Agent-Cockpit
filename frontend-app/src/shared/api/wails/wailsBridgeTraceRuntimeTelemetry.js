import { FRONTEND_RUNTIME_TRACE_SKIP_METHODS } from "./wailsBridgeConstants.js";
import { writeBridgeLog } from "./wailsBridgeLogRuntime.js";
import {
  isFrontendTraceDebugEnabled,
  sanitizeFrontendTraceEvent,
} from "./wailsBridgeTraceSanitization.js";
import { emitFrontendTraceEvent } from "./wailsBridgeTraceTransport.js";

/** @typedef {Record<string, unknown>} RuntimeTelemetryRecord */
/**
 * @typedef {{
 *   phase?: unknown,
 *   method?: unknown,
 *   trace_id?: unknown,
 *   span_id?: unknown,
 *   call_id?: unknown,
 *   duration_ms?: unknown,
 *   status?: unknown,
 *   error?: unknown,
 *   metadata?: RuntimeTelemetryRecord,
 * }} RuntimeTelemetryTraceEvent
 */
/**
 * @typedef {{ ts: string, phase: string, status: string, method?: string }} RuntimeTelemetrySanitizedEvent
 */
/**
 * @typedef {((event: unknown) => void) & {
 *   __AO_BRIDGE_RUNTIME_TELEMETRY__?: boolean,
 *   __AO_PREVIOUS_RUNTIME_TELEMETRY__?: RuntimeTelemetryHook | null,
 * }} RuntimeTelemetryHook
 */

/** @param {unknown} value @returns {RuntimeTelemetryRecord | null} */
function runtimeTelemetryRecord(value) {
  if (!value || typeof value !== "object" || Array.isArray(value)) return null;
  return /** @type {RuntimeTelemetryRecord} */ (value);
}

/** @param {unknown} event @param {string} key @returns {unknown} */
function runtimeTelemetryMetadataValue(event, key) {
  const telemetryEvent = runtimeTelemetryRecord(event);
  if (!telemetryEvent) return undefined;
  if (Object.prototype.hasOwnProperty.call(telemetryEvent, key))
    return telemetryEvent[key];
  const metadata = runtimeTelemetryRecord(telemetryEvent.metadata);
  if (!metadata || !Object.prototype.hasOwnProperty.call(metadata, key))
    return undefined;
  return metadata[key];
}

/** @param {unknown} event @returns {RuntimeTelemetryRecord | undefined} */
function runtimeTelemetryMetadata(event) {
  /** @type {RuntimeTelemetryRecord} */
  const metadata = {};
  for (const key of ["req_id", "pending_count", "attempt"]) {
    const value = runtimeTelemetryMetadataValue(event, key);
    if (value !== undefined && value !== null && value !== "")
      metadata[key] = value;
  }
  return Object.keys(metadata).length > 0 ? metadata : undefined;
}

/** @param {unknown} event @returns {RuntimeTelemetryTraceEvent | null} */
function runtimeTelemetryTraceEvent(event) {
  if (!event || typeof event !== "object" || Array.isArray(event)) return null;
  const telemetryEvent = /** @type {RuntimeTelemetryRecord} */ (event);
  return {
    phase: telemetryEvent.phase,
    method: telemetryEvent.method,
    trace_id: telemetryEvent.trace_id,
    span_id: telemetryEvent.span_id,
    call_id: telemetryEvent.call_id,
    duration_ms: telemetryEvent.duration_ms,
    status: telemetryEvent.status,
    error: telemetryEvent.error,
    metadata: runtimeTelemetryMetadata(event),
  };
}

/** @param {unknown} event */
function handleRuntimeTelemetryEvent(event) {
  const traceEvent = runtimeTelemetryTraceEvent(event);
  const sanitized = /** @type {RuntimeTelemetrySanitizedEvent | null} */ (
    sanitizeFrontendTraceEvent(traceEvent)
  );
  if (!sanitized) return;
  if (
    sanitized.status === "error" ||
    sanitized.status === "slow" ||
    isFrontendTraceDebugEnabled()
  ) {
    writeBridgeLog(
      sanitized.status === "error" || sanitized.status === "slow"
        ? "warn"
        : "debug",
      "runtime.rpc.telemetry",
      sanitized,
    );
  }
  const method =
    typeof sanitized.method === "string" ? sanitized.method : undefined;
  if (method && FRONTEND_RUNTIME_TRACE_SKIP_METHODS.has(method)) return;
  emitFrontendTraceEvent(traceEvent);
}

/** @param {unknown} error */
function logRuntimeTelemetryExternalHookFailed(error) {
  writeBridgeLog("warn", "runtime.rpc.telemetry.external_hook_failed", {
    error,
  });
}

/** @param {RuntimeTelemetryHook | null} externalHook @param {unknown} event */
function invokeRuntimeTelemetryExternalHook(externalHook, event) {
  if (typeof externalHook !== "function") return;
  try {
    externalHook(event);
  } catch (error) {
    logRuntimeTelemetryExternalHookFailed(error);
  }
}

function installRuntimeTelemetryHook() {
  if (typeof window === "undefined") return;
  const runtimeWindow = /** @type {Window & {
   * __AO_WAILS_RUNTIME_TELEMETRY__?: RuntimeTelemetryHook,
   * }} */ (window);
  const currentHook =
    typeof runtimeWindow.__AO_WAILS_RUNTIME_TELEMETRY__ === "function"
      ? runtimeWindow.__AO_WAILS_RUNTIME_TELEMETRY__
      : null;
  let externalHook = currentHook;
  if (currentHook?.__AO_BRIDGE_RUNTIME_TELEMETRY__ === true) {
    externalHook = currentHook.__AO_PREVIOUS_RUNTIME_TELEMETRY__ ?? null;
  }
  /** @type {RuntimeTelemetryHook} */
  const hook = (event) => {
    invokeRuntimeTelemetryExternalHook(externalHook, event);
    handleRuntimeTelemetryEvent(event);
  };
  hook.__AO_BRIDGE_RUNTIME_TELEMETRY__ = true;
  hook.__AO_PREVIOUS_RUNTIME_TELEMETRY__ = externalHook;
  runtimeWindow.__AO_WAILS_RUNTIME_TELEMETRY__ = hook;
}

export { installRuntimeTelemetryHook };
