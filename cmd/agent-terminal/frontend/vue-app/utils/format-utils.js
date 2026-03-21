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
