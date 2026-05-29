// @ts-nocheck
// 通用「摘要 → seed instructions」工具。
// 被 useForkThread 用于普通对话继承摘要。
//
// 设计取舍：
// - 纯函数，无 side effect，便于单测。
// - Phase 2a 摘要源是「前一段对话原文截断」，质量一般但稳定；Phase 2b 后端
//   `thread/summarize` RPC 上线后，调用方只需把 LLM 摘要文本传进来即可，函数本身不变。

// fork 摘要总字数：从 2400 提到 4000，给 tool output / assistant 结论 / TodoWrite 内容
// 装得下更多。Phase 2a 截断式仍是兜底，Phase 2b LLM 摘要上线后这个常量可调小。
const DEFAULT_SUMMARY_LIMIT = 4000;

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

// 单个字段最多占多长。两档：
// - PER_ITEM_FIELD_LIMIT 280：用于短字段（tool name / file 路径 / status 这类 label 性内容）
// - LONG_FIELD_LIMIT 600：用于 tool output / command output / assistant 文本 / plan
//   text / TodoWrite 内容这类「实质信息」字段；280 太抠会让 agent 看到的全是空壳。
const PER_ITEM_FIELD_LIMIT = 280;
const LONG_FIELD_LIMIT = 600;

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
    // 注：截至本次改动，后端 PatchTimelineItem.Output 字段没有任何 producer 填——
    // internal/module/uistate/timeline/projector_parity.go 只写 Preview = previewText(ev.Result)。
    // 所以生产里实际生效的是 preview 这一支；output 作为未来 producer 填结构化结果时
    // 的备用（如果填了会优先用，因为更接近原始数据）。file 路径作为最后兜底。
    // LONG_FIELD_LIMIT 600 让 preview 不再被旧 280 狠截，agent 能看到完整工具结果片段。
    const detail = clipField(item.output, LONG_FIELD_LIMIT)
      || clipField(item.preview, LONG_FIELD_LIMIT)
      || clipField(item.file);
    const status = item.status === 'failed' ? ' 失败' : '';
    return detail ? `[tool${status}] ${tool} · ${detail}` : `[tool${status}] ${tool}`;
  }
  if (kind === 'command') {
    const cmd = clipField(item.command, 96) || '未知命令';
    const exit = Number.isFinite(Number(item.exitCode)) ? ` (exit=${Number(item.exitCode)})` : '';
    const out = clipField(item.output, LONG_FIELD_LIMIT);
    const status = item.status === 'failed' ? ' 失败' : '';
    return out ? `[cmd${status}] $ ${cmd}${exit} → ${out}` : `[cmd${status}] $ ${cmd}${exit}`;
  }
  if (kind === 'file' || kind === 'file_ref') {
    const file = clipField(item.file || item.path, 160) || '未知文件';
    const status = item.status === 'failed' ? ' 失败' : '';
    return `[file${status}] ${file}`;
  }
  if (kind === 'plan') {
    // plan kind 包含 TodoWrite 整个清单 / 阶段性结论文本，必须放宽到 LONG_FIELD_LIMIT
    // 否则 agent 只能看到 TodoWrite 头一句话。
    const planText = clipField(item.text || item.content, LONG_FIELD_LIMIT);
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
  // 用户诉求和 assistant 结论是承接关键，必须放宽到 LONG_FIELD_LIMIT。
  const text = clipField(item.text || item.content, LONG_FIELD_LIMIT);
  if (!text) return '';
  return `[${tag}] ${text}`;
}

// 进度段最少需要的 timeline 长度。短 timeline（<5）通常没什么工具风暴，
// 主摘要本身就涵盖一切，没必要再拼一段。
const PROGRESS_MIN_TIMELINE = 5;
// 进度段在 timeline 尾部的扫描窗口。再往前的内容已不算「最近」。
const PROGRESS_TAIL_SCAN = 40;
// 进度段总字数上限，防止三个字段都顶满时尾巴过长。
const PROGRESS_SECTION_LIMIT = 2000;

function findLastMatch(items, predicate) {
  for (let i = items.length - 1; i >= 0; i--) {
    if (predicate(items[i])) return items[i];
  }
  return null;
}

