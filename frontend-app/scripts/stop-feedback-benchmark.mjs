import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { performance } from 'node:perf_hooks';
import { JSDOM } from 'jsdom';
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
import { ChatActionFeedback as RUNNER_FEEDBACK_PROBE } from '../src/pages/chat/components/ChatActionFeedback.js';

// Eleven observations retain the 80% trimmed mean's outlier rejection without turning DOM commits into a load test.
const MEASUREMENT_ITERATIONS = 11;
const DOM_COMMIT_TIMEOUT_MS = 250;
const FROZEN_PLAN_BASE_SHA = 'b40867229af8e17916c00393639ccb0fcb4bf6fc';
const STOP_FEEDBACK = Object.freeze({
  base: Object.freeze({
    confirmed: Object.freeze({ message: '已发送中断请求', tone: 'success' }),
    unconfirmed: Object.freeze({ message: '中断当前执行失败：stop confirmation timed out', tone: 'warning' }),
  }),
  candidate: Object.freeze({
    confirmed: Object.freeze({ message: '已发送中断请求', tone: 'success' }),
    unconfirmed: Object.freeze({ message: '停止未确认，任务可能仍在运行', tone: 'warning' }),
  }),
});
const STOP_FEEDBACK_PENDING = Object.freeze({ message: '正在请求停止，尚未确认，任务可能仍在运行', tone: 'info' });
const STOP_FEEDBACK_COPY = Object.freeze({ noticeTitle: '操作通知' });
const P03_SUBJECT_CONTENT_PATHS = Object.freeze([
  'frontend-app/package-lock.json',
  'frontend-app/package.json',
  'frontend-app/src/entities/client/model/contractStoreModel.js',
  'frontend-app/src/entities/client/model/threadLifecycleRuntime.js',
]);
const P03_RUNTIME_PATH = 'frontend-app/src/entities/client/model/threadLifecycleRuntime.js';
const P03_RUNNER_FEEDBACK_PROBE_PATH = 'frontend-app/src/pages/chat/components/ChatActionFeedback.js';

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

function normalizeDOMMutation(mutation = {}) {
  if (mutation == null || typeof mutation !== 'object' || Array.isArray(mutation)) {
    throw new TypeError('domMutation must be an object');
  }
  const mode = mutation.mode ?? 'none';
  const delayMs = mutation.delayMs ?? 0;
  if (!['none', 'suppress', 'delay'].includes(mode)) throw new TypeError(`unsupported domMutation mode: ${mode}`);
  if (!Number.isFinite(delayMs) || delayMs < 0) throw new TypeError('domMutation delayMs must be finite and non-negative');
  if (mode !== 'delay' && delayMs !== 0) throw new TypeError('only delay domMutation may set delayMs');
  return Object.freeze({ delayMs, mode });
}

function stopFeedbackContractForSubject(subjectSha) {
  if (!/^[0-9a-f]{40}$/.test(subjectSha || '')) {
    throw new TypeError('subjectSha must be a full 40-character Git SHA');
  }
  return subjectSha === FROZEN_PLAN_BASE_SHA ? STOP_FEEDBACK.base : STOP_FEEDBACK.candidate;
}

function subjectContentEvidence(subjectRoot, contentPaths = P03_SUBJECT_CONTENT_PATHS) {
  if (typeof subjectRoot !== 'string' || subjectRoot.length === 0) throw new TypeError('subjectRoot is required');
  if (!Array.isArray(contentPaths) || contentPaths.length === 0 || new Set(contentPaths).size !== contentPaths.length) {
    throw new TypeError('P03 subject content paths must be a non-empty unique array');
  }
  const files = contentPaths.map((path) => {
    const content = readFileSync(resolve(subjectRoot, path));
    return Object.freeze({ path, sha256: createHash('sha256').update(content).digest('hex') });
  });
  const aggregate = createHash('sha256');
  files.forEach(({ path, sha256 }) => aggregate.update(`${path}\0${sha256}\n`));
  return Object.freeze({ contentHash: aggregate.digest('hex'), files: Object.freeze(files) });
}

function resolveRunnerFeedbackProbe(feedbackProbe = RUNNER_FEEDBACK_PROBE) {
  if (typeof feedbackProbe !== 'function') throw new TypeError('P03 runner feedback probe is required');
  return feedbackProbe;
}

async function loadStopFeedbackTarget({
  feedbackProbe = RUNNER_FEEDBACK_PROBE,
  subjectRoot,
  subjectSha,
  subjectTree,
}) {
  if (!/^[0-9a-f]{40}$/.test(subjectSha || '')) {
    throw new TypeError('subjectSha must be a full 40-character Git SHA');
  }
  if (!/^[0-9a-f]{40}$/.test(subjectTree || '')) {
    throw new TypeError('subjectTree must be a full 40-character Git tree SHA');
  }
  const root = resolve(subjectRoot || '');
  const runtimePath = resolve(root, P03_RUNTIME_PATH);
  const runtimeModule = await import(/* @vite-ignore */ pathToFileURL(runtimePath).href);
  if (typeof runtimeModule.attachActiveThreadRpcRuntime !== 'function') {
    throw new Error(`P03 target runtime does not export attachActiveThreadRpcRuntime: ${P03_RUNTIME_PATH}`);
  }
  return Object.freeze({
    attachRuntime: runtimeModule.attachActiveThreadRpcRuntime,
    feedbackProbe: resolveRunnerFeedbackProbe(feedbackProbe),
    provenance: Object.freeze({
      content: subjectContentEvidence(root),
      runtimePath: P03_RUNTIME_PATH,
      subjectSha,
      subjectTree,
    }),
  });
}

