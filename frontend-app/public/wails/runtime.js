// @ts-nocheck
// Development Wails runtime shim.
//
// When the desktop shell is launched with
// FRONTEND_DEVSERVER_URL=http://localhost:5173, the WebView loads this file
// from the Vite dev server instead of Wails' native /wails/runtime.js.  The
// production build still treats /wails/runtime.js as external; this file is
// only served by Vite during local development and bridges calls/events over
// the backend WebSocket endpoint exposed at /wails/ws.

const METHOD_IDS = Object.freeze({
  CALL_API: 1391035622,
  GET_BUILD_INFO: 4089071466,
  SAVE_CLIPBOARD_IMAGE: 333893560,
  SELECT_FILES: 3596120745,
  SELECT_PROJECT_DIR: 1814866518,
});

const JSONRPC_VERSION = '2.0';
const WS_PATH = '/wails/ws';
const EVENT_RECONNECT_DELAY_MS = 500;
const RPC_SEND_ATTEMPT = 1;
const RUNTIME_TELEMETRY_EVENT = 'ao:wails-runtime-telemetry';

let nextRequestId = 1;
let socket = null;
let connectPromise = null;
let eventReconnectTimer = null;
let reconnectNotificationPending = false;
const pendingCalls = new Map();
const eventListeners = new Map();

markDebugShim();

function markDebugShim() {
  const win = resolveWindow();
  if (win) {
    win.__WAILS_SHIM_DEBUG__ = true;
  }
}

function resolveWindow() {
  if (typeof window !== 'undefined') return window;
  if (typeof globalThis !== 'undefined' && globalThis.window) return globalThis.window;
  return null;
}

function resolveWebSocketCtor() {
  if (typeof WebSocket !== 'undefined') return WebSocket;
  const win = resolveWindow();
  return win?.WebSocket || null;
}

function wsState(name, fallback) {
  const ctor = resolveWebSocketCtor();
  return typeof ctor?.[name] === 'number' ? ctor[name] : fallback;
}

function isSocketOpen(candidate) {
  return Boolean(candidate) && candidate.readyState === wsState('OPEN', 1);
}

function isSocketConnecting(candidate) {
  return Boolean(candidate) && candidate.readyState === wsState('CONNECTING', 0);
}

function resolveWebSocketURL() {
  const win = resolveWindow();
  const loc = win?.location;
  if (loc?.host) {
    const protocol = loc.protocol === 'https:' ? 'wss:' : 'ws:';
    return `${protocol}//${loc.host}${WS_PATH}`;
  }
  return `ws://127.0.0.1:4511${WS_PATH}`;
}

function toError(value, fallbackMessage) {
  if (value instanceof Error) return value;
  const message = value && typeof value === 'object' && typeof value.message === 'string'
    ? value.message
    : fallbackMessage;
  const error = new Error(message || 'runtime shim: websocket error');
  if (value && typeof value === 'object') {
    if ('code' in value) error.code = value.code;
    if ('data' in value) error.data = value.data;
  }
  return error;
}

function closeError(event) {
  const code = typeof event?.code === 'number' ? event.code : 0;
  const reason = typeof event?.reason === 'string' ? event.reason : '';
  return new Error(`runtime shim: websocket closed${code ? ` (${code})` : ''}${reason ? `: ${reason}` : ''}`);
}

function rejectPending(error, failureCode = 'websocket_closed') {
  if (pendingCalls.size === 0) return;
  const normalized = toError(error, 'runtime shim: websocket closed');
  for (const [callId, pending] of [...pendingCalls.entries()]) {
    pendingCalls.delete(callId);
    emitPendingTelemetry(pending, {
      phase: 'runtime.rpc.failed',
      status: 'error',
      error: failureCode,
      duration_ms: pendingSettleDuration(pending),
    });
    pending.reject(normalized);
  }
}

function hasEventListeners() {
  for (const callbacks of eventListeners.values()) {
    if (callbacks?.size > 0) return true;
  }
  return false;
}

function clearEventReconnectIfIdle() {
  if (hasEventListeners()) return;
  if (eventReconnectTimer != null) {
    clearTimeout(eventReconnectTimer);
    eventReconnectTimer = null;
  }
  reconnectNotificationPending = false;
}

