import React, { useMemo } from 'react';
import ReactMarkdown, { defaultUrlTransform } from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { MarkdownCitationLinkChip, MarkdownDirectiveChip } from './MarkdownDirectiveChip.jsx';
import { MarkdownImagePreview } from './MarkdownImagePreview.jsx';
import { MermaidDiagram } from './MermaidDiagram.jsx';
import { CODEX_DIRECTIVE_RE, citationMarkdownLinkChipModel, directiveChipModel } from './markdownDirectiveModel.js';
import { isMermaidLanguage, isMermaidSource } from './markdownMermaidModel.js';
import {
  basenameFromPath,
  firstText,
  firstTrimmedText,
  imagePreviewSource,
  normalizeMessageText,
  requiredMarkdownObject,
  textValue,
  trimmedText,
} from './markdownMessageModel.js';
import { detectMessageOutput, diffLineClass, markdownRendererText, normalizeFenceLanguageToken } from './markdownRendererModel.js';

const EMPTY_MARKDOWN_ACTIONS = Object.freeze({});
const MARKDOWN_REMARK_PLUGINS = [remarkGfm];
const DIRECTIVE_HREF_PREFIX = 'codex-directive:';
const PLAIN_TEXT_MARKDOWN_TOKEN_RE = /[#>*_[\]()`|~!]/;
const SAFE_MARKDOWN_RASTER_DATA_URL_RE = /^data:image\/(?:png|jpe?g|webp|gif|bmp);base64,[a-z0-9+/=\s]+$/i;

const INLINE_IMAGE_PATH_RE = /(?:file:\/\/\/?[^\s`<>()"']+|~?\/(?!\/)[^\s`<>()"']+|\.{1,2}\/[^\s`<>()"']+|[A-Za-z]:[\\/][^\s`<>()"']+)\.(?:png|jpe?g|webp|gif|svg)(?:[?#][^\s`<>()"']*)?/gi;

function CodePreviewMarkdown({ content }) {
  return <MarkdownRenderer text={content} />;
}

function parsedMarkdownUrl(value) {
  try {
    return new URL(value, firstText(window.location?.origin, 'http://localhost'));
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
    const path = decodeURIComponent(textValue(parsed.pathname));
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
  const value = trimmedText(rawUrl);
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
  const value = trimmedText(rawUrl);
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
  const value = trimmedText(rawUrl);
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
  const label = firstTrimmedText(altText, basenameFromPath(rawSource), '\u56fe\u7247\u9884\u89c8');
  return <MarkdownImagePreview key={key} src={src} label={label} />;
}

function trimTrailingImagePathPunctuation(value) {
  let path = textValue(value);
  let suffix = '';
  while (/[.,;:!?\uFF0C\u3002\uFF1B\uFF1A\uFF01\uFF1F\u3001]$/.test(path)) {
    suffix = `${path.at(-1)}${suffix}`;
    path = path.slice(0, -1);
  }
  return { path, suffix };
}

function renderPlainTextWithImagePreviews(text, keyPrefix) {
  const source = textValue(text);
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
  const value = textValue(href);
  if (!value.startsWith(DIRECTIVE_HREF_PREFIX)) return '';
  try {
    return decodeURIComponent(value.slice(DIRECTIVE_HREF_PREFIX.length));
  }
  catch {
    return '';
  }
}

function markdownLinkToken(label, href) {
  return `[${textValue(label)}](${textValue(href)})`;
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
  const openFile = actions?.onOpenPath;
  const legacyOpenFile = actions?.onFileRef;
  const fileOpener = typeof openFile === 'function' ? openFile : legacyOpenFile;
  if (fileRef && fileOpener) {
    const handleFileClick = (event) => {
      event.preventDefault();
      fileOpener({ ...fileRef, raw: label });
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
  if (!safeSrc) return firstText(alt, basenameFromPath(src));
  return <MarkdownImagePreview src={safeSrc} label={firstText(alt, basenameFromPath(src))} />;
}

function languageFromClassName(className = '') {
  const match = className.match(/(?:^|\s)language-([^\s]+)/);
  return normalizeFenceLanguageToken(textValue(match?.[1]));
}

function codeBlockFromPreChildren(children) {
  const child = React.Children.toArray(children).find((item) => (
    React.isValidElement(item) && (item.type === 'code' || item.type === MarkdownCode)
  ));
  if (!child) return null;
  return {
    language: languageFromClassName(textValue(child.props.className)),
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
  const value = trimmedText(url);
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
  requiredMarkdownObject({ text }, 'MessageContent payload');
  const output = detectMessageOutput(text);
  if (output.kind === 'markdown') return <MarkdownMessage text={output.text} actions={actions} />;
  return <StructuredMessage kind={output.kind} text={output.text} />;
}

export { CodePreviewMarkdown, MarkdownImagePreview, MessageContent };
