import { execFileSync, spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';

const scriptPath = fileURLToPath(import.meta.url);
const frozenScriptRoot = path.dirname(scriptPath);
const frozenAppRoot = path.resolve(frozenScriptRoot, '..');
const frozenRepoRoot = path.resolve(frozenAppRoot, '..');
const controls = readFrozenJSON('frontend-maintainability-controls.json');
const fixtures = readFrozenJSON('frontend-maintainability-red-fixtures.json');
const baseline = readFrozenJSON('frontend-maintainability-baseline.json');
const frozenControlIDs = new Set(controls.controls.map(({ id }) => id));
const frozenFixtureIDs = new Set(fixtures.fixtures.map(({ id }) => id));
const weakCommands = new Set([':', 'echo', 'false', 'true']);
const governancePaths = [
  'frontend-app/scripts/frontend-maintainability-controls.json',
  'frontend-app/scripts/frontend-maintainability-score.mjs',
  'frontend-app/scripts/frontend-maintainability-baseline.json',
  'frontend-app/scripts/frontend-maintainability-red-fixtures.json',
];
const artifactProbes = new Set(['promptHistoryVisibleError', 'criticalTypecheck']);
const plannedBaseSha = 'b40867229af8e17916c00393639ccb0fcb4bf6fc';
const plannedThresholds = { overall: 90, dimensions: { E: 90, A: 85, C: 85, T: 85, P: 80 } };
const requiredDoDControls = new Set(['E06-failure-matrix', 'C05-provider-rpc-parity', 'T05-build-embed-smoke']);
const customEvidenceProtocols = new Set([
  'action-producer-guard-v1',
  'failure-matrix-report-v1',
  'desktop-failure-report-v1',
  'performance-budget-json-v1',
  'delivery-smoke-json-v1',
]);
const failureMatrixCaseIds = Object.freeze(Array.from({ length: 24 }, (_, index) => (
  `FM-${String(index + 1).padStart(2, '0')}`
)));
const failureMatrixLayers = Object.freeze({
  'FM-01': ['frontend', 'go-wails'],
  'FM-02': ['frontend'],
  'FM-03': ['frontend'],
  'FM-04': ['frontend'],
  'FM-05': ['frontend'],
  'FM-06': ['frontend'],
  'FM-07': ['go-codex'],
  'FM-08': ['go-claude'],
  'FM-09': ['go-codex'],
  'FM-10': ['go-codex'],
  'FM-11': ['go-codex'],
  'FM-12': ['go-codex'],
  'FM-13': ['go-codex'],
  'FM-14': ['go-codex'],
  'FM-15': ['go-turn'],
  'FM-16': ['go-turn'],
  'FM-17': ['go-turn'],
  'FM-18': ['frontend'],
  'FM-19': ['frontend', 'go-codex'],
  'FM-20': ['frontend', 'go-codex'],
  'FM-21': ['frontend'],
  'FM-22': ['frontend'],
  'FM-23': ['frontend'],
  'FM-24': ['frontend'],
});
const failureMatrixEvidencePairs = Object.freeze(Object.entries(failureMatrixLayers)
  .flatMap(([caseId, layers]) => layers.map((layer) => `${caseId}\0${layer}`)));
const failureMatrixControlIds = Object.freeze([
  'E06-failure-matrix',
  'C05-provider-rpc-parity',
  'T01-red-green-regression',
  'T03-wails-integration',
]);
const performanceCaseIds = Object.freeze({
  'P01-render-isolation': ['render-main-page-update-commits', 'render-unrelated-subtree-update-commits', 'render-broad-subscription-mutation-detected'],
  'P02-history-budget': ['turns-200-tools-1', 'turns-200-tools-3', 'turns-1000-tools-1', 'turns-1000-tools-3', 'turns-5000-tools-1', 'turns-5000-tools-3'],
  'P03-feedback-budget': ['stop-visible-feedback'],
  'P04-resource-budget': ['bundle-total-bytes', 'bundle-max-chunk-bytes'],
});
const allPerformanceCaseIds = Object.freeze(Object.values(performanceCaseIds).flat());
const performanceRunnerFiles = Object.freeze([
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
const performanceEnvironmentKeys = Object.freeze([
  'os', 'cpu', 'totalMemoryBytes', 'loadAverage', 'node', 'npm', 'go',
]);
const performanceAuditAllowedPaths = new Set([
  ...performanceRunnerFiles,
  'docs/doc/codemap/README.md',
  'docs/doc/codemap/ai-index.json',
  'frontend-app/package.json',
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
]);
const performanceAuditAllowedPrefixes = Object.freeze(['docs/doc/codemap/project-map/']);

export function performanceAuditPathAllowed(changedPath) {
  return performanceAuditAllowedPaths.has(changedPath)
    || performanceAuditAllowedPrefixes.some((prefix) => changedPath.startsWith(prefix));
}
const failureMatrixChecks = Object.freeze({
  'E03-background-health': Object.freeze({ probe: 'backgroundProviderHealth', caseIds: ['FM-18'], testCount: 1 }),
  'E05-safe-recovery': Object.freeze({ probe: 'safeRecovery', caseIds: ['FM-16', 'FM-17', 'FM-18'], testCount: 3 }),
  'E06-failure-matrix': Object.freeze({ probe: 'failureMatrix', caseIds: failureMatrixCaseIds, testCount: 27 }),
  'C05-provider-rpc-parity': Object.freeze({
    probe: 'providerRpcParity',
    caseIds: ['FM-07', 'FM-08', 'FM-09', 'FM-10', 'FM-11', 'FM-12', 'FM-13', 'FM-14', 'FM-19', 'FM-20'],
    testCount: 12,
  }),
  'T01-red-green-regression': Object.freeze({ probe: 'redGreenRegression', caseIds: failureMatrixCaseIds, testCount: 27 }),
  'T03-wails-integration': Object.freeze({ probe: 'wailsFailureMatrix', caseIds: ['FM-01'], testCount: 2 }),
});
const deliveryCaseIds = Object.freeze([
  'frontend-build',
  'frontend-embed-verify',
  'desktop-start-smoke',
  'desktop-failure-smoke',
]);
const plannedLaneAllOfArgv = Object.freeze({
  'A02-state-ownership': [
    ['node', 'scripts/frontend-state-ownership-guard.mjs'],
    ['node', 'node_modules/vitest/vitest.mjs', 'run', 'scripts/frontend-state-ownership-guard.test.mjs', '--reporter=json', '--no-file-parallelism', '--maxWorkers=1'],
  ],
  'A03-dependency-direction': [
    ['node', 'scripts/frontend-dependency-direction-guard.mjs'],
    ['node', 'node_modules/vitest/vitest.mjs', 'run', 'scripts/frontend-dependency-direction-guard.test.mjs', '--reporter=json', '--no-file-parallelism', '--maxWorkers=1'],
  ],
  'A04-action-registry': [
    ['node', 'scripts/action-producer-guard.selftest.mjs'],
    ['node', 'scripts/action-producer-guard.mjs'],
  ],
  'C04-critical-typecheck': [
    ['node', 'scripts/critical-typecheck-guard.mjs'],
    ['node', 'node_modules/vitest/vitest.mjs', 'run', 'scripts/contracts-typecheck-guard.test.mjs', '--reporter=json', '--no-file-parallelism', '--maxWorkers=1'],
  ],
  'T02-critical-action-coverage': [
    ['node', 'scripts/action-producer-guard.selftest.mjs'],
    ['node', 'scripts/action-producer-guard.mjs'],
  ],
});

function readFrozenJSON(name) {
  return JSON.parse(fs.readFileSync(path.join(frozenScriptRoot, name), 'utf8'));
}

function fail(message) {
  throw new Error(message);
}

class NotVerifiedEvidenceError extends Error {}

function notVerified(message) {
  throw new NotVerifiedEvidenceError(message);
}

function sorted(values) {
  return [...values].sort();
}

function exactSet(actual, expected, label) {
  if (JSON.stringify(sorted(actual)) !== JSON.stringify(sorted(expected))) fail(`${label} exact set mismatch`);
}

function exactKeys(value, expected, label) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) fail(`${label} must be an object`);
  exactSet(Object.keys(value), expected, `${label} keys`);
}

function git(repoRoot, args) {
  return execFileSync('git', args, { cwd: repoRoot, encoding: 'utf8' }).trim();
}

function gitSucceeds(repoRoot, args) {
  return spawnSync('git', args, { cwd: repoRoot, encoding: 'utf8', stdio: 'pipe' }).status === 0;
}

function canonicalRepoRoot(candidate) {
  const requested = fs.realpathSync(path.resolve(candidate));
  const discovered = fs.realpathSync(git(requested, ['rev-parse', '--show-toplevel']));
  if (requested !== discovered) fail(`--repo must be the target repository root: ${discovered}`);
  return discovered;
}

function frozenHead() {
  return git(frozenRepoRoot, ['rev-parse', 'HEAD']);
}

function assertFrozenProvenance(scoreBaseSha = frozenHead()) {
  if (baseline.baseSha !== plannedBaseSha) fail('baseline baseSha differs from the frozen plan BASE_SHA');
  for (const [label, sha] of [
    ['baseline baseSha', baseline.baseSha],
    ['baseline planSnapshotSha', baseline.planSnapshotSha],
  ]) {
    if (!/^[0-9a-f]{40}$/.test(sha)
      || !gitSucceeds(frozenRepoRoot, ['cat-file', '-e', `${sha}^{commit}`])
      || !gitSucceeds(frozenRepoRoot, ['merge-base', '--is-ancestor', sha, scoreBaseSha])) {
      fail(`${label} is not committed ancestry of SCORE_BASE_SHA`);
    }
  }
}

function assertFrozenScorerClean() {
  if (git(frozenRepoRoot, ['status', '--porcelain=v1', '--untracked-files=all']).length > 0) {
    fail('--final requires a clean frozen SCORE_BASE worktree');
  }
}

function assertGovernanceUnchanged(targetRepoRoot) {
  for (const relativePath of governancePaths) {
    const frozen = fs.readFileSync(path.join(frozenRepoRoot, relativePath));
    const targetPath = path.join(targetRepoRoot, relativePath);
    if (!fs.existsSync(targetPath) || !frozen.equals(fs.readFileSync(targetPath))) {
      fail(`frozen governance drift: ${relativePath}`);
    }
  }
}

export function inspectTargetRepository({
  repoRoot = frozenRepoRoot,
  subjectSha,
  requireClean = false,
  requireFinalContract = false,
} = {}) {
  const targetRepoRoot = canonicalRepoRoot(repoRoot);
  const head = git(targetRepoRoot, ['rev-parse', 'HEAD']);
  if (subjectSha !== undefined && subjectSha !== head) fail('subject must equal target HEAD');
  if (requireClean && git(targetRepoRoot, ['status', '--porcelain=v1', '--untracked-files=all']).length > 0) {
    fail('scorer rejects dirty or untracked target worktrees');
  }
  const subjectTree = git(targetRepoRoot, ['rev-parse', 'HEAD^{tree}']);
  const scoreBaseSha = frozenHead();
  assertFrozenProvenance(scoreBaseSha);
  if (requireFinalContract) {
    if (head === scoreBaseSha) fail('final subject must be a strict descendant of SCORE_BASE_SHA');
    if (!gitSucceeds(targetRepoRoot, ['cat-file', '-e', `${scoreBaseSha}^{commit}`])
      || !gitSucceeds(targetRepoRoot, ['merge-base', '--is-ancestor', scoreBaseSha, head])) {
      fail('SCORE_BASE_SHA must be a strict ancestor of the final subject');
    }
    assertGovernanceUnchanged(targetRepoRoot);
  }
  return {
    repoRoot: targetRepoRoot,
    appRoot: path.join(targetRepoRoot, 'frontend-app'),
    subjectSha: head,
    subjectTree,
    scoreBaseSha,
  };
}

function contextForExecution(context, repoRoot) {
  return { ...context, repoRoot, appRoot: path.join(repoRoot, 'frontend-app') };
}

function source(context, relativePath) {
  return fs.readFileSync(path.join(context.appRoot, relativePath), 'utf8');
}

export function sourceFingerprint(context, paths) {
  if (!Array.isArray(paths) || paths.length === 0) fail('source fingerprint paths are empty');
  const hash = createHash('sha256');
  for (const relativePath of paths) {
    hash.update(relativePath);
    hash.update('\0');
    hash.update(source(context, relativePath));
    hash.update('\0');
  }
  return hash.digest('hex');
}

function terminalTruthTestResults(report) {
  if (!report || !Array.isArray(report.testResults)) return [];
  return report.testResults.flatMap((fileResult) => (
    Array.isArray(fileResult.assertionResults) ? fileResult.assertionResults : []
  )).map((result) => ({
    name: result.title,
    status: result.status,
  }));
}

export function terminalTruthEvidenceStatus(evidence, expected) {
  if (!evidence || evidence.failed === true || evidence.fingerprint !== expected?.fingerprint) return 'FAIL';
  if (!Array.isArray(evidence.testResults) || evidence.testResults.length === 0) return 'FAIL';
  if (!Array.isArray(expected?.testNames) || expected.testNames.length === 0) return 'FAIL';
  const byName = new Map(evidence.testResults.map((result) => [result.name, result.status]));
  if (byName.size !== expected.testNames.length || evidence.testResults.length !== expected.testNames.length) return 'FAIL';
  return expected.testNames.every((name) => byName.get(name) === 'passed') ? 'PASS' : 'FAIL';
}

function sameDependencyManifest(leftAppRoot, rightAppRoot) {
  const left = path.join(leftAppRoot, 'package-lock.json');
  const right = path.join(rightAppRoot, 'package-lock.json');
  return fs.existsSync(left) && fs.existsSync(right) && fs.readFileSync(left).equals(fs.readFileSync(right));
}

function dependencyAppRoots(context) {
  const candidates = [context.appRoot, frozenAppRoot];
  try {
    const commonDir = git(context.repoRoot, ['rev-parse', '--git-common-dir']);
    const absoluteCommonDir = path.resolve(context.repoRoot, commonDir);
    candidates.push(path.join(path.dirname(absoluteCommonDir), 'frontend-app'));
  }
  catch {
    // The target repository validation reports the actionable Git error.
  }
  return [...new Set(candidates.map((candidate) => path.resolve(candidate)))];
}

function resolveVitestRuntime(context) {
  for (const candidate of dependencyAppRoots(context)) {
    const vitestPath = path.join(candidate, 'node_modules', 'vitest', 'vitest.mjs');
    const candidateConfig = path.join(candidate, 'vite.config.js');
    const targetConfig = path.join(context.appRoot, 'vite.config.js');
    if (fs.existsSync(vitestPath) && sameDependencyManifest(context.appRoot, candidate)
      && fs.existsSync(candidateConfig) && fs.existsSync(targetConfig)
      && fs.readFileSync(candidateConfig).equals(fs.readFileSync(targetConfig))) {
      return { vitestPath, dependencyAppRoot: candidate };
    }
  }
  return undefined;
}

function collectVitestEvidence(context, check) {
  try {
    const fingerprint = sourceFingerprint(context, check.sourcePaths);
    const runtime = resolveVitestRuntime(context);
    if (!runtime) return { fingerprint, failed: true, testResults: [], summary: 'matching Vitest dependency and config are unavailable' };
    const targetNodeModules = path.join(context.appRoot, 'node_modules');
    let linkedDependencies = false;
    if (!fs.existsSync(targetNodeModules)) {
      fs.symlinkSync(path.join(runtime.dependencyAppRoot, 'node_modules'), targetNodeModules, 'dir');
      linkedDependencies = true;
    }
    let output;
    try {
      const [configuredNode, configuredVitest, ...args] = check.argv;
      if (configuredNode !== 'node' || configuredVitest !== 'node_modules/vitest/vitest.mjs') {
        fail(`invalid frozen Vitest argv for ${check.probe}`);
      }
      output = execFileSync(process.execPath, [runtime.vitestPath, ...args], {
        cwd: context.appRoot,
        encoding: 'utf8',
        stdio: ['ignore', 'pipe', 'pipe'],
        timeout: check.timeoutMs,
      });
    }
    finally {
      if (linkedDependencies) fs.unlinkSync(targetNodeModules);
    }
    const expectedNames = new Set(check.testNames);
    return {
      fingerprint,
      testResults: terminalTruthTestResults(JSON.parse(output)).filter(({ name }) => expectedNames.has(name)),
    };
  }
  catch (error) {
    const output = `${error.stdout || ''}${error.stderr || ''}`.trim();
    return {
      fingerprint: undefined,
      failed: true,
      testResults: [],
      summary: output.slice(-1200) || error.message,
    };
  }
}

function vitestProbe(context, control, check) {
  const fingerprint = sourceFingerprint(context, check.sourcePaths);
  const evidence = collectVitestEvidence(context, check);
  const status = terminalTruthEvidenceStatus(evidence, { fingerprint, testNames: check.testNames });
  return evidenceRecord(context, control, check, {
    status,
    exitCode: status === 'PASS' ? 0 : 1,
    summary: evidence.summary || `${evidence.testResults.length}/${check.testCount} exact named tests passed`,
  });
}

export function sourceHasPromptHistoryConsoleOnly(context = inspectTargetRepository()) {
  const dock = source(context, 'src/pages/chat/composer/ComposerDock.jsx');
  const action = source(context, 'src/shared/ui/runUIAction.js');
  return dock.includes('runUIAction(() => promptHistory[direction]());')
    && action.includes('logger = console.error')
    && !dock.includes('promptHistory[direction](), { onError');
}

export function sourceHasCriticalTypecheckGap(context = inspectTargetRepository()) {
  const config = JSON.parse(source(context, 'tsconfig.contracts.json'));
  return config.compilerOptions?.checkJs !== true || config.compilerOptions?.strict !== true;
}

function artifactProbe(context, control, check) {
  let hasConfirmedGap;
  if (check.probe === 'promptHistoryVisibleError') hasConfirmedGap = sourceHasPromptHistoryConsoleOnly(context);
  else if (check.probe === 'criticalTypecheck') hasConfirmedGap = sourceHasCriticalTypecheckGap(context);
  else fail(`unknown artifact probe: ${check.probe}`);
  return evidenceRecord(context, control, check, {
    status: hasConfirmedGap ? 'FAIL' : 'NOT_VERIFIED',
    exitCode: hasConfirmedGap ? 1 : null,
    summary: hasConfirmedGap
      ? `artifact reproduces the frozen ${check.probe} defect`
      : `artifact defect is absent, but no complete executable control evidence is registered`,
  });
}

function normalizedRunnerStatus(status) {
  if (status === 'PASS' || status === 'covered') return 'PASS';
  if (status === 'FAIL') return 'FAIL';
  if (status === 'NOT_VERIFIED' || status === 'partial') return 'NOT_VERIFIED';
  fail(`unsupported runner status: ${String(status)}`);
}

function validateReportBinding(report, context, startedAt, finishedAt) {
  if (report?.schemaVersion !== 1) fail('runner report schemaVersion must equal 1');
  if (report.subjectSha !== context.subjectSha) fail('runner report subjectSha mismatch');
  const subjectTree = report.subjectTree ?? report.subjectTreeSha;
  if (subjectTree !== context.subjectTree) fail('runner report subject tree mismatch');
  const generatedAt = Date.parse(report.generatedAt);
  if (!Number.isFinite(generatedAt) || generatedAt < startedAt - 5_000 || generatedAt > finishedAt + 5_000) {
    fail('runner report is stale or generatedAt is missing');
  }
}

function validateExactCaseResult(caseIds, testCount, check, label) {
  if (!Array.isArray(caseIds)) fail(`${label} caseIds must be an array`);
  exactSet(caseIds, check.caseIds, `${label} caseIds`);
  if (testCount !== check.testCount || !Number.isInteger(testCount) || testCount <= 0) {
    fail(`${label} testCount mismatch`);
  }
}

function validateFailureMatrixReport(report, { context, check, startedAt, finishedAt }) {
  validateReportBinding(report, context, startedAt, finishedAt);
  exactSet(report.controlIds || [], failureMatrixControlIds, 'failure matrix controlIds');
  validateExactCaseResult(report.caseIds, report.testCount, {
    caseIds: failureMatrixCaseIds,
    testCount: 27,
  }, 'failure matrix aggregate');
  if (report.caseCount !== failureMatrixCaseIds.length
    || !Array.isArray(report.evidence) || report.evidence.length !== 27) {
    fail('failure matrix aggregate count mismatch');
  }
  const evidencePairs = report.evidence.map(({ caseId, layer }) => `${caseId}\0${layer}`);
  if (evidencePairs.some((pair) => pair.endsWith('\0'))
    || report.evidence.some(({ test }) => typeof test !== 'string' || !test.trim())
    || new Set(evidencePairs).size !== evidencePairs.length) {
    fail('failure matrix evidence contains empty or duplicate case/layer pairs');
  }
  exactSet(evidencePairs, failureMatrixEvidencePairs, 'failure matrix evidence case/layer pairs');
  exactSet(new Set(report.evidence.map(({ caseId }) => caseId)), failureMatrixCaseIds, 'failure matrix evidence caseIds');
  if (!Array.isArray(report.blockedCases)) fail('failure matrix blockedCases must be an array');
  const blockedIds = report.blockedCases.map(({ caseId, blockedBy, blocker }) => {
    if (!failureMatrixCaseIds.includes(caseId) || !String(blockedBy || '').trim() || !String(blocker || '').trim()) {
      fail('failure matrix blocked case is malformed');
    }
    return caseId;
  });
  if (new Set(blockedIds).size !== blockedIds.length) fail('failure matrix blocked cases are duplicated');
  const aggregateStatus = normalizedRunnerStatus(report.status);
  if ((aggregateStatus === 'PASS') !== (blockedIds.length === 0)) {
    fail('failure matrix aggregate status conflicts with blocked cases');
  }
  const selectedEvidence = report.evidence.filter(({ caseId }) => check.caseIds.includes(caseId));
  validateExactCaseResult(
    [...new Set(selectedEvidence.map(({ caseId }) => caseId))],
    selectedEvidence.length,
    check,
    'failure matrix selected control',
  );
  return blockedIds.some((caseId) => check.caseIds.includes(caseId)) ? 'NOT_VERIFIED' : 'PASS';
}

function validateDesktopFailureReport(report, { context, control, check, startedAt, finishedAt }) {
  validateReportBinding(report, context, startedAt, finishedAt);
  if (report.controlId !== control.id) fail('desktop failure smoke controlId mismatch');
  validateExactCaseResult(report.caseIds, report.testCount, check, 'desktop failure smoke');
  return normalizedRunnerStatus(report.status);
}

function requireFiniteNonNegative(value, label) {
  if (!Number.isFinite(value) || value < 0) fail(`${label} must be finite and non-negative`);
  return value;
}

function exactMedian(values, label) {
  if (!Array.isArray(values) || values.length !== 5) fail(`${label} must contain five raw samples`);
  const sortedValues = values.map((value, index) => requireFiniteNonNegative(value, `${label}[${index}]`))
    .sort((left, right) => left - right);
  return sortedValues[2];
}

function validateTimingMetric(metric, caseIds, subjectSha, label) {
  if (metric?.subjectSha !== subjectSha || metric.sampleCount !== 5 || metric.warmupCount !== 1) {
    fail(`${label} subject or sample contract mismatch`);
  }
  exactSet(Object.keys(metric.cases || {}), caseIds, `${label} cases`);
  for (const caseId of caseIds) {
    const entry = metric.cases[caseId];
    if (!Array.isArray(entry?.durationAttemptSamplesMs) || entry.durationAttemptSamplesMs.length !== 5
      || !Array.isArray(entry.durationSamplesMs) || entry.durationSamplesMs.length !== 5) {
      fail(`${label}.${caseId} raw samples are missing`);
    }
    entry.durationAttemptSamplesMs.forEach((attempts, sampleIndex) => {
      if (!Array.isArray(attempts) || attempts.length !== 3) fail(`${label}.${caseId} must record three attempts per sample`);
      const measured = attempts.map((value, attemptIndex) => (
        requireFiniteNonNegative(value, `${label}.${caseId}.attempt[${sampleIndex}][${attemptIndex}]`)
      ));
      if (entry.durationSamplesMs[sampleIndex] !== Math.min(...measured)) {
        fail(`${label}.${caseId} selected sample is not the fastest fixed attempt`);
      }
    });
    if (entry.durationMedianMs !== exactMedian(entry.durationSamplesMs, `${label}.${caseId}.durationSamplesMs`)) {
      fail(`${label}.${caseId} median does not match raw samples`);
    }
  }
}

function validateRenderMetric(metric, subjectSha, label) {
  if (metric?.subjectSha !== subjectSha || metric.updateCount !== 20
    || !Number.isSafeInteger(metric.warmupUpdates) || metric.warmupUpdates <= 0) {
    fail(`${label} render subject, warmup, or update count mismatch`);
  }
  for (const field of ['mainPageUpdateCommits', 'unrelatedSubtreeUpdateCommits', 'mutationUpdateCommits']) {
    if (!Number.isSafeInteger(metric[field]) || metric[field] < 0) fail(`${label}.${field} must be a non-negative integer`);
  }
  if (metric.mutationDetected !== true || metric.mutationUpdateCommits <= 1) {
    fail(`${label} must prove the broad-subscription mutation`);
  }
  if (metric.updateAction !== 'useClientStore.getState().setLogLevel'
    || metric.productionBoundary !== 'src/App.jsx#App'
    || !Array.isArray(metric.productionStoreSubscriptions)
    || metric.productionStoreSubscriptions.length === 0) {
    fail(`${label} render probe contract is incomplete`);
  }
  const subscriptionKeys = metric.productionStoreSubscriptions.map((subscription) => {
    if (!subscription || typeof subscription !== 'object' || Array.isArray(subscription)
      || typeof subscription.source !== 'string' || !subscription.source.trim()
      || !Number.isSafeInteger(subscription.line) || subscription.line <= 0
      || !Number.isSafeInteger(subscription.column) || subscription.column <= 0) {
      fail(`${label} render probe contract is incomplete`);
    }
    return JSON.stringify([subscription.source, subscription.line, subscription.column]);
  });
  if (new Set(subscriptionKeys).size !== subscriptionKeys.length) {
    fail(`${label} render probe contract is incomplete`);
  }
}

function validateResourceMetric(metric, subjectSha, label) {
  if (metric?.subjectSha !== subjectSha || !Number.isSafeInteger(metric.fileCount) || metric.fileCount <= 0
    || !Array.isArray(metric.files) || metric.files.length !== metric.fileCount) {
    fail(`${label} resource raw sample set is invalid`);
  }
  const paths = metric.files.map(({ path: filePath, bytes }) => {
    if (typeof filePath !== 'string' || !filePath || !Number.isSafeInteger(bytes) || bytes <= 0) {
      fail(`${label} resource raw sample is invalid`);
    }
    return filePath;
  });
  if (new Set(paths).size !== paths.length) fail(`${label} resource paths are duplicated`);
  const total = metric.files.reduce((sum, { bytes }) => sum + bytes, 0);
  const maximum = Math.max(...metric.files.map(({ bytes }) => bytes));
  if (metric.totalBundleBytes !== total || metric.maxChunkBytes !== maximum) {
    fail(`${label} resource summary does not match raw files`);
  }
}

function validatePerformanceEnvironment(environment, label) {
  exactKeys(environment, performanceEnvironmentKeys, `${label} environment`);
  exactKeys(environment.os, ['platform', 'release', 'arch'], `${label} OS`);
  exactKeys(environment.cpu, ['model', 'logicalCores'], `${label} CPU`);
  if ([environment.os.platform, environment.os.release, environment.os.arch, environment.cpu.model,
    environment.node, environment.npm, environment.go]
    .some((value) => typeof value !== 'string' || !value.trim())
    || !Number.isSafeInteger(environment.cpu.logicalCores) || environment.cpu.logicalCores <= 0
    || !Number.isSafeInteger(environment.totalMemoryBytes) || environment.totalMemoryBytes <= 0
    || !Array.isArray(environment.loadAverage) || environment.loadAverage.length !== 3
    || environment.loadAverage.some((value) => !Number.isFinite(value) || value < 0)) {
    fail(`${label} environment metadata is incomplete`);
  }
}

function stablePerformanceEnvironment(environment) {
  return {
    os: {
      platform: environment.os.platform,
      release: environment.os.release,
      arch: environment.os.arch,
    },
    cpu: {
      model: environment.cpu.model,
      logicalCores: environment.cpu.logicalCores,
    },
    totalMemoryBytes: environment.totalMemoryBytes,
    node: environment.node,
    npm: environment.npm,
    go: environment.go,
  };
}

function runnerFileSha256(repoRoot, revision, relativePath) {
  const content = revision
    ? execFileSync('git', ['show', `${revision}:${relativePath}`], { cwd: repoRoot })
    : fs.readFileSync(path.join(repoRoot, relativePath));
  return createHash('sha256').update(content).digest('hex');
}

function validateRunnerContent(provenance, context, check, label) {
  exactKeys(provenance, [
    'runnerId', 'runnerSha', 'runnerTree', 'runnerContentHash', 'runnerFiles',
    'worktreeClean', 'worktreeStatus', 'baselineAudit',
  ], `${label} provenance`);
  if (provenance.runnerId !== 'frontend-performance-budget'
    || !/^[0-9a-f]{40}$/u.test(provenance.runnerSha)
    || !/^[0-9a-f]{40}$/u.test(provenance.runnerTree)
    || !/^[0-9a-f]{64}$/u.test(provenance.runnerContentHash)
    || provenance.worktreeClean !== true
    || !Array.isArray(provenance.worktreeStatus) || provenance.worktreeStatus.length !== 0) {
    fail(`${label} runner identity or clean-worktree binding is invalid`);
  }
  if (!gitSucceeds(context.repoRoot, ['cat-file', '-e', `${provenance.runnerSha}^{commit}`])
    || !gitSucceeds(context.repoRoot, ['merge-base', '--is-ancestor', provenance.runnerSha, context.subjectSha])
    || git(context.repoRoot, ['rev-parse', `${provenance.runnerSha}^{tree}`]) !== provenance.runnerTree) {
    fail(`${label} runner SHA/tree is not committed ancestry of the candidate`);
  }
  if (!Array.isArray(provenance.runnerFiles)) fail(`${label} runnerFiles must be an array`);
  exactSet(provenance.runnerFiles.map(({ path: runnerPath }) => runnerPath), check.runnerFiles, `${label} runnerFiles`);
  const byPath = new Map(provenance.runnerFiles.map((entry) => [entry.path, entry]));
  const aggregate = createHash('sha256');
  for (const runnerPath of check.runnerFiles) {
    const entry = byPath.get(runnerPath);
    if (!entry || !/^[0-9a-f]{64}$/u.test(entry.sha256)
      || entry.sha256 !== runnerFileSha256(context.repoRoot, provenance.runnerSha, runnerPath)) {
      fail(`${label} runner file hash mismatch: ${runnerPath}`);
    }
    aggregate.update(`${runnerPath}\0${entry.sha256}\n`);
  }
  if (aggregate.digest('hex') !== provenance.runnerContentHash) {
    fail(`${label} runnerContentHash does not match runnerFiles`);
  }
}

function validatePerformanceProvenance(evidence, context, check, baselineDocument) {
  const baseSha = baselineDocument.baseSha;
  if (baseSha !== plannedBaseSha) fail('performance baseline does not bind the frozen plan BASE_SHA');
  if (!baselineDocument.provenance || !baselineDocument.subjectSha || !baselineDocument.subjectTree) {
    notVerified('performance baseline has not been audited against immutable BASE_SHA');
  }
  if (baselineDocument.subjectSha !== baseSha || baseSha === context.subjectSha
    || !/^[0-9a-f]{40}$/u.test(baseSha)
    || !gitSucceeds(context.repoRoot, ['cat-file', '-e', `${baseSha}^{commit}`])
    || !gitSucceeds(context.repoRoot, ['merge-base', '--is-ancestor', baseSha, context.subjectSha])) {
    fail('performance baseline subject must be a distinct immutable BASE_SHA ancestor');
  }
  const baseTree = git(context.repoRoot, ['rev-parse', `${baseSha}^{tree}`]);
  if (baselineDocument.subjectTree !== baseTree || !Number.isFinite(Date.parse(baselineDocument.generatedAt))) {
    fail('performance baseline subject tree or generatedAt mismatch');
  }
  validatePerformanceEnvironment(baselineDocument.environment, 'baseline');
  validatePerformanceEnvironment(evidence.environment, 'candidate');
  if (JSON.stringify(stablePerformanceEnvironment(baselineDocument.environment))
    !== JSON.stringify(stablePerformanceEnvironment(evidence.environment))) {
    fail('performance baseline and candidate environments differ');
  }
  validateRunnerContent(baselineDocument.provenance, context, check, 'baseline');
  validateRunnerContent(evidence.provenance, context, check, 'candidate');
  if (baselineDocument.provenance.runnerContentHash !== evidence.provenance.runnerContentHash
    || JSON.stringify(baselineDocument.provenance.runnerFiles) !== JSON.stringify(evidence.provenance.runnerFiles)) {
    fail('baseline and candidate runner content differs');
  }
  for (const runnerPath of check.runnerFiles) {
    const entry = evidence.provenance.runnerFiles.find(({ path: entryPath }) => entryPath === runnerPath);
    if (runnerFileSha256(context.repoRoot, null, runnerPath) !== entry.sha256) {
      fail(`candidate worktree runner content differs from evidence: ${runnerPath}`);
    }
  }
  if (evidence.provenance.baselineAudit !== null) fail('candidate provenance must not claim a baseline audit');
  const audit = baselineDocument.provenance.baselineAudit;
  exactKeys(audit, ['baseSha', 'baseTree', 'changedPaths'], 'performance baseline audit');
  if (audit.baseSha !== baseSha || audit.baseTree !== baseTree
    || !gitSucceeds(context.repoRoot, ['merge-base', '--is-ancestor', baseSha, baselineDocument.provenance.runnerSha])) {
    fail('performance baselineAudit does not bind BASE_SHA ancestry');
  }
  if (!Array.isArray(audit.changedPaths) || new Set(audit.changedPaths).size !== audit.changedPaths.length
    || audit.changedPaths.some((changedPath) => !performanceAuditPathAllowed(changedPath))) {
    fail('performance baselineAudit changedPaths violate the frozen allowlist');
  }
  const actualChangedPaths = git(context.repoRoot, [
    'diff', '--name-only', baseSha, baselineDocument.provenance.runnerSha,
  ]).split('\n').filter(Boolean);
  if (JSON.stringify(audit.changedPaths) !== JSON.stringify(actualChangedPaths)) {
    fail('performance baselineAudit changedPaths exact order mismatch');
  }
}

function recomputePerformanceStatuses(evidence, baselineDocument, context) {
  const current = evidence.metrics;
  const frozen = baselineDocument.metrics;
  exactSet(Object.keys(current || {}), Object.keys(performanceCaseIds), 'performance evidence metricIds');
  exactSet(Object.keys(frozen || {}), Object.keys(performanceCaseIds), 'performance baseline metricIds');
  for (const metricId of Object.keys(performanceCaseIds)) {
    if (current[metricId]?.metricId !== metricId || frozen[metricId]?.metricId !== metricId) {
      fail(`${metricId} metric identity mismatch`);
    }
    if (frozen[metricId]?.status !== 'PASS' || frozen[metricId].subjectSha !== baselineDocument.baseSha) {
      fail(`${metricId} baseline is not an audited PASS bound to BASE_SHA`);
    }
  }
  validateRenderMetric(current['P01-render-isolation'], context.subjectSha, 'P01 current');
  validateRenderMetric(frozen['P01-render-isolation'], baselineDocument.baseSha, 'P01 baseline');
  if (frozen['P01-render-isolation'].absoluteUpdateLimit !== 1) fail('P01 absolute update limit must equal 1');
  validateTimingMetric(current['P02-history-budget'], performanceCaseIds['P02-history-budget'], context.subjectSha, 'P02 current');
  validateTimingMetric(frozen['P02-history-budget'], performanceCaseIds['P02-history-budget'], baselineDocument.baseSha, 'P02 baseline');
  validateTimingMetric(current['P03-feedback-budget'], performanceCaseIds['P03-feedback-budget'], context.subjectSha, 'P03 current');
  validateTimingMetric(frozen['P03-feedback-budget'], performanceCaseIds['P03-feedback-budget'], baselineDocument.baseSha, 'P03 baseline');
  if (frozen['P02-history-budget'].maxRegressionRatio !== 1.15
    || frozen['P03-feedback-budget'].maxRegressionRatio !== 1.15) {
    fail('P02/P03 frozen regression ratio must equal 1.15');
  }
  validateResourceMetric(current['P04-resource-budget'], context.subjectSha, 'P04 current');
  validateResourceMetric(frozen['P04-resource-budget'], baselineDocument.baseSha, 'P04 baseline');
  if (frozen['P04-resource-budget'].maxRegressionRatio !== 1.05) fail('P04 frozen regression ratio must equal 1.05');

  const statuses = new Map();
  const p01Current = current['P01-render-isolation'];
  const p01Frozen = frozen['P01-render-isolation'];
  statuses.set('render-main-page-update-commits', p01Current.mainPageUpdateCommits <= Math.min(1, p01Frozen.mainPageUpdateCommits));
  statuses.set('render-unrelated-subtree-update-commits', p01Current.unrelatedSubtreeUpdateCommits <= Math.min(1, p01Frozen.unrelatedSubtreeUpdateCommits));
  statuses.set('render-broad-subscription-mutation-detected', p01Current.mutationDetected === true);
  for (const metricId of ['P02-history-budget', 'P03-feedback-budget']) {
    for (const caseId of performanceCaseIds[metricId]) {
      statuses.set(caseId, current[metricId].cases[caseId].durationMedianMs
        <= frozen[metricId].cases[caseId].durationMedianMs * 1.15);
    }
  }
  statuses.set('bundle-total-bytes', current['P04-resource-budget'].totalBundleBytes
    <= frozen['P04-resource-budget'].totalBundleBytes * 1.05);
  statuses.set('bundle-max-chunk-bytes', current['P04-resource-budget'].maxChunkBytes
    <= frozen['P04-resource-budget'].maxChunkBytes * 1.05);
  return statuses;
}

function validatePerformanceReport(report, {
  context, check, startedAt, finishedAt, baselineDocument = baseline,
}) {
  if (report?.schemaVersion !== 1 || report.evidence?.schemaVersion !== 1) {
    fail('performance report and evidence schemaVersion must equal 1');
  }
  validatePerformanceProvenance(report.evidence, context, check, baselineDocument);
  validateReportBinding(report.evidence, context, startedAt, finishedAt);
  const computedCaseStatuses = recomputePerformanceStatuses(report.evidence, baselineDocument, context);
  const verdict = report.verdict;
  validateExactCaseResult(verdict?.caseIds, verdict?.testCount, {
    caseIds: allPerformanceCaseIds,
    testCount: allPerformanceCaseIds.length,
  }, 'performance aggregate');
  if (!Array.isArray(verdict.caseResults) || verdict.caseResults.length !== allPerformanceCaseIds.length) {
    fail('performance caseResults exact count mismatch');
  }
  exactSet(verdict.caseResults.map(({ caseId }) => caseId), allPerformanceCaseIds, 'performance caseResults');
  for (const [metricId, caseIds] of Object.entries(performanceCaseIds)) {
    for (const caseId of caseIds) {
      const result = verdict.caseResults.find((entry) => entry.caseId === caseId);
      if (result?.metricId !== metricId) fail(`performance case metric mismatch: ${caseId}`);
    }
  }
  const selectedCases = verdict.caseResults.filter(({ metricId }) => metricId === check.metricId);
  validateExactCaseResult(selectedCases.map(({ caseId }) => caseId), selectedCases.length, check, check.metricId);
  const metricVerdicts = Array.isArray(verdict.verdicts) ? verdict.verdicts : [];
  exactSet(metricVerdicts.map(({ metricId }) => metricId), Object.keys(performanceCaseIds), 'performance metricIds');
  for (const entry of verdict.caseResults) {
    const expected = computedCaseStatuses.get(entry.caseId) ? 'PASS' : 'FAIL';
    if (entry.status !== expected) fail(`performance case status was not derived from raw evidence: ${entry.caseId}`);
  }
  for (const entry of metricVerdicts) {
    const expected = performanceCaseIds[entry.metricId].every((caseId) => computedCaseStatuses.get(caseId)) ? 'PASS' : 'FAIL';
    if (entry.status !== expected) fail(`performance metric status was not derived from raw evidence: ${entry.metricId}`);
  }
  const aggregateStatus = [...computedCaseStatuses.values()].every(Boolean) ? 'PASS' : 'FAIL';
  if (verdict.status !== aggregateStatus) fail('performance aggregate status was not derived from raw evidence');
  const metricVerdict = metricVerdicts.find(({ metricId }) => metricId === check.metricId);
  const status = normalizedRunnerStatus(metricVerdict.status);
  const caseStatuses = selectedCases.map((entry) => normalizedRunnerStatus(entry.status));
  if (status === 'PASS' && caseStatuses.some((caseStatus) => caseStatus !== 'PASS')) {
    fail(`${check.metricId} PASS conflicts with case results`);
  }
  if (status === 'FAIL' && !caseStatuses.includes('FAIL')) fail(`${check.metricId} FAIL has no failed case`);
  return status;
}

function validateDeliveryReport(report, { context, control, check, startedAt, finishedAt }) {
  validateReportBinding(report, context, startedAt, finishedAt);
  if (report.metricId !== control.id) fail('delivery smoke metricId mismatch');
  validateExactCaseResult(report.caseIds, report.testCount, check, 'delivery smoke');
  const commands = report.verdict?.commands;
  if (!Array.isArray(commands)) fail('delivery smoke commands must be an array');
  exactSet(commands.map(({ id }) => id), check.caseIds, 'delivery smoke commands');
  const status = normalizedRunnerStatus(report.verdict.status);
  if (status === 'PASS' && commands.some((command) => command.status !== 'PASS')) {
    fail('delivery smoke PASS conflicts with command results');
  }
  return status;
}

function validateActualRunnerEvidence(report, options) {
  switch (options.check.evidenceProtocol) {
    case 'failure-matrix-report-v1': return validateFailureMatrixReport(report, options);
    case 'desktop-failure-report-v1': return validateDesktopFailureReport(report, options);
    case 'performance-budget-json-v1': return validatePerformanceReport(report, options);
    case 'delivery-smoke-json-v1': return validateDeliveryReport(report, options);
    default: fail(`unsupported actual runner protocol: ${options.check.evidenceProtocol}`);
  }
}

export function structuredEvidenceStatus(evidence, options) {
  try {
    return validateActualRunnerEvidence(evidence, options);
  }
  catch (error) {
    return error instanceof NotVerifiedEvidenceError ? 'NOT_VERIFIED' : 'FAIL';
  }
}

function commandResult(command, args, options) {
  const result = spawnSync(command, args, {
    cwd: options.cwd,
    encoding: 'utf8',
    env: options.env,
    maxBuffer: 16 * 1024 * 1024,
    stdio: ['ignore', 'pipe', 'pipe'],
    timeout: options.timeoutMs,
  });
  return {
    exitCode: result.status ?? 1,
    stdout: result.stdout || '',
    stderr: result.stderr || '',
    error: result.error,
  };
}

function expandedRunnerArgv(context, check) {
  return check.argv.map((argument) => {
    if (argument === '$SUBJECT_SHA') return context.subjectSha;
    if (argument === '$FROZEN_BASELINE') return path.join(frozenScriptRoot, 'frontend-maintainability-baseline.json');
    return argument;
  });
}

function executeActualRunner(context, check) {
  if (!context.evidenceCache) context.evidenceCache = new Map();
  const argv = expandedRunnerArgv(context, check);
  const cacheKey = JSON.stringify([check.evidenceProtocol, check.cwd, argv, check.reportPath || '']);
  if (context.evidenceCache.has(cacheKey)) return context.evidenceCache.get(cacheKey);
  const [command, runnerPath, ...args] = argv;
  const cwd = fs.realpathSync(path.resolve(context.repoRoot, check.cwd));
  const requestedRunnerPath = path.resolve(cwd, runnerPath);
  if (!fs.existsSync(requestedRunnerPath)) {
    const missing = { missing: true, runnerPath: path.join(check.cwd, runnerPath) };
    context.evidenceCache.set(cacheKey, missing);
    return missing;
  }
  if (!fs.lstatSync(requestedRunnerPath).isFile()) {
    const invalid = { invalid: true, runnerPath: path.join(check.cwd, runnerPath) };
    context.evidenceCache.set(cacheKey, invalid);
    return invalid;
  }
  const absoluteRunnerPath = fs.realpathSync(requestedRunnerPath);
  const reportPath = check.reportPath ? path.resolve(context.repoRoot, check.reportPath) : undefined;
  if (reportPath) fs.rmSync(reportPath, { force: true });
  const startedAt = Date.now();
  const result = commandResult(command === 'node' ? process.execPath : command, [absoluteRunnerPath, ...args], {
    cwd,
    timeoutMs: check.timeoutMs,
    env: process.env,
  });
  const finishedAt = Date.now();
  let report;
  try {
    const raw = reportPath ? fs.readFileSync(reportPath, 'utf8') : result.stdout;
    report = JSON.parse(raw.trim());
  }
  catch (error) {
    report = { parseError: error.message };
  }
  const executed = { result, report, startedAt, finishedAt };
  context.evidenceCache.set(cacheKey, executed);
  return executed;
}

export function actionProducerGuardOutputStatus(stdout) {
  const match = String(stdout || '').trim().match(
    /^action producer guard passed: discovered=(\d+) covered=(\d+) exempted=(\d+)$/u,
  );
  if (!match) return 'FAIL';
  const [, discoveredText, coveredText, exemptedText] = match;
  const discovered = Number(discoveredText);
  const covered = Number(coveredText);
  const exempted = Number(exemptedText);
  if (discovered > 0 && covered === discovered && exempted === 0) return 'PASS';
  if (discovered > 0 && covered + exempted === discovered && exempted > 0) return 'NOT_VERIFIED';
  return 'FAIL';
}

function actionProducerGuardProbe(context, control, check) {
  const execution = executeActualRunner(context, check);
  if (execution.missing) {
    return evidenceRecord(context, control, check, {
      status: 'NOT_VERIFIED', exitCode: null, summary: `target runner is missing: ${execution.runnerPath}`,
    });
  }
  if (execution.invalid) {
    return evidenceRecord(context, control, check, {
      status: 'FAIL', exitCode: 1, summary: `target runner must be a regular file: ${execution.runnerPath}`,
    });
  }
  const { result } = execution;
  const output = `${result.stdout}${result.stderr}`.trim();
  if (result.error || result.exitCode !== 0) {
    return evidenceRecord(context, control, check, {
      status: 'FAIL', exitCode: result.exitCode, summary: output.slice(-1200) || result.error?.message,
    });
  }
  const status = actionProducerGuardOutputStatus(result.stdout);
  return evidenceRecord(context, control, check, {
    status,
    exitCode: status === 'PASS' ? 0 : status === 'FAIL' ? 1 : null,
    summary: result.stdout.trim() || 'action producer guard output format mismatch',
  });
}

function structuredProbe(context, control, check) {
  if (check.evidenceProtocol === 'action-producer-guard-v1') {
    return actionProducerGuardProbe(context, control, check);
  }
  const execution = executeActualRunner(context, check);
  if (execution.missing) {
    return evidenceRecord(context, control, check, {
      status: 'NOT_VERIFIED', exitCode: null, summary: `target runner is missing: ${execution.runnerPath}`,
    });
  }
  if (execution.invalid) {
    return evidenceRecord(context, control, check, {
      status: 'FAIL', exitCode: 1, summary: `target runner must be a regular file: ${execution.runnerPath}`,
    });
  }
  const { result, report, startedAt, finishedAt } = execution;
  const output = `${result.stdout}${result.stderr}`.trim();
  if (result.error) {
    return evidenceRecord(context, control, check, {
      status: 'FAIL', exitCode: result.exitCode, summary: result.error.message,
    });
  }
  if (report.parseError) {
    return evidenceRecord(context, control, check, {
      status: 'FAIL', exitCode: result.exitCode, summary: `runner report is not valid JSON: ${report.parseError}; ${output.slice(-600)}`,
    });
  }
  let status;
  try {
    status = validateActualRunnerEvidence(report, { context, control, check, startedAt, finishedAt });
  }
  catch (error) {
    const notVerifiedResult = error instanceof NotVerifiedEvidenceError;
    return evidenceRecord(context, control, check, {
      status: notVerifiedResult ? 'NOT_VERIFIED' : 'FAIL',
      exitCode: notVerifiedResult ? null : 1,
      summary: error.message,
    });
  }
  if (status === 'PASS' && result.exitCode !== 0) {
    return evidenceRecord(context, control, check, {
      status: 'FAIL', exitCode: result.exitCode, summary: 'runner reported PASS with a non-zero exit',
    });
  }
  return evidenceRecord(context, control, check, {
    status,
    exitCode: status === 'PASS' ? 0 : status === 'FAIL' ? 1 : null,
    summary: `${check.evidenceProtocol} ${status}; cases=${check.caseIds.length} tests=${check.testCount}`,
  });
}

export function commandEvidenceStatus({ repoRoot = frozenRepoRoot, cwd = '.', argv, timeoutMs = 10_000 }) {
  if (!Array.isArray(argv) || argv.length === 0) fail('command evidence argv is empty');
  const [command, ...args] = argv;
  const result = commandResult(command, args, {
    cwd: path.resolve(repoRoot, cwd),
    timeoutMs,
    env: process.env,
  });
  return result.error || result.exitCode !== 0 ? 'FAIL' : 'PASS';
}

function evidenceRecord(context, control, check, result) {
  return {
    scoreBaseSha: context.scoreBaseSha,
    subjectSha: context.subjectSha,
    subjectTree: context.subjectTree,
    controlId: control.id,
    cwd: check.cwd,
    argv: [...check.argv],
    caseIds: [...check.caseIds],
    testCount: check.testCount,
    recordedAt: new Date().toISOString(),
    environment: { node: process.version, platform: process.platform, arch: process.arch },
    ...result,
  };
}

function runCommand(context, control, check) {
  const [command, ...args] = check.argv;
  const cwd = path.resolve(context.repoRoot, check.cwd);
  const result = commandResult(command, args, {
    cwd,
    timeoutMs: check.timeoutMs,
    env: process.env,
  });
  const output = `${result.stdout}${result.stderr}`.trim();
  return evidenceRecord(context, control, check, {
    status: result.error || result.exitCode !== 0 ? 'FAIL' : 'PASS',
    exitCode: result.exitCode,
    summary: output.slice(result.exitCode === 0 ? -600 : -1200) || result.error?.message || '',
  });
}

function evaluateProbe(context, control, check) {
  try {
    if (check.evidenceProtocol === 'vitest-json-v1') return vitestProbe(context, control, check);
    if (check.evidenceProtocol === 'artifact-v1') return artifactProbe(context, control, check);
    if (customEvidenceProtocols.has(check.evidenceProtocol)) {
      return structuredProbe(context, control, check);
    }
    fail(`unsupported executable probe protocol: ${check.evidenceProtocol}`);
  }
  catch (error) {
    return evidenceRecord(context, control, check, { status: 'FAIL', exitCode: 1, summary: error.message });
  }
}

function evaluateCheck(context, control, check, runCommands) {
  if (check.kind === 'probe') {
    if (customEvidenceProtocols.has(check.evidenceProtocol) && !runCommands) {
      return evidenceRecord(context, control, check, {
        status: 'NOT_VERIFIED',
        exitCode: null,
        summary: 'structured evidence execution was not requested',
      });
    }
    return evaluateProbe(context, control, check);
  }
  if (!runCommands) {
    return evidenceRecord(context, control, check, {
      status: 'NOT_VERIFIED',
      exitCode: null,
      summary: 'command execution was not requested',
    });
  }
  return runCommand(context, control, check);
}

export function controlStatus(results) {
  if (results.length === 0) return 'NOT_VERIFIED';
  if (results.some(({ status }) => status === 'FAIL')) return 'FAIL';
  if (results.every(({ status }) => status === 'PASS')) return 'PASS';
  return 'NOT_VERIFIED';
}

function validateFixtureDocument(fixtureDocument) {
  if (!Array.isArray(fixtureDocument.fixtures)) fail('RED fixtures must be an array');
  const fixtureIDs = fixtureDocument.fixtures.map(({ id }) => id);
  if (new Set(fixtureIDs).size !== fixtureIDs.length) fail('duplicate RED fixture id');
  exactSet(fixtureIDs, frozenFixtureIDs, 'frozen RED fixture ids');
  for (const fixture of fixtureDocument.fixtures) {
    exactKeys(fixture, ['id', 'area', 'expected'], `RED fixture ${fixture.id}`);
    if (![fixture.id, fixture.area, fixture.expected].every((value) => typeof value === 'string' && value.length > 0)) {
      fail(`invalid RED fixture: ${fixture.id}`);
    }
  }
}

function validateVitestCheck(control, check) {
  if (check.evidenceProtocol !== 'vitest-json-v1') fail(`invalid Vitest evidence protocol: ${control.id}`);
  if (check.argv[0] !== 'node' || check.argv[1] !== 'node_modules/vitest/vitest.mjs'
    || !check.argv.includes('--reporter=json')) {
    fail(`invalid frozen Vitest command: ${control.id}`);
  }
  if (!Array.isArray(check.sourcePaths) || check.sourcePaths.length === 0
    || new Set(check.sourcePaths).size !== check.sourcePaths.length) {
    fail(`invalid source fingerprint contract: ${control.id}`);
  }
  if (!Array.isArray(check.testNames) || check.testNames.length !== check.testCount
    || new Set(check.testNames).size !== check.testNames.length) {
    fail(`exact named test evidence mismatch: ${control.id}`);
  }
}

function validateArtifactCheck(control, check) {
  if (!artifactProbes.has(check.probe) || check.evidenceProtocol !== 'artifact-v1') {
    fail(`unknown scorer probe: ${control.id}`);
  }
  const expectedArgv = ['node', 'scripts/frontend-maintainability-score.mjs', '--probe', check.probe];
  if (JSON.stringify(check.argv) !== JSON.stringify(expectedArgv)
    || !Array.isArray(check.sourcePaths) || check.sourcePaths.length === 0) {
    fail(`invalid frozen artifact probe: ${control.id}`);
  }
}

function validateStructuredCheck(control, check) {
  const exact = (actual, expected, label) => {
    if (JSON.stringify(actual) !== JSON.stringify(expected)) fail(`${label} differs from frozen contract: ${control.id}`);
  };
  if (check.evidenceProtocol === 'action-producer-guard-v1') {
    const expectedProbe = control.id === 'A04-action-registry' ? 'actionProducerGuard' : 'criticalActionCoverage';
    if (!['A04-action-registry', 'T02-critical-action-coverage'].includes(control.id)
      || check.probe !== expectedProbe || check.requireZeroExemptions !== true) {
      fail(`invalid action producer guard contract: ${control.id}`);
    }
    exact(check.argv, ['node', 'scripts/action-producer-guard.mjs'], 'action producer argv');
    exact(check.caseIds, ['action-scorer-missing-stale-zero-test'], 'action producer caseIds');
    if (check.testCount !== 1) fail(`action producer testCount differs from frozen contract: ${control.id}`);
    return;
  }
  if (check.evidenceProtocol === 'failure-matrix-report-v1') {
    const expected = failureMatrixChecks[control.id];
    if (!expected || check.probe !== expected.probe) fail(`invalid failure matrix control: ${control.id}`);
    exact(check.argv, ['node', 'frontend-app/scripts/failure-matrix-runner.mjs'], 'failure matrix argv');
    exact(check.caseIds, expected.caseIds, 'failure matrix caseIds');
    if (check.cwd !== '.' || check.reportPath !== '.tmp/failure-matrix/report.json'
      || check.testCount !== expected.testCount) {
      fail(`failure matrix report contract differs from frozen contract: ${control.id}`);
    }
    return;
  }
  if (check.evidenceProtocol === 'desktop-failure-report-v1') {
    if (control.id !== 'T03-wails-integration' || check.probe !== 'desktopFailureSmoke') {
      fail(`invalid desktop failure smoke control: ${control.id}`);
    }
    exact(check.argv, ['node', 'scripts/desktop-failure-smoke.mjs'], 'desktop failure smoke argv');
    exact(check.caseIds, ['terminal-failed', 'prompt-history-reject'], 'desktop failure smoke caseIds');
    if (check.reportPath !== '.tmp/desktop-failure-smoke/report.json' || check.testCount !== 2) {
      fail(`desktop failure smoke report contract differs from frozen contract: ${control.id}`);
    }
    return;
  }
  if (check.evidenceProtocol === 'performance-budget-json-v1') {
    if (!Object.hasOwn(performanceCaseIds, control.id) || check.probe !== 'performanceBudget'
      || check.metricId !== control.id || !Object.hasOwn(baseline.metrics || {}, control.id)) {
      fail(`invalid performance budget control: ${control.id}`);
    }
    exact(check.argv, ['node', 'scripts/performance-budget-runner.mjs', '--verify', '--subject', '$SUBJECT_SHA', '--baseline', '$FROZEN_BASELINE'], 'performance budget argv');
    exact(check.caseIds, performanceCaseIds[control.id], 'performance budget caseIds');
    exact(check.runnerFiles, performanceRunnerFiles, 'performance audited runner files');
    if (check.testCount !== performanceCaseIds[control.id].length) {
      fail(`performance testCount differs from frozen contract: ${control.id}`);
    }
    return;
  }
  if (check.evidenceProtocol === 'delivery-smoke-json-v1') {
    if (control.id !== 'T05-build-embed-smoke' || check.probe !== 'deliverySmoke'
      || check.metricId !== control.id) fail(`invalid delivery smoke control: ${control.id}`);
    exact(check.argv, ['node', 'scripts/delivery-smoke-runner.mjs', '--verify', '--subject', '$SUBJECT_SHA'], 'delivery smoke argv');
    exact(check.caseIds, deliveryCaseIds, 'delivery smoke caseIds');
    if (check.testCount !== deliveryCaseIds.length) fail(`delivery smoke testCount differs from frozen contract: ${control.id}`);
    return;
  }
  fail(`unregistered structured evidence runner: ${control.id}`);
}

function validateConfiguredCheck(control, check) {
  if (!['command', 'probe'].includes(check.kind) || typeof check.cwd !== 'string'
    || !Array.isArray(check.argv) || check.argv.length === 0) {
    fail(`invalid runner command: ${control.id}`);
  }
  if (weakCommands.has(check.argv[0]) || check.argv.includes('--help')
    || !Number.isInteger(check.timeoutMs) || check.timeoutMs <= 0) {
    fail(`weak runner command: ${control.id}`);
  }
  if ('status' in check || 'score' in check) fail(`hand-authored check result is forbidden: ${control.id}`);
  if (!Array.isArray(check.caseIds) || check.caseIds.length === 0
    || !Number.isInteger(check.testCount) || check.testCount <= 0) {
    fail(`zero-test runner evidence: ${control.id}`);
  }
  if (new Set(check.caseIds).size !== check.caseIds.length) fail(`duplicate fixture case: ${control.id}`);
  if (!customEvidenceProtocols.has(check.evidenceProtocol)) {
    for (const caseID of check.caseIds) {
      if (!caseID.startsWith('frontend-') && !frozenFixtureIDs.has(caseID)) fail(`missing fixture case: ${caseID}`);
    }
  }
  if (check.kind !== 'probe') return;
  if (check.evidenceProtocol === 'vitest-json-v1') validateVitestCheck(control, check);
  else if (check.evidenceProtocol === 'artifact-v1') validateArtifactCheck(control, check);
  else validateStructuredCheck(control, check);
}

function validateConfiguredControl(config, control, seen) {
  if (!frozenControlIDs.has(control.id) || seen.has(control.id)) fail(`duplicate or unknown control id: ${control.id}`);
  seen.add(control.id);
  if ('status' in control || 'score' in control) fail(`hand-authored result is forbidden: ${control.id}`);
  if (typeof control.required !== 'boolean' || !Number.isFinite(control.points) || control.points <= 0
    || !Array.isArray(control.allOf) || control.allOf.length === 0) {
    fail(`invalid control shape: ${control.id}`);
  }
  if (requiredDoDControls.has(control.id) && !control.required) fail(`DoD control must be required: ${control.id}`);
  if (!Object.hasOwn(config.weights, control.dimension)) fail(`unknown control dimension: ${control.id}`);
  for (const check of control.allOf) validateConfiguredCheck(control, check);
  if (Object.hasOwn(plannedLaneAllOfArgv, control.id)) {
    const actual = control.allOf.map(({ argv }) => JSON.stringify(argv));
    const expected = plannedLaneAllOfArgv[control.id].map((argv) => JSON.stringify(argv));
    exactSet(actual, expected, `lane allOf argv ${control.id}`);
  }
}

export function validateConfiguration(config = controls, fixtureDocument = fixtures) {
  if (config.schemaVersion !== 1 || fixtureDocument.schemaVersion !== 1) fail('unsupported scorer schema version');
  if (!Array.isArray(config.controls) || config.controls.length !== 25) fail('controls must contain exactly 25 entries');
  exactSet(Object.keys(config.weights || {}), ['E', 'A', 'C', 'T', 'P'], 'dimension weights');
  if (Object.values(config.weights).reduce((sum, weight) => sum + weight, 0) !== 100) fail('dimension weights must total 100');
  if (JSON.stringify(config.thresholds) !== JSON.stringify(plannedThresholds)) fail('score thresholds differ from the frozen plan');
  validateFixtureDocument(fixtureDocument);

  const seen = new Set();
  for (const control of config.controls) validateConfiguredControl(config, control, seen);
  exactSet(seen, frozenControlIDs, 'control ids');
  for (const dimension of Object.keys(config.weights)) {
    const points = config.controls
      .filter((control) => control.dimension === dimension)
      .reduce((sum, control) => sum + control.points, 0);
    if (points !== 100) fail(`dimension points must total 100: ${dimension}`);
  }
  if (baseline.baseSha !== plannedBaseSha || !/^[0-9a-f]{40}$/.test(baseline.planSnapshotSha)) {
    fail('baseline provenance is incomplete');
  }
  return true;
}
function scoreContext(context, { runCommands }) {
  const executionContext = { ...context, evidenceCache: new Map() };
  const scoredControls = controls.controls.map((control) => {
    const evidence = control.allOf.map((check) => evaluateCheck(executionContext, control, check, runCommands));
    return {
      id: control.id,
      dimension: control.dimension,
      points: control.points,
      required: control.required,
      status: controlStatus(evidence),
      evidence,
    };
  });
  const dimensions = {};
  for (const dimension of Object.keys(controls.weights)) {
    const members = scoredControls.filter((control) => control.dimension === dimension);
    const earned = members.filter((control) => control.status === 'PASS').reduce((sum, control) => sum + control.points, 0);
    const total = members.reduce((sum, control) => sum + control.points, 0);
    dimensions[dimension] = {
      earned,
      total,
      score: total === 0 ? 0 : (earned / total) * 100,
      weight: controls.weights[dimension],
    };
  }
  const rawBasisPoints = Object.values(dimensions)
    .reduce((sum, dimension) => sum + (dimension.score * dimension.weight), 0);
  return {
    scoreBaseSha: context.scoreBaseSha,
    subjectSha: context.subjectSha,
    subjectTree: context.subjectTree,
    baseline,
    controls: scoredControls,
    dimensions,
    rawBasisPoints: Math.round(rawBasisPoints),
    displayScore: Number((rawBasisPoints / 100).toFixed(1)),
  };
}

export function scoreCurrentTree({
  runCommands = false,
  repoRoot = frozenRepoRoot,
  subjectSha,
  requireClean = subjectSha !== undefined,
} = {}) {
  validateConfiguration();
  const context = inspectTargetRepository({ repoRoot, subjectSha, requireClean });
  return scoreContext(context, { runCommands });
}

function probeCheck(probe) {
  for (const control of controls.controls) {
    const check = control.allOf.find((candidate) => candidate.kind === 'probe' && candidate.probe === probe);
    if (check) return { control, check };
  }
  return undefined;
}

export function probeResult(probe, { repoRoot = frozenRepoRoot, subjectSha } = {}) {
  validateConfiguration();
  const match = probeCheck(probe);
  if (!match) return 'NOT_VERIFIED';
  const context = inspectTargetRepository({ repoRoot, subjectSha, requireClean: subjectSha !== undefined });
  return evaluateProbe(context, match.control, match.check).status;
}

function dependencySource(context) {
  return dependencyAppRoots(context).find((candidate) => (
    fs.existsSync(path.join(candidate, 'node_modules', 'vitest', 'vitest.mjs'))
    && sameDependencyManifest(context.appRoot, candidate)
  ));
}

function mountDetachedDependencies(sourceAppRoot, detachedAppRoot) {
  const sourceNodeModules = path.join(sourceAppRoot, 'node_modules');
  const detachedNodeModules = path.join(detachedAppRoot, 'node_modules');
  fs.mkdirSync(detachedNodeModules);
  for (const entry of fs.readdirSync(sourceNodeModules).sort()) {
    const sourcePath = path.join(sourceNodeModules, entry);
    const detachedPath = path.join(detachedNodeModules, entry);
    fs.symlinkSync(sourcePath, detachedPath, fs.statSync(sourcePath).isDirectory() ? 'dir' : 'file');
  }
}

function withDetachedSubject(context, callback) {
  const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), 'frontend-maintainability-subject-'));
  const detachedRoot = path.join(fs.realpathSync(tempRoot), 'repo');
  execFileSync('git', ['worktree', 'add', '--detach', detachedRoot, context.subjectSha], {
    cwd: context.repoRoot,
    stdio: 'ignore',
  });
  try {
    const executionContext = contextForExecution(context, fs.realpathSync(detachedRoot));
    const dependencies = dependencySource(context);
    const detachedNodeModules = path.join(executionContext.appRoot, 'node_modules');
    if (dependencies && !fs.existsSync(detachedNodeModules)) {
      mountDetachedDependencies(dependencies, executionContext.appRoot);
    }
    return callback(executionContext);
  }
  finally {
    execFileSync('git', ['worktree', 'remove', '--force', detachedRoot], { cwd: context.repoRoot, stdio: 'ignore' });
    fs.rmSync(tempRoot, { recursive: true, force: true });
  }
}

