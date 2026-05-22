// @ts-nocheck
import { test, expect } from '@playwright/test';

import {
  CALL_API_METHOD_ID,
  installMockBackend,
  readBackendState,
  readMethodCalls,
} from './support/mock-backend.js';

const PROJECT_CWD = '/workspace/project-alpha';
const OTHER_PROJECT_CWD = '/workspace/project-beta';

async function openSystemPromptPage(page, config = {}) {
  await installMockBackend(page, {
    projects: [PROJECT_CWD],
    activeProject: PROJECT_CWD,
    runtimeConfig: { cwd: PROJECT_CWD },
    promptIntentDelayMs: 750,
    ...config,
  });
  await page.goto('/');
  await expect(page.getByTestId('app-shell')).toBeVisible();
  await page.getByTestId('nav-prompts').click();
  await expect(page.getByTestId('system-prompt-page')).toBeVisible();
  await expect(page.getByTestId('sp-card-grid')).toBeVisible();
}

async function openIntentWizard(page) {
  await page.getByTestId('sp-create-btn').click();
  await expect(page.getByTestId('sp-intent-wizard')).toBeVisible();
  await expect(page.getByTestId('sp-intent-raw-input')).toHaveCount(1);
  await expect(page.getByTestId('sp-editor-overlay')).toHaveCount(0);
  await expect(page.getByTestId('sp-name-input')).toHaveCount(0);
  await expect(page.getByTestId('sp-desc-input')).toHaveCount(0);
  await expect(page.getByTestId('sp-advanced-debug')).toHaveCount(0);
}

async function locatorHeight(locator) {
  const box = await locator.boundingBox();
  expect(box).toBeTruthy();
  return box.height;
}

async function backendPromptNames(page, cwd) {
  const prompts = await backendPrompts(page, cwd);
  return prompts.map((item) => item.name || item.title || item.prompt_key || '');
}

async function backendPrompts(page, cwd) {
  const result = await page.evaluate(
    ([methodId, targetCwd]) => globalThis.__AO_E2E_BACKEND__.byId(methodId, 'prompt-assets/list', { cwd: targetCwd }),
    [CALL_API_METHOD_ID, cwd],
  );
  return result?.prompts || [];
}

function expectStableHeight(before, during) {
  expect(Math.abs(during - before)).toBeLessThanOrEqual(1);
}

