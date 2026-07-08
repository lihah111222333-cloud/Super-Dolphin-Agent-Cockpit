import {
  UI_TEST_ACTIONS,
  UI_TEST_GLOBAL,
  UI_TEST_LIMITS,
  normalizeLimit,
  validateExactKeys,
} from './uiTestContract.js';
import { safeLogFields } from '../shared/diagnostics/safeLogFields.js';

const UI_TEST_ACCEPTANCE_GLOBAL = '__SUPER_DOLPHIN_UI_TEST_ACCEPTANCE__';
const LOG_ENTRY_KEYS = Object.freeze(['id', 'ts', 'level', 'source', 'message', 'fields']);
const SNAPSHOT_KEYS = Object.freeze([
  'route',
  'currentThreadId',
  'inputTextLength',
  'hasRunningTurn',
  'visibleErrors',
  'availableActions',
]);
const DIAGNOSTIC_KEYS = Object.freeze([
  'consoleErrors',
  'bridgeErrors',
  'unhandledErrors',
  'warningEntries',
  'url',
  'readyState',
]);
const RECORD_LOG_KEYS = Object.freeze(['level', 'source', 'message', 'fields']);
const FRONTEND_LOG_FILTER_KEYS = Object.freeze(['level', 'source', 'since', 'limit']);
const ACCEPTANCE_TOKEN_KEYS = Object.freeze(['token']);
const SAFE_EVENT_SEGMENT_RE = /^[a-z][a-z0-9_]*$/;
const BUSY_THREAD_STATUS = new Set(['running', 'waiting', 'interrupting', 'force_completing', 'thinking', 'responding']);
const SystemDate = globalThis.Date;

function isPlainObject(value) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false;
  const prototype = Object.getPrototypeOf(value);
  return prototype === Object.prototype || prototype === null;
}

function createStrictObject(keys, values) {
  return Object.freeze(Object.fromEntries(keys.map((key) => [key, values[key]])));
}

function firstTruthyString(values) {
  for (const value of values) {
    if (value) return value.toString();
  }
  return '';
}

function firstTruthyValue(values) {
  for (const value of values) {
    if (value) return value;
  }
  return undefined;
}

function currentSystemDate() {
  return new SystemDate();
}

function currentTimestampISO() {
  return currentSystemDate().toISOString();
}

function parseTimestampMillis(value) {
  return SystemDate.parse(value);
}

function assertString(value, label) {
  if (typeof value !== 'string') {
    throw new Error(`${label} must be a string`);
  }
  return value;
}

function assertRecordLogInput(entry) {
  if (!isPlainObject(entry)) {
    throw new Error('recordLog input must be a plain object');
  }
  validateExactKeys(entry, RECORD_LOG_KEYS, 'recordLog input');
  const level = assertString(entry.level, 'recordLog level');
  const source = assertString(entry.source, 'recordLog source');
  const message = assertString(entry.message, 'recordLog message');
  if (source !== 'ui_test_mcp') {
    throw new Error('recordLog source must be ui_test_mcp');
  }
  if (!SAFE_EVENT_SEGMENT_RE.test(message)) {
    throw new Error('recordLog message must be a safe event segment');
  }
  if (!isPlainObject(entry.fields)) {
    throw new Error('recordLog fields must be a plain object');
  }
  return { level, source, message, fields: entry.fields };
}

function assertTokenInput(input, label) {
  if (!isPlainObject(input)) {
    throw new Error(`${label} input must be a plain object`);
  }
  validateExactKeys(input, ACCEPTANCE_TOKEN_KEYS, `${label} input`);
  return assertString(input.token, `${label} token`);
}

function elementText(element) {
  const text = element && typeof element.textContent === 'string' ? element.textContent : '';
  return text.trim();
}

function visibleAlertTexts(documentRef) {
  return Array.from(documentRef.querySelectorAll('[role="alert"], [data-ui-error], .error, .error-message'))
    .filter((element) => !element.hidden && element.getAttribute('aria-hidden') !== 'true')
    .map(elementText)
    .filter(Boolean);
}

function composerInput(documentRef) {
  return documentRef.querySelector('[data-testid="composer-input"]');
}

function inputTextLength(documentRef, state) {
  const input = composerInput(documentRef);
  if (input && typeof input.value === 'string') return input.value.length;
  return typeof state?.draft === 'string' ? state.draft.length : 0;
}

