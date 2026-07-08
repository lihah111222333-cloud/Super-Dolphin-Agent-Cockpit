import { describe, expect, it } from 'vitest';
import {
  FRONTEND_CODE_SIZE_LIMITS,
  checkFrontendCodeSizeSource,
  countEffectiveLines,
  extractFunctions,
  measureFrontendCodeSizeSource,
  measureMaxNesting,
  parseFrontendCodeSizeGuardArgs,
} from './frontend-code-size-guard.mjs';

describe('frontend code size guard', () => {
  it('counts effective lines without comments and blank lines', () => {
    expect(countEffectiveLines([
      '',
      '// comment',
      '/* block',
      'still comment */',
      'const value = 1;',
      'const other = 2; // inline comment still code',
    ])).toBe(2);
  });

  it('detects oversized files, functions, exports, params, and long lines', () => {
    const oversizedFile = Array.from(
      { length: FRONTEND_CODE_SIZE_LIMITS.maxFileLines + 1 },
      (_, index) => `const value${index} = ${index};`,
    ).join('\n');
    expect(checkFrontendCodeSizeSource('src/large.js', oversizedFile)).toEqual(expect.arrayContaining([
      expect.objectContaining({ rule: 'file-length' }),
    ]));

    const longFunction = [
      'export function tooLarge(a, b, c, d, e, f) {',
      ...Array.from({ length: FRONTEND_CODE_SIZE_LIMITS.maxFunctionLines }, (_, index) => `  const value${index} = ${index};`),
      '}',
      ...Array.from({ length: FRONTEND_CODE_SIZE_LIMITS.maxExports + 1 }, (_, index) => `export const value${index} = ${index};`),
      `const compact = '${'x'.repeat(FRONTEND_CODE_SIZE_LIMITS.maxLineLength + 1)}';`,
    ].join('\n');

    expect(checkFrontendCodeSizeSource('src/shape.js', longFunction).map((entry) => entry.rule)).toEqual(expect.arrayContaining([
      'func-length',
      'params',
      'exports',
      'line-length',
    ]));
  });

  it('keeps test files out of production size debt rules', () => {
    const testSource = [
      ...Array.from({ length: FRONTEND_CODE_SIZE_LIMITS.maxFileLines + 1 }, (_, index) => `const value${index} = ${index};`),
      'describe("suite", () => {',
      '  it("case", () => {',
      '    console.log("stub");',
      '    const noop = () => {};',
      '  });',
      '});',
    ].join('\n');

    expect(checkFrontendCodeSizeSource('src/example.test.jsx', testSource)).toEqual([]);
    expect(checkFrontendCodeSizeSource('src/example.test-helper.js', testSource)).toEqual([]);
  });

  it('measures function and nesting metrics for baseline ratchets', () => {
    const source = `
      function outer() {
        if (first) {
          for (const item of items) {
            if (item.ok) {
              console.log(item);
            }
          }
        }
      }
    `;

    const lines = source.split('\n');
    expect(extractFunctions(lines)).toEqual([expect.objectContaining({ name: 'outer' })]);
    expect(measureMaxNesting(lines)).toBeGreaterThan(3);
    expect(measureFrontendCodeSizeSource('src/nested.js', source)).toEqual(expect.objectContaining({
      consoleLogs: 1,
      maxNesting: expect.any(Number),
    }));
  });

  it('parses scope and repeatable file filters', () => {
    expect(parseFrontendCodeSizeGuardArgs(['--scope', 'production']).scope).toBe('production');
    expect(parseFrontendCodeSizeGuardArgs(['--scope', 'test']).scope).toBe('test');
    expect(parseFrontendCodeSizeGuardArgs(['--scope', 'all']).scope).toBe('all');
    expect(parseFrontendCodeSizeGuardArgs(['--file', 'src/App.jsx']).files).toEqual(['src/App.jsx']);
    expect(parseFrontendCodeSizeGuardArgs([
      '--file',
      'src/App.jsx',
      '--file',
      'src/AppShell.jsx',
    ]).files).toEqual(['src/App.jsx', 'src/AppShell.jsx']);
    expect(() => parseFrontendCodeSizeGuardArgs(['--scope', 'bad'])).toThrow(/invalid value for --scope/);
  });
});
