import { test, expect } from '@playwright/test';

import { installMockBackend } from './support/mock-backend.js';

const MULTI_FILE_DIFF = [
  'diff --git a/src/a.js b/src/a.js',
  '--- a/src/a.js',
  '+++ b/src/a.js',
  '@@ -1,1 +1,2 @@',
  ' line1',
  '+added',
  'diff --git a/src/b.js b/src/b.js',
  '--- a/src/b.js',
  '+++ b/src/b.js',
  '@@ -10,1 +10,2 @@',
  ' line9',
  '+other',
].join('\n');

test('diff panel groups multi-file changes and re-expands the focused file', async ({ page }) => {
  await installMockBackend(page, {
    threads: [
      {
        id: 'thread-diff-grouping-1',
        name: '多文件 Diff 线程',
        alias: '多文件 Diff 线程',
        cwd: '/workspace/project-alpha',
        provider: 'claude',
        timeline: [
          {
            id: 'assistant-diff-file-ref',
            kind: 'assistant',
            text: '请先查看 `src/a.js:2`，再核对 `src/b.js:11`。',
            ts: '2026-03-10T12:00:00.000Z',
          },
        ],
        diffText: MULTI_FILE_DIFF,
      },
    ],
    activeThreadId: 'thread-diff-grouping-1',

  });

  await page.goto('/');
  await expect(page.getByTestId('app-shell')).toBeVisible();
  await expect(page.getByTestId('chat-page')).toBeVisible();

  const diffPanel = page.locator('#diff-panel');
  const fileGroups = diffPanel.locator('.diff-file-group');
  await expect(fileGroups).toHaveCount(2);
  await expect(fileGroups.nth(0)).toContainText('src/a.js');
  await expect(fileGroups.nth(1)).toContainText('src/b.js');

  const firstFileGroup = fileGroups.filter({ hasText: 'src/a.js' }).first();
  const secondFileGroup = fileGroups.filter({ hasText: 'src/b.js' }).first();
  const firstToggle = firstFileGroup.locator('.diff-file-toggle');
  const firstLines = firstFileGroup.locator('.diff-file-lines');
  const secondLines = secondFileGroup.locator('.diff-file-lines');

  await expect(firstToggle).toHaveAttribute('aria-expanded', 'true');
  await expect(firstToggle).toHaveAttribute('aria-label', '折叠 src/a.js 的变更');
  await expect(firstLines).toBeVisible();
  await expect(secondLines).toBeVisible();

  await firstToggle.click();
  await expect(firstToggle).toHaveAttribute('aria-expanded', 'false');
  await expect(firstToggle).toHaveAttribute('aria-label', '展开 src/a.js 的变更');
  await expect(firstLines).not.toBeVisible();
  await expect(secondLines).toBeVisible();

  const fileRef = page.locator('.chat-md-file-ref[data-file-path="src/a.js"]').first();
  await expect(fileRef).toBeVisible();
  await fileRef.click();

  await expect(firstFileGroup).toHaveClass(/is-focused/);
  await expect(firstToggle).toHaveAttribute('aria-expanded', 'true');
  await expect(firstToggle).toHaveAttribute('aria-label', '折叠 src/a.js 的变更');
  await expect(firstLines).toBeVisible();
  await expect(firstFileGroup.locator('.diff-line.is-focused-line')).toBeVisible();
  await expect(firstFileGroup.locator('.diff-line.is-focused-line')).toContainText('added');
});
