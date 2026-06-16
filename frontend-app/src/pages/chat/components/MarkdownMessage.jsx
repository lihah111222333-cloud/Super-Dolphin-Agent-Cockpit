import React, { useMemo } from 'react';
import { EMPTY_MARKDOWN_ACTIONS, InlineMarkdown } from './MarkdownInline.jsx';
import { MermaidDiagram } from './MermaidDiagram.jsx';
import { MarkdownImagePreview } from './MarkdownImagePreview.jsx';
import { isMermaidLanguage, isMermaidSource } from './markdownMermaidModel.js';
import { normalizeMessageText } from './markdownMessageModel.js';

function CodePreviewMarkdown({ content }) {
  return <MarkdownBlocks lines={normalizeMessageText(content).split('\n')} />;
}

function markdownTableCells(line) {
  return (
    line
    .trim()
    .replace(/^\|/, '')
    .replace(/\|$/, '')
    .split('|')
    .map((cell) => cell.trim())
  );
}

function isMarkdownTableDivider(line) {
  const cells = markdownTableCells(line);
  return cells.length > 0 && cells.every((cell) => /^:?-{3,}:?$/.test(cell));
}

function MarkdownParagraph({ lines, paragraphKey, actions = EMPTY_MARKDOWN_ACTIONS }) {
  const nodes = [];
  const seenLines = new Map();
  for (let index = 0; index < lines.length; index += 1) {
    const line = lines[index];
    const seenCount = seenLines.get(line) || 0;
    seenLines.set(line, seenCount + 1);
    const lineKey = `${paragraphKey}-line-${line}${seenCount > 0 ? `-${seenCount}` : ''}`;
    if (index > 0) nodes.push(<br key={`${paragraphKey}-br-${lineKey}`} />);
    nodes.push(
      <InlineMarkdown
        key={`${paragraphKey}-inline-${lineKey}`}
        text={line}
        inlineKey={lineKey}
        actions={actions}
      />,
    );
  }
  return (
    <p>
      {nodes}
    </p>
  );
}

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

function isIndentedMarkdownCodeLine(line) {
  return /^(?: {4}|\t)/.test(line || '');
}

function unindentMarkdownCodeLine(line) {
  return (line || '').toString().replace(/^(?: {4}|\t)/, '');
}

function isTerminalPromptLine(line) {
  return /^\s{0,3}(?:(?:[$❯➜λ])|(?:PS [^>]*>)|(?:[A-Za-z]:[\\/][^>]*>)|(?:[\w.-]+@[\w.-]+:[^\s$#>]*[$#]))\s+\S/.test(line || '');
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

function markdownHeadingMatch(line) {
  const trimmed = line.trim();
  const standard = trimmed.match(/^(#{1,6})\s+(.+)$/);
  if (standard) return standard;
  const compact = trimmed.match(/^(#{2,6})([A-Za-z0-9_].*)$/);
  if (compact) return [compact[0], compact[1], compact[2]];
  return null;
}

function unorderedMarkdownListItemText(line) {
  const trimmed = line.trim();
  const standard = trimmed.match(/^[-*]\s+(.+)$/);
  if (standard) return standard[1];
  const compact = trimmed.match(/^[-*]((?:[A-Z][A-Za-z0-9_-]{1,40}|[\u4e00-\u9fff][\u4e00-\u9fffA-Za-z0-9_-]{0,20})[:：].+)$/);
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
    return /[\s。！？!?；;：:，,.)）\]}]/.test(source[index - 1]);
  }
  if (!/^[A-Za-z0-9_]/.test(source[cursor])) return false;
  return /[\s。！？!?；;：:，,.)）\]}]/.test(source[index - 1]);
}

function compactHeadingPrefixBeforeList(value) {
  return /^#{2,6}[^:：\s]*$/.test(value.trim());
}

function startsSoftMarkdownList(source, index, segmentStart) {
  if (index <= 0 || source[index] !== '-' || isInsideInlineCode(source, index)) return false;
  if (compactHeadingPrefixBeforeList(source.slice(segmentStart, index))) return false;
  if (!unorderedMarkdownListItemText(source.slice(index))) return false;
  if (/^-\s+/.test(source.slice(index))) {
    return /[\s。！？!?；;：:，,.)）\]}]/.test(source[index - 1]);
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

