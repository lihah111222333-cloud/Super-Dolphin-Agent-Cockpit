// @ts-nocheck

import { isSafeNumber, parse as parseLosslessJSON } from 'lossless-json';
import {
  METHOD_IDS, FRONTEND_TRACE_INGEST_METHOD, FRONTEND_TRACE_BATCH_LIMIT, FRONTEND_TRACE_QUEUE_LIMIT, FRONTEND_TRACE_RPC_SLOW_MS,
  FRONTEND_PERFORMANCE_TRACE_PHASES,
  FRONTEND_TRACE_ALLOWED_PHASES, FRONTEND_TRACE_ALLOWED_METADATA_KEYS, FRONTEND_TRACE_ALLOWED_STATUSES,
  FRONTEND_RUNTIME_TRACE_DEFAULT_PHASES, FRONTEND_RUNTIME_TRACE_SKIP_METHODS, FRONTEND_TRACE_FORBIDDEN_KEYS,
  FRONTEND_TRACE_SENSITIVE_TEXT_PATTERNS,
} from './wailsBridgeConstants.js';
import { bridgeEventParseFailureEnvelope, optionalDiagnosticString, waitRuntime, writeBridgeLog } from './wailsBridgeLogRuntime.js';

function parseRuntimeEventNumber(value) {
  return isSafeNumber(value) ? Number(value) : value;
}

function parseRuntimeEventJSON(rawText, eventName) {
  let parsed;
  try {
    parsed = parseLosslessJSON(String(rawText), null, { parseNumber: parseRuntimeEventNumber });
  }
  catch (error) {
    return bridgeEventParseFailureEnvelope(rawText, error, eventName);
  }
  return parsed;
}

function noopBridgeUnsubscribe() {
  return undefined;
}

function normalizeRuntimeEventEnvelope(evt) {
  if (!evt || typeof evt !== 'object') return {};
  const hasWailsEnvelope = Object.prototype.hasOwnProperty.call(evt, 'name')
    && Object.prototype.hasOwnProperty.call(evt, 'data');
  if (!hasWailsEnvelope) return evt;

  const inner = evt.data;
  if (inner == null || inner === '') return {};
  if (typeof inner === 'object') return inner;
  if (typeof inner === 'string') return parseRuntimeEventJSON(inner, evt.name);
  return { data: inner };
}

function subscribeRuntimeEvent(eventName, callback, options = {}) {
  let off = noopBridgeUnsubscribe;
  let cancelled = false;
  let readySettled = false;
  let resolveReady;
  const ready = new Promise((resolve) => {
    resolveReady = resolve;
  });

  const settleReady = (value) => {
    if (readySettled) return;
    readySettled = true;
    resolveReady(value === true);
  };

  const teardown = (runtime, unbind) => {
    try {
      if (typeof unbind === 'function') {
        unbind();
        return true;
      }
      if (runtime?.Events?.Off) {
        runtime.Events.Off(eventName);
        return true;
      }
    }
    catch {
      // ignore
    }
    return false;
  };

  const unsubscribe = () => {
    cancelled = true;
    off();
  };
  const subscription = { ready, unsubscribe };

  const shouldEscalateCallbackError = (error, normalized) => {
    if (typeof options.escalateCallbackError === 'function') {
      return options.escalateCallbackError(error, normalized) === true;
    }
    return options.escalateCallbackError === true;
  };

  const wrapped = (evt) => {
    const normalized = normalizeRuntimeEventEnvelope(evt);
    if (typeof options.beforeCallback === 'function') {
      options.beforeCallback(normalized);
    }
    try {
      callback(normalized);
    }
    catch (error) {
      writeBridgeLog('error', options.callbackFailedLog || 'runtime.callback.failed', { error });
      if (typeof options.onCallbackError === 'function') {
        options.onCallbackError(error, normalized);
      }
      if (shouldEscalateCallbackError(error, normalized)) {
        throw error;
      }
    }
  };

  void waitRuntime().then((runtime) => {
    if (!runtime?.Events?.On) {
      writeBridgeLog('warn', options.subscribeUnavailableLog || 'runtime.subscribe.unavailable', { eventName });
            settleReady(false);
      return;
    }
    const unbind = runtime.Events.On(eventName, wrapped);
    if (cancelled) {
      teardown(runtime, unbind);
      settleReady(false);
      return;
    }
    off = () => {
      cancelled = true;
      teardown(runtime, unbind);
    };
    settleReady(true);
  }).catch((error) => {
        writeBridgeLog('error', options.subscribeFailedLog || 'runtime.subscribe.failed', { eventName, error });
    settleReady(false);
  });

  return subscription;
}

