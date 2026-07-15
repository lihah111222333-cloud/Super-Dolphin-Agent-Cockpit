/* global process */
import { defineConfig } from '@playwright/test';

const executablePath = process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE;
const baseURL = process.env.SUPER_DOLPHIN_DESKTOP_WIDE_BASE_URL || 'http://127.0.0.1:5177';

export default defineConfig({
  testDir: './tests/e2e',
  testMatch: 'desktop-wide.spec.js',
  timeout: 90_000,
  expect: {
    timeout: 15_000,
  },
  reporter: [['list']],
  outputDir: '../.tmp/playwright-desktop-wide',
  use: {
    baseURL,
    browserName: 'chromium',
    launchOptions: {
      ...(executablePath ? { executablePath } : {}),
    },
    screenshot: 'only-on-failure',
    trace: 'retain-on-failure',
    video: 'retain-on-failure',
  },
  projects: [
    { name: 'desktop-1440', use: { viewport: { width: 1440, height: 900 } } },
    { name: 'desktop-1600', use: { viewport: { width: 1600, height: 1000 } } },
  ],
  webServer: {
    command: 'npm run dev -- --port 5177',
    url: baseURL,
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
  },
  workers: 1,
});
