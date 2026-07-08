# Desktop Wide UI E2E Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** Add an opt-in Desktop Wide UI Playwright smoke suite that catches wide desktop UI regressions with strict mock Wails and sandbox evidence.

**Architecture:** Add a dedicated Playwright config and spec so existing business-flow, agentic goal runner, and real desktop smoke boundaries stay separate. Reuse the existing agentic sandbox and strict Wails mock instead of introducing a second backend stub. Keep generated evidence under ignored `.tmp` paths.

**Tech Stack:** React/Vite frontend, Playwright, existing agentic E2E sandbox/mock helpers, Vitest for script-level regression tests.

**Verification Surface:** `frontend-app` Playwright E2E, `frontend-app` Vitest script tests, LSP diagnostics, `git diff --check`.

---

## File Structure

- Create: `frontend-app/playwright.desktop-wide.config.js`
  - Dedicated opt-in Playwright config.
  - `testMatch` only includes `desktop-wide.spec.js`.
  - Projects cover `1440x900` and `1600x1000`.
  - Output goes to `../.tmp/playwright-desktop-wide`.

- Create: `frontend-app/tests/e2e/desktop-wide.spec.js`
  - Desktop-wide smoke tests.
  - Uses existing `agentic-e2e-sandbox.mjs` and `agentic-e2e-wails-mock.mjs`.
  - Captures page/console/network/mock/runtime evidence.
  - Writes JSON bug reports to `.tmp/agentic-e2e/desktop-wide-playwright`.

- Modify: `frontend-app/package.json`
  - Add `test:e2e:desktop-wide`.

- Modify: `frontend-app/scripts/agentic-e2e.test.mjs`
  - Add config/script contract checks so the new entry cannot accidentally broaden existing desktop smoke.

- Create: `docs/superpowers/specs/2026-07-08-desktop-wide-ui-e2e-design.md`
  - Written design spec with multi-agent conditions.

- Create: `docs/plans/2026-07-08-desktop-wide-ui-e2e.md`
  - This implementation plan.

## Task 1: Add Desktop Wide Playwright Config

**Files:**
- Create: `frontend-app/playwright.desktop-wide.config.js`
- Modify: `frontend-app/package.json`
- Modify: `frontend-app/scripts/agentic-e2e.test.mjs`

- [x] **Step 1: Add failing script/config contract checks**

Add Vitest expectations in `frontend-app/scripts/agentic-e2e.test.mjs` near existing package/config tests:

```js
it('exposes the opt-in desktop wide e2e script without changing desktop smoke', async () => {
  const pkg = JSON.parse(await readFile(new URL('../package.json', import.meta.url), 'utf8'));
  expect(pkg.scripts?.['test:e2e:desktop-wide']).toBe('playwright test --config playwright.desktop-wide.config.js');
  expect(pkg.scripts?.['smoke:desktop:ux']).toBe('node scripts/desktop-ux-smoke.mjs');
});
```

Add a config text check:

```js
it('keeps desktop wide playwright isolated to its spec and tmp output', async () => {
  const config = await readFile(new URL('../playwright.desktop-wide.config.js', import.meta.url), 'utf8');
  expect(config).toContain(\"testMatch: 'desktop-wide.spec.js'\");
  expect(config).toContain(\"outputDir: '../.tmp/playwright-desktop-wide'\");
  expect(config).toContain(\"name: 'desktop-1440'\");
  expect(config).toContain(\"name: 'desktop-1600'\");
});
```

- [x] **Step 2: Run the focused test and confirm RED**

Run:

```bash
cd frontend-app
npm test -- scripts/agentic-e2e.test.mjs -t "desktop wide"
```

Expected: fail because `playwright.desktop-wide.config.js` and package script do not exist yet.

- [x] **Step 3: Add the package script**

In `frontend-app/package.json`, add:

```json
"test:e2e:desktop-wide": "playwright test --config playwright.desktop-wide.config.js"
```

Place it next to `test:e2e:business`.

- [x] **Step 4: Create the Playwright config**

Create `frontend-app/playwright.desktop-wide.config.js`:

```js
/* global process */
import { defineConfig } from '@playwright/test';

const executablePath = process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE;
const baseURL = process.env.SUPER_DOLPHIN_DESKTOP_WIDE_BASE_URL || 'http://127.0.0.1:5177';

export default defineConfig({
  testDir: './tests/e2e',
  testMatch: 'desktop-wide.spec.js',
  timeout: 90_000,
  expect: { timeout: 15_000 },
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
```