function resolveClientMeta() {
  const clientKind = typeof window !== 'undefined' && window.__WAILS_SHIM_DEBUG__ === true
    ? 'web-debug-shim'
    : 'desktop-wails';
  const clientRoute = typeof window !== 'undefined' && window.location
    ? (window.location.pathname || '/').toString()
    : '';
  return { clientKind, clientRoute };
}

function randomHex(byteLength) {
  const cryptoSource = globalThis.crypto;
  if (!cryptoSource || typeof cryptoSource.getRandomValues !== 'function') {
    throw new Error('secure random source is required for Wails RPC trace context');
  }
  const bytes = new Uint8Array(byteLength);
  while (true) {
    cryptoSource.getRandomValues(bytes);
    const value = Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join('');
    if (!/^0+$/.test(value)) return value;
  }
}

function createTraceContext() {
  const traceId = randomHex(16);
  const spanId = randomHex(8);
  return {
    traceId,
    spanId,
    traceparent: `00-${traceId}-${spanId}-01`,
  };
}

let frontendTraceQueue = [];
let frontendTraceFlushScheduled = false;
let frontendTraceFlushInFlight = false;
let frontendTracePendingBatch = null;
let frontendTraceRetryTimer = null;
let frontendTraceRetryAttempt = 0;
let frontendTraceRetryDelayMS = 0;
let frontendTraceDisabled = false;
let frontendTraceDisabledReason = '';
const frontendTraceHealth = {
  accepted: 0,
  acknowledged: 0,
  serverDropped: 0,
  failures: 0,
  malformedACKs: 0,
  overflowDropped: 0,
  terminalDropped: 0,
  lastFailure: '',
};
const FRONTEND_TRACE_RETRY_BASE_MS = 100;
const FRONTEND_TRACE_RETRY_MAX_MS = 5000;

function isUITestMCPTraceSuppressed() {
  return !import.meta.env.PROD && import.meta.env.VITE_SUPER_DOLPHIN_UI_TEST_MCP === '1';
}

function isFrontendTraceDebugEnabled() {
  if (typeof window === 'undefined') return false;
  if (window.__AO_FRONTEND_TRACE_DEBUG__ === true) return true;
  try {
    return window.localStorage?.getItem('observability.frontend.debug') === 'true';
  }
  catch {
    return false;
  }
}

function safeTraceString(value, limit = 160) {
  const text = optionalDiagnosticString(value).trim();
  if (!text) return '';
  return text.length > limit ? `${text.slice(0, limit)}…` : text;
}

function safeTraceDiagnosticToken(value, limit = 80) {
  const text = safeTraceString(value, limit);
  if (!text || containsForbiddenTraceText(text)) return '';
  return text;
}

function safeTraceErrorMessage(error) {
  const code = safeTraceDiagnosticToken(error?.code, 80);
  const name = safeTraceDiagnosticToken(error?.name, 80);
  const message = safeTraceString(error?.message, 240);
  const safeMessage = containsForbiddenTraceText(message) ? '' : message;
  if (code && safeMessage) return `${code}: ${safeMessage}`;
  if (safeMessage) return safeMessage;
  return code || name || 'Error';
}

function safeTraceErrorValue(value) {
  if (value instanceof Error || (value && typeof value === 'object')) {
    return safeTraceErrorMessage(value);
  }
  const message = safeTraceString(value, 240);
  if (!message || containsForbiddenTraceText(message)) return '';
  return safeTraceString(message, 120);
}

function containsForbiddenTraceText(text) {
  // 误判防护：containsForbiddenTraceText 过滤 error/message 中的敏感 trace 文本。
  const value = safeTraceString(text, 512);
  const normalized = value.toLowerCase();
  if (!normalized) return false;
  if (FRONTEND_TRACE_SENSITIVE_TEXT_PATTERNS.some((pattern) => pattern.test(value))) {
    return true;
  }
  for (const key of FRONTEND_TRACE_FORBIDDEN_KEYS) {
    const token = key.toLowerCase();
    if (normalized.includes(token) || normalized.includes(token.replaceAll('_', ' '))) {
      return true;
    }
  }
  return false;
}

function safeTraceMetadata(metadata) {
  // 误判防护：safeTraceMetadata 只允许白名单 metadata key 进入 trace。
  if (!metadata || typeof metadata !== 'object' || Array.isArray(metadata)) return undefined;
  const out = {};
  for (const [key, value] of Object.entries(metadata)) {
    if (!FRONTEND_TRACE_ALLOWED_METADATA_KEYS.has(key)) continue;
    if (FRONTEND_TRACE_FORBIDDEN_KEYS.has(key)) continue;
    if (value === undefined || value === null || value === '') continue;
    if (typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean') {
      out[key] = typeof value === 'string' ? safeTraceString(value) : value;
    }
  }
  return Object.keys(out).length > 0 ? out : undefined;
}

