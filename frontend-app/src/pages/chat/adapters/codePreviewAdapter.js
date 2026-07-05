function normalizeCodeOpenSnippet(snippet) {
  if (typeof snippet === 'string') return snippet;
  if (!Array.isArray(snippet)) return '';
  return snippet.map((line) => {
    if (typeof line === 'string') return line;
    if (line && typeof line === 'object') return (line.text ?? '').toString();
    return '';
  }).join('\n');
}

function normalizeCodePreviewText(value) {
  return (value || '').toString().replace(/\r\n?/g, '\n');
}

function countCodePreviewLines(text) {
  const normalized = normalizeCodePreviewText(text);
  if (!normalized) return 0;
  const lineBreaks = normalized.match(/\n/g)?.length || 0;
  return normalized.endsWith('\n') ? lineBreaks : lineBreaks + 1;
}

function codeOpenDisplayPath(result, fallback = '') {
  return (result?.relative || result?.filePath || result?.path || fallback || '').toString().trim();
}

function isCodePreviewMarkdownPath(path) {
  return /\.(md|markdown)$/i.test((path || '').toString().trim());
}

function isCodePreviewImagePath(path) {
  return /\.(png|jpe?g|gif|webp|svg|ico)$/i.test((path || '').toString().trim());
}

function codePreviewFileUrl(path) {
  const raw = (path || '').toString().trim();
  if (!raw) return '';
  if (/^(?:file|https?):\/\//i.test(raw) || /^data:image\//i.test(raw)) return raw;
  if (/^[A-Za-z]:[\\/]/.test(raw)) return `file:///${raw.replace(/\\/g, '/')}`;
  return `file://${raw}`;
}

function codePreviewLanguage(result, relative, previewKind) {
  const language = (result?.language || '').toString().trim().toLowerCase();
  if (language) return language === 'text' ? 'plaintext' : language;
  if (previewKind === 'markdown') return 'markdown';
  if (/\.json$/i.test(relative)) return 'json';
  if (/\.(ya?ml)$/i.test(relative)) return 'yaml';
  return 'plaintext';
}

function codePreviewLineRange(result, content) {
  const startRaw = Number(result?.startLine);
  const startLine = Number.isFinite(startRaw) && startRaw > 0 ? Math.floor(startRaw) : 1;
  const endRaw = Number(result?.endLine);
  const fallbackEnd = startLine + Math.max(0, countCodePreviewLines(content) - 1);
  const endLine = Number.isFinite(endRaw) && endRaw >= startLine ? Math.floor(endRaw) : fallbackEnd;
  const totalRaw = Number(result?.totalLines);
  const totalLines = Number.isFinite(totalRaw) && totalRaw > 0 ? Math.floor(totalRaw) : Math.max(endLine, countCodePreviewLines(content));
  return { startLine, endLine, totalLines };
}

function codePreviewIntegerField(value) {
  const parsed = Number(value);
  if (!Number.isInteger(parsed)) return null;
  return parsed;
}

function isFullCodePreview(result, content) {
  const startLine = codePreviewIntegerField(result?.startLine);
  const endLine = codePreviewIntegerField(result?.endLine);
  const totalLines = codePreviewIntegerField(result?.totalLines);
  if (startLine !== 1 || endLine === null || totalLines === null || totalLines < 1) return false;
  return endLine === totalLines && countCodePreviewLines(content) === totalLines;
}

function codePreviewStateFromOpenResult(result, requestedPath, fallbackRelative = '') {
  const filePath = (result?.filePath || result?.path || requestedPath || '').toString();
  const relative = codeOpenDisplayPath(result, fallbackRelative || requestedPath);
  const mediaType = (result?.mediaType || '').toString().trim().toLowerCase();
  const image = Boolean(result?.image) || mediaType.startsWith('image/') || isCodePreviewImagePath(relative || filePath);
  if (image) return codePreviewImageState(result, filePath, relative, mediaType);
  const content = normalizeCodePreviewText(normalizeCodeOpenSnippet(result?.snippet));
  const explicitKind = (result?.previewKind || '').toString().trim().toLowerCase();
  const previewKind = explicitKind === 'markdown' || isCodePreviewMarkdownPath(relative) || mediaType === 'text/markdown' ? 'markdown' : 'text';
  const { startLine, endLine, totalLines } = codePreviewLineRange(result, content);
  const previewMode = (result?.previewMode || '').toString().trim().toLowerCase();
  const editable = previewMode === 'full' && Boolean(filePath) && isFullCodePreview(result, content);
  return {
    ...emptyCodePreviewState(),
    open: true,
    filePath,
    relative,
    content,
    draft: content,
    previewKind,
    previewMode,
    contentVersion: (result?.contentVersion || '').toString(),
    language: codePreviewLanguage(result, relative, previewKind),
    editable,
    editing: editable && previewKind !== 'markdown',
    startLine,
    endLine,
    rangeStartLine: Number.isFinite(Number(result?.rangeStartLine)) ? Math.floor(Number(result.rangeStartLine)) : startLine,
    rangeEndLine: Number.isFinite(Number(result?.rangeEndLine)) ? Math.floor(Number(result.rangeEndLine)) : endLine,
    totalLines,
    sizeBytes: Number.isFinite(Number(result?.sizeBytes)) ? Math.floor(Number(result.sizeBytes)) : 0,
  };
}

function codePreviewImageState(result, filePath, relative, mediaType) {
  const previewUrl = (result?.previewURL || result?.previewUrl || '').toString().trim();
  const thumbnailUrl = (result?.thumbnailURL || result?.thumbnailUrl || '').toString().trim();
  const imageSrc = thumbnailUrl || previewUrl || codePreviewFileUrl(filePath);
  return {
    ...emptyCodePreviewState(),
    open: true,
    filePath,
    relative,
    previewKind: 'image',
    previewMode: (result?.previewMode || 'image').toString().trim().toLowerCase(),
    contentVersion: '',
    language: '',
    editable: false,
    editing: false,
    image: true,
    imageSrc,
    imageFullSrc: previewUrl || imageSrc,
    mediaType: mediaType || 'image/*',
    sizeBytes: Number.isFinite(Number(result?.sizeBytes)) ? Math.floor(Number(result.sizeBytes)) : 0,
  };
}

function emptyCodePreviewState() {
  return {
    open: false,
    loading: false,
    saving: false,
    filePath: '',
    relative: '',
    content: '',
    draft: '',
    error: '',
    status: '',
    previewKind: 'text',
    previewMode: '',
    contentVersion: '',
    language: 'plaintext',
    editable: false,
    editing: true,
    image: false,
    imageSrc: '',
    imageFullSrc: '',
    mediaType: '',
    sizeBytes: 0,
    startLine: 0,
    endLine: 0,
    rangeStartLine: 0,
    rangeEndLine: 0,
    totalLines: 0,
  };
}

export {
  codeOpenDisplayPath,
  codePreviewStateFromOpenResult,
  countCodePreviewLines,
  emptyCodePreviewState,
};
