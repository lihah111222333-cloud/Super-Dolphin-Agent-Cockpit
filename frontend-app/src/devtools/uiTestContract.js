const UI_TEST_GLOBAL = '__SUPER_DOLPHIN_UI_TEST__';

const UI_TEST_TOOLS = Object.freeze([
  'ui_snapshot',
  'ui_action',
  'ui_diagnostics',
  'ui_frontend_logs',
  'ui_scenario_run',
]);

const UI_TEST_ACTIONS = Object.freeze([
  'navigate',
  'fill_composer',
  'submit_composer',
  'wait_for',
]);

const UI_TEST_TARGETS = Object.freeze([
  'composer_input',
  'composer_submit',
]);

const UI_TEST_ROUTES = Object.freeze({
  chat: '/',
  settings: '/settings',
  observability: '/observability',
  skills: '/skills',
  automation: '/dags',
  prompts: '/prompts',
  files: '/files',
  memory: '/memory',
});

const UI_TEST_WAIT_STATES = Object.freeze([
  'frontend_ready',
  'composer_text_length',
  'route',
]);

const UI_TEST_LIMITS = Object.freeze({
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

const UI_TEST_SCENARIOS = Object.freeze({
  chat_composer_probe: Object.freeze({
    id: 'chat_composer_probe',
    risk: 'local_ui_only',
  }),
  frontend_navigation_probe: Object.freeze({
    id: 'frontend_navigation_probe',
    risk: 'local_ui_only',
  }),
  observability_logs_probe: Object.freeze({
    id: 'observability_logs_probe',
    risk: 'read_only',
  }),
  settings_open_probe: Object.freeze({
    id: 'settings_open_probe',
    risk: 'read_only',
  }),
  open_route_probe: Object.freeze({
    id: 'open_route_probe',
    risk: 'read_only',
  }),
});

const UI_TEST_SCENARIO_IDS = Object.freeze(Object.keys(UI_TEST_SCENARIOS));

function assertKnownName(name, values, label) {
  if (typeof name === 'string' && values.includes(name)) return name;
  throw new Error(`unknown UI test ${label}: ${String(name)}`);
}

function assertKnownToolName(name) {
  return assertKnownName(name, UI_TEST_TOOLS, 'tool');
}

function assertKnownActionName(name) {
  return assertKnownName(name, UI_TEST_ACTIONS, 'action');
}

function assertKnownTargetName(target) {
  return assertKnownName(target, UI_TEST_TARGETS, 'target');
}

function assertKnownScenarioName(name) {
  return assertKnownName(name, UI_TEST_SCENARIO_IDS, 'scenario');
}

function normalizePositiveInteger(value, defaultValue, maxValue, label) {
  if (value === undefined || value === null) return defaultValue;
  if (!Number.isInteger(value) || value <= 0) {
    throw new Error(`${label} must be a positive integer`);
  }
  return Math.min(value, maxValue);
}

function normalizeLimit(limit) {
  return normalizePositiveInteger(limit, UI_TEST_LIMITS.defaultLimit, UI_TEST_LIMITS.maxLimit, 'limit');
}

function normalizeTimeoutMs(timeoutMs) {
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

function validateExactKeys(value, allowedKeys, label) {
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

export {
  UI_TEST_GLOBAL,
  UI_TEST_TOOLS,
  UI_TEST_ACTIONS,
  UI_TEST_TARGETS,
  UI_TEST_ROUTES,
  UI_TEST_WAIT_STATES,
  UI_TEST_LIMITS,
  UI_TEST_SCENARIOS,
  UI_TEST_SCENARIO_IDS,
  assertKnownToolName,
  assertKnownActionName,
  assertKnownTargetName,
  assertKnownScenarioName,
  normalizeLimit,
  normalizeTimeoutMs,
  validateExactKeys,
};
