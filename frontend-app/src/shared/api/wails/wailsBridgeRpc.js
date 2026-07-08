// @ts-nocheck

import { METHOD_IDS, FRONTEND_TRACE_RPC_SLOW_MS } from './wailsBridgeConstants.js';
import { compactBridgeValuePreview, waitRuntime, writeBridgeDebugLog, writeBridgeLog, writeBridgeSuccessDiagnosticLog } from './wailsBridgeLogRuntime.js';
import { createTraceContext, currentMonotonicMS, elapsedMS, emitFrontendTraceEvent, resolveClientMeta, safeTraceErrorMessage } from './wailsBridgeTraceEvents.js';

let bridgeRequestSeq = 0;
let rpcRequestSeq = 0;

async function invokeRuntimeByID(methodID, args = [], options = {}) {
  const reqId = ++bridgeRequestSeq;
  const start = currentMonotonicMS();
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
        duration_ms: elapsedMS(start),
        error,
      });
    }
    throw error;
  }
  const durationMs = elapsedMS(start);
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

function logAPIStart(...entries) {
  const [{ reqId, method, payload, clientKind, clientRoute, trace }] = entries;
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

function logAPIDone(...entries) {
  const [{ reqId, method, start, result, clientKind, clientRoute, trace }] = entries;
  const durationMs = elapsedMS(start);
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

function logAPIFailed(...entries) {
  const [{ reqId, method, start, error, clientKind, clientRoute, trace }] = entries;
  const durationMs = elapsedMS(start);
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

function attachAPITraceToError(...entries) {
  const [error, { reqId, method, clientKind, clientRoute, trace }] = entries;
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

async function callAPI(method, params = {}) {
  const reqId = ++rpcRequestSeq;
  const start = currentMonotonicMS();
  const rpcMethod = normalizeAPIMethod(method, reqId);
  const rawPayload = normalizeAPIPayload(rpcMethod, params, reqId);
  const { clientKind, clientRoute, trace } = createAPITrace(rpcMethod, reqId);
  const payload = buildAPIPayload(rawPayload, reqId, clientKind, clientRoute, trace);
  const traceContext = { reqId, method: rpcMethod, clientKind, clientRoute, trace };
  logAPIStart({ ...traceContext, payload });

  let result;
  try {
    result = await invokeRuntimeByID(METHOD_IDS.CALL_API, [rpcMethod, payload], {
      logFailure: false,
      logRuntimeUnavailable: false,
    });
  }
  catch (error) {
    // 误判防护：callAPI 附加 trace 后继续抛出错误，不吞掉 backend/runtime 失败。
    const tracedError = attachAPITraceToError(error, traceContext);
    logAPIFailed({ ...traceContext, start, error: tracedError });
    throw tracedError;
  }
  logAPIDone({ ...traceContext, start, result });
  return result;
}

async function sendFrontendLogBatch(entries) {
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


export {
  invokeRuntimeByID, callByID, normalizeAPIMethod, normalizeAPIPayload, createAPITrace, buildAPIPayload,
  logAPIStart, logAPIDone, logAPIFailed, attachAPITraceToError, callAPI, sendFrontendLogBatch,
};
