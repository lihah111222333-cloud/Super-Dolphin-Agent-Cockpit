import { execFileSync } from 'node:child_process';
import process from 'node:process';
import { fileURLToPath } from 'node:url';
import { canonicalGitDiffChangedPaths } from './evidence-provenance.mjs';
import { repositoryLocalGitEnvironment } from './runtime/git-environment.mjs';

const FROZEN_PLAN_BASE_SHA = '314a8e240b2fe58de23651a00b74f05c985cf5e4';
const PURE_PERFORMANCE_RUNNER_PATHS = Object.freeze(new Set([
  'frontend-app/scripts/chat-history-benchmark.mjs',
  'frontend-app/scripts/chat-history-benchmark.test.mjs',
  'frontend-app/scripts/evidence-provenance.mjs',
  'frontend-app/scripts/evidence-provenance.test.mjs',
  'frontend-app/scripts/frontend-performance-cases.json',
  'frontend-app/scripts/runtime/git-environment.mjs',
  'frontend-app/scripts/managed-command.mjs',
  'frontend-app/scripts/managed-command.test.mjs',
  'frontend-app/scripts/performance-baseline-provenance.mjs',
  'frontend-app/scripts/performance-baseline-provenance.test.mjs',
  'frontend-app/scripts/performance-budget-config.mjs',
  'frontend-app/scripts/performance-budget-model.mjs',
  'frontend-app/scripts/performance-budget-model.test.mjs',
  'frontend-app/scripts/performance-budget-runner.mjs',
  'frontend-app/scripts/performance-budget-runner.test.mjs',
  'frontend-app/scripts/render-isolation-probe.test.jsx',
  'frontend-app/scripts/resource-budget.mjs',
  'frontend-app/scripts/resource-budget.test.mjs',
  'frontend-app/scripts/stop-feedback-benchmark.mjs',
  'frontend-app/scripts/stop-feedback-benchmark.test.mjs',
  'frontend-app/src/pages/chat/components/ChatActionFeedback.js',
]));

const PURE_AUDIT_PATHS = Object.freeze(new Set([
  'docs/doc/codemap/README.md',
  'docs/doc/codemap/ai-index.json',
  'docs/plans/2026-07-15-frontend-maintainability-error-discoverability-90-plan.md',
]));

const PURE_AUDIT_PREFIXES = Object.freeze([
  'docs/doc/codemap/project-map/',
]);

function requireFullSha(value, label) {
  if (typeof value !== 'string' || !/^[0-9a-f]{40}$/u.test(value)) {
    throw new TypeError(`${label} must be a full 40-character Git SHA`);
  }
  return value;
}

function requireRepositoryRoot(repositoryRoot) {
  if (typeof repositoryRoot !== 'string' || repositoryRoot.length === 0) {
    throw new TypeError('repositoryRoot is required');
  }
  return repositoryRoot;
}

function git(repositoryRoot, args, options = {}) {
  try {
    return execFileSync('git', args, {
      cwd: repositoryRoot,
      encoding: options.encoding || 'utf8',
      env: repositoryLocalGitEnvironment(),
      stdio: ['ignore', 'pipe', 'pipe'],
    });
  } catch (error) {
    const detail = String(error.stderr || '').trim();
    throw new Error(`git ${args.join(' ')} failed${detail ? `: ${detail}` : ''}`);
  }
}

function isAllowedPerformanceBaselinePath(path) {
  return PURE_PERFORMANCE_RUNNER_PATHS.has(path)
    || PURE_AUDIT_PATHS.has(path)
    || PURE_AUDIT_PREFIXES.some((prefix) => path.startsWith(prefix));
}

function changedPathsBetween(repositoryRoot, planBaseSha, baselineBaseSha) {
  return canonicalGitDiffChangedPaths(repositoryRoot, planBaseSha, baselineBaseSha);
}

function analyzePerformanceBaselineProvenance({
  repositoryRoot,
  planBaseSha = FROZEN_PLAN_BASE_SHA,
  baselineBaseSha,
}) {
  const root = requireRepositoryRoot(repositoryRoot);
  const planBase = requireFullSha(planBaseSha, 'planBaseSha');
  const baselineBase = requireFullSha(baselineBaseSha, 'baselineBaseSha');
  git(root, ['cat-file', '-e', `${planBase}^{commit}`]);
  git(root, ['cat-file', '-e', `${baselineBase}^{commit}`]);
  try {
    git(root, ['merge-base', '--is-ancestor', planBase, baselineBase]);
  } catch {
    return Object.freeze({
      schemaVersion: 1,
      planBaseSha: planBase,
      baselineBaseSha: baselineBase,
      changedPaths: Object.freeze([]),
      forbiddenPaths: Object.freeze([]),
      valid: false,
      reason: 'baseline base must descend from the frozen plan BASE_SHA',
    });
  }
  const changedPaths = changedPathsBetween(root, planBase, baselineBase);
  const forbiddenPaths = Object.freeze(changedPaths.filter((path) => !isAllowedPerformanceBaselinePath(path)));
  return Object.freeze({
    schemaVersion: 1,
    planBaseSha: planBase,
    baselineBaseSha: baselineBase,
    changedPaths,
    forbiddenPaths,
    valid: forbiddenPaths.length === 0,
    reason: forbiddenPaths.length === 0
      ? 'baseline provenance contains only pure performance runner or audit changes'
      : `baseline provenance crosses forbidden product or dependency path(s): ${forbiddenPaths.join(', ')}`,
  });
}

function assertPerformanceBaselineProvenance(options) {
  const result = analyzePerformanceBaselineProvenance(options);
  if (!result.valid) throw new Error(result.reason);
  return result;
}

function parseCLI(argv) {
  const options = {};
  for (let index = 0; index < argv.length; index += 1) {
    const flag = argv[index];
    const value = argv[index + 1];
    if (!['--repo', '--plan-base', '--baseline-base'].includes(flag) || !value || value.startsWith('--')) {
      throw new Error('usage: performance-baseline-provenance.mjs --repo <path> --baseline-base <sha> [--plan-base <sha>]');
    }
    if (options[flag]) throw new Error(`duplicate CLI argument: ${flag}`);
    options[flag] = value;
    index += 1;
  }
  if (!options['--repo'] || !options['--baseline-base']) {
    throw new Error('usage: performance-baseline-provenance.mjs --repo <path> --baseline-base <sha> [--plan-base <sha>]');
  }
  return {
    repositoryRoot: options['--repo'],
    baselineBaseSha: options['--baseline-base'],
    ...(options['--plan-base'] ? { planBaseSha: options['--plan-base'] } : {}),
  };
}

function runCLI() {
  const result = analyzePerformanceBaselineProvenance(parseCLI(process.argv.slice(2)));
  process.stdout.write(`${JSON.stringify(result)}\n`);
  if (!result.valid) process.exitCode = 1;
}

if (process.argv[1] === fileURLToPath(import.meta.url)) runCLI();

export {
  FROZEN_PLAN_BASE_SHA,
  PURE_AUDIT_PATHS,
  PURE_PERFORMANCE_RUNNER_PATHS,
  analyzePerformanceBaselineProvenance,
  assertPerformanceBaselineProvenance,
  isAllowedPerformanceBaselinePath,
};