function writeReport(result) {
  const reportRoot = fs.mkdtempSync(path.join(os.tmpdir(), 'frontend-maintainability-score-'));
  const reportPath = path.join(reportRoot, `${result.subjectSha}.json`);
  fs.writeFileSync(reportPath, `${JSON.stringify(result, null, 2)}\n`);
  return reportPath;
}

function printScore(result, reportPath) {
  for (const control of result.controls) process.stdout.write(`${control.id}\t${control.status}\n`);
  process.stdout.write(`SCORE\t${result.displayScore.toFixed(1)}\t${result.subjectSha}\n`);
  process.stdout.write(`SUBJECT_TREE\t${result.subjectTree}\n`);
  process.stdout.write(`SCORE_BASE\t${result.scoreBaseSha}\n`);
  if (reportPath) process.stdout.write(`REPORT\t${reportPath}\n`);
}

function finalGateFailures(result) {
  const failures = [];
  if (result.displayScore < controls.thresholds.overall) {
    failures.push(`score ${result.displayScore.toFixed(1)} < ${controls.thresholds.overall}`);
  }
  for (const [dimension, minimum] of Object.entries(controls.thresholds.dimensions)) {
    if (result.dimensions[dimension].score < minimum) {
      failures.push(`${dimension} dimension ${result.dimensions[dimension].score.toFixed(1)} < ${minimum}`);
    }
  }
  const required = result.controls.filter((control) => control.required && control.status !== 'PASS');
  if (required.length > 0) failures.push(`required controls not PASS: ${required.map(({ id }) => id).join(', ')}`);
  return failures;
}

