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
];
const vitestProbes = new Set(['terminalTruth', 'publicError', 'terminalSsot', 'strictRpc']);
const artifactProbes = new Set(['promptHistoryVisibleError', 'criticalTypecheck']);
const plannedThresholds = { overall: 90, dimensions: { E: 90, A: 85, C: 85, T: 85, P: 80 } };
const requiredDoDControls = new Set(['E06-failure-matrix', 'C05-provider-rpc-parity', 'T05-build-embed-smoke']);
const plannedStructuredRunners = {
  'E03-background-health': ['backgroundHealth', ['node', 'scripts/frontend-background-health-evidence.mjs', '--json']],
  'E05-safe-recovery': ['safeRecovery', ['node', 'scripts/frontend-safe-recovery-evidence.mjs', '--json']],
  'E06-failure-matrix': ['failureMatrix', ['node', 'scripts/frontend-failure-matrix-evidence.mjs', '--json']],
  'A02-state-ownership': ['stateOwnership', ['node', 'scripts/frontend-state-ownership-guard.mjs', '--evidence-json']],
  'A03-dependency-direction': ['dependencyDirection', ['node', 'scripts/frontend-dependency-direction-guard.mjs', '--evidence-json']],
  'A04-action-registry': ['actionRegistry', ['node', 'scripts/frontend-action-registry-guard.mjs', '--evidence-json']],
  'A05-generated-boundary': ['generatedBoundary', ['node', 'scripts/frontend-generated-boundary-guard.mjs', '--evidence-json']],
  'C03-public-error-contract': ['publicErrorContract', ['node', 'scripts/frontend-public-error-contract-evidence.mjs', '--json']],
  'C05-provider-rpc-parity': ['providerRpcParity', ['node', 'scripts/provider-rpc-parity-evidence.mjs', '--json']],
  'T01-red-green-regression': ['redGreenRegression', ['node', 'scripts/frontend-red-green-regression-evidence.mjs', '--json']],
  'T02-critical-action-coverage': ['criticalActionCoverage', ['node', 'scripts/frontend-critical-action-coverage-evidence.mjs', '--json']],
  'T03-wails-integration': ['wailsIntegration', ['node', 'scripts/desktop-failure-smoke.mjs', '--evidence-json', '--control', 'T03-wails-integration']],
  'T05-build-embed-smoke': ['desktopFailureSmoke', ['node', 'scripts/desktop-failure-smoke.mjs', '--evidence-json', '--control', 'T05-build-embed-smoke']],
  'P01-render-isolation': ['renderIsolation', ['node', 'scripts/frontend-render-isolation-benchmark.mjs', '--verify', '--json']],
  'P02-history-budget': ['historyBudget', ['node', 'scripts/chat-history-benchmark.mjs', '--verify', '--json']],
  'P03-feedback-budget': ['feedbackBudget', ['node', 'scripts/frontend-feedback-benchmark.mjs', '--verify', '--json']],
  'P04-resource-budget': ['resourceBudget', ['node', 'scripts/frontend-resource-budget.mjs', '--verify', '--json']],
};
const plannedMetricContracts = {
  'P01-render-isolation': {
    mainPageCommits: { operator: 'lte', unit: 'commits', minSamples: 20, baselineMultiplier: 1, absoluteMax: 1 },
    unrelatedSubtreeCommits: { operator: 'lte', unit: 'commits', minSamples: 20, baselineMultiplier: 1, absoluteMax: 1 },
  },
  'P02-history-budget': {
    history200MedianMs: { operator: 'lte', unit: 'ms', minSamples: 5, baselineMultiplier: 1.15 },
    history1000MedianMs: { operator: 'lte', unit: 'ms', minSamples: 5, baselineMultiplier: 1.15 },
    history5000MedianMs: { operator: 'lte', unit: 'ms', minSamples: 5, baselineMultiplier: 1.15 },
  },
  'P03-feedback-budget': {
    feedbackMedianMs: { operator: 'lte', unit: 'ms', minSamples: 5, baselineMultiplier: 1.15 },
  },
  'P04-resource-budget': {
    bundleBytes: { operator: 'lte', unit: 'bytes', minSamples: 1, baselineMultiplier: 1.05 },
    maxChunkBytes: { operator: 'lte', unit: 'bytes', minSamples: 1, baselineMultiplier: 1.05 },
    heapBytes: { operator: 'lte', unit: 'bytes', minSamples: 1, baselineMultiplier: 1.05 },
  },
};

function readFrozenJSON(name) {
  return JSON.parse(fs.readFileSync(path.join(frozenScriptRoot, name), 'utf8'));
}

