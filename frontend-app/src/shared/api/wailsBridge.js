// Wails Bridge Adapter for React Frontend

const METHOD_IDS = Object.freeze({
  CALL_API: 2963398832,
  GET_BUILD_INFO: 2341363104,
  SAVE_CLIPBOARD_IMAGE: 3733550318,
  SELECT_FILES: 4126105303,
  SELECT_PROJECT_DIR: 3694631468,
});

let bridgeRequestSeq = 0;
let rpcRequestSeq = 0;
let runtimePromise = null;
const WAILS_RUNTIME_MODULE = '/wails/runtime.js';
const RPC_RESULT_PREVIEW_LIMIT = 1200;
const FRONTEND_TRACE_INGEST_METHOD = 'observability/frontend/ingest';
const FRONTEND_TRACE_BATCH_LIMIT = 50;
const FRONTEND_TRACE_QUEUE_LIMIT = 500;
const FRONTEND_TRACE_RPC_SLOW_MS = 1000;
const FRONTEND_TRACE_ALLOWED_PHASES = new Set([
  'frontend.rpc.start',
  'frontend.rpc.done',
  'frontend.rpc.failed',
  'runtime.rpc.pending',
  'runtime.rpc.send.done',
  'runtime.rpc.send.failed',
  'runtime.rpc.settled',
  'runtime.rpc.timeout',
  'runtime.rpc.failed',
  'frontend.warning',
  'frontend.vitals.fcp',
  'frontend.vitals.lcp',
  'frontend.longtask',
  'frontend.patch.apply.slow',
  'frontend.render.slow',
  'frontend.render.chat.slow',
  'frontend.render.threadrail.slow',
  'frontend.render.timeline.slow',
]);
const FRONTEND_TRACE_ALLOWED_METADATA_KEYS = new Set([
  'req_id',
  'component',
  'react_phase',
  'pending_count',
  'attempt',
]);
const FRONTEND_TRACE_ALLOWED_STATUSES = new Set(['ok', 'slow', 'error']);
const FRONTEND_RUNTIME_TRACE_DEFAULT_PHASES = new Set([
  'frontend.vitals.fcp',
  'frontend.vitals.lcp',
  'runtime.rpc.pending',
  'runtime.rpc.send.done',
  'runtime.rpc.settled',
]);
const FRONTEND_RUNTIME_TRACE_SKIP_METHODS = new Set([FRONTEND_TRACE_INGEST_METHOD, 'ui/log']);
// 误判防护：FRONTEND_TRACE_FORBIDDEN_KEYS 阻断 prompt/content/tool result 进入前端 trace。
const FRONTEND_TRACE_FORBIDDEN_KEYS = new Set([
  'result_preview',
  'prompt',
  'user_prompt',
  'user_message',
  'message_text',
  'text',
  'content',
  'file_content',
  'file_contents',
  'tool_result',
  'tool_results',
  'stack',
  'raw_stack',
]);
const BRIDGE_LOG_FORBIDDEN_KEYS = new Set([
  ...[...FRONTEND_TRACE_FORBIDDEN_KEYS].filter((key) => key !== 'result_preview'),
  'params',
  'raw_params',
  'request_params',
  'secret',
  'token',
  'password',
  'api_key',
  'auth',
  'credential',
  'credentials',
  'authorization',
  'auth_token',
  'access_token',
  'refresh_token',
  'id_token',
  'stack_trace',
  'stacktrace',
]);
const BRIDGE_ERROR_DATA_SAFE_KEYS = new Set(['message', 'code', 'name', 'type', 'status']);
const BRIDGE_REDACTED_VALUE = '[redacted]';

// Track active log store to pipe warnings and errors
let logStoreInstance = null;
export function registerBridgeLogStore(store) {
  logStoreInstance = store;
}

function serializableBridgeValue(value, seen = new WeakSet()) {
  if (!value || typeof value !== 'object') return value;
  if (seen.has(value)) return '[Circular]';
  seen.add(value);
  if (value instanceof Error) {
    const out = {
      name: value.name || 'Error',
      message: value.message || '',
    };
    if ('code' in value && value.code !== undefined) out.code = value.code;
    if ('data' in value && value.data !== undefined) out.data = serializableBridgeErrorData(value.data, seen);
    return out;
  }
  if (Array.isArray(value)) return value.map((item) => serializableBridgeValue(item, seen));
  const parentIsErrorLike = isBridgeErrorLikeObject(value);
  return Object.fromEntries(
    Object.entries(value)
      .filter(([key]) => !isForbiddenBridgeLogKey(key))
      .map(([key, item]) => [
        key,
        parentIsErrorLike && normalizeBridgeLogKey(key) === 'data'
          ? serializableBridgeErrorData(item, seen)
          : serializableBridgeValue(item, seen),
      ]),
  );
}

