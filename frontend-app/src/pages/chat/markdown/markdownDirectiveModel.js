import {
  basenameFromPath,
  firstText,
  firstTrimmedText,
  textValue,
  trimmedText,
} from './markdownMessageModel.js';

const CODEX_DIRECTIVE_RE = /:(codex-file-citation|codex-terminal-citation|codex-image-citation|task-stub|automation-update|code-comment)(?:\[([^\]\n]*)])?(?:\{([^{}\n]*)})?/g;

const CODEX_DIRECTIVE_TOKEN_RE = /^:(codex-file-citation|codex-terminal-citation|codex-image-citation|task-stub|automation-update|code-comment)(?:\[([^\]\n]*)])?(?:\{([^{}\n]*)})?$/;

const DIRECTIVE_ATTR_RE = /([A-Za-z_][\w-]*)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'{}]+))/g;

function skillNameFromPath(path) {
  const value = trimmedText(path).split(/[?#]/, 1)[0].replace(/\\/g, '/');
  const segments = value.split('/').filter(Boolean);
  if (segments.length >= 2 && /^SKILL\.md$/i.test(segments[segments.length - 1])) return textValue(segments[segments.length - 2]);
  return basenameFromPath(value).replace(/\.md$/i, '');
}

function parseDirectiveAttrs(rawAttrs) {
  const attrs = {};
  const source = textValue(rawAttrs);
  for (const match of source.matchAll(DIRECTIVE_ATTR_RE)) {
    const key = trimmedText(match[1]);
    if (!key) continue;
    attrs[key] = textValue(match[2] ?? match[3] ?? match[4]);
  }
  return attrs;
}

function positiveInt(value, fallback = 0) {
  const parsed = Number.parseInt(textValue(value), 10);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : fallback;
}

function lineRangeLabel(startLine, endLine) {
  const start = positiveInt(startLine, 0);
  const end = positiveInt(endLine, 0);
  if (start > 0 && end > 0 && end !== start) return `lines ${start}-${end}`;
  if (start > 0) return `line ${start}`;
  if (end > 0) return `line ${end}`;
  return '';
}

function fileCitationChip({ path, line, endLine, raw }) {
  const filePath = trimmedText(path);
  if (!filePath) return { type: 'text', text: textValue(raw) };
  const location = lineRangeLabel(line, endLine);
  const display = firstTrimmedText(raw, `${basenameFromPath(filePath)}${location ? ` (${location})` : ''}`);
  return {
    type: 'file',
    display,
    filePath,
    title: location ? `${filePath} (${location})` : filePath,
    payload: { path: filePath, line: positiveInt(line, positiveInt(endLine, 1)), column: 0, raw: display },
  };
}

function citationChip({ className = '', icon = '', label, payload, title = '' }) {
  const displayLabel = firstTrimmedText(label, payload?.raw, payload?.title, payload?.kind, '\u5f15\u7528');
  return {
    type: 'citation',
    className,
    displayLabel,
    icon,
    payload,
    title: title || displayLabel,
  };
}

function directiveChipModel(token) {
  const match = token.match(CODEX_DIRECTIVE_TOKEN_RE);
  if (!match) return null;
  const [, name, labelValue = '', rawAttrs = ''] = match;
  const attrs = parseDirectiveAttrs(rawAttrs);
  const label = trimmedText(labelValue);
  if (name === 'codex-file-citation') {
    return fileCitationChip({
      path: firstText(attrs.path, label),
      line: attrs.line_range_start,
      endLine: attrs.line_range_end,
      raw: label,
    });
  }
  if (name === 'codex-terminal-citation') {
    const lineStart = positiveInt(attrs.line_range_start, 0);
    const lineEnd = positiveInt(attrs.line_range_end, 0);
    return citationChip({
      className: 'chat-md-terminal-citation',
      icon: '\u2318',
      label: firstText(label, 'Terminal output'),
      title: firstText(attrs.terminal_chunk_id, label),
      payload: { kind: 'terminal', chunkId: textValue(attrs.terminal_chunk_id), lineStart, lineEnd, raw: firstText(label, 'Terminal output') },
    });
  }
  if (name === 'codex-image-citation') {
    return citationChip({
      className: 'chat-md-image-citation',
      icon: 'IMG',
      label: firstText(label, 'Image citation'),
      title: firstText(attrs.asset_pointer, label),
      payload: { kind: 'image', assetPointer: textValue(attrs.asset_pointer), imageSrc: textValue(attrs.image_src), path: textValue(attrs.path), raw: firstText(label, 'Image citation') },
    });
  }
  if (name === 'task-stub') {
    const title = firstTrimmedText(attrs.title, label, 'Task');
    return citationChip({
      className: 'chat-md-task-stub',
      icon: '\u2726',
      label: title,
      payload: { kind: 'task', title, prompt: label, raw: title },
    });
  }
  if (name === 'automation-update') {
    const title = firstTrimmedText(attrs.name, label, 'Automation');
    const prompt = firstTrimmedText(attrs.prompt, label);
    return citationChip({
      className: 'chat-md-automation-update',
      icon: '\u2699',
      label: title,
      payload: { kind: 'automation-update', title, message: label, prompt, path: '', lineStart: 0, lineEnd: 0, raw: title },
    });
  }
  if (name === 'code-comment') {
    const path = trimmedText(attrs.path);
    const startLine = positiveInt(attrs.line_range_start, 0);
    const endLine = positiveInt(attrs.line_range_end, startLine);
    const title = firstTrimmedText(attrs.title, 'Code comment');
    const location = path ? lineRangeLabel(startLine, endLine) : '';
    const display = path ? `${title} \u00b7 ${basenameFromPath(path)}${location ? ` (${location})` : ''}` : title;
    return citationChip({
      className: 'chat-md-code-comment',
      icon: '\ud83d\udcac',
      label: display,
      title: label || title,
      payload: { kind: 'code-comment', title, message: label, prompt: '', path, lineStart: startLine, lineEnd: endLine, raw: display },
    });
  }
  return null;
}

function citationLinkPayload(label, rawHref) {
  const href = trimmedText(rawHref);
  if (!href) return null;
  const skillMatch = href.match(/^app:\/\/([^/?#]+)/i);
  if (skillMatch) {
    return {
      kind: 'skill',
      skillId: textValue(skillMatch[1]),
      skillName: firstTrimmedText(label, skillMatch[1]),
      path: '',
      conversationId: '',
      raw: textValue(label),
    };
  }
  const conversationMatch = href.match(/^agent:\/\/([^/?#]+)/i);
  if (conversationMatch) {
    return {
      kind: 'conversation',
      skillId: '',
      skillName: '',
      path: '',
      conversationId: textValue(conversationMatch[1]),
      raw: textValue(label),
    };
  }
  if (/(^|[/\\])SKILL\.md(?:[?#].*)?$/i.test(href)) {
    return {
      kind: 'skill',
      skillId: '',
      skillName: skillNameFromPath(href),
      path: href,
      conversationId: '',
      raw: textValue(label),
    };
  }
  return null;
}

function citationMarkdownLinkChipModel(token) {
  const parsed = token.match(/^\[([^\]]+)]\(([^)]+)\)$/);
  const citationPayload = citationLinkPayload(parsed?.[1], parsed?.[2]);
  if (!citationPayload) return null;
  const label = citationPayload.kind === 'skill' && citationPayload.path && parsed?.[1] === parsed?.[2]
    ? citationPayload.skillName
    : parsed?.[1];
  return citationChip({
    className: citationPayload.kind === 'conversation' ? 'chat-md-conversation-chip' : 'chat-md-skill-chip',
    icon: citationPayload.kind === 'conversation' ? '\u2197' : '\u25c6',
    label,
    title: parsed?.[2] || label,
    payload: { ...citationPayload, raw: label || citationPayload.raw },
  });
}

export { CODEX_DIRECTIVE_RE, citationMarkdownLinkChipModel, directiveChipModel };
