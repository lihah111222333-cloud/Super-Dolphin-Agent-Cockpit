import { useCallback, useEffect, useRef, useState } from 'react';
import { textValue } from '../../shared/pageShared.js';
import { composerAttachmentKey } from '../components/composerAttachmentKey.js';
import { onFilesDropped } from '../services/chatCodeService.js';

export const CONVERSATION_DROP_TARGET_ID = 'conversation-drop-zone';

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

export function hasFilesTransfer(event) {
  const transfer = event?.dataTransfer;
  if (!transfer) return false;
  if (transfer.files && transfer.files.length > 0) return true;
  const types = Array.from(transfer.types || []).map((type) => textValue(type));
  if (types.includes('Files')) return true;
  return types.some((type) => DROP_FILE_PATH_TYPES.has(type));
}

export function collectTransferFiles(event) {
  const transfer = event?.dataTransfer;
  if (!transfer) return [];
  const files = Array.from(transfer.files || []).filter(Boolean);
  if (files.length > 0) return files;
  const collected = [];
  for (const item of Array.from(transfer.items || [])) {
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
    let pathname = decodeURIComponent(url.pathname || '');
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

export function clipboardPathsFromText(text) {
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

export function extractFilePathsFromTransferData(transferData) {
  if (!transferData || typeof transferData.getData !== 'function') return [];
  const types = new Set(Array.from(transferData.types || []).map((type) => textValue(type)));
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

function extractTransferFilePaths(event) {
  return extractFilePathsFromTransferData(event?.dataTransfer);
}

function extractClipboardFilePaths(event) {
  return extractFilePathsFromTransferData(event?.clipboardData);
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
  Array.from(clipboard.files || []).forEach(add);
  Array.from(clipboard.items || []).forEach((item) => {
    if (item?.kind !== 'file') return;
    add(item.getAsFile?.());
  });
  return files;
}

export function nativeDropFiles(event, options) {
  if (!event || typeof event !== 'object') return [];
  const candidates = [event, event.data, event.payload, event.data?.payload];
  const payload = candidates.find((item) => item && typeof item === 'object' && Array.isArray(item.files));
  if (!nativeDropTargetAcceptsFiles(payload?.details, options)) return [];
  return payload?.files || [];
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

export function nativeDropTargetAcceptsFiles(details, options = {}) {
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

export function useComposerInteractions({
  attachments,
  attachPaths,
  attachDroppedFiles,
  removeAttachment,
  projectActionBlocked,
  canUseProjectActions,
}) {
  /*
   * composer 交互层只管理浏览器本地状态：预览、拖拽深度、IME 输入。
   * 附件保存、去重和发送 input 都在 store 里完成。
   */
  const [previewAttachment, setPreviewAttachment] = useState(null);
  const [dropActive, setDropActive] = useState(false);
  const dropDepthRef = useRef(0);
  const isComposingRef = useRef(false);
  const activePreview = previewAttachment && attachments.some((item) => composerAttachmentKey(item) === composerAttachmentKey(previewAttachment))
    ? previewAttachment
    : null;

  const previewAttachmentItem = (item) => {
    setPreviewAttachment(item);
  };
  const removeAttachmentItem = (item) => {
    removeAttachment(composerAttachmentKey(item));
    if (activePreview && composerAttachmentKey(activePreview) === composerAttachmentKey(item)) {
      setPreviewAttachment(null);
    }
  };
  const handlers = useComposerTransferHandlers({
    attachDroppedFiles,
    attachPaths,
    canUseProjectActions,
    dropDepthRef,
    projectActionBlocked,
    setDropActive,
  });

  return {
    activePreview,
    dropActive,
    handleCompositionEnd: () => { isComposingRef.current = false; },
    handleCompositionStart: () => { isComposingRef.current = true; },
    isComposing: () => isComposingRef.current,
    previewAttachmentItem,
    removeAttachmentItem,
    setPreviewAttachment,
    ...handlers,
  };
}

function useComposerTransferHandlers({ attachDroppedFiles, attachPaths, canUseProjectActions, dropDepthRef, projectActionBlocked, setDropActive }) {
  /*
   * 拖拽/粘贴可能来自 File、Wails 原生事件或 file:// 文本。
   * 项目还没准备好时，只处理 UI 事件，不把路径写进 composer。
   */
  const resetDropState = useCallback(() => {
    dropDepthRef.current = 0;
    setDropActive(false);
  }, [dropDepthRef, setDropActive]);

  useEffect(() => {
    if (typeof attachPaths !== 'function') return undefined;
    return onFilesDropped((event) => {
      const files = nativeDropFiles(event, { acceptEmptyDetails: dropDepthRef.current > 0 });
      if (files.length === 0) return;
      if (!canUseProjectActions) return;
      attachPaths(files);
      resetDropState();
    });
  }, [attachPaths, canUseProjectActions, dropDepthRef, resetDropState]);
  const handlePaste = async (event) => {
    const paths = extractClipboardFilePaths(event);
    if (paths.length > 0) {
      event.preventDefault();
      if (projectActionBlocked) return;
      if (typeof attachPaths === 'function') attachPaths(paths);
      return;
    }
    const files = extractClipboardFiles(event);
    if (files.length === 0) return;
    event.preventDefault();
    if (projectActionBlocked) return;
    await attachDroppedFiles(files);
  };
  const handleDragEnter = (event) => {
    if (!hasFilesTransfer(event)) return;
    event.preventDefault();
    event.stopPropagation();
    if (projectActionBlocked) return;
    dropDepthRef.current += 1;
    setDropActive(true);
  };
  const handleDragOver = (event) => {
    if (!hasFilesTransfer(event)) return;
    event.preventDefault();
    event.stopPropagation();
    if (projectActionBlocked) return;
    if (event.dataTransfer) event.dataTransfer.dropEffect = 'copy';
    setDropActive(true);
  };
  const handleDragLeave = (event) => {
    if (!hasFilesTransfer(event)) return;
    event.preventDefault();
    event.stopPropagation();
    dropDepthRef.current = Math.max(dropDepthRef.current - 1, 0);
    if (dropDepthRef.current === 0) setDropActive(false);
  };
  const handleDrop = async (event) => {
    if (!hasFilesTransfer(event)) return;
    event.preventDefault();
    event.stopPropagation();
    resetDropState();
    if (projectActionBlocked) return;
    const files = collectTransferFiles(event);
    const paths = extractTransferFilePaths(event);
    if (files.length > 0) {
      const attachedCount = await attachDroppedFiles(files);
      if (attachedCount > 0 && paths.length === 0) return;
    }
    if (paths.length > 0 && typeof attachPaths === 'function') attachPaths(paths);
  };
  return { handleDragEnter, handleDragLeave, handleDragOver, handleDrop, handlePaste };
}
