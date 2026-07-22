
import { BRIDGE_REDACTED_VALUE, isForbiddenBridgeLogKey, normalizeBridgeLogFieldKey, safeBridgeLogFields } from '../bridgeSafeLogFields.js';
import { compactSafeDiagnosticPreview } from '../safeDiagnosticPreview.js';
import { WAILS_RUNTIME_MODULE, RPC_RESULT_PREVIEW_LIMIT, BRIDGE_ERROR_DATA_SAFE_KEYS } from './wailsBridgeConstants.js';

/** @typedef {Record<string, unknown>} BridgeRecord */
/** @type {Promise<unknown> | null} */
let runtimePromise = null;

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

/** @param {string} modulePath @returns {Promise<unknown>} */
function nativeImportModule(modulePath) {
  // public 目录里的 Wails runtime 只能由浏览器原生加载，避免 Vite 注入 ?import 后拦截。
  return import(/* @vite-ignore */ modulePath);
}

// Track active log store to pipe warnings and errors
/** @type {BridgeRecord | null} */
let logStoreInstance = null;
/** @param {BridgeRecord} store */
function registerBridgeLogStore(store) {
  logStoreInstance = store;
}

/** @param {unknown} value @param {WeakSet<object>} seen @returns {unknown} */
function serializableBridgeValue(value, seen = new WeakSet()) {
  if (!value || typeof value !== 'object') return value;
  if (seen.has(value)) return '[Circular]';
  seen.add(value);
  if (value instanceof Error) {
    /** @type {BridgeRecord} */
    const out = {
      name: value.name || 'Error',
      message: optionalDiagnosticString(value.message),
    };
    const error = /** @type {Error & BridgeRecord} */ (value);
    if ('code' in error && error.code !== undefined) out.code = error.code;
    if ('data' in error && error.data !== undefined) out.data = serializableBridgeErrorData(error.data, seen);
    return out;
  }
  if (Array.isArray(value)) return value.map((item) => serializableBridgeValue(item, seen));
  const parentIsErrorLike = isBridgeErrorLikeObject(value);
  return Object.fromEntries(
    Object.entries(value)
      .filter(([key]) => !isForbiddenBridgeLogKey(key))
      .map(([key, item]) => [
        key,
        parentIsErrorLike && normalizeBridgeLogFieldKey(key) === 'data'
          ? serializableBridgeErrorData(item, seen)
          : serializableBridgeValue(item, seen),
      ]),
  );
}

/** @param {unknown} value @returns {value is BridgeRecord} */
function isPlainBridgeObject(value) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false;
  const proto = Object.getPrototypeOf(value);
  return proto === Object.prototype || proto === null;
}

/** @param {object} value @param {PropertyKey} key */
function hasOwnBridgeProperty(value, key) {
  return Object.prototype.hasOwnProperty.call(value, key);
}

/** @param {unknown} value @returns {string} */
function optionalDiagnosticString(value) {
  if (value === undefined || value === null) return '';
  return String(value);
}

/** @param {unknown} fields @returns {unknown} */
function optionalDiagnosticFields(fields) {
  if (fields === undefined || fields === null) return {};
  return fields;
}

/** @param {unknown} value @returns {boolean} */
function isBridgeErrorLikeObject(value) {
  if (!isPlainBridgeObject(value) || !hasOwnBridgeProperty(value, 'data')) return false;
  return ['message', 'code', 'name', 'type'].some((key) => hasOwnBridgeProperty(value, key));
}

/** @param {unknown} value @returns {unknown} */
function serializableBridgeDiagnosticValue(value) {
  if (value === undefined) return undefined;
  if (value === null) return null;
  if (typeof value === 'string') return BRIDGE_REDACTED_VALUE;
  if (typeof value === 'number' || typeof value === 'boolean') return value;
  return BRIDGE_REDACTED_VALUE;
}

/** @param {unknown} value @param {WeakSet<object>} seen @returns {unknown} */
function serializableBridgeErrorData(value, seen) {
  if (!isPlainBridgeObject(value)) return BRIDGE_REDACTED_VALUE;
  if (seen.has(value)) return BRIDGE_REDACTED_VALUE;
  seen.add(value);

  /** @type {BridgeRecord} */
  const out = {};
  for (const [key, item] of Object.entries(value)) {
    const normalizedKey = normalizeBridgeLogFieldKey(key);
    if (!BRIDGE_ERROR_DATA_SAFE_KEYS.has(normalizedKey)) continue;
    if (isForbiddenBridgeLogKey(key)) continue;
    const safeValue = serializableBridgeDiagnosticValue(item);
    if (safeValue !== undefined) out[key] = safeValue;
  }
  return Object.keys(out).length > 0 ? out : BRIDGE_REDACTED_VALUE;
}

/** @param {'debug' | 'info' | 'warn' | 'error'} level @param {string} event @param {unknown} fields */
function writeBridgeLog(level, event, fields) {
  const serializableFields = serializableBridgeValue(optionalDiagnosticFields(fields));
  const safeFields = safeBridgeLogFields(serializableFields);
  const writer = logStoreInstance?.[level];
  if (typeof writer === 'function') {
    writer(event, safeFields);
  } else {
    console[level === 'error' ? 'error' : 'log'](`[Bridge ${level}] ${event}`, safeFields);
  }
}

/** @param {string} event @param {unknown} fields */
function writeBridgeDebugLog(event, fields) {
  if (!isFrontendTraceDebugEnabled()) return;
  writeBridgeLog('debug', event, fields);
}

/** @param {string} event @param {unknown} fields @param {boolean} isSlow */
function writeBridgeSuccessDiagnosticLog(event, fields, isSlow) {
  if (isSlow) {
    writeBridgeLog('warn', event, fields);
    return;
  }
  writeBridgeDebugLog(event, fields);
}

/** @param {unknown} value */
function compactBridgeValuePreview(value) {
  return compactSafeDiagnosticPreview(value, RPC_RESULT_PREVIEW_LIMIT);
}

/** @returns {Promise<unknown>} */
function waitRuntime() {
  if (!runtimePromise) {
    writeBridgeLog('info', 'bridge.runtime.load.start', {});
    runtimePromise = nativeImportModule(WAILS_RUNTIME_MODULE)
      .then((module) => {
        const runtime = module && typeof module === 'object' ? /** @type {BridgeRecord} */ (module) : {};
        const call = runtime.Call && typeof runtime.Call === 'object' ? /** @type {BridgeRecord} */ (runtime.Call) : {};
        const events = runtime.Events && typeof runtime.Events === 'object' ? /** @type {BridgeRecord} */ (runtime.Events) : {};
        writeBridgeLog('info', 'bridge.runtime.load.done', {
          ready: Boolean(call.ByID),
          has_events: Boolean(events.On),
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

/** @param {unknown} rawText @param {unknown} error @param {string} eventName */
function bridgeEventParseFailureEnvelope(rawText, error, eventName) {
  const rawValue = rawText == null ? '' : String(rawText);
  const payload = {
    eventName: eventName || 'runtime-event',
    error: error instanceof Error && error.message ? error.message : String(error),
    rawLen: rawValue.length,
  };
  writeBridgeLog('error', 'bridge.event.parse_failed', payload);
  return {
    method: 'bridge.event.parse_failed',
    type: 'bridge.event.parse_failed',
    payload,
  };
}
export {
  registerBridgeLogStore, serializableBridgeValue, optionalDiagnosticString, optionalDiagnosticFields, writeBridgeLog, writeBridgeDebugLog,
  writeBridgeSuccessDiagnosticLog, compactBridgeValuePreview, waitRuntime, bridgeEventParseFailureEnvelope,
};
