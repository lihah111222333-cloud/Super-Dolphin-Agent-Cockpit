// @ts-check

import {
  normalizeThreadId,
} from './threadIdentity.js';

const IMAGE_ATTACHMENT_RE = /\.(png|jpe?g|gif|webp|bmp|svg)$/i;

/**
 * @typedef {{ path: string, name: string, kind: string, previewUrl: string }} ComposerAttachment
 * @typedef {{ path?: unknown, url?: unknown, name?: unknown, kind?: unknown, previewUrl?: unknown }} AttachmentLike
 * @typedef {AttachmentLike | ComposerAttachment} AttachmentObjectInput
 * @typedef {string | AttachmentObjectInput} AttachmentInput
 * @typedef {{ draft?: unknown, attachments?: AttachmentObjectInput[] }} ComposerDraftSnapshotInput
 * @typedef {{ activeProject?: unknown, cwd?: unknown, activeThreadId?: unknown }} ComposerScopeState
 * @typedef {{ path?: unknown, type?: unknown, name?: unknown }} DroppedFileLike
 * @typedef {{ type: 'text', text: string } | { type: 'localImage', path: string, url?: string } | { type: 'mention', name: string, path: string }} TurnInputItem
 */

/**
 * @param {unknown} value
 * @returns {string}
 */
function normalizeString(value) {
  return (value || '').toString().trim();
}

/**
 * @param {unknown} value
 * @returns {string}
 */
function normalizePath(value) {
  const path = normalizeString(value);
  if (!path) return '';
  if (path !== '/' && !/^[a-zA-Z]:[\\/]?$/.test(path)) {
    return path.replace(/[\\/]+$/, '');
  }
  return path;
}

/**
 * @param {Blob} blob
 * @returns {Promise<string>}
 */
function blobToDataURL(blob) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(normalizeString(reader.result));
    reader.onerror = () => reject(reader.error || new Error('read image blob failed'));
    reader.readAsDataURL(blob);
  });
}

/**
 * @param {unknown} path
 * @returns {string}
 */
export function basename(path) {
  const value = normalizeString(path);
  return value.split(/[\\/]/).filter(Boolean).pop() || value;
}

/**
 * @param {AttachmentInput | null | undefined} value
 * @returns {string}
 */
export function attachmentDisplayName(value) {
  const attachment = normalizeAttachment(value);
  if (!attachment) return '附件';
  return normalizeString(attachment.name) || basename(attachment.path) || '附件';
}

/**
 * @param {unknown} path
 * @returns {boolean}
 */
export function isImagePath(path) {
  return IMAGE_ATTACHMENT_RE.test(normalizeString(path));
}

/**
 * @param {unknown} path
 * @returns {ComposerAttachment | null}
 */
export function normalizeFileAttachment(path) {
  const value = normalizeString(path);
  if (!value) return null;
  const name = basename(value);
  const image = isImagePath(name);
  return {
    path: value,
    name,
    kind: image ? 'image' : 'file',
    previewUrl: '',
  };
}

/**
 * @param {AttachmentInput | null | undefined} value
 * @returns {ComposerAttachment | null}
 */
export function normalizeAttachment(value) {
  if (typeof value === 'string') {
    return normalizeFileAttachment(value);
  }
  if (!value || typeof value !== 'object') return null;
  const attachmentValue = /** @type {AttachmentLike} */ (value);
  const path = normalizeString(attachmentValue.path || attachmentValue.url);
  if (!path) return null;
  const kind = normalizeString(attachmentValue.kind) || (isImagePath(path) ? 'image' : 'file');
  const previewUrl = normalizeString(attachmentValue.previewUrl || attachmentValue.url);
  return {
    path,
    name: normalizeString(attachmentValue.name) || basename(path),
    kind,
    previewUrl,
  };
}

/**
 * @param {AttachmentObjectInput[] | null | undefined} attachments
 * @returns {ComposerAttachment[]}
 */
export function cloneComposerAttachments(attachments) {
  return (
    Array.isArray(attachments)
      ? attachments.map((item) => ({ ...item })).map(normalizeAttachment).filter(Boolean)
      : []
  );
}

/**
 * @param {ComposerDraftSnapshotInput} [value]
 * @returns {{ draft: string, attachments: ComposerAttachment[] }}
 */
export function normalizeComposerDraftSnapshot(value = {}) {
  return {
    draft: (value.draft || '').toString(),
    attachments: cloneComposerAttachments(value.attachments),
  };
}

/**
 * @param {ComposerDraftSnapshotInput} [value]
 * @returns {boolean}
 */
