import { performance } from 'node:perf_hooks';
import {
  ATTEMPTS_PER_SAMPLE,
  FEEDBACK_DURATION_CLOCK,
  FEEDBACK_REGRESSION_RATIO,
  SAMPLE_COUNT,
  WARMUP_COUNT,
  evaluateMedianCases,
  measurementTrimmedMean,
  median,
} from './performance-budget-model.mjs';
import { attachActiveThreadRpcRuntime } from '../src/entities/client/model/threadLifecycleRuntime.js';

const MEASUREMENT_ITERATIONS = 10_001;

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
    const durationsMs = [];
    for (let index = 0; index < MEASUREMENT_ITERATIONS; index += 1) {
      durationsMs.push(await harness.measure());
    }
    const sorted = [...durationsMs].sort((left, right) => left - right);
    const durationMs = measurementTrimmedMean(durationsMs, 'stop feedback measurements');
    return Object.freeze({
      count: durationsMs.length,
      minMs: sorted[0],
      p50Ms: sorted[Math.floor(sorted.length * 0.5)],
      p95Ms: sorted[Math.floor(sorted.length * 0.95)],
      maxMs: sorted.at(-1),
      durationMs,
    });
  };
  for (let index = 0; index < warmupCount; index += 1) await measureBatch();
  const sampleDiagnostics = [];
  for (let sampleIndex = 0; sampleIndex < sampleCount; sampleIndex += 1) {
    sampleDiagnostics.push(await measureBatch());
  }
  const durationSamplesMs = sampleDiagnostics.map(({ durationMs }) => durationMs);
  const durationAttemptSamplesMs = durationSamplesMs.map((durationMs) => Object.freeze([durationMs]));
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
        rawSamplesMs: Object.freeze(durationSamplesMs),
        sampleDiagnostics: Object.freeze(sampleDiagnostics),
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
  measurementTrimmedMean,
  runStopFeedbackBenchmark,
  verifyStopFeedbackEvidence,
};
