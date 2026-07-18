import {
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  readdirSync,
  rmSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { cwd } from 'node:process';
import { describe, expect, it, vi } from 'vitest';
import {
  FREEZE_RUN_COUNT,
  buildFrozenPerformanceBaseline,
  freezePerformanceBaseline,
  parseArguments,
  validateCaseRegistry,
  validateFreezeOutputPath,
  validateFreezePreconditions,
  verifyPerformanceEvidence,
  writeFrozenBaselineAtomically,
} from './performance-budget-runner.mjs';
import {
  CPU_DURATION_CLOCK,
  FEEDBACK_DURATION_CLOCK,
} from './performance-budget-model.mjs';

const SUBJECT_SHA = 'a'.repeat(40);
const SUBJECT_TREE = '1'.repeat(40);
const RUNNER_SHA = 'b'.repeat(40);
const RUNNER_TREE = '2'.repeat(40);
const PLAN_SNAPSHOT_SHA = 'c'.repeat(40);
const RUNNER_CONTENT_HASH = 'd'.repeat(64);

function timingCase(durationMedianMs, durationClock = CPU_DURATION_CLOCK) {
  return {
    attemptsPerSample: 1,
    durationClock,
    iterationCount: 100,
    durationAttemptSamplesMs: Array.from(
      { length: 5 },
      () => [durationMedianMs],
    ),
    durationSamplesMs: Array.from({ length: 5 }, () => durationMedianMs),
    durationMedianMs,
  };
}

function pairedTimingCase(normalizedRatio) {
  const blockCount = 3;
  const sampleDiagnostics = Array.from({ length: 5 }, (_, sampleIndex) => {
    const referenceBlockCpuDurationsMs = [10, 11, 12];
    const productionBlockCpuDurationsMs = referenceBlockCpuDurationsMs
      .map((reference) => reference * normalizedRatio);
    const rawNormalizedBlockRatios = productionBlockCpuDurationsMs
      .map((production, blockIndex) => production / referenceBlockCpuDurationsMs[blockIndex]);
    return {
      blockOrders: Array.from(
        { length: blockCount },
        (_, blockIndex) => ((sampleIndex + blockIndex) % 2 === 0
          ? 'production-reference'
          : 'reference-production'),
      ),
      productionBlockCpuDurationsMs,
      referenceBlockCpuDurationsMs,
      rawNormalizedBlockRatios,
      normalizedRatio: [...rawNormalizedBlockRatios].sort((left, right) => left - right)[1],
    };
  });
  const normalizedRatioSamples = sampleDiagnostics.map(({ normalizedRatio: sample }) => sample);
  return {
    attemptsPerSample: 1,
    durationClock: CPU_DURATION_CLOCK,
    blockCount,
    blockIterationCount: 100,
    iterationCount: 300,
    materializedCount: 80,
    referenceMaterializedCount: 80,
    sampleDiagnostics,
    normalizedRatioSamples,
    normalizedRatioMedian: [...normalizedRatioSamples].sort((left, right) => left - right)[2],
  };
}

function evidence(subjectSha = SUBJECT_SHA) {
  const historyCases = Object.fromEntries([
    'turns-200-tools-1',
    'turns-200-tools-3',
    'turns-1000-tools-1',
    'turns-1000-tools-3',
    'turns-5000-tools-1',
    'turns-5000-tools-3',
  ].map((caseId) => [caseId, pairedTimingCase(1)]));
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
    provenance: { runnerContentHash: 'runner-hash' },
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
      },
      'P02-history-budget': {
        metricId: 'P02-history-budget',
        subjectSha,
        sampleCount: 5,
        warmupCount: 1,
        cases: historyCases,
      },
      'P03-feedback-budget': {
        metricId: 'P03-feedback-budget',
        subjectSha,
        sampleCount: 5,
        warmupCount: 1,
        cases: { 'stop-visible-feedback': timingCase(100, FEEDBACK_DURATION_CLOCK) },
      },
      'P04-resource-budget': {
        metricId: 'P04-resource-budget',
        subjectSha,
        fileCount: 2,
        totalBundleBytes: 100,
        maxChunkBytes: 50,
        files: [
          { path: 'assets/a.js', bytes: 50 },
          { path: 'assets/Chat.js', bytes: 50 },
        ],
      },
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
  current.provenance = {
    runnerId: 'frontend-performance-budget',
    runnerSha: RUNNER_SHA,
    runnerTree: RUNNER_TREE,
    runnerContentHash: RUNNER_CONTENT_HASH,
    runnerFiles: [
      { path: 'frontend-app/scripts/performance-budget-runner.mjs', sha256: 'e'.repeat(64) },
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
  const frozenSampled = {
    status: 'PASS',
    subjectSha: 'a'.repeat(40),
    sampleCount: 5,
    warmupCount: 1,
    maxRegressionRatio: 1.15,
  };
  const current = evidence();
  return {
    provenance: { runnerContentHash: 'runner-hash' },
    metrics: {
      'P01-render-isolation': {
        status: 'PASS',
        subjectSha: 'a'.repeat(40),
        absoluteUpdateLimit: 1,
        updateCount: 20,
        warmupUpdates: 2,
        mainPageUpdateCommits: 0,
        unrelatedSubtreeUpdateCommits: 0,
      },
      'P02-history-budget': {
        ...frozenSampled,
        cases: current.metrics['P02-history-budget'].cases,
      },
      'P03-feedback-budget': {
        ...frozenSampled,
        cases: current.metrics['P03-feedback-budget'].cases,
      },
      'P04-resource-budget': {
        status: 'PASS',
        subjectSha: 'a'.repeat(40),
        maxRegressionRatio: 1.05,
        totalBundleBytes: 100,
        maxChunkBytes: 50,
      },
    },
  };
}

function registry() {
  return JSON.parse(readFileSync(join(cwd(), 'scripts/frontend-performance-cases.json'), 'utf8'));
}

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

  it('derives twelve exact current-tree cases and rejects zero, missing, stale, and duplicate registrations', () => {
    const currentEvidence = evidence();
    const verdict = verifyPerformanceEvidence(currentEvidence, baseline());
    expect(verdict).toEqual(expect.objectContaining({
      status: 'PASS',
      testCount: 12,
    }));
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
      },
    ]) {
      const forged = freezeRuns();
      forged.forEach(mutate);
      expect(() => buildFreezeArtifact(forged)).toThrow(/provenance mismatch/);
    }
  });

  it('allows loadAverage drift but rejects forged P02/P03 raw evidence and legacy P02 schema', () => {
    expect(() => buildFreezeArtifact(freezeRuns())).not.toThrow();

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
      preflight,
      writeBaseline,
    });
    expect(FREEZE_RUN_COUNT).toBe(3);
    expect(collectEvidence).toHaveBeenCalledTimes(3);
    expect(collectEvidence.mock.calls).toEqual(Array.from(
      { length: 3 },
      () => [{ subjectSha: SUBJECT_SHA }],
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
