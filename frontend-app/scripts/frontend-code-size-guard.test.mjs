import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { describe, expect, it } from 'vitest';
import {
  FRONTEND_CODE_SIZE_LIMITS,
  checkFrontendCodeSizeSource,
  countEffectiveLines,
  extractFunctions,
  measureFrontendCodeSizeSource,
  measureFrontendCodeSizeSourceAstShadow,
  measureMaxNesting,
  parseFrontendCodeSizeGuardArgs,
} from './frontend-code-size-guard.mjs';

const appRoot = process.cwd();
const guardScriptSourcePath = path.join(appRoot, 'scripts/frontend-code-size-guard.mjs');
const guardBaselineSourcePath = path.join(appRoot, 'scripts/lib/frontend-code-size-baseline.mjs');

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

function nestedSource(nesting) {
  const lines = ['function nested() {'];
  for (let depth = 1; depth < nesting; depth += 1) {
    lines.push(`${'  '.repeat(depth)}if (value${depth}) {`);
  }
  lines.push(`${'  '.repeat(nesting)}return true;`);
  for (let depth = nesting - 1; depth > 0; depth -= 1) {
    lines.push(`${'  '.repeat(depth)}}`);
  }
  lines.push('}');
  return lines.join('\n');
}

function functionSource(name, lineCount) {
  return [
    `function ${name}() {`,
    ...Array.from({ length: lineCount - 2 }, (_, index) => `  const value${index} = ${index};`),
    '}',
  ].join('\n');
}

function frozenMetricsForSource(relFile, source) {
  const violations = checkFrontendCodeSizeSource(relFile, source);
  return {
    ...measureFrontendCodeSizeSource(relFile, source),
    frozenViolations: violations.map((entry) => `${entry.rule}\0${entry.message}`).sort(),
  };
}

