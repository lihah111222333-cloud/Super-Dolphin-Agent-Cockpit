// @ts-nocheck

const SKILL_LINK_RE = /^app:\/\/([^/?#]+)/i;
const CONVERSATION_LINK_RE = /^agent:\/\/([^/?#]+)/i;
const SKILL_FILE_RE = new RegExp(String.raw`(^|[/\\])SKILL\.md([?#].*)?$`, 'i');

function escapeHtml(value) {
  return (value || '')
    .toString()
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

function toPositiveInt(value) {
  const parsed = Number.parseInt((value || '').toString(), 10);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : 0;
}

function formatLineRange(startLine, endLine) {
  const start = toPositiveInt(startLine);
  const end = toPositiveInt(endLine);
  if (start > 0 && end > 0 && end !== start) return `lines ${start}-${end}`;
  if (start > 0) return `line ${start}`;
  if (end > 0) return `line ${end}`;
  return '';
}

function basename(path) {
  return (path || '').toString().split(/[/\\]/).filter(Boolean).pop() || (path || '').toString();
}

export function deriveSkillNameFromPath(path) {
  const source = (path || '').toString().trim().replace(/[/\\]+$/, '');
  if (!source) return '';
  const normalized = source.split(/[?#]/, 1)[0].replace(/\\/g, '/');
  const segments = normalized.split('/').filter(Boolean);
  if (segments.length >= 2 && /^SKILL\.md$/i.test(segments[segments.length - 1])) {
    return segments[segments.length - 2] || '';
  }
  return basename(normalized).replace(/\.md$/i, '');
}

/**
 * 渲染一个轻量的 inline citation badge（与 chat-md-citation 样式兼容）。
 * 替代原先臃肿的 card 布局，保持与其他 citation 风格一致。
 */
function renderInlineBadge({ className, icon, label, title, dataAttrs = {} }) {
  const attrStr = Object.entries(dataAttrs)
    .filter(([, v]) => (v || '').toString().trim())
    .map(([k, v]) => `${k}="${escapeHtml(v)}"`)
    .join(' ');
  const extraAttrs = attrStr ? ` ${attrStr}` : '';
  const iconHtml = icon ? `<span class="chat-md-citation__icon" aria-hidden="true">${escapeHtml(icon)}</span>` : '';
  const labelHtml = `<span class="chat-md-citation__body"><span class="chat-md-citation__label">${escapeHtml(label)}</span></span>`;
  return `<button type="button" class="chat-md-citation ${className}" title="${escapeHtml(title || label)}"${extraAttrs}>${iconHtml}${labelHtml}</button>`;
}

export function resolveCodexLinkMeta(rawHref) {
  const href = (rawHref || '').toString().trim();
  if (!href) return null;
  const skillMatch = href.match(SKILL_LINK_RE);
  if (skillMatch) {
    return {
      className: 'chat-md-link chat-md-citation chat-md-skill-chip',
      title: href,
      dataAttrs: {
        'data-citation-kind': 'skill',
        'data-skill-id': (skillMatch[1] || '').toString(),
        'data-skill-href': href,
      },
    };
  }
  const conversationMatch = href.match(CONVERSATION_LINK_RE);
  if (conversationMatch) {
    return {
      className: 'chat-md-link chat-md-citation chat-md-conversation-chip',
      title: href,
      dataAttrs: {
        'data-citation-kind': 'conversation',
        'data-conversation-id': (conversationMatch[1] || '').toString(),
      },
    };
  }
  if (SKILL_FILE_RE.test(href)) {
    return {
      className: 'chat-md-link chat-md-citation chat-md-skill-chip chat-md-skill-file',
      title: href,
      dataAttrs: {
        'data-citation-kind': 'skill',
        'data-skill-path': href,
        'data-skill-name': deriveSkillNameFromPath(href),
      },
    };
  }
  return null;
}

export function renderTaskStubCard(payload) {
  const attrs = payload?.attrs || {};
  const title = (attrs.title || payload?.label || 'Task').toString().trim() || 'Task';
  const prompt = (payload?.label || '').toString().trim();
  return renderInlineBadge({
    className: 'chat-md-task-stub',
    icon: '✦',
    label: title,
    title: prompt && prompt !== title ? `${title}: ${prompt}` : title,
    dataAttrs: {
      'data-citation-kind': 'task',
      'data-task-title': title,
      'data-task-prompt': prompt,
    },
  });
}

export function renderAutomationDirectiveCard(payload) {
  const attrs = payload?.attrs || {};
  const title = (attrs.name || payload?.label || 'Automation').toString().trim() || 'Automation';
  const prompt = (attrs.prompt || payload?.label || '').toString().trim();
  return renderInlineBadge({
    className: 'chat-md-automation-update',
    icon: '⚙',
    label: title,
    title: prompt && prompt !== title ? `${title}: ${prompt}` : title,
    dataAttrs: {
      'data-citation-kind': 'automation-update',
      'data-message': (payload?.label || '').toString(),
      'data-automation-id': (attrs.id || '').toString(),
      'data-automation-name': title,
      'data-automation-prompt': prompt,
    },
  });
}

export function renderCodeCommentDirectiveCard(payload) {
  const attrs = payload?.attrs || {};
  const path = (attrs.path || '').toString().trim();
  const startLine = toPositiveInt(attrs.line_range_start);
  const endLine = toPositiveInt(attrs.line_range_end) || startLine;
  const location = formatLineRange(startLine, endLine);
  const title = (attrs.title || 'Code comment').toString().trim() || 'Code comment';
  let fileMeta = '';
  if (path) {
    const locationSuffix = location ? ` line ${location}` : '';
    fileMeta = `${basename(path)}${locationSuffix}`;
  }
  const displayLabel = fileMeta ? `${title} · ${fileMeta}` : title;
  return renderInlineBadge({
    className: 'chat-md-code-comment',
    icon: '💬',
    label: displayLabel,
    title: (payload?.label || title).toString().trim() || title,
    dataAttrs: {
      'data-citation-kind': 'code-comment',
      'data-message': (payload?.label || '').toString(),
      'data-comment-title': (attrs.title || '').toString(),
      'data-file-path': path,
      'data-line-start': attrs.line_range_start,
      'data-line-end': attrs.line_range_end,
      'data-comment-priority': (attrs.priority || '').toString(),
    },
  });
}