function isPlainBridgeObject(value) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false;
  const proto = Object.getPrototypeOf(value);
  return proto === Object.prototype || proto === null;
}

function hasOwnBridgeProperty(value, key) {
  return Object.prototype.hasOwnProperty.call(value, key);
}

function isBridgeErrorLikeObject(value) {
  if (!isPlainBridgeObject(value) || !hasOwnBridgeProperty(value, 'data')) return false;
  return ['message', 'code', 'name', 'type'].some((key) => hasOwnBridgeProperty(value, key));
}

function serializableBridgeDiagnosticValue(value) {
  if (value === undefined) return undefined;
  if (value === null) return null;
  if (typeof value === 'string') return BRIDGE_REDACTED_VALUE;
  if (typeof value === 'number' || typeof value === 'boolean') return value;
  return BRIDGE_REDACTED_VALUE;
}

function serializableBridgeErrorData(value, seen) {
  if (!isPlainBridgeObject(value)) return BRIDGE_REDACTED_VALUE;
  if (seen.has(value)) return BRIDGE_REDACTED_VALUE;
  seen.add(value);

  const out = {};
  for (const [key, item] of Object.entries(value)) {
    const normalizedKey = normalizeBridgeLogKey(key);
    if (!BRIDGE_ERROR_DATA_SAFE_KEYS.has(normalizedKey)) continue;
    if (isForbiddenBridgeLogKey(key)) continue;
    const safeValue = serializableBridgeDiagnosticValue(item);
    if (safeValue !== undefined) out[key] = safeValue;
  }
  return Object.keys(out).length > 0 ? out : BRIDGE_REDACTED_VALUE;
}

function normalizeBridgeLogKey(key) {
  return (key || '')
    .toString()
    .replace(/([a-z0-9])([A-Z])/g, '$1_$2')
    .replace(/[\s.-]+/g, '_')
    .toLowerCase();
}

function isForbiddenBridgeLogKey(key) {
  return BRIDGE_LOG_FORBIDDEN_KEYS.has(normalizeBridgeLogKey(key));
}

function writeBridgeLog(level, event, fields) {
  const serializableFields = serializableBridgeValue(fields || {});
  if (logStoreInstance && typeof logStoreInstance[level] === 'function') {
    logStoreInstance[level](event, serializableFields);
  } else {
    console[level === 'error' ? 'error' : 'log'](`[Bridge ${level}] ${event}`, serializableFields);
  }
}

function writeBridgeDebugLog(event, fields) {
  if (!isFrontendTraceDebugEnabled()) return;
  writeBridgeLog('debug', event, fields);
}

function writeBridgeSuccessDiagnosticLog(event, fields, isSlow) {
  if (isSlow) {
    writeBridgeLog('warn', event, fields);
    return;
  }
  writeBridgeDebugLog(event, fields);
}

function compactBridgeValuePreview(value) {
  const serializable = serializableBridgeValue(value);
  const text = typeof serializable === 'string' ? serializable : JSON.stringify(serializable);
  if (!text) return '';
  if (text.length <= RPC_RESULT_PREVIEW_LIMIT) return text;
  return `${text.slice(0, RPC_RESULT_PREVIEW_LIMIT)}...`;
}

function resolveRuntimeModuleSpecifier() {
  const origin = typeof window !== 'undefined' && window.location
    ? window.location.origin || ''
    : '';
  if (/^https?:\/\//.test(origin)) {
    return `${origin}${WAILS_RUNTIME_MODULE}`;
  }
  return WAILS_RUNTIME_MODULE;
}

