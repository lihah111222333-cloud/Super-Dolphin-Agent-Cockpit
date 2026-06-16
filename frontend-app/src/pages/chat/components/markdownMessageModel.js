const IMAGE_PATH_RE = /\.(?:png|jpe?g|webp|gif|svg|bmp)(?:[?#].*)?$/i;
const LOCAL_IMAGE_PATH_RE = /\.(?:png|jpe?g|webp|gif|bmp)(?:[?#].*)?$/i;

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

function isAbsoluteLocalPath(value) {
  return /^[A-Za-z]:[\\/]/.test(value) || /^\//.test(value);
}

function localImagePreviewSource(rawValue) {
  const path = stripPathSuffix((rawValue || '').toString().trim());
  if (!path || !LOCAL_IMAGE_PATH_RE.test(path) || !isAbsoluteLocalPath(path)) return '';
  return `/local-image?path=${encodeURIComponent(path)}`;
}

function imagePreviewSource(rawValue) {
  const value = (rawValue || '').toString().trim();
  if (
    value.startsWith('/clipboard/') ||
    value.startsWith('/generated-image?') ||
    value.startsWith('/local-image?')
  ) {
    return value;
  }
  if (!value || !IMAGE_PATH_RE.test(value)) return '';
  if (/^data:image\//i.test(value) || /^https?:\/\//i.test(value)) return value;
  const localPath = /^file:\/\//i.test(value) ? fileURLToPath(value) : value;
  if (isGeneratedImagePath(localPath)) {
    return `/generated-image?path=${encodeURIComponent(localPath)}`;
  }
  return localImagePreviewSource(localPath);
}

function normalizeMessageText(text) {
  return (text || '').toString().replace(/\r\n/g, '\n');
}

export { basenameFromPath, imagePreviewSource, normalizeMessageText };
