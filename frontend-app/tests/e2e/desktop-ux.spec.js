import { expect, test } from '@playwright/test';

test('desktop new UI core UX smoke', async ({ page }) => {
  const pageErrors = [];
  page.on('pageerror', (error) => pageErrors.push(error.message));

  await page.goto('/');

  await expect(page.getByTestId('frontend-app')).toBeVisible();
  await expect(page.getByTestId('chat-page')).toBeVisible();
  await expect(page.getByTestId('chat-toolbar')).toBeVisible();
  await expect(page.getByTestId('sidebar-nav').getByRole('button')).toHaveCount(8);

  const composer = page.getByTestId('composer-input');
  await composer.fill('Playwright UX smoke input');
  await expect(composer).toHaveValue('Playwright UX smoke input');

  const sidebarToggle = page.locator('.titlebar .sidebar-toggle');
  const initialPressed = await sidebarToggle.getAttribute('aria-pressed');
  await sidebarToggle.click();
  await expect(sidebarToggle).toHaveAttribute('aria-pressed', initialPressed === 'true' ? 'false' : 'true');

  await page.getByTestId('sidebar-nav').getByRole('button', { name: '链路追踪' }).click();
  await expect(page).toHaveURL(/\/observability$/);
  await expect(page.getByTestId('observability-page')).toBeVisible();
  await page.getByRole('button', { name: '查询最新日志' }).click();
  await expect(page.getByTestId('observability-recent-logs')).toBeVisible();

  await page.getByTestId('sidebar-nav').getByRole('button', { name: '设置' }).click();
  await expect(page).toHaveURL(/\/settings$/);
  await expect(page.getByTestId('settings-page')).toBeVisible();
  await expect(page.getByTestId('settings-provider-sandbox-card')).toBeVisible();
  await expect(page.getByTestId('settings-video-card')).toBeVisible();

  await page.getByTestId('sidebar-nav').getByRole('button', { name: 'Chat' }).click();
  await expect(page).toHaveURL(/\/$/);
  await expect(page.getByTestId('chat-page')).toBeVisible();
  expect(pageErrors).toEqual([]);
});
