import { execFileSync } from 'node:child_process';
import { readdirSync, statSync } from 'node:fs';
import { join, relative, resolve } from 'node:path';
import process from 'node:process';
import {
  evaluateResourceBudget,
  median,
} from './performance-budget-model.mjs';

const HEAP_SAMPLE_COUNT = 5;
const HEAP_WARMUP_COUNT = 1;
const HEAP_MEASUREMENT_CLOCK = 'median(node --expose-gc process.memoryUsage().heapUsed after runtime-asset materialization and post-materialization global.gc())';
const RUNTIME_ASSET_PATTERN = /\.(?:css|html|js|mjs)$/;
const HEAP_PROBE_SOURCE = `
import { readFileSync } from 'node:fs';

const request = JSON.parse(process.env.FRONTEND_HEAP_PROBE_REQUEST || '');
if (!Array.isArray(request.runtimeAssetPaths) || request.runtimeAssetPaths.length === 0) {
  throw new Error('runtime asset paths are required');
}
if (typeof global.gc !== 'function') throw new Error('V8 garbage collector is unavailable');
global.gc();
const before = process.memoryUsage().heapUsed;
const runtimeAssets = request.runtimeAssetPaths.map((path) => {
  const source = readFileSync(path, 'utf8');
  return Object.freeze({ source, tokens: Object.freeze(source.match(/[A-Za-z_$][\\w$]*/g) || []) });
});
global.gc();
const after = process.memoryUsage().heapUsed;
if (!Number.isSafeInteger(after - before) || after - before <= 0) {
  throw new Error('V8 heapUsed delta must be a positive safe integer');
}
process.stdout.write(JSON.stringify({
  heapUsedBytes: after - before,
  environment: {
    node: process.version,
    v8: process.versions.v8,
    platform: process.platform,
    arch: process.arch,
  },
  runtimeAssetCount: runtimeAssets.length,
}));
`;

function collectFiles(root, directory = root) {
  return readdirSync(directory, { withFileTypes: true })
    .flatMap((entry) => {
      const path = join(directory, entry.name);
      return entry.isDirectory() ? collectFiles(root, path) : [{ path, relativePath: relative(root, path) }];
    });
}

function heapEnvironmentIsValid(environment) {
  return environment && typeof environment === 'object'
    && ['node', 'v8', 'platform', 'arch'].every((key) => (
      typeof environment[key] === 'string' && environment[key].length > 0
    ));
}

