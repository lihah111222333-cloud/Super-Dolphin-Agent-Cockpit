import { expect, test } from '@playwright/test';

test('terminal-failed crosses production Wails application and EventBridge into real DOM', async ({ page }) => {
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

test('prompt-history-reject crosses production Wails application and preserves real DOM input', async ({ page }) => {
  const pageErrors = [];
  page.on('pageerror', (error) => pageErrors.push(error.message));

  await page.goto('/');
  await expect(page.getByTestId('chat-page')).toBeVisible();
  const composer = page.getByTestId('composer-input');
  await composer.fill('draft kept');
  await composer.evaluate((textarea) => textarea.setSelectionRange(3, 3));
  await composer.press('ArrowUp');

  const alert = page.getByTestId('global-action-failure');
  await expect(alert).toBeVisible();
  await expect(alert).toContainText('提示历史暂时不可用');
  await expect(alert).not.toContainText('prompt history private token=secret');
  await expect(page.getByText('prompt history production Wails hop')).toBeVisible();
  await expect(composer).toHaveValue('draft kept');
  expect(await composer.evaluate((textarea) => {
    const input = /** @type {HTMLTextAreaElement} */ (textarea);
    return [input.selectionStart, input.selectionEnd];
  })).toEqual([3, 3]);
  await expect(page.getByText('桌面 smoke 重试恢复')).toHaveCount(0);

  await alert.getByRole('button', { name: '重试' }).click();
  await expect(composer).toHaveValue('桌面 smoke 重试恢复');
  await expect(alert).toHaveCount(0);
  await expect(page.getByText('prompt history private token=secret')).toHaveCount(0);
  expect(pageErrors).toEqual([]);
});
