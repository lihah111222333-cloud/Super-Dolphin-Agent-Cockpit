import { execFileSync, spawnSync } from 'node:child_process';
import {
  copyFileSync,
  mkdtempSync,
  mkdirSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { afterEach, describe, expect, it } from 'vitest';
import {
  commandEvidenceStatus,
  controlStatus,
  inspectTargetRepository,
  performanceMetricStatus,
  probeResult,
  scoreCurrentTree,
  sourceHasCriticalTypecheckGap,
  sourceHasPromptHistoryConsoleOnly,
  structuredEvidenceStatus,
  terminalTruthEvidenceStatus,
  validateConfiguration,
} from './frontend-maintainability-score.mjs';

const scriptRoot = dirname(fileURLToPath(import.meta.url));
const frozenRepoRoot = resolve(scriptRoot, '..', '..');
const scorerPath = join(scriptRoot, 'frontend-maintainability-score.mjs');
const temporaryRepositories = [];

function documents() {
  return {
    controls: JSON.parse(readFileSync(join(scriptRoot, 'frontend-maintainability-controls.json'), 'utf8')),
    fixtures: JSON.parse(readFileSync(join(scriptRoot, 'frontend-maintainability-red-fixtures.json'), 'utf8')),
  };
}

function git(repoRoot, args) {
  return execFileSync('git', args, { cwd: repoRoot, encoding: 'utf8' }).trim();
}

function cli(args, cwd = frozenRepoRoot) {
  return spawnSync(process.execPath, [scorerPath, ...args], { cwd, encoding: 'utf8' });
}

function write(relativePath, content, repoRoot) {
  const target = join(repoRoot, relativePath);
  mkdirSync(dirname(target), { recursive: true });
  writeFileSync(target, content);
}

function createTargetRepository() {
  const repoRoot = mkdtempSync(join(tmpdir(), 'frontend-maintainability-target-'));
  temporaryRepositories.push(repoRoot);
  git(repoRoot, ['init', '-q']);
  git(repoRoot, ['config', 'user.email', 'scorer-test@example.invalid']);
  git(repoRoot, ['config', 'user.name', 'Scorer Test']);
  write('README.md', 'target repository\n', repoRoot);
  write('frontend-app/package.json', '{"name":"target","private":true}\n', repoRoot);
  write('frontend-app/package-lock.json', '{"name":"target","lockfileVersion":3}\n', repoRoot);
  git(repoRoot, ['add', '.']);
  git(repoRoot, ['commit', '-q', '-m', '测试：建立评分目标']);
  return {
    repoRoot,
    subjectSha: git(repoRoot, ['rev-parse', 'HEAD']),
    subjectTree: git(repoRoot, ['rev-parse', 'HEAD^{tree}']),
  };
}

function createFinalContractTarget() {
  const repoRoot = mkdtempSync(join(tmpdir(), 'frontend-maintainability-final-target-'));
  rmSync(repoRoot, { recursive: true, force: true });
  temporaryRepositories.push(repoRoot);
  execFileSync('git', ['clone', '-q', '--shared', frozenRepoRoot, repoRoot]);
  git(repoRoot, ['config', 'user.email', 'scorer-test@example.invalid']);
  git(repoRoot, ['config', 'user.name', 'Scorer Test']);
  for (const name of [
    'frontend-maintainability-controls.json',
    'frontend-maintainability-score.mjs',
    'frontend-maintainability-baseline.json',
  ]) {
    copyFileSync(join(scriptRoot, name), join(repoRoot, 'frontend-app', 'scripts', name));
  }
  write('frontend-app/scorer-final-subject.txt', 'strict descendant\n', repoRoot);
  git(repoRoot, ['add', '.']);
  git(repoRoot, ['commit', '-q', '-m', '测试：建立最终评分后代']);
  return {
    repoRoot,
    subjectSha: git(repoRoot, ['rev-parse', 'HEAD']),
  };
}

function createFinalCliFixture() {
  const baseRoot = mkdtempSync(join(tmpdir(), 'frontend-maintainability-final-cli-base-'));
  rmSync(baseRoot, { recursive: true, force: true });
  temporaryRepositories.push(baseRoot);
  execFileSync('git', ['clone', '-q', '--shared', frozenRepoRoot, baseRoot]);
  git(baseRoot, ['config', 'user.email', 'scorer-test@example.invalid']);
  git(baseRoot, ['config', 'user.name', 'Scorer Test']);
  for (const name of [
    'frontend-maintainability-controls.json',
    'frontend-maintainability-score.mjs',
    'frontend-maintainability-baseline.json',
  ]) {
    copyFileSync(join(scriptRoot, name), join(baseRoot, 'frontend-app', 'scripts', name));
  }
  git(baseRoot, ['add', '.']);
  git(baseRoot, ['commit', '-q', '-m', '测试：冻结最终评分器']);
  const scoreBaseSha = git(baseRoot, ['rev-parse', 'HEAD']);

  const subjectRoot = mkdtempSync(join(tmpdir(), 'frontend-maintainability-final-cli-subject-'));
  rmSync(subjectRoot, { recursive: true, force: true });
  execFileSync('git', ['worktree', 'add', '-q', '--detach', subjectRoot, scoreBaseSha], { cwd: baseRoot });
  write('frontend-app/final-subject.txt', 'strict descendant\n', subjectRoot);
  write('go.mod', 'invalid final fixture\n', subjectRoot);
  const packageDocument = JSON.parse(readFileSync(join(subjectRoot, 'frontend-app', 'package.json'), 'utf8'));
  packageDocument.scripts.lint = 'false';
  packageDocument.scripts.test = 'false';
  packageDocument.scripts.build = 'false';
  write('frontend-app/package.json', `${JSON.stringify(packageDocument, null, 2)}\n`, subjectRoot);
  write('Makefile', 'frontend-embed-verify:\n\t@false\n', subjectRoot);
  git(subjectRoot, ['add', '.']);
  git(subjectRoot, ['commit', '-q', '-m', '测试：建立最终评分目标']);
  return { baseRoot, scoreBaseSha, subjectRoot, subjectSha: git(subjectRoot, ['rev-parse', 'HEAD']) };
}

function structuredEvidence(context, control, check, overrides = {}) {
  return {
    schemaVersion: 1,
    subjectSha: context.subjectSha,
    subjectTree: context.subjectTree,
    controlId: control.id,
    caseIds: [...check.caseIds],
    testCount: check.testCount,
    tests: check.caseIds.map((caseId, index) => ({ caseId, name: `case ${index + 1}`, status: 'passed' })),
    generatedAt: new Date().toISOString(),
    environment: { node: process.version, platform: process.platform, arch: process.arch },
    ...overrides,
  };
}


afterEach(() => {
  while (temporaryRepositories.length > 0) {
    rmSync(temporaryRepositories.pop(), { recursive: true, force: true });
  }
}, 30_000);

describe('frontend maintainability scorer configuration', () => {
  it('rejects hand-authored PASS, weak or mutable probes, threshold drift, fixtures drift, and zero-test evidence', () => {
    expect(validateConfiguration()).toBe(true);

    const handAuthored = documents();
    handAuthored.controls.controls[0].status = 'PASS';
    expect(() => validateConfiguration(handAuthored.controls, handAuthored.fixtures))
      .toThrow('hand-authored result is forbidden');

    const weakCommand = documents();
    weakCommand.controls.controls.find(({ id }) => id === 'T04-local-gates').allOf[0].argv = ['echo', 'PASS'];
    expect(() => validateConfiguration(weakCommand.controls, weakCommand.fixtures)).toThrow('weak runner command');

    const mutableProbe = documents();
    mutableProbe.controls.controls[1].allOf[0].argv.push('--extra');
    expect(() => validateConfiguration(mutableProbe.controls, mutableProbe.fixtures))
      .toThrow('invalid frozen artifact probe');

    const lowerThreshold = documents();
    lowerThreshold.controls.thresholds.dimensions.P = 0;
    expect(() => validateConfiguration(lowerThreshold.controls, lowerThreshold.fixtures))
      .toThrow('score thresholds differ from the frozen plan');

    const zeroTest = documents();
    zeroTest.controls.controls[0].allOf[0].testCount = 0;
    expect(() => validateConfiguration(zeroTest.controls, zeroTest.fixtures)).toThrow('zero-test runner evidence');

    const emptyAllOf = documents();
    emptyAllOf.controls.controls.find(({ id }) => id === 'A04-action-registry').allOf = [];
    expect(() => validateConfiguration(emptyAllOf.controls, emptyAllOf.fixtures)).toThrow('invalid control shape');

    const mutableFutureRunner = documents();
    mutableFutureRunner.controls.controls.find(({ id }) => id === 'A04-action-registry')
      .allOf[0].argv[1] = 'scripts/mutable-runner.mjs';
    expect(() => validateConfiguration(mutableFutureRunner.controls, mutableFutureRunner.fixtures))
      .toThrow('structured evidence runner differs from frozen contract');

    const optionalDoD = documents();
    optionalDoD.controls.controls.find(({ id }) => id === 'E06-failure-matrix').required = false;
    expect(() => validateConfiguration(optionalDoD.controls, optionalDoD.fixtures))
      .toThrow('DoD control must be required');

    const relaxedPerformanceFormula = documents();
    relaxedPerformanceFormula.controls.controls.find(({ id }) => id === 'P02-history-budget')
      .allOf[0].metrics.history200MedianMs.baselineMultiplier = 2;
    expect(() => validateConfiguration(relaxedPerformanceFormula.controls, relaxedPerformanceFormula.fixtures))
      .toThrow('performance formula differs from frozen contract');

    const missingFixture = documents();
    missingFixture.controls.controls[0].allOf[0].caseIds = ['does-not-exist'];
    expect(() => validateConfiguration(missingFixture.controls, missingFixture.fixtures)).toThrow('missing fixture case');

    const staleFixture = documents();
    staleFixture.fixtures.fixtures.push({ id: 'stale-red', area: 'test', expected: 'reject' });
    expect(() => validateConfiguration(staleFixture.controls, staleFixture.fixtures))
      .toThrow('frozen RED fixture ids exact set mismatch');
  });

  it('predeclares a non-empty executable contract for every frozen control', () => {
    const { controls } = documents();
    expect(controls.controls).toHaveLength(25);
    expect(controls.controls.every(({ required }) => typeof required === 'boolean')).toBe(true);
    expect(controls.controls.every(({ allOf }) => allOf.length > 0)).toBe(true);
    expect(['E06-failure-matrix', 'C05-provider-rpc-parity', 'T05-build-embed-smoke'].every((id) => (
      controls.controls.find((control) => control.id === id).required
    ))).toBe(true);
    expect(JSON.stringify(controls)).not.toContain('notImplemented');
    expect(JSON.stringify(controls)).not.toContain('frontend-maintainability-evidence.mjs');
  });

  it('enforces exact CLI forms', () => {
    expect(cli(['--validate'])).toMatchObject({ status: 0 });
    expect(cli(['--validate', 'extra']).status).not.toBe(0);
    expect(cli(['--probe']).status).not.toBe(0);
    expect(cli(['--score', '--run', '--run']).status).not.toBe(0);
    expect(cli(['--final', '--repo', frozenRepoRoot]).status).not.toBe(0);
  });
});

describe('frozen scorer target binding', () => {
  it('scores another Git target without loading a scorer from that target', () => {
    const target = createTargetRepository();
    const result = scoreCurrentTree({ repoRoot: target.repoRoot, subjectSha: target.subjectSha });

    expect(result.subjectSha).toBe(target.subjectSha);
    expect(result.subjectTree).toBe(target.subjectTree);
    expect(result.controls).toHaveLength(25);
    expect(result.controls.every(({ status }) => status !== 'PASS')).toBe(true);
    expect(result.displayScore).toBe(0);
  });

  it('rejects a mismatched subject and a dirty or untracked target', () => {
    const target = createTargetRepository();
    expect(() => inspectTargetRepository({
      repoRoot: target.repoRoot,
      subjectSha: 'f'.repeat(40),
      requireClean: true,
    })).toThrow('subject must equal target HEAD');
    expect(cli(['--score', '--repo', target.repoRoot, '--subject', 'f'.repeat(40)]).status).not.toBe(0);

    write('untracked.txt', 'dirty\n', target.repoRoot);
    expect(() => inspectTargetRepository({
      repoRoot: target.repoRoot,
      subjectSha: target.subjectSha,
      requireClean: true,
    })).toThrow('dirty or untracked target worktrees');
    expect(cli(['--score', '--repo', target.repoRoot, '--subject', target.subjectSha]).status).not.toBe(0);
  });

  it('accepts only a clean strict descendant with byte-identical frozen governance', () => {
    const target = createFinalContractTarget();
    const context = inspectTargetRepository({
      repoRoot: target.repoRoot,
      subjectSha: target.subjectSha,
      requireClean: true,
      requireFinalContract: true,
    });

    expect(context.subjectSha).toBe(target.subjectSha);
    expect(context.scoreBaseSha).toBe(git(frozenRepoRoot, ['rev-parse', 'HEAD']));

    write('frontend-app/scripts/frontend-maintainability-score.mjs', '// drift\n', target.repoRoot);
    git(target.repoRoot, ['add', '.']);
    git(target.repoRoot, ['commit', '-q', '-m', '测试：制造治理漂移']);
    expect(() => inspectTargetRepository({
      repoRoot: target.repoRoot,
      subjectSha: git(target.repoRoot, ['rev-parse', 'HEAD']),
      requireClean: true,
      requireFinalContract: true,
    })).toThrow('frozen governance drift');
  }, 30_000);

  it('runs the exact final CLI from a clean SCORE_BASE against a detached subject', () => {
    const fixture = createFinalCliFixture();
    try {
      const result = spawnSync(process.execPath, [
        join(fixture.baseRoot, 'frontend-app', 'scripts', 'frontend-maintainability-score.mjs'),
        '--final',
        '--repo', fixture.subjectRoot,
        '--subject', fixture.subjectSha,
      ], { cwd: join(fixture.baseRoot, 'frontend-app'), encoding: 'utf8' });

      expect(result.status).toBe(1);
      expect(result.stdout).toContain(`SCORE_BASE\t${fixture.scoreBaseSha}`);
      expect(result.stdout).toContain(`SCORE\t0.0\t${fixture.subjectSha}`);
      expect(result.stdout).toContain('REPORT\t');
      expect(result.stderr).toContain('FINAL_GATE\tFAIL');
    }
    finally {
      execFileSync('git', ['worktree', 'remove', '--force', fixture.subjectRoot], { cwd: fixture.baseRoot });
    }
  }, 60_000);
});

describe('executable evidence registry', () => {
  it('derives known artifact failures from the subject and never turns their absence into PASS', () => {
    expect(sourceHasPromptHistoryConsoleOnly()).toBe(true);
    expect(sourceHasCriticalTypecheckGap()).toBe(true);
    expect(probeResult('promptHistoryVisibleError')).toBe('FAIL');
    expect(probeResult('criticalTypecheck')).toBe('FAIL');
  });

  it('keeps an unregistered redMatrix and a missing exact actionRegistry runner NOT_VERIFIED', () => {
    expect(probeResult('redMatrix')).toBe('NOT_VERIFIED');
    expect(probeResult('actionRegistry')).toBe('NOT_VERIFIED');
  });

  it('rejects stale, mismatched, zero-test, and wrong-case structured evidence', () => {
    const { controls } = documents();
    const control = controls.controls.find(({ id }) => id === 'A04-action-registry');
    const check = control.allOf[0];
    const context = inspectTargetRepository();
    const now = Date.now();
    const options = { context, control, check, startedAt: now - 100, finishedAt: now + 100 };
    const valid = structuredEvidence(context, control, check);

    expect(structuredEvidenceStatus(valid, options)).toBe('PASS');
    expect(structuredEvidenceStatus({ ...valid, subjectSha: 'f'.repeat(40) }, options)).toBe('FAIL');
    expect(structuredEvidenceStatus({ ...valid, generatedAt: '2000-01-01T00:00:00.000Z' }, options)).toBe('FAIL');
    expect(structuredEvidenceStatus({ ...valid, testCount: 0, tests: [] }, options)).toBe('FAIL');
    expect(structuredEvidenceStatus({
      ...valid,
      caseIds: ['prompt-history-console-only'],
      tests: [{ caseId: 'prompt-history-console-only', name: 'wrong case', status: 'passed' }],
    }, options)).toBe('FAIL');
  });

  it('fails a missing executable command instead of treating it as evidence', () => {
    expect(commandEvidenceStatus({
      repoRoot: frozenRepoRoot,
      argv: ['frontend-maintainability-command-does-not-exist'],
    })).toBe('FAIL');
  });
});

describe('scoring semantics', () => {
  it('requires fresh named terminal behavior evidence', () => {
    const expected = {
      fingerprint: 'current-tree-fingerprint',
      testNames: ['terminal failed behavior', 'terminal stale behavior'],
    };
    const passing = {
      fingerprint: expected.fingerprint,
      testResults: expected.testNames.map((name) => ({ name, status: 'passed' })),
    };

    expect(terminalTruthEvidenceStatus(passing, expected)).toBe('PASS');
    expect(terminalTruthEvidenceStatus({ ...passing, testResults: [] }, expected)).toBe('FAIL');
    expect(terminalTruthEvidenceStatus({ ...passing, testResults: passing.testResults.slice(0, 1) }, expected)).toBe('FAIL');
    expect(terminalTruthEvidenceStatus({
      ...passing,
      testResults: [{ name: expected.testNames[0], status: 'failed' }, passing.testResults[1]],
    }, expected)).toBe('FAIL');
    expect(terminalTruthEvidenceStatus({ ...passing, fingerprint: 'stale-tree-fingerprint' }, expected)).toBe('FAIL');
  });

  it('uses three-state allOf semantics', () => {
    expect(controlStatus([])).toBe('NOT_VERIFIED');
    expect(controlStatus([{ status: 'PASS' }, { status: 'PASS' }])).toBe('PASS');
    expect(controlStatus([{ status: 'PASS' }, { status: 'NOT_VERIFIED' }])).toBe('NOT_VERIFIED');
    expect(controlStatus([{ status: 'PASS' }, { status: 'FAIL' }])).toBe('FAIL');
  });

  it('executes frozen performance formulas without inventing missing baseline values', () => {
    const { controls } = documents();
    const p01 = controls.controls.find(({ id }) => id === 'P01-render-isolation').allOf[0];
    const p02 = controls.controls.find(({ id }) => id === 'P02-history-budget').allOf[0];
    const metrics = [
      { name: 'mainPageCommits', value: 1, unit: 'commits', sampleCount: 20 },
      { name: 'unrelatedSubtreeCommits', value: 1, unit: 'commits', sampleCount: 20 },
    ];
    const frozenReferences = {
      metrics: {
        'P01-render-isolation': {
          references: {
            mainPageCommits: { value: 1, unit: 'commits' },
            unrelatedSubtreeCommits: { value: 1, unit: 'commits' },
          },
        },
      },
    };

    expect(performanceMetricStatus(metrics, p01)).toBe('NOT_VERIFIED');
    expect(performanceMetricStatus(metrics, p01, frozenReferences)).toBe('PASS');
    expect(performanceMetricStatus([
      { ...metrics[0], value: 2 },
      metrics[1],
    ], p01, frozenReferences)).toBe('FAIL');
    expect(performanceMetricStatus([
      { ...metrics[0], sampleCount: 19 },
      metrics[1],
    ], p01, frozenReferences)).toBe('FAIL');

    const historyReferences = {
      metrics: {
        'P02-history-budget': {
          references: Object.fromEntries(Object.keys(p02.metrics).map((name) => [name, { value: 100, unit: 'ms' }])),
        },
      },
    };
    const historyMetrics = Object.keys(p02.metrics).map((name) => ({
      name, value: 114.9, unit: 'ms', sampleCount: 5,
    }));
    expect(performanceMetricStatus(historyMetrics, p02, historyReferences)).toBe('PASS');
    expect(performanceMetricStatus([
      { ...historyMetrics[0], value: 115.1 },
      ...historyMetrics.slice(1),
    ], p02, historyReferences)).toBe('FAIL');
  });

  it('does not turn zero evidence or confirmed artifact gaps into score', () => {
    const result = scoreCurrentTree();
    expect(result.controls.find(({ id }) => id === 'E06-failure-matrix').status).toBe('NOT_VERIFIED');
    expect(result.controls.find(({ id }) => id === 'A04-action-registry').status).toBe('NOT_VERIFIED');
    expect(result.controls.find(({ id }) => id === 'E02-visible-action-error').status).toBe('FAIL');
    expect(result.controls.find(({ id }) => id === 'C04-critical-typecheck').status).toBe('FAIL');
    expect(result.controls.find(({ id }) => id === 'T05-build-embed-smoke').status).toBe('NOT_VERIFIED');
    expect(result.displayScore).toBe(22.8);
  }, 150_000);
});