function markdownInputLines(text) {
  return normalizeMessageText(text).split('\n').flatMap(splitMarkdownSoftBlocks);
}

function standaloneCodeFence(text) {
  const lines = normalizeMessageText(text).trim().split('\n');
  if (lines.length < 1) return null;
  const opening = splitMarkdownFenceLine(lines[0]);
  if (!opening || opening.prefix.trim()) return null;

  // Find if there is a closing fence in the lines
  let closingIndex = -1;
  for (let i = 1; i < lines.length; i++) {
    if (markdownClosingFence(lines[i], opening)) {
      closingIndex = i;
      break;
    }
  }

  if (closingIndex !== -1) {
    // If there is a closing fence, it must be the last line to be a standalone code fence
    if (closingIndex !== lines.length - 1) {
      return null; // Closed in the middle, has trailing text -> not standalone
    }
    // Complete code fence
    const bodyLines = lines.slice(1, closingIndex);
    if (opening.firstCodeLine) bodyLines.unshift(opening.firstCodeLine);
    return {
      language: opening.language,
      body: bodyLines.join('\n'),
    };
  }

  // No closing fence found -> it is an incomplete/streaming code fence!
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

function markdownBlockContext(lines, actions = {}) {
  const nodes = [];
  return {
    actions,
    lines,
    nodes,
    nextKey: (kind) => `${kind}-${nodes.length}`,
  };
}

function consumeBlankMarkdownLine(context, index) {
  return context.lines[index].trim() ? null : { index: index + 1 };
}

function consumeMarkdownSeparator(context, index) {
  const trimmed = context.lines[index].trim();
  if (!/^(-{3,}|\*{3,}|_{3,})$/.test(trimmed)) return null;
  context.nodes.push(<hr key={context.nextKey('separator')} />);
  return { index: index + 1 };
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

function readMarkdownCodeLines(lines, index, fence) {
  const codeLines = fence.firstCodeLine ? [fence.firstCodeLine] : [];
  let cursor = index + 1;
  while (cursor < lines.length) {
    const closing = markdownClosingFence(lines[cursor], fence);
    if (closing) {
      const beforeClose = lines[cursor].slice(0, closing.markerStart);
      if (beforeClose.trim()) codeLines.push(beforeClose);
      return { codeLines, index: cursor + 1 };
    }
    codeLines.push(lines[cursor]);
    cursor += 1;
  }
  return { codeLines, index: cursor };
}

function consumeMarkdownFence(context, index) {
  const fence = splitMarkdownFenceLine(context.lines[index]);
  if (!fence) return null;
  if (fence.prefix.trim()) {
    const paragraphKey = context.nextKey('paragraph');
    context.nodes.push(<MarkdownParagraph key={paragraphKey} lines={[fence.prefix.trimEnd()]} paragraphKey={paragraphKey} actions={context.actions} />);
  }
  const key = context.nextKey('code');
  const code = readMarkdownCodeLines(context.lines, index, fence);
  context.nodes.push(<CodeBlock key={key} language={fence.language} code={code.codeLines.join('\n')} />);
  return { index: code.index };
}

function readIndentedMarkdownCodeLines(lines, index) {
  const codeLines = [];
  let cursor = index;
  while (cursor < lines.length) {
    if (!lines[cursor].trim()) {
      codeLines.push('');
      cursor += 1;
      continue;
    }
    if (!isIndentedMarkdownCodeLine(lines[cursor])) break;
    codeLines.push(unindentMarkdownCodeLine(lines[cursor]));
    cursor += 1;
  }
  while (codeLines.length > 0 && codeLines.at(-1) === '') codeLines.pop();
  return { codeLines, index: cursor };
}

function consumeIndentedMarkdownCode(context, index) {
  if (!isIndentedMarkdownCodeLine(context.lines[index])) return null;
  const result = readIndentedMarkdownCodeLines(context.lines, index);
  if (result.codeLines.length === 0) return null;
  context.nodes.push(<CodeBlock key={context.nextKey('code')} code={result.codeLines.join('\n')} />);
  return { index: result.index };
}

function readTerminalTranscriptLines(lines, index) {
  const codeLines = [];
  let cursor = index;
  while (cursor < lines.length) {
    if (!lines[cursor].trim()) break;
    codeLines.push(lines[cursor]);
    cursor += 1;
  }
  return { codeLines, index: cursor };
}

function consumeTerminalTranscript(context, index) {
  if (!isTerminalPromptLine(context.lines[index])) return null;
  const result = readTerminalTranscriptLines(context.lines, index);
  context.nodes.push(<CodeBlock key={context.nextKey('terminal')} language="terminal" code={result.codeLines.join('\n')} />);
  return { index: result.index };
}

function consumeMarkdownHeading(context, index) {
  const heading = markdownHeadingMatch(context.lines[index]);
  if (!heading) return null;
  const level = Math.min(6, heading[1].length);
  const HeadingTag = `h${level}`;
  context.nodes.push(
    <HeadingTag key={context.nextKey('heading')}>
      <InlineMarkdown text={heading[2]} inlineKey={`heading-${context.nodes.length}`} actions={context.actions} />
    </HeadingTag>,
  );
  return { index: index + 1 };
}

function markdownTableStarts(lines, index) {
  return (
    index + 1 < lines.length
    && lines[index].trim().includes('|')
    && isMarkdownTableDivider(lines[index + 1])
  );
}

function readMarkdownTableRows(lines, index) {
  const rows = [];
  let cursor = index;
  while (cursor < lines.length && lines[cursor].trim().includes('|')) {
    rows.push(markdownTableCells(lines[cursor]));
    cursor += 1;
  }
  return { rows, index: cursor };
}

function MarkdownTableHeaderCell({ cell, cellKey, actions = EMPTY_MARKDOWN_ACTIONS }) {
  return (
    <th>
      <InlineMarkdown text={cell} inlineKey={cellKey} actions={actions} />
    </th>
  );
}

function MarkdownTableCell({ value, cellKey, actions = EMPTY_MARKDOWN_ACTIONS }) {
  return (
    <td>
      <InlineMarkdown text={value} inlineKey={cellKey} actions={actions} />
    </td>
  );
}

function renderMarkdownTable(headers, rows, key, actions = {}) {
  return (
    <table key={key}>
      <thead>
        <tr>
          {headers.map((cell, cellIndex) => (
            <MarkdownTableHeaderCell key={`${key}-h-${cellIndex}`} cell={cell} cellKey={`${key}-h-${cellIndex}`} actions={actions} />
          ))}
        </tr>
      </thead>
      <tbody>
        {rows.map((row, rowIndex) => (
          <tr key={`${key}-r-${rowIndex}`}>
            {headers.map((_, cellIndex) => (
              <MarkdownTableCell
                key={`${key}-r-${rowIndex}-${cellIndex}`}
                value={row[cellIndex] || ''}
                cellKey={`${key}-r-${rowIndex}-${cellIndex}`}
                actions={actions}
              />
            ))}
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function consumeMarkdownTable(context, index) {
  if (!markdownTableStarts(context.lines, index)) return null;
  const key = context.nextKey('table');
  const headers = markdownTableCells(context.lines[index]);
  const body = readMarkdownTableRows(context.lines, index + 2);
  context.nodes.push(renderMarkdownTable(headers, body.rows, key, context.actions));
  return { index: body.index };
}

function consumeMarkdownQuote(context, index) {
  if (!context.lines[index].trim().startsWith('>')) return null;
  const key = context.nextKey('quote');
  const quoteLines = [];
  let cursor = index;
  while (cursor < context.lines.length && context.lines[cursor].trim().startsWith('>')) {
    quoteLines.push(context.lines[cursor].trim().replace(/^>\s?/, ''));
    cursor += 1;
  }
  context.nodes.push(
    <blockquote key={key}>
      <MarkdownParagraph lines={quoteLines} paragraphKey={`${key}-p`} actions={context.actions} />
    </blockquote>,
  );
  return { index: cursor };
}

function readMarkdownTaskItems(lines, index) {
  const items = [];
  let cursor = index;
  while (cursor < lines.length) {
    const itemMatch = lines[cursor].trim().match(/^[-*]\s*\[([ xX])]\s+(.+)$/);
    if (!itemMatch) break;
    items.push({ checked: itemMatch[1].toLowerCase() === 'x', text: itemMatch[2] });
    cursor += 1;
  }
  return { items, index: cursor };
}

function consumeMarkdownTaskList(context, index) {
  if (!context.lines[index].trim().match(/^[-*]\s*\[([ xX])]\s+(.+)$/)) return null;
  const key = context.nextKey('task-list');
  const result = readMarkdownTaskItems(context.lines, index);
  context.nodes.push(
    <ul key={key} className="task-list">
      {result.items.map((item, itemIndex) => (
        <li key={`${key}-${itemIndex}`}>
          <input type="checkbox" checked={item.checked} disabled readOnly aria-label={item.text} />
          <span><InlineMarkdown text={item.text} inlineKey={`${key}-${itemIndex}`} actions={context.actions} /></span>
        </li>
      ))}
    </ul>,
  );
  return { index: result.index };
}

function readMarkdownListItems(lines, index, ordered) {
  const items = [];
  let cursor = index;
  while (cursor < lines.length) {
    if (ordered) {
      const itemMatch = lines[cursor].trim().match(/^\d+\.\s+(.+)$/);
      if (!itemMatch) break;
      items.push(itemMatch[1]);
    }
    else {
      const itemText = unorderedMarkdownListItemText(lines[cursor]);
      if (!itemText) break;
      items.push(itemText);
    }
    cursor += 1;
  }
  return { items, index: cursor };
}

function consumeMarkdownList(context, index) {
  const trimmed = context.lines[index].trim();
  const unordered = unorderedMarkdownListItemText(trimmed);
  const ordered = trimmed.match(/^\d+\.\s+(.+)$/);
  if (!unordered && !ordered) return null;
  const key = context.nextKey('list');
  const ListTag = ordered ? 'ol' : 'ul';
  const result = readMarkdownListItems(context.lines, index, Boolean(ordered));
  context.nodes.push(
    <ListTag key={key}>
      {result.items.map((item, itemIndex) => (
        <li key={`${key}-${itemIndex}`}>
          <InlineMarkdown text={item} inlineKey={`${key}-${itemIndex}`} actions={context.actions} />
        </li>
      ))}
    </ListTag>,
  );
  return { index: result.index };
}

function startsMarkdownBlock(lines, index) {
  const next = lines[index];
  const trimmed = next.trim();
  if (!trimmed) return true;
  if (fenceMarkerMatch(next) || isIndentedMarkdownCodeLine(next) || isTerminalPromptLine(next) || trimmed.startsWith('>')) return true;
  if (/^(-{3,}|\*{3,}|_{3,})$/.test(trimmed)) return true;
  if (markdownHeadingMatch(trimmed)) return true;
  if (unorderedMarkdownListItemText(trimmed) || /^\d+\.\s+(.+)$/.test(trimmed)) return true;
  return markdownTableStarts(lines, index);
}

function consumeMarkdownParagraphBlock(context, index) {
  const paragraph = [context.lines[index]];
  let cursor = index + 1;
  while (cursor < context.lines.length && !startsMarkdownBlock(context.lines, cursor)) {
    paragraph.push(context.lines[cursor]);
    cursor += 1;
  }
  const paragraphKey = context.nextKey('paragraph');
  context.nodes.push(<MarkdownParagraph key={paragraphKey} lines={paragraph} paragraphKey={paragraphKey} actions={context.actions} />);
  return { index: cursor };
}

const MARKDOWN_BLOCK_CONSUMERS = [
  consumeBlankMarkdownLine,
  consumeMarkdownSeparator,
  consumeMarkdownFence,
  consumeIndentedMarkdownCode,
  consumeTerminalTranscript,
  consumeMarkdownHeading,
  consumeMarkdownTable,
  consumeMarkdownQuote,
  consumeMarkdownTaskList,
  consumeMarkdownList,
  consumeMarkdownParagraphBlock,
];

function consumeMarkdownBlock(context, index) {
  for (const consumer of MARKDOWN_BLOCK_CONSUMERS) {
    const result = consumer(context, index);
    if (result) return result.index;
  }
  throw new Error('markdown block consumer pipeline is incomplete');
}

function renderMarkdownBlocks(lines, actions = {}, cache = null) {
  const context = markdownBlockContext(lines, actions);
  let index = 0;
  const checkpoints = [];

  if (cache && cache.lines && cache.nodes && cache.checkpoints) {
    let matchingCount = 0;
    const maxMatch = Math.min(lines.length, cache.lines.length);
    while (matchingCount < maxMatch && lines[matchingCount] === cache.lines[matchingCount]) {
      matchingCount++;
    }

    let splitIndex = -1;
    for (let i = matchingCount - 1; i >= 0; i--) {
      if (cache.checkpoints[i] !== undefined) {
        splitIndex = i;
        break;
      }
    }

    if (splitIndex >= 0) {
      index = splitIndex;
      const reuseCount = cache.checkpoints[splitIndex];
      for (let i = 0; i < reuseCount; i++) {
        context.nodes.push(cache.nodes[i]);
      }
      for (let i = 0; i <= splitIndex; i++) {
        if (cache.checkpoints[i] !== undefined) {
          checkpoints[i] = cache.checkpoints[i];
        }
      }
    }
  }

  while (index < lines.length) {
    checkpoints[index] = context.nodes.length;
    index = consumeMarkdownBlock(context, index);
  }

  if (cache) {
    cache.lines = lines;
    cache.nodes = context.nodes;
    cache.checkpoints = checkpoints;
  }

  return context.nodes;
}

const MarkdownBlocks = React.memo(
  function MarkdownBlocks({ lines, actions = EMPTY_MARKDOWN_ACTIONS, fallback = null }) {
    const cache = useMemo(() => ({ lines: [], nodes: [], checkpoints: [], actions }), [actions]);
    const nodes = renderMarkdownBlocks(lines, actions, cache);
    return <>{nodes.length > 0 ? nodes : fallback}</>;
  },
  (prevProps, nextProps) => {
    if (prevProps.fallback !== nextProps.fallback) return false;
    if (prevProps.actions !== nextProps.actions) return false;
    const prevLines = prevProps.lines;
    const nextLines = nextProps.lines;
    if (prevLines === nextLines) return true;
    if (!prevLines || !nextLines) return false;
    if (prevLines.length !== nextLines.length) return false;
    for (let i = 0; i < prevLines.length; i++) {
      if (prevLines[i] !== nextLines[i]) return false;
    }
    return true;
  }
);

function MarkdownMessage({ text, actions }) {
  return (
    <div className="message-markdown">
      <MarkdownBlocks lines={markdownInputLines(text)} actions={actions} fallback={<p />} />
    </div>
  );
}

function MessageContent({ text, actions }) {
  const output = detectMessageOutput(text);
  if (output.kind === 'markdown') return <MarkdownMessage text={output.text} actions={actions} />;
  return <StructuredMessage kind={output.kind} text={output.text} />;
}

export { CodePreviewMarkdown, MarkdownImagePreview, MessageContent };
