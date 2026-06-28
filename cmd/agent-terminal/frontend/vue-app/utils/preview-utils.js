// @ts-nocheck
/**
 * @typedef {Object} CodeOpenSnippetLine
 * @property {number} [line]
 * @property {string} [text]
 */

/**
 * @typedef {Object} CodeOpenResult
 * @property {boolean} [ok]
 * @property {string} [relative]
 * @property {string} [filePath]
 * @property {string} [previewKind]
 * @property {boolean} [image]
 * @property {string} [plugin]
 * @property {string} [mediaType]
 * @property {string} [previewURL]
 * @property {string} [thumbnailURL]
 * @property {number} [sizeBytes]
 * @property {number} [startLine]
 * @property {number} [endLine]
 * @property {number} [totalLines]
 * @property {string} [language]
 * @property {string | CodeOpenSnippetLine[]} [snippet]
 */

/**
 * @typedef {Object} ImagePreviewState
 * @property {string} src
 * @property {string} fullSrc
 * @property {string} path
 * @property {string} mediaType
 * @property {number} sizeBytes
 */

/**
 * @typedef {Object} TextPreviewState
 * @property {'markdown' | 'text'} previewKind
 * @property {string} path
 * @property {string} filePath
 * @property {string} text
 * @property {string} language
 * @property {number} startLine
 * @property {number} endLine
 * @property {number} totalLines
 * @property {boolean} editable
 */

export const PREVIEW_KIND = Object.freeze({
  MARKDOWN: 'markdown',
  TEXT: 'text',
});

/**
 * @param {CodeOpenResult | null | undefined} codeOpenResult
 * @returns {string}
 */
export function buildSyntheticDiffFromCodeOpen(codeOpenResult) {
  const path = (codeOpenResult?.relative || codeOpenResult?.filePath || '').toString().trim();
  if (!path) return '';
  const snippetRaw = codeOpenResult?.snippet;
  const snippetLines = codeOpenSnippetLines(codeOpenResult);
  if (!Array.isArray(snippetLines) || snippetLines.length === 0) return '';
  const startLineRaw = Number(codeOpenResult?.startLine);
  const fallbackStartLine = Array.isArray(snippetRaw) ? Number(snippetRaw?.[0]?.line) : 0;
  const startLine = Number.isFinite(startLineRaw) && startLineRaw > 0
    ? Math.floor(startLineRaw)
    : (Number.isFinite(fallbackStartLine) && fallbackStartLine > 0 ? Math.floor(fallbackStartLine) : 1);
  const span = Math.max(1, snippetLines.length);
  return [
    `diff --git a/${path} b/${path}`,
    `--- a/${path}`,
    `+++ b/${path}`,
    `@@ -${startLine},${span} +${startLine},${span} @@`,
    ...snippetLines.map((line) => ` ${line}`),
  ].join('\n');
}

/**
 * @param {CodeOpenResult | null | undefined} codeOpenResult
 * @returns {string[]}
 */
export function codeOpenSnippetLines(codeOpenResult) {
  const snippetRaw = codeOpenResult?.snippet;
  return Array.isArray(snippetRaw)
    ? snippetRaw.map((item) => (item?.text || '').toString())
    : ((snippetRaw || '').toString().split('\n'));
}

const DESKTOP_MOJIBAKE_SEQUENCE_RE = /(?:Ã.|Â.|â.|å.|æ.|ç.|é.|ê.|ë.|ï.|ð.|ñ.|ò.|ô.|ö.|û.|ü.|[\u0080-\u009f])/;
const CJK_CHAR_RE = /[\u3400-\u9fff\uf900-\ufaff]/g;
const LATIN1_HIGH_CHAR_RE = /[\u00c0-\u00ff]/g;
const REPLACEMENT_CHAR_RE = /�/g;
const LATIN1_CONTROL_RE = /[\u0080-\u009f]/g;
const NULL_CHAR_RE = /\u0000/g;
const WINDOWS_1252_CHAR_TO_BYTE = new Map([
  ['€', 0x80], ['‚', 0x82], ['ƒ', 0x83], ['„', 0x84], ['…', 0x85], ['†', 0x86], ['‡', 0x87],
  ['ˆ', 0x88], ['‰', 0x89], ['Š', 0x8a], ['‹', 0x8b], ['Œ', 0x8c], ['Ž', 0x8e],
  ['‘', 0x91], ['’', 0x92], ['“', 0x93], ['”', 0x94], ['•', 0x95], ['–', 0x96], ['—', 0x97],
  ['˜', 0x98], ['™', 0x99], ['š', 0x9a], ['›', 0x9b], ['œ', 0x9c], ['ž', 0x9e], ['Ÿ', 0x9f],
]);