// 真实 timeline item 用 `kind` 标识用户/助手；早期 fixture 用过 `role`。两者都兼容，
// 避免 fixture 与生产数据形状脱钩导致进展段抽取静默失效（见 07e3220 回归）。
function itemRoleOrKind(item) {
  if (!item || typeof item !== 'object') return '';
  return ((item.role || item.kind || '') + '').toString().trim().toLowerCase();
}

/**
 * 在主摘要之外额外抽出「最近进展」段，作为独立锚点贴在末尾。
 * 与 main 摘要的「最近 N 条」解耦：即便尾部全是 tool/command 风暴，
 * 这里仍能保留：
 *   - 最近 1 条 user 消息（任务诉求）
 *   - 最近 1 条有实质内容的 assistant 文本（当前思路 / 阶段性结论）
 *   - 最近 ≤3 条 plan（如果有）
 * 三个字段独立 clip，互不挤压。
 * @param {Array<object>} items
 * @returns {string}
 */
function extractProgressSection(items) {
  if (!Array.isArray(items) || items.length < PROGRESS_MIN_TIMELINE) return '';
  const tail = items.slice(-PROGRESS_TAIL_SCAN);

  const lastUser = findLastMatch(tail, (it) => {
    if (itemRoleOrKind(it) !== 'user') return false;
    return Boolean(clipField(it.text || it.content));
  });
  const lastAssistant = findLastMatch(tail, (it) => {
    if (itemRoleOrKind(it) !== 'assistant') return false;
    // 长度门槛过滤掉「好的」「明白了」这类没有进度信息的短答复。
    const text = clipField(it.text || it.content, 600);
    return text.length >= 40;
  });
  const lastPlans = tail
    .filter((it) => it && itemRoleOrKind(it) === 'plan')
    .slice(-3);

  const lines = [];
  if (lastUser) {
    lines.push(`• 最近用户诉求：${clipField(lastUser.text || lastUser.content, 400)}`);
  }
  if (lastAssistant) {
    lines.push(`• 助手当前思路：${clipField(lastAssistant.text || lastAssistant.content, 600)}`);
  }
  for (const plan of lastPlans) {
    const text = clipField(plan.text || plan.content, 300);
    if (!text) continue;
    const flag = plan.done ? '已完成' : '进行中';
    lines.push(`• 进度【${flag}】：${text}`);
  }
  return truncateSummaryText(lines.join('\n'), PROGRESS_SECTION_LIMIT);
}

/**
 * 把 thread timeline 抽成纯文本摘要（兜底版，截断式）。
 * 选取策略：
 *   - 最早 1 条（锐化任务起点）
 *   - 所有 plan 节点（进度心智）
 *   - 最近 N 条（近期上下文）
 * 三类去重、按出现顺序拼接，再按总字数截断。
 * 末尾追加「最近进展」锚点段（仅当 timeline ≥ 5 条），与主摘要解耦避免被工具风暴挤掉。
 * @param {Array<object>} timelineItems
 * @param {{ recentCount?: number, charLimit?: number }} [opts]
 * @returns {string}
 */
// fork 摘要场景特化的 truncate：超额时保留尾部（picked 已按 [首条, 全 plan, 尾 N]
// 排序，尾部是最近内容，对承接最有价值）。前缀 '…' 提示有截断。
// 普通继承摘要仍用通用 truncateSummaryText（保留首部）。
// review P1 #3 修复：之前 truncateSummaryText 一刀切保留首部，超 4000 时把
// 「尾 12」最近内容截掉，跟 fork 意图相反。
function clipPickedKeepTail(text, limit) {
  if (!text || text.length <= limit) return text || '';
  return '…' + text.slice(text.length - limit + 1);
}

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
    if (itemRoleOrKind(item) === 'plan') tryAdd(item);
  }
  // 最近 N 条
  const tail = items.slice(Math.max(0, items.length - recentCount));
  for (const item of tail) tryAdd(item);

  const main = clipPickedKeepTail(picked.join('\n\n'), charLimit);
  const progress = extractProgressSection(items);
  return progress ? `${main}\n\n## 最近进展\n${progress}` : main;
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