test('prompt intent ordinary create flow requires review confirmation and saves with recorded payload', async ({ page }) => {
  await openSystemPromptPage(page);
  await expect(page.locator('.sp-card.is-recall').first()).toContainText('Prompt Intent UX Notes');
  await expect(page.locator('.sp-card.is-recall').first().locator('.sp-card-preview')).not.toContainText('暂无内容');
  await expect(page.locator('.sp-card.is-default-rule').first()).toContainText('No Silent Fallback Rule');
  await expect(page.locator('.sp-card.is-default-rule').first().locator('.sp-card-preview')).not.toContainText('暂无内容');

  await openIntentWizard(page);
  const typeTabs = page.getByTestId('sp-intent-type-tabs');
  const draftButton = page.getByTestId('sp-intent-draft-btn');
  const tabsHeight = await locatorHeight(typeTabs);
  const draftHeight = await locatorHeight(draftButton);

  await page.getByTestId('sp-intent-type-recall').click();
  await expect(page.getByTestId('sp-intent-confirmation')).toHaveCount(0);
  await page.getByTestId('sp-intent-type-expert').click();
  await page.getByTestId('sp-intent-raw-input').fill('review risk: create an expert that checks prompt intent regressions');
  await draftButton.click();
  await expect(draftButton).toContainText('整理中');
  expectStableHeight(draftHeight, await locatorHeight(draftButton));
  expectStableHeight(tabsHeight, await locatorHeight(typeTabs));

  await expect(page.getByTestId('sp-intent-confirmation')).toBeVisible();
  await expect(page.getByTestId('sp-intent-card-title')).toContainText('SQLC 迁移审查专家');
  await expect(page.getByTestId('sp-intent-examples')).toContainText('帮我补齐提示词意图 E2E');
  await expect(page.getByTestId('sp-intent-examples')).toContainText('闲聊一下');
  await expect(page.locator('.sp-intent-issue--review')).toBeVisible();
  await expect(page.getByTestId('sp-intent-review-confirm')).toBeVisible();
  await expect(page.getByTestId('sp-intent-save-btn')).toBeDisabled();

  await page.getByTestId('sp-intent-review-confirm').locator('input').check();
  await expect(page.getByTestId('sp-intent-save-btn')).toBeEnabled();
  await page.getByTestId('sp-intent-type-recall').click();
  await expect(page.getByTestId('sp-intent-confirmation')).toHaveCount(0);

  await page.getByTestId('sp-intent-type-expert').click();
  await page.getByTestId('sp-intent-raw-input').fill('review risk again: re-draft must reset confirmation');
  await draftButton.click();
  await expect(page.getByTestId('sp-intent-confirmation')).toBeVisible();
  await expect(page.getByTestId('sp-intent-save-btn')).toBeDisabled();
  const disabledSaveHeight = await locatorHeight(page.getByTestId('sp-intent-save-btn'));

  await page.getByTestId('sp-intent-review-confirm').locator('input').check();
  await expect(page.getByTestId('sp-intent-save-btn')).toBeEnabled();
  expectStableHeight(disabledSaveHeight, await locatorHeight(page.getByTestId('sp-intent-save-btn')));
  const listCallsBeforeSave = (await readMethodCalls(page, 'prompt-assets/list')).length;
  const saveButton = page.getByTestId('sp-intent-save-btn');
  await saveButton.click();

  await expect(page.getByTestId('sp-intent-wizard')).toHaveCount(0);
  await expect.poll(async () => (await readMethodCalls(page, 'prompt-assets/list')).length).toBeGreaterThan(listCallsBeforeSave);
  await expect(page.getByTestId('sp-notice')).toContainText('已保存，可在新对话中被 AI 发现和使用');
  await expect(page.getByTestId('sp-card-grid')).toContainText('SQLC 迁移审查专家');
  const listCallsAfterSave = await readMethodCalls(page, 'prompt-assets/list');
  expect(listCallsAfterSave.slice(listCallsBeforeSave).every(call => call.params?.cwd === PROJECT_CWD)).toBe(true);
  await expect.poll(async () => (await backendPromptNames(page, PROJECT_CWD)).includes('SQLC 迁移审查专家')).toBe(true);
  expect(await backendPromptNames(page, OTHER_PROJECT_CWD)).not.toContain('SQLC 迁移审查专家');

  const draftCalls = await readMethodCalls(page, 'prompt-intents/draft');
  expect(draftCalls).toHaveLength(2);
  expect(draftCalls.map(call => call.params?.kind)).toEqual(['expert', 'expert']);
  expect(draftCalls.every(call => call.params?.cwd === PROJECT_CWD)).toBe(true);
  expect(draftCalls.every(call => call.params?.source_type === 'user_input')).toBe(true);

  const commitCalls = await readMethodCalls(page, 'prompt-intents/commit');
  expect(commitCalls).toHaveLength(1);
  expect(commitCalls[0]?.params?.cwd).toBe(PROJECT_CWD);
  expect(commitCalls[0]?.params?.confirm_risk).toBe(true);
  const backendState = await readBackendState(page);
  expect(backendState.promptIntentCommits[0]?.savedPrompt?.enabled).toBe(true);
});

test('prompt intent global scope is visible across projects and project assets override it', async ({ page }) => {
  await openSystemPromptPage(page, { promptIntentDelayMs: 0 });
  await openIntentWizard(page);

  await page.getByTestId('sp-intent-scope-global').check({ force: true });
  await expect(page.getByTestId('sp-intent-scope')).toContainText('所有项目');
  await page.getByTestId('sp-intent-raw-input').fill('create a global expert for prompt intent regression checks');
  await page.getByTestId('sp-intent-draft-btn').click();
  await expect(page.getByTestId('sp-intent-confirmation')).toBeVisible();
  await page.getByTestId('sp-intent-save-btn').click();

  await expect(page.getByTestId('sp-intent-wizard')).toHaveCount(0);
  await expect(page.getByTestId('sp-card-grid')).toContainText('SQLC 迁移审查专家');
  await expect(page.locator('.sp-card').filter({ hasText: 'SQLC 迁移审查专家' }).first()).toContainText('所有项目');

  const commitCalls = await readMethodCalls(page, 'prompt-intents/commit');
  expect(commitCalls.at(-1)?.params).toMatchObject({
    cwd: PROJECT_CWD,
    enable_global: true,
    confirm_global: true,
  });
  await expect.poll(async () => (await backendPromptNames(page, OTHER_PROJECT_CWD)).includes('SQLC 迁移审查专家')).toBe(true);

  await page.evaluate(
    ([methodId, cwd]) => globalThis.__AO_E2E_BACKEND__.byId(methodId, 'prompts/write', {
      id: 'project/sqlc-global-override',
      name: 'SQLC 迁移审查专家',
      description: 'Project override',
      content: 'project copy',
      agentType: 'main',
      enabled: true,
      scope: 'project',
      tags: ['intent:expert', 'project-override'],
      cwd,
    }),
    [CALL_API_METHOD_ID, PROJECT_CWD],
  );

  const projectMatches = (await backendPrompts(page, PROJECT_CWD))
    .filter((item) => item.name === 'SQLC 迁移审查专家');
  expect(projectMatches).toHaveLength(1);
  expect(projectMatches[0]?.scope).toBe('project');
  expect(projectMatches[0]?.content).toBe('project copy');

  const otherMatches = (await backendPrompts(page, OTHER_PROJECT_CWD))
    .filter((item) => item.name === 'SQLC 迁移审查专家');
  expect(otherMatches).toHaveLength(1);
  expect(otherMatches[0]?.scope).toBe('global');
});

