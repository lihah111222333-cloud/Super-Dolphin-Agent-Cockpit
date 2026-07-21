import {
  FRONTEND_TRACE_BATCH_LIMIT,
  FRONTEND_TRACE_INGEST_METHOD,
  FRONTEND_TRACE_QUEUE_LIMIT,
  METHOD_IDS,
} from "./wailsBridgeConstants.js";
import { waitRuntime, writeBridgeLog } from "./wailsBridgeLogRuntime.js";
import {
  safeTraceErrorMessage,
  sanitizeFrontendTraceEvent,
  shouldRemoteFlushFrontendTrace,
} from "./wailsBridgeTraceSanitization.js";

/** @typedef {Record<string, unknown>} FrontendTraceEvent */
/**
 * @typedef {{
 *   recorded: number,
 *   dropped?: number,
 *   enabled?: boolean,
 *   disabled_reason?: string,
 * }} FrontendTraceACK
 */
/** @typedef {{ ByID: (methodID: number, method: string, payload: { events: FrontendTraceEvent[] }) => Promise<unknown> }} WailsRuntimeCall */
/** @typedef {{ Call?: WailsRuntimeCall }} WailsRuntime */
/** @typedef {{ flush?: boolean }} FrontendTraceEmitOptions */

/** @type {FrontendTraceEvent[]} */
let frontendTraceQueue = [];
let frontendTraceFlushScheduled = false;
let frontendTraceFlushInFlight = false;
/** @type {FrontendTraceEvent[] | null} */
let frontendTracePendingBatch = null;
/** @type {ReturnType<typeof setTimeout> | null} */
let frontendTraceRetryTimer = null;
let frontendTraceRetryAttempt = 0;
let frontendTraceRetryDelayMS = 0;
let frontendTraceDisabled = false;
let frontendTraceDisabledReason = "";
let frontendTraceConsecutiveFailures = 0;
const frontendTraceHealth = {
  accepted: 0,
  acknowledged: 0,
  serverDropped: 0,
  failures: 0,
  flushFailureWarnings: 0,
  malformedACKs: 0,
  overflowDropped: 0,
  overflowWarnings: 0,
  terminalDropped: 0,
  lastFailure: "",
};
const FRONTEND_TRACE_RETRY_BASE_MS = 100;
const FRONTEND_TRACE_RETRY_MAX_MS = 5000;
const FRONTEND_TRACE_OVERFLOW_WARN_DROP_INTERVAL = 100;
const FRONTEND_TRACE_FLUSH_FAILURE_WARN_INTERVAL = 5;

function frontendTraceQueuedCount() {
  return frontendTraceQueue.length + (frontendTracePendingBatch?.length || 0);
}
function getFrontendTraceQueueHealth() {
  return {
    ...frontendTraceHealth,
    queueLength: frontendTraceQueuedCount(),
    consecutiveFlushFailures: frontendTraceConsecutiveFailures,
    retryPending: frontendTraceRetryTimer !== null,
    retryAttempt: frontendTraceRetryAttempt,
    retryDelayMS: frontendTraceRetryDelayMS,
    disabled: frontendTraceDisabled,
    disabledReason: frontendTraceDisabledReason,
  };
}
function clearFrontendTraceRetry() {
  if (frontendTraceRetryTimer !== null) {
    clearTimeout(frontendTraceRetryTimer);
    frontendTraceRetryTimer = null;
  }
  frontendTraceRetryAttempt = 0;
  frontendTraceRetryDelayMS = 0;
}
function scheduleFrontendTraceRetry() {
  if (
    frontendTraceDisabled ||
    frontendTraceRetryTimer !== null ||
    frontendTraceQueuedCount() === 0
  )
    return;
  frontendTraceRetryAttempt += 1;
  frontendTraceRetryDelayMS = Math.min(
    FRONTEND_TRACE_RETRY_BASE_MS * 2 ** (frontendTraceRetryAttempt - 1),
    FRONTEND_TRACE_RETRY_MAX_MS,
  );
  frontendTraceRetryTimer = setTimeout(() => {
    frontendTraceRetryTimer = null;
    frontendTraceRetryDelayMS = 0;
    scheduleFrontendTraceFlush();
  }, frontendTraceRetryDelayMS);
}
/**
 * @param {unknown} response
 * @param {number} batchLength
 * @returns {"malformed" | "disabled" | "acknowledged"}
 */
