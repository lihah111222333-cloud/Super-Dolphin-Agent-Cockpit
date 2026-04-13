import { defineConfig, devices } from '@playwright/test';

const BASE_URL = process.env.PLAYWRIGHT_REAL_BASE_URL || 'http://127.0.0.1:4501';

export default defineConfig({
    testDir: './tests/e2e',
    testMatch: '**/*.real.spec.js',
    fullyParallel: false,
    retries: 0,
    timeout: 60_000,
    expect: {
        timeout: 10_000,
    },
    reporter: [
        ['list'],
        ['html', { open: 'never' }],
    ],
    globalSetup: './scripts/vitest-global-setup.js',
    use: {
        baseURL: BASE_URL,
        testIdAttribute: 'data-testid',
        trace: 'retain-on-failure',
        screenshot: 'only-on-failure',
        video: 'retain-on-failure',
    },
    projects: [
        {
            name: 'chromium',
            use: { ...devices['Desktop Chrome'] },
        },
    ],
});
