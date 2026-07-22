import { createHash } from 'node:crypto';
import { mkdirSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
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

export const SUBJECT_SHA = 'a'.repeat(40);
export const SUBJECT_TREE = '1'.repeat(40);

export function p03SubjectContentFiles() {
  return P03_SUBJECT_CONTENT_PATHS.map((path, index) => ({
    path,
    sha256: String(index).repeat(64),
  }));
}

export function createP03SubjectClosure(root) {
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

export function p03SubjectContentHash(files) {
  return createHash('sha256')
    .update(files.map(({ path, sha256 }) => path + '\0' + sha256 + '\n').join(''))
    .digest('hex');
}

function p02SubjectContentHash(files) {
  return createHash('sha256')
    .update(files.map(({ path, sha256 }) => path + '\0' + sha256 + '\n').join(''))
    .digest('hex');
}

export function timingCase(durationMedianMs, durationClock = CPU_DURATION_CLOCK) {
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

export function pairedTimingCase(normalizedRatio) {
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

export function evidence(subjectSha = SUBJECT_SHA) {
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
            'assets/a.js\0' + 50 + '\0' + 'a'.repeat(64) + '\n',
            'assets/Chat.js\0' + 50 + '\0' + 'b'.repeat(64) + '\n',
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
            'assets/a.js\0' + 50 + '\0' + 'a'.repeat(64) + '\n',
            'assets/Chat.js\0' + 50 + '\0' + 'b'.repeat(64) + '\n',
          ].join('')).digest('hex'),
        },
      },
    },
  };
}

export function baseline() {
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
