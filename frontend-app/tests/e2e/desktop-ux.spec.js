import { expect, test } from '@playwright/test';

test('desktop new UI core UX smoke', async ({ page }) => {
  const pageErrors = [];
  page.on('pageerror', (error) => pageErrors.push(error.message));

  await page.goto('/');

  await expect(page.getByTestId('frontend-app')).toBeVisible();
  await expect(page.getByTestId('app-sidebar')).toBeVisible();
  await expect(page.getByTestId('chat-page')).toBeVisible();
  await expect(page.getByRole('heading', { name: '聊天页面' })).toBeVisible();
  await expect(page.getByRole('button', { name: '插件与技能', exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: '链路追踪', exact: true })).toBeVisible();

  const composer = page.getByTestId('composer-input');
  await composer.fill('Playwright UX smoke input');
  await expect(composer).toHaveValue('Playwright UX smoke input');

  const chatActions = page.locator('.chat-page-header .chat-more-button');
  await expect(chatActions).toHaveAttribute('aria-label', '聊天操作');
  await chatActions.click();
  const actionsMenu = page.getByTestId('chat-actions-menu');
  await expect(actionsMenu).toBeVisible();
  await expect(actionsMenu.getByRole('menuitem', { name: '新窗口（独立进程）' })).toBeVisible();
  await actionsMenu.getByRole('menuitem', { name: '显示侧边栏' }).click();
  await expect(page.getByTestId('runtime-panel')).toBeVisible();

  await page.getByRole('button', { name: '链路追踪', exact: true }).click();
  await expect(page).toHaveURL(/\/observability$/);
  await expect(page.getByTestId('observability-page')).toBeVisible();
  await page.getByRole('button', { name: '查询最新日志' }).click();
  await expect(page.getByTestId('observability-recent-logs')).toBeVisible();

  await page.getByRole('button', { name: 'Settings' }).click();
  await expect(page).toHaveURL(/\/settings$/);
  await expect(page.getByTestId('settings-page')).toBeVisible();
  await expect(page.getByTestId('settings-provider-sandbox-card')).toBeVisible();
  await expect(page.getByTestId('settings-video-card')).toBeVisible();

  await page.getByRole('button', { name: '新会话' }).click();
  await expect(page).toHaveURL(/\/$/);
  await expect(page.getByTestId('chat-page')).toBeVisible();
  expect(pageErrors).toEqual([]);
});

test('mobile client keeps workbench settings and composer within viewport', async ({ page }) => {
  const pageErrors = [];
  page.on('pageerror', (error) => pageErrors.push(error.message));
  await page.setViewportSize({ width: 760, height: 900 });

  await page.goto('/');

  await expect(page.getByTestId('frontend-app')).toBeVisible();
  const composer = page.locator('.composer').first();
  await expect(composer).toBeVisible();
  const composerBox = await composer.boundingBox();
  expect(composerBox).toBeTruthy();
  expect(composerBox.x).toBeGreaterThanOrEqual(0);
  expect(composerBox.x + composerBox.width).toBeLessThanOrEqual(760);

  await page.getByRole('button', { name: '打开工作台' }).click();
  await expect(page.getByTestId('frontend-app')).toHaveClass(/sidebar-open/);
  await page.getByRole('button', { name: 'Settings' }).click();
  await expect(page).toHaveURL(/\/settings$/);
  await expect(page.getByTestId('settings-page')).toBeVisible();
  await expect(page.getByTestId('frontend-app')).not.toHaveClass(/sidebar-open/);
  expect(pageErrors).toEqual([]);
});