export function isEmptyComposerDraftSnapshot(value = {}) {
  const draft = normalizeComposerDraftSnapshot(value);
  return !draft.draft && draft.attachments.length === 0;
}

/**
 * @param {ComposerScopeState} [state]
 * @returns {string}
 */
export function composerScopeCwd(state = {}) {
  const activeProject = normalizePath(state.activeProject);
  if (activeProject && activeProject !== '.') return activeProject;
  return normalizePath(state.cwd);
}

/**
 * @param {ComposerScopeState} [state]
 * @param {unknown} [threadId]
 * @returns {string}
 */
export function composerDraftKey(state = {}, threadId = state.activeThreadId) {
  const cwd = composerScopeCwd(state);
  const id = normalizeThreadId(threadId);
  return `${cwd || '__missing_cwd__'}::${id ? `thread:${id}` : 'new:chat'}`;
}

/**
 * @param {AttachmentInput | null | undefined} value
 * @returns {string}
 */
export function attachmentKey(value) {
  const attachment = normalizeAttachment(value);
  return attachment ? (attachment.path || attachment.previewUrl) : '';
}

/**
 * @param {ComposerAttachment[]} current
 * @param {AttachmentInput[] | null | undefined} incoming
 * @returns {ComposerAttachment[]}
 */
export function appendUniqueAttachments(current, incoming) {
  const next = [...current];
  const seen = new Set(next.map(attachmentKey).filter(Boolean));
  for (const item of incoming || []) {
    const attachment = normalizeAttachment(item);
    const key = attachmentKey(attachment);
    if (!attachment || !key || seen.has(key)) continue;
    seen.add(key);
    next.push(attachment);
  }
  return next;
}

/**
 * @param {Iterable<File> | ArrayLike<File> | null | undefined} value
 * @returns {File[]}
 */
export function fileListOf(value) {
  return Array.from(value || []).filter(Boolean);
}

/**
 * @param {DroppedFileLike | null | undefined} file
 * @returns {string}
 */
export function droppedFilePath(file) {
  return normalizeString(file?.path);
}

/**
 * @param {DroppedFileLike | null | undefined} file
 * @returns {boolean}
 */
export function fileLooksImage(file) {
  return normalizeString(file?.type).toLowerCase().startsWith('image/') || isImagePath(file?.name);
}

/**
 * 保存浏览器内存里的图片附件，返回的本地路径只用于发送边界。
 * @param {{ saveClipboardImage?: (base64: string) => Promise<string> | string, nowMillis?: () => number }} [options]
 * @returns {(file: Blob & { name?: string }, index: number, fallbackPrefix: string) => Promise<ComposerAttachment>}
 */
export function createImageFileAttachment({ saveClipboardImage, nowMillis = () => Date.now() } = {}) {
  if (typeof saveClipboardImage !== 'function') throw new Error('saveClipboardImage is required');
  return async function imageFileAttachment(file, index, fallbackPrefix) {
    const dataUrl = await blobToDataURL(file);
    const base64 = dataUrl.split(',')[1] || '';
    if (!base64) throw new Error('image attachment data is empty');
    const path = normalizeString(await saveClipboardImage(base64));
    if (!path) throw new Error('clipboard image save returned empty path');
    return {
      path,
      name: normalizeString(file?.name) || `${fallbackPrefix}-${nowMillis()}-${index}.png`,
      kind: 'image',
      previewUrl: dataUrl,
    };
  };
}

/**
 * @param {AttachmentInput | null | undefined} item
 * @returns {TurnInputItem | null}
 */
export function attachmentToInputItem(item) {
  const attachment = normalizeAttachment(item);
  if (!attachment) return null;
  if (attachment.kind === 'image') {
    const payload = /** @type {{ type: 'localImage', path: string, url?: string }} */ ({
      type: 'localImage',
      path: attachment.path,
    });
    if (attachment.previewUrl.toLowerCase().startsWith('data:image/')) {
      payload.url = attachment.previewUrl;
    }
    return payload;
  }
  return { type: 'mention', name: attachment.name || basename(attachment.path), path: attachment.path };
}

/**
 * @param {unknown} text
 * @param {AttachmentInput[] | null | undefined} attachments
 * @returns {TurnInputItem[]}
 */
export function buildTurnInput(text, attachments) {
  const items = /** @type {TurnInputItem[]} */ ([]);
  const message = normalizeString(text);
  if (message) items.push({ type: 'text', text: message });
  for (const attachment of attachments || []) {
    const item = attachmentToInputItem(attachment);
    if (item) items.push(item);
  }
  return items;
}