function classifyFrontendTraceACK(response, batchLength) {
  if (!response || typeof response !== "object" || Array.isArray(response))
    return "malformed";
  const value = /** @type {Record<string, unknown>} */ (response);
  const recorded = value.recorded;
  const dropped = value.dropped ?? 0;
  if (
    typeof recorded !== "number" ||
    typeof dropped !== "number" ||
    !Number.isInteger(recorded) ||
    recorded < 0 ||
    !Number.isInteger(dropped) ||
    dropped < 0
  )
    return "malformed";
  if (value.enabled === false) {
    const disabledReason =
      typeof value.disabled_reason === "string"
        ? value.disabled_reason.trim()
        : "";
    return recorded === 0 && dropped === batchLength && disabledReason
      ? "disabled"
      : "malformed";
  }
  if (value.enabled !== undefined && value.enabled !== true) return "malformed";
  return recorded + dropped === batchLength ? "acknowledged" : "malformed";
}
/** @param {number} count */
function logFrontendTraceFlushFailure(count) {
  frontendTraceConsecutiveFailures += 1;
  if (
    frontendTraceConsecutiveFailures !== 1 &&
    frontendTraceConsecutiveFailures %
      FRONTEND_TRACE_FLUSH_FAILURE_WARN_INTERVAL !==
      0
  )
    return;
  frontendTraceHealth.flushFailureWarnings += 1;
  writeBridgeLog("warn", "frontend.trace.flush.failed", {
    failures: frontendTraceHealth.failures,
    count,
    retryAttempt: frontendTraceRetryAttempt,
  });
}
async function flushFrontendTraceQueue() {
  frontendTraceFlushScheduled = false;
  if (
    frontendTraceDisabled ||
    frontendTraceFlushInFlight ||
    frontendTraceQueuedCount() === 0
  )
    return;
  frontendTraceFlushInFlight = true;
  if (!frontendTracePendingBatch)
    frontendTracePendingBatch = frontendTraceQueue.splice(
      0,
      FRONTEND_TRACE_BATCH_LIMIT,
    );
  const batch = frontendTracePendingBatch;
  let shouldRetry = false;
  try {
    const runtime = /** @type {WailsRuntime | null} */ (await waitRuntime());
    const call = runtime?.Call;
    if (!call || typeof call.ByID !== "function")
      throw new Error("runtime Call.ByID is unavailable");
    const response = await call.ByID(
      METHOD_IDS.CALL_API,
      FRONTEND_TRACE_INGEST_METHOD,
      { events: batch },
    );
    const ack = classifyFrontendTraceACK(response, batch.length);
    if (ack === "malformed") {
      frontendTraceHealth.malformedACKs += 1;
      throw new Error("frontend trace ingest returned a malformed ACK");
    }
    if (ack === "disabled") {
      const disabledResponse = /** @type {FrontendTraceACK} */ (response);
      frontendTraceDisabled = true;
      frontendTraceDisabledReason =
        typeof disabledResponse.disabled_reason === "string"
          ? disabledResponse.disabled_reason
          : "";
      frontendTraceHealth.terminalDropped +=
        batch.length + frontendTraceQueue.length;
      frontendTracePendingBatch = null;
      frontendTraceQueue = [];
      clearFrontendTraceRetry();
      return;
    }
    const acknowledgedResponse = /** @type {FrontendTraceACK} */ (response);
    const recorded = acknowledgedResponse.recorded;
    const dropped =
      acknowledgedResponse.dropped === undefined
        ? 0
        : acknowledgedResponse.dropped;
    frontendTraceHealth.acknowledged += recorded;
    frontendTraceHealth.serverDropped += dropped;
    if (dropped > 0)
      writeBridgeLog("warn", "frontend.trace.flush.partial_drop", {
        count: batch.length,
        recorded,
        dropped,
      });
    frontendTracePendingBatch = null;
    frontendTraceHealth.lastFailure = "";
    frontendTraceConsecutiveFailures = 0;
    clearFrontendTraceRetry();
  } catch (error) {
    frontendTraceHealth.failures += 1;
    frontendTraceHealth.lastFailure = safeTraceErrorMessage(error);
    shouldRetry = true;
    logFrontendTraceFlushFailure(batch.length);
  } finally {
    frontendTraceFlushInFlight = false;
    if (shouldRetry) scheduleFrontendTraceRetry();
    else if (!frontendTraceDisabled && frontendTraceQueuedCount() > 0)
      scheduleFrontendTraceFlush();
  }
}
function scheduleFrontendTraceFlush() {
  if (
    frontendTraceDisabled ||
    frontendTraceFlushScheduled ||
    frontendTraceFlushInFlight ||
    frontendTraceRetryTimer !== null ||
    frontendTraceQueuedCount() === 0
  )
    return;
  frontendTraceFlushScheduled = true;
  void Promise.resolve()
    .then(flushFrontendTraceQueue)
    .catch((error) => {
      frontendTraceFlushScheduled = false;
      writeBridgeLog("error", "frontend.trace.flush.schedule.failed", {
        error,
      });
      scheduleFrontendTraceRetry();
    });
}
function logFrontendTraceQueueOverflow() {
  const dropped = frontendTraceHealth.overflowDropped;
  if (
    dropped !== 1 &&
    dropped % FRONTEND_TRACE_OVERFLOW_WARN_DROP_INTERVAL !== 0
  )
    return;
  frontendTraceHealth.overflowWarnings += 1;
  writeBridgeLog("warn", "frontend.trace.queue.overflow", {
    dropped,
    queueLength: frontendTraceQueuedCount(),
    retryAttempt: frontendTraceRetryAttempt,
  });
}
/** @param {FrontendTraceEvent} event */
function enqueueFrontendTraceEvent(event) {
  frontendTraceHealth.accepted += 1;
  if (frontendTraceQueuedCount() >= FRONTEND_TRACE_QUEUE_LIMIT) {
    frontendTraceHealth.overflowDropped += 1;
    logFrontendTraceQueueOverflow();
    if (frontendTraceQueue.length === 0) return false;
    frontendTraceQueue.splice(0, 1);
  }
  frontendTraceQueue.push(event);
  return true;
}
/**
 * @param {unknown} event
 * @param {FrontendTraceEmitOptions} [options]
 */
function emitFrontendTraceEvent(event, options = {}) {
  if (frontendTraceDisabled) return false;
  const sanitized = sanitizeFrontendTraceEvent(event);
  if (
    !sanitized ||
    !shouldRemoteFlushFrontendTrace(sanitized) ||
    !enqueueFrontendTraceEvent(sanitized)
  )
    return false;
  if (options.flush !== false) scheduleFrontendTraceFlush();
  return true;
}
/** @returns {Promise<void>} */
function flushFrontendTraceQueueForTest() {
  return flushFrontendTraceQueue();
}

export {
  emitFrontendTraceEvent,
  flushFrontendTraceQueueForTest,
  getFrontendTraceQueueHealth,
};