function scheduleEventReconnect(error) {
  if (!hasEventListeners() || eventReconnectTimer != null) return;
  reconnectNotificationPending = true;
  eventReconnectTimer = setTimeout(() => {
    eventReconnectTimer = null;
    if (!hasEventListeners() || isSocketOpen(socket) || isSocketConnecting(socket)) return;
    ensureSocket().catch((nextError) => {
      console.warn('[wails-dev-shim] event bridge reconnect failed', nextError || error);
      scheduleEventReconnect(nextError || error);
    });
  }, EVENT_RECONNECT_DELAY_MS);
}

function ensureSocket() {
  if (isSocketOpen(socket)) return Promise.resolve(socket);
  if (isSocketConnecting(socket) && connectPromise) return connectPromise;

  const WebSocketCtor = resolveWebSocketCtor();
  if (!WebSocketCtor) {
    return Promise.reject(new Error('runtime shim: WebSocket is not available'));
  }

  connectPromise = new Promise((resolve, reject) => {
    let settled = false;
    const nextSocket = new WebSocketCtor(resolveWebSocketURL());
    socket = nextSocket;

    const failConnect = (error) => {
      if (settled) return;
      settled = true;
      if (socket === nextSocket) socket = null;
      connectPromise = null;
      reject(toError(error, 'runtime shim: websocket connect failed'));
    };

    nextSocket.onopen = () => {
      if (settled) return;
      settled = true;
      connectPromise = null;
      const notifyReconnect = reconnectNotificationPending;
      reconnectNotificationPending = false;
      resolve(nextSocket);
      if (notifyReconnect) emitEvent('wails:loaded', {});
    };

    nextSocket.onerror = (event) => {
      if (!settled) {
        failConnect(new Error(`runtime shim: failed to connect ${resolveWebSocketURL()}`));
        return;
      }
      const err = toError(event, 'runtime shim: websocket error');
      if (socket === nextSocket) socket = null;
      connectPromise = null;
      rejectPending(err, 'websocket_error');
      scheduleEventReconnect(err);
    };

    nextSocket.onclose = (event) => {
      const err = closeError(event);
      if (socket === nextSocket) socket = null;
      if (!settled) {
        failConnect(err);
        return;
      }
      rejectPending(err);
      scheduleEventReconnect(err);
    };

    nextSocket.onmessage = (event) => {
      void Promise.resolve(readSocketText(event?.data))
        .then(handleSocketText)
        .catch((error) => {
          // A malformed push frame must not break the bridge; keep the error
          // visible in devtools because this shim only runs during development.
          console.warn('[wails-dev-shim] failed to handle websocket message', error);
        });
    };
  });

  return connectPromise;
}

function readSocketText(data) {
  if (typeof data === 'string') return data;
  if (data == null) return '';
  if (typeof data.text === 'function') return data.text();
  if (data instanceof ArrayBuffer) return new TextDecoder().decode(data);
  if (ArrayBuffer.isView(data)) return new TextDecoder().decode(data);
  return String(data);
}

function hasOwn(obj, key) {
  return Object.prototype.hasOwnProperty.call(obj, key);
}

function handleSocketText(text) {
  const raw = (text || '').toString().trim();
  if (!raw) return;
  const message = JSON.parse(raw);
  if (Array.isArray(message)) {
    message.forEach(handleJSONRPCMessage);
    return;
  }
  handleJSONRPCMessage(message);
}

function handleJSONRPCMessage(message) {
  if (!message || typeof message !== 'object') return;

  if (hasOwn(message, 'id')) {
    const key = String(message.id);
    const pending = pendingCalls.get(key);
    if (!pending) return;
    pendingCalls.delete(key);
    if (message.error) {
      emitPendingTelemetry(pending, {
        phase: 'runtime.rpc.settled',
        status: 'error',
        error: 'rpc_error',
        duration_ms: pendingSettleDuration(pending),
      });
      pending.reject(toError(message.error, 'runtime shim: rpc call failed'));
      return;
    }
    emitPendingTelemetry(pending, {
      phase: 'runtime.rpc.settled',
      status: 'ok',
      duration_ms: pendingSettleDuration(pending),
    });
    pending.resolve(message.result);
    return;
  }

  if (typeof message.method === 'string' && message.method.trim()) {
    emitRPCNotification(message.method.trim(), message.params);
  }
}

