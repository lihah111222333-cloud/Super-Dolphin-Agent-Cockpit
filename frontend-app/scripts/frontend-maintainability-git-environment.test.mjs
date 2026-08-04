import { execFileSync, spawn, spawnSync } from 'node:child_process';
import { once } from 'node:events';
import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import {
  REPOSITORY_LOCAL_GIT_ENV_KEYS,
  repositoryLocalGitEnvironment,
} from './runtime/git-environment.mjs';

const frontendRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const vitestPath = resolve(frontendRoot, 'node_modules', 'vitest', 'vitest.mjs');
const scorerPath = resolve(frontendRoot, 'scripts', 'frontend-maintainability-score.mjs');
const watchdogPath = resolve(frontendRoot, 'scripts', 'runtime', 'detached-subject-watchdog.mjs');
const gitEnvironment = (overrides = {}) => ({ ...repositoryLocalGitEnvironment(), ...overrides });

function git(repositoryRoot, args) {
  return execFileSync('git', args, {
    cwd: repositoryRoot,
    encoding: 'utf8',
    env: gitEnvironment(),
  }).trim();
}

function repositorySnapshot(repositoryRoot) {
  return {
    head: git(repositoryRoot, ['rev-parse', 'HEAD']),
    tree: git(repositoryRoot, ['rev-parse', 'HEAD^{tree}']),
    status: git(repositoryRoot, ['status', '--porcelain=v1']),
    name: git(repositoryRoot, ['config', '--local', '--get', 'user.name']),
    email: git(repositoryRoot, ['config', '--local', '--get', 'user.email']),
  };
}