function fail(message) {
  throw new Error(message);
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

function validateEvidenceTests(evidence, check) {
  if (!Array.isArray(evidence.tests) || evidence.tests.length !== check.testCount || evidence.tests.length === 0) {
    fail('structured evidence tests exact count mismatch');
  }
  const names = [];
  const coveredCases = new Set();
  for (const test of evidence.tests) {
    exactKeys(test, ['caseId', 'name', 'status'], 'structured evidence test');
    if (!check.caseIds.includes(test.caseId)) fail(`structured evidence unknown caseId: ${test.caseId}`);
    if (typeof test.name !== 'string' || test.name.length === 0) fail('structured evidence test name is empty');
    if (!['passed', 'failed'].includes(test.status)) fail(`structured evidence invalid test status: ${test.status}`);
    names.push(test.name);
    coveredCases.add(test.caseId);
  }
  if (new Set(names).size !== names.length) fail('structured evidence test names must be unique');
  exactSet(coveredCases, check.caseIds, 'structured evidence covered cases');
  return evidence.tests.every(({ status }) => status === 'passed') ? 'PASS' : 'FAIL';
}

export function performanceMetricStatus(evidenceMetrics, check, baselineDocument = baseline) {
  const formulas = check.metrics;
  if (!Array.isArray(evidenceMetrics) || !formulas || typeof formulas !== 'object') return 'FAIL';
  const expectedNames = Object.keys(formulas);
  if (evidenceMetrics.length !== expectedNames.length) return 'FAIL';
  const metricsByName = new Map();
  try {
    for (const metric of evidenceMetrics) {
      exactKeys(metric, ['name', 'value', 'unit', 'sampleCount'], 'structured metric evidence item');
      if (!expectedNames.includes(metric.name) || metricsByName.has(metric.name)
        || !Number.isFinite(metric.value) || metric.value < 0
        || typeof metric.unit !== 'string'
        || !Number.isInteger(metric.sampleCount) || metric.sampleCount <= 0) {
        return 'FAIL';
      }
      metricsByName.set(metric.name, metric);
    }
    exactSet(metricsByName.keys(), expectedNames, 'structured metric evidence names');
  }
  catch {
    return 'FAIL';
  }

  const references = baselineDocument.metrics?.[check.baselineMetricKey]?.references;
  if (references === undefined) return 'NOT_VERIFIED';
  try {
    exactSet(Object.keys(references), expectedNames, 'frozen metric references');
    for (const name of expectedNames) {
      const formula = formulas[name];
      const metric = metricsByName.get(name);
      const reference = references[name];
      exactKeys(reference, ['value', 'unit'], `frozen metric reference ${name}`);
      if (formula.operator !== 'lte' || !Number.isFinite(formula.baselineMultiplier)
        || formula.baselineMultiplier <= 0 || !Number.isFinite(reference.value) || reference.value < 0
        || reference.unit !== formula.unit || metric.unit !== formula.unit
        || metric.sampleCount < formula.minSamples) {
        return 'FAIL';
      }
      const baselineLimit = reference.value * formula.baselineMultiplier;
      const limit = Number.isFinite(formula.absoluteMax)
        ? Math.min(baselineLimit, formula.absoluteMax)
        : baselineLimit;
      if (metric.value > limit) return 'FAIL';
    }
  }
  catch {
    return 'FAIL';
  }
  return 'PASS';
}

function validateStructuredEvidence(evidence, { context, control, check, startedAt, finishedAt }) {
  const expectedKeys = [
    'schemaVersion', 'subjectSha', 'subjectTree', 'controlId', 'caseIds', 'testCount',
    'tests', 'generatedAt', 'environment',
  ];
  if (check.evidenceProtocol === 'metric-json-v1') expectedKeys.push('metrics');
  exactKeys(evidence, expectedKeys, 'structured evidence');
  if (evidence.schemaVersion !== 1) fail('structured evidence schemaVersion must equal 1');
  if (evidence.subjectSha !== context.subjectSha || evidence.subjectTree !== context.subjectTree) {
    fail('structured evidence subject binding mismatch');
  }
  if (evidence.controlId !== control.id) fail('structured evidence controlId mismatch');
  if (!Array.isArray(evidence.caseIds)) fail('structured evidence caseIds must be an array');
  exactSet(evidence.caseIds, check.caseIds, 'structured evidence caseIds');
  if (evidence.testCount !== check.testCount || !Number.isInteger(evidence.testCount) || evidence.testCount <= 0) {
    fail('structured evidence testCount mismatch');
  }
  const generatedAt = Date.parse(evidence.generatedAt);
  if (!Number.isFinite(generatedAt) || generatedAt < startedAt - 5_000 || generatedAt > finishedAt + 5_000) {
    fail('structured evidence is stale or has an invalid generatedAt');
  }
  exactKeys(evidence.environment, ['node', 'platform', 'arch'], 'structured evidence environment');
  if (![evidence.environment.node, evidence.environment.platform, evidence.environment.arch]
    .every((value) => typeof value === 'string' && value.length > 0)) {
    fail('structured evidence environment is incomplete');
  }
  const testStatus = validateEvidenceTests(evidence, check);
  if (testStatus !== 'PASS') return testStatus;
  return check.evidenceProtocol === 'metric-json-v1'
    ? performanceMetricStatus(evidence.metrics, check)
    : 'PASS';
}

export function structuredEvidenceStatus(evidence, options) {
  try {
    return validateStructuredEvidence(evidence, options);
  }
  catch {
    return 'FAIL';
  }
}

function commandResult(command, args, options) {
  const result = spawnSync(command, args, {
    cwd: options.cwd,
    encoding: 'utf8',
    env: options.env,
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

function structuredProbe(context, control, check) {
  const [command, runnerPath, ...args] = check.argv;
  const cwd = path.resolve(context.repoRoot, check.cwd);
  const absoluteRunnerPath = path.resolve(cwd, runnerPath);
  if (!fs.existsSync(absoluteRunnerPath)) {
    return evidenceRecord(context, control, check, {
      status: 'NOT_VERIFIED',
      exitCode: null,
      summary: `target evidence runner is missing: ${path.join(check.cwd, runnerPath)}`,
    });
  }
  if (!fs.lstatSync(absoluteRunnerPath).isFile()) {
    return evidenceRecord(context, control, check, {
      status: 'FAIL',
      exitCode: 1,
      summary: `target evidence runner must be a regular file: ${path.join(check.cwd, runnerPath)}`,
    });
  }
  const startedAt = Date.now();
  const result = commandResult(command === 'node' ? process.execPath : command, [absoluteRunnerPath, ...args], {
    cwd,
    timeoutMs: check.timeoutMs,
    env: process.env,
  });
  const finishedAt = Date.now();
  const output = `${result.stdout}${result.stderr}`.trim();
  if (result.error || result.exitCode !== 0) {
    return evidenceRecord(context, control, check, {
      status: 'FAIL',
      exitCode: result.exitCode,
      summary: output.slice(-1200) || result.error?.message || 'target evidence runner failed',
    });
  }
  let structured;
  try {
    structured = JSON.parse(result.stdout.trim());
  }
  catch (error) {
    return evidenceRecord(context, control, check, {
      status: 'FAIL',
      exitCode: 1,
      summary: `target evidence output is not a single JSON object: ${error.message}`,
    });
  }
  let status;
  try {
    status = validateStructuredEvidence(structured, { context, control, check, startedAt, finishedAt });
  }
  catch (error) {
    return evidenceRecord(context, control, check, { status: 'FAIL', exitCode: 1, summary: error.message });
  }
  return evidenceRecord(context, control, check, {
    status,
    exitCode: status === 'PASS' ? 0 : status === 'FAIL' ? 1 : null,
    summary: status === 'NOT_VERIFIED'
      ? 'performance evidence is valid, but frozen baseline references are absent'
      : `${structured.testCount} exact structured tests ${status === 'PASS' ? 'passed' : 'failed'}`,
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
    if (['test-json-v1', 'metric-json-v1'].includes(check.evidenceProtocol)) {
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
    if (['test-json-v1', 'metric-json-v1'].includes(check.evidenceProtocol) && !runCommands) {
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
  const planned = plannedStructuredRunners[control.id];
  if (!planned) fail(`unregistered structured evidence runner: ${control.id}`);
  const [expectedProbe, expectedArgv] = planned;
  const expectedProtocol = control.dimension === 'P' ? 'metric-json-v1' : 'test-json-v1';
  if (check.probe !== expectedProbe || check.evidenceProtocol !== expectedProtocol
    || JSON.stringify(check.argv) !== JSON.stringify(expectedArgv)) {
    fail(`structured evidence runner differs from frozen contract: ${control.id}`);
  }
  if (expectedProtocol !== 'metric-json-v1') return;
  if (check.baselineMetricKey !== control.id || !Object.hasOwn(baseline.metrics || {}, control.id)
    || JSON.stringify(check.metrics) !== JSON.stringify(plannedMetricContracts[control.id])) {
    fail(`performance formula differs from frozen contract: ${control.id}`);
  }
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
  for (const caseID of check.caseIds) {
    if (!caseID.startsWith('frontend-') && !frozenFixtureIDs.has(caseID)) fail(`missing fixture case: ${caseID}`);
  }
  if (check.kind !== 'probe') return;
  if (vitestProbes.has(check.probe)) validateVitestCheck(control, check);
  else if (artifactProbes.has(check.probe)) validateArtifactCheck(control, check);
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
  if (!/^[0-9a-f]{40}$/.test(baseline.baseSha) || !/^[0-9a-f]{40}$/.test(baseline.planSnapshotSha)) {
    fail('baseline provenance is incomplete');
  }
  return true;
}
function scoreContext(context, { runCommands }) {
  const scoredControls = controls.controls.map((control) => {
    const evidence = control.allOf.map((check) => evaluateCheck(context, control, check, runCommands));
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

function withDetachedSubject(context, callback) {
  const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), 'frontend-maintainability-subject-'));
  const detachedRoot = path.join(tempRoot, 'repo');
  execFileSync('git', ['worktree', 'add', '--detach', detachedRoot, context.subjectSha], {
    cwd: context.repoRoot,
    stdio: 'ignore',
  });
  try {
    const executionContext = contextForExecution(context, detachedRoot);
    const dependencies = dependencySource(context);
    const detachedNodeModules = path.join(executionContext.appRoot, 'node_modules');
    if (dependencies && !fs.existsSync(detachedNodeModules)) {
      fs.symlinkSync(path.join(dependencies, 'node_modules'), detachedNodeModules, 'dir');
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