function normalizePayload(params) {
  if (params && typeof params === 'object' && !Array.isArray(params)) return params;
  if (params == null) return {};
  return { data: params };
}

function firstPayloadString(payload, keys) {
  for (const key of keys) {
    const value = typeof payload?.[key] === 'string' ? payload[key].trim() : '';
    if (value) return value;
  }
  return '';
}

function emitEvent(eventName, data) {
  const callbacks = eventListeners.get(eventName);
  if (!callbacks || callbacks.size === 0) return;
  const envelope = { name: eventName, data };
  for (const callback of [...callbacks]) {
    try {
      callback(envelope);
    }
    catch (error) {
      console.warn(`[wails-dev-shim] ${eventName} callback failed`, error);
    }
  }
}

function emitRPCNotification(method, params) {
  const payload = normalizePayload(params);
  emitEvent('bridge-event', { type: method, method, payload });

  const threadId = firstPayloadString(payload, ['threadId', 'thread_id', 'agent_id', 'agentId']);
  if (threadId) {
    emitEvent('agent-event', {
      agent_id: threadId,
      type: method,
      payload,
    });
  }

  // Keep a raw-method escape hatch for future debug consumers.
  emitEvent(method, payload);
}

// Long-running backend operations need a generous timeout; fast UI calls keep
// a short one to surface disconnects quickly. Methods are matched by substring
// so new endpoints don't silently inherit the short default.
const DREAM_RPC_TIMEOUT_MS = 300_000;
const UPDATE_RPC_TIMEOUT_MS = 900_000;
const INTERACTIVE_RPC_TIMEOUT_MS = 900_000;
const LONG_RPC_TIMEOUT_MS = 120_000;
const SHORT_RPC_TIMEOUT_MS = 30_000;
const SESSION_ESTABLISHMENT_RPC_METHODS = new Set(['turn/start']);
const DREAM_RPC_PATTERNS = ['prompt-intents/draft'];
const UPDATE_RPC_PATTERNS = ['app/update/download', 'app/update/installlatest'];
const INTERACTIVE_RPC_PATTERNS = ['ui/selectfiles', 'ui/selectprojectdir', 'ui/selectprojectdirs', 'ui/selectdatasourceimportfile'];
const LONG_RPC_PATTERNS = ['compact', 'summary', 'memory', 'dream', 'extract', 'state/get', 'fork', 'cron'];

function rpcTimeoutMs(methodName) {
  const lower = methodName.toLowerCase();
  if (SESSION_ESTABLISHMENT_RPC_METHODS.has(lower)) return LONG_RPC_TIMEOUT_MS;
  for (const pattern of DREAM_RPC_PATTERNS) {
    if (lower.includes(pattern)) return DREAM_RPC_TIMEOUT_MS;
  }
  for (const pattern of UPDATE_RPC_PATTERNS) {
    if (lower.includes(pattern)) return UPDATE_RPC_TIMEOUT_MS;
  }
  for (const pattern of INTERACTIVE_RPC_PATTERNS) {
    if (lower.includes(pattern)) return INTERACTIVE_RPC_TIMEOUT_MS;
  }
  for (const pattern of LONG_RPC_PATTERNS) {
    if (lower.includes(pattern)) return LONG_RPC_TIMEOUT_MS;
  }
  return SHORT_RPC_TIMEOUT_MS;
}

function safeTelemetryString(value, limit = 160) {
  const text = (value ?? '').toString().trim();
  if (!text) return '';
  return text.length > limit ? text.slice(0, limit) : text;
}

function safeTelemetryNumber(value) {
  const number = Number(value);
  if (!Number.isFinite(number) || number < 0) return undefined;
  return Math.round(number);
}

function extractTelemetryContext(params) {
  if (!params || typeof params !== 'object' || Array.isArray(params)) return {};
  return {
    reqId: safeTelemetryNumber(params._aoRequestId),
    traceId: safeTelemetryString(params._aoTraceId, 64),
    spanId: safeTelemetryString(params._aoSpanId, 32),
  };
}

