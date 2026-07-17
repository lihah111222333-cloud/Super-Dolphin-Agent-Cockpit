import { expect, it } from 'vitest';
import {
  ATTEMPTS_PER_SAMPLE,
  FEEDBACK_DURATION_CLOCK,
  MEASUREMENT_ITERATIONS,
  measurementTrimmedMean,
  runStopFeedbackBenchmark,
  verifyStopFeedbackEvidence,
} from './stop-feedback-benchmark.mjs';

it('uses an 80 percent trimmed mean and rejects invalid timing values', () => {
  const values = [1_000, ...Array.from({ length: 8 }, () => 5), 0];
  expect(measurementTrimmedMean(values)).toBe(5);
  expect(() => measurementTrimmedMean([])).toThrow(/at least 5/);
  expect(() => measurementTrimmedMean([1, 2, 3, 4, Number.NaN])).toThrow(/finite non-negative/);
  expect(() => measurementTrimmedMean([1, 2, 3, 4, -1])).toThrow(/finite non-negative/);
});

it('measures visible stop feedback after warmup with five production-runtime samples', async () => {
  const evidence = await runStopFeedbackBenchmark({ subjectSha: 'a'.repeat(40) });

  expect(evidence).toEqual(expect.objectContaining({
    metricId: 'P03-feedback-budget',
    sampleCount: 5,
    warmupCount: 1,
  }));
  const stop = evidence.cases['stop-visible-feedback'];
  expect(stop.attemptsPerSample).toBe(ATTEMPTS_PER_SAMPLE);
  expect(stop.durationClock).toBe(FEEDBACK_DURATION_CLOCK);
  expect(stop.iterationCount).toBe(MEASUREMENT_ITERATIONS);
  expect(stop.durationAttemptSamplesMs).toHaveLength(5);
  stop.durationAttemptSamplesMs.forEach((attempts, index) => {
    expect(attempts).toHaveLength(ATTEMPTS_PER_SAMPLE);
    expect(stop.durationSamplesMs[index]).toBe(attempts[0]);
  });
  expect(stop.rawSamplesMs).toEqual(stop.durationSamplesMs);
  expect(stop.sampleDiagnostics).toHaveLength(5);
  stop.sampleDiagnostics.forEach((sample) => {
    expect(sample).toEqual(expect.objectContaining({ count: MEASUREMENT_ITERATIONS }));
    expect(sample.minMs).toBeLessThanOrEqual(sample.p50Ms);
    expect(sample.p50Ms).toBeLessThanOrEqual(sample.p95Ms);
    expect(sample.p95Ms).toBeLessThanOrEqual(sample.maxMs);
  });
  expect(stop.durationSamplesMs).toHaveLength(5);
  expect(stop.durationMedianMs).toBeGreaterThanOrEqual(0);
});

it('does not verify stop feedback against an unfrozen baseline', async () => {
  const evidence = await runStopFeedbackBenchmark({ subjectSha: 'a'.repeat(40) });
  expect(verifyStopFeedbackEvidence(evidence, {
    metrics: {
      'P03-feedback-budget': { status: 'NOT_VERIFIED' },
    },
  }).status).toBe('NOT_VERIFIED');
});
