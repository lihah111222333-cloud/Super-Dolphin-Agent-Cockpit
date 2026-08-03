import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';
import { repositoryLocalGitEnvironment } from './git-environment.mjs';

function pruneWorktrees(repoRoot) {
  execFileSync('git', ['worktree', 'prune'], {
    cwd: repoRoot,
    env: repositoryLocalGitEnvironment(),
    stdio: 'ignore',
  });
}

function removeDetachedWorktree(context) {
  if (!fs.existsSync(context.detachedRoot)) {
    pruneWorktrees(context.repoRoot);
    return;
  }
  try {
    execFileSync('git', ['worktree', 'remove', '--force', context.detachedRoot], {
      cwd: context.repoRoot,
      env: repositoryLocalGitEnvironment(),
      stdio: 'ignore',
    });
  } catch {
    fs.rmSync(context.detachedRoot, { recursive: true, force: true });
    pruneWorktrees(context.repoRoot);
  }
}

function cleanupDetachedSubject(context) {
  if (!fs.existsSync(context.leasePath)) return;
  try {
    removeDetachedWorktree(context);
  } finally {
    fs.rmSync(context.tempRoot, { recursive: true, force: true });
  }
}

function requireWatchdogContext(context) {
  const pathKeys = ['leasePath', 'tempRoot', 'detachedRoot', 'repoRoot'];
  if (!context || typeof context !== 'object' || !Number.isSafeInteger(context.parentPid)
    || context.parentPid < 1 || pathKeys.some((key) => typeof context[key] !== 'string' || !context[key])) {
    throw new TypeError('detached subject watchdog context is invalid');
  }
  return context;
}

export function runDetachedSubjectWatchdog(input) {
  const context = requireWatchdogContext(input);
  const timer = setInterval(() => {
    if (!fs.existsSync(context.leasePath)) process.exit(0);
    try {
      process.kill(context.parentPid, 0);
    } catch (error) {
      if (error.code !== 'ESRCH') return;
      clearInterval(timer);
      cleanupDetachedSubject(context);
      process.exit(0);
    }
  }, 100);
}

const scriptPath = fileURLToPath(import.meta.url);
if (process.argv[1] && path.resolve(process.argv[1]) === scriptPath) {
  try {
    runDetachedSubjectWatchdog(JSON.parse(process.argv[2] || ''));
  } catch (error) {
    process.stderr.write(`detached subject watchdog failed: ${error.message}\n`);
    process.exit(1);
  }
}
