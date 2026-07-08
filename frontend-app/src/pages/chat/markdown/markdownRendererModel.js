import { normalizeMessageText, parseMarkdownJsonSnippet, textValue, trimmedText } from './markdownMessageModel.js';
import { CODEX_DIRECTIVE_RE } from './markdownDirectiveModel.js';

const DIRECTIVE_HREF_PREFIX = 'codex-directive:';
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
  const value = trimmedText(token).toLowerCase();
  if (!value) return '';
  const classMatch = value.match(/^\{\.?([a-z][\w+-]*)/);
  if (classMatch) return classMatch[1].replace(/^language-/, '');
  return value.replace(/^language-/, '').replace(/^\./, '');
}

function fenceInfoRestIsMetadata(rest) {
  const value = trimmedText(rest);
  if (!value) return false;
  return (
    /^[{[(]/.test(value) ||
    /^(?:title|filename|file|caption|linenos?|highlight|hl_lines|showlinenumbers|numberlines)\b/i.test(value) ||
    /^[\w-]+\s*=/.test(value)
  );
}

function parseFenceInfo(rawInfo) {
  const info = trimmedText(rawInfo);
  if (!info) return { language: '', firstCodeLine: '' };

  const tokenMatch = info.match(/^([A-Za-z][\w+-]*)(?:\s+(.+))?$/);
  if (tokenMatch) {
    const rest = textValue(tokenMatch[2]);
    return fenceInfoFromTokenMatch(tokenMatch, rest);
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

function fenceInfoFromTokenMatch(tokenMatch, rest) {
  return {
    language: normalizeFenceLanguageToken(tokenMatch[1]),
    firstCodeLine: fenceInfoRestIsMetadata(rest) ? '' : rest,
  };
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
  const value = textValue(line);
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
  return /^(?: {4}|\t)/.test(textValue(line));
}

function isIndentedMarkdownListItem(line) {
  return /^\s{4,}(?:[-*+]|\d+[.)])\s+/.test(textValue(line));
}

function indentedMarkdownCodeText(line) {
  const value = textValue(line);
  if (value.startsWith('\t')) return value.slice(1);
  return value.replace(/^ {4}/, '');
}

function isTerminalPromptLine(line) {
  return /^\s{0,3}(?:(?:[$\u276f\u279c\u03bb])|(?:PS [^>]*>)|(?:[A-Za-z]:[\\/][^>]*>)|(?:[\w.-]+@[\w.-]+:[^\s$#>]*[$#]))\s+\S/.test(textValue(line));
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
  return textValue(compact?.[1]);
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
  const source = textValue(line);
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
  const parsed = parseMarkdownJsonSnippet(trimmed);
  if (parsed.ok) return { kind: 'json', text: parsed.text };
  if (!payload.language) return null;
  return { kind: 'json-error', text: `Invalid JSON: ${parsed.message}\n\n${trimmed}` };
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
  if (json) return json;
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


export {
  detectMessageOutput,
  diffLineClass,
  markdownRendererText,
  normalizeFenceLanguageToken,
};
