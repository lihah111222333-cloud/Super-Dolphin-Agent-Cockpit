import { basename } from '../../../entities/client/model/composerAttachments.js';
import { textValue } from '../../shared/pageShared.js';

const CONVERSATION_DROP_TARGET_ID = 'conversation-drop-zone';
const CLIPBOARD_FILE_PATH_TYPES = Object.freeze(['x-special/gnome-copied-files', 'text/uri-list', 'text/plain']);
const DROP_FILE_PATH_TYPES = new Set(['x-special/gnome-copied-files', 'text/uri-list']);
const NATIVE_FILE_DROP_TARGET_IDS = new Set(['composer-input', 'chat-input-bar', CONVERSATION_DROP_TARGET_ID]);
const NATIVE_FILE_DROP_TARGET_ATTRIBUTE = 'data-file-drop-target';
const NATIVE_FILE_DROP_TARGET_CLASSES = new Set([
  'composer',
  'composer-actions',
  'composer-attach',
  'composer--docked',
  'composer--floating',
  'composer-card',
  'composer-drop-hint',
  'composer-meta',
  'composer-model',
  'composer-model-wrap',
  'conversation',
  'conversation--intro',
  'send',
  'timeline',
  'timeline-shell',
]);

function transferList(value) {
  if (!value) return [];
  return Array.from(value);
}

function hasFilesTransfer(event) {
  const transfer = event?.dataTransfer;
  if (!transfer) return false;
  if (transfer.files && transfer.files.length > 0) return true;
  const types = transferList(transfer.types).map((type) => textValue(type));
  if (types.includes('Files')) return true;
  return types.some((type) => DROP_FILE_PATH_TYPES.has(type));
}

function collectTransferFiles(event) {
  const transfer = event?.dataTransfer;
  if (!transfer) return [];
  const files = transferList(transfer.files).filter(Boolean);
  if (files.length > 0) return files;
  const collected = [];
  for (const item of transferList(transfer.items)) {
    if (item?.kind !== 'file') continue;
    const file = item.getAsFile?.();
    if (file) collected.push(file);
  }
  return collected;
}

function decodeClipboardFileUri(value) {
  const raw = textValue(value).trim();
  if (!/^file:/i.test(raw)) return '';
  try {
    const url = new URL(raw);
    if (url.protocol !== 'file:') return '';
    const hostname = textValue(url.hostname);
    let pathname = decodeURIComponent(url.pathname ? url.pathname : '');
    if (/^\/[a-zA-Z]:[\\/]/.test(pathname)) pathname = pathname.slice(1);
    if (hostname && hostname !== 'localhost') return `//${hostname}${pathname}`;
    return pathname;
  }
  catch {
    try {
      return decodeURIComponent(raw.replace(/^file:\/+/i, '/'));
    }
    catch {
      return raw.replace(/^file:\/+/i, '/');
    }
  }
}

function normalizeClipboardPathLine(line) {
  let value = textValue(line).trim();
  if (
    value.length >= 2 &&
    ((value.startsWith('"') && value.endsWith('"')) || (value.startsWith("'") && value.endsWith("'")))
  ) {
    value = value.slice(1, -1).trim();
  }
  if (!value || value.startsWith('#')) return '';
  if (value === 'copy' || value === 'cut') return '';
  if (/^file:/i.test(value)) return decodeClipboardFileUri(value);
  if (/^[a-zA-Z]:[\\/]/.test(value) || value.startsWith('/') || value.startsWith('\\\\')) return value;
  return '';
}

function clipboardPathsFromText(text) {
  const paths = [];
  const seen = new Set();
  for (const line of textValue(text).split(/\r?\n/)) {
    const path = normalizeClipboardPathLine(line);
    if (!path || seen.has(path)) continue;
    seen.add(path);
    paths.push(path);
  }
  return paths;
}

function extractFilePathsFromTransferData(transferData) {
  if (!transferData || typeof transferData.getData !== 'function') return [];
  const types = new Set(transferList(transferData.types).map((type) => textValue(type)));
  const paths = [];
  const seen = new Set();
  for (const type of CLIPBOARD_FILE_PATH_TYPES) {
    if (types.size > 0 && !types.has(type)) continue;
    let data;
    try {
      data = transferData.getData(type);
    }
    catch {
      continue;
    }
    for (const path of clipboardPathsFromText(data)) {
      if (seen.has(path)) continue;
      seen.add(path);
      paths.push(path);
    }
  }
  return paths;
}

