import { execFileSync } from 'node:child_process';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { tmpdir } from 'node:os';
import { fileURLToPath } from 'node:url';

import { repositoryLocalGitEnvironment } from './runtime/git-environment.mjs';

const SCRIPT_PATH = fileURLToPath(import.meta.url);
const FRONTEND_ROOT = dirname(SCRIPT_PATH);
const REPOSITORY_ROOT = join(FRONTEND_ROOT, '..', '..');
const TEMPORARY_DIRECTORY_PREFIX = 'frontend-verification-isolation-';
export const FRONTEND_VERIFICATION_ISOLATION_MAX_BUFFER = 64 * 1024 * 1024;

export const FRONTEND_VERIFICATION_ISOLATION_MODES = Object.freeze([
  'delivery-test',
  'embed-verify',
]);

function defaultRunCommand(command, args, options) {
  return execFileSync(command, args, {
    ...options,
    encoding: 'utf8',
    maxBuffer: FRONTEND_VERIFICATION_ISOLATION_MAX_BUFFER,
    stdio: ['ignore', 'pipe', 'pipe'],
  });
}

function runGit(runCommand, args, repositoryRoot, environment) {
  return String(runCommand('git', args, {
    cwd: repositoryRoot,
    env: environment,
  })).trim();
}

export function parseFrontendVerificationIsolationMode(args) {
  if (!Array.isArray(args) || args.length !== 1 || !FRONTEND_VERIFICATION_ISOLATION_MODES.includes(args[0])) {
    throw new TypeError('Expected exactly one isolation mode: ' + FRONTEND_VERIFICATION_ISOLATION_MODES.join('|'));
  }
  return args[0];
}

export function commandForFrontendVerificationIsolation(mode, candidateRoot, platform = process.platform) {
  if (mode === 'delivery-test') {
    return {
      command: platform === 'win32' ? 'npm.cmd' : 'npm',
      args: ['exec', 'vitest', 'run', 'scripts/delivery-smoke-runner.test.mjs', '--no-file-parallelism', '--maxWorkers=1'],
      cwd: join(candidateRoot, 'frontend-app'),
    };
  }
  if (mode === 'embed-verify') {
    return {
      command: 'make',
      args: ['frontend-embed-verify'],
      cwd: candidateRoot,
    };
  }
  throw new TypeError('Unsupported frontend verification isolation mode: ' + mode);
}

export function runFrontendVerificationIsolation(mode, {
  repositoryRoot = REPOSITORY_ROOT,
  runCommand = defaultRunCommand,
  makeTemporaryDirectory = (prefix) => mkdtempSync(join(tmpdir(), prefix)),
  removeTemporaryDirectory = (directory) => rmSync(directory, { recursive: true, force: false, maxRetries: 3 }),
  writeOverlay = writeFileSync,
  environment = process.env,
  platform = process.platform,
} = {}) {
  const verifiedMode = parseFrontendVerificationIsolationMode([mode]);
  const isolatedEnvironment = repositoryLocalGitEnvironment(environment);
  const sourceRoot = runGit(runCommand, ['rev-parse', '--show-toplevel'], repositoryRoot, isolatedEnvironment);
  const revision = runGit(runCommand, ['rev-parse', 'HEAD'], sourceRoot, isolatedEnvironment);
  const overlay = runGit(runCommand, ['diff', '--binary', '--full-index', 'HEAD'], sourceRoot, isolatedEnvironment);
  const temporaryRoot = makeTemporaryDirectory(TEMPORARY_DIRECTORY_PREFIX);
  const candidateRoot = join(temporaryRoot, 'candidate');

  try {
    runCommand('git', ['clone', '--no-local', '--quiet', '--no-checkout', sourceRoot, candidateRoot], {
      cwd: temporaryRoot,
      env: isolatedEnvironment,
    });
    runCommand('git', ['-C', candidateRoot, 'checkout', '--detach', '--quiet', revision], {
      cwd: temporaryRoot,
      env: isolatedEnvironment,
    });
    if (overlay) {
      const overlayPath = join(temporaryRoot, 'tracked-overlay.patch');
      writeOverlay(overlayPath, overlay + '\n', 'utf8');
      runCommand('git', ['-C', candidateRoot, 'apply', '--index', '--whitespace=error-all', overlayPath], {
        cwd: temporaryRoot,
        env: isolatedEnvironment,
      });
    }

    const npmCommand = platform === 'win32' ? 'npm.cmd' : 'npm';
    runCommand(npmCommand, ['ci'], {
      cwd: join(candidateRoot, 'frontend-app'),
      env: isolatedEnvironment,
    });
    const command = commandForFrontendVerificationIsolation(verifiedMode, candidateRoot, platform);
    runCommand(command.command, command.args, {
      cwd: command.cwd,
      env: isolatedEnvironment,
    });
  } finally {
    removeTemporaryDirectory(temporaryRoot);
  }
}

if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) {
  try {
    runFrontendVerificationIsolation(parseFrontendVerificationIsolationMode(process.argv.slice(2)));
  } catch (error) {
    process.stderr.write((error instanceof Error ? error.stack : String(error)) + '\n');
    process.exitCode = 1;
  }
}
