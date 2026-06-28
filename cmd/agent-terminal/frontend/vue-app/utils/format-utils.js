/**
 * 从 UnifiedChatPage.js setup() 内提取的纯格式化函数。
 * 签名/行为与原始实现保持一致。
 */

/**
 * @param {string | number | Date | null | undefined} ts
 * @returns {string}
 */
export function formatTimelineTime(ts) {
  const raw = (ts || '').toString().trim();
  if (!raw) return '';
  const date = new Date(raw);
  if (Number.isNaN(date.getTime())) return '';
  return date.toLocaleTimeString([], {
    hour: '2-digit',
    minute: '2-digit',
  });
}

/**
 * @param {unknown} value
 * @returns {string}
 */
export function normalizeActivityOutput(value) {
  const text = (value || '').toString();
  if (!text.trim()) return '';
  const maxLen = 420;
  if (text.length <= maxLen) return text;
  return `${text.slice(0, maxLen)}\n...[truncated]`;
}

export function displayToolName(value) {
  const raw = (value || '').toString().trim();
  if (!raw) return '未知工具';
  const normalized = normalizeToolNameToken(raw)
    .replace(/[./:-]+/g, '_')
    .replace(/^functions_+/, '')
    .replace(/^function_+/, '')
    .replace(/^tools_+/, '')
    .replace(/^tool_+/, '')
    .replace(/_+/g, '_')
    .replace(/^_+|_+$/g, '');
  return canonicalLspToolName(normalized) || raw;
}

function normalizeToolNameToken(raw) {
  const text = (raw || '').toString().trim();
  if (!/^mcp__/i.test(text)) return text;
  const parts = text.split('__');
  if (parts.length < 3) return text;
  return parts.slice(2).join('__');
}

function canonicalLspToolName(name) {
  const key = (name || '').toString().toLowerCase();
  return ({
    lsp_file: 'file',
    lsp_grep: 'grep',
    lsp_inspect: 'inspect',
    lsp_xref: 'xref',
    lsp_structure: 'structure',
    lsp_edit: 'edit',
    lsp_completion: 'completion',
    lsp_format_preview: 'format_preview',
  })[key] || name;
}