function waitRuntime() {
  if (!runtimePromise) {
    writeBridgeLog('info', 'bridge.runtime.load.start', {});
    runtimePromise = import(/* @vite-ignore */ resolveRuntimeModuleSpecifier())
      .then((module) => {
        writeBridgeLog('info', 'bridge.runtime.load.done', {
          ready: Boolean(module?.Call?.ByID),
          has_events: Boolean(module?.Events?.On),
        });
        return module || null;
      })
      .catch((error) => {
        writeBridgeLog('error', 'bridge.runtime.load.failed', { error });
        runtimePromise = null; // allow retry on next call
        return null;
      });
  }
  return runtimePromise;
}

function parseRuntimeEventJSON(rawText) {
  let parsed;
  try {
    if (typeof rawText === 'string') {
      const preparedText = rawText
        .replace(/:\s*(-?\d{16,21})\b/g, ':"$1"')
        .replace(/([[,]\s*)(-?\d{16,21})\b/g, '$1"$2"');
      parsed = JSON.parse(preparedText);
    } else {
      parsed = JSON.parse(rawText);
    }
  }
  catch (error) {
    writeBridgeLog('warn', 'api.json.parse.failed', {
      error,
      raw_len: rawText.length,
      raw_preview: rawText.slice(0, 200),
    });
    return {};
  }
  return parsed;
}

export function normalizeRuntimeEventEnvelope(evt) {
  if (!evt || typeof evt !== 'object') return {};
  const hasWailsEnvelope = Object.prototype.hasOwnProperty.call(evt, 'name')
    && Object.prototype.hasOwnProperty.call(evt, 'data');
  if (!hasWailsEnvelope) return evt;

  const inner = evt.data;
  if (inner == null || inner === '') return {};
  if (typeof inner === 'object') return inner;
  if (typeof inner === 'string') return parseRuntimeEventJSON(inner);
  return { data: inner };
}

function subscribeRuntimeEvent(eventName, callback, options = {}) {
  let off = () => {};
  let cancelled = false;

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
      return;
    }
    const unbind = runtime.Events.On(eventName, wrapped);
    if (cancelled) {
      teardown(runtime, unbind);
      return;
    }
    off = () => {
      cancelled = true;
      teardown(runtime, unbind);
    };
  }).catch((error) => {
    writeBridgeLog('error', options.subscribeFailedLog || 'runtime.subscribe.failed', { eventName, error });
  });

  return () => {
    cancelled = true;
    off();
  };
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
  const text = (value || '').toString().trim();
  if (!text) return '';
  return text.length > limit ? `${text.slice(0, limit)}…` : text;
}

