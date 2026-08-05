import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { arch, cpus, loadavg, platform, release, totalmem } from 'node:os';
import { resolve } from 'node:path';
import process from 'node:process';
import { repositoryLocalGitEnvironment } from './runtime/git-environment.mjs';

const RUNNER_CONTENT_PATHS = Object.freeze([
  'frontend-app/scripts/chat-history-benchmark.mjs',
  'frontend-app/scripts/evidence-provenance.mjs',
  'frontend-app/scripts/frontend-performance-cases.json',
  'frontend-app/scripts/runtime/git-environment.mjs',
  'frontend-app/scripts/managed-command.mjs',
  'frontend-app/scripts/performance-baseline-provenance.mjs',
  'frontend-app/scripts/performance-budget-config.mjs',
  'frontend-app/scripts/performance-budget-model.mjs',
  'frontend-app/scripts/performance-budget-runner.mjs',
  'frontend-app/scripts/render-isolation-probe.test.jsx',
  'frontend-app/scripts/resource-budget.mjs',
  'frontend-app/scripts/stop-feedback-benchmark.mjs',
]);

const BASELINE_AUDIT_ALLOWED_PATHS = Object.freeze(new Set([
  ...RUNNER_CONTENT_PATHS,
  'frontend-app/src/entities/client/model/contractStoreModel.js',
  'frontend-app/src/entities/client/model/threadLifecycleRuntime.js',
  'frontend-app/src/pages/chat/components/ChatActionFeedback.js',
  'docs/doc/codemap/README.md',
  'docs/doc/codemap/ai-index.json',
  'docs/plans/2026-07-15-frontend-maintainability-error-discoverability-90-plan.md',
  'frontend-app/scripts/chat-history-benchmark.test.mjs',
  'frontend-app/scripts/evidence-provenance.test.mjs',
  'frontend-app/scripts/managed-command.test.mjs',
  'frontend-app/scripts/performance-baseline-provenance.test.mjs',
  'frontend-app/scripts/performance-budget-model.test.mjs',
  'frontend-app/scripts/performance-budget-runner.test.mjs',
  'frontend-app/scripts/resource-budget.test.mjs',
  'frontend-app/scripts/stop-feedback-benchmark.test.mjs',
  'frontend-app/src/entities/client/model/threadLifecycleRuntime.test.js',
]));

const BASELINE_AUDIT_ALLOWED_PREFIXES = Object.freeze([
  'docs/doc/codemap/project-map/',
]);
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

function baselineAuditPathAllowed(path) {
  return BASELINE_AUDIT_ALLOWED_PATHS.has(path)
    || BASELINE_AUDIT_ALLOWED_PREFIXES.some((prefix) => path.startsWith(prefix));
}

function assertRepoRelativePath(path) {
  if (typeof path !== 'string' || path.length === 0) {
    throw new TypeError('git diff changed path must be a non-empty UTF-8 string');
  }
  if (path === '.' || path === '..' || path.startsWith('/') || path.startsWith('\\')
    || /^[A-Za-z]:[\\/]/u.test(path)) {
    throw new Error(`git diff changed path must be repo-relative: ${JSON.stringify(path)}`);
  }
  const segments = path.split('/');
  if (segments.some((segment) => segment.length === 0 || segment === '.' || segment === '..')) {
    throw new Error(`git diff changed path contains an invalid segment: ${JSON.stringify(path)}`);
  }
  return path;
}

function canonicalizeRepoRelativePaths(paths) {
  if (!Array.isArray(paths)) throw new TypeError('git diff changed paths must be an array');
  const unique = new Set(paths.map(assertRepoRelativePath));
  return Object.freeze([...unique].sort((left, right) => (
    Buffer.compare(Buffer.from(left, 'utf8'), Buffer.from(right, 'utf8'))
  )));
}

function parseGitDiffNameOnlyZ(output) {
  if (!Buffer.isBuffer(output)) throw new TypeError('git diff --name-only -z output must be a Buffer');
  if (output.length === 0) return Object.freeze([]);
  if (output[output.length - 1] !== 0) {
    throw new Error('git diff --name-only -z output has a malformed trailing path');
  }
  const decoder = new TextDecoder('utf-8', { fatal: true });
  const paths = [];
  let start = 0;
  for (let index = 0; index < output.length; index += 1) {
    if (output[index] !== 0) continue;
    if (index === start) throw new Error('git diff --name-only -z output contains an empty path');
    try {
      paths.push(decoder.decode(output.subarray(start, index)));
    } catch (error) {
      throw new Error(`git diff --name-only -z output contains non-UTF-8 path: ${error.message}`);
    }
    start = index + 1;
  }
  return canonicalizeRepoRelativePaths(paths);
}

