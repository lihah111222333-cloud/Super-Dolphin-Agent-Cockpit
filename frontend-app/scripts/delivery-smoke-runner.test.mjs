import { join } from 'node:path';
import process, { cwd, execPath } from 'node:process';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { runManagedCommand } from './managed-command.mjs';
import { FROZEN_T04_T05_EXECUTION_CLOSURE_PATHS } from './frontend-execution-closure.mjs';
import {
  DELIVERY_CASE_IDS,
  DELIVERY_COMMANDS,
  DELIVERY_COMMAND_TIMEOUT_MS,
  DELIVERY_RUNNER_CONTENT_PATHS,
  inspectDeliveryCommands,
  runDeliveryCommands,
  validateDeliveryCaseResult,
} from './delivery-smoke-runner.mjs';

const MAKEFILE = 'frontend-embed-verify: frontend-app-build\n\t./scripts/frontend_embed_verify.sh\n';
const COMPLETE_SCRIPTS = {
  build: 'vite build && node scripts/sync-frontend-dist.mjs',
  'smoke:desktop:rpc': 'node scripts/desktop-smoke.mjs',
  'smoke:desktop:failure': 'node scripts/desktop-failure-smoke.mjs',
};
const FAILURE_SMOKE_ENV_CONFLICTS = Object.freeze([
  'VITE_DEV_URL',
  'FRONTEND_DEVSERVER_URL',
]);

describe('delivery smoke runner', () => {
  afterEach(() => {
    vi.unstubAllEnvs();
    vi.useRealTimers();
  });

  it('locks build, embed, start and failure smoke commands exactly', () => {
    expect(DELIVERY_COMMANDS.map(({ id, cwd: commandCwd, argv }) => ({ id, cwd: commandCwd, argv }))).toEqual([
      { id: 'frontend-build', cwd: 'frontend-app', argv: ['npm', 'run', 'build'] },
      { id: 'frontend-embed-verify', cwd: '.', argv: ['make', 'frontend-embed-verify'] },
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
  });

  it.each([
    ['missing caseIds', undefined, 4],
    ['wrong case order', [...DELIVERY_CASE_IDS].reverse(), 4],
    ['zero test count', [], 0],
  ])('rejects invalid structured evidence registry: %s', (_name, caseIds, testCount) => {
    expect(() => validateDeliveryCaseResult(caseIds, testCount)).toThrow(/delivery/);
  });

  it.each([
    ['missing failure smoke', { ...COMPLETE_SCRIPTS, 'smoke:desktop:failure': undefined }, MAKEFILE],
    ['stale embed target', COMPLETE_SCRIPTS, 'frontend-embed-verify:\n\t@true\n'],
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
      vi.stubEnv('SUPER_DOLPHIN_FAILURE_SMOKE_VITE_URL', 'http://127.0.0.1:5178');
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
      }
      const failureCall = calls[DELIVERY_COMMANDS.findIndex(({ id }) => id === 'desktop-failure-smoke')];
      expect(failureCall.options.env).not.toHaveProperty(conflictingVariable);
      expect(process.env[conflictingVariable]).toBe('http://127.0.0.1:5999');
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

  it('keeps the legacy failure verdict schema deep-equal for non-exit-2 failures', async () => {
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
    });
  });

  it('runs the complete integration delivery surface in final verify mode', async () => {
    const result = await runManagedCommand(execPath, [join(cwd(), 'scripts/delivery-smoke-runner.mjs'), '--verify'], {
      cwd: cwd(),
      timeoutMs: 300_000,
      killGraceMs: 20_000,
    });
    expect(result.timedOut).toBe(false);
    expect(result.status, result.stderr).toBe(0);
    const report = JSON.parse(result.stdout);
    expect(report.caseIds).toEqual(DELIVERY_CASE_IDS);
    expect(report.testCount).toBe(DELIVERY_CASE_IDS.length);
    expect(report.verdict).toEqual(expect.objectContaining({
      status: 'PASS',
      executedCommands: DELIVERY_COMMANDS.length,
      commands: DELIVERY_COMMANDS.map(({ id, argv, cwd: commandCwd }) => expect.objectContaining({
        id,
        argv,
        cwd: commandCwd,
        exitCode: 0,
        signal: null,
        startedAt: expect.any(String),
        finishedAt: expect.any(String),
        durationMs: expect.any(Number),
        status: 'PASS',
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