function safeTraceErrorMessage(error) {
  const code = safeTraceString(error?.code, 80);
  const name = safeTraceString(error?.name, 80);
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
  const normalized = safeTraceString(text, 512).toLowerCase();
  if (!normalized) return false;
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
  const durationMS = Number(event.duration_ms);
  const out = {
    ts: new Date().toISOString(),
    phase,
    status: FRONTEND_TRACE_ALLOWED_STATUSES.has(status) ? status : 'ok',
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
  if (event.status === 'error') return true;
  if (event.status === 'slow') return true;
  if (FRONTEND_RUNTIME_TRACE_DEFAULT_PHASES.has(event.phase)) return true;
  if (event.phase === 'frontend.patch.apply.slow' || event.phase === 'frontend.render.slow') return true;
  if (event.phase === 'frontend.rpc.done' && Number(event.duration_ms) >= FRONTEND_TRACE_RPC_SLOW_MS) return true;
  return isFrontendTraceDebugEnabled();
}

async function flushFrontendTraceQueue() {
  if (frontendTraceFlushInFlight || frontendTraceQueue.length === 0) return;
  frontendTraceFlushScheduled = false;
  frontendTraceFlushInFlight = true;
  const batch = frontendTraceQueue.splice(0, FRONTEND_TRACE_BATCH_LIMIT);
  try {
    const runtime = await waitRuntime();
    if (runtime?.Call?.ByID) {
      await runtime.Call.ByID(METHOD_IDS.CALL_API, FRONTEND_TRACE_INGEST_METHOD, { events: batch });
    }
  }
  catch (error) {
    console.warn('[Bridge warn] frontend.trace.flush.failed', {
      error: error?.name || 'Error',
      count: batch.length,
    });
  }
  finally {
    frontendTraceFlushInFlight = false;
    if (frontendTraceQueue.length > 0) scheduleFrontendTraceFlush();
  }
}

function scheduleFrontendTraceFlush() {
  if (frontendTraceFlushScheduled || frontendTraceFlushInFlight) return;
  frontendTraceFlushScheduled = true;
  void Promise.resolve()
    .then(flushFrontendTraceQueue)
    .catch((error) => {
      writeBridgeLog('error', 'frontend.trace.flush.schedule.failed', { error });
    });
}

function enqueueFrontendTraceEvent(event) {
  // 误判防护：enqueueFrontendTraceEvent 使用 FRONTEND_TRACE_QUEUE_LIMIT 限制 trace 队列。
  if (frontendTraceQueue.length >= FRONTEND_TRACE_QUEUE_LIMIT) {
    const overflow = frontendTraceQueue.length - FRONTEND_TRACE_QUEUE_LIMIT + 1;
    frontendTraceQueue.splice(0, overflow);
  }
  frontendTraceQueue.push(event);
}

export function emitFrontendTraceEvent(event, options = {}) {
  const sanitized = sanitizeFrontendTraceEvent(event);
  if (!shouldRemoteFlushFrontendTrace(sanitized)) return false;
  enqueueFrontendTraceEvent(sanitized);
  if (options.flush !== false) scheduleFrontendTraceFlush();
  return true;
}

function runtimeTelemetryMetadata(event) {
  const metadata = {};
  for (const key of ['req_id', 'pending_count', 'attempt']) {
    const value = event?.[key] ?? event?.metadata?.[key];
    if (value !== undefined && value !== null && value !== '') metadata[key] = value;
  }
  return Object.keys(metadata).length > 0 ? metadata : undefined;
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
        writeBridgeLog('warn', 'runtime.rpc.telemetry.external_hook_failed', { error });
      }
    }
    handleRuntimeTelemetryEvent(event);
  };
  hook.__AO_BRIDGE_RUNTIME_TELEMETRY__ = true;
  hook.__AO_PREVIOUS_RUNTIME_TELEMETRY__ = externalHook;
  window.__AO_WAILS_RUNTIME_TELEMETRY__ = hook;
}

installRuntimeTelemetryHook();

async function invokeRuntimeByID(methodID, args = [], options = {}) {
  const reqId = ++bridgeRequestSeq;
  const start = Date.now();
  writeBridgeDebugLog('bridge.call.start', {
    req_id: reqId,
    method_id: methodID,
    arg_count: args.length,
  });

  const runtime = await waitRuntime();
  if (!runtime?.Call?.ByID) {
    // 误判防护：invokeRuntimeByID 在 Wails runtime 不可用时抛错，不静默成功。
    if (options.logRuntimeUnavailable !== false) {
      writeBridgeLog('warn', 'bridge.call.runtime.unavailable', {
        req_id: reqId,
        method_id: methodID,
      });
    }
    throw new Error('Wails runtime bridge not ready');
  }

  let result;
  try {
    result = await runtime.Call.ByID(methodID, ...args);
  }
  catch (error) {
    if (options.logFailure !== false) {
      writeBridgeLog('error', 'bridge.call.failed', {
        req_id: reqId,
        method_id: methodID,
        duration_ms: Date.now() - start,
        error,
      });
    }
    throw error;
  }
  const durationMs = Date.now() - start;
  writeBridgeSuccessDiagnosticLog('bridge.call.done', {
    req_id: reqId,
    method_id: methodID,
    duration_ms: durationMs,
  }, durationMs >= FRONTEND_TRACE_RPC_SLOW_MS);
  return result;
}

function callByID(methodID, ...args) {
  return invokeRuntimeByID(methodID, args);
}

function normalizeAPIMethod(method, reqId) {
  if (!method || typeof method !== 'string' || !method.trim()) {
    const error = new Error('callAPI method must be a non-empty string');
    writeBridgeLog('warn', 'api.rpc.invalid_method', {
      req_id: reqId,
      error,
    });
    throw error;
  }
  return method;
}

function normalizeAPIPayload(method, params, reqId) {
  const rawPayload = params == null ? {} : params;
  if (typeof rawPayload !== 'object' || Array.isArray(rawPayload)) {
    const error = new TypeError('callAPI params must be an object');
    writeBridgeLog('warn', 'api.rpc.invalid_params', {
      req_id: reqId,
      method,
      param_type: typeof rawPayload,
      error,
    });
    throw error;
  }
  return rawPayload;
}

