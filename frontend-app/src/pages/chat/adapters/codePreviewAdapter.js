import { trustedImagePreviewSource } from '../markdown/markdownMessageModel.js';

function codePreviewTextValue(value) {
  if (value === null || value === undefined) return '';
  return value.toString();
}

function firstCodePreviewText(values) {
  for (const value of values) {
    const text = codePreviewTextValue(value).trim();
    if (text) return text;
  }
  return '';
}

function codeOpenPathCandidates(result, requestedPath, fallbackRelative) {
  return [result?.relative, result?.filePath, result?.path, fallbackRelative, requestedPath];
}

function codeOpenFilePathCandidates(result, requestedPath) {
  return [result?.filePath, result?.path, requestedPath];
}

function codeOpenPreviewUrlCandidates(result) {
  return [result?.previewURL, result?.previewUrl];
}

function codeOpenThumbnailUrlCandidates(result) {
  return [result?.thumbnailURL, result?.thumbnailUrl];
}

function requireCodePreviewOpenResult(result) {
  if (!result || typeof result !== 'object' || Array.isArray(result)) {
    throw new Error('code preview open result must be an object');
  }
  return result;
}

function normalizeCodeOpenSnippet(snippet) {
  if (typeof snippet === 'string') return snippet;
  if (!Array.isArray(snippet)) return '';
  return snippet.map((line) => {
    if (typeof line === 'string') return line;
    if (line && typeof line === 'object') return codePreviewTextValue(line.text);
    return '';
  }).join('\n');
}

function normalizeCodePreviewText(value) {
  return codePreviewTextValue(value).replace(/\r\n?/g, '\n');
}

function countCodePreviewLines(text) {
  const normalized = normalizeCodePreviewText(text);
  if (!normalized) return 0;
  const lineBreaks = normalized.match(/\n/g)?.length || 0;
  return normalized.endsWith('\n') ? lineBreaks : lineBreaks + 1;
}

function codeOpenDisplayPath(result, fallback) {
  return firstCodePreviewText(codeOpenPathCandidates(result, undefined, fallback));
}

function isCodePreviewMarkdownPath(path) {
  return /\.(md|markdown)$/i.test(codePreviewTextValue(path).trim());
}

function isCodePreviewImagePath(path) {
  return /\.(png|jpe?g|gif|webp|svg|ico)$/i.test(codePreviewTextValue(path).trim());
}

function codePreviewLanguage(result, relative, previewKind) {
  const language = codePreviewTextValue(result?.language).trim().toLowerCase();
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
  requireCodePreviewOpenResult(result);
  const filePath = firstCodePreviewText(codeOpenFilePathCandidates(result, requestedPath));
  if (!filePath) throw new Error('code preview open result requires filePath');
  const relative = firstCodePreviewText(codeOpenPathCandidates(result, requestedPath, fallbackRelative));
  const mediaType = codePreviewTextValue(result?.mediaType).trim().toLowerCase();
  const image = Boolean(result?.image) || mediaType.startsWith('image/') || isCodePreviewImagePath(relative || filePath);
  if (image) return codePreviewImageState(result, filePath, relative, mediaType);
  const content = normalizeCodePreviewText(normalizeCodeOpenSnippet(result?.snippet));
  const explicitKind = codePreviewTextValue(result?.previewKind).trim().toLowerCase();
  const previewKind = explicitKind === 'markdown' || isCodePreviewMarkdownPath(relative) || mediaType === 'text/markdown' ? 'markdown' : 'text';
  const { startLine, endLine, totalLines } = codePreviewLineRange(result, content);
  const previewMode = codePreviewTextValue(result?.previewMode).trim().toLowerCase();
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
    contentVersion: codePreviewTextValue(result?.contentVersion),
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

function codePreviewStateAfterSave(current, result, relative, savedDraft) {
  const filePath = firstCodePreviewText([result?.filePath, current.filePath]);
  const savedContent = normalizeCodePreviewText(savedDraft);
  const draftChangedDuringSave = current.draft !== savedContent;
  const totalLines = Number.isFinite(Number(result?.totalLines))
    ? Math.floor(Number(result.totalLines))
    : countCodePreviewLines(savedContent);
  return {
    ...current,
    saving: false,
    filePath,
    relative,
    content: savedContent,
    editing: current.previewKind === 'markdown' && !draftChangedDuringSave ? false : current.editing,
    totalLines,
    status: draftChangedDuringSave ? `已保存 ${relative}，仍有未保存更改` : `已保存 ${relative}`,
  };
}

function codePreviewImageState(result, filePath, relative, mediaType) {
  const previewUrl = firstCodePreviewText(codeOpenPreviewUrlCandidates(result));
  const thumbnailUrl = firstCodePreviewText(codeOpenThumbnailUrlCandidates(result));
  const previewSrc = trustedImagePreviewSource(previewUrl);
  const thumbnailSrc = trustedImagePreviewSource(thumbnailUrl);
  const imageSrc = thumbnailSrc || previewSrc;
  const imageFullSrc = previewSrc || imageSrc;
  return {
    ...emptyCodePreviewState(),
    open: true,
    filePath,
    relative,
    previewKind: 'image',
    previewMode: codePreviewTextValue(result?.previewMode).trim().toLowerCase() || 'image',
    contentVersion: '',
    language: '',
    editable: false,
    editing: false,
    image: true,
    error: imageSrc ? '' : '图片预览需要后端提供安全预览 URL',
    imageSrc,
    imageFullSrc,
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
  codePreviewStateAfterSave,
  codePreviewStateFromOpenResult,
  countCodePreviewLines,
  emptyCodePreviewState,
};
