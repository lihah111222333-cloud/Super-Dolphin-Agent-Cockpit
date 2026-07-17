import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { cwd } from 'node:process';
import { describe, expect, it } from 'vitest';
import {
  validateCaseRegistry,
  verifyPerformanceEvidence,
} from './performance-budget-runner.mjs';
import {
  CPU_DURATION_CLOCK,
  FEEDBACK_DURATION_CLOCK,
} from './performance-budget-model.mjs';

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

function evidence() {
  const historyCases = Object.fromEntries([
    'turns-200-tools-1',
    'turns-200-tools-3',
    'turns-1000-tools-1',
    'turns-1000-tools-3',
    'turns-5000-tools-1',
    'turns-5000-tools-3',
  ].map((caseId) => [caseId, timingCase(100)]));
  return {
    provenance: { runnerContentHash: 'runner-hash' },
    metrics: {
      'P01-render-isolation': {
        updateCount: 20,
        warmupUpdates: 2,
        mainPageUpdateCommits: 0,
        unrelatedSubtreeUpdateCommits: 0,
        mutationDetected: true,
      },
      'P02-history-budget': { cases: historyCases },
      'P03-feedback-budget': {
        cases: { 'stop-visible-feedback': timingCase(100, FEEDBACK_DURATION_CLOCK) },
      },
      'P04-resource-budget': {
        totalBundleBytes: 100,
        maxChunkBytes: 50,
      },
    },
  };
}

function baseline() {
  const frozenSampled = {
    status: 'PASS',
    subjectSha: 'a'.repeat(40),
    sampleCount: 5,
    warmupCount: 1,
    maxRegressionRatio: 1.15,
  };
  const current = evidence();
  return {
    provenance: { runnerContentHash: 'runner-hash' },
    metrics: {
      'P01-render-isolation': {
        status: 'PASS',
        subjectSha: 'a'.repeat(40),
        absoluteUpdateLimit: 1,
        updateCount: 20,
        warmupUpdates: 2,
        mainPageUpdateCommits: 0,
        unrelatedSubtreeUpdateCommits: 0,
      },
      'P02-history-budget': {
        ...frozenSampled,
        cases: current.metrics['P02-history-budget'].cases,
      },
      'P03-feedback-budget': {
        ...frozenSampled,
        cases: current.metrics['P03-feedback-budget'].cases,
      },
      'P04-resource-budget': {
        status: 'PASS',
        subjectSha: 'a'.repeat(40),
        maxRegressionRatio: 1.05,
        totalBundleBytes: 100,
        maxChunkBytes: 50,
      },
    },
  };
}

function registry() {
  return JSON.parse(readFileSync(join(cwd(), 'scripts/frontend-performance-cases.json'), 'utf8'));
}

describe('performance budget runner registry', () => {
  it('keeps the fixed plan threshold even when a baseline is hand-relaxed', () => {
    const relaxedThreshold = baseline();
    relaxedThreshold.metrics['P02-history-budget'].maxRegressionRatio = 1.25;
    const currentEvidence = evidence();
    expect(verifyPerformanceEvidence(currentEvidence, relaxedThreshold).verdicts
      .find(({ metricId }) => metricId === 'P02-history-budget').status).toBe('NOT_VERIFIED');
  });

  it('refuses a missing or changed frozen runner content hash', () => {
    const missing = baseline();
    delete missing.provenance;
    expect(verifyPerformanceEvidence(evidence(), missing).status).toBe('NOT_VERIFIED');

    const changed = baseline();
    changed.provenance.runnerContentHash = 'different-runner';
    expect(verifyPerformanceEvidence(evidence(), changed).status).toBe('NOT_VERIFIED');
  });

  it('derives twelve exact current-tree cases and rejects zero, missing, stale, and duplicate registrations', () => {
    const currentEvidence = evidence();
    const verdict = verifyPerformanceEvidence(currentEvidence, baseline());
    expect(verdict).toEqual(expect.objectContaining({
      status: 'PASS',
      testCount: 12,
    }));
    expect(verdict.caseIds).toEqual(registry().cases.map(({ caseId }) => caseId));

    const zero = { ...registry(), testCount: 0, cases: [] };
    expect(() => validateCaseRegistry(currentEvidence, verdict.verdicts, zero)).toThrow(/zero tests/);

    const missing = registry();
    missing.cases.pop();
    missing.testCount -= 1;
    expect(() => validateCaseRegistry(currentEvidence, verdict.verdicts, missing))
      .toThrow(/exact set mismatch/);

    const stale = registry();
    stale.cases.push({
      caseId: 'stale-performance-case',
      metricId: 'P04-resource-budget',
      evidenceKey: 'stale',
    });
    stale.testCount += 1;
    expect(() => validateCaseRegistry(currentEvidence, verdict.verdicts, stale))
      .toThrow(/exact set mismatch/);

    const duplicate = registry();
    duplicate.cases[1] = { ...duplicate.cases[0] };
    expect(() => validateCaseRegistry(currentEvidence, verdict.verdicts, duplicate))
      .toThrow(/duplicate caseIds/);
  });

  it('fails the exact history case that exceeds the frozen fifteen percent threshold', () => {
    const regressed = evidence();
    regressed.metrics['P02-history-budget'].cases['turns-5000-tools-3'] = timingCase(116);
    const verdict = verifyPerformanceEvidence(regressed, baseline());

    expect(verdict.status).toBe('FAIL');
    expect(verdict.caseResults.find(({ caseId }) => caseId === 'turns-5000-tools-3'))
      .toEqual(expect.objectContaining({ status: 'FAIL' }));
  });
});
