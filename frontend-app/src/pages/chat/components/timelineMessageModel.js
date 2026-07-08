import { textValue } from '../../shared/pageShared.js';
import { basenameFromPath, firstText, imagePreviewSource, trustedImagePreviewSource } from './markdownMessageModel.js';

const CLIPBOARD_IMAGE_NAME_RE = /^(?:codex-)?clipboard-.+\.png$/i;

function clipboardImagePreviewSource(rawValue) {
  const base = basenameFromPath(rawValue);
  return CLIPBOARD_IMAGE_NAME_RE.test(base) ? `/clipboard/${encodeURIComponent(base)}` : '';
}

function resolveImagePreviewValue(rawValue) {
  const value = textValue(rawValue);
  if (!value) return '';
  const trustedPreview = trustedImagePreviewSource(value);
  if (trustedPreview) return trustedPreview;
  if (/^blob:/i.test(value)) return value;
  return clipboardImagePreviewSource(value) || imagePreviewSource(value);
}

function resolveAttachmentImageSrc(att) {
  const preview = resolveImagePreviewValue(firstText(att?.previewUrl, att?.url, att?.src));
  if (preview) return preview;
  return resolveImagePreviewValue(att?.path);
}

export { resolveAttachmentImageSrc };
