import { expect, test } from '@playwright/test';

function captureBrowserDiagnostics(page) {
  const diagnostics = [];
  const context = page.context();
  const browser = context.browser();
  page.on('pageerror', (error) => diagnostics.push(`pageerror: ${error.message}`));
  page.on('close', () => diagnostics.push('page: closed'));
  context.on('close', () => diagnostics.push('context: closed'));
  browser?.on('disconnected', () => diagnostics.push('browser: disconnected'));
  page.on('console', (message) => {
    if (message.type() === 'error' || message.type() === 'warning') {
      diagnostics.push(`console.${message.type()}: ${message.text()}`);
    }
  });
  page.on('requestfailed', (request) => diagnostics.push(
    `requestfailed: ${request.method()} ${request.url()} ${request.failure()?.errorText || ''}`,
  ));
  return diagnostics;
}

async function expectChatShellReady(page, browserDiagnostics) {
  await expect(page.getByTestId('frontend-app')).toBeVisible();
  const deadline = Date.now() + 30_000;
  let bodyText = '';
  let readyState = '';
  while (Date.now() < deadline && !page.isClosed()) {
    if (await page.getByTestId('chat-page').isVisible()) return;
    bodyText = await page.locator('body').innerText().catch((error) => `<unavailable: ${error.message}>`);
    readyState = await page.evaluate(() => document.readyState).catch((error) => `<unavailable: ${error.message}>`);
    await new Promise((resolve) => setTimeout(resolve, 500));
  }
  throw new Error([
    `chat shell unavailable at ${page.url()}`,
    `readyState: ${readyState}`,
    `browser diagnostics: ${JSON.stringify(browserDiagnostics)}`,
    `body: ${bodyText.slice(0, 2048)}`,
  ].join('\n'));
}

test('terminal-failed crosses production Wails application and EventBridge into real DOM', {
  annotation: [
    { type: 't03-hops', description: JSON.stringify(['claudecli.raw', 'claudecli.adapter', 'turndto.TurnOutputDelta', 'wails.EventBridge', 'chromium.DOM', 'codexapp.raw', 'codexapp.adapter', 'turndto.TurnCompleted', 'turn/terminal', 'chromium.DOM']) },
    { type: 't03-dom-assertions', description: JSON.stringify(['partial-response-visible', 'safe-terminal-visible', 'raw-secret-absent', 'raw-private-path-absent', 'raw-stack-absent', 'legacy-remote-copy-absent']) },
  ],
}, async ({ page }, testInfo) => {
  const browserDiagnostics = captureBrowserDiagnostics(page);

  await page.goto('/');
  await expectChatShellReady(page, browserDiagnostics);
  const trigger = await page.evaluate(async () => {
    const { callAPI } = await import('/src/shared/api/wailsBridge.js');
    return callAPI('failure-smoke/trigger', { caseId: 'terminal-failed' });
  });
  expect(trigger).toEqual({ ok: true, caseId: 'terminal-failed' });

  await expect(page.getByText('桌面 smoke 部分响应')).toBeVisible();
  const terminalAlert = page.locator('.turn-terminal-error');
  await expect(terminalAlert).toBeVisible();
  await expect(terminalAlert).toContainText('提供方暂不可用');
  await expect(terminalAlert).toContainText('提供方未能完成本轮请求，请稍后重试。');
  await expect(page.locator('.turn-terminal-status')).toHaveCount(0);
  await expect(page.getByTestId('chat-action-feedback')).toHaveClass(/is-error/u);
  const body = page.locator('body');
  await expect(body).not.toContainText('Authorization: Bearer t03-raw-provider-secret-do-not-persist');
  await expect(body).not.toContainText('/private/provider/config.yaml');
  await expect(body).not.toContainText('stack: provider failure');
  await expect(body).not.toContainText('本次执行失败');
  await expect(body).not.toContainText('Provider 未能完成本次执行。');
  expect(browserDiagnostics).toEqual([]);
  await testInfo.attach('t03-execution-evidence', {
    body: JSON.stringify({
      hops: ['claudecli.raw', 'claudecli.adapter', 'turndto.TurnOutputDelta', 'wails.EventBridge', 'chromium.DOM', 'codexapp.raw', 'codexapp.adapter', 'turndto.TurnCompleted', 'turn/terminal', 'chromium.DOM'],
      domAssertions: ['partial-response-visible', 'safe-terminal-visible', 'raw-secret-absent', 'raw-private-path-absent', 'raw-stack-absent', 'legacy-remote-copy-absent'],
    }),
    contentType: 'application/json',
  });
});

test('prompt-history-reject crosses production Wails application and preserves real DOM input', {
  annotation: [
    { type: 't03-hops', description: JSON.stringify(['wails.rpc', 'thread/promptHistory', 'frontend.action', 'chromium.DOM', 'retry.control', 'wails.rpc', 'chromium.DOM']) },
    { type: 't03-dom-assertions', description: JSON.stringify(['draft-preserved', 'cursor-preserved', 'retry-click-recovers']) },
  ],
}, async ({ page }, testInfo) => {
  const browserDiagnostics = captureBrowserDiagnostics(page);

  await page.goto('/');
  await expectChatShellReady(page, browserDiagnostics);
  const composer = page.getByTestId('composer-input');
  await composer.fill('draft kept');
  await composer.evaluate((textarea) => textarea.setSelectionRange(3, 3));
  await composer.press('ArrowUp');

  const alert = page.getByTestId('global-action-failure');
  await expect(alert).toBeVisible();
  await expect(alert).toContainText('提示历史暂时不可用');
  await expect(alert).not.toContainText('prompt history private token=secret');
  await expect(page.getByText('Authorization: Bearer t03-raw-provider-secret-do-not-persist')).toHaveCount(0);
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
  await expect(page.getByText('Authorization: Bearer t03-raw-provider-secret-do-not-persist')).toHaveCount(0);
  expect(browserDiagnostics).toEqual([]);
  await testInfo.attach('t03-execution-evidence', {
    body: JSON.stringify({
      hops: ['wails.rpc', 'thread/promptHistory', 'frontend.action', 'chromium.DOM', 'retry.control', 'wails.rpc', 'chromium.DOM'],
      domAssertions: ['draft-preserved', 'cursor-preserved', 'retry-click-recovers'],
    }),
    contentType: 'application/json',
  });
});
