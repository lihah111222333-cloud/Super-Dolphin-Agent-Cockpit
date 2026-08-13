/* global process */
import { expect, test } from '@playwright/test';
import { mkdir, writeFile } from 'node:fs/promises';
import path from 'node:path';

import { agenticE2ESandboxForRun, prepareAgenticE2ESandbox } from '../../scripts/agentic-e2e-sandbox.mjs';
import {
  assertAgenticE2EMockWailsClean,
  installAgenticE2EMockWails,
  readAgenticE2EMockWailsState,
} from '../../scripts/agentic-e2e-wails-mock.mjs';

const REPO_ROOT = path.resolve(process.cwd(), '..');
const BUG_REPORT_DIR = path.join(REPO_ROOT, '.tmp', 'agentic-e2e', 'desktop-wide-playwright');
const SETTINGS_BUTTON_NAME = /^(Settings|设置)$/u;

const BUSINESS_PAGES = Object.freeze([
  {
    label: '插件与技能',
    route: /\/skills$/u,
    navTestId: 'sidebar-nav',
    assert: async (page) => expect(page.getByRole('heading', { name: 'MCP工具' })).toBeVisible(),
  },
  {
    label: '自动化',
    route: /\/dags$/u,
    navTestId: 'sidebar-nav',
    assert: async (page) => {
      const overview = page.getByRole('region', { name: '自动化资产', exact: true });
      await expect(overview).toBeVisible();
      await expect(overview.getByRole('heading', { name: '自动化和运行状态', exact: true })).toBeVisible();
    },
  },
  {
    label: '提示词',
    route: /\/prompts$/u,
    navTestId: 'sidebar-nav',
    assert: async (page) => {
      const overview = page.getByRole('region', { name: '个性化概览', exact: true });
      await expect(overview).toBeVisible();
      await expect(overview.getByRole('heading', { name: '定制角色、知识和记忆', exact: true })).toBeVisible();
    },
  },
  {
    label: '共享文件',
    route: /\/files$/u,
    navTestId: 'sidebar-nav',
    assert: async (page) => {
      const overview = page.getByRole('region', { name: '共享文件状态', exact: true });
      await expect(overview).toBeVisible();
      await expect(overview.getByRole('heading', { name: '共享文件和最终产物', exact: true })).toBeVisible();
    },
  },
  {
    label: '记忆中心',
    route: /\/memory$/u,
    navTestId: 'sidebar-nav',
    assert: async (page) => expect(page.getByRole('heading', { name: '记忆中心', exact: true })).toBeVisible(),
  },
  {
    label: '链路追踪',
    route: /\/observability$/u,
    navTestId: 'sidebar-nav',
    assert: async (page) => expect(page.getByTestId('observability-page')).toBeVisible(),
  },
]);

test.beforeEach(async ({ page }, testInfo) => {
  const runID = safeReportName(`${testInfo.project.name}-${testInfo.title}-${testInfo.retry}`);
  const sandbox = agenticE2ESandboxForRun(REPO_ROOT, runID);
  const config = { repoRoot: REPO_ROOT, runID, sandbox };
  await prepareAgenticE2ESandbox(config);
  await installDesktopWideBugCapture(page, testInfo);
  await installAgenticE2EMockWails(page, { sandbox });
  testInfo._desktopWide = { sandbox };
});

test.afterEach(async ({ page }, testInfo) => {
  await writeDesktopWideBugReport(page, testInfo);
});

