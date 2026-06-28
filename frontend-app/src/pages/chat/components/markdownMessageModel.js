const IMAGE_PATH_RE = /\.(?:png|jpe?g|webp|gif|svg|bmp)(?:[?#].*)?$/i;

function basenameFromPath(path) {
  const value = (path || '').toString().trim().split(/[?#]/, 1)[0];
  if (!value) return '';
  return value.split(/[\\/]/).filter(Boolean).pop() || value;
}

function decodeLocalPath(value) {
  try {
    return decodeURIComponent(value);
  } catch {
    return value;
  }
}

function stripPathSuffix(value) {
  return (value || '').toString().split(/[?#]/, 1)[0];
}

function fileURLToPath(value) {
  const raw = (value || '').toString().trim();
  const stripped = decodeLocalPath(stripPathSuffix(raw.replace(/^file:\/\//i, '')));
  if (/^\/[A-Za-z]:[\\/]/.test(stripped)) return stripped.slice(1);
  if (/^[A-Za-z]:[\\/]/.test(stripped)) return stripped;
  if (/^\/(?!\/)/.test(stripped)) return stripped;
  try {
    const url = new URL(raw);
    if (url.protocol.toLowerCase() !== 'file:') return '';
    const path = decodeLocalPath(url.pathname || '');
    return /^\/[A-Za-z]:[\\/]/.test(path) ? path.slice(1) : path;
  } catch {
    return '';
  }
}

function isGeneratedImagePath(value) {
  const path = (value || '').toString().trim();
  if (!path || !IMAGE_PATH_RE.test(path)) return false;
  return /(?:^|[/\\])\.codex[/\\]generated_images[/\\]/i.test(path);
}

function backendLocalImagePreviewSource(rawValue) {
  const value = (rawValue || '').toString().trim();
  if (!value.startsWith('/local-image?')) return '';
  try {
    const url = new URL(value, 'http://localhost');
    const id = (url.searchParams.get('id') || url.searchParams.get('token') || '').trim();
    if (url.pathname !== '/local-image' || !id || url.searchParams.has('path')) return '';
    return value;
  } catch {
    return '';
  }
}

function imagePreviewSource(rawValue) {
  const value = (rawValue || '').toString().trim();
  if (value.startsWith('/clipboard/') || value.startsWith('/generated-image?')) {
    return value;
  }
  const localPreview = backendLocalImagePreviewSource(value);
  if (localPreview) return localPreview;
  if (!value || !IMAGE_PATH_RE.test(value)) return '';
  if (/^data:image\//i.test(value) || /^https?:\/\//i.test(value)) return value;
  const localPath = /^file:\/\//i.test(value) ? fileURLToPath(value) : value;
  if (isGeneratedImagePath(localPath)) {
    return `/generated-image?path=${encodeURIComponent(localPath)}`;
  }
  return '';
}

function normalizeMessageText(text) {
  return (text || '').toString().replace(/\r\n/g, '\n');
}

export { basenameFromPath, imagePreviewSource, normalizeMessageText };
