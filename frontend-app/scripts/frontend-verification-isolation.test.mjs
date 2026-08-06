import { execFileSync } from 'node:child_process';
import { existsSync, mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { tmpdir } from 'node:os';

import { afterEach, describe, expect, it, vi } from 'vitest';

import {
  FRONTEND_VERIFICATION_ISOLATION_MODES,
  commandForFrontendVerificationIsolation,
  parseFrontendVerificationIsolationMode,
  runFrontendVerificationIsolation,
} from './frontend-verification-isolation.mjs';

const temporaryDirectories = [];

function makeTemporaryDirectory(prefix) {
  const directory = mkdtempSync(join(tmpdir(), prefix));
  temporaryDirectories.push(directory);
  return directory;
}

function run(command, args, options) {
  return execFileSync(command, args, {
    ...options,
    encoding: 'utf8',
    stdio: ['ignore', 'pipe', 'pipe'],
  });
}

function initializeRepository() {
  const repositoryRoot = makeTemporaryDirectory('frontend-verification-source-');
  mkdirSync(join(repositoryRoot, 'frontend-app', 'scripts'), { recursive: true });
  writeFileSync(join(repositoryRoot, '.gitignore'), 'dist/\n', 'utf8');
  writeFileSync(join(repositoryRoot, 'tracked.txt'), 'base\n', 'utf8');
  writeFileSync(join(repositoryRoot, 'frontend-app', 'package.json'), '{"name":"fixture"}\n', 'utf8');
  writeFileSync(join(repositoryRoot, 'frontend-app', 'scripts', 'delivery-smoke-runner.test.mjs'), 'export {};\n', 'utf8');
  run('git', ['init', '--quiet'], { cwd: repositoryRoot });
  run('git', ['add', '.'], { cwd: repositoryRoot });
  run('git', ['-c', 'user.name=Codex', '-c', 'user.email=codex@example.invalid', 'commit', '--quiet', '-m', 'fixture'], { cwd: repositoryRoot });
  return repositoryRoot;
}

afterEach(() => {
  for (const directory of temporaryDirectories.splice(0)) {
    rmSync(directory, { recursive: true, force: true });
  }
});

describe('frontend verification isolation', () => {
  it('accepts only the fixed isolation modes', () => {
    expect(FRONTEND_VERIFICATION_ISOLATION_MODES).toEqual(['delivery-test', 'embed-verify']);
    expect(parseFrontendVerificationIsolationMode(['delivery-test'])).toBe('delivery-test');
    expect(() => parseFrontendVerificationIsolationMode(['delivery-test', 'extra'])).toThrow('Expected exactly one isolation mode');
    expect(() => parseFrontendVerificationIsolationMode(['anything-else'])).toThrow('Expected exactly one isolation mode');
  });

  it('locks each mode to its fixed command', () => {
    expect(commandForFrontendVerificationIsolation('delivery-test', '/candidate', 'linux')).toEqual({
      command: 'npm',
      args: ['exec', 'vitest', 'run', 'scripts/delivery-smoke-runner.test.mjs', '--no-file-parallelism', '--maxWorkers=1'],
      cwd: '/candidate/frontend-app',
    });
    expect(commandForFrontendVerificationIsolation('embed-verify', '/candidate', 'linux')).toEqual({
      command: 'make',
      args: ['frontend-embed-verify'],
      cwd: '/candidate',
    });
  });

  it('clones HEAD, overlays only tracked changes, removes the candidate, and clears Git sentinels', () => {
    const repositoryRoot = initializeRepository();
    writeFileSync(join(repositoryRoot, 'tracked.txt'), 'overlay\n', 'utf8');
    mkdirSync(join(repositoryRoot, 'dist'), { recursive: true });
    writeFileSync(join(repositoryRoot, 'dist', 'ignored-sentinel.txt'), 'must-not-copy\n', 'utf8');
    const invocation = vi.fn((command, args, options) => {
      expect(options.env.GIT_DIR).toBeUndefined();
      if (command === 'npm') {
        if (args[0] === 'exec') {
          const candidateRoot = dirname(options.cwd);
          expect(readFileSync(join(candidateRoot, 'tracked.txt'), 'utf8')).toBe('overlay\n');
          expect(existsSync(join(candidateRoot, 'dist', 'ignored-sentinel.txt'))).toBe(false);
        }
        return '';
      }
      return run(command, args, options);
    });
    let temporaryRoot = '';

    runFrontendVerificationIsolation('delivery-test', {
      repositoryRoot,
      runCommand: invocation,
      makeTemporaryDirectory: (prefix) => {
        temporaryRoot = makeTemporaryDirectory(prefix);
        return temporaryRoot;
      },
      environment: { ...process.env, GIT_DIR: '/must-not-leak' },
      platform: 'linux',
    });

    expect(invocation).toHaveBeenCalledWith('git', expect.arrayContaining(['clone', '--no-local', '--quiet', '--no-checkout']), expect.any(Object));
    expect(invocation).toHaveBeenCalledWith('git', expect.arrayContaining(['apply', '--index', '--whitespace=error-all']), expect.any(Object));
    expect(existsSync(temporaryRoot)).toBe(false);
  });

  it('removes its own temporary directory when a fixed command fails', () => {
    const repositoryRoot = initializeRepository();
    const temporaryRoot = makeTemporaryDirectory('frontend-verification-cleanup-');
    const removeTemporaryDirectory = vi.fn((directory) => rmSync(directory, { recursive: true, force: false }));
    const runCommand = vi.fn((command, args, options) => {
      if (command === 'npm' && args[0] === 'ci') {
        throw new Error('npm ci failed');
      }
      return run(command, args, options);
    });

    expect(() => runFrontendVerificationIsolation('delivery-test', {
      repositoryRoot,
      runCommand,
      makeTemporaryDirectory: () => temporaryRoot,
      removeTemporaryDirectory,
      platform: 'linux',
    })).toThrow('npm ci failed');
    expect(removeTemporaryDirectory).toHaveBeenCalledWith(temporaryRoot);
    expect(existsSync(temporaryRoot)).toBe(false);
  });
});
