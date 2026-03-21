// @ts-nocheck
import { parseUnifiedDiff } from '../services/diff.js';

/**
 * @typedef {Object} DiffLine
 * @property {string} type
 * @property {string} text
 * @property {string | number} [oldNo]
 * @property {string | number} [newNo]
 */

/**
 * @typedef {Object} DiffFile
 * @property {string} filename
 * @property {DiffLine[]} lines
 */

/**
 * @typedef {Object} FocusedDiffSelection
 * @property {string} filename
 * @property {string} diffText
 */

/**
 * @typedef {Object} CrossThreadDiffSelection
 * @property {string} threadId
 * @property {string} path
 */

/**
 * @param {string} rawPath
 * @returns {string}
 */
export function normalizeDiffPath(rawPath) {
  return (rawPath || '')
    .toString()
    .trim()
    .replace(/\\/g, '/')
    .replace(/^\.\/+/, '')
    .replace(/^(a|b)\//, '')
    .toLowerCase();
}

/**
 * @param {string} value
 * @returns {{ value: string, compact: string, hasMultiSpace: boolean, charLen: number }}
 */
export function whitespaceTrace(value) {
  const raw = (value || '').toString();
  return {
    value: raw,
    compact: raw.replace(/\s+/g, ' ').trim(),
    hasMultiSpace: /\s{2,}/.test(raw),
    charLen: raw.length,
  };
}

/**
 * @param {string} path
 * @returns {string}
 */
export function basename(path) {
  const normalized = normalizeDiffPath(path);
  if (!normalized) return '';
  const segments = normalized.split('/').filter(Boolean);
  return segments[segments.length - 1] || '';
}

/**
 * @param {DiffFile[] | null | undefined} files
 * @param {string} targetPath
 * @returns {DiffFile | null}
 */
export function pickDiffFile(files, targetPath) {
  const target = normalizeDiffPath(targetPath);
  const list = /** @type {DiffFile[]} */ (Array.isArray(files) ? files : []);
  if (!target || list.length === 0) return null;
  const targetBase = basename(target);
  /** @type {DiffFile | null} */
  let best = null;
  let bestScore = -1;
  /** @type {DiffFile | null} */
  let basenameMatch = null;
  let basenameMatchCount = 0;
  list.forEach((file) => {
    const filename = (file?.filename || '').toString();
    const normalizedFile = normalizeDiffPath(filename);
    if (!normalizedFile) return;
    let score = -1;
    if (normalizedFile === target) {
      score = 10_000 + normalizedFile.length;
    } else if (normalizedFile.endsWith(`/${target}`)) {
      score = 9_000 + target.length;
    } else if (target.endsWith(`/${normalizedFile}`)) {
      score = 8_000 + normalizedFile.length;
    }
    if (score > bestScore) {
      bestScore = score;
      best = /** @type {DiffFile} */ (file);
      return;
    }
    const fileBase = basename(normalizedFile);
    if (!fileBase || !targetBase || fileBase !== targetBase) {
      return;
    }
    basenameMatchCount += 1;
    if (basenameMatchCount === 1) {
      basenameMatch = /** @type {DiffFile} */ (file);
    }
  });
  if (bestScore >= 0) return best;
  return basenameMatchCount === 1 ? basenameMatch : null;
}

/**
 * @param {DiffFile | null | undefined} file
 * @returns {string}
 */
export function serializeDiffFile(file) {
  if (!file || typeof file !== 'object') return '';
  const filename = (file.filename || '').toString().trim();
  if (!filename) return '';
  const lines = Array.isArray(file.lines) ? file.lines : [];
  const out = [
    `diff --git a/${filename} b/${filename}`,
    `--- a/${filename}`,
    `+++ b/${filename}`,
  ];
  lines.forEach((line) => {
    const type = (line?.type || '').toString();
    const text = (line?.text || '').toString();
    if (type === 'hunk') {
      out.push(text || '@@');
      return;
    }
    if (type === 'add') {
      out.push(`+${text}`);
      return;
    }
    if (type === 'del') {
      out.push(`-${text}`);
      return;
    }
    if (type === 'ctx') {
      out.push(` ${text}`);
      return;
    }
    if (type === 'meta') {
      out.push(text);
    }
  });
  return out.join('\n');
}

/**
 * @param {string} rawDiffText
 * @param {string} targetPath
 * @returns {FocusedDiffSelection | null}
 */
export function buildFocusedDiffSelection(rawDiffText, targetPath) {
  const text = (rawDiffText || '').toString().trim();
  if (!text) return null;
  const files = /** @type {DiffFile[]} */ (parseUnifiedDiff(text));
  const target = /** @type {DiffFile | null} */ (pickDiffFile(files, targetPath));
  if (!target) return null;
  const focusedText = serializeDiffFile(target);
  if (!focusedText) return null;
  return {
    filename: (target.filename || '').toString().trim(),
    diffText: focusedText,
  };
}

/**
 * Cross-thread diff lookup is intentionally removed.
 * Diff restoration must stay scoped to the currently selected thread / agent ID.
 */
