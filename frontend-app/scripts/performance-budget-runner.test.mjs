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
import { dirname, join, resolve } from 'node:path';
import { cwd } from 'node:process';
import { describe, expect, it, vi } from 'vitest';
import {
  FREEZE_RUN_COUNT,
  buildFrozenPerformanceBaseline,
  collectCandidateResourceBudget,
  collectDetachedP01P02Evidence,
  collectDetachedStopFeedbackBudget,
  freezePerformanceBaseline,
  parseArguments,
  validateCaseRegistry,
  validateFreezeOutputPath,
  validateFreezePreconditions,
  validateP03SubjectRuntime,
  verifyPerformanceEvidence,
  writeFrozenBaselineAtomically,
} from './performance-budget-runner.mjs';
import {
  CPU_DURATION_CLOCK,
  FEEDBACK_DURATION_CLOCK,
} from './performance-budget-model.mjs';
import { HEAP_MEASUREMENT_CLOCK } from './resource-budget.mjs';
import { P02_SUBJECT_CONTENT_PATHS } from './chat-history-benchmark.mjs';
import {
  P03_SUBJECT_FEEDBACK_COMPONENT_PATH,
  P03_SUBJECT_CONTENT_PATHS,
} from './stop-feedback-benchmark.mjs';

const SUBJECT_SHA = 'a'.repeat(40);
const SUBJECT_TREE = '1'.repeat(40);
const RUNNER_SHA = 'b'.repeat(40);
const RUNNER_TREE = '2'.repeat(40);
const PLAN_SNAPSHOT_SHA = 'c'.repeat(40);
const RUNNER_CONTENT_HASH = 'd'.repeat(64);

function p03SubjectContentFiles() {
  return P03_SUBJECT_CONTENT_PATHS.map((path, index) => ({
    path,
    sha256: String(index).repeat(64),
  }));
}

function createP03SubjectClosure(root) {
  for (const path of P03_SUBJECT_CONTENT_PATHS) {
    const filePath = join(root, path);
    mkdirSync(dirname(filePath), { recursive: true });
    writeFileSync(filePath, 'subject closure');
  }
}

function p02SubjectContentFiles() {
  return P02_SUBJECT_CONTENT_PATHS.map((path, index) => ({
    path,
    sha256: String(index + 5).repeat(64),
  }));
}

function p03SubjectContentHash(files) {
  return createHash('sha256').update(files.map(({ path, sha256 }) => `${path}\0${sha256}\n`).join('')).digest('hex');
}

