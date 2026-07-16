import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';

const scriptPath = fileURLToPath(import.meta.url);
const appRoot = path.resolve(path.dirname(scriptPath), '..');
const repoRoot = path.resolve(appRoot, '..');
const controls = readJSON('frontend-maintainability-controls.json');
const fixtures = readJSON('frontend-maintainability-red-fixtures.json');
const baseline = readJSON('frontend-maintainability-baseline.json');
const controlIDs = new Set(controls.controls.map(({ id }) => id));
const weakCommands = new Set([':', 'echo', 'false', 'true']);
const supportedProbes = new Set(['terminalTruth', 'promptHistoryVisibleError', 'criticalTypecheck', 'redMatrix', 'actionRegistry', 'notImplemented']);

function readJSON(name) {
  return JSON.parse(fs.readFileSync(path.join(path.dirname(scriptPath), name), 'utf8'));
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

function source(relativePath) {
  return fs.readFileSync(path.join(appRoot, relativePath), 'utf8');
}

function terminalTruthCheck() {
  return controls.controls.find(({ id }) => id === 'E01-terminal-truth')?.allOf?.[0];
}

function terminalTruthFingerprint() {
  const paths = [
    'src/entities/client/model/helpers/assistantEventRuntime.js',
    'src/entities/client/model/helpers/runtimeAssistantTimelineMerge.js',
    'src/entities/client/model/runtimeAssistantTimeline.js',
    'src/entities/client/model/useClientStore.test.js',
  ];
  const hash = createHash('sha256');
  for (const relativePath of paths) {
    hash.update(relativePath);
    hash.update('\0');
    hash.update(source(relativePath));
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
  if (byName.size !== expected.testNames.length) return 'FAIL';
  return expected.testNames.every((name) => byName.get(name) === 'passed') ? 'PASS' : 'FAIL';
}

function collectTerminalTruthEvidence() {
  const check = terminalTruthCheck();
  const fingerprint = terminalTruthFingerprint();
  const vitestPath = path.join(appRoot, 'node_modules', 'vitest', 'vitest.mjs');
  try {
    const output = execFileSync(process.execPath, [
      vitestPath,
      'run',
      'src/entities/client/model/useClientStore.test.js',
      '--reporter=json',
      '--no-file-parallelism',
      '--maxWorkers=1',
    ], {
      cwd: appRoot,
      encoding: 'utf8',
      stdio: ['ignore', 'pipe', 'pipe'],
      timeout: check.timeoutMs,
    });
    const expectedNames = new Set(check.testNames);
    return {
      fingerprint,
      testResults: terminalTruthTestResults(JSON.parse(output)).filter(({ name }) => expectedNames.has(name)),
    };
  } catch (error) {
    return { fingerprint, failed: true, testResults: [], summary: error.message };
  }
}

function terminalTruthProbeResult() {
  const check = terminalTruthCheck();
  const expected = { fingerprint: terminalTruthFingerprint(), testNames: check.testNames };
  return terminalTruthEvidenceStatus(collectTerminalTruthEvidence(), expected);
}

function sourceHasPromptHistoryConsoleOnly() {
  const dock = source('src/pages/chat/composer/ComposerDock.jsx');
  const action = source('src/shared/ui/runUIAction.js');
  return dock.includes('runUIAction(() => promptHistory[direction]());')
    && action.includes('logger = console.error')
    && !dock.includes('promptHistory[direction](), { onError');
}

function sourceHasCriticalTypecheckGap() {
  const tsconfig = JSON.parse(source('tsconfig.contracts.json'));
  return tsconfig.compilerOptions?.checkJs !== true || tsconfig.compilerOptions?.strict !== true;
}

export function validateConfiguration(config = controls, fixtureDocument = fixtures) {
  if (config.schemaVersion !== 1 || fixtureDocument.schemaVersion !== 1) fail('unsupported scorer schema version');
  if (!Array.isArray(config.controls) || config.controls.length !== 25) fail('controls must contain exactly 25 entries');
  exactSet(Object.keys(config.weights || {}), ['E', 'A', 'C', 'T', 'P'], 'dimension weights');
  if (Object.values(config.weights).reduce((sum, weight) => sum + weight, 0) !== 100) fail('dimension weights must total 100');
  const fixtureIDs = fixtureDocument.fixtures.map(({ id }) => id);
  if (new Set(fixtureIDs).size !== fixtureIDs.length) fail('duplicate RED fixture id');
  const seen = new Set();
  const referencedFixtures = new Set();
  for (const control of config.controls) {
    if (!controlIDs.has(control.id) || seen.has(control.id)) fail(`duplicate or unknown control id: ${control.id}`);
    seen.add(control.id);
    if ('status' in control || 'score' in control) fail(`hand-authored result is forbidden: ${control.id}`);
    if (!Number.isFinite(control.points) || control.points <= 0 || !Array.isArray(control.allOf) || control.allOf.length === 0) {
      fail(`invalid control shape: ${control.id}`);
    }
    if (!Object.hasOwn(config.weights, control.dimension)) fail(`unknown control dimension: ${control.id}`);
    for (const check of control.allOf) {
      if (!['command', 'probe'].includes(check.kind) || typeof check.cwd !== 'string' || !Array.isArray(check.argv) || check.argv.length === 0) {
        fail(`invalid runner command: ${control.id}`);
      }
      if (weakCommands.has(check.argv[0]) || check.argv.includes('--help') || !Number.isInteger(check.timeoutMs) || check.timeoutMs <= 0) {
        fail(`weak runner command: ${control.id}`);
      }
      if ('status' in check || 'score' in check) fail(`hand-authored check result is forbidden: ${control.id}`);
      if (check.kind === 'probe' && !supportedProbes.has(check.probe)) fail(`unknown scorer probe: ${control.id}`);
      if (!Array.isArray(check.caseIds) || check.caseIds.length === 0 || !Number.isInteger(check.testCount) || check.testCount <= 0) {
        fail(`zero-test runner evidence: ${control.id}`);
      }
      if (new Set(check.caseIds).size !== check.caseIds.length) fail(`duplicate fixture case: ${control.id}`);
      if (control.id === 'E01-terminal-truth') {
        if (!Array.isArray(check.testNames) || check.testNames.length !== check.testCount || new Set(check.testNames).size !== check.testNames.length) {
          fail('terminal truth named test evidence mismatch');
        }
      }
      for (const caseID of check.caseIds) {
        if (caseID.includes('frontend-')) continue;
        if (!fixtureIDs.includes(caseID)) fail(`missing fixture case: ${caseID}`);
        referencedFixtures.add(caseID);
      }
    }
  }
  exactSet(seen, controlIDs, 'control ids');
  exactSet(referencedFixtures, fixtureIDs, 'fixture coverage');
  for (const dimension of Object.keys(config.weights)) {
    const points = config.controls
      .filter((control) => control.dimension === dimension)
      .reduce((sum, control) => sum + control.points, 0);
    if (points !== 100) fail(`dimension points must total 100: ${dimension}`);
  }
  if (baseline.baseSha.length !== 40 || baseline.planSnapshotSha.length !== 40) fail('baseline provenance is incomplete');
  return true;
}

function probeResult(probe) {
  if (probe === 'terminalTruth') return terminalTruthProbeResult();
  if (probe === 'promptHistoryVisibleError') return sourceHasPromptHistoryConsoleOnly() ? 'FAIL' : 'PASS';
  if (probe === 'criticalTypecheck') return sourceHasCriticalTypecheckGap() ? 'FAIL' : 'PASS';
  if (probe === 'redMatrix') return 'FAIL';
  if (probe === 'actionRegistry') return 'FAIL';
  return 'NOT_VERIFIED';
}

function runCommand(check) {
  const [command, ...args] = check.argv;
  const cwd = path.resolve(repoRoot, check.cwd);
  try {
    const output = execFileSync(command, args, {
      cwd,
      encoding: 'utf8',
      stdio: ['ignore', 'pipe', 'pipe'],
      timeout: check.timeoutMs,
    });
    return { status: 'PASS', summary: output.trim().slice(-600) };
  }
  catch (error) {
    const output = `${error.stdout || ''}${error.stderr || ''}`.trim();
    return { status: 'FAIL', summary: output.slice(-1200) || error.message };
  }
}

function evaluateCheck(check, runCommands) {
  if (check.kind === 'probe') return { status: probeResult(check.probe), summary: check.probe };
  if (!runCommands) return { status: 'NOT_VERIFIED', summary: 'command execution was not requested' };
  return runCommand(check);
}

function controlStatus(results) {
  if (results.some(({ status }) => status === 'FAIL')) return 'FAIL';
  if (results.every(({ status }) => status === 'PASS')) return 'PASS';
  return 'NOT_VERIFIED';
}

export function scoreCurrentTree({ runCommands = false } = {}) {
  validateConfiguration();
  const scoredControls = controls.controls.map((control) => {
    const evidence = control.allOf.map((check) => ({ ...check, ...evaluateCheck(check, runCommands) }));
    return { id: control.id, dimension: control.dimension, points: control.points, status: controlStatus(evidence), evidence };
  });
  const dimensions = {};
  for (const dimension of Object.keys(controls.weights)) {
    const members = scoredControls.filter((control) => control.dimension === dimension);
    const earned = members.filter((control) => control.status === 'PASS').reduce((sum, control) => sum + control.points, 0);
    const total = members.reduce((sum, control) => sum + control.points, 0);
    dimensions[dimension] = { earned, total, score: total === 0 ? 0 : (earned / total) * 100, weight: controls.weights[dimension] };
  }
  const rawBasisPoints = Object.values(dimensions).reduce((sum, dimension) => sum + (dimension.score * dimension.weight), 0);
  return {
    subjectSha: git(['rev-parse', 'HEAD']),
    subjectTree: git(['rev-parse', 'HEAD^{tree}']),
    baseline,
    controls: scoredControls,
    dimensions,
    rawBasisPoints: Math.round(rawBasisPoints),
    displayScore: Number((rawBasisPoints / 100).toFixed(1)),
  };
}

function git(args) {
  return execFileSync('git', args, { cwd: repoRoot, encoding: 'utf8' }).trim();
}

function requireCleanSubject(subject) {
  if (subject !== git(['rev-parse', 'HEAD'])) fail('subject must equal current clean HEAD');
  if (git(['status', '--porcelain']).length > 0) fail('scorer rejects dirty or untracked worktrees');
}

function printScore(result) {
  for (const control of result.controls) process.stdout.write(`${control.id}\t${control.status}\n`);
  process.stdout.write(`SCORE\t${result.displayScore.toFixed(1)}\t${result.subjectSha}\n`);
}

if (process.argv[1] && path.resolve(process.argv[1]) === scriptPath) {
  const args = process.argv.slice(2);
  if (args.length === 1 && args[0] === '--validate') {
    validateConfiguration();
    process.stdout.write('frontend maintainability scorer configuration valid\n');
  }
  else if (args[0] === '--probe' && args.length === 2) {
    process.stdout.write(`${probeResult(args[1])}\n`);
  }
  else if (args[0] === '--score') {
    const runCommands = args.includes('--run');
    const subjectIndex = args.indexOf('--subject');
    if (subjectIndex !== -1) requireCleanSubject(args[subjectIndex + 1]);
    printScore(scoreCurrentTree({ runCommands }));
  }
  else {
    fail('usage: --validate | --probe <name> | --score [--run] [--subject <HEAD>]');
  }
}

export { controlStatus, probeResult, sourceHasPromptHistoryConsoleOnly };
