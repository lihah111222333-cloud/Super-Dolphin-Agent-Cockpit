import React, { useMemo } from 'react';
import ReactMarkdown, { defaultUrlTransform } from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { MarkdownCitationLinkChip, MarkdownDirectiveChip } from './MarkdownDirectiveChip.jsx';
import { MarkdownImagePreview } from './MarkdownImagePreview.jsx';
import { MermaidDiagram } from './MermaidDiagram.jsx';
import { CODEX_DIRECTIVE_RE, citationMarkdownLinkChipModel, directiveChipModel } from './markdownDirectiveModel.js';
import { isMermaidLanguage, isMermaidSource } from './markdownMermaidModel.js';
import { basenameFromPath, imagePreviewSource, normalizeMessageText } from './markdownMessageModel.js';

const EMPTY_MARKDOWN_ACTIONS = Object.freeze({});
const MARKDOWN_REMARK_PLUGINS = [remarkGfm];
const DIRECTIVE_HREF_PREFIX = 'codex-directive:';
const PLAIN_TEXT_MARKDOWN_TOKEN_RE = /[#>*_[\]()`|~!]/;
const SAFE_MARKDOWN_RASTER_DATA_URL_RE = /^data:image\/(?:png|jpe?g|webp|gif|bmp);base64,[a-z0-9+/=\s]+$/i;

const CODE_FENCE_LANGUAGE_PREFIXES = Object.freeze([
  'mermaid',
  'javascript',
  'typescript',
  'powershell',
  'plaintext',
  'markdown',
  'dockerfile',
  'makefile',
  'terminal',
  'console',
  'python',
  'jsonc',
  'json',
  'bash',
  'shell',
  'text',
  'diff',
  'patch',
  'yaml',
  'toml',
  'html',
  'css',
  'tsx',
  'jsx',
  'yml',
  'zsh',
  'sh',
  'txt',
  'sql',
  'log',
  'xml',
  'env',
  'ini',
  'ps1',
  'php',
  'cpp',
  'c++',
  'rust',
  'ruby',
  'go',
  'py',
  'rs',
  'rb',
  'c',
  'md',
].sort((left, right) => right.length - left.length));

const INLINE_IMAGE_PATH_RE = /(?:file:\/\/\/?[^\s`<>()"']+|~?\/(?!\/)[^\s`<>()"']+|\.{1,2}\/[^\s`<>()"']+|[A-Za-z]:[\\/][^\s`<>()"']+)\.(?:png|jpe?g|webp|gif|svg)(?:[?#][^\s`<>()"']*)?/gi;

function CodePreviewMarkdown({ content }) {
  return <MarkdownRenderer text={content} />;
}

function parsedMarkdownUrl(value) {
  try {
    return new URL(value, window.location?.origin || 'http://localhost');
  }
  catch {
    return null;
  }
}

function markdownImageUrl(value, protocol) {
  if (protocol === 'data:') return SAFE_MARKDOWN_RASTER_DATA_URL_RE.test(value) ? value : '';
  const allowed = new Set(['http:', 'https:']);
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

function productMarkdownUrl(rawUrl, options = {}) {
  const value = (rawUrl || '').toString().trim();
  if (!value) return '';
  if (options.image) return safeMarkdownUrl(value, { image: true });
  if (value.startsWith(DIRECTIVE_HREF_PREFIX)) return value;
  if (/^(?:agent|app):\/\//i.test(value)) return value;
  if (isLikelyLocalMarkdownPath(value)) return value;
  return '';
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

function hasInlineImagePath(value) {
  INLINE_IMAGE_PATH_RE.lastIndex = 0;
  return INLINE_IMAGE_PATH_RE.test(value);
}

function hasCodexDirective(value) {
  CODEX_DIRECTIVE_RE.lastIndex = 0;
  return CODEX_DIRECTIVE_RE.test(value);
}

function shouldRenderPlainTextMarkdown(text) {
  const value = normalizeMessageText(text);
  if (!value.trim() || value.includes('\n')) return false;
  if (PLAIN_TEXT_MARKDOWN_TOKEN_RE.test(value)) return false;
  return !hasInlineImagePath(value) && !hasCodexDirective(value);
}

function CodeBlock({ language = '', code = '' }) {
  if (isMermaidLanguage(language) || isMermaidSource(code)) {
    return <MermaidDiagram key={code} source={code} />;
  }
  return <pre><code>{code}</code></pre>;
}

function fenceMarkerMatch(line) {
  const backtickIndex = line.indexOf('```');
  const tildeIndex = line.indexOf('~~~');
  if (backtickIndex < 0 && tildeIndex < 0) return null;
  const markerIndex = backtickIndex < 0
    ? tildeIndex
    : (tildeIndex < 0 ? backtickIndex : Math.min(backtickIndex, tildeIndex));
  const markerChar = line[markerIndex];
  let fenceLength = 0;
  while (line[markerIndex + fenceLength] === markerChar) fenceLength += 1;
  if (fenceLength < 3) return null;
  return { markerIndex, markerChar, fenceLength };
}

function normalizeFenceLanguageToken(token) {
  const value = (token || '').toString().trim().toLowerCase();
  if (!value) return '';
  const classMatch = value.match(/^\{\.?([a-z][\w+-]*)/);
  if (classMatch) return classMatch[1].replace(/^language-/, '');
  return value.replace(/^language-/, '').replace(/^\./, '');
}

function fenceInfoRestIsMetadata(rest) {
  const value = (rest || '').toString().trim();
  if (!value) return false;
  return (
    /^[{[(]/.test(value) ||
    /^(?:title|filename|file|caption|linenos?|highlight|hl_lines|showlinenumbers|numberlines)\b/i.test(value) ||
    /^[\w-]+\s*=/.test(value)
  );
}

function parseFenceInfo(rawInfo) {
  const info = (rawInfo || '').toString().trim();
  if (!info) return { language: '', firstCodeLine: '' };

  const tokenMatch = info.match(/^([A-Za-z][\w+-]*)(?:\s+(.+))?$/);
  if (tokenMatch) {
    const rest = tokenMatch[2] || '';
    return {
      language: normalizeFenceLanguageToken(tokenMatch[1]),
      firstCodeLine: fenceInfoRestIsMetadata(rest) ? '' : rest,
    };
  }

  const classMatch = info.match(/^\{\.?([A-Za-z][\w+-]*)(?:\s+[^}]*)?}$/);
  if (classMatch) {
    return { language: normalizeFenceLanguageToken(classMatch[1]), firstCodeLine: '' };
  }

  const lower = info.toLowerCase();
  const language = CODE_FENCE_LANGUAGE_PREFIXES.find((item) => lower.startsWith(item));
  if (language && info.length > language.length) {
    const suffix = info.slice(language.length);
    return {
      language,
      firstCodeLine: fenceInfoRestIsMetadata(suffix) ? '' : suffix,
    };
  }

  return { language: normalizeFenceLanguageToken(info), firstCodeLine: '' };
}

function splitMarkdownFenceLine(line) {
  const marker = fenceMarkerMatch(line);
  if (!marker) return null;
  const prefix = line.slice(0, marker.markerIndex);
  const rawInfo = line.slice(marker.markerIndex + marker.fenceLength).replace(/^\s+/, '');
  return {
    prefix,
    markerChar: marker.markerChar,
    fenceLength: marker.fenceLength,
    ...parseFenceInfo(rawInfo),
  };
}

function markdownClosingFence(line, openingFence) {
  const value = (line || '').toString();
  const indentMatch = value.match(/^ {0,3}/);
  const markerStart = indentMatch?.[0].length || 0;
  const rest = value.slice(markerStart);
  const marker = openingFence.markerChar.repeat(openingFence.fenceLength);
  if (!rest.startsWith(marker)) return null;
  let cursor = openingFence.fenceLength;
  while (rest[cursor] === openingFence.markerChar) cursor += 1;
  return rest.slice(cursor).trim() ? null : { markerStart, markerLength: cursor };
}

function isIndentedMarkdownCodeLine(line) {
  return /^(?: {4}|\t)/.test(line || '');
}

function isIndentedMarkdownListItem(line) {
  return /^\s{4,}(?:[-*+]|\d+[.)])\s+/.test(line || '');
}

function indentedMarkdownCodeText(line) {
  const value = (line || '').toString();
  if (value.startsWith('\t')) return value.slice(1);
  return value.replace(/^ {4}/, '');
}

function isTerminalPromptLine(line) {
  return /^\s{0,3}(?:(?:[$\u276f\u279c\u03bb])|(?:PS [^>]*>)|(?:[A-Za-z]:[\\/][^>]*>)|(?:[\w.-]+@[\w.-]+:[^\s$#>]*[$#]))\s+\S/.test(line || '');
}

function isInsideInlineCode(source, offset) {
  let open = false;
  for (let index = 0; index < offset; index += 1) {
    if (source[index] !== '`') continue;
    let runLength = 1;
    while (source[index + runLength] === '`') runLength += 1;
    if (runLength === 1) open = !open;
    index += runLength - 1;
  }
  return open;
}

function unorderedMarkdownListItemText(line) {
  const trimmed = line.trim();
  const standard = trimmed.match(/^[-*]\s+(.+)$/);
  if (standard) return standard[1];
  const compact = trimmed.match(/^[-*]((?:[A-Z][A-Za-z0-9_-]{1,40}|[\u4e00-\u9fff][\u4e00-\u9fffA-Za-z0-9_-]{0,20})[:\uFF1A].+)$/);
  return compact?.[1] || '';
}

function startsSoftMarkdownHeading(source, index) {
  if (index <= 0 || source[index] !== '#' || isInsideInlineCode(source, index)) return false;
  let cursor = index;
  while (source[cursor] === '#') cursor += 1;
  const level = cursor - index;
  if (level < 2 || level > 6 || !source[cursor]) return false;
  const hasSpace = /\s/.test(source[cursor]);
  if (hasSpace) {
    return /[\s\u3002\uff01\uff1f!?；;\uff1a:，,.)）\]}]/.test(source[index - 1]);
  }
  if (!/^[A-Za-z0-9_]/.test(source[cursor])) return false;
  return /[\s\u3002\uff01\uff1f!?；;\uff1a:，,.)）\]}]/.test(source[index - 1]);
}

function compactHeadingPrefixBeforeList(value) {
  return /^#{2,6}[^:\uFF1A\s]*$/.test(value.trim());
}

function startsSoftMarkdownList(source, index, segmentStart) {
  if (index <= 0 || source[index] !== '-' || isInsideInlineCode(source, index)) return false;
  if (!source.slice(0, index).trim()) return false;
  if (compactHeadingPrefixBeforeList(source.slice(segmentStart, index))) return false;
  if (!unorderedMarkdownListItemText(source.slice(index))) return false;
  if (/^-\s+/.test(source.slice(index))) {
    return /[\s\u3002\uff01\uff1f!?；;\uff1a:，,.)）\]}]/.test(source[index - 1]);
  }
  return !/[\\/]/.test(source[index - 1]);
}

function splitMarkdownSoftBlocks(line) {
  const source = (line || '').toString();
  if (!source || fenceMarkerMatch(source)) return [source];
  const boundaries = [];
  let segmentStart = 0;
  for (let index = 1; index < source.length; index += 1) {
    if (startsSoftMarkdownHeading(source, index)) {
      boundaries.push(index);
      segmentStart = index;
      continue;
    }
    if (startsSoftMarkdownList(source, index, segmentStart)) {
      boundaries.push(index);
      segmentStart = index;
    }
  }
  if (boundaries.length === 0) return [source];
  const chunks = [];
  let start = 0;
  boundaries.forEach((boundary) => {
    const chunk = source.slice(start, boundary).trimEnd();
    if (chunk) chunks.push(chunk);
    start = boundary;
  });
  const tail = source.slice(start).trimStart();
  if (tail) chunks.push(tail);
  return chunks.length > 0 ? chunks : [source];
}

function normalizeCompactMarkdownLine(line) {
  const heading = line.match(/^(\s*)(#{2,6})([A-Za-z0-9_].*)$/);
  if (heading) return `${heading[1]}${heading[2]} ${heading[3]}`;
  const compactList = line.match(/^(\s*)([-*])((?:[A-Z][A-Za-z0-9_-]{1,40}|[\u4e00-\u9fff][\u4e00-\u9fffA-Za-z0-9_-]{0,20})[:\uFF1A].+)$/);
  if (compactList) return `${compactList[1]}${compactList[2]} ${compactList[3]}`;
  return line;
}

function normalizedFenceLine(fence) {
  const marker = fence.markerChar.repeat(fence.fenceLength);
  return fence.language ? `${marker}${fence.language}` : marker;
}

function normalizeMarkdownLinesForRenderer(lines) {
  const normalized = [];
  let openFence = null;
  for (let index = 0; index < lines.length; index += 1) {
    const line = lines[index];
    if (openFence) {
      if (markdownClosingFence(line, openFence)) {
        normalized.push(openFence.markerChar.repeat(openFence.fenceLength));
        openFence = null;
      }
      else {
        normalized.push(line);
      }
      continue;
    }

    if (isTerminalPromptLine(line)) {
      normalized.push('```terminal');
      let cursor = index;
      while (cursor < lines.length && lines[cursor].trim()) {
        normalized.push(lines[cursor]);
        cursor += 1;
      }
      normalized.push('```');
      index = cursor - 1;
      continue;
    }

    if (isIndentedMarkdownCodeLine(line) && !isIndentedMarkdownListItem(line)) {
      normalized.push('```');
      let cursor = index;
      while (cursor < lines.length) {
        const codeLine = lines[cursor];
        if (codeLine.trim() && (!isIndentedMarkdownCodeLine(codeLine) || isIndentedMarkdownListItem(codeLine))) break;
        normalized.push(codeLine.trim() ? indentedMarkdownCodeText(codeLine) : '');
        cursor += 1;
      }
      normalized.push('```');
      index = cursor - 1;
      continue;
    }

    const fence = splitMarkdownFenceLine(line);
    if (fence) {
      if (fence.prefix.trim()) normalized.push(normalizeCompactMarkdownLine(fence.prefix.trimEnd()));
      normalized.push(normalizedFenceLine(fence));
      if (fence.firstCodeLine) normalized.push(fence.firstCodeLine);
      openFence = fence;
      continue;
    }

    normalized.push(normalizeCompactMarkdownLine(line));
  }
  return normalized;
}

function encodeCodexDirectives(text) {
  return text.replace(CODEX_DIRECTIVE_RE, (token) => `[citation](${DIRECTIVE_HREF_PREFIX}${encodeURIComponent(token)})`);
}

function markdownRendererText(text) {
  const lines = normalizeMessageText(text)
    .split('\n')
    .flatMap(splitMarkdownSoftBlocks);
  return encodeCodexDirectives(normalizeMarkdownLinesForRenderer(lines).join('\n'));
}

function standaloneCodeFence(text) {
  const lines = normalizeMessageText(text).trim().split('\n');
  if (lines.length < 1) return null;
  const opening = splitMarkdownFenceLine(lines[0]);
  if (!opening || opening.prefix.trim()) return null;

  let closingIndex = -1;
  for (let i = 1; i < lines.length; i++) {
    if (markdownClosingFence(lines[i], opening)) {
      closingIndex = i;
      break;
    }
  }

  if (closingIndex !== -1) {
    if (closingIndex !== lines.length - 1) return null;
    const bodyLines = lines.slice(1, closingIndex);
    if (opening.firstCodeLine) bodyLines.unshift(opening.firstCodeLine);
    return {
      language: opening.language,
      body: bodyLines.join('\n'),
    };
  }

  const bodyLines = lines.slice(1);
  if (opening.firstCodeLine) bodyLines.unshift(opening.firstCodeLine);
  return {
    language: opening.language,
    body: bodyLines.join('\n'),
  };
}

function candidatePayload(text) {
  const fenced = standaloneCodeFence(text);
  if (!fenced) return { language: '', body: normalizeMessageText(text) };
  return fenced;
}

function parseJsonOutput(text) {
  const payload = candidatePayload(text);
  if (payload.language && !['json', 'jsonc'].includes(payload.language)) return null;
  const trimmed = payload.body.trim();
  if (!trimmed.startsWith('{') && !trimmed.startsWith('[')) return null;
  try {
    return JSON.stringify(JSON.parse(trimmed), null, 2);
  }
  catch {
    return null;
  }
}

function isDiffOutput(text) {
  const payload = candidatePayload(text);
  const body = payload.body.trim();
  if (['diff', 'patch'].includes(payload.language)) return body.length > 0;
  const lines = body.split('\n');
  if (body.startsWith('diff --git ') || body.startsWith('*** Begin Patch')) return true;
  const hasOldHeader = lines.some((line) => line.startsWith('--- '));
  const hasNewHeader = lines.some((line) => line.startsWith('+++ '));
  const hasHunk = lines.some((line) => line.startsWith('@@ '));
  return hasOldHeader && hasNewHeader && hasHunk;
}

function isLogOutput(text) {
  const payload = candidatePayload(text);
  const lines = [];
  for (const line of payload.body.split('\n')) {
    const trimmed = line.trimEnd();
    if (trimmed) lines.push(trimmed);
  }
  if (lines.length === 0) return false;
  if (['log', 'logs', 'console', 'terminal'].includes(payload.language)) return true;

  const levelLines = lines.filter((line) => /^(\[[A-Z]+]|\d{4}-\d{2}-\d{2}[T\s]\d{2}:\d{2}:\d{2}|(?:TRACE|DEBUG|INFO|WARN|WARNING|ERROR|FATAL)\b)/.test(line));
  const stackTrace = lines.some((line) => /^(?:\w+\s*)?Error:/.test(line))
    && lines.some((line) => /^\s*at\s+.+:\d+:\d+\)?$/.test(line));
  const terminalPrompt = isTerminalPromptLine(lines[0]);
  return stackTrace || levelLines.length > 0 || terminalPrompt;
}

function isConfigOutput(text) {
  const payload = candidatePayload(text);
  const lines = [];
  for (const line of payload.body.split('\n')) {
    const trimmed = line.trim();
    if (trimmed) lines.push(trimmed);
  }
  if (lines.length < 2) return false;
  if (['yaml', 'yml', 'toml', 'ini', 'env', 'dotenv', 'properties'].includes(payload.language)) return true;
  const keyValueLines = lines.filter((line) => /^[-\w."']+(\s*[:=]\s*|\s+=\s+).+/.test(line));
  return keyValueLines.length >= 2 && keyValueLines.length / lines.length >= 0.6;
}

function detectMessageOutput(text) {
  const json = parseJsonOutput(text);
  if (json) return { kind: 'json', text: json };
  const payload = candidatePayload(text);
  const body = payload.body.trimEnd();
  if (isDiffOutput(text)) return { kind: 'diff', text: body };
  if (isLogOutput(text)) return { kind: 'log', text: body };
  if (isConfigOutput(text)) return { kind: 'config', text: body };
  return { kind: 'markdown', text: normalizeMessageText(text) };
}

function diffLineClass(line) {
  if (line.startsWith('@@')) return 'diff-line diff-line--hunk';
  if (line.startsWith('+++') || line.startsWith('---') || line.startsWith('diff --git') || line.startsWith('index ')) return 'diff-line diff-line--meta';
  if (line.startsWith('+')) return 'diff-line diff-line--added';
  if (line.startsWith('-')) return 'diff-line diff-line--deleted';
  return 'diff-line';
}

function StructuredMessage({ kind, text }) {
  const outputText = normalizeMessageText(text);
  if (kind === 'diff') {
    return (
      <div className={`message-output message-output--${kind}`} data-output-kind={kind}>
        <pre>
          <code>
            {outputText.split('\n').map((line, index) => (
              <span key={`${kind}-${index}`} className={diffLineClass(line)}>{line || ' '}</span>
            ))}
          </code>
        </pre>
      </div>
    );
  }
  return (
    <div className={`message-output message-output--${kind}`} data-output-kind={kind}>
      <pre><code>{outputText}</code></pre>
    </div>
  );
}

function reactChildrenText(children) {
  if (children === null || children === undefined) return '';
  if (typeof children === 'string' || typeof children === 'number' || typeof children === 'boolean') return children.toString();
  if (Array.isArray(children)) return children.map((child) => reactChildrenText(child)).join('');
  if (React.isValidElement(children)) return reactChildrenText(children.props.children);
  return '';
}

function directiveTokenFromHref(href) {
  const value = (href || '').toString();
  if (!value.startsWith(DIRECTIVE_HREF_PREFIX)) return '';
  try {
    return decodeURIComponent(value.slice(DIRECTIVE_HREF_PREFIX.length));
  }
  catch {
    return '';
  }
}

function markdownLinkToken(label, href) {
  return `[${(label || '').toString()}](${(href || '').toString()})`;
}

function MarkdownLink({ href = '', children, actions = EMPTY_MARKDOWN_ACTIONS }) {
  const directiveToken = directiveTokenFromHref(href);
  if (directiveToken) {
    return <MarkdownDirectiveChip chip={directiveChipModel(directiveToken)} actions={actions} />;
  }

  const label = reactChildrenText(children).trim() || href;
  const citation = citationMarkdownLinkChipModel(markdownLinkToken(label, href));
  if (citation) return <MarkdownCitationLinkChip chip={citation} actions={actions} />;

  const fileRef = markdownFileLinkRef(href);
  const openFile = actions?.onOpenPath || actions?.onFileRef;
  if (fileRef && openFile) {
    const handleFileClick = (event) => {
      event.preventDefault();
      openFile({ ...fileRef, raw: label });
    };
    return (
      <button
        type="button"
        className="chat-md-file-ref chat-md-file-link"
        aria-label={`\u6253\u5f00\u6587\u4ef6 ${label}`}
        title={fileRef.path}
        onClick={handleFileClick}
      >
        {children}
      </button>
    );
  }
  if (fileRef) return <>{children}</>;

  const safeHref = safeMarkdownUrl(href);
  if (!safeHref) return <>{children}</>;
  const handleClick = (event) => {
    event.preventDefault();
    if (window.wails?.Browser?.OpenURL) {
      window.wails.Browser.OpenURL(safeHref);
    } else {
      window.open(safeHref, '_blank', 'noreferrer');
    }
  };
  return <a href={safeHref} onClick={handleClick} rel="noreferrer">{children}</a>;
}

function MarkdownImage({ src = '', alt = '' }) {
  const safeSrc = safeMarkdownUrl(src, { image: true });
  if (!safeSrc) return alt || basenameFromPath(src) || '';
  return <MarkdownImagePreview src={safeSrc} label={alt || basenameFromPath(src)} />;
}

function languageFromClassName(className = '') {
  const match = className.match(/(?:^|\s)language-([^\s]+)/);
  return normalizeFenceLanguageToken(match?.[1] || '');
}

function codeBlockFromPreChildren(children) {
  const child = React.Children.toArray(children).find((item) => (
    React.isValidElement(item) && (item.type === 'code' || item.type === MarkdownCode)
  ));
  if (!child) return null;
  return {
    language: languageFromClassName(child.props.className || ''),
    code: reactChildrenText(child.props.children).replace(/\n$/, ''),
  };
}

function MarkdownPre({ node: _node, children }) {
  const block = codeBlockFromPreChildren(children);
  if (block) return <CodeBlock language={block.language} code={block.code} />;
  return <pre>{children}</pre>;
}

function MarkdownCode({ node: _node, className = '', children, ...props }) {
  const codeText = reactChildrenText(children);
  if (!className) {
    const image = renderImagePreview(codeText.trim(), basenameFromPath(codeText.trim()), 'inline-code-image');
    return image || <code {...props}>{children}</code>;
  }
  return <code className={className} {...props}>{children}</code>;
}

function MarkdownParagraph({ children }) {
  const text = reactChildrenText(children);
  const imageParts = renderPlainTextWithImagePreviews(text, 'paragraph-image');
  if (imageParts.length === 1 && imageParts[0] === text) return <p>{children}</p>;
  return <p>{imageParts}</p>;
}

function MarkdownListItem({ node: _node, className = '', children, ...props }) {
  if (!className.includes('task-list-item')) return <li {...props} className={className}>{children}</li>;
  const label = reactChildrenText(children).trim();
  const patchedChildren = React.Children.map(children, (child) => {
    if (!React.isValidElement(child) || child.type !== MarkdownInput) return child;
    return React.cloneElement(child, { 'aria-label': label });
  });
  return <li {...props} className={className}>{patchedChildren}</li>;
}

function MarkdownUnorderedList({ className = '', children, ...props }) {
  const classNames = [className];
  if (className.includes('contains-task-list')) classNames.push('task-list');
  return <ul {...props} className={classNames.filter(Boolean).join(' ')}>{children}</ul>;
}

function MarkdownInput({ node: _node, checked, ...props }) {
  return <input {...props} checked={Boolean(checked)} disabled readOnly />;
}

function markdownComponents(actions) {
  return {
    a({ node: _node, href, children }) {
      return <MarkdownLink href={href} actions={actions}>{children}</MarkdownLink>;
    },
    img({ node: _node, src, alt }) {
      return <MarkdownImage src={src} alt={alt} />;
    },
    p({ node: _node, children }) {
      return <MarkdownParagraph>{children}</MarkdownParagraph>;
    },
    pre: MarkdownPre,
    code: MarkdownCode,
    li: MarkdownListItem,
    ul({ node: _node, className, children, ...props }) {
      return <MarkdownUnorderedList className={className} {...props}>{children}</MarkdownUnorderedList>;
    },
    input: MarkdownInput,
  };
}

function markdownUrlTransform(url, key, node) {
  const value = (url || '').toString().trim();
  if (!value) return '';
  const productUrl = productMarkdownUrl(value, { image: key === 'src' || node?.tagName === 'img' });
  if (productUrl) return productUrl;
  return defaultUrlTransform(value);
}

function MarkdownRenderer({ text, actions = EMPTY_MARKDOWN_ACTIONS, fallback = null }) {
  const components = useMemo(() => markdownComponents(actions), [actions]);
  if (shouldRenderPlainTextMarkdown(text)) return <p>{normalizeMessageText(text)}</p>;
  const markdownText = markdownRendererText(text);
  if (!markdownText.trim()) return fallback;
  return (
    <ReactMarkdown
      remarkPlugins={MARKDOWN_REMARK_PLUGINS}
      components={components}
      urlTransform={markdownUrlTransform}
    >
      {markdownText}
    </ReactMarkdown>
  );
}

function MarkdownMessage({ text, actions }) {
  return (
    <div className="message-markdown">
      <MarkdownRenderer text={text} actions={actions} fallback={<p />} />
    </div>
  );
}

function MessageContent({ text, actions }) {
  const output = detectMessageOutput(text);
  if (output.kind === 'markdown') return <MarkdownMessage text={output.text} actions={actions} />;
  return <StructuredMessage kind={output.kind} text={output.text} />;
}

export { CodePreviewMarkdown, MarkdownImagePreview, MessageContent };
