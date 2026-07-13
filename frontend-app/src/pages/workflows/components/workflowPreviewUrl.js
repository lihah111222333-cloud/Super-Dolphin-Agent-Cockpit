function normalizedPreviewText(value) {
  return value == null ? '' : String(value);
}

function previewUrlFromResponse(response) {
  const url = normalizedPreviewText(response?.url).trim();
  if (!url) throw new Error('preview URL is empty');
  assertSafeSharedFilePreviewURL(url, 'preview URL');
  const contentType = normalizedPreviewText(response?.contentType).trim();
  if (!/^(?:image|video)\/[a-z0-9.+-]+$/i.test(contentType)) {
    throw new TypeError('preview content type must be a non-empty image or video MIME type');
  }
  return {
    contentType,
    url,
  };
}

export { previewUrlFromResponse };
import { assertSafeSharedFilePreviewURL } from '../../../shared/api/wails/sharedFilePreviewContract.js';
