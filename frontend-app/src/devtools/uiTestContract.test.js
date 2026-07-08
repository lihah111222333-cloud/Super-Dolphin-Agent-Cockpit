import { describe, expect, it } from 'vitest';
import {
  UI_TEST_ACTIONS,
  UI_TEST_GLOBAL,
  UI_TEST_LIMITS,
  UI_TEST_ROUTES,
  UI_TEST_TARGETS,
  UI_TEST_TOOLS,
  UI_TEST_WAIT_STATES,
  assertKnownActionName,
  assertKnownTargetName,
  assertKnownToolName,
  normalizeLimit,
  normalizeTimeoutMs,
  validateExactKeys,
} from './uiTestContract.js';

function expectExactUniqueValues(values, expected) {
  expect(values).toEqual(expected);
  expect(new Set(values).size).toBe(values.length);
}

describe('uiTestContract constants', () => {
  it('exports exact global name', () => {
    expect(UI_TEST_GLOBAL).toBe('__SUPER_DOLPHIN_UI_TEST__');
  });

  it('exports exact tool names without duplicates', () => {
    expectExactUniqueValues(UI_TEST_TOOLS, [
      'ui_snapshot',
      'ui_action',
      'ui_diagnostics',
      'ui_frontend_logs',
    ]);
  });

  it('exports exact action names without duplicates', () => {
    expectExactUniqueValues(UI_TEST_ACTIONS, [
      'navigate',
      'fill_composer',
      'submit_composer',
      'wait_for',
    ]);
  });

  it('exports exact target names without duplicates', () => {
    expectExactUniqueValues(UI_TEST_TARGETS, [
      'composer_input',
      'composer_submit',
    ]);
  });

  it('exports exact routes and wait states without duplicates', () => {
    expect(UI_TEST_ROUTES).toEqual({
      chat: '/',
      settings: '/settings',
      observability: '/observability',
    });
    expectExactUniqueValues(UI_TEST_WAIT_STATES, [
      'frontend_ready',
      'composer_text_length',
      'route',
    ]);
  });

  it('exports exact shared limits', () => {
    expect(UI_TEST_LIMITS).toEqual({
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
  });
});

describe('uiTestContract validators', () => {
  it('accepts known names and rejects unknown names', () => {
    expect(assertKnownToolName('ui_snapshot')).toBe('ui_snapshot');
    expect(assertKnownActionName('navigate')).toBe('navigate');
    expect(assertKnownTargetName('composer_input')).toBe('composer_input');

    expect(() => assertKnownToolName('eval_js')).toThrow('unknown UI test tool: eval_js');
    expect(() => assertKnownActionName('click_selector')).toThrow('unknown UI test action: click_selector');
    expect(() => assertKnownTargetName('body')).toThrow('unknown UI test target: body');
  });

  it('normalizes limits and timeouts through contract bounds', () => {
    expect(normalizeLimit(undefined)).toBe(UI_TEST_LIMITS.defaultLimit);
    expect(normalizeLimit(null)).toBe(UI_TEST_LIMITS.defaultLimit);
    expect(normalizeLimit(1)).toBe(1);
    expect(normalizeLimit(UI_TEST_LIMITS.maxLimit + 1)).toBe(UI_TEST_LIMITS.maxLimit);

    expect(normalizeTimeoutMs(undefined)).toBe(UI_TEST_LIMITS.defaultTimeoutMs);
    expect(normalizeTimeoutMs(null)).toBe(UI_TEST_LIMITS.defaultTimeoutMs);
    expect(normalizeTimeoutMs(250)).toBe(250);
    expect(normalizeTimeoutMs(UI_TEST_LIMITS.maxTimeoutMs + 1)).toBe(UI_TEST_LIMITS.maxTimeoutMs);
  });

  it('rejects invalid limit and timeout values', () => {
    expect(() => normalizeLimit(0)).toThrow('limit must be a positive integer');
    expect(() => normalizeLimit(1.5)).toThrow('limit must be a positive integer');
    expect(() => normalizeLimit('10')).toThrow('limit must be a positive integer');

    expect(() => normalizeTimeoutMs(0)).toThrow('timeoutMs must be a positive integer');
    expect(() => normalizeTimeoutMs(1.5)).toThrow('timeoutMs must be a positive integer');
    expect(() => normalizeTimeoutMs('100')).toThrow('timeoutMs must be a positive integer');
  });

  it('rejects unknown fields instead of ignoring them', () => {
    const value = { action: 'navigate', route: 'chat' };

    expect(validateExactKeys(value, ['action', 'route'], 'ui_action arguments')).toBe(value);
    expect(() => validateExactKeys({ action: 'navigate', selector: 'body' }, ['action'], 'ui_action arguments'))
      .toThrow('ui_action arguments contains unknown field: selector');
  });

  it('rejects non-object values and duplicate allowed keys', () => {
    expect(() => validateExactKeys(null, ['action'], 'ui_action arguments'))
      .toThrow('ui_action arguments must be a plain object');
    expect(() => validateExactKeys([], ['action'], 'ui_action arguments'))
      .toThrow('ui_action arguments must be a plain object');
    expect(() => validateExactKeys({}, ['action', 'action'], 'ui_action arguments'))
      .toThrow('ui_action arguments allowed keys contain duplicate: action');
  });
});
