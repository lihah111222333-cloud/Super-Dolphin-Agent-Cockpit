
import { isSafeNumber, parse as parseLosslessJSON } from 'lossless-json';
import {
  METHOD_IDS, FRONTEND_TRACE_INGEST_METHOD, FRONTEND_TRACE_BATCH_LIMIT, FRONTEND_TRACE_QUEUE_LIMIT, FRONTEND_TRACE_RPC_SLOW_MS,
  FRONTEND_PERFORMANCE_TRACE_PHASES,
  FRONTEND_TRACE_ALLOWED_PHASES, FRONTEND_TRACE_ALLOWED_METADATA_KEYS, FRONTEND_TRACE_ALLOWED_STATUSES,
  FRONTEND_RUNTIME_TRACE_DEFAULT_PHASES, FRONTEND_RUNTIME_TRACE_SKIP_METHODS, FRONTEND_TRACE_FORBIDDEN_KEYS,
  FRONTEND_TRACE_SENSITIVE_TEXT_PATTERNS,
} from './wailsBridgeConstants.js';
import { bridgeEventParseFailureEnvelope, optionalDiagnosticString, waitRuntime, writeBridgeLog } from './wailsBridgeLogRuntime.js';

/** @typedef {Record<string, unknown>} TraceRecord */
/** @typedef {TraceRecord & { ts: string, phase: string, status: string }} SanitizedTraceEvent */
/** @typedef {{ beforeCallback?: (event: unknown) => void, escalateCallbackError?: boolean | ((error: unknown, event: unknown) => boolean), onCallbackError?: (error: unknown, event: unknown) => void }} RuntimeEventCallbackOptions */
/** @typedef {{ callbackFailedLog?: string, subscribeFailedLog?: string, subscribeReadyLog?: string, subscribeUnavailableLog?: string, unsubscribeDoneLog?: string }} RuntimeEventLogOptions */
/** @typedef {RuntimeEventCallbackOptions & RuntimeEventLogOptions} RuntimeEventOptions */

/** @param {string} value @returns {number | string} */
function parseRuntimeEventNumber(value) {
  return isSafeNumber(value) ? Number(value) : value;
}

/** @param {unknown} rawText @param {string} eventName @returns {unknown} */
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

/** @param {unknown} evt @returns {unknown} */
function normalizeRuntimeEventEnvelope(evt) {
  if (!evt || typeof evt !== 'object') return {};
  const envelope = /** @type {TraceRecord} */ (evt);
  const hasWailsEnvelope = Object.prototype.hasOwnProperty.call(envelope, 'name')
    && Object.prototype.hasOwnProperty.call(envelope, 'data');
  if (!hasWailsEnvelope) return evt;

  const inner = envelope.data;
  if (inner == null || inner === '') return {};
  if (typeof inner === 'object') return inner;
  if (typeof inner === 'string') return parseRuntimeEventJSON(inner, optionalDiagnosticString(envelope.name));
  return { data: inner };
}

