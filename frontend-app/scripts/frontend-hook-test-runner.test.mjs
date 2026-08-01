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
    const stdoutOutput = [];
    const stderrOutput = [];
    await expect(runFrontendHookTests({
      runCommand: async (_command, args) => args.at(-1) === 'test:hook:core'
        ? { ...success, status: 1, stdout: 'FAIL src/example.test.js > suite > case', stderr: 'core failed' }
        : success,
      terminate: (signal) => terminations.push(signal),
      stdout: { write: (value) => stdoutOutput.push(value) },
      stderr: { write: (value) => stderrOutput.push(value) },
    })).rejects.toThrow('core (status=1');
    expect(terminations).toEqual(['SIGTERM']);
    expect(stdoutOutput).not.toContain(expect.stringContaining('FAIL src/example.test.js'));
    expect(stderrOutput.at(-1)).toContain(
      '[frontend-hook:core:failure-summary]\nFAIL src/example.test.js > suite > case\ncore failed',
    );
  });
});