function sanitizeFrontendTraceEvent(event) {
  // 误判防护：sanitizeFrontendTraceEvent 是前端 trace 入队前的统一去敏守卫。
  if (!event || typeof event !== 'object' || Array.isArray(event)) return null;
  const phase = safeTraceString(event.phase);
  if (!FRONTEND_TRACE_ALLOWED_PHASES.has(phase)) return null;
  const status = safeTraceString(event.status).toLowerCase();
  if (!FRONTEND_TRACE_ALLOWED_STATUSES.has(status)) return null;
  if (FRONTEND_PERFORMANCE_TRACE_PHASES.has(phase)) {
    const expectedStatus = phase === 'frontend.performance.capability_absent' ? 'ok' : 'slow';
    if (status !== expectedStatus) return null;
  }
  const durationMS = Number(event.duration_ms);
  const out = {
    ts: createFrontendTraceTimestamp(),
    phase,
    status,
  };
  for (const [target, source, limit] of [
    ['trace_id', 'trace_id', 64],
    ['span_id', 'span_id', 32],
    ['parent_span_id', 'parent_span_id', 32],
    ['method', 'method', 160],
    ['thread_id', 'thread_id', 160],
    ['agent_id', 'agent_id', 160],
    ['turn_id', 'turn_id', 160],
    ['call_id', 'call_id', 160],
    ['client_kind', 'client_kind', 80],
    ['client_route', 'client_route', 240],
  ]) {
    const value = safeTraceString(event[source], limit);
    if (value) out[target] = value;
  }
  if (Number.isFinite(durationMS) && durationMS >= 0) out.duration_ms = Math.round(durationMS);
  if (out.status === 'error') {
    const error = safeTraceErrorValue(event.error);
    if (error) out.error = error;
  }
  const metadata = safeTraceMetadata(event.metadata);
  if (metadata) out.metadata = metadata;
  return out;
}

function shouldRemoteFlushFrontendTrace(event) {
  if (!event) return false;
  if (isUITestMCPTraceSuppressed()) return false;
  if (event.status === 'error') return true;
  if (event.status === 'slow') return true;
  if (FRONTEND_RUNTIME_TRACE_DEFAULT_PHASES.has(event.phase)) return true;
  if (event.phase === 'frontend.patch.apply.slow' || event.phase === 'frontend.render.slow') return true;
  if (FRONTEND_PERFORMANCE_TRACE_PHASES.has(event.phase)) return true;
  if (event.phase === 'frontend.rpc.done' && Number(event.duration_ms) >= FRONTEND_TRACE_RPC_SLOW_MS) return true;
  return isFrontendTraceDebugEnabled();
}

function frontendTraceQueuedCount() {
  return frontendTraceQueue.length + (frontendTracePendingBatch?.length || 0);
}

