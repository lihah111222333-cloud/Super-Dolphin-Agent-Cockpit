const LEVELS = Object.freeze({
  debug: 10,
  info: 20,
  warn: 30,
  error: 40,
});

const STORAGE_KEY = 'agent-orchestrator.log.level';
const BUFFER_LIMIT = 600;
const LOG_PREFIX = '[AO]';
const BRIDGE_BATCH_LIMIT = 24;
const BRIDGE_QUEUE_LIMIT = 240;

let sequence = 0;
let currentLevel = resolveInitialLevel();
const ringBuffer = [];
const bridgeQueue = [];
let bridgeFlushScheduled = false;
let bridgeSink = null;
const levelListeners = new Set();

function notifyLevelListeners(level) {
  if (levelListeners.size === 0) return;
  // snapshot to avoid mutation during iteration if a listener unsubscribes
  for (const cb of Array.from(levelListeners)) {
    try {
      cb(level);
    } catch {
      // ignore listener errors so a faulty subscriber cannot poison others
    }
  }
}

function resolveInitialLevel() {
  try {
    const query = new URLSearchParams(window.location.search);
    const fromQuery = normalizeLevel(query.get('log'));
    if (fromQuery) {
      localStorage.setItem(STORAGE_KEY, fromQuery);
      return fromQuery;
    }
    const fromStorage = normalizeLevel(localStorage.getItem(STORAGE_KEY));
    if (fromStorage) return fromStorage;
  } catch {
    // ignore
  }
  return 'info';
}

function normalizeLevel(value) {
  const level = (value || '').toString().trim().toLowerCase();
  return Object.prototype.hasOwnProperty.call(LEVELS, level) ? level : '';
}

function shouldLog(level) {
  return (LEVELS[level] || LEVELS.info) >= (LEVELS[currentLevel] || LEVELS.info);
}

function serializeValue(value, depth = 0) {
  if (value == null) return value;
  if (depth > 2) return '[max-depth]';
  if (value instanceof Error) {
    return {
      name: value.name,
      message: value.message,
      stack: value.stack || '',
    };
  }
  if (Array.isArray(value)) {
    const list = value.slice(0, 20).map((item) => serializeValue(item, depth + 1));
    if (value.length > 20) list.push(`[+${value.length - 20} items]`);
    return list;
  }
  const type = typeof value;
  if (type === 'string') {
    if (value.length <= 800) return value;
    return `${value.slice(0, 800)}...[+${value.length - 800}]`;
  }
  if (type === 'number' || type === 'boolean') return value;
  if (type === 'function') return `[function ${value.name || 'anonymous'}]`;
  if (type !== 'object') return String(value);

  const out = {};
  const keys = Object.keys(value).slice(0, 30);
  for (const key of keys) {
    out[key] = serializeValue(value[key], depth + 1);
  }
  if (Object.keys(value).length > keys.length) {
    out.__truncated__ = `+${Object.keys(value).length - keys.length} keys`;
  }
  return out;
}

function pushBuffer(entry) {
  ringBuffer.push(entry);
  if (ringBuffer.length > BUFFER_LIMIT) {
    ringBuffer.splice(0, ringBuffer.length - BUFFER_LIMIT);
  }
}

function scheduleBridgeFlush() {
  if (bridgeFlushScheduled || typeof bridgeSink !== 'function' || bridgeQueue.length === 0) return;
  bridgeFlushScheduled = true;
  const schedule = typeof queueMicrotask === 'function'
    ? queueMicrotask
    : (fn) => Promise.resolve().then(fn).catch(() => {});
  schedule(async () => {
    bridgeFlushScheduled = false;
    if (typeof bridgeSink !== 'function' || bridgeQueue.length === 0) return;
    const batch = bridgeQueue.splice(0, BRIDGE_BATCH_LIMIT);
    try {
      await bridgeSink(batch);
    } catch {
      // ignore bridge sink failures; local ring buffer + console remain available
    }
    if (bridgeQueue.length > 0) scheduleBridgeFlush();
  });
}

