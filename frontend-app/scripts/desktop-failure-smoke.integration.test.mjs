import { expect, it } from 'vitest';
import { runDesktopFailureSmoke } from './desktop-failure-smoke.mjs';

it('runs the production Wails failure smoke as an independently cacheable workload', async () => {
  const report = await runDesktopFailureSmoke();
  expect(report).toEqual(expect.objectContaining({
    status: 'covered',
    blockedCases: [],
    testCount: 2,
  }));
}, 180_000);