function createAPITrace(method, reqId) {
  const { clientKind, clientRoute } = resolveClientMeta();
  try {
    return {
      clientKind,
      clientRoute,
      trace: createTraceContext(),
    };
  }
  catch (error) {
    writeBridgeLog('error', 'api.rpc.trace_context_failed', {
      req_id: reqId,
      method,
      error,
    });
    throw error;
  }
}

function buildAPIPayload(rawPayload, reqId, clientKind, clientRoute, trace) {
  return {
    ...rawPayload,
    _aoClientKind: clientKind,
    _aoClientRoute: clientRoute,
    _aoRequestId: reqId,
    _aoTraceparent: trace.traceparent,
    _aoTraceId: trace.traceId,
    _aoSpanId: trace.spanId,
  };
}

function logAPIStart(reqId, method, payload, clientKind, clientRoute, trace) {
  writeBridgeDebugLog('api.rpc.start', {
    req_id: reqId,
    method,
    client_kind: clientKind,
    client_route: clientRoute,
    trace_id: trace.traceId,
    span_id: trace.spanId,
    traceparent: trace.traceparent,
    param_keys: Object.keys(payload),
  });
  emitFrontendTraceEvent({
    phase: 'frontend.rpc.start',
    method,
    trace_id: trace.traceId,
    span_id: trace.spanId,
    client_kind: clientKind,
    client_route: clientRoute,
    status: 'ok',
    metadata: { req_id: reqId },
  }, { flush: false });
}

function logAPIDone(reqId, method, start, result, clientKind, clientRoute, trace) {
  const durationMs = Date.now() - start;
  const status = durationMs >= FRONTEND_TRACE_RPC_SLOW_MS ? 'slow' : 'ok';
  writeBridgeSuccessDiagnosticLog('api.rpc.done', {
    req_id: reqId,
    method,
    client_kind: clientKind,
    client_route: clientRoute,
    trace_id: trace.traceId,
    span_id: trace.spanId,
    traceparent: trace.traceparent,
    duration_ms: durationMs,
    result_preview: compactBridgeValuePreview(result),
  }, status === 'slow');
  emitFrontendTraceEvent({
    phase: 'frontend.rpc.done',
    method,
    trace_id: trace.traceId,
    span_id: trace.spanId,
    client_kind: clientKind,
    client_route: clientRoute,
    duration_ms: durationMs,
    status,
    metadata: { req_id: reqId },
  });
}

function logAPIFailed(reqId, method, start, error, clientKind, clientRoute, trace) {
  const durationMs = Date.now() - start;
  writeBridgeLog('error', 'api.rpc.failed', {
    req_id: reqId,
    method,
    client_kind: clientKind,
    client_route: clientRoute,
    trace_id: trace.traceId,
    span_id: trace.spanId,
    traceparent: trace.traceparent,
    duration_ms: durationMs,
    error,
  });
  emitFrontendTraceEvent({
    phase: 'frontend.rpc.failed',
    method,
    trace_id: trace.traceId,
    span_id: trace.spanId,
    client_kind: clientKind,
    client_route: clientRoute,
    duration_ms: durationMs,
    status: 'error',
    error: safeTraceErrorMessage(error),
    metadata: { req_id: reqId },
  });
}

function attachAPITraceToError(error, reqId, method, clientKind, clientRoute, trace) {
  if (!error || (typeof error !== 'object' && typeof error !== 'function')) return error;
  error.traceId = trace.traceId;
  error.trace_id = trace.traceId;
  error.spanId = trace.spanId;
  error.span_id = trace.spanId;
  error.traceparent = trace.traceparent;
  error.reqId = reqId;
  error.req_id = reqId;
  error.method = method;
  error.clientKind = clientKind;
  error.clientRoute = clientRoute;
  return error;
}

