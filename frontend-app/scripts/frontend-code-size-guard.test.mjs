import { spawn, spawnSync } from 'node:child_process';
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
import {
  assertBaselineUpdateOnlyImproves,
  acquireBaselineLock,
  baselineBytes,
  formatBaselineTransactionErrorForStderr,
  hashBaselineBytes,
  writeBaselineTransaction,
} from './lib/frontend-code-size-baseline-transaction.mjs';

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
    maxFuncLen: 0,
    maxNesting: 0,
    maxParams: 0,
    exportCount: 0,
    consoleLogs: 0,
    anyCount: 0,
    emptyFuncs: 0,
    [['to', 'do', 'Count'].join('')]: 0,
    longLineCount: 0,
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
  extraSources = {},
  omitProductionBaseline = false,
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
    const fixtureBabelRoot = path.join(fixtureRoot, 'node_modules/@babel');
    fs.mkdirSync(fixtureBabelRoot, { recursive: true });
    fs.cpSync(
      path.join(appRoot, 'node_modules/@babel/parser'),
      path.join(fixtureBabelRoot, 'parser'),
      { recursive: true },
    );
    fs.copyFileSync(guardScriptSourcePath, fixtureGuardScriptPath);
    fs.copyFileSync(guardBaselineSourcePath, path.join(fixtureScriptDir, 'lib/frontend-code-size-baseline.mjs'));
    fs.copyFileSync(
      path.join(appRoot, 'scripts/lib/frontend-code-size-baseline-transaction.mjs'),
      path.join(fixtureScriptDir, 'lib/frontend-code-size-baseline-transaction.mjs'),
    );
    fs.copyFileSync(
      path.join(appRoot, 'scripts/lib/frontend-code-size-guard-runner.mjs'),
      path.join(fixtureScriptDir, 'lib/frontend-code-size-guard-runner.mjs'),
    );
    fs.writeFileSync(sourcePath, currentSource ?? sourceWithEffectiveLines(currentLines), 'utf8');
    for (const [extraRelFile, extraSource] of Object.entries(extraSources)) {
      const extraPath = path.join(fixtureRoot, extraRelFile);
      fs.mkdirSync(path.dirname(extraPath), { recursive: true });
      fs.writeFileSync(extraPath, extraSource, 'utf8');
    }
    const files = frozenProductionFiles
      ?? (frozenLines === undefined ? {} : { [relFile]: frozenFileLengthMetrics(frozenLines) });
    if (!omitProductionBaseline) {
      fs.writeFileSync(fixtureBaselinePath, `${JSON.stringify(baselineData(files), null, 2)}\n`, 'utf8');
    }
    fs.writeFileSync(fixtureBaselineTestPath, `${JSON.stringify(baselineData(frozenTestFiles), null, 2)}\n`, 'utf8');
    const productionBaselineBefore = fs.existsSync(fixtureBaselinePath) ? fs.readFileSync(fixtureBaselinePath) : null;
    const testBaselineBefore = fs.readFileSync(fixtureBaselineTestPath);
    const productionStatBefore = fs.existsSync(fixtureBaselinePath) ? fs.statSync(fixtureBaselinePath, { bigint: true }) : null;
    const testStatBefore = fs.statSync(fixtureBaselineTestPath, { bigint: true });

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
      productionBaselineBefore,
      productionBaselineAfter: fs.existsSync(fixtureBaselinePath) ? fs.readFileSync(fixtureBaselinePath) : null,
      testBaselineBefore,
      testBaselineAfter: fs.readFileSync(fixtureBaselineTestPath),
      productionStatBefore,
      productionStatAfter: fs.existsSync(fixtureBaselinePath) ? fs.statSync(fixtureBaselinePath, { bigint: true }) : null,
      testStatBefore,
      testStatAfter: fs.statSync(fixtureBaselineTestPath, { bigint: true }),
      productionBaseline: fs.existsSync(fixtureBaselinePath)
        ? JSON.parse(fs.readFileSync(fixtureBaselinePath, 'utf8'))
        : null,
      testBaseline: JSON.parse(fs.readFileSync(fixtureBaselineTestPath, 'utf8')),
    };
  } finally {
    fs.rmSync(fixtureRoot, { recursive: true, force: true });
  }
}

