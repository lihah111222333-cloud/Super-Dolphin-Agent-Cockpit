import { describe, expect, it } from 'vitest';
import {
  CPU_DURATION_CLOCK,
  evaluateMedianCases,
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

function timingCase(durationMedianMs) {
  return {
    attemptsPerSample: 1,
    durationClock: CPU_DURATION_CLOCK,
    iterationCount: 100,
    durationAttemptSamplesMs: Array.from(
      { length: 5 },
      () => [durationMedianMs],
    ),
    durationSamplesMs: Array.from({ length: 5 }, () => durationMedianMs),
    durationMedianMs,
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
    const verdict = evaluateMedianCases({
      baselineMetric,
      currentMetric: { cases: {} },
      durationClock: CPU_DURATION_CLOCK,
      metricId: 'P02-history-budget',
      ratioKey: 'maxRegressionRatio',
      requiredRatio: 1.15,
      valueKey: 'durationMedianMs',
    });
    expect(verdict.status).toBe('NOT_VERIFIED');
  });

  it('fails a median that exceeds the fixed 15 percent threshold', () => {
    const verdict = evaluateMedianCases({
      baselineMetric: frozenMetric({
        maxRegressionRatio: 1.15,
        cases: { history: timingCase(100) },
      }),
      currentMetric: { cases: { history: timingCase(116) } },
      durationClock: CPU_DURATION_CLOCK,
      metricId: 'P02-history-budget',
      ratioKey: 'maxRegressionRatio',
      requiredRatio: 1.15,
      valueKey: 'durationMedianMs',
    });
    expect(verdict).toEqual(expect.objectContaining({ status: 'FAIL' }));
  });

  it('rejects a hand-edited median or a missing deterministic attempt', () => {
    const baselineCase = timingCase(100);
    const editedMedian = { ...timingCase(100), durationMedianMs: 99 };
    const missingAttempt = timingCase(100);
    missingAttempt.durationAttemptSamplesMs[0] = [];
    const evaluate = (currentCase) => evaluateMedianCases({
      baselineMetric: frozenMetric({
        maxRegressionRatio: 1.15,
        cases: { history: baselineCase },
      }),
      currentMetric: { cases: { history: currentCase } },
      durationClock: CPU_DURATION_CLOCK,
      metricId: 'P02-history-budget',
      ratioKey: 'maxRegressionRatio',
      requiredRatio: 1.15,
      valueKey: 'durationMedianMs',
    });

    expect(() => evaluate(editedMedian)).toThrow(/median does not match/);
    expect(() => evaluate(missingAttempt)).toThrow(/must contain 1 attempts/);
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

  it('fails bundle growth above the fixed five percent resource budget', () => {
    const verdict = evaluateResourceBudget({
      totalBundleBytes: 106,
      maxChunkBytes: 100,
    }, frozenMetric({
      maxRegressionRatio: 1.05,
      totalBundleBytes: 100,
      maxChunkBytes: 100,
    }));
    expect(verdict.status).toBe('FAIL');
  });
});
