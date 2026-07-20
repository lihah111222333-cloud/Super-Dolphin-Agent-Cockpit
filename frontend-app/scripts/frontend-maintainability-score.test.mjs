import { execFileSync, spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import {
  copyFileSync,
  existsSync,
  mkdtempSync,
  mkdirSync,
  readFileSync,
  readdirSync,
  realpathSync,
  rmSync,
  writeFileSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { afterEach, describe, expect, it } from 'vitest';
import { DESKTOP_FAILURE_SOURCE_PATHS } from './desktop-failure-smoke.mjs';
import { runManagedCommand } from './managed-command.mjs';
import { DELIVERY_RUNNER_CONTENT_PATHS } from './delivery-smoke-runner.mjs';
import { FROZEN_T04_T05_EXECUTION_CLOSURE_PATHS } from './frontend-execution-closure.mjs';
import { productionActionFailureMatrixTitle } from '../src/shared/ui/productionActionFailureMatrixTitles.js';
import { HEAP_MEASUREMENT_CLOCK } from './resource-budget.mjs';
import {
  acceptsNonZeroRunnerExitForValidatedSlice,
  actionProducerGuardOutputStatus,
  commandEvidenceStatus,
  controlStatus,
  createSubjectContract,
  dependencyTreeIntegrity,
  executionControlIds,
  finalGateFailures,
  frontendPlanSizeStatus,
  inspectTargetRepository,
  persistScoreReport,
  performanceAuditPathAllowed,
  probeResult,
  scoreCurrentTree,
  sourceHasCriticalTypecheckGap,
  sourceHasPromptHistoryConsoleOnly,
  structuredEvidenceStatus,
  structuredRunnerExecutionKey,
  terminalTruthEvidenceStatus,
  validateConfiguration,
  waitForPerformanceLoadWindow,
  withFrozenDeliveryRunnerFiles,
} from './frontend-maintainability-score.mjs';

const scriptRoot = dirname(fileURLToPath(import.meta.url));
const frozenRepoRoot = resolve(scriptRoot, '..', '..');
const scorerPath = join(scriptRoot, 'frontend-maintainability-score.mjs');
const plannedBaseSha = 'b40867229af8e17916c00393639ccb0fcb4bf6fc';
const T05_DELIVERY_EVIDENCE_TEST_TIMEOUT_MS = 30_000;
const EXECUTABLE_PROBE_TEST_TIMEOUT_MS = 90_000;
const FINAL_SCORER_COMMAND_TIMEOUT_MS = 180_000;
const FINAL_SCORER_TEST_TIMEOUT_MS = FINAL_SCORER_COMMAND_TIMEOUT_MS + 30_000;
const ACTION_PRODUCER_PROBE_TIMEOUT_MS = 180_000;
const ACTION_PRODUCER_PROBE_TEST_TIMEOUT_MS = ACTION_PRODUCER_PROBE_TIMEOUT_MS + 30_000;
const STRUCTURAL_SCORE_TEST_TIMEOUT_MS = 210_000;
const temporaryRepositories = [];
const addedGovernancePaths = [
  'frontend-app/scripts/failure-matrix-runner.mjs',
  'frontend-app/scripts/failure-matrix-cases.json',
  'frontend-app/scripts/failure-matrix-fixtures.json',
  'frontend-app/scripts/failure-matrix-mutations.json',
  'frontend-app/scripts/action-producer-guard.mjs',
  'frontend-app/scripts/action-producer-guard.selftest.mjs',
  'frontend-app/scripts/action-production-runtime-runner.mjs',
  'frontend-app/config/action-producer-registry.json',
  'frontend-app/config/action-producer-test-matrix.json',
  'frontend-app/src/shared/ui/productionActionFailureMatrixTitles.js',
  'frontend-app/src/entities/client/model/failureMatrix.test.js',
  'frontend-app/src/entities/client/model/runtimeSlice.test.js',
  'frontend-app/src/pages/chat/composer/ComposerDock.actionFailure.test.jsx',
  'frontend-app/src/pages/chat/thread/ThreadRail.test.jsx',
  'frontend-app/src/pages/settings/SettingsPage.test.jsx',
  'frontend-app/src/features/approval/ui/ApprovalDecisionShelf.test.jsx',
  'internal/provider/codexapp/failure_matrix_test.go',
  'internal/provider/claudecli/failure_matrix_test.go',
  'internal/module/turn/interrupt_rpc_test.go',
  'frontend-app/scripts/desktop-failure-smoke.mjs',
  'frontend-app/tests/e2e/desktop-failure.spec.js',
  'frontend-app/playwright.failure.config.js',
  'frontend-app/package.json',
  'frontend-app/src/shared/api/wailsBridge.js',
  'internal/ui/wails/testdata/failure_smoke_host/main.go',
  'internal/provider/claudecli/event_map.go',
  'internal/provider/codexapp/event_map.go',
  'internal/provider/unified/event_map.go',
  'internal/ui/wails/bridge.go',
];

function copyWorkspaceFile(relativePath, repoRoot) {
  const target = join(repoRoot, relativePath);
  mkdirSync(dirname(target), { recursive: true });
  copyFileSync(join(frozenRepoRoot, relativePath), target);
}

function documents() {
  return {
    controls: withFrozenDeliveryRunnerFiles(JSON.parse(readFileSync(join(scriptRoot, 'frontend-maintainability-controls.json'), 'utf8'))),
    fixtures: JSON.parse(readFileSync(join(scriptRoot, 'frontend-maintainability-red-fixtures.json'), 'utf8')),
  };
}

function git(repoRoot, args) {
  return execFileSync('git', args, { cwd: repoRoot, encoding: 'utf8' }).trim();
}

function commitTree(repoRoot, message) {
  const parent = git(repoRoot, ['rev-parse', 'HEAD']);
  const tree = git(repoRoot, ['write-tree']);
  const commit = execFileSync('git', ['commit-tree', tree, '-p', parent, '-m', message], {
    cwd: repoRoot,
    encoding: 'utf8',
  }).trim();
  git(repoRoot, ['update-ref', 'HEAD', commit, parent]);
  git(repoRoot, ['reset', '--mixed', commit]);
  return commit;
}

function cloneSparseRepository(repoRoot, revision, paths) {
  execFileSync('git', ['clone', '-q', '--shared', '--no-checkout', frozenRepoRoot, repoRoot]);
  git(repoRoot, ['sparse-checkout', 'init', '--no-cone']);
  git(repoRoot, ['sparse-checkout', 'set', '--no-cone', ...paths]);
  git(repoRoot, ['checkout', '-q', '--detach', revision]);
}

function createSubjectFrontendSnapshot(context) {
  const repoRoot = mkdtempSync(join(tmpdir(), 'frontend-maintainability-action-subject-'));
  temporaryRepositories.push(repoRoot);
  cloneSparseRepository(repoRoot, context.subjectSha, ['frontend-app']);
  execFileSync('npm', ['ci', '--ignore-scripts', '--no-audit', '--no-fund', '--offline'], {
    cwd: join(repoRoot, 'frontend-app'),
    stdio: 'ignore',
    timeout: 180_000,
  });
  return repoRoot;
}

function cli(args, cwd = frozenRepoRoot) {
  return spawnSync(process.execPath, [scorerPath, ...args], { cwd, encoding: 'utf8' });
}

function detachedTmpAlias() {
  const canonical = realpathSync(tmpdir());
  return canonical === '/private/var' || canonical.startsWith('/private/var/')
    ? canonical.replace(/^\/private\/var/u, '/var')
    : tmpdir();
}

it('normalizes empty immutable dependency directories while retaining file integrity', () => {
  const appRoot = mkdtempSync(join(tmpdir(), 'frontend-maintainability-dependency-tree-'));
  temporaryRepositories.push(appRoot);
  const nodeModulesRoot = join(appRoot, 'node_modules');
  const toolPath = join(nodeModulesRoot, 'fixture', 'tool.js');
  mkdirSync(dirname(toolPath), { recursive: true });
  writeFileSync(toolPath, 'export const version = 1;\n');

  const original = dependencyTreeIntegrity(appRoot);
  mkdirSync(join(nodeModulesRoot, '@empty-scope'));

  expect(dependencyTreeIntegrity(appRoot)).toEqual(original);

  writeFileSync(toolPath, 'export const version = 2;\n');
  expect(dependencyTreeIntegrity(appRoot)).not.toEqual(original);
});

function write(relativePath, content, repoRoot) {
  const target = join(repoRoot, relativePath);
  mkdirSync(dirname(target), { recursive: true });
  writeFileSync(target, content);
}

function createTargetRepository() {
  const repoRoot = mkdtempSync(join(tmpdir(), 'frontend-maintainability-target-'));
  temporaryRepositories.push(repoRoot);
  git(repoRoot, ['init', '-q']);
  git(repoRoot, ['config', 'user.email', 'scorer-test@example.invalid']);
  git(repoRoot, ['config', 'user.name', 'Scorer Test']);
  write('README.md', 'target repository\n', repoRoot);
  write('frontend-app/package.json', '{"name":"target","private":true}\n', repoRoot);
  write('frontend-app/package-lock.json', '{"name":"target","lockfileVersion":3}\n', repoRoot);
  git(repoRoot, ['add', '.']);
  git(repoRoot, ['commit', '-q', '-m', '测试：建立评分目标']);
  return {
    repoRoot,
    subjectSha: git(repoRoot, ['rev-parse', 'HEAD']),
    subjectTree: git(repoRoot, ['rev-parse', 'HEAD^{tree}']),
  };
}

function createDeliveryEvidenceContext() {
  const repoRoot = mkdtempSync(join(tmpdir(), 'frontend-maintainability-delivery-evidence-'));
  temporaryRepositories.push(repoRoot);
  git(repoRoot, ['init', '-q']);
  git(repoRoot, ['config', 'user.email', 'scorer-test@example.invalid']);
  git(repoRoot, ['config', 'user.name', 'Scorer Test']);
  for (const relativePath of FROZEN_T04_T05_EXECUTION_CLOSURE_PATHS) copyWorkspaceFile(relativePath, repoRoot);
  git(repoRoot, ['add', '.']);
  git(repoRoot, ['commit', '-q', '-m', '测试：冻结交付证据闭包']);
  const scoreBaseSha = git(repoRoot, ['rev-parse', 'HEAD']);
  return {
    repoRoot,
    scoreBaseSha,
    subjectSha: scoreBaseSha,
    subjectTree: git(repoRoot, ['rev-parse', 'HEAD^{tree}']),
  };
}

function createPerformanceTarget(runnerFiles) {
  const repoRoot = mkdtempSync(join(tmpdir(), 'frontend-maintainability-performance-'));
  rmSync(repoRoot, { recursive: true, force: true });
  temporaryRepositories.push(repoRoot);
  cloneSparseRepository(repoRoot, plannedBaseSha, [
    ...runnerFiles,
    'docs/doc/codemap/README.md',
    'docs/doc/codemap/ai-index.json',
    'frontend-app/candidate-product.js',
    'frontend-app/scripts/evidence-provenance.test.mjs',
  ]);
  git(repoRoot, ['config', 'user.email', 'scorer-test@example.invalid']);
  git(repoRoot, ['config', 'user.name', 'Scorer Test']);
  const baseTree = git(repoRoot, ['rev-parse', 'HEAD^{tree}']);
  runnerFiles.forEach((runnerPath, index) => write(runnerPath, `runner fixture ${index}\n`, repoRoot));
  write('docs/doc/codemap/README.md', 'generated codemap fixture\n', repoRoot);
  write('docs/doc/codemap/ai-index.json', '{"generated":true}\n', repoRoot);
  write('frontend-app/scripts/evidence-provenance.test.mjs', 'export const provenanceTestFixture = true;\n', repoRoot);
  git(repoRoot, ['add', '.']);
  git(repoRoot, ['commit', '-q', '-m', '测试：加入性能评分器闭包']);
  const runnerSha = git(repoRoot, ['rev-parse', 'HEAD']);
  const runnerTree = git(repoRoot, ['rev-parse', 'HEAD^{tree}']);
  write('frontend-app/candidate-product.js', 'export const candidate = true;\n', repoRoot);
  git(repoRoot, ['add', '.']);
  git(repoRoot, ['commit', '-q', '-m', '测试：建立独立候选提交']);
  return {
    ...inspectTargetRepository({
      repoRoot,
      subjectSha: git(repoRoot, ['rev-parse', 'HEAD']),
    }),
    baseSha: plannedBaseSha,
    baseTree,
    runnerSha,
    runnerTree,
  };
}

function createFinalContractTarget() {
  const repoRoot = mkdtempSync(join(tmpdir(), 'frontend-maintainability-final-target-'));
  rmSync(repoRoot, { recursive: true, force: true });
  temporaryRepositories.push(repoRoot);
  cloneSparseRepository(repoRoot, git(frozenRepoRoot, ['rev-parse', 'HEAD']), [
    'frontend-app/scripts/frontend-maintainability-controls.json',
    'frontend-app/scripts/frontend-maintainability-score.mjs',
    'frontend-app/scripts/frontend-execution-closure.mjs',
    'frontend-app/scripts/frontend-maintainability-baseline.json',
    'frontend-app/scripts/frontend-maintainability-dependencies.json',
    'frontend-app/scripts/frontend-maintainability-red-fixtures.json',
    'frontend-app/scripts/delivery-smoke-runner.mjs',
    'frontend-app/scripts/managed-command.mjs',
    'frontend-app/scripts/evidence-provenance.mjs',
    'frontend-app/scripts/performance-baseline-provenance.mjs',
    'frontend-app/scripts/performance-budget-model.mjs',
    'frontend-app/scripts/resource-budget.mjs',
    'frontend-app/scorer-final-subject.txt',
    ...addedGovernancePaths,
  ]);
  git(repoRoot, ['config', 'user.email', 'scorer-test@example.invalid']);
  git(repoRoot, ['config', 'user.name', 'Scorer Test']);
  for (const name of [
    'frontend-maintainability-controls.json',
    'frontend-maintainability-score.mjs',
    'frontend-maintainability-baseline.json',
    'frontend-maintainability-dependencies.json',
    'frontend-maintainability-red-fixtures.json',
    'delivery-smoke-runner.mjs',
    'managed-command.mjs',
    'evidence-provenance.mjs',
    'performance-baseline-provenance.mjs',
    'performance-budget-model.mjs',
    'resource-budget.mjs',
  ]) {
    copyFileSync(join(scriptRoot, name), join(repoRoot, 'frontend-app', 'scripts', name));
  }
  for (const relativePath of DELIVERY_RUNNER_CONTENT_PATHS) copyWorkspaceFile(relativePath, repoRoot);
  for (const relativePath of addedGovernancePaths) copyWorkspaceFile(relativePath, repoRoot);
  write('frontend-app/scorer-final-subject.txt', 'strict descendant\n', repoRoot);
  git(repoRoot, ['add', '--sparse', '--all']);
  git(repoRoot, ['commit', '-q', '-m', '测试：建立最终评分后代']);
  return {
    repoRoot,
    subjectSha: git(repoRoot, ['rev-parse', 'HEAD']),
  };
}

function createFinalCliFixture({ hangFirstProbe = false } = {}) {
  const baseRoot = mkdtempSync(join(tmpdir(), 'frontend-maintainability-final-cli-base-'));
  temporaryRepositories.push(baseRoot);
  execFileSync('git', ['init', '-q'], { cwd: baseRoot });
  git(baseRoot, ['config', 'user.email', 'scorer-test@example.invalid']);
  git(baseRoot, ['config', 'user.name', 'Scorer Test']);
  const commonDirectory = resolve(frozenRepoRoot, git(frozenRepoRoot, ['rev-parse', '--git-common-dir']));
  const alternateObjects = realpathSync(join(commonDirectory, 'objects'));
  writeFileSync(join(baseRoot, '.git', 'objects', 'info', 'alternates'), `${alternateObjects}\n`);
  git(baseRoot, ['update-ref', 'refs/heads/main', git(frozenRepoRoot, ['rev-parse', 'HEAD'])]);
  git(baseRoot, ['symbolic-ref', 'HEAD', 'refs/heads/main']);
  mkdirSync(join(baseRoot, 'frontend-app', 'scripts'), { recursive: true });
  for (const name of [
    'frontend-maintainability-controls.json',
    'frontend-maintainability-score.mjs',
    'frontend-maintainability-baseline.json',
    'frontend-maintainability-dependencies.json',
    'frontend-maintainability-red-fixtures.json',
    'delivery-smoke-runner.mjs',
    'managed-command.mjs',
    'evidence-provenance.mjs',
    'performance-baseline-provenance.mjs',
    'performance-budget-model.mjs',
    'resource-budget.mjs',
  ]) {
    copyFileSync(join(scriptRoot, name), join(baseRoot, 'frontend-app', 'scripts', name));
  }
  for (const relativePath of DELIVERY_RUNNER_CONTENT_PATHS) copyWorkspaceFile(relativePath, baseRoot);
  for (const relativePath of addedGovernancePaths) copyWorkspaceFile(relativePath, baseRoot);
  for (const name of ['.gitignore', 'package.json', 'package-lock.json', 'vite.config.js']) {
    copyFileSync(join(frozenRepoRoot, 'frontend-app', name), join(baseRoot, 'frontend-app', name));
  }
  mkdirSync(join(baseRoot, 'frontend-app', 'src'), { recursive: true });
  copyFileSync(
    join(frozenRepoRoot, 'frontend-app', 'src', 'test-setup.js'),
    join(baseRoot, 'frontend-app', 'src', 'test-setup.js'),
  );
  write('frontend-app/scripts/delivery-smoke-runner.mjs', [
    "import { execFile } from 'node:child_process';",
    "import { lstatSync, writeFileSync } from 'node:fs';",
    "import process from 'node:process';",
    "import { resolve } from 'node:path';",
    "import { promisify } from 'node:util';",
    '',
    'const run = promisify(execFile);',
    'const repoIndex = process.argv.indexOf(\'--repo\');',
    "if (repoIndex < 0 || !process.argv[repoIndex + 1]) throw new Error('--repo is required');",
    'const REPO_ROOT = resolve(process.argv[repoIndex + 1]);',
    "const FRONTEND_ROOT = resolve(REPO_ROOT, 'frontend-app');",
    'const proofPath = process.env.SCORER_DETACHED_MOUNT_PROOF;',
    "if (!proofPath) throw new Error('SCORER_DETACHED_MOUNT_PROOF is required');",
    "const gitStatus = await run('git', ['status', '--porcelain', '--untracked-files=all'], { cwd: REPO_ROOT, encoding: 'utf8', timeout: 10_000 });",
    'const vitest = await run(process.execPath, [',
    "  resolve(FRONTEND_ROOT, 'node_modules', 'vitest', 'vitest.mjs'),",
    "  'run', 'scripts/detached-mount-probe.test.mjs', '--reporter=json', '--no-file-parallelism', '--maxWorkers=1',",
    "], { cwd: FRONTEND_ROOT, encoding: 'utf8', timeout: 30_000 });",
    "const nodeModules = lstatSync(resolve(FRONTEND_ROOT, 'node_modules'));",
    'const proof = {',
    '  gitStatusExitCode: 0,',
    '  gitStatus: gitStatus.stdout,',
    '  nodeModulesIsDirectory: nodeModules.isDirectory(),',
    '  nodeModulesIsSymbolicLink: nodeModules.isSymbolicLink(),',
    '  vitestExitCode: 0,',
    '  vitestStderr: vitest.stderr,',
    '  vitestStdout: vitest.stdout,',
    '};',
    'writeFileSync(proofPath, JSON.stringify(proof));',
    'const vitestReport = JSON.parse(vitest.stdout);',
    'writeFileSync(proofPath, JSON.stringify({ ...proof, vitestPassedTests: vitestReport.numPassedTests }));',
    "process.stdout.write('{}\\n');",
    '',
  ].join('\n'), baseRoot);
  write(
    'frontend-app/scripts/desktop-failure-smoke.mjs',
    `export const DESKTOP_FAILURE_SOURCE_PATHS = Object.freeze(${JSON.stringify(DESKTOP_FAILURE_SOURCE_PATHS)});\nif (process.argv[1]?.endsWith('/desktop-failure-smoke.mjs')) process.exitCode = 1;\n`,
    baseRoot,
  );
  for (const relativePath of [
    'frontend-app/scripts/failure-matrix-runner.mjs',
    'frontend-app/scripts/action-production-runtime-runner.mjs',
    'frontend-app/scripts/action-producer-guard.selftest.mjs',
    'frontend-app/scripts/action-producer-guard.mjs',
  ]) {
    write(relativePath, 'process.exitCode = 1;\n', baseRoot);
  }
  write('frontend-app/scorer-freeze-marker.txt', 'frozen scorer fixture\n', baseRoot);
  if (hangFirstProbe) {
    const packageDocument = JSON.parse(readFileSync(join(baseRoot, 'frontend-app', 'package.json'), 'utf8'));
    packageDocument.scripts.lint = `${process.execPath} -e "setInterval(() => {}, 1000)"`;
    write('frontend-app/package.json', `${JSON.stringify(packageDocument, null, 2)}\n`, baseRoot);
  }
  git(baseRoot, ['add', '-A']);
  git(baseRoot, ['commit', '-q', '-m', '测试：冻结最终评分器']);
  const scoreBaseSha = git(baseRoot, ['rev-parse', 'HEAD']);

  const subjectRoot = mkdtempSync(join(tmpdir(), 'frontend-maintainability-final-cli-subject-'));
  rmSync(subjectRoot, { recursive: true, force: true });
  execFileSync('git', ['worktree', 'add', '-q', '--detach', subjectRoot, scoreBaseSha], { cwd: baseRoot });
  write('frontend-app/final-subject.txt', 'strict descendant\n', subjectRoot);
  write('go.mod', 'invalid final fixture\n', subjectRoot);
  const probeSources = [
    'frontend-app/src/entities/client/model/useClientStore.test.js',
    'frontend-app/src/shared/diagnostics/frontendHealthStore.test.js',
    'frontend-app/src/shared/ui/productionActionFailureMatrix.test.js',
    'frontend-app/src/shared/contracts/turnContractValidators.test.js',
    'frontend-app/scripts/frontend-state-ownership-guard.test.mjs',
    'frontend-app/scripts/frontend-dependency-direction-guard.test.mjs',
    'frontend-app/src/shared/ui/runUIAction.test.js',
  ];
  for (const relativePath of probeSources) rmSync(join(subjectRoot, relativePath), { force: true });
  for (const relativePath of [
    'frontend-app/scripts/performance-budget-runner.mjs',
  ]) {
    write(relativePath, 'process.exitCode = 1;\n', subjectRoot);
  }
  write(
    'frontend-app/scripts/detached-mount-probe.test.mjs',
    "import { expect, it } from 'vitest';\nit('executes through detached dependencies', () => expect(true).toBe(true));\n",
    subjectRoot,
  );
  git(subjectRoot, ['add', '.']);
  git(subjectRoot, ['commit', '-q', '-m', '测试：建立最终评分目标']);
  return { baseRoot, scoreBaseSha, subjectRoot, subjectSha: git(subjectRoot, ['rev-parse', 'HEAD']) };
}

function failureMatrixEvidence(context, overrides = {}) {
  const layersByCase = {
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
  };
  const frontendFiles = {
    'FM-18': 'src/entities/client/model/runtimeSlice.test.js',
    'FM-21': 'src/pages/chat/composer/ComposerDock.actionFailure.test.jsx',
    'FM-22': 'src/pages/chat/composer/ComposerDock.actionFailure.test.jsx',
    'FM-23': 'src/pages/settings/SettingsPage.test.jsx',
    'FM-24': 'src/features/approval/ui/ApprovalDecisionShelf.test.jsx',
  };
  const evidence = Object.entries(layersByCase).flatMap(([caseId, layers]) => (
    layers.map((layer) => ({
      caseId,
      layer,
      test: `${caseId}:${layer}`,
      ...(layer === 'frontend' ? {
        file: frontendFiles[caseId] || 'src/entities/client/model/failureMatrix.test.js',
      } : {}),
    }))
  ));
  const mutationDocument = JSON.parse(readFileSync(join(scriptRoot, 'failure-matrix-mutations.json'), 'utf8'));
  const mutationByCase = new Map(mutationDocument.mutations.flatMap((mutation) => (
    mutation.caseIds.map((caseId) => [caseId, mutation])
  )));
  const goPackages = {
    'go-codex': './internal/provider/codexapp',
    'go-claude': './internal/provider/claudecli',
    'go-turn': './internal/module/turn',
    'go-wails': './internal/ui/wails',
  };
  const redGreenCases = Object.keys(layersByCase).map((caseId) => {
    const mutation = mutationByCase.get(caseId);
    const greenEvidence = evidence.find((entry) => entry.caseId === caseId && entry.layer === mutation.layer);
    const source = execFileSync('git', ['show', `${context.subjectSha}:${mutation.sourcePath}`], {
      cwd: context.repoRoot,
      encoding: 'utf8',
    });
    const mutated = source.replace(mutation.search, mutation.replacement);
    const command = greenEvidence.layer === 'frontend'
      ? {
        cwd: 'frontend-app',
        argv: [
          join('node_modules', '.bin', 'vitest'), 'run', greenEvidence.file,
          '-t', greenEvidence.test, '--no-file-parallelism', '--maxWorkers=1',
        ],
      }
      : {
        cwd: '.',
        argv: ['go', 'test', goPackages[greenEvidence.layer], '-run', `^${greenEvidence.test}$`, '-count=1'],
      };
    return {
      caseId,
      subjectSha: context.subjectSha,
      subjectTreeSha: context.subjectTree,
      green: {
        ...greenEvidence,
        ...command,
        exitCode: 0,
        signal: null,
        outputSha256: createHash('sha256').update(`GREEN:${caseId}`).digest('hex'),
      },
      red: {
        mutationId: mutation.id,
        sourcePath: mutation.sourcePath,
        sourceSha256: createHash('sha256').update(source).digest('hex'),
        mutatedSha256: createHash('sha256').update(mutated).digest('hex'),
        ...command,
        exitCode: 1,
        signal: null,
        outputSha256: createHash('sha256').update(`RED:${caseId}`).digest('hex'),
      },
    };
  });
  return {
    schemaVersion: 1,
    generatedAt: new Date().toISOString(),
    subjectSha: context.subjectSha,
    subjectTreeSha: context.subjectTree,
    controlIds: [
      'E06-failure-matrix',
      'C05-provider-rpc-parity',
      'T01-red-green-regression',
      'T03-wails-integration',
    ],
    caseIds: Object.keys(layersByCase),
    caseCount: Object.keys(layersByCase).length,
    testCount: evidence.length,
    status: 'covered',
    blockedCases: [],
    evidence,
    redGreenCases,
    ...overrides,
  };
}

function desktopFailureEvidence(context, overrides = {}) {
  const command = ['node', 'scripts/desktop-failure-smoke.mjs'];
  const sourceHashes = Object.fromEntries(DESKTOP_FAILURE_SOURCE_PATHS.map((sourcePath) => [
    sourcePath,
    createHash('sha256').update(execFileSync('git', ['show', `${context.subjectSha}:${sourcePath}`], {
      cwd: context.repoRoot,
    })).digest('hex'),
  ]));
  const caseEvidence = (caseId, hops, domAssertions) => ({
    caseId,
    result: 'GREEN',
    command,
    hops,
    domAssertions,
    secretAssertions: ['dom-does-not-contain-raw-provider-secret', 'report-does-not-contain-raw-provider-secret'],
    execution: { status: 'passed', durationMs: 1 },
  });
  return {
    schemaVersion: 2,
    generatedAt: new Date().toISOString(),
    subjectSha: context.subjectSha,
    subjectTreeSha: context.subjectTree,
    controlId: 'T03-wails-integration',
    caseIds: ['terminal-failed', 'prompt-history-reject'],
    testCount: 2,
    status: 'covered',
    blockedCases: [],
    sourceHashes,
    execution: {
      goBuild: { argv: ['go', 'build'], cwd: '.', exitCode: 0, signal: null, outputSha256: 'a'.repeat(64) },
      playwright: { argv: ['playwright', 'test'], cwd: 'frontend-app', exitCode: 0, signal: null, outputSha256: 'b'.repeat(64), testCount: 2 },
      wailsHost: { argv: ['failure-smoke-host'], cwd: '.', exitCode: null, signal: 'SIGTERM', outputSha256: 'c'.repeat(64) },
      vite: { argv: ['npm', 'run', 'dev'], cwd: 'frontend-app', exitCode: null, signal: 'SIGTERM', outputSha256: 'd'.repeat(64) },
    },
    cases: [
      caseEvidence('terminal-failed', ['claudecli.raw', 'claudecli.adapter', 'turndto.TurnOutputDelta', 'wails.EventBridge', 'chromium.DOM', 'codexapp.raw', 'codexapp.adapter', 'turndto.TurnCompleted', 'turn/terminal', 'chromium.DOM'], ['partial-response-visible', 'safe-terminal-visible', 'raw-secret-absent']),
      caseEvidence('prompt-history-reject', ['wails.rpc', 'thread/promptHistory', 'frontend.action', 'chromium.DOM', 'retry.control', 'wails.rpc', 'chromium.DOM'], ['draft-preserved', 'cursor-preserved', 'retry-click-recovers']),
    ],
    ...overrides,
  };
}

function performanceEvidence(context, check, metricStatus = 'PASS', overrides = {}) {
  const casesByMetric = {
    'P01-render-isolation': ['render-main-page-update-commits', 'render-unrelated-subtree-update-commits', 'render-broad-subscription-mutation-detected'],
    'P02-history-budget': ['turns-200-tools-1', 'turns-200-tools-3', 'turns-1000-tools-1', 'turns-1000-tools-3', 'turns-5000-tools-1', 'turns-5000-tools-3'],
    'P03-feedback-budget': ['stop-visible-feedback'],
    'P04-resource-budget': ['bundle-total-bytes', 'bundle-max-chunk-bytes', 'heap-used-median-bytes'],
  };
  const caseResults = Object.entries(casesByMetric).flatMap(([metricId, caseIds]) => (
    caseIds.map((caseId) => ({
      caseId,
      metricId,
      evidenceKey: caseId,
      status: metricId === check.metricId ? metricStatus : 'PASS',
    }))
  ));
  const baseSha = context.baseSha;
  const baseTree = context.baseTree;
  const environment = {
    os: { platform: 'darwin', release: 'test-release', arch: 'arm64' },
    cpu: { model: 'test-cpu', logicalCores: 8 },
    totalMemoryBytes: 16_000_000_000,
    loadAverage: [0.1, 0.2, 0.3],
    node: 'v25.6.1',
    npm: '11.8.0',
    go: 'go version go1.25.0 darwin/arm64',
  };
  const timingCase = () => ({
    attemptsPerSample: 1,
    durationClock: 'test-clock',
    iterationCount: 1,
    durationAttemptSamplesMs: Array.from({ length: 5 }, () => [10]),
    durationSamplesMs: [10, 10, 10, 10, 10],
    durationMedianMs: 10,
  });
  const timingMetric = (metricId, subjectSha, caseIds, frozen = false) => ({
    ...(frozen ? { status: 'PASS', maxRegressionRatio: 1.15 } : {}),
    metricId,
    subjectSha,
    warmupCount: 1,
    sampleCount: 5,
    cases: Object.fromEntries(caseIds.map((caseId) => [caseId, timingCase()])),
  });
  const pairedSample = (sampleIndex, normalizedRatio = 1.2) => ({
    blockOrders: Array.from({ length: 3 }, (_, blockIndex) => (
      (sampleIndex + blockIndex) % 2 === 0 ? 'production-reference' : 'reference-production'
    )),
    productionBlockCpuDurationsMs: Array.from({ length: 3 }, () => normalizedRatio * 10),
    referenceBlockCpuDurationsMs: Array.from({ length: 3 }, () => 10),
    rawNormalizedBlockRatios: Array.from({ length: 3 }, () => normalizedRatio),
    normalizedRatio,
  });
  const pairedTimingCase = (normalizedRatio = 1.2) => ({
    attemptsPerSample: 1,
    durationClock: 'p50(production/reference process.cpuUsage(user+system),alternating,500000-iteration-blocks)',
    blockCount: 3,
    blockIterationCount: 10,
    iterationCount: 30,
    materializedCount: 80,
    referenceMaterializedCount: 80,
    sampleDiagnostics: Array.from({ length: 5 }, (_, sampleIndex) => pairedSample(sampleIndex, normalizedRatio)),
    normalizedRatioSamples: Array.from({ length: 5 }, () => normalizedRatio),
    normalizedRatioMedian: normalizedRatio,
  });
  const pairedTimingMetric = (subjectSha, caseIds, frozen = false) => ({
    ...(frozen ? { status: 'PASS', maxRegressionRatio: 1.15 } : {}),
    metricId: 'P02-history-budget',
    subjectSha,
    warmupCount: 1,
    sampleCount: 5,
    cases: Object.fromEntries(caseIds.map((caseId) => [caseId, pairedTimingCase()])),
  });
  const baseDistManifest = [{ path: 'assets/index.js', bytes: 100, sha256: 'e'.repeat(64) }];
  const baseDistManifestHash = createHash('sha256').update(
    baseDistManifest.map(({ path: filePath, bytes, sha256 }) => `${filePath}\0${bytes}\0${sha256}\n`).join(''),
  ).digest('hex');
  const metricsFor = (subjectSha, frozen = false) => ({
    'P01-render-isolation': {
      ...(frozen ? { status: 'PASS', absoluteUpdateLimit: 1 } : {}),
      metricId: 'P01-render-isolation',
      subjectSha,
      warmupUpdates: 2,
      updateCount: 20,
      updateAction: 'useClientStore.getState().setLogLevel',
      mainPageUpdateCommits: frozen ? 40 : 1,
      unrelatedSubtreeUpdateCommits: 0,
      mutationUpdateCommits: 20,
      mutationDetected: true,
      productionBoundary: 'src/App.jsx#App',
      productionStoreSubscriptions: [{ source: 'src/App.jsx', line: 1, column: 1 }],
    },
    'P02-history-budget': pairedTimingMetric(subjectSha, casesByMetric['P02-history-budget'], frozen),
    'P03-feedback-budget': timingMetric('P03-feedback-budget', subjectSha, casesByMetric['P03-feedback-budget'], frozen),
    'P04-resource-budget': {
      ...(frozen ? {
        status: 'PASS',
        maxRegressionRatio: 1.05,
        baseBuild: {
          baseSha,
          baseTree,
          installArgv: ['npm', 'ci'],
          buildArgv: ['npm', 'run', 'build'],
          distManifest: baseDistManifest,
          distManifestHash: baseDistManifestHash,
        },
      } : {}),
      ...(!frozen ? {
        candidateBuild: {
          subjectSha,
          subjectTree: context.subjectTree,
          installArgv: ['npm', 'ci'],
          buildArgv: ['npm', 'run', 'build'],
          distManifest: baseDistManifest,
          distManifestHash: baseDistManifestHash,
        },
      } : {}),
      metricId: 'P04-resource-budget',
      subjectSha,
      fileCount: 1,
      totalBundleBytes: 100,
      maxChunkBytes: 100,
      files: [{ path: 'assets/index.js', bytes: 100 }],
      heapMeasurementClock: HEAP_MEASUREMENT_CLOCK,
      heapSampleCount: 5,
      heapWarmupCount: 1,
      heapUsedSamplesBytes: [100, 100, 100, 100, 100],
      heapUsedMedianBytes: 100,
      heapEnvironment: {
        node: 'v25.6.1',
        v8: '14.1.0',
        platform: 'darwin',
        arch: 'arm64',
      },
    },
  });
  const measurementMetricsFor = () => {
    const metrics = metricsFor(baseSha, true);
    delete metrics['P01-render-isolation'].status;
    delete metrics['P01-render-isolation'].absoluteUpdateLimit;
    for (const metricId of ['P02-history-budget', 'P03-feedback-budget', 'P04-resource-budget']) {
      delete metrics[metricId].status;
      delete metrics[metricId].maxRegressionRatio;
    }
    return metrics;
  };
  const runnerFiles = check.runnerFiles.map((runnerPath) => ({
    path: runnerPath,
    sha256: createHash('sha256').update(readFileSync(join(context.repoRoot, runnerPath))).digest('hex'),
  }));
  const runnerContentHash = createHash('sha256');
  runnerFiles.forEach(({ path: runnerPath, sha256 }) => runnerContentHash.update(`${runnerPath}\0${sha256}\n`));
  const sharedProvenance = {
    runnerId: 'frontend-performance-budget',
    runnerSha: context.runnerSha,
    runnerTree: context.runnerTree,
    runnerContentHash: runnerContentHash.digest('hex'),
    runnerFiles,
    worktreeClean: true,
    worktreeStatus: [],
  };
  const candidateProvenance = { ...sharedProvenance, baselineAudit: null };
  const baselineProvenance = {
    ...sharedProvenance,
    baselineAudit: {
      baseSha,
      baseTree,
      changedPaths: git(context.repoRoot, ['diff', '--name-only', baseSha, context.runnerSha]).split('\n'),
    },
  };
  const baselineGeneratedAt = new Date().toISOString();
  const measurementBindings = {
    subjectSha: baseSha,
    subjectTree: baseTree,
    environment: {
      os: environment.os,
      cpu: environment.cpu,
      totalMemoryBytes: environment.totalMemoryBytes,
      node: environment.node,
      npm: environment.npm,
      go: environment.go,
    },
    runnerSha: baselineProvenance.runnerSha,
    runnerTree: baselineProvenance.runnerTree,
    runnerContentHash: baselineProvenance.runnerContentHash,
    changedPaths: baselineProvenance.baselineAudit.changedPaths,
  };
  const report = {
    schemaVersion: 1,
    evidence: {
      schemaVersion: 1,
      subjectSha: context.subjectSha,
      subjectTree: context.subjectTree,
      generatedAt: new Date().toISOString(),
      environment,
      provenance: candidateProvenance,
      metrics: metricsFor(context.subjectSha),
    },
    verdict: {
      status: metricStatus,
      testCount: caseResults.length,
      caseIds: caseResults.map(({ caseId }) => caseId),
      caseResults,
      verdicts: Object.keys(casesByMetric).map((metricId) => ({
        metricId,
        status: metricId === check.metricId ? metricStatus : 'PASS',
        reason: '',
      })),
    },
    ...overrides,
  };
  return {
    report,
    baselineDocument: {
      schemaVersion: 1,
      baseSha,
      planSnapshotSha: 'a'.repeat(40),
      subjectSha: baseSha,
      subjectTree: baseTree,
      generatedAt: baselineGeneratedAt,
      environment,
      provenance: baselineProvenance,
      measurementAudit: {
        runCount: 3,
        designatedRun: 1,
        reproducibilityRuns: [2, 3].map((run) => ({
          run,
          generatedAt: new Date(Date.parse(baselineGeneratedAt) + ((run - 1) * 1_000)).toISOString(),
          runnerContentHash: baselineProvenance.runnerContentHash,
          bindings: structuredClone(measurementBindings),
          metrics: measurementMetricsFor(),
        })),
      },
      metrics: metricsFor(baseSha, true),
    },
  };
}

function deliveryEvidence(context, check, overrides = {}) {
  const now = Date.now();
  const commandContracts = [
    { id: 'frontend-build', cwd: 'frontend-app', argv: ['npm', 'run', 'build'] },
    { id: 'frontend-embed-verify', cwd: '.', argv: ['make', 'frontend-embed-verify'] },
    { id: 'desktop-start-smoke', cwd: 'frontend-app', argv: ['npm', 'run', 'smoke:desktop:rpc'] },
    { id: 'desktop-failure-smoke', cwd: 'frontend-app', argv: ['npm', 'run', 'smoke:desktop:failure'] },
  ];
  const runnerFiles = check.runnerFiles.map((runnerPath) => ({
    path: runnerPath,
    sha256: createHash('sha256').update(execFileSync('git', [
      'show', `${context.scoreBaseSha}:${runnerPath}`,
    ], { cwd: context.repoRoot })).digest('hex'),
  }));
  const runnerContentHash = createHash('sha256');
  runnerFiles.forEach(({ path: runnerPath, sha256 }) => runnerContentHash.update(`${runnerPath}\0${sha256}\n`));
  return {
    schemaVersion: 1,
    generatedAt: new Date(now).toISOString(),
    subjectSha: context.subjectSha,
    subjectTree: context.subjectTree,
    metricId: 'T05-build-embed-smoke',
    caseIds: [...check.caseIds],
    testCount: check.testCount,
    provenance: {
      runnerId: 'frontend-delivery-smoke',
      runnerSha: context.scoreBaseSha,
      runnerTree: git(context.repoRoot, ['rev-parse', `${context.scoreBaseSha}^{tree}`]),
      runnerContentHash: runnerContentHash.digest('hex'),
      runnerFiles,
      worktreeClean: true,
      worktreeStatus: [],
      baselineAudit: null,
    },
    verdict: {
      status: 'PASS',
      reason: '',
      executedCommands: commandContracts.length,
      commands: commandContracts.map((command) => ({
        ...command,
        exitCode: 0,
        signal: null,
        startedAt: new Date(now).toISOString(),
        finishedAt: new Date(now).toISOString(),
        durationMs: 0,
        status: 'PASS',
      })),
    },
    ...overrides,
  };
}


afterEach(() => {
  while (temporaryRepositories.length > 0) {
    rmSync(temporaryRepositories.pop(), { recursive: true, force: true });
  }
}, 30_000);

describe('frontend maintainability scorer configuration', () => {
  it('rejects hand-authored PASS, weak or mutable probes, threshold drift, fixtures drift, and zero-test evidence', () => {
    expect(validateConfiguration()).toBe(true);

    const handAuthored = documents();
    handAuthored.controls.controls[0].status = 'PASS';
    expect(() => validateConfiguration(handAuthored.controls, handAuthored.fixtures))
      .toThrow('hand-authored result is forbidden');

    const weakCommand = documents();
    weakCommand.controls.controls.find(({ id }) => id === 'T04-local-gates').allOf[0].argv = ['echo', 'PASS'];
    expect(() => validateConfiguration(weakCommand.controls, weakCommand.fixtures)).toThrow('weak runner command');

    const missingDiffCheck = documents();
    missingDiffCheck.controls.controls.find(({ id }) => id === 'T04-local-gates').allOf.pop();
    expect(() => validateConfiguration(missingDiffCheck.controls, missingDiffCheck.fixtures))
      .toThrow('lane allOf argv T04-local-gates exact set mismatch');

    const mutableDiffCheck = documents();
    mutableDiffCheck.controls.controls.find(({ id }) => id === 'T04-local-gates').allOf[3]
      .argv = ['git', 'diff', '--check'];
    expect(() => validateConfiguration(mutableDiffCheck.controls, mutableDiffCheck.fixtures))
      .toThrow('git diff check argv differs from frozen contract');

    const t04FrontendTest = documents();
    const frontendTestCheck = t04FrontendTest.controls.controls.find(({ id }) => id === 'T04-local-gates')
      .allOf.find(({ argv }) => JSON.stringify(argv) === JSON.stringify(['npm', 'test']));
    expect(frontendTestCheck).toMatchObject({
      kind: 'command',
      cwd: 'frontend-app',
      argv: ['npm', 'test'],
      timeoutMs: 900000,
      caseIds: ['frontend-test'],
      testCount: 1,
    });
    frontendTestCheck.timeoutMs = 600000;
    expect(() => validateConfiguration(t04FrontendTest.controls, t04FrontendTest.fixtures))
      .toThrow('T04 frontend-test timeout must be 900000ms');

    const t04HookRouteGuard = documents();
    const hookRouteGuard = t04HookRouteGuard.controls.controls.find(({ id }) => id === 'T04-local-gates')
      .allOf.find(({ argv }) => argv[0] === 'go'
        && argv.some((argument) => argument.includes('TestAIMaintenanceGateRouteDeletionMutations')));
    hookRouteGuard.caseIds.pop();
    expect(() => validateConfiguration(t04HookRouteGuard.controls, t04HookRouteGuard.fixtures))
      .toThrow('T04 hook-route guard contract differs from frozen contract');

    const t04PlanSizeGuard = documents();
    const planSizeGuard = t04PlanSizeGuard.controls.controls.find(({ id }) => id === 'T04-local-gates')
      .allOf.find(({ argv }) => argv.includes('--assert-frontend-plan-size'));
    planSizeGuard.timeoutMs = 1;
    expect(() => validateConfiguration(t04PlanSizeGuard.controls, t04PlanSizeGuard.fixtures))
      .toThrow('T04 frontend-plan size guard contract differs from frozen contract');

    const mutableProbe = documents();
    mutableProbe.controls.controls.find(({ id }) => id === 'A02-state-ownership').allOf[0].argv[1] = 'scripts/renamed-guard.mjs';
    expect(() => validateConfiguration(mutableProbe.controls, mutableProbe.fixtures))
      .toThrow('lane allOf argv A02-state-ownership exact set mismatch');

    const lowerThreshold = documents();
    lowerThreshold.controls.thresholds.dimensions.P = 0;
    expect(() => validateConfiguration(lowerThreshold.controls, lowerThreshold.fixtures))
      .toThrow('score thresholds differ from the frozen plan');

    const zeroTest = documents();
    zeroTest.controls.controls[0].allOf[0].testCount = 0;
    expect(() => validateConfiguration(zeroTest.controls, zeroTest.fixtures)).toThrow('zero-test runner evidence');

    const emptyAllOf = documents();
    emptyAllOf.controls.controls.find(({ id }) => id === 'A04-action-registry').allOf = [];
    expect(() => validateConfiguration(emptyAllOf.controls, emptyAllOf.fixtures)).toThrow('invalid control shape');

    const mutableFutureRunner = documents();
    mutableFutureRunner.controls.controls.find(({ id }) => id === 'A04-action-registry')
      .allOf[1].argv[1] = 'scripts/mutable-runner.mjs';
    expect(() => validateConfiguration(mutableFutureRunner.controls, mutableFutureRunner.fixtures))
      .toThrow('action producer argv differs from frozen contract');

    const optionalDoD = documents();
    optionalDoD.controls.controls.find(({ id }) => id === 'E06-failure-matrix').required = false;
    expect(() => validateConfiguration(optionalDoD.controls, optionalDoD.fixtures))
      .toThrow('DoD control must be required');

    const optionalP04 = documents();
    optionalP04.controls.controls.find(({ id }) => id === 'P04-resource-budget').required = false;
    expect(() => validateConfiguration(optionalP04.controls, optionalP04.fixtures))
      .toThrow('DoD control must be required');

    const missingDesktopSources = documents();
    delete missingDesktopSources.controls.controls.find(({ id }) => id === 'T03-wails-integration')
      .allOf.find(({ probe }) => probe === 'desktopFailureSmoke').sourcePaths;
    expect(() => validateConfiguration(missingDesktopSources.controls, missingDesktopSources.fixtures))
      .toThrow('desktop failure smoke report contract differs from frozen contract');

    const mutablePerformanceCLI = documents();
    mutablePerformanceCLI.controls.controls.find(({ id }) => id === 'P02-history-budget')
      .allOf[0].argv.push('--json');
    expect(() => validateConfiguration(mutablePerformanceCLI.controls, mutablePerformanceCLI.fixtures))
      .toThrow('performance budget argv differs from frozen contract');

    const mutableDeliveryRunner = documents();
    mutableDeliveryRunner.controls.controls.find(({ id }) => id === 'T05-build-embed-smoke')
      .allOf[0].runnerFiles[0] = 'frontend-app/scripts/forged-delivery-runner.mjs';
    expect(() => validateConfiguration(mutableDeliveryRunner.controls, mutableDeliveryRunner.fixtures))
      .toThrow('delivery runnerFiles differs from the frozen execution closure');

    const missingFixture = documents();
    missingFixture.controls.controls[0].allOf[0].caseIds = ['does-not-exist'];
    expect(() => validateConfiguration(missingFixture.controls, missingFixture.fixtures)).toThrow('missing fixture case');

    const staleFixture = documents();
    staleFixture.fixtures.fixtures.push({ id: 'stale-red', area: 'test', expected: 'reject' });
    expect(() => validateConfiguration(staleFixture.controls, staleFixture.fixtures))
      .toThrow('frozen RED fixture ids exact set mismatch');
  });

  it('predeclares a non-empty executable contract for every frozen control', () => {
    const { controls } = documents();
    expect(controls.controls).toHaveLength(25);
    expect(controls.controls.every(({ required }) => typeof required === 'boolean')).toBe(true);
    expect(controls.controls.every(({ allOf }) => allOf.length > 0)).toBe(true);
    expect(['E06-failure-matrix', 'C05-provider-rpc-parity', 'T05-build-embed-smoke'].every((id) => (
      controls.controls.find((control) => control.id === id).required
    ))).toBe(true);
    expect(controls.controls.find(({ id }) => id === 'P04-resource-budget').required).toBe(true);
    const desktopFailureCheck = controls.controls.find(({ id }) => id === 'T03-wails-integration')
      .allOf.find(({ probe }) => probe === 'desktopFailureSmoke');
    expect(desktopFailureCheck.sourcePaths).toEqual(DESKTOP_FAILURE_SOURCE_PATHS);
    expect(JSON.stringify(controls)).not.toContain('notImplemented');
    expect(JSON.stringify(controls)).not.toMatch(/frontend-[a-z-]+-evidence\.mjs/u);
    expect(controls.controls.find(({ id }) => id === 'E06-failure-matrix').allOf[0].argv)
      .toEqual(['node', 'frontend-app/scripts/failure-matrix-runner.mjs']);
    expect(controls.controls.find(({ id }) => id === 'A04-action-registry').allOf[1].argv)
      .toEqual(['node', 'scripts/action-producer-guard.mjs']);
    expect(controls.controls.find(({ id }) => id === 'P01-render-isolation').allOf[0].argv)
      .toEqual(['node', 'scripts/performance-budget-runner.mjs', '--verify', '--subject', '$SUBJECT_SHA', '--baseline', '$FROZEN_BASELINE']);
    expect(controls.controls.find(({ id }) => id === 'T05-build-embed-smoke').allOf[0].argv)
      .toEqual([
        'node', 'scripts/delivery-smoke-runner.mjs', '--verify',
        '--repo', '$SUBJECT_REPO', '--subject', '$SUBJECT_SHA',
      ]);
    expect(controls.controls.find(({ id }) => id === 'T04-local-gates').allOf[3]).toMatchObject({
      kind: 'probe',
      probe: 'gitDiffCheck',
      evidenceProtocol: 'git-diff-check-v1',
      cwd: '.',
      argv: ['git', 'diff', '--check', '$SCORE_BASE_SHA', '$SUBJECT_SHA'],
      caseIds: ['frontend-diff-check'],
      testCount: 1,
    });
    expect(controls.controls.find(({ id }) => id === 'T04-local-gates').allOf[4]).toMatchObject({
      kind: 'command',
      cwd: '.',
      argv: ['go', 'test', './scripts', './scripts/ai_maintenance', '-run', '^(TestAIMaintenanceGateVerifiesLocalHookArtifacts|TestAIMaintenanceGateRouteDeletionMutations|TestBuildGatePlanRoutesFrontendBackendAndGeneratedFiles)$', '-count=1'],
      caseIds: ['hook-route-pre-commit-deleted', 'hook-route-pre-push-deleted', 'hook-route-ai-maintenance-deleted'],
      testCount: 3,
    });
    expect(controls.controls.find(({ id }) => id === 'T04-local-gates').allOf[5]).toMatchObject({
      kind: 'command',
      cwd: '.',
      argv: ['node', 'frontend-app/scripts/frontend-maintainability-score.mjs', '--assert-frontend-plan-size'],
      caseIds: ['frontend-plan-size-bytes', 'frontend-plan-size-lines'],
      testCount: 2,
    });
    expect(JSON.parse(readFileSync(join(scriptRoot, 'frontend-maintainability-baseline.json'), 'utf8')).baseSha)
      .toBe(plannedBaseSha);
    const backgroundHealth = controls.controls.find(({ id }) => id === 'E03-background-health');
    expect(backgroundHealth.allOf).toHaveLength(3);
    expect(backgroundHealth.allOf.map(({ argv }) => argv)).toEqual([
      ['node', 'node_modules/vitest/vitest.mjs', 'run', 'src/shared/diagnostics/frontendHealthStore.test.js', '--reporter=json', '--no-file-parallelism', '--maxWorkers=1'],
      ['node', 'node_modules/vitest/vitest.mjs', 'run', 'src/shared/ui/productionActionFailureMatrix.test.js', '--reporter=json', '--no-file-parallelism', '--maxWorkers=1'],
      ['node', 'frontend-app/scripts/failure-matrix-runner.mjs'],
    ]);
    expect(backgroundHealth.allOf[0].testNames).toEqual([
      'persists and deduplicates safe Health records with first and last occurrence times',
      'fails fast on malformed or field-expanded persisted Health data',
      'exposes one finite observable persistence failure state without a fallback store',
      'makes a storage read exception observable even when the storage throws TypeError',
      'uses the exact same identity for merge semantics and list keys',
      'makes default persistence failure observable and clear does not silently report success',
    ]);
    expect(backgroundHealth.allOf[1]).toMatchObject({
      testCount: 1,
      testNames: ['routes provider reconnect cancellation to Health without an interactive error'],
    });
    const publicErrorControl = controls.controls.find(({ id }) => id === 'C03-public-error-contract');
    expect(publicErrorControl.required).toBe(true);
    const publicErrorContract = publicErrorControl.allOf[0];
    expect(publicErrorContract.testCount).toBe(7);
    expect(publicErrorContract.testNames).toEqual([
      'routes synchronous failures to visible and persistent sinks without console-only reporting',
      'routes Promise rejection through the same sinks',
      'fails fast when actionId or the retryable action thunk is missing',
      'records a visible failure sink exception in Health without recursive reporting',
      'records an onError callback exception in Health without exposing the raw action cause',
      'terminates async reporting when the id factory and all caller-owned sinks throw',
      'survives throwing health and visible sinks with finite observable Health records',
    ]);
  });

  it('keeps the repaired terminal and visible-action truth bound to current executable probes', async () => {
    expect(sourceHasPromptHistoryConsoleOnly()).toBe(false);
    await expect(probeResult('terminalTruth')).resolves.toBe('PASS');
    await expect(probeResult('visibleActionError')).resolves.toBe('PASS');
    await expect(probeResult('actionProducerGuard')).resolves.toBe('PASS');
  }, EXECUTABLE_PROBE_TEST_TIMEOUT_MS);

  it('enforces exact CLI forms', () => {
    expect(cli(['--validate'])).toMatchObject({ status: 0 });
    expect(cli(['--validate', 'extra']).status).not.toBe(0);
    expect(cli(['--probe']).status).not.toBe(0);
    expect(cli(['--score', '--run', '--run']).status).not.toBe(0);
    expect(cli(['--final', '--repo', frozenRepoRoot]).status).not.toBe(0);
  });
});

describe('frozen scorer target binding', () => {
  it('keeps a validated performance metric PASS when another metric produces the aggregate exit', () => {
    const { controls } = documents();
    const performanceCheck = controls.controls.find(({ id }) => id === 'P01-render-isolation').allOf[0];
    const deliveryCheck = controls.controls.find(({ id }) => id === 'T05-build-embed-smoke').allOf[0];

    expect(acceptsNonZeroRunnerExitForValidatedSlice(
      performanceCheck,
      { verdict: { status: 'FAIL' } },
      2,
      'PASS',
    )).toBe(true);
    expect(acceptsNonZeroRunnerExitForValidatedSlice(
      performanceCheck,
      { verdict: { status: 'FAIL' } },
      1,
      'PASS',
    )).toBe(false);
    expect(acceptsNonZeroRunnerExitForValidatedSlice(
      performanceCheck,
      { verdict: { status: 'PASS' } },
      2,
      'PASS',
    )).toBe(false);
    expect(acceptsNonZeroRunnerExitForValidatedSlice(
      deliveryCheck,
      { verdict: { status: 'FAIL' } },
      2,
      'PASS',
    )).toBe(false);
    expect(acceptsNonZeroRunnerExitForValidatedSlice(
      performanceCheck,
      { verdict: { status: 'FAIL' } },
      2,
      'FAIL',
    )).toBe(false);
  });

  it('executes the single cached performance probe before npm test, build, and delivery controls', () => {
    const order = executionControlIds();
    const controlIds = documents().controls.controls.map((control) => control.id);
    const performanceChecks = documents().controls.controls
      .filter((control) => control.id.startsWith('P0'))
      .flatMap((control) => control.allOf);
    const executionKeys = performanceChecks.map((check) => structuredRunnerExecutionKey({
      scoreBaseSha: 'a'.repeat(40),
      subjectSha: 'b'.repeat(40),
    }, check));
    const firstHeavyControl = Math.min(
      order.indexOf('T04-local-gates'),
      order.indexOf('T05-build-embed-smoke'),
    );

    expect(order.slice(0, 4)).toEqual([
      'P01-render-isolation',
      'P02-history-budget',
      'P03-feedback-budget',
      'P04-resource-budget',
    ]);
    for (const controlId of order.slice(0, 4)) {
      expect(order.indexOf(controlId)).toBeLessThan(firstHeavyControl);
    }
    expect(new Set(executionKeys).size).toBe(1);
    expect(order).toHaveLength(controlIds.length);
    expect(new Set(order)).toEqual(new Set(controlIds));
  });

  it('waits for two exact load-window samples and rejects a 4.5001 mutation', async () => {
    const baselineEnvironment = { cpu: { logicalCores: 18 }, loadAverage: [10, 20, 30] };
    const samples = [
      [14.5, 20, 30],
      [14.5001, 20, 30],
      [14.5, 20, 30],
      [14.5, 20, 30],
    ];
    const rejected = [];
    let currentTime = 0;
    const audit = await waitForPerformanceLoadWindow({
      runnerArgv: ['node', 'performance-budget-runner.mjs', '--verify'],
      runnerCwd: '/detached/frontend-app',
      baselineEnvironment,
      candidateLogicalCores: 18,
      sampleLoadAverage: () => samples.shift(),
      now: () => currentTime,
      pause: async (milliseconds) => { currentTime += milliseconds; },
      onRejectedObservation: (observation, remainingMs) => rejected.push({ observation, remainingMs }),
      pollIntervalMs: 1_000,
      maxWaitMs: 10_000,
      requiredConsecutiveSamples: 2,
    });

    expect(audit).toEqual(expect.objectContaining({
      status: 'READY',
      runnerCwd: '/detached/frontend-app',
      runnerArgv: ['node', 'performance-budget-runner.mjs', '--verify'],
      elapsedMs: 3_000,
      tolerance: 4.5,
      requiredConsecutiveSamples: 2,
    }));
    expect(audit.observations.map(({ comparable, consecutiveComparableSamples }) => (
      [comparable, consecutiveComparableSamples]
    ))).toEqual([[true, 1], [false, 0], [true, 1], [true, 2]]);
    expect(audit.observations[1].deltas[0]).toBeCloseTo(4.5001, 4);
    expect(rejected).toHaveLength(1);
  });

  it('fails closed after a finite audited load-window timeout', async () => {
    let currentTime = 0;
    const rejected = [];
    const audit = await waitForPerformanceLoadWindow({
      runnerArgv: ['node', 'performance-budget-runner.mjs', '--verify'],
      runnerCwd: '/detached/frontend-app',
      baselineEnvironment: { cpu: { logicalCores: 18 }, loadAverage: [10, 20, 30] },
      candidateLogicalCores: 18,
      sampleLoadAverage: () => [14.5001, 20, 30],
      now: () => currentTime,
      pause: async (milliseconds) => { currentTime += milliseconds; },
      onRejectedObservation: (observation, remainingMs) => rejected.push({ observation, remainingMs }),
      pollIntervalMs: 1_000,
      maxWaitMs: 2_000,
      requiredConsecutiveSamples: 2,
    });

    expect(audit).toEqual(expect.objectContaining({
      status: 'TIMEOUT', elapsedMs: 2_000, maxWaitMs: 2_000, tolerance: 4.5,
    }));
    expect(audit.observations).toHaveLength(3);
    expect(audit.observations.every(({ comparable }) => comparable === false)).toBe(true);
    expect(rejected).toHaveLength(2);
  });

  it('keeps the audited load window before the shared runner command', () => {
    const source = readFileSync(scorerPath, 'utf8');
    const executionStart = source.indexOf('async function executeActualRunner');
    const loadWindowIndex = source.indexOf('loadWindow = await waitForPerformanceLoadWindow', executionStart);
    const commandIndex = source.indexOf('const result = await commandResult(runnerArgv[0]', executionStart);
    expect(executionStart).toBeGreaterThanOrEqual(0);
    expect(loadWindowIndex).toBeGreaterThan(executionStart);
    expect(commandIndex).toBeGreaterThan(loadWindowIndex);
    expect(source.slice(loadWindowIndex, commandIndex)).toContain("loadWindow.status !== 'READY'");
  });

  it('does not resolve ignored external dependencies while bootstrapping the frozen scorer', () => {
    const fixture = createFinalCliFixture();
    write('frontend-app/scripts/desktop-failure-smoke.mjs', [
      "import '@playwright/test';",
      `export const DESKTOP_FAILURE_SOURCE_PATHS = Object.freeze(${JSON.stringify(DESKTOP_FAILURE_SOURCE_PATHS)});`,
      '',
    ].join('\n'), fixture.baseRoot);
    write('frontend-app/node_modules/@playwright/test/package.json', JSON.stringify({
      name: '@playwright/test', type: 'module', exports: './index.js',
    }), fixture.baseRoot);
    write('frontend-app/node_modules/@playwright/test/index.js', [
      "throw new Error('untrusted ignored dependency executed');",
      '',
    ].join('\n'), fixture.baseRoot);

    const result = spawnSync(process.execPath, [
      join(fixture.baseRoot, 'frontend-app', 'scripts', 'frontend-maintainability-score.mjs'),
      '--validate',
    ], { cwd: join(fixture.baseRoot, 'frontend-app'), encoding: 'utf8' });

    expect(result.status).toBe(0);
    expect(`${result.stdout}${result.stderr}`).not.toContain('untrusted ignored dependency executed');
    expect(result.stdout).toContain('frontend maintainability scorer configuration valid');
  });

  it('scores another Git target without loading a scorer from that target', async () => {
    const target = createTargetRepository();
    const result = await scoreCurrentTree({ repoRoot: target.repoRoot, subjectSha: target.subjectSha });

    expect(result.subjectSha).toBe(target.subjectSha);
    expect(result.subjectTree).toBe(target.subjectTree);
    expect(result.controls).toHaveLength(25);
    expect(result.controls.map((control) => control.id))
      .toEqual(documents().controls.controls.map((control) => control.id));
    expect(result.controls.every(({ status }) => status !== 'PASS')).toBe(true);
    expect(result.displayScore).toBe(0);
  });

  it('rejects a mismatched subject and a dirty or untracked target', () => {
    const target = createTargetRepository();
    expect(() => inspectTargetRepository({
      repoRoot: target.repoRoot,
      subjectSha: 'f'.repeat(40),
      requireClean: true,
    })).toThrow('subject must equal target HEAD');
    expect(cli(['--score', '--repo', target.repoRoot, '--subject', 'f'.repeat(40)]).status).not.toBe(0);

    write('untracked.txt', 'dirty\n', target.repoRoot);
    expect(() => inspectTargetRepository({
      repoRoot: target.repoRoot,
      subjectSha: target.subjectSha,
      requireClean: true,
    })).toThrow('dirty or untracked target worktrees');
    expect(cli(['--score', '--repo', target.repoRoot, '--subject', target.subjectSha]).status).not.toBe(0);
  });

  it('accepts only a clean strict descendant with byte-identical frozen governance', () => {
    const target = createFinalContractTarget();
    expect(git(target.repoRoot, ['status', '--porcelain=v1', '--untracked-files=all'])).toBe('');
    const context = inspectTargetRepository({
      repoRoot: target.repoRoot,
      subjectSha: target.subjectSha,
      requireClean: true,
      requireFinalContract: true,
    });

    expect(context.subjectSha).toBe(target.subjectSha);
    expect(context.scoreBaseSha).toBe(git(frozenRepoRoot, ['rev-parse', 'HEAD']));
    expect(context.subjectContract).toMatchObject({
      rule: 'strict-descendant-governance-frozen-tree-relation-v1',
      finalContractEnforced: true,
      ancestry: 'strict-descendant',
      treeRelation: 'changed-tree',
      identityFreeze: false,
      governanceFreeze: {
        rule: 'byte-identical-governance-v1',
        pathCount: expect.any(Number),
      },
    });
    expect(context.subjectContract.governanceFreeze.sha256).toMatch(/^[0-9a-f]{64}$/u);

    const emptyCommitContract = {
      scoreBaseSha: 'a'.repeat(40),
      scoreBaseTree: 'b'.repeat(40),
      subjectSha: 'c'.repeat(40),
      subjectTree: 'b'.repeat(40),
      strictDescendant: true,
      governanceFreeze: context.subjectContract.governanceFreeze,
      finalContractEnforced: true,
    };
    expect(() => createSubjectContract(emptyCommitContract)).toThrow('empty-commit evidence is rejected');
    expect(() => createSubjectContract({
      ...emptyCommitContract,
      subjectTree: 'd'.repeat(40),
      strictDescendant: true,
      governanceFreeze: null,
      finalContractEnforced: true,
    })).toThrow('exact frozen governance evidence');
    expect(() => createSubjectContract({
      ...emptyCommitContract,
      subjectTree: 'd'.repeat(40),
      strictDescendant: false,
      finalContractEnforced: true,
    })).toThrow('strict descendant ancestry');

    write('frontend-app/scripts/delivery-smoke-runner.mjs', 'process.stdout.write("forged PASS\\n");\n', target.repoRoot);
    git(target.repoRoot, ['add', '.']);
    git(target.repoRoot, ['commit', '-q', '-m', '测试：伪造候选交付运行器']);
    expect(() => inspectTargetRepository({
      repoRoot: target.repoRoot,
      subjectSha: git(target.repoRoot, ['rev-parse', 'HEAD']),
      requireClean: false,
      requireFinalContract: true,
    })).toThrow('frozen governance drift');
    for (const path of [
      'frontend-app/scripts/desktop-failure-smoke.mjs',
      'frontend-app/tests/e2e/desktop-failure.spec.js',
      'frontend-app/playwright.failure.config.js',
      'frontend-app/package.json',
      'frontend-app/src/shared/api/wailsBridge.js',
      'internal/ui/wails/testdata/failure_smoke_host/main.go',
      'internal/provider/claudecli/event_map.go',
      'internal/provider/codexapp/event_map.go',
      'internal/provider/unified/event_map.go',
      'internal/ui/wails/bridge.go',
    ]) {
      const controlled = createFinalContractTarget();
      write(path, 'forged governance drift\\n', controlled.repoRoot);
      git(controlled.repoRoot, ['add', '.']);
      git(controlled.repoRoot, ['commit', '-q', '-m', '测试：篡改 T03 冻结闭包']);
      expect(() => inspectTargetRepository({
        repoRoot: controlled.repoRoot,
        subjectSha: git(controlled.repoRoot, ['rev-parse', 'HEAD']),
        requireClean: true,
        requireFinalContract: true,
      })).toThrow('frozen governance drift');
    }
  }, 90_000);

  it.each([
    'frontend-app/eslint.config.js',
    'frontend-app/package-lock.json',
    'frontend-app/scripts/no-critical-skip.mjs',
    'frontend-app/scripts/managed-command.mjs',
    'frontend-app/scripts/rpc-contract-audit.mjs',
    'frontend-app/scripts/critical-typecheck-guard.mjs',
    'frontend-app/playwright.failure.config.js',
  ])('rejects frozen delivery closure mutation: %s', (relativePath) => {
    const target = createFinalContractTarget();
    const source = relativePath.endsWith('.json') ? '{"forged":true}' : '// forged delivery closure';
    write(relativePath, `${source}\n`, target.repoRoot);
    git(target.repoRoot, ['add', '.']);
    commitTree(target.repoRoot, '测试：伪造交付闭包');
    expect(() => inspectTargetRepository({
      repoRoot: target.repoRoot,
      subjectSha: git(target.repoRoot, ['rev-parse', 'HEAD']),
      requireClean: false,
      requireFinalContract: true,
    })).toThrow('frozen governance drift');
  });

  it('keeps the canonical detached immutable dependency installation Git-clean and Vitest-executable', async () => {
    const fixture = createFinalCliFixture();
    try {
      const tmpAlias = detachedTmpAlias();
      const proofRoot = mkdtempSync(join(tmpdir(), 'frontend-maintainability-detached-proof-'));
      const proofPath = join(proofRoot, 'proof.json');
      const loadAveragePreloadPath = join(proofRoot, 'load-average-preload.mjs');
      const frozenLoadAverage = JSON.parse(readFileSync(join(scriptRoot, 'frontend-maintainability-baseline.json'), 'utf8'))
        .environment.loadAverage;
      writeFileSync(loadAveragePreloadPath, [
        "import os from 'node:os';",
        `os.loadavg = () => ${JSON.stringify(frozenLoadAverage)};`,
        '',
      ].join('\n'));
      temporaryRepositories.push(proofRoot);
      if (process.platform === 'darwin') {
        expect(tmpAlias).toMatch(/^\/var(?:\/|$)/u);
        expect(realpathSync(tmpAlias)).toMatch(/^\/private\/var(?:\/|$)/u);
      }
      const result = await runManagedCommand(process.execPath, [
        join(fixture.baseRoot, 'frontend-app', 'scripts', 'frontend-maintainability-score.mjs'),
        '--final',
        '--repo', fixture.subjectRoot,
        '--subject', fixture.subjectSha,
      ], {
        cwd: join(fixture.baseRoot, 'frontend-app'),
        encoding: 'utf8',
        env: {
          ...process.env,
          NODE_OPTIONS: [
            process.env.NODE_OPTIONS,
            `--import=${pathToFileURL(loadAveragePreloadPath).href}`,
          ].filter(Boolean).join(' '),
          SCORER_DETACHED_MOUNT_PROOF: proofPath,
          TMPDIR: tmpAlias,
        },
        timeoutMs: FINAL_SCORER_COMMAND_TIMEOUT_MS,
        killGraceMs: 2_000,
      });

      expect(result.status, `${result.stdout}\n${result.stderr}`).toBe(1);
      expect(result.stdout, result.stderr).toContain(`SCORE_BASE\t${fixture.scoreBaseSha}`);
      expect(result.stdout).toMatch(new RegExp(`^SCORE\\t\\d+\\.\\d\\t${fixture.subjectSha}$`, 'mu'));
      expect(result.stdout).toContain('REPORT\t');
      expect(result.stderr).toContain('FINAL_GATE\tFAIL');
      const reportPath = result.stdout.match(/^REPORT\t(.+)$/mu)?.[1];
      expect(reportPath).toBeTruthy();
      temporaryRepositories.push(dirname(reportPath));
      const report = JSON.parse(readFileSync(reportPath, 'utf8'));
      const deliveryEvidence = report.controls.find(({ id }) => id === 'T05-build-embed-smoke').evidence[0];
      expect(existsSync(proofPath), [result.stdout, result.stderr, deliveryEvidence.summary].join('\n')).toBe(true);
      const mountProof = JSON.parse(readFileSync(proofPath, 'utf8'));
      expect(mountProof).toMatchObject({
        gitStatusExitCode: 0,
        gitStatus: '',
        nodeModulesIsDirectory: true,
        nodeModulesIsSymbolicLink: false,
        vitestExitCode: 0,
        vitestPassedTests: 1,
      });
      expect(deliveryEvidence.summary).not.toContain('runner report is not valid JSON');
      expect(deliveryEvidence.summary).toBe('runner report schemaVersion must equal 1');
    }
    finally {
      execFileSync('git', ['worktree', 'remove', '--force', fixture.subjectRoot], { cwd: fixture.baseRoot });
    }
  }, FINAL_SCORER_TEST_TIMEOUT_MS);

  it.each([
    'eslint/bin/eslint.js',
    'vitest/vitest.mjs',
    'vite/bin/vite.js',
    '@playwright/test/index.js',
  ])('rejects an immutable installed-tool mutation before final execution: %s', async (relativePath) => {
    const fixture = createFinalCliFixture();
    const commandRoot = mkdtempSync(join(tmpdir(), 'frontend-maintainability-installer-mutation-'));
    const trustedToolsRoot = join(commandRoot, 'trusted-tools');
    const npmShim = join(commandRoot, 'npm');
    const immutableToolPaths = [
      'eslint/bin/eslint.js',
      'vitest/vitest.mjs',
      'vite/bin/vite.js',
      '@playwright/test/index.js',
    ];
    try {
      const ignoredHostTool = join(fixture.baseRoot, 'frontend-app', 'node_modules', relativePath);
      mkdirSync(dirname(ignoredHostTool), { recursive: true });
      writeFileSync(ignoredHostTool, 'throw new Error("ignored host tool must not execute");\n');
      expect(git(fixture.baseRoot, ['status', '--porcelain', '--untracked-files=all'])).toBe('');
      for (const toolPath of immutableToolPaths) {
        const target = join(trustedToolsRoot, toolPath);
        mkdirSync(dirname(target), { recursive: true });
        copyFileSync(join(frozenRepoRoot, 'frontend-app', 'node_modules', toolPath), target);
      }
      writeFileSync(npmShim, [
        '#!/bin/sh',
        'set -eu',
        'mkdir -p ./node_modules/eslint/bin ./node_modules/vitest ./node_modules/vite/bin ./node_modules/@playwright/test',
        'cp "$SCORER_TRUSTED_TOOLS/eslint/bin/eslint.js" ./node_modules/eslint/bin/eslint.js',
        'cp "$SCORER_TRUSTED_TOOLS/vitest/vitest.mjs" ./node_modules/vitest/vitest.mjs',
        'cp "$SCORER_TRUSTED_TOOLS/vite/bin/vite.js" ./node_modules/vite/bin/vite.js',
        'cp "$SCORER_TRUSTED_TOOLS/@playwright/test/index.js" ./node_modules/@playwright/test/index.js',
        `printf '%s\\n' 'throw new Error("forged installed tool must not execute");' > "./node_modules/${relativePath}"`,
      ].join('\n'), { mode: 0o755 });
      const result = await runManagedCommand(process.execPath, [
        join(fixture.baseRoot, 'frontend-app', 'scripts', 'frontend-maintainability-score.mjs'),
        '--final',
        '--repo', fixture.subjectRoot,
        '--subject', fixture.subjectSha,
      ], {
        cwd: join(fixture.baseRoot, 'frontend-app'),
        env: {
          ...process.env,
          PATH: `${commandRoot}:${process.env.PATH}`,
          SCORER_TRUSTED_TOOLS: trustedToolsRoot,
        },
        timeoutMs: 90_000,
        killGraceMs: 2_000,
      });
      expect(result.timedOut, [result.stdout, result.stderr].join('\n')).toBe(false);
      expect(result.status).toBe(1);
      expect(result.stderr).toContain(`immutable dependency tool SHA-256 mismatch: ${relativePath}`);
      expect(result.stderr).not.toContain('forged installed tool must not execute');
    }
    finally {
      execFileSync('git', ['worktree', 'remove', '--force', fixture.subjectRoot], { cwd: fixture.baseRoot });
    }
  }, 120_000);

  it('terminates a timed-out final gate process tree and removes its detached worktree', async () => {
    const fixture = createFinalCliFixture({ hangFirstProbe: true });
    const commandTmpRoot = mkdtempSync(join(tmpdir(), 'frontend-maintainability-timeout-proof-'));
    temporaryRepositories.push(commandTmpRoot);
    const npmShim = join(commandTmpRoot, 'npm');
    const descendantPidPath = join(commandTmpRoot, 'descendant-pids.json');
    writeFileSync(npmShim, `#!/bin/sh
exec '${process.execPath}' -e '
  const { spawn } = require("node:child_process");
  const fs = require("node:fs");
  const child = spawn(process.execPath, ["-e", "setInterval(() => {}, 1000)"], { stdio: "ignore" });
  fs.writeFileSync(process.env.SCORER_TIMEOUT_DESCENDANT_PID, JSON.stringify({ parent: process.pid, child: child.pid }));
  setInterval(() => {}, 1000);
'
`, { mode: 0o755 });
    try {
      const result = await runManagedCommand(process.execPath, [
        join(fixture.baseRoot, 'frontend-app', 'scripts', 'frontend-maintainability-score.mjs'),
        '--final',
        '--repo', fixture.subjectRoot,
        '--subject', fixture.subjectSha,
      ], {
        cwd: join(fixture.baseRoot, 'frontend-app'),
        env: {
          ...process.env,
          TMPDIR: commandTmpRoot,
          PATH: `${commandTmpRoot}:${process.env.PATH}`,
          SCORER_TIMEOUT_DESCENDANT_PID: descendantPidPath,
        },
        timeoutMs: 5_000,
        killGraceMs: 20_000,
      });

      expect(result.timedOut, [result.stdout, result.stderr, result.error?.message].filter(Boolean).join('\n')).toBe(true);
      expect(result.signal ?? result.status).not.toBe(null);
      const cleanupDeadline = Date.now() + 5_000;
      while (readdirSync(commandTmpRoot).some((entry) => entry.startsWith('frontend-maintainability-subject-'))
        && Date.now() < cleanupDeadline) {
        await new Promise((resolve) => setTimeout(resolve, 100));
      }
      expect(readdirSync(commandTmpRoot).filter((entry) => entry.startsWith('frontend-maintainability-subject-'))).toEqual([]);
      const worktrees = git(fixture.baseRoot, ['worktree', 'list', '--porcelain']);
      expect(worktrees).not.toContain('frontend-maintainability-subject-');
      const { parent, child } = JSON.parse(readFileSync(descendantPidPath, 'utf8'));
      const pidDeadline = Date.now() + 5_000;
      const processIsGone = (pid) => {
        try {
          process.kill(pid, 0);
          return false;
        }
        catch (error) {
          return error.code === 'ESRCH';
        }
      };
      while ((!processIsGone(parent) || !processIsGone(child)) && Date.now() < pidDeadline) {
        await new Promise((resolve) => setTimeout(resolve, 100));
      }
      expect(processIsGone(parent)).toBe(true);
      expect(processIsGone(child)).toBe(true);
      expect(existsSync(join(fixture.baseRoot, '.tmp', 'frontend-maintainability-score', `${fixture.subjectSha}.json`))).toBe(false);
      expect(existsSync(commandTmpRoot)).toBe(true);
    }
    finally {
      execFileSync('git', ['worktree', 'remove', '--force', fixture.subjectRoot], { cwd: fixture.baseRoot });
    }
  }, 45_000);
});

describe('executable evidence registry', () => {
  it('keeps Task2 action exemptions NOT_VERIFIED and requires exact zero-exemption output for PASS', () => {
    expect(actionProducerGuardOutputStatus(
      'action producer guard passed: discovered=30 covered=2 exempted=28',
    )).toBe('NOT_VERIFIED');
    expect(actionProducerGuardOutputStatus(
      'action producer guard passed: discovered=30 covered=30 exempted=0',
    )).toBe('PASS');
    expect(actionProducerGuardOutputStatus(
      'action producer guard passed: discovered=30 covered=29 exempted=0',
    )).toBe('FAIL');
    expect(actionProducerGuardOutputStatus('action producer guard passed')).toBe('FAIL');
  });

  it('keeps resolved legacy artifact probes read-only and unregistered until real lane evidence exists', async () => {
    expect(sourceHasPromptHistoryConsoleOnly()).toBe(false);
    expect(sourceHasCriticalTypecheckGap()).toBe(false);
    await expect(probeResult('promptHistoryVisibleError')).resolves.toBe('NOT_VERIFIED');
    await expect(probeResult('criticalTypecheck')).resolves.toBe('NOT_VERIFIED');
  });

  it('keeps an unregistered redMatrix NOT_VERIFIED and accepts the exact action runner', async () => {
    await expect(probeResult('redMatrix')).resolves.toBe('NOT_VERIFIED');
    const result = await runManagedCommand(process.execPath, [
      scorerPath,
      '--probe',
      'actionProducerGuard',
    ], {
      cwd: frozenRepoRoot,
      timeoutMs: ACTION_PRODUCER_PROBE_TIMEOUT_MS,
      killGraceMs: 10_000,
    });
    expect(result.timedOut).toBe(false);
    expect(result.status, result.stderr).toBe(0);
    expect(result.stdout.trim()).toBe('PASS');
  }, ACTION_PRODUCER_PROBE_TEST_TIMEOUT_MS);

  it('rejects stale, mismatched, zero-test, wrong-control, and wrong-case Task3 evidence', () => {
    const { controls } = documents();
    const control = controls.controls.find(({ id }) => id === 'E06-failure-matrix');
    const check = control.allOf[0];
    const context = inspectTargetRepository();
    const now = Date.now();
    const options = { context, control, check, startedAt: now - 5_000, finishedAt: now + 5_000 };
    const valid = failureMatrixEvidence(context);

    expect(structuredEvidenceStatus(valid, options)).toBe('PASS');
    expect(structuredEvidenceStatus({ ...valid, subjectSha: 'f'.repeat(40) }, options)).toBe('FAIL');
    expect(structuredEvidenceStatus({ ...valid, generatedAt: '2000-01-01T00:00:00.000Z' }, options)).toBe('FAIL');
    expect(structuredEvidenceStatus({ ...valid, testCount: 0, evidence: [] }, options)).toBe('FAIL');
    expect(structuredEvidenceStatus({ ...valid, controlIds: valid.controlIds.slice(0, 3) }, options)).toBe('FAIL');
    expect(structuredEvidenceStatus({
      ...valid,
      caseIds: valid.caseIds.slice(1),
    }, options)).toBe('FAIL');
    expect(structuredEvidenceStatus({
      ...valid,
      evidence: valid.evidence.map((entry) => (
        ['FM-15', 'FM-18', 'FM-21', 'FM-22', 'FM-23', 'FM-24'].includes(entry.caseId)
          ? { ...entry, layer: 'fixture-replay' }
          : entry
      )),
    }, options)).toBe('FAIL');
  });

  it('requires non-zero mutation RED and immutable source binding for every T01 case', () => {
    const { controls } = documents();
    const control = controls.controls.find(({ id }) => id === 'T01-red-green-regression');
    const check = control.allOf[0];
    const context = inspectTargetRepository();
    const mutationDocument = JSON.parse(readFileSync(join(scriptRoot, 'failure-matrix-mutations.json'), 'utf8'));
    expect(mutationDocument.mutations.find(({ id }) => id === 'force-codex-terminal-success')).toMatchObject({
      search: 'Success:              outcome.',
      replacement: 'Success:              true || outcome.',
    });
    expect(mutationDocument.mutations.find(({ id }) => id === 'misclassify-claude-interruption')).toMatchObject({
      search: 'case "turn:interrupted":',
      replacement: 'case "turn:interrupted-disabled":',
    });
    const now = Date.now();
    const options = { context, control, check, startedAt: now - 100, finishedAt: now + 100 };
    const valid = failureMatrixEvidence(context);
    expect(structuredEvidenceStatus(valid, options)).toBe('PASS');
    expect(structuredEvidenceStatus({
      ...valid,
      redGreenCases: valid.redGreenCases.slice(1),
    }, options)).toBe('FAIL');
    expect(structuredEvidenceStatus({
      ...valid,
      redGreenCases: valid.redGreenCases.map((entry) => (
        entry.caseId === 'FM-01' ? { ...entry, red: { ...entry.red, exitCode: 0 } } : entry
      )),
    }, options)).toBe('FAIL');
    expect(structuredEvidenceStatus({
      ...valid,
      redGreenCases: valid.redGreenCases.map((entry) => (
        entry.caseId === 'FM-01' ? { ...entry, green: { ...entry.green, exitCode: 1 } } : entry
      )),
    }, options)).toBe('FAIL');
    expect(structuredEvidenceStatus({
      ...valid,
      redGreenCases: valid.redGreenCases.map((entry) => (
        entry.caseId === 'FM-01' ? { ...entry, red: { ...entry.red, sourceSha256: '0'.repeat(64) } } : entry
      )),
    }, options)).toBe('FAIL');
  });

  it('requires action-specific production callbacks and source hashes for T02', () => {
    const { controls } = documents();
    const control = controls.controls.find(({ id }) => id === 'T02-critical-action-coverage');
    const check = control.allOf.find(({ evidenceProtocol }) => evidenceProtocol === 'action-production-runtime-report-v1');
    const context = inspectTargetRepository();
    const now = Date.now();
    const subjectRoot = createSubjectFrontendSnapshot(context);
    const reportPath = join(subjectRoot, 'action-binding-report.json');
    const actionProducerGuardPath = realpathSync(join(subjectRoot, 'frontend-app', 'scripts', 'action-producer-guard.mjs'));
    execFileSync(
      process.execPath,
      [actionProducerGuardPath, '--report', reportPath],
      { cwd: subjectRoot },
    );
    const report = JSON.parse(readFileSync(reportPath, 'utf8'));
    expect(report).toMatchObject({
      reportKind: 'action-production-binding-structure-v1',
      status: 'structural-only',
    });
    report.controlId = 'T02-critical-action-coverage';
    report.status = 'covered';
    report.schemaVersion = 2;
    const matrix = JSON.parse(readFileSync(join(frozenRepoRoot, 'frontend-app/config/action-producer-test-matrix.json'), 'utf8'));
    const registry = JSON.parse(readFileSync(join(frozenRepoRoot, 'frontend-app/config/action-producer-registry.json'), 'utf8'));
    report.structuralActionCount = report.actionIds.length;
    report.errorSourceCaseCount = matrix.cells.length;
    report.runtimeEvidenceScope = 'five-semantic-class-anchors';
    report.runtimeAnchorCount = matrix.runtimeBindings.length;
    report.runtimeClaimsFullMatrix = false;
    report.requiredRuntimeActionIds = matrix.runtimeBindings.map(({ actionId }) => actionId).sort();
    report.requiredRuntimeSemanticClasses = matrix.runtimeBindings.map(({ semanticClass }) => semanticClass).sort();
    report.runtimeCases = matrix.runtimeBindings.map((anchor) => {
      const bindings = report.bindings.filter(({ actionId, sourcePath }) => (
        actionId === anchor.actionId && sourcePath === anchor.sourcePath
      )).sort((left, right) => right.callbackStart - left.callbackStart);
      const source = execFileSync('git', [
        'show', `${context.subjectSha}:frontend-app/${anchor.sourcePath}`,
      ], { cwd: context.repoRoot, encoding: 'utf8' });
      let mutated = source;
      for (const binding of bindings) {
        mutated = `${mutated.slice(0, binding.callbackStart)}() => {}${mutated.slice(binding.callbackEnd)}`;
      }
      const testSource = execFileSync('git', [
        'show', `${context.subjectSha}:frontend-app/${anchor.testFile}`,
      ], { cwd: context.repoRoot });
      return {
        semanticClass: anchor.semanticClass,
        actionId: anchor.actionId,
        sourcePath: anchor.sourcePath,
        sourceSha256: createHash('sha256').update(source).digest('hex'),
        mutatedSha256: createHash('sha256').update(mutated).digest('hex'),
        handlers: [...new Set(bindings.flatMap(({ handlers }) => handlers))].sort(),
        bindingLocations: bindings.map(({ line, column }) => ({ line, column })),
        testFile: anchor.testFile,
        testName: anchor.testName,
        testFileSha256: createHash('sha256').update(testSource).digest('hex'),
        green: {
          cwd: 'frontend-app',
          argv: [
            join('node_modules', '.bin', 'vitest'), 'run', anchor.testFile,
            '-t', anchor.testName, '--no-file-parallelism', '--maxWorkers=1',
          ],
          exitCode: 0,
          signal: null,
          outputSha256: createHash('sha256').update(`GREEN:${anchor.actionId}`).digest('hex'),
        },
        red: {
          cwd: 'frontend-app',
          argv: [
            join('node_modules', '.bin', 'vitest'), 'run', anchor.testFile,
            '-t', anchor.testName, '--no-file-parallelism', '--maxWorkers=1',
          ],
          exitCode: 1,
          signal: null,
          outputSha256: createHash('sha256').update(`RED:${anchor.actionId}`).digest('hex'),
        },
      };
    });
    const matrixTestFile = 'src/shared/ui/productionActionFailureMatrix.test.js';
    const matrixTestSource = execFileSync('git', [
      'show', `${context.subjectSha}:frontend-app/${matrixTestFile}`,
    ], { cwd: context.repoRoot });
    report.matrixExecution = {
      testFile: matrixTestFile,
      testFileSha256: createHash('sha256').update(matrixTestSource).digest('hex'),
      cwd: 'frontend-app',
      argv: [
        join('node_modules', '.bin', 'vitest'), 'run', matrixTestFile,
        '--reporter=json', '--no-file-parallelism', '--maxWorkers=1',
      ],
      exitCode: 0,
      signal: null,
      outputSha256: createHash('sha256').update('T02 matrix execution').digest('hex'),
      vitest: {
        numTotalTests: matrix.cells.length,
        numPassedTests: matrix.cells.length,
        numFailedTests: 0,
        numPendingTests: 0,
      },
    };
    report.cellResults = matrix.cells.map((cell, index) => ({
      actionId: cell.actionId,
      errorSource: cell.errorSource,
      evidence: [...cell.evidence],
      bindingKeys: report.bindings.filter(({ actionId }) => actionId === cell.actionId)
        .map(({ sourcePath, line, column }) => `${sourcePath}:${line}:${column}`).sort(),
      testName: productionActionFailureMatrixTitle(index, cell),
      testFileSha256: report.matrixExecution.testFileSha256,
      vitest: {
        status: 'passed',
        title: productionActionFailureMatrixTitle(index, cell),
      },
    }));
    report.testCount = report.cellResults.length;
    expect(report.structuralActionCount).toBe(registry.coveredProducers.length);
    expect(report.runtimeAnchorCount).toBe(5);
    expect(report.runtimeAnchorCount).toBeLessThan(report.bindingCount);
    report.generatedAt = new Date(now).toISOString();
    const options = { context, control, check, startedAt: now - 100, finishedAt: now + 100 };
    expect(structuredEvidenceStatus(report, options)).toBe('PASS');
    const specialCellIndex = matrix.cells.findIndex(({ actionId, errorSource }) => (
      actionId === 'provider.reconnect' && errorSource === 'promise-reject'
    ));
    expect(specialCellIndex).toBeGreaterThanOrEqual(0);
    expect(report.cellResults[specialCellIndex].testName)
      .toBe('routes provider reconnect cancellation to Health without an interactive error');
    expect(structuredEvidenceStatus({
      ...report,
      cellResults: report.cellResults.map((entry, index) => (
        index === specialCellIndex
          ? { ...entry, testName: `cell-${index}`, vitest: { ...entry.vitest, title: `cell-${index}` } }
          : entry
      )),
    }, options)).toBe('FAIL');
    expect(structuredEvidenceStatus({
      ...report,
      cellResults: report.cellResults.map((entry, index) => (
        index === specialCellIndex
          ? { ...entry, testName: report.cellResults[0].testName, vitest: { ...entry.vitest, title: report.cellResults[0].testName } }
          : entry
      )),
    }, options)).toBe('FAIL');
    expect(structuredEvidenceStatus({
      ...report,
      cellResults: report.cellResults.map((entry, index) => {
        if (index !== specialCellIndex) return entry;
        return Object.fromEntries(Object.entries(entry).filter(([key]) => key !== 'testName'));
      }),
    }, options)).toBe('FAIL');
    expect(structuredEvidenceStatus({ ...report, bindings: report.bindings.slice(1) }, options)).toBe('FAIL');
    expect(structuredEvidenceStatus({
      ...report,
      bindings: report.bindings.map((entry, index) => (
        index === 0 ? { ...entry, handlers: [] } : entry
      )),
    }, options)).toBe('FAIL');
    expect(structuredEvidenceStatus({
      ...report,
      runtimeClaimsFullMatrix: true,
    }, options)).toBe('FAIL');
    expect(structuredEvidenceStatus({
      ...report,
      runtimeCases: report.runtimeCases.map((entry, index) => (
        index === 0 ? { ...entry, red: { ...entry.red, exitCode: 0 } } : entry
      )),
    }, options)).toBe('FAIL');
    expect(structuredEvidenceStatus({
      ...report,
      runtimeCases: report.runtimeCases.map((entry, index) => (
        index === 0 ? { ...entry, green: { ...entry.green, exitCode: 1 } } : entry
      )),
    }, options)).toBe('FAIL');
    expect(structuredEvidenceStatus({
      ...report,
      bindings: report.bindings.map((entry, index) => (
        index === 0 ? { ...entry, sourceSha256: '0'.repeat(64) } : entry
      )),
    }, options)).toBe('FAIL');
    expect(structuredEvidenceStatus({ ...report, cellResults: report.cellResults.slice(1) }, options)).toBe('FAIL');
    expect(structuredEvidenceStatus({
      ...report,
      cellResults: [...report.cellResults.slice(0, -1), report.cellResults[0]],
    }, options)).toBe('FAIL');
    expect(structuredEvidenceStatus({
      ...report,
      cellResults: report.cellResults.map((entry, index) => (
        index === 0 ? { ...entry, bindingKeys: [] } : entry
      )),
    }, options)).toBe('FAIL');
    expect(structuredEvidenceStatus({ ...report, matrixExecution: {} }, options)).toBe('FAIL');
    expect(structuredEvidenceStatus({
      ...report,
      cellResults: report.cellResults.map((entry, index) => (
        index === 0 ? { ...entry, vitest: { ...entry.vitest, status: 'pending' } } : entry
      )),
    }, options)).toBe('FAIL');
  }, ACTION_PRODUCER_PROBE_TEST_TIMEOUT_MS);

  it('accepts source-hashed T03 v2 evidence while retaining v1-only validation for other runners', () => {
    const { controls } = documents();
    const context = inspectTargetRepository();
    const now = Date.now();
    const t03Control = controls.controls.find(({ id }) => id === 'T03-wails-integration');
    const t03Check = t03Control.allOf.find(({ evidenceProtocol }) => evidenceProtocol === 'desktop-failure-report-v1');
    const t03Options = { context, control: t03Control, check: t03Check, startedAt: now - 100, finishedAt: now + 100 };
    const t03Report = desktopFailureEvidence(context);
    expect(DESKTOP_FAILURE_SOURCE_PATHS).toHaveLength(10);
    expect(Object.isFrozen(DESKTOP_FAILURE_SOURCE_PATHS)).toBe(true);
    expect(structuredEvidenceStatus(t03Report, t03Options)).toBe('PASS');
    expect(structuredEvidenceStatus({ ...t03Report, schemaVersion: 1 }, t03Options)).toBe('FAIL');

    const t01Control = controls.controls.find(({ id }) => id === 'T01-red-green-regression');
    const t01Check = t01Control.allOf.find(({ evidenceProtocol }) => evidenceProtocol === 'failure-matrix-report-v1');
    const t01Options = { context, control: t01Control, check: t01Check, startedAt: now - 100, finishedAt: now + 100 };
    const t01Report = failureMatrixEvidence(context);
    expect(structuredEvidenceStatus(t01Report, t01Options)).toBe('PASS');
    expect(structuredEvidenceStatus({ ...t01Report, schemaVersion: 2 }, t01Options)).toBe('FAIL');
  });

  it('persists normalized raw reports with reproducible SHA-256 metadata', () => {
    const repoRoot = mkdtempSync(join(tmpdir(), 'frontend-score-report-'));
    temporaryRepositories.push(repoRoot);
    const canonical = '{\n  "a": {\n    "b": 1\n  },\n  "z": 2\n}\n';
    const sha256 = createHash('sha256').update(canonical).digest('hex');
    const result = {
      subjectSha: 'a'.repeat(40),
      rawReports: {
        performance: {
          protocol: 'performance-budget-json-v1',
          sourcePath: 'frontend-app/.tmp/performance-report.json',
          sourceSha256: 'b'.repeat(64),
          sourceBytes: canonical.length,
          sha256,
          bytes: Buffer.byteLength(canonical),
          metrics: [{ metricId: 'P01-render-isolation', threshold: { absoluteUpdateLimit: 8 }, path: 'evidence.metrics.P01-render-isolation' }],
          report: { z: 2, a: { b: 1 } },
        },
      },
    };

    const reportPath = persistScoreReport(result, repoRoot);
    const rawPath = join(repoRoot, '.tmp/frontend-maintainability-score', `${result.subjectSha}.performance.raw.json`);
    expect(readFileSync(rawPath, 'utf8')).toBe(canonical);
    const persisted = JSON.parse(readFileSync(reportPath, 'utf8'));
    expect(persisted.rawReports.performance).toMatchObject({
      sha256,
      bytes: Buffer.byteLength(canonical),
      sourcePath: 'frontend-app/.tmp/performance-report.json',
      sourceSha256: 'b'.repeat(64),
      sourceBytes: canonical.length,
      persistedPath: `.tmp/frontend-maintainability-score/${result.subjectSha}.performance.raw.json`,
      metrics: result.rawReports.performance.metrics,
      report: { a: { b: 1 }, z: 2 },
    });
    const summaryOnly = JSON.parse(JSON.stringify(result));
    delete summaryOnly.rawReports.performance.report;
    expect(() => persistScoreReport(summaryOnly, repoRoot))
      .toThrow('normalized raw report bytes or SHA-256 changed before persistence');
  });

  it('derives Task3 control status from each frozen semantic subset', () => {
    const { controls } = documents();
    const context = inspectTargetRepository();
    for (const [controlId, expectedCases, expectedCount] of [
      ['E03-background-health', ['FM-18'], 1],
      ['E05-safe-recovery', ['FM-16', 'FM-17', 'FM-18'], 3],
      ['C05-provider-rpc-parity', ['FM-07', 'FM-08', 'FM-09', 'FM-10', 'FM-11', 'FM-12', 'FM-13', 'FM-14', 'FM-19', 'FM-20'], 12],
      ['T03-wails-integration', ['FM-01'], 2],
    ]) {
      const control = controls.controls.find(({ id }) => id === controlId);
      const check = control.allOf.find(({ evidenceProtocol }) => evidenceProtocol === 'failure-matrix-report-v1');
      expect(check.caseIds).toEqual(expectedCases);
      expect(check.testCount).toBe(expectedCount);
      const now = Date.now();
      const options = { context, control, check, startedAt: now - 100, finishedAt: now + 30_000 };
      expect(structuredEvidenceStatus(failureMatrixEvidence(context), options), controlId).toBe('PASS');
      if (['E03-background-health', 'E05-safe-recovery'].includes(controlId)) {
        expect(structuredEvidenceStatus(failureMatrixEvidence(context, {
          status: 'partial',
          blockedCases: [{ caseId: 'FM-18', blockedBy: 'Task2A', blocker: 'reconnect recovery is absent' }],
        }), options)).toBe('NOT_VERIFIED');
      }
    }
  }, 30000);

  it('validates P01 production subscription locations by stable value', () => {
    const { controls } = documents();
    const control = controls.controls.find(({ id }) => id === 'P01-render-isolation');
    const check = control.allOf[0];
    const context = createPerformanceTarget(check.runnerFiles);
    const now = Date.now();
    const options = { context, control, check, startedAt: now - 100, finishedAt: now + 100 };
    const { report: valid, baselineDocument } = performanceEvidence(context, check);
    options.baselineDocument = baselineDocument;
    const withSubscriptions = (productionStoreSubscriptions) => ({
      ...valid,
      evidence: {
        ...valid.evidence,
        metrics: {
          ...valid.evidence.metrics,
          'P01-render-isolation': {
            ...valid.evidence.metrics['P01-render-isolation'],
            productionStoreSubscriptions,
          },
        },
      },
    });

    expect(structuredEvidenceStatus(valid, options)).toBe('PASS');
    expect(structuredEvidenceStatus(withSubscriptions(['src/App.jsx:fixture']), options)).toBe('FAIL');
    expect(structuredEvidenceStatus(withSubscriptions([
      { source: 'src/App.jsx', line: 1, column: 1 },
      { source: 'src/App.jsx', line: 1, column: 1 },
    ]), options)).toBe('FAIL');
    expect(structuredEvidenceStatus(withSubscriptions([
      { source: 'src/App.jsx', line: 0, column: 1 },
    ]), options)).toBe('FAIL');
    expect(structuredEvidenceStatus(withSubscriptions([
      { source: 'src/App.jsx', line: 1, column: 1.5 },
    ]), options)).toBe('FAIL');
  }, 30_000);

  it('selects the exact Task4C metric and rejects missing provenance or conflicting case status', () => {
    const { controls } = documents();
    const control = controls.controls.find(({ id }) => id === 'P02-history-budget');
    const check = control.allOf[0];
    const context = createPerformanceTarget(check.runnerFiles);
    const now = Date.now();
    const options = { context, control, check, startedAt: now - 100, finishedAt: now + 100 };
    const { report: valid, baselineDocument } = performanceEvidence(context, check);
    options.baselineDocument = baselineDocument;

    expect(baselineDocument.metrics['P01-render-isolation'].mainPageUpdateCommits).toBe(40);
    expect(structuredEvidenceStatus(valid, options)).toBe('PASS');
    const timingCaseId = check.caseIds[0];
    const mutateCandidateCase = (mutate) => {
      const report = structuredClone(valid);
      mutate(report.evidence.metrics['P02-history-budget'].cases[timingCaseId]);
      return report;
    };
    const mutateBaselineCase = (mutate) => {
      const document = structuredClone(baselineDocument);
      mutate(document.metrics['P02-history-budget'].cases[timingCaseId]);
      return document;
    };
    const malformedCaseMutations = [
      (entry) => {
        for (const key of Object.keys(entry)) delete entry[key];
        Object.assign(entry, {
          attemptsPerSample: 1,
          durationClock: 'test-clock',
          iterationCount: 1,
          durationAttemptSamplesMs: Array.from({ length: 5 }, () => [10]),
          durationSamplesMs: [10, 10, 10, 10, 10],
          durationMedianMs: 10,
        });
      },
      (entry) => { entry.sampleDiagnostics[0].rawNormalizedBlockRatios[0] = 1.3; },
      (entry) => { entry.sampleDiagnostics[0].blockOrders[0] = 'reference-production'; },
      (entry) => { entry.sampleDiagnostics[0].referenceBlockCpuDurationsMs.pop(); },
      (entry) => { entry.normalizedRatioSamples[0] = 1.3; },
      (entry) => { entry.normalizedRatioMedian = 1.3; },
      (entry) => { entry.sampleDiagnostics[0].productionBlockCpuDurationsMs[0] = 0; },
      (entry) => { entry.sampleDiagnostics[0].referenceBlockCpuDurationsMs[0] = Number.NaN; },
    ];
    for (const mutate of malformedCaseMutations) {
      expect(structuredEvidenceStatus(mutateCandidateCase(mutate), options)).toBe('FAIL');
      expect(structuredEvidenceStatus(valid, {
        ...options,
        baselineDocument: mutateBaselineCase(mutate),
      })).toBe('FAIL');
    }
    const overBudget = mutateCandidateCase((entry) => {
      entry.sampleDiagnostics.forEach((sample) => {
        sample.productionBlockCpuDurationsMs.fill(15);
        sample.referenceBlockCpuDurationsMs.fill(10);
        sample.rawNormalizedBlockRatios.fill(1.5);
        sample.normalizedRatio = 1.5;
      });
      entry.normalizedRatioSamples.fill(1.5);
      entry.normalizedRatioMedian = 1.5;
    });
    expect(structuredEvidenceStatus(overBudget, options)).toBe('FAIL');
    expect(structuredEvidenceStatus({
      ...valid,
      evidence: {
        ...valid.evidence,
        metrics: {
          ...valid.evidence.metrics,
          'P01-render-isolation': {
            ...valid.evidence.metrics['P01-render-isolation'],
            mainPageUpdateCommits: 0,
          },
        },
      },
    }, options)).toBe('PASS');
    expect(structuredEvidenceStatus({
      ...valid,
      evidence: {
        ...valid.evidence,
        metrics: {
          ...valid.evidence.metrics,
          'P01-render-isolation': {
            ...valid.evidence.metrics['P01-render-isolation'],
            mainPageUpdateCommits: 2,
          },
        },
      },
    }, options)).toBe('FAIL');
    expect(structuredEvidenceStatus({
      ...valid,
      evidence: { ...valid.evidence, subjectTree: undefined },
    }, options)).toBe('FAIL');
    expect(structuredEvidenceStatus({
      ...valid,
      evidence: { ...valid.evidence, generatedAt: undefined },
    }, options)).toBe('FAIL');
    expect(structuredEvidenceStatus({
      ...valid,
      verdict: {
        ...valid.verdict,
        caseResults: valid.verdict.caseResults.map((entry) => (
          entry.metricId === check.metricId ? { ...entry, status: 'FAIL' } : entry
        )),
      },
    }, options)).toBe('FAIL');
    expect(structuredEvidenceStatus({
      ...valid,
      verdict: {
        ...valid.verdict,
        caseResults: valid.verdict.caseResults.map((entry) => (
          entry.caseId === 'turns-200-tools-1'
            ? { ...entry, metricId: 'P03-feedback-budget' }
            : entry
        )),
      },
    }, options)).toBe('FAIL');
    expect(structuredEvidenceStatus(valid, {
      ...options,
      baselineDocument: { ...baselineDocument, provenance: undefined },
    })).toBe('NOT_VERIFIED');
    expect(structuredEvidenceStatus({
      ...valid,
      evidence: {
        ...valid.evidence,
        metrics: {
          ...valid.evidence.metrics,
          'P02-history-budget': {
            ...valid.evidence.metrics['P02-history-budget'],
            cases: {
              ...valid.evidence.metrics['P02-history-budget'].cases,
              'turns-200-tools-1': {
                ...valid.evidence.metrics['P02-history-budget'].cases['turns-200-tools-1'],
                blockIterationCount: 20,
                iterationCount: 60,
              },
            },
          },
        },
      },
    }, options)).toBe('FAIL');
    expect(structuredEvidenceStatus({
      ...valid,
      evidence: {
        ...valid.evidence,
        provenance: { ...valid.evidence.provenance, runnerContentHash: 'f'.repeat(64) },
      },
    }, options)).toBe('FAIL');
    const forgedCandidateBuild = structuredClone(valid);
    forgedCandidateBuild.evidence.metrics['P04-resource-budget'].candidateBuild.subjectSha = 'f'.repeat(40);
    expect(structuredEvidenceStatus(forgedCandidateBuild, options)).toBe('FAIL');
    expect(structuredEvidenceStatus({
      ...valid,
      evidence: {
        ...valid.evidence,
        environment: { ...valid.evidence.environment, go: '' },
      },
    }, options)).toBe('FAIL');
    const candidateBaseline = {
      ...baselineDocument,
      baseSha: context.subjectSha,
      subjectSha: context.subjectSha,
      subjectTree: context.subjectTree,
      provenance: {
        ...baselineDocument.provenance,
        baselineAudit: {
          ...baselineDocument.provenance.baselineAudit,
          baseSha: context.subjectSha,
          baseTree: context.subjectTree,
        },
      },
    };
    expect(structuredEvidenceStatus(valid, { ...options, baselineDocument: candidateBaseline })).toBe('FAIL');
    expect(structuredEvidenceStatus(valid, {
      ...options,
      baselineDocument: {
        ...baselineDocument,
        baseSha: '895ca09998b2c09a4c6b86a18b5c4ea3f50be8d0',
      },
    })).toBe('FAIL');
    expect(structuredEvidenceStatus(valid, {
      ...options,
      baselineDocument: {
        ...baselineDocument,
        provenance: {
          ...baselineDocument.provenance,
          baselineAudit: {
            ...baselineDocument.provenance.baselineAudit,
            changedPaths: [...baselineDocument.provenance.baselineAudit.changedPaths, 'src/candidate-product.js'],
          },
        },
      },
    })).toBe('FAIL');
    expect(structuredEvidenceStatus(valid, {
      ...options,
      baselineDocument: {
        ...baselineDocument,
        metrics: {
          ...baselineDocument.metrics,
          'P01-render-isolation': {
            ...baselineDocument.metrics['P01-render-isolation'],
            mutationDetected: false,
          },
        },
      },
    })).toBe('FAIL');
  }, 60_000);

  it('fails closed when the three-run measurement audit is forged or incomplete', () => {
    const { controls } = documents();
    const control = controls.controls.find(({ id }) => id === 'P02-history-budget');
    const check = control.allOf[0];
    const context = createPerformanceTarget(check.runnerFiles);
    const now = Date.now();
    const options = { context, control, check, startedAt: now - 100, finishedAt: now + 100 };
    const { report, baselineDocument } = performanceEvidence(context, check);
    const statusAfter = (mutate) => {
      const forged = structuredClone(baselineDocument);
      mutate(forged);
      return structuredEvidenceStatus(report, { ...options, baselineDocument: forged });
    };

    expect(structuredEvidenceStatus(report, { ...options, baselineDocument })).toBe('PASS');
    const mutations = [
      ['measurementAudit extra field', (document) => { document.measurementAudit.forged = true; }],
      ['runCount', (document) => { document.measurementAudit.runCount = 999; }],
      ['designatedRun', (document) => { document.measurementAudit.designatedRun = 999; }],
      ['missing reproduction', (document) => { document.measurementAudit.reproducibilityRuns.pop(); }],
      ['duplicate reproduction', (document) => { document.measurementAudit.reproducibilityRuns[1].run = 2; }],
      ['reordered reproduction', (document) => { document.measurementAudit.reproducibilityRuns.reverse(); }],
      ['unordered timestamp', (document) => {
        document.measurementAudit.reproducibilityRuns[0].generatedAt = document.generatedAt;
      }],
      ['reproduction extra field', (document) => {
        document.measurementAudit.reproducibilityRuns[0].forged = true;
      }],
      ['bindings extra field', (document) => {
        document.measurementAudit.reproducibilityRuns[0].bindings.forged = true;
      }],
      ['subjectSha binding', (document) => {
        document.measurementAudit.reproducibilityRuns[0].bindings.subjectSha = 'f'.repeat(40);
      }],
      ['subjectTree binding', (document) => {
        document.measurementAudit.reproducibilityRuns[0].bindings.subjectTree = 'f'.repeat(40);
      }],
      ['environment binding', (document) => {
        document.measurementAudit.reproducibilityRuns[0].bindings.environment.cpu.model = 'forged';
      }],
      ['BASE detached build hash', (document) => {
        document.metrics['P04-resource-budget'].baseBuild.distManifestHash = 'f'.repeat(64);
      }],
      ['BASE detached build manifest', (document) => {
        document.metrics['P04-resource-budget'].baseBuild.distManifest[0].sha256 = 'f'.repeat(64);
      }],
      ['runnerSha binding', (document) => {
        document.measurementAudit.reproducibilityRuns[0].bindings.runnerSha = 'f'.repeat(40);
      }],
      ['runnerTree binding', (document) => {
        document.measurementAudit.reproducibilityRuns[0].bindings.runnerTree = 'f'.repeat(40);
      }],
      ['bindings runnerContentHash', (document) => {
        document.measurementAudit.reproducibilityRuns[0].bindings.runnerContentHash = 'f'.repeat(64);
      }],
      ['run runnerContentHash', (document) => {
        document.measurementAudit.reproducibilityRuns[0].runnerContentHash = 'f'.repeat(64);
      }],
      ['changedPaths binding', (document) => {
        document.measurementAudit.reproducibilityRuns[0]
          .bindings.changedPaths.push('frontend-app/src/forged.js');
      }],
      ['missing metric', (document) => {
        delete document.measurementAudit.reproducibilityRuns[0].metrics['P04-resource-budget'];
      }],
      ['metric identity', (document) => {
        document.measurementAudit.reproducibilityRuns[0]
          .metrics['P02-history-budget'].metricId = 'P03-feedback-budget';
      }],
      ['metric subject', (document) => {
        document.measurementAudit.reproducibilityRuns[0]
          .metrics['P03-feedback-budget'].subjectSha = 'f'.repeat(40);
      }],
      ['metric raw sample', (document) => {
        document.measurementAudit.reproducibilityRuns[0].metrics['P03-feedback-budget']
          .cases['stop-visible-feedback'].durationSamplesMs[0] = 999;
      }],
      ['designated metric subject', (document) => {
        document.metrics['P01-render-isolation'].subjectSha = 'f'.repeat(40);
      }],
    ];
    for (const [label, mutate] of mutations) {
      expect(statusAfter(mutate), label).toBe('FAIL');
    }
  }, 60_000);

  it('binds every performance control to the audited runner content files', () => {
    const { controls } = documents();
    const baselineDocument = JSON.parse(readFileSync(
      join(scriptRoot, 'frontend-maintainability-baseline.json'),
      'utf8',
    ));
    const auditedRunnerFiles = baselineDocument.provenance.runnerFiles.map(({ path }) => path);
    const nonRunnerFiles = [
      'frontend-app/package.json',
      'frontend-app/src/entities/client/model/contractStoreModel.js',
      'frontend-app/src/entities/client/model/threadLifecycleRuntime.js',
    ];

    expect(auditedRunnerFiles).toContain('frontend-app/src/pages/chat/components/ChatActionFeedback.js');
    for (const path of nonRunnerFiles) expect(auditedRunnerFiles).not.toContain(path);
    for (const controlId of ['P01-render-isolation', 'P02-history-budget', 'P03-feedback-budget', 'P04-resource-budget']) {
      const control = controls.controls.find(({ id }) => id === controlId);
      const check = control.allOf.find(({ evidenceProtocol }) => evidenceProtocol === 'performance-budget-json-v1');
      expect(check.runnerFiles).toEqual(auditedRunnerFiles);
      for (const path of nonRunnerFiles) expect(check.runnerFiles).not.toContain(path);
    }
    expect(performanceAuditPathAllowed('frontend-app/package.json')).toBe(false);
  });

  it('matches the runner codemap audit allowlist without widening the codemap prefix', () => {
    expect(performanceAuditPathAllowed('docs/doc/codemap/README.md')).toBe(true);
    expect(performanceAuditPathAllowed('docs/doc/codemap/ai-index.json')).toBe(true);
    expect(performanceAuditPathAllowed('docs/doc/codemap/project-map/index/app-ui.tsv')).toBe(true);
    expect(performanceAuditPathAllowed('docs/doc/codemap/OTHER.md')).toBe(false);
  });

  // This builds a detached delivery-evidence repository and validates all four T05 commands.
  // Its Vitest budget is independent from, and must not relax, the frozen production T05 checks.
  it('requires Task4C delivery output to bind the subject and all four exact commands', () => {
    const { controls } = documents();
    const control = controls.controls.find(({ id }) => id === 'T05-build-embed-smoke');
    const check = control.allOf[0];
    const context = createDeliveryEvidenceContext();
    const now = Date.now();
    const options = { context, control, check, startedAt: now - 100, finishedAt: now + 100 };
    const valid = deliveryEvidence(context, check);

    expect(structuredEvidenceStatus(valid, options)).toBe('PASS');
    expect(structuredEvidenceStatus({ ...valid, subjectSha: 'f'.repeat(40) }, options)).toBe('FAIL');
    expect(structuredEvidenceStatus({ ...valid, testCount: 0 }, options)).toBe('FAIL');
    expect(structuredEvidenceStatus({
      ...valid,
      verdict: { ...valid.verdict, executedCommands: 0 },
    }, options)).toBe('FAIL');
    expect(structuredEvidenceStatus({
      ...valid,
      provenance: {
        ...valid.provenance,
        runnerFiles: valid.provenance.runnerFiles.map((entry, index) => (
          index === 0 ? { ...entry, sha256: 'f'.repeat(64) } : entry
        )),
      },
    }, options)).toBe('FAIL');
    expect(structuredEvidenceStatus({
      ...valid,
      verdict: { ...valid.verdict, commands: valid.verdict.commands.slice(1) },
    }, options)).toBe('FAIL');
  }, T05_DELIVERY_EVIDENCE_TEST_TIMEOUT_MS);

  it('fails a missing executable command instead of treating it as evidence', async () => {
    await expect(commandEvidenceStatus({
      repoRoot: frozenRepoRoot,
      argv: ['frontend-maintainability-command-does-not-exist'],
    })).resolves.toBe('FAIL');
  });

  it('fails closed when the scored commit range contains whitespace errors', async () => {
    const context = createTargetRepository();
    const scoreBaseSha = context.subjectSha;
    write('candidate.txt', 'trailing whitespace \n', context.repoRoot);
    git(context.repoRoot, ['add', 'candidate.txt']);
    git(context.repoRoot, ['commit', '-q', '-m', '测试：加入空白错误']);
    const invalidSubjectSha = git(context.repoRoot, ['rev-parse', 'HEAD']);

    await expect(commandEvidenceStatus({
      repoRoot: context.repoRoot,
      argv: ['git', 'diff', '--check', scoreBaseSha, invalidSubjectSha],
    })).resolves.toBe('FAIL');

    write('candidate.txt', 'clean whitespace\n', context.repoRoot);
    git(context.repoRoot, ['add', 'candidate.txt']);
    git(context.repoRoot, ['commit', '-q', '-m', '测试：修复空白错误']);
    await expect(commandEvidenceStatus({
      repoRoot: context.repoRoot,
      argv: ['git', 'diff', '--check', scoreBaseSha, git(context.repoRoot, ['rev-parse', 'HEAD'])],
    })).resolves.toBe('PASS');
  });

  it('does not turn a verbose successful command into ENOBUFS failure', async () => {
    await expect(commandEvidenceStatus({
      repoRoot: frozenRepoRoot,
      argv: [process.execPath, '-e', 'process.stdout.write("x".repeat(2 * 1024 * 1024))'],
      timeoutMs: 60_000,
    })).resolves.toBe('PASS');
  });
});

describe('scoring semantics', () => {
  it('rejects FINAL_GATE when P04 is the only failing control', () => {
    const { controls } = documents();
    const result = {
      displayScore: 98,
      dimensions: Object.fromEntries(Object.keys(controls.thresholds.dimensions).map((dimension) => [
        dimension,
        { score: controls.thresholds.dimensions[dimension] },
      ])),
      controls: controls.controls.map((control) => ({
        id: control.id,
        required: control.required,
        status: control.id === 'P04-resource-budget' ? 'FAIL' : 'PASS',
      })),
    };

    expect(finalGateFailures(result)).toEqual([
      'required controls not PASS: P04-resource-budget',
    ]);
  });

  it('requires fresh named terminal behavior evidence', () => {
    const expected = {
      fingerprint: 'current-tree-fingerprint',
      testNames: ['terminal failed behavior', 'terminal stale behavior'],
    };
    const passing = {
      fingerprint: expected.fingerprint,
      testResults: expected.testNames.map((name) => ({ name, status: 'passed' })),
    };

    expect(terminalTruthEvidenceStatus(passing, expected)).toBe('PASS');
    expect(terminalTruthEvidenceStatus({ ...passing, testResults: [] }, expected)).toBe('FAIL');
    expect(terminalTruthEvidenceStatus({ ...passing, testResults: passing.testResults.slice(0, 1) }, expected)).toBe('FAIL');
    expect(terminalTruthEvidenceStatus({
      ...passing,
      testResults: [{ name: expected.testNames[0], status: 'failed' }, passing.testResults[1]],
    }, expected)).toBe('FAIL');
    expect(terminalTruthEvidenceStatus({ ...passing, fingerprint: 'stale-tree-fingerprint' }, expected)).toBe('FAIL');
  });

  it('uses three-state allOf semantics', () => {
    expect(controlStatus([])).toBe('NOT_VERIFIED');
    expect(controlStatus([{ status: 'PASS' }, { status: 'PASS' }])).toBe('PASS');
    expect(controlStatus([{ status: 'PASS' }, { status: 'NOT_VERIFIED' }])).toBe('NOT_VERIFIED');
    expect(controlStatus([{ status: 'PASS' }, { status: 'FAIL' }])).toBe('FAIL');
  });

  it('requires P03 feedback to satisfy both the frozen 1.15x ratio and the absolute 2000ms limit', () => {
    const { controls } = documents();
    const control = controls.controls.find(({ id }) => id === 'P03-feedback-budget');
    const check = control.allOf[0];
    const context = createPerformanceTarget(check.runnerFiles);
    const now = Date.now();
    const options = { context, control, check, startedAt: now - 100, finishedAt: now + 100 };
    const { report, baselineDocument } = performanceEvidence(context, check);
    const setDuration = (metric, duration) => {
      const entry = metric.cases['stop-visible-feedback'];
      entry.durationAttemptSamplesMs = Array.from({ length: 5 }, () => [duration]);
      entry.durationSamplesMs = Array.from({ length: 5 }, () => duration);
      entry.durationMedianMs = duration;
    };
    const baseline = structuredClone(baselineDocument);
    setDuration(baseline.metrics['P03-feedback-budget'], 1800);
    baseline.measurementAudit.reproducibilityRuns.forEach(({ metrics }) => {
      setDuration(metrics['P03-feedback-budget'], 1800);
    });
    const overAbsoluteLimit = structuredClone(report);
    setDuration(overAbsoluteLimit.evidence.metrics['P03-feedback-budget'], 2001);

    expect(2001).toBeLessThanOrEqual(1800 * 1.15);
    expect(structuredEvidenceStatus(overAbsoluteLimit, { ...options, baselineDocument: baseline })).toBe('FAIL');
  }, 60_000);

  it('fails closed when the frontend plan exceeds either frozen size limit', () => {
    expect(frontendPlanSizeStatus('x'.repeat(25_600))).toMatchObject({ status: 'PASS', bytes: 25_600, lines: 1 });
    expect(frontendPlanSizeStatus('x'.repeat(25_601))).toMatchObject({ status: 'FAIL', bytes: 25_601 });
    expect(frontendPlanSizeStatus('x\n'.repeat(501))).toMatchObject({ status: 'FAIL', lines: 501 });
    expect(frontendPlanSizeStatus(`${'x\n'.repeat(500)}x`)).toMatchObject({ status: 'FAIL', lines: 501 });
  });

  it('does not turn zero evidence or confirmed artifact gaps into score', async () => {
    const result = await scoreCurrentTree();
    expect(result.controls.find(({ id }) => id === 'E06-failure-matrix').status).toBe('NOT_VERIFIED');
    expect(result.controls.find(({ id }) => id === 'A04-action-registry').status).toBe('NOT_VERIFIED');
    expect(result.controls.find(({ id }) => id === 'E02-visible-action-error').status).toBe('PASS');
    expect(result.controls.find(({ id }) => id === 'C04-critical-typecheck').status).toBe('NOT_VERIFIED');
    expect(result.controls.find(({ id }) => id === 'T05-build-embed-smoke').status).toBe('NOT_VERIFIED');
    expect(result.controls.find(({ id }) => id === 'E01-terminal-truth').status).toBe('PASS');
    expect(result.displayScore).not.toBe(61.8);
  }, STRUCTURAL_SCORE_TEST_TIMEOUT_MS);
});
