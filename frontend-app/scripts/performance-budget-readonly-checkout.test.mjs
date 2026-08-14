import { execFileSync } from 'node:child_process';
import {
  chmodSync,
  existsSync,
  mkdirSync,
  mkdtempSync,
  readdirSync,
  rmSync,
  writeFileSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { describe, expect, it, vi } from 'vitest';
import { collectDetachedP01P02Evidence } from './performance-budget-runner.mjs';

function setTreeMode(root, writable) {
  for (const entry of readdirSync(root, { withFileTypes: true })) {
    const path = join(root, entry.name);
    if (entry.isDirectory()) setTreeMode(path, writable);
    chmodSync(path, entry.isDirectory() ? (writable ? 0o755 : 0o555) : (writable ? 0o644 : 0o444));
  }
  chmodSync(root, writable ? 0o755 : 0o555);
}

function createReadOnlyRepository() {
  const repositoryRoot = mkdtempSync(join(tmpdir(), 'frontend-performance-readonly-source-'));
  mkdirSync(join(repositoryRoot, 'frontend-app', 'scripts'), { recursive: true });
  writeFileSync(join(repositoryRoot, 'frontend-app', 'package.json'), '{"name":"readonly-subject"}\n');
  writeFileSync(join(repositoryRoot, 'frontend-app', 'scripts', 'render-isolation-probe.test.jsx'), 'subject probe');
  const git = (args) => execFileSync('git', args, {
    cwd: repositoryRoot, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'],
  }).trim();
  for (const args of [
    ['init', '--quiet'],
    ['config', 'user.name', 'readonly-subject-test'],
    ['config', 'user.email', 'readonly-subject-test@example.invalid'],
    ['add', '.'],
    ['commit', '--quiet', '-m', 'readonly subject'],
  ]) git(args);
  return Object.freeze({
    repositoryRoot,
    subjectSha: git(['rev-parse', 'HEAD']),
    subjectTree: git(['rev-parse', 'HEAD^{tree}']),
  });
}

function commandResult() {
  return {
    error: undefined,
    outputTruncated: false,
    status: 0,
    stderr: '',
    stdout: '',
    timedOut: false,
  };
}

describe('read-only source-data subject materialization', () => {
  it('checks out an exact subject from the local object database without source writes', async () => {
    const fixture = createReadOnlyRepository();
    const commands = [];
    const sourceGitWorktrees = join(fixture.repositoryRoot, '.git', 'worktrees');
    const execute = (command, args, options) => {
      commands.push([command, ...args]);
      return execFileSync(command, args, options);
    };
    setTreeMode(fixture.repositoryRoot, false);
    try {
      const measured = await collectDetachedP01P02Evidence({
        ...fixture,
        repositoryRoot: fixture.repositoryRoot,
        execute,
        runCommand: vi.fn(async () => commandResult()),
        collectRender: vi.fn(({ frontendRoot }) => {
          expect(existsSync(join(frontendRoot, 'scripts/render-isolation-probe.test.jsx'))).toBe(true);
          return { metricId: 'P01-render-isolation', updateCount: 20 };
        }),
        loadHistoryTarget: vi.fn(async ({ subjectRoot, subjectSha, subjectTree }) => ({
          provenance: { subjectRoot, subjectSha, subjectTree },
        })),
        runHistory: vi.fn(({ commit, target }) => ({
          metricId: 'P02-history-budget', subjectSha: commit, subjectProduct: target.provenance,
        })),
      });
      expect(measured.historyBudget.subjectSha).toBe(fixture.subjectSha);
      expect(measured.renderIsolation.subjectProduct).toEqual(expect.objectContaining({
        subjectSha: fixture.subjectSha, subjectTree: fixture.subjectTree,
      }));
      expect(commands[0]).toEqual([
        'git', 'clone', '--local', '--no-hardlinks', '--no-checkout', '--no-tags',
        '--no-recurse-submodules', fixture.repositoryRoot, expect.any(String),
      ]);
      expect(commands[1]).toEqual(['git', 'checkout', '--detach', fixture.subjectSha]);
      expect(existsSync(sourceGitWorktrees)).toBe(false);
      expect(execFileSync('git', ['status', '--porcelain', '--untracked-files=all'], {
        cwd: fixture.repositoryRoot, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'],
      }).trim()).toBe('');
    } finally {
      setTreeMode(fixture.repositoryRoot, true);
      rmSync(fixture.repositoryRoot, { recursive: true, force: true });
    }
  });

  it('fails closed and removes the temporary clone when clone or checkout fails', async () => {
    for (const failedStage of ['clone', 'checkout']) {
      const commands = [];
      let temporaryRoot = '';
      const execute = (command, args) => {
        commands.push([command, ...args]);
        if (command === 'git' && args[0] === 'clone') {
          temporaryRoot = args.at(-1);
          if (failedStage === 'clone') throw new Error('local clone failed');
          mkdirSync(join(temporaryRoot, 'frontend-app', 'scripts'), { recursive: true });
          writeFileSync(join(temporaryRoot, 'frontend-app', 'scripts', 'render-isolation-probe.test.jsx'), 'subject probe');
          return '';
        }
        if (command === 'git' && args[0] === 'checkout' && failedStage === 'checkout') throw new Error('detached checkout failed');
        if (command === 'git' && args[0] === 'checkout') return '';
        if (command === 'git' && args.join(' ') === 'rev-parse HEAD') return 'a'.repeat(40);
        if (command === 'git' && args.join(' ') === 'rev-parse HEAD^{tree}') return '1'.repeat(40);
        if (command === 'git' && args.join(' ') === 'status --porcelain --untracked-files=all') return '';
        throw new Error(`unexpected command: ${command} ${args.join(' ')}`);
      };
      await expect(collectDetachedP01P02Evidence({
        subjectSha: 'a'.repeat(40), subjectTree: '1'.repeat(40), repositoryRoot: '/readonly-source-data', execute,
      })).rejects.toThrow(failedStage);
      expect(existsSync(temporaryRoot)).toBe(false);
      expect(commands.some((command) => command.includes('worktree'))).toBe(false);
    }
  });
});