function composerHasText(documentRef) {
  const input = composerInput(documentRef);
  return Boolean(input && typeof input.value === 'string' && input.value.trim().length > 0);
}

function normalizeCurrentThreadId(state) {
  if (!state) return '';
  return firstTruthyString([state.currentThreadId, state.activeThreadId]);
}

function statusValue(value) {
  if (typeof value === 'string') return value.toLowerCase();
  if (value && typeof value === 'object') {
    return firstTruthyString([value.state, value.status, value.phase]).toLowerCase();
  }
  return '';
}

function stateHasRunningTurn(state, currentThreadId) {
  if (!state) return false;
  if (state.sending) return true;
  if (typeof state.hasInterruptibleThreadAction === 'function' && state.hasInterruptibleThreadAction(currentThreadId)) {
    return true;
  }
  const activeTurn = state.activeTurnByThread?.[currentThreadId];
  if (activeTurn && statusValue(activeTurn) !== 'completed') return true;
  const status = statusValue(firstTruthyValue([
    state.statuses?.[currentThreadId],
    state.threadStatuses?.[currentThreadId],
    state.threadStatusByThread?.[currentThreadId],
  ]));
  if (BUSY_THREAD_STATUS.has(status)) return true;
  const thread = Array.isArray(state.threads)
    ? state.threads.find((candidate) => candidate?.id === currentThreadId || candidate?.thread_id === currentThreadId)
    : null;
  return BUSY_THREAD_STATUS.has(statusValue(thread));
}

function action(name, enabled, disabledReason = null) {
  return { name, enabled, disabledReason };
}

function submitDisabledReason(documentRef, acceptanceToken) {
  const interruptButton = documentRef.querySelector('[data-testid="composer-interrupt"]');
  if (interruptButton) return 'primary_action_is_interrupt';
  const submitButton = documentRef.querySelector('[data-testid="composer-submit"]');
  if (!submitButton) return 'composer_submit_not_available';
  if (!acceptanceToken) return 'isolated_acceptance_required';
  if (!composerHasText(documentRef)) return 'composer_input_empty';
  return null;
}

function availableActions(documentRef, acceptanceToken) {
  const input = composerInput(documentRef);
  const submitReason = submitDisabledReason(documentRef, acceptanceToken);
  const canFillComposer = Boolean(input && !input.disabled && !input.readOnly);
  const actionMap = {
    navigate: action('navigate', true),
    fill_composer: action(
      'fill_composer',
      canFillComposer,
      canFillComposer ? null : (input ? 'composer_input_disabled' : 'composer_input_not_available'),
    ),
    submit_composer: action('submit_composer', submitReason === null, submitReason),
    wait_for: action('wait_for', true),
  };
  return UI_TEST_ACTIONS.map((name) => {
    if (!actionMap[name]) {
      throw new Error(`ui test action is not implemented by harness: ${name}`);
    }
    return actionMap[name];
  });
}

function requireState(getState) {
  const state = getState();
  if (!state || typeof state !== 'object') {
    throw new Error('ui test harness getState() must return a state object');
  }
  return state;
}

function requireLogState(getState) {
  const state = requireState(getState);
  if (!Array.isArray(state.logEntries)) {
    throw new Error('ui test harness requires state.logEntries');
  }
  return state;
}

function normalizeStoreLogEntry(entry) {
  if (!entry || typeof entry !== 'object') {
    throw new Error('frontend log entry must be an object');
  }
  const source = firstTruthyString([entry.source, entry.scope]);
  const message = firstTruthyString([entry.message, entry.event]);
  const fields = isPlainObject(entry.fields) ? safeLogFields(entry.fields) : {};
  return createStrictObject(LOG_ENTRY_KEYS, {
    id: firstTruthyString([entry.id]),
    ts: firstTruthyString([entry.ts, entry.timestamp]),
    level: firstTruthyString([entry.level]),
    source,
    message,
    fields,
  });
}

function normalizeWarningEntry(entry) {
  const normalized = normalizeStoreLogEntry(entry);
  return {
    id: normalized.id,
    ts: normalized.ts,
    level: normalized.level,
    source: normalized.source,
    message: normalized.message,
    fields: normalized.fields,
  };
}

