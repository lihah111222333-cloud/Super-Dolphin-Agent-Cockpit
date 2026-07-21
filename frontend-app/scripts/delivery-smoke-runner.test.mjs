import { describe, expect, it } from 'vitest';
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

describe('delivery smoke runner', () => {
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

  it('runs every ready delivery command in order through the injected command port', async () => {
    const inspected = inspectDeliveryCommands({ scripts: COMPLETE_SCRIPTS }, MAKEFILE);
    const calls = [];
    const verdict = await runDeliveryCommands(inspected, async (program, args, options) => {
      calls.push({ program, args, options });
      return { status: 0, signal: null, timedOut: false };
    }, '/repo');

    expect(verdict).toEqual(expect.objectContaining({
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
    expect(calls).toEqual(DELIVERY_COMMANDS.map(({ argv, cwd: commandCwd }) => ({
      program: argv[0],
      args: argv.slice(1),
      options: {
        cwd: commandCwd === '.' ? '/repo' : `/repo/${commandCwd}`,
        timeoutMs: DELIVERY_COMMAND_TIMEOUT_MS,
        killGraceMs: 20_000,
      },
    })));
  });
});
