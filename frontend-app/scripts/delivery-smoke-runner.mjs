import { spawnSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import process from 'node:process';
import { collectEvidenceProvenance } from './evidence-provenance.mjs';
import { FROZEN_T04_T05_EXECUTION_CLOSURE_PATHS } from './frontend-execution-closure.mjs';
import { repositoryLocalGitEnvironment } from './runtime/git-environment.mjs';
import { requireSubjectSha } from './performance-budget-model.mjs';
import { runManagedCommand } from './managed-command.mjs';

const SCRIPT_PATH = fileURLToPath(import.meta.url);
const FRONTEND_ROOT = resolve(dirname(SCRIPT_PATH), '..');
const REPOSITORY_ROOT = resolve(FRONTEND_ROOT, '..');
const DELIVERY_RUNNER_CONTENT_PATHS = FROZEN_T04_T05_EXECUTION_CLOSURE_PATHS;
export const DELIVERY_COMMAND_TIMEOUT_MS = 900_000;
const DELIVERY_DIAGNOSTIC_MAX_BYTES = 4 * 1024;
const RUNNER_TRUNCATION_MARKER = '\n...[runner truncated]...\n';

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
    argv: Object.freeze(['make', 'frontend-embed-verify-after-build']),
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
const DESKTOP_FAILURE_SMOKE_CONFLICT_ENV_KEYS = Object.freeze([
  'VITE_DEV_URL',
  'FRONTEND_DEVSERVER_URL',
]);
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
    env: repositoryLocalGitEnvironment(),
  });
  if (result.status !== 0) throw new Error(`git rev-parse HEAD failed: ${result.stderr}`);
  return result.stdout.trim();
}

function canonicalRepositoryRoot(candidate) {
  const result = spawnSync('git', ['rev-parse', '--show-toplevel'], {
    cwd: resolve(candidate),
    encoding: 'utf8',
    env: repositoryLocalGitEnvironment(),
  });
  if (result.status !== 0) throw new Error(`git repository discovery failed: ${result.stderr}`);
  return resolve(result.stdout.trim());
}

