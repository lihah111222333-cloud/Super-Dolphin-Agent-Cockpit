// Bridge Policy (must keep):
// - This file is the only frontend bridge for desktop capabilities.
// - Vue/JS is UI-only; system capabilities must go through Wails v3 runtime bridge.
// - Do not introduce browser-native fallbacks for file/system access.
import * as logService from './log.js';

const {
  logDebug,
  logInfo,
  logWarn,
  logError,
} = logService;

const registerLogBridgeSink = typeof logService.registerLogBridgeSink === 'function'
  ? logService.registerLogBridgeSink
  : () => {};

const METHOD_IDS = Object.freeze({
  CALL_API: 2963398832,
  GET_BUILD_INFO: 2341363104,
  SAVE_CLIPBOARD_IMAGE: 3733550318,
  SELECT_FILES: 4126105303,
  SELECT_PROJECT_DIR: 3694631468,
});

const EVENT_SAMPLE_EVERY = 120;

let bridgeRequestSeq = 0;
let rpcRequestSeq = 0;
let agentEventCount = 0;
let bridgeEventCount = 0;

function perfNow() {
  if (typeof performance !== 'undefined' && typeof performance.now === 'function') {
    return performance.now();
  }
  return Date.now();
}

function parseRuntimeEventJSON(rawText) {
  try {
    return JSON.parse(rawText);
  } catch (error) {
    logWarn('api', 'json.parse.failed', {
      error,
      raw_len: rawText.length,
      raw_preview: rawText.slice(0, 200),
    });
    return {};
  }
}

function normalizeRPCError(error) {
  const rawMessage = (
    (error && typeof error === 'object' && typeof error.message === 'string' ? error.message : '')
    || String(error || '')
  ).toLowerCase();
  const overloaded = rawMessage.includes('code -32001') || rawMessage.includes('server overloaded');
  if (!overloaded) {
    return error;
  }
  const normalized = new Error('Server overloaded; retry later.');
  normalized.code = -32001;
  normalized.retryAfterMs = 500;
  normalized.cause = error;
  return normalized;
}

export function normalizeRuntimeEventEnvelope(evt) {
  if (!evt || typeof evt !== 'object') return {};

  const hasWailsEnvelope = Object.prototype.hasOwnProperty.call(evt, 'name')
    && Object.prototype.hasOwnProperty.call(evt, 'data');
  if (!hasWailsEnvelope) {
    return evt;
  }

  const inner = evt.data;
  if (inner == null || inner === '') return {};
  if (typeof inner === 'object') return inner;
  if (typeof inner === 'string') return parseRuntimeEventJSON(inner);
  return { data: inner };
}

let runtimePromise = null;

