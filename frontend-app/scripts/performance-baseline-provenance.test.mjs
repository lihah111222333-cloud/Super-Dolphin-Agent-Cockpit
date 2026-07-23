import { execFileSync, spawnSync } from 'node:child_process';
import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { resolve } from 'node:path';
import { cwd } from 'node:process';
import { describe, expect, it } from 'vitest';
import {
  FROZEN_PLAN_BASE_SHA,
  analyzePerformanceBaselineProvenance,
  assertPerformanceBaselineProvenance,
} from './performance-baseline-provenance.mjs';

const REPOSITORY_ROOT = resolve(cwd(), '..');
const SCRIPT_PATH = resolve(cwd(), 'scripts/performance-baseline-provenance.mjs');

describe('performance baseline provenance', () => {
  it('pins the frozen plan BASE_SHA and accepts the frozen baseline commit', () => {
    expect(FROZEN_PLAN_BASE_SHA).toBe('314a8e240b2fe58de23651a00b74f05c985cf5e4');
    const result = assertPerformanceBaselineProvenance({
      repositoryRoot: REPOSITORY_ROOT,
      baselineBaseSha: FROZEN_PLAN_BASE_SHA,
    });
    expect(result).toMatchObject({ valid: true, changedPaths: [], forbiddenPaths: [] });
  });

  it('allows only runner, audit, plan, and generated map changes', () => withFixtureRepository((repositoryRoot, planBaseSha) => {
    const baselineBaseSha = commit(repositoryRoot, {
      'frontend-app/scripts/performance-budget-runner.mjs': 'runner v2\n',
      'docs/plans/2026-07-15-frontend-maintainability-error-discoverability-90-plan.md': 'plan v2\n',
      'docs/doc/codemap/project-map/index/app-ui.tsv': 'generated map\n',
    }, 'runner-only');
    const result = assertPerformanceBaselineProvenance({ repositoryRoot, planBaseSha, baselineBaseSha });
    expect(result).toMatchObject({ valid: true, forbiddenPaths: [] });
    expect(result.changedPaths).toEqual([
      'docs/doc/codemap/project-map/index/app-ui.tsv',
      'docs/plans/2026-07-15-frontend-maintainability-error-discoverability-90-plan.md',
      'frontend-app/scripts/performance-budget-runner.mjs',
    ]);
  }));

  it('allows only the exact audited feedback component path', () => withFixtureRepository((repositoryRoot, planBaseSha) => {
    const baselineBaseSha = commit(repositoryRoot, {
      'frontend-app/src/pages/chat/components/ChatActionFeedback.js': 'audited component\n',
    }, 'audited-feedback-component');
    expect(assertPerformanceBaselineProvenance({ repositoryRoot, planBaseSha, baselineBaseSha }))
      .toMatchObject({ valid: true, forbiddenPaths: [] });
  }));

  it.each([
    'frontend-app/src/pages/chat/ThreadPage.jsx',
    'frontend-app/src/pages/chat/ChatPage.jsx',
    'frontend-app/src/pages/chat/components/ChatActionFailureSink.js',
    'cmd/agent-terminal/main.go',
    'internal/ui/wails/bridge.go',
    'frontend-app/package.json',
    'frontend-app/package-lock.json',
  ])('rejects a product or dependency path: %s', (forbiddenPath) => withFixtureRepository((repositoryRoot, planBaseSha) => {
    const baselineBaseSha = commit(repositoryRoot, { [forbiddenPath]: 'forbidden\n' }, 'forbidden-change');
    const result = analyzePerformanceBaselineProvenance({ repositoryRoot, planBaseSha, baselineBaseSha });
    expect(result).toMatchObject({ valid: false, forbiddenPaths: [forbiddenPath] });
    expect(() => assertPerformanceBaselineProvenance({ repositoryRoot, planBaseSha, baselineBaseSha }))
      .toThrow(/forbidden product or dependency path/);
  }));

  it('fails closed for a non-descendant baseline and exposes the same result through the CLI', () => withFixtureRepository((repositoryRoot, planBaseSha) => {
    git(repositoryRoot, ['checkout', '-b', 'side', `${planBaseSha}^`]);
    const baselineBaseSha = commit(repositoryRoot, {
      'frontend-app/scripts/performance-budget-runner.mjs': 'side runner\n',
    }, 'side-runner');
    const result = analyzePerformanceBaselineProvenance({ repositoryRoot, planBaseSha, baselineBaseSha });
    expect(result).toMatchObject({ valid: false, changedPaths: [], forbiddenPaths: [] });
    expect(result.reason).toMatch(/must descend/);

    const cli = spawnSync(process.execPath, [SCRIPT_PATH, '--repo', repositoryRoot, '--plan-base', planBaseSha, '--baseline-base', baselineBaseSha], {
      encoding: 'utf8',
    });
    expect(cli.status).toBe(1);
    expect(JSON.parse(cli.stdout)).toMatchObject({ valid: false, reason: expect.stringMatching(/must descend/) });
  }));
});

function withFixtureRepository(testCase) {
  const repositoryRoot = mkdtempSync(resolve(tmpdir(), 'performance-baseline-provenance-'));
  try {
    git(repositoryRoot, ['init', '--initial-branch=main']);
    git(repositoryRoot, ['config', 'user.email', 'tests@example.invalid']);
    git(repositoryRoot, ['config', 'user.name', 'Performance Baseline Tests']);
    commit(repositoryRoot, { 'README.md': 'root\n' }, 'root');
    const planBaseSha = commit(repositoryRoot, { 'README.md': 'base\n' }, 'base');
    return testCase(repositoryRoot, planBaseSha);
  } finally {
    rmSync(repositoryRoot, { recursive: true, force: true });
  }
}

function commit(repositoryRoot, files, message) {
  for (const [relativePath, content] of Object.entries(files)) {
    const path = resolve(repositoryRoot, relativePath);
    mkdirSync(resolve(path, '..'), { recursive: true });
    writeFileSync(path, content);
  }
  git(repositoryRoot, ['add', '--all']);
  git(repositoryRoot, ['commit', '-m', message]);
  return git(repositoryRoot, ['rev-parse', 'HEAD']).trim();
}

function git(repositoryRoot, args) {
  return execFileSync('git', args, { cwd: repositoryRoot, encoding: 'utf8' });
}
