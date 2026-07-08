// @ts-nocheck

import { BRIDGE_REDACTED_VALUE, isForbiddenBridgeLogKey, normalizeBridgeLogFieldKey, safeBridgeLogFields } from '../bridgeSafeLogFields.js';
import { compactSafeDiagnosticPreview } from '../safeDiagnosticPreview.js';
import { WAILS_RUNTIME_MODULE, RPC_RESULT_PREVIEW_LIMIT, BRIDGE_ERROR_DATA_SAFE_KEYS } from './wailsBridgeConstants.js';

let runtimePromise = null;

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

function nativeImportModule(modulePath) {
  // public 目录里的 Wails runtime 只能由浏览器原生加载，避免 Vite 注入 ?import 后拦截。
  return import(/* @vite-ignore */ modulePath);
}

// Track active log store to pipe warnings and errors
let logStoreInstance = null;
function registerBridgeLogStore(store) {
  logStoreInstance = store;
}

function serializableBridgeValue(value, seen = new WeakSet()) {
  if (!value || typeof value !== 'object') return value;
  if (seen.has(value)) return '[Circular]';
  seen.add(value);
  if (value instanceof Error) {
    const out = {
      name: value.name || 'Error',
      message: optionalDiagnosticString(value.message),
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
        parentIsErrorLike && normalizeBridgeLogFieldKey(key) === 'data'
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

function optionalDiagnosticString(value) {
  if (value === undefined || value === null) return '';
  return String(value);
}

function optionalDiagnosticFields(fields) {
  if (fields === undefined || fields === null) return {};
  return fields;
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
    const normalizedKey = normalizeBridgeLogFieldKey(key);
    if (!BRIDGE_ERROR_DATA_SAFE_KEYS.has(normalizedKey)) continue;
    if (isForbiddenBridgeLogKey(key)) continue;
    const safeValue = serializableBridgeDiagnosticValue(item);
    if (safeValue !== undefined) out[key] = safeValue;
  }
  return Object.keys(out).length > 0 ? out : BRIDGE_REDACTED_VALUE;
}

function writeBridgeLog(level, event, fields) {
  const serializableFields = serializableBridgeValue(optionalDiagnosticFields(fields));
  const safeFields = safeBridgeLogFields(serializableFields);
  if (logStoreInstance && typeof logStoreInstance[level] === 'function') {
    logStoreInstance[level](event, safeFields);
  } else {
    console[level === 'error' ? 'error' : 'log'](`[Bridge ${level}] ${event}`, safeFields);
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
  return compactSafeDiagnosticPreview(value, RPC_RESULT_PREVIEW_LIMIT);
}

function waitRuntime() {
  if (!runtimePromise) {
    writeBridgeLog('info', 'bridge.runtime.load.start', {});
    runtimePromise = nativeImportModule(WAILS_RUNTIME_MODULE)
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

function bridgeEventParseFailureEnvelope(rawText, error, eventName) {
  const rawValue = rawText == null ? '' : String(rawText);
  const payload = {
    eventName: eventName || 'runtime-event',
    error: error?.message || String(error),
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