test.describe('desktop-shell', () => {
test('workbench shell keeps critical desktop regions visible and reachable', async ({ page }) => {
  await page.goto('/');

  await expectLocatorInViewport(page.getByTestId('frontend-app'));
  await expectLocatorInViewport(page.getByTestId('app-sidebar'));
  await expectLocatorInViewport(page.getByTestId('chat-page'));
  await expectLocatorInViewport(page.getByTestId('chat-layout'));
  await expectLocatorInViewport(page.getByTestId('composer-input'));
  await expectNoDocumentHorizontalOverflow(page);

  await expectCenterPointClickable(page.getByRole('button', { name: SETTINGS_BUTTON_NAME }));
  await expectCenterPointClickable(page.getByTestId('sidebar-nav').getByRole('button', { name: '链路追踪' }));
  await expectCenterPointClickable(page.getByRole('button', { name: '发送消息' }));
});

test('wide chat thread rail stays within the main canvas and receives its own hit target', async ({ page }) => {
  await page.goto('/');

  const threadRail = page.getByTestId('thread-rail');
  const mainCanvas = page.locator('.super-dolphin-agent-main-canvas');
  const chatMainColumn = page.getByTestId('chat-main-column');
  const composer = page.getByTestId('composer-dock');
  const spotlight = page.getByTestId('chat-intro-spotlight');
  await expect(threadRail).toBeVisible();
  const [railBox, canvasBox, mainBox] = await Promise.all([
    threadRail.boundingBox(),
    mainCanvas.boundingBox(),
    chatMainColumn.boundingBox(),
  ]);
  expect(railBox).toBeTruthy();
  expect(canvasBox).toBeTruthy();
  expect(mainBox).toBeTruthy();
  expect(railBox.x).toBeGreaterThanOrEqual(canvasBox.x);
  expect(railBox.x + railBox.width).toBeLessThanOrEqual(canvasBox.x + canvasBox.width);
  expectBoxesDisjointHorizontally(railBox, mainBox);
  await expectBoxWithin(composer, chatMainColumn);
  await expectBoxWithin(spotlight, chatMainColumn);
  await expectCenterPointClickable(threadRail.getByRole('button', { name: '新建对话' }));

  await page.getByRole('button', { name: '显示侧边栏' }).click();
  await expect(page.getByTestId('right-panel-resizer')).toBeVisible();
  await page.getByTestId('right-panel-resizer').press('End');
  const openMainBox = await chatMainColumn.boundingBox();
  expect(openMainBox).toBeTruthy();
  expect(openMainBox.width).toBeGreaterThanOrEqual(440);
  await expectCenterPointClickable(threadRail.getByRole('button', { name: '新建对话' }));
  await expectBoxWithin(composer, chatMainColumn);
  await expectBoxWithin(spotlight, chatMainColumn);

  const agentBoardRuntimeToggle = page.getByTestId('agent-board-show-runtime');
  if (await agentBoardRuntimeToggle.isVisible()) {
    await agentBoardRuntimeToggle.click();
    await expect(page.getByTestId('runtime-panel')).toBeVisible();
  } else {
    await page.getByTestId('runtime-show-agents').click();
    await expect(page.getByTestId('agent-board-panel')).toBeVisible();
    await page.getByTestId('right-panel-resizer').press('End');
    await page.getByTestId('agent-board-show-runtime').click();
    await expect(page.getByTestId('runtime-panel')).toBeVisible();
  }
  await page.getByTestId('right-panel-resizer').press('End');
  const runtimeMainBox = await chatMainColumn.boundingBox();
  expect(runtimeMainBox).toBeTruthy();
  expect(runtimeMainBox.width).toBeGreaterThanOrEqual(440);
  await expectCenterPointClickable(threadRail);

  const [railBeforeClose, mainBeforeClose, panelBeforeClose] = await Promise.all([
    threadRail.boundingBox(),
    chatMainColumn.boundingBox(),
    page.getByTestId('runtime-panel').boundingBox(),
  ]);
  expect(railBeforeClose).toBeTruthy();
  expect(mainBeforeClose).toBeTruthy();
  expect(panelBeforeClose).toBeTruthy();
  expect(panelBeforeClose.y).toBeCloseTo(mainBeforeClose.y, 1);
  expect(railBeforeClose.height).toBeCloseTo(mainBeforeClose.height, 1);

  await page.getByTestId('runtime-panel-collapse').click();
  await expect(page.getByTestId('runtime-panel')).not.toBeVisible();
  await page.waitForTimeout(90);
  const [railDuringClose, mainDuringClose] = await Promise.all([
    threadRail.boundingBox(),
    chatMainColumn.boundingBox(),
  ]);
  expect(railDuringClose.height).toBeCloseTo(railBeforeClose.height, 1);
  expect(mainDuringClose.height).toBeCloseTo(mainBeforeClose.height, 1);

  await page.waitForTimeout(130);
  const [railAfterClose, mainAfterClose] = await Promise.all([
    threadRail.boundingBox(),
    chatMainColumn.boundingBox(),
  ]);
  expect(railAfterClose.height).toBeCloseTo(railBeforeClose.height, 1);
  expect(mainAfterClose.height).toBeCloseTo(mainBeforeClose.height, 1);
});
});