function canonicalGitDiffChangedPaths(repositoryRoot, baseSha, headSha) {
  const output = execFileSync('git', [
    'diff', '--name-only', '-z', '--no-renames', baseSha, headSha,
  ], {
    cwd: repositoryRoot,
    encoding: 'buffer',
    env: repositoryLocalGitEnvironment(),
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  return parseGitDiffNameOnlyZ(output);
}

function validateBaselineAuditDiff(changedPaths) {
  if (!Array.isArray(changedPaths)) throw new TypeError('changedPaths must be an array');
  const seen = new Set();
  for (let index = 0; index < changedPaths.length; index += 1) {
    const path = assertRepoRelativePath(changedPaths[index]);
    if (seen.has(path)
      || (index > 0 && Buffer.compare(
        Buffer.from(changedPaths[index - 1], 'utf8'),
        Buffer.from(path, 'utf8'),
      ) >= 0)) {
      throw new Error('baseline audit changedPaths must be exact, unique, and sorted');
    }
    seen.add(path);
  }
  const forbidden = changedPaths.filter((path) => !baselineAuditPathAllowed(path));
  if (forbidden.length > 0) {
    throw new Error(`baseline audit runner changed forbidden path(s): ${forbidden.join(', ')}`);
  }
  return Object.freeze([...changedPaths]);
}

function runnerContentEvidence(repositoryRoot, runnerContentPaths = RUNNER_CONTENT_PATHS) {
  if (!Array.isArray(runnerContentPaths) || runnerContentPaths.length === 0
    || new Set(runnerContentPaths).size !== runnerContentPaths.length) {
    throw new TypeError('runnerContentPaths must be a non-empty unique array');
  }
  const files = runnerContentPaths.map((path) => {
    const content = readFileSync(resolve(repositoryRoot, path));
    return Object.freeze({
      path,
      sha256: createHash('sha256').update(content).digest('hex'),
    });
  });
  const aggregate = createHash('sha256');
  files.forEach(({ path, sha256 }) => aggregate.update(`${path}\0${sha256}\n`));
  return Object.freeze({
    runnerContentHash: aggregate.digest('hex'),
    runnerFiles: Object.freeze(files),
  });
}

function collectEvidenceProvenance({
  repositoryRoot,
  runnerRepositoryRoot = repositoryRoot,
  runnerContentPaths = RUNNER_CONTENT_PATHS,
  recordBaselineAudit = true,
  runnerId,
  subjectSha,
}) {
  if (typeof runnerId !== 'string' || runnerId.length === 0) throw new TypeError('runnerId is required');
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
  let baselineAudit = null;
  if (subjectSha !== runnerSha) {
    if (status) throw new Error('baseline audit runner requires a clean worktree');
    const ancestry = recordBaselineAudit ? [subjectSha, runnerSha] : [runnerSha, subjectSha];
    try {
      execFileSync('git', ['merge-base', '--is-ancestor', ...ancestry], {
        cwd: repositoryRoot,
        env: repositoryLocalGitEnvironment(),
        stdio: ['ignore', 'ignore', 'pipe'],
      });
    } catch (error) {
      const detail = String(error.stderr || '').trim();
      const relation = recordBaselineAudit
        ? 'baseline subject must be an ancestor of runner HEAD'
        : 'frozen runner must be an ancestor of the subject';
      throw new Error(`${relation}${detail ? `: ${detail}` : ''}`);
    }
    if (recordBaselineAudit) {
      baselineAudit = Object.freeze({
        baseSha: subjectSha,
        baseTree: subjectTree,
        changedPaths: validateBaselineAuditDiff(canonicalGitDiffChangedPaths(
          repositoryRoot,
          subjectSha,
          runnerSha,
        )),
      });
    }
  }
  const cpuList = cpus();
  if (cpuList.length === 0 || !cpuList[0]?.model) throw new Error('CPU metadata is unavailable');
  const runnerContent = runnerContentEvidence(runnerRepositoryRoot, runnerContentPaths);
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
      ...runnerContent,
      worktreeClean: status.length === 0,
      worktreeStatus: Object.freeze(status ? status.split('\n') : []),
      baselineAudit,
    }),
  });
}

export {
  BASELINE_AUDIT_ALLOWED_PATHS,
  canonicalGitDiffChangedPaths,
  canonicalizeRepoRelativePaths,
  RUNNER_CONTENT_PATHS,
  collectEvidenceProvenance,
  parseGitDiffNameOnlyZ,
  runnerContentEvidence,
  validateBaselineAuditDiff,
};
