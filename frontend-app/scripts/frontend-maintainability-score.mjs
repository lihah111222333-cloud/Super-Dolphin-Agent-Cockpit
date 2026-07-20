import { execFileSync, spawn, spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';
import { FROZEN_T04_T05_EXECUTION_CLOSURE_PATHS } from './frontend-execution-closure.mjs';
import {
  assertPerformanceBaselineProvenance,
  isAllowedPerformanceBaselinePath,
} from './performance-baseline-provenance.mjs';
import { productionActionFailureMatrixTitle } from '../src/shared/ui/productionActionFailureMatrixTitles.js';
import { runManagedCommand, terminateManagedCommands } from './managed-command.mjs';
import { validateV8HeapEvidence } from './resource-budget.mjs';

const scriptPath = fileURLToPath(import.meta.url);
const frozenScriptRoot = path.dirname(scriptPath);
const frozenAppRoot = path.resolve(frozenScriptRoot, '..');
const frozenRepoRoot = path.resolve(frozenAppRoot, '..');
const controls = withFrozenDeliveryRunnerFiles(readFrozenJSON('frontend-maintainability-controls.json'));
const fixtures = readFrozenJSON('frontend-maintainability-red-fixtures.json');
const baseline = readFrozenJSON('frontend-maintainability-baseline.json');
const dependencyIntegrity = readFrozenJSON('frontend-maintainability-dependencies.json');
const frozenControlIDs = new Set(controls.controls.map(({ id }) => id));
const frozenFixtureIDs = new Set(fixtures.fixtures.map(({ id }) => id));
const weakCommands = new Set([':', 'echo', 'false', 'true']);
const governancePaths = [
  'frontend-app/scripts/frontend-maintainability-controls.json',
  'frontend-app/scripts/frontend-maintainability-score.mjs',
  'frontend-app/scripts/frontend-maintainability-baseline.json',
  'frontend-app/scripts/frontend-maintainability-dependencies.json',
  'frontend-app/scripts/frontend-maintainability-red-fixtures.json',
  'frontend-app/scripts/failure-matrix-runner.mjs',
  'frontend-app/scripts/failure-matrix-cases.json',
  'frontend-app/scripts/failure-matrix-fixtures.json',
  'frontend-app/scripts/failure-matrix-mutations.json',
  'frontend-app/src/entities/client/model/failureMatrix.test.js',
  'frontend-app/src/entities/client/model/runtimeSlice.test.js',
  'frontend-app/src/pages/chat/composer/ComposerDock.actionFailure.test.jsx',
  'frontend-app/src/pages/chat/thread/ThreadRail.test.jsx',
  'frontend-app/src/pages/settings/SettingsPage.test.jsx',
  'frontend-app/src/features/approval/ui/ApprovalDecisionShelf.test.jsx',
  'internal/provider/codexapp/failure_matrix_test.go',
  'internal/provider/claudecli/failure_matrix_test.go',
  'internal/module/turn/interrupt_rpc_test.go',
  'frontend-app/scripts/action-producer-guard.mjs',
  'frontend-app/scripts/action-producer-guard.selftest.mjs',
  'frontend-app/scripts/action-production-runtime-runner.mjs',
  'frontend-app/config/action-producer-registry.json',
  'frontend-app/config/action-producer-test-matrix.json',
  'frontend-app/src/shared/ui/productionActionFailureMatrixTitles.js',
  ...FROZEN_T04_T05_EXECUTION_CLOSURE_PATHS,
  'frontend-app/src/shared/api/wailsBridge.js',
  'internal/provider/claudecli/event_map.go',
  'internal/provider/codexapp/event_map.go',
  'internal/provider/unified/event_map.go',
  'internal/ui/wails/bridge.go',
];
const artifactProbes = new Set(['promptHistoryVisibleError', 'criticalTypecheck']);
const plannedBaseSha = 'b40867229af8e17916c00393639ccb0fcb4bf6fc';
const plannedThresholds = { overall: 90, dimensions: { E: 90, A: 85, C: 85, T: 85, P: 80 } };
const requiredDoDControls = new Set(['E06-failure-matrix', 'C05-provider-rpc-parity', 'T05-build-embed-smoke', 'P04-resource-budget']);
const customEvidenceProtocols = new Set([
  'action-producer-guard-v1',
  'action-production-runtime-report-v1',
  'failure-matrix-report-v1',
  'desktop-failure-report-v1',
  'performance-budget-json-v1',
  'delivery-smoke-json-v1',
  'git-diff-check-v1',
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
const criticalRuntimeAnchors = Object.freeze([
  Object.freeze({
    semanticClass: 'background-reconnect',
    actionId: 'provider.reconnect.bootstrap',
    sourcePath: 'src/entities/client/model/runtimeSlice.js',
    testFile: 'src/entities/client/model/runtimeSlice.test.js',
    testName: 'matrix:FM-18 layer:frontend persists reconnect bootstrap failure in Health and permits recovery',
  }),
  Object.freeze({
    semanticClass: 'prompt-history',
    actionId: 'prompt-history.previous',
    sourcePath: 'src/pages/chat/composer/ComposerDock.jsx',
    testFile: 'src/pages/chat/composer/ComposerDock.actionFailure.test.jsx',
    testName: 'keeps draft cursor and selection stable on rejected prompt history RPC',
  }),
  Object.freeze({
    semanticClass: 'thread-mutation',
    actionId: 'thread.pin',
    sourcePath: 'src/pages/chat/thread/ThreadCard.jsx',
    testFile: 'src/pages/chat/thread/ThreadRail.test.jsx',
    testName: 'renders active threads and routes thread actions through the store',
  }),
  Object.freeze({
    semanticClass: 'settings-save',
    actionId: 'settings.video.save',
    sourcePath: 'src/pages/settings/components/VideoSettingsCard.jsx',
    testFile: 'src/pages/settings/SettingsPage.test.jsx',
    testName: 'shows save failures from the named video facade method',
  }),
  Object.freeze({
    semanticClass: 'approval-pending',
    actionId: 'approval.respond',
    sourcePath: 'src/features/approval/ui/ApprovalDecisionShelf.jsx',
    testFile: 'src/features/approval/ui/ApprovalDecisionShelf.test.jsx',
    testName: 'matrix:FM-24 layer:frontend retains the selected choice after failure and allows an explicit retry',
  }),
]);
const criticalRuntimeActionIds = Object.freeze(criticalRuntimeAnchors.map(({ actionId }) => actionId));
const criticalRuntimeSemanticClasses = Object.freeze(criticalRuntimeAnchors.map(({ semanticClass }) => semanticClass));
const performanceCaseIds = Object.freeze({
  'P01-render-isolation': ['render-main-page-update-commits', 'render-unrelated-subtree-update-commits', 'render-broad-subscription-mutation-detected'],
  'P02-history-budget': ['turns-200-tools-1', 'turns-200-tools-3', 'turns-1000-tools-1', 'turns-1000-tools-3', 'turns-5000-tools-1', 'turns-5000-tools-3'],
  'P03-feedback-budget': ['stop-visible-feedback'],
  'P04-resource-budget': ['bundle-total-bytes', 'bundle-max-chunk-bytes', 'heap-used-median-bytes'],
});
const allPerformanceCaseIds = Object.freeze(Object.values(performanceCaseIds).flat());
const performanceRunnerFiles = Object.freeze([
  'frontend-app/scripts/chat-history-benchmark.mjs',
  'frontend-app/scripts/evidence-provenance.mjs',
  'frontend-app/scripts/frontend-performance-cases.json',
  'frontend-app/scripts/performance-baseline-provenance.mjs',
  'frontend-app/scripts/performance-budget-config.mjs',
  'frontend-app/scripts/performance-budget-model.mjs',
  'frontend-app/scripts/performance-budget-runner.mjs',
  'frontend-app/scripts/render-isolation-probe.test.jsx',
  'frontend-app/scripts/resource-budget.mjs',
  'frontend-app/scripts/stop-feedback-benchmark.mjs',
  'frontend-app/src/pages/chat/components/ChatActionFeedback.js',
]);
const performanceEnvironmentKeys = Object.freeze([
  'os', 'cpu', 'totalMemoryBytes', 'loadAverage', 'node', 'npm', 'go',
]);
const MAX_LOAD_AVERAGE_DELTA_PER_LOGICAL_CORE = 0.25;
const PERFORMANCE_LOAD_WINDOW_POLL_INTERVAL_MS = 5_000;
const PERFORMANCE_LOAD_WINDOW_MAX_WAIT_MS = 600_000;
const PERFORMANCE_LOAD_WINDOW_REQUIRED_SAMPLES = 2;
const frontendPlanPath = 'docs/plans/2026-07-15-frontend-maintainability-error-discoverability-90-plan.md';
const frontendPlanMaxLines = 500;
const frontendPlanMaxBytes = 25_600;
export function performanceAuditPathAllowed(changedPath) {
  return isAllowedPerformanceBaselinePath(changedPath);
}

export function frontendPlanSizeStatus(content) {
  if (typeof content !== 'string') fail('frontend plan content must be a string');
  const bytes = Buffer.byteLength(content);
  const lines = content.length === 0 ? 0 : content.split('\n').length - Number(content.endsWith('\n'));
  return {
    status: bytes <= frontendPlanMaxBytes && lines <= frontendPlanMaxLines ? 'PASS' : 'FAIL',
    bytes,
    lines,
  };
}

export function assertFrontendPlanSize(repoRoot = frozenRepoRoot) {
  const planPath = path.join(repoRoot, frontendPlanPath);
  let content;
  try {
    content = fs.readFileSync(planPath, 'utf8');
  }
  catch (error) {
    fail(`frontend plan size guard cannot read ${frontendPlanPath}: ${error.message}`);
  }
  const result = frontendPlanSizeStatus(content);
  if (result.status !== 'PASS') {
    fail(`frontend plan size exceeds frozen limits: lines=${result.lines}/${frontendPlanMaxLines} bytes=${result.bytes}/${frontendPlanMaxBytes}`);
  }
  return result;
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
const deliveryRunnerFiles = FROZEN_T04_T05_EXECUTION_CLOSURE_PATHS;
const deliveryCommands = Object.freeze([
  Object.freeze({ id: 'frontend-build', cwd: 'frontend-app', argv: Object.freeze(['npm', 'run', 'build']) }),
  Object.freeze({ id: 'frontend-embed-verify', cwd: '.', argv: Object.freeze(['make', 'frontend-embed-verify']) }),
  Object.freeze({ id: 'desktop-start-smoke', cwd: 'frontend-app', argv: Object.freeze(['npm', 'run', 'smoke:desktop:rpc']) }),
  Object.freeze({ id: 'desktop-failure-smoke', cwd: 'frontend-app', argv: Object.freeze(['npm', 'run', 'smoke:desktop:failure']) }),
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
    ['node', 'node_modules/vitest/vitest.mjs', 'run', 'src/shared/ui/productionActionFailureMatrix.test.js', '--no-file-parallelism', '--maxWorkers=1'],
    ['node', 'frontend-app/scripts/action-production-runtime-runner.mjs'],
  ],
  'T04-local-gates': [
    ['npm', 'run', 'lint'],
    ['npm', 'test'],
    ['npm', 'run', 'build'],
    ['git', 'diff', '--check', '$SCORE_BASE_SHA', '$SUBJECT_SHA'],
    ['go', 'test', './scripts', './scripts/ai_maintenance', '-run', '^(TestAIMaintenanceGateVerifiesLocalHookArtifacts|TestAIMaintenanceGateRouteDeletionMutations|TestBuildGatePlanRoutesFrontendBackendAndGeneratedFiles)$', '-count=1'],
    ['node', 'frontend-app/scripts/frontend-maintainability-score.mjs', '--assert-frontend-plan-size'],
  ],
});
const t04FrontendTestCommand = Object.freeze(['npm', 'test']);
const t04FrontendTestTimeoutMs = 900000;
const t04HookRouteGuardCommand = Object.freeze([
  'go', 'test', './scripts', './scripts/ai_maintenance', '-run',
  '^(TestAIMaintenanceGateVerifiesLocalHookArtifacts|TestAIMaintenanceGateRouteDeletionMutations|TestBuildGatePlanRoutesFrontendBackendAndGeneratedFiles)$',
  '-count=1',
]);
const t04HookRouteGuardCaseIds = Object.freeze([
  'hook-route-pre-commit-deleted',
  'hook-route-pre-push-deleted',
  'hook-route-ai-maintenance-deleted',
]);
const t04HookRouteGuardTimeoutMs = 120000;
const P03_ABSOLUTE_FEEDBACK_LIMIT_MS = 2000;
const t04FrontendPlanSizeCommand = Object.freeze([
  'node', 'frontend-app/scripts/frontend-maintainability-score.mjs', '--assert-frontend-plan-size',
]);
const t04FrontendPlanSizeCaseIds = Object.freeze([
  'frontend-plan-size-bytes',
  'frontend-plan-size-lines',
]);
const t04FrontendPlanSizeTimeoutMs = 120000;

function readFrozenJSON(name) {
  return JSON.parse(fs.readFileSync(path.join(frozenScriptRoot, name), 'utf8'));
}

export function withFrozenDeliveryRunnerFiles(config) {
  return {
    ...config,
    controls: config.controls.map((control) => {
      if (control.id !== 'T05-build-embed-smoke') return control;
      return {
        ...control,
        allOf: control.allOf.map((check) => {
          if (Object.hasOwn(check, 'runnerFiles')
            && JSON.stringify(check.runnerFiles) !== JSON.stringify(FROZEN_T04_T05_EXECUTION_CLOSURE_PATHS)) {
            fail('delivery runnerFiles differs from the frozen execution closure');
          }
          return {
            ...check,
            runnerFiles: [...FROZEN_T04_T05_EXECUTION_CLOSURE_PATHS],
          };
        }),
      };
    }),
  };
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

function exactOrdered(actual, expected, label) {
  if (JSON.stringify(actual) !== JSON.stringify(expected)) fail(`${label} differs from frozen contract`);
}

function exactKeys(value, expected, label) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) fail(`${label} must be an object`);
  exactSet(Object.keys(value), expected, `${label} keys`);
}

function exactValue(actual, expected, label) {
  if (JSON.stringify(actual) !== JSON.stringify(expected)) fail(`${label} mismatch`);
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

function governanceSnapshot(repoRoot) {
  const aggregate = createHash('sha256');
  for (const relativePath of governancePaths) {
    aggregate.update(relativePath);
    aggregate.update('\0');
    aggregate.update(fs.readFileSync(path.join(repoRoot, relativePath)));
    aggregate.update('\0');
  }
  return {
    rule: 'byte-identical-governance-v1',
    pathCount: governancePaths.length,
    sha256: aggregate.digest('hex'),
  };
}

function assertGovernanceUnchanged(targetRepoRoot) {
  for (const relativePath of governancePaths) {
    const frozen = fs.readFileSync(path.join(frozenRepoRoot, relativePath));
    const targetPath = path.join(targetRepoRoot, relativePath);
    if (!fs.existsSync(targetPath) || !frozen.equals(fs.readFileSync(targetPath))) {
      fail(`frozen governance drift: ${relativePath}`);
    }
  }
  const frozenSnapshot = governanceSnapshot(frozenRepoRoot);
  const targetSnapshot = governanceSnapshot(targetRepoRoot);
  if (JSON.stringify(targetSnapshot) !== JSON.stringify(frozenSnapshot)) {
    fail('frozen governance aggregate differs from SCORE_BASE');
  }
  return targetSnapshot;
}

export function createSubjectContract({
  scoreBaseSha,
  scoreBaseTree,
  subjectSha,
  subjectTree,
  strictDescendant,
  governanceFreeze = null,
  finalContractEnforced = false,
}) {
  for (const [label, value] of Object.entries({ scoreBaseSha, scoreBaseTree, subjectSha, subjectTree })) {
    if (!/^[0-9a-f]{40}$/u.test(value || '')) fail(`subject contract ${label} is invalid`);
  }
  if (finalContractEnforced && strictDescendant !== true) {
    fail('final subject contract requires strict descendant ancestry');
  }
  if (finalContractEnforced && (governanceFreeze?.rule !== 'byte-identical-governance-v1'
    || governanceFreeze.pathCount !== governancePaths.length
    || !/^[0-9a-f]{64}$/u.test(governanceFreeze.sha256 || ''))) {
    fail('final subject contract requires exact frozen governance evidence');
  }
  const sameTree = subjectTree === scoreBaseTree;
  if (finalContractEnforced && sameTree) {
    fail('final subject must change the SCORE_BASE tree; empty-commit evidence is rejected');
  }
  return {
    schemaVersion: 1,
    rule: 'strict-descendant-governance-frozen-tree-relation-v1',
    finalContractEnforced,
    ancestry: strictDescendant ? 'strict-descendant' : (subjectSha === scoreBaseSha ? 'score-base' : 'unverified'),
    scoreBaseSha,
    scoreBaseTree,
    subjectSha,
    subjectTree,
    treeRelation: sameTree ? (strictDescendant ? 'empty-commit-tree' : 'score-base-tree') : 'changed-tree',
    identityFreeze: false,
    governanceFreeze,
  };
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
  const scoreBaseTree = git(frozenRepoRoot, ['rev-parse', `${scoreBaseSha}^{tree}`]);
  assertFrozenProvenance(scoreBaseSha);
  const strictDescendant = head !== scoreBaseSha
    && gitSucceeds(targetRepoRoot, ['cat-file', '-e', `${scoreBaseSha}^{commit}`])
    && gitSucceeds(targetRepoRoot, ['merge-base', '--is-ancestor', scoreBaseSha, head]);
  let governanceFreeze = null;
  if (requireFinalContract) {
    if (head === scoreBaseSha) fail('final subject must be a strict descendant of SCORE_BASE_SHA');
    if (!strictDescendant) {
      fail('SCORE_BASE_SHA must be a strict ancestor of the final subject');
    }
    if (subjectTree === scoreBaseTree) {
      fail('final subject must change the SCORE_BASE tree; empty-commit evidence is rejected');
    }
    governanceFreeze = assertGovernanceUnchanged(targetRepoRoot);
  }
  const subjectContract = createSubjectContract({
    scoreBaseSha,
    scoreBaseTree,
    subjectSha: head,
    subjectTree,
    strictDescendant,
    governanceFreeze,
    finalContractEnforced: requireFinalContract,
  });
  return {
    repoRoot: targetRepoRoot,
    appRoot: path.join(targetRepoRoot, 'frontend-app'),
    subjectSha: head,
    subjectTree,
    scoreBaseSha,
    scoreBaseTree,
    subjectContract,
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

function dependencyTreeIntegrity(appRoot) {
  const nodeModulesRoot = path.join(appRoot, 'node_modules');
  const rootStat = fs.lstatSync(nodeModulesRoot);
  if (!rootStat.isDirectory() || rootStat.isSymbolicLink()) {
    fail(`immutable dependency root must be a physical directory: ${nodeModulesRoot}`);
  }
  const aggregate = createHash('sha256');
  let pathCount = 0;
  const add = (value) => {
    aggregate.update(value);
    aggregate.update('\0');
  };
  const visit = (absoluteRoot, relativeRoot = '') => {
    for (const entry of fs.readdirSync(absoluteRoot).sort()) {
      const absolutePath = path.join(absoluteRoot, entry);
      const relativePath = relativeRoot ? `${relativeRoot}/${entry}` : entry;
      const stat = fs.lstatSync(absolutePath);
      pathCount += 1;
      add(relativePath);
      if (stat.isDirectory()) {
        add('directory');
        visit(absolutePath, relativePath);
      }
      else if (stat.isFile()) {
        add('file');
        aggregate.update(fs.readFileSync(absolutePath));
        aggregate.update('\0');
      }
      else if (stat.isSymbolicLink()) {
        add('symlink');
        add(fs.readlinkSync(absolutePath));
      }
      else {
        fail(`unsupported immutable dependency entry: ${relativePath}`);
      }
    }
  };
  visit(nodeModulesRoot);
  return {
    nodeModulesRoot,
    pathCount,
    sha256: aggregate.digest('hex'),
  };
}

function assertImmutableDependencies(appRoot) {
  const packageLockPath = path.join(appRoot, 'package-lock.json');
  if (!fs.existsSync(packageLockPath)) fail(`immutable dependency package-lock is missing: ${packageLockPath}`);
  const packageLockSha256 = createHash('sha256').update(fs.readFileSync(packageLockPath)).digest('hex');
  if (packageLockSha256 !== dependencyIntegrity.packageLockSha256) {
    fail(`immutable dependency package-lock SHA-256 mismatch: ${appRoot}`);
  }
  const actual = dependencyTreeIntegrity(appRoot);
  for (const [relativePath, expectedSha256] of Object.entries(dependencyIntegrity.requiredTools)) {
    const absolutePath = path.join(actual.nodeModulesRoot, relativePath);
    if (!fs.existsSync(absolutePath)) fail(`immutable dependency tool is missing: ${relativePath}`);
    const actualSha256 = createHash('sha256').update(fs.readFileSync(absolutePath)).digest('hex');
    if (actualSha256 !== expectedSha256) fail(`immutable dependency tool SHA-256 mismatch: ${relativePath}`);
  }
  if (actual.pathCount !== dependencyIntegrity.nodeModulesPathCount) {
    fail(`immutable dependency path count mismatch: ${appRoot}`);
  }
  if (actual.sha256 !== dependencyIntegrity.nodeModulesTreeSha256) {
    fail(`immutable dependency tree SHA-256 mismatch: ${appRoot}`);
  }
  return actual;
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

async function collectVitestEvidence(context, check) {
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
      const result = await runManagedCommand(process.execPath, [runtime.vitestPath, ...args], {
        cwd: context.appRoot,
        timeoutMs: check.timeoutMs,
        killGraceMs: 20_000,
      });
      if (result.timedOut) throw new Error(`Vitest evidence timed out after ${check.timeoutMs}ms`);
      if (result.error || result.status !== 0) {
        throw new Error(`${result.stderr || result.stdout || result.error?.message || 'Vitest evidence failed'}`.trim());
      }
      output = result.stdout;
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

async function vitestProbe(context, control, check) {
  try {
    const fingerprint = sourceFingerprint(context, check.sourcePaths);
    const evidence = await collectVitestEvidence(context, check);
    const status = terminalTruthEvidenceStatus(evidence, { fingerprint, testNames: check.testNames });
    return evidenceRecord(context, control, check, {
      status,
      exitCode: status === 'PASS' ? 0 : 1,
      summary: evidence.summary || `${evidence.testResults.length}/${check.testCount} exact named tests passed`,
    });
  }
  catch (error) {
    return evidenceRecord(context, control, check, {
      status: 'FAIL',
      exitCode: 1,
      summary: error.message,
    });
  }
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

function normalizeReportValue(value) {
  if (Array.isArray(value)) return value.map(normalizeReportValue);
  if (!value || typeof value !== 'object') return value;
  return Object.fromEntries(Object.keys(value).sort().map((key) => [key, normalizeReportValue(value[key])]));
}

function canonicalReportJSON(report) {
  return `${JSON.stringify(normalizeReportValue(report), null, 2)}\n`;
}

function performanceThreshold(metricId) {
  const metric = baseline.metrics?.[metricId];
  if (metricId === 'P01-render-isolation') {
    return { absoluteUpdateLimit: metric?.absoluteUpdateLimit, mutationDetected: true };
  }
  return { maxRegressionRatio: metric?.maxRegressionRatio };
}

function recordRawReport(context, check, report, reportArtifact) {
  if (!context.rawReports) context.rawReports = new Map();
  const kinds = {
    'action-production-runtime-report-v1': 'actionProductionRuntime',
    'failure-matrix-report-v1': 'failureMatrix',
    'desktop-failure-report-v1': 'desktopFailure',
    'performance-budget-json-v1': 'performance',
    'delivery-smoke-json-v1': 'delivery',
  };
  const kind = kinds[check.evidenceProtocol];
  if (!kind) fail(`structured report kind is not registered: ${check.evidenceProtocol}`);
  const canonical = canonicalReportJSON(report);
  const sha = createHash('sha256').update(canonical).digest('hex');
  let metrics = [];
  if (kind === 'performance') {
    metrics = Object.keys(performanceCaseIds).sort().map((metricId) => ({
      metricId,
      threshold: performanceThreshold(metricId),
      path: `evidence.metrics.${metricId}`,
    }));
  }
  else if (kind === 'delivery') {
    metrics = [{
      metricId: 'T05-build-embed-smoke',
      threshold: { requiredExitCode: 0, requiredCommandCount: deliveryCommands.length },
      path: 'verdict.commands',
    }];
  }
  const artifact = {
    protocol: check.evidenceProtocol,
    sourcePath: reportArtifact.path,
    sourceSha256: reportArtifact.sha256,
    sourceBytes: reportArtifact.bytes,
    sha256: sha,
    bytes: Buffer.byteLength(canonical),
    metrics,
    report: normalizeReportValue(report),
  };
  const current = context.rawReports.get(kind);
  if (current && (current.sha256 !== artifact.sha256
    || current.sourcePath !== artifact.sourcePath
    || current.sourceSha256 !== artifact.sourceSha256)) {
    fail(`${kind} raw report changed during one scorer run`);
  }
  context.rawReports.set(kind, artifact);
}

function validateReportBinding(report, context, startedAt, finishedAt, schemaVersion = 1) {
  if (report?.schemaVersion !== schemaVersion) fail(`runner report schemaVersion must equal ${schemaVersion}`);
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

function validateFailureMatrixRedGreen(report, context) {
  if (!Array.isArray(report.redGreenCases) || report.redGreenCases.length !== failureMatrixCaseIds.length) {
    fail('failure matrix RED/GREEN evidence must contain one entry per case');
  }
  exactSet(report.redGreenCases.map(({ caseId }) => caseId), failureMatrixCaseIds, 'failure matrix RED/GREEN caseIds');
  const mutationDocument = readFrozenJSON('failure-matrix-mutations.json');
  if (mutationDocument.schemaVersion !== 1 || !Array.isArray(mutationDocument.mutations)) {
    fail('failure matrix mutation manifest is invalid');
  }
  const mutationByCase = new Map();
  for (const mutation of mutationDocument.mutations) {
    for (const caseId of mutation.caseIds || []) {
      if (mutationByCase.has(caseId)) fail(`failure matrix mutation case is duplicated: ${caseId}`);
      mutationByCase.set(caseId, mutation);
    }
  }
  exactSet([...mutationByCase.keys()], failureMatrixCaseIds, 'failure matrix mutation manifest caseIds');
  const mutationDocuments = new Map();
  const goPackages = {
    'go-codex': './internal/provider/codexapp',
    'go-claude': './internal/provider/claudecli',
    'go-turn': './internal/module/turn',
    'go-wails': './internal/ui/wails',
  };
  for (const entry of report.redGreenCases) {
    if (entry.subjectSha !== context.subjectSha || entry.subjectTreeSha !== context.subjectTree) {
      fail(`failure matrix RED/GREEN subject binding mismatch: ${entry.caseId}`);
    }
    if (entry.green?.exitCode !== 0 || entry.green.signal !== null
      || typeof entry.green.test !== 'string'
      || !entry.green.test.includes(entry.caseId) || typeof entry.green.layer !== 'string') {
      fail(`failure matrix GREEN execution is invalid: ${entry.caseId}`);
    }
    const red = entry.red;
    const mutation = mutationByCase.get(entry.caseId);
    if (!mutation || mutation.layer !== entry.green.layer || mutation.id !== red?.mutationId
      || mutation.sourcePath !== red?.sourcePath) {
      fail(`failure matrix mutation binding mismatch: ${entry.caseId}`);
    }
    const mutationKey = `${mutation.sourcePath}\0${mutation.id}`;
    if (!mutationDocuments.has(mutationKey)) {
      const original = execFileSync('git', ['show', `${context.subjectSha}:${mutation.sourcePath}`], {
        cwd: context.repoRoot,
        encoding: 'utf8',
      });
      const firstMatch = original.indexOf(mutation.search);
      if (firstMatch < 0 || original.indexOf(mutation.search, firstMatch + mutation.search.length) >= 0
        || !mutation.replacement || mutation.replacement === mutation.search) {
        fail(`failure matrix mutation is not an exact production replacement: ${mutation.id}`);
      }
      mutationDocuments.set(mutationKey, {
        sourceSha256: createHash('sha256').update(original).digest('hex'),
        mutatedSha256: createHash('sha256').update(original.replace(mutation.search, mutation.replacement)).digest('hex'),
      });
    }
    const source = mutationDocuments.get(mutationKey);
    const expectedCommand = entry.green.layer === 'frontend'
      ? {
        cwd: 'frontend-app',
        argv: [
          path.join('node_modules', '.bin', 'vitest'), 'run', entry.green.file,
          '-t', entry.green.test, '--no-file-parallelism', '--maxWorkers=1',
        ],
      }
      : {
        cwd: '.',
        argv: ['go', 'test', goPackages[entry.green.layer], '-run', `^${entry.green.test}$`, '-count=1'],
      };
    if ((entry.green.layer === 'frontend' && typeof entry.green.file !== 'string')
      || (!goPackages[entry.green.layer] && entry.green.layer !== 'frontend')
      || entry.green.cwd !== expectedCommand.cwd
      || JSON.stringify(entry.green.argv) !== JSON.stringify(expectedCommand.argv)
      || !/^[0-9a-f]{64}$/u.test(entry.green.outputSha256)
      || red.cwd !== expectedCommand.cwd || JSON.stringify(red.argv) !== JSON.stringify(expectedCommand.argv)) {
      fail(`failure matrix mutation command mismatch: ${entry.caseId}`);
    }
    if (!Number.isInteger(red?.exitCode) || red.exitCode <= 0 || red.signal !== null
      || typeof red.mutationId !== 'string' || !red.mutationId.trim()
      || typeof red.sourcePath !== 'string' || !red.sourcePath.trim()
      || red.sourceSha256 !== source.sourceSha256 || red.mutatedSha256 !== source.mutatedSha256
      || !/^[0-9a-f]{64}$/u.test(red.outputSha256)
      || typeof red.cwd !== 'string' || !Array.isArray(red.argv) || red.argv.length === 0) {
      fail(`failure matrix mutation RED execution is invalid: ${entry.caseId}`);
    }
  }
}

function validateFailureMatrixReport(report, { context, control, check, startedAt, finishedAt }) {
  validateReportBinding(report, context, startedAt, finishedAt);
  if (control.id === 'T01-red-green-regression') validateFailureMatrixRedGreen(report, context);
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
  const sourcePaths = check.sourcePaths;
  validateReportBinding(report, context, startedAt, finishedAt, 2);
  if (report.controlId !== control.id) fail('desktop failure smoke controlId mismatch');
  validateExactCaseResult(report.caseIds, report.testCount, check, 'desktop failure smoke');
  if (report.schemaVersion !== 2 || !report.sourceHashes || JSON.stringify(Object.keys(report.sourceHashes).sort()) !== JSON.stringify([...sourcePaths].sort())) {
    fail('desktop failure smoke requires source-hashed v2 raw report');
  }
  for (const sourcePath of sourcePaths) {
    if (report.sourceHashes[sourcePath] !== runnerFileSha256(context.repoRoot, context.subjectSha, sourcePath)) {
      fail(`desktop failure source hash mismatch: ${sourcePath}`);
    }
  }
  const executionLocations = [
    ['goBuild', '.'],
    ['playwright', 'frontend-app'],
    ['wailsHost', '.'],
    ['vite', 'frontend-app'],
  ];
  if (!report.execution || executionLocations.some(([name, cwd]) => {
    const execution = report.execution[name];
    return !Array.isArray(execution?.argv) || execution.argv.length === 0 || execution.cwd !== cwd
      || !Number.isInteger(execution.exitCode) && execution.exitCode !== null
      || execution.signal !== null && typeof execution.signal !== 'string'
      || !/^[0-9a-f]{64}$/u.test(execution.outputSha256 || '');
  })
    || report.execution.goBuild.exitCode !== 0 || report.execution.playwright.exitCode !== 0
    || report.execution.goBuild.signal !== null || report.execution.playwright.signal !== null
    || report.execution.playwright.testCount !== check.testCount) {
    fail('desktop failure execution evidence is incomplete');
  }
  if (!Array.isArray(report.cases) || report.cases.length !== check.caseIds.length) fail('desktop failure summary-only report is forbidden');
  const requiredEvidence = {
    'terminal-failed': {
      hops: ['claudecli.raw', 'claudecli.adapter', 'turndto.TurnOutputDelta', 'wails.EventBridge', 'chromium.DOM', 'codexapp.raw', 'codexapp.adapter', 'turndto.TurnCompleted', 'turn/terminal', 'chromium.DOM'],
      domAssertions: ['partial-response-visible', 'safe-terminal-visible', 'raw-secret-absent'],
    },
    'prompt-history-reject': {
      hops: ['wails.rpc', 'thread/promptHistory', 'frontend.action', 'chromium.DOM', 'retry.control', 'wails.rpc', 'chromium.DOM'],
      domAssertions: ['draft-preserved', 'cursor-preserved', 'retry-click-recovers'],
    },
  };
  for (const [index, evidence] of report.cases.entries()) {
    const expectedEvidence = requiredEvidence[check.caseIds[index]];
    if (evidence?.caseId !== check.caseIds[index] || evidence.result !== 'GREEN'
      || JSON.stringify(evidence.command) !== JSON.stringify(check.argv)
      || JSON.stringify(evidence.hops) !== JSON.stringify(expectedEvidence.hops)
      || JSON.stringify(evidence.domAssertions) !== JSON.stringify(expectedEvidence.domAssertions)
      || !Array.isArray(evidence.secretAssertions) || evidence.secretAssertions.length !== 2
      || evidence.execution?.status !== 'passed' || !Number.isFinite(evidence.execution.durationMs)) {
      fail(`desktop failure case evidence is incomplete: ${check.caseIds[index]}`);
    }
  }
  if (JSON.stringify(report).includes('t03-raw-provider-secret-do-not-persist')) {
    fail('desktop failure raw report leaked provider secret');
  }
  return normalizedRunnerStatus(report.status);
}

function requireFiniteNonNegative(value, label) {
  if (!Number.isFinite(value) || value < 0) fail(`${label} must be finite and non-negative`);
  return value;
}

function requireFinitePositive(value, label) {
  if (!Number.isFinite(value) || value <= 0) fail(`${label} must be finite and positive`);
  return value;
}

function exactMeasurementMedian(values, label) {
  if (!Array.isArray(values) || values.length === 0) fail(`${label} must contain raw measurements`);
  const sortedValues = values.map((value, index) => requireFiniteNonNegative(value, `${label}[${index}]`))
    .sort((left, right) => left - right);
  return sortedValues[Math.floor(sortedValues.length / 2)];
}

function exactMedian(values, label) {
  if (!Array.isArray(values) || values.length !== 5) fail(`${label} must contain five raw samples`);
  return exactMeasurementMedian(values, label);
}

function validateTimingMetric(metric, caseIds, subjectSha, label) {
  if (metric?.subjectSha !== subjectSha || metric.sampleCount !== 5 || metric.warmupCount !== 1) {
    fail(`${label} subject or sample contract mismatch`);
  }
  exactSet(Object.keys(metric.cases || {}), caseIds, `${label} cases`);
  for (const caseId of caseIds) {
    const entry = metric.cases[caseId];
    if (entry?.attemptsPerSample !== 1
      || !Array.isArray(entry.durationAttemptSamplesMs) || entry.durationAttemptSamplesMs.length !== 5
      || !Array.isArray(entry.durationSamplesMs) || entry.durationSamplesMs.length !== 5) {
      fail(`${label}.${caseId} raw samples are missing`);
    }
    entry.durationAttemptSamplesMs.forEach((attempts, sampleIndex) => {
      if (!Array.isArray(attempts) || attempts.length !== 1) fail(`${label}.${caseId} must record one attempt per sample`);
      const measured = requireFiniteNonNegative(attempts[0], `${label}.${caseId}.attempt[${sampleIndex}][0]`);
      const selected = requireFiniteNonNegative(
        entry.durationSamplesMs[sampleIndex],
        `${label}.${caseId}.sample[${sampleIndex}]`,
      );
      if (selected !== measured) {
        fail(`${label}.${caseId} selected sample does not preserve its raw measurement`);
      }
    });
    if (entry.durationMedianMs !== exactMedian(entry.durationSamplesMs, `${label}.${caseId}.durationSamplesMs`)) {
      fail(`${label}.${caseId} median does not match raw samples`);
    }
  }
}

const p02DurationClock = 'p50(production/reference process.cpuUsage(user+system),alternating,500000-iteration-blocks)';

function validatePairedTimingMetric(metric, caseIds, subjectSha, label) {
  if (metric?.subjectSha !== subjectSha || metric.sampleCount !== 5 || metric.warmupCount !== 1) {
    fail(`${label} subject or sample contract mismatch`);
  }
  exactSet(Object.keys(metric.cases || {}), caseIds, `${label} cases`);
  for (const caseId of caseIds) {
    const entry = metric.cases[caseId];
    if (entry?.durationClock !== p02DurationClock || entry.attemptsPerSample !== 1
      || !Number.isSafeInteger(entry.blockCount) || entry.blockCount <= 0
      || !Number.isSafeInteger(entry.blockIterationCount) || entry.blockIterationCount <= 0
      || !Number.isSafeInteger(entry.iterationCount)
      || entry.iterationCount !== entry.blockCount * entry.blockIterationCount
      || !Number.isSafeInteger(entry.materializedCount) || entry.materializedCount <= 0
      || entry.referenceMaterializedCount !== entry.materializedCount
      || !Array.isArray(entry.sampleDiagnostics) || entry.sampleDiagnostics.length !== 5
      || !Array.isArray(entry.normalizedRatioSamples) || entry.normalizedRatioSamples.length !== 5) {
      fail(`${label}.${caseId} paired measurement shape is invalid`);
    }
    entry.sampleDiagnostics.forEach((sample, sampleIndex) => {
      const pairedArrays = [
        sample?.blockOrders,
        sample?.productionBlockCpuDurationsMs,
        sample?.referenceBlockCpuDurationsMs,
        sample?.rawNormalizedBlockRatios,
      ];
      if (pairedArrays.some((values) => !Array.isArray(values) || values.length !== entry.blockCount)) {
        fail(`${label}.${caseId} sample ${sampleIndex} paired block evidence is incomplete`);
      }
      const recomputedRatios = sample.blockOrders.map((order, blockIndex) => {
        const expectedOrder = (sampleIndex + blockIndex) % 2 === 0
          ? 'production-reference'
          : 'reference-production';
        if (order !== expectedOrder) fail(`${label}.${caseId} sample ${sampleIndex} block order is invalid`);
        const production = requireFinitePositive(
          sample.productionBlockCpuDurationsMs[blockIndex],
          `${label}.${caseId}.production[${sampleIndex}][${blockIndex}]`,
        );
        const reference = requireFinitePositive(
          sample.referenceBlockCpuDurationsMs[blockIndex],
          `${label}.${caseId}.reference[${sampleIndex}][${blockIndex}]`,
        );
        const recordedRatio = requireFinitePositive(
          sample.rawNormalizedBlockRatios[blockIndex],
          `${label}.${caseId}.ratio[${sampleIndex}][${blockIndex}]`,
        );
        const recomputedRatio = production / reference;
        if (recordedRatio !== recomputedRatio) fail(`${label}.${caseId} raw ratio is not reproducible`);
        return recomputedRatio;
      });
      const recomputedSample = exactMeasurementMedian(
        recomputedRatios,
        `${label}.${caseId}.rawNormalizedBlockRatios[${sampleIndex}]`,
      );
      const recordedSample = requireFinitePositive(
        entry.normalizedRatioSamples[sampleIndex],
        `${label}.${caseId}.normalizedRatioSamples[${sampleIndex}]`,
      );
      const diagnosticSample = requireFinitePositive(
        sample.normalizedRatio,
        `${label}.${caseId}.sampleDiagnostics[${sampleIndex}].normalizedRatio`,
      );
      if (recordedSample !== recomputedSample || diagnosticSample !== recomputedSample) {
        fail(`${label}.${caseId} sample ${sampleIndex} normalized ratio summary is invalid`);
      }
    });
    if (entry.normalizedRatioMedian !== exactMedian(
      entry.normalizedRatioSamples,
      `${label}.${caseId}.normalizedRatioSamples`,
    )) {
      fail(`${label}.${caseId} normalized ratio median does not match samples`);
    }
  }
}

function validatePairedTimingCompatibility(currentMetric, frozenMetric, caseIds) {
  for (const caseId of caseIds) {
    for (const field of [
      'blockCount', 'blockIterationCount', 'iterationCount', 'materializedCount', 'referenceMaterializedCount',
    ]) {
      if (currentMetric.cases[caseId][field] !== frozenMetric.cases[caseId][field]) {
        fail(`P02 ${caseId} ${field} mismatch`);
      }
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
  try {
    validateV8HeapEvidence(metric, label);
  } catch (error) {
    fail(error.message);
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

function validateComparableLoadAverage(baselineEnvironment, candidateEnvironment) {
  const baselineCores = baselineEnvironment.cpu.logicalCores;
  const candidateCores = candidateEnvironment.cpu.logicalCores;
  const tolerance = Math.min(
    Math.max(1, baselineCores * MAX_LOAD_AVERAGE_DELTA_PER_LOGICAL_CORE),
    Math.max(1, candidateCores * MAX_LOAD_AVERAGE_DELTA_PER_LOGICAL_CORE),
  );
  for (const [index, baselineLoad] of baselineEnvironment.loadAverage.entries()) {
    if (Math.abs(baselineLoad - candidateEnvironment.loadAverage[index]) > tolerance) {
      fail(`performance baseline and candidate loadAverage differ beyond ${tolerance}`);
    }
  }
}

function performanceLoadTolerance(baselineEnvironment, candidateLogicalCores) {
  const baselineLogicalCores = baselineEnvironment?.cpu?.logicalCores;
  if (!Number.isSafeInteger(baselineLogicalCores) || baselineLogicalCores <= 0
    || !Number.isSafeInteger(candidateLogicalCores) || candidateLogicalCores <= 0) {
    fail('performance load window requires positive logical CPU counts');
  }
  return Math.min(
    Math.max(1, baselineLogicalCores * MAX_LOAD_AVERAGE_DELTA_PER_LOGICAL_CORE),
    Math.max(1, candidateLogicalCores * MAX_LOAD_AVERAGE_DELTA_PER_LOGICAL_CORE),
  );
}

export async function waitForPerformanceLoadWindow({
  runnerArgv,
  runnerCwd,
  baselineEnvironment = baseline.environment,
  candidateLogicalCores = os.cpus().length,
  sampleLoadAverage = () => os.loadavg(),
  now = () => Date.now(),
  pause = (milliseconds) => new Promise((resolvePause) => setTimeout(resolvePause, milliseconds)),
  onRejectedObservation = (observation, remainingMs) => {
    process.stderr.write(`performance load window waiting: ${JSON.stringify({ observation, remainingMs })}\n`);
  },
  pollIntervalMs = PERFORMANCE_LOAD_WINDOW_POLL_INTERVAL_MS,
  maxWaitMs = PERFORMANCE_LOAD_WINDOW_MAX_WAIT_MS,
  requiredConsecutiveSamples = PERFORMANCE_LOAD_WINDOW_REQUIRED_SAMPLES,
} = {}) {
  if (!Array.isArray(runnerArgv) || runnerArgv.length === 0
    || typeof runnerCwd !== 'string' || runnerCwd.length === 0
    || !Array.isArray(baselineEnvironment?.loadAverage) || baselineEnvironment.loadAverage.length !== 3
    || baselineEnvironment.loadAverage.some((value) => !Number.isFinite(value) || value < 0)
    || !Number.isSafeInteger(pollIntervalMs) || pollIntervalMs <= 0
    || !Number.isSafeInteger(maxWaitMs) || maxWaitMs <= 0
    || !Number.isSafeInteger(requiredConsecutiveSamples) || requiredConsecutiveSamples <= 0) {
    fail('performance load window configuration is invalid');
  }
  const tolerance = performanceLoadTolerance(baselineEnvironment, candidateLogicalCores);
  const startedAtMs = now();
  const observations = [];
  let consecutiveComparableSamples = 0;
  for (;;) {
    const observedAtMs = now();
    const loadAverage = sampleLoadAverage();
    if (!Array.isArray(loadAverage) || loadAverage.length !== 3
      || loadAverage.some((value) => !Number.isFinite(value) || value < 0)) {
      fail('performance load window observation is invalid');
    }
    const deltas = baselineEnvironment.loadAverage.map((value, index) => Math.abs(value - loadAverage[index]));
    const comparable = deltas.every((delta) => delta <= tolerance);
    consecutiveComparableSamples = comparable ? consecutiveComparableSamples + 1 : 0;
    const observation = Object.freeze({
      observedAt: new Date(observedAtMs).toISOString(),
      loadAverage: Object.freeze([...loadAverage]),
      deltas: Object.freeze(deltas),
      comparable,
      consecutiveComparableSamples,
    });
    observations.push(observation);
    const elapsedMs = observedAtMs - startedAtMs;
    const audit = (status) => Object.freeze({
      status,
      runnerCwd,
      runnerArgv: Object.freeze([...runnerArgv]),
      startedAt: new Date(startedAtMs).toISOString(),
      finishedAt: new Date(observedAtMs).toISOString(),
      elapsedMs,
      pollIntervalMs,
      maxWaitMs,
      requiredConsecutiveSamples,
      tolerance,
      baselineLogicalCores: baselineEnvironment.cpu.logicalCores,
      candidateLogicalCores,
      baselineLoadAverage: Object.freeze([...baselineEnvironment.loadAverage]),
      observations: Object.freeze([...observations]),
    });
    if (comparable && consecutiveComparableSamples >= requiredConsecutiveSamples) return audit('READY');
    if (elapsedMs >= maxWaitMs) return audit('TIMEOUT');
    const remainingMs = maxWaitMs - elapsedMs;
    if (!comparable) onRejectedObservation(observation, remainingMs);
    await pause(Math.min(pollIntervalMs, remainingMs));
  }
}

function validateBaseResourceBuild(metric, baseSha, baseTree) {
  const build = metric?.baseBuild;
  exactKeys(build, [
    'baseSha', 'baseTree', 'installArgv', 'buildArgv', 'distManifest', 'distManifestHash',
  ], 'P04 baseline build');
  if (build.baseSha !== baseSha || build.baseTree !== baseTree
    || JSON.stringify(build.installArgv) !== JSON.stringify(['npm', 'ci'])
    || JSON.stringify(build.buildArgv) !== JSON.stringify(['npm', 'run', 'build'])
    || !Array.isArray(build.distManifest) || build.distManifest.length !== metric.files.length
    || !/^[0-9a-f]{64}$/u.test(build.distManifestHash || '')) {
    fail('P04 baseline detached-build provenance is invalid');
  }
  const files = metric.files.map(({ path: filePath, bytes }) => `${filePath}\0${bytes}`);
  const manifest = build.distManifest.map(({ path: filePath, bytes, sha256 }) => {
    if (!/^[0-9a-f]{64}$/u.test(sha256 || '')) fail('P04 baseline dist manifest hash is invalid');
    return `${filePath}\0${bytes}`;
  });
  exactValue(manifest, files, 'P04 baseline dist manifest');
  const hash = createHash('sha256').update(build.distManifest.map(({ path: filePath, bytes, sha256 }) => (
    `${filePath}\0${bytes}\0${sha256}\n`
  )).join('')).digest('hex');
  if (hash !== build.distManifestHash) fail('P04 baseline dist manifest aggregate hash mismatch');
}

function validateCandidateResourceBuild(metric, subjectSha, subjectTree) {
  const build = metric?.candidateBuild;
  exactKeys(build, [
    'subjectSha', 'subjectTree', 'installArgv', 'buildArgv', 'distManifest', 'distManifestHash',
  ], 'P04 candidate build');
  if (build.subjectSha !== subjectSha || build.subjectTree !== subjectTree
    || JSON.stringify(build.installArgv) !== JSON.stringify(['npm', 'ci'])
    || JSON.stringify(build.buildArgv) !== JSON.stringify(['npm', 'run', 'build'])
    || !Array.isArray(build.distManifest) || build.distManifest.length !== metric.files.length
    || !/^[0-9a-f]{64}$/u.test(build.distManifestHash || '')) {
    fail('P04 candidate detached-build provenance is invalid');
  }
  const files = metric.files.map(({ path: filePath, bytes }) => `${filePath}\0${bytes}`);
  const manifest = build.distManifest.map(({ path: filePath, bytes, sha256 }) => {
    if (!/^[0-9a-f]{64}$/u.test(sha256 || '')) fail('P04 candidate dist manifest hash is invalid');
    return `${filePath}\0${bytes}`;
  });
  exactValue(manifest, files, 'P04 candidate dist manifest');
  const hash = createHash('sha256').update(build.distManifest.map(({ path: filePath, bytes, sha256 }) => (
    `${filePath}\0${bytes}\0${sha256}\n`
  )).join('')).digest('hex');
  if (hash !== build.distManifestHash) fail('P04 candidate dist manifest aggregate hash mismatch');
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
  try {
    assertPerformanceBaselineProvenance({
      repositoryRoot: context.repoRoot,
      planBaseSha: plannedBaseSha,
      baselineBaseSha: baseSha,
    });
  } catch (error) {
    fail(`performance baseline provenance rejected: ${error.message}`);
  }
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
  validateComparableLoadAverage(baselineDocument.environment, evidence.environment);
  validateMeasurementAudit(baselineDocument);
  validateBaseResourceBuild(baselineDocument.metrics['P04-resource-budget'], baseSha, baseTree);
  validateCandidateResourceBuild(
    evidence.metrics['P04-resource-budget'],
    context.subjectSha,
    context.subjectTree,
  );
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

function validateMeasurementMetricSet(metrics, subjectSha, label) {
  exactSet(Object.keys(metrics || {}), Object.keys(performanceCaseIds), `${label} metricIds`);
  for (const metricId of Object.keys(performanceCaseIds)) {
    if (metrics[metricId]?.metricId !== metricId) fail(`${label} ${metricId} identity mismatch`);
  }
  validateRenderMetric(metrics['P01-render-isolation'], subjectSha, `${label} P01`);
  validatePairedTimingMetric(
    metrics['P02-history-budget'],
    performanceCaseIds['P02-history-budget'],
    subjectSha,
    `${label} P02`,
  );
  validateTimingMetric(
    metrics['P03-feedback-budget'],
    performanceCaseIds['P03-feedback-budget'],
    subjectSha,
    `${label} P03`,
  );
  validateResourceMetric(metrics['P04-resource-budget'], subjectSha, `${label} P04`);
}

function validateMeasurementAudit(baselineDocument) {
  const measurementAudit = baselineDocument.measurementAudit;
  exactKeys(
    measurementAudit,
    ['runCount', 'designatedRun', 'reproducibilityRuns'],
    'performance measurementAudit',
  );
  if (measurementAudit.runCount !== 3 || measurementAudit.designatedRun !== 1
    || !Array.isArray(measurementAudit.reproducibilityRuns)
    || measurementAudit.reproducibilityRuns.length !== 2) {
    fail('performance measurementAudit must bind exactly three runs with designated run one');
  }

  const designatedGeneratedAt = Date.parse(baselineDocument.generatedAt);
  if (!Number.isFinite(designatedGeneratedAt)) fail('performance designated run timestamp is invalid');
  const expectedEnvironment = stablePerformanceEnvironment(baselineDocument.environment);
  const expectedProvenance = baselineDocument.provenance;
  let previousGeneratedAt = designatedGeneratedAt;
  const seenRuns = new Set([measurementAudit.designatedRun]);

  validateMeasurementMetricSet(baselineDocument.metrics, baselineDocument.baseSha, 'performance designated run');
  for (const [index, run] of measurementAudit.reproducibilityRuns.entries()) {
    const expectedRun = index + 2;
    exactKeys(
      run,
      ['run', 'generatedAt', 'runnerContentHash', 'bindings', 'metrics'],
      `performance reproduction run ${expectedRun}`,
    );
    if (run.run !== expectedRun || seenRuns.has(run.run)) {
      fail('performance measurementAudit contains a missing, duplicate, or reordered run');
    }
    seenRuns.add(run.run);
    const generatedAt = Date.parse(run.generatedAt);
    if (!Number.isFinite(generatedAt) || generatedAt <= previousGeneratedAt) {
      fail(`performance reproduction run ${expectedRun} timestamp is invalid or unordered`);
    }
    previousGeneratedAt = generatedAt;

    exactKeys(run.bindings, [
      'subjectSha', 'subjectTree', 'environment', 'runnerSha', 'runnerTree',
      'runnerContentHash', 'changedPaths',
    ], `performance reproduction run ${expectedRun} bindings`);
    exactValue(run.bindings.subjectSha, baselineDocument.baseSha, `performance reproduction run ${expectedRun} subjectSha`);
    exactValue(run.bindings.subjectTree, baselineDocument.subjectTree, `performance reproduction run ${expectedRun} subjectTree`);
    exactValue(run.bindings.environment, expectedEnvironment, `performance reproduction run ${expectedRun} environment`);
    exactValue(run.bindings.runnerSha, expectedProvenance.runnerSha, `performance reproduction run ${expectedRun} runnerSha`);
    exactValue(run.bindings.runnerTree, expectedProvenance.runnerTree, `performance reproduction run ${expectedRun} runnerTree`);
    exactValue(run.bindings.runnerContentHash, expectedProvenance.runnerContentHash, `performance reproduction run ${expectedRun} runnerContentHash`);
    exactValue(run.runnerContentHash, expectedProvenance.runnerContentHash, `performance reproduction run ${expectedRun} content hash`);
    exactValue(
      run.bindings.changedPaths,
      expectedProvenance.baselineAudit.changedPaths,
      `performance reproduction run ${expectedRun} changedPaths`,
    );
    validateMeasurementMetricSet(run.metrics, baselineDocument.baseSha, `performance reproduction run ${expectedRun}`);
  }
  if (seenRuns.size !== measurementAudit.runCount) {
    fail('performance measurementAudit run count does not match its unique runs');
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
  validatePairedTimingMetric(current['P02-history-budget'], performanceCaseIds['P02-history-budget'], context.subjectSha, 'P02 current');
  validatePairedTimingMetric(frozen['P02-history-budget'], performanceCaseIds['P02-history-budget'], baselineDocument.baseSha, 'P02 baseline');
  validatePairedTimingCompatibility(
    current['P02-history-budget'],
    frozen['P02-history-budget'],
    performanceCaseIds['P02-history-budget'],
  );
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
  for (const caseId of performanceCaseIds['P02-history-budget']) {
    statuses.set(caseId, current['P02-history-budget'].cases[caseId].normalizedRatioMedian
      <= frozen['P02-history-budget'].cases[caseId].normalizedRatioMedian * 1.15);
  }
  for (const caseId of performanceCaseIds['P03-feedback-budget']) {
    statuses.set(caseId, current['P03-feedback-budget'].cases[caseId].durationMedianMs
      <= frozen['P03-feedback-budget'].cases[caseId].durationMedianMs * 1.15
      && current['P03-feedback-budget'].cases[caseId].durationMedianMs <= P03_ABSOLUTE_FEEDBACK_LIMIT_MS);
  }
  statuses.set('bundle-total-bytes', current['P04-resource-budget'].totalBundleBytes
    <= frozen['P04-resource-budget'].totalBundleBytes * 1.05);
  statuses.set('bundle-max-chunk-bytes', current['P04-resource-budget'].maxChunkBytes
    <= frozen['P04-resource-budget'].maxChunkBytes * 1.05);
  statuses.set('heap-used-median-bytes', current['P04-resource-budget'].heapUsedMedianBytes
    <= frozen['P04-resource-budget'].heapUsedMedianBytes * 1.05);
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
  validateDeliveryRunnerProvenance(report.provenance, context, check);
  const commands = report.verdict?.commands;
  if (!Array.isArray(commands)) fail('delivery smoke commands must be an array');
  const status = normalizedRunnerStatus(report.verdict.status);
  if (status === 'PASS') {
    if (report.verdict.executedCommands !== deliveryCommands.length
      || commands.length !== report.verdict.executedCommands) {
      fail('delivery smoke PASS must execute every frozen command');
    }
    commands.forEach((command, index) => {
      const expected = deliveryCommands[index];
      exactKeys(command, [
        'id', 'argv', 'cwd', 'exitCode', 'signal', 'startedAt', 'finishedAt', 'durationMs', 'status',
      ], `delivery smoke command ${index}`);
      if (command.id !== expected.id || command.cwd !== expected.cwd) {
        fail(`delivery smoke command ${index} identity mismatch`);
      }
      exactOrdered(command.argv, expected.argv, `delivery smoke command ${expected.id} argv`);
      const commandStartedAt = Date.parse(command.startedAt);
      const commandFinishedAt = Date.parse(command.finishedAt);
      if (!Number.isFinite(commandStartedAt) || !Number.isFinite(commandFinishedAt)
        || commandStartedAt < startedAt - 5_000 || commandFinishedAt > finishedAt + 5_000
        || commandFinishedAt < commandStartedAt
        || command.durationMs !== commandFinishedAt - commandStartedAt
        || command.exitCode !== 0 || command.signal !== null || command.status !== 'PASS') {
        fail(`delivery smoke command ${expected.id} execution provenance is invalid`);
      }
    });
  }
  return status;
}

function validateDeliveryRunnerProvenance(provenance, context, check) {
  exactKeys(provenance, [
    'runnerId', 'runnerSha', 'runnerTree', 'runnerContentHash', 'runnerFiles',
    'worktreeClean', 'worktreeStatus', 'baselineAudit',
  ], 'delivery runner provenance');
  const scoreBaseTree = git(context.repoRoot, ['rev-parse', `${context.scoreBaseSha}^{tree}`]);
  if (provenance.runnerId !== 'frontend-delivery-smoke'
    || provenance.runnerSha !== context.scoreBaseSha
    || provenance.runnerTree !== scoreBaseTree
    || provenance.worktreeClean !== true
    || !Array.isArray(provenance.worktreeStatus) || provenance.worktreeStatus.length !== 0
    || provenance.baselineAudit !== null
    || !gitSucceeds(context.repoRoot, ['merge-base', '--is-ancestor', provenance.runnerSha, context.subjectSha])) {
    fail('delivery runner is not the clean frozen SCORE_BASE runner');
  }
  if (!Array.isArray(provenance.runnerFiles)) fail('delivery runnerFiles must be an array');
  exactSet(provenance.runnerFiles.map(({ path: runnerPath }) => runnerPath), check.runnerFiles, 'delivery runnerFiles');
  const byPath = new Map(provenance.runnerFiles.map((entry) => [entry.path, entry]));
  const aggregate = createHash('sha256');
  for (const runnerPath of check.runnerFiles) {
    const expectedSha = runnerFileSha256(context.repoRoot, context.scoreBaseSha, runnerPath);
    const entry = byPath.get(runnerPath);
    if (!entry || entry.sha256 !== expectedSha || !/^[0-9a-f]{64}$/u.test(entry.sha256)) {
      fail(`delivery frozen runner file hash mismatch: ${runnerPath}`);
    }
    if (context.subjectSha !== context.scoreBaseSha
      && runnerFileSha256(context.repoRoot, null, runnerPath) !== expectedSha) {
      fail(`delivery candidate runner content differs from SCORE_BASE: ${runnerPath}`);
    }
    aggregate.update(`${runnerPath}\0${entry.sha256}\n`);
  }
  if (aggregate.digest('hex') !== provenance.runnerContentHash) {
    fail('delivery runnerContentHash does not match runnerFiles');
  }
}

function validateActionProductionBindingReport(report, { context, control, check, startedAt, finishedAt }) {
  validateReportBinding(report, context, startedAt, finishedAt, 2);
  if (control.id !== 'T02-critical-action-coverage' || report.controlId !== control.id
    || report.status !== 'covered'
    || !Array.isArray(report.bindings) || report.bindings.length === 0) {
    fail('T02 action production binding report is incomplete');
  }
  const registry = JSON.parse(fs.readFileSync(path.join(frozenAppRoot, 'config/action-producer-registry.json'), 'utf8'));
  const matrix = JSON.parse(fs.readFileSync(path.join(frozenAppRoot, 'config/action-producer-test-matrix.json'), 'utf8'));
  if (!Array.isArray(registry.coveredProducers) || !Array.isArray(registry.exemptions)
    || registry.exemptions.length !== 0) {
    fail('T02 frozen action registry must have covered producers and zero exemptions');
  }
  const expectedActionIds = registry.coveredProducers.map(({ actionId }) => actionId);
  const expectedErrorSourceCaseCount = registry.coveredProducers.reduce((sum, entry) => sum + entry.errorSources.length, 0);
  if (!Array.isArray(matrix.runtimeBindings)) fail('T02 production runtime binding matrix is missing');
  exactValue(matrix.runtimeBindings, criticalRuntimeAnchors, 'T02 frozen runtime anchor contracts');
  const runtimeAnchors = new Map(matrix.runtimeBindings.map((entry) => [entry.actionId, entry]));
  if (runtimeAnchors.size !== matrix.runtimeBindings.length) fail('T02 production runtime binding matrix has duplicates');
  exactSet(report.actionIds || [], expectedActionIds, 'T02 production actionIds');
  const expectedBindingCount = registry.coveredProducers.reduce((sum, entry) => sum + entry.producerCount, 0);
  if (report.structuralActionCount !== expectedActionIds.length
    || report.errorSourceCaseCount !== expectedErrorSourceCaseCount
    || matrix.cells.length !== expectedErrorSourceCaseCount
    || report.bindingCount !== expectedBindingCount || report.bindings.length !== expectedBindingCount) {
    fail('T02 production binding count mismatch');
  }
  const counts = new Map();
  const bindingKeys = new Set();
  const sourceDocuments = new Map();
  for (const binding of report.bindings) {
    exactKeys(binding, [
      'actionId', 'kind', 'sourcePath', 'line', 'column', 'callbackKind', 'handlers',
      'callbackStart', 'callbackEnd', 'sourceSha256', 'guardMutationDetection',
    ], `T02 binding ${binding?.actionId || '<unknown>'}`);
    if (!expectedActionIds.includes(binding.actionId) || !['user', 'background'].includes(binding.kind)
      || !['identifier', 'member', 'arrow', 'function'].includes(binding.callbackKind)
      || !Array.isArray(binding.handlers) || binding.handlers.length === 0
      || new Set(binding.handlers).size !== binding.handlers.length
      || binding.handlers.some((handler) => typeof handler !== 'string' || !handler.trim())
      || binding.handlers.every((handler) => ['runUIAction', 'runBackgroundAction'].includes(handler))
      || typeof binding.sourcePath !== 'string' || !binding.sourcePath.startsWith('src/')
      || /(?:^|\/)runUIAction\.js$/u.test(binding.sourcePath) || /\.test\.[jt]sx?$/u.test(binding.sourcePath)
      || !Number.isInteger(binding.line) || binding.line <= 0
      || !Number.isInteger(binding.column) || binding.column <= 0
      || !Number.isInteger(binding.callbackStart) || binding.callbackStart < 0
      || !Number.isInteger(binding.callbackEnd) || binding.callbackEnd <= binding.callbackStart) {
      fail(`T02 action-specific production binding is invalid: ${binding.actionId}`);
    }
    const repoPath = `frontend-app/${binding.sourcePath}`;
    if (!sourceDocuments.has(repoPath)) {
      const contents = execFileSync('git', ['show', `${context.subjectSha}:${repoPath}`], { cwd: context.repoRoot });
      sourceDocuments.set(repoPath, {
        sha256: createHash('sha256').update(contents).digest('hex'),
        source: contents.toString('utf8'),
        lines: contents.toString('utf8').split(/\r?\n/u),
      });
    }
    const sourceDocument = sourceDocuments.get(repoPath);
    if (binding.sourceSha256 !== sourceDocument.sha256) {
      fail(`T02 production source hash mismatch: ${binding.actionId}`);
    }
    const sourceLine = sourceDocument.lines[binding.line - 1] || '';
    if (!sourceLine.includes(binding.actionId)) fail(`T02 production actionId is not bound at reported line: ${binding.actionId}`);
    exactKeys(binding.guardMutationDetection, [
      'mutationId', 'detected', 'sourceSha256', 'mutatedSha256',
    ], `T02 structural mutation detection ${binding.actionId}`);
    const structurallyMutated = `${sourceDocument.source.slice(0, binding.callbackStart)}() => {}${sourceDocument.source.slice(binding.callbackEnd)}`;
    if (binding.guardMutationDetection.mutationId !== 'empty-production-callback'
      || binding.guardMutationDetection.detected !== true
      || binding.guardMutationDetection.sourceSha256 !== sourceDocument.sha256
      || binding.guardMutationDetection.mutatedSha256 !== createHash('sha256').update(structurallyMutated).digest('hex')
      || binding.guardMutationDetection.mutatedSha256 === sourceDocument.sha256) {
      fail(`T02 structural production callback mutation was not detected: ${binding.actionId}`);
    }
    const key = `${binding.actionId}\0${binding.sourcePath}\0${binding.line}\0${binding.column}`;
    if (bindingKeys.has(key)) fail(`T02 duplicate production binding: ${binding.actionId}`);
    bindingKeys.add(key);
    counts.set(binding.actionId, (counts.get(binding.actionId) || 0) + 1);
  }
  for (const entry of registry.coveredProducers) {
    if (counts.get(entry.actionId) !== entry.producerCount) {
      fail(`T02 per-action production binding count mismatch: ${entry.actionId}`);
    }
  }
  if (!Array.isArray(report.requiredRuntimeActionIds) || !Array.isArray(report.runtimeCases)
    || report.runtimeEvidenceScope !== 'five-semantic-class-anchors'
    || report.runtimeAnchorCount !== criticalRuntimeAnchors.length
    || report.runtimeClaimsFullMatrix !== false) {
    fail('T02 production runtime coverage sets are missing');
  }
  exactSet([...runtimeAnchors.keys()], criticalRuntimeActionIds, 'T02 matrix runtime actionIds');
  exactSet(matrix.runtimeBindings.map(({ semanticClass }) => semanticClass), criticalRuntimeSemanticClasses, 'T02 matrix runtime semantic classes');
  exactSet(report.requiredRuntimeActionIds, criticalRuntimeActionIds, 'T02 required runtime actionIds');
  exactSet(report.requiredRuntimeSemanticClasses || [], criticalRuntimeSemanticClasses, 'T02 required runtime semantic classes');
  exactSet(report.runtimeCases.map(({ actionId }) => actionId), criticalRuntimeActionIds, 'T02 runtime actionIds');
  exactSet(report.runtimeCases.map(({ semanticClass }) => semanticClass), criticalRuntimeSemanticClasses, 'T02 runtime semantic classes');
  for (const runtimeCase of report.runtimeCases) {
    exactKeys(runtimeCase, [
      'semanticClass', 'actionId', 'sourcePath', 'sourceSha256', 'mutatedSha256', 'handlers',
      'bindingLocations', 'testFile', 'testName', 'testFileSha256', 'green', 'red',
    ], `T02 runtime case ${runtimeCase?.actionId || '<unknown>'}`);
    const anchor = runtimeAnchors.get(runtimeCase.actionId);
    if (!anchor || runtimeCase.semanticClass !== anchor.semanticClass || runtimeCase.sourcePath !== anchor.sourcePath
      || runtimeCase.testFile !== anchor.testFile || runtimeCase.testName !== anchor.testName) {
      fail(`T02 runtime test anchor mismatch: ${runtimeCase.actionId}`);
    }
    const anchoredBindings = report.bindings.filter(({ actionId, sourcePath }) => (
      actionId === runtimeCase.actionId && sourcePath === runtimeCase.sourcePath
    )).sort((left, right) => right.callbackStart - left.callbackStart);
    if (anchoredBindings.length === 0) fail(`T02 runtime case has no production binding: ${runtimeCase.actionId}`);
    const sourceDocument = sourceDocuments.get(`frontend-app/${runtimeCase.sourcePath}`);
    let mutated = sourceDocument.source;
    for (const binding of anchoredBindings) {
      mutated = `${mutated.slice(0, binding.callbackStart)}() => {}${mutated.slice(binding.callbackEnd)}`;
    }
    const expectedHandlers = [...new Set(anchoredBindings.flatMap(({ handlers }) => handlers))].sort();
    const expectedLocations = anchoredBindings.map(({ line, column }) => ({ line, column }));
    const testContents = execFileSync('git', [
      'show', `${context.subjectSha}:frontend-app/${runtimeCase.testFile}`,
    ], { cwd: context.repoRoot });
    exactValue(runtimeCase.handlers, expectedHandlers, `T02 runtime handlers ${runtimeCase.actionId}`);
    exactValue(runtimeCase.bindingLocations, expectedLocations, `T02 runtime locations ${runtimeCase.actionId}`);
    exactKeys(runtimeCase.green, ['cwd', 'argv', 'exitCode', 'signal', 'outputSha256'], `T02 runtime GREEN ${runtimeCase.actionId}`);
    exactKeys(runtimeCase.red, ['cwd', 'argv', 'exitCode', 'signal', 'outputSha256'], `T02 runtime RED ${runtimeCase.actionId}`);
    const expectedArgv = [
      path.join('node_modules', '.bin', 'vitest'), 'run', runtimeCase.testFile,
      '-t', runtimeCase.testName, '--no-file-parallelism', '--maxWorkers=1',
    ];
    if (runtimeCase.sourceSha256 !== sourceDocument.sha256
      || runtimeCase.mutatedSha256 !== createHash('sha256').update(mutated).digest('hex')
      || runtimeCase.mutatedSha256 === runtimeCase.sourceSha256
      || runtimeCase.testFileSha256 !== createHash('sha256').update(testContents).digest('hex')
      || runtimeCase.green.cwd !== 'frontend-app'
      || JSON.stringify(runtimeCase.green.argv) !== JSON.stringify(expectedArgv)
      || runtimeCase.green.exitCode !== 0 || runtimeCase.green.signal !== null
      || !/^[0-9a-f]{64}$/u.test(runtimeCase.green.outputSha256)
      || runtimeCase.red.cwd !== 'frontend-app'
      || JSON.stringify(runtimeCase.red.argv) !== JSON.stringify(expectedArgv)
      || !Number.isInteger(runtimeCase.red.exitCode) || runtimeCase.red.exitCode <= 0
      || runtimeCase.red.signal !== null || !/^[0-9a-f]{64}$/u.test(runtimeCase.red.outputSha256)) {
      fail(`T02 runtime production mutation RED is invalid: ${runtimeCase.actionId}`);
    }
  }
  if (!report.matrixExecution || !Array.isArray(report.cellResults)
    || report.testCount !== report.cellResults.length || report.testCount !== matrix.cells.length
    || report.testCount !== check.testCount) {
    fail('T02 production report must derive testCount from the complete cell result set');
  }
  exactKeys(report.matrixExecution, [
    'testFile', 'testFileSha256', 'cwd', 'argv', 'exitCode', 'signal', 'outputSha256', 'vitest',
  ], 'T02 matrix execution');
  const matrixTestFile = 'src/shared/ui/productionActionFailureMatrix.test.js';
  const matrixTestSource = execFileSync('git', [
    'show', `${context.subjectSha}:frontend-app/${matrixTestFile}`,
  ], { cwd: context.repoRoot });
  const expectedMatrixArgv = [
    path.join('node_modules', '.bin', 'vitest'), 'run', matrixTestFile,
    '--reporter=json', '--no-file-parallelism', '--maxWorkers=1',
  ];
  if (report.matrixExecution.testFile !== matrixTestFile
    || report.matrixExecution.testFileSha256 !== createHash('sha256').update(matrixTestSource).digest('hex')
    || report.matrixExecution.cwd !== 'frontend-app'
    || JSON.stringify(report.matrixExecution.argv) !== JSON.stringify(expectedMatrixArgv)
    || report.matrixExecution.exitCode !== 0 || report.matrixExecution.signal !== null
    || !/^[0-9a-f]{64}$/u.test(report.matrixExecution.outputSha256)) {
    fail('T02 production matrix execution is invalid');
  }
  exactKeys(report.matrixExecution.vitest, [
    'numTotalTests', 'numPassedTests', 'numFailedTests', 'numPendingTests',
  ], 'T02 matrix Vitest execution');
  if (report.matrixExecution.vitest.numTotalTests !== matrix.cells.length
    || report.matrixExecution.vitest.numPassedTests !== matrix.cells.length
    || report.matrixExecution.vitest.numFailedTests !== 0
    || report.matrixExecution.vitest.numPendingTests !== 0) {
    fail('T02 production matrix execution did not run every cell exactly once');
  }
  const expectedCells = new Map(matrix.cells.map((cell, index) => [
    `${cell.actionId}\0${cell.errorSource}`,
    { ...cell, testName: productionActionFailureMatrixTitle(index, cell) },
  ]));
  if (expectedCells.size !== matrix.cells.length || report.cellResults.length !== expectedCells.size) {
    fail('T02 production report has missing or duplicate cell results');
  }
  const actualCells = new Set();
  for (const cell of report.cellResults) {
    exactKeys(cell, [
      'actionId', 'errorSource', 'evidence', 'bindingKeys', 'testName', 'testFileSha256', 'vitest',
    ], `T02 cell ${cell?.actionId || '<unknown>'}`);
    const key = `${cell.actionId}\0${cell.errorSource}`;
    const expected = expectedCells.get(key);
    const expectedBindingKeys = report.bindings.filter(({ actionId }) => actionId === cell.actionId)
      .map(({ sourcePath, line, column }) => `${sourcePath}:${line}:${column}`).sort();
    if (!expected || actualCells.has(key) || !Array.isArray(cell.evidence) || !Array.isArray(cell.bindingKeys)
      || cell.evidence.length === 0 || new Set(cell.evidence).size !== cell.evidence.length
      || JSON.stringify(cell.evidence) !== JSON.stringify(expected.evidence)
      || JSON.stringify(cell.bindingKeys) !== JSON.stringify(expectedBindingKeys)
      || cell.testName !== expected.testName
      || cell.testFileSha256 !== report.matrixExecution.testFileSha256) {
      fail(`T02 cell result is invalid: ${cell?.actionId || '<unknown>'}/${cell?.errorSource || '<unknown>'}`);
    }
    exactKeys(cell.vitest, ['status', 'title'], `T02 cell Vitest result ${cell.actionId}/${cell.errorSource}`);
    if (cell.vitest.status !== 'passed' || cell.vitest.title !== cell.testName) {
      fail(`T02 cell has no non-empty passing named test: ${cell.actionId}/${cell.errorSource}`);
    }
    actualCells.add(key);
  }
  exactSet([...actualCells], [...expectedCells.keys()], 'T02 production cell results');
  return 'PASS';
}

function validateActualRunnerEvidence(report, options) {
  switch (options.check.evidenceProtocol) {
    case 'action-production-runtime-report-v1': return validateActionProductionBindingReport(report, options);
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

async function commandResult(command, args, options) {
  const result = await runManagedCommand(command, args, {
    cwd: options.cwd,
    env: options.env,
    maxBuffer: 16 * 1024 * 1024,
    timeoutMs: options.timeoutMs,
    killGraceMs: 20_000,
  });
  return {
    exitCode: result.status ?? 1,
    signal: result.signal ?? null,
    stdout: result.stdout || '',
    stderr: result.stderr || '',
    timedOut: result.timedOut,
    error: result.error || (result.timedOut ? new Error(`managed command timed out after ${options.timeoutMs}ms`) : undefined),
  };
}

function expandedRunnerArgv(context, check) {
  return check.argv.map((argument) => {
    if (argument === '$SCORE_BASE_SHA') return context.scoreBaseSha;
    if (argument === '$SUBJECT_SHA') return context.subjectSha;
    if (argument === '$SUBJECT_REPO') return context.repoRoot;
    if (argument === '$FROZEN_BASELINE') return path.join(frozenScriptRoot, 'frontend-maintainability-baseline.json');
    return argument;
  });
}

async function executeActualRunner(context, check) {
  if (!context.evidenceCache) context.evidenceCache = new Map();
  const argv = expandedRunnerArgv(context, check);
  const cacheKey = structuredRunnerExecutionKey(context, check);
  if (context.evidenceCache.has(cacheKey)) return context.evidenceCache.get(cacheKey);
  const [command, runnerPath, ...args] = argv;
  const cwd = fs.realpathSync(path.resolve(context.repoRoot, check.cwd));
  const runnerRoot = check.evidenceProtocol === 'delivery-smoke-json-v1' ? frozenAppRoot : cwd;
  const requestedRunnerPath = path.resolve(runnerRoot, runnerPath);
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
  let reportPath;
  let reportSourcePath = `${check.cwd}/${runnerPath}#stdout`.replace(/^\.\//u, '');
  if (check.reportPath !== undefined) {
    if (typeof check.reportPath !== 'string' || !check.reportPath.trim() || path.isAbsolute(check.reportPath)
      || check.reportPath.split('/').includes('..')) {
      fail(`structured runner reportPath is invalid: ${String(check.reportPath)}`);
    }
    reportPath = path.resolve(context.repoRoot, check.reportPath);
    reportSourcePath = path.relative(context.repoRoot, reportPath).split(path.sep).join('/');
    if (reportSourcePath.startsWith('../') || reportSourcePath === '..') {
      fail(`structured runner reportPath escapes target repository: ${check.reportPath}`);
    }
    fs.rmSync(reportPath, { force: true });
  }
  const runnerArgv = [command === 'node' ? process.execPath : command, absoluteRunnerPath, ...args];
  const runnerSha256 = createHash('sha256').update(fs.readFileSync(absoluteRunnerPath)).digest('hex');
  let loadWindow;
  if (check.evidenceProtocol === 'performance-budget-json-v1') {
    loadWindow = await waitForPerformanceLoadWindow({ runnerArgv, runnerCwd: cwd });
    if (loadWindow.status !== 'READY') {
      const blocked = {
        loadWindowTimeout: true,
        invocation: {
          cwd,
          argv: runnerArgv,
          runnerPath: absoluteRunnerPath,
          runnerSha256,
          startedAt: null,
          finishedAt: null,
          exitCode: null,
          signal: null,
          loadWindow,
        },
      };
      context.evidenceCache.set(cacheKey, blocked);
      return blocked;
    }
  }
  const startedAt = Date.now();
  const result = await commandResult(runnerArgv[0], runnerArgv.slice(1), {
    cwd,
    timeoutMs: check.timeoutMs,
    env: process.env,
  });
  const finishedAt = Date.now();
  let report;
  let reportArtifact;
  try {
    if (reportPath) {
      const stat = fs.lstatSync(reportPath);
      if (!stat.isFile() || stat.isSymbolicLink()) fail('structured runner report must be a regular file');
    }
    const raw = reportPath ? fs.readFileSync(reportPath, 'utf8') : result.stdout;
    report = JSON.parse(raw.trim());
    reportArtifact = {
      path: reportSourcePath,
      sha256: createHash('sha256').update(raw).digest('hex'),
      bytes: Buffer.byteLength(raw),
    };
  }
  catch (error) {
    report = { parseError: error.message };
  }
  const executed = {
    result,
    report,
    reportArtifact,
    startedAt,
    finishedAt,
    invocation: {
      cwd,
      argv: runnerArgv,
      runnerPath: absoluteRunnerPath,
      runnerSha256,
      startedAt: new Date(startedAt).toISOString(),
      finishedAt: new Date(finishedAt).toISOString(),
      exitCode: result.exitCode,
      signal: result.signal,
      ...(loadWindow ? { loadWindow } : {}),
    },
  };
  context.evidenceCache.set(cacheKey, executed);
  return executed;
}

export function structuredRunnerExecutionKey(context, check) {
  return JSON.stringify([
    check.evidenceProtocol,
    check.cwd,
    expandedRunnerArgv(context, check),
    check.reportPath || '',
  ]);
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

export function acceptsNonZeroRunnerExitForValidatedSlice(check, report, exitCode, status) {
  return status === 'PASS'
    && check.evidenceProtocol === 'performance-budget-json-v1'
    && exitCode === 2
    && report?.verdict?.status === 'FAIL';
}

async function actionProducerGuardProbe(context, control, check) {
  const execution = await executeActualRunner(context, check);
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

async function structuredProbe(context, control, check) {
  if (check.evidenceProtocol === 'action-producer-guard-v1') {
    return actionProducerGuardProbe(context, control, check);
  }
  const execution = await executeActualRunner(context, check);
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
  if (execution.loadWindowTimeout) {
    const { loadWindow } = execution.invocation;
    return evidenceRecord(context, control, check, {
      status: 'FAIL',
      exitCode: 1,
      summary: `performance load window timed out after ${loadWindow.elapsedMs}ms; tolerance=${loadWindow.tolerance}`,
      runnerExecution: execution.invocation,
    });
  }
  const { result, report, reportArtifact, startedAt, finishedAt, invocation } = execution;
  const output = `${result.stdout}${result.stderr}`.trim();
  if (result.error) {
    return evidenceRecord(context, control, check, {
      status: 'FAIL', exitCode: result.exitCode, summary: result.error.message, runnerExecution: invocation,
    });
  }
  if (report.parseError) {
    return evidenceRecord(context, control, check, {
      status: 'FAIL', exitCode: result.exitCode, summary: `runner report is not valid JSON: ${report.parseError}; ${output.slice(-600)}`,
      runnerExecution: invocation,
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
      runnerExecution: invocation,
    });
  }
  recordRawReport(context, check, report, reportArtifact);
  if (status === 'PASS' && result.exitCode !== 0
    && !acceptsNonZeroRunnerExitForValidatedSlice(check, report, result.exitCode, status)) {
    return evidenceRecord(context, control, check, {
      status: 'FAIL', exitCode: result.exitCode, summary: 'runner reported PASS with a non-zero exit',
      runnerReport: reportArtifact,
      runnerExecution: invocation,
    });
  }
  return evidenceRecord(context, control, check, {
    status,
    exitCode: status === 'PASS' ? 0 : status === 'FAIL' ? 1 : null,
    summary: `${check.evidenceProtocol} ${status}; cases=${check.caseIds.length} tests=${check.testCount}`,
    runnerReport: reportArtifact,
    runnerExecution: invocation,
  });
}

export async function commandEvidenceStatus({ repoRoot = frozenRepoRoot, cwd = '.', argv, timeoutMs = 10_000 }) {
  if (!Array.isArray(argv) || argv.length === 0) fail('command evidence argv is empty');
  const [command, ...args] = argv;
  const result = await commandResult(command, args, {
    cwd: path.resolve(repoRoot, cwd),
    timeoutMs,
    env: process.env,
  });
  return result.error || result.exitCode !== 0 ? 'FAIL' : 'PASS';
}

function evidenceRecord(context, control, check, result) {
  return {
    scoreBaseSha: context.scoreBaseSha,
    scoreBaseTree: context.scoreBaseTree,
    subjectSha: context.subjectSha,
    subjectTree: context.subjectTree,
    subjectContract: context.subjectContract,
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

async function runCommand(context, control, check) {
  const argv = expandedRunnerArgv(context, check);
  const [command, ...args] = argv;
  const cwd = path.resolve(context.repoRoot, check.cwd);
  const result = await commandResult(command, args, {
    cwd,
    timeoutMs: check.timeoutMs,
    env: process.env,
  });
  const output = `${result.stdout}${result.stderr}`.trim();
  return evidenceRecord(context, control, check, {
    status: result.error || result.exitCode !== 0 ? 'FAIL' : 'PASS',
    exitCode: result.exitCode,
    summary: output.slice(result.exitCode === 0 ? -600 : -1200) || result.error?.message || '',
    runnerExecution: { cwd, argv, exitCode: result.exitCode, signal: result.signal },
  });
}

async function evaluateProbe(context, control, check) {
  try {
    if (check.evidenceProtocol === 'git-diff-check-v1') return runCommand(context, control, check);
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

async function evaluateCheck(context, control, check, runCommands) {
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
  if (check.evidenceProtocol === 'action-production-runtime-report-v1') {
    if (control.id !== 'T02-critical-action-coverage' || check.probe !== 'criticalActionCoverage'
      || check.requireZeroExemptions !== true) {
      fail(`invalid action production binding contract: ${control.id}`);
    }
    exact(check.argv, [
      'node', 'frontend-app/scripts/action-production-runtime-runner.mjs',
    ], 'action production binding argv');
    exact(check.caseIds, criticalRuntimeActionIds, 'action production binding caseIds');
    if (check.cwd !== '.' || check.reportPath !== '.tmp/action-producer/runtime-report.json' || check.testCount !== 382) {
      fail(`action production binding report contract differs from frozen contract: ${control.id}`);
    }
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
    if (check.reportPath !== '.tmp/desktop-failure-smoke/report.json' || check.testCount !== 2
      || !Array.isArray(check.sourcePaths) || check.sourcePaths.length === 0
      || new Set(check.sourcePaths).size !== check.sourcePaths.length
      || check.sourcePaths.some((sourcePath) => typeof sourcePath !== 'string' || sourcePath.length === 0)) {
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
    exact(check.argv, [
      'node', 'scripts/delivery-smoke-runner.mjs', '--verify',
      '--repo', '$SUBJECT_REPO', '--subject', '$SUBJECT_SHA',
    ], 'delivery smoke argv');
    exact(check.caseIds, deliveryCaseIds, 'delivery smoke caseIds');
    exact(check.runnerFiles, deliveryRunnerFiles, 'delivery audited runner files');
    if (check.testCount !== deliveryCaseIds.length) fail(`delivery smoke testCount differs from frozen contract: ${control.id}`);
    return;
  }
  if (check.evidenceProtocol === 'git-diff-check-v1') {
    if (control.id !== 'T04-local-gates' || check.probe !== 'gitDiffCheck') {
      fail(`invalid git diff check control: ${control.id}`);
    }
    exact(check.argv, [
      'git', 'diff', '--check', '$SCORE_BASE_SHA', '$SUBJECT_SHA',
    ], 'git diff check argv');
    exact(check.caseIds, ['frontend-diff-check'], 'git diff check caseIds');
    if (check.cwd !== '.' || check.testCount !== 1) {
      fail(`git diff check contract differs from frozen contract: ${control.id}`);
    }
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
  if (control.id === 'T04-local-gates'
    && JSON.stringify(check.argv) === JSON.stringify(t04FrontendTestCommand)
    && check.timeoutMs !== t04FrontendTestTimeoutMs) {
    fail(`T04 frontend-test timeout must be ${t04FrontendTestTimeoutMs}ms`);
  }
  if (control.id === 'T04-local-gates'
    && JSON.stringify(check.argv) === JSON.stringify(t04HookRouteGuardCommand)
    && (check.cwd !== '.' || check.timeoutMs !== t04HookRouteGuardTimeoutMs
      || JSON.stringify(check.caseIds) !== JSON.stringify(t04HookRouteGuardCaseIds)
      || check.testCount !== t04HookRouteGuardCaseIds.length)) {
    fail('T04 hook-route guard contract differs from frozen contract');
  }
  if (control.id === 'T04-local-gates'
    && JSON.stringify(check.argv) === JSON.stringify(t04FrontendPlanSizeCommand)
    && (check.cwd !== '.' || check.timeoutMs !== t04FrontendPlanSizeTimeoutMs
      || JSON.stringify(check.caseIds) !== JSON.stringify(t04FrontendPlanSizeCaseIds)
      || check.testCount !== t04FrontendPlanSizeCaseIds.length)) {
    fail('T04 frontend-plan size guard contract differs from frozen contract');
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

function validateT02DerivedLayerCounts(config) {
  const registry = JSON.parse(fs.readFileSync(path.join(frozenAppRoot, 'config/action-producer-registry.json'), 'utf8'));
  const matrix = JSON.parse(fs.readFileSync(path.join(frozenAppRoot, 'config/action-producer-test-matrix.json'), 'utf8'));
  const expectedMatrixCount = registry.coveredProducers.reduce((sum, entry) => sum + entry.errorSources.length, 0);
  if (!Array.isArray(matrix.cells) || matrix.cells.length !== expectedMatrixCount) {
    fail('T02 producer/error-source matrix count differs from the registry-derived exact set');
  }
  if (!Array.isArray(matrix.runtimeBindings) || matrix.runtimeBindings.length !== criticalRuntimeActionIds.length) {
    fail('T02 representative runtime binding count differs from the exact required set');
  }
  exactSet(matrix.runtimeBindings.map(({ actionId }) => actionId), criticalRuntimeActionIds, 'T02 derived runtime actionIds');
  exactSet(matrix.runtimeBindings.map(({ semanticClass }) => semanticClass), criticalRuntimeSemanticClasses, 'T02 derived runtime semantic classes');
  const control = config.controls.find(({ id }) => id === 'T02-critical-action-coverage');
  const matrixCheck = control?.allOf.find(({ argv }) => argv.includes('src/shared/ui/productionActionFailureMatrix.test.js'));
  const runtimeCheck = control?.allOf.find(({ evidenceProtocol }) => evidenceProtocol === 'action-production-runtime-report-v1');
  if (matrixCheck?.testCount !== expectedMatrixCount || runtimeCheck?.testCount !== expectedMatrixCount) {
    fail('T02 configured layer counts differ from registry/matrix-derived exact counts');
  }
}

export function validateConfiguration(config = controls, fixtureDocument = fixtures) {
  if (config !== controls) config = withFrozenDeliveryRunnerFiles(config);
  if (config.schemaVersion !== 1 || fixtureDocument.schemaVersion !== 1) fail('unsupported scorer schema version');
  if (!Array.isArray(config.controls) || config.controls.length !== 25) fail('controls must contain exactly 25 entries');
  exactSet(Object.keys(config.weights || {}), ['E', 'A', 'C', 'T', 'P'], 'dimension weights');
  if (Object.values(config.weights).reduce((sum, weight) => sum + weight, 0) !== 100) fail('dimension weights must total 100');
  if (JSON.stringify(config.thresholds) !== JSON.stringify(plannedThresholds)) fail('score thresholds differ from the frozen plan');
  validateFixtureDocument(fixtureDocument);

  const seen = new Set();
  for (const control of config.controls) validateConfiguredControl(config, control, seen);
  exactSet(seen, frozenControlIDs, 'control ids');
  validateT02DerivedLayerCounts(config);
  for (const dimension of Object.keys(config.weights)) {
    const points = config.controls
      .filter((control) => control.dimension === dimension)
      .reduce((sum, control) => sum + control.points, 0);
    if (points !== 100) fail(`dimension points must total 100: ${dimension}`);
  }
  if (baseline.baseSha !== plannedBaseSha || !/^[0-9a-f]{40}$/.test(baseline.planSnapshotSha)) {
    fail('baseline provenance is incomplete');
  }
  exactKeys(dependencyIntegrity, [
    'schemaVersion', 'packageLockSha256', 'nodeModulesTreeSha256', 'nodeModulesPathCount', 'requiredTools',
  ], 'immutable dependency integrity');
  if (dependencyIntegrity.schemaVersion !== 1
    || !/^[0-9a-f]{64}$/.test(dependencyIntegrity.packageLockSha256)
    || !/^[0-9a-f]{64}$/.test(dependencyIntegrity.nodeModulesTreeSha256)
    || !Number.isSafeInteger(dependencyIntegrity.nodeModulesPathCount)
    || dependencyIntegrity.nodeModulesPathCount <= 0) {
    fail('immutable dependency integrity is invalid');
  }
  exactSet(Object.keys(dependencyIntegrity.requiredTools || {}), [
    '@playwright/test/index.js', 'eslint/bin/eslint.js', 'vite/bin/vite.js', 'vitest/vitest.mjs',
  ], 'immutable dependency required tools');
  for (const [relativePath, sha256] of Object.entries(dependencyIntegrity.requiredTools)) {
    if (!relativePath.startsWith('@') && !relativePath.includes('/')) fail('immutable dependency tool path is invalid');
    if (!/^[0-9a-f]{64}$/.test(sha256)) fail(`immutable dependency tool SHA-256 is invalid: ${relativePath}`);
  }
  return true;
}
function performanceControl(control) {
  return control.allOf.some((check) => check.evidenceProtocol === 'performance-budget-json-v1');
}

function executionControls(controlDocument = controls) {
  const performance = controlDocument.controls.filter(performanceControl);
  const remaining = controlDocument.controls.filter((control) => !performanceControl(control));
  return [...performance, ...remaining];
}

export function executionControlIds(controlDocument = controls) {
  return executionControls(controlDocument).map((control) => control.id);
}

async function scoreContext(context, { runCommands }) {
  const executionContext = { ...context, evidenceCache: new Map(), rawReports: new Map() };
  const preflightEvidence = new Map();
  if (runCommands) {
    for (const control of executionControls()) {
      if (!performanceControl(control)) break;
      for (const [index, check] of control.allOf.entries()) {
        preflightEvidence.set(`${control.id}:${index}`, await evaluateCheck(executionContext, control, check, runCommands));
      }
    }
  }
  const scoredControls = [];
  for (const control of controls.controls) {
    const evidence = [];
    for (const [index, check] of control.allOf.entries()) {
      evidence.push(preflightEvidence.get(`${control.id}:${index}`)
        ?? await evaluateCheck(executionContext, control, check, runCommands));
    }
    scoredControls.push({
      id: control.id,
      dimension: control.dimension,
      points: control.points,
      required: control.required,
      status: controlStatus(evidence),
      evidence,
    });
  }
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
    scoreBaseTree: context.scoreBaseTree,
    subjectSha: context.subjectSha,
    subjectTree: context.subjectTree,
    subjectContract: context.subjectContract,
    baseline,
    controls: scoredControls,
    dimensions,
    rawBasisPoints: Math.round(rawBasisPoints),
    displayScore: Number((rawBasisPoints / 100).toFixed(1)),
    rawReports: Object.fromEntries(executionContext.rawReports),
  };
}

export async function scoreCurrentTree({
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

export async function probeResult(probe, { repoRoot = frozenRepoRoot, subjectSha } = {}) {
  validateConfiguration();
  const match = probeCheck(probe);
  if (!match) return 'NOT_VERIFIED';
  const context = inspectTargetRepository({ repoRoot, subjectSha, requireClean: subjectSha !== undefined });
  return (await evaluateProbe(context, match.control, match.check)).status;
}

async function installDetachedDependencies(detachedAppRoot) {
  const detachedNodeModules = path.join(detachedAppRoot, 'node_modules');
  if (fs.existsSync(detachedNodeModules)) fail('detached SUBJECT already contains node_modules before immutable dependency installation');
  const result = await commandResult('npm', ['ci', '--ignore-scripts', '--no-audit', '--no-fund', '--offline'], {
    cwd: detachedAppRoot,
    env: process.env,
    timeoutMs: 180_000,
  });
  if (result.exitCode !== 0 || result.signal || result.timedOut || result.error) {
    const output = `${result.stdout || ''}${result.stderr || ''}`.trim();
    fail(`immutable detached dependency installation failed: ${output.slice(-1200) || result.error?.message || `exit ${result.exitCode}`}`);
  }
  assertImmutableDependencies(detachedAppRoot);
}

function startDetachedSubjectWatchdog({ leasePath, tempRoot, detachedRoot, repoRoot }) {
  const payload = JSON.stringify({ leasePath, tempRoot, detachedRoot, repoRoot, parentPid: process.pid });
  const watchdog = spawn(process.execPath, ['-e', `
    const { execFileSync } = require('node:child_process');
    const fs = require('node:fs');
    const context = JSON.parse(process.argv[1]);
    const cleanup = () => {
      if (!fs.existsSync(context.leasePath)) return;
      try {
        if (fs.existsSync(context.detachedRoot)) {
          try {
            execFileSync('git', ['worktree', 'remove', '--force', context.detachedRoot], { cwd: context.repoRoot, stdio: 'ignore' });
          } catch {
            fs.rmSync(context.detachedRoot, { recursive: true, force: true });
            execFileSync('git', ['worktree', 'prune'], { cwd: context.repoRoot, stdio: 'ignore' });
          }
        } else {
          execFileSync('git', ['worktree', 'prune'], { cwd: context.repoRoot, stdio: 'ignore' });
        }
      } finally {
        fs.rmSync(context.tempRoot, { recursive: true, force: true });
      }
    };
    const timer = setInterval(() => {
      if (!fs.existsSync(context.leasePath)) process.exit(0);
      try {
        process.kill(context.parentPid, 0);
      } catch (error) {
        if (error.code === 'ESRCH') {
          clearInterval(timer);
          cleanup();
          process.exit(0);
        }
      }
    }, 100);
  `, payload], { detached: true, stdio: 'ignore' });
  watchdog.unref();
}

async function withDetachedSubject(context, callback) {
  const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), 'frontend-maintainability-subject-'));
  const detachedRoot = path.join(fs.realpathSync(tempRoot), 'repo');
  const leasePath = path.join(tempRoot, '.cleanup-lease');
  fs.writeFileSync(leasePath, `${process.pid}\n`);
  startDetachedSubjectWatchdog({ leasePath, tempRoot, detachedRoot, repoRoot: context.repoRoot });
  let worktreeRemoved = false;
  let tempRootRemoved = false;
  const cleanup = () => {
    fs.rmSync(leasePath, { force: true });
    if (!worktreeRemoved) {
      if (fs.existsSync(detachedRoot)) {
        try {
          execFileSync('git', ['worktree', 'remove', '--force', detachedRoot], { cwd: context.repoRoot, stdio: 'ignore' });
        }
        catch (removeError) {
          fs.rmSync(detachedRoot, { recursive: true, force: true });
          execFileSync('git', ['worktree', 'prune'], { cwd: context.repoRoot, stdio: 'ignore' });
          const registered = git(context.repoRoot, ['worktree', 'list', '--porcelain']);
          if (registered.includes(`worktree ${detachedRoot}`)) throw removeError;
        }
      }
      else {
        execFileSync('git', ['worktree', 'prune'], { cwd: context.repoRoot, stdio: 'ignore' });
      }
      worktreeRemoved = true;
    }
    if (!tempRootRemoved) {
      fs.rmSync(tempRoot, { recursive: true, force: true });
      tempRootRemoved = true;
    }
  };
  const terminate = (signal) => {
    try {
      terminateManagedCommands(signal);
      cleanup();
    }
    catch (error) {
      process.stderr.write(`detached subject cleanup failed after ${signal}: ${error.message}\n`);
      process.exit(1);
    }
    process.exit(signal === 'SIGINT' ? 130 : 143);
  };
  const onInterrupt = () => terminate('SIGINT');
  const onTerminate = () => terminate('SIGTERM');
  process.once('SIGINT', onInterrupt);
  process.once('SIGTERM', onTerminate);
  try {
    execFileSync('git', ['worktree', 'add', '--detach', detachedRoot, context.subjectSha], {
      cwd: context.repoRoot,
      stdio: 'ignore',
    });
    const executionContext = contextForExecution(context, fs.realpathSync(detachedRoot));
    const detachedNodeModules = path.join(executionContext.appRoot, 'node_modules');
    if (fs.existsSync(detachedNodeModules)) fail('detached SUBJECT unexpectedly contains node_modules');
    await installDetachedDependencies(executionContext.appRoot);
    return await callback(executionContext);
  }
  finally {
    process.off('SIGINT', onInterrupt);
    process.off('SIGTERM', onTerminate);
    cleanup();
  }
}

export function persistScoreReport(result, repoRoot) {
  const reportRoot = path.join(repoRoot, '.tmp', 'frontend-maintainability-score');
  fs.mkdirSync(reportRoot, { recursive: true });
  for (const [kind, artifact] of Object.entries(result.rawReports || {})) {
    const rawPath = path.join(reportRoot, `${result.subjectSha}.${kind}.raw.json`);
    const canonical = canonicalReportJSON(artifact.report);
    if (Buffer.byteLength(canonical) !== artifact.bytes
      || createHash('sha256').update(canonical).digest('hex') !== artifact.sha256) {
      fail(`${kind} normalized raw report bytes or SHA-256 changed before persistence`);
    }
    fs.writeFileSync(rawPath, canonical);
    artifact.persistedPath = path.relative(repoRoot, rawPath).split(path.sep).join('/');
  }
  const reportPath = path.join(reportRoot, `${result.subjectSha}.json`);
  fs.writeFileSync(reportPath, `${JSON.stringify(result, null, 2)}\n`);
  return reportPath;
}

function printScore(result, reportPath) {
  for (const control of result.controls) process.stdout.write(`${control.id}\t${control.status}\n`);
  process.stdout.write(`SCORE\t${result.displayScore.toFixed(1)}\t${result.subjectSha}\n`);
  process.stdout.write(`SUBJECT_TREE\t${result.subjectTree}\n`);
  process.stdout.write(`SCORE_BASE\t${result.scoreBaseSha}\n`);
  process.stdout.write(`SCORE_BASE_TREE\t${result.scoreBaseTree}\n`);
  process.stdout.write(`SUBJECT_TREE_RULE\t${result.subjectContract.treeRelation}\n`);
  if (reportPath) process.stdout.write(`REPORT\t${reportPath}\n`);
}

export function finalGateFailures(result) {
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
  if (!['--validate', '--probe', '--score', '--final', '--assert-frontend-plan-size'].includes(mode)) fail('unknown scorer mode');
  if (mode === '--validate' || mode === '--assert-frontend-plan-size') {
    if (args.length !== 1) fail(`${mode} does not accept additional arguments`);
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
  (async () => {
  const cli = parseCLI(process.argv.slice(2));
  if (cli.mode === '--validate') {
    validateConfiguration();
    process.stdout.write('frontend maintainability scorer configuration valid\n');
  }
  else if (cli.mode === '--assert-frontend-plan-size') {
    const result = assertFrontendPlanSize();
    process.stdout.write(`frontend plan size valid: lines=${result.lines} bytes=${result.bytes}\n`);
  }
  else if (cli.mode === '--probe') {
    process.stdout.write(`${await probeResult(cli.probe, { repoRoot: cli.repo, subjectSha: cli.subject })}\n`);
  }
  else if (cli.mode === '--score') {
    const result = await scoreCurrentTree({
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
    const result = await withDetachedSubject(targetContext, (executionContext) => (
      scoreContext(executionContext, { runCommands: true })
    ));
    const reportPath = persistScoreReport(result, targetContext.repoRoot);
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
  })().catch((error) => {
    process.stderr.write(`frontend maintainability scorer failed: ${error.message}\n`);
    process.exitCode = 1;
  });
}