export async function callAPI(method, params = {}) {
  const reqId = ++rpcRequestSeq;
  const start = Date.now();
  const rpcMethod = normalizeAPIMethod(method, reqId);
  const rawPayload = normalizeAPIPayload(rpcMethod, params, reqId);
  const { clientKind, clientRoute, trace } = createAPITrace(rpcMethod, reqId);
  const payload = buildAPIPayload(rawPayload, reqId, clientKind, clientRoute, trace);
  logAPIStart(reqId, rpcMethod, payload, clientKind, clientRoute, trace);

  let result;
  try {
    result = await invokeRuntimeByID(METHOD_IDS.CALL_API, [rpcMethod, payload], {
      logFailure: false,
      logRuntimeUnavailable: false,
    });
  }
  catch (error) {
    // 误判防护：callAPI 附加 trace 后继续抛出错误，不吞掉 backend/runtime 失败。
    const tracedError = attachAPITraceToError(error, reqId, rpcMethod, clientKind, clientRoute, trace);
    logAPIFailed(reqId, rpcMethod, start, tracedError, clientKind, clientRoute, trace);
    throw tracedError;
  }
  logAPIDone(reqId, rpcMethod, start, result, clientKind, clientRoute, trace);
  return result;
}

export async function sendFrontendLogBatch(entries) {
  const batch = Array.isArray(entries) ? entries.filter(Boolean) : [];
  if (batch.length === 0) return;
  const runtime = await waitRuntime();
  if (!runtime?.Call?.ByID) {
    throw new Error('frontend log bridge runtime Call.ByID is required');
  }
  const { clientKind, clientRoute } = resolveClientMeta();
  await runtime.Call.ByID(METHOD_IDS.CALL_API, 'ui/log', {
    entries: batch,
    _aoClientKind: clientKind,
    _aoClientRoute: clientRoute,
  });
}

export async function selectProjectDir(defaultPath = '') {
  const seed = typeof defaultPath === 'string' ? defaultPath : '';
  writeBridgeLog('info', 'ui.selectProjectDir.start', { default_path: seed });

  if (!seed) {
    try {
      const value = await callByID(METHOD_IDS.SELECT_PROJECT_DIR);
      if (typeof value === 'string') {
        writeBridgeLog('info', 'ui.selectProjectDir.done', {
          selected: Boolean(value),
          path: value,
          via: 'binding',
        });
        return value;
      }
    }
    catch (error) {
      writeBridgeLog('warn', 'ui.selectProjectDir.byId.failed', { error });
    }
  }

  const raw = await callAPI('ui/selectProjectDir', { defaultPath: seed });
  const path = raw && typeof raw === 'object' && typeof raw.path === 'string' ? raw.path : '';
  writeBridgeLog('info', 'ui.selectProjectDir.done', {
    selected: Boolean(path),
    path,
    via: 'rpc',
  });
  return path;
}

export async function selectProjectDirs() {
  writeBridgeLog('info', 'ui.selectProjectDirs.start', {});
  const raw = await callAPI('ui/selectProjectDirs', {});
  const paths = Array.isArray(raw?.paths) ? raw.paths : [];
  writeBridgeLog('info', 'ui.selectProjectDirs.done', {
    count: paths.length,
    first: paths[0] || '',
  });
  return paths;
}

export async function selectFiles() {
  writeBridgeLog('info', 'ui.selectFiles.start', {});
  const normalize = (raw) => {
    if (Array.isArray(raw)) return raw;
    if (raw && typeof raw === 'object' && Array.isArray(raw.paths)) return raw.paths;
    return null;
  };

  try {
    const values = await callByID(METHOD_IDS.SELECT_FILES);
    const files = normalize(values);
    if (files != null) {
      writeBridgeLog('info', 'ui.selectFiles.done', {
        count: files.length,
        first: files[0] || '',
      });
      return files;
    }
  }
  catch (error) {
    writeBridgeLog('warn', 'ui.selectFiles.byId.failed', { error });
  }

  const raw = await callAPI('ui/selectFiles', {});
  const files = normalize(raw) || [];
  writeBridgeLog('info', 'ui.selectFiles.done', {
    count: files.length,
    first: files[0] || '',
  });
  return files;
}

export async function readDroppedTextFiles(files, targetId = '') {
  const paths = Array.isArray(files)
    ? files.map((item) => (item || '').toString().trim()).filter(Boolean)
    : [];
  if (paths.length === 0) return [];
  writeBridgeLog('info', 'ui.readDroppedTextFiles.start', {
    count: paths.length,
    target_id: targetId,
  });
  const raw = await callAPI('ui/readDroppedTextFiles', {
    files: paths,
    targetId: targetId,
  });
  const items = Array.isArray(raw?.files) ? raw.files : [];
  return items.map((item) => ({
    path: (item?.path || '').toString(),
    name: (item?.name || '').toString(),
    text: (item?.text || '').toString(),
    sizeBytes: Number(item?.sizeBytes) || 0,
  }));
}

