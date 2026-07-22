import { describe, expect, it } from 'vitest';

import { FRONTEND_HOOK_TEST_LANES, runFrontendHookTests } from './frontend-hook-test-runner.mjs';

const success = Object.freeze({
  status: 0,
  signal: null,
  stdout: '',
  stderr: '',
  timedOut: false,
  outputTruncated: false,
  error: undefined,
});

describe('frontend hook test runner', () => {
  it('runs the quick hook checks once across three non-overlapping lanes', async () => {
    const invocations = [];
    await runFrontendHookTests({
      runCommand: async (command, args) => {
        invocations.push([command, ...args]);
        return success;
      },
      terminate: () => expect.unreachable('successful lanes must not terminate peers'),
      cwd: '/repo/frontend-app',
      platform: 'linux',
      stdout: { write() {} },
      stderr: { write() {} },
    });

    expect(FRONTEND_HOOK_TEST_LANES).toEqual([
      { name: 'preflight', script: 'test:hook:preflight' },
      { name: 'core', script: 'test:hook:core' },
      { name: 'dependency-integrity', script: 'test:hook:dependency-integrity' },
    ]);
    expect(invocations).toEqual(FRONTEND_HOOK_TEST_LANES.map(({ script }) => ['npm', 'run', script]));
  });

  it('fails closed and terminates peer lanes after a command failure', async () => {
    const terminations = [];
    await expect(runFrontendHookTests({
      runCommand: async (_command, args) => args.at(-1) === 'test:hook:core'
        ? { ...success, status: 1, stderr: 'core failed' }
        : success,
      terminate: (signal) => terminations.push(signal),
      stdout: { write() {} },
      stderr: { write() {} },
    })).rejects.toThrow('core (status=1');
    expect(terminations).toEqual(['SIGTERM']);
  });
});
