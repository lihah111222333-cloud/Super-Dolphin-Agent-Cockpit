import { join } from 'node:path';
import { cwd, execPath } from 'node:process';
import { describe, expect, it } from 'vitest';
import { runManagedCommand } from './managed-command.mjs';
import {
  DELIVERY_CASE_IDS,
  DELIVERY_COMMANDS,
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
    expect(DELIVERY_RUNNER_CONTENT_PATHS).toEqual([
      'Makefile',
      'run-new-ui-desktop.sh',
      'scripts/frontend_embed_verify.sh',
      'frontend-app/package.json',
      'frontend-app/vite.config.js',
      'frontend-app/playwright.failure.config.js',
      'frontend-app/scripts/delivery-smoke-runner.mjs',
      'frontend-app/scripts/desktop-smoke.mjs',
      'frontend-app/scripts/desktop-failure-smoke.mjs',
      'frontend-app/scripts/evidence-provenance.mjs',
      'frontend-app/scripts/performance-budget-model.mjs',
      'frontend-app/scripts/sync-frontend-dist.mjs',
      'frontend-app/tests/e2e/desktop-failure.spec.js',
      'internal/ui/wails/testdata/failure_smoke_host/main.go',
    ]);
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

  it('stops before running commands when any required smoke is missing', () => {
    const inspected = inspectDeliveryCommands({
      scripts: { ...COMPLETE_SCRIPTS, 'smoke:desktop:failure': undefined },
    }, MAKEFILE);
    let calls = 0;
    const verdict = runDeliveryCommands(inspected, () => {
      calls += 1;
      return { status: 0, signal: null };
    });
    expect(verdict.status).toBe('NOT_VERIFIED');
    expect(verdict.executedCommands).toBe(0);
    expect(calls).toBe(0);
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