function parsedToolResult(preview) {
  if (Array.isArray(preview)) return preview;
  if (preview && typeof preview === 'object' && !Array.isArray(preview)) return preview;
  const text = (preview || '').toString().trim();
  if (!text || !/^[{[]/.test(text)) return null;
  try {
    return JSON.parse(text);
  } catch {
    return null;
  }
}

function structuredToolResult(result) {
  if (!result || typeof result !== 'object' || Array.isArray(result)) return null;
  const structured = result.structuredContent;
  if (!structured) return null;
  if (typeof structured === 'object') return structured;
  if (typeof structured !== 'string') return null;
  const text = structured.trim();
  if (!/^[{[]/.test(text)) return null;
  try {
    return JSON.parse(text);
  } catch {
    return null;
  }
}

function previewText(value) {
  if (value == null) return '';
  if (typeof value === 'string') return value.trim();
  try {
    return JSON.stringify(value);
  } catch {
    return String(value).trim();
  }
}

function toolActivityDetailValue(item = {}) {
  for (const key of ['preview', 'output', 'result', 'argumentsPreview', 'arguments_preview', 'error']) {
    const value = item?.[key];
    if (value == null) continue;
    const text = previewText(value);
    if (text) return { key, value, text };
  }
  return { key: '', value: '', text: '' };
}

export function toolActivityDetail(item = {}) {
  return explicitToolErrorDetail(item).text || toolActivityDetailValue(item).text;
}

function explicitToolErrorDetail(item = {}) {
  const value = item?.error;
  if (value == null) return { key: '', value: '', text: '' };
  const text = previewText(value);
  if (!text) return { key: '', value: '', text: '' };
  return { key: 'error', value, text };
}

function toolResultText(result, preview, keys) {
  if (result && typeof result === 'object') {
    for (const key of keys) {
      const text = previewText(result[key]);
      if (text) return text;
    }
  }
  return previewText(preview);
}

function toolPayloadFailed(result) {
  if (!result || typeof result !== 'object' || Array.isArray(result)) return false;
  if (result.success === false) return true;
  if (result.isError === true || String(result.isError).trim().toLowerCase() === 'true') return true;
  if (previewText(result.error)) return true;
  const errorCode = previewText(result.error_code).toLowerCase();
  return Boolean(errorCode && errorCode !== 'none');
}

function formatEditCountText(result) {
  const count = Number(result?.text_edit_count ?? result?.applied_count);
  if (!Number.isFinite(count)) return '';
  return `${Math.trunc(count)} 处改动`;
}

function formatPreviewSummary(result, failed, preview) {
  if (failed) return knownToolFailureSummary('预览格式化失败', result, preview);
  const count = Number(result?.text_edit_count);
  if (Number.isFinite(count)) {
    return count > 0 ? `预览到 ${Math.trunc(count)} 处格式化改动` : '无需格式化';
  }
  return '已预览格式化';
}

function editToolSummary(failed, result, preview) {
  const action = previewText(result?.action).toLowerCase();
  if (action === 'format') {
    if (failed) return knownToolFailureSummary('格式化文件失败', result, preview);
    const countText = formatEditCountText(result);
    return countText ? `已应用格式化（${countText}）` : '已应用格式化';
  }
  return failed ? knownToolFailureSummary('编辑文件失败', result, preview) : '已替换文件内容';
}

function knownToolFailureSummary(label, result, preview) {
  return `${label}${toolFailureSuffix(result, preview)}`;
}

function knownToolSummary(name, failed, result, preview) {
  if (result?.error_code === 'result_too_large') {
    const hint = result?.hint || '请缩小范围';
    return `结果过大（${hint}）`;
  }
  if (name === 'edit') return editToolSummary(failed, result, preview);
  if (name === 'format_preview') return formatPreviewSummary(result, failed, preview);
  if (name === 'file') return failed ? knownToolFailureSummary('读取文件失败', result, preview) : '已读取文件';
  if (name === 'grep') {
    if (failed) return knownToolFailureSummary('搜索代码失败', result, preview);
    const total = Number(result?.total ?? result?.summary?.total ?? result?.count);
    if (Number.isFinite(total)) return total > 0 ? `搜索到 ${Math.trunc(total)} 处` : '搜索无结果';
    return '已搜索代码';
  }
  if (name === 'inspect') return failed ? knownToolFailureSummary('查看类型信息失败', result, preview) : '已查看类型信息';
  if (name === 'xref') {
    if (failed) return knownToolFailureSummary('查找引用失败', result, preview);
    const total = Number(result?.total ?? result?.summary?.total ?? result?.count);
    if (Number.isFinite(total)) return total > 0 ? `找到 ${Math.trunc(total)} 处引用` : '未找到引用';
    return '已查找引用';
  }
  if (name === 'structure') {
    if (failed) return knownToolFailureSummary('获取文档结构失败', result, preview);
    const count = Array.isArray(result) ? result.length : Number(result?.total);
    if (Number.isFinite(count)) return `获取到 ${Math.trunc(count)} 个符号`;
    return '已获取文档结构';
  }
  if (name === 'completion') {
    if (failed) return knownToolFailureSummary('获取补全失败', result, preview);
    const count = Array.isArray(result) ? result.length : Number(result?.total);
    if (Number.isFinite(count)) return `${Math.trunc(count)} 条补全建议`;
    return '已获取补全建议';
  }
  if (name === 'go_run' || name === 'exec_command') return failed ? `命令执行失败${toolFailureSuffix(result, preview)}` : '命令执行成功';

  if (name.startsWith('orchestration_launch') || name === 'spawn_agent') return failed ? '启动 Agent 失败' : '已启动 Agent';
  if (name.startsWith('orchestration_send') || name === 'send_input') return failed ? '发送消息失败' : '已发送消息';
  if (name.startsWith('workspace_merge')) return failed ? '合并工作区失败' : '已合并工作区';
  if (name.startsWith('workspace_create')) return failed ? '创建工作区失败' : '已创建工作区';
  if (name.startsWith('browser_click') || name.endsWith('_click')) return failed ? '点击页面失败' : '已点击页面';
  if (name === 'ToolSearch') return failed ? '查找工具失败' : '已查找工具';
  return '';
}

function toolFailureSuffix(result, preview) {
  const text = toolResultText(result, preview, ['error', 'output', 'message', 'result']);
  return text ? `：${normalizeActivityOutput(text).replace(/\n/g, ' ')}` : '';
}

export function summarizeToolActivity(toolName, item = {}) {
  const name = displayToolName(toolName);
  const status = (item?.status || '').toString().trim().toLowerCase();
  const detail = toolActivityDetailValue(item);
  const errorDetail = explicitToolErrorDetail(item);
  const preview = errorDetail.text || detail.text;
  const explicitFailed = item?.success === false ||
    status === 'failed' ||
    status === 'error' ||
    Boolean(errorDetail.text) ||
    (detail.key === 'error' && Boolean(preview));
  if (status === 'running' && !explicitFailed) return { name, summary: '执行中', status: 'active' };

  const parsed = parsedToolResult(detail.value);
  const result = structuredToolResult(parsed) || parsed;
  const failed = explicitFailed ||
    toolPayloadFailed(parsed) ||
    toolPayloadFailed(result);

  const known = knownToolSummary(name, failed, result, preview);
  if (known) return { name, summary: known, status: failed ? 'failed' : 'done' };
  if (failed) return { name, summary: `执行失败${toolFailureSuffix(result, preview)}`, status: 'failed' };
  return { name, summary: '已完成', status: 'done' };
}

export function formatTokenCompact(value) {
  const number = Number(value);
  if (!Number.isFinite(number) || number < 0) return '0';
  if (number >= 1_000_000) return `${(number / 1_000_000).toFixed(1).replace(/\\.0$/, '')}m`;
  if (number >= 1_000) return `${(number / 1_000).toFixed(1).replace(/\\.0$/, '')}k`;
  return `${Math.round(number)}`;
}

export function formatTokenPercent(value) {
  const number = Number(value);
  if (!Number.isFinite(number)) return '';
  const clamped = Math.max(0, Math.min(100, number));
  return `${Math.round(clamped)}%`;
}

export function formatTokenInline(usage) {
  if (!usage || typeof usage !== 'object') return '';
  let used = Number(usage.usedTokens);
  const limit = Number(usage.contextWindowTokens);
  if (!Number.isFinite(used) || used < 0) used = 0;
  if (used === 0 && !(Number.isFinite(limit) && limit > 0)) {
    // tokenUsage 对象存在但数据尚未填充 — 显示占位符而非完全隐藏
    if (usage.updatedAt) return '—';
    return '';
  }
  if (Number.isFinite(limit) && limit > 0) {
    const usedPercent = Number.isFinite(Number(usage.usedPercent))
      ? Number(usage.usedPercent)
      : (used / limit) * 100;
    return `${formatTokenPercent(usedPercent)} · ${formatTokenCompact(used)} / ${formatTokenCompact(limit)}`;
  }
  return `${formatTokenCompact(used)}`;
}

export function formatTokenTooltip(usage) {
  if (!usage || typeof usage !== 'object') return '';
  let used = Number(usage.usedTokens);
  const limit = Number(usage.contextWindowTokens);
  if (!Number.isFinite(used) || used < 0) used = 0;
  if (used === 0 && !(Number.isFinite(limit) && limit > 0)) return '';
  if (Number.isFinite(limit) && limit > 0) {
    const usedPercent = Number.isFinite(Number(usage.usedPercent))
      ? Number(usage.usedPercent)
      : (used / limit) * 100;
    const leftPercent = 100 - usedPercent;
    return [
      'Context window:',
      `${formatTokenPercent(usedPercent)} used (${formatTokenPercent(leftPercent)} left)`,
      `${formatTokenCompact(used)} / ${formatTokenCompact(limit)} tokens used`,
    ].join('\n');
  }
  return [
    'Context window:',
    `${formatTokenCompact(used)} tokens used`,
  ].join('\n');
}

/**
 * 默认上下文用量警报阈值（百分比）。
 * 顺序固定：warn → danger → critical。
 * 后续若需用户可配，从 settings (`contextUsageAlerts.thresholds`) 读出后传给 getTokenLevelFromPercent。
 */
export const DEFAULT_CONTEXT_USAGE_THRESHOLDS = Object.freeze([70, 85, 95]);

/**
 * 把上下文使用百分比映射到告警等级。
 * @param {number} percent 0~100 的使用百分比
 * @param {ReadonlyArray<number>} [thresholds] 升序的 3 档阈值；非法或缺省时回退默认
 * @returns {'normal'|'warn'|'danger'|'critical'}
 */
export function getTokenLevelFromPercent(percent, thresholds) {
  const pct = Number(percent);
  if (!Number.isFinite(pct) || pct <= 0) return 'normal';
  const candidate = Array.isArray(thresholds) && thresholds.length
    ? thresholds.map((value) => Number(value)).filter((value) => Number.isFinite(value) && value > 0)
    : [];
  const normalized = (candidate.length >= 1 ? candidate : DEFAULT_CONTEXT_USAGE_THRESHOLDS.slice())
    .slice(0, 3)
    .sort((a, b) => a - b);
  if (normalized.length === 0) return 'normal';
  if (normalized.length >= 3 && pct >= normalized[2]) return 'critical';
  if (normalized.length >= 2 && pct >= normalized[1]) return 'danger';
  if (pct >= normalized[0]) return 'warn';
  return 'normal';
}

/**
 * 从 tokenUsage 对象推导告警等级。优先用 usedPercent；否则用 usedTokens / contextWindowTokens 算。
 * @param {{usedPercent?: number, usedTokens?: number, contextWindowTokens?: number} | null | undefined} usage
 * @param {ReadonlyArray<number>} [thresholds]
 * @returns {'normal'|'warn'|'danger'|'critical'}
 */
export function getTokenLevel(usage, thresholds) {
  if (!usage || typeof usage !== 'object') return 'normal';
  const usedPercent = Number(usage.usedPercent);
  if (Number.isFinite(usedPercent) && usedPercent > 0) {
    return getTokenLevelFromPercent(usedPercent, thresholds);
  }
  const used = Number(usage.usedTokens);
  const limit = Number(usage.contextWindowTokens);
  if (!Number.isFinite(used) || used <= 0) return 'normal';
  if (!Number.isFinite(limit) || limit <= 0) return 'normal';
  return getTokenLevelFromPercent((used / limit) * 100, thresholds);
}

export function formatElapsedCompact(elapsedSeconds) {
  const seconds = Math.max(0, Math.floor(Number(elapsedSeconds) || 0));
  if (seconds < 60) return `${seconds}s`;
  if (seconds < 3600) {
    const minutes = Math.floor(seconds / 60);
    const sec = seconds % 60;
    return `${minutes}m ${sec.toString().padStart(2, '0')}s`;
  }
  const hours = Math.floor(seconds / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  const sec = seconds % 60;
  return `${hours}h ${minutes.toString().padStart(2, '0')}m ${sec.toString().padStart(2, '0')}s`;
}
