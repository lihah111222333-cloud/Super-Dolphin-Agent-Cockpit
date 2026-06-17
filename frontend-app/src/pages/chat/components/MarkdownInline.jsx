import React from 'react';
import { MarkdownCitationLinkChip, MarkdownDirectiveChip } from './MarkdownDirectiveChip.jsx';
import { MarkdownImagePreview } from './MarkdownImagePreview.jsx';
import { CODEX_DIRECTIVE_RE, citationMarkdownLinkChipModel, directiveChipModel } from './markdownDirectiveModel.js';
import { basenameFromPath, imagePreviewSource } from './markdownMessageModel.js';

const EMPTY_MARKDOWN_ACTIONS = Object.freeze({});
const INLINE_IMAGE_PATH_RE = /(?:file:\/\/\/?[^\s`<>()"']+|~?\/(?!\/)[^\s`<>()"']+|\.{1,2}\/[^\s`<>()"']+|[A-Za-z]:[\\/][^\s`<>()"']+)\.(?:png|jpe?g|webp|gif|svg)(?:[?#][^\s`<>()"']*)?/gi;

function parsedMarkdownUrl(value) {
  try {
    return new URL(value, window.location?.origin || 'http://localhost');
  }
  catch {
    return null;
  }
}

function markdownImageUrl(value, protocol) {
  const allowed = new Set(['http:', 'https:', 'data:', 'file:']);
  return allowed.has(protocol) ? value : '';
}

function markdownLinkUrl(parsed, protocol) {
  const allowed = new Set(['http:', 'https:', 'mailto:', 'file:']);
  return allowed.has(protocol) ? parsed.href : '';
}

function isExternalMarkdownHref(value) {
  return /^[a-z][a-z0-9+.-]*:/i.test(value) && !/^file:/i.test(value);
}

function isLikelyLocalMarkdownPath(value) {
  if (!value || value.startsWith('#') || value.startsWith('//') || isExternalMarkdownHref(value)) return false;
  if (/^file:/i.test(value)) return true;
  if (/^[A-Za-z]:[\\/]/.test(value)) return true;
  if (/^~?\//.test(value) || /^\.{1,2}[\\/]/.test(value)) return true;
  return /[\\/]/.test(value) || /\.[A-Za-z0-9]{1,12}(?:$|[#?])/.test(value);
}

function fileUrlToLocalPath(value) {
  try {
    const parsed = new URL(value);
    if (parsed.protocol.toLowerCase() !== 'file:') return '';
    const path = decodeURIComponent(parsed.pathname || '');
    if (/^\/[A-Za-z]:[\\/]/.test(path)) return path.slice(1);
    return path;
  }
  catch {
    return '';
  }
}

function decodeMarkdownFilePath(value) {
  try {
    return decodeURIComponent(value);
  }
  catch {
    return '';
  }
}

function markdownFileLinkRef(rawUrl) {
  const value = (rawUrl || '').toString().trim();
  if (!isLikelyLocalMarkdownPath(value)) return null;
  const lineMatch = value.match(/#L(\d+)/i);
  const cleanValue = value.split(/[?#]/, 1)[0];
  const path = /^file:/i.test(cleanValue) ? fileUrlToLocalPath(cleanValue) : decodeMarkdownFilePath(cleanValue);
  if (!path) return null;
  return {
    path,
    line: lineMatch ? Number.parseInt(lineMatch[1], 10) : 1,
    column: 0,
  };
}

function safeMarkdownUrl(rawUrl, options = {}) {
  const value = (rawUrl || '').toString().trim();
  if (!value) return '';
  const localSrc = options.image ? imagePreviewSource(value) : '';
  if (localSrc) return localSrc;
  const parsed = parsedMarkdownUrl(value);
  if (!parsed) return '';
  const protocol = parsed.protocol.toLowerCase();
  if (options.image) return markdownImageUrl(value, protocol);
  return markdownLinkUrl(parsed, protocol);
}

function renderImagePreview(rawSource, altText, key) {
  const src = imagePreviewSource(rawSource);
  if (!src) return null;
  const label = (altText || '').toString().trim() || basenameFromPath(rawSource) || '\u56fe\u7247\u9884\u89c8';
  return <MarkdownImagePreview key={key} src={src} label={label} />;
}

function trimTrailingImagePathPunctuation(value) {
  let path = (value || '').toString();
  let suffix = '';
  while (/[.,;:!?\uFF0C\u3002\uFF1B\uFF1A\uFF01\uFF1F\u3001]$/.test(path)) {
    suffix = `${path.at(-1)}${suffix}`;
    path = path.slice(0, -1);
  }
  return { path, suffix };
}

function renderPlainTextWithImagePreviews(text, keyPrefix) {
  const source = (text || '').toString();
  const parts = [];
  let lastIndex = 0;
  let matchIndex = 0;
  for (const match of source.matchAll(INLINE_IMAGE_PATH_RE)) {
    const token = match[0];
    const start = match.index ?? 0;
    const { path, suffix } = trimTrailingImagePathPunctuation(token);
    const image = renderImagePreview(path, basenameFromPath(path), `${keyPrefix}-image-${matchIndex}`);
    if (!image) continue;
    if (start > lastIndex) parts.push(source.slice(lastIndex, start));
    parts.push(image);
    if (suffix) parts.push(suffix);
    lastIndex = start + token.length;
    matchIndex += 1;
  }
  if (lastIndex < source.length) parts.push(source.slice(lastIndex));
  return parts.length > 0 ? parts : [source];
}

function inlineMarkdownPattern() {
  const tokenPattern = `${CODEX_DIRECTIVE_RE.source}|!\\[[^\\]]*]\\([^)]+\\)|\\[[^\\]]+]\\([^)]+\\)|\`[^\`]+\`|\\*\\*[^*]+\\*\\*|__[^_]+__|~~[^~]+~~|\\*[^*]+\\*|_[^_]+_`;
  return new RegExp(`(${INLINE_IMAGE_PATH_RE.source})|(${tokenPattern})`, 'gi');
}

function appendInlineTextSegment(parts, source, start, end, keyPrefix) {
  if (end <= start) return;
  parts.push(...renderPlainTextWithImagePreviews(source.slice(start, end), keyPrefix));
}

function renderMarkdownImageToken(token, key) {
  const parsed = token.match(/^!\[([^\]]*)]\(([^)]+)\)$/);
  const src = safeMarkdownUrl(parsed?.[2], { image: true });
  if (!src) return token;
  return <MarkdownImagePreview key={key} src={src} label={parsed?.[1] || basenameFromPath(parsed?.[2])} />;
}

function renderDirectiveToken(token, key, actions = {}) {
  const chip = directiveChipModel(token);
  if (!chip) return null;
  return <MarkdownDirectiveChip key={key} chip={chip} actions={actions} />;
}

function renderMarkdownLinkToken(token, key, actions = {}) {
  const citation = citationMarkdownLinkChipModel(token);
  if (citation) return <MarkdownCitationLinkChip key={key} chip={citation} actions={actions} />;
  const parsed = token.match(/^\[([^\]]+)]\(([^)]+)\)$/);
  const fileRef = markdownFileLinkRef(parsed?.[2]);
  const openFile = actions?.onOpenPath || actions?.onFileRef;
  if (fileRef && openFile) {
    const label = parsed?.[1] || fileRef.path;
    const handleFileClick = (event) => {
      event.preventDefault();
      openFile({ ...fileRef, raw: label });
    };
    return (
      <button
        key={key}
        type="button"
        className="chat-md-file-ref chat-md-file-link"
        aria-label={`\u6253\u5f00\u6587\u4ef6 ${label}`}
        title={fileRef.path}
        onClick={handleFileClick}
      >
        {label}
      </button>
    );
  }
  if (fileRef) return parsed?.[1] || fileRef.path;
  const href = safeMarkdownUrl(parsed?.[2]);
  if (!href) return parsed?.[1] || token;
  const handleClick = (event) => {
    event.preventDefault();
    if (window.wails?.Browser?.OpenURL) {
      window.wails.Browser.OpenURL(href);
    } else {
      window.open(href, '_blank', 'noreferrer');
    }
  };
  return <a key={key} href={href} onClick={handleClick} rel="noreferrer">{parsed?.[1]}</a>;
}

function renderInlineCodeToken(token, key) {
  const codeText = token.slice(1, -1).trim();
  const image = renderImagePreview(codeText, basenameFromPath(codeText), key);
  return image || <code key={key}>{token.slice(1, -1)}</code>;
}

function renderStyledInlineToken(token, key) {
  if (token.startsWith('~~')) return <del key={key}>{token.slice(2, -2)}</del>;
  if (token.startsWith('*') && !token.startsWith('**')) return <em key={key}>{token.slice(1, -1)}</em>;
  if (token.startsWith('_') && !token.startsWith('__')) return <em key={key}>{token.slice(1, -1)}</em>;
  return <strong key={key}>{token.slice(2, -2)}</strong>;
}

function renderInlineMarkdownToken(token, key, actions = {}) {
  const inlineImage = renderImagePreview(token, basenameFromPath(token), key);
  if (inlineImage) return inlineImage;
  const directive = renderDirectiveToken(token, key, actions);
  if (directive) return directive;
  if (token.startsWith('![')) return renderMarkdownImageToken(token, key);
  if (token.startsWith('[')) return renderMarkdownLinkToken(token, key, actions);
  if (token.startsWith('`')) return renderInlineCodeToken(token, key);
  return renderStyledInlineToken(token, key);
}

function renderInlineMarkdown(text, keyPrefix, actions = {}) {
  const source = (text || '').toString();
  const parts = [];
  let lastIndex = 0;
  let matchIndex = 0;
  for (const match of source.matchAll(inlineMarkdownPattern())) {
    appendInlineTextSegment(parts, source, lastIndex, match.index, `${keyPrefix}-text-${matchIndex}`);
    const token = match[0];
    parts.push(renderInlineMarkdownToken(token, `${keyPrefix}-inline-${matchIndex}`, actions));
    lastIndex = match.index + token.length;
    matchIndex += 1;
  }
  appendInlineTextSegment(parts, source, lastIndex, source.length, `${keyPrefix}-text-tail`);
  return parts.length > 0 ? parts : source;
}

function InlineMarkdown({ text, inlineKey, actions = EMPTY_MARKDOWN_ACTIONS }) {
  const nodes = renderInlineMarkdown(text, inlineKey, actions);
  return <>{nodes}</>;
}

export { EMPTY_MARKDOWN_ACTIONS, InlineMarkdown };
