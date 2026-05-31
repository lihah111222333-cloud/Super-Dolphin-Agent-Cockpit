// @ts-nocheck
import MarkdownIt from 'markdown-it';
import markdownItKatexModule from '@vscode/markdown-it-katex';
import { highlightSnippet } from './code-highlight.js';
import { toFilePreviewURL } from './preview-utils.js';
import {
  deriveSkillNameFromPath,
  isCodexInlineLiteral,
  postprocessCodexHtml,
  preprocessCodexMarkdown,
  resolveCodexLinkMeta,
} from './assistant-markdown-codex.js';

const markdownItKatex = /** @type {any} */ (markdownItKatexModule);





const FILE_REF_COLON_RE = new RegExp(String.raw`^(?<path>.*?):(?<line>\d+)(:(?<column>\d+))?([-?](?<endLine>\d+)(:(?<endColumn>\d+))?)?$`);
const FILE_REF_HASH_RE = new RegExp(String.raw`^(?<path>.*?)#L(?<line>\d+)(C(?<column>\d+))?(-L(?<endLine>\d+)(C(?<endColumn>\d+))?)?$`);
const FILE_REF_LINE_LABEL_RE = new RegExp(String.raw`^(?<path>.+?)\s*\((line|lines)\s*(?<line>\d+)(\s*[,?c\s]*(col|column)\s*(?<column>\d+))?\)$`, 'i');
const INLINE_FILE_REF_LINE_LABEL_RE = new RegExp(String.raw`(^|[\s(?["'?????-])(-?[A-Za-z0-9_./\\][^\s<>()]*)\s*\((line|lines)\s*(\d+)(\s*[,?c\s]*(col|column)\s*(\d+))?\)(?=$|[\s).???????:]"'-])`, 'gi');
const INLINE_FILE_REF_RE = new RegExp(String.raw`(^|[\s(?["'?????-])(-?[A-Za-z0-9_./\\][^\s<>()]*)(?=$|[\s).???????:]"'-])`, 'g');
const FILE_REF_TRAILING_PUNCTUATION_RE = /[),.!?;:'"。，、！？；：）】》]+$/;
const FILE_REF_LINE_LABEL_TRAILING_PUNCTUATION_RE = /[,.!?;:'"。，、！？；：】》]+$/;
const LONG_EXTENSION_ALLOWLIST = new Set([
  'bashrc',
  'dockerignore',
  'editorconfig',
  'eslintrc',
  'gitignore',
  'gitattributes',
  'npmignore',
  'prettierignore',
  'prettierrc',
  'terraform',
  'workspace',
]);
const KNOWN_FILE_EXT_ALLOWLIST = new Set([
  'avif', 'bmp', 'c', 'cc', 'cpp', 'cs', 'css', 'csv', 'gif', 'go', 'h', 'hpp', 'html', 'ico', 'ini',
  'java', 'js', 'jpeg', 'jpg', 'json', 'jsx', 'kt', 'less', 'log', 'lua', 'm', 'md', 'mjs', 'mm', 'php',
  'png', 'pl', 'properties', 'proto', 'ps1', 'py', 'rb', 'rs', 'sass', 'scala', 'scss', 'sh', 'svg', 'sql',
  'swift', 'toml', 'ts', 'tsx', 'txt', 'vue', 'webp', 'xml', 'yaml', 'yml', 'zsh',
]);
const BARE_FILENAME_ALLOWLIST = new Set(['dockerfile', 'makefile', 'readme', 'license']);
const LANGUAGE_ALIAS_MAP = {
  js: 'javascript',
  jsx: 'javascript',
  mjs: 'javascript',
  cjs: 'javascript',
  ts: 'typescript',
  tsx: 'typescript',
  yml: 'yaml',
  sh: 'bash',
  shell: 'bash',
  zsh: 'bash',
  console: 'bash',
  text: 'plaintext',
  txt: 'plaintext',
  md: 'markdown',
};

const markdown = createMarkdownRenderer();

function escapeHtml(value) {
  return (value || '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

function stashToken(tokens, label, html) {
  const token = `\u0000${label}${tokens.length}\u0000`;
  tokens.push(html);
  return token;
}

function restoreToken(text, label, tokens) {
  return text.replace(new RegExp(`\\u0000${label}(\\d+)\\u0000`, 'g'), (_, index) => {
    const i = Number(index);
    if (!Number.isFinite(i) || i < 0 || i >= tokens.length) return '';
    return tokens[i];
  });
}

function normalizeLanguage(rawLanguage) {
  const raw = (rawLanguage || '').toString().trim().toLowerCase();
  if (!raw) return '';
  return LANGUAGE_ALIAS_MAP[raw] || raw;
}

function isLikelyFilePath(pathRaw, hasLocation) {
  const hasPathSeparator = /[\\/]/.test(pathRaw);
  const hasRelativePrefix = /^\.{1,2}[\\/]/.test(pathRaw);
  const hasAbsolutePrefix = /^(?:[\\/]|~[\\/]|[A-Za-z]:[\\/])/.test(pathRaw);
  const isBarePathLikeToken = !hasPathSeparator && !hasRelativePrefix && !hasAbsolutePrefix;

  // 避免把 `markdown-it + highlight.js` 这类 inline code 表达式误判成可点击文件引用。
  // 保守策略：无目录前缀/无定位信息的“裸文件名”一旦包含空白，就不视为文件路径。
  if (isBarePathLikeToken && !hasLocation && /\s/.test(pathRaw)) {
    return false;
  }

  const filename = pathRaw.split(/[\\/]/).filter(Boolean).pop() || pathRaw;
  const filenameLower = filename.toLowerCase();
  if (BARE_FILENAME_ALLOWLIST.has(filenameLower)) return true;

  if (!filename.includes('.')) {
    if (hasLocation && (hasRelativePrefix || hasAbsolutePrefix)) return true;
    return false;
  }

  const extension = filename.split('.').pop() || '';
  if (!/^[a-zA-Z][a-zA-Z0-9_-]{0,20}$/.test(extension)) return false;

  const extLower = extension.toLowerCase();
  const hasLower = /[a-z]/.test(extension);
  const hasUpperAfterFirst = /[A-Z]/.test(extension.slice(1));
  if (hasLower && hasUpperAfterFirst) return false;

  const knownExtension = KNOWN_FILE_EXT_ALLOWLIST.has(extLower) || LONG_EXTENSION_ALLOWLIST.has(extLower);
  if (!knownExtension && !hasLocation) return false;
  if (!knownExtension && isBarePathLikeToken) return false;
  if (extLower.length > 10 && !LONG_EXTENSION_ALLOWLIST.has(extLower)) return false;
  return true;
}

export function parseInlineFileReference(rawText) {
  const raw = (rawText || '').toString().trim();
  if (!raw) return null;

  let text = raw;
  if (text.includes('：')) {
    text = text.split('：')[0].trim();
  }
  if (/^-[A-Za-z0-9_./\\]/.test(text)) {
    text = text.slice(1).trim();
  }

  const lineLabelText = text.replace(FILE_REF_LINE_LABEL_TRAILING_PUNCTUATION_RE, '').trim();
  if (lineLabelText) {
    const lineLabelMatch = lineLabelText.match(FILE_REF_LINE_LABEL_RE);
    if (lineLabelMatch?.groups) {
      const pathRaw = (lineLabelMatch.groups.path || '').toString().trim().replace(FILE_REF_TRAILING_PUNCTUATION_RE, '');
      const line = Number.parseInt(lineLabelMatch.groups.line || '0', 10) || 0;
      const column = Number.parseInt(lineLabelMatch.groups.column || '0', 10) || 0;
      if (pathRaw && line > 0 && isLikelyFilePath(pathRaw, true)) {
        return { path: pathRaw, line, column, endLine: 0, endColumn: 0 };
      }
    }
  }

  text = text.replace(FILE_REF_TRAILING_PUNCTUATION_RE, '').trim();
  if (!text) return null;

  let pathRaw = text;
  let line = 0;
  let column = 0;
  let endLine = 0;
  let endColumn = 0;

  const match = text.match(FILE_REF_COLON_RE) || text.match(FILE_REF_HASH_RE);
  if (match?.groups) {
    pathRaw = (match.groups.path || '').toString().trim();
    line = Number.parseInt(match.groups.line || '0', 10) || 0;
    column = Number.parseInt(match.groups.column || '0', 10) || 0;
    endLine = Number.parseInt(match.groups.endLine || '0', 10) || 0;
    endColumn = Number.parseInt(match.groups.endColumn || '0', 10) || 0;
  }

  if (/^-[A-Za-z0-9_./\\]/.test(pathRaw)) {
    pathRaw = pathRaw.slice(1).trim();
  }
  if (!pathRaw) return null;
  if (/^https?:\/\//i.test(pathRaw)) return null;
  if (/^www\./i.test(pathRaw)) return null;
  if (/^(mailto|tel):/i.test(pathRaw)) return null;

  const hasLocation = line > 0 || column > 0 || endLine > 0 || endColumn > 0;
  if (!isLikelyFilePath(pathRaw, hasLocation)) return null;

  return { path: pathRaw, line, column, endLine, endColumn };
}

function formatFileRefLocation(ref) {
  const line = Number(ref?.line) || 0;
  const column = Number(ref?.column) || 0;
  const endLine = Number(ref?.endLine) || 0;
  const endColumn = Number(ref?.endColumn) || 0;
  let location = '';

  if (line > 0) {
    location = endLine > 0 && endLine !== line ? `lines ${line}-${endLine}` : `line ${line}`;
  }
  if (column > 0 || endColumn > 0) {
    const columnText = column > 0 && endColumn > 0 && endColumn !== column
      ? `columns ${column}-${endColumn}`
      : `column ${column > 0 ? column : endColumn}`;
    location = location ? `${location}, ${columnText}` : columnText;
  }
  return location;
}

function formatFileRefLabel(ref) {
  const fullPath = (ref?.path || '').toString().trim();
  const filename = fullPath.split(/[\\/]/).filter(Boolean).pop() || fullPath;
  const location = formatFileRefLocation(ref);
  return location ? `${filename} (${location})` : filename;
}

function buildFileRefMeta(parsedFileRef) {
  const location = formatFileRefLocation(parsedFileRef);
  const titleText = location ? `${parsedFileRef.path} (${location})` : `${parsedFileRef.path}`;
  const label = formatFileRefLabel(parsedFileRef) || parsedFileRef.path;
  const line = Number(parsedFileRef?.line) > 0 ? Number(parsedFileRef.line) : 1;
  const column = Number(parsedFileRef?.column) > 0 ? Number(parsedFileRef.column) : 0;
  return { titleText, label, line, column };
}

function renderFileRefCode(parsedFileRef) {
  const { titleText, label, line, column } = buildFileRefMeta(parsedFileRef);
  return `<code class="chat-md-inline-code chat-md-file-ref is-file-ref" data-file-path="${escapeHtml(parsedFileRef.path)}" data-file-line="${line}" data-file-column="${column}" title="定位 ${escapeHtml(titleText)}">${escapeHtml(label)}</code>`;
}

function isInsideLinkToken(tokens, idx) {
  let depth = 0;
  for (let index = 0; index < idx; index += 1) {
    const type = (tokens[index]?.type || '').toString();
    if (type === 'link_open') depth += 1;
    else if (type === 'link_close' && depth > 0) depth -= 1;
  }
  return depth > 0;
}

function renderTextWithFileRefs(rawText, tokens = null, idx = -1) {
  const source = (rawText || '').toString();
  if (!source) return '';
  if (Array.isArray(tokens) && idx >= 0 && isInsideLinkToken(tokens, idx)) return escapeHtml(source);


  const fileRefTokens = [];
  let text = source.replace(INLINE_FILE_REF_LINE_LABEL_RE, (full, prefix, path, lineText, columnText) => {
    const line = Number.parseInt((lineText || '').toString(), 10) || 0;
    const column = Number.parseInt((columnText || '').toString(), 10) || 0;
    const rawRef = column > 0 ? `${path} (line ${line}, column ${column})` : `${path} (line ${line})`;
    const parsedFileRef = parseInlineFileReference(rawRef);
    if (!parsedFileRef) return full;
    return `${prefix}${stashToken(fileRefTokens, 'FILEREF', renderFileRefCode(parsedFileRef))}`;
  });

  text = text.replace(INLINE_FILE_REF_RE, (full, prefix, candidate) => {
    const parsedFileRef = parseInlineFileReference(candidate);
    if (!parsedFileRef) return full;
    return `${prefix}${stashToken(fileRefTokens, 'FILEREF', renderFileRefCode(parsedFileRef))}`;
  });

  text = escapeHtml(text);
  return restoreToken(text, 'FILEREF', fileRefTokens);
}

function appendClass(token, className) {
  const index = token.attrIndex('class');
  if (index < 0) {
    token.attrPush(['class', className]);
    return;
  }
  const current = (token.attrs?.[index]?.[1] || '').toString();
  const classes = new Set(current.split(/\s+/).filter(Boolean));
  classes.add(className);
  token.attrs[index][1] = Array.from(classes).join(' ');
}

function setAttr(token, name, value) {
  const index = token.attrIndex(name);
  if (index < 0) token.attrPush([name, value]);
  else token.attrs[index][1] = value;
}
function renderCodeBlock(content, rawLanguage = '') {
  const source = (content || '').toString();
  const label = (rawLanguage || '').toString().trim().split(/\s+/, 1)[0] || '';
  const normalizedLanguage = normalizeLanguage(label);
  const highlighted = highlightSnippet(source, { language: label });
  const classLanguage = normalizedLanguage || highlighted.language || 'plaintext';
  const codeHtml = highlighted.html || escapeHtml(source);
  const langText = label || (classLanguage === 'plaintext' ? 'text' : classLanguage);
  const header = `<div class="chat-md-code-head"><span class="chat-md-code-lang">${escapeHtml(langText)}</span></div>`;
  return `<div class="chat-md-code-block">${header}<pre class="chat-md-code" data-language="${escapeHtml(classLanguage)}"><code class="hljs language-${escapeHtml(classLanguage)}">${codeHtml}</code></pre></div>`;
}


function basenameFromSource(rawSource) {
  const source = (rawSource || '').toString().trim().split(/[?#]/, 1)[0];
  if (!source) return '';
  return source.split(/[\\/]/).filter(Boolean).pop() || source;
}

const RENDERABLE_IMAGE_SOURCE_RE = new RegExp(String.raw`^(data:image/|https?://|file://)`, 'i');
const LOCAL_FILE_IMAGE_SOURCE_RE = new RegExp(String.raw`^([\\/]|~[\\/]|\.{1,2}[\\/]|[A-Za-z]:[\\/])`);

function resolveRenderableImageSource(rawSource) {
  const source = (rawSource || '').toString().trim();
  if (!source) return '';
  if (RENDERABLE_IMAGE_SOURCE_RE.test(source)) return source;
  if (LOCAL_FILE_IMAGE_SOURCE_RE.test(source)) return toFilePreviewURL(source);
  return '';
}
function renderMarkdownImage(tokens, idx, options, env, self) {
  const token = tokens[idx];
  const rawSource = (token?.attrGet('src') || '').toString().trim();
  if (!rawSource) return '';
  const altText = (self.renderInlineAsText?.(token.children || [], options, env) || token.content || '').toString().trim();
  const rawTitle = (token?.attrGet('title') || '').toString().trim();
  const parsedFileRef = parseInlineFileReference(rawSource);
  const fileRefMeta = parsedFileRef ? buildFileRefMeta(parsedFileRef) : null;
  const filePath = parsedFileRef?.path || '';
  const resolvedSrc = resolveRenderableImageSource(rawSource);
  const caption = altText || basenameFromSource(rawSource) || 'Image';
  const titleText = rawTitle || altText || rawSource || 'Image preview';
  const classes = ['chat-md-citation', 'chat-md-image-citation', 'chat-md-image-card'];
  if (fileRefMeta) classes.push('chat-md-file-ref', 'is-file-ref', 'chat-md-file-link');
  const fileAttrs = fileRefMeta ? ` data-file-path="${escapeHtml(filePath)}" data-file-line="${fileRefMeta.line}" data-file-column="${fileRefMeta.column}"` : '';
  const srcAttr = resolvedSrc ? ` data-image-src="${escapeHtml(resolvedSrc)}"` : '';
  const previewHtml = resolvedSrc ? `<span class="chat-md-image-card__media"><img class="chat-md-image-card__img" src="${escapeHtml(resolvedSrc)}" alt="${escapeHtml(altText || caption)}" loading="lazy" decoding="async"></span>` : `<span class="chat-md-image-card__placeholder" aria-hidden="true">IMG</span>`;
  return `<button type="button" class="${classes.join(' ')}" data-citation-kind="image" data-asset-pointer="${escapeHtml(rawSource)}"${srcAttr}${fileAttrs} title="${escapeHtml(titleText)}">${previewHtml}<span class="chat-md-image-card__caption">${escapeHtml(caption)}</span></button>`;
}

function createMarkdownRenderer() {
  const instance = new MarkdownIt({
    html: false,
    linkify: true,
    breaks: true,
    typographer: false,
  });

  instance.renderer.rules.text = (tokens, idx) => renderTextWithFileRefs(tokens[idx]?.content || '', tokens, idx);

  instance.renderer.rules.code_inline = (tokens, idx) => {
    const code = (tokens[idx]?.content || '').toString();
    if (!isCodexInlineLiteral(code)) {
      const parsedFileRef = parseInlineFileReference(code);
      if (parsedFileRef) return renderFileRefCode(parsedFileRef);
    }
    return `<code class="chat-md-inline-code">${escapeHtml(code)}</code>`;
  };

  function shouldUseDerivedSkillLinkLabel(labelText, href, derivedName) {
    const normalizedLabel = (labelText || '').toString().trim();
    const normalizedHref = (href || '').toString().trim();
    const normalizedDerivedName = (derivedName || '').toString().trim();
    if (!normalizedDerivedName) return false;
    if (!normalizedLabel) return true;
    if (normalizedLabel === normalizedHref) return true;
    const hrefBaseName = normalizedHref.split(/[?#]/, 1)[0].split(/[\\/]/).filter(Boolean).pop() || '';
    if (normalizedLabel === hrefBaseName) return true;
    return /^SKILL\.md$/i.test(normalizedLabel);
  }

  function normalizeSkillFileLinkLabel(tokens, startIdx, href) {
    const derivedName = deriveSkillNameFromPath(href);
    if (!derivedName) return;
    const visibleTextTokens = [];
    for (let cursor = startIdx + 1; cursor < tokens.length; cursor += 1) {
      const token = tokens[cursor];
      if (token?.type === 'link_close') break;
      if (token?.type === 'text' || token?.type === 'code_inline') visibleTextTokens.push(token);
    }
    if (visibleTextTokens.length !== 1) return;
    const [labelToken] = visibleTextTokens;
    if (!shouldUseDerivedSkillLinkLabel(labelToken?.content || '', href, derivedName)) return;
    labelToken.content = derivedName;
  }

  instance.renderer.rules.link_open = (tokens, idx, options, _env, self) => {
    const token = tokens[idx];
    const href = (token?.attrGet('href') || '').toString().trim();
    const specialLink = resolveCodexLinkMeta(href); appendClass(token, 'chat-md-link');
    if (specialLink) {
      if ((specialLink?.dataAttrs?.['data-skill-path'] || '').toString().trim()) {
        normalizeSkillFileLinkLabel(tokens, idx, href);
      }
      specialLink.className.split(/\s+/).filter(Boolean).forEach((className) => appendClass(token, className));
      Object.entries(specialLink.dataAttrs || {}).forEach(([key, value]) => setAttr(token, key, `${value}`));
      setAttr(token, 'href', '#'); setAttr(token, 'title', specialLink.title || href); return self.renderToken(tokens, idx, options);
    }
    const parsedFileRef = parseInlineFileReference(href);
    const linkTextContent = (!parsedFileRef && tokens[idx + 1]) ? (tokens[idx + 1].content || '').toString().trim() : '';
    const effectiveFileRef = parsedFileRef || (linkTextContent ? parseInlineFileReference(linkTextContent) : null);
    if (effectiveFileRef) {
      const { titleText, line, column } = buildFileRefMeta(effectiveFileRef);
      appendClass(token, 'chat-md-file-ref'); appendClass(token, 'is-file-ref'); appendClass(token, 'chat-md-file-link');
      setAttr(token, 'data-file-path', effectiveFileRef.path); setAttr(token, 'data-file-line', `${line}`); setAttr(token, 'data-file-column', `${column}`); setAttr(token, 'title', `定位 ${titleText}`); return self.renderToken(tokens, idx, options);
    }
    setAttr(token, 'target', '_blank'); setAttr(token, 'rel', 'noopener noreferrer'); return self.renderToken(tokens, idx, options);
  };


  instance.renderer.rules.image = (tokens, idx, options, env, self) => renderMarkdownImage(tokens, idx, options, env, self);
  instance.renderer.rules.fence = (tokens, idx) => renderCodeBlock(tokens[idx]?.content || '', tokens[idx]?.info || '');
  instance.renderer.rules.code_block = (tokens, idx) => renderCodeBlock(tokens[idx]?.content || '', '');
  instance.use(markdownItKatex, { enableFencedBlocks: true, throwOnError: false });

  return instance;
}

/**
 * 推理风格内容中常见的工具调用行起始模式。
 * 当一段 assistant 文本中此类行 ≥ 2 时，说明内容可能是内部推理泄漏，
 * 需要在每个工具调用前后插入换行，避免渲染为一坨纯文本。
 */
const REASONING_TOOL_CALL_NAMES_RE = new RegExp(String.raw`(read_file|replace_range|open_file|did_change|rename|hover|definition|references|document_symbol|workspace_symbol|implementation|type_definition|signature_help|code_action|call_hierarchy|type_hierarchy|completion|format|semantic_tokens|folding_range|lsp_[a-z_]+|exec_command|update_plan|request_user_input)`);
const REASONING_TOOL_LINE_RE = new RegExp('^' + REASONING_TOOL_CALL_NAMES_RE.source + '\\s*\\(', 'i');

/**
 * 快速检测正则：检查文本中是否包含 tool_name( 模式。
 */
const REASONING_TOOL_DETECT_RE = new RegExp(REASONING_TOOL_CALL_NAMES_RE.source + '\\s*\\(', 'gi');

/**
 * 匹配推理文本中的工具调用片段（如 `read_file(offset=0) // 来自 ...`），用于分行处理。
 * - tool_name(...) 匹配工具名称和括号内参数
 * - 可选的 // 注释捕获，但在中文标点（。；，）或换行处停止
 */
const REASONING_TOOL_INLINE_RE = new RegExp(
  REASONING_TOOL_CALL_NAMES_RE.source + '\\s*\\([^)]*\\)(?:\\s*\\/\\/[^\\n。；，]*)?',
  'gi',
);

const REASONING_PROGRESS_SENTENCE_RE = new RegExp(String.raw`^(i(['?]m| am)\b|i found\b|now\b|next\b|adding\b|final verification\b|red check\b|the new test file\b|phase-\d+\b)`, 'i');
const REASONING_SENTENCE_START_RE = '(?:I\\b|Now\\b|Next\\b|Adding\\b|Final\\b|RED\\b|Phase-\\d+\\b|The\\b|It\\b|If\\b|[一-龥]|`)';
const REASONING_CODEISH_RE = new RegExp('(`[^`]+`|\\b[a-z_][a-z0-9_]*\\([^)]*\\)|\\b[a-z]+([A-Z][a-z0-9]+){1,}\\b|\\b[\\w./-]+\\.([cm]?[jt]sx?|go|py|rb|java|rs|md|json|ya?ml|sql)\\b)');
const REASONING_SENTENCE_BOUNDARY_RE = new RegExp('([。；！？.!?])\\s*(?=' + REASONING_SENTENCE_START_RE + ')', 'g');
const MARKDOWN_BLOCKISH_RE = new RegExp('(^|\\n)\\s{0,3}([#>*-]|\\d+\\.)\\s|```|\\n\\s*\\n', 'm');

function countMatches(text, regex) {
  const matches = (text || '').toString().match(regex);
  return matches ? matches.length : 0;
}

function splitReasoningSentences(text) {
  const normalized = (text || '')
    .toString()
    .replace(/\r\n?/g, '\n')
    .replace(REASONING_SENTENCE_BOUNDARY_RE, '$1\n');
  return normalized
    .split(/\n+/)
    .map((line) => line.trim())
    .filter(Boolean);
}

/**
 * 判断 assistant 文本是否像“思考/执行进度”泄漏：
 * - 明确的 tool call ≥ 2；
 * - 或者是一整坨第一人称进度句子，并混有代码/函数名/文件名等 code-ish 信号。
 */

export function isLikelyReasoningLeakText(text) {
  if (!text || typeof text !== 'string') return false;
  const normalized = text.replace(/\r\n?/g, '\n').trim();
  if (!normalized) return false;

  const toolCallCount = countMatches(normalized, REASONING_TOOL_DETECT_RE);
  if (toolCallCount >= 2) return true;

  if (normalized.length < 180 || MARKDOWN_BLOCKISH_RE.test(normalized)) return false;

  const sentences = splitReasoningSentences(normalized);
  if (sentences.length < 4) return false;

  let progressCount = 0;
  let codeishCount = 0;
  let toolishLineCount = 0;
  for (const sentence of sentences.slice(0, 12)) {
    if (REASONING_PROGRESS_SENTENCE_RE.test(sentence)) progressCount += 1;
    if (REASONING_CODEISH_RE.test(sentence)) codeishCount += 1;
    if (REASONING_TOOL_LINE_RE.test(sentence)) toolishLineCount += 1;
  }

  const mergedBoundaryCount = countMatches(normalized, REASONING_SENTENCE_BOUNDARY_RE);
  return progressCount >= 3 && (codeishCount >= 2 || mergedBoundaryCount >= 2 || toolishLineCount >= 2);
}

/**
 * 对疑似推理泄漏的助手消息文本进行预处理：
 * - 检测到 ≥ 2 个工具调用模式时激活；
 * - 或命中“长串进度句 + code-ish token”模式时激活；
 * - 在工具调用片段前后插入换行，确保独占一行；
 * - 在句子边界处补齐段落换行，避免渲染成一坨。
 *
 * 对于不含推理模式的正常文本直接原样返回。
 */
export function normalizeReasoningText(text) {
  if (!text || typeof text !== 'string') return text || '';

  const normalized = text.replace(/\r\n?/g, '\n');
  const toolCallCount = countMatches(normalized, REASONING_TOOL_DETECT_RE);
  const hasMarkdownStructure = MARKDOWN_BLOCKISH_RE.test(normalized);
  const shouldNormalize = !hasMarkdownStructure && (toolCallCount >= 2 || isLikelyReasoningLeakText(normalized));
  if (!shouldNormalize) return text;

  let result = normalized;

  if (toolCallCount >= 2) {
    result = result.replace(REASONING_TOOL_INLINE_RE, (match) => `\n\`${match.trim()}\`\n`);
  }

  result = result.replace(REASONING_SENTENCE_BOUNDARY_RE, '$1\n\n');
  result = result.replace(/\n{3,}/g, '\n\n');

  return result.trim();
}

/**
 * 修复跨行断裂的粗体/强调标记 (**)。
 * AI 模型有时会把 **粗体文本** 拆到多行，导致 markdown-it 无法正确匹配开闭：
 *   - **B.
 *   代码功能**：...
 * 此函数将断裂的标记合并回同一行。
 */
function fixBrokenBoldMarkers(text) {
  return text.replace(/\*\*([^*\n|]{1,80})\n{1,2}(?!\s*(?:[-+*]|\d+\.|#|>|\|))([^*\n|]{0,80}\*\*)/g, '**$1 $2');
}

export function injectSentenceBreaks(text) {
  if (!text) return text;
  const parts = text.split(/(`[^`]*`|```[\s\S]*?```|\[[^\]]+\]\([^)]+\))/g);
  for (let i = 0; i < parts.length; i += 2) {
    if (parts[i]) {
      parts[i] = parts[i]
        .replace(/(^|[^。])。(?!。)(['"’”\\)）\\]】》]{0,2})/g, '$1。$2\n')
        .replace(/\n{3,}/g, '\n\n');
    }
  }
  return parts.join('').replace(/\n{3,}/g, '\n\n').trimEnd();
}

export function renderAssistantMarkdown(rawText) {
  const text = (rawText || '').toString().replace(/\r\n?/g, '\n');
  if (!text.trim()) return '';
  const withBreaks = injectSentenceBreaks(text);
  const reasoningNormalized = normalizeReasoningText(withBreaks);
  const normalized = preprocessCodexMarkdown(reasoningNormalized);
  const fixed = fixBrokenBoldMarkers(normalized);
  return postprocessCodexHtml(markdown.render(fixed));
}
