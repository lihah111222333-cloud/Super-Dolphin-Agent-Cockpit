import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  createUITestHarness,
  installUITestHarness,
  isUITestHarnessEnabled,
} from './uiTestHarness.js';

function renderHarnessDom({
  app = true,
  input = 'hello',
  submit = true,
  submitDisabled = false,
  interrupt = false,
  visibleError = '',
} = {}) {
  document.body.innerHTML = `
    ${app ? '<main data-testid="frontend-app">' : '<main>'}
      <textarea data-testid="composer-input">${input}</textarea>
      ${submit ? `<button type="button" data-testid="composer-submit"${submitDisabled ? ' disabled' : ''}>send</button>` : ''}
      ${interrupt ? '<button type="button" data-testid="composer-interrupt">stop</button>' : ''}
      ${visibleError ? `<div role="alert">${visibleError}</div>` : ''}
    </main>
  `;
  const inputEl = document.querySelector('[data-testid="composer-input"]');
  if (inputEl) inputEl.value = input;
}

function createState(patch = {}) {
  const state = {
    activeThreadId: 'thread-1',
    draft: 'hello',
    sending: false,
    warningEntries: [],
    logEntries: [],
    addLog: vi.fn((level, event, fields) => {
      const [scope, ...eventParts] = event.split('.');
      state.logEntries.unshift({
        id: `${event}-1`,
        ts: '2026-07-08T00:00:00.000Z',
        level,
        scope,
        event: eventParts.join('.'),
        fields,
      });
    }),
    hasInterruptibleThreadAction: vi.fn(() => false),
    ...patch,
  };
  return state;
}

function actionByName(snapshot, name) {
  return snapshot.availableActions.find((action) => action.name === name);
}

function expectExactKeys(value, keys) {
  expect(Object.keys(value)).toEqual(keys);
}

const DIAGNOSTIC_WARNING_ENTRIES = [{
  id: 'warn-1',
  ts: '2026-07-08T00:00:00.000Z',
  level: 'warn',
  event: 'api.warn',
  fields: { cwd: '/home/me/repo' },
}];

const DIAGNOSTIC_STATE = {
  consoleErrors: [{ ts: '2026-07-08T00:00:00.000Z', message: 'console failed' }],
  unhandledErrors: [{ ts: '2026-07-08T00:00:00.000Z', message: 'promise failed' }],
};

const FILTERED_LOG_ENTRIES = [
  {
    id: 'new-match',
    ts: '2026-07-08T00:00:02.000Z',
    level: 'info',
    scope: 'ui_test_mcp',
    event: 'submit_composer',
    fields: { cwd: '/home/me/repo' },
  },
  {
    id: 'old-match',
    ts: '2026-07-08T00:00:00.000Z',
    level: 'info',
    scope: 'ui_test_mcp',
    event: 'submit_composer',
    fields: {},
  },
  {
    id: 'wrong-level',
    ts: '2026-07-08T00:00:03.000Z',
    level: 'debug',
    scope: 'ui_test_mcp',
    event: 'submit_composer',
    fields: {},
  },
];

describe('isUITestHarnessEnabled', () => {
  it('never enables the harness in production', () => {
    expect(isUITestHarnessEnabled({ PROD: true, VITE_SUPER_DOLPHIN_UI_TEST_MCP: '1' })).toBe(false);
    expect(isUITestHarnessEnabled({ PROD: true, DEV: true, MODE: 'test' })).toBe(false);
  });

  it('enables the harness for dev, test mode, and explicit UI test flag outside production', () => {
    expect(isUITestHarnessEnabled({ PROD: false, DEV: true })).toBe(true);
    expect(isUITestHarnessEnabled({ PROD: false, MODE: 'test' })).toBe(true);
    expect(isUITestHarnessEnabled({ PROD: false, VITE_SUPER_DOLPHIN_UI_TEST_MCP: '1' })).toBe(true);
    expect(isUITestHarnessEnabled({ PROD: false, MODE: 'production' })).toBe(false);
  });
});

