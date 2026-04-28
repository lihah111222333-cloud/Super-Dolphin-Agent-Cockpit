// @ts-nocheck
// 通用「摘要 → seed instructions」工具。
// 既被 useTaskHandoff（task 接力）使用，也被 useForkThread（普通对话继承）使用。
//
// 设计取舍：
// - 纯函数，无 side effect，便于单测。
// - Phase 2a 摘要源是「前一段对话原文截断」，质量一般但稳定；Phase 2b 后端
//   `thread/summarize` RPC 上线后，调用方只需把 LLM 摘要文本传进来即可，函数本身不变。

const DEFAULT_SUMMARY_LIMIT = 2400;

/**
 * 把任意原始内容截断到指定字符数，加省略号；空内容返回空串。
 * @param {string} raw
 * @param {number} [limit]
 * @returns {string}
 */
export function truncateSummaryText(raw, limit = DEFAULT_SUMMARY_LIMIT) {
  const text = (raw || '').toString().trim();
  if (!text) return '';
  return text.length > limit ? `${text.slice(0, limit)}…` : text;
}

// 单个 item 内部字段最多占多长（避免单条 tool / command output 吃掉整个摘要预算）。
const PER_ITEM_FIELD_LIMIT = 280;

function clipField(value, limit = PER_ITEM_FIELD_LIMIT) {
  const text = (value || '').toString().replace(/\s+/g, ' ').trim();
  if (!text) return '';
  return text.length > limit ? `${text.slice(0, limit)}…` : text;
}

/**
 * 把一个 timeline item 抽成一行文本。覆盖常见 kind：
 * - user / assistant / system / thinking：拼role + text
 * - tool：拼工具名 + preview / file + status
 * - command：拼命令 + exitCode + 截断输出
 * - file：拼文件路径 + status
 * - plan：拼 plan 文本 + 完成标记
 * - approval / file_ref：尽量从可用字段重拼
 * 返回 '' 表示不加入摘要。
 */
function itemToLine(item) {
  if (!item || typeof item !== 'object') return '';
  const kind = (item.kind || '').toString().trim().toLowerCase();
  const role = (item.role || '').toString().trim().toLowerCase();
  const tag = (role || kind || 'msg').toLowerCase();

  // 优先走特定 kind 的抽取，其次才是通用 text/content。
  if (kind === 'tool') {
    const tool = clipField(item.tool, 56) || '未知工具';
    const detail = clipField(item.preview) || clipField(item.file);
    const status = item.status === 'failed' ? ' 失败' : '';
    return detail ? `[tool${status}] ${tool} · ${detail}` : `[tool${status}] ${tool}`;
  }
  if (kind === 'command') {
    const cmd = clipField(item.command, 96) || '未知命令';
    const exit = Number.isFinite(Number(item.exitCode)) ? ` (exit=${Number(item.exitCode)})` : '';
    const out = clipField(item.output);
    const status = item.status === 'failed' ? ' 失败' : '';
    return out ? `[cmd${status}] $ ${cmd}${exit} → ${out}` : `[cmd${status}] $ ${cmd}${exit}`;
  }
  if (kind === 'file' || kind === 'file_ref') {
    const file = clipField(item.file || item.path, 160) || '未知文件';
    const status = item.status === 'failed' ? ' 失败' : '';
    return `[file${status}] ${file}`;
  }
  if (kind === 'plan') {
    const planText = clipField(item.text || item.content);
    if (!planText) return '';
    const flag = item.done ? ' 完成' : ' 进行中';
    return `[plan${flag}] ${planText}`;
  }
  if (kind === 'approval') {
    const what = clipField(item.text || item.summary || item.title);
    return what ? `[approval] ${what}` : '';
  }
  if (kind === 'thinking') {
    const text = clipField(item.text || item.content);
    return text ? `[thinking] ${text}` : '';
  }

  // 通用 fallback：user / assistant / system / 其他 拼 text/content。
  const text = clipField(item.text || item.content);
  if (!text) return '';
  return `[${tag}] ${text}`;
}

