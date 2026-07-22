import { execFileSync } from 'node:child_process';
import {
  mkdirSync,
  chmodSync,
  existsSync,
  mkdtempSync,
  readFileSync,
  readdirSync,
  rmSync,
  writeFileSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import { join, sep } from 'node:path';
import { platform } from 'node:process';
import { describe, expect, it } from 'vitest';

import { runFailureMatrix } from './failure-matrix-runner.mjs';

const describePosix = platform === 'win32' ? describe.skip : describe;

function processIsGone(pid) {
  try {
    process.kill(pid, 0);
    return false;
  }
  catch (error) {
    return error.code === 'ESRCH';
  }
}

async function waitForProcessesToExit({ parent, child }) {
  const deadline = Date.now() + 5_000;
  while ((!processIsGone(parent) || !processIsGone(child)) && Date.now() < deadline) {
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  expect(processIsGone(parent)).toBe(true);
  expect(processIsGone(child)).toBe(true);
}

function worktreePaths(repoRoot) {
  const output = execFileSync('git', ['worktree', 'list', '--porcelain'], {
    cwd: repoRoot,
    encoding: 'utf8',
  });
  return output.split('\n').flatMap((line) => (
    line.startsWith('worktree ') ? [line.slice('worktree '.length)] : []
  ));
}

function createFixtureRepository(root) {
  const repoRoot = join(root, 'repository');
  mkdirSync(join(repoRoot, 'frontend-app', 'node_modules'), { recursive: true });
  writeFileSync(join(repoRoot, 'frontend-app', 'placeholder.txt'), 'fixture\n');
  execFileSync('git', ['init', '--quiet'], { cwd: repoRoot });
  execFileSync('git', ['add', '.'], { cwd: repoRoot });
  execFileSync('git', [
    '-c', 'user.name=Lifecycle Test',
    '-c', 'user.email=lifecycle@example.invalid',
    'commit', '--quiet', '-m', 'fixture',
  ], { cwd: repoRoot });
  return repoRoot;
}

function isWithin(root, candidate) {
  return candidate === root || candidate.startsWith(`${root}${sep}`);
}

function isManagedWorktree(root, repoRoot, candidate) {
  return candidate !== repoRoot && isWithin(root, candidate);
}

function writeHangingVitest(root) {
  const scriptPath = join(root, 'hanging-vitest.cjs');
  writeFileSync(scriptPath, `#!/usr/bin/env node
const { spawn } = require('node:child_process');
const { writeFileSync } = require('node:fs');
const child = spawn(process.execPath, ['-e', 'setInterval(() => {}, 1000)'], { stdio: 'ignore' });
writeFileSync(process.env.RUNNER_PID_PATH, JSON.stringify({ parent: process.pid, child: child.pid }));
setInterval(() => {}, 1000);
`);
  chmodSync(scriptPath, 0o755);
  return scriptPath;
}

function writeFailureDocuments(root) {
  const caseIds = Array.from({ length: 24 }, (_, index) => `FM-${String(index + 1).padStart(2, '0')}`);
  const manifestPath = join(root, 'manifest.json');
  const fixturesPath = join(root, 'fixtures.json');
  const mutationsPath = join(root, 'mutations.json');
  writeFileSync(manifestPath, JSON.stringify({
    schemaVersion: 1,
    cases: caseIds.map((caseId) => ({
      caseId,
      subject: `${caseId} timeout fixture`,
      status: 'covered',
      requiredLayers: ['frontend'],
    })),
  }));
  writeFileSync(fixturesPath, JSON.stringify({
    schemaVersion: 1,
    fixtures: caseIds.map((caseId) => ({ caseId, expected: `${caseId} fixture` })),
  }));
  writeFileSync(mutationsPath, JSON.stringify({
    schemaVersion: 1,
    mutations: [{
      id: 'timeout-fixture',
      layer: 'frontend',
      sourcePath: 'frontend-app/src/timeout-fixture.js',
      search: 'before',
      replacement: 'after',
      caseIds,
    }],
  }));
  return { manifestPath, fixturesPath, mutationsPath };
}

function cleanupArtifacts(root, repoRoot, pidPath) {
  if (existsSync(pidPath)) {
    const { parent, child } = JSON.parse(readFileSync(pidPath, 'utf8'));
    for (const pid of [parent, child]) {
      if (processIsGone(pid)) continue;
      try {
        process.kill(pid, 'SIGKILL');
      }
      catch (error) {
        if (error.code !== 'ESRCH') throw error;
      }
    }
  }
  for (const worktreePath of worktreePaths(repoRoot).filter((candidate) => (
    isManagedWorktree(root, repoRoot, candidate)
  ))) {
    execFileSync('git', ['worktree', 'remove', '--force', worktreePath], {
      cwd: repoRoot,
      encoding: 'utf8',
    });
  }
  rmSync(root, { recursive: true, force: true });
}

async function expectTimedOutRunner(runner, root, repoRoot, pidPath, runnerPrefix) {
  await expect(runner()).rejects.toThrow(/managed command timed out after/u);
  const pids = JSON.parse(readFileSync(pidPath, 'utf8'));
  await waitForProcessesToExit(pids);
  expect(readdirSync(root).filter((entry) => entry.startsWith(runnerPrefix))).toEqual([]);
  expect(worktreePaths(repoRoot).some((candidate) => (
    isManagedWorktree(root, repoRoot, candidate)
  ))).toBe(false);
}

describePosix('evidence runner lifecycle', () => {
  it('cleans failure-matrix worktrees and descendants after a command timeout', async () => {
    const root = mkdtempSync(join(tmpdir(), 'failure-runner-lifecycle-'));
    const repoRoot = createFixtureRepository(root);
    const pidPath = join(root, 'failure-pids.json');
    try {
      const vitestCommand = writeHangingVitest(root);
      const documents = writeFailureDocuments(root);
      await expectTimedOutRunner(() => runFailureMatrix({
        repoRoot,
        ...documents,
        tempDirectory: root,
        vitestCommand,
        commandEnv: { ...process.env, RUNNER_PID_PATH: pidPath },
        commandTimeoutMs: 1_000,
        commandKillGraceMs: 100,
      }), root, repoRoot, pidPath, 'failure-matrix-');
    }
    finally {
      cleanupArtifacts(root, repoRoot, pidPath);
    }
  }, 15_000);
});