- [x] **Step 5: Run the focused test and confirm GREEN**

Run:

```bash
cd frontend-app
npm test -- scripts/agentic-e2e.test.mjs -t "desktop wide"
```

Expected: pass for the new script/config contract checks.

## Task 2: Add Desktop Wide Spec

**Files:**
- Create: `frontend-app/tests/e2e/desktop-wide.spec.js`

- [x] **Step 1: Add failing spec skeleton**

Create a spec that imports:

```js
import { expect, test } from '@playwright/test';
import { mkdir, writeFile } from 'node:fs/promises';
import path from 'node:path';

import { agenticE2ESandboxForRun, prepareAgenticE2ESandbox } from '../../scripts/agentic-e2e-sandbox.mjs';
import {
  assertAgenticE2EMockWailsClean,
  installAgenticE2EMockWails,
  readAgenticE2EMockWailsState,
} from '../../scripts/agentic-e2e-wails-mock.mjs';
```

Add `test.beforeEach` to prepare sandbox, install bug capture, and install mock Wails. Add an initial test that goes to `/` and expects `frontend-app` visible.

- [x] **Step 2: Run the new E2E and confirm initial behavior**

Run:

```bash
cd frontend-app
npm run test:e2e:desktop-wide
```

Expected: initial skeleton passes if mock setup is correct; if it fails, fix the mock setup before adding more assertions.

- [x] **Step 3: Add shell health helpers**

Add helpers:

```js
async function expectNoDocumentHorizontalOverflow(page, tolerance = 2) {
  const metrics = await page.evaluate(() => ({
    scrollWidth: document.documentElement.scrollWidth,
    clientWidth: document.documentElement.clientWidth,
  }));
  expect(metrics.scrollWidth).toBeLessThanOrEqual(metrics.clientWidth + tolerance);
}

async function expectLocatorInViewport(locator) {
  await expect(locator).toBeVisible();
  await expect(locator).toBeInViewport();
  const box = await locator.boundingBox();
  expect(box).toBeTruthy();
  expect(box.width).toBeGreaterThan(0);
  expect(box.height).toBeGreaterThan(0);
}

async function expectCenterPointClickable(page, locator) {
  const result = await locator.evaluate((element) => {
    const rect = element.getBoundingClientRect();
    const x = rect.left + rect.width / 2;
    const y = rect.top + rect.height / 2;
    const top = document.elementFromPoint(x, y);
    return Boolean(top && (top === element || element.contains(top) || top.contains(element)));
  });
  expect(result).toBe(true);
}
```

- [x] **Step 4: Add shell health test**

Test:

```js
test('workbench shell keeps critical desktop regions visible and reachable', async ({ page }) => {
  await page.goto('/');
  await expectLocatorInViewport(page.getByTestId('frontend-app'));
  await expectLocatorInViewport(page.getByTestId('app-sidebar'));
  await expectLocatorInViewport(page.getByTestId('chat-page'));
  await expectLocatorInViewport(page.getByTestId('chat-layout'));
  await expectLocatorInViewport(page.getByTestId('composer-input'));
  await expectNoDocumentHorizontalOverflow(page);

  const settings = page.getByRole('button', { name: '设置' });
  await expectCenterPointClickable(page, settings);

  await expectCenterPointClickable(page, page.getByTestId('sidebar-secondary-nav').getByRole('button', { name: '链路追踪' }));
  await expectCenterPointClickable(page, page.getByRole('button', { name: '发送消息' }));
  await expectNoDocumentHorizontalOverflow(page);
});
```

- [x] **Step 5: Add business page first-screen test**

Use canonical routes and stable headings/key controls:

```js
const BUSINESS_PAGES = [
  { label: '插件与技能', route: /\/skills$/, nav: 'sidebar-nav', assert: async (page) => expect(page.getByRole('heading', { name: 'MCP工具' })).toBeVisible() },
  { label: '自动化', route: /\/dags$/, nav: 'sidebar-nav', assert: async (page) => expect(page.getByRole('heading', { name: '自动化', exact: true })).toBeVisible() },
  { label: '提示词', route: /\/prompts$/, nav: 'sidebar-nav', assert: async (page) => expect(page.getByRole('heading', { name: '个性化' })).toBeVisible() },
  { label: '共享文件', route: /\/files$/, nav: 'sidebar-nav', assert: async (page) => expect(page.getByRole('heading', { name: '文件产物', exact: true })).toBeVisible() },
  { label: '记忆中心', route: /\/memory$/, nav: 'sidebar-secondary-nav', assert: async (page) => expect(page.getByRole('heading', { name: '记忆中心', exact: true })).toBeVisible() },
  { label: '链路追踪', route: /\/observability$/, nav: 'sidebar-secondary-nav', assert: async (page) => expect(page.getByTestId('observability-page')).toBeVisible() },
];
```

