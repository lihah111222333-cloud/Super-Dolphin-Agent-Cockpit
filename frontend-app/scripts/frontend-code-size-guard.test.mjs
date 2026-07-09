import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
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

const appRoot = process.cwd();
const baselinePath = path.join(appRoot, '.frontend_code_size_guard_baseline.json');
const baselineTestPath = path.join(appRoot, '.frontend_code_size_guard_baseline_test.json');
const guardScriptPath = path.join(appRoot, 'scripts/frontend-code-size-guard.mjs');

function toRel(filePath) {
  return path.relative(appRoot, filePath).split(path.sep).join('/');
}

function sourceWithEffectiveLines(lineCount) {
  return Array.from({ length: lineCount }, (_, index) => `const value${index} = ${index};`).join('\n');
}

function baselineData(files = {}) {
  return {
    _meta: { updatedAt: '2026-07-09T00:00:00Z' },
    files,
  };
}

function frozenFileLengthMetrics(lines) {
  return {
    lines,
    frozenViolations: [
      `file-length\0文件有效代码 ${lines} 行，超过上限 ${FRONTEND_CODE_SIZE_LIMITS.maxFileLines} 行`,
    ],
  };
}

function readIfExists(filePath) {
  return fs.existsSync(filePath) ? fs.readFileSync(filePath, 'utf8') : null;
}

function restoreFile(filePath, content) {
  if (content === null) fs.rmSync(filePath, { force: true });
  else fs.writeFileSync(filePath, content, 'utf8');
}

function runGuardWithFixture({ currentLines, frozenLines }) {
  const fixtureDir = fs.mkdtempSync(path.join(appRoot, '.tmp-code-size-guard-'));
  const sourcePath = path.join(fixtureDir, 'fixture.js');
  const relFile = toRel(sourcePath);
  const prodBaseline = readIfExists(baselinePath);
  const testBaseline = readIfExists(baselineTestPath);

  try {
    fs.writeFileSync(sourcePath, sourceWithEffectiveLines(currentLines), 'utf8');
    const files = frozenLines === undefined ? {} : { [relFile]: frozenFileLengthMetrics(frozenLines) };
    fs.writeFileSync(baselinePath, `${JSON.stringify(baselineData(files), null, 2)}\n`, 'utf8');
    fs.writeFileSync(baselineTestPath, `${JSON.stringify(baselineData(), null, 2)}\n`, 'utf8');

    const result = spawnSync(process.execPath, [
      guardScriptPath,
      '--dir',
      toRel(fixtureDir),
    ], {
      cwd: appRoot,
      encoding: 'utf8',
    });
    if (result.error) throw result.error;
    return {
      status: result.status,
      output: `${result.stdout}${result.stderr}`,
    };
  } finally {
    restoreFile(baselinePath, prodBaseline);
    restoreFile(baselineTestPath, testBaseline);
    fs.rmSync(fixtureDir, { recursive: true, force: true });
  }
}

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

  it('allows frozen file-length to decrease while remaining over the limit', () => {
    const result = runGuardWithFixture({
      currentLines: FRONTEND_CODE_SIZE_LIMITS.maxFileLines + 1,
      frozenLines: FRONTEND_CODE_SIZE_LIMITS.maxFileLines + 3,
    });

    expect(result.status).toBe(0);
    expect(result.output).not.toContain('[file-length]');
  });

  it('reports frozen file-length growth as a ratchet violation', () => {
    const result = runGuardWithFixture({
      currentLines: FRONTEND_CODE_SIZE_LIMITS.maxFileLines + 2,
      frozenLines: FRONTEND_CODE_SIZE_LIMITS.maxFileLines + 1,
    });

    expect(result.status).toBe(1);
    expect(result.output).toContain('[freeze/file-length]');
    expect(result.output).not.toContain('[file-length] 文件有效代码');
  });

  it('reports non-frozen file-length violations', () => {
    const result = runGuardWithFixture({
      currentLines: FRONTEND_CODE_SIZE_LIMITS.maxFileLines + 1,
      frozenLines: undefined,
    });

    expect(result.status).toBe(1);
    expect(result.output).toContain('[file-length]');
    expect(result.output).not.toContain('[freeze/file-length]');
  });
});
