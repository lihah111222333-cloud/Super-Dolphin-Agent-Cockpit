import {
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  readdirSync,
  rmSync,
  writeFileSync,
} from 'node:fs';
import { createHash } from 'node:crypto';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { cwd } from 'node:process';
import { describe, expect, it, vi } from 'vitest';
import {
  FREEZE_RUN_COUNT,
  buildFrozenPerformanceBaseline,
  collectCandidateResourceBudget,
  collectDetachedP01Evidence,
  freezePerformanceBaseline,
  parseArguments,
  validateCaseRegistry,
  validateFreezeOutputPath,
  validateFreezePreconditions,
  verifyPerformanceEvidence,
  writeFrozenBaselineAtomically,
} from './performance-budget-runner.mjs';
import { HEAP_MEASUREMENT_CLOCK } from './resource-budget.mjs';

const SUBJECT_SHA = 'a'.repeat(40);
const SUBJECT_TREE = '1'.repeat(40);
const RUNNER_SHA = 'b'.repeat(40);
const RUNNER_TREE = '2'.repeat(40);
const PLAN_SNAPSHOT_SHA = 'c'.repeat(40);
const RUNNER_CONTENT_HASH = 'd'.repeat(64);

function resourceMetric(subjectSha = SUBJECT_SHA) {
  const files = [
    { path: 'assets/Chat.js', bytes: 50 },
    { path: 'assets/a.js', bytes: 50 },
  ];
  const distManifest = files.map(({ path, bytes }, index) => ({
    path,
    bytes,
    sha256: String(index + 1).repeat(64),
  }));
  const distManifestHash = createHash('sha256').update(distManifest.map(({ path, bytes, sha256 }) => (
    `${path}\0${bytes}\0${sha256}\n`
  )).join('')).digest('hex');
  return {
    metricId: 'P04-resource-budget',
    subjectSha,
    fileCount: files.length,
    totalBundleBytes: 100,
    maxChunkBytes: 50,
    heapMeasurementClock: HEAP_MEASUREMENT_CLOCK,
    heapSampleCount: 5,
    heapWarmupCount: 1,
    heapUsedSamplesBytes: [100, 100, 100, 100, 100],
    heapUsedMedianBytes: 100,
    heapEnvironment: {
      node: 'v25.6.1',
      v8: '14.1.0',
      platform: 'darwin',
      arch: 'arm64',
    },
    files,
    baseBuild: {
      baseSha: subjectSha,
      baseTree: SUBJECT_TREE,
      installArgv: ['npm', 'ci'],
      buildArgv: ['npm', 'run', 'build'],
      distManifest,
      distManifestHash,
    },
    candidateBuild: {
      subjectSha,
      subjectTree: SUBJECT_TREE,
      installArgv: ['npm', 'ci'],
      buildArgv: ['npm', 'run', 'build'],
      distManifest,
      distManifestHash,
    },
  };
}

function evidence(subjectSha = SUBJECT_SHA) {
  return {
    schemaVersion: 1,
    subjectSha,
    subjectTree: SUBJECT_TREE,
    generatedAt: '2026-07-18T00:00:00.000Z',
    environment: {
      os: { platform: 'darwin', release: 'test-release', arch: 'arm64' },
      cpu: { model: 'test-cpu', logicalCores: 8 },
      totalMemoryBytes: 1024,
      loadAverage: [1, 2, 3],
      node: 'v25.6.1',
      npm: '11.0.0',
      go: 'go version go1.25.0 darwin/arm64',
    },
    provenance: {
      runnerContentHash: 'runner-hash',
      runnerFiles: [{ path: 'frontend-app/scripts/performance-budget-runner.mjs', sha256: 'e'.repeat(64) }],
    },
    metrics: {
      'P01-render-isolation': {
        metricId: 'P01-render-isolation',
        subjectSha,
        updateCount: 20,
        warmupUpdates: 2,
        mainPageUpdateCommits: 0,
        unrelatedSubtreeUpdateCommits: 0,
        mutationUpdateCommits: 20,
        mutationDetected: true,
        subjectProduct: {
          probePath: 'scripts/render-isolation-probe.test.jsx',
          probeSha256: '9'.repeat(64),
          subjectSha,
          subjectTree: SUBJECT_TREE,
          worktreeClean: true,
        },
      },
      'P04-resource-budget': resourceMetric(subjectSha),
    },
  };
}

