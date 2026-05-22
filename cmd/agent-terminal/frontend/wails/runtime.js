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
  CALL_API: 2963398832,
  GET_BUILD_INFO: 2341363104,
  SAVE_CLIPBOARD_IMAGE: 3733550318,
  SELECT_FILES: 4126105303,
  SELECT_PROJECT_DIR: 3694631468,
});

const JSONRPC_VERSION = '2.0';
const WS_PATH = '/wails/ws';

let nextRequestId = 1;
let socket = null;
let connectPromise = null;
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

function rejectPending(error) {
  if (pendingCalls.size === 0) return;
  const normalized = toError(error, 'runtime shim: websocket closed');
  for (const pending of pendingCalls.values()) {
    pending.reject(normalized);
  }
  pendingCalls.clear();
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
      resolve(nextSocket);
    };

    nextSocket.onerror = () => {
      if (!settled) {
        failConnect(new Error(`runtime shim: failed to connect ${resolveWebSocketURL()}`));
      }
    };

    nextSocket.onclose = (event) => {
      const err = closeError(event);
      if (socket === nextSocket) socket = null;
      if (!settled) {
        failConnect(err);
        return;
      }
      rejectPending(err);
    };

    nextSocket.onmessage = (event) => {
      readSocketText(event?.data)
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

async function readSocketText(data) {
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
      pending.reject(toError(message.error, 'runtime shim: rpc call failed'));
      return;
    }
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
    } catch (error) {
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
const LONG_RPC_TIMEOUT_MS = 120_000;
const SHORT_RPC_TIMEOUT_MS = 30_000;
const DREAM_RPC_PATTERNS = ['prompt-intents/draft'];
const LONG_RPC_PATTERNS = ['compact', 'summary', 'memory', 'dream', 'extract', 'state/get', 'fork', 'cron'];

function rpcTimeoutMs(methodName) {
  const lower = methodName.toLowerCase();
  for (const pattern of DREAM_RPC_PATTERNS) {
    if (lower.includes(pattern)) return DREAM_RPC_TIMEOUT_MS;
  }
  for (const pattern of LONG_RPC_PATTERNS) {
    if (lower.includes(pattern)) return LONG_RPC_TIMEOUT_MS;
  }
  return SHORT_RPC_TIMEOUT_MS;
}

async function rpcCall(method, params = {}) {
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
    const timer = setTimeout(() => {
      if (pendingCalls.has(callId)) {
        pendingCalls.delete(callId);
        reject(new Error(`runtime shim: rpc call timeout (${timeoutMs / 1000}s) for ${methodName}`));
      }
    }, timeoutMs);
    pendingCalls.set(callId, {
      resolve: (value) => { clearTimeout(timer); resolve(value); },
      reject: (error) => { clearTimeout(timer); reject(error); },
    });
    try {
      activeSocket.send(JSON.stringify(request));
    } catch (error) {
      clearTimeout(timer);
      pendingCalls.delete(callId);
      reject(toError(error, 'runtime shim: websocket send failed'));
    }
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
    if (key.startsWith('_ao')) {
      changed = true;
      continue;
    }
    cleaned[key] = item;
  }
  return changed ? cleaned : value;
}

async function callByID(methodID, ...args) {
  const id = Number(methodID);
  switch (id) {
    case METHOD_IDS.CALL_API: {
      const method = (args[0] || '').toString().trim();
      const params = args.length > 1 ? args[1] : {};
      const payload = params == null ? {} : params;
      return rpcCall(method, method === 'ui/log' ? payload : stripFrontendMetaPayload(payload));
    }
    case METHOD_IDS.GET_BUILD_INFO: {
      try {
        const info = await rpcCall('ui/buildInfo', {});
        return info && typeof info === 'object' ? info : {};
      } catch (error) {
        console.warn('[wails-dev-shim] ui/buildInfo failed', error);
        return { version: 'dev' };
      }
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
    });
    return () => {
      const current = eventListeners.get(name);
      if (!current) return;
      current.delete(callback);
      if (current.size === 0) eventListeners.delete(name);
    };
  },
  Off(eventName) {
    const name = (eventName || '').toString().trim();
    if (name) eventListeners.delete(name);
  },
};
