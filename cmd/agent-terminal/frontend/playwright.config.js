import { defineConfig, devices } from "@playwright/test";

const PORT = 4173;
const HOST = "127.0.0.1";
const BASE_URL = `http://${HOST}:${PORT}`;
const IS_CI = Boolean((/** @type {any} */ (globalThis)).process?.env?.CI);

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
    use: {
        baseURL: BASE_URL,
        testIdAttribute: "data-testid",
        trace: "on-first-retry",
        screenshot: "only-on-failure",
        video: "retain-on-failure",
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