function resolveStopFeedbackAttachRuntime(attachRuntime, target) {
  if (attachRuntime === undefined) {
    if (typeof target?.attachRuntime !== 'function') throw new TypeError('P03 target attachRuntime is required');
    return target.attachRuntime;
  }
  if (typeof attachRuntime !== 'function') throw new TypeError('attachRuntime must be a function');
  return attachRuntime;
}

function resolveStopFeedbackComponent(target, feedbackProbe) {
  return resolveRunnerFeedbackProbe(feedbackProbe ?? target?.feedbackProbe);
}

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

function unconfirmedInterruptResponse() {
  return {
    ok: false,
    mode: 'interrupt_timeout',
    message: 'stop confirmation timed out',
  };
}

function createStopFeedbackHarness({
  attachRuntime,
  domMutation,
  feedbackProbe,
  subjectSha,
  target,
} = {}) {
  const runtimeAttacher = resolveStopFeedbackAttachRuntime(attachRuntime, target);
  const FeedbackComponent = resolveStopFeedbackComponent(target, feedbackProbe);
  const contract = stopFeedbackContractForSubject(subjectSha);
  const mutation = normalizeDOMMutation(domMutation);
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
  const scheduledFeedback = new Set();
  const renderFeedback = (feedback) => {
    if (mutation.mode === 'suppress') return;
    if (mutation.mode === 'delay') {
      const timeoutId = globalThis.setTimeout(() => {
        scheduledFeedback.delete(timeoutId);
        commitFeedback(feedback);
      }, mutation.delayMs);
      scheduledFeedback.add(timeoutId);
      return;
    }
    commitFeedback(feedback);
  };
  const runtime = {
    addWarning() {},
    get: () => state,
    notifyAction(message, tone) {
      const isPending = message === STOP_FEEDBACK_PENDING.message && tone === STOP_FEEDBACK_PENDING.tone;
      const isConfirmed = message === contract.confirmed.message && tone === contract.confirmed.tone;
      const isUnconfirmed = message === contract.unconfirmed.message && tone === contract.unconfirmed.tone;
      if (!isPending && !isConfirmed && !isUnconfirmed) throw new Error(`unexpected stop feedback: ${tone}:${message}`);
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
      for (const timeoutId of scheduledFeedback) globalThis.clearTimeout(timeoutId);
      scheduledFeedback.clear();
      root.unmount();
      host.remove();
    },
    measureConfirmed() {
      return measureOutcome(confirmedInterruptResponse(), contract.confirmed, true);
    },
    measureUnconfirmed() {
      return measureOutcome(unconfirmedInterruptResponse(), contract.unconfirmed, false);
    },
    async measure() {
      const confirmedMs = await this.measureConfirmed();
      const unconfirmedMs = await this.measureUnconfirmed();
      return Math.max(confirmedMs, unconfirmedMs);
    },
  };
}

async function runStopFeedbackBenchmark({
  attachRuntime,
  feedbackProbe,
  subjectSha,
  target,
  sampleCount = SAMPLE_COUNT,
  warmupCount = WARMUP_COUNT,
} = {}) {
  if (sampleCount !== SAMPLE_COUNT) throw new TypeError(`sampleCount must be ${SAMPLE_COUNT}`);
  if (warmupCount !== WARMUP_COUNT) throw new TypeError(`warmupCount must be ${WARMUP_COUNT}`);
  const harness = createStopFeedbackHarness({ attachRuntime, feedbackProbe, subjectSha, target });
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
      metricId: 'P03-feedback-budget',
      runnerFeedbackProbe: Object.freeze({
        path: P03_RUNNER_FEEDBACK_PROBE_PATH,
        source: 'runner',
      }),
      subjectSha,
      subjectRuntime: target?.provenance,
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
  DOM_COMMIT_TIMEOUT_MS,
  FEEDBACK_DURATION_CLOCK,
  FROZEN_PLAN_BASE_SHA,
  MEASUREMENT_ITERATIONS,
  P03_RUNNER_FEEDBACK_PROBE_PATH,
  P03_RUNTIME_PATH,
  P03_SUBJECT_CONTENT_PATHS,
  RUNNER_FEEDBACK_PROBE,
  STOP_FEEDBACK_PENDING,
  createStopFeedbackHarness,
  loadStopFeedbackTarget,
  measurementTrimmedMean,
  runStopFeedbackBenchmark,
  resolveStopFeedbackAttachRuntime,
  resolveStopFeedbackComponent,
  resolveRunnerFeedbackProbe,
  stopFeedbackContractForSubject,
  subjectContentEvidence,
  verifyStopFeedbackEvidence,
};
