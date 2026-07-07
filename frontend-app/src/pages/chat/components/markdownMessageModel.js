const IMAGE_PATH_RE = /\.(?:png|jpe?g|webp|gif|svg|bmp)(?:[?#].*)?$/i;
const SAFE_RASTER_DATA_URL_RE = /^data:image\/(?:png|jpe?g|webp|gif|bmp);base64,[a-z0-9+/=\s]+$/i;

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

function localPathSegments(value) {
  return decodeLocalPath(stripPathSuffix(value)).replace(/\\/g, '/').split('/').filter(Boolean);
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
  const segments = localPathSegments(path);
  if (segments.includes('..')) return false;
  return segments.some((segment, index) => (
    segment.toLowerCase() === '.codex'
    && segments[index + 1]?.toLowerCase() === 'generated_images'
    && Boolean(segments[index + 2])
  ));
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

function backendGeneratedImagePreviewSource(rawValue) {
  const value = (rawValue || '').toString().trim();
  if (!value.startsWith('/generated-image?')) return '';
  try {
    const url = new URL(value, 'http://localhost');
    if (url.origin !== 'http://localhost' || url.pathname !== '/generated-image') return '';
    if (url.searchParams.getAll('path').length !== 1) return '';
    if ([...url.searchParams.keys()].some((key) => key !== 'path')) return '';
    const path = (url.searchParams.get('path') || '').trim();
    if (!isGeneratedImagePath(path)) return '';
    return `/generated-image?path=${encodeURIComponent(path)}`;
  } catch {
    return '';
  }
}

function trustedImagePreviewSource(rawValue) {
  const value = (rawValue || '').toString().trim();
  if (!value) return '';
  if (value.startsWith('/clipboard/')) return value;
  const generatedPreview = backendGeneratedImagePreviewSource(value);
  if (generatedPreview) return generatedPreview;
  const localPreview = backendLocalImagePreviewSource(value);
  if (localPreview) return localPreview;
  if (SAFE_RASTER_DATA_URL_RE.test(value) || /^https?:\/\//i.test(value)) return value;
  return '';
}

function imagePreviewSource(rawValue) {
  const value = (rawValue || '').toString().trim();
  const trustedPreview = trustedImagePreviewSource(value);
  if (trustedPreview) return trustedPreview;
  if (!value || !IMAGE_PATH_RE.test(value)) return '';
  const localPath = /^file:\/\//i.test(value) ? fileURLToPath(value) : value;
  if (isGeneratedImagePath(localPath)) {
    return `/generated-image?path=${encodeURIComponent(localPath)}`;
  }
  return '';
}

function normalizeMessageText(text) {
  return (text || '').toString().replace(/\r\n/g, '\n');
}

export { basenameFromPath, imagePreviewSource, normalizeMessageText, trustedImagePreviewSource };
