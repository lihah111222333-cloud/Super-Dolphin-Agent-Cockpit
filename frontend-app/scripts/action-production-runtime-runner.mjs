import { createHash } from 'node:crypto';
import { readFileSync, realpathSync } from 'node:fs';
import { mkdir, mkdtemp, readFile, rm, symlink, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import {
  mutateProductionBindingsSource,
  runActionProducerGuard,
} from './action-producer-guard.mjs';
import { runManagedCommand } from './managed-command.mjs';
import { productionActionFailureMatrixTitle } from '../src/shared/ui/productionActionFailureMatrixTitles.js';

const APP_ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const REPO_ROOT = path.resolve(APP_ROOT, '..');
const matrix = JSON.parse(readFileSync(path.join(APP_ROOT, 'config/action-producer-test-matrix.json'), 'utf8'));
const registry = JSON.parse(readFileSync(path.join(APP_ROOT, 'config/action-producer-registry.json'), 'utf8'));
const DEFAULT_COMMAND_TIMEOUT_MS = 15 * 60 * 1_000;
const DEFAULT_COMMAND_KILL_GRACE_MS = 1_000;
const DEFAULT_COMMAND_MAX_BUFFER = 16 * 1024 * 1024;

function sha256(value) {
  return createHash('sha256').update(value).digest('hex');
}

function parseCellVitestResult(result) {
  const output = `${result.stdout}\n${result.stderr}`;
  if (result.exitCode !== 0 || result.signal !== null || result.error || result.timedOut) {
    throw new Error('production action matrix Vitest GREEN must exit zero');
  }
  let summary;
  try {
    summary = JSON.parse(result.stdout);
  } catch (error) {
    throw new Error(`production action matrix Vitest JSON report is invalid: ${error.message}`);
  }
  if (summary.numTotalTests !== matrix.cells.length || summary.numPassedTests !== matrix.cells.length
    || summary.numFailedTests !== 0 || summary.numPendingTests !== 0 || !Array.isArray(summary.testResults)) {
    throw new Error('production action matrix Vitest must execute every cell exactly once');
  }
  const assertions = summary.testResults.flatMap(({ assertionResults = [] }) => assertionResults);
  if (assertions.length !== matrix.cells.length) {
    throw new Error('production action matrix Vitest report does not contain one assertion per cell');
  }
  const assertionsByTitle = new Map(assertions.map((assertion) => [assertion.title, assertion]));
  if (assertionsByTitle.size !== assertions.length) throw new Error('production action matrix Vitest report has duplicate assertion titles');
  return {
    outputSha256: sha256(output),
    vitest: {
      numTotalTests: summary.numTotalTests,
      numPassedTests: summary.numPassedTests,
      numFailedTests: summary.numFailedTests,
      numPendingTests: summary.numPendingTests,
    },
    assertionsByTitle,
  };
}

function managedCommandOptions(options) {
  return {
    env: options.commandEnv ?? process.env,
    timeoutMs: options.commandTimeoutMs ?? DEFAULT_COMMAND_TIMEOUT_MS,
    killGraceMs: options.commandKillGraceMs ?? DEFAULT_COMMAND_KILL_GRACE_MS,
    maxBuffer: options.commandMaxBuffer ?? DEFAULT_COMMAND_MAX_BUFFER,
  };
}

function commandFailure(command, args, result) {
  const status = 'exit=' + result.exitCode + ' signal=' + (result.signal || '');
  const reason = result.error?.message || status;
  return new Error(command + ' ' + args.join(' ') + ' failed: ' + reason + '\n' + result.stderr);
}

async function capture(command, args, cwd, options) {
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

async function requireSuccess(command, args, cwd, options) {
  const result = await capture(command, args, cwd, options);
  if (result.exitCode !== 0 || result.signal !== null || result.error || result.timedOut) {
    throw commandFailure(command, args, result);
  }
  return result.stdout;
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
  const output = await requireSuccess('git', ['worktree', 'list', '--porcelain'], repoRoot, options);
  const registered = output.split('\n').some((line) => (
    line.startsWith('worktree ') && canonicalWorktreePath(line.slice('worktree '.length)) === expectedPath
  ));
  if (!registered) return;
  await requireSuccess('git', ['worktree', 'remove', '--force', worktreePath], repoRoot, options);
}

export async function runActionProductionRuntime(options = {}) {
  const repoRoot = options.repoRoot || REPO_ROOT;
  const appRoot = path.join(repoRoot, 'frontend-app');
  const managedOptions = managedCommandOptions(options);
  const vitestCommand = options.vitestCommand;
  const structural = runActionProducerGuard({ root: appRoot });
  const anchors = matrix.runtimeBindings;
  if (!Array.isArray(anchors) || anchors.length === 0) throw new Error('production runtime action anchors must not be empty');
  const subjectSha = (await requireSuccess('git', ['rev-parse', 'HEAD'], repoRoot, managedOptions)).trim();
  const subjectTreeSha = (await requireSuccess('git', ['rev-parse', 'HEAD^{tree}'], repoRoot, managedOptions)).trim();
  const tempRoot = await mkdtemp(path.join(options.tempDirectory ?? os.tmpdir(), 'action-production-runtime-'));
  const runtimeCases = [];
  if (!Array.isArray(matrix.cells) || matrix.cells.length === 0) throw new Error('production action matrix must not be empty');
  let matrixExecution;
  try {
    for (const anchor of anchors) {
      const bindings = structural.bindings.filter(({ actionId, sourcePath }) => (
        actionId === anchor.actionId && sourcePath === anchor.sourcePath
      ));
      if (bindings.length === 0) throw new Error(`${anchor.actionId}: runtime anchor has no production binding`);
      const mutationRoot = path.join(tempRoot, anchor.actionId.replace(/[^a-z0-9.-]+/giu, '-'));
      try {
        await requireSuccess('git', ['worktree', 'add', '--detach', mutationRoot, subjectSha], repoRoot, managedOptions);
        await symlink(path.join(appRoot, 'node_modules'), path.join(mutationRoot, 'frontend-app', 'node_modules'), process.platform === 'win32' ? 'junction' : 'dir');
        const testArgv = [
          vitestCommand ?? process.execPath, ...(vitestCommand ? [] : [path.join(mutationRoot, 'frontend-app', 'node_modules', 'vitest', 'vitest.mjs')]), 'run', '--configLoader', 'runner', anchor.testFile,
          '-t', anchor.testName, '--no-file-parallelism', '--maxWorkers=1',
        ];
        const detachedAppRoot = path.join(mutationRoot, 'frontend-app');
        const sourceFile = path.join(mutationRoot, 'frontend-app', anchor.sourcePath);
        const source = await readFile(sourceFile, 'utf8');
        const testFile = path.join(mutationRoot, 'frontend-app', anchor.testFile);
        const testSource = await readFile(testFile);
        const green = await capture(testArgv[0], testArgv.slice(1), detachedAppRoot, managedOptions);
        const greenOutput = `${green.stdout}\n${green.stderr}`;
        if (green.exitCode !== 0 || green.signal !== null || green.error || green.timedOut) {
          throw new Error(`${anchor.actionId}: production runtime named GREEN must exit zero`);
        }
        const mutated = mutateProductionBindingsSource(source, bindings);
        await writeFile(sourceFile, mutated, 'utf8');
        const red = await capture(testArgv[0], testArgv.slice(1), detachedAppRoot, managedOptions);
        const redOutput = `${red.stdout}\n${red.stderr}`;
        if (!(red.exitCode > 0) || red.signal !== null || red.error || red.timedOut || !redOutput.includes(anchor.testName)) {
          throw new Error(`${anchor.actionId}: production runtime mutation must produce a named non-zero RED`);
        }
        runtimeCases.push({
          semanticClass: anchor.semanticClass,
          actionId: anchor.actionId,
          sourcePath: anchor.sourcePath,
          sourceSha256: sha256(source),
          mutatedSha256: sha256(mutated),
          handlers: [...new Set(bindings.flatMap(({ handlers }) => handlers))].sort(),
          bindingLocations: bindings.map(({ line, column }) => ({ line, column })),
          testFile: anchor.testFile,
          testName: anchor.testName,
          testFileSha256: sha256(testSource),
          green: {
            cwd: 'frontend-app',
            argv: testArgv,
            exitCode: green.exitCode,
            signal: green.signal,
            outputSha256: sha256(greenOutput),
          },
          red: {
            cwd: 'frontend-app',
            argv: testArgv,
            exitCode: red.exitCode,
            signal: red.signal,
            outputSha256: sha256(redOutput),
          },
        });
      } finally {
        await removeRegisteredWorktree(repoRoot, mutationRoot, managedOptions);
      }
    }

    const testFile = 'src/shared/ui/productionActionFailureMatrix.test.js';
    const testSource = await readFile(path.join(appRoot, testFile));
    const argv = [
      vitestCommand ?? process.execPath, ...(vitestCommand ? [] : [path.join(appRoot, 'node_modules', 'vitest', 'vitest.mjs')]), 'run', '--configLoader', 'runner', testFile,
      '--reporter=json', '--no-file-parallelism', '--maxWorkers=1',
    ];
    const green = await capture(argv[0], argv.slice(1), appRoot, managedOptions);
    const parsed = parseCellVitestResult(green);
    matrixExecution = {
      testFile,
      testFileSha256: sha256(testSource),
      cwd: 'frontend-app',
      argv,
      exitCode: green.exitCode,
      signal: green.signal,
      outputSha256: parsed.outputSha256,
      vitest: parsed.vitest,
      assertionsByTitle: parsed.assertionsByTitle,
    };
  } finally {
    await rm(tempRoot, { recursive: true, force: true });
  }
  const coveredActionIds = anchors.map(({ actionId }) => actionId).sort();
  const allActionIds = registry.coveredProducers.map(({ actionId }) => actionId).sort();
  const cellResults = matrix.cells.map((cell, index) => {
    const bindingKeys = structural.bindings
      .filter(({ actionId }) => actionId === cell.actionId)
      .map(({ sourcePath, line, column }) => `${sourcePath}:${line}:${column}`)
      .sort();
    if (bindingKeys.length === 0) throw new Error(`${cell.actionId}: matrix cell has no production binding`);
    const testName = productionActionFailureMatrixTitle(index, cell);
    const assertion = matrixExecution.assertionsByTitle.get(testName);
    if (!assertion || assertion.status !== 'passed') {
      throw new Error(`${cell.actionId}/${cell.errorSource}: production matrix has no passing named cell test`);
    }
    return {
      actionId: cell.actionId,
      errorSource: cell.errorSource,
      evidence: [...cell.evidence],
      bindingKeys,
      testName,
      testFileSha256: matrixExecution.testFileSha256,
      vitest: { status: assertion.status, title: assertion.title },
    };
  });
  const report = {
    schemaVersion: 2,
    generatedAt: new Date().toISOString(),
    subjectSha,
    subjectTreeSha,
    controlId: 'T02-critical-action-coverage',
    actionIds: allActionIds,
    structuralActionCount: allActionIds.length,
    errorSourceCaseCount: matrix.cells.length,
    bindingCount: structural.bindings.length,
    bindings: structural.bindings,
    runtimeEvidenceScope: 'five-semantic-class-anchors',
    runtimeAnchorCount: anchors.length,
    runtimeClaimsFullMatrix: false,
    requiredRuntimeActionIds: coveredActionIds,
    requiredRuntimeSemanticClasses: anchors.map(({ semanticClass }) => semanticClass).sort(),
    runtimeCases,
    matrixExecution: {
      testFile: matrixExecution.testFile,
      testFileSha256: matrixExecution.testFileSha256,
      cwd: matrixExecution.cwd,
      argv: matrixExecution.argv,
      exitCode: matrixExecution.exitCode,
      signal: matrixExecution.signal,
      outputSha256: matrixExecution.outputSha256,
      vitest: matrixExecution.vitest,
    },
    cellResults,
    testCount: cellResults.length,
    status: 'covered',
  };
  const reportPath = options.reportPath || path.join(repoRoot, '.tmp', 'action-producer', 'runtime-report.json');
  await writeFileEnsured(reportPath, `${JSON.stringify(report, null, 2)}\n`);
  return report;
}

async function writeFileEnsured(file, contents) {
  await mkdir(path.dirname(file), { recursive: true });
  await writeFile(file, contents, 'utf8');
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const report = await runActionProductionRuntime();
  process.stdout.write(`action production runtime evidence: status=${report.status} structural=${report.structuralActionCount} matrix=${report.errorSourceCaseCount} runtime=${report.requiredRuntimeActionIds.length}\n`);
}