function emitRuntimeTelemetry(event) {
  const win = resolveWindow();
  if (!win) return;
  const detail = {};
  for (const [target, source, limit] of [
    ['phase', 'phase', 80],
    ['method', 'method', 160],
    ['trace_id', 'trace_id', 64],
    ['span_id', 'span_id', 32],
    ['call_id', 'call_id', 80],
    ['status', 'status', 32],
    ['error', 'error', 80],
  ]) {
    const value = safeTelemetryString(event[source], limit);
    if (value) detail[target] = value;
  }
  for (const [target, source] of [
    ['duration_ms', 'duration_ms'],
    ['req_id', 'req_id'],
    ['pending_count', 'pending_count'],
    ['attempt', 'attempt'],
  ]) {
    const value = safeTelemetryNumber(event[source]);
    if (value !== undefined) detail[target] = value;
  }
  if (!detail.phase) return;

  const hook = win.__AO_WAILS_RUNTIME_TELEMETRY__;
  if (typeof hook === 'function') {
    try {
      hook(detail);
    }
    catch (error) {
      console.warn('[wails-dev-shim] runtime telemetry hook failed', error);
    }
  }
  if (typeof win.dispatchEvent === 'function' && typeof win.CustomEvent === 'function') {
    try {
      win.dispatchEvent(new win.CustomEvent(RUNTIME_TELEMETRY_EVENT, { detail }));
    }
    catch (error) {
      console.warn('[wails-dev-shim] runtime telemetry event dispatch failed', error);
    }
  }
}

function pendingTelemetryBase(pending) {
  return {
    method: pending.methodName,
    trace_id: pending.traceId,
    span_id: pending.spanId,
    call_id: pending.callId,
    req_id: pending.reqId,
    pending_count: pendingCalls.size,
  };
}

function pendingSettleDuration(pending) {
  return Date.now() - (pending.sendAt || pending.pendingAt);
}

function emitPendingTelemetry(pending, fields) {
  emitRuntimeTelemetry({
    ...pendingTelemetryBase(pending),
    ...fields,
  });
}

async function rpcCall(method, params = {}, telemetryContext = {}) {
  const callStartAt = Date.now();
  const methodName = (method || '').toString().trim();
  if (!methodName) throw new Error('runtime shim: rpc method is required');

  const id = nextRequestId++;
  const request = {
    jsonrpc: JSONRPC_VERSION,
    id,
    method: methodName,
    params: params == null ? {} : params,
  };

  const activeSocket = await ensureSocket();
  if (!isSocketOpen(activeSocket)) {
    throw new Error('runtime shim: websocket is not open');
  }

  const timeoutMs = rpcTimeoutMs(methodName);
  return new Promise((resolve, reject) => {
    const callId = String(id);
    const pending = registerPendingCall(callId, methodName, timeoutMs, resolve, reject, {
      ...telemetryContext,
      callStartAt,
    });
    sendRPCRequest(activeSocket, request, pending);
  });
}

function registerPendingCall(callId, methodName, timeoutMs, resolve, reject, telemetryContext = {}) {
  const pendingAt = Date.now();
  const pending = {
    callId,
    methodName,
    timeoutMs,
    reqId: telemetryContext.reqId,
    traceId: telemetryContext.traceId || '',
    spanId: telemetryContext.spanId || '',
    callStartAt: telemetryContext.callStartAt || pendingAt,
    pendingAt,
    sendAt: 0,
    timer: null,
    resolve: null,
    reject: null,
  };
  const timer = setTimeout(() => {
    if (!pendingCalls.has(callId)) return;
    pendingCalls.delete(callId);
    emitPendingTelemetry(pending, {
      phase: 'runtime.rpc.timeout',
      status: 'error',
      error: 'timeout',
      duration_ms: pendingSettleDuration(pending),
    });
    pending.reject(new Error(`runtime shim: rpc call timeout (${timeoutMs / 1000}s) for ${methodName}`));
  }, timeoutMs);
  pending.timer = timer;
  pending.resolve = (value) => { clearTimeout(timer); resolve(value); };
  pending.reject = (error) => { clearTimeout(timer); reject(error); };
  pendingCalls.set(callId, pending);
  emitPendingTelemetry(pending, {
    phase: 'runtime.rpc.pending',
    status: 'ok',
    duration_ms: pendingAt - pending.callStartAt,
  });
  return pending;
}

