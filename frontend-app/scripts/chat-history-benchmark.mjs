import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';
import { performance } from 'node:perf_hooks';
import process from 'node:process';
import {
  ATTEMPTS_PER_SAMPLE,
  CPU_DURATION_CLOCK,
  HISTORY_REGRESSION_RATIO,
  SAMPLE_COUNT,
  WARMUP_COUNT,
  evaluateMedianCases,
  median,
  requireSubjectSha,
} from './performance-budget-model.mjs';
import { buildChatHistoryFixture } from '../src/pages/chat/model/chatHistoryBenchmarkFixture.js';
import {
  TIMELINE_INITIAL_MATERIALIZED_MESSAGES,
  selectMaterializedTimeline,
} from '../src/pages/chat/model/timelineMaterializationModel.js';

const SCRIPT_PATH = fileURLToPath(import.meta.url);
const REPOSITORY_ROOT = resolve(dirname(SCRIPT_PATH), '..', '..');
const DEFAULT_TURNS = Object.freeze([200, 1_000, 5_000]);
const EXTENDED_TURNS = 10_000;
const TOOL_COUNTS = Object.freeze([1, 3]);
const BASELINE_PATH = resolve(dirname(SCRIPT_PATH), 'frontend-maintainability-baseline.json');
const MEASUREMENT_ITERATIONS = 100_000;

function buildChatHistoryBenchmarkCases({ extended }) {
  if (typeof extended !== 'boolean') throw new TypeError('extended must be a boolean');
  const turns = extended ? [...DEFAULT_TURNS, EXTENDED_TURNS] : DEFAULT_TURNS;
  return Object.freeze(turns.flatMap((turnCount) => TOOL_COUNTS.map((toolsPerTurn) => Object.freeze({
    turns: turnCount,
    toolsPerTurn,
  }))));
}

function requireMeasurementMetadata({ caseName, turns, toolsPerTurn, node, commit }) {
  if (typeof caseName !== 'string' || caseName.length === 0) throw new TypeError('caseName is required');
  if (!Number.isSafeInteger(turns) || turns <= 0) throw new TypeError('turns must be a positive integer');
  if (!Number.isSafeInteger(toolsPerTurn) || toolsPerTurn <= 0) {
    throw new TypeError('toolsPerTurn must be a positive integer');
  }
  if (typeof node !== 'string' || node.length === 0) throw new TypeError('node is required');
  if (typeof commit !== 'string' || commit.length === 0) throw new TypeError('commit is required');
}

function measureChatHistoryCase(history, metadata) {
  requireMeasurementMetadata(metadata);
  const heapBefore = process.memoryUsage().heapUsed;
  const startedAt = performance.now();
  const materialized = selectMaterializedTimeline(history, TIMELINE_INITIAL_MATERIALIZED_MESSAGES);
  const durationMs = performance.now() - startedAt;
  const heapDeltaBytes = process.memoryUsage().heapUsed - heapBefore;
  return Object.freeze({
    case: metadata.caseName,
    turns: metadata.turns,
    toolsPerTurn: metadata.toolsPerTurn,
    materializedCount: materialized.length,
    durationMs,
    heapDeltaBytes,
    node: metadata.node,
    commit: metadata.commit,
  });
}

function currentCommit() {
  return execFileSync('git', ['rev-parse', 'HEAD'], {
    cwd: REPOSITORY_ROOT,
    encoding: 'utf8',
    stdio: ['ignore', 'pipe', 'pipe'],
  }).trim();
}

function runChatHistoryBenchmark({ extended, commit = currentCommit() }) {
  return buildChatHistoryBenchmarkCases({ extended }).map(({ turns, toolsPerTurn }) => {
    const history = buildChatHistoryFixture({ archived: true, seed: 7, toolsPerTurn, turns });
    return measureChatHistoryCase(history, {
      caseName: `turns-${turns}-tools-${toolsPerTurn}`,
      turns,
      toolsPerTurn,
      node: process.version,
      commit,
    });
  });
}