function requireCleanDeliveryWorktree(repositoryRoot) {
  const result = spawnSync('git', ['status', '--porcelain=v1', '--untracked-files=all'], {
    cwd: repositoryRoot,
    encoding: 'utf8',
    env: repositoryLocalGitEnvironment(),
  });
  if (result.status !== 0) throw new Error(`git worktree status failed: ${result.stderr}`);
  if (result.stdout.trim()) throw new Error('delivery smoke requires a clean worktree');
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
    const targetPresent = /^frontend-embed-verify-after-build:\s*$/m.test(makefile)
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

function redactDiagnostic(value) {
  return value
    .replace(/Authorization:\s*Bearer\s+[^\s"']+/giu, 'Authorization: Bearer [redacted]')
    .replace(
      /\b(api[_-]?key|access[_-]?token|token|password|passwd|secret)\b(\s*[:=]\s*)(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s,;]+)/giu,
      '$1$2[redacted]',
    )
    .replace(/\b(?:sk-[a-z0-9_-]{8,}|gh[pousr]_[a-z0-9_]{8,}|github_pat_[a-z0-9_]{8,})\b/giu, '[redacted]')
    .replace(/t03-raw-provider-secret-do-not-persist/gu, '[redacted]');
}

function safeErrorMessage(error) {
  if (typeof error === 'string') return error;
  if (error && typeof error.message === 'string') return error.message;
  return '';
}

function utf8Prefix(buffer, maxBytes) {
  let end = Math.min(buffer.byteLength, Math.max(0, maxBytes));
  while (end > 0 && end < buffer.byteLength && (buffer[end] & 0xC0) === 0x80) end -= 1;
  return buffer.subarray(0, end).toString('utf8');
}

function utf8Suffix(buffer, maxBytes) {
  let start = Math.max(0, buffer.byteLength - Math.max(0, maxBytes));
  while (start < buffer.byteLength && (buffer[start] & 0xC0) === 0x80) start += 1;
  return buffer.subarray(start).toString('utf8');
}

function truncateUtf8(value, maxBytes) {
  const buffer = Buffer.from(value, 'utf8');
  if (buffer.byteLength <= maxBytes) return value;
  const markerBytes = Buffer.byteLength(RUNNER_TRUNCATION_MARKER, 'utf8');
  if (maxBytes <= markerBytes) return utf8Prefix(Buffer.from(RUNNER_TRUNCATION_MARKER), maxBytes);
  const retainedBytes = maxBytes - markerBytes;
  const prefixBytes = Math.ceil(retainedBytes / 2);
  const suffixBytes = retainedBytes - prefixBytes;
  return `${utf8Prefix(buffer, prefixBytes)}${RUNNER_TRUNCATION_MARKER}${utf8Suffix(buffer, suffixBytes)}`;
}

function boundedDiagnostics(result) {
  const fields = [
    redactDiagnostic(typeof result.stdout === 'string' ? result.stdout : ''),
    redactDiagnostic(typeof result.stderr === 'string' ? result.stderr : ''),
    redactDiagnostic(safeErrorMessage(result.error)),
  ];
  const byteLengths = fields.map((value) => Buffer.byteLength(value, 'utf8'));
  const totalBytes = byteLengths.reduce((total, length) => total + length, 0);
  let runnerTruncated = totalBytes > DELIVERY_DIAGNOSTIC_MAX_BYTES;
  let bounded = fields;
  if (runnerTruncated) {
    let low = 0;
    let high = DELIVERY_DIAGNOSTIC_MAX_BYTES;
    while (low < high) {
      const candidate = Math.ceil((low + high) / 2);
      const candidateTotal = byteLengths.reduce(
        (total, length) => total + Math.min(length, candidate),
        0,
      );
      if (candidateTotal <= DELIVERY_DIAGNOSTIC_MAX_BYTES) low = candidate;
      else high = candidate - 1;
    }
    bounded = fields.map((value) => truncateUtf8(value, low));
    runnerTruncated = bounded.some((value, index) => value !== fields[index]);
  }
  const [stdout, stderr, error] = bounded;
  const outputTruncated = Boolean(result.outputTruncated);
  return Object.freeze({
    availability: stdout || stderr || error || outputTruncated ? 'available' : 'unavailable',
    stdout,
    stderr,
    error,
    outputTruncated,
    runnerTruncated,
  });
}

async function runDeliveryCommands(inspected, runCommand = runManagedCommand, repositoryRoot = REPOSITORY_ROOT) {
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
    const env = repositoryLocalGitEnvironment();
    delete env.SUPER_DOLPHIN_DESKTOP_SMOKE_SKIP_FRONTEND_BUILD;
    if (command.id === 'desktop-start-smoke') env.SUPER_DOLPHIN_DESKTOP_SMOKE_SKIP_FRONTEND_BUILD = '1';
    if (command.id === 'desktop-failure-smoke') {
      for (const key of DESKTOP_FAILURE_SMOKE_CONFLICT_ENV_KEYS) delete env[key];
    }
    const startedAtMs = Date.now();
    const result = await runCommand(program, args, {
      cwd,
      env,
      timeoutMs: DELIVERY_COMMAND_TIMEOUT_MS,
      killGraceMs: 20_000,
    });
    const finishedAtMs = Date.now();
    results.push(Object.freeze({
      id: command.id,
      argv: command.argv,
      cwd: command.cwd,
      exitCode: result.status ?? 1,
      signal: result.signal ?? null,
      startedAt: new Date(startedAtMs).toISOString(),
      finishedAt: new Date(finishedAtMs).toISOString(),
      durationMs: finishedAtMs - startedAtMs,
      status: !result.timedOut && !result.error && result.status === 0 ? 'PASS' : 'FAIL',
    }));
    if (result.timedOut || result.error || result.status !== 0) {
      const failure = {
        status: 'FAIL',
        reason: result.timedOut
          ? `${command.id} timed out after ${DELIVERY_COMMAND_TIMEOUT_MS}ms`
          : `${command.id} failed with exit ${result.status} signal ${result.signal || ''}`,
        executedCommands: results.length,
        commands: Object.freeze(results),
      };
      failure.diagnostics = boundedDiagnostics(result);
      return Object.freeze(failure);
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
      options.repositoryRoot = args[++index] || '';
    } else {
      throw new TypeError(`unsupported delivery smoke argument: ${arg}`);
    }
  }
  if (!options.mode) throw new TypeError('one of --inspect or --verify is required');
  options.repositoryRoot = canonicalRepositoryRoot(options.repositoryRoot);
  requireCleanDeliveryWorktree(options.repositoryRoot);
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
      ? await runDeliveryCommands(inspected, runManagedCommand, options.repositoryRoot)
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
  canonicalRepositoryRoot,
  inspectDeliveryCommands,
  parseArguments,
  runDeliveryCommands,
  validateDeliveryCaseResult,
};
