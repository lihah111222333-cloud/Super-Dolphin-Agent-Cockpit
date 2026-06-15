import { textValue } from '../../shared/pageShared.js';
import { imagePreviewSource } from './markdownMessageModel.js';

function resolveAttachmentImageSrc(att) {
  const preview = textValue(att.previewUrl || att.url);
  if (preview) {
    if (
      /^data:image\//i.test(preview) ||
      /^https?:\/\//i.test(preview) ||
      preview.startsWith('/clipboard/')
    ) {
      return preview;
    }
    const resolved = imagePreviewSource(preview);
    if (resolved) return resolved;
  }
  const path = textValue(att.path);
  return path ? imagePreviewSource(path) : '';
}

export { resolveAttachmentImageSrc };
