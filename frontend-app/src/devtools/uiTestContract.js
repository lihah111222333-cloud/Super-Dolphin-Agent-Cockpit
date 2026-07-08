export const UI_TEST_GLOBAL = '__SUPER_DOLPHIN_UI_TEST__';

export const UI_TEST_TOOLS = Object.freeze([
  'ui_snapshot',
  'ui_action',
  'ui_diagnostics',
  'ui_frontend_logs',
]);

export const UI_TEST_ACTIONS = Object.freeze([
  'navigate',
  'fill_composer',
  'submit_composer',
  'wait_for',
]);

export const UI_TEST_TARGETS = Object.freeze([
  'composer_input',
  'composer_submit',
]);

export const UI_TEST_ROUTES = Object.freeze({
  chat: '/',
  settings: '/settings',
  observability: '/observability',
});

export const UI_TEST_WAIT_STATES = Object.freeze([
  'frontend_ready',
  'composer_text_length',
  'route',
]);

export const UI_TEST_LIMITS = Object.freeze({
  defaultLimit: 100,
  maxLimit: 100,
  maxTextLength: 4000,
  maxStringLength: 500,
  maxFieldDepth: 4,
  maxFieldCount: 50,
  defaultTimeoutMs: 5000,
  maxTimeoutMs: 30000,
  pollIntervalMs: 100,
  maxFrameBytes: 1024 * 1024,
  maxHeaderBytes: 8192,
  maxLineBytes: 1024 * 1024,
});

function assertKnownName(name, values, label) {
  if (typeof name === 'string' && values.includes(name)) return name;
  throw new Error(`unknown UI test ${label}: ${String(name)}`);
}

export function assertKnownToolName(name) {
  return assertKnownName(name, UI_TEST_TOOLS, 'tool');
}

export function assertKnownActionName(name) {
  return assertKnownName(name, UI_TEST_ACTIONS, 'action');
}

export function assertKnownTargetName(target) {
  return assertKnownName(target, UI_TEST_TARGETS, 'target');
}

function normalizePositiveInteger(value, defaultValue, maxValue, label) {
  if (value === undefined || value === null) return defaultValue;
  if (!Number.isInteger(value) || value <= 0) {
    throw new Error(`${label} must be a positive integer`);
  }
  return Math.min(value, maxValue);
}

export function normalizeLimit(limit) {
  return normalizePositiveInteger(limit, UI_TEST_LIMITS.defaultLimit, UI_TEST_LIMITS.maxLimit, 'limit');
}

export function normalizeTimeoutMs(timeoutMs) {
  return normalizePositiveInteger(
    timeoutMs,
    UI_TEST_LIMITS.defaultTimeoutMs,
    UI_TEST_LIMITS.maxTimeoutMs,
    'timeoutMs',
  );
}

function isPlainObject(value) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false;
  const proto = Object.getPrototypeOf(value);
  return proto === Object.prototype || proto === null;
}

export function validateExactKeys(value, allowedKeys, label) {
  if (!isPlainObject(value)) {
    throw new Error(`${label} must be a plain object`);
  }
  const allowed = new Set();
  for (const key of allowedKeys) {
    if (allowed.has(key)) {
      throw new Error(`${label} allowed keys contain duplicate: ${key}`);
    }
    allowed.add(key);
  }
  for (const key of Object.keys(value)) {
    if (!allowed.has(key)) {
      throw new Error(`${label} contains unknown field: ${key}`);
    }
  }
  return value;
}
