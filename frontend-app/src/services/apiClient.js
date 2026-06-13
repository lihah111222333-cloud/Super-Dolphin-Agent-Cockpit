const DEFAULT_REQUEST_TIMEOUT_MS = 8000;

/*
 * services 层把 backendApi/Wails 错误统一变成 ApiError。
 * 页面可以读 code/requestId/traceId；service 不吞错、不返回空兜底。
 */

class ApiError extends Error {
  constructor(message, options = {}) {
    super(message);
    this.name = 'ApiError';
    this.code = options.code || '';
    this.requestId = options.requestId || options.reqId || '';
    this.traceId = options.traceId || options.trace_id || '';
    this.cause = options.cause;
  }
}

function toApiError(error, fallbackMessage = '请求失败') {
  if (error instanceof ApiError) return error;
  const message = error?.message || String(error || fallbackMessage);
  return new ApiError(message || fallbackMessage, {
    code: error?.code,
    requestId: error?.requestId || error?.reqId,
    traceId: error?.traceId || error?.trace_id,
    cause: error,
  });
}

function withRequestTimeout(promise, timeoutMs = DEFAULT_REQUEST_TIMEOUT_MS, message = '请求超时') {
  let timeoutID;
  const timeout = new Promise((_, reject) => {
    timeoutID = globalThis.setTimeout(() => reject(new ApiError(message, { code: 'TIMEOUT' })), timeoutMs);
  });
  return Promise.race([promise, timeout]).finally(() => {
    if (timeoutID) globalThis.clearTimeout(timeoutID);
  });
}

async function runServiceRequest(fn, fallbackMessage = '请求失败') {
  try {
    return await fn();
  } catch (error) {
    throw toApiError(error, fallbackMessage);
  }
}

export { ApiError, DEFAULT_REQUEST_TIMEOUT_MS, runServiceRequest, toApiError, withRequestTimeout };
