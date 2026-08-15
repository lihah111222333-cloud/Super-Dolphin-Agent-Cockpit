import { performance } from 'node:perf_hooks';
import process from 'node:process';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { JSDOM } from 'jsdom';
import { attachActiveThreadRpcRuntime } from '../src/entities/client/model/threadLifecycleRuntime.js';
import { ChatActionFeedback } from '../src/pages/chat/components/ChatActionFeedback.js';

const SAMPLE_COUNT = 5;
const WARMUP_COUNT = 1;
const ATTEMPTS_PER_SAMPLE = 1;
const FEEDBACK_DURATION_CLOCK = 'trimmed-mean-80%(performance.now(feedbackAt-startedAt))';

// Eleven observations retain the 80% trimmed mean's outlier rejection without turning DOM commits into a load test.
const MEASUREMENT_ITERATIONS = 11;
const DOM_COMMIT_TIMEOUT_MS = 250;
const SCRIPT_PATH = fileURLToPath(import.meta.url);
const STOP_FEEDBACK = Object.freeze({
  confirmed: Object.freeze({ message: '已发送中断请求', tone: 'success' }),
  unconfirmed: Object.freeze({ message: '停止未确认，任务可能仍在运行', tone: 'warning' }),
  unconfirmedResponse: Object.freeze({
    ok: false,
    accepted: true,
    requestId: 'performance-stop-request',
    expectedTurnId: 'turn-performance',
    turnId: 'turn-performance',
    status: 'running',
    confirmed: true,
    mode: 'interrupt_timeout',
    interruptSent: true,
    stateBefore: 'running',
    stateAfter: 'running',
    waitedMs: 0,
    activeObserved: true,
  }),
});
const STOP_FEEDBACK_COPY = Object.freeze({ noticeTitle: '操作通知' });

function requireFiniteNonNegative(value, label) {
  if (!Number.isFinite(value) || value < 0) throw new TypeError(`${label} must be a finite non-negative number`);
  return value;
}

function measurementTrimmedMean(values, label = 'measurements') {
  if (!Array.isArray(values) || values.length < 5) {
    throw new TypeError(`${label} must contain at least 5 measurements`);
  }
  const sorted = values.map((value, index) => requireFiniteNonNegative(value, `${label}[${index}]`))
    .sort((left, right) => left - right);
  const trimCount = Math.floor(sorted.length * 0.1);
  const retained = sorted.slice(trimCount, sorted.length - trimCount);
  return retained.reduce((total, value) => total + value, 0) / retained.length;
}

function measurementMedian(values, label = 'measurements') {
  if (!Array.isArray(values) || values.length === 0) throw new TypeError(`${label} must be non-empty`);
  const sorted = values.map((value, index) => requireFiniteNonNegative(value, `${label}[${index}]`))
    .sort((left, right) => left - right);
  return sorted[Math.floor(sorted.length / 2)];
}

function median(values, label = 'samples') {
  if (!Array.isArray(values) || values.length !== SAMPLE_COUNT) {
    throw new TypeError(`${label} must contain exactly ${SAMPLE_COUNT} samples`);
  }
  return measurementMedian(values, label);
}

if (typeof globalThis.document === 'undefined') {
  const { window } = new JSDOM('<!doctype html><html><body></body></html>', { pretendToBeVisual: true });
  Object.defineProperties(globalThis, {
    HTMLElement: { configurable: true, writable: true, value: window.HTMLElement },
    MutationObserver: { configurable: true, writable: true, value: window.MutationObserver },
    Node: { configurable: true, writable: true, value: window.Node },
    document: { configurable: true, writable: true, value: window.document },
    navigator: { configurable: true, writable: true, value: window.navigator },
    window: { configurable: true, writable: true, value: window },
  });
}

const React = await import('react');
const { flushSync } = await import('react-dom');
const { createRoot } = await import('react-dom/client');

function observeFeedbackCommit(host, expected) {
  return new Promise((resolve, reject) => {
    let timeoutId;
    const observer = new MutationObserver(() => check());
    const finish = (callback, value) => {
      globalThis.clearTimeout(timeoutId);
      observer.disconnect();
      callback(value);
    };
    const check = () => {
      const output = host.querySelector('[data-testid="chat-action-feedback"]');
      if (
        output?.classList.contains(`is-${expected.tone}`)
        && output.getAttribute('role') === 'alert'
        && output.querySelector('span')?.textContent === expected.message
      ) {
        finish(resolve, performance.now());
      }
    };
    observer.observe(host, { attributes: true, characterData: true, childList: true, subtree: true });
    timeoutId = globalThis.setTimeout(() => {
      finish(reject, new Error(`stop feedback did not commit to the React DOM: ${expected.tone}:${expected.message}`));
    }, DOM_COMMIT_TIMEOUT_MS);
    check();
  });
}

