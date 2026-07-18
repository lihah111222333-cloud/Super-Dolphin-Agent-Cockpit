
import { METHOD_IDS } from './wailsBridgeConstants.js';
import { writeBridgeLog } from './wailsBridgeLogRuntime.js';
import { currentMonotonicMS, elapsedMS } from './wailsBridgeTraceEvents.js';
import { callAPI, callByID } from './wailsBridgeRpc.js';
import { assertSafeSharedFilePreviewURL } from './sharedFilePreviewContract.js';

const SHARED_FILE_PREVIEW_MAX_BYTES = 50 * 1024 * 1024;
/** @typedef {Record<string, unknown>} NativePayload */

const SHARED_FILE_PREVIEW_FIELD_CONSUMERS = Object.freeze({
  url: Object.freeze({
    direction: 'bridge-to-workflow-ui',
    reason: 'WorkflowFinalOutputPanel renders the tokenized media URL.',
    owner: 'frontend-app/src/pages/workflows/components/WorkflowFinalOutputPanel.jsx',
  }),
  path: Object.freeze({
    direction: 'bridge-terminal-validation',
    reason: 'The bridge validates the producer path; the workflow already owns the requested path.',
    owner: 'frontend-app/src/shared/api/wails/wailsBridgeNativeFiles.js',
  }),
  contentType: Object.freeze({
    direction: 'bridge-to-workflow-ui',
    reason: 'WorkflowFinalOutputPanel consumes the producer media type.',
    owner: 'frontend-app/src/pages/workflows/components/WorkflowFinalOutputPanel.jsx',
  }),
  sizeBytes: Object.freeze({
    direction: 'bridge-terminal-validation',
    reason: 'The bridge validates the producer size before terminating this field.',
    owner: 'frontend-app/src/shared/api/wails/wailsBridgeNativeFiles.js',
  }),
});

/** @param {string} path @param {string} via */
function logProjectDirSelection(path, via) {
  writeBridgeLog('info', 'ui.selectProjectDir.done', {
    selected: Boolean(path),
    path,
    via,
  });
}

/** @param {unknown} defaultPath */
async function selectProjectDir(defaultPath = '') {
  const seed = typeof defaultPath === 'string' ? defaultPath : '';
  writeBridgeLog('info', 'ui.selectProjectDir.start', { default_path: seed });

  if (!seed) {
    try {
      const value = await callByID(METHOD_IDS.SELECT_PROJECT_DIR);
      if (typeof value === 'string') {
        logProjectDirSelection(value, 'binding');
        return value;
      }
    }
    catch (error) {
      writeBridgeLog('warn', 'ui.selectProjectDir.byId.failed', { error });
    }
  }

  const raw = await callAPI('ui/selectProjectDir', { defaultPath: seed });
  const response = raw && typeof raw === 'object' ? /** @type {NativePayload} */ (raw) : {};
  const path = typeof response.path === 'string' ? response.path : '';
  logProjectDirSelection(path, 'rpc');
  return path;
}

async function selectProjectDirs() {
  writeBridgeLog('info', 'ui.selectProjectDirs.start', {});
  const raw = await callAPI('ui/selectProjectDirs', {});
  const paths = nativePathListResponse('ui/selectProjectDirs', raw);
  writeBridgeLog('info', 'ui.selectProjectDirs.done', {
    count: paths.length,
    first: firstDiagnosticPath(paths),
  });
  return paths;
}

/** @param {unknown} options @returns {NativePayload} */
function normalizeSelectFilesOptions(options = {}) {
  if (!options || typeof options !== 'object' || Array.isArray(options)) return {};
  const source = /** @type {NativePayload} */ (options);
  /** @type {NativePayload} */
  const payload = {};
  if (typeof source.defaultPath === 'string' && source.defaultPath.trim()) {
    payload.defaultPath = source.defaultPath.trim();
  }
  if (Array.isArray(source.filters)) {
    const filters = source.filters
      .map((filter) => ({
        displayName: normalizeBridgeInputString(filter && typeof filter === 'object' ? /** @type {NativePayload} */ (filter).displayName : undefined),
        pattern: normalizeBridgeInputString(filter && typeof filter === 'object' ? /** @type {NativePayload} */ (filter).pattern : undefined),
      }))
      .filter((filter) => filter.displayName && filter.pattern);
    if (filters.length > 0) payload.filters = filters;
  }
  return payload;
}

