import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { arch, cpus, loadavg, platform, release, totalmem } from 'node:os';
import { resolve } from 'node:path';
import process from 'node:process';

const RUNNER_CONTENT_PATHS = Object.freeze([
  'frontend-app/package.json',
  'frontend-app/scripts/chat-history-benchmark.mjs',
  'frontend-app/scripts/evidence-provenance.mjs',
  'frontend-app/scripts/frontend-performance-cases.json',
  'frontend-app/scripts/performance-budget-config.mjs',
  'frontend-app/scripts/performance-budget-model.mjs',
  'frontend-app/scripts/performance-budget-runner.mjs',
  'frontend-app/scripts/render-isolation-probe.test.jsx',
  'frontend-app/scripts/resource-budget.mjs',
  'frontend-app/scripts/stop-feedback-benchmark.mjs',
]);

const BASELINE_AUDIT_ALLOWED_PATHS = Object.freeze(new Set([
  ...RUNNER_CONTENT_PATHS,
  'frontend-app/scripts/chat-history-benchmark.test.mjs',
  'frontend-app/scripts/delivery-smoke-runner.mjs',
  'frontend-app/scripts/delivery-smoke-runner.test.mjs',
  'frontend-app/scripts/evidence-provenance.test.mjs',
  'frontend-app/scripts/performance-budget-model.test.mjs',
  'frontend-app/scripts/performance-budget-runner.test.mjs',
  'frontend-app/scripts/resource-budget.test.mjs',
  'frontend-app/scripts/stop-feedback-benchmark.test.mjs',
  'scripts/ai_maintenance/gate_execution.go',
  'scripts/ai_maintenance/main.go',
  'scripts/ai_maintenance/main_test.go',
  'scripts/sqlc_verify_worktree_guard_test.go',
]));

const BASELINE_AUDIT_ALLOWED_PREFIXES = Object.freeze([
  'docs/doc/codemap/project-map/',
]);

function commandOutput(command, args, cwd) {
  const output = execFileSync(command, args, {
    cwd,
    encoding: 'utf8',
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

function validateBaselineAuditDiff(changedPaths) {
  if (!Array.isArray(changedPaths)) throw new TypeError('changedPaths must be an array');
  const normalized = [...new Set(changedPaths)].sort();
  if (JSON.stringify(changedPaths) !== JSON.stringify(normalized)) {
    throw new Error('baseline audit changedPaths must be exact, unique, and sorted');
  }
  const forbidden = changedPaths.filter((path) => !baselineAuditPathAllowed(path));
  if (forbidden.length > 0) {
    throw new Error(`baseline audit runner changed forbidden path(s): ${forbidden.join(', ')}`);
  }
  return Object.freeze([...changedPaths]);
}

function runnerContentEvidence(repositoryRoot) {
  const files = RUNNER_CONTENT_PATHS.map((path) => {
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

function collectEvidenceProvenance({ repositoryRoot, runnerId, subjectSha }) {
  if (typeof runnerId !== 'string' || runnerId.length === 0) throw new TypeError('runnerId is required');
  requireFullSha(subjectSha, 'subjectSha');
  const git = (args) => commandOutput('git', args, repositoryRoot);
  const runnerSha = requireFullSha(git(['rev-parse', 'HEAD']), 'runnerSha');
  const runnerTree = requireFullSha(git(['rev-parse', 'HEAD^{tree}']), 'runnerTree');
  const subjectTree = requireFullSha(git(['rev-parse', `${subjectSha}^{tree}`]), 'subjectTree');
  const status = execFileSync('git', ['status', '--porcelain', '--untracked-files=all'], {
    cwd: repositoryRoot,
    encoding: 'utf8',
    stdio: ['ignore', 'pipe', 'pipe'],
  }).trim();
  let baselineAudit = null;
  if (subjectSha !== runnerSha) {
    if (status) throw new Error('baseline audit runner requires a clean worktree');
    try {
      execFileSync('git', ['merge-base', '--is-ancestor', subjectSha, runnerSha], {
        cwd: repositoryRoot,
        stdio: ['ignore', 'ignore', 'pipe'],
      });
    } catch (error) {
      const detail = String(error.stderr || '').trim();
      throw new Error(`baseline subject must be an ancestor of runner HEAD${detail ? `: ${detail}` : ''}`);
    }
    const changedPaths = git(['diff', '--name-only', subjectSha, runnerSha]).split('\n');
    baselineAudit = Object.freeze({
      baseSha: subjectSha,
      baseTree: subjectTree,
      changedPaths: validateBaselineAuditDiff(changedPaths),
    });
  }
  const cpuList = cpus();
  if (cpuList.length === 0 || !cpuList[0]?.model) throw new Error('CPU metadata is unavailable');
  const runnerContent = runnerContentEvidence(repositoryRoot);
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
  RUNNER_CONTENT_PATHS,
  collectEvidenceProvenance,
  runnerContentEvidence,
  validateBaselineAuditDiff,
};
