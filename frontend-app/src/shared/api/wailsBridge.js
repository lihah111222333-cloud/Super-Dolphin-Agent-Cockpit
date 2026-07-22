// @ts-check

// public 目录里的 Wails runtime 只能由浏览器原生加载，避免 Vite 注入 ?import 后拦截。
const WAILS_RUNTIME_MODULE = '/wails/runtime.js';

/** @param {string} modulePath */
function nativeImportModule(modulePath) {
  return import(/* @vite-ignore */ modulePath);
}

function loadWailsRuntime() {
  return nativeImportModule(WAILS_RUNTIME_MODULE);
}

export { loadWailsRuntime };
export { registerBridgeLogStore } from './wails/wailsBridgeLogRuntime.js';
export { normalizeRuntimeEventEnvelope, emitFrontendTraceEvent } from './wails/wailsBridgeTraceEvents.js';
export { callAPI, sendFrontendLogBatch } from './wails/wailsBridgeRpc.js';
export {
  selectProjectDir, selectProjectDirs, selectFiles, selectDatasourceImportFile,
  readDroppedTextFiles, saveClipboardImage, saveTextFile, openSharedFile,
  previewSharedFile,
} from './wails/wailsBridgeNativeFiles.js';
export {
  copyTextToClipboard, beginTextClipboardWrite, resolveThreadIdentity, getBuildInfo, onAgentEvent,
  onBridgeEvent, onFilesDropped, onAppWillQuit, onRuntimeReconnect,
} from './wails/wailsBridgeClipboardEvents.js';