function extractClipboardFiles(event) {
  const clipboard = event?.clipboardData;
  if (!clipboard) return [];
  const files = [];
  const seen = new Set();
  const add = (file) => {
    if (!file || seen.has(file)) return;
    seen.add(file);
    files.push(file);
  };
  transferList(clipboard.files).forEach(add);
  transferList(clipboard.items).forEach((item) => {
    if (item?.kind !== 'file') return;
    add(item.getAsFile?.());
  });
  return files;
}

function nativeDropPreviewUrl(value) {
  const raw = textValue(value);
  if (!raw.startsWith('/local-image?')) return '';
  const params = new URLSearchParams(raw.slice('/local-image?'.length));
  return params.get('id') && !params.has('path') ? raw : '';
}

function nativeDropAttachments(payload) {
  const previews = payload?.imagePreviews && typeof payload.imagePreviews === 'object'
    ? payload.imagePreviews
    : {};
  return payload.files.map((path) => {
    const previewUrl = nativeDropPreviewUrl(previews[path]);
    if (!previewUrl) return path;
    return { path, name: basename(path), kind: 'image', previewUrl };
  });
}

function nativeDropClassTokens(value) {
  if (!value) return [];
  const raw = Array.isArray(value)
    ? value
    : (typeof value === 'string' ? value.split(/\s+/) : []);
  return raw.map((item) => textValue(item)).filter(Boolean);
}

function nativeDropHasAcceptedClass(value) {
  return nativeDropClassTokens(value).some((className) => NATIVE_FILE_DROP_TARGET_CLASSES.has(className));
}

function nativeDropHasTargetEvidence(details, attributes) {
  if (textValue(details?.id) || nativeDropClassTokens(details?.classList).length > 0) return true;
  if (!attributes) return false;
  return Boolean(textValue(attributes.id)
    || nativeDropClassTokens(attributes.class).length > 0
    || Object.keys(attributes).length > 0);
}

function nativeDropTargetAcceptsFiles(details, options = {}) {
  if (!details || typeof details !== 'object') return Boolean(options.acceptEmptyDetails);
  const id = textValue(details.id);
  if (NATIVE_FILE_DROP_TARGET_IDS.has(id)) return true;
  if (nativeDropHasAcceptedClass(details.classList)) return true;

  const attributes = details.attributes && typeof details.attributes === 'object' ? details.attributes : null;
  if (!attributes) return Boolean(options.acceptEmptyDetails && !nativeDropHasTargetEvidence(details, attributes));
  const attributeId = textValue(attributes.id);
  return NATIVE_FILE_DROP_TARGET_IDS.has(attributeId)
    || Object.prototype.hasOwnProperty.call(attributes, NATIVE_FILE_DROP_TARGET_ATTRIBUTE)
    || nativeDropHasAcceptedClass(attributes.class)
    || Boolean(options.acceptEmptyDetails && !nativeDropHasTargetEvidence(details, attributes));
}

function nativeDropFiles(event, options) {
  if (!event || typeof event !== 'object') return [];
  const candidates = [event, event.data, event.payload, event.data?.payload];
  const payload = candidates.find((item) => item && typeof item === 'object' && Array.isArray(item.files));
  if (!nativeDropTargetAcceptsFiles(payload?.details, options)) return [];
  return nativeDropAttachments(payload);
}

function fileDropSubscriptionCleanup(subscription) {
  if (typeof subscription === 'function') return subscription;
  if (subscription && typeof subscription.unsubscribe === 'function') return () => subscription.unsubscribe();
  return undefined;
}

export {
  CONVERSATION_DROP_TARGET_ID,
  clipboardPathsFromText,
  collectTransferFiles,
  extractClipboardFiles,
  extractFilePathsFromTransferData,
  fileDropSubscriptionCleanup,
  hasFilesTransfer,
  nativeDropFiles,
  nativeDropTargetAcceptsFiles,
};
