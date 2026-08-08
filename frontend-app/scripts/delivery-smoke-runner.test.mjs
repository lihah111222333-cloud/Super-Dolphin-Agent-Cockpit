import { execFileSync } from 'node:child_process';
import {
  mkdtempSync,
  mkdirSync,
  readFileSync,
  realpathSync,
  rmSync,
  writeFileSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import process, { cwd, execPath } from 'node:process';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { runManagedCommand } from './managed-command.mjs';
import { FROZEN_T04_T05_EXECUTION_CLOSURE_PATHS } from './frontend-execution-closure.mjs';
import { REPOSITORY_LOCAL_GIT_ENV_KEYS } from './runtime/git-environment.mjs';
import {
  DELIVERY_CASE_IDS,
  DELIVERY_COMMANDS,
  DELIVERY_COMMAND_TIMEOUT_MS,
  DELIVERY_RUNNER_CONTENT_PATHS,
  canonicalRepositoryRoot,
  inspectDeliveryCommands,
  parseArguments,
  runDeliveryCommands,
  validateDeliveryCaseResult,
} from './delivery-smoke-runner.mjs';

const MAKEFILE = 'frontend-embed-verify-after-build:\n\t./scripts/frontend_embed_verify.sh\n';
const COMPLETE_SCRIPTS = {
  build: 'vite build --configLoader runner && node scripts/sync-frontend-dist.mjs',
  'smoke:desktop:rpc': 'node scripts/desktop-smoke.mjs',
  'smoke:desktop:failure': 'node scripts/desktop-failure-smoke.mjs',
};
const FAILURE_SMOKE_ENV_CONFLICTS = Object.freeze([
  'VITE_DEV_URL',
  'FRONTEND_DEVSERVER_URL',
]);
const temporaryDirectories = [];

function runGit(repositoryRoot, args) {
  return execFileSync('git', args, { cwd: repositoryRoot, encoding: 'utf8' });
}

function syncSubjectFrontendFixture(sourceFrontendRoot, subjectRepositoryRoot) {
  const fixturePaths = ['frontend-app/package.json', 'frontend-app/scripts/delivery-smoke-runner.mjs'];
  for (const relativePath of ['package.json', 'scripts/delivery-smoke-runner.mjs']) {
    writeFileSync(
      join(subjectRepositoryRoot, 'frontend-app', relativePath),
      readFileSync(join(sourceFrontendRoot, relativePath)),
    );
  }
  runGit(subjectRepositoryRoot, ['add', ...fixturePaths]);
  try {
    runGit(subjectRepositoryRoot, ['diff', '--cached', '--quiet', '--', ...fixturePaths]);
    return;
  } catch (error) {
    if (error?.status !== 1) throw error;
  }
  runGit(subjectRepositoryRoot, [
    '-c', 'user.name=Codex',
    '-c', 'user.email=codex@example.invalid',
    'commit', '--quiet', '-m', 'fixture frontend package',
  ]);
}

function createDeliveryRepositoryFixture() {
  const repositoryRoot = mkdtempSync(join(tmpdir(), 'delivery-smoke-runner-'));
  temporaryDirectories.push(repositoryRoot);
  mkdirSync(join(repositoryRoot, 'frontend-app'), { recursive: true });
  mkdirSync(join(repositoryRoot, 'nested', 'path'), { recursive: true });
  writeFileSync(join(repositoryRoot, 'tracked.txt'), 'base\n', 'utf8');
  runGit(repositoryRoot, ['init', '--quiet']);
  runGit(repositoryRoot, ['add', '.']);
  runGit(repositoryRoot, ['-c', 'user.name=Codex', '-c', 'user.email=codex@example.invalid', 'commit', '--quiet', '-m', 'fixture']);
  return repositoryRoot;
}

describe('delivery smoke runner', () => {
  afterEach(() => {
    vi.unstubAllEnvs();
    vi.useRealTimers();
    for (const directory of temporaryDirectories.splice(0)) {
      rmSync(directory, { recursive: true, force: true });
    }
  });

  it('locks build, embed, start and failure smoke commands exactly', () => {
    expect(DELIVERY_COMMANDS.map(({ id, cwd: commandCwd, argv }) => ({ id, cwd: commandCwd, argv }))).toEqual([
      { id: 'frontend-build', cwd: 'frontend-app', argv: ['npm', 'run', 'build'] },
      { id: 'frontend-embed-verify', cwd: '.', argv: ['make', 'frontend-embed-verify-after-build'] },
      { id: 'desktop-start-smoke', cwd: 'frontend-app', argv: ['npm', 'run', 'smoke:desktop:rpc'] },
      { id: 'desktop-failure-smoke', cwd: 'frontend-app', argv: ['npm', 'run', 'smoke:desktop:failure'] },
    ]);
    expect(DELIVERY_CASE_IDS).toEqual([
      'frontend-build',
      'frontend-embed-verify',
      'desktop-start-smoke',
      'desktop-failure-smoke',
    ]);
    expect(DELIVERY_COMMAND_TIMEOUT_MS).toBe(900_000);
    expect(DELIVERY_RUNNER_CONTENT_PATHS).toBe(FROZEN_T04_T05_EXECUTION_CLOSURE_PATHS);
    expect(DELIVERY_RUNNER_CONTENT_PATHS).toContain('frontend-app/config/vitest-suite-policy.json');
  });

  it.each([
    ['missing caseIds', undefined, 4],
    ['wrong case order', [...DELIVERY_CASE_IDS].reverse(), 4],
    ['zero test count', [], 0],
  ])('rejects invalid structured evidence registry: %s', (_name, caseIds, testCount) => {
    expect(() => validateDeliveryCaseResult(caseIds, testCount)).toThrow(/delivery/);
  });

  it('canonicalizes the default runner root and a nested --repo path to their Git top-levels', () => {
    const repositoryRoot = createDeliveryRepositoryFixture();
    const nestedRepositoryPath = join(repositoryRoot, 'nested', 'path');

    expect(canonicalRepositoryRoot(join(cwd(), 'scripts'))).toBe(resolve(cwd(), '..'));
    expect(parseArguments(['--inspect', '--repo', nestedRepositoryPath])).toEqual({
      mode: 'inspect',
      repositoryRoot: canonicalRepositoryRoot(repositoryRoot),
      subjectSha: runGit(repositoryRoot, ['rev-parse', 'HEAD']).trim(),
    });
  });

  it.each([
    ['tracked', (repositoryRoot) => writeFileSync(join(repositoryRoot, 'tracked.txt'), 'changed\n', 'utf8')],
    ['staged', (repositoryRoot) => {
      writeFileSync(join(repositoryRoot, 'tracked.txt'), 'staged\n', 'utf8');
      runGit(repositoryRoot, ['add', 'tracked.txt']);
    }],
    ['untracked', (repositoryRoot) => writeFileSync(join(repositoryRoot, 'untracked.txt'), 'new\n', 'utf8')],
  ])('fails fast before delivery work for a %s worktree change', (_name, makeDirty) => {
    const repositoryRoot = createDeliveryRepositoryFixture();
    makeDirty(repositoryRoot);

    expect(() => parseArguments(['--verify', '--repo', join(repositoryRoot, 'nested', 'path')]))
      .toThrow('delivery smoke requires a clean worktree');
  });

  it('preflights worktree cleanliness before validating a detached subject', () => {
    const repositoryRoot = createDeliveryRepositoryFixture();
    writeFileSync(join(repositoryRoot, 'untracked.txt'), 'new\n', 'utf8');

    expect(() => parseArguments([
      '--inspect', '--repo', join(repositoryRoot, 'nested', 'path'), '--subject', 'a'.repeat(40),
    ])).toThrow('delivery smoke requires a clean worktree');
  });

  it.each([
    ['missing failure smoke', { ...COMPLETE_SCRIPTS, 'smoke:desktop:failure': undefined }, MAKEFILE],
    ['stale embed target', COMPLETE_SCRIPTS, 'frontend-embed-verify-after-build:\n\t@true\n'],
    ['weak build script', { ...COMPLETE_SCRIPTS, build: 'echo PASS' }, MAKEFILE],
  ])('keeps T05 NOT_VERIFIED for %s', (_name, scripts, makefile) => {
    const inspected = inspectDeliveryCommands({ scripts }, makefile);
    expect(inspected.status).toBe('NOT_VERIFIED');
  });

  it('stops before running commands when any required smoke is missing', async () => {
    const inspected = inspectDeliveryCommands({
      scripts: { ...COMPLETE_SCRIPTS, 'smoke:desktop:failure': undefined },
    }, MAKEFILE);
    let calls = 0;
    const verdict = await runDeliveryCommands(inspected, async () => {
      calls += 1;
      return { status: 0, signal: null };
    });
    expect(verdict.status).toBe('NOT_VERIFIED');
    expect(verdict.executedCommands).toBe(0);
    expect(calls).toBe(0);
  });

  it.each(FAILURE_SMOKE_ENV_CONFLICTS)(
    'isolates desktop failure smoke from inherited %s',
    async (conflictingVariable) => {
      vi.stubEnv(conflictingVariable, 'http://127.0.0.1:5999');
      vi.stubEnv('GIT_DIR', '/shared/repository/.git');
      vi.stubEnv('GIT_WORK_TREE', '/shared/repository');
      for (const key of REPOSITORY_LOCAL_GIT_ENV_KEYS) vi.stubEnv(key, `/hostile/${key}`);
      vi.stubEnv('SUPER_DOLPHIN_FAILURE_SMOKE_VITE_URL', 'http://127.0.0.1:5178');
      vi.stubEnv('SUPER_DOLPHIN_DESKTOP_SMOKE_SKIP_FRONTEND_BUILD', 'inherited');
      vi.stubEnv('DELIVERY_SMOKE_COMMON_SENTINEL', 'preserved');
      const inheritedPath = process.env.PATH;
      const inspected = inspectDeliveryCommands({ scripts: COMPLETE_SCRIPTS }, MAKEFILE);
      const calls = [];
      const verdict = await runDeliveryCommands(inspected, async (program, args, options) => {
        calls.push({ program, args, options });
        return { status: 0, signal: null };
      });

      expect(verdict.status).toBe('PASS');
      expect(calls).toHaveLength(DELIVERY_COMMANDS.length);
      expect(new Set(calls.map(({ options }) => options.env))).toHaveProperty('size', DELIVERY_COMMANDS.length);
      for (const { options } of calls) {
        expect(options.env).not.toBe(process.env);
        expect(options.env.PATH).toBe(inheritedPath);
        expect(options.env.SUPER_DOLPHIN_FAILURE_SMOKE_VITE_URL).toBe('http://127.0.0.1:5178');
        expect(options.env.DELIVERY_SMOKE_COMMON_SENTINEL).toBe('preserved');
        for (const key of REPOSITORY_LOCAL_GIT_ENV_KEYS) expect(options.env).not.toHaveProperty(key);
      }
      const failureCall = calls[DELIVERY_COMMANDS.findIndex(({ id }) => id === 'desktop-failure-smoke')];
      const startCall = calls[DELIVERY_COMMANDS.findIndex(({ id }) => id === 'desktop-start-smoke')];
      expect(startCall.options.env.SUPER_DOLPHIN_DESKTOP_SMOKE_SKIP_FRONTEND_BUILD).toBe('1');
      expect(calls.filter(({ options }) => options.env.SUPER_DOLPHIN_DESKTOP_SMOKE_SKIP_FRONTEND_BUILD !== undefined))
        .toEqual([startCall]);
      expect(process.env.SUPER_DOLPHIN_DESKTOP_SMOKE_SKIP_FRONTEND_BUILD).toBe('inherited');
      expect(failureCall.options.env).not.toHaveProperty(conflictingVariable);
      expect(process.env[conflictingVariable]).toBe('http://127.0.0.1:5999');
      for (const key of REPOSITORY_LOCAL_GIT_ENV_KEYS) expect(process.env[key]).toBe(`/hostile/${key}`);
      for (const call of calls.slice(0, -1)) {
        expect(call.options.env[conflictingVariable]).toBe('http://127.0.0.1:5999');
      }
    },
  );

  it('returns exit-2 semantics after the third command fails and never starts the fourth', async () => {
    const inspected = inspectDeliveryCommands({ scripts: COMPLETE_SCRIPTS }, MAKEFILE);
    const calls = [];
    const verdict = await runDeliveryCommands(inspected, async (_program, _args, options) => {
      calls.push(options.cwd);
      return { status: calls.length === 3 ? 2 : 0, signal: null, timedOut: false };
    });

    expect(verdict).toEqual(expect.objectContaining({
      status: 'FAIL',
      executedCommands: 3,
      reason: expect.stringContaining('desktop-start-smoke failed with exit 2'),
    }));
    expect(calls).toHaveLength(3);
    expect(verdict.commands.map(({ id }) => id)).toEqual(DELIVERY_CASE_IDS.slice(0, 3));
  });

  it('preserves redacted failure-only diagnostics for exit 2 with an exact schema', async () => {
    const inspected = inspectDeliveryCommands({ scripts: COMPLETE_SCRIPTS }, MAKEFILE);
    const secret = 't03-raw-provider-secret-do-not-persist';
    const failure = new Error(`safe error Authorization: Bearer ${secret}`);
    failure.stack = `leaked-stack ${secret}`;
    const verdict = await runDeliveryCommands(inspected, async () => ({
      status: 2,
      signal: null,
      timedOut: false,
      stdout: `stdout Authorization: Bearer ${secret}`,
      stderr: `stderr token=${secret}`,
      error: failure,
      outputTruncated: false,
    }));

    expect(verdict.diagnostics).toEqual({
      availability: 'available',
      stdout: 'stdout Authorization: Bearer [redacted]',
      stderr: 'stderr token=[redacted]',
      error: 'safe error Authorization: Bearer [redacted]',
      outputTruncated: false,
      runnerTruncated: false,
    });
    expect(JSON.stringify(verdict.diagnostics)).not.toContain(secret);
    expect(JSON.stringify(verdict.diagnostics)).not.toContain('leaked-stack');
  });

  it('preserves redacted failure-only diagnostics for exit 1 with an exact schema', async () => {
    const inspected = inspectDeliveryCommands({ scripts: COMPLETE_SCRIPTS }, MAKEFILE);
    const secret = 't03-exit-one-provider-secret-do-not-persist';
    const failure = new Error(`safe error Authorization: Bearer ${secret}`);
    failure.stack = `leaked-stack ${secret}`;
    const verdict = await runDeliveryCommands(inspected, async () => ({
      status: 1,
      signal: null,
      timedOut: false,
      stdout: `stdout Authorization: Bearer ${secret}`,
      stderr: `stderr token=${secret}`,
      error: failure,
      outputTruncated: false,
    }));

    expect(verdict.diagnostics).toEqual({
      availability: 'available',
      stdout: 'stdout Authorization: Bearer [redacted]',
      stderr: 'stderr token=[redacted]',
      error: 'safe error Authorization: Bearer [redacted]',
      outputTruncated: false,
      runnerTruncated: false,
    });
    expect(JSON.stringify(verdict.diagnostics)).not.toContain(secret);
    expect(JSON.stringify(verdict.diagnostics)).not.toContain('leaked-stack');
  });

  it('redacts GitHub raw tokens from exit-2 stdout diagnostics', async () => {
    const inspected = inspectDeliveryCommands({ scripts: COMPLETE_SCRIPTS }, MAKEFILE);
    const classicToken = 'ghp_syntheticClassicToken123456789';
    const fineGrainedToken = 'github_pat_syntheticFineGrainedToken123456789';
    const verdict = await runDeliveryCommands(inspected, async () => ({
      status: 2,
      signal: null,
      timedOut: false,
      stdout: `classic=${classicToken} fine-grained=${fineGrainedToken}`,
      stderr: '',
      error: null,
      outputTruncated: false,
    }));

    expect(verdict.diagnostics).toEqual({
      availability: 'available',
      stdout: 'classic=[redacted] fine-grained=[redacted]',
      stderr: '',
      error: '',
      outputTruncated: false,
      runnerTruncated: false,
    });
    expect(JSON.stringify(verdict.diagnostics)).not.toContain(classicToken);
    expect(JSON.stringify(verdict.diagnostics)).not.toContain(fineGrainedToken);
  });

  it('redacts before applying the aggregate UTF-8-safe diagnostics budget', async () => {
    const inspected = inspectDeliveryCommands({ scripts: COMPLETE_SCRIPTS }, MAKEFILE);
    const secret = 't03-raw-provider-secret-do-not-persist';
    const verdict = await runDeliveryCommands(inspected, async () => ({
      status: 2,
      signal: null,
      timedOut: false,
      stdout: `${'界'.repeat(1500)} Authorization: Bearer ${secret}`,
      stderr: `${'错'.repeat(1500)} token=${secret}`,
      error: new Error(`失${'败'.repeat(1500)} password=${secret}`),
      outputTruncated: false,
    }));

    expect(verdict.diagnostics.availability).toBe('available');
    expect(verdict.diagnostics.outputTruncated).toBe(false);
    expect(verdict.diagnostics.runnerTruncated).toBe(true);
    expect(Buffer.byteLength(
      verdict.diagnostics.stdout + verdict.diagnostics.stderr + verdict.diagnostics.error,
      'utf8',
    )).toBeLessThanOrEqual(4096);
    expect(JSON.stringify(verdict.diagnostics)).not.toContain(secret);
    expect(JSON.stringify(verdict.diagnostics)).not.toContain('\uFFFD');
  });

  it('keeps upstream output truncation distinct from runner truncation', async () => {
    const inspected = inspectDeliveryCommands({ scripts: COMPLETE_SCRIPTS }, MAKEFILE);
    const verdict = await runDeliveryCommands(inspected, async () => ({
      status: 2,
      signal: null,
      timedOut: false,
      stdout: 'partial upstream output',
      stderr: '',
      error: null,
      outputTruncated: true,
    }));

    expect(verdict.diagnostics).toEqual({
      availability: 'available',
      stdout: 'partial upstream output',
      stderr: '',
      error: '',
      outputTruncated: true,
      runnerTruncated: false,
    });
  });

  it('marks an exit-2 diagnostic package explicitly unavailable when no detail exists', async () => {
    const inspected = inspectDeliveryCommands({ scripts: COMPLETE_SCRIPTS }, MAKEFILE);
    const verdict = await runDeliveryCommands(inspected, async () => ({
      status: 2,
      signal: null,
      timedOut: false,
    }));

    expect(verdict.diagnostics).toEqual({
      availability: 'unavailable',
      stdout: '',
      stderr: '',
      error: '',
      outputTruncated: false,
      runnerTruncated: false,
    });
  });

  it('keeps the legacy PASS verdict schema deep-equal when every command exits zero', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-07-29T00:00:00.000Z'));
    const inspected = inspectDeliveryCommands({ scripts: COMPLETE_SCRIPTS }, MAKEFILE);
    const verdict = await runDeliveryCommands(inspected, async () => ({
      status: 0,
      signal: null,
      timedOut: false,
      stdout: 'must not be retained',
      stderr: 'must not be retained',
      outputTruncated: false,
    }));

    expect(verdict).toEqual({
      status: 'PASS',
      reason: '',
      executedCommands: DELIVERY_COMMANDS.length,
      commands: DELIVERY_COMMANDS.map(({ id, argv, cwd: commandCwd }) => ({
        id,
        argv,
        cwd: commandCwd,
        exitCode: 0,
        signal: null,
        startedAt: '2026-07-29T00:00:00.000Z',
        finishedAt: '2026-07-29T00:00:00.000Z',
        durationMs: 0,
        status: 'PASS',
      })),
    });
  });

  it('projects diagnostics for exit 1 while preserving upstream truncation', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-07-29T00:00:00.000Z'));
    const inspected = inspectDeliveryCommands({ scripts: COMPLETE_SCRIPTS }, MAKEFILE);
    const verdict = await runDeliveryCommands(inspected, async () => ({
      status: 1,
      signal: null,
      timedOut: false,
      stdout: 'must not be retained',
      stderr: 'must not be retained',
      error: null,
      outputTruncated: true,
    }));

    expect(verdict).toEqual({
      status: 'FAIL',
      reason: 'frontend-build failed with exit 1 signal ',
      executedCommands: 1,
      commands: [{
        id: 'frontend-build',
        argv: DELIVERY_COMMANDS[0].argv,
        cwd: 'frontend-app',
        exitCode: 1,
        signal: null,
        startedAt: '2026-07-29T00:00:00.000Z',
        finishedAt: '2026-07-29T00:00:00.000Z',
        durationMs: 0,
        status: 'FAIL',
      }],
      diagnostics: {
        availability: 'available',
        stdout: 'must not be retained',
        stderr: 'must not be retained',
        error: '',
        outputTruncated: true,
        runnerTruncated: false,
      },
    });
  });

  // This test deliberately clones the real subject tree and commits a fixture; cold ECI Git I/O can exceed Vitest's 5s default.
  it('only commits the subject fixture when staged content changes', () => {
    const repositoryRoot = mkdtempSync(join(tmpdir(), 'delivery-smoke-runner-subject-'));
    temporaryDirectories.push(repositoryRoot);
    runGit(resolve(cwd(), '..'), ['clone', '--no-local', '--quiet', resolve(cwd(), '..'), repositoryRoot]);
    const originalHead = runGit(repositoryRoot, ['rev-parse', 'HEAD']).trim();

    syncSubjectFrontendFixture(join(repositoryRoot, 'frontend-app'), repositoryRoot);
    expect(runGit(repositoryRoot, ['rev-parse', 'HEAD']).trim()).toBe(originalHead);

    const sourceFrontendRoot = mkdtempSync(join(tmpdir(), 'delivery-smoke-runner-source-'));
    temporaryDirectories.push(sourceFrontendRoot);
    mkdirSync(join(sourceFrontendRoot, 'scripts'), { recursive: true });
    for (const relativePath of ['package.json', 'scripts/delivery-smoke-runner.mjs']) {
      writeFileSync(
        join(sourceFrontendRoot, relativePath),
        readFileSync(join(repositoryRoot, 'frontend-app', relativePath)),
        'utf8',
      );
    }
    const packagePath = join(sourceFrontendRoot, 'package.json');
    writeFileSync(packagePath, `${readFileSync(packagePath, 'utf8')}\n`, 'utf8');

    syncSubjectFrontendFixture(sourceFrontendRoot, repositoryRoot);
    expect(runGit(repositoryRoot, ['rev-parse', 'HEAD']).trim()).not.toBe(originalHead);
    expect(runGit(repositoryRoot, ['show', '-s', '--format=%s', 'HEAD']).trim())
      .toBe('fixture frontend package');
    expect(runGit(repositoryRoot, ['status', '--porcelain'])).toBe('');
  }, 30_000);

  it('keeps T05 verification independently invocable from a clean subject clone while checking its hashed runner contract', async () => {
    const repositoryRoot = mkdtempSync(join(tmpdir(), 'delivery-smoke-runner-subject-'));
    temporaryDirectories.push(repositoryRoot);
    runGit(resolve(cwd(), '..'), ['clone', '--no-local', '--quiet', resolve(cwd(), '..'), repositoryRoot]);
    syncSubjectFrontendFixture(cwd(), repositoryRoot);
    const result = await runManagedCommand(execPath, [
      realpathSync(join(repositoryRoot, 'frontend-app/scripts/delivery-smoke-runner.mjs')),
      '--inspect', '--repo', join(repositoryRoot, 'frontend-app'),
    ], {
      cwd: join(repositoryRoot, 'frontend-app'),
      timeoutMs: 30_000,
      killGraceMs: 1_000,
    });
    expect(result.timedOut).toBe(false);
    expect(
      result.status,
      [result.stdout, result.stderr].filter(Boolean).join('\n'),
    ).toBe(0);
    const report = JSON.parse(result.stdout);
    expect(report.caseIds).toEqual(DELIVERY_CASE_IDS);
    expect(report.testCount).toBe(DELIVERY_CASE_IDS.length);
    expect(report.verdict).toEqual(expect.objectContaining({
      status: 'READY',
      commands: DELIVERY_COMMANDS.map(({ id, argv, cwd: commandCwd }) => expect.objectContaining({
        id,
        argv,
        cwd: commandCwd,
        status: 'AVAILABLE',
      })),
    }));
    expect(report.provenance).toEqual(expect.objectContaining({
      runnerId: 'frontend-delivery-smoke',
      runnerContentHash: expect.stringMatching(/^[0-9a-f]{64}$/),
      runnerFiles: DELIVERY_RUNNER_CONTENT_PATHS.map((path) => ({
        path,
        sha256: expect.stringMatching(/^[0-9a-f]{64}$/),
      })),
    }));
  }, 325_000);
});