function runGuardWithFixture({
  currentLines,
  currentSource,
  frozenLines,
  frozenProductionFiles,
  guardArgs = [],
  frozenTestFiles = {},
  relFile = 'src/fixture.js',
  scanDir = 'src',
}) {
  const fixtureRoot = fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(), 'frontend-code-size-guard-')));
  const fixtureSourceDir = path.join(fixtureRoot, 'src');
  const fixtureScriptDir = path.join(fixtureRoot, 'scripts');
  const fixtureGuardScriptPath = path.join(fixtureScriptDir, 'frontend-code-size-guard.mjs');
  const fixtureBaselinePath = path.join(fixtureRoot, '.frontend_code_size_guard_baseline.json');
  const fixtureBaselineTestPath = path.join(fixtureRoot, '.frontend_code_size_guard_baseline_test.json');
  const sourcePath = path.join(fixtureRoot, relFile);

  try {
    fs.mkdirSync(fixtureSourceDir, { recursive: true });
    fs.mkdirSync(fixtureScriptDir, { recursive: true });
    fs.mkdirSync(path.join(fixtureScriptDir, 'lib'), { recursive: true });
    fs.mkdirSync(path.dirname(sourcePath), { recursive: true });
    fs.symlinkSync(path.join(appRoot, 'node_modules'), path.join(fixtureRoot, 'node_modules'), 'dir');
    fs.copyFileSync(guardScriptSourcePath, fixtureGuardScriptPath);
    fs.copyFileSync(guardBaselineSourcePath, path.join(fixtureScriptDir, 'lib/frontend-code-size-baseline.mjs'));
    fs.writeFileSync(sourcePath, currentSource ?? sourceWithEffectiveLines(currentLines), 'utf8');
    const files = frozenProductionFiles
      ?? (frozenLines === undefined ? {} : { [relFile]: frozenFileLengthMetrics(frozenLines) });
    fs.writeFileSync(fixtureBaselinePath, `${JSON.stringify(baselineData(files), null, 2)}\n`, 'utf8');
    fs.writeFileSync(fixtureBaselineTestPath, `${JSON.stringify(baselineData(frozenTestFiles), null, 2)}\n`, 'utf8');

    const result = spawnSync(process.execPath, [
      fixtureGuardScriptPath,
      ...(scanDir ? ['--dir', scanDir] : []),
      ...guardArgs,
    ], {
      cwd: fixtureRoot,
      encoding: 'utf8',
    });
    if (result.error) throw result.error;
    return {
      status: result.status,
      output: `${result.stdout}${result.stderr}`,
      productionBaseline: JSON.parse(fs.readFileSync(fixtureBaselinePath, 'utf8')),
      testBaseline: JSON.parse(fs.readFileSync(fixtureBaselineTestPath, 'utf8')),
    };
  } finally {
    fs.rmSync(fixtureRoot, { recursive: true, force: true });
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

  it('enforces a dedicated test file limit while keeping production-only rules disabled', () => {
    const testSource = [
      ...Array.from({ length: FRONTEND_CODE_SIZE_LIMITS.maxTestFileLines + 1 }, (_, index) => `const value${index} = ${index};`),
      'describe("suite", () => {',
      '  it("case", () => {',
      '    console.log("stub");',
      '    const noop = () => {};',
      '  });',
      '});',
    ].join('\n');

    for (const relFile of ['src/example.test.jsx', 'src/example.test-helper.js']) {
      expect(checkFrontendCodeSizeSource(relFile, testSource)).toEqual([
        expect.objectContaining({ rule: 'file-length' }),
      ]);
    }
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

  it('keeps AST shadow metrics aligned with handwritten metrics for key scenarios', () => {
    const source = [
      'export function outer(a, b, c, d, e, f) {',
      '  if (first) {',
      '    for (const item of items) {',
      '      if (item.ok) {',
      '        console.log(item);',
      '      }',
      '    }',
      '  }',
      '}',
      'export const makePayload = (value) => {',
      '  return { value };',
      '};',
      `const longLine = '${'x'.repeat(FRONTEND_CODE_SIZE_LIMITS.maxLineLength + 1)}';`,
    ].join('\n');

    expect(measureFrontendCodeSizeSourceAstShadow('src/nested.js', source)).toEqual(
      measureFrontendCodeSizeSource('src/nested.js', source),
    );
  });

  it('parses scope and repeatable file filters', () => {
    expect(parseFrontendCodeSizeGuardArgs([]).dirs.map((dir) => path.basename(dir))).toEqual(['src', 'scripts']);
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

  it('keeps the production gate scanning src and non-test scripts in one guard invocation', () => {
    const packageJSON = JSON.parse(fs.readFileSync(path.join(appRoot, 'package.json'), 'utf8'));
    const command = packageJSON.scripts?.['guard:critical-skip'];
    expect(command).toEqual(expect.any(String));
    const productionInvocations = command
      .split('&&')
      .map((step) => step.trim())
      .filter((step) => step.includes('frontend-code-size-guard.mjs --scope production'));
    expect(productionInvocations).toEqual([
      'node scripts/frontend-code-size-guard.mjs --scope production --dir src --dir scripts',
    ]);
  });

  it('rejects new production debt in non-test scripts', () => {
    const result = runGuardWithFixture({
      currentLines: FRONTEND_CODE_SIZE_LIMITS.maxFileLines + 1,
      frozenLines: undefined,
      guardArgs: ['--scope', 'production'],
      relFile: 'scripts/non-test.mjs',
      scanDir: 'scripts',
    });

    expect(result.status).toBe(1);
    expect(result.output).toContain('[file-length]');
    expect(result.output).toContain('scripts/non-test.mjs');
  });

  it('allows frozen nesting debt to improve and rejects a nesting regression', () => {
    const relFile = 'src/fixture.js';
    const frozenSource = nestedSource(6);
    const frozenProductionFiles = {
      [relFile]: frozenMetricsForSource(relFile, frozenSource),
    };
    const improved = runGuardWithFixture({
      currentSource: nestedSource(5),
      frozenProductionFiles,
      guardArgs: ['--scope', 'production'],
    });
    const regressed = runGuardWithFixture({
      currentSource: nestedSource(7),
      frozenProductionFiles,
      guardArgs: ['--scope', 'production'],
    });

    expect(improved.status, improved.output).toBe(0);
    expect(regressed.status).toBe(1);
    expect(regressed.output).toContain('[freeze/nesting]');
  });

  it('keeps function debt attached to its function while allowing that function to improve', () => {
    const relFile = 'src/fixture.js';
    const frozenSource = functionSource('oldDebt', 160);
    const frozenProductionFiles = {
      [relFile]: frozenMetricsForSource(relFile, frozenSource),
    };
    const improved = runGuardWithFixture({
      currentSource: functionSource('oldDebt', 155),
      frozenProductionFiles,
      guardArgs: ['--scope', 'production'],
    });
    const migrated = runGuardWithFixture({
      currentSource: functionSource('newDebt', 160),
      frozenProductionFiles,
      guardArgs: ['--scope', 'production'],
    });
    const regressed = runGuardWithFixture({
      currentSource: functionSource('oldDebt', 161),
      frozenProductionFiles,
      guardArgs: ['--scope', 'production'],
    });

    expect(improved.status, improved.output).toBe(0);
    expect(migrated.status).toBe(1);
    expect(migrated.output).toContain('[func-length]');
    expect(regressed.status).toBe(1);
    expect(regressed.output).toContain('[freeze/func-length]');
  });

  it('preserves the test baseline when freezing only the production scope', () => {
    const frozenTestFiles = {
      'src/existing.test.js': frozenFileLengthMetrics(FRONTEND_CODE_SIZE_LIMITS.maxTestFileLines + 1),
    };
    const result = runGuardWithFixture({
      currentLines: FRONTEND_CODE_SIZE_LIMITS.maxFileLines + 1,
      frozenLines: undefined,
      guardArgs: ['--freeze', '--scope', 'production'],
      frozenTestFiles,
    });

    expect(result.status, result.output).toBe(0);
    expect(result.testBaseline.files).toEqual(frozenTestFiles);
  });

  it('preserves the opposite-scope baseline during a scoped check', () => {
    const frozenTestFiles = {
      'src/existing.test.js': frozenFileLengthMetrics(FRONTEND_CODE_SIZE_LIMITS.maxTestFileLines + 1),
    };
    const result = runGuardWithFixture({
      currentLines: 1,
      frozenLines: undefined,
      guardArgs: ['--scope', 'production'],
      frozenTestFiles,
    });

    expect(result.status, result.output).toBe(0);
    expect(result.testBaseline.files).toEqual(frozenTestFiles);
  });

  it('preserves unscanned production baseline entries during a partial file check', () => {
    const frozenProductionFiles = {
      'scripts/unscanned.mjs': frozenFileLengthMetrics(FRONTEND_CODE_SIZE_LIMITS.maxFileLines + 1),
    };
    const result = runGuardWithFixture({
      currentSource: 'const clean = true;',
      frozenProductionFiles,
      guardArgs: ['--scope', 'production', '--file', 'src/fixture.js'],
    });

    expect(result.status, result.output).toBe(0);
    expect(result.productionBaseline.files).toEqual(frozenProductionFiles);
  });

  it('prunes a missing production entry only during a canonical full scan', () => {
    const guardRelFile = 'scripts/frontend-code-size-guard.mjs';
    const frozenProductionFiles = {
      [guardRelFile]: frozenMetricsForSource(guardRelFile, fs.readFileSync(guardScriptSourcePath, 'utf8')),
      'scripts/missing.mjs': frozenFileLengthMetrics(FRONTEND_CODE_SIZE_LIMITS.maxFileLines + 1),
    };
    const result = runGuardWithFixture({
      currentSource: 'const clean = true;',
      frozenProductionFiles,
      guardArgs: ['--scope', 'production'],
      scanDir: null,
    });

    expect(result.status, result.output).toBe(0);
    expect(result.productionBaseline.files).not.toHaveProperty('scripts/missing.mjs');
  });

  it('allows frozen file-length to decrease while remaining over the limit', () => {
    const result = runGuardWithFixture({
      currentLines: FRONTEND_CODE_SIZE_LIMITS.maxFileLines + 1,
      frozenLines: FRONTEND_CODE_SIZE_LIMITS.maxFileLines + 3,
    });

    expect(result.status, result.output).toBe(0);
    expect(result.output).not.toContain('[file-length]');
  });

  it('reports frozen file-length growth as a ratchet violation', () => {
    const result = runGuardWithFixture({
      currentLines: FRONTEND_CODE_SIZE_LIMITS.maxFileLines + 2,
      frozenLines: FRONTEND_CODE_SIZE_LIMITS.maxFileLines + 1,
    });

    expect(result).toEqual(expect.objectContaining({ status: 1 }));
    expect(result.output).toContain('[freeze/file-length]');
    expect(result.output).not.toContain('[file-length] 文件有效代码');
  });

  it('reports non-frozen file-length violations', () => {
    const result = runGuardWithFixture({
      currentLines: FRONTEND_CODE_SIZE_LIMITS.maxFileLines + 1,
      frozenLines: undefined,
    });

    expect(result).toEqual(expect.objectContaining({ status: 1 }));
    expect(result.output).toContain('[file-length]');
    expect(result.output).not.toContain('[freeze/file-length]');
  });
});