test('1280 chat hides the thread rail without collapsing the chat main column', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.goto('/');

  await expect(page.getByTestId('thread-rail')).not.toBeVisible();
  const mainBox = await page.getByTestId('chat-main-column').boundingBox();
  expect(mainBox).toBeTruthy();
  expect(mainBox.width).toBeGreaterThan(0);
});

test.describe('desktop-business-pages', () => {
test('business pages keep their desktop first screens healthy', async ({ page }) => {
  await page.goto('/');
  await expectLocatorInViewport(page.getByTestId('frontend-app'));

  for (const entry of BUSINESS_PAGES) {
    await page.getByTestId(entry.navTestId).getByRole('button', { name: entry.label }).click();
    await expect(page).toHaveURL(entry.route);
    await entry.assert(page);
    await expectNoDocumentHorizontalOverflow(page);
    await expectLocatorInViewport(page.getByTestId('frontend-app'));
  }

  await page.getByRole('button', { name: SETTINGS_BUTTON_NAME }).click();
  await expect(page).toHaveURL(/\/settings$/u);
  await expectLocatorInViewport(page.getByTestId('settings-page'));
  await expect(page.getByTestId('settings-provider-sandbox-card')).toBeVisible();
  await expect(page.getByTestId('settings-video-card')).toBeVisible();
  await expectNoDocumentHorizontalOverflow(page);
});
});

test.describe('desktop-read-settings', () => {
test('risk-controlled read and settings probes stay inside mock Wails', async ({ page }) => {
  await page.goto('/');

  await page.getByTestId('sidebar-nav').getByRole('button', { name: '链路追踪' }).click();
  await expect(page).toHaveURL(/\/observability$/u);
  await page.getByRole('button', { name: '查询最新日志' }).click();
  await expect(page.getByTestId('observability-recent-logs')).toBeVisible();
  await expectNoDocumentHorizontalOverflow(page);

  await page.getByRole('button', { name: SETTINGS_BUTTON_NAME }).click();
  await expect(page).toHaveURL(/\/settings$/u);
  await page.locator('#settings-sf-key').fill('desktop-wide-video-key');
  await page.getByTestId('settings-video-card').getByRole('button', { name: '保存' }).click();
  await expect(page.getByTestId('settings-video-notice')).toBeVisible();

  const mock = await readAgenticE2EMockWailsState(page);
  expect(mock.settingsWrites).toEqual([
    expect.objectContaining({ method: 'ui/video/setApiKey', apiKeyLength: 'desktop-wide-video-key'.length }),
  ]);
});
});

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

async function expectCenterPointClickable(locator) {
  await expect(locator).toBeVisible();
  const result = await locator.evaluate((element) => {
    const rect = element.getBoundingClientRect();
    const x = rect.left + rect.width / 2;
    const y = rect.top + rect.height / 2;
    const top = document.elementFromPoint(x, y);
    return Boolean(top && (top === element || element.contains(top) || top.contains(element)));
  });
  expect(result).toBe(true);
}

async function expectBoxWithin(locator, container) {
  const [box, containerBox] = await Promise.all([locator.boundingBox(), container.boundingBox()]);
  expect(box).toBeTruthy();
  expect(containerBox).toBeTruthy();
  expect(box.x).toBeGreaterThanOrEqual(containerBox.x);
  expect(box.x + box.width).toBeLessThanOrEqual(containerBox.x + containerBox.width);
}

function expectBoxesDisjointHorizontally(leftBox, rightBox) {
  expect(leftBox.x + leftBox.width).toBeLessThanOrEqual(rightBox.x);
}

async function installDesktopWideBugCapture(page, testInfo) {
  const pageErrors = [];
  const consoleErrors = [];
  const failedRequests = [];
  const httpErrors = [];
  await page.addInitScript(() => {
    window.__DESKTOP_WIDE_CAPTURE__ = { runtimeTelemetry: [] };
    window.__AO_WAILS_RUNTIME_TELEMETRY__ = (detail) => {
      window.__DESKTOP_WIDE_CAPTURE__.runtimeTelemetry.push(detail);
    };
  });
  page.on('pageerror', (error) => {
    pageErrors.push(error.message);
  });
  page.on('console', (message) => {
    if (message.type() === 'error') consoleErrors.push(message.text());
  });
  page.on('requestfailed', (request) => {
    failedRequests.push({
      method: request.method(),
      url: request.url(),
      failure: request.failure()?.errorText || 'request failed',
    });
  });
  page.on('response', (response) => {
    if (response.status() < 400 || isAllowedHTTPError(response.url())) return;
    httpErrors.push({
      status: response.status(),
      url: response.url(),
      requestMethod: response.request().method(),
    });
  });
  testInfo._desktopWideBugs = { pageErrors, consoleErrors, failedRequests, httpErrors };
}

