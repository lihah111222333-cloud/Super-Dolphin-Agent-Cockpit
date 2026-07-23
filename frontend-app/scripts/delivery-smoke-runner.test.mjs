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