For each page, click the nav button, assert URL, run the page assertion, and assert no document horizontal overflow.

- [x] **Step 6: Add risk-controlled probe test**

Test observability latest logs and settings mock save. Do not open the runtime panel in this mock suite until there is a safe active-thread entry path; existing desktop UX smoke still covers runtime panel visibility.

```js
test('risk-controlled read and settings probes stay inside mock Wails', async ({ page }) => {
  await page.goto('/');
  await page.getByTestId('sidebar-secondary-nav').getByRole('button', { name: '链路追踪' }).click();
  await expect(page).toHaveURL(/\/observability$/);
  await page.getByRole('button', { name: '查询最新日志' }).click();
  await expect(page.getByTestId('observability-recent-logs')).toBeVisible();

  await page.getByRole('button', { name: '设置' }).click();
  await expect(page).toHaveURL(/\/settings$/);
  await page.locator('#settings-sf-key').fill('desktop-wide-video-key');
  await page.getByTestId('settings-video-card').getByRole('button', { name: '保存' }).click();
  await expect(page.getByTestId('settings-video-notice')).toBeVisible();

  const mock = await readAgenticE2EMockWailsState(page);
  expect(mock.settingsWrites).toEqual([expect.objectContaining({ method: 'ui/video/setApiKey' })]);
});
```

- [x] **Step 7: Add afterEach bug report and hard failure checks**

Write a JSON report with:

- test title/status
- pageErrors
- consoleErrors
- failedRequests
- httpErrors
- mock failures/unhandled RPC/sandbox violations
- runtime telemetry count
- call methods

Then assert arrays are empty for hard-fail categories. Do not dump raw API key or prompt content.

- [x] **Step 8: Run desktop-wide E2E and confirm GREEN**

Run:

```bash
cd frontend-app
npm run test:e2e:desktop-wide
```

Expected: all desktop-wide tests pass in both `desktop-1440` and `desktop-1600` projects.

## Task 3: Validate Existing Boundaries

**Files:**
- No source changes expected unless validation finds a real bug.

- [x] **Step 1: Run business-flow suite**

Run:

```bash
cd frontend-app
npm run test:e2e:business
```

Expected: 2 tests pass.

- [x] **Step 2: Run agentic script tests**

Run:

```bash
cd frontend-app
npm test -- scripts/agentic-e2e.test.mjs
```

Expected: all script tests pass, including new desktop-wide contract checks.

- [x] **Step 3: Run LSP diagnostics**

Open and diagnose:

- `frontend-app/playwright.desktop-wide.config.js`
- `frontend-app/tests/e2e/desktop-wide.spec.js`
- `frontend-app/scripts/agentic-e2e.test.mjs`

Expected: no diagnostics.

- [x] **Step 4: Run diff check**

Run:

```bash
git diff --check
```

Expected: no whitespace errors.

## Task 4: Commit

**Files:**
- Stage only owned files created or changed by this plan.

- [x] **Step 1: Review owned diff**

Run:

```bash
git status --short
git diff -- frontend-app/package.json frontend-app/playwright.desktop-wide.config.js frontend-app/tests/e2e/desktop-wide.spec.js frontend-app/scripts/agentic-e2e.test.mjs docs/superpowers/specs/2026-07-08-desktop-wide-ui-e2e-design.md docs/plans/2026-07-08-desktop-wide-ui-e2e.md
```

- [x] **Step 2: Stage owned files**

Run:

```bash
git add frontend-app/package.json frontend-app/playwright.desktop-wide.config.js frontend-app/tests/e2e/desktop-wide.spec.js frontend-app/scripts/agentic-e2e.test.mjs docs/superpowers/specs/2026-07-08-desktop-wide-ui-e2e-design.md docs/plans/2026-07-08-desktop-wide-ui-e2e.md
git diff --cached --check
```

- [x] **Step 3: Commit**

Run:

```bash
git commit -m "test: 新增桌面宽屏 UI E2E 套件"
```

Expected: pre-commit hook passes. If hook refreshes codemap/project-map, inspect the generated diff and include only relevant generated files.
