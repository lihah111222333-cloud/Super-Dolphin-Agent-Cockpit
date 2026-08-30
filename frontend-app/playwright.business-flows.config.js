/* global process */
import { defineConfig } from '@playwright/test';
import { resolvePlaywrightVideoMode } from './playwright-capabilities.mjs';

const executablePath = process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE;
const video = resolvePlaywrightVideoMode(process.env);
if (video === false) process.stderr.write('Playwright video disabled: ffmpeg is unavailable; set SUPER_DOLPHIN_PLAYWRIGHT_VIDEO=1 after installing ffmpeg.\n');

export default defineConfig({
  testDir: './tests/e2e',
  testMatch: 'business-flows.spec.js',
  timeout: 90_000,
  expect: {
    timeout: 15_000,
  },
  reporter: [['list']],
  outputDir: '../.tmp/playwright-business-flows',
  use: {
    baseURL: process.env.SUPER_DOLPHIN_BUSINESS_FLOW_BASE_URL || 'http://127.0.0.1:5175',
    browserName: 'chromium',
    launchOptions: {
      ...(executablePath ? { executablePath } : {}),
    },
    screenshot: 'only-on-failure',
    trace: 'retain-on-failure',
    video,
  },
  webServer: {
    command: 'npm run dev',
    url: process.env.SUPER_DOLPHIN_BUSINESS_FLOW_BASE_URL || 'http://127.0.0.1:5175',
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
  },
  workers: 1,
});
