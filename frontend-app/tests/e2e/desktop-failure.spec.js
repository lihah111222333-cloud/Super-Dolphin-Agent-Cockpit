import { expect, test } from '@playwright/test';

test('terminal-failed crosses Go RPC WebSocket and canonical event surface into real DOM', async ({ page }) => {
  const pageErrors = [];
  page.on('pageerror', (error) => pageErrors.push(error.message));

  await page.goto('/');
  await expect(page.getByTestId('frontend-app')).toBeVisible();
  await expect(page.getByTestId('chat-page')).toBeVisible();
  await expect(page.getByRole('button', { name: /^Failure smoke thread/u })).toBeVisible();

  const trigger = await page.evaluate(async () => {
    const { callAPI } = await import('/src/shared/api/wailsBridge.js');
    return callAPI('failure-smoke/trigger', { caseId: 'terminal-failed' });
  });
  expect(trigger).toEqual({ ok: true, caseId: 'terminal-failed' });

  await expect(page.getByText('桌面 smoke 部分响应')).toBeVisible();
  const terminalAlert = page.locator('.turn-terminal-error');
  await expect(terminalAlert).toBeVisible();
  await expect(terminalAlert).toContainText('运行失败');
  await expect(terminalAlert).toContainText('提供方未能完成本轮响应');
  await expect(page.locator('.turn-terminal-status')).toHaveCount(0);
  await expect(page.getByTestId('chat-action-feedback')).toHaveClass(/is-error/u);
  await expect(page.getByText('provider internal stack secret')).toHaveCount(0);
  expect(pageErrors).toEqual([]);
});