function freezeEvidence({
  generatedAt = '2026-07-18T00:00:00.000Z',
  loadAverage = [1, 2, 3],
} = {}) {
  const current = evidence(SUBJECT_SHA);
  current.generatedAt = generatedAt;
  current.environment.loadAverage = loadAverage;
  current.metrics['P01-render-isolation'].mainPageUpdateCommits = 40;
  delete current.metrics['P04-resource-budget'].candidateBuild;
  current.provenance = {
    runnerId: 'frontend-performance-budget',
    runnerSha: RUNNER_SHA,
    runnerTree: RUNNER_TREE,
    runnerContentHash: RUNNER_CONTENT_HASH,
    runnerFiles: [
      { path: 'frontend-app/scripts/performance-budget-runner.mjs', sha256: 'e'.repeat(64) },
      { path: 'frontend-app/scripts/managed-command.mjs', sha256: 'f'.repeat(64) },
    ],
    worktreeClean: true,
    worktreeStatus: [],
    baselineAudit: {
      baseSha: SUBJECT_SHA,
      baseTree: SUBJECT_TREE,
      changedPaths: ['frontend-app/scripts/performance-budget-runner.mjs'],
    },
  };
  return current;
}

function freezeRuns() {
  return [
    freezeEvidence({ generatedAt: '2026-07-18T00:00:00.000Z', loadAverage: [1, 2, 3] }),
    freezeEvidence({ generatedAt: '2026-07-18T00:01:00.000Z', loadAverage: [2, 3, 4] }),
    freezeEvidence({ generatedAt: '2026-07-18T00:02:00.000Z', loadAverage: [3, 4, 5] }),
  ];
}

function measurementBindings(run) {
  const { loadAverage: _loadAverage, ...environment } = run.environment;
  return {
    subjectSha: run.subjectSha,
    subjectTree: run.subjectTree,
    environment,
    runnerSha: run.provenance.runnerSha,
    runnerTree: run.provenance.runnerTree,
    runnerContentHash: run.provenance.runnerContentHash,
    changedPaths: run.provenance.baselineAudit.changedPaths,
  };
}

function buildFreezeArtifact(runs, expectedProvenance = {
  runnerSha: RUNNER_SHA,
  runnerTree: RUNNER_TREE,
  subjectTree: SUBJECT_TREE,
}) {
  return buildFrozenPerformanceBaseline({
    runs,
    subjectSha: SUBJECT_SHA,
    planSnapshotSha: PLAN_SNAPSHOT_SHA,
    expectedProvenance,
  });
}

function baseline() {
  const current = evidence();
  return {
    provenance: { runnerContentHash: 'runner-hash' },
    metrics: {
      'P01-render-isolation': {
        status: 'PASS',
        subjectSha: SUBJECT_SHA,
        absoluteUpdateLimit: 1,
        updateCount: 20,
        warmupUpdates: 2,
        mainPageUpdateCommits: 0,
        unrelatedSubtreeUpdateCommits: 0,
      },
      'P04-resource-budget': {
        status: 'PASS',
        subjectSha: SUBJECT_SHA,
        maxRegressionRatio: 1.05,
        totalBundleBytes: current.metrics['P04-resource-budget'].totalBundleBytes,
        maxChunkBytes: current.metrics['P04-resource-budget'].maxChunkBytes,
        heapMeasurementClock: HEAP_MEASUREMENT_CLOCK,
        heapSampleCount: 5,
        heapWarmupCount: 1,
        heapUsedSamplesBytes: [100, 100, 100, 100, 100],
        heapUsedMedianBytes: 100,
        heapEnvironment: current.metrics['P04-resource-budget'].heapEnvironment,
      },
    },
  };
}

