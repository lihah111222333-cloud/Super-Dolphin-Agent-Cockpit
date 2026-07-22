import { execFileSync } from 'node:child_process';
import {
  mkdtempSync,
  rmSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { expect, it } from 'vitest';
import { createElement } from 'react';
import {
  ATTEMPTS_PER_SAMPLE,
  FEEDBACK_DURATION_CLOCK,
  FROZEN_PLAN_BASE_SHA,
  MEASUREMENT_ITERATIONS,
  P03_SUBJECT_FEEDBACK_COMPONENT_PATH,
  P03_SUBJECT_CONTENT_PATHS,
  STOP_FEEDBACK_PENDING,
  createStopFeedbackHarness,
  loadStopFeedbackTarget,
  measurementTrimmedMean,
  resolveStopFeedbackAttachRuntime,
  resolveStopFeedbackComponent,
  runStopFeedbackBenchmark,
  stopFeedbackContractForSubject,
  verifyStopFeedbackEvidence,
} from './stop-feedback-benchmark.mjs';

let benchmarkEvidence;
const CANDIDATE_SUBJECT_SHA = 'c'.repeat(40);

function testFeedbackProbe({ feedback }) {
  if (!feedback?.message) return null;
  return createElement(
    'output',
    { className: `chat-action-toast is-${feedback.tone || 'info'}`, role: 'alert', 'data-testid': 'chat-action-feedback' },
    createElement('span', null, feedback.message),
  );
}

function fullBenchmarkEvidence() {
  benchmarkEvidence ??= runStopFeedbackBenchmark({
    attachRuntime: attachBaseRuntime,
    subjectSha: FROZEN_PLAN_BASE_SHA,
    target: testTarget(),
  });
  return benchmarkEvidence;
}

function testTarget(attachRuntime = attachBaseRuntime) {
  return Object.freeze({
    attachRuntime,
    feedbackProbe: testFeedbackProbe,
    provenance: Object.freeze({
      content: Object.freeze({ contentHash: 'a'.repeat(64), files: Object.freeze([]) }),
      feedbackComponentPath: P03_SUBJECT_FEEDBACK_COMPONENT_PATH,
      runtimePath: 'frontend-app/src/entities/client/model/threadLifecycleRuntime.js',
      subjectSha: FROZEN_PLAN_BASE_SHA,
      subjectTree: 'b'.repeat(40),
    }),
  });
}

function attachBaseRuntime(runtime) {
  runtime.activeThreadRPC = async (action, rpc) => {
    if (action !== 'thread.interrupt') throw new Error(`unexpected action: ${action}`);
    const response = await rpc({});
    if (response.ok) {
      runtime.notifyAction('已发送中断请求', 'success');
      return true;
    }
    runtime.notifyAction('中断当前执行失败：stop confirmation timed out', 'warning');
    return false;
  };
}

function attachCandidateRuntime(runtime, deps) {
  runtime.activeThreadRPC = async (action, rpc) => {
    if (action !== 'thread.interrupt') throw new Error(`unexpected action: ${action}`);
    if (deps.createRequestId() !== 'performance-stop-request') throw new Error('candidate request id mismatch');
    runtime.notifyAction(STOP_FEEDBACK_PENDING.message, STOP_FEEDBACK_PENDING.tone);
    const response = await rpc({});
    if (response.ok) {
      runtime.notifyAction('已发送中断请求', 'success');
      return true;
    }
    runtime.notifyAction('停止未确认，任务可能仍在运行', 'warning');
    return false;
  };
}

it('measures BASE confirmed and timeout-unconfirmed Stop outcomes only after their React DOM output commits', async () => {
  const harness = createStopFeedbackHarness({
    attachRuntime: attachBaseRuntime,
    subjectSha: FROZEN_PLAN_BASE_SHA,
    target: testTarget(),
  });
  try {
    await expect(harness.measureConfirmed()).resolves.toBeGreaterThanOrEqual(0);
    await expect(harness.measureUnconfirmed()).resolves.toBeGreaterThanOrEqual(0);
  }
  finally {
    harness.destroy();
  }
});

it('requires an explicitly loaded product target instead of defaulting to the runner worktree runtime', () => {
  expect(resolveStopFeedbackAttachRuntime(undefined, testTarget())).toBe(attachBaseRuntime);
  expect(resolveStopFeedbackAttachRuntime(attachCandidateRuntime, testTarget())).toBe(attachCandidateRuntime);
  expect(resolveStopFeedbackComponent(testTarget())).toBe(testFeedbackProbe);
  expect(() => resolveStopFeedbackAttachRuntime()).toThrow(/target attachRuntime is required/);
  expect(() => resolveStopFeedbackAttachRuntime(null, testTarget())).toThrow(/attachRuntime must be a function/);
});

it('imports in a plain Node child process with a configurable read-only navigator getter', () => {
  const repositoryRoot = resolve(process.cwd(), '..');
  const loaderUrl = pathToFileURL(resolve(repositoryRoot, 'frontend-app/scripts/stop-feedback-benchmark.mjs')).href;
  const script = `
    Object.defineProperty(globalThis, 'navigator', {
      configurable: true,
      enumerable: true,
      get() { return Object.freeze({ source: 'node-read-only' }); },
    });
    await import(${JSON.stringify(loaderUrl)});
    const descriptor = Object.getOwnPropertyDescriptor(globalThis, 'navigator');
    process.stdout.write(JSON.stringify({
      configurable: descriptor.configurable,
      hasDocument: Boolean(globalThis.document),
      navigatorMatchesWindow: globalThis.navigator === globalThis.window.navigator,
      writable: descriptor.writable,
    }));
  `;
  const result = JSON.parse(execFileSync(process.execPath, ['--input-type=module', '--eval', script], {
    cwd: join(repositoryRoot, 'frontend-app'),
    encoding: 'utf8',
  }));
  expect(result).toEqual({
    configurable: true,
    hasDocument: true,
    navigatorMatchesWindow: true,
    writable: true,
  });
}, 30000);

it('fails closed when b408 does not contain the subject feedback component', async () => {
  const repositoryRoot = resolve(process.cwd(), '..');
  const temporaryRoot = mkdtempSync(join(tmpdir(), 'p03-subject-target-'));
  const baseRoot = join(temporaryRoot, 'base');
  try {
    execFileSync('git', ['worktree', 'add', '--detach', baseRoot, FROZEN_PLAN_BASE_SHA], {
      cwd: repositoryRoot,
      stdio: 'ignore',
    });
    expect(P03_SUBJECT_CONTENT_PATHS).toContain(P03_SUBJECT_FEEDBACK_COMPONENT_PATH);
    expect(() => execFileSync(
      'git', ['cat-file', '-e', `${FROZEN_PLAN_BASE_SHA}:${P03_SUBJECT_FEEDBACK_COMPONENT_PATH}`],
      { cwd: repositoryRoot, stdio: 'ignore' },
    )).toThrow();
    await expect(loadStopFeedbackTarget({
      subjectRoot: baseRoot,
      subjectSha: FROZEN_PLAN_BASE_SHA,
      subjectTree: execFileSync('git', ['rev-parse', `${FROZEN_PLAN_BASE_SHA}^{tree}`], {
        cwd: repositoryRoot,
        encoding: 'utf8',
      }).trim(),
    })).rejects.toThrow(/detached subject feedback component/);
  } finally {
    execFileSync('git', ['worktree', 'remove', '--force', baseRoot], { cwd: repositoryRoot, stdio: 'ignore' });
    rmSync(temporaryRoot, { recursive: true, force: true });
  }
}, 30000);

it('binds the immutable BASE subject to the old final contract and every candidate subject to the new final contract', () => {
  expect(stopFeedbackContractForSubject(FROZEN_PLAN_BASE_SHA)).toEqual({
    confirmed: { message: '已发送中断请求', tone: 'success' },
    unconfirmed: { message: '中断当前执行失败：stop confirmation timed out', tone: 'warning' },
  });
  expect(stopFeedbackContractForSubject(CANDIDATE_SUBJECT_SHA)).toEqual({
    confirmed: { message: '已发送中断请求', tone: 'success' },
    unconfirmed: { message: '停止未确认，任务可能仍在运行', tone: 'warning' },
  });
  expect(() => stopFeedbackContractForSubject('HEAD')).toThrow(/full 40-character/);
});

it('measures the candidate pending-plus-final runtime with the candidate final contract', async () => {
  const harness = createStopFeedbackHarness({
    attachRuntime: attachCandidateRuntime,
    subjectSha: CANDIDATE_SUBJECT_SHA,
    target: testTarget(attachCandidateRuntime),
  });
  try {
    await expect(harness.measureConfirmed()).resolves.toBeGreaterThanOrEqual(0);
    await expect(harness.measureUnconfirmed()).resolves.toBeGreaterThanOrEqual(0);
  }
  finally {
    harness.destroy();
  }
});

it('fails closed when a candidate runtime emits the BASE final message', async () => {
  const harness = createStopFeedbackHarness({
    attachRuntime(runtime) {
      runtime.activeThreadRPC = async () => {
        runtime.notifyAction('中断当前执行失败：stop confirmation timed out', 'warning');
        return false;
      };
    },
    subjectSha: CANDIDATE_SUBJECT_SHA,
    target: testTarget(),
  });
  try {
    await expect(harness.measureUnconfirmed()).rejects.toThrow(/unexpected stop feedback/);
  }
  finally {
    harness.destroy();
  }
});

it('does not let pending feedback satisfy the final DOM commit', async () => {
  const harness = createStopFeedbackHarness({
    attachRuntime(runtime) {
      runtime.activeThreadRPC = async () => {
        runtime.notifyAction(STOP_FEEDBACK_PENDING.message, STOP_FEEDBACK_PENDING.tone);
        return true;
      };
    },
    subjectSha: CANDIDATE_SUBJECT_SHA,
    target: testTarget(),
  });
  try {
    await expect(harness.measureConfirmed()).rejects.toThrow(/did not commit to the React DOM: success:已发送中断请求/);
  }
  finally {
    harness.destroy();
  }
});

it('fails rather than recording a synchronous notifyAction callback when a DOM mutation suppresses feedback', async () => {
  const harness = createStopFeedbackHarness({
    attachRuntime: attachBaseRuntime,
    domMutation: { mode: 'suppress' },
    subjectSha: FROZEN_PLAN_BASE_SHA,
    target: testTarget(),
  });
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
  const harness = createStopFeedbackHarness({
    attachRuntime: attachBaseRuntime,
    domMutation: { delayMs, mode: 'delay' },
    subjectSha: FROZEN_PLAN_BASE_SHA,
    target: testTarget(),
  });
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

it('measures visible BASE stop feedback after warmup with five samples', async () => {
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
