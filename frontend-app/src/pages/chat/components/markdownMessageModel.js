const IMAGE_PATH_RE = /\.(?:png|jpe?g|webp|gif|svg)(?:[?#].*)?$/i;

function basenameFromPath(path) {
  const value = (path || '').toString().trim().split(/[?#]/, 1)[0];
  if (!value) return '';
  return value.split(/[\\/]/).filter(Boolean).pop() || value;
}

function fileURLToPath(value) {
  try {
    const url = new URL(value);
    if (url.protocol.toLowerCase() !== 'file:') return '';
    return decodeURIComponent(url.pathname || '');
  } catch {
    return '';
  }
}

function isGeneratedImagePath(value) {
  const path = (value || '').toString().trim();
  if (!path || !IMAGE_PATH_RE.test(path)) return false;
  return /(?:^|[/\\])\.codex[/\\]generated_images[/\\]/i.test(path);
}

function imagePreviewSource(rawValue) {
  const value = (rawValue || '').toString().trim();
  if (!value || !IMAGE_PATH_RE.test(value)) return '';
  if (/^data:image\//i.test(value) || /^https?:\/\//i.test(value)) return value;
  const localPath = /^file:\/\//i.test(value) ? fileURLToPath(value) : value;
  if (isGeneratedImagePath(localPath)) {
    return `/generated-image?path=${encodeURIComponent(localPath)}`;
  }
  if (/^[A-Za-z]:[\\/]/.test(localPath)) {
    return `file:///${localPath.replace(/\\/g, '/')}`;
  }
  if (/^(?:\/|~\/|\.{1,2}\/)/.test(localPath)) {
    return `file://${localPath}`;
  }
  return '';
}

function normalizeMessageText(text) {
  return (text || '').toString().replace(/\r\n/g, '\n');
}

export { basenameFromPath, imagePreviewSource, normalizeMessageText };
