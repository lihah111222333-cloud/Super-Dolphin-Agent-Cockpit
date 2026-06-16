import { textValue } from '../../shared/pageShared.js';
import { basenameFromPath, imagePreviewSource } from './markdownMessageModel.js';

const CLIPBOARD_IMAGE_NAME_RE = /^(?:codex-)?clipboard-.+\.png$/i;

function clipboardImagePreviewSource(rawValue) {
  const base = basenameFromPath(rawValue);
  return CLIPBOARD_IMAGE_NAME_RE.test(base) ? `/clipboard/${encodeURIComponent(base)}` : '';
}

function resolveImagePreviewValue(rawValue) {
  const value = textValue(rawValue);
  if (!value) return '';
  if (
    /^data:image\//i.test(value) ||
    /^blob:/i.test(value) ||
    /^https?:\/\//i.test(value) ||
    value.startsWith('/clipboard/') ||
    value.startsWith('/generated-image?')
  ) {
    return value;
  }
  return clipboardImagePreviewSource(value) || imagePreviewSource(value);
}

function resolveAttachmentImageSrc(att) {
  const preview = resolveImagePreviewValue(att?.previewUrl || att?.url || att?.src);
  if (preview) return preview;
  return resolveImagePreviewValue(att?.path);
}

export { resolveAttachmentImageSrc };
