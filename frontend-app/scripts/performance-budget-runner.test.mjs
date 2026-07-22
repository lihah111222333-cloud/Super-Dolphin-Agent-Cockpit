import {
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  readdirSync,
  rmSync,
  writeFileSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { cwd } from 'node:process';
import { describe, expect, it, vi } from 'vitest';
import {
  FREEZE_RUN_COUNT,
  buildFrozenPerformanceBaseline,
  collectCandidateResourceBudget,
  freezePerformanceBaseline,
  parseArguments,
  validateCaseRegistry,
  validateFreezeOutputPath,
  validateFreezePreconditions,
  verifyPerformanceEvidence,
  writeFrozenBaselineAtomically,
} from './performance-budget-runner.mjs';
import {
  baseline,
  evidence,
  pairedTimingCase,
  SUBJECT_SHA,
  SUBJECT_TREE,
  timingCase,
} from './performance-budget-runner.test-helper.mjs';

const RUNNER_SHA = 'b'.repeat(40);
const RUNNER_TREE = '2'.repeat(40);
const PLAN_SNAPSHOT_SHA = 'c'.repeat(40);
const RUNNER_CONTENT_HASH = 'd'.repeat(64);

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

function registry() {
  return JSON.parse(readFileSync(join(cwd(), 'scripts/frontend-performance-cases.json'), 'utf8'));
}

describe('candidate detached resource build', () => {
  it('builds a clean SUBJECT with bounded managed commands and binds the measured artifact provenance', async () => {
    const commands = [];
    let measured = 0;
    const execute = (command, args) => {
      commands.push([command, ...args]);
      if (command === 'git' && args[0] === 'worktree' && args[1] === 'add') {
        mkdirSync(join(args[3], 'frontend-app'), { recursive: true });
      }
      return '';
    };
    const runCommand = async (command, args, options) => {
      commands.push([command, ...args]);
      expect(options).toEqual(expect.objectContaining({
        killGraceMs: expect.any(Number),
        maxBuffer: expect.any(Number),
        timeoutMs: expect.any(Number),
      }));
      if (command === 'npm' && args[0] === 'ci') {
        expect(existsSync(join(options.cwd, 'dist'))).toBe(false);
      } else if (command === 'npm' && args.join(' ') === 'run build') {
        mkdirSync(join(options.cwd, 'dist', 'assets'), { recursive: true });
        writeFileSync(join(options.cwd, 'dist', 'index.html'), '<main>candidate</main>');
        writeFileSync(join(options.cwd, 'dist', 'assets', 'app.js'), 'globalThis.candidate = true;');
      }
      return {
        error: undefined,
        outputTruncated: false,
        status: 0,
        stderr: '',
        stdout: '',
        timedOut: false,
      };
    };
    const metric = await collectCandidateResourceBudget({
      subjectSha: SUBJECT_SHA,
      subjectTree: SUBJECT_TREE,
      repositoryRoot: '/repository',
      execute,
      measureResources: ({ distDir, subjectSha }) => {
        measured += 1;
        const files = [
          { path: 'assets/app.js', bytes: readFileSync(join(distDir, 'assets', 'app.js')).byteLength },
          { path: 'index.html', bytes: readFileSync(join(distDir, 'index.html')).byteLength },
        ];
        return {
          metricId: 'P04-resource-budget',
          subjectSha,
          fileCount: files.length,
          totalBundleBytes: files.reduce((total, file) => total + file.bytes, 0),
          maxChunkBytes: Math.max(...files.map(({ bytes }) => bytes)),
          files,
        };
      },
      runCommand,
    });

    expect(measured).toBe(1);
    expect(commands.map((command) => command.slice(0, 3))).toEqual([
      ['git', 'worktree', 'add'],
      ['npm', 'ci'],
      ['npm', 'run', 'build'].slice(0, 3),
      ['git', 'worktree', 'remove'],
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

describe('performance budget runner registry', () => {
  it('keeps the fixed plan threshold even when a baseline is hand-relaxed', () => {
    const relaxedThreshold = baseline();
    relaxedThreshold.metrics['P02-history-budget'].maxRegressionRatio = 1.25;
    const currentEvidence = evidence();
    expect(verifyPerformanceEvidence(currentEvidence, relaxedThreshold).verdicts
      .find(({ metricId }) => metricId === 'P02-history-budget').status).toBe('NOT_VERIFIED');
  });

  it('refuses a missing or changed frozen runner content hash', () => {
    const missing = baseline();
    delete missing.provenance;
    expect(verifyPerformanceEvidence(evidence(), missing).status).toBe('NOT_VERIFIED');

    const changed = baseline();
    changed.provenance.runnerContentHash = 'different-runner';
    expect(verifyPerformanceEvidence(evidence(), changed).status).toBe('NOT_VERIFIED');
  });

  it('marks a legacy absolute-duration P02 baseline NOT_VERIFIED', () => {
    const legacy = baseline();
    legacy.metrics['P02-history-budget'].cases = Object.fromEntries(
      Object.keys(legacy.metrics['P02-history-budget'].cases)
        .map((caseId) => [
          caseId,
          timingCase(100, 'p50(process.cpuUsage(user+system),500000-iteration-blocks)'),
        ]),
    );

    const verdict = verifyPerformanceEvidence(evidence(), legacy);
    expect(verdict.status).toBe('NOT_VERIFIED');
    expect(verdict.verdicts.find(({ metricId }) => metricId === 'P02-history-budget'))
      .toEqual(expect.objectContaining({
        status: 'NOT_VERIFIED',
        reason: expect.stringMatching(/paired schema is invalid/),
      }));
  });

  it('derives thirteen exact current-tree cases and rejects zero, missing, stale, and duplicate registrations', () => {
    const currentEvidence = evidence();
    const verdict = verifyPerformanceEvidence(currentEvidence, baseline());
    expect(verdict).toEqual(expect.objectContaining({
      status: 'PASS',
      testCount: 13,
    }));
    expect(verdict.verdicts).toHaveLength(4);
    expect(verdict.verdicts.every(({ status }) => status === 'PASS')).toBe(true);
    expect(verdict.caseIds).toEqual(registry().cases.map(({ caseId }) => caseId));

    const zero = { ...registry(), testCount: 0, cases: [] };
    expect(() => validateCaseRegistry(currentEvidence, verdict.verdicts, zero)).toThrow(/zero tests/);

    const missing = registry();
    missing.cases.pop();
    missing.testCount -= 1;
    expect(() => validateCaseRegistry(currentEvidence, verdict.verdicts, missing))
      .toThrow(/exact set mismatch/);

    const stale = registry();
    stale.cases.push({
      caseId: 'stale-performance-case',
      metricId: 'P04-resource-budget',
      evidenceKey: 'stale',
    });
    stale.testCount += 1;
    expect(() => validateCaseRegistry(currentEvidence, verdict.verdicts, stale))
      .toThrow(/exact set mismatch/);

    const duplicate = registry();
    duplicate.cases[1] = { ...duplicate.cases[0] };
    expect(() => validateCaseRegistry(currentEvidence, verdict.verdicts, duplicate))
      .toThrow(/duplicate caseIds/);
  });

  it('fails the exact history case that exceeds the frozen fifteen percent threshold', () => {
    const regressed = evidence();
    regressed.metrics['P02-history-budget'].cases['turns-5000-tools-3'] = pairedTimingCase(1.16);
    const verdict = verifyPerformanceEvidence(regressed, baseline());

    expect(verdict.status).toBe('FAIL');
    expect(verdict.caseResults.find(({ caseId }) => caseId === 'turns-5000-tools-3'))
      .toEqual(expect.objectContaining({ status: 'FAIL' }));
  });

  it('fails P04 when the V8 heap measurement exceeds the frozen five percent threshold', () => {
    const regressed = evidence();
    regressed.metrics['P04-resource-budget'].heapUsedSamplesBytes = [106, 106, 106, 106, 106];
    regressed.metrics['P04-resource-budget'].heapUsedMedianBytes = 106;
    const verdict = verifyPerformanceEvidence(regressed, baseline());
    expect(verdict.status).toBe('FAIL');
    expect(verdict.verdicts.find(({ metricId }) => metricId === 'P04-resource-budget'))
      .toEqual(expect.objectContaining({ status: 'FAIL' }));
  });

  it('emits the required heap verdict when a bundle case has already failed', () => {
    const regressed = evidence();
    regressed.metrics['P04-resource-budget'].totalBundleBytes = 106;
    const verdict = verifyPerformanceEvidence(regressed, baseline());

    expect(verdict.status).toBe('FAIL');
    expect(verdict.caseResults.filter(({ metricId }) => metricId === 'P04-resource-budget'))
      .toEqual([
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
      mode: 'freeze',
      subjectSha: SUBJECT_SHA,
      planSnapshotSha: PLAN_SNAPSHOT_SHA,
      outputPath: resolve(outputPath),
    }));
  });

  it('restricts freeze output to the baseline artifact or system temporary JSON paths', () => {
    expect(validateFreezeOutputPath(join(tmpdir(), 'safe-baseline.json')))
      .toBe(resolve(tmpdir(), 'safe-baseline.json'));
    expect(() => validateFreezeOutputPath(join(cwd(), 'unsafe-baseline.json')))
      .toThrow(/baseline artifact or a JSON path/);
    expect(() => validateFreezeOutputPath(join(tmpdir(), 'not-json.txt')))
      .toThrow(/baseline artifact or a JSON path/);
  });

  it('fails preflight for dirty worktrees, self subjects, non-ancestors, and missing plan commits', () => {
    const outputPath = join(tmpdir(), 'frontend-baseline-preflight.json');
    const validate = (overrides = {}) => validateFreezePreconditions({
      subjectSha: SUBJECT_SHA,
      planSnapshotSha: PLAN_SNAPSHOT_SHA,
      outputPath,
      git: validGit,
      ...overrides,
    });
    expect(validate()).toEqual({
      outputPath: resolve(outputPath),
      planSnapshotSha: PLAN_SNAPSHOT_SHA,
      runnerSha: RUNNER_SHA,
      runnerTree: RUNNER_TREE,
      subjectSha: SUBJECT_SHA,
      subjectTree: SUBJECT_TREE,
    });
    expect(() => validate({
      git: (args) => (args[0] === 'status' ? ' M dirty-file' : validGit(args)),
    })).toThrow(/clean committed runner worktree/);
    expect(() => validate({ subjectSha: RUNNER_SHA })).toThrow(/must differ/);
    expect(() => validate({
      git: (args) => {
        if (args[0] === 'merge-base') throw new Error('not ancestor');
        return validGit(args);
      },
    })).toThrow(/must be an ancestor/);
    expect(() => validate({
      git: (args) => {
        if (args.join(' ') === `cat-file -e ${PLAN_SNAPSHOT_SHA}^{commit}`) throw new Error('missing');
        return validGit(args);
      },
    })).toThrow(/plan snapshot commit does not exist/);
  });

  it('builds the exact frozen artifact from designated run one without applying the P01 candidate limit', () => {
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
        'P02-history-budget': {
          ...runs[0].metrics['P02-history-budget'],
          status: 'PASS',
          maxRegressionRatio: 1.15,
        },
        'P03-feedback-budget': {
          ...runs[0].metrics['P03-feedback-budget'],
          status: 'PASS',
          maxRegressionRatio: 1.15,
        },
        'P04-resource-budget': {
          ...runs[0].metrics['P04-resource-budget'],
          status: 'PASS',
          maxRegressionRatio: 1.05,
        },
      },
    });
    expect(baselineArtifact.metrics['P01-render-isolation'].mainPageUpdateCommits).toBe(40);
  });

  it('rejects missing baselineAudit and runner, hash, or stable environment drift across runs', () => {
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
  });

  it('rejects three internally consistent runs whose Git identity differs from preflight', () => {
    for (const mutate of [
      (run) => { run.provenance.runnerSha = 'f'.repeat(40); },
      (run) => { run.provenance.runnerTree = 'f'.repeat(40); },
      (run) => {
        run.subjectTree = 'f'.repeat(40);
        run.provenance.baselineAudit.baseTree = 'f'.repeat(40);
        run.metrics['P01-render-isolation'].subjectProduct.subjectTree = 'f'.repeat(40);
        run.metrics['P02-history-budget'].subjectProduct.subjectTree = 'f'.repeat(40);
        run.metrics['P03-feedback-budget'].subjectRuntime.subjectTree = 'f'.repeat(40);
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
    expect(() => buildFreezeArtifact(overloaded)).toThrow(/loadAverage/);

  });

  it('rejects forged P02/P03 raw evidence and legacy P02 schema', () => {

    const forgedP02 = freezeRuns();
    forgedP02[0].metrics['P02-history-budget'].cases['turns-200-tools-1']
      .sampleDiagnostics[0].rawNormalizedBlockRatios[0] = 99;
    expect(() => buildFreezeArtifact(forgedP02)).toThrow(/not reproducible/);

    const forgedP03 = freezeRuns();
    forgedP03[0].metrics['P03-feedback-budget'].cases['stop-visible-feedback']
      .durationSamplesMs[0] = 999;
    expect(() => buildFreezeArtifact(forgedP03)).toThrow(/raw measurement/);

    const legacy = freezeRuns();
    legacy[0].metrics['P02-history-budget'].cases['turns-200-tools-1'] = timingCase(
      100,
      'p50(process.cpuUsage(user+system),500000-iteration-blocks)',
    );
    expect(() => buildFreezeArtifact(legacy)).toThrow(/paired schema/);
  });

  it('rejects forged P01 mutation and P04 resource summaries', () => {
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

  it('collects exactly three runs and passes the structured artifact to the writer', async () => {
    const runs = freezeRuns();
    const collectEvidence = vi.fn()
      .mockResolvedValueOnce(runs[0])
      .mockResolvedValueOnce(runs[1])
      .mockResolvedValueOnce(runs[2]);
    const outputPath = join(tmpdir(), 'frontend-freeze-orchestration.json');
    const preflight = vi.fn(() => ({
      outputPath,
      planSnapshotSha: PLAN_SNAPSHOT_SHA,
      runnerSha: RUNNER_SHA,
      runnerTree: RUNNER_TREE,
      subjectSha: SUBJECT_SHA,
      subjectTree: SUBJECT_TREE,
    }));
    const writeBaseline = vi.fn();
    const artifact = await freezePerformanceBaseline({
      subjectSha: SUBJECT_SHA,
      planSnapshotSha: PLAN_SNAPSHOT_SHA,
      outputPath,
      collectEvidence,
      collectBaseResources: () => runs[0].metrics['P04-resource-budget'],
      preflight,
      writeBaseline,
    });
    expect(FREEZE_RUN_COUNT).toBe(3);
    expect(collectEvidence).toHaveBeenCalledTimes(3);
    expect(collectEvidence.mock.calls).toEqual(Array.from(
      { length: 3 },
      () => [{
        subjectSha: SUBJECT_SHA,
        resourceBudget: runs[0].metrics['P04-resource-budget'],
      }],
    ));
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
