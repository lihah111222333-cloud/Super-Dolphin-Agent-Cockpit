export const SAFE_DIAGNOSTIC_PREVIEW_REDACTED = '[redacted]';

const SENSITIVE_PREVIEW_KEY_TOKENS = new Set([
  'auth',
  'body',
  'content',
  'credential',
  'credentials',
  'cwd',
  'env',
  'password',
  'path',
  'paths',
  'profile',
  'prompt',
  'raw',
  'secret',
  'stack',
  'text',
  'token',
]);

const SENSITIVE_PREVIEW_KEYS = new Set([
  'access_token',
  'api_key',
  'auth_token',
  'authorization',
  'file_content',
  'file_contents',
  'file_path',
  'id_token',
  'message',
  'params',
  'raw_params',
  'raw_stack',
  'refresh_token',
  'request_params',
  'result_preview',
  'stack_trace',
  'stacktrace',
  'tool_result',
  'tool_results',
  'workspace_root',
  'workspace_roots',
]);

function normalizePreviewKey(key) {
  return (key || '')
    .toString()
    .replace(/([a-z0-9])([A-Z])/g, '$1_$2')
    .replace(/[\s.-]+/g, '_')
    .toLowerCase();
}

function isPlainPreviewObject(value) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false;
  const proto = Object.getPrototypeOf(value);
  return proto === Object.prototype || proto === null;
}

function isSensitivePreviewKey(key) {
  const normalized = normalizePreviewKey(key);
  if (SENSITIVE_PREVIEW_KEYS.has(normalized)) return true;
  return normalized.split('_').some((part) => SENSITIVE_PREVIEW_KEY_TOKENS.has(part));
}

function parsePreviewJSONText(value) {
  if (typeof value !== 'string') return value;
  const text = value.trim();
  if (!text) return value;
  try {
    return JSON.parse(text);
  } catch {
    return value;
  }
}

export function safeDiagnosticPreviewValue(value, seen = new WeakSet()) {
  if (value === null || value === undefined) return value;
  if (typeof value === 'string') return SAFE_DIAGNOSTIC_PREVIEW_REDACTED;
  if (typeof value === 'number') return Number.isFinite(value) ? value : SAFE_DIAGNOSTIC_PREVIEW_REDACTED;
  if (typeof value === 'boolean') return value;
  if (typeof value === 'bigint') return SAFE_DIAGNOSTIC_PREVIEW_REDACTED;
  if (typeof value !== 'object') return undefined;
  if (seen.has(value)) return SAFE_DIAGNOSTIC_PREVIEW_REDACTED;
  seen.add(value);

  if (Array.isArray(value)) {
    return value
      .map((item) => safeDiagnosticPreviewValue(item, seen))
      .filter((item) => item !== undefined);
  }
  if (!isPlainPreviewObject(value)) return SAFE_DIAGNOSTIC_PREVIEW_REDACTED;

  const out = {};
  for (const [key, item] of Object.entries(value)) {
    if (isSensitivePreviewKey(key)) continue;
    const safeValue = safeDiagnosticPreviewValue(item, seen);
    if (safeValue !== undefined) out[key] = safeValue;
  }
  return out;
}

export function compactSafeDiagnosticPreview(value, limit, options = {}) {
  if (!Number.isInteger(limit) || limit <= 0) {
    throw new Error('safe diagnostic preview limit must be a positive integer');
  }
  const source = options.parseJsonStrings ? parsePreviewJSONText(value) : value;
  const safeValue = safeDiagnosticPreviewValue(source);
  const text = typeof safeValue === 'string' ? safeValue : JSON.stringify(safeValue);
  if (!text) return '';
  if (text.length <= limit) return text;
  return `${text.slice(0, limit)}...`;
}
