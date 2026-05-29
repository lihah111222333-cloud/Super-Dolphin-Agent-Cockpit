/**
 * @param {string | null | undefined} value
 * @returns {string}
 */
export function normalizeLogScopeCwd(value) {
  const raw = (value || '').toString().trim();
  if (!raw || raw === '.') return '';
  return raw.replace(/[\\/]+$/, '');
}

/**
 * 与 cmd/agent-terminal/main.go / internal/apiserver/methods_ui_projects.go 的日志目录规则保持一致。
 * @param {string | null | undefined} cwd
 * @returns {string | null}
 */
export function buildCwdLogPath(cwd) {
  const normalized = normalizeLogScopeCwd(cwd);
  if (!normalized || /^[A-Za-z]:$/.test(normalized) || /^[\\/]+$/.test(normalized)) {
    return null;
  }
  const segments = normalized.split(/[\\/]/).filter(Boolean);
  const projectName = segments[segments.length - 1] || '';
  if (!projectName || projectName === '.' || projectName === '/') return null;
  return `~/.multi-agent/log/${projectName}/`;
}

/**
 * @param {Date | string | number} [value]
 * @returns {string}
 */
export function formatUTC8HumanReadable(value = new Date()) {
  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime())) return '';
  const utc8 = new Date(date.getTime() + (8 * 60 * 60 * 1000));
  const year = utc8.getUTCFullYear();
  const month = String(utc8.getUTCMonth() + 1).padStart(2, '0');
  const day = String(utc8.getUTCDate()).padStart(2, '0');
  const hours = String(utc8.getUTCHours()).padStart(2, '0');
  const minutes = String(utc8.getUTCMinutes()).padStart(2, '0');
  const seconds = String(utc8.getUTCSeconds()).padStart(2, '0');
  return `${year}-${month}-${day} ${hours}:${minutes}:${seconds} UTC+8`;
}
