// @ts-check

import {
  normalizeThreadId,
} from './threadIdentity.js';

const IMAGE_ATTACHMENT_RE = /\.(png|jpe?g|gif|webp|bmp|svg)$/i;

function normalizeString(value) {
  return (value || '').toString().trim();
}

function normalizePath(value) {
  const path = normalizeString(value);
  if (!path) return '';
  if (path !== '/' && !/^[a-zA-Z]:[\\/]?$/.test(path)) {
    return path.replace(/[\\/]+$/, '');
  }
  return path;
}

function blobToDataURL(blob) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(normalizeString(reader.result));
    reader.onerror = () => reject(reader.error || new Error('read image blob failed'));
    reader.readAsDataURL(blob);
  });
}

export function basename(path) {
  const value = normalizeString(path);
  return value.split(/[\\/]/).filter(Boolean).pop() || value;
}

export function isImagePath(path) {
  return IMAGE_ATTACHMENT_RE.test(normalizeString(path));
}

export function normalizeFileAttachment(path) {
  const value = normalizeString(path);
  if (!value) return null;
  const name = basename(value);
  const image = isImagePath(name);
  return {
    path: value,
    name,
    kind: image ? 'image' : 'file',
    previewUrl: image ? `file://${value}` : '',
  };
}

export function normalizeAttachment(value) {
  if (typeof value === 'string') {
    return normalizeFileAttachment(value);
  }
  if (!value || typeof value !== 'object') return null;
  const path = normalizeString(value.path || value.url);
  if (!path) return null;
  const kind = normalizeString(value.kind) || (isImagePath(path) ? 'image' : 'file');
  const previewUrl = normalizeString(value.previewUrl || value.url) || (kind === 'image' && isImagePath(path) ? `file://${path}` : '');
  return {
    path,
    name: normalizeString(value.name) || basename(path),
    kind,
    previewUrl,
  };
}

export function cloneComposerAttachments(attachments) {
  return (
    Array.isArray(attachments)
      ? attachments.map((item) => ({ ...item })).map(normalizeAttachment).filter(Boolean)
      : []
  );
}

export function normalizeComposerDraftSnapshot(value = {}) {
  return {
    draft: (value.draft || '').toString(),
    attachments: cloneComposerAttachments(value.attachments),
  };
}

export function isEmptyComposerDraftSnapshot(value = {}) {
  const draft = normalizeComposerDraftSnapshot(value);
  return !draft.draft && draft.attachments.length === 0;
}

export function composerScopeCwd(state = {}) {
  const activeProject = normalizePath(state.activeProject);
  if (activeProject && activeProject !== '.') return activeProject;
  return normalizePath(state.cwd);
}

export function composerDraftKey(state = {}, threadId = state.activeThreadId) {
  const cwd = composerScopeCwd(state);
  const id = normalizeThreadId(threadId);
  return `${cwd || '__missing_cwd__'}::${id ? `thread:${id}` : 'new:chat'}`;
}

export function attachmentKey(value) {
  const attachment = normalizeAttachment(value);
  return attachment ? (attachment.path || attachment.previewUrl) : '';
}

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

export function fileListOf(value) {
  return Array.from(value || []).filter(Boolean);
}

export function droppedFilePath(file) {
  return normalizeString(file?.path);
}

export function fileLooksImage(file) {
  return normalizeString(file?.type).toLowerCase().startsWith('image/') || isImagePath(file?.name);
}

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

export function attachmentToInputItem(item) {
  const attachment = normalizeAttachment(item);
  if (!attachment) return null;
  if (attachment.kind === 'image') {
    const payload = { type: 'localImage', path: attachment.path };
    if (attachment.previewUrl.toLowerCase().startsWith('data:image/')) {
      payload.url = attachment.previewUrl;
    }
    return payload;
  }
  return { type: 'mention', name: attachment.name || basename(attachment.path), path: attachment.path };
}

export function buildTurnInput(text, attachments) {
  const items = [];
  const message = normalizeString(text);
  if (message) items.push({ type: 'text', text: message });
  for (const attachment of attachments || []) {
    const item = attachmentToInputItem(attachment);
    if (item) items.push(item);
  }
  return items;
}