/** @param {string} method @param {unknown} raw @returns {NativePayload} */
function assertNativeResponseObject(method, raw) {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) {
    throw new TypeError(`${method} response must be an object`);
  }
  return /** @type {NativePayload} */ (raw);
}

/** @param {string} method @param {string} field @param {unknown} value @returns {string[]} */
function assertNativeStringArray(method, field, value) {
  if (!Array.isArray(value)) {
    throw new TypeError(`${method} response ${field} must be an array`);
  }
  for (const item of value) {
    if (typeof item !== 'string') {
      throw new TypeError(`${method} response ${field} entries must be strings`);
    }
  }
  return /** @type {string[]} */ (value);
}

/** @param {string} method @param {unknown} raw */
function nativePathListResponse(method, raw) {
  const value = assertNativeResponseObject(method, raw);
  return assertNativeStringArray(method, 'paths', value.paths);
}

/** @param {readonly string[]} paths @returns {string} */
function firstDiagnosticPath(paths) {
  if (!Array.isArray(paths) || paths.length === 0) return '';
  if (paths[0] === undefined || paths[0] === null) return '';
  return paths[0];
}

/** @param {unknown} value @returns {string} */
function normalizeBridgeInputString(value) {
  if (value === undefined || value === null) return '';
  return String(value).trim();
}

/** @param {object} value @param {PropertyKey} key */
function hasOwnBridgeProperty(value, key) {
  return Object.prototype.hasOwnProperty.call(value, key);
}

/** @param {string} method @param {unknown} raw @param {{ allowArray?: boolean }} options */
function nativeSelectFilesResponse(method, raw, { allowArray = false } = {}) {
  if (allowArray && Array.isArray(raw)) {
    return assertNativeStringArray(method, 'paths', raw);
  }
  return nativePathListResponse(method, raw);
}

/** @param {string} method @param {unknown} raw */
function nativeDatasourceImportFileResponse(method, raw) {
  const value = assertNativeResponseObject(method, raw);
  if (typeof value.sourcePath !== 'string') {
    throw new TypeError(`${method} response sourcePath must be a string`);
  }
  if (typeof value.pickerToken !== 'string') {
    throw new TypeError(`${method} response pickerToken must be a non-empty string`);
  }
  const sourcePath = value.sourcePath.trim();
  const pickerToken = value.pickerToken.trim();
  if (sourcePath && !pickerToken) {
    throw new TypeError(`${method} response pickerToken must be a non-empty string`);
  }
  if (pickerToken && !sourcePath) {
    throw new TypeError(`${method} response sourcePath must be a non-empty string when pickerToken is present`);
  }
  return { sourcePath, pickerToken };
}

/** @param {string} method @param {unknown} raw */
function nativeDroppedTextFilesResponse(method, raw) {
  const value = assertNativeResponseObject(method, raw);
  if (!Array.isArray(value.files)) {
    throw new TypeError(`${method} response files must be an array`);
  }
  return value.files.map((item) => nativeDroppedTextFileItem(method, item));
}

/** @param {string} method @param {unknown} item */
function nativeDroppedTextFileItem(method, item) {
  if (!item || typeof item !== 'object' || Array.isArray(item)) {
    throw new TypeError(`${method} response files entries must be objects`);
  }
  const file = /** @type {NativePayload} */ (item);
  if (typeof file.path !== 'string') throw new TypeError(`${method} response file path must be a string`);
  if (typeof file.name !== 'string') throw new TypeError(`${method} response file name must be a string`);
  if (typeof file.text !== 'string') throw new TypeError(`${method} response file text must be a string`);
  if (typeof file.sizeBytes !== 'number' || !Number.isFinite(file.sizeBytes) || file.sizeBytes < 0) {
    throw new TypeError(`${method} response file sizeBytes must be a non-negative number`);
  }
  return {
    path: file.path,
    name: file.name,
    text: file.text,
    sizeBytes: file.sizeBytes,
  };
}