function normalizeDiagnosticError(entry) {
  if (typeof entry === 'string') {
    return { ts: '', message: entry };
  }
  if (!entry || typeof entry !== 'object') {
    return { ts: '', message: String(entry) };
  }
  return {
    ts: firstTruthyString([entry.ts, entry.timestamp]),
    message: firstTruthyString([entry.message, entry.reason]),
  };
}

function filtersFromInput(input = {}) {
  if (!isPlainObject(input)) {
    throw new Error('frontendLogs filters must be a plain object');
  }
  validateExactKeys(input, FRONTEND_LOG_FILTER_KEYS, 'frontendLogs filters');
  const level = input.level === undefined ? undefined : assertString(input.level, 'frontendLogs level');
  const source = input.source === undefined ? undefined : assertString(input.source, 'frontendLogs source');
  const since = input.since === undefined ? undefined : assertString(input.since, 'frontendLogs since');
  const limit = normalizeLimit(input.limit);
  const sinceMs = since === undefined ? undefined : parseTimestampMillis(since);
  if (since !== undefined && !Number.isFinite(sinceMs)) {
    throw new Error('frontendLogs since must be an ISO timestamp');
  }
  return { level, source, sinceMs, limit };
}

function captureConsoleError(windowRef, diagnosticState) {
  const consoleRef = windowRef.console;
  if (!consoleRef || typeof consoleRef.error !== 'function' || consoleRef.__superDolphinUITestWrapped) {
    return;
  }
  const originalError = consoleRef.error.bind(consoleRef);
  const wrappedError = (...args) => {
    diagnosticState.consoleErrors.unshift({
      ts: currentTimestampISO(),
      message: args.map((arg) => (arg instanceof Error ? arg.message : String(arg))).join(' '),
    });
    originalError(...args);
  };
  wrappedError.__superDolphinUITestOriginal = originalError;
  consoleRef.error = wrappedError;
  consoleRef.__superDolphinUITestWrapped = true;
}

function captureUnhandledErrors(windowRef, diagnosticState) {
  if (typeof windowRef.addEventListener !== 'function') return;
  windowRef.addEventListener('error', (event) => {
    diagnosticState.unhandledErrors.unshift({
      ts: currentTimestampISO(),
      message: windowErrorMessage(event),
    });
  });
  windowRef.addEventListener('unhandledrejection', (event) => {
    diagnosticState.unhandledErrors.unshift({
      ts: currentTimestampISO(),
      message: rejectionMessage(event),
    });
  });
}

function windowErrorMessage(event) {
  if (event?.error?.message) return event.error.message;
  if (event?.message) return event.message;
  return 'window error';
}

function rejectionMessage(event) {
  if (event?.reason?.message) return event.reason.message;
  if (event?.reason) return String(event.reason);
  return 'unhandled rejection';
}

function acceptanceTokenFromWindow(windowRef) {
  const config = windowRef?.[UI_TEST_ACCEPTANCE_GLOBAL];
  if (!config || typeof config !== 'object') return '';
  return typeof config.token === 'string' ? config.token : '';
}

export function isUITestHarnessEnabled(metaEnv = import.meta.env) {
  const env = metaEnv === undefined || metaEnv === null ? {} : metaEnv;
  if (env.PROD) return false;
  return Boolean(
    env.DEV ||
    env.MODE === 'test' ||
    env.VITE_SUPER_DOLPHIN_UI_TEST_MCP === '1',
  );
}