describe('createUITestHarness snapshot', () => {
  beforeEach(() => {
    renderHarnessDom();
  });

  it('throws when the frontend app anchor is missing', () => {
    renderHarnessDom({ app: false });
    const harness = createUITestHarness({
      getState: () => createState(),
      documentRef: document,
      locationRef: window.location,
    });

    expect(() => harness.snapshot()).toThrow(/data-testid="frontend-app"/);
  });

  it('returns the exact snapshot keys', () => {
    const harness = createUITestHarness({
      getState: () => createState(),
      documentRef: document,
      locationRef: new URL('http://127.0.0.1:5175/settings'),
    });

    const snapshot = harness.snapshot();

    expectExactKeys(snapshot, [
      'route',
      'currentThreadId',
      'inputTextLength',
      'hasRunningTurn',
      'visibleErrors',
      'availableActions',
    ]);
    expect(snapshot).toMatchObject({
      route: '/settings',
      currentThreadId: 'thread-1',
      inputTextLength: 5,
      hasRunningTurn: false,
      visibleErrors: [],
    });
  });

  it('reports conditional action availability with disabled reasons', () => {
    const normalHarness = createUITestHarness({
      getState: () => createState(),
      documentRef: document,
      locationRef: window.location,
    });

    expect(actionByName(normalHarness.snapshot(), 'submit_composer')).toEqual({
      name: 'submit_composer',
      enabled: false,
      disabledReason: 'isolated_acceptance_required',
    });

    renderHarnessDom({ submitDisabled: true });
    const disabledHarness = createUITestHarness({
      getState: () => createState(),
      documentRef: document,
      locationRef: window.location,
      acceptanceToken: 'token-1',
    });

    expect(actionByName(disabledHarness.snapshot(), 'submit_composer')).toEqual({
      name: 'submit_composer',
      enabled: true,
      disabledReason: null,
    });

    renderHarnessDom({ input: '', submitDisabled: true });
    const emptyHarness = createUITestHarness({
      getState: () => createState({ draft: '' }),
      documentRef: document,
      locationRef: window.location,
      acceptanceToken: 'token-1',
    });

    expect(actionByName(emptyHarness.snapshot(), 'submit_composer')).toEqual({
      name: 'submit_composer',
      enabled: false,
      disabledReason: 'composer_input_empty',
    });

    renderHarnessDom({ submit: false, interrupt: true });
    const interruptHarness = createUITestHarness({
      getState: () => createState(),
      documentRef: document,
      locationRef: window.location,
      acceptanceToken: 'token-1',
    });

    expect(actionByName(interruptHarness.snapshot(), 'submit_composer')).toEqual({
      name: 'submit_composer',
      enabled: false,
      disabledReason: 'primary_action_is_interrupt',
    });
  });
});

describe('createUITestHarness diagnostics', () => {
  it('returns exact diagnostic keys', () => {
    renderHarnessDom();
    const harness = createUITestHarness({
      getState: () => createState({
        warningEntries: DIAGNOSTIC_WARNING_ENTRIES,
      }),
      documentRef: document,
      locationRef: new URL('http://127.0.0.1:5175/observability'),
      diagnosticState: DIAGNOSTIC_STATE,
    });

    const diagnostics = harness.diagnostics();

    expectExactKeys(diagnostics, [
      'consoleErrors',
      'bridgeErrors',
      'unhandledErrors',
      'warningEntries',
      'url',
      'readyState',
    ]);
    expect(diagnostics.url).toBe('http://127.0.0.1:5175/observability');
    expect(diagnostics.readyState).toBe(document.readyState);
  });
});

describe('createUITestHarness frontend logs', () => {
  it('throws when the store does not expose logEntries', () => {
    renderHarnessDom();
    const harness = createUITestHarness({
      getState: () => ({ activeThreadId: 'thread-1' }),
      documentRef: document,
      locationRef: window.location,
    });

    expect(() => harness.frontendLogs()).toThrow(/logEntries/);
  });

  it('applies level, source, since, and limit filters and returns exact log keys', () => {
    renderHarnessDom();
    const harness = createUITestHarness({
      getState: () => createState({
        logEntries: FILTERED_LOG_ENTRIES,
      }),
      documentRef: document,
      locationRef: window.location,
    });

    const logs = harness.frontendLogs({
      level: 'info',
      source: 'ui_test_mcp',
      since: '2026-07-08T00:00:01.000Z',
      limit: 1,
    });

    expect(logs).toHaveLength(1);
    expectExactKeys(logs[0], ['id', 'ts', 'level', 'source', 'message', 'fields']);
    expect(logs[0]).toMatchObject({
      id: 'new-match',
      level: 'info',
      source: 'ui_test_mcp',
      message: 'submit_composer',
    });
  });
});

