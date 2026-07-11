import { describe, expect, it } from 'vitest';
import { rightPanelWidthSchema } from './shellLayoutSchema.js';
import shellLayoutSchemaSource from './shellLayoutSchema.js?raw';

function expectRightPanelWidthValidationFailure(run) {
  let failure;
  try {
    run();
  }
  catch (error) {
    failure = error;
  }
  expect(failure).toMatchObject({
    name: 'ShellLayoutValidationError',
    code: 'shell_layout.invalid_right_panel_width',
  });
  return failure;
}

describe('rightPanelWidthSchema', () => {
  it('declares one exact key and the existing explicit initial width', () => {
    expect(rightPanelWidthSchema.key).toBe('super-dolphin.shell.right-panel-width');
    expect(rightPanelWidthSchema.initialValue).toBe(380);
  });

  it('parses the scalar directly without a JSON fallback', () => {
    expect(shellLayoutSchemaSource).not.toContain('JSON.parse');
  });

  it('does not expose a tampered persisted scalar in diagnostics', () => {
    const maliciousValue = '<script>leak-storage-value</script>';
    const failure = expectRightPanelWidthValidationFailure(
      () => rightPanelWidthSchema.parse(maliciousValue),
    );

    expect(failure.message).not.toContain(maliciousValue);
  });

  it.each([
    ['0', 0],
    ['380', 380],
    ['380.5', 380.5],
    [String(Number.MAX_SAFE_INTEGER), Number.MAX_SAFE_INTEGER],
  ])('strictly parses canonical persisted scalar %s independently from viewport clamping', (stored, expected) => {
    expect(rightPanelWidthSchema.parse(stored)).toBe(expected);
  });

  it.each([
    null,
    undefined,
    380,
    '',
    ' 380 ',
    '0380',
    '+380',
    '-1',
    'NaN',
    'Infinity',
    '1e3',
    '380px',
    String(Number.MAX_SAFE_INTEGER + 1),
  ])('rejects invalid or non-canonical persisted scalar %j', (stored) => {
    expectRightPanelWidthValidationFailure(() => rightPanelWidthSchema.parse(stored));
  });

  it.each([
    [0, '0'],
    [380, '380'],
    [380.5, '380.5'],
    [Number.MAX_SAFE_INTEGER, String(Number.MAX_SAFE_INTEGER)],
  ])('serializes in-range numeric width %s without JSON', (value, expected) => {
    expect(rightPanelWidthSchema.serialize(value)).toBe(expected);
  });

  it.each([
    -1,
    Number.NaN,
    Number.POSITIVE_INFINITY,
    Number.MAX_SAFE_INTEGER + 1,
    '380',
  ])('rejects invalid runtime width %j before persistence', (value) => {
    expectRightPanelWidthValidationFailure(() => rightPanelWidthSchema.serialize(value));
  });
});