describe('frontend maintainability Git environment isolation', () => {
  it('removes every repository-local variable declared by the installed Git', () => {
    const declaredKeys = execFileSync('git', ['rev-parse', '--local-env-vars'], {
      cwd: resolve(frontendRoot, '..'),
      encoding: 'utf8',
      env: repositoryLocalGitEnvironment(),
    }).split('\n').filter(Boolean);
    const source = Object.fromEntries(declaredKeys.map((key) => [key, `/hostile/${key}`]));
    source.PATH = process.env.PATH;
    source.FRONTEND_GIT_ENV_SENTINEL = 'preserved';

    const isolated = repositoryLocalGitEnvironment(source);

    declaredKeys.forEach((key) => expect(REPOSITORY_LOCAL_GIT_ENV_KEYS).toContain(key));
    declaredKeys.forEach((key) => expect(isolated).not.toHaveProperty(key));
    expect(isolated.PATH).toBe(process.env.PATH);
    expect(isolated.FRONTEND_GIT_ENV_SENTINEL).toBe('preserved');
    declaredKeys.forEach((key) => expect(source[key]).toBe(`/hostile/${key}`));
  });

  it('removes a detached subject when the watchdog starts after its parent died', () => {
    const repoRoot = mkdtempSync(join(tmpdir(), 'frontend-maintainability-watchdog-repo-'));
    const tempRoot = mkdtempSync(join(tmpdir(), 'frontend-maintainability-watchdog-subject-'));
    try {
      execFileSync('git', ['init', '-q'], { cwd: repoRoot, env: gitEnvironment() });
      const leasePath = join(tempRoot, '.cleanup-lease');
      writeFileSync(leasePath, 'dead parent\n');
      const result = spawnSync(process.execPath, [watchdogPath, JSON.stringify({
        leasePath,
        tempRoot,
        detachedRoot: join(tempRoot, 'repo'),
        repoRoot,
        parentPid: 2_147_483_647,
      })], { encoding: 'utf8', timeout: 5_000 });

      expect(result.status, result.stderr).toBe(0);
      expect(existsSync(tempRoot)).toBe(false);
    } finally {
      rmSync(repoRoot, { recursive: true, force: true });
      rmSync(tempRoot, { recursive: true, force: true });
    }
  });

  it('removes the detached worktree after its monitored parent is killed', async () => {
    const repoRoot = mkdtempSync(join(tmpdir(), 'frontend-maintainability-parent-death-repo-'));
    const tempRoot = mkdtempSync(join(tmpdir(), 'frontend-maintainability-parent-death-subject-'));
    let parent;
    let watchdog;
    try {
      execFileSync('git', ['init', '-q'], { cwd: repoRoot, env: gitEnvironment() });
      git(repoRoot, ['config', 'user.name', 'Watchdog Test']);
      git(repoRoot, ['config', 'user.email', 'watchdog@example.invalid']);
      git(repoRoot, ['commit', '--allow-empty', '-q', '-m', '测试：建立 watchdog 仓库']);
      const detachedRoot = join(tempRoot, 'repo');
      const leasePath = join(tempRoot, '.cleanup-lease');
      writeFileSync(leasePath, 'live parent\n');
      execFileSync('git', ['worktree', 'add', '-q', '--detach', detachedRoot, 'HEAD'], {
        cwd: repoRoot,
        env: gitEnvironment(),
      });
      parent = spawn(process.execPath, ['-e', 'setInterval(() => {}, 1000)'], { stdio: 'ignore' });
      watchdog = spawn(process.execPath, [watchdogPath, JSON.stringify({
        leasePath, tempRoot, detachedRoot, repoRoot, parentPid: parent.pid,
      })], { stdio: 'ignore' });
      expect(readFileSync(scorerPath, 'utf8')).toContain(
        'spawn(process.execPath, [watchdogScriptPath, payload]',
      );
      const watchdogExit = once(watchdog, 'exit');
      parent.kill('SIGKILL');
      await once(parent, 'exit');
      await watchdogExit;

      expect(existsSync(detachedRoot)).toBe(false);
      expect(git(repoRoot, ['worktree', 'list', '--porcelain'])).not.toContain(detachedRoot);
    } finally {
      if (parent?.exitCode === null && parent.signalCode === null) parent.kill('SIGKILL');
      if (watchdog?.exitCode === null && watchdog.signalCode === null) watchdog.kill('SIGKILL');
      rmSync(repoRoot, { recursive: true, force: true });
      rmSync(tempRoot, { recursive: true, force: true });
    }
  }, 45_000);

  it.each([
    ['GIT_DIR', false, false],
    ['GIT_DIR/GIT_WORK_TREE', true, false],
    ['GIT_INDEX_FILE', false, true],
    ['GIT_DIR/GIT_WORK_TREE/GIT_INDEX_FILE', true, true],
  ])('preserves an inherited %s sentinel while scoring a separate target', (_label, includeWorkTree, includeIndex) => {
    const sentinelRoot = mkdtempSync(join(tmpdir(), 'frontend-maintainability-git-env-sentinel-'));
    try {
      git(sentinelRoot, ['init', '-q']);
      git(sentinelRoot, ['config', 'user.name', 'Sentinel Owner']);
      git(sentinelRoot, ['config', 'user.email', 'sentinel-owner@example.invalid']);
      git(sentinelRoot, ['commit', '--allow-empty', '-q', '-m', '测试：建立外部 Git sentinel']);
      const before = repositorySnapshot(sentinelRoot);
      const alternateIndex = join(sentinelRoot, '.git', 'alternate-index');
      execFileSync('git', ['read-tree', 'HEAD'], {
        cwd: sentinelRoot,
        env: gitEnvironment({ GIT_INDEX_FILE: alternateIndex }),
      });
      const beforeIndex = readFileSync(alternateIndex);
      const beforeIndexEntries = execFileSync('git', ['ls-files', '--stage'], {
        cwd: sentinelRoot,
        encoding: 'utf8',
        env: gitEnvironment({ GIT_INDEX_FILE: alternateIndex }),
      });
      const overrides = includeIndex ? { GIT_INDEX_FILE: alternateIndex } : { GIT_DIR: join(sentinelRoot, '.git') };
      if (includeIndex && includeWorkTree) overrides.GIT_DIR = join(sentinelRoot, '.git');
      if (includeWorkTree) overrides.GIT_WORK_TREE = sentinelRoot;

      const result = spawnSync(process.execPath, [
        vitestPath,
        'run',
        'scripts/frontend-maintainability-score.test.mjs',
        '-t',
        'scores another Git target without loading a scorer from that target',
        '--maxWorkers=1',
        '--no-file-parallelism',
      ], {
        cwd: frontendRoot,
        encoding: 'utf8',
        env: gitEnvironment(overrides),
        timeout: 30_000,
      });

      expect(result.status, `${result.stdout}\n${result.stderr}`).toBe(0);
      expect(repositorySnapshot(sentinelRoot)).toEqual(before);
      expect(readFileSync(alternateIndex)).toEqual(beforeIndex);
      expect(execFileSync('git', ['ls-files', '--stage'], {
        cwd: sentinelRoot,
        encoding: 'utf8',
        env: gitEnvironment({ GIT_INDEX_FILE: alternateIndex }),
      })).toBe(beforeIndexEntries);
    }
    finally {
      rmSync(sentinelRoot, { recursive: true, force: true });
    }
  }, 40_000);
});
