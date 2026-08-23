import { expect, it } from 'vitest';
import {
  ATTEMPTS_PER_SAMPLE,
  FEEDBACK_DURATION_CLOCK,
  MEASUREMENT_ITERATIONS,
  createStopFeedbackHarness,
  runStopFeedbackBenchmark,
} from './stop-feedback-benchmark.mjs';

it('measures confirmed and unconfirmed Stop outcomes after their React DOM output commits', async () => {
  const harness = createStopFeedbackHarness();
  try {
    await expect(harness.measureConfirmed()).resolves.toBeGreaterThanOrEqual(0);
    await expect(harness.measureUnconfirmed()).resolves.toBeGreaterThanOrEqual(0);
  } finally {
    harness.destroy();
  }
});

it('reports bounded five-sample timing evidence for the measure command', async () => {
  const evidence = await runStopFeedbackBenchmark();
  expect(evidence).toEqual(expect.objectContaining({
    metricId: 'stop-feedback-timing-diagnostic',
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
});
