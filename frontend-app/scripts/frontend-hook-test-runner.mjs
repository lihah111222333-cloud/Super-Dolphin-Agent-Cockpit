import path from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';

import { runManagedCommand, terminateManagedCommands } from './managed-command.mjs';

const SCRIPT_PATH = fileURLToPath(import.meta.url);
const LANE_TIMEOUT_MS = 9 * 60_000;
const LANE_MAX_BUFFER = 32 * 1024 * 1024;
const FAILURE_SUMMARY_MAX_LINES = 40;
const ANSI_ESCAPE_PATTERN = /\u001b\[[0-?]*[ -/]*[@-~]/g;
const FAILURE_SIGNAL_PATTERN = /\b(?:FAIL|failed|AssertionError|Error:|Expected|Received)\b/i;

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

function writeLaneFailureSummary(lane, result, stderr) {
  const output = `${result.stdout ?? ''}\n${result.stderr ?? ''}`.replace(ANSI_ESCAPE_PATTERN, '');
  const lines = output.split(/\r?\n/).map((line) => line.trimEnd()).filter(Boolean);
  const failureLines = lines.filter((line) => FAILURE_SIGNAL_PATTERN.test(line));
  const summary = (failureLines.length > 0 ? failureLines : lines)
    .slice(-FAILURE_SUMMARY_MAX_LINES)
    .join('\n');
  if (summary) stderr.write(`\n[frontend-hook:${lane.name}:failure-summary]\n${summary}\n`);
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
  const outcomes = await Promise.all(FRONTEND_HOOK_TEST_LANES.map(async (lane) => {
    const result = await runCommand(npmCommand, ['run', lane.script], {
      cwd,
      timeoutMs: LANE_TIMEOUT_MS,
      maxBuffer: LANE_MAX_BUFFER,
    });
    const failure = laneFailure(lane, result);
    if (failure && !terminating) {
      terminating = true;
      terminate('SIGTERM');
    }
    return { lane, result, failure };
  }));
  const failed = outcomes.filter(({ failure }) => failure);
  for (const { lane, result, failure } of outcomes) {
    if (!failure) writeLaneOutput(lane, result, stdout, stderr);
  }
  for (const { lane, result } of failed) writeLaneFailureSummary(lane, result, stderr);
  if (failed.length > 0) {
    throw new Error(`frontend hook lanes failed: ${failed.map(({ failure }) => failure).join('; ')}`);
  }
}

if (process.argv[1] && path.resolve(process.argv[1]) === SCRIPT_PATH) {
  runFrontendHookTests().catch((error) => {
    console.error(error instanceof Error ? error.message : String(error));
    process.exitCode = 1;
  });
}
