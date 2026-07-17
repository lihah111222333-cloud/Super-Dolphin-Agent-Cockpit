import { execFileSync } from 'node:child_process';
import { resolve } from 'node:path';
import { cwd } from 'node:process';
import { describe, expect, it } from 'vitest';
import {
  RUNNER_CONTENT_PATHS,
  collectEvidenceProvenance,
  runnerContentEvidence,
  validateBaselineAuditDiff,
} from './evidence-provenance.mjs';

const REPOSITORY_ROOT = resolve(cwd(), '..');

describe('evidence provenance', () => {
  it('hashes the exact runner file manifest deterministically', () => {
    const first = runnerContentEvidence(REPOSITORY_ROOT);
    const second = runnerContentEvidence(REPOSITORY_ROOT);
    expect(first).toEqual(second);
    expect(first.runnerFiles.map(({ path }) => path)).toEqual(RUNNER_CONTENT_PATHS);
    expect(first.runnerContentHash).toMatch(/^[0-9a-f]{64}$/);
    first.runnerFiles.forEach(({ sha256 }) => expect(sha256).toMatch(/^[0-9a-f]{64}$/));
  });

  it('rejects duplicate, unsorted, and forbidden baseline audit changes', () => {
    expect(validateBaselineAuditDiff(['frontend-app/scripts/evidence-provenance.test.mjs']))
      .toEqual(['frontend-app/scripts/evidence-provenance.test.mjs']);
    expect(() => validateBaselineAuditDiff(['frontend-app/package.json', 'frontend-app/package.json']))
      .toThrow(/exact, unique, and sorted/);
    expect(() => validateBaselineAuditDiff([
      'frontend-app/scripts/performance-budget-runner.mjs',
      'frontend-app/package.json',
    ])).toThrow(/exact, unique, and sorted/);
    expect(() => validateBaselineAuditDiff(['frontend-app/src/App.jsx']))
      .toThrow(/forbidden path/);
  });

  it('separates subject identity from runner identity and records the environment', () => {
    const { generatedAt, environment, provenance, subjectTree } = collectEvidenceProvenance({
      repositoryRoot: REPOSITORY_ROOT,
      runnerId: 'unit-provenance',
      subjectSha: provenanceHead(),
    });
    expect(subjectTree).toMatch(/^[0-9a-f]{40}$/);
    expect(Number.isNaN(Date.parse(generatedAt))).toBe(false);
    expect(environment).toEqual(expect.objectContaining({
      node: expect.stringMatching(/^v/),
      npm: expect.any(String),
      go: expect.stringMatching(/^go version /),
    }));
    expect(provenance).toEqual(expect.objectContaining({
      runnerId: 'unit-provenance',
      runnerSha: provenanceHead(),
      runnerTree: subjectTree,
      runnerContentHash: expect.stringMatching(/^[0-9a-f]{64}$/),
      baselineAudit: null,
    }));
  });
});

function provenanceHead() {
  return execFileSync('git', ['rev-parse', 'HEAD'], {
    cwd: REPOSITORY_ROOT,
    encoding: 'utf8',
  }).trim();
}
