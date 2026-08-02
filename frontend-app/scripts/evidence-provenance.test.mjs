import { execFileSync } from 'node:child_process';
import {
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import { resolve } from 'node:path';
import { cwd } from 'node:process';
import { describe, expect, it } from 'vitest';
import {
  BASELINE_AUDIT_ALLOWED_PATHS,
  RUNNER_CONTENT_PATHS,
  collectEvidenceProvenance,
  runnerContentEvidence,
  validateBaselineAuditDiff,
} from './evidence-provenance.mjs';
import { isAllowedPerformanceBaselinePath } from './performance-baseline-provenance.mjs';
import { repositoryLocalGitEnvironment } from './runtime/git-environment.mjs';
import {
  P03_SUBJECT_FEEDBACK_COMPONENT_PATH,
  P03_SUBJECT_CONTENT_PATHS,
  subjectContentEvidence,
} from './stop-feedback-benchmark.mjs';

const REPOSITORY_ROOT = resolve(cwd(), '..');
const FROZEN_PLAN_PATH = 'docs/plans/2026-07-15-frontend-maintainability-error-discoverability-90-plan.md';
const P03_SUBJECT_RUNTIME_CONTENT_PATHS = Object.freeze([
  'frontend-app/src/entities/client/model/contractStoreModel.js',
  'frontend-app/src/entities/client/model/threadLifecycleRuntime.js',
  P03_SUBJECT_FEEDBACK_COMPONENT_PATH,
]);

describe('evidence provenance', () => {
  it('hashes the exact runner file manifest deterministically', () => {
    const first = runnerContentEvidence(REPOSITORY_ROOT);
    const second = runnerContentEvidence(REPOSITORY_ROOT);
    expect(first).toEqual(second);
    expect(first.runnerFiles.map(({ path }) => path)).toEqual(RUNNER_CONTENT_PATHS);
    expect(RUNNER_CONTENT_PATHS).not.toContain('frontend-app/package.json');
    expect(first.runnerContentHash).toMatch(/^[0-9a-f]{64}$/);
    first.runnerFiles.forEach(({ sha256 }) => expect(sha256).toMatch(/^[0-9a-f]{64}$/));
  });

  it('partitions P03 subject runtime content from runner content', () => {
    const runnerFiles = runnerContentEvidence(REPOSITORY_ROOT).runnerFiles.map(({ path }) => path);
    const subjectFiles = subjectContentEvidence(REPOSITORY_ROOT).files.map(({ path }) => path);

    expect(runnerFiles).not.toContain(P03_SUBJECT_FEEDBACK_COMPONENT_PATH);
    expect(P03_SUBJECT_CONTENT_PATHS).toContain(P03_SUBJECT_FEEDBACK_COMPONENT_PATH);
    expect(subjectFiles).toEqual(expect.arrayContaining(P03_SUBJECT_RUNTIME_CONTENT_PATHS));
    P03_SUBJECT_RUNTIME_CONTENT_PATHS.forEach((path) => {
      expect(runnerFiles).not.toContain(path);
      expect(BASELINE_AUDIT_ALLOWED_PATHS).toContain(path);
    });
  });

  it('changes the subject content hash when the subject feedback component changes', () => {
    const temporaryRoot = mkdtempSync(resolve(tmpdir(), 'subject-content-hash-'));
    try {
      for (const subjectPath of P03_SUBJECT_CONTENT_PATHS) {
        const sourcePath = resolve(REPOSITORY_ROOT, subjectPath);
        const targetPath = resolve(temporaryRoot, subjectPath);
        mkdirSync(resolve(targetPath, '..'), { recursive: true });
        writeFileSync(targetPath, readFileSync(sourcePath));
      }
      const first = subjectContentEvidence(temporaryRoot);
      const feedbackPath = resolve(temporaryRoot, P03_SUBJECT_FEEDBACK_COMPONENT_PATH);
      writeFileSync(feedbackPath, `${readFileSync(feedbackPath, 'utf8')}\n// hash mutation\n`);
      const second = subjectContentEvidence(temporaryRoot);
      expect(first.files.map(({ path }) => path)).toEqual(expect.arrayContaining([
        P03_SUBJECT_FEEDBACK_COMPONENT_PATH,
      ]));
      expect(second.contentHash).not.toBe(first.contentHash);
    } finally {
      rmSync(temporaryRoot, { recursive: true, force: true });
    }
  });

  it('rejects duplicate, unsorted, and forbidden baseline audit changes', () => {
    expect(validateBaselineAuditDiff([
      'docs/doc/codemap/README.md',
      'docs/doc/codemap/ai-index.json',
    ])).toEqual([
      'docs/doc/codemap/README.md',
      'docs/doc/codemap/ai-index.json',
    ]);
    expect(validateBaselineAuditDiff(['frontend-app/scripts/evidence-provenance.test.mjs']))
      .toEqual(['frontend-app/scripts/evidence-provenance.test.mjs']);
    expect(validateBaselineAuditDiff(['frontend-app/src/entities/client/model/threadLifecycleRuntime.test.js']))
      .toEqual(['frontend-app/src/entities/client/model/threadLifecycleRuntime.test.js']);
    expect(validateBaselineAuditDiff(['frontend-app/src/pages/chat/components/ChatActionFeedback.js']))
      .toEqual(['frontend-app/src/pages/chat/components/ChatActionFeedback.js']);
    expect(() => validateBaselineAuditDiff(['frontend-app/package.json', 'frontend-app/package.json']))
      .toThrow(/exact, unique, and sorted/);
    expect(() => validateBaselineAuditDiff([
      'frontend-app/scripts/performance-budget-runner.mjs',
      'frontend-app/package.json',
    ])).toThrow(/exact, unique, and sorted/);
    expect(() => validateBaselineAuditDiff(['frontend-app/src/App.jsx']))
      .toThrow(/forbidden path/);
    expect(() => validateBaselineAuditDiff(['frontend-app/src/pages/chat/ThreadPage.jsx']))
      .toThrow(/forbidden path/);
    expect(() => validateBaselineAuditDiff(['frontend-app/src/pages/chat/ChatPage.jsx']))
      .toThrow(/forbidden path/);
    expect(() => validateBaselineAuditDiff(['frontend-app/src/pages/chat/components/ChatActionFailureSink.js']))
      .toThrow(/forbidden path/);
    expect(() => validateBaselineAuditDiff(['frontend-app/src/entities/client/model/threadLifecycleRuntime.test.js.bak']))
      .toThrow(/forbidden path/);
  });

  it('keeps the exact frozen plan audit path aligned with baseline provenance', () => {
    expect(validateBaselineAuditDiff([FROZEN_PLAN_PATH])).toEqual([FROZEN_PLAN_PATH]);
    expect(isAllowedPerformanceBaselinePath(FROZEN_PLAN_PATH)).toBe(true);

    [
      'docs/plans/2026-07-15-frontend-maintainability-error-discoverability-90-plan-copy.md',
      'docs/plans/2026-07-15-frontend-maintainability-error-discoverability-90-plan.md.fake',
    ].forEach((forbiddenPath) => {
      expect(() => validateBaselineAuditDiff([forbiddenPath])).toThrow(/forbidden path/);
      expect(isAllowedPerformanceBaselinePath(forbiddenPath)).toBe(false);
    });
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

  it('does not mutate an inherited alternate index while collecting Git evidence', () => {
    const temporaryRoot = mkdtempSync(resolve(tmpdir(), 'evidence-provenance-index-'));
    const alternateIndex = resolve(temporaryRoot, 'index');
    const subjectSha = provenanceHead();
    execFileSync('git', ['read-tree', 'HEAD'], {
      cwd: REPOSITORY_ROOT,
      env: { ...repositoryLocalGitEnvironment(), GIT_INDEX_FILE: alternateIndex },
    });
    const before = readFileSync(alternateIndex);
    const inherited = process.env.GIT_INDEX_FILE;
    process.env.GIT_INDEX_FILE = alternateIndex;
    try {
      expect(collectEvidenceProvenance({
        repositoryRoot: REPOSITORY_ROOT,
        runnerId: 'alternate-index-proof',
        subjectSha,
      }).subjectTree).toMatch(/^[0-9a-f]{40}$/u);
      expect(readFileSync(alternateIndex)).toEqual(before);
    } finally {
      if (inherited === undefined) delete process.env.GIT_INDEX_FILE;
      else process.env.GIT_INDEX_FILE = inherited;
      rmSync(temporaryRoot, { recursive: true, force: true });
    }
  });
});

function provenanceHead() {
  return execFileSync('git', ['rev-parse', 'HEAD'], {
    cwd: REPOSITORY_ROOT,
    encoding: 'utf8',
  }).trim();
}