function registry() {
  return JSON.parse(readFileSync(join(cwd(), 'scripts/frontend-performance-cases.json'), 'utf8'));
}

describe('candidate detached resource build', () => {
  it('builds a clean SUBJECT with bounded managed commands and binds the measured artifact provenance', async () => {
    const commands = [];
    let measured = 0;
    const execute = (command, args) => {
      commands.push([command, ...args]);
      if (command === 'git' && args[0] === 'clone') mkdirSync(join(args.at(-1), 'frontend-app'), { recursive: true });
      if (command === 'git' && args.join(' ') === 'rev-parse HEAD') return SUBJECT_SHA;
      if (command === 'git' && args.join(' ') === 'rev-parse HEAD^{tree}') return SUBJECT_TREE;
      return '';
    };
    const runCommand = async (command, args, options) => {
      commands.push([command, ...args]);
      expect(options).toEqual(expect.objectContaining({
        killGraceMs: expect.any(Number),
        maxBuffer: expect.any(Number),
        timeoutMs: expect.any(Number),
      }));
      if (command === 'npm' && args[0] === 'ci') expect(existsSync(join(options.cwd, 'dist'))).toBe(false);
      if (command === 'npm' && args.join(' ') === 'run build') {
        mkdirSync(join(options.cwd, 'dist', 'assets'), { recursive: true });
        writeFileSync(join(options.cwd, 'dist', 'index.html'), '<main>candidate</main>');
        writeFileSync(join(options.cwd, 'dist', 'assets', 'app.js'), 'globalThis.candidate = true;');
      }
      return { error: undefined, outputTruncated: false, status: 0, stderr: '', stdout: '', timedOut: false };
    };
    const metric = await collectCandidateResourceBudget({
      subjectSha: SUBJECT_SHA,
      subjectTree: SUBJECT_TREE,
      repositoryRoot: '/repository',
      execute,
      measureResources: ({ distDir, subjectSha }) => {
        measured += 1;
        const files = [
          { path: 'assets/app.js', bytes: readFileSync(join(distDir, 'assets/app.js')).byteLength },
          { path: 'index.html', bytes: readFileSync(join(distDir, 'index.html')).byteLength },
        ];
        return {
          metricId: 'P04-resource-budget', subjectSha, fileCount: files.length,
          totalBundleBytes: files.reduce((total, file) => total + file.bytes, 0),
          maxChunkBytes: Math.max(...files.map(({ bytes }) => bytes)), files,
        };
      },
      runCommand,
    });
    expect(measured).toBe(1);
    expect(commands.map((command) => command.slice(0, 3))).toEqual([
      ['git', 'clone', '--local'],
      ['git', 'checkout', '--detach'],
      ['git', 'rev-parse', 'HEAD'],
      ['git', 'rev-parse', 'HEAD^{tree}'],
      ['git', 'status', '--porcelain'],
      ['npm', 'ci'],
      ['npm', 'run', 'build'],
    ]);
    expect(metric.candidateBuild).toEqual(expect.objectContaining({
      subjectSha: SUBJECT_SHA,
      subjectTree: SUBJECT_TREE,
      installArgv: ['npm', 'ci'],
      buildArgv: ['npm', 'run', 'build'],
      distManifest: expect.arrayContaining([
        expect.objectContaining({ path: 'index.html', sha256: expect.stringMatching(/^[0-9a-f]{64}$/) }),
      ]),
      distManifestHash: expect.stringMatching(/^[0-9a-f]{64}$/),
    }));
  });
});