export async function saveClipboardImage(base64Payload) {
  const start = Date.now();
  const path = (await callByID(METHOD_IDS.SAVE_CLIPBOARD_IMAGE, base64Payload)) || '';
  writeBridgeLog('debug', 'ui.clipboardImage.saved', {
    ok: Boolean(path),
    duration_ms: Date.now() - start,
  });
  return path;
}

export async function saveTextFile({ defaultPath = '', defaultFilename = '', content = '' } = {}) {
  const filename = (defaultFilename || '').toString().trim();
  if (!filename) throw new Error('saveTextFile defaultFilename is required');
  writeBridgeLog('info', 'ui.saveTextFile.start', {
    default_path: defaultPath,
    default_filename: filename,
    content_len: content.length,
  });
  const raw = await callAPI('ui/saveTextFile', {
    defaultPath,
    defaultFilename: filename,
    content,
  });
  const path = raw && typeof raw === 'object' && typeof raw.path === 'string' ? raw.path : '';
  writeBridgeLog('info', 'ui.saveTextFile.done', {
    selected: Boolean(path),
    path,
  });
  return path;
}

export async function openSharedFile({ path } = {}) {
  const filePath = (path || '').toString().trim();
  if (!filePath) throw new Error('openSharedFile path is required');
  writeBridgeLog('info', 'ui.openSharedFile.start', { path: filePath });
  const raw = await callAPI('ui/sharedFile/open', { path: filePath });
  writeBridgeLog('info', 'ui.openSharedFile.done', { path: filePath });
  return raw && typeof raw === 'object' ? raw : {};
}

export async function copyTextToClipboard(text) {
  const value = (text || '').toString().trim();
  if (!value) throw new Error('clipboard text is empty');

  const failures = [];
  if (await copyTextViaNativeBridge(value, failures)) return true;
  if (await copyTextViaClipboardAPI(value, failures)) return true;
  if (copyTextViaExecCommand(value, failures)) return true;

  throw new Error(`clipboard copy failed: ${failures.join('; ')}`);
}

function isDebugRuntimeShim() {
  return typeof window !== 'undefined' && window.__WAILS_SHIM_DEBUG__ === true;
}

async function copyTextViaNativeBridge(value, failures) {
  if (isDebugRuntimeShim()) return false;
  try {
    const res = await callAPI('ui/copyText', { text: value });
    if (res?.ok) return true;
    failures.push(`native ui/copyText returned ok=false${res?.error ? `: ${res.error}` : ''}`);
  }
  catch (error) {
    failures.push(`native ui/copyText failed: ${error.message || String(error)}`);
  }
  return false;
}

async function copyTextViaClipboardAPI(value, failures) {
  if (!navigator?.clipboard?.writeText) {
    failures.push('browser clipboard.writeText is unavailable');
    return false;
  }
  let copied = false;
  try {
    await navigator.clipboard.writeText(value);
    copied = true;
  }
  catch (error) {
    failures.push(`browser clipboard.writeText failed: ${error.message || String(error)}`);
    writeBridgeLog('warn', 'ui.copyText.clipboard_api_failed', { error: error.message || String(error) });
  }
  return copied;
}

function copyTextViaExecCommand(value, failures) {
  let copied = false;
  try {
    if (!document?.body || typeof document.execCommand !== 'function') {
      throw new Error('document.execCommand is unavailable');
    }
    const textarea = createClipboardTextarea(value);
    document.body.appendChild(textarea);
    const selection = document.getSelection?.();
    const ranges = getSelectionRanges(selection);
    try {
      textarea.focus();
      textarea.select();
      textarea.setSelectionRange?.(0, value.length);
      copied = document.execCommand('copy');
      if (!copied) throw new Error("document.execCommand('copy') returned false");
    }
    finally {
      document.body.removeChild(textarea);
      if (selection) {
        selection.removeAllRanges();
        ranges.forEach((range) => selection.addRange(range));
      }
    }
  }
  catch (error) {
    failures.push(`document.execCommand fallback failed: ${error.message || String(error)}`);
    writeBridgeLog('warn', 'ui.copyText.exec_command_failed', { error: error.message || String(error) });
  }
  return copied;
}