/** @param {string} method @param {unknown} raw */
function nativeTextFileSaveResponse(method, raw) {
  const value = assertNativeResponseObject(method, raw);
  if (typeof value.path !== 'string') {
    throw new TypeError(`${method} response path must be a string`);
  }
  return value.path;
}

/** @param {string} method @param {unknown} raw */
function nativeSharedFileOpenResponse(method, raw) {
  const value = assertNativeResponseObject(method, raw);
  if (value.opened !== true) {
    throw new Error(`${method} response opened must be true`);
  }
  return value;
}

/** @param {string} method @param {unknown} raw */
function nativeSharedFilePreviewResponse(method, raw) {
  const value = assertNativeResponseObject(method, raw);
  const expectedFields = Object.keys(SHARED_FILE_PREVIEW_FIELD_CONSUMERS);
  for (const field of Object.keys(value)) {
    if (!hasOwnBridgeProperty(SHARED_FILE_PREVIEW_FIELD_CONSUMERS, field)) {
      throw new TypeError(`${method} response contains unknown field ${field}`);
    }
  }
  const url = assertSafeSharedFilePreviewURL(value.url, `${method} response url`);
  if (typeof value.path !== 'string' || !value.path.trim()) {
    throw new TypeError(`${method} response path must be a non-empty string`);
  }
  if (typeof value.contentType !== 'string' || !value.contentType.trim()) {
    throw new TypeError(`${method} response contentType must be a non-empty string`);
  }
  if (typeof value.sizeBytes !== 'number' || !Number.isSafeInteger(value.sizeBytes) || value.sizeBytes < 0 || value.sizeBytes > SHARED_FILE_PREVIEW_MAX_BYTES) {
    throw new TypeError(`${method} response sizeBytes must be within the preview size limit`);
  }
  if (Object.keys(value).length !== expectedFields.length) {
    const missing = expectedFields.find((field) => !hasOwnBridgeProperty(value, field));
    throw new TypeError(`${method} response ${missing} is required`);
  }
  return {
    url,
    path: value.path,
    contentType: value.contentType,
    sizeBytes: value.sizeBytes,
  };
}

/** @param {unknown} options */
async function selectFiles(options = {}) {
  const payload = normalizeSelectFilesOptions(options);
  const hasOptions = Object.keys(payload).length > 0;
  writeBridgeLog('info', 'ui.selectFiles.start', {
    filtered: Array.isArray(payload.filters) && payload.filters.length > 0,
  });
  if (!hasOptions) {
    try {
      const values = await callByID(METHOD_IDS.SELECT_FILES);
      const files = nativeSelectFilesResponse('ui/selectFiles', values, { allowArray: true });
      writeBridgeLog('info', 'ui.selectFiles.done', {
        count: files.length,
        first: firstDiagnosticPath(files),
      });
      return files;
    }
    catch (error) {
      writeBridgeLog('warn', 'ui.selectFiles.byId.failed', { error });
      throw error;
    }
  }

  const raw = await callAPI('ui/selectFiles', payload);
  const files = nativeSelectFilesResponse('ui/selectFiles', raw);
  writeBridgeLog('info', 'ui.selectFiles.done', {
    count: files.length,
    first: firstDiagnosticPath(files),
  });
  return files;
}

/** @param {unknown} options */
async function selectDatasourceImportFile(options = {}) {
  const payload = normalizeSelectFilesOptions(options);
  writeBridgeLog('info', 'ui.selectDatasourceImportFile.start', {
    filtered: Array.isArray(payload.filters) && payload.filters.length > 0,
  });
  const raw = await callAPI('ui/selectDatasourceImportFile', payload);
  const selection = nativeDatasourceImportFileResponse('ui/selectDatasourceImportFile', raw);
  writeBridgeLog('info', 'ui.selectDatasourceImportFile.done', {
    selected: Boolean(selection.sourcePath),
    has_picker_token: Boolean(selection.pickerToken),
  });
  return selection;
}