export function createUITestHarness({
  getState,
  documentRef = document,
  locationRef = window.location,
  now = currentSystemDate,
  acceptanceToken = '',
  diagnosticState = { consoleErrors: [], unhandledErrors: [] },
}) {
  if (typeof getState !== 'function') {
    throw new Error('ui test harness requires getState');
  }

  const snapshot = () => {
    if (!documentRef.querySelector('[data-testid="frontend-app"]')) {
      throw new Error('ui test harness requires data-testid="frontend-app"');
    }
    const state = requireState(getState);
    const currentThreadId = normalizeCurrentThreadId(state);
    return createStrictObject(SNAPSHOT_KEYS, {
      route: locationRef.pathname || '/',
      currentThreadId,
      inputTextLength: inputTextLength(documentRef, state),
      hasRunningTurn: stateHasRunningTurn(state, currentThreadId),
      visibleErrors: visibleAlertTexts(documentRef),
      availableActions: availableActions(documentRef, acceptanceToken),
    });
  };

  const frontendLogs = (input = {}) => {
    const state = requireLogState(getState);
    const filters = filtersFromInput(input);
    return state.logEntries
      .map(normalizeStoreLogEntry)
      .filter((entry) => (filters.level === undefined ? true : entry.level === filters.level))
      .filter((entry) => (filters.source === undefined ? true : entry.source === filters.source))
      .filter((entry) => {
        if (filters.sinceMs === undefined) return true;
        const entryMs = parseTimestampMillis(entry.ts);
        return Number.isFinite(entryMs) && entryMs >= filters.sinceMs;
      })
      .slice(0, filters.limit);
  };

  const diagnostics = () => {
    const state = requireState(getState);
    const logEntries = Array.isArray(state.logEntries) ? state.logEntries : [];
    const bridgeErrors = logEntries
      .filter((entry) => entry?.level === 'error')
      .filter((entry) => /bridge|wails|api/.test(`${firstTruthyString([entry.scope])}.${firstTruthyString([entry.event])}`))
      .map(normalizeStoreLogEntry)
      .slice(0, UI_TEST_LIMITS.maxLimit);
    const warningEntries = Array.isArray(state.warningEntries)
      ? state.warningEntries.map(normalizeWarningEntry).slice(0, UI_TEST_LIMITS.maxLimit)
      : [];
    return createStrictObject(DIAGNOSTIC_KEYS, {
      consoleErrors: diagnosticState.consoleErrors.map(normalizeDiagnosticError).slice(0, UI_TEST_LIMITS.maxLimit),
      bridgeErrors,
      unhandledErrors: diagnosticState.unhandledErrors.map(normalizeDiagnosticError).slice(0, UI_TEST_LIMITS.maxLimit),
      warningEntries,
      url: locationRef.href || locationRef.toString(),
      readyState: documentRef.readyState,
    });
  };

  const recordLog = (entry) => {
    const input = assertRecordLogInput(entry);
    const state = requireLogState(getState);
    if (typeof state.addLog !== 'function') {
      throw new Error('ui test harness requires state.addLog');
    }
    const sanitizedFields = safeLogFields(input.fields);
    state.addLog(input.level, `${input.source}.${input.message}`, sanitizedFields);
    const updatedState = requireLogState(getState);
    const persisted = updatedState.logEntries[0];
    if (!persisted) {
      throw new Error('ui test harness addLog did not persist a log entry');
    }
    return normalizeStoreLogEntry(persisted);
  };

  const verifyIsolatedAcceptance = (input) => {
    const token = assertTokenInput(input, 'verifyIsolatedAcceptance');
    if (!acceptanceToken || token !== acceptanceToken) {
      return { isolated: false, tokenMatched: false, reason: 'invalid_acceptance_token' };
    }
    return { isolated: true, tokenMatched: true };
  };

  const submitComposerInIsolation = (input) => {
    const verified = verifyIsolatedAcceptance(input);
    if (!verified.isolated) {
      throw new Error(verified.reason);
    }
    const disabledReason = submitDisabledReason(documentRef, acceptanceToken);
    if (disabledReason) {
      throw new Error(disabledReason);
    }
    const logEntry = recordLog({
      level: 'info',
      source: 'ui_test_mcp',
      message: 'submit_composer',
      fields: {
        isolated: true,
        submittedAt: now().toISOString(),
      },
    });
    return { submitted: true, logEntry };
  };

  return {
    snapshot,
    frontendLogs,
    diagnostics,
    recordLog,
    verifyIsolatedAcceptance,
    submitComposerInIsolation,
  };
}

export function installUITestHarness({
  windowRef = window,
  getState,
  metaEnv = import.meta.env,
}) {
  if (!isUITestHarnessEnabled(metaEnv)) {
    return null;
  }
  if (windowRef[UI_TEST_GLOBAL]) {
    return windowRef[UI_TEST_GLOBAL];
  }
  const diagnosticState = { consoleErrors: [], unhandledErrors: [] };
  captureConsoleError(windowRef, diagnosticState);
  captureUnhandledErrors(windowRef, diagnosticState);
  const harness = createUITestHarness({
    getState,
    documentRef: windowRef.document,
    locationRef: windowRef.location,
    acceptanceToken: acceptanceTokenFromWindow(windowRef),
    diagnosticState,
  });
  windowRef[UI_TEST_GLOBAL] = harness;
  return harness;
}
