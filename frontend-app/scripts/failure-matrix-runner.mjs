import { spawn } from 'node:child_process';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const DEFAULT_MANIFEST = path.join(path.dirname(fileURLToPath(import.meta.url)), 'failure-matrix-cases.json');
const DEFAULT_FIXTURES = path.join(path.dirname(fileURLToPath(import.meta.url)), 'failure-matrix-fixtures.json');
const CASE_ID_PATTERN = /^FM-\d{2}$/u;
const CASE_STATUSES = new Set(['covered', 'blocked']);
const REQUIRED_CASE_IDS = Object.freeze(
  Array.from({ length: 24 }, (_, index) => `FM-${String(index + 1).padStart(2, '0')}`),
);
const REQUIRED_EVIDENCE_COUNT = 27;

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
      left.caseId.localeCompare(right.caseId) || left.layer.localeCompare(right.layer)
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

export function parseVitestEvidence(report) {
  const suites = Array.isArray(report?.testResults) ? report.testResults : [];
  const evidence = [];
  for (const suite of suites) {
    for (const assertion of suite.assertionResults || []) {
      const title = String(assertion.fullName || assertion.title || '');
      const match = title.match(/\bmatrix:(FM-\d{2})\s+layer:([a-z-]+)\b/u);
      if (!match || assertion.status !== 'passed') continue;
      evidence.push({ caseId: match[1], layer: match[2], test: title });
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

export async function runFailureMatrix(options = {}) {
  const manifestPath = options.manifestPath || DEFAULT_MANIFEST;
  const fixturesPath = options.fixturesPath || DEFAULT_FIXTURES;
  const cases = validateFailureMatrixManifest(await loadFailureMatrixDocument(manifestPath));
  const fixtures = validateFailureMatrixFixtures(cases, await loadFailureMatrixDocument(fixturesPath));
  const repoRoot = options.repoRoot || path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..');
  const frontendRoot = path.join(repoRoot, 'frontend-app');
  const tempRoot = await mkdtemp(path.join(os.tmpdir(), 'failure-matrix-'));
  try {
    const vitestReport = path.join(tempRoot, 'vitest.json');
    await runCommand(
      path.join(frontendRoot, 'node_modules', '.bin', 'vitest'),
      [
        'run',
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
      frontendRoot,
    );
    const frontendEvidence = parseVitestEvidence(JSON.parse(await readFile(vitestReport, 'utf8')));
    const goCommands = [
      { layer: 'go-codex', pkg: './internal/provider/codexapp' },
      { layer: 'go-claude', pkg: './internal/provider/claudecli' },
      { layer: 'go-turn', pkg: './internal/module/turn' },
      { layer: 'go-wails', pkg: './internal/ui/wails' },
    ];
    const goEvidence = [];
    for (const command of goCommands) {
      const output = await runCommand('go', ['test', '-json', command.pkg, '-run', '^TestFailureMatrix', '-count=1'], repoRoot, true);
      goEvidence.push(...parseGoEvidence(output, command.layer));
    }
    const result = validateFailureMatrixEvidence(cases, fixtures, [...frontendEvidence, ...goEvidence]);
    const git = options.git || ((args) => runCommand('git', args, repoRoot, true));
    const report = {
      schemaVersion: 1,
      generatedAt: new Date().toISOString(),
      subjectSha: (await git(['rev-parse', 'HEAD'])).trim(),
      subjectTreeSha: (await git(['rev-parse', 'HEAD^{tree}'])).trim(),
      controlIds: ['E06-failure-matrix', 'C05-provider-rpc-parity', 'T01-red-green-regression', 'T03-wails-integration'],
      cwd: repoRoot,
      argv: process.argv.slice(2),
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

function runCommand(command, args, cwd, capture = false) {
  return new Promise((resolve, reject) => {
    const child = spawn(command, args, { cwd, stdio: capture ? ['ignore', 'pipe', 'pipe'] : 'inherit' });
    let stdout = '';
    let stderr = '';
    child.stdout?.on('data', (chunk) => { stdout += chunk; });
    child.stderr?.on('data', (chunk) => { stderr += chunk; });
    child.once('error', reject);
    child.once('exit', (code, signal) => {
      if (code === 0) {
        resolve(stdout);
        return;
      }
      reject(new Error(`${command} ${args.join(' ')} failed: exit=${code} signal=${signal || ''}\n${stderr}`));
    });
  });
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
