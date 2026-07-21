import {
  createFrontendTraceTimestamp,
  createTraceContext,
  currentMonotonicMS,
  elapsedMS,
  resolveClientMeta,
} from "./wailsBridgeTraceContext.js";
import {
  normalizeRuntimeEventEnvelope,
  parseRuntimeEventJSON,
  parseRuntimeEventNumber,
  subscribeRuntimeEvent,
} from "./wailsBridgeTraceRuntimeEvents.js";
import {
  isFrontendTraceDebugEnabled,
  isUITestMCPTraceSuppressed,
  safeTraceErrorMessage,
} from "./wailsBridgeTraceSanitization.js";
import {
  emitFrontendTraceEvent,
  flushFrontendTraceQueueForTest,
  getFrontendTraceQueueHealth,
} from "./wailsBridgeTraceTransport.js";
import { installRuntimeTelemetryHook } from "./wailsBridgeTraceRuntimeTelemetry.js";

installRuntimeTelemetryHook();

export {
  parseRuntimeEventNumber,
  parseRuntimeEventJSON,
  normalizeRuntimeEventEnvelope,
  subscribeRuntimeEvent,
  resolveClientMeta,
  createTraceContext,
  isUITestMCPTraceSuppressed,
  isFrontendTraceDebugEnabled,
  safeTraceErrorMessage,
  currentMonotonicMS,
  elapsedMS,
  createFrontendTraceTimestamp,
  installRuntimeTelemetryHook,
  emitFrontendTraceEvent,
  getFrontendTraceQueueHealth,
  flushFrontendTraceQueueForTest,
};
