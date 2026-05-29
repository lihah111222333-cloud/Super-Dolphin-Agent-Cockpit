// @ts-nocheck
import { toFilePreviewURL } from './preview-utils.js';

const DIRECT_IMAGE_POINTER_RE = new RegExp(String.raw`^(data:|https?:|file:)`, 'i');
const LOCAL_IMAGE_POINTER_RE = new RegExp(String.raw`^([\\/]|~[\\/]|\.{1,2}[\\/]|[A-Za-z]:[\\/])`);

function directImageSource(pointer) {
  if (DIRECT_IMAGE_POINTER_RE.test(pointer)) return pointer;
  if (LOCAL_IMAGE_POINTER_RE.test(pointer)) return toFilePreviewURL(pointer);
  return '';
}

function imageAttachmentMeta(attachment) {
  if (!attachment || typeof attachment !== 'object') return null;
  const previewUrl = (attachment.previewUrl || '').toString().trim();
  const path = (attachment.path || '').toString().trim();
  const name = (attachment.name || '').toString().trim();
  const attachmentPointer = (attachment.assetPointer || attachment.asset_pointer || attachment.id || '').toString().trim();
  const isImage = (attachment.kind || '').toString().trim() === 'image'
    || previewUrl.startsWith('data:image/')
    || /\.(png|jpe?g|gif|webp|svg)$/i.test(path || name);
  if (!isImage) return null;
  return { previewUrl, path, name, attachmentPointer };
}

/**
 * @param {Array<any>} activeTimelineItems
 * @param {string} assetPointer
 * @param {string} [rawLabel]
 */
export function resolveCitationImagePreview(activeTimelineItems, assetPointer, rawLabel = '') {
  const pointer = (assetPointer || '').toString().trim();
  const directSrc = directImageSource(pointer);
  if (directSrc) {
    return {
      src: directSrc,
      fullSrc: directSrc,
      path: pointer,
      mediaType: 'image/*',
      sizeBytes: 0,
    };
  }

  const items = Array.isArray(activeTimelineItems) ? activeTimelineItems : [];
  let imageCount = 0;
  /** @type {any} */
  let fallbackAttachment = null;
  /** @type {any} */
  let matched = null;

  for (let index = items.length - 1; index >= 0; index -= 1) {
    const item = items[index];
    const attachments = Array.isArray(item?.attachments) ? item.attachments : [];
    for (const attachment of attachments) {
      const meta = imageAttachmentMeta(attachment);
      if (!meta) continue;
      imageCount += 1;
      if (!fallbackAttachment) fallbackAttachment = meta;
      if (pointer && [meta.attachmentPointer, meta.path, meta.previewUrl, meta.name].filter(Boolean).includes(pointer)) {
        matched = meta;
        break;
      }
    }
    if (matched) break;
  }

  const resolved = matched || (imageCount === 1 ? fallbackAttachment : null);
  if (!resolved) return null;
  const src = (resolved.previewUrl || toFilePreviewURL(resolved.path)).toString().trim();
  if (!src) return null;
  return {
    src,
    fullSrc: src,
    path: resolved.path || pointer || rawLabel,
    mediaType: 'image/*',
    sizeBytes: 0,
  };
}
