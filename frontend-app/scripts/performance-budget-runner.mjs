import { execFileSync } from 'node:child_process';
import { createHash, randomUUID } from 'node:crypto';
import {
  existsSync,
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
import {
  P02_SUBJECT_CONTENT_PATHS,
  loadChatHistoryBenchmarkTarget,
  runChatHistoryBenchmarkSamples,
  verifyChatHistoryEvidence,
} from './chat-history-benchmark.mjs';
import { DEFAULT_BASELINE_PATH } from './performance-budget-config.mjs';
import { evaluateRenderIsolation, requireSubjectSha } from './performance-budget-model.mjs';
import { collectEvidenceProvenance } from './evidence-provenance.mjs';
import { runManagedCommand } from './managed-command.mjs';
import {
  measureFrontendResources,
  validateV8HeapEvidence,
  verifyResourceEvidence,
} from './resource-budget.mjs';
import {
  loadStopFeedbackTarget,
  P03_RUNTIME_PATH,
  P03_SUBJECT_FEEDBACK_COMPONENT_PATH,
  P03_SUBJECT_CONTENT_PATHS,
  runStopFeedbackBenchmark,
  verifyStopFeedbackEvidence,
} from './stop-feedback-benchmark.mjs';

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
// Advancing this runner intentionally gives each final baseline freeze a non-empty audited runner delta.
const FREEZE_RUN_COUNT = 3;
const P02_MAX_REGRESSION_RATIO = 1.15;
const P03_MAX_REGRESSION_RATIO = 1.15;
const P04_MAX_REGRESSION_RATIO = 1.05;
const INSTALL_ARGV = Object.freeze(['ci']);
const BASE_BUILD_ARGV = Object.freeze(['run', 'build']);
const MAX_LOAD_AVERAGE_DELTA_PER_LOGICAL_CORE = 0.25;
const P01_RENDER_PROBE_PATH = 'scripts/render-isolation-probe.test.jsx';
const SUBJECT_COMMAND_TIMEOUT_MS = 10 * 60 * 1_000;
const SUBJECT_COMMAND_KILL_GRACE_MS = 1_000;
const SUBJECT_COMMAND_MAX_BUFFER_BYTES = 2 * 1024 * 1024;
const SUBJECT_COMMAND_ERROR_OUTPUT_LIMIT = 4_096;

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

function commandOutputSnippet({ stderr = '', stdout = '' } = {}) {
  const output = `${stdout}${stderr}`.trim();
  if (!output) return '';
  const suffix = output.length > SUBJECT_COMMAND_ERROR_OUTPUT_LIMIT ? '…' : '';
  return `: ${output.slice(0, SUBJECT_COMMAND_ERROR_OUTPUT_LIMIT)}${suffix}`;
}

async function runManagedSubjectCommand(command, args, {
  cwd,
  env = process.env,
  label,
  runCommand = runManagedCommand,
} = {}) {
  if (typeof label !== 'string' || label.length === 0) throw new TypeError('managed subject command label is required');
  const result = await runCommand(command, args, {
    cwd,
    env,
    killGraceMs: SUBJECT_COMMAND_KILL_GRACE_MS,
    maxBuffer: SUBJECT_COMMAND_MAX_BUFFER_BYTES,
    timeoutMs: SUBJECT_COMMAND_TIMEOUT_MS,
  });
  if (!result || typeof result !== 'object') throw new Error(`${label} did not return managed command evidence`);
  if (result.error || result.outputTruncated || result.status !== 0 || result.timedOut) {
    const reason = result.error?.message
      || (result.timedOut ? `timed out after ${SUBJECT_COMMAND_TIMEOUT_MS}ms` : `exited with ${result.status}`);
    throw new Error(`${label} failed: ${reason}${commandOutputSnippet(result)}`, { cause: result.error });
  }
  return result.stdout;
}

function materializeDetachedSubject({ execute, repositoryRoot, temporaryRoot, subjectSha }) {
  execute('git', [
    'clone',
    '--local',
    '--no-hardlinks',
    '--no-checkout',
    '--no-tags',
    '--no-recurse-submodules',
    repositoryRoot,
    temporaryRoot,
  ], {
    cwd: repositoryRoot, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'],
  });
  execute('git', ['checkout', '--detach', subjectSha], {
    cwd: temporaryRoot, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'],
  });
}

function inspectDetachedSubject({ execute, temporaryRoot, subject, label }) {
  const { subjectSha, subjectTree } = subject;
  const targetSha = requireFullSha(execute('git', ['rev-parse', 'HEAD'], {
    cwd: temporaryRoot, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'],
  }).trim(), `${label} detached target SHA`);
  const targetTree = requireFullSha(execute('git', ['rev-parse', 'HEAD^{tree}'], {
    cwd: temporaryRoot, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'],
  }).trim(), `${label} detached target tree`);
  const status = execute('git', ['status', '--porcelain', '--untracked-files=all'], {
    cwd: temporaryRoot, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'],
  }).trim();
  if (targetSha !== subjectSha || targetTree !== subjectTree) {
    throw new Error(`${label} detached target Git identity does not match the requested subject`);
  }
  if (status) throw new Error(`${label} detached target subject must be clean`);
  return Object.freeze({ subjectSha: targetSha, subjectTree: targetTree });
}

function cleanupDetachedSubject({ temporaryRoot }) {
  rmSync(temporaryRoot, { recursive: true, force: true });
}

function requireDetachedP03SubjectClosure(subjectRoot) {
  for (const path of P03_SUBJECT_CONTENT_PATHS) {
    if (!existsSync(resolve(subjectRoot, path))) {
      throw new Error(`P03 detached subject production closure is missing: ${path}`);
    }
  }
}

function loadAverageTolerance(environment) {
  if (!Number.isSafeInteger(environment?.cpu?.logicalCores) || environment.cpu.logicalCores <= 0) {
    throw new TypeError('load-average comparison requires logical CPU cores');
  }
  return Math.max(1, environment.cpu.logicalCores * MAX_LOAD_AVERAGE_DELTA_PER_LOGICAL_CORE);
}

function assertComparableLoadAverage(left, right, label) {
  const tolerance = Math.min(loadAverageTolerance(left), loadAverageTolerance(right));
  for (const [index, value] of left.loadAverage.entries()) {
    const expected = right.loadAverage[index];
    const delta = Math.abs(value - expected);
    if (delta > tolerance) {
      throw new Error(`${label} loadAverage[${index}] differs beyond ${tolerance}: left=${value}, right=${expected}, delta=${delta}`);
    }
  }
}

function sha256(content) {
  return createHash('sha256').update(content).digest('hex');
}

function baseDistManifest(distDir, resourceMetric) {
  const manifest = resourceMetric.files.map(({ path, bytes }) => Object.freeze({
    path,
    bytes,
    sha256: sha256(readFileSync(join(distDir, path))),
  }));
  const manifestHash = sha256(manifest.map(({ path, bytes, sha256: fileHash }) => (
    `${path}\0${bytes}\0${fileHash}\n`
  )).join(''));
  return Object.freeze({ manifest: Object.freeze(manifest), manifestHash });
}

async function collectDetachedResourceBudget({
  subjectSha,
  subjectTree,
  buildLabel,
  temporaryPrefix,
  repositoryRoot = REPOSITORY_ROOT,
  execute = execFileSync,
  measureResources = measureFrontendResources,
  runCommand = runManagedCommand,
} = {}) {
  requireFullSha(subjectSha, `${buildLabel} subject SHA`);
  requireFullSha(subjectTree, `${buildLabel} subject tree`);
  const temporaryRoot = mkdtempSync(join(tmpdir(), temporaryPrefix));
  try {
    materializeDetachedSubject({ execute, repositoryRoot, temporaryRoot, subjectSha });
    const target = inspectDetachedSubject({
      execute, temporaryRoot, subject: { subjectSha, subjectTree }, label: buildLabel,
    });
    const frontendRoot = join(temporaryRoot, 'frontend-app');
    const distDir = join(frontendRoot, 'dist');
    if (existsSync(distDir)) throw new Error(`${buildLabel} detached tree must not contain a prebuilt dist directory`);
    await runManagedSubjectCommand('npm', INSTALL_ARGV, {
      cwd: frontendRoot,
      label: `${buildLabel} detached npm ci`,
      runCommand,
    });
    await runManagedSubjectCommand('npm', BASE_BUILD_ARGV, {
      cwd: frontendRoot,
      label: `${buildLabel} detached npm run build`,
      runCommand,
    });
    if (!existsSync(join(distDir, 'index.html'))) throw new Error(`${buildLabel} build did not produce dist/index.html`);
    const metric = measureResources({ distDir, subjectSha: target.subjectSha });
    const { manifest, manifestHash } = baseDistManifest(distDir, metric);
    return Object.freeze({
      metric,
      build: Object.freeze({
        subjectSha: target.subjectSha,
        subjectTree: target.subjectTree,
        installArgv: Object.freeze(['npm', ...INSTALL_ARGV]),
        buildArgv: Object.freeze(['npm', ...BASE_BUILD_ARGV]),
        distManifest: manifest,
        distManifestHash: manifestHash,
      }),
    });
  } finally {
    cleanupDetachedSubject({ temporaryRoot });
  }
}

async function collectBaseResourceBudget(options = {}) {
  const { metric, build } = await collectDetachedResourceBudget({
    ...options,
    buildLabel: 'BASE',
    temporaryPrefix: 'frontend-performance-base-',
  });
  const { subjectSha, subjectTree, ...buildEvidence } = build;
  return Object.freeze({
    ...metric,
    baseBuild: Object.freeze({
      baseSha: subjectSha,
      baseTree: subjectTree,
      ...buildEvidence,
    }),
  });
}

async function collectCandidateResourceBudget(options = {}) {
  const { metric, build } = await collectDetachedResourceBudget({
    ...options,
    buildLabel: 'candidate',
    temporaryPrefix: 'frontend-performance-candidate-',
  });
  return Object.freeze({ ...metric, candidateBuild: build });
}

async function collectDetachedStopFeedbackBudget({
  subjectSha,
  subjectTree,
  repositoryRoot = REPOSITORY_ROOT,
  execute = execFileSync,
  loadTarget = loadStopFeedbackTarget,
  runCommand = runManagedCommand,
  runBenchmark = runStopFeedbackBenchmark,
} = {}) {
  requireFullSha(subjectSha, 'P03 subject SHA');
  requireFullSha(subjectTree, 'P03 subject tree');
  const temporaryRoot = mkdtempSync(join(tmpdir(), 'frontend-stop-feedback-subject-'));
  try {
    materializeDetachedSubject({ execute, repositoryRoot, temporaryRoot, subjectSha });
    const { subjectSha: targetSha, subjectTree: targetTree } = inspectDetachedSubject({
      execute, temporaryRoot, subject: { subjectSha, subjectTree }, label: 'P03',
    });
    requireDetachedP03SubjectClosure(temporaryRoot);
    const frontendRoot = join(temporaryRoot, 'frontend-app');
    await runManagedSubjectCommand('npm', INSTALL_ARGV, {
      cwd: frontendRoot,
      label: 'P03 detached npm ci',
      runCommand,
    });
    const postInstallStatus = execute('git', ['status', '--porcelain', '--untracked-files=all'], {
      cwd: temporaryRoot, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'],
    }).trim();
    if (postInstallStatus) throw new Error('P03 detached target worktree changed during npm ci');
    const target = await loadTarget({
      subjectRoot: temporaryRoot,
      subjectSha: targetSha,
      subjectTree: targetTree,
    });
    const metric = await runBenchmark({ subjectSha: targetSha, target });
    if (metric?.subjectSha !== targetSha || metric.subjectRuntime?.subjectSha !== targetSha
      || metric.subjectFeedbackComponent?.source !== 'subject'
      || metric.subjectFeedbackComponent?.path !== P03_SUBJECT_FEEDBACK_COMPONENT_PATH) {
      throw new Error('P03 target benchmark subject provenance mismatch');
    }
    return Object.freeze({
      ...metric,
      subjectRuntime: Object.freeze({
        ...metric.subjectRuntime,
        installArgv: Object.freeze(['npm', ...INSTALL_ARGV]),
        worktreeClean: true,
        worktreeStatus: Object.freeze([]),
      }),
    });
  } finally {
    cleanupDetachedSubject({ temporaryRoot });
  }
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

async function collectRenderIsolationEvidence({
  frontendRoot = FRONTEND_ROOT,
  runCommand = runManagedCommand,
} = {}) {
  const probePath = resolve(frontendRoot, P01_RENDER_PROBE_PATH);
  const vitestPath = resolve(frontendRoot, 'node_modules', 'vitest', 'vitest.mjs');
  if (!existsSync(probePath)) throw new Error(`P01 detached subject is missing render isolation probe: ${P01_RENDER_PROBE_PATH}`);
  if (!existsSync(vitestPath)) throw new Error('P01 detached subject is missing the Vitest runtime after npm ci');
  const temporaryRoot = mkdtempSync(join(tmpdir(), 'frontend-render-isolation-'));
  const evidencePath = join(temporaryRoot, 'evidence.json');
  try {
    await runManagedSubjectCommand(
      process.execPath,
      [
        vitestPath,
        'run',
        P01_RENDER_PROBE_PATH,
        '--no-file-parallelism',
        '--maxWorkers=1',
      ],
      {
        cwd: frontendRoot,
        env: { ...process.env, FRONTEND_PERFORMANCE_EVIDENCE_PATH: evidencePath },
        label: 'P01 detached render isolation probe',
        runCommand,
      },
    );
    return JSON.parse(readFileSync(evidencePath, 'utf8'));
  } finally {
    rmSync(temporaryRoot, { recursive: true, force: true });
  }
}

async function collectDetachedP01P02Evidence({
  subjectSha,
  subjectTree,
  repositoryRoot = REPOSITORY_ROOT,
  execute = execFileSync,
  collectRender = collectRenderIsolationEvidence,
  loadHistoryTarget = loadChatHistoryBenchmarkTarget,
  runCommand = runManagedCommand,
  runHistory = runChatHistoryBenchmarkSamples,
} = {}) {
  requireFullSha(subjectSha, 'P01/P02 subject SHA');
  requireFullSha(subjectTree, 'P01/P02 subject tree');
  const temporaryRoot = mkdtempSync(join(tmpdir(), 'frontend-performance-subject-'));
  try {
    materializeDetachedSubject({ execute, repositoryRoot, temporaryRoot, subjectSha });
    const { subjectSha: targetSha, subjectTree: targetTree } = inspectDetachedSubject({
      execute, temporaryRoot, subject: { subjectSha, subjectTree }, label: 'P01/P02',
    });
    const frontendRoot = join(temporaryRoot, 'frontend-app');
    await runManagedSubjectCommand('npm', INSTALL_ARGV, {
      cwd: frontendRoot,
      label: 'P01/P02 detached npm ci',
      runCommand,
    });
    const postInstallStatus = execute('git', ['status', '--porcelain', '--untracked-files=all'], {
      cwd: temporaryRoot, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'],
    }).trim();
    if (postInstallStatus) throw new Error('P01/P02 detached target worktree changed during npm ci');
    const probePath = resolve(frontendRoot, P01_RENDER_PROBE_PATH);
    if (!existsSync(probePath)) {
      throw new Error(`P01 detached subject is missing render isolation probe: ${P01_RENDER_PROBE_PATH}`);
    }
    const renderIsolation = await collectRender({ frontendRoot });
    if (renderIsolation?.metricId !== 'P01-render-isolation') {
      throw new Error('P01 detached render isolation probe did not produce P01 evidence');
    }
    const historyTarget = await loadHistoryTarget({
      subjectRoot: temporaryRoot,
      subjectSha: targetSha,
      subjectTree: targetTree,
    });
    const historyBudget = runHistory({ commit: targetSha, target: historyTarget });
    if (historyBudget?.metricId !== 'P02-history-budget' || historyBudget.subjectSha !== targetSha
      || historyBudget.subjectProduct?.subjectSha !== targetSha
      || historyBudget.subjectProduct?.subjectTree !== targetTree) {
      throw new Error('P02 detached target benchmark subject provenance mismatch');
    }
    const finalStatus = execute('git', ['status', '--porcelain', '--untracked-files=all'], {
      cwd: temporaryRoot, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'],
    }).trim();
    if (finalStatus) throw new Error('P01/P02 detached target worktree changed during measurement');
    return Object.freeze({
      historyBudget,
      renderIsolation: Object.freeze({
        ...renderIsolation,
        subjectProduct: Object.freeze({
          probePath: P01_RENDER_PROBE_PATH,
          probeSha256: sha256(readFileSync(probePath)),
          subjectSha: targetSha,
          subjectTree: targetTree,
          worktreeClean: true,
        }),
      }),
    });
  } finally {
    cleanupDetachedSubject({ temporaryRoot });
  }
}

async function collectPerformanceEvidence({
  subjectSha = currentCommit(),
  collectP01P02 = collectDetachedP01P02Evidence,
  resourceBudget,
  collectCandidateResources = collectCandidateResourceBudget,
  collectStopFeedback = collectDetachedStopFeedbackBudget,
} = {}) {
  const context = collectEvidenceProvenance({
    repositoryRoot: REPOSITORY_ROOT,
    runnerId: 'frontend-performance-budget',
    subjectSha,
  });
  if (context.provenance.worktreeClean !== true) {
    throw new Error('performance evidence requires a clean committed runner worktree');
  }
  const p01p02 = await collectP01P02({
    subjectSha,
    subjectTree: context.subjectTree,
  });
  const feedbackBudget = await collectStopFeedback({
    subjectSha,
    subjectTree: context.subjectTree,
  });
  const measuredResources = resourceBudget || await collectCandidateResources({
    subjectSha,
    subjectTree: context.subjectTree,
  });
  return Object.freeze({
    schemaVersion: 1,
    subjectSha,
    ...context,
    metrics: Object.freeze({
      'P01-render-isolation': Object.freeze({ ...p01p02.renderIsolation, subjectSha }),
      'P02-history-budget': p01p02.historyBudget,
      'P03-feedback-budget': feedbackBudget,
      'P04-resource-budget': measuredResources,
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

function validateP01SubjectProduct(metric, subjectSha, subjectTree) {
  const product = metric?.subjectProduct;
  if (!product || product.subjectSha !== subjectSha || product.subjectTree !== subjectTree
    || product.probePath !== P01_RENDER_PROBE_PATH || product.worktreeClean !== true
    || !/^[0-9a-f]{64}$/.test(product.probeSha256 || '')) {
    throw new Error('P01 requires detached subject probe provenance');
  }
}

function validateSubjectContent(content, expectedPaths, label) {
  if (!/^[0-9a-f]{64}$/.test(content?.contentHash || '')
    || !Array.isArray(content.files) || content.files.length === 0) {
    throw new Error(`${label} provenance is missing content evidence`);
  }
  const paths = content.files.map(({ path }) => path);
  if (JSON.stringify(paths) !== JSON.stringify(expectedPaths)) {
    throw new Error(`${label} provenance has an incomplete production closure`);
  }
  content.files.forEach(({ path, sha256: fileHash }) => {
    if (typeof path !== 'string' || !/^[0-9a-f]{64}$/.test(fileHash || '')) {
      throw new TypeError(`${label} files require path and SHA-256`);
    }
  });
  const contentHash = sha256(content.files.map(({ path, sha256: fileHash }) => (
    `${path}\0${fileHash}\n`
  )).join(''));
  if (contentHash !== content.contentHash) {
    throw new Error(`${label} provenance content hash mismatch`);
  }
}

function validateP02SubjectProduct(metric, subjectSha, subjectTree) {
  const product = metric?.subjectProduct;
  if (!product || product.subjectSha !== subjectSha || product.subjectTree !== subjectTree) {
    throw new Error('P02 requires detached subject product provenance');
  }
  validateSubjectContent(product.content, P02_SUBJECT_CONTENT_PATHS, 'P02 subject product');
}

function validateRenderIsolationFreezeMetric(metric, subjectSha, subjectTree) {
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
  validateP01SubjectProduct(metric, subjectSha, subjectTree);
}

function validateResourceFreezeMetric(metric, subjectSha) {
  if (metric?.metricId !== 'P04-resource-budget' || metric.subjectSha !== subjectSha) {
    throw new Error('P04 freeze metric subject or metricId mismatch');
  }
  if (!Array.isArray(metric.files) || metric.files.length === 0 || metric.fileCount !== metric.files.length) {
    throw new Error('P04 resource fileCount must match a non-empty files array');
  }
  const paths = metric.files.map(({ path }) => path);
  const sortedPaths = [...paths].sort((left, right) => left < right ? -1 : left > right ? 1 : 0);
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
  validateV8HeapEvidence(metric, 'P04 freeze');
}

function validateP03SubjectRuntime(metric, subjectSha, subjectTree) {
  const target = metric?.subjectRuntime;
  if (!target || target.subjectSha !== subjectSha || target.subjectTree !== subjectTree
    || target.runtimePath !== P03_RUNTIME_PATH
    || target.feedbackComponentPath !== P03_SUBJECT_FEEDBACK_COMPONENT_PATH
    || JSON.stringify(target.installArgv) !== JSON.stringify(['npm', ...INSTALL_ARGV])
    || target.worktreeClean !== true || !Array.isArray(target.worktreeStatus) || target.worktreeStatus.length !== 0) {
    throw new Error('P03 requires detached subject runtime provenance');
  }
  const component = metric.subjectFeedbackComponent;
  if (component?.source !== 'subject' || component.path !== P03_SUBJECT_FEEDBACK_COMPONENT_PATH) {
    throw new Error('P03 requires detached subject feedback component provenance');
  }
  validateSubjectContent(target.content, P03_SUBJECT_CONTENT_PATHS, 'P03 subject runtime');
}

function validateBaseResourceBuild(metric, subjectSha, subjectTree) {
  const build = metric?.baseBuild;
  if (!build || build.baseSha !== subjectSha || build.baseTree !== subjectTree
    || JSON.stringify(build.buildArgv) !== JSON.stringify(['npm', ...BASE_BUILD_ARGV])
    || !Array.isArray(build.distManifest) || build.distManifest.length !== metric.files.length
    || !/^[0-9a-f]{64}$/.test(build.distManifestHash || '')) {
    throw new Error('P04 requires BASE detached-build provenance');
  }
  const expectedFiles = metric.files.map(({ path, bytes }) => `${path}\0${bytes}`);
  const actualFiles = build.distManifest.map(({ path, bytes, sha256: fileHash }) => {
    if (!/^[0-9a-f]{64}$/.test(fileHash || '')) throw new Error('P04 BASE dist manifest requires SHA-256');
    return `${path}\0${bytes}`;
  });
  exactJSON(actualFiles, expectedFiles, 'P04 BASE dist manifest');
  const actualHash = sha256(build.distManifest.map(({ path, bytes, sha256: fileHash }) => (
    `${path}\0${bytes}\0${fileHash}\n`
  )).join(''));
  if (actualHash !== build.distManifestHash) throw new Error('P04 BASE dist manifest hash mismatch');
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
  validateRenderIsolationFreezeMetric(p01, subjectSha, evidence.subjectTree);
  if (p02?.subjectSha !== subjectSha || p03?.subjectSha !== subjectSha) {
    throw new Error('P02/P03 freeze metric subject mismatch');
  }
  validateP02SubjectProduct(p02, subjectSha, evidence.subjectTree);
  validateP03SubjectRuntime(p03, subjectSha, evidence.subjectTree);
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
  validateBaseResourceBuild(p04, subjectSha, evidence.subjectTree);
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
  validateBaseResourceBuild(designated.metrics['P04-resource-budget'], subjectSha, expectedProvenance.subjectTree);
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
    assertComparableLoadAverage(run.environment, designated.environment, label);
  }
}

function freezeMetric(metric, metadata) {
  return Object.freeze({ ...metric, status: 'PASS', ...metadata });
}

function freezeWorstTimingMetric(runs, metricId, metadata) {
  const designated = runs[0].metrics[metricId];
  const cases = Object.fromEntries(Object.entries(designated.cases).map(([caseId, designatedCase]) => {
    const worstCase = runs
      .map((run) => run.metrics[metricId].cases[caseId])
      .reduce((worst, current) => (
        current.durationMedianMs > worst.durationMedianMs ? current : worst
      ), designatedCase);
    return [caseId, worstCase];
  }));
  return freezeMetric({ ...designated, cases: Object.freeze(cases) }, metadata);
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
      'P02-history-budget': freezeWorstTimingMetric(runs, 'P02-history-budget', {
        maxRegressionRatio: P02_MAX_REGRESSION_RATIO,
      }),
      'P03-feedback-budget': freezeWorstTimingMetric(runs, 'P03-feedback-budget', {
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
  collectBaseResources = collectBaseResourceBudget,
  preflight = validateFreezePreconditions,
  writeBaseline = writeFrozenBaselineAtomically,
} = {}) {
  const validated = preflight({ subjectSha, planSnapshotSha, outputPath });
  const resourceBudget = await collectBaseResources({
    subjectSha: validated.subjectSha,
    subjectTree: validated.subjectTree,
  });
  const runs = [];
  for (let run = 0; run < FREEZE_RUN_COUNT; run += 1) {
    runs.push(await collectEvidence({ subjectSha: validated.subjectSha, resourceBudget }));
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

function invalidateVerificationMetric(verdicts, metricId, reason) {
  return verdicts.map((verdict) => (verdict.metricId === metricId
    ? Object.freeze({ metricId, status: 'NOT_VERIFIED', reason })
    : verdict));
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
  const provenanceValidators = [
    {
      metricId: 'P01-render-isolation',
      validate: () => validateP01SubjectProduct(
        evidence?.metrics?.['P01-render-isolation'], evidence?.subjectSha, evidence?.subjectTree,
      ),
      label: 'P01 detached subject probe provenance',
    },
    {
      metricId: 'P02-history-budget',
      validate: () => validateP02SubjectProduct(
        evidence?.metrics?.['P02-history-budget'], evidence?.subjectSha, evidence?.subjectTree,
      ),
      label: 'P02 detached subject product provenance',
    },
    {
      metricId: 'P03-feedback-budget',
      validate: () => validateP03SubjectRuntime(
        evidence?.metrics?.['P03-feedback-budget'], evidence?.subjectSha, evidence?.subjectTree,
      ),
      label: 'P03 detached subject runtime provenance',
    },
  ];
  for (const { label, metricId, validate } of provenanceValidators) {
    try {
      validate();
    } catch (error) {
      verdicts = invalidateVerificationMetric(verdicts, metricId, `${label} is invalid: ${error.message}`);
    }
  }
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
    ['heap-used-median-bytes', 'heapUsedMedianBytes'],
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
  collectCandidateResourceBudget,
  collectDetachedP01P02Evidence,
  collectDetachedStopFeedbackBudget,
  collectPerformanceEvidence,
  collectRenderIsolationEvidence,
  freezePerformanceBaseline,
  parseArguments,
  runPerformanceVerification,
  validateCaseRegistry,
  validateFreezeOutputPath,
  validateFreezePreconditions,
  validateP03SubjectRuntime,
  verifyPerformanceEvidence,
  writeFrozenBaselineAtomically,
};