describe('createUITestHarness recordLog', () => {
  beforeEach(() => {
    renderHarnessDom();
  });

  it('sanitizes and persists a ui_test_mcp log entry', () => {
    const state = createState();
    const harness = createUITestHarness({
      getState: () => state,
      documentRef: document,
      locationRef: window.location,
    });

    const entry = harness.recordLog({
      level: 'info',
      source: 'ui_test_mcp',
      message: 'submit_composer',
      fields: { cwd: '/home/me/repo', ok: true },
    });

    expect(state.addLog).toHaveBeenCalledWith('info', 'ui_test_mcp.submit_composer', entry.fields);
    expectExactKeys(entry, ['id', 'ts', 'level', 'source', 'message', 'fields']);
    expect(entry).toMatchObject({
      level: 'info',
      source: 'ui_test_mcp',
      message: 'submit_composer',
    });
  });

  it('rejects unknown fields, unsafe sources, and invalid field payloads without writing', () => {
    const state = createState();
    const harness = createUITestHarness({
      getState: () => state,
      documentRef: document,
      locationRef: window.location,
    });

    expect(() => harness.recordLog({
      level: 'info',
      source: 'ui_test_mcp',
      message: 'submit_composer',
      fields: {},
      extra: true,
    })).toThrow(/unknown/i);
    expect(() => harness.recordLog({
      level: 'info',
      source: 'product',
      message: 'submit_composer',
      fields: {},
    })).toThrow(/source/);
    expect(() => harness.recordLog({
      level: 'info',
      source: 'ui_test_mcp',
      message: 'submit_composer',
    })).toThrow(/fields/);
    expect(() => harness.recordLog({
      level: 'info',
      source: 'ui_test_mcp',
      message: 'submit_composer',
      fields: [],
    })).toThrow(/fields/);
    expect(state.addLog).not.toHaveBeenCalled();
  });

  it('requires addLog and logEntries before writing', () => {
    const noAddLog = createUITestHarness({
      getState: () => ({ logEntries: [] }),
      documentRef: document,
      locationRef: window.location,
    });
    const noLogEntries = createUITestHarness({
      getState: () => ({ addLog: vi.fn() }),
      documentRef: document,
      locationRef: window.location,
    });

    expect(() => noAddLog.recordLog({
      level: 'info',
      source: 'ui_test_mcp',
      message: 'submit_composer',
      fields: {},
    })).toThrow(/addLog/);
    expect(() => noLogEntries.recordLog({
      level: 'info',
      source: 'ui_test_mcp',
      message: 'submit_composer',
      fields: {},
    })).toThrow(/logEntries/);
  });
});

describe('installUITestHarness isolated acceptance helpers', () => {
  it('installs only when enabled and gates isolated submit by token', () => {
    renderHarnessDom();
    let clicked = false;
    document.querySelector('[data-testid="composer-submit"]').addEventListener('click', () => {
      clicked = true;
    });
    const state = createState();
    const windowRef = {
      document,
      location: new URL('http://127.0.0.1:5175/'),
      addEventListener: vi.fn(),
      console: { error: vi.fn() },
      __SUPER_DOLPHIN_UI_TEST_ACCEPTANCE__: { token: 'token-1' },
    };

    const harness = installUITestHarness({
      windowRef,
      getState: () => state,
      metaEnv: { PROD: false, DEV: true },
    });

    expect(windowRef.__SUPER_DOLPHIN_UI_TEST__).toBe(harness);
    expect(harness.verifyIsolatedAcceptance({ token: 'wrong' })).toEqual({
      isolated: false,
      tokenMatched: false,
      reason: 'invalid_acceptance_token',
    });
    expect(harness.verifyIsolatedAcceptance({ token: 'token-1' })).toEqual({
      isolated: true,
      tokenMatched: true,
    });

    const result = harness.submitComposerInIsolation({ token: 'token-1' });

    expect(result.submitted).toBe(true);
    expect(clicked).toBe(false);
    expect(state.addLog).toHaveBeenCalledWith('info', 'ui_test_mcp.submit_composer', expect.any(Object));
  });

  it('does not install in production', () => {
    const windowRef = {
      document,
      location: new URL('http://127.0.0.1:5175/'),
      addEventListener: vi.fn(),
      console: { error: vi.fn() },
    };

    expect(installUITestHarness({
      windowRef,
      getState: () => createState(),
      metaEnv: { PROD: true, DEV: true, VITE_SUPER_DOLPHIN_UI_TEST_MCP: '1' },
    })).toBeNull();
    expect(windowRef.__SUPER_DOLPHIN_UI_TEST__).toBeUndefined();
  });
});
