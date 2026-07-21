// @ts-check

export { loadWailsRuntime } from './wails/wailsRuntimeLoader.js';

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
