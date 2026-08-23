import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { arch, cpus, loadavg, platform, release, totalmem } from 'node:os';
import { resolve } from 'node:path';
import process from 'node:process';
import { repositoryLocalGitEnvironment } from './runtime/git-environment.mjs';

function commandOutput(command, args, cwd) {
  const output = execFileSync(command, args, {
    cwd,
    encoding: 'utf8',
    env: repositoryLocalGitEnvironment(),
    stdio: ['ignore', 'pipe', 'pipe'],
  }).trim();
  if (!output) throw new Error(`${command} ${args.join(' ')} returned empty output`);
  return output;
}

function requireFullSha(value, label) {
  if (!/^[0-9a-f]{40}$/.test(value || '')) throw new TypeError(`${label} must be a full 40-character Git SHA`);
  return value;
}

function runnerContentEvidence(repositoryRoot, runnerContentPaths) {
  if (!Array.isArray(runnerContentPaths) || runnerContentPaths.length === 0
    || new Set(runnerContentPaths).size !== runnerContentPaths.length) {
    throw new TypeError('runnerContentPaths must be a non-empty unique array');
  }
  const files = runnerContentPaths.map((path) => Object.freeze({
    path,
    sha256: createHash('sha256').update(readFileSync(resolve(repositoryRoot, path))).digest('hex'),
  }));
  const aggregate = createHash('sha256');
  files.forEach(({ path, sha256 }) => aggregate.update(`${path}\0${sha256}\n`));
  return Object.freeze({ runnerContentHash: aggregate.digest('hex'), runnerFiles: Object.freeze(files) });
}

function collectEvidenceProvenance({
  repositoryRoot,
  runnerRepositoryRoot = repositoryRoot,
  runnerContentPaths,
  runnerId,
  subjectSha,
}) {
  if (typeof runnerId !== 'string' || runnerId.length === 0) throw new TypeError('runnerId is required');
  if (!Array.isArray(runnerContentPaths) || runnerContentPaths.length === 0) {
    throw new TypeError('runnerContentPaths is required');
  }
  requireFullSha(subjectSha, 'subjectSha');
  const subjectGit = (args) => commandOutput('git', args, repositoryRoot);
  const runnerGit = (args) => commandOutput('git', args, runnerRepositoryRoot);
  const runnerSha = requireFullSha(runnerGit(['rev-parse', 'HEAD']), 'runnerSha');
  const runnerTree = requireFullSha(runnerGit(['rev-parse', 'HEAD^{tree}']), 'runnerTree');
  const subjectTree = requireFullSha(subjectGit(['rev-parse', `${subjectSha}^{tree}`]), 'subjectTree');
  const status = execFileSync('git', ['status', '--porcelain', '--untracked-files=all'], {
    cwd: runnerRepositoryRoot,
    encoding: 'utf8',
    env: repositoryLocalGitEnvironment(),
    stdio: ['ignore', 'pipe', 'pipe'],
  }).trim();
  if (subjectSha !== runnerSha) {
    if (status) throw new Error('provenance runner requires a clean worktree');
    const ancestry = [runnerSha, subjectSha];
    execFileSync('git', ['merge-base', '--is-ancestor', ...ancestry], {
      cwd: repositoryRoot,
      env: repositoryLocalGitEnvironment(),
      stdio: ['ignore', 'ignore', 'pipe'],
    });
  }
  const cpuList = cpus();
  if (cpuList.length === 0 || !cpuList[0]?.model) throw new Error('CPU metadata is unavailable');
  return Object.freeze({
    subjectTree,
    generatedAt: new Date().toISOString(),
    environment: Object.freeze({
      os: Object.freeze({ platform: platform(), release: release(), arch: arch() }),
      cpu: Object.freeze({ model: cpuList[0].model, logicalCores: cpuList.length }),
      totalMemoryBytes: totalmem(),
      loadAverage: Object.freeze(loadavg()),
      node: process.version,
      npm: commandOutput('npm', ['--version'], repositoryRoot),
      go: commandOutput('go', ['version'], repositoryRoot),
    }),
    provenance: Object.freeze({
      runnerId,
      runnerSha,
      runnerTree,
      ...runnerContentEvidence(runnerRepositoryRoot, runnerContentPaths),
      worktreeClean: status.length === 0,
      worktreeStatus: Object.freeze(status ? status.split('\n') : []),
    }),
  });
}

export {
  collectEvidenceProvenance,
  runnerContentEvidence,
};
