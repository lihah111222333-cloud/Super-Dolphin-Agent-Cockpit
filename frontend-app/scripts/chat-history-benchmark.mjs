import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';
import { performance } from 'node:perf_hooks';
import process from 'node:process';
import { buildChatHistoryFixture } from '../src/pages/chat/model/chatHistoryBenchmarkFixture.js';
import {
  TIMELINE_INITIAL_MATERIALIZED_MESSAGES,
  selectMaterializedTimeline,
} from '../src/pages/chat/model/timelineMaterializationModel.js';

const SCRIPT_PATH = fileURLToPath(import.meta.url);
const REPOSITORY_ROOT = resolve(dirname(SCRIPT_PATH), '..', '..');
const SAMPLE_COUNT = 5;
const WARMUP_COUNT = 1;
const ATTEMPTS_PER_SAMPLE = 1;
const CPU_DURATION_CLOCK = 'p50(production/reference process.cpuUsage(user+system),alternating,500000-iteration-blocks)';
const DEFAULT_TURNS = Object.freeze([200, 1_000, 5_000]);
const EXTENDED_TURNS = 10_000;
const TOOL_COUNTS = Object.freeze([1, 3]);
const HISTORY_BLOCK_COUNT = 9;
const HISTORY_BLOCK_ITERATIONS = 500_000;
const MEASUREMENT_ITERATIONS = HISTORY_BLOCK_COUNT * HISTORY_BLOCK_ITERATIONS;

function requireFiniteNonNegative(value, label) {
  if (!Number.isFinite(value) || value < 0) throw new TypeError(`${label} must be a finite non-negative number`);
  return value;
}

function measurementMedian(values, label = 'measurements') {
  if (!Array.isArray(values) || values.length === 0) throw new TypeError(`${label} must be non-empty`);
  const sorted = values.map((value, index) => requireFiniteNonNegative(value, `${label}[${index}]`))
    .sort((left, right) => left - right);
  return sorted[Math.floor(sorted.length / 2)];
}

function median(values, label = 'samples') {
  if (!Array.isArray(values) || values.length !== SAMPLE_COUNT) {
    throw new TypeError(`${label} must contain exactly ${SAMPLE_COUNT} samples`);
  }
  return measurementMedian(values, label);
}

function resolveChatHistoryBenchmarkTarget(target) {
  if (target === undefined) {
    return Object.freeze({
      buildChatHistoryFixture,
      selectMaterializedTimeline,
      timelineInitialMaterializedMessages: TIMELINE_INITIAL_MATERIALIZED_MESSAGES,
    });
  }
  if (target == null || typeof target !== 'object'
    || typeof target.buildChatHistoryFixture !== 'function'
    || typeof target.selectMaterializedTimeline !== 'function'
    || !Number.isSafeInteger(target.timelineInitialMaterializedMessages)
    || target.timelineInitialMaterializedMessages <= 0) {
    throw new TypeError('chat history target requires fixture, timeline selector, and a positive materialization limit');
  }
  return target;
}

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