function parseCLI(args) {
  const mode = args[0];
  if (!['--validate', '--probe', '--score', '--final'].includes(mode)) fail('unknown scorer mode');
  if (mode === '--validate') {
    if (args.length !== 1) fail('--validate does not accept additional arguments');
    return { mode };
  }
  let index = 1;
  let probe;
  if (mode === '--probe') {
    probe = args[index];
    if (!probe || probe.startsWith('--')) fail('--probe requires a probe name');
    index += 1;
  }
  const parsed = { mode, probe, runCommands: false };
  while (index < args.length) {
    const argument = args[index];
    if (argument === '--run' && mode === '--score' && parsed.runCommands === false) {
      parsed.runCommands = true;
      index += 1;
      continue;
    }
    if (['--repo', '--subject'].includes(argument) && parsed[argument.slice(2)] === undefined) {
      const value = args[index + 1];
      if (!value || value.startsWith('--')) fail(`${argument} requires a value`);
      parsed[argument.slice(2)] = value;
      index += 2;
      continue;
    }
    fail(`unknown or duplicate scorer argument: ${argument}`);
  }
  if (mode === '--final' && (!parsed.repo || !parsed.subject)) fail('--final requires --repo and --subject');
  return parsed;
}

if (process.argv[1] && fs.realpathSync(path.resolve(process.argv[1])) === fs.realpathSync(scriptPath)) {
  const cli = parseCLI(process.argv.slice(2));
  if (cli.mode === '--validate') {
    validateConfiguration();
    process.stdout.write('frontend maintainability scorer configuration valid\n');
  }
  else if (cli.mode === '--probe') {
    process.stdout.write(`${probeResult(cli.probe, { repoRoot: cli.repo, subjectSha: cli.subject })}\n`);
  }
  else if (cli.mode === '--score') {
    const result = scoreCurrentTree({
      runCommands: cli.runCommands,
      repoRoot: cli.repo,
      subjectSha: cli.subject,
    });
    printScore(result);
  }
  else {
    validateConfiguration();
    assertFrozenScorerClean();
    const targetContext = inspectTargetRepository({
      repoRoot: cli.repo,
      subjectSha: cli.subject,
      requireClean: true,
      requireFinalContract: true,
    });
    const result = withDetachedSubject(targetContext, (executionContext) => (
      scoreContext(executionContext, { runCommands: true })
    ));
    const reportPath = writeReport(result);
    printScore(result, reportPath);
    const failures = finalGateFailures(result);
    if (failures.length > 0) {
      process.stderr.write(`FINAL_GATE\tFAIL\t${failures.join('; ')}\n`);
      process.exitCode = 1;
    }
    else {
      process.stdout.write('FINAL_GATE\tPASS\n');
    }
  }
}
