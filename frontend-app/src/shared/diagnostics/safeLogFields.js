export const SAFE_LOG_REDACTED_VALUE = '[redacted]';
export const SAFE_LOG_TRUNCATED_VALUE = '[truncated]';
export const SAFE_LOG_LIMITS = Object.freeze({
  maxStringLength: 500,
  maxFieldDepth: 4,
  maxFieldCount: 50,
});

export const SAFE_LOG_FORBIDDEN_KEYS = Object.freeze([
  'token',
  'api_key',
  'secret',
  'authorization',
  'prompt',
  'user_prompt',
  'user_message',
  'message_text',
  'text',
  'content',
  'file_content',
  'tool_result',
  'memory',
  'skill',
  'thread_messages',
  'file_contents',
  'tool_results',
  'password',
  'auth',
  'credential',
  'credentials',
  'auth_token',
  'access_token',
  'refresh_token',
  'id_token',
  'stack',
  'raw_stack',
  'stack_trace',
  'stacktrace',
]);

const SAFE_LOG_OPTION_KEYS = new Set([
  'forbiddenKeys',
  'forbiddenKeyMode',
  'maxStringLength',
  'maxFieldDepth',
  'maxFieldCount',
  'redactedValue',
  'truncatedValue',
]);

const SECRET_ASSIGNMENT_RE =
  /\b(?:api[_\s-]?key|auth[_\s-]?token|access[_\s-]?token|refresh[_\s-]?token|id[_\s-]?token|authorization|credential(?:s)?|password|secret|token)\b\s*[:=]\s*["']?[^"',\s}]+/i;
const TOKEN_VALUE_RE = /\b(?:bearer|basic)\s+[a-z0-9._~+/=-]{8,}|\bsk-[a-z0-9][a-z0-9_-]{6,}\b/i;
const POSIX_LOCAL_PATH_RE =
  /(^|[\s("'`=])\/(?:home|users|var|tmp|etc|opt|private|workspace|mnt|volumes|root)\/[^\s"'`<>]*/gi;
const WINDOWS_LOCAL_PATH_RE = /\b[a-z]:\\(?:[^\\/:*?"<>|\r\n]+\\?)+/gi;
const UNC_LOCAL_PATH_RE = /\\\\[a-z0-9._-]+\\[^\s"'`<>|]+/gi;

function isPlainLogObject(value) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false;
  const proto = Object.getPrototypeOf(value);
  return proto === Object.prototype || proto === null;
}

function assertPositiveInteger(value, label) {
  if (!Number.isInteger(value) || value <= 0) {
    throw new Error(`safeLogFields ${label} must be a positive integer`);
  }
  return value;
}

export function normalizeSafeLogFieldKey(key) {
  const raw = key ? key.toString() : '';
  return raw
    .toString()
    .replace(/([a-z0-9])([A-Z])/g, '$1_$2')
    .replace(/[\s.-]+/g, '_')
    .toLowerCase();
}

function normalizeForbiddenKeys(forbiddenKeys) {
  const keys = forbiddenKeys === undefined ? SAFE_LOG_FORBIDDEN_KEYS : forbiddenKeys;
  if (!Array.isArray(keys) && !(keys instanceof Set)) {
    throw new Error('safeLogFields forbiddenKeys must be an array or set');
  }
  return new Set([...keys].map((key) => normalizeSafeLogFieldKey(key)));
}

function normalizeSafeLogOptions(options = {}) {
  if (!isPlainLogObject(options)) {
    throw new Error('safeLogFields options must be a plain object');
  }
  for (const key of Object.keys(options)) {
    if (!SAFE_LOG_OPTION_KEYS.has(key)) {
      throw new Error(`safeLogFields option ${key} is not supported`);
    }
  }

  const forbiddenKeyMode = options.forbiddenKeyMode || 'redact';
  if (forbiddenKeyMode !== 'redact' && forbiddenKeyMode !== 'omit') {
    throw new Error('safeLogFields forbiddenKeyMode must be redact or omit');
  }

  return {
    forbiddenKeys: normalizeForbiddenKeys(options.forbiddenKeys),
    forbiddenKeyMode,
    maxStringLength: assertPositiveInteger(
      options.maxStringLength ?? SAFE_LOG_LIMITS.maxStringLength,
      'maxStringLength',
    ),
    maxFieldDepth: assertPositiveInteger(
      options.maxFieldDepth ?? SAFE_LOG_LIMITS.maxFieldDepth,
      'maxFieldDepth',
    ),
    maxFieldCount: assertPositiveInteger(
      options.maxFieldCount ?? SAFE_LOG_LIMITS.maxFieldCount,
      'maxFieldCount',
    ),
    redactedValue: options.redactedValue ?? SAFE_LOG_REDACTED_VALUE,
    truncatedValue: options.truncatedValue ?? SAFE_LOG_TRUNCATED_VALUE,
  };
}

export function isSafeLogForbiddenKey(key, options = {}) {
  const normalized = normalizeSafeLogOptions(options);
  return normalized.forbiddenKeys.has(normalizeSafeLogFieldKey(key));
}

function redactUnsafeString(value, options) {
  if (SECRET_ASSIGNMENT_RE.test(value) || TOKEN_VALUE_RE.test(value)) {
    return options.redactedValue;
  }
  const withoutPaths = value
    .replace(POSIX_LOCAL_PATH_RE, (_match, prefix = '') => `${prefix}[path]`)
    .replace(WINDOWS_LOCAL_PATH_RE, '[path]')
    .replace(UNC_LOCAL_PATH_RE, '[path]');
  if (withoutPaths.length <= options.maxStringLength) return withoutPaths;
  if (options.maxStringLength <= 3) return withoutPaths.slice(0, options.maxStringLength);
  return `${withoutPaths.slice(0, options.maxStringLength - 3)}...`;
}

function sanitizeError(error, options, depth, seen) {
  const out = {
    name: error.name || 'Error',
    message: error.message ? error.message : '',
  };
  for (const [key, value] of Object.entries(error)) {
    if (key === 'name' || key === 'message') continue;
    out[key] = value;
  }
  return sanitizeObject(out, options, depth, seen);
}

function sanitizeArray(value, options, depth, seen) {
  if (depth >= options.maxFieldDepth) return options.truncatedValue;
  if (seen.has(value)) return '[Circular]';
  seen.add(value);
  return value
    .slice(0, options.maxFieldCount)
    .map((item) => sanitizeValue(item, options, depth + 1, seen));
}

function sanitizeObject(value, options, depth, seen) {
  if (depth >= options.maxFieldDepth) return options.truncatedValue;
  if (seen.has(value)) return '[Circular]';
  seen.add(value);

  const out = {};
  let count = 0;
  for (const [key, item] of Object.entries(value)) {
    if (count >= options.maxFieldCount) break;
    if (options.forbiddenKeys.has(normalizeSafeLogFieldKey(key))) {
      if (options.forbiddenKeyMode === 'omit') continue;
      out[key] = options.redactedValue;
      count += 1;
      continue;
    }
    out[key] = sanitizeValue(item, options, depth + 1, seen);
    count += 1;
  }
  return out;
}

function sanitizeValue(value, options, depth, seen) {
  if (value === undefined || value === null) return value;
  if (typeof value === 'string') return redactUnsafeString(value, options);
  if (typeof value === 'number' || typeof value === 'boolean') return value;
  if (typeof value === 'bigint' || typeof value === 'symbol' || typeof value === 'function') {
    return options.redactedValue;
  }
  if (value instanceof Error) return sanitizeError(value, options, depth, seen);
  if (Array.isArray(value)) return sanitizeArray(value, options, depth, seen);
  if (!isPlainLogObject(value)) return options.redactedValue;
  return sanitizeObject(value, options, depth, seen);
}

export function redactUITestValue(value, options = {}) {
  return sanitizeValue(value, normalizeSafeLogOptions(options), 0, new WeakSet());
}

export function safeLogFields(fields, options = {}) {
  if (!isPlainLogObject(fields)) {
    throw new Error('safeLogFields fields must be a plain object');
  }
  return sanitizeObject(fields, normalizeSafeLogOptions(options), 0, new WeakSet());
}
