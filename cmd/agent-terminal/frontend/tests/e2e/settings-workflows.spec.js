// @ts-nocheck
import { test, expect } from '@playwright/test';

import { installMockBackend, readMethodCalls } from './support/mock-backend.js';
import { prepareVisualSnapshot, expectVisualSnapshot } from './support/visual.js';

test('settings page loads project-scoped preferences and saves prompt/provider changes', async ({ page }) => {
  await installMockBackend(page, {
    buildInfo: {
      version: '1.2.3-e2e',
      commit: 'abc1234',
      runtime: 'webkit',
    },
    projects: ['/workspace/project-alpha'],
    activeProject: '/workspace/project-alpha',
    threads: [
      {
        id: 'thread-settings-1',
        name: '设置线程',
        alias: '设置线程',
        cwd: '/workspace/project-alpha',
      },
    ],
    activeThreadId: 'thread-settings-1',

    preferences: {
      'settings.provider.active': 'claude',
      'settings.provider.claude.sandbox': JSON.stringify({
        type: 'workspaceWrite',
        writableRoots: ['/workspace/project-alpha'],
        networkAccess: true,
      }),
      'settings.provider.claude.summary': 'detailed',
      'settings.provider.claude.approvalPolicy': 'on-request',
      'settings.provider.claude.effort': 'high',
      'settings.provider.claude.model': 'gpt-5.3-codex',
      'settings.provider.claude.personality': 'friendly',
      'settings.showInjectedPromptInChat': true,
      stallThresholdSec: 90,
      'config/lspPromptHint.default': '默认提示词\n第二行',
      'config/lspPromptHint.override': '初始覆盖提示词',
    },
    runtimeConfig: {
      cwd: '/workspace/project-alpha',
    },
  });

  await prepareVisualSnapshot(page);

  await page.goto('/');
  await expect(page.getByTestId('app-shell')).toBeVisible();

  await page.getByTestId('nav-settings').click();
  await expect(page.getByTestId('settings-page')).toBeVisible();
  await expect(page.getByTestId('settings-about-card')).toContainText('1.2.3-e2e');
  await expectVisualSnapshot(page.getByTestId('settings-page'), 'settings-page-project-scoped.png');

  await page.getByTestId('settings-provider-active-select').selectOption('claude');
  await expect(page.getByTestId('provider-model-select')).toHaveValue('gpt-5.3-codex');
  await expect(page.getByTestId('provider-effort-mode-select')).toHaveValue('high');
  await expect(page.getByTestId('provider-personality-select')).toHaveValue('friendly');

  await page.getByTestId('provider-model-select').selectOption('gpt-5.5');
  await page.getByTestId('provider-effort-mode-select').selectOption('medium');
  await page.getByTestId('provider-personality-select').selectOption('pragmatic');
  await page.getByTestId('provider-summary-mode-select').selectOption('concise');
  await page.getByTestId('provider-approval-mode-select').selectOption('never');
  await page.getByTestId('provider-sandbox-save-button').click();

  await expect(page.getByTestId('settings-provider-sandbox-card')).toContainText('已保存：gpt-5.5 / medium / pragmatic');

  const providerSaveCalls = await readMethodCalls(page, 'ui/preferences/set');
  const providerKeys = providerSaveCalls.map((item) => item?.params?.key).filter(Boolean);
  expect(providerKeys).toContain('settings.provider.claude.model');
  expect(providerKeys).toContain('settings.provider.claude.effort');
  expect(providerKeys).toContain('settings.provider.claude.personality');
  expect(providerKeys).toContain('settings.provider.claude.summary');
  expect(providerKeys).toContain('settings.provider.claude.approvalPolicy');
  expect(providerSaveCalls.find((item) => item?.params?.key === 'settings.provider.claude.model')?.params?.cwd).toBe('/workspace/project-alpha');

  await page.getByTestId('settings-lsp-prompt-input').fill('覆盖提示词\n第三行');
  await page.getByTestId('settings-lsp-save-button').click();
  await expect(page.getByTestId('settings-lsp-prompt-notice')).toContainText('提示词已保存');
  await expect(page.getByTestId('settings-lsp-effective-output')).toHaveValue('覆盖提示词\n第三行');

  await page.getByTestId('settings-lsp-copy-button').click();
  await expect(page.getByTestId('settings-lsp-prompt-notice')).toContainText('已复制生效提示词');
  const copyCalls = await readMethodCalls(page, 'ui/copyText');
  expect(copyCalls.some((item) => (item?.params?.text || '').includes('覆盖提示词'))).toBeTruthy();

  await page.getByTestId('settings-lsp-reset-button').click();
  await expect(page.getByTestId('settings-lsp-effective-output')).toHaveValue('默认提示词\n第二行');

  await expect(page.getByTestId('settings-show-injected-toggle-input')).toBeChecked();
  await page.getByTestId('settings-show-injected-toggle-input').uncheck();
  await expect(page.getByTestId('settings-lsp-prompt-notice')).toContainText('聊天区已改为隐藏自动注入内容');

  await page.getByTestId('settings-stall-threshold-input').fill('120');
  await page.getByTestId('settings-stall-threshold-save-button').click();
  await expect(page.getByTestId('settings-stall-notice')).toContainText('120s');
});