function countPreviewRegex(text, regex) {
  const matches = (text || '').toString().match(regex);
  return matches ? matches.length : 0;
}
function scoreDesktopPreviewText(text) {
  const source = (text || '').toString();
  const cjkCount = countPreviewRegex(source, CJK_CHAR_RE);
  const suspiciousCount = countPreviewRegex(source, DESKTOP_MOJIBAKE_SEQUENCE_RE);
  const latinHighCount = countPreviewRegex(source, LATIN1_HIGH_CHAR_RE);
  const replacementCount = countPreviewRegex(source, REPLACEMENT_CHAR_RE);
  const controlCount = countPreviewRegex(source, LATIN1_CONTROL_RE);
  return (cjkCount * 6) - (suspiciousCount * 4) - (replacementCount * 5) - (controlCount * 3) - Math.max(0, latinHighCount - cjkCount);
}

function desktopPreviewCharToByte(char) {
  if (!char) return null;
  const code = char.charCodeAt(0);
  if (code <= 0xff) return code;
  return WINDOWS_1252_CHAR_TO_BYTE.get(char) ?? null;
}

function repairDesktopMarkdownPreviewLine(rawLine) {
  const source = (rawLine || '').toString();
  if (!source) return '';

  let normalized = source.replace(/^\uFEFF/, '');
  const nullCount = countPreviewRegex(normalized, NULL_CHAR_RE);
  if (nullCount > 0 && nullCount * 3 >= normalized.length) {
    normalized = normalized.replace(NULL_CHAR_RE, '');
  }
  if (!normalized || !DESKTOP_MOJIBAKE_SEQUENCE_RE.test(normalized) || typeof TextDecoder !== 'function') {
    return normalized;
  }
  if (countPreviewRegex(normalized, CJK_CHAR_RE) > 0) return normalized;

  const bytes = new Uint8Array(normalized.length);
  for (let index = 0; index < normalized.length; index += 1) {
    const byte = desktopPreviewCharToByte(normalized.charAt(index));
    if (byte === null) return normalized;
    bytes[index] = byte;
  }

  const repaired = new TextDecoder('utf-8', { fatal: false }).decode(bytes).replace(/^\uFEFF/, '');
  if (!repaired || repaired === normalized) return normalized;
  return scoreDesktopPreviewText(repaired) > scoreDesktopPreviewText(normalized) ? repaired : normalized;
}

export function normalizePreviewText(value) {
  return (value || '').toString().replace(/\r\n?/g, '\n');
}

function countPreviewLines(text) {
  const normalized = normalizePreviewText(text);
  if (!normalized) return 0;
  const lineBreaks = normalized.match(/\n/g)?.length || 0;
  return normalized.endsWith('\n') ? lineBreaks : lineBreaks + 1;
}

function resolveTextPreviewKind(codeOpenResult, resolvedPath, language) {
  const previewKind = (codeOpenResult?.previewKind || '').toString().trim().toLowerCase();
  if (previewKind === PREVIEW_KIND.MARKDOWN || previewKind === PREVIEW_KIND.TEXT) return previewKind;
  if (language === PREVIEW_KIND.MARKDOWN || isMarkdownPath(resolvedPath)) return PREVIEW_KIND.MARKDOWN;
  return isTextPreviewPath(resolvedPath) ? PREVIEW_KIND.TEXT : '';
}

function resolveTextPreviewLanguage(path, previewKind, rawLanguage) {
  const language = (rawLanguage || '').toString().trim().toLowerCase();
  if (language) return language === 'text' ? 'plaintext' : language;
  if (previewKind === 'markdown') return 'markdown';
  if (/\.json$/i.test(path)) return 'json';
  if (/\.(yaml|yml)$/i.test(path)) return 'yaml';
  return 'plaintext';
}

function resolvePreviewLineRange(codeOpenResult, text) {
  const startLineRaw = Number(codeOpenResult?.startLine);
  const fallbackStartLine = Array.isArray(codeOpenResult?.snippet)
    ? Number(codeOpenResult?.snippet?.[0]?.line)
    : 0;
  const startLine = Number.isFinite(startLineRaw) && startLineRaw > 0
    ? Math.floor(startLineRaw)
    : (Number.isFinite(fallbackStartLine) && fallbackStartLine > 0 ? Math.floor(fallbackStartLine) : 1);
  const endLineRaw = Number(codeOpenResult?.endLine);
  const fallbackEndLine = startLine + Math.max(0, countPreviewLines(text) - 1);
  const endLine = Number.isFinite(endLineRaw) && endLineRaw >= startLine
    ? Math.floor(endLineRaw)
    : fallbackEndLine;
  const totalLinesRaw = Number(codeOpenResult?.totalLines);
  const totalLines = Number.isFinite(totalLinesRaw) && totalLinesRaw > 0
    ? Math.floor(totalLinesRaw)
    : Math.max(endLine, countPreviewLines(text));
  return { startLine, endLine, totalLines };
}

