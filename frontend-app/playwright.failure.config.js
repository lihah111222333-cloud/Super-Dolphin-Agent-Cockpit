/* global process */
import { defineConfig } from '@playwright/test';
import { resolveDesktopFailureSmokeTimeout } from './scripts/desktop-failure-contract.mjs';

const executablePath = process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE;
if (!executablePath) {
  throw new Error('PLAYWRIGHT_CHROMIUM_EXECUTABLE is required for desktop failure smoke');
}

export default defineConfig({
  testDir: './tests/e2e',
  testMatch: 'desktop-failure.spec.js',
  timeout: resolveDesktopFailureSmokeTimeout(process.env),
  expect: {
    timeout: 45000,
  },
  reporter: [['list']],
  outputDir: '../.tmp/playwright-desktop-failure',
  use: {
    baseURL: process.env.SUPER_DOLPHIN_FAILURE_SMOKE_BASE_URL || 'http://127.0.0.1:5178',
    browserName: 'chromium',
    launchOptions: {
      executablePath,
      args: ['--disable-dev-shm-usage'],
    },
    screenshot: 'only-on-failure',
    trace: 'retain-on-failure',
  },
  // The two failure flows use isolated browser contexts and the same read-only
  // smoke host, so run them as independent workloads. This preserves both
  // production DOM assertions while preventing one startup failure from
  // consuming two sequential assertion timeouts.
  fullyParallel: true,
  workers: 2,
});
