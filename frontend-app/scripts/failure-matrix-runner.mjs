import { createHash } from 'node:crypto';
import { realpathSync } from 'node:fs';
import { mkdtemp, readFile, rm, symlink, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { runManagedCommand } from './managed-command.mjs';

const DEFAULT_MANIFEST = path.join(path.dirname(fileURLToPath(import.meta.url)), 'failure-matrix-cases.json');
const DEFAULT_FIXTURES = path.join(path.dirname(fileURLToPath(import.meta.url)), 'failure-matrix-fixtures.json');
const DEFAULT_MUTATIONS = path.join(path.dirname(fileURLToPath(import.meta.url)), 'failure-matrix-mutations.json');
const CASE_ID_PATTERN = /^FM-\d{2}$/u;
const CASE_STATUSES = new Set(['covered', 'blocked']);
const REQUIRED_CASE_IDS = Object.freeze(
  Array.from({ length: 24 }, (_, index) => `FM-${String(index + 1).padStart(2, '0')}`),
);
const REQUIRED_EVIDENCE_COUNT = 27;
const GO_PACKAGES = Object.freeze({
  'go-codex': './internal/provider/codexapp',
  'go-claude': './internal/provider/claudecli',
  'go-turn': './internal/module/turn',
  'go-wails': './internal/ui/wails',
});
const DEFAULT_COMMAND_TIMEOUT_MS = 15 * 60 * 1_000;
const DEFAULT_COMMAND_KILL_GRACE_MS = 1_000;
const DEFAULT_COMMAND_MAX_BUFFER = 16 * 1024 * 1024;

export function validateFailureMatrixManifest(manifest) {
  if (!manifest || manifest.schemaVersion !== 1 || !Array.isArray(manifest.cases)) {
    throw new Error('failure matrix manifest schemaVersion=1 and cases[] are required');
  }
  const caseIds = manifest.cases.map((entry) => String(entry?.caseId || '').trim());
  assertExactUniqueSet('failure matrix caseIds', caseIds, REQUIRED_CASE_IDS);
  for (const entry of manifest.cases) {
    if (!CASE_ID_PATTERN.test(entry.caseId)) throw new Error(`${entry.caseId}: caseId must match FM-NN`);
    if (!String(entry.subject || '').trim()) throw new Error(`${entry.caseId}: subject is required`);
    if (!CASE_STATUSES.has(entry.status)) throw new Error(`${entry.caseId}: status must be covered or blocked`);
    if (!Array.isArray(entry.requiredLayers) || entry.requiredLayers.length === 0) {
      throw new Error(`${entry.caseId}: requiredLayers must not be empty`);
    }
    assertExactUniqueSet(`${entry.caseId} requiredLayers`, entry.requiredLayers, [...new Set(entry.requiredLayers)]);
    if (entry.status === 'blocked') {
      if (entry.requiredLayers.length !== 1 || entry.requiredLayers[0] !== 'fixture-replay') {
        throw new Error(`${entry.caseId}: blocked cases require fixture-replay only`);
      }
      if (!String(entry.blockedBy || '').trim() || !String(entry.blocker || '').trim()) {
        throw new Error(`${entry.caseId}: blockedBy and blocker are required`);
      }
    } else if (entry.blockedBy || entry.blocker || entry.requiredLayers.includes('fixture-replay')) {
      throw new Error(`${entry.caseId}: covered case cannot declare dependency-only evidence`);
    }
  }
  return manifest.cases;
}

export function validateFailureMatrixFixtures(cases, fixtureDocument) {
  if (!fixtureDocument || fixtureDocument.schemaVersion !== 1 || !Array.isArray(fixtureDocument.fixtures)) {
    throw new Error('failure matrix fixtures schemaVersion=1 and fixtures[] are required');
  }
  const caseIds = cases.map((entry) => entry.caseId);
  const fixtureIds = fixtureDocument.fixtures.map((entry) => String(entry?.caseId || '').trim());
  assertExactUniqueSet('failure matrix fixture caseIds', fixtureIds, caseIds);
  const byID = new Map(cases.map((entry) => [entry.caseId, entry]));
  for (const fixture of fixtureDocument.fixtures) {
    const matrixCase = byID.get(fixture.caseId);
    if (!String(fixture.expected || '').trim()) throw new Error(`${fixture.caseId}: fixture expected is required`);
    const fixtureBlockedBy = String(fixture.blockedBy || '').trim();
    const caseBlockedBy = String(matrixCase.blockedBy || '').trim();
    if (fixtureBlockedBy !== caseBlockedBy) {
      throw new Error(`${fixture.caseId}: fixture blockedBy drift: got=${fixtureBlockedBy} want=${caseBlockedBy}`);
    }
  }
  return fixtureDocument.fixtures;
}

export function validateFailureMatrixMutations(document) {
  if (!document || document.schemaVersion !== 1 || !Array.isArray(document.mutations)) {
    throw new Error('failure matrix mutations schemaVersion=1 and mutations[] are required');
  }
  const mutationIds = document.mutations.map((entry) => String(entry?.id || '').trim());
  assertExactUniqueSet('failure matrix mutation ids', mutationIds, [...new Set(mutationIds)]);
  const caseIds = [];
  for (const mutation of document.mutations) {
    if (!['frontend', ...Object.keys(GO_PACKAGES)].includes(mutation.layer)) {
      throw new Error(`${mutation.id}: mutation layer is invalid`);
    }
    if (typeof mutation.sourcePath !== 'string' || path.isAbsolute(mutation.sourcePath)
      || mutation.sourcePath.split('/').includes('..')) {
      throw new Error(`${mutation.id}: mutation sourcePath must be repository-relative`);
    }
    if (!String(mutation.search || '') || !String(mutation.replacement || '')
      || mutation.search === mutation.replacement) {
      throw new Error(`${mutation.id}: mutation must define distinct search and replacement text`);
    }
    if (!Array.isArray(mutation.caseIds) || mutation.caseIds.length === 0) {
      throw new Error(`${mutation.id}: mutation caseIds must not be empty`);
    }
    caseIds.push(...mutation.caseIds);
  }
  assertExactUniqueSet('failure matrix mutation caseIds', caseIds, REQUIRED_CASE_IDS);
  return document.mutations;
}

export function validateFailureMatrixEvidence(cases, fixtures, evidence) {
  if (!Array.isArray(evidence) || evidence.length === 0) {
    throw new Error('failure matrix evidence testCount must be greater than zero');
  }
  validateFailureMatrixFixtures(cases, { schemaVersion: 1, fixtures });
  const expectedPairs = cases.flatMap((entry) => entry.requiredLayers.map((layer) => `${entry.caseId}\u0000${layer}`));
  if (expectedPairs.length !== REQUIRED_EVIDENCE_COUNT) {
    throw new Error(`failure matrix expected testCount=${REQUIRED_EVIDENCE_COUNT}, got ${expectedPairs.length}`);
  }
  const actualPairs = evidence.map((entry) => `${entry.caseId}\u0000${entry.layer}`);
  assertExactUniqueSet('failure matrix evidence', actualPairs, expectedPairs);
  const blockedCases = cases
    .filter((entry) => entry.status === 'blocked')
    .map(({ caseId, blockedBy, blocker }) => ({ caseId, blockedBy, blocker }));
  return {
    caseIds: cases.map((entry) => entry.caseId),
    caseCount: cases.length,
    testCount: evidence.length,
    status: blockedCases.length === 0 ? 'covered' : 'partial',
    blockedCases,
    evidence: [...evidence].sort((left, right) => (
    (left.caseId < right.caseId ? -1 : left.caseId > right.caseId ? 1 : 0) || (left.layer < right.layer ? -1 : left.layer > right.layer ? 1 : 0)
    )),
  };
}

function assertExactUniqueSet(label, actual, expected) {
  if (actual.some((value) => !String(value).trim())) throw new Error(`${label}: empty value`);
  const duplicates = actual.filter((value, index) => actual.indexOf(value) !== index);
  const actualSet = new Set(actual);
  const expectedSet = new Set(expected);
  const missing = expected.filter((value) => !actualSet.has(value));
  const stale = actual.filter((value) => !expectedSet.has(value));
  if (duplicates.length || missing.length || stale.length || actual.length !== expected.length) {
    throw new Error(`${label} exact diff failed: missing=${JSON.stringify(missing)} stale=${JSON.stringify(stale)} duplicate=${JSON.stringify([...new Set(duplicates)])}`);
  }
}

export async function loadFailureMatrixDocument(documentPath) {
  const raw = await readFile(documentPath, 'utf8');
  return JSON.parse(raw);
}

export function parseVitestEvidence(report, frontendRoot = '') {
  const suites = Array.isArray(report?.testResults) ? report.testResults : [];
  const evidence = [];
  for (const suite of suites) {
    for (const assertion of suite.assertionResults || []) {
      const title = String(assertion.fullName || assertion.title || '');
      const match = title.match(/\bmatrix:(FM-\d{2})\s+layer:([a-z-]+)\b/u);
      if (!match || assertion.status !== 'passed') continue;
      const suitePath = String(suite.name || suite.file || '').trim();
      const file = suitePath && frontendRoot
        ? path.relative(realpathSync(frontendRoot), realpathSync(path.resolve(suitePath))).split(path.sep).join('/')
        : '';
      evidence.push({ caseId: match[1], layer: match[2], test: title, ...(file ? { file } : {}) });
    }
  }
  return evidence;
}

export function parseGoEvidence(lines, layer) {
  const evidence = [];
  for (const line of String(lines || '').split(/\r?\n/u)) {
    if (!line.trim()) continue;
    let event;
    try {
      event = JSON.parse(line);
    } catch {
      continue;
    }
    if (event.Action !== 'pass') continue;
    const match = String(event.Test || '').match(/\/(FM-\d{2})$/u);
    if (match) evidence.push({ caseId: match[1], layer, test: event.Test });
  }
  return evidence;
}

function sha256(value) {
  return createHash('sha256').update(value).digest('hex');
}

function countOccurrences(source, search) {
  if (!search) return 0;
  let count = 0;
  let offset = 0;
  while ((offset = source.indexOf(search, offset)) !== -1) {
    count += 1;
    offset += search.length;
  }
  return count;
}

function caseCommand(evidence, vitestCommand) {
  if (evidence.layer === 'frontend') {
    if (!evidence.file) throw new Error(`${evidence.caseId}: Vitest evidence file is missing`);
    return {
      command: vitestCommand ?? process.execPath,
      args: [...(vitestCommand ? [] : [path.join('node_modules', 'vitest', 'vitest.mjs')]), 'run', '--configLoader', 'runner', evidence.file, '-t', evidence.test, '--no-file-parallelism', '--maxWorkers=1'],
      cwd: 'frontend-app',
    };
  }
  const pkg = GO_PACKAGES[evidence.layer];
  if (!pkg) throw new Error(`${evidence.caseId}: unsupported mutation evidence layer ${evidence.layer}`);
  return {
    command: 'go',
    args: ['test', pkg, '-run', `^${evidence.test}$`, '-count=1'],
    cwd: '.',
  };
}

function commandRecord(spec) {
  return { cwd: spec.cwd, argv: [spec.command, ...spec.args] };
}

async function runMutationEvidence({
  repoRoot,
  frontendRoot,
  tempRoot,
  subjectSha,
  subjectTreeSha,
  mutations,
  evidence,
  managedOptions,
  vitestCommand,
}) {
  const byPair = new Map(evidence.map((entry) => [`${entry.caseId}\u0000${entry.layer}`, entry]));
  const results = [];
  for (const mutation of mutations) {
    const mutationRoot = path.join(tempRoot, `mutation-${mutation.id}`);
    try {
      await runCommand('git', ['worktree', 'add', '--detach', mutationRoot, subjectSha], repoRoot, managedOptions);
      const targetNodeModules = path.join(mutationRoot, 'frontend-app', 'node_modules');
      if (mutation.layer === 'frontend') await symlink(path.join(frontendRoot, 'node_modules'), targetNodeModules, process.platform === 'win32' ? 'junction' : 'dir');
      const sourceFile = path.join(mutationRoot, mutation.sourcePath);
      const original = await readFile(sourceFile, 'utf8');
      if (countOccurrences(original, mutation.search) !== 1) {
        throw new Error(`${mutation.id}: production mutation search must match exactly once`);
      }
      const mutated = original.replace(mutation.search, mutation.replacement);
      const mutationBinding = {
        mutationId: mutation.id,
        sourcePath: mutation.sourcePath,
        sourceSha256: sha256(original),
        mutatedSha256: sha256(mutated),
      };
      const greenRuns = new Map();
      for (const caseId of mutation.caseIds) {
        const green = byPair.get(`${caseId}\u0000${mutation.layer}`);
        if (!green) throw new Error(`${mutation.id}: missing GREEN evidence for ${caseId} layer ${mutation.layer}`);
        const spec = caseCommand(green, vitestCommand);
        const execution = await captureCommand(
          spec.command,
          spec.args,
          path.join(mutationRoot, spec.cwd),
          managedOptions,
        );
        const output = `${execution.stdout}\n${execution.stderr}`;
        if (execution.exitCode !== 0 || execution.signal !== null || execution.error || execution.timedOut) {
          throw new Error(`${mutation.id}: ${caseId} immutable focused GREEN must exit zero`);
        }
        greenRuns.set(caseId, { evidence: green, spec, execution, output });
      }
      await writeFile(sourceFile, mutated, 'utf8');
      for (const caseId of mutation.caseIds) {
        const { evidence: green, spec, execution: greenExecution, output: greenOutput } = greenRuns.get(caseId);
        const red = await captureCommand(
          spec.command,
          spec.args,
          path.join(mutationRoot, spec.cwd),
          managedOptions,
        );
        const combined = `${red.stdout}\n${red.stderr}`;
        if (!(red.exitCode > 0) || red.signal !== null || red.error || red.timedOut || !combined.includes(caseId)) {
          throw new Error(`${mutation.id}: ${caseId} mutation RED must exit non-zero and identify the case`);
        }
        results.push({
          caseId,
          subjectSha,
          subjectTreeSha,
          green: {
            layer: green.layer,
            test: green.test,
            ...(green.file ? { file: green.file } : {}),
            ...commandRecord(spec),
            exitCode: greenExecution.exitCode,
            signal: greenExecution.signal,
            outputSha256: sha256(greenOutput),
          },
          red: {
            ...mutationBinding,
            ...commandRecord(spec),
            exitCode: red.exitCode,
            signal: red.signal,
            outputSha256: sha256(combined),
          },
        });
      }
    } finally {
      await removeRegisteredWorktree(repoRoot, mutationRoot, managedOptions);
    }
  }
  assertExactUniqueSet('failure matrix RED/GREEN caseIds', results.map(({ caseId }) => caseId), REQUIRED_CASE_IDS);
  return results.sort((left, right) => left.caseId < right.caseId ? -1 : left.caseId > right.caseId ? 1 : 0);
}

export async function runFailureMatrix(options = {}) {
  const manifestPath = options.manifestPath || DEFAULT_MANIFEST;
  const fixturesPath = options.fixturesPath || DEFAULT_FIXTURES;
  const mutationsPath = options.mutationsPath || DEFAULT_MUTATIONS;
  const cases = validateFailureMatrixManifest(await loadFailureMatrixDocument(manifestPath));
  const fixtures = validateFailureMatrixFixtures(cases, await loadFailureMatrixDocument(fixturesPath));
  const mutations = validateFailureMatrixMutations(await loadFailureMatrixDocument(mutationsPath));
  const repoRoot = options.repoRoot || path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..');
  const frontendRoot = path.join(repoRoot, 'frontend-app');
  const managedOptions = managedCommandOptions(options);
  const vitestCommand = options.vitestCommand;
  const tempRoot = await mkdtemp(path.join(options.tempDirectory ?? os.tmpdir(), 'failure-matrix-'));
  try {
    const git = options.git || ((args) => runCommand('git', args, repoRoot, managedOptions));
    const subjectSha = (await git(['rev-parse', 'HEAD'])).trim();
    const subjectTreeSha = (await git(['rev-parse', 'HEAD^{tree}'])).trim();
    const greenRoot = path.join(tempRoot, 'green-subject');
    const vitestReport = path.join(tempRoot, 'vitest.json');
    let executableEvidence;
    try {
      await runCommand('git', ['worktree', 'add', '--detach', greenRoot, subjectSha], repoRoot, managedOptions);
      const greenFrontendRoot = path.join(greenRoot, 'frontend-app');
      await symlink(path.join(frontendRoot, 'node_modules'), path.join(greenFrontendRoot, 'node_modules'), process.platform === 'win32' ? 'junction' : 'dir');
      await runCommand(
        vitestCommand ?? process.execPath,
        [
          ...(vitestCommand ? [] : [path.join(greenFrontendRoot, 'node_modules', 'vitest', 'vitest.mjs')]), 'run', '--configLoader', 'runner',
          'src/entities/client/model/failureMatrix.test.js',
          'src/entities/client/model/runtimeSlice.test.js',
          'src/pages/chat/composer/ComposerDock.actionFailure.test.jsx',
          'src/pages/settings/SettingsPage.test.jsx',
          'src/features/approval/ui/ApprovalDecisionShelf.test.jsx',
          '--reporter=json',
          `--outputFile=${vitestReport}`,
          '--no-file-parallelism',
          '--maxWorkers=1',
        ],
        greenFrontendRoot,
        managedOptions,
      );
      const frontendEvidence = parseVitestEvidence(JSON.parse(await readFile(vitestReport, 'utf8')), greenFrontendRoot);
      const goCommands = [
        { layer: 'go-codex', pkg: './internal/provider/codexapp' },
        { layer: 'go-claude', pkg: './internal/provider/claudecli' },
        { layer: 'go-turn', pkg: './internal/module/turn' },
        { layer: 'go-wails', pkg: './internal/ui/wails' },
      ];
      const goEvidence = [];
      for (const command of goCommands) {
        const output = await runCommand(
          'go',
          ['test', '-json', command.pkg, '-run', '^TestFailureMatrix', '-count=1'],
          greenRoot,
          managedOptions,
        );
        goEvidence.push(...parseGoEvidence(output, command.layer));
      }
      executableEvidence = [...frontendEvidence, ...goEvidence];
    } finally {
      await removeRegisteredWorktree(repoRoot, greenRoot, managedOptions);
    }
    const result = validateFailureMatrixEvidence(cases, fixtures, executableEvidence);
    const redGreenCases = await runMutationEvidence({
      repoRoot,
      frontendRoot,
      tempRoot,
      subjectSha,
      subjectTreeSha,
      mutations,
      evidence: executableEvidence,
      managedOptions,
      vitestCommand,
    });
    const report = {
      schemaVersion: 1,
      generatedAt: new Date().toISOString(),
      subjectSha,
      subjectTreeSha,
      controlIds: ['E06-failure-matrix', 'C05-provider-rpc-parity', 'T01-red-green-regression', 'T03-wails-integration'],
      cwd: repoRoot,
      argv: process.argv.slice(2),
      redGreenCases,
      ...result,
    };
    const reportPath = options.reportPath || path.join(repoRoot, '.tmp', 'failure-matrix', 'report.json');
    await writeFileWithParents(reportPath, `${JSON.stringify(report, null, 2)}\n`);
    return { report, reportPath };
  } finally {
    await rm(tempRoot, { recursive: true, force: true });
  }
}

async function writeFileWithParents(filePath, contents) {
  const { mkdir } = await import('node:fs/promises');
  await mkdir(path.dirname(filePath), { recursive: true });
  await writeFile(filePath, contents, 'utf8');
}

function canonicalWorktreePath(worktreePath) {
  try {
    return realpathSync(worktreePath);
  } catch (error) {
    if (error.code !== 'ENOENT') throw error;
  }
  try {
    return path.join(realpathSync(path.dirname(worktreePath)), path.basename(worktreePath));
  } catch (error) {
    if (error.code !== 'ENOENT') throw error;
  }
  return path.resolve(worktreePath);
}

async function removeRegisteredWorktree(repoRoot, worktreePath, options) {
  const expectedPath = canonicalWorktreePath(worktreePath);
  const output = await runCommand('git', ['worktree', 'list', '--porcelain'], repoRoot, options);
  const registered = output.split('\n').some((line) => (
    line.startsWith('worktree ') && canonicalWorktreePath(line.slice('worktree '.length)) === expectedPath
  ));
  if (!registered) return;
  await runCommand('git', ['worktree', 'remove', '--force', worktreePath], repoRoot, options);
}

function managedCommandOptions(options) {
  return {
    env: options.commandEnv ?? process.env,
    timeoutMs: options.commandTimeoutMs ?? DEFAULT_COMMAND_TIMEOUT_MS,
    killGraceMs: options.commandKillGraceMs ?? DEFAULT_COMMAND_KILL_GRACE_MS,
    maxBuffer: options.commandMaxBuffer ?? DEFAULT_COMMAND_MAX_BUFFER,
  };
}

function commandFailure(command, args, execution) {
  const status = 'exit=' + execution.exitCode + ' signal=' + (execution.signal || '');
  const reason = execution.error?.message || status;
  return new Error(command + ' ' + args.join(' ') + ' failed: ' + reason + '\n' + execution.stderr);
}

async function captureCommand(command, args, cwd, options) {
  const result = await runManagedCommand(command, args, { cwd, ...options });
  return {
    exitCode: result.status ?? 1,
    signal: result.signal || null,
    stdout: result.stdout,
    stderr: result.stderr,
    timedOut: result.timedOut,
    outputTruncated: result.outputTruncated,
    error: result.error,
  };
}

async function runCommand(command, args, cwd, options) {
  const execution = await captureCommand(command, args, cwd, options);
  if (execution.exitCode === 0 && execution.signal === null && !execution.error && !execution.timedOut) {
    return execution.stdout;
  }
  throw commandFailure(command, args, execution);
}

export function isMain(metaURL, argv1) {
  return argv1 ? path.resolve(fileURLToPath(metaURL)) === path.resolve(argv1) : false;
}

if (isMain(import.meta.url, process.argv[1])) {
  runFailureMatrix().then(({ report, reportPath }) => {
    console.log(`failure matrix validated: status=${report.status} cases=${report.caseCount} tests=${report.testCount} blocked=${report.blockedCases.length} report=${reportPath}`);
  }).catch((error) => {
    console.error(`failure matrix failed: ${error.message}`);
    process.exitCode = 1;
  });
}