function measureV8HeapUsed({ runtimeAssetPaths }) {
  const output = execFileSync(process.execPath, ['--expose-gc', '--input-type=module', '--eval', HEAP_PROBE_SOURCE], {
    encoding: 'utf8',
    env: {
      ...process.env,
      FRONTEND_HEAP_PROBE_REQUEST: JSON.stringify({ runtimeAssetPaths }),
    },
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  const result = JSON.parse(output);
  if (!Number.isSafeInteger(result?.heapUsedBytes) || result.heapUsedBytes <= 0
    || !heapEnvironmentIsValid(result.environment)
    || result.runtimeAssetCount !== runtimeAssetPaths.length) {
    throw new Error('V8 heap probe returned invalid evidence');
  }
  return Object.freeze(result);
}

function validateV8HeapEvidence(metric, label = 'P04') {
  if (metric?.heapMeasurementClock !== HEAP_MEASUREMENT_CLOCK
    || metric.heapSampleCount !== HEAP_SAMPLE_COUNT
    || metric.heapWarmupCount !== HEAP_WARMUP_COUNT
    || !Array.isArray(metric.heapUsedSamplesBytes)
    || metric.heapUsedSamplesBytes.length !== HEAP_SAMPLE_COUNT
    || !heapEnvironmentIsValid(metric.heapEnvironment)) {
    throw new TypeError(`${label} V8 heap evidence shape is invalid`);
  }
  const computedMedian = median(metric.heapUsedSamplesBytes, `${label} V8 heap samples`);
  if (!Number.isSafeInteger(metric.heapUsedMedianBytes)
    || metric.heapUsedMedianBytes <= 0
    || computedMedian !== metric.heapUsedMedianBytes) {
    throw new Error(`${label} V8 heap median is not reproducible from raw samples`);
  }
  return Object.freeze({
    environment: Object.freeze({ ...metric.heapEnvironment }),
    medianBytes: metric.heapUsedMedianBytes,
    samplesBytes: Object.freeze([...metric.heapUsedSamplesBytes]),
  });
}

function collectV8HeapEvidence({ runtimeAssetPaths, heapSampler = measureV8HeapUsed }) {
  const warmup = heapSampler({ runtimeAssetPaths });
  if (!Number.isSafeInteger(warmup?.heapUsedBytes) || warmup.heapUsedBytes <= 0
    || !heapEnvironmentIsValid(warmup.environment)) {
    throw new Error('V8 heap warmup returned invalid evidence');
  }
  const samples = Array.from({ length: HEAP_SAMPLE_COUNT }, () => heapSampler({ runtimeAssetPaths }));
  const environment = warmup.environment;
  samples.forEach((sample, index) => {
    if (!Number.isSafeInteger(sample?.heapUsedBytes) || sample.heapUsedBytes <= 0
      || JSON.stringify(sample.environment) !== JSON.stringify(environment)) {
      throw new Error(`V8 heap sample ${index} is invalid or changed environment`);
    }
  });
  const heapUsedSamplesBytes = samples.map(({ heapUsedBytes }) => heapUsedBytes);
  return Object.freeze({
    heapMeasurementClock: HEAP_MEASUREMENT_CLOCK,
    heapSampleCount: HEAP_SAMPLE_COUNT,
    heapWarmupCount: HEAP_WARMUP_COUNT,
    heapUsedSamplesBytes: Object.freeze(heapUsedSamplesBytes),
    heapUsedMedianBytes: median(heapUsedSamplesBytes, 'P04 V8 heap samples'),
    heapEnvironment: Object.freeze({ ...environment }),
  });
}

function measureFrontendResources({
  distDir = resolve('dist'),
  subjectSha,
  heapSampler,
} = {}) {
  const indexPath = join(distDir, 'index.html');
  if (!statSync(indexPath).isFile()) throw new Error(`frontend dist index is missing: ${indexPath}`);
  const files = collectFiles(distDir);
  if (files.length === 0) throw new Error('frontend dist contains zero files');
  const measured = files.map(({ path, relativePath }) => ({
    path: relativePath,
    bytes: statSync(path).size,
  }));
  measured.forEach(({ bytes, path }) => {
    if (!Number.isSafeInteger(bytes) || bytes <= 0) throw new Error(`resource ${path} has invalid size ${bytes}`);
  });
  const runtimeAssetPaths = files
    .filter(({ relativePath }) => RUNTIME_ASSET_PATTERN.test(relativePath))
    .map(({ path }) => path)
    .sort((left, right) => left.localeCompare(right));
  if (runtimeAssetPaths.length === 0) throw new Error('frontend dist contains zero runtime heap assets');
  const heapEvidence = collectV8HeapEvidence({ runtimeAssetPaths, heapSampler });
  return Object.freeze({
    metricId: 'P04-resource-budget',
    subjectSha,
    fileCount: measured.length,
    totalBundleBytes: measured.reduce((total, { bytes }) => total + bytes, 0),
    maxChunkBytes: Math.max(...measured.map(({ bytes }) => bytes)),
    files: Object.freeze(measured.sort((left, right) => left.path.localeCompare(right.path))),
    ...heapEvidence,
  });
}

function verifyResourceEvidence(evidence, baseline) {
  const baselineMetric = baseline?.metrics?.['P04-resource-budget'];
  const currentHeap = validateV8HeapEvidence(evidence, 'P04 candidate');
  const baselineHeap = validateV8HeapEvidence(baselineMetric, 'P04 baseline');
  if (JSON.stringify(currentHeap.environment) !== JSON.stringify(baselineHeap.environment)) {
    return Object.freeze({
      metricId: 'P04-resource-budget',
      status: 'NOT_VERIFIED',
      reason: 'P04 V8 heap environment differs from frozen baseline',
    });
  }
  return evaluateResourceBudget(evidence, baselineMetric);
}

export {
  HEAP_MEASUREMENT_CLOCK,
  HEAP_SAMPLE_COUNT,
  HEAP_WARMUP_COUNT,
  measureFrontendResources,
  validateV8HeapEvidence,
  verifyResourceEvidence,
};