function measureChatHistoryCase(history, metadata, target) {
  requireMeasurementMetadata(metadata);
  const subjectTarget = resolveChatHistoryBenchmarkTarget(target);
  const {
    selectMaterializedTimeline: selectTimeline,
    timelineInitialMaterializedMessages: materializationLimit,
  } = subjectTarget;
  const heapBefore = process.memoryUsage().heapUsed;
  const startedAt = performance.now();
  const materialized = selectTimeline(history, materializationLimit);
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

function runChatHistoryBenchmark({ extended, commit = currentCommit(), target } = {}) {
  const subjectTarget = resolveChatHistoryBenchmarkTarget(target);
  const { buildChatHistoryFixture: buildFixture } = subjectTarget;
  return buildChatHistoryBenchmarkCases({ extended }).map(({ turns, toolsPerTurn }) => {
    const history = buildFixture({ archived: true, seed: 7, toolsPerTurn, turns });
    return measureChatHistoryCase(history, {
      caseName: `turns-${turns}-tools-${toolsPerTurn}`,
      turns,
      toolsPerTurn,
      node: process.version,
      commit,
    }, subjectTarget);
  });
}

function runChatHistoryBenchmarkSamples({
  extended = false,
  commit = currentCommit(),
  sampleCount = SAMPLE_COUNT,
  target,
  warmupCount = WARMUP_COUNT,
} = {}) {
  if (sampleCount !== SAMPLE_COUNT) throw new TypeError(`sampleCount must be ${SAMPLE_COUNT}`);
  if (warmupCount !== WARMUP_COUNT) throw new TypeError(`warmupCount must be ${WARMUP_COUNT}`);
  const subjectTarget = resolveChatHistoryBenchmarkTarget(target);
  const {
    buildChatHistoryFixture: buildFixture,
    selectMaterializedTimeline: selectTimeline,
    timelineInitialMaterializedMessages: materializationLimit,
  } = subjectTarget;
  const cases = buildChatHistoryBenchmarkCases({ extended });
  const measuredCases = {};
  for (const { turns, toolsPerTurn } of cases) {
    const caseName = `turns-${turns}-tools-${toolsPerTurn}`;
    const history = buildFixture({ archived: true, seed: 7, toolsPerTurn, turns });
    const expectedBlockMaterializedCount = materializationLimit * HISTORY_BLOCK_ITERATIONS;
    const measureBlock = (workload, label) => {
      let materializedCount = 0;
      const startedAt = process.cpuUsage();
      for (let index = 0; index < HISTORY_BLOCK_ITERATIONS; index += 1) {
        materializedCount += workload().length;
      }
      const cpuUsage = process.cpuUsage(startedAt);
      if (materializedCount !== expectedBlockMaterializedCount) {
        throw new Error(`${caseName} ${label} materialized ${materializedCount}, expected ${expectedBlockMaterializedCount}`);
      }
      const durationMs = (cpuUsage.user + cpuUsage.system) / 1_000;
      if (!Number.isFinite(durationMs) || durationMs <= 0) {
        throw new Error(`${caseName} ${label} CPU duration must be finite and positive`);
      }
      return durationMs;
    };
    const productionWorkload = () => selectTimeline(history, materializationLimit);
    const referenceWorkload = () => history.slice(-materializationLimit);
    const measureBatch = (sampleIndex) => {
      const blockOrders = [];
      const productionBlockCpuDurationsMs = [];
      const referenceBlockCpuDurationsMs = [];
      const rawNormalizedBlockRatios = [];
      for (let block = 0; block < HISTORY_BLOCK_COUNT; block += 1) {
        const productionFirst = (sampleIndex + block) % 2 === 0;
        const order = productionFirst ? 'production-reference' : 'reference-production';
        let productionDurationMs;
        let referenceDurationMs;
        if (productionFirst) {
          productionDurationMs = measureBlock(productionWorkload, `production block ${block}`);
          referenceDurationMs = measureBlock(referenceWorkload, `reference block ${block}`);
        } else {
          referenceDurationMs = measureBlock(referenceWorkload, `reference block ${block}`);
          productionDurationMs = measureBlock(productionWorkload, `production block ${block}`);
        }
        const normalizedRatio = productionDurationMs / referenceDurationMs;
        if (!Number.isFinite(normalizedRatio) || normalizedRatio <= 0) {
          throw new Error(`${caseName} block ${block} normalized ratio must be finite and positive`);
        }
        blockOrders.push(order);
        productionBlockCpuDurationsMs.push(productionDurationMs);
        referenceBlockCpuDurationsMs.push(referenceDurationMs);
        rawNormalizedBlockRatios.push(normalizedRatio);
      }
      return Object.freeze({
        blockOrders: Object.freeze(blockOrders),
        productionBlockCpuDurationsMs: Object.freeze(productionBlockCpuDurationsMs),
        referenceBlockCpuDurationsMs: Object.freeze(referenceBlockCpuDurationsMs),
        rawNormalizedBlockRatios: Object.freeze(rawNormalizedBlockRatios),
        normalizedRatio: measurementMedian(rawNormalizedBlockRatios, `${caseName}.rawNormalizedBlockRatios`),
      });
    };
    for (let index = 0; index < warmupCount; index += 1) {
      measureBatch(index - warmupCount);
    }
    const sampleDiagnostics = Array.from({ length: sampleCount }, (_, sampleIndex) => measureBatch(sampleIndex));
    const normalizedRatioSamples = sampleDiagnostics.map(({ normalizedRatio }) => normalizedRatio);
    measuredCases[caseName] = Object.freeze({
      turns,
      toolsPerTurn,
      attemptsPerSample: ATTEMPTS_PER_SAMPLE,
      durationClock: CPU_DURATION_CLOCK,
      blockCount: HISTORY_BLOCK_COUNT,
      blockIterationCount: HISTORY_BLOCK_ITERATIONS,
      iterationCount: MEASUREMENT_ITERATIONS,
      materializedCount: materializationLimit,
      referenceMaterializedCount: materializationLimit,
      sampleDiagnostics: Object.freeze(sampleDiagnostics),
      normalizedRatioSamples: Object.freeze(normalizedRatioSamples),
      normalizedRatioMedian: median(normalizedRatioSamples, `${caseName}.normalizedRatioSamples`),
    });
  }
  return Object.freeze({
    metricId: 'history-timing-diagnostic',
    subjectSha: commit,
    node: process.version,
    warmupCount,
    sampleCount,
    cases: Object.freeze(measuredCases),
  });
}

function parseArguments(args) {
  const options = {
    extended: false,
  };
  for (let index = 0; index < args.length; index += 1) {
    const arg = args[index];
    if (arg === '--extended') options.extended = true;
    else throw new TypeError(`unsupported chat history benchmark argument: ${arg}`);
  }
  return options;
}

if (process.argv[1] && resolve(process.argv[1]) === SCRIPT_PATH) {
  try {
    const options = parseArguments(process.argv.slice(2));
    process.stdout.write(`${JSON.stringify(runChatHistoryBenchmark({ extended: options.extended }))}\n`);
  } catch (error) {
    process.stderr.write(`chat history benchmark failed: ${error.message}\n`);
    process.exitCode = 1;
  }
}

export {
  ATTEMPTS_PER_SAMPLE,
  HISTORY_BLOCK_COUNT,
  HISTORY_BLOCK_ITERATIONS,
  MEASUREMENT_ITERATIONS,
  buildChatHistoryBenchmarkCases,
  measureChatHistoryCase,
  measurementMedian,
  median,
  runChatHistoryBenchmark,
  runChatHistoryBenchmarkSamples,
};