async function waitRuntime() {
  if (!runtimePromise) {
    logInfo('bridge', 'runtime.load.start', {});
    runtimePromise = import('/wails/runtime.js')
      .then((module) => {
        logInfo('bridge', 'runtime.load.done', {
          ready: Boolean(module?.Call?.ByID),
          has_events: Boolean(module?.Events?.On),
        });
        return module || null;
      })
      .catch((error) => {
        logError('bridge', 'runtime.load.failed', { error });
        return null;
      });
  }
  return runtimePromise;
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
    } catch {
      // ignore unsubscribe failures
    }
    return false;
  };

  const wrapped = (evt) => {
    const normalized = normalizeRuntimeEventEnvelope(evt);
    if (typeof options.beforeCallback === 'function') {
      options.beforeCallback(normalized);
    }
    try {
      callback(normalized);
    } catch (error) {
      logError('event', options.callbackFailedLog || 'runtime.callback.failed', { error });
    }
  };

  waitRuntime().then((runtime) => {
    if (!runtime?.Events?.On) {
      logWarn('event', options.subscribeUnavailableLog || 'runtime.subscribe.unavailable', { eventName });
      return;
    }
    const unbind = runtime.Events.On(eventName, wrapped);
    if (cancelled) {
      teardown(runtime, unbind);
      logInfo('event', options.unsubscribeDoneLog || 'runtime.unsubscribe.done', {
        late_registration: true,
      });
      return;
    }
    logInfo('event', options.subscribeReadyLog || 'runtime.subscribe.ready', {});
    off = () => {
      cancelled = true;
      if (teardown(runtime, unbind)) {
        logInfo('event', options.unsubscribeDoneLog || 'runtime.unsubscribe.done', {});
      }
    };
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

async function sendFrontendLogBatch(entries) {
  const batch = Array.isArray(entries) ? entries.filter(Boolean) : [];
  if (batch.length === 0) return;
  const runtime = await waitRuntime();
  if (!runtime?.Call?.ByID) return;
  const { clientKind, clientRoute } = resolveClientMeta();
  try {
    await runtime.Call.ByID(METHOD_IDS.CALL_API, 'ui/log', {
      entries: batch,
      _aoClientKind: clientKind,
      _aoClientRoute: clientRoute,
    });
  } catch {
    // ignore bridge logging failures to avoid recursive frontend log storms
  }
}

registerLogBridgeSink(sendFrontendLogBatch);

async function callByID(methodID, ...args) {
  // Hard bridge boundary:
  // If Wails runtime is unavailable, we fail fast instead of silently
  // falling back to browser-style system APIs.
  const reqId = ++bridgeRequestSeq;
  const start = perfNow();
  logDebug('bridge', 'call.start', {
    req_id: reqId,
    method_id: methodID,
    arg_count: args.length,
  });

  const runtime = await waitRuntime();
  if (!runtime?.Call?.ByID) {
    logWarn('bridge', 'call.runtime.unavailable', {
      req_id: reqId,
      method_id: methodID,
    });
    throw new Error('Wails runtime bridge not ready');
  }
  try {
    const result = await runtime.Call.ByID(methodID, ...args);
    logDebug('bridge', 'call.done', {
      req_id: reqId,
      method_id: methodID,
      duration_ms: Math.round(perfNow() - start),
    });
    return result;
  } catch (error) {
    logDebug('bridge', 'call.failed', {
      req_id: reqId,
      method_id: methodID,
      duration_ms: Math.round(perfNow() - start),
      error,
    });
    throw error;
  }
}

export async function callAPI(method, params = {}) {
  const reqId = ++rpcRequestSeq;
  const start = perfNow();
  const rawPayload = params == null ? {} : params;
  if (typeof rawPayload !== 'object' || Array.isArray(rawPayload)) {
    const error = new TypeError('callAPI params must be an object');
    logWarn('api', 'rpc.invalid_params', {
      req_id: reqId,
      method,
      param_type: typeof rawPayload,
      is_array: Array.isArray(rawPayload),
      error,
    });
    throw error;
  }
  const clientKind = typeof window !== 'undefined' && window.__WAILS_SHIM_DEBUG__ === true
    ? 'web-debug-shim'
    : 'desktop-wails';
  const clientRoute = typeof window !== 'undefined' && window.location
    ? (window.location.pathname || '/').toString()
    : '';
  const payload = {
    ...rawPayload,
    _aoClientKind: clientKind,
    _aoClientRoute: clientRoute,
  };
  logDebug('api', 'rpc.start', {
    req_id: reqId,
    method,
    client_kind: clientKind,
    client_route: clientRoute,
    param_keys: Object.keys(payload),
  });
  try {
    const result = await callByID(METHOD_IDS.CALL_API, method, payload);
    logDebug('api', 'rpc.done', {
      req_id: reqId,
      method,
      client_kind: clientKind,
      client_route: clientRoute,
      duration_ms: Math.round(perfNow() - start),
    });
    return result;
  } catch (error) {
    const normalizedError = normalizeRPCError(error);
    logDebug('api', 'rpc.failed', {
      req_id: reqId,
      method,
      client_kind: clientKind,
      client_route: clientRoute,
      duration_ms: Math.round(perfNow() - start),
      error: normalizedError,
    });
    throw normalizedError;
  }
}


export async function selectProjectDir(defaultPath = '') {
  // Project directory chooser must be handled by Go/Wails native dialog.
  const seed = typeof defaultPath === 'string' ? defaultPath : '';
  logInfo('ui', 'selectProjectDir.start', { default_path: seed });

  if (!seed) {
    try {
      const value = await callByID(METHOD_IDS.SELECT_PROJECT_DIR);
      if (typeof value === 'string') {
        logInfo('ui', 'selectProjectDir.done', {
          selected: Boolean(value),
          path: value,
          via: 'binding',
        });
        return value;
      }
      logWarn('ui', 'selectProjectDir.unexpectedShape', {
        type: typeof value,
        is_array: Array.isArray(value),
      });
    } catch (error) {
      logWarn('ui', 'selectProjectDir.byId.failed', { error });
    }
  }

  const raw = await callAPI('ui/selectProjectDir', { defaultPath: seed });
  const path = raw && typeof raw === 'object' && typeof raw.path === 'string' ? raw.path : '';
  logInfo('ui', 'selectProjectDir.done', {
    selected: Boolean(path),
    path,
    via: 'rpc',
  });
  return path;
}

export async function selectProjectDirs() {
  // Multi-project directory chooser must be handled by Go/Wails native dialog.
  logInfo('ui', 'selectProjectDirs.start', {});
  const raw = await callAPI('ui/selectProjectDirs', {});
  const paths = Array.isArray(raw?.paths) ? raw.paths : [];
  logInfo('ui', 'selectProjectDirs.done', {
    count: paths.length,
    first: paths[0] || '',
  });
  return paths;
}

export async function selectFiles() {
  // Attachment file chooser must be handled by Go/Wails native dialog.
  logInfo('ui', 'selectFiles.start', {});
  const normalize = (raw) => {
    if (Array.isArray(raw)) return raw;
    if (raw && typeof raw === 'object' && Array.isArray(raw.paths)) return raw.paths;
    return null;
  };

  try {
    const values = await callByID(METHOD_IDS.SELECT_FILES);
    const files = normalize(values);
    if (files != null) {
      logInfo('ui', 'selectFiles.done', {
        count: files.length,
        first: files[0] || '',
      });
      return files;
    }
    logWarn('ui', 'selectFiles.unexpectedShape', {
      type: typeof values,
      is_array: Array.isArray(values),
    });
  } catch (error) {
    logWarn('ui', 'selectFiles.byId.failed', { error });
  }

  const raw = await callAPI('ui/selectFiles', {});
  const files = normalize(raw) || [];
  logInfo('ui', 'selectFiles.done', {
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
  logInfo('ui', 'readDroppedTextFiles.start', {
    count: paths.length,
    target_id: (targetId || '').toString().trim(),
  });
  const raw = await callAPI('ui/readDroppedTextFiles', {
    files: paths,
    targetId: (targetId || '').toString().trim(),
  });
  const items = Array.isArray(raw?.files) ? raw.files : [];
  logInfo('ui', 'readDroppedTextFiles.done', {
    count: items.length,
    first: (items[0]?.path || '').toString(),
  });
  return items.map((item) => ({
    path: (item?.path || '').toString(),
    name: (item?.name || '').toString(),
    text: (item?.text || '').toString(),
    sizeBytes: Number(item?.sizeBytes) || 0,
  }));
}

export async function saveClipboardImage(base64Payload) {
  const start = perfNow();
  const path = (await callByID(METHOD_IDS.SAVE_CLIPBOARD_IMAGE, base64Payload)) || '';
  logDebug('ui', 'clipboardImage.saved', {
    ok: Boolean(path),
    duration_ms: Math.round(perfNow() - start),
  });
  return path;
}

export async function saveTextFile({ defaultPath = '', defaultFilename = '', content = '' } = {}) {
  const filename = (defaultFilename || '').toString().trim();
  if (!filename) throw new Error('saveTextFile defaultFilename is required');
  logInfo('ui', 'saveTextFile.start', {
    default_path: (defaultPath || '').toString(),
    default_filename: filename,
    content_len: (content || '').toString().length,
  });
  const raw = await callAPI('ui/saveTextFile', {
    defaultPath: (defaultPath || '').toString(),
    defaultFilename: filename,
    content: (content || '').toString(),
  });
  const path = raw && typeof raw === 'object' && typeof raw.path === 'string' ? raw.path : '';
  logInfo('ui', 'saveTextFile.done', {
    selected: Boolean(path),
    path,
  });
  return path;
}

export async function copyTextToClipboard(text) {
  const value = (text || '').toString().trim();
  if (!value) return false;

  // Debug shim 模式: Go 端没有 wailsApp.Clipboard, RPC 必定失败,
  // 且 await HTTP 往返会耗尽 transient user activation → navigator.clipboard 也失败。
  // 直接走浏览器 API, 跳过注定失败的 RPC 调用。
  const isDebugShim = typeof window !== 'undefined' && window.__WAILS_SHIM_DEBUG__ === true;

  if (!isDebugShim) {
    // Wails 原生桩贴板桥 (桌面端)
    try {
      const res = await callAPI('ui/copyText', { text: value });
      if (res?.ok) return true;
    } catch {
      // 降级到浏览器 API
    }
  }

  // 浏览器 Clipboard API
  try {
    if (navigator?.clipboard?.writeText) {
      await navigator.clipboard.writeText(value);
      return true;
    }
  } catch {
    logDebug('ui', 'copyText.clipboard_api_failed', {});
  }

  // 最终降级: execCommand
  try {
    const textarea = document.createElement('textarea');
    textarea.value = value;
    textarea.style.position = 'fixed';
    textarea.style.opacity = '0';
    document.body.appendChild(textarea);
    textarea.select();
    const ok = document.execCommand('copy');
    document.body.removeChild(textarea);
    return ok;
  } catch {
    logDebug('ui', 'copyText.exec_command_failed', {});
    return false;
  }
}

export async function resolveThreadIdentity(threadId) {
  const id = (threadId || '').toString().trim();
  if (!id) return {};
  const res = await callAPI('thread/resolve', { threadId: id });
  return res && typeof res === 'object' ? res : {};
}

export async function getBuildInfo() {
  const raw = await callByID(METHOD_IDS.GET_BUILD_INFO);
  const info = raw && typeof raw === 'object' ? raw : {};
  logDebug('api', 'buildInfo.read', {
    version: info?.version || '',
    commit: info?.commit || '',
  });
  return info;
}

export function onAgentEvent(callback) {
  return subscribeRuntimeEvent('agent-event', callback, {
    beforeCallback(normalized) {
      agentEventCount += 1;
      if (agentEventCount % EVENT_SAMPLE_EVERY === 0) {
        logDebug('event', 'agent.sample', {
          count: agentEventCount,
          type: (normalized?.type || '').toString(),
        });
      }
    },
    callbackFailedLog: 'agent.callback.failed',
    subscribeUnavailableLog: 'agent.subscribe.unavailable',
    subscribeReadyLog: 'agent.subscribe.ready',
    unsubscribeDoneLog: 'agent.unsubscribe.done',
  });
}

export function onBridgeEvent(callback) {
  return subscribeRuntimeEvent('bridge-event', callback, {
    beforeCallback(normalized) {
      bridgeEventCount += 1;
      if (bridgeEventCount % EVENT_SAMPLE_EVERY === 0) {
        logDebug('event', 'bridge.sample', {
          count: bridgeEventCount,
          type: (normalized?.type || normalized?.method || '').toString(),
        });
      }
    },
    callbackFailedLog: 'bridge.callback.failed',
    subscribeUnavailableLog: 'bridge.subscribe.unavailable',
    subscribeReadyLog: 'bridge.subscribe.ready',
    unsubscribeDoneLog: 'bridge.unsubscribe.done',
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
