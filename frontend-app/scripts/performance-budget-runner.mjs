import { execFileSync } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import {
  mkdtempSync,
  readFileSync,
  renameSync,
  rmSync,
  writeFileSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import {
  dirname,
  join,
  relative,
  resolve,
} from 'node:path';
import { fileURLToPath } from 'node:url';
import process from 'node:process';
import { runChatHistoryBenchmarkSamples, verifyChatHistoryEvidence } from './chat-history-benchmark.mjs';
import { DEFAULT_BASELINE_PATH } from './performance-budget-config.mjs';
import { evaluateRenderIsolation, requireSubjectSha } from './performance-budget-model.mjs';
import { collectEvidenceProvenance } from './evidence-provenance.mjs';
import { measureFrontendResources, verifyResourceEvidence } from './resource-budget.mjs';
import { runStopFeedbackBenchmark, verifyStopFeedbackEvidence } from './stop-feedback-benchmark.mjs';

const SCRIPT_PATH = fileURLToPath(import.meta.url);
const FRONTEND_ROOT = resolve(dirname(SCRIPT_PATH), '..');
const REPOSITORY_ROOT = resolve(FRONTEND_ROOT, '..');
const CASE_REGISTRY_PATH = resolve(dirname(SCRIPT_PATH), 'frontend-performance-cases.json');
const METRIC_IDS = Object.freeze([
  'P01-render-isolation',
  'P02-history-budget',
  'P03-feedback-budget',
  'P04-resource-budget',
]);
const FREEZE_RUN_COUNT = 3;
const P02_MAX_REGRESSION_RATIO = 1.15;
const P03_MAX_REGRESSION_RATIO = 1.15;
const P04_MAX_REGRESSION_RATIO = 1.05;

function currentCommit() {
  return execFileSync('git', ['rev-parse', 'HEAD'], {
    cwd: REPOSITORY_ROOT,
    encoding: 'utf8',
    stdio: ['ignore', 'pipe', 'pipe'],
  }).trim();
}

function executeGit(args, repositoryRoot = REPOSITORY_ROOT) {
  return execFileSync('git', args, {
    cwd: repositoryRoot,
    encoding: 'utf8',
    stdio: ['ignore', 'pipe', 'pipe'],
  }).trim();
}

function requireFullSha(value, label) {
  if (!/^[0-9a-f]{40}$/.test(value || '')) {
    throw new TypeError(`${label} must be a full 40-character Git SHA`);
  }
  return value;
}

function pathIsWithin(root, candidate) {
  const rel = relative(resolve(root), resolve(candidate));
  return rel.length > 0 && rel !== '..' && !rel.startsWith(`..${process.platform === 'win32' ? '\\' : '/'}`);
}

function validateFreezeOutputPath(outputPath) {
  if (typeof outputPath !== 'string' || outputPath.length === 0) {
    throw new TypeError('freeze output path is required');
  }
  const resolved = resolve(outputPath);
  const isBaseline = resolved === DEFAULT_BASELINE_PATH;
  const isTemporary = pathIsWithin(tmpdir(), resolved);
  if ((!isBaseline && !isTemporary) || !resolved.endsWith('.json')) {
    throw new Error('freeze output must be the baseline artifact or a JSON path under the system temporary directory');
  }
  return resolved;
}

function validateFreezePreconditions({
  subjectSha,
  planSnapshotSha,
  outputPath,
  repositoryRoot = REPOSITORY_ROOT,
  git = (args) => executeGit(args, repositoryRoot),
}) {
  requireFullSha(subjectSha, 'freeze subject');
  requireFullSha(planSnapshotSha, 'plan snapshot');
  const runnerSha = requireFullSha(git(['rev-parse', 'HEAD']), 'runner SHA');
  const runnerTree = requireFullSha(git(['rev-parse', 'HEAD^{tree}']), 'runner tree');
  const status = git(['status', '--porcelain', '--untracked-files=all']);
  if (status) throw new Error('freeze requires a clean committed runner worktree');
  if (subjectSha === runnerSha) throw new Error('freeze subject must differ from runner SHA');
  for (const [label, sha] of [['freeze subject', subjectSha], ['plan snapshot', planSnapshotSha]]) {
    try {
      git(['cat-file', '-e', `${sha}^{commit}`]);
    } catch (error) {
      throw new Error(`${label} commit does not exist`, { cause: error });
    }
  }
  try {
    git(['merge-base', '--is-ancestor', subjectSha, runnerSha]);
  } catch (error) {
    throw new Error('freeze subject must be an ancestor of runner SHA', { cause: error });
  }
  const subjectTree = requireFullSha(git(['rev-parse', `${subjectSha}^{tree}`]), 'subject tree');
  return Object.freeze({
    outputPath: validateFreezeOutputPath(outputPath),
    planSnapshotSha,
    runnerSha,
    runnerTree,
    subjectSha,
    subjectTree,
  });
}

function collectRenderIsolationEvidence() {
  const temporaryRoot = mkdtempSync(join(tmpdir(), 'frontend-render-isolation-'));
  const evidencePath = join(temporaryRoot, 'evidence.json');
  try {
    execFileSync(
      process.execPath,
      [
        resolve(FRONTEND_ROOT, 'node_modules', 'vitest', 'vitest.mjs'),
        'run',
        'scripts/render-isolation-probe.test.jsx',
        '--no-file-parallelism',
        '--maxWorkers=1',
      ],
      {
        cwd: FRONTEND_ROOT,
        env: { ...process.env, FRONTEND_PERFORMANCE_EVIDENCE_PATH: evidencePath },
        encoding: 'utf8',
        stdio: ['ignore', 'pipe', 'pipe'],
      },
    );
    return JSON.parse(readFileSync(evidencePath, 'utf8'));
  } finally {
    rmSync(temporaryRoot, { recursive: true, force: true });
  }
}

async function collectPerformanceEvidence({
  subjectSha = currentCommit(),
  distDir = resolve(FRONTEND_ROOT, 'dist'),
} = {}) {
  const context = collectEvidenceProvenance({
    repositoryRoot: REPOSITORY_ROOT,
    runnerId: 'frontend-performance-budget',
    subjectSha,
  });
  const renderIsolation = collectRenderIsolationEvidence();
  const historyBudget = runChatHistoryBenchmarkSamples({ commit: subjectSha });
  const feedbackBudget = await runStopFeedbackBenchmark({ subjectSha });
  const resourceBudget = measureFrontendResources({ distDir, subjectSha });
  return Object.freeze({
    schemaVersion: 1,
    subjectSha,
    ...context,
    metrics: Object.freeze({
      'P01-render-isolation': Object.freeze({ ...renderIsolation, subjectSha }),
      'P02-history-budget': historyBudget,
      'P03-feedback-budget': feedbackBudget,
      'P04-resource-budget': resourceBudget,
    }),
  });
}

function exactJSON(actual, expected, label) {
  if (JSON.stringify(actual) !== JSON.stringify(expected)) {
    throw new Error(`${label} mismatch`);
  }
}

function requireNonNegativeInteger(value, label) {
  if (!Number.isSafeInteger(value) || value < 0) throw new TypeError(`${label} must be a non-negative integer`);
  return value;
}

function validateRenderIsolationFreezeMetric(metric, subjectSha) {
  if (metric?.metricId !== 'P01-render-isolation' || metric.subjectSha !== subjectSha) {
    throw new Error('P01 freeze metric subject or metricId mismatch');
  }
  if (!Number.isSafeInteger(metric.warmupUpdates) || metric.warmupUpdates <= 0) {
    throw new TypeError('P01 warmupUpdates must be a positive integer');
  }
  if (metric.updateCount !== 20) throw new Error('P01 freeze requires exactly 20 measured updates');
  requireNonNegativeInteger(metric.mainPageUpdateCommits, 'P01 mainPageUpdateCommits');
  requireNonNegativeInteger(metric.unrelatedSubtreeUpdateCommits, 'P01 unrelatedSubtreeUpdateCommits');
  requireNonNegativeInteger(metric.mutationUpdateCommits, 'P01 mutationUpdateCommits');
  if (metric.mutationDetected !== true || metric.mutationUpdateCommits !== metric.updateCount) {
    throw new Error('P01 mutation sensitivity contract failed');
  }
}

function validateResourceFreezeMetric(metric, subjectSha) {
  if (metric?.metricId !== 'P04-resource-budget' || metric.subjectSha !== subjectSha) {
    throw new Error('P04 freeze metric subject or metricId mismatch');
  }
  if (!Array.isArray(metric.files) || metric.files.length === 0 || metric.fileCount !== metric.files.length) {
    throw new Error('P04 resource fileCount must match a non-empty files array');
  }
  const paths = metric.files.map(({ path }) => path);
  const sortedPaths = [...paths].sort((left, right) => left.localeCompare(right));
  if (new Set(paths).size !== paths.length || JSON.stringify(paths) !== JSON.stringify(sortedPaths)) {
    throw new Error('P04 resource paths must be exact, unique, and sorted');
  }
  metric.files.forEach(({ bytes, path }) => {
    if (typeof path !== 'string' || path.length === 0 || !Number.isSafeInteger(bytes) || bytes <= 0) {
      throw new TypeError('P04 resource entries require a path and positive integer bytes');
    }
  });
  const totalBundleBytes = metric.files.reduce((total, { bytes }) => total + bytes, 0);
  const maxChunkBytes = Math.max(...metric.files.map(({ bytes }) => bytes));
  if (metric.totalBundleBytes !== totalBundleBytes || metric.maxChunkBytes !== maxChunkBytes) {
    throw new Error('P04 resource summary is not recomputable from files');
  }
}

function requireFreezeVerifierPass(verdict, metricId) {
  if (verdict?.status !== 'PASS') {
    throw new Error(`${metricId} freeze evidence failed schema validation: ${verdict?.reason || 'unknown reason'}`);
  }
}

function validateFreezeEvidence(evidence, subjectSha) {
  if (evidence?.schemaVersion !== 1 || evidence.subjectSha !== subjectSha) {
    throw new Error('freeze evidence schema or subject mismatch');
  }
  requireFullSha(evidence.subjectTree, 'freeze subject tree');
  if (!Number.isFinite(Date.parse(evidence.generatedAt))) throw new TypeError('freeze generatedAt must be an ISO timestamp');
  exactSet(Object.keys(evidence.metrics || {}), METRIC_IDS, 'freeze metric ids');
  const provenance = evidence.provenance;
  if (!provenance || provenance.worktreeClean !== true || provenance.worktreeStatus?.length !== 0) {
    throw new Error('freeze evidence requires clean worktree provenance');
  }
  requireFullSha(provenance.runnerSha, 'freeze runner SHA');
  requireFullSha(provenance.runnerTree, 'freeze runner tree');
  if (provenance.runnerSha === subjectSha) throw new Error('freeze evidence runner SHA must differ from subject');
  if (!/^[0-9a-f]{64}$/.test(provenance.runnerContentHash || '')) {
    throw new TypeError('freeze runnerContentHash must be SHA-256');
  }
  if (!Array.isArray(provenance.runnerFiles) || provenance.runnerFiles.length === 0) {
    throw new Error('freeze runnerFiles must be non-empty');
  }
  const runnerPaths = provenance.runnerFiles.map(({ path }) => path);
  if (new Set(runnerPaths).size !== runnerPaths.length) throw new Error('freeze runnerFiles contain duplicate paths');
  provenance.runnerFiles.forEach(({ path, sha256 }) => {
    if (typeof path !== 'string' || !/^[0-9a-f]{64}$/.test(sha256 || '')) {
      throw new TypeError('freeze runnerFiles require path and SHA-256');
    }
  });
  const audit = provenance.baselineAudit;
  if (!audit || audit.baseSha !== subjectSha || audit.baseTree !== evidence.subjectTree) {
    throw new Error('freeze evidence requires matching baselineAudit provenance');
  }
  if (!Array.isArray(audit.changedPaths) || audit.changedPaths.length === 0) {
    throw new Error('freeze baselineAudit changedPaths must be non-empty');
  }
  exactJSON(audit.changedPaths, [...new Set(audit.changedPaths)].sort(), 'freeze baselineAudit changedPaths');
  if (!evidence.environment || typeof evidence.environment !== 'object') {
    throw new Error('freeze environment metadata is required');
  }

  const p01 = evidence.metrics['P01-render-isolation'];
  const p02 = evidence.metrics['P02-history-budget'];
  const p03 = evidence.metrics['P03-feedback-budget'];
  const p04 = evidence.metrics['P04-resource-budget'];
  validateRenderIsolationFreezeMetric(p01, subjectSha);
  if (p02?.subjectSha !== subjectSha || p03?.subjectSha !== subjectSha) {
    throw new Error('P02/P03 freeze metric subject mismatch');
  }
  requireFreezeVerifierPass(verifyChatHistoryEvidence(p02, {
    metrics: {
      'P02-history-budget': {
        ...p02,
        status: 'PASS',
        maxRegressionRatio: P02_MAX_REGRESSION_RATIO,
      },
    },
  }), 'P02-history-budget');
  requireFreezeVerifierPass(verifyStopFeedbackEvidence(p03, {
    metrics: {
      'P03-feedback-budget': {
        ...p03,
        status: 'PASS',
        maxRegressionRatio: P03_MAX_REGRESSION_RATIO,
      },
    },
  }), 'P03-feedback-budget');
  validateResourceFreezeMetric(p04, subjectSha);
  requireFreezeVerifierPass(verifyResourceEvidence(p04, {
    metrics: {
      'P04-resource-budget': {
        ...p04,
        status: 'PASS',
        maxRegressionRatio: P04_MAX_REGRESSION_RATIO,
      },
    },
  }), 'P04-resource-budget');
}

function stableEnvironmentIdentity(environment) {
  const { loadAverage: _loadAverage, ...identity } = environment;
  return identity;
}

function freezeMeasurementBindings(run) {
  return Object.freeze({
    subjectSha: run.subjectSha,
    subjectTree: run.subjectTree,
    environment: Object.freeze(stableEnvironmentIdentity(run.environment)),
    runnerSha: run.provenance.runnerSha,
    runnerTree: run.provenance.runnerTree,
    runnerContentHash: run.provenance.runnerContentHash,
    changedPaths: Object.freeze([...run.provenance.baselineAudit.changedPaths]),
  });
}

function validateFreezeRunConsistency(runs, subjectSha, expectedProvenance) {
  if (!Array.isArray(runs) || runs.length !== FREEZE_RUN_COUNT) {
    throw new Error(`freeze requires exactly ${FREEZE_RUN_COUNT} evidence runs`);
  }
  if (!expectedProvenance || typeof expectedProvenance !== 'object') {
    throw new Error('freeze expected provenance is required');
  }
  requireFullSha(expectedProvenance.runnerSha, 'expected runner SHA');
  requireFullSha(expectedProvenance.runnerTree, 'expected runner tree');
  requireFullSha(expectedProvenance.subjectTree, 'expected subject tree');
  runs.forEach((run) => validateFreezeEvidence(run, subjectSha));
  const designated = runs[0];
  exactJSON(designated.provenance.runnerSha, expectedProvenance.runnerSha, 'freeze runnerSha provenance');
  exactJSON(designated.provenance.runnerTree, expectedProvenance.runnerTree, 'freeze runnerTree provenance');
  exactJSON(designated.subjectTree, expectedProvenance.subjectTree, 'freeze subjectTree provenance');
  for (const [index, run] of runs.slice(1).entries()) {
    const label = `freeze run ${index + 2}`;
    exactJSON(run.subjectSha, designated.subjectSha, `${label} subjectSha`);
    exactJSON(run.subjectTree, designated.subjectTree, `${label} subjectTree`);
    exactJSON(run.provenance.runnerSha, designated.provenance.runnerSha, `${label} runnerSha`);
    exactJSON(run.provenance.runnerTree, designated.provenance.runnerTree, `${label} runnerTree`);
    exactJSON(
      run.provenance.runnerContentHash,
      designated.provenance.runnerContentHash,
      `${label} runnerContentHash`,
    );
    exactJSON(run.provenance.runnerFiles, designated.provenance.runnerFiles, `${label} runnerFiles`);
    exactJSON(run.provenance.baselineAudit, designated.provenance.baselineAudit, `${label} baselineAudit`);
    exactJSON(
      stableEnvironmentIdentity(run.environment),
      stableEnvironmentIdentity(designated.environment),
      `${label} environment identity`,
    );
  }
}

function freezeMetric(metric, metadata) {
  return Object.freeze({ ...metric, status: 'PASS', ...metadata });
}

function buildFrozenPerformanceBaseline({
  runs,
  subjectSha,
  planSnapshotSha,
  expectedProvenance,
}) {
  requireFullSha(subjectSha, 'freeze subject');
  requireFullSha(planSnapshotSha, 'plan snapshot');
  validateFreezeRunConsistency(runs, subjectSha, expectedProvenance);
  const designated = runs[0];
  return Object.freeze({
    schemaVersion: 1,
    baseSha: subjectSha,
    subjectSha,
    subjectTree: designated.subjectTree,
    planSnapshotSha,
    generatedAt: designated.generatedAt,
    environment: designated.environment,
    provenance: designated.provenance,
    measurementAudit: Object.freeze({
      runCount: FREEZE_RUN_COUNT,
      designatedRun: 1,
      reproducibilityRuns: Object.freeze(runs.slice(1).map((run, index) => Object.freeze({
        run: index + 2,
        generatedAt: run.generatedAt,
        runnerContentHash: run.provenance.runnerContentHash,
        bindings: freezeMeasurementBindings(run),
        metrics: run.metrics,
      }))),
    }),
    metrics: Object.freeze({
      'P01-render-isolation': freezeMetric(designated.metrics['P01-render-isolation'], {
        absoluteUpdateLimit: 1,
      }),
      'P02-history-budget': freezeMetric(designated.metrics['P02-history-budget'], {
        maxRegressionRatio: P02_MAX_REGRESSION_RATIO,
      }),
      'P03-feedback-budget': freezeMetric(designated.metrics['P03-feedback-budget'], {
        maxRegressionRatio: P03_MAX_REGRESSION_RATIO,
      }),
      'P04-resource-budget': freezeMetric(designated.metrics['P04-resource-budget'], {
        maxRegressionRatio: P04_MAX_REGRESSION_RATIO,
      }),
    }),
  });
}

function writeFrozenBaselineAtomically(outputPath, baseline) {
  const resolvedOutput = validateFreezeOutputPath(outputPath);
  const temporaryPath = `${resolvedOutput}.${process.pid}.${randomUUID()}.tmp`;
  try {
    writeFileSync(temporaryPath, `${JSON.stringify(baseline, null, 2)}\n`, {
      encoding: 'utf8',
      flag: 'wx',
      mode: 0o600,
    });
    renameSync(temporaryPath, resolvedOutput);
  } catch (error) {
    rmSync(temporaryPath, { force: true });
    throw error;
  }
  return resolvedOutput;
}

async function freezePerformanceBaseline({
  subjectSha,
  planSnapshotSha,
  outputPath,
  collectEvidence = collectPerformanceEvidence,
  preflight = validateFreezePreconditions,
  writeBaseline = writeFrozenBaselineAtomically,
} = {}) {
  const validated = preflight({ subjectSha, planSnapshotSha, outputPath });
  const runs = [];
  for (let run = 0; run < FREEZE_RUN_COUNT; run += 1) {
    runs.push(await collectEvidence({ subjectSha: validated.subjectSha }));
  }
  const baseline = buildFrozenPerformanceBaseline({
    runs,
    subjectSha: validated.subjectSha,
    planSnapshotSha: validated.planSnapshotSha,
    expectedProvenance: {
      runnerSha: validated.runnerSha,
      runnerTree: validated.runnerTree,
      subjectTree: validated.subjectTree,
    },
  });
  writeBaseline(validated.outputPath, baseline);
  return baseline;
}

function verifyPerformanceEvidence(evidence, baseline) {
  let verdicts = [
    evaluateRenderIsolation(
      evidence.metrics['P01-render-isolation'],
      baseline?.metrics?.['P01-render-isolation'],
    ),
    verifyChatHistoryEvidence(evidence.metrics['P02-history-budget'], baseline),
    verifyStopFeedbackEvidence(evidence.metrics['P03-feedback-budget'], baseline),
    verifyResourceEvidence(evidence.metrics['P04-resource-budget'], baseline),
  ];
  const baselineRunnerHash = baseline?.provenance?.runnerContentHash;
  const currentRunnerHash = evidence?.provenance?.runnerContentHash;
  if (!baselineRunnerHash || baselineRunnerHash !== currentRunnerHash) {
    const reason = !baselineRunnerHash
      ? 'frozen baseline runnerContentHash is missing'
      : 'candidate runnerContentHash does not match the frozen baseline runner';
    verdicts = METRIC_IDS.map((metricId) => Object.freeze({ metricId, status: 'NOT_VERIFIED', reason }));
  }
  const registry = JSON.parse(readFileSync(CASE_REGISTRY_PATH, 'utf8'));
  const caseResults = validateCaseRegistry(evidence, verdicts, registry);
  const status = verdicts.some((verdict) => verdict.status === 'FAIL')
    ? 'FAIL'
    : verdicts.every((verdict) => verdict.status === 'PASS') ? 'PASS' : 'NOT_VERIFIED';
  return Object.freeze({
    status,
    testCount: caseResults.length,
    caseIds: Object.freeze(caseResults.map(({ caseId }) => caseId)),
    caseResults,
    verdicts,
  });
}

function exactSet(actual, expected, label) {
  const normalizedActual = [...actual].sort();
  const normalizedExpected = [...expected].sort();
  if (JSON.stringify(normalizedActual) !== JSON.stringify(normalizedExpected)) {
    throw new Error(`${label} exact set mismatch`);
  }
}

function actualEvidenceCases(evidence) {
  const metrics = evidence?.metrics || {};
  const cases = [];
  const render = metrics['P01-render-isolation'] || {};
  for (const [caseId, evidenceKey] of [
    ['render-main-page-update-commits', 'mainPageUpdateCommits'],
    ['render-unrelated-subtree-update-commits', 'unrelatedSubtreeUpdateCommits'],
    ['render-broad-subscription-mutation-detected', 'mutationDetected'],
  ]) {
    if (Object.hasOwn(render, evidenceKey)) {
      cases.push({ caseId, metricId: 'P01-render-isolation', evidenceKey });
    }
  }
  for (const evidenceKey of Object.keys(metrics['P02-history-budget']?.cases || {})) {
    cases.push({ caseId: evidenceKey, metricId: 'P02-history-budget', evidenceKey });
  }
  for (const evidenceKey of Object.keys(metrics['P03-feedback-budget']?.cases || {})) {
    cases.push({ caseId: evidenceKey, metricId: 'P03-feedback-budget', evidenceKey });
  }
  for (const [caseId, evidenceKey] of [
    ['bundle-total-bytes', 'totalBundleBytes'],
    ['bundle-max-chunk-bytes', 'maxChunkBytes'],
  ]) {
    if (Object.hasOwn(metrics['P04-resource-budget'] || {}, evidenceKey)) {
      cases.push({ caseId, metricId: 'P04-resource-budget', evidenceKey });
    }
  }
  return cases;
}

function caseStatus(entry, evidence, verdictByMetric) {
  const verdict = verdictByMetric.get(entry.metricId);
  if (verdict.status !== 'PASS' && verdict.status !== 'FAIL') return 'NOT_VERIFIED';
  if (entry.evidenceKey === 'mutationDetected') {
    return evidence.metrics[entry.metricId][entry.evidenceKey] === true ? 'PASS' : 'FAIL';
  }
  const comparisonKey = entry.metricId === 'P01-render-isolation'
    || entry.metricId === 'P04-resource-budget'
    ? entry.evidenceKey
    : entry.caseId;
  const comparison = verdict.comparisons?.find(({ case: name }) => name === comparisonKey);
  if (!comparison) throw new Error(`missing verdict result for performance case ${entry.caseId}`);
  return comparison.current <= comparison.threshold ? 'PASS' : 'FAIL';
}

function validateCaseRegistry(evidence, verdicts, registry) {
  if (registry?.schemaVersion !== 1 || !Number.isInteger(registry.testCount) || registry.testCount <= 0) {
    throw new Error('performance case registry has zero tests or an unsupported schema');
  }
  if (!Array.isArray(registry.cases) || registry.cases.length !== registry.testCount) {
    throw new Error('performance case registry testCount mismatch');
  }
  const registryIds = registry.cases.map(({ caseId }) => caseId);
  if (new Set(registryIds).size !== registryIds.length) {
    throw new Error('performance case registry contains duplicate caseIds');
  }
  const registryKeys = registry.cases.map(({ caseId, metricId, evidenceKey }) => (
    `${metricId}:${caseId}:${evidenceKey}`
  ));
  const actualCases = actualEvidenceCases(evidence);
  const actualKeys = actualCases.map(({ caseId, metricId, evidenceKey }) => (
    `${metricId}:${caseId}:${evidenceKey}`
  ));
  exactSet(actualKeys, registryKeys, 'performance evidence cases');
  exactSet(Object.keys(evidence.metrics || {}), METRIC_IDS, 'performance metric ids');
  const verdictByMetric = new Map(verdicts.map((verdict) => [verdict.metricId, verdict]));
  if (verdictByMetric.size !== verdicts.length) throw new Error('duplicate performance metric verdict');
  exactSet(verdictByMetric.keys(), METRIC_IDS, 'performance verdict metric ids');
  const actualById = new Map(actualCases.map((entry) => [entry.caseId, entry]));
  const results = registry.cases.map((registered) => Object.freeze({
    ...registered,
    status: caseStatus(actualById.get(registered.caseId), evidence, verdictByMetric),
  }));
  if (results.length === 0) throw new Error('performance verification executed zero cases');
  return Object.freeze(results);
}

function parseArguments(args) {
  const options = {
    mode: '',
    subjectSha: currentCommit(),
    baselinePath: DEFAULT_BASELINE_PATH,
    baselineProvided: false,
    outputPath: '',
    planSnapshotSha: '',
    subjectProvided: false,
  };
  for (let index = 0; index < args.length; index += 1) {
    const arg = args[index];
    if (arg === '--measure' || arg === '--verify' || arg === '--freeze') {
      if (options.mode) throw new TypeError('choose exactly one of --measure, --verify, or --freeze');
      options.mode = arg.slice(2);
    } else if (arg === '--subject') {
      options.subjectSha = args[++index] || '';
      options.subjectProvided = true;
    } else if (arg === '--baseline') {
      options.baselinePath = resolve(args[++index] || '');
      options.baselineProvided = true;
    } else if (arg === '--plan-snapshot') {
      options.planSnapshotSha = args[++index] || '';
    } else if (arg === '--output') {
      options.outputPath = resolve(args[++index] || '');
    } else {
      throw new TypeError(`unsupported performance budget argument: ${arg}`);
    }
  }
  if (!options.mode) throw new TypeError('one of --measure, --verify, or --freeze is required');
  if (options.mode === 'freeze') {
    if (!options.subjectProvided) throw new TypeError('--freeze requires an explicit --subject');
    requireFullSha(options.subjectSha, 'freeze subject');
    requireFullSha(options.planSnapshotSha, 'plan snapshot');
    validateFreezeOutputPath(options.outputPath);
  } else if (options.planSnapshotSha || options.outputPath) {
    throw new TypeError('--plan-snapshot and --output are only valid with --freeze');
  }
  if (options.mode !== 'verify' && options.baselineProvided) {
    throw new TypeError('--baseline is only valid with --verify');
  }
  if (options.mode === 'verify') requireSubjectSha(options.subjectSha, currentCommit());
  else if (!/^[0-9a-f]{40}$/.test(options.subjectSha || '')) {
    throw new TypeError('subject must be a full 40-character Git SHA');
  }
  return Object.freeze(options);
}

async function runPerformanceVerification({ subjectSha, baselinePath = DEFAULT_BASELINE_PATH }) {
  const baseline = JSON.parse(readFileSync(baselinePath, 'utf8'));
  requireSubjectSha(subjectSha, currentCommit());
  const evidence = await collectPerformanceEvidence({ subjectSha });
  const verdict = verifyPerformanceEvidence(evidence, baseline);
  return Object.freeze({ schemaVersion: 1, evidence, verdict });
}

if (process.argv[1] && resolve(process.argv[1]) === SCRIPT_PATH) {
  try {
    const options = parseArguments(process.argv.slice(2));
    if (options.mode === 'measure') {
      const evidence = await collectPerformanceEvidence({ subjectSha: options.subjectSha });
      process.stdout.write(`${JSON.stringify({ schemaVersion: 1, status: 'MEASURED', evidence })}\n`);
    } else if (options.mode === 'verify') {
      const report = await runPerformanceVerification({
        baselinePath: options.baselinePath,
        subjectSha: options.subjectSha,
      });
      process.stdout.write(`${JSON.stringify(report)}\n`);
      if (report.verdict.status !== 'PASS') process.exitCode = 2;
    } else {
      const baseline = await freezePerformanceBaseline({
        subjectSha: options.subjectSha,
        planSnapshotSha: options.planSnapshotSha,
        outputPath: options.outputPath,
      });
      process.stdout.write(`${JSON.stringify({
        schemaVersion: 1,
        status: 'FROZEN',
        outputPath: options.outputPath,
        baseline,
      })}\n`);
    }
  } catch (error) {
    process.stderr.write(`performance budget failed: ${error.message}\n`);
    process.exitCode = 1;
  }
}

export {
  DEFAULT_BASELINE_PATH,
  FREEZE_RUN_COUNT,
  buildFrozenPerformanceBaseline,
  collectPerformanceEvidence,
  collectRenderIsolationEvidence,
  freezePerformanceBaseline,
  parseArguments,
  runPerformanceVerification,
  validateCaseRegistry,
  validateFreezeOutputPath,
  validateFreezePreconditions,
  verifyPerformanceEvidence,
  writeFrozenBaselineAtomically,
};