/**
 * 把 thread timeline 抽成纯文本摘要（兜底版，截断式）。
 * 选取策略：
 *   - 最早 1 条（锐化任务起点）
 *   - 所有 plan 节点（进度心智）
 *   - 最近 N 条（近期上下文）
 * 后者三类去重、按出现顺序拼接，再按总字数截断。
 * @param {Array<object>} timelineItems
 * @param {{ recentCount?: number, charLimit?: number }} [opts]
 * @returns {string}
 */
export function extractTimelineSummary(timelineItems, opts = {}) {
  const { recentCount = 12, charLimit = DEFAULT_SUMMARY_LIMIT } = opts;
  const items = Array.isArray(timelineItems) ? timelineItems : [];
  if (items.length === 0) return '';

  // 去重优先用 id，其次用 item 引用，其次用产出行文本。三道保证同一项不会重复加入。
  const seenIds = new Set();
  const seenItems = new Set();
  const seenLines = new Set();
  const picked = [];
  function tryAdd(item) {
    if (!item || typeof item !== 'object') return;
    if (seenItems.has(item)) return;
    const id = (item.id || '').toString();
    if (id) {
      if (seenIds.has(id)) return;
      seenIds.add(id);
    }
    seenItems.add(item);
    const line = itemToLine(item);
    if (!line) return;
    if (seenLines.has(line)) return;
    seenLines.add(line);
    picked.push(line);
  }

  // 最早 1 条
  tryAdd(items[0]);
  // 所有 plan 节点
  for (const item of items) {
    if ((item?.kind || '').toString().toLowerCase() === 'plan') tryAdd(item);
  }
  // 最近 N 条
  const tail = items.slice(Math.max(0, items.length - recentCount));
  for (const item of tail) tryAdd(item);

  return truncateSummaryText(picked.join('\n\n'), charLimit);
}

/**
 * 用摘要 + 可选共享文件构造新会话的 base_instructions 文本。
 * @param {string} summary 原始摘要（任意来源）
 * @param {{
 *   sourceTitle?: string,                 // 摘要来源的标题，例如 "前一个对话: foo"
 *   limit?: number,                        // 摘要字符上限
 *   sharedFiles?: Array<{ path: string, content: string }>, // 已挂载的共享文件
 *   intro?: string,                        // 替换默认开场白
 * }} [options]
 * @returns {string}
 */
export function buildSeedInstructionsFromSummary(summary, options = {}) {
  const {
    sourceTitle = '前一个对话',
    limit = DEFAULT_SUMMARY_LIMIT,
    sharedFiles = [],
    intro = '以下是前一个对话的摘要，可作为当前新对话的背景参考。',
  } = options;

  const summaryText = truncateSummaryText(summary, limit);
  // 共享文件内容本身可能含三反引号（markdown / 代码块常见），这里用四个反引号作为外层围栏，
  // 避免被内部三反引号提前闭合。如果内容本身含四反引号还是会冲突（极小概率），后续 Phase 2b LLM 摘要上线后不再需要外层 fence。
  const sharedBlocks = (Array.isArray(sharedFiles) ? sharedFiles : [])
    .map((file) => {
      const path = (file?.path || '').toString().trim();
      const content = (file?.content || '').toString().trim();
      if (!path || !content) return '';
      return [`共享文件：${path}`, '````', content, '````'].join('\n');
    })
    .filter(Boolean);

  if (!summaryText && sharedBlocks.length === 0) return '';

  const lines = [intro];
  lines.push('如果后续用户消息提出了新的目标，请以新的用户消息为准。');
  lines.push('');
  lines.push(`来源：${sourceTitle}`);
  if (summaryText) {
    lines.push('');
    lines.push('摘要：');
    lines.push(summaryText);
  }
  if (sharedBlocks.length > 0) {
    lines.push('');
    lines.push('挂载的共享文件（已在数据库中持久化，可作为权威背景）：');
    lines.push('');
    lines.push(sharedBlocks.join('\n\n'));
  }
  return lines.join('\n');
}