describe('frontend code size guard', () => {
  it('projects typed transaction failures without serializing private message, state, or cause', () => {
    const secret = 'SENTINEL-PRIVATE-BASELINE-BYTES';
    const error = new Error(`/private/secret/baseline.json ${secret}`, {
      cause: new Error(`/private/secret/cause ${secret}`),
    });
    error.code = 'BASELINE_COMMITTED_DURABILITY_UNKNOWN';
    error.phase = 'post-commit-cleanup';
    error.recoveryAction = 'inspect-final-state-and-marker-without-mutating';
    error.finalState = { bytes: secret, hash: 'raw-next-hash' };
    const output = formatBaselineTransactionErrorForStderr(error);
    expect(output).toBe(
      'code=BASELINE_COMMITTED_DURABILITY_UNKNOWN phase=post-commit-cleanup recoveryAction=inspect-final-state-and-marker-without-mutating',
    );
    expect(output).not.toContain('/private/secret');
    expect(output).not.toContain(secret);
    expect(output).not.toContain('raw-next-hash');
  });

  it('fails closed for unknown codes and missing or mutated public recovery fields', () => {
    const unknown = Object.assign(new Error('private'), {
      code: 'BASELINE_SENTINEL_UNKNOWN', phase: 'sentinel-phase', recoveryAction: 'inspect-without-mutating',
    });
    expect(() => formatBaselineTransactionErrorForStderr(unknown)).toThrow(/unknown public error code/);

    const missing = Object.assign(new Error('private'), { code: 'BASELINE_COMMITTED_DURABILITY_UNKNOWN' });
    expect(() => formatBaselineTransactionErrorForStderr(missing)).toThrow(/phase/);

    Object.assign(missing, { phase: 'post-commit-cleanup', recoveryAction: 'delete-everything' });
    expect(() => formatBaselineTransactionErrorForStderr(missing)).toThrow(/recovery action/);
  });

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
    expect(parseFrontendCodeSizeGuardArgs(['--check']).mode).toBe('check');
    expect(parseFrontendCodeSizeGuardArgs(['--update', '--scope', 'production']).mode).toBe('update');
    expect(() => parseFrontendCodeSizeGuardArgs(['--check', '--update'])).toThrow(/exactly one mode/);
    expect(() => parseFrontendCodeSizeGuardArgs(['--scope', 'bad'])).toThrow(/invalid value for --scope/);
  });

  it('keeps the production gate scanning src and non-test scripts in one guard invocation', () => {
    const packageJSON = JSON.parse(fs.readFileSync(path.join(appRoot, 'package.json'), 'utf8'));
    const command = packageJSON.scripts?.['guard:critical-skip'];
    expect(command).toEqual(expect.any(String));
    const productionInvocations = command
      .split('&&')
      .map((step) => step.trim())
      .filter((step) => step.includes('frontend-code-size-guard.mjs --check --scope production'));
    expect(productionInvocations).toEqual([
      'node scripts/frontend-code-size-guard.mjs --check --scope production --dir src --dir scripts',
    ]);
    expect(command).not.toMatch(/frontend-code-size-guard\.mjs --(?:update|freeze)/);
    expect(packageJSON.scripts?.['test:hook:preflight']).toMatch(/^npm run guard:critical-skip\s*&&/);
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

  it('preserves the test baseline when updating only the production scope', () => {
    const frozenTestFiles = {
      'src/existing.test.js': frozenFileLengthMetrics(FRONTEND_CODE_SIZE_LIMITS.maxTestFileLines + 1),
    };
    const guardRelFile = 'scripts/frontend-code-size-guard.mjs';
    const result = runGuardWithFixture({
      currentLines: FRONTEND_CODE_SIZE_LIMITS.maxFileLines + 1,
      frozenProductionFiles: {
        [guardRelFile]: frozenMetricsForSource(guardRelFile, fs.readFileSync(guardScriptSourcePath, 'utf8')),
        'src/fixture.js': frozenFileLengthMetrics(FRONTEND_CODE_SIZE_LIMITS.maxFileLines + 3),
      },
      guardArgs: ['--update', '--scope', 'production'],
      frozenTestFiles,
      scanDir: null,
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
    expect(result.productionBaselineAfter.equals(result.productionBaselineBefore)).toBe(true);
    expect(result.testBaselineAfter.equals(result.testBaselineBefore)).toBe(true);
    expect(result.productionStatAfter.mtimeNs).toBe(result.productionStatBefore.mtimeNs);
    expect(result.testStatAfter.mtimeNs).toBe(result.testStatBefore.mtimeNs);
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

  it('reports canonical candidate drift without pruning or touching the tracked baseline', () => {
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

    expect(result.status).toBe(1);
    expect(result.output).toContain('baseline drift');
    expect(result.output).toContain('--update');
    expect(result.productionBaseline.files).toEqual(frozenProductionFiles);
    expect(result.productionBaselineAfter.equals(result.productionBaselineBefore)).toBe(true);
    expect(result.testBaselineAfter.equals(result.testBaselineBefore)).toBe(true);
    expect(result.productionStatAfter.mtimeNs).toBe(result.productionStatBefore.mtimeNs);
    expect(result.testStatAfter.mtimeNs).toBe(result.testStatBefore.mtimeNs);
  });

  it('reports improvement and debt clearance as candidate drift without writing', () => {
    const guardRelFile = 'scripts/frontend-code-size-guard.mjs';
    const guardEntry = frozenMetricsForSource(guardRelFile, fs.readFileSync(guardScriptSourcePath, 'utf8'));
    const improved = runGuardWithFixture({
      currentLines: FRONTEND_CODE_SIZE_LIMITS.maxFileLines + 1,
      frozenProductionFiles: {
        [guardRelFile]: guardEntry,
        'src/fixture.js': frozenFileLengthMetrics(FRONTEND_CODE_SIZE_LIMITS.maxFileLines + 3),
      },
      guardArgs: ['--check', '--scope', 'production'],
      scanDir: null,
    });
    const cleared = runGuardWithFixture({
      currentSource: 'const clean = true;',
      frozenProductionFiles: {
        [guardRelFile]: guardEntry,
        'src/fixture.js': frozenFileLengthMetrics(FRONTEND_CODE_SIZE_LIMITS.maxFileLines + 1),
      },
      guardArgs: ['--check', '--scope', 'production'],
      scanDir: null,
    });

    for (const result of [improved, cleared]) {
      expect(result.status).toBe(1);
      expect(result.output).toContain('baseline drift');
      expect(result.output).toContain('--update --scope production');
      expect(result.productionBaselineAfter.equals(result.productionBaselineBefore)).toBe(true);
      expect(result.productionStatAfter.mtimeNs).toBe(result.productionStatBefore.mtimeNs);
    }
  });

  it('reports directory ratchet improvement as drift without changing the directory entry', () => {
    const guardRelFile = 'scripts/frontend-code-size-guard.mjs';
    const extraSources = Object.fromEntries(
      Array.from({ length: 15 }, (_, index) => [`src/ratchet/file${index + 1}.js`, `export const value${index + 1} = true;`]),
    );
    const result = runGuardWithFixture({
      currentSource: 'export const value0 = true;',
      relFile: 'src/ratchet/file0.js',
      extraSources,
      frozenProductionFiles: {
        [guardRelFile]: frozenMetricsForSource(guardRelFile, fs.readFileSync(guardScriptSourcePath, 'utf8')),
        '__dir__:src/ratchet': { lines: 17 },
      },
      guardArgs: ['--check', '--scope', 'production'],
      scanDir: null,
    });

    expect(result.status).toBe(1);
    expect(result.output).toContain('baseline drift');
    expect(result.productionBaseline.files['__dir__:src/ratchet']).toEqual({ lines: 17 });
    expect(result.productionBaselineAfter.equals(result.productionBaselineBefore)).toBe(true);
  });

  it('fails fast when a tracked baseline is missing and never creates it', () => {
    const result = runGuardWithFixture({
      currentSource: 'const clean = true;',
      guardArgs: ['--check', '--scope', 'production'],
      omitProductionBaseline: true,
    });

    expect(result.status).toBe(2);
    expect(result.output).toContain('tracked baseline is missing');
    expect(result.productionBaselineBefore).toBeNull();
    expect(result.productionBaselineAfter).toBeNull();
    expect(result.testBaselineAfter.equals(result.testBaselineBefore)).toBe(true);
  });

  it('allows only canonical update to write an improvement and prints the review diff first', () => {
    const guardRelFile = 'scripts/frontend-code-size-guard.mjs';
    const result = runGuardWithFixture({
      currentLines: FRONTEND_CODE_SIZE_LIMITS.maxFileLines + 1,
      frozenProductionFiles: {
        [guardRelFile]: frozenMetricsForSource(guardRelFile, fs.readFileSync(guardScriptSourcePath, 'utf8')),
        'src/fixture.js': frozenFileLengthMetrics(FRONTEND_CODE_SIZE_LIMITS.maxFileLines + 3),
      },
      guardArgs: ['--update', '--scope', 'production'],
      scanDir: null,
    });

    expect(result.status, result.output).toBe(0);
    expect(result.output).toContain('baseline update diff');
    expect(result.output).toContain('- src/fixture.js:');
    expect(result.output).toContain('+ src/fixture.js:');
    expect(result.output).toContain('updated atomically for production');
    expect(result.productionBaselineAfter.equals(result.productionBaselineBefore)).toBe(false);
    expect(result.productionBaseline.files['src/fixture.js'].lines).toBe(FRONTEND_CODE_SIZE_LIMITS.maxFileLines + 1);
    expect(result.productionStatAfter.mode).toBe(result.productionStatBefore.mode);
    expect(result.testBaselineAfter.equals(result.testBaselineBefore)).toBe(true);
  });

  it('rejects partial update, retired freeze, and new debt without modifying either baseline', () => {
    const partial = runGuardWithFixture({
      currentLines: 1,
      guardArgs: ['--update', '--scope', 'production'],
    });
    const retiredFreeze = runGuardWithFixture({
      currentLines: 1,
      guardArgs: ['--freeze', '--scope', 'production'],
    });
    const guardRelFile = 'scripts/frontend-code-size-guard.mjs';
    const newDebt = runGuardWithFixture({
      currentLines: FRONTEND_CODE_SIZE_LIMITS.maxFileLines + 1,
      frozenProductionFiles: {
        [guardRelFile]: frozenMetricsForSource(guardRelFile, fs.readFileSync(guardScriptSourcePath, 'utf8')),
      },
      guardArgs: ['--update', '--scope', 'production'],
      scanDir: null,
    });

    expect(partial.status).toBe(2);
    expect(partial.output).toContain('canonical full scan');
    expect(retiredFreeze.status).toBe(2);
    expect(retiredFreeze.output).toContain('--freeze is retired');
    expect(newDebt.status).toBe(2);
    expect(newDebt.output).toContain('--update refused');
    for (const result of [partial, retiredFreeze, newDebt]) {
      expect(result.productionBaselineAfter.equals(result.productionBaselineBefore)).toBe(true);
      expect(result.testBaselineAfter.equals(result.testBaselineBefore)).toBe(true);
    }
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

describe('frontend code size baseline transaction', () => {
  function transactionFixture() {
    const root = fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(), 'frontend-code-size-transaction-')));
    const filePath = path.join(root, '.frontend_code_size_guard_baseline.json');
    const previous = baselineData({
      'src/fixture.js': frozenFileLengthMetrics(FRONTEND_CODE_SIZE_LIMITS.maxFileLines + 3),
    });
    const candidate = baselineData({
      'src/fixture.js': frozenFileLengthMetrics(FRONTEND_CODE_SIZE_LIMITS.maxFileLines + 1),
    });
    const bytes = baselineBytes(previous);
    fs.writeFileSync(filePath, bytes);
    return { root, filePath, previous, candidate, bytes, hash: hashBaselineBytes(bytes) };
  }

  it('refuses added or widened debt before opening the writer', () => {
    const previous = baselineData({
      'src/fixture.js': frozenFileLengthMetrics(FRONTEND_CODE_SIZE_LIMITS.maxFileLines + 1),
    });
    const widened = baselineData({
      'src/fixture.js': frozenFileLengthMetrics(FRONTEND_CODE_SIZE_LIMITS.maxFileLines + 2),
    });
    const added = baselineData({
      ...previous.files,
      'src/new-debt.js': frozenFileLengthMetrics(FRONTEND_CODE_SIZE_LIMITS.maxFileLines + 1),
    });

    expect(() => assertBaselineUpdateOnlyImproves(previous, widened)).toThrow(/widen/);
    expect(() => assertBaselineUpdateOnlyImproves(previous, added)).toThrow(/add debt entry/);
    expect(() => assertBaselineUpdateOnlyImproves(
      { ...previous, unexpected: true },
      previous,
    )).toThrow(/expected only _meta and files/);
  });

  it('does not overwrite a target changed after the initial hash check', () => {
    const fixture = transactionFixture();
    const concurrent = baselineBytes(baselineData({
      'src/fixture.js': frozenFileLengthMetrics(FRONTEND_CODE_SIZE_LIMITS.maxFileLines + 2),
    }));
    try {
      expect(() => writeBaselineTransaction({
        filePath: fixture.filePath,
        expectedHash: fixture.hash,
        previous: fixture.previous,
        candidate: fixture.candidate,
        failpoint(point) {
          if (point === 'after-temp-fsync') fs.writeFileSync(fixture.filePath, concurrent);
        },
      })).toThrow(/target changed after check/);
      expect(fs.readFileSync(fixture.filePath).equals(concurrent)).toBe(true);
      expect(fs.statSync(fixture.filePath).mode & 0o777).toBe(0o644);
      expect(fs.readdirSync(fixture.root)).toEqual(['.frontend_code_size_guard_baseline.json']);
    } finally {
      fs.rmSync(fixture.root, { recursive: true, force: true });
    }
  });

  it('keeps the committed candidate on a commit-directory-fsync failure', () => {
    const fixture = transactionFixture();
    const privateCauseSentinel = 'SENTINEL-PRIVATE-NESTED-CAUSE';
    const now = () => new Date('2026-07-10T00:00:00Z');
    const candidateBytes = baselineBytes({
      _meta: { updatedAt: '2026-07-10T00:00:00Z' },
      files: fixture.candidate.files,
    });
    try {
      let caught;
      try {
        writeBaselineTransaction({
          filePath: fixture.filePath,
          expectedHash: fixture.hash,
          previous: fixture.previous,
          candidate: fixture.candidate,
          now,
          failpoint(point) {
            if (point === 'before-commit-dir-fsync') {
              throw new Error(`injected directory fsync failure ${fixture.filePath} ${privateCauseSentinel}`);
            }
          },
        });
      } catch (error) {
        caught = error;
      }
      expect(caught?.code).toBe('BASELINE_COMMITTED_DURABILITY_UNKNOWN');
      expect(caught?.committed).toBe(true);
      expect(caught?.cause?.message).toContain('injected directory fsync failure');
      expect(caught?.cause?.message).toContain(privateCauseSentinel);
      expect(caught?.recoveryAction).toBe('inspect-final-state-and-marker-without-mutating');
      expect(caught?.message).not.toContain(fixture.root);
      expect(caught?.message).not.toContain(fixture.filePath);
      expect(caught?.message).not.toContain(privateCauseSentinel);
      expect(formatBaselineTransactionErrorForStderr(caught)).not.toContain(privateCauseSentinel);
      expect(formatBaselineTransactionErrorForStderr(caught)).not.toContain(fixture.root);
      expect(fs.readFileSync(fixture.filePath).equals(candidateBytes)).toBe(true);
      expect(fs.readdirSync(fixture.root)).toEqual(['.frontend_code_size_guard_baseline.json']);
    } finally {
      fs.rmSync(fixture.root, { recursive: true, force: true });
    }
  });

  it.each([
    ['before-claim-rename', 'claim rename'],
    ['before-install', 'candidate install'],
  ])('leaves the old bytes on an injected %s failure', (failurePoint) => {
    const fixture = transactionFixture();
    try {
      expect(() => writeBaselineTransaction({
        filePath: fixture.filePath,
        expectedHash: fixture.hash,
        previous: fixture.previous,
        candidate: fixture.candidate,
        failpoint(point) {
          if (point === failurePoint) throw new Error(`injected ${failurePoint}`);
        },
      })).toThrow(new RegExp(`injected ${failurePoint}`));
      expect(fs.readFileSync(fixture.filePath).equals(fixture.bytes)).toBe(true);
      expect(fs.readdirSync(fixture.root)).toEqual(['.frontend_code_size_guard_baseline.json']);
    } finally {
      fs.rmSync(fixture.root, { recursive: true, force: true });
    }
  });

  it.each([
    ['before-rollback-rename', 'rollback rename'],
    ['before-rollback-dir-fsync', 'rollback directory fsync'],
  ])('reports durable-unknown when %s fails', (failurePoint) => {
    const fixture = transactionFixture();
    try {
      let caught;
      try {
        writeBaselineTransaction({
          filePath: fixture.filePath,
          expectedHash: fixture.hash,
          previous: fixture.previous,
          candidate: fixture.candidate,
          failpoint(point) {
            if (point === 'before-atomic-replace') throw new Error('force rollback');
            if (point === failurePoint) throw new Error(`injected ${failurePoint}`);
          },
        });
      } catch (error) {
        caught = error;
      }
      expect(caught?.code).toBe('BASELINE_DURABILITY_UNKNOWN');
      expect(caught?.message).toContain('reconcile required');
      expect(caught?.finalState).toBeDefined();
      if (caught.finalState.exists) {
        expect(hashBaselineBytes(fs.readFileSync(fixture.filePath))).toBe(caught.finalState.hash);
      } else {
        expect(fs.existsSync(fixture.filePath)).toBe(false);
      }
    } finally {
      fs.rmSync(fixture.root, { recursive: true, force: true });
    }
  });

  it('rejects a live owner and malformed non-protocol lock, but recovers dead and PID-reused locks', () => {
    const fixture = transactionFixture();
    const lockPath = `${fixture.filePath}.lock`;
    try {
      const held = acquireBaselineLock(fixture.filePath, { installSignalHandlers: false });
      expect(() => acquireBaselineLock(fixture.filePath, { installSignalHandlers: false })).toThrow(/live cooperative process/);
      expect(JSON.parse(fs.readFileSync(lockPath, 'utf8')).nonce).toBe(held.owner.nonce);
      held.release();

      fs.writeFileSync(lockPath, 'competing writer\n');
      expect(() => acquireBaselineLock(fixture.filePath, { installSignalHandlers: false })).toThrow(/malformed or outside the cooperative protocol/);
      expect(fs.readFileSync(lockPath, 'utf8')).toBe('competing writer\n');
      expect(fs.readFileSync(fixture.filePath).equals(fixture.bytes)).toBe(true);
      fs.unlinkSync(lockPath);

      const staleOwner = {
        version: 1,
        pid: 2147483647,
        startIdentity: 'dead-process',
        nonce: '0123456789abcdef',
        createdAt: new Date().toISOString(),
      };
      fs.writeFileSync(lockPath, `${JSON.stringify(staleOwner)}\n`);
      const afterDeath = acquireBaselineLock(fixture.filePath, {
        installSignalHandlers: false,
        resolveProcessIdentity: () => null,
      });
      afterDeath.release();

      fs.writeFileSync(lockPath, `${JSON.stringify({ ...staleOwner, pid: process.pid })}\n`);
      const afterReuse = acquireBaselineLock(fixture.filePath, {
        installSignalHandlers: false,
        resolveProcessIdentity: () => 'different-start-identity',
      });
      afterReuse.release();
    } finally {
      fs.rmSync(fixture.root, { recursive: true, force: true });
    }
  });

  it.each([
    ['SIGTERM', false],
    ['SIGKILL', true],
  ])('handles %s lock ownership and stale recovery', async (signal, leavesStaleLock) => {
    const fixture = transactionFixture();
    const modulePath = path.join(appRoot, 'scripts/lib/frontend-code-size-baseline-transaction.mjs');
    const childSource = [
      `import { acquireBaselineLock } from ${JSON.stringify(modulePath)};`,
      `acquireBaselineLock(${JSON.stringify(fixture.filePath)});`,
      `process.stdout.write('ready\\n');`,
      `setInterval(() => {}, 1000);`,
    ].join('\n');
    const child = spawn(process.execPath, ['--input-type=module', '-e', childSource], {
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    try {
      await new Promise((resolve, reject) => {
        let output = '';
        child.stdout.on('data', (chunk) => {
          output += chunk;
          if (output.includes('ready\n')) resolve();
        });
        child.once('error', reject);
        child.once('exit', (code) => reject(new Error(`lock child exited before ready: ${code}`)));
      });
      child.kill(signal);
      await new Promise((resolve) => child.once('exit', resolve));
      expect(fs.existsSync(`${fixture.filePath}.lock`)).toBe(leavesStaleLock);
      const recovered = acquireBaselineLock(fixture.filePath, { installSignalHandlers: false });
      recovered.release();
      expect(fs.existsSync(`${fixture.filePath}.lock`)).toBe(false);
    } finally {
      if (child.exitCode === null && child.signalCode === null) child.kill('SIGKILL');
      fs.rmSync(fixture.root, { recursive: true, force: true });
    }
  });
});