function getFrontendTraceQueueHealth() {
  return {
    ...frontendTraceHealth,
    queueLength: frontendTraceQueuedCount(),
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
  if (frontendTraceDisabled || frontendTraceRetryTimer !== null || frontendTraceQueuedCount() === 0) return;
  frontendTraceRetryAttempt += 1;
  frontendTraceRetryDelayMS = Math.min(
    FRONTEND_TRACE_RETRY_BASE_MS * (2 ** (frontendTraceRetryAttempt - 1)),
    FRONTEND_TRACE_RETRY_MAX_MS,
  );
  frontendTraceRetryTimer = setTimeout(() => {
    frontendTraceRetryTimer = null;
    frontendTraceRetryDelayMS = 0;
    scheduleFrontendTraceFlush();
  }, frontendTraceRetryDelayMS);
}

function classifyFrontendTraceACK(response, batchLength) {
  if (!response || typeof response !== 'object' || Array.isArray(response)) return 'malformed';
  const recorded = response.recorded;
  const dropped = response.dropped ?? 0;
  if (!Number.isInteger(recorded) || recorded < 0 || !Number.isInteger(dropped) || dropped < 0) return 'malformed';
  if (response.enabled === false) {
    const disabledReason = typeof response.disabled_reason === 'string' ? response.disabled_reason.trim() : '';
    return recorded === 0 && dropped === batchLength && disabledReason ? 'disabled' : 'malformed';
  }
  if (response.enabled !== undefined && response.enabled !== true) return 'malformed';
  return recorded + dropped === batchLength ? 'acknowledged' : 'malformed';
}

async function flushFrontendTraceQueue() {
  frontendTraceFlushScheduled = false;
  if (frontendTraceDisabled || frontendTraceFlushInFlight || frontendTraceQueuedCount() === 0) return;
  frontendTraceFlushInFlight = true;
  if (!frontendTracePendingBatch) {
    frontendTracePendingBatch = frontendTraceQueue.splice(0, FRONTEND_TRACE_BATCH_LIMIT);
  }
  const batch = frontendTracePendingBatch;
  let shouldRetry = false;
  try {
    const runtime = await waitRuntime();
    if (typeof runtime?.Call?.ByID !== 'function') {
      throw new Error('runtime Call.ByID is unavailable');
    }
    const response = await runtime.Call.ByID(
      METHOD_IDS.CALL_API,
      FRONTEND_TRACE_INGEST_METHOD,
      { events: batch },
    );
    const ack = classifyFrontendTraceACK(response, batch.length);
    if (ack === 'malformed') {
      frontendTraceHealth.malformedACKs += 1;
      throw new Error('frontend trace ingest returned a malformed ACK');
    }
    if (ack === 'disabled') {
      frontendTraceDisabled = true;
      frontendTraceDisabledReason = typeof response.disabled_reason === 'string' ? response.disabled_reason : '';
      frontendTraceHealth.terminalDropped += batch.length + frontendTraceQueue.length;
      frontendTracePendingBatch = null;
      frontendTraceQueue = [];
      clearFrontendTraceRetry();
      return;
    }
    frontendTraceHealth.acknowledged += response.recorded;
    frontendTraceHealth.serverDropped += response.dropped ?? 0;
    if ((response.dropped ?? 0) > 0) {
      writeBridgeLog('warn', 'frontend.trace.flush.partial_drop', {
        count: batch.length,
        recorded: response.recorded,
        dropped: response.dropped,
      });
    }
    frontendTracePendingBatch = null;
    frontendTraceHealth.lastFailure = '';
    clearFrontendTraceRetry();
  }
  catch (error) {
    frontendTraceHealth.failures += 1;
    frontendTraceHealth.lastFailure = safeTraceErrorMessage(error);
    shouldRetry = true;
    console.warn('[Bridge warn] frontend.trace.flush.failed', {
      error: error?.name || 'Error',
      count: batch.length,
    });
  }
  finally {
    frontendTraceFlushInFlight = false;
    if (shouldRetry) scheduleFrontendTraceRetry();
    else if (!frontendTraceDisabled && frontendTraceQueuedCount() > 0) scheduleFrontendTraceFlush();
  }
}

function scheduleFrontendTraceFlush() {
  if (
    frontendTraceDisabled
    || frontendTraceFlushScheduled
    || frontendTraceFlushInFlight
    || frontendTraceRetryTimer !== null
    || frontendTraceQueuedCount() === 0
  ) return;
  frontendTraceFlushScheduled = true;
  void Promise.resolve()
    .then(flushFrontendTraceQueue)
    .catch((error) => {
      frontendTraceFlushScheduled = false;
      writeBridgeLog('error', 'frontend.trace.flush.schedule.failed', { error });
      scheduleFrontendTraceRetry();
    });
}

function enqueueFrontendTraceEvent(event) {
  // 误判防护：enqueueFrontendTraceEvent 使用 FRONTEND_TRACE_QUEUE_LIMIT 限制 trace 队列。
  frontendTraceHealth.accepted += 1;
  if (frontendTraceQueuedCount() >= FRONTEND_TRACE_QUEUE_LIMIT) {
    if (frontendTraceQueue.length === 0) {
      frontendTraceHealth.overflowDropped += 1;
      return false;
    }
    frontendTraceQueue.splice(0, 1);
    frontendTraceHealth.overflowDropped += 1;
  }
  frontendTraceQueue.push(event);
  return true;
}

function emitFrontendTraceEvent(event, options = {}) {
  if (frontendTraceDisabled) return false;
  const sanitized = sanitizeFrontendTraceEvent(event);
  if (!shouldRemoteFlushFrontendTrace(sanitized)) return false;
  if (!enqueueFrontendTraceEvent(sanitized)) return false;
  if (options.flush !== false) scheduleFrontendTraceFlush();
  return true;
}

function flushFrontendTraceQueueForTest() {
  return flushFrontendTraceQueue();
}

function runtimeTelemetryMetadata(event) {
  const metadata = {};
  for (const key of ['req_id', 'pending_count', 'attempt']) {
    const value = runtimeTelemetryMetadataValue(event, key);
    if (value !== undefined && value !== null && value !== '') metadata[key] = value;
  }
  return Object.keys(metadata).length > 0 ? metadata : undefined;
}

function runtimeTelemetryMetadataValue(event, key) {
  if (event && Object.prototype.hasOwnProperty.call(event, key)) return event[key];
  if (event?.metadata && Object.prototype.hasOwnProperty.call(event.metadata, key)) return event.metadata[key];
  return undefined;
}

function currentMonotonicMS() {
  if (typeof performance === 'undefined' || typeof performance.now !== 'function') {
    const error = new Error('bridge monotonic clock is unavailable');
    error.name = 'BridgeClockUnavailableError';
    throw error;
  }
  const value = performance.now();
  if (!Number.isFinite(value)) {
    const error = new Error('bridge clock returned an invalid timestamp');
    error.name = 'BridgeClockUnavailableError';
    throw error;
  }
  return value;
}

function elapsedMS(start) {
  if (!Number.isFinite(start)) {
    const error = new Error('bridge start timestamp is invalid');
    error.name = 'BridgeClockUnavailableError';
    throw error;
  }
  return Math.max(0, Math.round(currentMonotonicMS() - start));
}

function createFrontendTraceTimestamp(clock = Date) {
  if (!clock || typeof clock !== 'function') {
    const error = new Error('frontend trace wall clock is unavailable');
    error.name = 'BridgeClockUnavailableError';
    throw error;
  }
  return new clock().toISOString();
}

function runtimeTelemetryTraceEvent(event) {
  if (!event || typeof event !== 'object' || Array.isArray(event)) return null;
  const traceEvent = {
    phase: event.phase,
    method: event.method,
    trace_id: event.trace_id,
    span_id: event.span_id,
    call_id: event.call_id,
    duration_ms: event.duration_ms,
    status: event.status,
    error: event.error,
    metadata: runtimeTelemetryMetadata(event),
  };
  return traceEvent;
}

function handleRuntimeTelemetryEvent(event) {
  const traceEvent = runtimeTelemetryTraceEvent(event);
  const sanitized = sanitizeFrontendTraceEvent(traceEvent);
  if (!sanitized) return;
  if (sanitized.status === 'error' || sanitized.status === 'slow' || isFrontendTraceDebugEnabled()) {
    writeBridgeLog(
      sanitized.status === 'error' || sanitized.status === 'slow' ? 'warn' : 'debug',
      'runtime.rpc.telemetry',
      sanitized,
    );
  }
  if (FRONTEND_RUNTIME_TRACE_SKIP_METHODS.has(sanitized.method)) return;
  emitFrontendTraceEvent(traceEvent);
}

function logRuntimeTelemetryExternalHookFailed(error) {
  writeBridgeLog('warn', 'runtime.rpc.telemetry.external_hook_failed', { error });
}

function installRuntimeTelemetryHook() {
  if (typeof window === 'undefined') return;
  const currentHook = typeof window.__AO_WAILS_RUNTIME_TELEMETRY__ === 'function'
    ? window.__AO_WAILS_RUNTIME_TELEMETRY__
    : null;
  const externalHook = currentHook?.__AO_BRIDGE_RUNTIME_TELEMETRY__ === true
    ? currentHook.__AO_PREVIOUS_RUNTIME_TELEMETRY__ || null
    : currentHook;
  const hook = (event) => {
    if (typeof externalHook === 'function') {
      try {
        externalHook(event);
      }
      catch (error) {
        logRuntimeTelemetryExternalHookFailed(error);
      }
    }
    handleRuntimeTelemetryEvent(event);
  };
  hook.__AO_BRIDGE_RUNTIME_TELEMETRY__ = true;
  hook.__AO_PREVIOUS_RUNTIME_TELEMETRY__ = externalHook;
  window.__AO_WAILS_RUNTIME_TELEMETRY__ = hook;
}

installRuntimeTelemetryHook();


export {
  parseRuntimeEventNumber, parseRuntimeEventJSON, normalizeRuntimeEventEnvelope, subscribeRuntimeEvent, resolveClientMeta, createTraceContext,
  isUITestMCPTraceSuppressed, isFrontendTraceDebugEnabled, safeTraceErrorMessage, currentMonotonicMS, elapsedMS, createFrontendTraceTimestamp,
  installRuntimeTelemetryHook, emitFrontendTraceEvent, getFrontendTraceQueueHealth, flushFrontendTraceQueueForTest,
};