describe('P01 detached subject closure', () => {
  it('runs the render probe against the detached requested subject and records its provenance', async () => {
    const commands = [];
    let temporaryRoot = '';
    const execute = (command, args) => {
      commands.push([command, ...args]);
      if (command === 'git' && args[0] === 'clone') {
        temporaryRoot = args.at(-1);
        mkdirSync(join(temporaryRoot, 'frontend-app', 'scripts'), { recursive: true });
        writeFileSync(join(temporaryRoot, 'frontend-app', 'scripts', 'render-isolation-probe.test.jsx'), 'subject probe');
        return '';
      }
      if (command === 'git' && args[0] === 'checkout') return '';
      if (command === 'git' && args.join(' ') === 'rev-parse HEAD') return SUBJECT_SHA;
      if (command === 'git' && args.join(' ') === 'rev-parse HEAD^{tree}') return SUBJECT_TREE;
      if (command === 'git' && args.join(' ') === 'status --porcelain --untracked-files=all') return '';
      throw new Error(`unexpected command: ${command} ${args.join(' ')}`);
    };
    const runCommand = vi.fn(async (command, args, options) => {
      commands.push([command, ...args]);
      expect([command, ...args]).toEqual(['npm', 'ci']);
      expect(options.cwd).toBe(join(temporaryRoot, 'frontend-app'));
      return { error: undefined, outputTruncated: false, status: 0, stderr: '', stdout: '', timedOut: false };
    });
    const collectRender = vi.fn(({ frontendRoot }) => {
      expect(frontendRoot).toBe(join(temporaryRoot, 'frontend-app'));
      return { metricId: 'P01-render-isolation', updateCount: 20 };
    });
    const measured = await collectDetachedP01Evidence({
      subjectSha: SUBJECT_SHA,
      subjectTree: SUBJECT_TREE,
      repositoryRoot: '/repository',
      execute,
      collectRender,
      runCommand,
    });
    expect(measured).toEqual(expect.objectContaining({
      metricId: 'P01-render-isolation',
      subjectSha: SUBJECT_SHA,
      subjectProduct: expect.objectContaining({ subjectSha: SUBJECT_SHA, subjectTree: SUBJECT_TREE }),
      updateCount: 20,
    }));
    expect(collectRender).toHaveBeenCalledOnce();
    expect(commands.map((command) => command.slice(0, 3))).toEqual([
      ['git', 'clone', '--local'],
      ['git', 'checkout', '--detach'],
      ['git', 'rev-parse', 'HEAD'],
      ['git', 'rev-parse', 'HEAD^{tree}'],
      ['git', 'status', '--porcelain'],
      ['npm', 'ci'],
      ['git', 'status', '--porcelain'],
      ['git', 'status', '--porcelain'],
    ]);
    expect(existsSync(temporaryRoot)).toBe(false);
  });
});

describe('performance budget runner registry', () => {
  it('refuses a missing or changed frozen runner content hash', () => {
    const missing = baseline();
    delete missing.provenance;
    expect(verifyPerformanceEvidence(evidence(), missing).status).toBe('NOT_VERIFIED');
    const changed = baseline();
    changed.provenance.runnerContentHash = 'different-runner';
    expect(verifyPerformanceEvidence(evidence(), changed).status).toBe('NOT_VERIFIED');
  });

  it('derives the six exact current-tree cases and rejects invalid registrations', () => {
    const currentEvidence = evidence();
    const verdict = verifyPerformanceEvidence(currentEvidence, baseline());
    expect(verdict).toEqual(expect.objectContaining({ status: 'PASS', testCount: 6 }));
    expect(verdict.verdicts).toHaveLength(2);
    expect(verdict.verdicts.every(({ status }) => status === 'PASS')).toBe(true);
    expect(verdict.caseIds).toEqual(registry().cases.map(({ caseId }) => caseId));

    const zero = { ...registry(), testCount: 0, cases: [] };
    expect(() => validateCaseRegistry(currentEvidence, verdict.verdicts, zero)).toThrow(/zero tests/);
    const missing = registry();
    missing.cases.pop();
    missing.testCount -= 1;
    expect(() => validateCaseRegistry(currentEvidence, verdict.verdicts, missing)).toThrow(/exact set mismatch/);
    const stale = registry();
    stale.cases.push({
      caseId: 'stale-performance-case',
      metricId: 'P04-resource-budget',
      evidenceKey: 'stale',
    });
    stale.testCount += 1;
    expect(() => validateCaseRegistry(currentEvidence, verdict.verdicts, stale)).toThrow(/exact set mismatch/);
    const duplicate = registry();
    duplicate.cases[1] = { ...duplicate.cases[0] };
    expect(() => validateCaseRegistry(currentEvidence, verdict.verdicts, duplicate)).toThrow(/duplicate caseIds/);
  });

  it('fails P04 when the V8 heap or bundle measurement exceeds the frozen threshold', () => {
    const heapRegression = evidence();
    heapRegression.metrics['P04-resource-budget'].heapUsedSamplesBytes = [106, 106, 106, 106, 106];
    heapRegression.metrics['P04-resource-budget'].heapUsedMedianBytes = 106;
    expect(verifyPerformanceEvidence(heapRegression, baseline()).status).toBe('FAIL');

    const bundleRegression = evidence();
    bundleRegression.metrics['P04-resource-budget'].totalBundleBytes = 106;
    const verdict = verifyPerformanceEvidence(bundleRegression, baseline());
    expect(verdict.status).toBe('FAIL');
    expect(verdict.caseResults.filter(({ metricId }) => metricId === 'P04-resource-budget')).toEqual([
      expect.objectContaining({ caseId: 'bundle-total-bytes', status: 'FAIL' }),
      expect.objectContaining({ caseId: 'bundle-max-chunk-bytes', status: 'PASS' }),
      expect.objectContaining({ caseId: 'heap-used-median-bytes', status: 'PASS' }),
    ]);
  });
});