/** @param {string} eventName @param {(event: unknown) => unknown} callback @param {RuntimeEventOptions} options */
function subscribeRuntimeEvent(eventName, callback, options = {}) {
  let off = noopBridgeUnsubscribe;
  let cancelled = false;
  let readySettled = false;
  /** @type {(value: boolean) => void} */
  let resolveReady;
  /** @type {Promise<boolean>} */
  const ready = new Promise((resolve) => {
    resolveReady = resolve;
  });

  const settleReady = (/** @type {unknown} */ value) => {
    if (readySettled) return;
    readySettled = true;
    resolveReady(value === true);
  };

  const teardown = (/** @type {unknown} */ runtime, /** @type {unknown} */ unbind) => {
    try {
      if (typeof unbind === 'function') {
        unbind();
        return true;
      }
      const runtimeRecord = runtime && typeof runtime === 'object' ? /** @type {TraceRecord} */ (runtime) : {};
      const events = runtimeRecord.Events && typeof runtimeRecord.Events === 'object' ? /** @type {TraceRecord} */ (runtimeRecord.Events) : {};
      if (typeof events.Off === 'function') {
        events.Off(eventName);
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

  const shouldEscalateCallbackError = (/** @type {unknown} */ error, /** @type {unknown} */ normalized) => {
    if (typeof options.escalateCallbackError === 'function') {
      return options.escalateCallbackError(error, normalized) === true;
    }
    return options.escalateCallbackError === true;
  };

  const wrapped = (/** @type {unknown} */ evt) => {
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
    const runtimeRecord = runtime && typeof runtime === 'object' ? /** @type {TraceRecord} */ (runtime) : {};
    const events = runtimeRecord.Events && typeof runtimeRecord.Events === 'object' ? /** @type {TraceRecord} */ (runtimeRecord.Events) : {};
    if (typeof events.On !== 'function') {
      writeBridgeLog('warn', options.subscribeUnavailableLog || 'runtime.subscribe.unavailable', { eventName });
            settleReady(false);
      return;
    }
    const unbind = events.On(eventName, wrapped);
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
  const clientKind = typeof window !== 'undefined'
    && /** @type {Window & { __WAILS_SHIM_DEBUG__?: boolean }} */ (window).__WAILS_SHIM_DEBUG__ === true
    ? 'web-debug-shim'
    : 'desktop-wails';
  const clientRoute = typeof window !== 'undefined' && window.location
    ? (window.location.pathname || '/').toString()
    : '';
  return { clientKind, clientRoute };
}

/** @param {number} byteLength @returns {string} */
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

/** @type {SanitizedTraceEvent[]} */
let frontendTraceQueue = [];
let frontendTraceFlushScheduled = false;
let frontendTraceFlushInFlight = false;
/** @type {SanitizedTraceEvent[] | null} */
let frontendTracePendingBatch = null;
/** @type {ReturnType<typeof setTimeout> | null} */
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
  const env = /** @type {ImportMeta & { env: Record<string, unknown> }} */ (import.meta).env;
  return env.PROD !== true && env.VITE_SUPER_DOLPHIN_UI_TEST_MCP === '1';
}

function isFrontendTraceDebugEnabled() {
  if (typeof window === 'undefined') return false;
  if (/** @type {Window & { __AO_FRONTEND_TRACE_DEBUG__?: boolean }} */ (window).__AO_FRONTEND_TRACE_DEBUG__ === true) return true;
  try {
    return window.localStorage?.getItem('observability.frontend.debug') === 'true';
  }
  catch {
    return false;
  }
}

/** @param {unknown} value @param {number} limit @returns {string} */
function safeTraceString(value, limit = 160) {
  const text = optionalDiagnosticString(value).trim();
  if (!text) return '';
  return text.length > limit ? `${text.slice(0, limit)}…` : text;
}

/** @param {unknown} value @param {number} limit @returns {string} */
function safeTraceDiagnosticToken(value, limit = 80) {
  const text = safeTraceString(value, limit);
  if (!text || containsForbiddenTraceText(text)) return '';
  return text;
}

/** @param {unknown} error @returns {string} */
function safeTraceErrorMessage(error) {
  const value = error && (typeof error === 'object' || typeof error === 'function') ? /** @type {TraceRecord} */ (error) : {};
  const code = safeTraceDiagnosticToken(value.code, 80);
  const name = safeTraceDiagnosticToken(value.name, 80);
  const message = safeTraceString(value.message, 240);
  const safeMessage = containsForbiddenTraceText(message) ? '' : message;
  if (code && safeMessage) return `${code}: ${safeMessage}`;
  if (safeMessage) return safeMessage;
  return code || name || 'Error';
}

/** @param {unknown} value @returns {string} */
function safeTraceErrorValue(value) {
  if (value instanceof Error || (value && typeof value === 'object')) {
    return safeTraceErrorMessage(value);
  }
  const message = safeTraceString(value, 240);
  if (!message || containsForbiddenTraceText(message)) return '';
  return safeTraceString(message, 120);
}

/** @param {unknown} text @returns {boolean} */
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

/** @param {unknown} metadata @returns {TraceRecord | undefined} */
function safeTraceMetadata(metadata) {
  // 误判防护：safeTraceMetadata 只允许白名单 metadata key 进入 trace。
  if (!metadata || typeof metadata !== 'object' || Array.isArray(metadata)) return undefined;
  /** @type {TraceRecord} */
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

/** @param {unknown} event @returns {SanitizedTraceEvent | null} */
function sanitizeFrontendTraceEvent(event) {
  // 误判防护：sanitizeFrontendTraceEvent 是前端 trace 入队前的统一去敏守卫。
  if (!event || typeof event !== 'object' || Array.isArray(event)) return null;
  const source = /** @type {TraceRecord} */ (event);
  const phase = safeTraceString(source.phase);
  if (!FRONTEND_TRACE_ALLOWED_PHASES.has(phase)) return null;
  const status = safeTraceString(source.status).toLowerCase();
  if (!FRONTEND_TRACE_ALLOWED_STATUSES.has(status)) return null;
  if (FRONTEND_PERFORMANCE_TRACE_PHASES.has(phase)) {
    const expectedStatus = phase === 'frontend.performance.capability_absent' ? 'ok' : 'slow';
    if (status !== expectedStatus) return null;
  }
  const durationMS = Number(source.duration_ms);
  /** @type {SanitizedTraceEvent} */
  const out = {
    ts: createFrontendTraceTimestamp(),
    phase,
    status,
  };
  /** @type {Array<[string, string, number]>} */
  const stringFields = [
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
  ];
  for (const [target, sourceKey, limit] of stringFields) {
    const value = safeTraceString(source[sourceKey], limit);
    if (value) out[target] = value;
  }
  if (Number.isFinite(durationMS) && durationMS >= 0) out.duration_ms = Math.round(durationMS);
  if (out.status === 'error') {
    const error = safeTraceErrorValue(source.error);
    if (error) out.error = error;
  }
  const metadata = safeTraceMetadata(source.metadata);
  if (metadata) out.metadata = metadata;
  return out;
}

/** @param {SanitizedTraceEvent | null} event @returns {boolean} */
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

/** @param {unknown} response @param {number} batchLength @returns {'malformed' | 'disabled' | 'acknowledged'} */
function classifyFrontendTraceACK(response, batchLength) {
  if (!response || typeof response !== 'object' || Array.isArray(response)) return 'malformed';
  const value = /** @type {TraceRecord} */ (response);
  const recorded = value.recorded;
  const dropped = value.dropped ?? 0;
  if (typeof recorded !== 'number' || typeof dropped !== 'number' || !Number.isInteger(recorded) || recorded < 0 || !Number.isInteger(dropped) || dropped < 0) return 'malformed';
  if (value.enabled === false) {
    const disabledReason = typeof value.disabled_reason === 'string' ? value.disabled_reason.trim() : '';
    return recorded === 0 && dropped === batchLength && disabledReason ? 'disabled' : 'malformed';
  }
  if (value.enabled !== undefined && value.enabled !== true) return 'malformed';
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
    const runtimeRecord = runtime && typeof runtime === 'object' ? /** @type {TraceRecord} */ (runtime) : {};
    const call = runtimeRecord.Call && typeof runtimeRecord.Call === 'object' ? /** @type {TraceRecord} */ (runtimeRecord.Call) : {};
    const byID = call.ByID;
    if (typeof byID !== 'function') {
      throw new Error('runtime Call.ByID is unavailable');
    }
    const response = await byID(
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
      const responseValue = /** @type {TraceRecord} */ (response);
      frontendTraceDisabled = true;
      frontendTraceDisabledReason = typeof responseValue.disabled_reason === 'string' ? responseValue.disabled_reason : '';
      frontendTraceHealth.terminalDropped += batch.length + frontendTraceQueue.length;
      frontendTracePendingBatch = null;
      frontendTraceQueue = [];
      clearFrontendTraceRetry();
      return;
    }
    const responseValue = /** @type {TraceRecord} */ (response);
    const recorded = /** @type {number} */ (responseValue.recorded);
    const dropped = responseValue.dropped === undefined ? 0 : /** @type {number} */ (responseValue.dropped);
    frontendTraceHealth.acknowledged += recorded;
    frontendTraceHealth.serverDropped += dropped;
    if (dropped > 0) {
      writeBridgeLog('warn', 'frontend.trace.flush.partial_drop', {
        count: batch.length,
        recorded,
        dropped,
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
      error: error instanceof Error && error.name ? error.name : 'Error',
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

/** @param {SanitizedTraceEvent} event @returns {boolean} */
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

/** @param {unknown} event @param {{ flush?: boolean }} options @returns {boolean} */
function emitFrontendTraceEvent(event, options = {}) {
  if (frontendTraceDisabled) return false;
  const sanitized = sanitizeFrontendTraceEvent(event);
  if (!sanitized || !shouldRemoteFlushFrontendTrace(sanitized)) return false;
  if (!enqueueFrontendTraceEvent(sanitized)) return false;
  if (options.flush !== false) scheduleFrontendTraceFlush();
  return true;
}

function flushFrontendTraceQueueForTest() {
  return flushFrontendTraceQueue();
}

/** @param {unknown} event @returns {TraceRecord | undefined} */
function runtimeTelemetryMetadata(event) {
  /** @type {TraceRecord} */
  const metadata = {};
  for (const key of ['req_id', 'pending_count', 'attempt']) {
    const value = runtimeTelemetryMetadataValue(event, key);
    if (value !== undefined && value !== null && value !== '') metadata[key] = value;
  }
  return Object.keys(metadata).length > 0 ? metadata : undefined;
}

/** @param {unknown} event @param {string} key @returns {unknown} */
function runtimeTelemetryMetadataValue(event, key) {
  if (!event || typeof event !== 'object' || Array.isArray(event)) return undefined;
  const source = /** @type {TraceRecord} */ (event);
  if (Object.prototype.hasOwnProperty.call(source, key)) return source[key];
  if (source.metadata && typeof source.metadata === 'object' && !Array.isArray(source.metadata)) {
    const metadata = /** @type {TraceRecord} */ (source.metadata);
    if (Object.prototype.hasOwnProperty.call(metadata, key)) return metadata[key];
  }
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

/** @param {number} start @returns {number} */
function elapsedMS(start) {
  if (!Number.isFinite(start)) {
    const error = new Error('bridge start timestamp is invalid');
    error.name = 'BridgeClockUnavailableError';
    throw error;
  }
  return Math.max(0, Math.round(currentMonotonicMS() - start));
}

/** @param {DateConstructor} clock @returns {string} */
function createFrontendTraceTimestamp(clock = Date) {
  if (!clock || typeof clock !== 'function') {
    const error = new Error('frontend trace wall clock is unavailable');
    error.name = 'BridgeClockUnavailableError';
    throw error;
  }
  return new clock().toISOString();
}

/** @param {unknown} event @returns {TraceRecord | null} */
function runtimeTelemetryTraceEvent(event) {
  if (!event || typeof event !== 'object' || Array.isArray(event)) return null;
  const source = /** @type {TraceRecord} */ (event);
  const traceEvent = {
    phase: source.phase,
    method: source.method,
    trace_id: source.trace_id,
    span_id: source.span_id,
    call_id: source.call_id,
    duration_ms: source.duration_ms,
    status: source.status,
    error: source.error,
    metadata: runtimeTelemetryMetadata(source),
  };
  return traceEvent;
}

/** @param {unknown} event */
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
  if (FRONTEND_RUNTIME_TRACE_SKIP_METHODS.has(safeTraceString(sanitized.method))) return;
  emitFrontendTraceEvent(traceEvent);
}

/** @param {unknown} error */
function logRuntimeTelemetryExternalHookFailed(error) {
  writeBridgeLog('warn', 'runtime.rpc.telemetry.external_hook_failed', { error });
}

function installRuntimeTelemetryHook() {
  if (typeof window === 'undefined') return;
  /** @typedef {((event: unknown) => void) & { __AO_BRIDGE_RUNTIME_TELEMETRY__?: boolean, __AO_PREVIOUS_RUNTIME_TELEMETRY__?: ((event: unknown) => void) | null }} TelemetryHook */
  const runtimeWindow = /** @type {Window & { __AO_WAILS_RUNTIME_TELEMETRY__?: TelemetryHook }} */ (window);
  const currentHook = typeof runtimeWindow.__AO_WAILS_RUNTIME_TELEMETRY__ === 'function'
    ? runtimeWindow.__AO_WAILS_RUNTIME_TELEMETRY__
    : null;
  const externalHook = currentHook?.__AO_BRIDGE_RUNTIME_TELEMETRY__ === true
    ? currentHook.__AO_PREVIOUS_RUNTIME_TELEMETRY__ || null
    : currentHook;
  /** @type {TelemetryHook} */
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
  runtimeWindow.__AO_WAILS_RUNTIME_TELEMETRY__ = hook;
}

installRuntimeTelemetryHook();


export {
  parseRuntimeEventNumber, parseRuntimeEventJSON, normalizeRuntimeEventEnvelope, subscribeRuntimeEvent, resolveClientMeta, createTraceContext,
  isUITestMCPTraceSuppressed, isFrontendTraceDebugEnabled, safeTraceErrorMessage, currentMonotonicMS, elapsedMS, createFrontendTraceTimestamp,
  installRuntimeTelemetryHook, emitFrontendTraceEvent, getFrontendTraceQueueHealth, flushFrontendTraceQueueForTest,
};