async function writeDesktopWideBugReport(page, testInfo) {
  const mock = await readAgenticE2EMockWailsState(page);
  const runtimeTelemetry = await page.evaluate(() => window.__DESKTOP_WIDE_CAPTURE__?.runtimeTelemetry || []).catch(() => []);
  const runtimeTelemetryFailures = runtimeTelemetry.filter((item) => (
    item.status === 'error' ||
    String(item.phase || '').endsWith('.failed') ||
    String(item.phase || '').endsWith('.timeout')
  ));
  const callMethods = (mock?.calls || []).map((call) => String(call.method || '')).filter(Boolean);
  const runtimeTelemetryMissing = callMethods.length > 0 && runtimeTelemetry.length === 0
    ? ['runtime telemetry hook did not receive events for observed mock Wails calls']
    : [];
  const unexpectedNonWailsSockets = (mock?.nonWailsSockets || []).filter((url) => !isAllowedNonWailsSocket(url));
  const bugs = {
    test: testInfo.title,
    project: testInfo.project.name,
    status: testInfo.status,
    expectedStatus: testInfo.expectedStatus,
    pageErrors: testInfo._desktopWideBugs?.pageErrors || [],
    consoleErrors: testInfo._desktopWideBugs?.consoleErrors || [],
    failedRequests: testInfo._desktopWideBugs?.failedRequests || [],
    httpErrors: testInfo._desktopWideBugs?.httpErrors || [],
    runtimeTelemetryFailures,
    runtimeTelemetryMissing,
    unhandledRPC: mock?.unhandledRPC || [],
    rpcFailures: mock?.failures || [],
    sandboxViolations: mock?.sandboxViolations || [],
    unexpectedNonWailsSockets,
    eventNotifications: mock?.eventNotifications || 0,
    runtimeTelemetryCount: runtimeTelemetry.length,
    callMethods,
    settingsWrites: mock?.settingsWrites || [],
  };
  bugs.capturedBugFailure = hasCapturedBugs(bugs);
  await mkdir(BUG_REPORT_DIR, { recursive: true });
  await writeFile(
    path.join(BUG_REPORT_DIR, `${testInfo.project.name}-${safeReportName(testInfo.title)}.json`),
    `${JSON.stringify(bugs, null, 2)}\n`,
    'utf8',
  );

  if (mock) assertAgenticE2EMockWailsClean(mock);
  expect(bugs.pageErrors).toEqual([]);
  expect(bugs.consoleErrors).toEqual([]);
  expect(bugs.failedRequests).toEqual([]);
  expect(bugs.httpErrors).toEqual([]);
  expect(bugs.runtimeTelemetryFailures).toEqual([]);
  expect(bugs.runtimeTelemetryMissing).toEqual([]);
  expect(bugs.unexpectedNonWailsSockets).toEqual([]);
}

function hasCapturedBugs(bugs) {
  return bugs.pageErrors.length > 0 ||
    bugs.consoleErrors.length > 0 ||
    bugs.failedRequests.length > 0 ||
    bugs.httpErrors.length > 0 ||
    bugs.runtimeTelemetryFailures.length > 0 ||
    bugs.runtimeTelemetryMissing.length > 0 ||
    bugs.unhandledRPC.length > 0 ||
    bugs.rpcFailures.length > 0 ||
    bugs.sandboxViolations.length > 0 ||
    bugs.unexpectedNonWailsSockets.length > 0;
}

function isAllowedHTTPError(value) {
  try {
    const url = new URL(value);
    return url.pathname.endsWith('.map') || url.pathname === '/favicon.ico';
  }
  catch {
    return false;
  }
}

function isAllowedNonWailsSocket(value) {
  try {
    const url = new URL(value);
    return (url.protocol === 'ws:' || url.protocol === 'wss:') &&
      (url.hostname === '127.0.0.1' || url.hostname === 'localhost') &&
      url.pathname === '/' &&
      url.searchParams.has('token');
  }
  catch {
    return false;
  }
}

function safeReportName(value) {
  return String(value || '').toLowerCase().replace(/[^a-z0-9\u4e00-\u9fff]+/giu, '-').replace(/^-+|-+$/g, '') || 'report';
}