describe('performance baseline freeze', () => {
  function validGit(args) {
    const command = args.join(' ');
    if (command === 'rev-parse HEAD') return RUNNER_SHA;
    if (command === 'rev-parse HEAD^{tree}') return RUNNER_TREE;
    if (command === `rev-parse ${SUBJECT_SHA}^{tree}`) return SUBJECT_TREE;
    if (command === 'status --porcelain --untracked-files=all') return '';
    if (command.startsWith('cat-file -e ')) return '';
    if (command === `merge-base --is-ancestor ${SUBJECT_SHA} ${RUNNER_SHA}`) return '';
    throw new Error(`unexpected git command: ${command}`);
  }

  it('requires explicit complete freeze arguments and rejects caller-controlled run counts', () => {
    const outputPath = join(tmpdir(), 'frontend-baseline-freeze-test.json');
    expect(() => parseArguments(['--freeze'])).toThrow(/explicit --subject/);
    expect(() => parseArguments(['--freeze', '--subject', SUBJECT_SHA])).toThrow(/plan snapshot/);
    expect(() => parseArguments([
      '--freeze', '--subject', SUBJECT_SHA, '--plan-snapshot', PLAN_SNAPSHOT_SHA,
    ])).toThrow(/output path/);
    expect(() => parseArguments([
      '--freeze', '--subject', SUBJECT_SHA, '--plan-snapshot', PLAN_SNAPSHOT_SHA,
      '--output', outputPath, '--runs', '1',
    ])).toThrow(/unsupported performance budget argument/);
    expect(parseArguments([
      '--freeze', '--subject', SUBJECT_SHA, '--plan-snapshot', PLAN_SNAPSHOT_SHA,
      '--output', outputPath,
    ])).toEqual(expect.objectContaining({
      mode: 'freeze', subjectSha: SUBJECT_SHA, planSnapshotSha: PLAN_SNAPSHOT_SHA, outputPath: resolve(outputPath),
    }));
  });

  it('restricts freeze output to the baseline artifact or system temporary JSON paths', () => {
    expect(validateFreezeOutputPath(join(tmpdir(), 'safe-baseline.json'))).toBe(resolve(tmpdir(), 'safe-baseline.json'));
    expect(() => validateFreezeOutputPath(join(cwd(), 'unsafe-baseline.json'))).toThrow(/baseline artifact or a JSON path/);
    expect(() => validateFreezeOutputPath(join(tmpdir(), 'not-json.txt'))).toThrow(/baseline artifact or a JSON path/);
  });

  it('fails preflight for dirty worktrees, self subjects, non-ancestors, and missing plan commits', () => {
    const outputPath = join(tmpdir(), 'frontend-baseline-preflight.json');
    const validate = (overrides = {}) => validateFreezePreconditions({
      subjectSha: SUBJECT_SHA, planSnapshotSha: PLAN_SNAPSHOT_SHA, outputPath, git: validGit, ...overrides,
    });
    expect(validate()).toEqual({
      outputPath: resolve(outputPath), planSnapshotSha: PLAN_SNAPSHOT_SHA, runnerSha: RUNNER_SHA,
      runnerTree: RUNNER_TREE, subjectSha: SUBJECT_SHA, subjectTree: SUBJECT_TREE,
    });
    expect(() => validate({ git: (args) => (args[0] === 'status' ? ' M dirty-file' : validGit(args)) }))
      .toThrow(/clean committed runner worktree/);
    expect(() => validate({ subjectSha: RUNNER_SHA })).toThrow(/must differ/);
    expect(() => validate({ git: (args) => {
      if (args[0] === 'merge-base') throw new Error('not ancestor');
      return validGit(args);
    } })).toThrow(/must be an ancestor/);
    expect(() => validate({ git: (args) => {
      if (args.join(' ') === `cat-file -e ${PLAN_SNAPSHOT_SHA}^{commit}`) throw new Error('missing');
      return validGit(args);
    } })).toThrow(/plan snapshot commit does not exist/);
  });

  it('builds the exact frozen artifact without applying the P01 candidate limit', () => {
    const runs = freezeRuns();
    const baselineArtifact = buildFreezeArtifact(runs);
    expect(baselineArtifact).toEqual({
      schemaVersion: 1,
      baseSha: SUBJECT_SHA,
      subjectSha: SUBJECT_SHA,
      subjectTree: SUBJECT_TREE,
      planSnapshotSha: PLAN_SNAPSHOT_SHA,
      generatedAt: runs[0].generatedAt,
      environment: runs[0].environment,
      provenance: runs[0].provenance,
      measurementAudit: {
        runCount: 3,
        designatedRun: 1,
        reproducibilityRuns: [
          {
            run: 2,
            generatedAt: runs[1].generatedAt,
            runnerContentHash: RUNNER_CONTENT_HASH,
            bindings: measurementBindings(runs[1]),
            metrics: runs[1].metrics,
          },
          {
            run: 3,
            generatedAt: runs[2].generatedAt,
            runnerContentHash: RUNNER_CONTENT_HASH,
            bindings: measurementBindings(runs[2]),
            metrics: runs[2].metrics,
          },
        ],
      },
      metrics: {
        'P01-render-isolation': {
          ...runs[0].metrics['P01-render-isolation'],
          status: 'PASS',
          absoluteUpdateLimit: 1,
        },
        'P04-resource-budget': {
          ...runs[0].metrics['P04-resource-budget'],
          status: 'PASS',
          maxRegressionRatio: 1.05,
        },
      },
    });
    expect(baselineArtifact.metrics['P01-render-isolation'].mainPageUpdateCommits).toBe(40);
    expect(baselineArtifact.measurementAudit.reproducibilityRuns).toHaveLength(2);
  });

  it('rejects missing provenance or drift across runs and preserves canonical metric summaries', () => {
    const missingAudit = freezeRuns();
    missingAudit[0].provenance.baselineAudit = null;
    expect(() => buildFreezeArtifact(missingAudit)).toThrow(/baselineAudit/);
    for (const mutate of [
      (run) => { run.provenance.runnerSha = 'f'.repeat(40); },
      (run) => { run.provenance.runnerContentHash = 'f'.repeat(64); },
      (run) => { run.environment.cpu.model = 'different-cpu'; },
    ]) {
      const drifted = freezeRuns();
      mutate(drifted[1]);
      expect(() => buildFreezeArtifact(drifted)).toThrow(/mismatch/);
    }
    const insensitive = freezeRuns();
    insensitive[0].metrics['P01-render-isolation'].mutationDetected = false;
    expect(() => buildFreezeArtifact(insensitive)).toThrow(/mutation sensitivity/);
    const forgedResource = freezeRuns();
    forgedResource[0].metrics['P04-resource-budget'].totalBundleBytes = 101;
    expect(() => buildFreezeArtifact(forgedResource)).toThrow(/not recomputable/);
    const pollutedRunnerDist = freezeRuns();
    pollutedRunnerDist[0].metrics['P04-resource-budget'].baseBuild.distManifest[0].sha256 = 'f'.repeat(64);
    expect(() => buildFreezeArtifact(pollutedRunnerDist)).toThrow(/manifest hash/);
  });

  it('rejects internally consistent runs whose Git identity differs from preflight', () => {
    for (const mutate of [
      (run) => { run.provenance.runnerSha = 'f'.repeat(40); },
      (run) => { run.provenance.runnerTree = 'f'.repeat(40); },
      (run) => {
        run.subjectTree = 'f'.repeat(40);
        run.provenance.baselineAudit.baseTree = 'f'.repeat(40);
        run.metrics['P01-render-isolation'].subjectProduct.subjectTree = 'f'.repeat(40);
      },
    ]) {
      const forged = freezeRuns();
      forged.forEach(mutate);
      expect(() => buildFreezeArtifact(forged)).toThrow(/provenance mismatch|BASE detached-build/);
    }
  });

  it('allows bounded loadAverage drift but rejects a materially different host load', () => {
    expect(() => buildFreezeArtifact(freezeRuns())).not.toThrow();
    const overloaded = freezeRuns();
    overloaded[2].environment.loadAverage = [20, 20, 20];
    expect(() => buildFreezeArtifact(overloaded))
      .toThrow('freeze run 3 loadAverage[0] differs beyond 4: left=20, right=1, delta=19');
  });

  it('collects exactly three runs and passes the structured artifact to the writer', async () => {
    const runs = freezeRuns();
    const collectEvidence = vi.fn()
      .mockResolvedValueOnce(runs[0]).mockResolvedValueOnce(runs[1]).mockResolvedValueOnce(runs[2]);
    const outputPath = join(tmpdir(), 'frontend-freeze-orchestration.json');
    const preflight = vi.fn(() => ({
      outputPath, planSnapshotSha: PLAN_SNAPSHOT_SHA, runnerSha: RUNNER_SHA, runnerTree: RUNNER_TREE,
      subjectSha: SUBJECT_SHA, subjectTree: SUBJECT_TREE,
    }));
    const writeBaseline = vi.fn();
    const artifact = await freezePerformanceBaseline({
      subjectSha: SUBJECT_SHA, planSnapshotSha: PLAN_SNAPSHOT_SHA, outputPath, collectEvidence,
      collectBaseResources: () => runs[0].metrics['P04-resource-budget'], preflight, writeBaseline,
    });
    expect(FREEZE_RUN_COUNT).toBe(3);
    expect(collectEvidence).toHaveBeenCalledTimes(3);
    expect(collectEvidence.mock.calls).toEqual(Array.from({ length: 3 }, () => [{
      subjectSha: SUBJECT_SHA, resourceBudget: runs[0].metrics['P04-resource-budget'],
    }]));
    expect(writeBaseline).toHaveBeenCalledOnce();
    expect(writeBaseline).toHaveBeenCalledWith(outputPath, artifact);
  });

  it('writes exact JSON atomically and removes temporary output when rename fails', () => {
    const directory = mkdtempSync(join(tmpdir(), 'frontend-freeze-writer-'));
    try {
      const outputPath = join(directory, 'baseline.json');
      const artifact = buildFreezeArtifact(freezeRuns());
      expect(writeFrozenBaselineAtomically(outputPath, artifact)).toBe(outputPath);
      expect(JSON.parse(readFileSync(outputPath, 'utf8'))).toEqual(artifact);
      const blockedPath = join(directory, 'blocked.json');
      mkdirSync(blockedPath);
      expect(() => writeFrozenBaselineAtomically(blockedPath, artifact)).toThrow();
      expect(existsSync(blockedPath)).toBe(true);
      expect(readdirSync(directory).filter((name) => name.includes('.tmp'))).toEqual([]);
    } finally {
      rmSync(directory, { recursive: true, force: true });
    }
  });
});
