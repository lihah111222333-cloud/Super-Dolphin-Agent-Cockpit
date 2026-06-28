import { defineConfig, devices } from "@playwright/test";

const PORT = 4173;
const HOST = "127.0.0.1";
const BASE_URL = `http://${HOST}:${PORT}`;
const IS_CI = Boolean((/** @type {any} */ (globalThis)).process?.env?.CI);
const CHROMIUM_EXECUTABLE = (/** @type {any} */ (globalThis)).process?.env?.PLAYWRIGHT_CHROMIUM_EXECUTABLE || "";
const CHROMIUM_LAUNCH_OPTIONS = CHROMIUM_EXECUTABLE ? { executablePath: CHROMIUM_EXECUTABLE } : {};

export default defineConfig({
    testDir: "./tests/e2e",
    fullyParallel: true,
    retries: IS_CI ? 2 : 0,
    workers: IS_CI ? 1 : undefined,
    timeout: 30_000,
    expect: {
        timeout: 5_000,
    },
    reporter: [
        ["list"],
        ["html", { open: "never" }],
    ],
    globalSetup: "./scripts/vitest-global-setup.js",
    use: {
        baseURL: BASE_URL,
        testIdAttribute: "data-testid",
        trace: "on-first-retry",
        screenshot: "only-on-failure",
        video: "retain-on-failure",
        launchOptions: CHROMIUM_LAUNCH_OPTIONS,
    },
    projects: [
        {
            name: "chromium",
            use: { ...devices["Desktop Chrome"] },
        },
    ],
    webServer: {
        command: `npm run dev -- --host ${HOST} --port ${PORT}`,
        url: BASE_URL,
        reuseExistingServer: !IS_CI,
        timeout: 120_000,
    },
});
