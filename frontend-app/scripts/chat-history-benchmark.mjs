import { execFileSync, spawnSync } from 'node:child_process';
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
  measurementMedian,
  median,
  requireSubjectSha,
} from './performance-budget-model.mjs';
import { DEFAULT_BASELINE_PATH } from './performance-budget-config.mjs';
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
const HISTORY_BLOCK_COUNT = 9;
const HISTORY_BLOCK_ITERATIONS = 500_000;
const MEASUREMENT_ITERATIONS = HISTORY_BLOCK_COUNT * HISTORY_BLOCK_ITERATIONS;

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
      let materializedCount = 0;
      const blockDurationsMs = [];
      for (let block = 0; block < HISTORY_BLOCK_COUNT; block += 1) {
        const startedAt = process.cpuUsage();
        for (let index = 0; index < HISTORY_BLOCK_ITERATIONS; index += 1) {
          materializedCount += selectMaterializedTimeline(
            history,
            TIMELINE_INITIAL_MATERIALIZED_MESSAGES,
          ).length;
        }
        const cpuUsage = process.cpuUsage(startedAt);
        blockDurationsMs.push((cpuUsage.user + cpuUsage.system) / 1_000);
      }
      const expectedCount = TIMELINE_INITIAL_MATERIALIZED_MESSAGES * MEASUREMENT_ITERATIONS;
      if (materializedCount !== expectedCount) {
        throw new Error(`${caseName} materialized ${materializedCount}, expected ${expectedCount}`);
      }
      return Object.freeze({
        blockDurationsMs: Object.freeze(blockDurationsMs),
        durationMs: measurementMedian(blockDurationsMs, `${caseName}.blockDurationsMs`),
      });
    };
    for (let index = 0; index < warmupCount; index += 1) {
      measureBatch();
    }
    const sampleDiagnostics = Array.from({ length: sampleCount }, measureBatch);
    const durationSamplesMs = sampleDiagnostics.map(({ durationMs }) => durationMs);
    const durationAttemptSamplesMs = durationSamplesMs.map((durationMs) => Object.freeze([durationMs]));
    measuredCases[caseName] = Object.freeze({
      turns,
      toolsPerTurn,
      attemptsPerSample: ATTEMPTS_PER_SAMPLE,
      durationClock: CPU_DURATION_CLOCK,
      blockCount: HISTORY_BLOCK_COUNT,
      blockIterationCount: HISTORY_BLOCK_ITERATIONS,
      iterationCount: MEASUREMENT_ITERATIONS,
      materializedCount: TIMELINE_INITIAL_MATERIALIZED_MESSAGES,
      rawSamplesMs: Object.freeze(durationSamplesMs),
      sampleDiagnostics: Object.freeze(sampleDiagnostics),
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
    baselinePath: DEFAULT_BASELINE_PATH,
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
      const result = spawnSync(process.execPath, [
        resolve(dirname(SCRIPT_PATH), 'performance-budget-runner.mjs'),
        '--verify',
        '--subject', options.subject,
        '--baseline', options.baselinePath,
      ], {
        cwd: REPOSITORY_ROOT,
        encoding: 'utf8',
        stdio: ['ignore', 'pipe', 'pipe'],
      });
      if (result.error) throw result.error;
      if (result.stdout) process.stdout.write(result.stdout);
      if (result.stderr) process.stderr.write(result.stderr);
      if (!Number.isInteger(result.status)) throw new Error(`performance runner terminated by ${result.signal || 'unknown signal'}`);
      process.exitCode = result.status;
    }
  } catch (error) {
    process.stderr.write(`chat history benchmark failed: ${error.message}\n`);
    process.exitCode = 1;
  }
}

export {
  ATTEMPTS_PER_SAMPLE,
  DEFAULT_BASELINE_PATH,
  HISTORY_BLOCK_COUNT,
  HISTORY_BLOCK_ITERATIONS,
  MEASUREMENT_ITERATIONS,
  buildChatHistoryBenchmarkCases,
  measureChatHistoryCase,
  runChatHistoryBenchmark,
  runChatHistoryBenchmarkSamples,
  verifyChatHistoryEvidence,
};
