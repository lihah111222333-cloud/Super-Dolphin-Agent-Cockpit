/* global process */
import { defineConfig } from '@playwright/test';

const executablePath = process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE;
if (!executablePath) {
  throw new Error('PLAYWRIGHT_CHROMIUM_EXECUTABLE is required for desktop failure smoke');
}

export default defineConfig({
  testDir: './tests/e2e',
  testMatch: 'desktop-failure.spec.js',
  timeout: 60000,
  expect: {
    timeout: 15000,
  },
  reporter: [['list']],
  outputDir: '../.tmp/playwright-desktop-failure',
  use: {
    baseURL: process.env.SUPER_DOLPHIN_FAILURE_SMOKE_BASE_URL || 'http://127.0.0.1:5178',
    browserName: 'chromium',
    launchOptions: {
      executablePath,
    },
    screenshot: 'only-on-failure',
    trace: 'retain-on-failure',
  },
  workers: 1,
});
