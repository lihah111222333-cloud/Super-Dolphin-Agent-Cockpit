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
  const normalized = raw
    .replace(/[./:-]+/g, '_')
    .replace(/^functions_+/, '')
    .replace(/^function_+/, '')
    .replace(/^tools_+/, '')
    .replace(/^tool_+/, '')
    .replace(/^mcp_+[a-z0-9]+_+/i, '')
    .replace(/_+/g, '_')
    .replace(/^_+|_+$/g, '');
  return normalized || raw;
}

function parsedToolResult(preview) {
  const text = (preview || '').toString().trim();
  if (!text || !/^[{[]/.test(text)) return null;
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

function toolResultText(result, preview, keys) {
  if (result && typeof result === 'object') {
    for (const key of keys) {
      const text = previewText(result[key]);
      if (text) return text;
    }
  }
  return previewText(preview);
}

function knownToolSummary(name, failed, result, preview) {
  if (name === 'lsp_edit') return failed ? '编辑文件失败' : '已替换文件内容';
  if (name === 'lsp_file') return failed ? '读取文件失败' : '已读取文件';
  if (name === 'lsp_grep') {
    const total = Number(result?.total ?? result?.count);
    if (Number.isFinite(total)) return total > 0 ? `搜索到 ${Math.trunc(total)} 处` : '搜索无结果';
    return failed ? '搜索代码失败' : '已搜索代码';
  }
  if (name === 'code_run' || name === 'go_run' || name === 'code_run_test') return failed ? `命令执行失败${toolFailureSuffix(result, preview)}` : '命令执行成功';

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
  const failed = item?.success === false || status === 'failed' || status === 'error' || Boolean((item?.error || '').toString().trim());
  if (status === 'running' && !failed) return { name, summary: '执行中', status: 'active' };
  const preview = item?.preview || item?.error || '';

  const result = parsedToolResult(preview);
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
