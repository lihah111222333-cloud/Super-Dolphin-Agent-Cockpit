import { execFileSync, spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { existsSync, readFileSync } from 'node:fs';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { dirname, resolve } from 'node:path';
import { performance } from 'node:perf_hooks';
import process from 'node:process';
import {
  ATTEMPTS_PER_SAMPLE,
  CPU_DURATION_CLOCK,
  HISTORY_REGRESSION_RATIO,
  SAMPLE_COUNT,
  WARMUP_COUNT,
  evaluatePairedMedianCases,
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
const P02_FIXTURE_PATH = 'frontend-app/src/pages/chat/model/chatHistoryBenchmarkFixture.js';
const P02_TIMELINE_PATH = 'frontend-app/src/pages/chat/model/timelineMaterializationModel.js';
const P02_SUBJECT_CONTENT_PATHS = Object.freeze([
  P02_FIXTURE_PATH,
  P02_TIMELINE_PATH,
]);

function requireFullSha(value, label) {
  if (!/^[0-9a-f]{40}$/.test(value || '')) {
    throw new TypeError(`${label} must be a full 40-character Git SHA`);
  }
  return value;
}

function subjectContentEvidence(subjectRoot) {
  const files = P02_SUBJECT_CONTENT_PATHS.map((path) => {
    const filePath = resolve(subjectRoot, path);
    if (!existsSync(filePath)) throw new Error(`P02 detached subject is missing ${path}`);
    return Object.freeze({
      path,
      sha256: createHash('sha256').update(readFileSync(filePath)).digest('hex'),
    });
  });
  const contentHash = createHash('sha256').update(files.map(({ path, sha256 }) => `${path}\0${sha256}\n`).join('')).digest('hex');
  return Object.freeze({ contentHash, files: Object.freeze(files) });
}

async function importSubjectModule(subjectRoot, path, label) {
  const filePath = resolve(subjectRoot, path);
  if (!existsSync(filePath)) throw new Error(`P02 detached subject is missing ${label}: ${path}`);
  try {
    return await import(/* @vite-ignore */ pathToFileURL(filePath).href);
  } catch (error) {
    throw new Error(`P02 detached subject ${label} cannot be imported: ${path}`, { cause: error });
  }
}

async function loadChatHistoryBenchmarkTarget({ subjectRoot, subjectSha, subjectTree } = {}) {
  if (typeof subjectRoot !== 'string' || subjectRoot.length === 0) throw new TypeError('P02 subjectRoot is required');
  requireFullSha(subjectSha, 'P02 subjectSha');
  requireFullSha(subjectTree, 'P02 subjectTree');
  const root = resolve(subjectRoot);
  const [fixtureModule, timelineModule] = await Promise.all([
    importSubjectModule(root, P02_FIXTURE_PATH, 'fixture'),
    importSubjectModule(root, P02_TIMELINE_PATH, 'timeline model'),
  ]);
  if (typeof fixtureModule.buildChatHistoryFixture !== 'function') {
    throw new Error(`P02 detached subject fixture does not export buildChatHistoryFixture: ${P02_FIXTURE_PATH}`);
  }
  if (typeof timelineModule.selectMaterializedTimeline !== 'function'
    || !Number.isSafeInteger(timelineModule.TIMELINE_INITIAL_MATERIALIZED_MESSAGES)
    || timelineModule.TIMELINE_INITIAL_MATERIALIZED_MESSAGES <= 0) {
    throw new Error(`P02 detached subject timeline model has an invalid materialization contract: ${P02_TIMELINE_PATH}`);
  }
  return Object.freeze({
    buildChatHistoryFixture: fixtureModule.buildChatHistoryFixture,
    selectMaterializedTimeline: timelineModule.selectMaterializedTimeline,
    timelineInitialMaterializedMessages: timelineModule.TIMELINE_INITIAL_MATERIALIZED_MESSAGES,
    provenance: Object.freeze({
      content: subjectContentEvidence(root),
      subjectSha,
      subjectTree,
    }),
  });
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
    throw new TypeError('P02 target requires fixture, timeline selector, and a positive materialization limit');
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
    provenance: subjectProduct,
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
    metricId: 'P02-history-budget',
    subjectSha: commit,
    ...(subjectProduct === undefined ? {} : { subjectProduct }),
    node: process.version,
    warmupCount,
    sampleCount,
    cases: Object.freeze(measuredCases),
  });
}

function verifyChatHistoryEvidence(evidence, baseline) {
  return evaluatePairedMedianCases({
    baselineMetric: baseline?.metrics?.['P02-history-budget'],
    currentMetric: evidence,
    durationClock: CPU_DURATION_CLOCK,
    metricId: 'P02-history-budget',
    ratioKey: 'maxRegressionRatio',
    requiredRatio: HISTORY_REGRESSION_RATIO,
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
  P02_SUBJECT_CONTENT_PATHS,
  buildChatHistoryBenchmarkCases,
  loadChatHistoryBenchmarkTarget,
  measureChatHistoryCase,
  runChatHistoryBenchmark,
  runChatHistoryBenchmarkSamples,
  verifyChatHistoryEvidence,
};
