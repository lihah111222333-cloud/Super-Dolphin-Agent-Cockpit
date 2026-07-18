import { spawnSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import process from 'node:process';
import { collectEvidenceProvenance } from './evidence-provenance.mjs';
import { FROZEN_T04_T05_EXECUTION_CLOSURE_PATHS } from './frontend-execution-closure.mjs';
import { requireSubjectSha } from './performance-budget-model.mjs';

const SCRIPT_PATH = fileURLToPath(import.meta.url);
const FRONTEND_ROOT = resolve(dirname(SCRIPT_PATH), '..');
const REPOSITORY_ROOT = resolve(FRONTEND_ROOT, '..');
const DELIVERY_RUNNER_CONTENT_PATHS = FROZEN_T04_T05_EXECUTION_CLOSURE_PATHS;

const DELIVERY_COMMANDS = Object.freeze([
  Object.freeze({
    id: 'frontend-build',
    cwd: 'frontend-app',
    argv: Object.freeze(['npm', 'run', 'build']),
    packageScript: Object.freeze({
      name: 'build',
      value: 'vite build && node scripts/sync-frontend-dist.mjs',
    }),
  }),
  Object.freeze({
    id: 'frontend-embed-verify',
    cwd: '.',
    argv: Object.freeze(['make', 'frontend-embed-verify']),
  }),
  Object.freeze({
    id: 'desktop-start-smoke',
    cwd: 'frontend-app',
    argv: Object.freeze(['npm', 'run', 'smoke:desktop:rpc']),
    packageScript: Object.freeze({
      name: 'smoke:desktop:rpc',
      value: 'node scripts/desktop-smoke.mjs',
    }),
  }),
  Object.freeze({
    id: 'desktop-failure-smoke',
    cwd: 'frontend-app',
    argv: Object.freeze(['npm', 'run', 'smoke:desktop:failure']),
    packageScript: Object.freeze({
      name: 'smoke:desktop:failure',
      value: 'node scripts/desktop-failure-smoke.mjs',
    }),
  }),
]);
const DELIVERY_CASE_IDS = Object.freeze(DELIVERY_COMMANDS.map(({ id }) => id));

function validateDeliveryCaseResult(caseIds, testCount) {
  if (!Array.isArray(caseIds)) throw new TypeError('delivery caseIds must be an array');
  if (testCount !== DELIVERY_CASE_IDS.length || testCount === 0) {
    throw new TypeError(`delivery testCount must be ${DELIVERY_CASE_IDS.length}`);
  }
  if (caseIds.length !== DELIVERY_CASE_IDS.length
    || caseIds.some((caseId, index) => caseId !== DELIVERY_CASE_IDS[index])) {
    throw new TypeError('delivery caseIds must match the frozen command order');
  }
}

function currentCommit(repositoryRoot) {
  const result = spawnSync('git', ['rev-parse', 'HEAD'], {
    cwd: repositoryRoot,
    encoding: 'utf8',
  });
  if (result.status !== 0) throw new Error(`git rev-parse HEAD failed: ${result.stderr}`);
  return result.stdout.trim();
}

function canonicalRepositoryRoot(candidate) {
  const result = spawnSync('git', ['rev-parse', '--show-toplevel'], {
    cwd: resolve(candidate),
    encoding: 'utf8',
  });
  if (result.status !== 0) throw new Error(`git repository discovery failed: ${result.stderr}`);
  return resolve(result.stdout.trim());
}

function inspectDeliveryCommands(packageJSON, makefile) {
  const commands = DELIVERY_COMMANDS.map((command) => {
    if (command.packageScript) {
      const actual = packageJSON?.scripts?.[command.packageScript.name];
      return Object.freeze({
        ...command,
        status: actual === command.packageScript.value ? 'AVAILABLE' : 'MISSING',
        actual: actual ?? null,
      });
    }
    const targetPresent = /^frontend-embed-verify:\s*frontend-app-build$/m.test(makefile)
      && /^\t\.\/scripts\/frontend_embed_verify\.sh$/m.test(makefile);
    return Object.freeze({
      ...command,
      status: targetPresent ? 'AVAILABLE' : 'MISSING',
    });
  });
  const missing = commands.filter(({ status }) => status !== 'AVAILABLE');
  return Object.freeze({
    status: missing.length === 0 ? 'READY' : 'NOT_VERIFIED',
    reason: missing.length === 0 ? '' : `missing executable delivery command(s): ${missing.map(({ id }) => id).join(', ')}`,
    commands: Object.freeze(commands),
  });
}

function runDeliveryCommands(inspected, spawn = spawnSync, repositoryRoot = REPOSITORY_ROOT) {
  if (inspected.status !== 'READY') {
    return Object.freeze({
      status: 'NOT_VERIFIED',
      reason: inspected.reason,
      executedCommands: 0,
      commands: inspected.commands,
    });
  }
  const results = [];
  for (const command of inspected.commands) {
    const [program, ...args] = command.argv;
    const cwd = command.cwd === '.' ? repositoryRoot : resolve(repositoryRoot, command.cwd);
    const startedAtMs = Date.now();
    const result = spawn(program, args, { cwd, encoding: 'utf8', stdio: 'pipe' });
    const finishedAtMs = Date.now();
    results.push(Object.freeze({
      id: command.id,
      argv: command.argv,
      cwd: command.cwd,
      exitCode: result.status,
      signal: result.signal,
      startedAt: new Date(startedAtMs).toISOString(),
      finishedAt: new Date(finishedAtMs).toISOString(),
      durationMs: finishedAtMs - startedAtMs,
      status: result.status === 0 ? 'PASS' : 'FAIL',
    }));
    if (result.status !== 0) {
      return Object.freeze({
        status: 'FAIL',
        reason: `${command.id} failed with exit ${result.status} signal ${result.signal || ''}`,
        executedCommands: results.length,
        commands: Object.freeze(results),
      });
    }
  }
  return Object.freeze({
    status: 'PASS',
    reason: '',
    executedCommands: results.length,
    commands: Object.freeze(results),
  });
}

function parseArguments(args) {
  const options = { mode: '', repositoryRoot: REPOSITORY_ROOT, subjectSha: '' };
  for (let index = 0; index < args.length; index += 1) {
    const arg = args[index];
    if (arg === '--inspect' || arg === '--verify') {
      if (options.mode) throw new TypeError('choose exactly one of --inspect or --verify');
      options.mode = arg.slice(2);
    } else if (arg === '--subject') {
      options.subjectSha = args[++index] || '';
    } else if (arg === '--repo') {
      options.repositoryRoot = canonicalRepositoryRoot(args[++index] || '');
    } else {
      throw new TypeError(`unsupported delivery smoke argument: ${arg}`);
    }
  }
  if (!options.mode) throw new TypeError('one of --inspect or --verify is required');
  const head = currentCommit(options.repositoryRoot);
  if (!options.subjectSha) options.subjectSha = head;
  requireSubjectSha(options.subjectSha, head);
  return options;
}

if (process.argv[1] && resolve(process.argv[1]) === SCRIPT_PATH) {
  try {
    const options = parseArguments(process.argv.slice(2));
    const frontendRoot = resolve(options.repositoryRoot, 'frontend-app');
    const inspected = inspectDeliveryCommands(
      JSON.parse(readFileSync(resolve(frontendRoot, 'package.json'), 'utf8')),
      readFileSync(resolve(options.repositoryRoot, 'Makefile'), 'utf8'),
    );
    const verdict = options.mode === 'verify'
      ? runDeliveryCommands(inspected, spawnSync, options.repositoryRoot)
      : inspected;
    const context = collectEvidenceProvenance({
      repositoryRoot: options.repositoryRoot,
      runnerRepositoryRoot: REPOSITORY_ROOT,
      runnerContentPaths: DELIVERY_RUNNER_CONTENT_PATHS,
      recordBaselineAudit: false,
      runnerId: 'frontend-delivery-smoke',
      subjectSha: options.subjectSha,
    });
    validateDeliveryCaseResult(DELIVERY_CASE_IDS, DELIVERY_CASE_IDS.length);
    process.stdout.write(`${JSON.stringify({
      schemaVersion: 1,
      metricId: 'T05-build-embed-smoke',
      subjectSha: options.subjectSha,
      ...context,
      caseIds: DELIVERY_CASE_IDS,
      testCount: DELIVERY_CASE_IDS.length,
      verdict,
    })}\n`);
    if (options.mode === 'verify' && verdict.status !== 'PASS') process.exitCode = 2;
  } catch (error) {
    process.stderr.write(`delivery smoke failed: ${error.message}\n`);
    process.exitCode = 1;
  }
}

export {
  DELIVERY_CASE_IDS,
  DELIVERY_COMMANDS,
  DELIVERY_RUNNER_CONTENT_PATHS,
  inspectDeliveryCommands,
  parseArguments,
  runDeliveryCommands,
  validateDeliveryCaseResult,
};
