// @ts-nocheck
import { toFilePreviewURL } from './preview-utils.js';

/**
 * @param {Array<any>} activeTimelineItems
 * @param {string} assetPointer
 * @param {string} [rawLabel]
 */
export function resolveCitationImagePreview(activeTimelineItems, assetPointer, rawLabel = '') {
  const pointer = (assetPointer || '').toString().trim();
  const directSrc = /^(?:data:|https?:|file:)/i.test(pointer)
    ? pointer
    : /^(?:[\\/]|~[\\/]|\.{1,2}[\\/]|[A-Za-z]:[\\/])/.test(pointer)
      ? toFilePreviewURL(pointer)
      : '';
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
      if (!attachment || typeof attachment !== 'object') continue;
      const previewUrl = (attachment.previewUrl || '').toString().trim();
      const path = (attachment.path || '').toString().trim();
      const name = (attachment.name || '').toString().trim();
      const attachmentPointer = (attachment.assetPointer || attachment.asset_pointer || attachment.id || '').toString().trim();
      const isImage = (attachment.kind || '').toString().trim() === 'image'
        || previewUrl.startsWith('data:image/')
        || /\.(png|jpe?g|gif|webp|svg)$/i.test(path || name);
      if (!isImage) continue;
      imageCount += 1;
      if (!fallbackAttachment) fallbackAttachment = { previewUrl, path, name, attachmentPointer };
      if (pointer && [attachmentPointer, path, previewUrl, name].filter(Boolean).includes(pointer)) {
        matched = { previewUrl, path, name, attachmentPointer };
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