function sendRPCRequest(activeSocket, request, pending) {
  const sendStart = Date.now();
  try {
    activeSocket.send(JSON.stringify(request));
  }
  catch (error) {
    pendingCalls.delete(pending.callId);
    emitPendingTelemetry(pending, {
      phase: 'runtime.rpc.send.failed',
      status: 'error',
      error: 'send_failed',
      duration_ms: Date.now() - sendStart,
      attempt: RPC_SEND_ATTEMPT,
    });
    pending.reject(toError(error, 'runtime shim: websocket send failed'));
    return;
  }
  pending.sendAt = Date.now();
  emitPendingTelemetry(pending, {
    phase: 'runtime.rpc.send.done',
    status: 'ok',
    duration_ms: pending.sendAt - sendStart,
    attempt: RPC_SEND_ATTEMPT,
  });
}

function normalizeListResult(raw) {
  if (Array.isArray(raw)) return raw;
  if (raw && typeof raw === 'object' && Array.isArray(raw.paths)) return raw.paths;
  return [];
}

function stripFrontendMetaPayload(value) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return value;
  let changed = false;
  const cleaned = {};
  for (const [key, item] of Object.entries(value)) {
    if (key.startsWith('_ao') && !isFrontendTraceMetaKey(key)) {
      changed = true;
      continue;
    }
    cleaned[key] = item;
  }
  return changed ? cleaned : value;
}

function isFrontendTraceMetaKey(key) {
  return key === '_aoTraceparent' || key === '_aoTraceId' || key === '_aoSpanId';
}

function stripFrontendTraceMetaPayload(value) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return value;
  let changed = false;
  const cleaned = {};
  for (const [key, item] of Object.entries(value)) {
    if (isFrontendTraceMetaKey(key)) {
      changed = true;
      continue;
    }
    cleaned[key] = item;
  }
  return changed ? cleaned : value;
}

function getBuildInfoFallback() {
  const defaultBuildInfo = { version: 'dev' };
  return (
    rpcCall('ui/buildInfo', {})
    .then((info) => (info && typeof info === 'object' ? info : {}))
    .catch(() => defaultBuildInfo)
  );
}

async function callByID(methodID, ...args) {
  const id = Number(methodID);
  switch (id) {
    case METHOD_IDS.CALL_API: {
      const method = (args[0] || '').toString().trim();
      const params = args.length > 1 ? args[1] : {};
      const payload = params == null ? {} : params;
      const telemetryContext = extractTelemetryContext(payload);
      return rpcCall(
        method,
        method === 'ui/log' ? stripFrontendTraceMetaPayload(payload) : stripFrontendMetaPayload(payload),
        telemetryContext,
      );
    }
    case METHOD_IDS.GET_BUILD_INFO: {
      return getBuildInfoFallback();
    }
    case METHOD_IDS.SAVE_CLIPBOARD_IMAGE: {
      const base64Payload = (args[0] || '').toString();
      const result = await rpcCall('ui/saveClipboardImage', { base64Payload });
      return (result?.path || '').toString();
    }
    case METHOD_IDS.SELECT_FILES: {
      const result = await rpcCall('ui/selectFiles', {});
      return normalizeListResult(result);
    }
    case METHOD_IDS.SELECT_PROJECT_DIR: {
      const result = await rpcCall('ui/selectProjectDir', {});
      return (result?.path || '').toString();
    }
    default:
      throw new Error(`runtime shim: unsupported method ID ${methodID}`);
  }
}

export const Call = {
  ByID: callByID,
};

export const Events = {
  On(eventName, callback) {
    const name = (eventName || '').toString().trim();
    if (!name || typeof callback !== 'function') return () => {};
    let callbacks = eventListeners.get(name);
    if (!callbacks) {
      callbacks = new Set();
      eventListeners.set(name, callbacks);
    }
    callbacks.add(callback);
    ensureSocket().catch((error) => {
      console.warn(`[wails-dev-shim] event bridge unavailable for ${name}`, error);
      scheduleEventReconnect(error);
    });
    return () => {
      const current = eventListeners.get(name);
      if (!current) return;
      current.delete(callback);
      if (current.size === 0) eventListeners.delete(name);
      clearEventReconnectIfIdle();
    };
  },
  Off(eventName) {
    const name = (eventName || '').toString().trim();
    if (name) eventListeners.delete(name);
  },
};