function confirmedInterruptResponse() {
  return {
    ok: true,
    accepted: true,
    requestId: 'performance-stop-request',
    expectedTurnId: 'turn-performance',
    turnId: 'turn-performance',
    status: 'interrupted',
    confirmed: true,
    mode: 'interrupt_confirmed',
    interruptSent: true,
    stateBefore: 'running',
    stateAfter: 'idle',
    waitedMs: 0,
    activeObserved: true,
  };
}

function createStopFeedbackHarness() {
  const runtimeAttacher = attachActiveThreadRpcRuntime;
  const FeedbackComponent = ChatActionFeedback;
  const contract = STOP_FEEDBACK;
  const host = globalThis.document.createElement('div');
  globalThis.document.body.append(host);
  const root = createRoot(host);
  const commitFeedback = (feedback) => {
    flushSync(() => root.render(React.createElement(FeedbackComponent, { copy: STOP_FEEDBACK_COPY, feedback })));
  };
  commitFeedback(null);
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
  const renderFeedback = (feedback) => {
    commitFeedback(feedback);
  };
  const runtime = {
    addWarning() {},
    get: () => state,
    notifyAction(message, tone) {
      const isConfirmed = message === contract.confirmed.message && tone === contract.confirmed.tone;
      const isUnconfirmed = message === contract.unconfirmed.message && tone === contract.unconfirmed.tone;
      if (!isConfirmed && !isUnconfirmed) throw new Error(`unexpected stop feedback: ${tone}:${message}`);
      renderFeedback({ message, tone });
    },
    requireCwd: () => state.cwd,
    set(patch) {
      state = { ...state, ...(typeof patch === 'function' ? patch(state) : patch) };
    },
  };
  runtimeAttacher(runtime, {
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
  const measureOutcome = async (response, expected, accepted) => {
    commitFeedback(null);
    const startedAt = performance.now();
    const committedAt = observeFeedbackCommit(host, expected);
    try {
      const result = await runtime.activeThreadRPC('thread.interrupt', async () => response);
      if (result !== accepted) throw new Error(`stop ${expected.message} branch returned ${result}`);
      return (await committedAt) - startedAt;
    } catch (error) {
      await committedAt.catch(() => undefined);
      throw error;
    }
  };
  return {
    destroy() {
      root.unmount();
      host.remove();
    },
    measureConfirmed() {
      return measureOutcome(confirmedInterruptResponse(), contract.confirmed, true);
    },
    measureUnconfirmed() {
      return measureOutcome(contract.unconfirmedResponse, contract.unconfirmed, false);
    },
    async measure() {
      const confirmedMs = await this.measureConfirmed();
      const unconfirmedMs = await this.measureUnconfirmed();
      return Math.max(confirmedMs, unconfirmedMs);
    },
  };
}

async function runStopFeedbackBenchmark({
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
  try {
    for (let index = 0; index < warmupCount; index += 1) await measureBatch();
    const sampleDiagnostics = [];
    for (let sampleIndex = 0; sampleIndex < sampleCount; sampleIndex += 1) {
      sampleDiagnostics.push(await measureBatch());
    }
    const durationSamplesMs = sampleDiagnostics.map(({ durationMs }) => durationMs);
    const durationAttemptSamplesMs = durationSamplesMs.map((durationMs) => Object.freeze([durationMs]));
    return Object.freeze({
      metricId: 'stop-feedback-timing-diagnostic',
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
  finally {
    harness.destroy();
  }
}

if (process.argv[1] && resolve(process.argv[1]) === SCRIPT_PATH) {
  try {
    process.stdout.write(`${JSON.stringify(await runStopFeedbackBenchmark())}\n`);
  } catch (error) {
    process.stderr.write(`stop feedback benchmark failed: ${error.message}\n`);
    process.exitCode = 1;
  }
}

export {
  ATTEMPTS_PER_SAMPLE,
  DOM_COMMIT_TIMEOUT_MS,
  FEEDBACK_DURATION_CLOCK,
  MEASUREMENT_ITERATIONS,
  createStopFeedbackHarness,
  measurementMedian,
  measurementTrimmedMean,
  runStopFeedbackBenchmark,
};