function p02SubjectContentHash(files) {
  return createHash('sha256').update(files.map(({ path, sha256 }) => `${path}\0${sha256}\n`).join('')).digest('hex');
}

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
  const p02ContentFiles = p02SubjectContentFiles();
  const p03ContentFiles = p03SubjectContentFiles();
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
      'P02-history-budget': {
        metricId: 'P02-history-budget',
        subjectSha,
        sampleCount: 5,
        warmupCount: 1,
        subjectProduct: {
          content: {
            contentHash: p02SubjectContentHash(p02ContentFiles),
            files: p02ContentFiles,
          },
          subjectSha,
          subjectTree: SUBJECT_TREE,
        },
        cases: historyCases,
      },
      'P03-feedback-budget': {
        metricId: 'P03-feedback-budget',
        subjectSha,
        sampleCount: 5,
        warmupCount: 1,
        subjectRuntime: {
          subjectSha,
          subjectTree: SUBJECT_TREE,
          runtimePath: 'frontend-app/src/entities/client/model/threadLifecycleRuntime.js',
          feedbackComponentPath: P03_SUBJECT_FEEDBACK_COMPONENT_PATH,
          installArgv: ['npm', 'ci'],
          worktreeClean: true,
          worktreeStatus: [],
          content: {
            contentHash: p03SubjectContentHash(p03ContentFiles),
            files: p03ContentFiles,
          },
        },
        subjectFeedbackComponent: { path: P03_SUBJECT_FEEDBACK_COMPONENT_PATH, source: 'subject' },
        cases: { 'stop-visible-feedback': timingCase(100, FEEDBACK_DURATION_CLOCK) },
      },
      'P04-resource-budget': {
        metricId: 'P04-resource-budget',
        subjectSha,
        fileCount: 2,
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
        files: [
          { path: 'assets/a.js', bytes: 50 },
          { path: 'assets/Chat.js', bytes: 50 },
        ],
        baseBuild: {
          baseSha: subjectSha,
          baseTree: SUBJECT_TREE,
          installArgv: ['npm', 'ci'],
          buildArgv: ['npm', 'run', 'build'],
          distManifest: [
            { path: 'assets/a.js', bytes: 50, sha256: 'a'.repeat(64) },
            { path: 'assets/Chat.js', bytes: 50, sha256: 'b'.repeat(64) },
          ],
          distManifestHash: createHash('sha256').update([
            `assets/a.js\0${50}\0${'a'.repeat(64)}\n`,
            `assets/Chat.js\0${50}\0${'b'.repeat(64)}\n`,
          ].join('')).digest('hex'),
        },
        candidateBuild: {
          subjectSha,
          subjectTree: SUBJECT_TREE,
          installArgv: ['npm', 'ci'],
          buildArgv: ['npm', 'run', 'build'],
          distManifest: [
            { path: 'assets/a.js', bytes: 50, sha256: 'a'.repeat(64) },
            { path: 'assets/Chat.js', bytes: 50, sha256: 'b'.repeat(64) },
          ],
          distManifestHash: createHash('sha256').update([
            `assets/a.js\0${50}\0${'a'.repeat(64)}\n`,
            `assets/Chat.js\0${50}\0${'b'.repeat(64)}\n`,
          ].join('')).digest('hex'),
        },
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

describe('P01/P02 detached subject closure', () => {
  it('runs the P01 probe and P02 workload against the detached requested subject, not runner imports', async () => {
    const commands = [];
    let temporaryRoot = '';
    const execute = (command, args) => {
      commands.push([command, ...args]);
      if (command === 'git' && args[0] === 'worktree' && args[1] === 'add') {
        temporaryRoot = args[3];
        mkdirSync(join(temporaryRoot, 'frontend-app', 'scripts'), { recursive: true });
        writeFileSync(join(temporaryRoot, 'frontend-app', 'scripts', 'render-isolation-probe.test.jsx'), 'subject probe');
        return '';
      }
      if (command === 'git' && args.join(' ') === 'rev-parse HEAD') return SUBJECT_SHA;
      if (command === 'git' && args.join(' ') === 'rev-parse HEAD^{tree}') return SUBJECT_TREE;
      if (command === 'git' && args.join(' ') === 'status --porcelain --untracked-files=all') return '';
      if (command === 'git' && args[0] === 'worktree' && args[1] === 'remove') return '';
      throw new Error(`unexpected command: ${command} ${args.join(' ')}`);
    };
    const runCommand = vi.fn(async (command, args, options) => {
      commands.push([command, ...args]);
      expect([command, ...args]).toEqual(['npm', 'ci']);
      expect(options.cwd).toBe(join(temporaryRoot, 'frontend-app'));
      return {
        error: undefined,
        outputTruncated: false,
        status: 0,
        stderr: '',
        stdout: '',
        timedOut: false,
      };
    });
    const target = Object.freeze({
      provenance: Object.freeze({ subjectSha: SUBJECT_SHA, subjectTree: SUBJECT_TREE }),
    });
    const collectRender = vi.fn(({ frontendRoot }) => {
      expect(frontendRoot).toBe(join(temporaryRoot, 'frontend-app'));
      return { metricId: 'P01-render-isolation', updateCount: 20 };
    });
    const loadHistoryTarget = vi.fn(async (options) => {
      expect(options).toEqual({
        subjectRoot: temporaryRoot,
        subjectSha: SUBJECT_SHA,
        subjectTree: SUBJECT_TREE,
      });
      return target;
    });
    const runHistory = vi.fn(({ commit, target: loadedTarget }) => {
      expect(loadedTarget).toBe(target);
      return {
        metricId: 'P02-history-budget',
        subjectProduct: target.provenance,
        subjectSha: commit,
      };
    });

    const measured = await collectDetachedP01P02Evidence({
      subjectSha: SUBJECT_SHA,
      subjectTree: SUBJECT_TREE,
      repositoryRoot: '/repository',
      execute,
      collectRender,
      loadHistoryTarget,
      runCommand,
      runHistory,
    });

    expect(measured.renderIsolation).toEqual(expect.objectContaining({
      metricId: 'P01-render-isolation',
      subjectProduct: expect.objectContaining({ subjectSha: SUBJECT_SHA, subjectTree: SUBJECT_TREE }),
      updateCount: 20,
    }));
    expect(measured.historyBudget.subjectSha).toBe(SUBJECT_SHA);
    expect(collectRender).toHaveBeenCalledOnce();
    expect(loadHistoryTarget).toHaveBeenCalledOnce();
    expect(runHistory).toHaveBeenCalledOnce();
    expect(commands.map((command) => command.slice(0, 3))).toEqual([
      ['git', 'worktree', 'add'],
      ['git', 'rev-parse', 'HEAD'],
      ['git', 'rev-parse', 'HEAD^{tree}'],
      ['git', 'status', '--porcelain'],
      ['npm', 'ci'],
      ['git', 'status', '--porcelain'],
      ['git', 'status', '--porcelain'],
      ['git', 'worktree', 'remove'],
    ]);
    expect(existsSync(temporaryRoot)).toBe(false);
  });
});

describe('P03 detached subject runtime', () => {
  it('uses a bounded managed npm command and cleans the detached worktree after timeout', async () => {
    const commands = [];
    let temporaryRoot = '';
    const execute = (command, args) => {
      commands.push([command, ...args]);
      if (command === 'git' && args[0] === 'worktree' && args[1] === 'add') {
        temporaryRoot = args[3];
        createP03SubjectClosure(temporaryRoot);
        return '';
      }
      if (command === 'git' && args.join(' ') === 'rev-parse HEAD') return SUBJECT_SHA;
      if (command === 'git' && args.join(' ') === 'rev-parse HEAD^{tree}') return SUBJECT_TREE;
      if (command === 'git' && args.join(' ') === 'status --porcelain --untracked-files=all') return '';
      if (command === 'git' && args[0] === 'worktree' && args[1] === 'remove') return '';
      throw new Error(`unexpected command: ${command} ${args.join(' ')}`);
    };
    const runCommand = vi.fn(async (command, args, options) => {
      expect([command, ...args]).toEqual(['npm', 'ci']);
      expect(options).toEqual(expect.objectContaining({
        cwd: join(temporaryRoot, 'frontend-app'),
        killGraceMs: expect.any(Number),
        maxBuffer: expect.any(Number),
        timeoutMs: expect.any(Number),
      }));
      return {
        error: new Error('managed command timed out after 1ms'),
        outputTruncated: false,
        signal: 'SIGTERM',
        status: null,
        stderr: '',
        stdout: 'bounded output',
        timedOut: true,
      };
    });

    await expect(collectDetachedStopFeedbackBudget({
      subjectSha: SUBJECT_SHA,
      subjectTree: SUBJECT_TREE,
      repositoryRoot: '/repository',
      execute,
      runCommand,
    })).rejects.toThrow(/timed out/);
    expect(runCommand).toHaveBeenCalledOnce();
    expect(commands).toContainEqual(['git', 'worktree', 'remove', '--force', temporaryRoot]);
    expect(existsSync(temporaryRoot)).toBe(false);
  });

  function subjectTarget() {
    return {
      attachRuntime() {},
      feedbackProbe() {},
      provenance: {
        subjectSha: SUBJECT_SHA,
        subjectTree: SUBJECT_TREE,
        runtimePath: 'frontend-app/src/entities/client/model/threadLifecycleRuntime.js',
        feedbackComponentPath: P03_SUBJECT_FEEDBACK_COMPONENT_PATH,
        content: {
          contentHash: p03SubjectContentHash(p03SubjectContentFiles()),
          files: p03SubjectContentFiles(),
        },
      },
    };
  }

  it('loads the requested clean subject worktree after npm ci and records its target provenance', async () => {
    const commands = [];
    const execute = (command, args) => {
      commands.push([command, ...args]);
      if (command === 'git' && args[0] === 'worktree' && args[1] === 'add') {
        createP03SubjectClosure(args[3]);
        return '';
      }
      if (command === 'git' && args.join(' ') === 'rev-parse HEAD') return SUBJECT_SHA;
      if (command === 'git' && args.join(' ') === 'rev-parse HEAD^{tree}') return SUBJECT_TREE;
      if (command === 'git' && args.join(' ') === 'status --porcelain --untracked-files=all') return '';
      if (command === 'git' && args[0] === 'worktree' && args[1] === 'remove') return '';
      throw new Error(`unexpected command: ${command} ${args.join(' ')}`);
    };
    const runCommand = vi.fn(async (command, args, options) => {
      commands.push([command, ...args]);
      expect([command, ...args]).toEqual(['npm', 'ci']);
      expect(options.cwd).toMatch(/frontend-app$/);
      return {
        error: undefined,
        outputTruncated: false,
        status: 0,
        stderr: '',
        stdout: '',
        timedOut: false,
      };
    });
    const target = subjectTarget();
    const metric = await collectDetachedStopFeedbackBudget({
      subjectSha: SUBJECT_SHA,
      subjectTree: SUBJECT_TREE,
      repositoryRoot: '/repository',
      execute,
      runCommand,
      loadTarget: vi.fn(async (options) => {
        expect(options).toEqual(expect.objectContaining({ subjectSha: SUBJECT_SHA, subjectTree: SUBJECT_TREE }));
        return target;
      }),
      runBenchmark: vi.fn(async ({ subjectSha, target: loadedTarget }) => {
        expect(loadedTarget).toBe(target);
        return {
          metricId: 'P03-feedback-budget',
          subjectFeedbackComponent: { path: P03_SUBJECT_FEEDBACK_COMPONENT_PATH, source: 'subject' },
          subjectSha,
          subjectRuntime: loadedTarget.provenance,
        };
      }),
    });
    expect(metric.subjectRuntime).toEqual(expect.objectContaining({
      subjectSha: SUBJECT_SHA,
      subjectTree: SUBJECT_TREE,
      installArgv: ['npm', 'ci'],
      worktreeClean: true,
      worktreeStatus: [],
    }));
    expect(commands.map((command) => command.slice(0, 3))).toEqual([
      ['git', 'worktree', 'add'],
      ['git', 'rev-parse', 'HEAD'],
      ['git', 'rev-parse', 'HEAD^{tree}'],
      ['git', 'status', '--porcelain'],
      ['npm', 'ci'],
      ['git', 'status', '--porcelain'],
      ['git', 'worktree', 'remove'],
    ]);
  });

  it('removes the detached worktree and temporary directory when Git identity differs from the requested subject', async () => {
    const commands = [];
    let temporaryRoot = '';
    const mismatchedSha = '9'.repeat(40);
    const execute = (command, args) => {
      commands.push([command, ...args]);
      if (command === 'git' && args[0] === 'worktree' && args[1] === 'add') {
        temporaryRoot = args[3];
        mkdirSync(join(temporaryRoot, 'frontend-app'), { recursive: true });
        return '';
      }
      if (command === 'git' && args.join(' ') === 'rev-parse HEAD') return mismatchedSha;
      if (command === 'git' && args.join(' ') === 'rev-parse HEAD^{tree}') return SUBJECT_TREE;
      if (command === 'git' && args.join(' ') === 'status --porcelain --untracked-files=all') return '';
      if (command === 'git' && args[0] === 'worktree' && args[1] === 'remove') return '';
      throw new Error(`unexpected command: ${command} ${args.join(' ')}`);
    };
    await expect(collectDetachedStopFeedbackBudget({
      subjectSha: SUBJECT_SHA,
      subjectTree: SUBJECT_TREE,
      repositoryRoot: '/repository',
      execute,
    })).rejects.toThrow(/Git identity/);
    expect(commands).toContainEqual(['git', 'worktree', 'remove', '--force', temporaryRoot]);
    expect(existsSync(temporaryRoot)).toBe(false);
  });

  it('cleans up when npm ci, target loading, or benchmark execution fails', async () => {
    for (const stage of ['npm ci', 'target load', 'benchmark']) {
      const commands = [];
      let temporaryRoot = '';
      const execute = (command, args) => {
        commands.push([command, ...args]);
        if (command === 'git' && args[0] === 'worktree' && args[1] === 'add') {
          temporaryRoot = args[3];
          createP03SubjectClosure(temporaryRoot);
          return '';
        }
        if (command === 'git' && args.join(' ') === 'rev-parse HEAD') return SUBJECT_SHA;
        if (command === 'git' && args.join(' ') === 'rev-parse HEAD^{tree}') return SUBJECT_TREE;
        if (command === 'git' && args.join(' ') === 'status --porcelain --untracked-files=all') return '';
        if (command === 'git' && args[0] === 'worktree' && args[1] === 'remove') return '';
        throw new Error(`unexpected command: ${command} ${args.join(' ')}`);
      };
      const runCommand = async () => (stage === 'npm ci'
        ? {
          error: new Error(stage),
          outputTruncated: false,
          status: null,
          stderr: '',
          stdout: '',
          timedOut: false,
        }
        : {
          error: undefined,
          outputTruncated: false,
          status: 0,
          stderr: '',
          stdout: '',
          timedOut: false,
        });
      await expect(collectDetachedStopFeedbackBudget({
        subjectSha: SUBJECT_SHA,
        subjectTree: SUBJECT_TREE,
        repositoryRoot: '/repository',
        execute,
        runCommand,
        loadTarget: stage === 'target load' ? async () => { throw new Error(stage); } : async () => subjectTarget(),
        runBenchmark: stage === 'benchmark'
          ? async () => { throw new Error(stage); }
          : async ({ subjectSha, target }) => ({
            metricId: 'P03-feedback-budget', subjectSha, subjectRuntime: target.provenance,
            subjectFeedbackComponent: { path: P03_SUBJECT_FEEDBACK_COMPONENT_PATH, source: 'subject' },
          }),
      })).rejects.toThrow(stage);
      expect(commands).toContainEqual(['git', 'worktree', 'remove', '--force', temporaryRoot]);
      expect(existsSync(temporaryRoot)).toBe(false);
    }
  });

  it('fails closed when P03 target provenance is absent or bound to another subject tree', () => {
    const absent = evidence();
    delete absent.metrics['P03-feedback-budget'].subjectRuntime;
    expect(verifyPerformanceEvidence(absent, baseline()).verdicts
      .find(({ metricId }) => metricId === 'P03-feedback-budget'))
      .toEqual(expect.objectContaining({ status: 'NOT_VERIFIED', reason: expect.stringMatching(/detached subject runtime/) }));

    const wrongTree = evidence();
    wrongTree.metrics['P03-feedback-budget'].subjectRuntime.subjectTree = '9'.repeat(40);
    expect(verifyPerformanceEvidence(wrongTree, baseline()).verdicts
      .find(({ metricId }) => metricId === 'P03-feedback-budget').status).toBe('NOT_VERIFIED');

    const missingSubjectComponent = evidence();
    delete missingSubjectComponent.metrics['P03-feedback-budget'].subjectFeedbackComponent;
    expect(verifyPerformanceEvidence(missingSubjectComponent, baseline()).verdicts
      .find(({ metricId }) => metricId === 'P03-feedback-budget').status).toBe('NOT_VERIFIED');
  });

  it('requires the exact ordered P03 production closure with no missing, extra, duplicated, or reordered paths', () => {
    const current = evidence();
    const runtime = current.metrics['P03-feedback-budget'];
    expect(() => validateP03SubjectRuntime(runtime, SUBJECT_SHA, SUBJECT_TREE)).not.toThrow();

    const mutations = [
      (files) => files.slice(1),
      (files) => [...files, { path: 'frontend-app/src/untracked.js', sha256: '9'.repeat(64) }],
      (files) => [...files].reverse(),
      (files) => files.map((file, index) => (index === 1 ? { ...file, path: files[0].path } : file)),
    ];
    for (const mutate of mutations) {
      const evidenceWithForgedClosure = evidence();
      const forged = evidenceWithForgedClosure.metrics['P03-feedback-budget'];
      forged.subjectRuntime.content.files = mutate(forged.subjectRuntime.content.files);
      expect(() => validateP03SubjectRuntime(forged, SUBJECT_SHA, SUBJECT_TREE))
        .toThrow(/incomplete production closure/);
    }
  });

  it('rejects a tampered P03 production file hash or aggregate hash', () => {
    const mutations = [
      (runtime) => { runtime.subjectRuntime.content.files[0].sha256 = 'f'.repeat(64); },
      (runtime) => { runtime.subjectRuntime.content.contentHash = 'e'.repeat(64); },
    ];
    for (const mutate of mutations) {
      const forged = evidence();
      mutate(forged.metrics['P03-feedback-budget']);
      expect(() => validateP03SubjectRuntime(
        forged.metrics['P03-feedback-budget'], SUBJECT_SHA, SUBJECT_TREE,
      )).toThrow(/content hash mismatch/);
      expect(verifyPerformanceEvidence(forged, baseline()).verdicts
        .find(({ metricId }) => metricId === 'P03-feedback-budget'))
        .toEqual(expect.objectContaining({ status: 'NOT_VERIFIED' }));
    }
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

  it('builds the exact frozen artifact with conservative timing cases without applying the P01 candidate limit', () => {
    const runs = freezeRuns();
    const slowP02Case = pairedTimingCase(1.09);
    const slowP03Case = timingCase(112, FEEDBACK_DURATION_CLOCK);
    runs[2].metrics['P02-history-budget'].cases['turns-200-tools-1'] = slowP02Case;
    runs[1].metrics['P03-feedback-budget'].cases['stop-visible-feedback'] = slowP03Case;
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
          cases: {
            ...runs[0].metrics['P02-history-budget'].cases,
            'turns-200-tools-1': slowP02Case,
          },
          status: 'PASS',
          maxRegressionRatio: 1.15,
        },
        'P03-feedback-budget': {
          ...runs[0].metrics['P03-feedback-budget'],
          cases: {
            ...runs[0].metrics['P03-feedback-budget'].cases,
            'stop-visible-feedback': slowP03Case,
          },
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
    expect(() => buildFreezeArtifact(overloaded))
      .toThrow('freeze run 3 loadAverage[0] differs beyond 4: left=20, right=1, delta=19');

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
