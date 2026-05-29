import {
  deriveSkillNameFromPath,
  renderAutomationDirectiveCard,
  renderCodeCommentDirectiveCard,
  renderTaskStubCard,
} from './assistant-markdown-codex-ui.js';
export { deriveSkillNameFromPath, resolveCodexLinkMeta } from './assistant-markdown-codex-ui.js';
const HIDDEN_CITE_RE = /\uE200cite\uE202[^\uE201]+\uE201/g;
const EXACT_AT_PATH_RE = /^@[A-Za-z0-9][\w.-]*[/][\w./-]*$/;
const EXACT_VAR_RE = /^\$(?:\[[^\]\n]+\]|[A-Za-z][\w-]*)$/;
const AT_PATH_RE = /(^|[\s(（\["'，。！？、\-])(@[A-Za-z0-9][\w.-]*[/][\w./-]*)(?=$|[\s).，。！？、:：;；\]"'\-])/g;
const VAR_REF_RE = /(^|[\s(（\["'，。！？、\-])(\$(?:\[[^\]\n]+\]|[A-Za-z][\w-]*))(?=$|[\s).，。！？、:：;；\]"'\-])/g;
const PROTECTED_MARKDOWN_RE = /```[\s\S]*?```|`[^`\n]+`/g;
const BLOCKQUOTE_CALLOUT_RE = /^\s*<p>\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\](?:<br>\s*)?/i;
const TABLE_CELL_ALIGN_RE = /<(th|td)([^>]*)\sstyle="text-align:(left|center|right);?"([^>]*)>/gi;
const TASK_LIST_ITEM_RE = /<li([^>]*)>\s*\[( |x|X)\]\s*([\s\S]*?)<\/li>/gi;
const DIRECTIVE_RE = new RegExp(
  String.raw`(^|[\s(??["'?????????-]):(codex-file-citation|codex-terminal-citation|codex-image-citation|task-stub|automation-update|code-comment)(?:\[([^\]\n]*)\])?(?:\{([^{}\n]*)\})?`,
  'g',
);
const DIRECTIVE_ATTR_RE = /([A-Za-z_][\w-]*)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'{}]+))/g;
const DIRECTIVE_PLACEHOLDER_RE = /§§CDX:([^§]+)§§/g;
const TOP_LEVEL_ORDERED_RE = /^\d+\.\s+/;
const TOP_LEVEL_UNORDERED_RE = /^[-*+]\s+/;
const CALLOUT_TITLES = {
  note: 'Note',
  tip: 'Tip',
  important: 'Important',
  warning: 'Warning',
  caution: 'Caution',
};

function escapeHtml(value) {
  return (value || '')
    .toString()
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

function parseDirectiveAttrs(rawAttrs) {
  const attrs = {};
  const source = (rawAttrs || '').toString();
  for (const match of source.matchAll(DIRECTIVE_ATTR_RE)) {
    const key = (match[1] || '').toString().trim();
    if (!key) continue;
    attrs[key] = (match[2] ?? match[3] ?? match[4] ?? '').toString();
  }
  return attrs;
}

function encodeDirectivePayload(payload) {
  return `§§CDX:${encodeURIComponent(JSON.stringify(payload))}§§`;
}

function decodeDirectivePayload(rawPayload) {
  try {
    return JSON.parse(decodeURIComponent((rawPayload || '').toString()));
  } catch {
    return null;
  }
}

function toPositiveInt(value) {
  const parsed = Number.parseInt((value || '').toString(), 10);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : 0;
}

function basename(path) {
  const value = (path || '').toString().trim();
  return value.split(/[\\/]/).filter(Boolean).pop() || value;
}

function formatLineRange(startLine, endLine) {
  const start = toPositiveInt(startLine);
  const end = toPositiveInt(endLine);
  if (start > 0 && end > 0 && end !== start) return `lines ${start}-${end}`;
  if (start > 0) return `line ${start}`;
  if (end > 0) return `line ${end}`;
  return '';
}

function renderCitationBadge({
  className,
  title,
  text,
  icon,
  meta = '',
  interactive = false,
  dataAttrs = {},
}) {
  const attrs = Object.entries(dataAttrs)
    .filter(([, value]) => (value || '').toString().trim())
    .map(([key, value]) => `${key}="${escapeHtml(value)}"`)
    .join(' ');
  const tag = interactive ? 'button' : 'span';
  const typeAttr = interactive ? ' type="button"' : '';
  const attrText = attrs ? ` ${attrs}` : '';
  const metaHtml = (meta || '').toString().trim()
    ? `<span class="chat-md-citation__meta">${escapeHtml(meta)}</span>`
    : '';
  return `<${tag}${typeAttr} class="chat-md-citation ${className}" title="${escapeHtml(title || text)}"${attrText}><span class="chat-md-citation__icon" aria-hidden="true">${escapeHtml(icon || '•')}</span><span class="chat-md-citation__body"><span class="chat-md-citation__label">${escapeHtml(text)}</span>${metaHtml}</span></${tag}>`;
}

function renderFileCitation(payload) {
  const attrs = payload?.attrs || {};
  const path = (attrs.path || payload?.label || '').toString().trim();
  if (!path) return payload?.label || '';
  const startLine = toPositiveInt(attrs.line_range_start);
  const endLine = toPositiveInt(attrs.line_range_end) || startLine;
  const location = formatLineRange(startLine, endLine);
  const text = (payload?.label || '').toString().trim() || `${basename(path)}${location ? ` (${location})` : ''}`;
  const title = location ? `${path} (${location})` : path;
  const line = startLine || endLine || 1;
  return `<code class="chat-md-inline-code chat-md-file-ref is-file-ref chat-md-file-citation" data-file-path="${escapeHtml(path)}" data-file-line="${line}" data-file-column="0" title="${escapeHtml(title)}">${escapeHtml(text)}</code>`;
}

function renderTerminalCitation(payload) {
  const attrs = payload?.attrs || {};
  const chunkId = (attrs.terminal_chunk_id || '').toString().trim();
  const location = formatLineRange(attrs.line_range_start, attrs.line_range_end);
  const text = (payload?.label || '').toString().trim() || 'Terminal output';
  const title = chunkId ? `Terminal chunk ${chunkId}${location ? ` (${location})` : ''}` : text;
  return renderCitationBadge({
    className: 'chat-md-terminal-citation',
    title,
    text,
    icon: '⌘',
    meta: location,
    interactive: true,
    dataAttrs: {
      'data-citation-kind': 'terminal',
      'data-terminal-chunk-id': chunkId,
      'data-line-start': attrs.line_range_start,
      'data-line-end': attrs.line_range_end,
    },
  });
}

function renderImageCitation(payload) {
  const attrs = payload?.attrs || {};
  const assetPointer = (attrs.asset_pointer || '').toString().trim();
  const text = (payload?.label || '').toString().trim() || 'Image citation';
  return renderCitationBadge({
    className: 'chat-md-image-citation',
    title: assetPointer || text,
    text,
    icon: 'IMG',
    meta: assetPointer,
    interactive: true,
    dataAttrs: {
      'data-citation-kind': 'image',
      'data-asset-pointer': assetPointer,
    },
  });
}


function replaceDirectivePlaceholders(rawHtml) {
  return (rawHtml || '').replace(DIRECTIVE_PLACEHOLDER_RE, (_, encoded) => {
    const payload = decodeDirectivePayload(encoded);
    if (!payload || !payload.name) return '';
    switch (payload.name) {
      case 'codex-file-citation':
        return renderFileCitation(payload);
      case 'codex-terminal-citation':
        return renderTerminalCitation(payload);
      case 'codex-image-citation':
        return renderImageCitation(payload);
      case 'task-stub':
        return renderTaskStubCard(payload);
      case 'automation-update':
        return renderAutomationDirectiveCard(payload);
      case 'code-comment':
        return renderCodeCommentDirectiveCard(payload);

      default:
        return payload.label || '';
    }
  });
}

function replaceCodexDirectives(rawText) {
  return (rawText || '').replace(DIRECTIVE_RE, (_, prefix, name, label, rawAttrs) => `${prefix}${encodeDirectivePayload({
    name,
    label: (label || '').toString(),
    attrs: parseDirectiveAttrs(rawAttrs),
  })}`);
}

function mergeAdjacentLists(text) {
  const lines = (text || '').toString().split('\n');
  for (let index = 1; index < lines.length; index += 1) {
    if (!TOP_LEVEL_UNORDERED_RE.test(lines[index]) || !TOP_LEVEL_ORDERED_RE.test((lines[index - 1] || '').trim())) continue;
    let end = index;
    while (end < lines.length && (TOP_LEVEL_UNORDERED_RE.test(lines[end]) || /^\s{2,}\S/.test(lines[end]) || lines[end] === '')) end += 1;
    const orderedText = (lines[index - 1] || '').replace(TOP_LEVEL_ORDERED_RE, '').trimEnd();
    const unorderedCount = lines.slice(index, end).filter((line) => TOP_LEVEL_UNORDERED_RE.test(line)).length;
    if (unorderedCount < 2 && !orderedText.endsWith(':')) continue;
    for (let cursor = index; cursor < end; cursor += 1) if (lines[cursor]) lines[cursor] = `   ${lines[cursor]}`;
    index = end - 1;
  }
  return lines.join('\n');
}

function encodeVisibleEscape(char) {
  const value = (char || '').toString();
  return `§§BSE:${value ? value.codePointAt(0).toString(16) : ''}§§`;
}


function protectVisibleBackslashes(text) {
  return (text || '').split('\n').map((line) => {
    let next = line.replace(/\\\\([\\`*_{}\[\]()#+\-.!>])/g, (_, char) => encodeVisibleEscape(char));
    const protocolIndex = next.search(/[A-Za-z]:\\/);
    if (protocolIndex >= 0) next = `${next.slice(0, protocolIndex)}${next.slice(protocolIndex).replace(/\\([\\`*_{}\[\]()#+\-.!>])/g, (_, char) => encodeVisibleEscape(char))}`;
    return next;
  }).join('\n');
}
function restoreVisibleBackslashes(text) {
  return (text || '').replace(/§§BSE:([^§]+)§§/g, (_, encoded) => `&#92;${escapeHtml(String.fromCodePoint(Number.parseInt(encoded || '0', 16) || 0))}`);
}





function transformPlainSegment(segment) {
  const source = (segment || '').toString();
  if (!source) return '';
  const normalized = protectVisibleBackslashes(mergeAdjacentLists(source.replace(HIDDEN_CITE_RE, '')));
  return replaceCodexDirectives(normalized)
    .replace(AT_PATH_RE, (_, prefix, value) => `${prefix}\`${value}\``)
    .replace(VAR_REF_RE, (_, prefix, value) => `${prefix}\`${value}\``);
}


function decorateTableHtml(rawHtml) {
  return (rawHtml || '')
    .replace(/<table>/g, '<div class="chat-md-table-wrap"><table class="chat-md-table">')
    .replace(/<\/table>/g, '</table></div>')
    .replace(TABLE_CELL_ALIGN_RE, (_, tag, before, align, after) => `<${tag}${before} data-align="${align}"${after}>`);
}

function decorateBlockquote(fullMatch, innerHtml) {
  const calloutMatch = (innerHtml || '').match(BLOCKQUOTE_CALLOUT_RE);
  if (!calloutMatch) return fullMatch;
  const kind = (calloutMatch[1] || '').toLowerCase();
  const title = CALLOUT_TITLES[kind] || kind;
  let bodyHtml = (innerHtml || '').replace(BLOCKQUOTE_CALLOUT_RE, '<p>');
  bodyHtml = bodyHtml.replace(/^<p>\s*<\/p>\s*/i, '');
  return `<div class="chat-md-callout chat-md-callout-${kind}"><div class="chat-md-callout-title">${title}</div><div class="chat-md-callout-body">${bodyHtml}</div></div>`;
}

function appendClassAttr(rawAttrs, className) {
  const attrs = (rawAttrs || '').toString();
  if (/\sclass="[^"]*"/i.test(attrs)) {
    return attrs.replace(/\sclass="([^"]*)"/i, (_, classes) => ` class="${classes} ${className}"`);
  }
  return `${attrs} class="${className}"`;
}

function decorateTaskListHtml(rawHtml) {
  return (rawHtml || '').replace(TASK_LIST_ITEM_RE, (_, rawAttrs, marker, innerHtml) => {
    const checked = /x/i.test((marker || '').toString());
    const attrs = appendClassAttr(rawAttrs, 'chat-md-task-item');
    const boxClass = checked ? 'chat-md-task-box is-checked' : 'chat-md-task-box';
    return `<li${attrs}><span class="${boxClass}" aria-hidden="true"></span><span class="chat-md-task-content">${innerHtml}</span></li>`;
  });
}

export function isCodexInlineLiteral(rawText) {
  const text = (rawText || '').toString().trim();
  if (!text) return false;
  return EXACT_AT_PATH_RE.test(text) || EXACT_VAR_RE.test(text);
}

export function preprocessCodexMarkdown(rawText) {
  const source = (rawText || '').toString();
  if (!source) return '';

  let output = '';
  let lastIndex = 0;
  for (const match of source.matchAll(PROTECTED_MARKDOWN_RE)) {
    const index = Number.isFinite(match.index) ? match.index : 0;
    const value = (match[0] || '').toString();
    output += transformPlainSegment(source.slice(lastIndex, index));
    output += value;
    lastIndex = index + value.length;
  }
  output += transformPlainSegment(source.slice(lastIndex));
  return output;
}

export function postprocessCodexHtml(rawHtml) {
  const source = (rawHtml || '').toString();
  if (!source) return '';

  let html = decorateTableHtml(source);
  html = html.replace(/<hr\s*\/?>/g, '<hr class="chat-md-divider">');
  html = html.replace(/<blockquote>\s*([\s\S]*?)<\/blockquote>/gi, (fullMatch, innerHtml) => decorateBlockquote(fullMatch, innerHtml));
  html = decorateTaskListHtml(html);
  html = replaceDirectivePlaceholders(html);
  return restoreVisibleBackslashes(html);
}
