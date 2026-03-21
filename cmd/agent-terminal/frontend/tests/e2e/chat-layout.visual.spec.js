// @ts-nocheck
import { test, expect } from '@playwright/test';

import { installMockBackend } from './support/mock-backend.js';
import { prepareVisualSnapshot, expectVisualSnapshot } from './support/visual.js';

const DIFF_TEXT = [
  'diff --git a/internal/apiserver/methods.go b/internal/apiserver/methods.go',
  'index 1111111..2222222 100644',
  '--- a/internal/apiserver/methods.go',
  '+++ b/internal/apiserver/methods.go',
  '@@ -12,6 +12,8 @@ func registerUIMethods() {',
  '  register("thread/list")',
  '  register("thread/start")',
  '+ register("thread/recover")',
  '+ register("ui/openNewWindow")',
  ' }',
  '',
  'diff --git a/cmd/agent-terminal/frontend/vue-app/pages/UnifiedChatPage.js b/cmd/agent-terminal/frontend/vue-app/pages/UnifiedChatPage.js',
  'index 3333333..4444444 100644',
  '--- a/cmd/agent-terminal/frontend/vue-app/pages/UnifiedChatPage.js',
  '+++ b/cmd/agent-terminal/frontend/vue-app/pages/UnifiedChatPage.js',
  '@@ -760,7 +760,10 @@ export const UnifiedChatPage = {',
  '  template: `',
  '-   <section class="page active unified-chat-page">',
  '+   <section class="page active unified-chat-page" data-testid="chat-page">',
  '+     <div class="unified-main">',
  '+       <div id="agent-workspace" class="chat-workspace with-diff">',
  ' `',
].join('\n');

async function installChatLayoutFixture(page) {
  await installMockBackend(page, {
    projects: ['/workspace/project-alpha'],
    activeProject: '/workspace/project-alpha',
    threads: [
      {
        id: 'thread-layout-active',
        name: '布局回归主线程',
        alias: '布局回归主线程',
        cwd: '/workspace/project-alpha',
        provider: 'codex',
        state: 'idle',
        interruptible: false,
        statusHeader: '等待指示',
        timeline: [
          {
            id: 'user-layout-1',
            kind: 'user',
            text: '帮我检查拆分后的聊天布局。',
            ts: '2026-03-10T10:00:00.000Z',
          },
          {
            id: 'assistant-layout-1',
            kind: 'assistant',
            text: '已确认左侧 Agent 列表和右侧 Diff 面板都需要保持稳定。',
            ts: '2026-03-10T10:00:02.000Z',
          },
        ],
        diffText: DIFF_TEXT,
      },
      {
        id: 'thread-layout-idle',
        name: '历史回归线程',
        alias: '历史回归线程',
        cwd: '/workspace/project-alpha',
        provider: 'claude',
        state: 'idle',
        interruptible: false,
        statusHeader: '等待指示',
        timeline: [
          {
            id: 'assistant-layout-2',
            kind: 'assistant',
            text: '第二条线程用于稳定左侧 rail 卡片布局。',
            ts: '2026-03-10T09:58:00.000Z',
          },
        ],
        diffText: '',
      },
    ],
    activeThreadId: 'thread-layout-active',

    statusHeadersByThread: {
      'thread-layout-active': '等待指示',
      'thread-layout-idle': '等待指示',
    },
    diffTextByThread: {
      'thread-layout-active': DIFF_TEXT,
      'thread-layout-idle': '',
    },
  });
}

test.beforeEach(async ({ page }) => {
  await installChatLayoutFixture(page);
  await prepareVisualSnapshot(page);
  await page.goto('/');
  await expect(page.getByTestId('app-shell')).toBeVisible();
  await expect(page.getByTestId('chat-page')).toBeVisible();
  await expect(page.getByTestId('thread-rail')).toBeVisible();
  await expect(page.locator('#agent-workspace')).toBeVisible();
  await expect(page.locator('.workspace-right-col #diff-panel')).toBeVisible();
});

test('chat page layout remains stable', async ({ page }) => {
  await expectVisualSnapshot(page.getByTestId('chat-page'), 'chat-page-layout-stable.png');
});

test('thread rail layout remains stable', async ({ page }) => {
  await expectVisualSnapshot(page.getByTestId('thread-rail'), 'chat-thread-rail-stable.png');
});

test('workspace and diff layout remain stable', async ({ page }) => {
  await expectVisualSnapshot(page.locator('#agent-workspace'), 'chat-workspace-diff-stable.png');
});
