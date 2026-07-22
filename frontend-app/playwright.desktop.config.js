/* global process */
import { defineConfig } from '@playwright/test';

const executablePath = process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE;
if (!executablePath) {
  throw new Error('PLAYWRIGHT_CHROMIUM_EXECUTABLE is required for desktop UX smoke');
}

export default defineConfig({
  testDir: './tests/e2e',
  timeout: 60_000,
  expect: {
    timeout: 15_000,
  },
  reporter: [['list']],
  use: {
    baseURL: process.env.SUPER_DOLPHIN_DESKTOP_UX_BASE_URL || 'http://127.0.0.1:5176',
    browserName: 'chromium',
    launchOptions: {
      executablePath,
    },
    trace: 'retain-on-failure',
  },
  workers: 1,
});
