import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { afterEach, expect, it } from 'vitest';
import { HEAP_MEASUREMENT_CLOCK, measureFrontendResources, verifyResourceEvidence } from './resource-budget.mjs';

const temporaryRoots = [];

function heapSampler(values, environment = {
  node: 'v25.6.1',
  v8: '14.1.0',
  platform: 'darwin',
  arch: 'arm64',
}) {
  let index = 0;
  return () => ({
    heapUsedBytes: values[index++],
    environment,
  });
}

function resourceFixture(sizes) {
  const root = mkdtempSync(join(tmpdir(), 'frontend-resource-budget-'));
  temporaryRoots.push(root);
  mkdirSync(join(root, 'assets'));
  writeFileSync(join(root, 'index.html'), 'x'.repeat(sizes.index));
  writeFileSync(join(root, 'assets', 'app.js'), 'x'.repeat(sizes.app));
  return root;
}

afterEach(() => {
  temporaryRoots.splice(0).forEach((root) => rmSync(root, { recursive: true, force: true }));
});

it('collects retained heap only after post-materialization garbage collection', () => {
  const source = readFileSync(resolve('scripts/resource-budget.mjs'), 'utf8');
  const firstGc = source.indexOf('global.gc();');
  const materialization = source.indexOf('const runtimeAssets = request.runtimeAssetPaths.map');
  const secondGc = source.indexOf('global.gc();', firstGc + 1);
  const afterMeasurement = source.indexOf('const after = process.memoryUsage().heapUsed;', materialization);
  expect(firstGc).toBeGreaterThanOrEqual(0);
  expect(materialization).toBeGreaterThan(firstGc);
  expect(secondGc).toBeGreaterThan(materialization);
  expect(afterMeasurement).toBeGreaterThan(secondGc);
  expect(source.indexOf('global.gc();', secondGc + 1)).toBe(-1);
  expect(HEAP_MEASUREMENT_CLOCK).toContain('post-materialization global.gc()');
});

it('collects non-zero bundle and maximum chunk evidence', () => {
  const evidence = measureFrontendResources({
    distDir: resourceFixture({ index: 10, app: 90 }),
    subjectSha: 'a'.repeat(40),
    heapSampler: heapSampler([50, 100, 120, 140, 160, 180]),
  });
  expect(evidence).toEqual(expect.objectContaining({
    fileCount: 2,
    maxChunkBytes: 90,
    totalBundleBytes: 100,
    heapUsedSamplesBytes: [100, 120, 140, 160, 180],
    heapUsedMedianBytes: 140,
  }));
});

it('fails a deliberately enlarged bundle', () => {
  const evidence = measureFrontendResources({
    distDir: resourceFixture({ index: 10, app: 96 }),
    subjectSha: 'a'.repeat(40),
    heapSampler: heapSampler([50, 100, 100, 100, 100, 100]),
  });
  const verdict = verifyResourceEvidence(evidence, {
    metrics: {
      'P04-resource-budget': {
        status: 'PASS',
        subjectSha: 'b'.repeat(40),
        sampleCount: 5,
        warmupCount: 1,
        maxRegressionRatio: 1.05,
        totalBundleBytes: 100,
        maxChunkBytes: 90,
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
  });
  expect(verdict.status).toBe('FAIL');
  expect(verdict.comparisons.map(({ case: caseName }) => caseName)).toEqual([
    'totalBundleBytes',
    'maxChunkBytes',
    'heapUsedMedianBytes',
  ]);
});

it('rejects zero-byte resource evidence', () => {
  const root = resourceFixture({ index: 1, app: 1 });
  writeFileSync(join(root, 'assets', 'app.js'), '');
  expect(() => measureFrontendResources({ distDir: root, subjectSha: 'a'.repeat(40) }))
    .toThrow(/invalid size 0/);
});

it('fails a deliberately enlarged V8 heap median and rejects forged raw samples', () => {
  const evidence = measureFrontendResources({
    distDir: resourceFixture({ index: 10, app: 90 }),
    subjectSha: 'a'.repeat(40),
    heapSampler: heapSampler([50, 120, 120, 120, 120, 120]),
  });
  const baseline = {
    metrics: {
      'P04-resource-budget': {
        status: 'PASS',
        subjectSha: 'b'.repeat(40),
        maxRegressionRatio: 1.05,
        totalBundleBytes: 100,
        maxChunkBytes: 90,
        heapMeasurementClock: evidence.heapMeasurementClock,
        heapSampleCount: 5,
        heapWarmupCount: 1,
        heapUsedSamplesBytes: [100, 100, 100, 100, 100],
        heapUsedMedianBytes: 100,
        heapEnvironment: evidence.heapEnvironment,
      },
    },
  };
  expect(verifyResourceEvidence(evidence, baseline)).toEqual(expect.objectContaining({ status: 'FAIL' }));

  const forged = { ...evidence, heapUsedMedianBytes: 1 };
  expect(() => verifyResourceEvidence(forged, baseline)).toThrow(/median is not reproducible/);

  const differentRuntime = structuredClone(baseline);
  differentRuntime.metrics['P04-resource-budget'].heapEnvironment.platform = 'linux';
  expect(verifyResourceEvidence(evidence, differentRuntime))
    .toEqual(expect.objectContaining({ status: 'NOT_VERIFIED' }));
});
