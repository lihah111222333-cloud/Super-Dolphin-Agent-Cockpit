import { performance } from 'node:perf_hooks';
import {
  ATTEMPTS_PER_SAMPLE,
  FEEDBACK_DURATION_CLOCK,
  FEEDBACK_REGRESSION_RATIO,
  SAMPLE_COUNT,
  WARMUP_COUNT,
  evaluateMedianCases,
  median,
} from './performance-budget-model.mjs';
import { attachActiveThreadRpcRuntime } from '../src/entities/client/model/threadLifecycleRuntime.js';

const MEASUREMENT_ITERATIONS = 10_000;

function createStopFeedbackHarness() {
  let state = {
    activeThreadId: 'thread-performance',
    activeTurnByThread: {
      'thread-performance': {
        id: 'turn-performance',
        threadId: 'thread-performance',
        status: 'running',
      },
    },
    cwd: '/performance/probe',
  };
  let feedbackAt = 0;
  const runtime = {
    addWarning() {},
    get: () => state,
    notifyAction(message, tone) {
      if (message !== '已发送中断请求' || tone !== 'success') {
        throw new Error(`unexpected stop feedback: ${tone}:${message}`);
      }
      feedbackAt = performance.now();
    },
    requireCwd: () => state.cwd,
    set(patch) {
      state = { ...state, ...(typeof patch === 'function' ? patch(state) : patch) };
    },
  };
  attachActiveThreadRpcRuntime(runtime, {
    activeThreadInterruptTarget: (current) => ({
      interruptible: true,
      threadId: current.activeThreadId,
      turnId: current.activeTurnByThread[current.activeThreadId].id,
    }),
    backendThreadIdForState: (current) => current.activeThreadId,
    cleanObject: (value) => Object.fromEntries(
      Object.entries(value).filter(([, entry]) => entry !== undefined && entry !== ''),
    ),
    createRequestId: () => 'performance-stop-request',
  });
  return {
    async measure() {
      feedbackAt = 0;
      const startedAt = performance.now();
      const accepted = await runtime.activeThreadRPC('thread.interrupt', async () => ({ ok: true }));
      if (!accepted || feedbackAt === 0) throw new Error('stop feedback was not produced');
      return feedbackAt - startedAt;
    },
  };
}

async function runStopFeedbackBenchmark({
  subjectSha,
  sampleCount = SAMPLE_COUNT,
  warmupCount = WARMUP_COUNT,
} = {}) {
  if (sampleCount !== SAMPLE_COUNT) throw new TypeError(`sampleCount must be ${SAMPLE_COUNT}`);
  if (warmupCount !== WARMUP_COUNT) throw new TypeError(`warmupCount must be ${WARMUP_COUNT}`);
  const harness = createStopFeedbackHarness();
  const measureBatch = async () => {
    let durationMs = 0;
    for (let index = 0; index < MEASUREMENT_ITERATIONS; index += 1) {
      durationMs += await harness.measure();
    }
    return durationMs;
  };
  for (let index = 0; index < warmupCount; index += 1) await measureBatch();
  const durationAttemptSamplesMs = [];
  for (let sampleIndex = 0; sampleIndex < sampleCount; sampleIndex += 1) {
    const attempts = [];
    for (let attemptIndex = 0; attemptIndex < ATTEMPTS_PER_SAMPLE; attemptIndex += 1) {
      attempts.push(await measureBatch());
    }
    durationAttemptSamplesMs.push(attempts);
  }
  const durationSamplesMs = durationAttemptSamplesMs.map((attempts) => Math.min(...attempts));
  return Object.freeze({
    metricId: 'P03-feedback-budget',
    subjectSha,
    warmupCount,
    sampleCount,
    cases: Object.freeze({
      'stop-visible-feedback': Object.freeze({
        attemptsPerSample: ATTEMPTS_PER_SAMPLE,
        durationClock: FEEDBACK_DURATION_CLOCK,
        iterationCount: MEASUREMENT_ITERATIONS,
        durationAttemptSamplesMs,
        durationSamplesMs,
        durationMedianMs: median(durationSamplesMs, 'stop-visible-feedback.durationSamplesMs'),
      }),
    }),
  });
}

function verifyStopFeedbackEvidence(evidence, baseline) {
  return evaluateMedianCases({
    baselineMetric: baseline?.metrics?.['P03-feedback-budget'],
    currentMetric: evidence,
    durationClock: FEEDBACK_DURATION_CLOCK,
    metricId: 'P03-feedback-budget',
    ratioKey: 'maxRegressionRatio',
    requiredRatio: FEEDBACK_REGRESSION_RATIO,
    valueKey: 'durationMedianMs',
  });
}

export {
  ATTEMPTS_PER_SAMPLE,
  FEEDBACK_DURATION_CLOCK,
  MEASUREMENT_ITERATIONS,
  createStopFeedbackHarness,
  runStopFeedbackBenchmark,
  verifyStopFeedbackEvidence,
};