/** @param {unknown} files @param {string} targetId */
async function readDroppedTextFiles(files, targetId = '') {
  const paths = Array.isArray(files)
    ? files.map((item) => normalizeBridgeInputString(item)).filter(Boolean)
    : [];
  if (paths.length === 0) return [];
  writeBridgeLog('info', 'ui.readDroppedTextFiles.start', {
    count: paths.length,
    target_id: targetId,
  });
  const raw = await callAPI('ui/readDroppedTextFiles', {
    files: paths,
    targetId: targetId,
  });
  return nativeDroppedTextFilesResponse('ui/readDroppedTextFiles', raw);
}

/** @param {unknown} base64Payload */
async function saveClipboardImage(base64Payload) {
  const start = currentMonotonicMS();
  const path = await callByID(METHOD_IDS.SAVE_CLIPBOARD_IMAGE, base64Payload);
  if (typeof path !== 'string') throw new TypeError('ui/saveClipboardImage response path must be a string');
  writeBridgeLog('debug', 'ui.clipboardImage.saved', {
    ok: Boolean(path),
    duration_ms: elapsedMS(start),
  });
  return path;
}

/** @param {{ defaultPath?: string, defaultFilename?: string, content?: string }} options */
async function saveTextFile({ defaultPath = '', defaultFilename = '', content = '' } = {}) {
  const filename = normalizeBridgeInputString(defaultFilename);
  if (!filename) throw new Error('saveTextFile defaultFilename is required');
  writeBridgeLog('info', 'ui.saveTextFile.start', {
    default_path: defaultPath,
    default_filename: filename,
    content_len: content.length,
  });
  const raw = await callAPI('ui/saveTextFile', {
    defaultPath,
    defaultFilename: filename,
    content,
  });
  const path = nativeTextFileSaveResponse('ui/saveTextFile', raw);
  writeBridgeLog('info', 'ui.saveTextFile.done', {
    selected: Boolean(path),
    path,
  });
  return path;
}

/** @param {{ path?: unknown }} options */
async function openSharedFile({ path } = {}) {
  const filePath = normalizeBridgeInputString(path);
  if (!filePath) throw new Error('openSharedFile path is required');
  writeBridgeLog('info', 'ui.openSharedFile.start', { path: filePath });
  const raw = await callAPI('ui/sharedFile/open', { path: filePath });
  writeBridgeLog('info', 'ui.openSharedFile.done', { path: filePath });
  return nativeSharedFileOpenResponse('ui/sharedFile/open', raw);
}

/** @param {{ path?: unknown }} options */
async function previewSharedFile({ path } = {}) {
  const filePath = normalizeBridgeInputString(path);
  if (!filePath) throw new Error('previewSharedFile path is required');
  writeBridgeLog('info', 'ui.previewSharedFile.start', { path: filePath });
  const raw = await callAPI('ui/sharedFile/open', { path: filePath, preview: true });
  writeBridgeLog('info', 'ui.previewSharedFile.done', { path: filePath });
  const response = nativeSharedFilePreviewResponse('ui/sharedFile/open', raw);
  if (response.path !== filePath) {
    throw new TypeError('ui/sharedFile/open response path must match requested path');
  }
  return response;
}

export {
  SHARED_FILE_PREVIEW_FIELD_CONSUMERS,
  SHARED_FILE_PREVIEW_MAX_BYTES,
  selectProjectDir, selectProjectDirs, normalizeSelectFilesOptions, assertNativeResponseObject, assertNativeStringArray, nativePathListResponse,
  firstDiagnosticPath, normalizeBridgeInputString, nativeSelectFilesResponse, nativeDatasourceImportFileResponse, nativeDroppedTextFilesResponse, nativeTextFileSaveResponse,
  nativeSharedFileOpenResponse, nativeSharedFilePreviewResponse, selectFiles, selectDatasourceImportFile, readDroppedTextFiles, saveClipboardImage,
  saveTextFile, openSharedFile, previewSharedFile,
};