function runChatHistoryBenchmarkSamples({
  extended = false,
  commit = currentCommit(),
  sampleCount = SAMPLE_COUNT,
  warmupCount = WARMUP_COUNT,
} = {}) {
  if (sampleCount !== SAMPLE_COUNT) throw new TypeError(`sampleCount must be ${SAMPLE_COUNT}`);
  if (warmupCount !== WARMUP_COUNT) throw new TypeError(`warmupCount must be ${WARMUP_COUNT}`);
  const cases = buildChatHistoryBenchmarkCases({ extended });
  const measuredCases = {};
  for (const { turns, toolsPerTurn } of cases) {
    const caseName = `turns-${turns}-tools-${toolsPerTurn}`;
    const history = buildChatHistoryFixture({ archived: true, seed: 7, toolsPerTurn, turns });
    const measureBatch = () => {
      const startedAt = process.cpuUsage();
      let materializedCount = 0;
      for (let index = 0; index < MEASUREMENT_ITERATIONS; index += 1) {
        materializedCount += selectMaterializedTimeline(
          history,
          TIMELINE_INITIAL_MATERIALIZED_MESSAGES,
        ).length;
      }
      const cpuUsage = process.cpuUsage(startedAt);
      const durationMs = (cpuUsage.user + cpuUsage.system) / 1_000;
      const expectedCount = TIMELINE_INITIAL_MATERIALIZED_MESSAGES * MEASUREMENT_ITERATIONS;
      if (materializedCount !== expectedCount) {
        throw new Error(`${caseName} materialized ${materializedCount}, expected ${expectedCount}`);
      }
      return durationMs;
    };
    for (let index = 0; index < warmupCount; index += 1) {
      measureBatch();
    }
    const durationAttemptSamplesMs = Array.from(
      { length: sampleCount },
      () => Array.from({ length: ATTEMPTS_PER_SAMPLE }, measureBatch),
    );
    const durationSamplesMs = durationAttemptSamplesMs.map((attempts) => Math.min(...attempts));
    measuredCases[caseName] = Object.freeze({
      turns,
      toolsPerTurn,
      attemptsPerSample: ATTEMPTS_PER_SAMPLE,
      durationClock: CPU_DURATION_CLOCK,
      iterationCount: MEASUREMENT_ITERATIONS,
      materializedCount: TIMELINE_INITIAL_MATERIALIZED_MESSAGES,
      durationAttemptSamplesMs,
      durationSamplesMs,
      durationMedianMs: median(durationSamplesMs, `${caseName}.durationSamplesMs`),
    });
  }
  return Object.freeze({
    metricId: 'P02-history-budget',
    subjectSha: commit,
    node: process.version,
    warmupCount,
    sampleCount,
    cases: Object.freeze(measuredCases),
  });
}

function verifyChatHistoryEvidence(evidence, baseline) {
  return evaluateMedianCases({
    baselineMetric: baseline?.metrics?.['P02-history-budget'],
    currentMetric: evidence,
    durationClock: CPU_DURATION_CLOCK,
    metricId: 'P02-history-budget',
    ratioKey: 'maxRegressionRatio',
    requiredRatio: HISTORY_REGRESSION_RATIO,
    valueKey: 'durationMedianMs',
  });
}

function parseArguments(args) {
  const options = {
    extended: false,
    verify: false,
    subject: currentCommit(),
    baselinePath: BASELINE_PATH,
  };
  for (let index = 0; index < args.length; index += 1) {
    const arg = args[index];
    if (arg === '--extended') options.extended = true;
    else if (arg === '--verify') options.verify = true;
    else if (arg === '--subject') options.subject = args[++index] || '';
    else if (arg === '--baseline') options.baselinePath = resolve(args[++index] || '');
    else throw new TypeError(`unsupported chat history benchmark argument: ${arg}`);
  }
  if (options.verify) requireSubjectSha(options.subject, currentCommit());
  return options;
}

if (process.argv[1] && resolve(process.argv[1]) === SCRIPT_PATH) {
  try {
    const options = parseArguments(process.argv.slice(2));
    if (!options.verify) {
      process.stdout.write(`${JSON.stringify(runChatHistoryBenchmark({ extended: options.extended }))}\n`);
    } else {
      if (options.extended) {
        throw new TypeError('--extended is not part of the frozen --verify case registry');
      }
      const { runPerformanceVerification } = await import('./performance-budget-runner.mjs');
      const report = await runPerformanceVerification({
        baselinePath: options.baselinePath,
        subjectSha: options.subject,
      });
      process.stdout.write(`${JSON.stringify(report)}\n`);
      if (report.verdict.status !== 'PASS') process.exitCode = 2;
    }
  } catch (error) {
    process.stderr.write(`chat history benchmark failed: ${error.message}\n`);
    process.exitCode = 1;
  }
}

export {
  ATTEMPTS_PER_SAMPLE,
  MEASUREMENT_ITERATIONS,
  buildChatHistoryBenchmarkCases,
  measureChatHistoryCase,
  runChatHistoryBenchmark,
  runChatHistoryBenchmarkSamples,
  verifyChatHistoryEvidence,
};