/**
 * @param {string} path
 * @returns {boolean}
 */
export function isTextPreviewPath(path) {
  return /\.(md|markdown|txt|json|yaml|yml)$/i.test((path || '').toString().trim());
}

/**
 * @param {string} path
 * @returns {boolean}
 */
export function isMarkdownPath(path) {
  return /\.(md|markdown)$/i.test((path || '').toString().trim());
}

/**
 * @param {CodeOpenResult | null | undefined} codeOpenResult
 * @returns {TextPreviewState | null}
 */
export function buildTextPreviewFromCodeOpen(codeOpenResult) {
  if (!codeOpenResult || codeOpenResult.ok !== true) return null;
  const rawLanguage = (codeOpenResult.language || '').toString().trim().toLowerCase();
  const resolvedPath = (codeOpenResult.relative || codeOpenResult.filePath || '').toString().trim();
  const previewKind = resolveTextPreviewKind(codeOpenResult, resolvedPath, rawLanguage);
  if (!previewKind) return null;
  const snippetLines = codeOpenSnippetLines(codeOpenResult).map((line) => repairDesktopMarkdownPreviewLine(line));
  const text = normalizePreviewText(snippetLines.join('\n'));
  if (!text.trim()) return null;

  const filePath = (codeOpenResult.filePath || '').toString().trim();
  const language = resolveTextPreviewLanguage(resolvedPath, previewKind, rawLanguage);
  const { startLine, endLine, totalLines } = resolvePreviewLineRange(codeOpenResult, text);

  return {
    previewKind,
    path: resolvedPath,
    filePath,
    text,
    language,
    startLine,
    endLine,
    totalLines,
    editable: Boolean(filePath),
  };
}

/**
 * @deprecated Use buildTextPreviewFromCodeOpen for all text preview types.
 * This function is kept for backward compatibility and only returns markdown previews.
 * @param {CodeOpenResult | null | undefined} codeOpenResult
 * @returns {TextPreviewState | null}
 */
export function buildMarkdownPreviewFromCodeOpen(codeOpenResult) {
  const preview = buildTextPreviewFromCodeOpen(codeOpenResult);
  if (!preview || preview.previewKind !== PREVIEW_KIND.MARKDOWN) return null;
  return preview;
}

/**
 * @param {string} path
 * @returns {boolean}
 */
export function isPreviewableImagePath(path) {
  const value = (path || '').toString().trim().toLowerCase();
  if (!value) return false;
  return /\.(png|jpg|jpeg|svg)$/.test(value);
}

/**
 * @param {string} path
 * @returns {string}
 */
export function toFilePreviewURL(path) {
  const raw = (path || '').toString().trim();
  if (!raw) return '';
  const lower = raw.toLowerCase();
  if (lower.startsWith('file://') || lower.startsWith('http://') || lower.startsWith('https://') || lower.startsWith('data:image/')) {
    return raw;
  }
  if (/^[a-z]:[\\/]/i.test(raw)) {
    return encodeURI(`file:///${raw.replace(/\\/g, '/')}`);
  }
  return encodeURI(`file://${raw}`);
}

/**
 * @param {CodeOpenResult | null | undefined} codeOpenResult
 * @returns {ImagePreviewState | null}
 */
export function buildImagePreviewFromCodeOpen(codeOpenResult) {
  if (!codeOpenResult || codeOpenResult.ok !== true) return null;
  const mediaType = (codeOpenResult.mediaType || '').toString().trim().toLowerCase();
  const resolvedPath = (codeOpenResult.relative || codeOpenResult.filePath || '').toString().trim();
  const imageByType = mediaType === 'image/png'
    || mediaType === 'image/jpeg'
    || mediaType === 'image/svg+xml';
  const imageByPath = isPreviewableImagePath(resolvedPath);
  if (!codeOpenResult.image && !imageByType && !imageByPath) return null;

  const thumb = (codeOpenResult.thumbnailURL || '').toString().trim();
  const preview = (codeOpenResult.previewURL || '').toString().trim();
  const src = thumb || preview || toFilePreviewURL((codeOpenResult.filePath || '').toString().trim());
  const fullSrc = preview || src;
  if (!src || !fullSrc) return null;

  const size = Number(codeOpenResult.sizeBytes);
  return {
    src,
    fullSrc,
    path: resolvedPath,
    mediaType: mediaType || 'image/*',
    sizeBytes: Number.isFinite(size) && size > 0 ? Math.floor(size) : 0,
  };
}