test('prompt intent blocked and default-rule conflict fixtures expose markers, alternative, and dry-run', async ({ page }) => {
  await openSystemPromptPage(page);
  await openIntentWizard(page);

  await page.getByTestId('sp-intent-type-default_rule').click();
  await page.getByTestId('sp-intent-raw-input').fill('conflict: save this as a default rule but show suggested alternative');
  await page.getByTestId('sp-intent-draft-btn').click();

  await expect(page.getByTestId('sp-intent-default-rule-review')).toBeVisible();
  await expect(page.getByTestId('sp-intent-default-rule-review')).toContainText('旧的默认规则');
  await expect(page.getByTestId('sp-intent-suggested-alternative')).toContainText('给 AI 查阅的资料');
  await expect(page.locator('.sp-intent-issue--review')).toBeVisible();
  await expect(page.getByTestId('sp-intent-dry-run-question')).not.toBeVisible();

  await page.getByTestId('sp-intent-dry-run-panel').locator('summary').click();
  await expect(page.getByTestId('sp-intent-dry-run-question')).toBeVisible();
  await page.getByTestId('sp-intent-dry-run-question').fill('这个规则会如何影响回答？');
  await page.getByTestId('sp-intent-dry-run-submit').click();
  await expect(page.getByTestId('sp-intent-dry-run-result')).toContainText('仅用于保存前验证');
  await expect(page.getByTestId('sp-intent-dry-run-result')).toContainText('default_rule');
  await expect(page.getByTestId('sp-intent-dry-run-result')).toContainText('这份草稿会建议先确认风险');
  const dryRunCalls = await readMethodCalls(page, 'prompt-intents/dry-run');
  expect(dryRunCalls).toHaveLength(1);
  expect(dryRunCalls[0]?.params?.cwd).toBe(PROJECT_CWD);
  expect(dryRunCalls[0]?.params?.question).toBe('这个规则会如何影响回答？');

  await page.getByTestId('sp-intent-type-expert').click();
  await page.getByTestId('sp-intent-raw-input').fill('blocked: missing required scope should block save');
  await page.getByTestId('sp-intent-draft-btn').click();
  await expect(page.locator('.sp-intent-issue--block')).toBeVisible();
  await expect(page.getByTestId('sp-intent-review-confirm')).toHaveCount(0);
  await expect(page.getByTestId('sp-intent-save-btn')).toBeDisabled();
  expect(await readMethodCalls(page, 'prompt-intents/commit')).toHaveLength(0);
});

test('prompt intent mobile layout has no horizontal overflow with confirmation and dry-run text', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openSystemPromptPage(page, { promptIntentDelayMs: 0 });
  await openIntentWizard(page);

  await page.getByTestId('sp-intent-type-recall').click();
  await page.getByTestId('sp-intent-raw-input').fill('review risk: mobile layout must keep review text readable');
  await page.getByTestId('sp-intent-draft-btn').click();
  await expect(page.getByTestId('sp-intent-confirmation')).toBeVisible();
  await expect(page.locator('.sp-intent-issue--review')).toBeVisible();
  await page.getByTestId('sp-intent-dry-run-panel').locator('summary').click();
  await expect(page.getByTestId('sp-intent-dry-run-question')).toBeVisible();

  const overflow = await page.evaluate(() => ({
    root: document.documentElement.scrollWidth - document.documentElement.clientWidth,
    body: document.body.scrollWidth - document.body.clientWidth,
  }));
  expect(overflow.root).toBeLessThanOrEqual(1);
  expect(overflow.body).toBeLessThanOrEqual(1);

  for (const selector of ['.sp-intent-confirmation', '.sp-intent-dry-run']) {
    const box = await page.locator(selector).boundingBox();
    expect(box).toBeTruthy();
    expect(box.x).toBeGreaterThanOrEqual(0);
    expect(box.x + box.width).toBeLessThanOrEqual(391);
  }
});
