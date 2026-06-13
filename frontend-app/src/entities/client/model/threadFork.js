export const FORK_KICKOFF_PROMPT = '请基于上文摘要，简要总结上次进展并提出下一步建议。';

const DEFAULT_SUMMARY_LIMIT = 4000;
const PER_ITEM_FIELD_LIMIT = 280;
const LONG_FIELD_LIMIT = 600;

/*
 * 这里生成继承会话的背景说明，不生成用户可见消息。
 * 摘要取首条、plan 和最近消息，shared files 另外附上。
 */

function textValue(value) {
  return (value || '').toString().trim();
}

function clipField(value, limit = PER_ITEM_FIELD_LIMIT) {
  const text = textValue(value).replace(/\s+/g, ' ').trim();
  if (!text) return '';
  return text.length > limit ? `${text.slice(0, limit)}...` : text;
}

function itemRoleOrKind(item) {
  if (!item || typeof item !== 'object') return '';
  return textValue(item.role || item.kind || item.type || item.eventType || item.event_type).toLowerCase();
}

function itemToLine(item) {
  if (!item || typeof item !== 'object') return '';
  const kind = itemRoleOrKind(item);
  if (kind === 'tool') {
    const tool = clipField(item.tool || item.toolName || item.name, 56) || '未知工具';
    const detail = clipField(item.output || item.preview || item.result || item.file || item.path, LONG_FIELD_LIMIT);
    const status = item.status === 'failed' || item.status === 'error' ? ' 失败' : '';
    return detail ? `[tool${status}] ${tool} · ${detail}` : `[tool${status}] ${tool}`;
  }
  if (kind === 'command') {
    const command = clipField(item.command, 96) || '未知命令';
    const output = clipField(item.output || item.preview, LONG_FIELD_LIMIT);
    return output ? `[cmd] $ ${command} -> ${output}` : `[cmd] $ ${command}`;
  }
  if (kind === 'file' || kind === 'file_ref') {
    const file = clipField(item.file || item.path, 160) || '未知文件';
    return `[file] ${file}`;
  }
  if (kind === 'plan') {
    const text = clipField(item.text || item.content || item.summary, LONG_FIELD_LIMIT);
    if (!text) return '';
    return `[plan${item.done ? ' 完成' : ' 进行中'}] ${text}`;
  }
  const text = clipField(item.text || item.content || item.message || item.summary || item.preview, LONG_FIELD_LIMIT);
  return text ? `[${kind || 'msg'}] ${text}` : '';
}

function truncateKeepTail(text, limit = DEFAULT_SUMMARY_LIMIT) {
  const value = textValue(text);
  if (!value || value.length <= limit) return value;
  return `...${value.slice(value.length - limit + 3)}`;
}

export function extractTimelineSummary(timelineItems, opts = {}) {
  const recentCount = Number.isFinite(Number(opts.recentCount)) ? Number(opts.recentCount) : 12;
  const charLimit = Number.isFinite(Number(opts.charLimit)) ? Number(opts.charLimit) : DEFAULT_SUMMARY_LIMIT;
  const items = Array.isArray(timelineItems) ? timelineItems : [];
  if (items.length === 0) return '';

  const picked = [];
  const seenIds = new Set();
  const seenLines = new Set();
  const addItem = (item) => {
    if (!item || typeof item !== 'object') return;
    const id = textValue(item.id || item.messageId || item.message_id);
    if (id) {
      if (seenIds.has(id)) return;
      seenIds.add(id);
    }
    const line = itemToLine(item);
    if (!line || seenLines.has(line)) return;
    seenLines.add(line);
    picked.push(line);
  };

  addItem(items[0]);
  for (const item of items) {
    if (itemRoleOrKind(item) === 'plan') addItem(item);
  }
  for (const item of items.slice(Math.max(0, items.length - recentCount))) {
    addItem(item);
  }

  return truncateKeepTail(picked.join('\n\n'), charLimit);
}

export function buildSeedInstructionsFromSummary(summary, options = {}) {
  const sourceTitle = textValue(options.sourceTitle) || '前一个对话';
  const intro = textValue(options.intro) || '以下是前一个对话的摘要，可作为当前新对话的背景参考。';
  const limit = Number.isFinite(Number(options.limit)) ? Number(options.limit) : DEFAULT_SUMMARY_LIMIT;
  const summaryText = truncateKeepTail(summary, limit);
  const sharedBlocks = (Array.isArray(options.sharedFiles) ? options.sharedFiles : [])
    .map((file) => {
      const path = textValue(file?.path);
      const content = textValue(file?.content);
      if (!path || !content) return '';
      return [`共享文件：${path}`, '````', content, '````'].join('\n');
    })
    .filter(Boolean);

  if (!summaryText && sharedBlocks.length === 0) return '';

  const lines = [
    intro,
    '如果后续用户消息提出了新的目标，请以新的用户消息为准。',
    '',
    `来源：${sourceTitle}`,
  ];
  if (summaryText) {
    lines.push('', '摘要：', summaryText);
  }
  if (sharedBlocks.length > 0) {
    lines.push('', '挂载的共享文件（已在数据库中持久化，可作为权威背景）：', '', sharedBlocks.join('\n\n'));
  }
  return lines.join('\n');
}
