import { execFileSync } from 'node:child_process';
import {
  mkdtempSync,
  readFileSync,
  rmSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
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

function currentCommit() {
  return execFileSync('git', ['rev-parse', 'HEAD'], {
    cwd: REPOSITORY_ROOT,
    encoding: 'utf8',
    stdio: ['ignore', 'pipe', 'pipe'],
  }).trim();
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
  };
  for (let index = 0; index < args.length; index += 1) {
    const arg = args[index];
    if (arg === '--measure' || arg === '--verify') {
      if (options.mode) throw new TypeError('choose exactly one of --measure or --verify');
      options.mode = arg.slice(2);
    } else if (arg === '--subject') {
      options.subjectSha = args[++index] || '';
    } else if (arg === '--baseline') {
      options.baselinePath = resolve(args[++index] || '');
    } else {
      throw new TypeError(`unsupported performance budget argument: ${arg}`);
    }
  }
  if (!options.mode) throw new TypeError('one of --measure or --verify is required');
  if (options.mode === 'verify') requireSubjectSha(options.subjectSha, currentCommit());
  else if (!/^[0-9a-f]{40}$/.test(options.subjectSha || '')) {
    throw new TypeError('subject must be a full 40-character Git SHA');
  }
  return options;
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
    } else {
      const report = await runPerformanceVerification({
        baselinePath: options.baselinePath,
        subjectSha: options.subjectSha,
      });
      process.stdout.write(`${JSON.stringify(report)}\n`);
      if (report.verdict.status !== 'PASS') process.exitCode = 2;
    }
  } catch (error) {
    process.stderr.write(`performance budget failed: ${error.message}\n`);
    process.exitCode = 1;
  }
}

export {
  DEFAULT_BASELINE_PATH,
  collectPerformanceEvidence,
  collectRenderIsolationEvidence,
  parseArguments,
  runPerformanceVerification,
  validateCaseRegistry,
  verifyPerformanceEvidence,
};
