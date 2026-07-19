import { describe, expect, it } from 'vitest';
import {
  CPU_DURATION_CLOCK,
  evaluatePairedMedianCases,
  evaluateRenderIsolation,
  evaluateResourceBudget,
  median,
  requireSubjectSha,
} from './performance-budget-model.mjs';

function frozenMetric(overrides = {}) {
  return {
    status: 'PASS',
    subjectSha: 'a'.repeat(40),
    sampleCount: 5,
    warmupCount: 1,
    ...overrides,
  };
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

describe('performance budget model', () => {
  it.each([
    [[1], /exactly 5 samples/],
    [[1, 2, 3, 4, Number.NaN], /finite non-negative/],
    [[1, 2, 3, 4, -1], /finite non-negative/],
  ])('rejects invalid samples %#', (samples, error) => {
    expect(() => median(samples)).toThrow(error);
  });

  it('computes the exact five-sample median without averaging', () => {
    expect(median([9, 2, 7, 1, 5])).toBe(5);
  });

  it('rejects a mismatched final subject', () => {
    expect(() => requireSubjectSha('a'.repeat(40), 'b'.repeat(40))).toThrow(/subject mismatch/);
  });

  it.each([
    ['zero samples', frozenMetric({ sampleCount: 0, maxRegressionRatio: 1.15, cases: {} })],
    ['no warmup', frozenMetric({ warmupCount: 0, maxRegressionRatio: 1.15, cases: {} })],
    ['missing threshold', frozenMetric({ cases: {} })],
    ['relaxed threshold', frozenMetric({ maxRegressionRatio: 1.25, cases: {} })],
  ])('does not verify a history baseline with %s', (_name, baselineMetric) => {
    const verdict = evaluatePairedMedianCases({
      baselineMetric,
      currentMetric: { cases: {} },
      durationClock: CPU_DURATION_CLOCK,
      metricId: 'P02-history-budget',
      ratioKey: 'maxRegressionRatio',
      requiredRatio: 1.15,
    });
    expect(verdict.status).toBe('NOT_VERIFIED');
  });

  it('fails a normalized median that exceeds the fixed 15 percent threshold', () => {
    const verdict = evaluatePairedMedianCases({
      baselineMetric: frozenMetric({
        maxRegressionRatio: 1.15,
        cases: { history: pairedTimingCase(1) },
      }),
      currentMetric: { cases: { history: pairedTimingCase(1.16) } },
      durationClock: CPU_DURATION_CLOCK,
      metricId: 'P02-history-budget',
      ratioKey: 'maxRegressionRatio',
      requiredRatio: 1.15,
    });
    expect(verdict).toEqual(expect.objectContaining({ status: 'FAIL' }));
  });

  it('marks the legacy absolute-duration baseline schema NOT_VERIFIED', () => {
    const verdict = evaluatePairedMedianCases({
      baselineMetric: frozenMetric({
        maxRegressionRatio: 1.15,
        cases: {
          history: timingCase(10, 'p50(process.cpuUsage(user+system),500000-iteration-blocks)'),
        },
      }),
      currentMetric: { cases: { history: pairedTimingCase(1) } },
      durationClock: CPU_DURATION_CLOCK,
      metricId: 'P02-history-budget',
      ratioKey: 'maxRegressionRatio',
      requiredRatio: 1.15,
    });

    expect(verdict).toEqual(expect.objectContaining({
      status: 'NOT_VERIFIED',
      reason: expect.stringMatching(/paired schema is invalid/),
    }));
  });

  it('recomputes paired ratios and rejects forged, incomplete, zero, and legacy evidence', () => {
    const baselineCase = pairedTimingCase(1);
    const evaluate = (currentCase) => evaluatePairedMedianCases({
      baselineMetric: frozenMetric({
        maxRegressionRatio: 1.15,
        cases: { history: baselineCase },
      }),
      currentMetric: { cases: { history: currentCase } },
      durationClock: CPU_DURATION_CLOCK,
      metricId: 'P02-history-budget',
      ratioKey: 'maxRegressionRatio',
      requiredRatio: 1.15,
    });

    const editedMedian = structuredClone(pairedTimingCase(1));
    editedMedian.normalizedRatioMedian = 0.5;
    const editedSample = structuredClone(pairedTimingCase(1));
    editedSample.normalizedRatioSamples[0] = 0.5;
    const forgedRatio = structuredClone(pairedTimingCase(1));
    forgedRatio.sampleDiagnostics[0].rawNormalizedBlockRatios[0] = 0.5;
    const missingBlocks = structuredClone(pairedTimingCase(1));
    delete missingBlocks.sampleDiagnostics[0].referenceBlockCpuDurationsMs;
    const zeroReference = structuredClone(pairedTimingCase(1));
    zeroReference.sampleDiagnostics[0].referenceBlockCpuDurationsMs[0] = 0;
    const nonFiniteRatio = structuredClone(pairedTimingCase(1));
    nonFiniteRatio.sampleDiagnostics[0].rawNormalizedBlockRatios[0] = Number.NaN;

    expect(() => evaluate(editedMedian)).toThrow(/median does not match/);
    expect(() => evaluate(editedSample)).toThrow(/summary is invalid/);
    expect(() => evaluate(forgedRatio)).toThrow(/ratio is not reproducible/);
    expect(() => evaluate(missingBlocks)).toThrow(/evidence is incomplete/);
    expect(() => evaluate(zeroReference)).toThrow(/finite positive/);
    expect(() => evaluate(nonFiniteRatio)).toThrow(/finite positive/);
    expect(() => evaluate(timingCase(1))).toThrow(/paired measurement shape is invalid/);
  });

  it('requires the P01 mutation counterexample and both absolute render limits', () => {
    const baseline = frozenMetric({
      absoluteUpdateLimit: 1,
      updateCount: 20,
      warmupUpdates: 2,
      mainPageUpdateCommits: 1,
      unrelatedSubtreeUpdateCommits: 1,
    });
    expect(evaluateRenderIsolation({
      updateCount: 20,
      warmupUpdates: 2,
      mainPageUpdateCommits: 0,
      unrelatedSubtreeUpdateCommits: 0,
      mutationDetected: false,
    }, baseline).status).toBe('FAIL');
    expect(evaluateRenderIsolation({
      updateCount: 20,
      warmupUpdates: 2,
      mainPageUpdateCommits: 2,
      unrelatedSubtreeUpdateCommits: 0,
      mutationDetected: true,
    }, baseline).status).toBe('FAIL');
  });

  it('fails bundle or heap growth above the fixed five percent resource budget', () => {
    const verdict = evaluateResourceBudget({
      totalBundleBytes: 106,
      maxChunkBytes: 100,
      heapUsedMedianBytes: 106,
    }, frozenMetric({
      maxRegressionRatio: 1.05,
      totalBundleBytes: 100,
      maxChunkBytes: 100,
      heapUsedMedianBytes: 100,
    }));
    expect(verdict.status).toBe('FAIL');
    expect(verdict.comparisons).toEqual(expect.arrayContaining([
      expect.objectContaining({
        case: 'heapUsedMedianBytes',
        baseline: 100,
        current: 106,
        threshold: 105,
      }),
    ]));
  });
});
