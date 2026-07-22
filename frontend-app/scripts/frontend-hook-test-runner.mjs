import path from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';

import { runManagedCommand, terminateManagedCommands } from './managed-command.mjs';

const SCRIPT_PATH = fileURLToPath(import.meta.url);
const LANE_TIMEOUT_MS = 9 * 60_000;
const LANE_MAX_BUFFER = 32 * 1024 * 1024;

export const FRONTEND_HOOK_TEST_LANES = Object.freeze([
  Object.freeze({ name: 'preflight', script: 'test:hook:preflight' }),
  Object.freeze({ name: 'core', script: 'test:hook:core' }),
  Object.freeze({ name: 'dependency-integrity', script: 'test:hook:dependency-integrity' }),
]);

function laneFailure(lane, result) {
  if (result.status === 0 && !result.error && !result.timedOut && !result.outputTruncated) return '';
  const details = [
    `status=${result.status ?? 'none'}`,
    `signal=${result.signal ?? 'none'}`,
    `timed_out=${Boolean(result.timedOut)}`,
    `output_truncated=${Boolean(result.outputTruncated)}`,
  ];
  if (result.error?.message) details.push(`error=${result.error.message}`);
  return `${lane.name} (${details.join(', ')})`;
}

function writeLaneOutput(lane, result, stdout, stderr) {
  if (result.stdout) stdout.write(`\n[frontend-hook:${lane.name}:stdout]\n${result.stdout}`);
  if (result.stderr) stderr.write(`\n[frontend-hook:${lane.name}:stderr]\n${result.stderr}`);
}

export async function runFrontendHookTests({
  runCommand = runManagedCommand,
  terminate = terminateManagedCommands,
  cwd = process.cwd(),
  platform = process.platform,
  stdout = process.stdout,
  stderr = process.stderr,
} = {}) {
  const npmCommand = platform === 'win32' ? 'npm.cmd' : 'npm';
  let terminating = false;
  const failures = await Promise.all(FRONTEND_HOOK_TEST_LANES.map(async (lane) => {
    const result = await runCommand(npmCommand, ['run', lane.script], {
      cwd,
      timeoutMs: LANE_TIMEOUT_MS,
      maxBuffer: LANE_MAX_BUFFER,
    });
    writeLaneOutput(lane, result, stdout, stderr);
    const failure = laneFailure(lane, result);
    if (failure && !terminating) {
      terminating = true;
      terminate('SIGTERM');
    }
    return failure;
  }));
  const failed = failures.filter(Boolean);
  if (failed.length > 0) throw new Error(`frontend hook lanes failed: ${failed.join('; ')}`);
}

if (process.argv[1] && path.resolve(process.argv[1]) === SCRIPT_PATH) {
  runFrontendHookTests().catch((error) => {
    console.error(error instanceof Error ? error.message : String(error));
    process.exitCode = 1;
  });
}
