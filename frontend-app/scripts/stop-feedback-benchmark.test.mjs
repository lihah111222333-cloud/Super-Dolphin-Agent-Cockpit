import { expect, it } from 'vitest';
import {
  ATTEMPTS_PER_SAMPLE,
  FEEDBACK_DURATION_CLOCK,
  MEASUREMENT_ITERATIONS,
  createStopFeedbackHarness,
  measurementTrimmedMean,
  runStopFeedbackBenchmark,
  verifyStopFeedbackEvidence,
} from './stop-feedback-benchmark.mjs';

let benchmarkEvidence;

function fullBenchmarkEvidence() {
  benchmarkEvidence ??= runStopFeedbackBenchmark({ subjectSha: 'a'.repeat(40) });
  return benchmarkEvidence;
}

it('measures confirmed and timeout-unconfirmed Stop outcomes only after their React DOM output commits', async () => {
  const harness = createStopFeedbackHarness();
  try {
    await expect(harness.measureConfirmed()).resolves.toBeGreaterThanOrEqual(0);
    await expect(harness.measureUnconfirmed()).resolves.toBeGreaterThanOrEqual(0);
  }
  finally {
    harness.destroy();
  }
});

it('fails rather than recording a synchronous notifyAction callback when a DOM mutation suppresses feedback', async () => {
  const harness = createStopFeedbackHarness({ domMutation: { mode: 'suppress' } });
  try {
    await expect(harness.measureConfirmed()).rejects.toThrow(/did not commit to the React DOM/);
    await expect(harness.measureUnconfirmed()).rejects.toThrow(/did not commit to the React DOM/);
  }
  finally {
    harness.destroy();
  }
});

it('includes delayed React DOM commits in Stop feedback timing', async () => {
  const delayMs = 25;
  const harness = createStopFeedbackHarness({ domMutation: { delayMs, mode: 'delay' } });
  try {
    await expect(harness.measureConfirmed()).resolves.toBeGreaterThanOrEqual(delayMs);
    await expect(harness.measureUnconfirmed()).resolves.toBeGreaterThanOrEqual(delayMs);
  }
  finally {
    harness.destroy();
  }
});

it('uses an 80 percent trimmed mean and rejects invalid timing values', () => {
  const values = [1_000, ...Array.from({ length: 8 }, () => 5), 0];
  expect(measurementTrimmedMean(values)).toBe(5);
  expect(() => measurementTrimmedMean([])).toThrow(/at least 5/);
  expect(() => measurementTrimmedMean([1, 2, 3, 4, Number.NaN])).toThrow(/finite non-negative/);
  expect(() => measurementTrimmedMean([1, 2, 3, 4, -1])).toThrow(/finite non-negative/);
});

it('measures visible stop feedback after warmup with five production-runtime samples', async () => {
  const evidence = await fullBenchmarkEvidence();

  expect(evidence).toEqual(expect.objectContaining({
    metricId: 'P03-feedback-budget',
    sampleCount: 5,
    warmupCount: 1,
  }));
  const stop = evidence.cases['stop-visible-feedback'];
  expect(MEASUREMENT_ITERATIONS).toBe(11);
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
}, 30_000);

it('does not verify stop feedback against an unfrozen baseline', async () => {
  const evidence = await fullBenchmarkEvidence();
  expect(verifyStopFeedbackEvidence(evidence, {
    metrics: {
      'P03-feedback-budget': { status: 'NOT_VERIFIED' },
    },
  }).status).toBe('NOT_VERIFIED');
});
