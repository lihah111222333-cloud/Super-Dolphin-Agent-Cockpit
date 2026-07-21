import { execFileSync } from 'node:child_process';
import { resolve } from 'node:path';
import { expect, it } from 'vitest';
import {
  RUNNER_FEEDBACK_PROBE,
  createStopFeedbackHarness,
  loadStopFeedbackTarget,
} from './stop-feedback-benchmark.mjs';

it('keeps the unconfirmed fixture synchronized with the current strict runtime interrupt-response contract', async () => {
  const repositoryRoot = resolve(process.cwd(), '..');
  const subjectSha = execFileSync('git', ['rev-parse', 'HEAD'], { cwd: repositoryRoot, encoding: 'utf8' }).trim();
  const subjectTree = execFileSync('git', ['rev-parse', 'HEAD^{tree}'], { cwd: repositoryRoot, encoding: 'utf8' }).trim();
  const target = await loadStopFeedbackTarget({
    feedbackProbe: RUNNER_FEEDBACK_PROBE,
    subjectRoot: repositoryRoot,
    subjectSha,
    subjectTree,
  });
  const harness = createStopFeedbackHarness({ subjectSha, target });
  try {
    await expect(harness.measureUnconfirmed()).resolves.toBeGreaterThanOrEqual(0);
  }
  finally {
    harness.destroy();
  }
});
