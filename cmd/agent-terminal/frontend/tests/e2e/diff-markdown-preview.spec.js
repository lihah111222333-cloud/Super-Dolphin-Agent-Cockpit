// @ts-nocheck
import { test, expect } from '@playwright/test';

import { installMockBackend } from './support/mock-backend.js';

function toDesktopMojibake(text) {
  return new TextDecoder('latin1').decode(new TextEncoder().encode((text || '').toString()));
}

test('diff markdown preview repairs desktop bridge mojibake', async ({ page }) => {
  const expectedMarkdown = [
    '# 标题',
    '',
    '桌面端中文预览正常。',
    '',
    '- 第一项',
    '- 第二项',
  ].join('\n');
  const mojibakeMarkdown = toDesktopMojibake(expectedMarkdown);

  await installMockBackend(page, {
    threads: [
      {
        id: 'thread-markdown-preview-1',
        name: 'Markdown 预览线程',
        alias: 'Markdown 预览线程',
        cwd: '/workspace/project-alpha',
        provider: 'claude',
        timeline: [
          {
            id: 'assistant-markdown-file-ref',
            kind: 'assistant',
            text: '请查看 `docs/guide.md:1` 的文档预览。',
            ts: '2026-03-07T12:00:00.000Z',
          },
        ],
        diffText: '',
      },
    ],
    activeThreadId: 'thread-markdown-preview-1',

    codeOpenByPath: {
      'docs/guide.md': {
        ok: true,
        relative: 'docs/guide.md',
        language: 'markdown',
        startLine: 1,
        endLine: 6,
        totalLines: 6,
        snippet: mojibakeMarkdown.split('\n').map((text, index) => ({ line: index + 1, text })),
      },
    },
  });

  await page.goto('/');
  await expect(page.getByTestId('app-shell')).toBeVisible();

  const fileRef = page.locator('.chat-md-file-ref').first();
  await expect(fileRef).toBeVisible();
  await fileRef.click();

  const diffPanel = page.locator('#diff-panel');
  const previewCard = diffPanel.locator('.diff-media-card.chat-item-markdown');
  await expect(diffPanel).toContainText('Markdown 预览');
  await expect(previewCard).toContainText('标题');
  await expect(previewCard).toContainText('桌面端中文预览正常。');
  await expect(previewCard).toContainText('第一项');
  await expect(previewCard).toContainText('第二项');

  const previewText = await previewCard.innerText();
  expect(previewText).not.toContain(mojibakeMarkdown.split('\n')[0]);
  expect(previewText).not.toContain(mojibakeMarkdown.split('\n')[2]);
});