function createClipboardTextarea(value) {
  const textarea = document.createElement('textarea');
  textarea.value = value;
  textarea.style.position = 'fixed';
  textarea.style.top = '0';
  textarea.style.left = '-9999px';
  textarea.style.opacity = '0';
  textarea.setAttribute('readonly', '');
  return textarea;
}

function getSelectionRanges(selection) {
  if (!selection) return [];
  return Array.from({ length: selection.rangeCount }, (_, index) => selection.getRangeAt(index));
}

export function beginTextClipboardWrite() {
  if (
    typeof navigator === 'undefined' ||
    typeof navigator.clipboard?.write !== 'function' ||
    typeof ClipboardItem === 'undefined' ||
    typeof Blob === 'undefined'
  ) {
    return null;
  }

  let settled = false;
  let resolveBlob;
  let rejectBlob;
  const blobPromise = new Promise((resolve, reject) => {
    resolveBlob = resolve;
    rejectBlob = reject;
  });

  let writePromise;
  try {
    writePromise = navigator.clipboard.write([
      new ClipboardItem({
        'text/plain': blobPromise,
      }),
    ]);
  }
  catch {
    return null;
  }

  writePromise.catch(() => {});

  return {
    async commit(text) {
      if (settled) throw new Error('prepared clipboard write is already settled');
      const value = (text || '').toString().trim();
      if (!value) {
        settled = true;
        rejectBlob(new Error('clipboard text is empty'));
        throw new Error('clipboard text is empty');
      }
      settled = true;
      resolveBlob(new Blob([value], { type: 'text/plain' }));
      await writePromise;
      return true;
    },
    cancel(reason) {
      if (settled) return;
      settled = true;
      rejectBlob(reason instanceof Error ? reason : new Error('clipboard write cancelled'));
    },
  };
}

export async function resolveThreadIdentity(threadId) {
  const id = (threadId || '').toString().trim();
  if (!id) return {};
  const res = await callAPI('thread/resolve', { threadId: id });
  return res && typeof res === 'object' ? res : {};
}

export async function getBuildInfo() {
  const raw = await callByID(METHOD_IDS.GET_BUILD_INFO);
  return raw && typeof raw === 'object' ? raw : {};
}

export function onAgentEvent(callback) {
  return subscribeRuntimeEvent('agent-event', callback, {
    callbackFailedLog: 'agent.callback.failed',
    subscribeUnavailableLog: 'agent.subscribe.unavailable',
    subscribeReadyLog: 'agent.subscribe.ready',
    unsubscribeDoneLog: 'agent.unsubscribe.done',
  });
}

export function onBridgeEvent(callback, options = {}) {
  return subscribeRuntimeEvent('bridge-event', callback, {
    callbackFailedLog: 'bridge.callback.failed',
    subscribeUnavailableLog: 'bridge.subscribe.unavailable',
    subscribeReadyLog: 'bridge.subscribe.ready',
    unsubscribeDoneLog: 'bridge.unsubscribe.done',
    ...options,
  });
}

export function onFilesDropped(callback) {
  return subscribeRuntimeEvent('files-dropped', callback, {
    callbackFailedLog: 'filesDropped.callback.failed',
    subscribeUnavailableLog: 'filesDropped.subscribe.unavailable',
    subscribeReadyLog: 'filesDropped.subscribe.ready',
    unsubscribeDoneLog: 'filesDropped.unsubscribe.done',
  });
}

export function onAppWillQuit(callback) {
  return subscribeRuntimeEvent('app-will-quit', callback, {
    callbackFailedLog: 'appWillQuit.callback.failed',
    subscribeUnavailableLog: 'appWillQuit.subscribe.unavailable',
    subscribeReadyLog: 'appWillQuit.subscribe.ready',
    unsubscribeDoneLog: 'appWillQuit.unsubscribe.done',
  });
}

export function onRuntimeReconnect(callback) {
  return subscribeRuntimeEvent('wails:loaded', callback, {
    callbackFailedLog: 'reconnect.callback.failed',
    subscribeUnavailableLog: 'reconnect.subscribe.unavailable',
    subscribeReadyLog: 'reconnect.subscribe.ready',
    unsubscribeDoneLog: 'reconnect.unsubscribe.done',
  });
}