// Levels forwarded to backend via ui/log; info excluded (too high-volume).
const BRIDGE_ALLOWED_LEVELS = new Set(['debug', 'warn', 'error']);

function enqueueBridge(entry) {
  if (typeof bridgeSink !== 'function') return;
  if (!BRIDGE_ALLOWED_LEVELS.has(entry.level)) return;
  bridgeQueue.push(entry);
  if (bridgeQueue.length > BRIDGE_QUEUE_LIMIT) {
    bridgeQueue.splice(0, bridgeQueue.length - BRIDGE_QUEUE_LIMIT);
  }
  scheduleBridgeFlush();
}

function consoleWrite(level, message, entry) {
  const method = level === 'debug'
    ? 'debug'
    : level === 'info'
      ? 'info'
      : level === 'warn'
        ? 'warn'
        : 'error';
  const fn = console[method] || console.log;
  fn(`${LOG_PREFIX} ${message}`, entry);
}

function emit(level, scope, event, fields = {}) {
  const normalizedLevel = normalizeLevel(level) || 'info';
  if (!shouldLog(normalizedLevel)) return;
  const entry = {
    seq: ++sequence,
    ts: new Date().toISOString(),
    level: normalizedLevel,
    scope: (scope || '').toString(),
    event: (event || '').toString(),
    fields: serializeValue(fields),
  };
  pushBuffer(entry);
  consoleWrite(normalizedLevel, `${entry.scope}.${entry.event}`, entry);
  enqueueBridge(entry);
}

export function logDebug(scope, event, fields = {}) {
  emit('debug', scope, event, fields);
}

export function logInfo(scope, event, fields = {}) {
  emit('info', scope, event, fields);
}

export function logWarn(scope, event, fields = {}) {
  emit('warn', scope, event, fields);
}

export function logError(scope, event, fields = {}) {
  emit('error', scope, event, fields);
}

function getLogLevel() {
  return currentLevel;
}

export function readLogLevel() {
  return getLogLevel();
}

export function setLogLevel(level) {
  const normalized = normalizeLevel(level);
  if (!normalized) return false;
  const previous = currentLevel;
  currentLevel = normalized;
  try {
    localStorage.setItem(STORAGE_KEY, normalized);
  } catch {
    // ignore
  }
  if (previous !== normalized) {
    notifyLevelListeners(normalized);
  }
  logInfo('log', 'level.changed', { level: normalized });
  return true;
}

export function onLogLevelChange(callback) {
  if (typeof callback !== 'function') return () => {};
  levelListeners.add(callback);
  return () => {
    levelListeners.delete(callback);
  };
}

function applyExternalLevel(value) {
  const normalized = normalizeLevel(value);
  if (!normalized || normalized === currentLevel) return;
  currentLevel = normalized;
  notifyLevelListeners(normalized);
}

function getLogBuffer() {
  return ringBuffer.slice();
}

export function readLogBuffer() {
  return getLogBuffer();
}

function clearLogBuffer() {
  ringBuffer.splice(0, ringBuffer.length);
}

export function clearLogHistory() {
  clearLogBuffer();
}

export function registerLogBridgeSink(sink) {
  bridgeSink = typeof sink === 'function' ? sink : null;
  if (bridgeSink && bridgeQueue.length > 0) {
    scheduleBridgeFlush();
  }
}

if (typeof window !== 'undefined') {
  window.AOLog = {
    getLevel: getLogLevel,
    setLevel: setLogLevel,
    getBuffer: getLogBuffer,
    clearBuffer: clearLogBuffer,
    setBridgeSink: registerLogBridgeSink,
    onLevelChange: onLogLevelChange,
  };
  if (typeof window.addEventListener === 'function') {
    window.addEventListener('storage', (event) => {
      if (!event || event.key !== STORAGE_KEY) return;
      applyExternalLevel(event.newValue);
    });
  }
}
