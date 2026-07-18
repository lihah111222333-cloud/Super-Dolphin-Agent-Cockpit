import { execFileSync, spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import {
  copyFileSync,
  mkdtempSync,
  mkdirSync,
  readFileSync,
  readdirSync,
  realpathSync,
  rmSync,
  statSync,
  symlinkSync,
  writeFileSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { afterEach, describe, expect, it } from 'vitest';
import {
  actionProducerGuardOutputStatus,
  commandEvidenceStatus,
  controlStatus,
  inspectTargetRepository,
  performanceAuditPathAllowed,
  probeResult,
  scoreCurrentTree,
  sourceHasCriticalTypecheckGap,
  sourceHasPromptHistoryConsoleOnly,
  structuredEvidenceStatus,
  terminalTruthEvidenceStatus,
  validateConfiguration,
} from './frontend-maintainability-score.mjs';

const scriptRoot = dirname(fileURLToPath(import.meta.url));
const frozenRepoRoot = resolve(scriptRoot, '..', '..');
const scorerPath = join(scriptRoot, 'frontend-maintainability-score.mjs');
const plannedBaseSha = 'b40867229af8e17916c00393639ccb0fcb4bf6fc';
const temporaryRepositories = [];

function documents() {
  return {
    controls: JSON.parse(readFileSync(join(scriptRoot, 'frontend-maintainability-controls.json'), 'utf8')),
    fixtures: JSON.parse(readFileSync(join(scriptRoot, 'frontend-maintainability-red-fixtures.json'), 'utf8')),
  };
}

function git(repoRoot, args) {
  return execFileSync('git', args, { cwd: repoRoot, encoding: 'utf8' }).trim();
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

function createPerformanceTarget(runnerFiles) {
  const repoRoot = mkdtempSync(join(tmpdir(), 'frontend-maintainability-performance-'));
  rmSync(repoRoot, { recursive: true, force: true });
  temporaryRepositories.push(repoRoot);
  execFileSync('git', ['clone', '-q', '--shared', frozenRepoRoot, repoRoot]);
  git(repoRoot, ['checkout', '-q', '--detach', plannedBaseSha]);
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
  execFileSync('git', ['clone', '-q', '--shared', frozenRepoRoot, repoRoot]);
  git(repoRoot, ['config', 'user.email', 'scorer-test@example.invalid']);
  git(repoRoot, ['config', 'user.name', 'Scorer Test']);
  for (const name of [
    'frontend-maintainability-controls.json',
    'frontend-maintainability-score.mjs',
    'frontend-maintainability-baseline.json',
    'frontend-maintainability-red-fixtures.json',
  ]) {
    copyFileSync(join(scriptRoot, name), join(repoRoot, 'frontend-app', 'scripts', name));
  }
  write('frontend-app/scorer-final-subject.txt', 'strict descendant\n', repoRoot);
  git(repoRoot, ['add', '.']);
  git(repoRoot, ['commit', '-q', '-m', '测试：建立最终评分后代']);
  return {
    repoRoot,
    subjectSha: git(repoRoot, ['rev-parse', 'HEAD']),
  };
}

function createFinalCliFixture() {
  const baseRoot = mkdtempSync(join(tmpdir(), 'frontend-maintainability-final-cli-base-'));
  rmSync(baseRoot, { recursive: true, force: true });
  temporaryRepositories.push(baseRoot);
  execFileSync('git', ['clone', '-q', '--shared', frozenRepoRoot, baseRoot]);
  git(baseRoot, ['config', 'user.email', 'scorer-test@example.invalid']);
  git(baseRoot, ['config', 'user.name', 'Scorer Test']);
  for (const name of [
    'frontend-maintainability-controls.json',
    'frontend-maintainability-score.mjs',
    'frontend-maintainability-baseline.json',
    'frontend-maintainability-red-fixtures.json',
  ]) {
    copyFileSync(join(scriptRoot, name), join(baseRoot, 'frontend-app', 'scripts', name));
  }
  const sourceNodeModules = join(frozenRepoRoot, 'frontend-app', 'node_modules');
  const baseNodeModules = join(baseRoot, 'frontend-app', 'node_modules');
  mkdirSync(baseNodeModules);
  for (const entry of readdirSync(sourceNodeModules)) {
    const sourcePath = join(sourceNodeModules, entry);
    symlinkSync(sourcePath, join(baseNodeModules, entry), statSync(sourcePath).isDirectory() ? 'dir' : 'file');
  }
  write('frontend-app/scorer-freeze-marker.txt', 'frozen scorer fixture\n', baseRoot);
  git(baseRoot, ['add', '.']);
  git(baseRoot, ['commit', '-q', '-m', '测试：冻结最终评分器']);
  const scoreBaseSha = git(baseRoot, ['rev-parse', 'HEAD']);

  const subjectRoot = mkdtempSync(join(tmpdir(), 'frontend-maintainability-final-cli-subject-'));
  rmSync(subjectRoot, { recursive: true, force: true });
  execFileSync('git', ['worktree', 'add', '-q', '--detach', subjectRoot, scoreBaseSha], { cwd: baseRoot });
  write('frontend-app/final-subject.txt', 'strict descendant\n', subjectRoot);
  write('go.mod', 'invalid final fixture\n', subjectRoot);
  const packageDocument = JSON.parse(readFileSync(join(subjectRoot, 'frontend-app', 'package.json'), 'utf8'));
  packageDocument.scripts.lint = 'false';
  packageDocument.scripts.test = 'false';
  packageDocument.scripts.build = 'false';
  write('frontend-app/package.json', `${JSON.stringify(packageDocument, null, 2)}\n`, subjectRoot);
  write(
    'frontend-app/scripts/detached-mount-probe.test.mjs',
    "import { expect, it } from 'vitest';\nit('executes through detached dependencies', () => expect(true).toBe(true));\n",
    subjectRoot,
  );
  write('Makefile', 'frontend-embed-verify:\n\t@false\n', subjectRoot);
  write('frontend-app/scripts/delivery-smoke-runner.mjs', [
    "import { spawnSync } from 'node:child_process';",
    "import { lstatSync, writeFileSync } from 'node:fs';",
    "import process from 'node:process';",
    "import { dirname, resolve } from 'node:path';",
    "import { fileURLToPath } from 'node:url';",
    '',
    'const SCRIPT_PATH = fileURLToPath(import.meta.url);',
    "const FRONTEND_ROOT = resolve(dirname(SCRIPT_PATH), '..');",
    "const REPO_ROOT = resolve(FRONTEND_ROOT, '..');",
    '',
    'if (process.argv[1] && resolve(process.argv[1]) === SCRIPT_PATH) {',
    '  const proofPath = process.env.SCORER_DETACHED_MOUNT_PROOF;',
    "  if (!proofPath) throw new Error('SCORER_DETACHED_MOUNT_PROOF is required');",
    "  const gitStatus = spawnSync('git', ['status', '--porcelain', '--untracked-files=all'], { cwd: REPO_ROOT, encoding: 'utf8' });",
    '  const vitest = spawnSync(process.execPath, [',
    "    resolve(FRONTEND_ROOT, 'node_modules', 'vitest', 'vitest.mjs'),",
    "    'run', 'scripts/detached-mount-probe.test.mjs', '--reporter=json', '--no-file-parallelism', '--maxWorkers=1',",
    "  ], { cwd: FRONTEND_ROOT, encoding: 'utf8' });",
    "  const nodeModules = lstatSync(resolve(FRONTEND_ROOT, 'node_modules'));",
    '  const proof = {',
    '    gitStatusExitCode: gitStatus.status,',
    '    gitStatus: gitStatus.stdout,',
    '    nodeModulesIsDirectory: nodeModules.isDirectory(),',
    '    nodeModulesIsSymbolicLink: nodeModules.isSymbolicLink(),',
    '    vitestExitCode: vitest.status,',
    '    vitestStderr: vitest.stderr,',
    '    vitestStdout: vitest.stdout,',
    '  };',
    '  writeFileSync(proofPath, JSON.stringify(proof));',
    '  const vitestReport = JSON.parse(vitest.stdout);',
    '  writeFileSync(proofPath, JSON.stringify({ ...proof, vitestPassedTests: vitestReport.numPassedTests }));',
    "  process.stdout.write('{}\\n');",
    '}',
    '',
  ].join('\n'), subjectRoot);
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
  const evidence = Object.entries(layersByCase).flatMap(([caseId, layers]) => (
    layers.map((layer) => ({ caseId, layer, test: `${caseId}:${layer}` }))
  ));
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
    ...overrides,
  };
}

function performanceEvidence(context, check, metricStatus = 'PASS', overrides = {}) {
  const casesByMetric = {
    'P01-render-isolation': ['render-main-page-update-commits', 'render-unrelated-subtree-update-commits', 'render-broad-subscription-mutation-detected'],
    'P02-history-budget': ['turns-200-tools-1', 'turns-200-tools-3', 'turns-1000-tools-1', 'turns-1000-tools-3', 'turns-5000-tools-1', 'turns-5000-tools-3'],
    'P03-feedback-budget': ['stop-visible-feedback'],
    'P04-resource-budget': ['bundle-total-bytes', 'bundle-max-chunk-bytes'],
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
      ...(frozen ? { status: 'PASS', maxRegressionRatio: 1.05 } : {}),
      metricId: 'P04-resource-budget',
      subjectSha,
      fileCount: 1,
      totalBundleBytes: 100,
      maxChunkBytes: 100,
      files: [{ path: 'assets/index.js', bytes: 100 }],
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
  return {
    schemaVersion: 1,
    generatedAt: new Date().toISOString(),
    subjectSha: context.subjectSha,
    subjectTree: context.subjectTree,
    metricId: 'T05-build-embed-smoke',
    caseIds: [...check.caseIds],
    testCount: check.testCount,
    verdict: {
      status: 'PASS',
      reason: '',
      commands: check.caseIds.map((id) => ({ id, status: 'PASS' })),
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

    const mutablePerformanceCLI = documents();
    mutablePerformanceCLI.controls.controls.find(({ id }) => id === 'P02-history-budget')
      .allOf[0].argv.push('--json');
    expect(() => validateConfiguration(mutablePerformanceCLI.controls, mutablePerformanceCLI.fixtures))
      .toThrow('performance budget argv differs from frozen contract');

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
    expect(JSON.stringify(controls)).not.toContain('notImplemented');
    expect(JSON.stringify(controls)).not.toMatch(/frontend-[a-z-]+-evidence\.mjs/u);
    expect(controls.controls.find(({ id }) => id === 'E06-failure-matrix').allOf[0].argv)
      .toEqual(['node', 'frontend-app/scripts/failure-matrix-runner.mjs']);
    expect(controls.controls.find(({ id }) => id === 'A04-action-registry').allOf[1].argv)
      .toEqual(['node', 'scripts/action-producer-guard.mjs']);
    expect(controls.controls.find(({ id }) => id === 'P01-render-isolation').allOf[0].argv)
      .toEqual(['node', 'scripts/performance-budget-runner.mjs', '--verify', '--subject', '$SUBJECT_SHA', '--baseline', '$FROZEN_BASELINE']);
    expect(controls.controls.find(({ id }) => id === 'T05-build-embed-smoke').allOf[0].argv)
      .toEqual(['node', 'scripts/delivery-smoke-runner.mjs', '--verify', '--subject', '$SUBJECT_SHA']);
    expect(JSON.parse(readFileSync(join(scriptRoot, 'frontend-maintainability-baseline.json'), 'utf8')).baseSha)
      .toBe(plannedBaseSha);
    const backgroundHealth = controls.controls.find(({ id }) => id === 'E03-background-health');
    expect(backgroundHealth.allOf).toHaveLength(3);
    expect(backgroundHealth.allOf.map(({ argv }) => argv)).toEqual([
      ['node', 'node_modules/vitest/vitest.mjs', 'run', 'src/shared/diagnostics/frontendHealthStore.test.js', '--reporter=json', '--no-file-parallelism', '--maxWorkers=1'],
      ['node', 'node_modules/vitest/vitest.mjs', 'run', 'src/shared/ui/productionActionFailureMatrix.test.js', '--reporter=json', '--no-file-parallelism', '--maxWorkers=1'],
      ['node', 'frontend-app/scripts/failure-matrix-runner.mjs'],
    ]);
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

  it('keeps the repaired terminal and visible-action truth bound to current executable probes', () => {
    expect(sourceHasPromptHistoryConsoleOnly()).toBe(false);
    expect(probeResult('terminalTruth')).toBe('PASS');
    expect(probeResult('visibleActionError')).toBe('PASS');
    expect(probeResult('actionProducerGuard')).toBe('PASS');
  }, 45_000);

  it('enforces exact CLI forms', () => {
    expect(cli(['--validate'])).toMatchObject({ status: 0 });
    expect(cli(['--validate', 'extra']).status).not.toBe(0);
    expect(cli(['--probe']).status).not.toBe(0);
    expect(cli(['--score', '--run', '--run']).status).not.toBe(0);
    expect(cli(['--final', '--repo', frozenRepoRoot]).status).not.toBe(0);
  });
});

describe('frozen scorer target binding', () => {
  it('scores another Git target without loading a scorer from that target', () => {
    const target = createTargetRepository();
    const result = scoreCurrentTree({ repoRoot: target.repoRoot, subjectSha: target.subjectSha });

    expect(result.subjectSha).toBe(target.subjectSha);
    expect(result.subjectTree).toBe(target.subjectTree);
    expect(result.controls).toHaveLength(25);
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
    const context = inspectTargetRepository({
      repoRoot: target.repoRoot,
      subjectSha: target.subjectSha,
      requireClean: true,
      requireFinalContract: true,
    });

    expect(context.subjectSha).toBe(target.subjectSha);
    expect(context.scoreBaseSha).toBe(git(frozenRepoRoot, ['rev-parse', 'HEAD']));

    write('frontend-app/scripts/frontend-maintainability-score.mjs', '// drift\n', target.repoRoot);
    git(target.repoRoot, ['add', '.']);
    git(target.repoRoot, ['commit', '-q', '-m', '测试：制造治理漂移']);
    expect(() => inspectTargetRepository({
      repoRoot: target.repoRoot,
      subjectSha: git(target.repoRoot, ['rev-parse', 'HEAD']),
      requireClean: true,
      requireFinalContract: true,
    })).toThrow('frozen governance drift');
  }, 30_000);

  it('keeps the canonical detached dependency mount Git-clean and Vitest-executable', () => {
    const fixture = createFinalCliFixture();
    try {
      const tmpAlias = detachedTmpAlias();
      const proofRoot = mkdtempSync(join(tmpdir(), 'frontend-maintainability-detached-proof-'));
      const proofPath = join(proofRoot, 'proof.json');
      temporaryRepositories.push(proofRoot);
      if (process.platform === 'darwin') {
        expect(tmpAlias).toMatch(/^\/var(?:\/|$)/u);
        expect(realpathSync(tmpAlias)).toMatch(/^\/private\/var(?:\/|$)/u);
      }
      const result = spawnSync(process.execPath, [
        join(fixture.baseRoot, 'frontend-app', 'scripts', 'frontend-maintainability-score.mjs'),
        '--final',
        '--repo', fixture.subjectRoot,
        '--subject', fixture.subjectSha,
      ], {
        cwd: join(fixture.baseRoot, 'frontend-app'),
        encoding: 'utf8',
        env: {
          ...process.env,
          SCORER_DETACHED_MOUNT_PROOF: proofPath,
          TMPDIR: tmpAlias,
        },
      });

      expect(result.status).toBe(1);
      expect(result.stdout).toContain(`SCORE_BASE\t${fixture.scoreBaseSha}`);
      expect(result.stdout).toMatch(new RegExp(`^SCORE\\t\\d+\\.\\d\\t${fixture.subjectSha}$`, 'mu'));
      expect(result.stdout).toContain('REPORT\t');
      expect(result.stderr).toContain('FINAL_GATE\tFAIL');
      const reportPath = result.stdout.match(/^REPORT\t(.+)$/mu)?.[1];
      expect(reportPath).toBeTruthy();
      temporaryRepositories.push(dirname(reportPath));
      const report = JSON.parse(readFileSync(reportPath, 'utf8'));
      const mountProof = JSON.parse(readFileSync(proofPath, 'utf8'));
      expect(mountProof).toMatchObject({
        gitStatusExitCode: 0,
        gitStatus: '',
        nodeModulesIsDirectory: true,
        nodeModulesIsSymbolicLink: false,
        vitestExitCode: 0,
        vitestPassedTests: 1,
      });
      const deliveryEvidence = report.controls.find(({ id }) => id === 'T05-build-embed-smoke').evidence[0];
      expect(deliveryEvidence.summary).not.toContain('runner report is not valid JSON');
      expect(deliveryEvidence.summary).toBe('runner report schemaVersion must equal 1');
    }
    finally {
      execFileSync('git', ['worktree', 'remove', '--force', fixture.subjectRoot], { cwd: fixture.baseRoot });
    }
  }, 180_000);
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

  it('keeps resolved legacy artifact probes read-only and unregistered until real lane evidence exists', () => {
    expect(sourceHasPromptHistoryConsoleOnly()).toBe(false);
    expect(sourceHasCriticalTypecheckGap()).toBe(false);
    expect(probeResult('promptHistoryVisibleError')).toBe('NOT_VERIFIED');
    expect(probeResult('criticalTypecheck')).toBe('NOT_VERIFIED');
  });

  it('keeps an unregistered redMatrix NOT_VERIFIED and accepts the exact action runner', () => {
    expect(probeResult('redMatrix')).toBe('NOT_VERIFIED');
    expect(probeResult('actionProducerGuard')).toBe('PASS');
  }, 15_000);

  it('rejects stale, mismatched, zero-test, wrong-control, and wrong-case Task3 evidence', () => {
    const { controls } = documents();
    const control = controls.controls.find(({ id }) => id === 'E06-failure-matrix');
    const check = control.allOf[0];
    const context = inspectTargetRepository();
    const now = Date.now();
    const options = { context, control, check, startedAt: now - 100, finishedAt: now + 100 };
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

  it('derives Task3 control status from each frozen semantic subset', () => {
    const { controls } = documents();
    const context = inspectTargetRepository();
    const now = Date.now();
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
      const options = { context, control, check, startedAt: now - 100, finishedAt: now + 100 };
      expect(structuredEvidenceStatus(failureMatrixEvidence(context), options)).toBe('PASS');
      if (['E03-background-health', 'E05-safe-recovery'].includes(controlId)) {
        expect(structuredEvidenceStatus(failureMatrixEvidence(context, {
          status: 'partial',
          blockedCases: [{ caseId: 'FM-18', blockedBy: 'Task2A', blocker: 'reconnect recovery is absent' }],
        }), options)).toBe('NOT_VERIFIED');
      }
    }
  });

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

    expect(auditedRunnerFiles).not.toContain('frontend-app/package.json');
    for (const controlId of ['P01-render-isolation', 'P02-history-budget', 'P03-feedback-budget', 'P04-resource-budget']) {
      const control = controls.controls.find(({ id }) => id === controlId);
      const check = control.allOf.find(({ evidenceProtocol }) => evidenceProtocol === 'performance-budget-json-v1');
      expect(check.runnerFiles).toEqual(auditedRunnerFiles);
    }
    expect(performanceAuditPathAllowed('frontend-app/package.json')).toBe(true);
  });

  it('matches the runner codemap audit allowlist without widening the codemap prefix', () => {
    expect(performanceAuditPathAllowed('docs/doc/codemap/README.md')).toBe(true);
    expect(performanceAuditPathAllowed('docs/doc/codemap/ai-index.json')).toBe(true);
    expect(performanceAuditPathAllowed('docs/doc/codemap/project-map/index/app-ui.tsv')).toBe(true);
    expect(performanceAuditPathAllowed('docs/doc/codemap/OTHER.md')).toBe(false);
  });

  it('requires Task4C delivery output to bind the subject and all four exact commands', () => {
    const { controls } = documents();
    const control = controls.controls.find(({ id }) => id === 'T05-build-embed-smoke');
    const check = control.allOf[0];
    const context = inspectTargetRepository();
    const now = Date.now();
    const options = { context, control, check, startedAt: now - 100, finishedAt: now + 100 };
    const valid = deliveryEvidence(context, check);

    expect(structuredEvidenceStatus(valid, options)).toBe('PASS');
    expect(structuredEvidenceStatus({ ...valid, subjectSha: 'f'.repeat(40) }, options)).toBe('FAIL');
    expect(structuredEvidenceStatus({ ...valid, testCount: 0 }, options)).toBe('FAIL');
    expect(structuredEvidenceStatus({
      ...valid,
      verdict: { ...valid.verdict, commands: valid.verdict.commands.slice(1) },
    }, options)).toBe('FAIL');
  });

  it('fails a missing executable command instead of treating it as evidence', () => {
    expect(commandEvidenceStatus({
      repoRoot: frozenRepoRoot,
      argv: ['frontend-maintainability-command-does-not-exist'],
    })).toBe('FAIL');
  });

  it('does not turn a verbose successful command into ENOBUFS failure', () => {
    expect(commandEvidenceStatus({
      repoRoot: frozenRepoRoot,
      argv: [process.execPath, '-e', 'process.stdout.write("x".repeat(2 * 1024 * 1024))'],
    })).toBe('PASS');
  });
});

describe('scoring semantics', () => {
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

  it('does not turn zero evidence or confirmed artifact gaps into score', () => {
    const result = scoreCurrentTree();
    expect(result.controls.find(({ id }) => id === 'E06-failure-matrix').status).toBe('NOT_VERIFIED');
    expect(result.controls.find(({ id }) => id === 'A04-action-registry').status).toBe('NOT_VERIFIED');
    expect(result.controls.find(({ id }) => id === 'E02-visible-action-error').status).toBe('PASS');
    expect(result.controls.find(({ id }) => id === 'C04-critical-typecheck').status).toBe('NOT_VERIFIED');
    expect(result.controls.find(({ id }) => id === 'T05-build-embed-smoke').status).toBe('NOT_VERIFIED');
    expect(result.controls.find(({ id }) => id === 'E01-terminal-truth').status).toBe('PASS');
    expect(result.displayScore).not.toBe(61.8);
  }, 150_000);
});
