// @ts-nocheck
import { test, expect } from '@playwright/test';

import { installMockBackend, readBackendState, readMethodCalls } from './support/mock-backend.js';
import { prepareVisualSnapshot, expectVisualSnapshot } from './support/visual.js';

async function mountCmdHarness(page) {
  await page.goto('/__e2e_cmd_blank__');
  await page.setContent(`<!doctype html>
<html lang="zh-CN" style="color-scheme: dark">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <link rel="stylesheet" href="/vue-app/styles.css">
  <link rel="stylesheet" href="/vue-app/agent-components.css">
  <script type="importmap">
  {
    "imports": {
      "react": "/.vite-cache/deps/react.js",
      "react-dom/client": "/.vite-cache/deps/react-dom_client.js"
    }
  }
  </script>
</head>
<body class="electron-dark">
  <div id="app"></div>
  <script type="module">
    import React from 'react';
    import ReactDOM from 'react-dom/client';
    import { UnifiedChatPage } from '/vue-app/pages/UnifiedChatPage.jsx';
    import { useProjectStore } from '/vue-app/stores/projects.js';
    import { useThreadStore } from '/vue-app/stores/threads.js';

    const { useEffect } = React;

    globalThis.__AO_CMD_HARNESS_READY__ = false;

    function Harness() {
      const projectStore = useProjectStore();
      const threadStore = useThreadStore();

      useEffect(() => {
        let active = true;
        async function init() {
          try {
            if (typeof projectStore.reloadProjects === 'function') {
              await projectStore.reloadProjects();
            }
            if (typeof threadStore.setPreferenceScopeCwd === 'function') {
              threadStore.setPreferenceScopeCwd(projectStore.state.active || '.');
            }
            if (typeof threadStore.refreshSidebarState === 'function') {
              await threadStore.refreshSidebarState();
            }
          } catch (e) {
            console.error('Harness init error:', e);
          } finally {
            if (active) {
              globalThis.__AO_CMD_HARNESS_READY__ = true;
            }
          }
        }
        init();
        return () => { active = false; };
      }, [projectStore, threadStore]);

      return React.createElement(UnifiedChatPage, {
        mode: "cmd",
        projectStore: projectStore,
        threadStore: threadStore
      });
    }

    const container = document.getElementById('app');
    const root = ReactDOM.createRoot(container);
    root.render(React.createElement(Harness));
  </script>
</body>
</html>`);

  await expect.poll(async () => page.evaluate(() => Boolean(globalThis.__AO_CMD_HARNESS_READY__))).toBe(true);
}

test('project modal, provider toggle, and new window flow work together', async ({ page }) => {
  await installMockBackend(page, {
    projects: ['/workspace/project-alpha'],
    activeProject: '.',
    preferences: {
      'settings.provider.active': 'codex',
    },
    threads: [
      {
        id: 'thread-chat-project',
        name: '项目流测试',
        alias: '项目流测试',
        cwd: '/workspace/project-alpha',
      },
    ],
    activeThreadId: 'thread-chat-project',

    selectProjectDirResult: '/workspace/project-beta',
    uiSelectProjectDirResult: '/workspace/new-window-target',
  });

  await page.goto('/');
  await expect(page.getByTestId('app-shell')).toBeVisible();

  await page.locator('.project-selector').click();
  await page.locator('.project-dropdown-add').click();
  await expect(page.locator('.modal-box')).toBeVisible();
  await page.getByRole('button', { name: '浏览...' }).click();
  await expect(page.locator('.modal-input')).toHaveValue('/workspace/project-beta');
  await page.getByRole('button', { name: '确定' }).click();

  const selector = page.locator('.project-selector');
  await expect(selector).toBeVisible();
  await expect(selector).toHaveAttribute('title', '.');
  await selector.click();
  await expect(page.locator('.project-dropdown-item[title="/workspace/project-alpha"]')).toBeVisible();
  await expect(page.locator('.project-dropdown-item[title="/workspace/project-beta"]')).toBeVisible();
  await page.locator('.project-dropdown-item[title="/workspace/project-beta"]').click();
  await expect(selector).toHaveAttribute('title', '/workspace/project-beta');
  await expect(async () => {
    const title = await page.locator('.cwd-badge').getAttribute('title');
    expect(title || '').toContain('活动项目：/workspace/project-beta');
  }).toPass();

  await page.getByTestId('provider-toggle').click();
  await expect(page.getByTestId('provider-toggle')).toContainText('Claude');

  await expect.poll(async () => {
    const calls = await readMethodCalls(page, 'ui/preferences/set');
    return calls.filter((item) => item?.params?.key === 'settings.provider.active').length;
  }).toBe(1);

  await page.getByTestId('new-window-btn').click();
  await expect.poll(async () => (await readMethodCalls(page, 'ui/openNewWindow')).length).toBe(1);

  const openNewWindowCalls = await readMethodCalls(page, 'ui/openNewWindow');
  expect(openNewWindowCalls[0]?.params?.cwd).toBe('/workspace/new-window-target');
});

test('chat composer supports attachments, send, rename, archive, recover, and restore', async ({ page }) => {
  await installMockBackend(page, {
    threads: [
      {
        id: 'thread-chat-ops',
        name: '原始会话',
        alias: '原始会话',
        cwd: '/workspace/project-alpha',
        provider: 'codex',
        state: 'idle',
        interruptible: false,
        statusHeader: '等待指示',
      },
    ],
    activeThreadId: 'thread-chat-ops',

    statuses: {
      'thread-chat-ops': 'idle',
    },
    interruptibleByThread: {
      'thread-chat-ops': false,
    },
    statusHeadersByThread: {
      'thread-chat-ops': '等待指示',
    },
    selectFilesResult: [
      '/workspace/project-alpha/screenshot.png',
      '/workspace/project-alpha/notes.txt',
    ],
  });

  await prepareVisualSnapshot(page);

  await page.goto('/');
  await expect(page.getByTestId('app-shell')).toBeVisible();
  await expect(page.getByTestId('chat-page')).toBeVisible();
  await expectVisualSnapshot(page.getByTestId('chat-page'), 'chat-page-empty-layout.png');

  await page.getByTestId('composer-attach-button').click();
  await expect(page.locator('.composer-attachments .chat-attachment-pill')).toHaveCount(2);
  await expect(page.locator('.composer-attachments')).toContainText('screenshot.png');
  await expect(page.locator('.composer-attachments')).toContainText('notes.txt');

  await page.getByTestId('composer-input').fill('请整理这些附件并汇总');
  await page.getByTestId('composer-send-button').click();

  await expect(page.getByTestId('composer-input')).toHaveValue('');
  await expect(page.locator('.composer-attachments .chat-attachment-pill')).toHaveCount(0);
  await expect.poll(async () => (await readMethodCalls(page, 'turn/start')).length).toBe(1);

  const turnStartCalls = await readMethodCalls(page, 'turn/start');
  const input = turnStartCalls[0]?.params?.input || [];
  expect(input.map((item) => item?.type)).toEqual(['text', 'localImage', 'mention']);
  expect(input[1]?.path).toBe('/workspace/project-alpha/screenshot.png');
  expect(input[2]?.path).toBe('/workspace/project-alpha/notes.txt');

  await page.locator('.thread-rail-name', { hasText: '原始会话' }).click();
  await expect(page.locator('.thread-rail-alias-input')).toBeVisible();
  await page.locator('.thread-rail-alias-input').fill('重命名会话');
  await page.locator('[data-rename-save-button-for="thread-chat-ops"]').click();
  await expect.poll(async () => (await readMethodCalls(page, 'thread/name/set')).length).toBe(1);
  await expect(page.locator('.thread-rail-item').filter({ hasText: '重命名会话' })).toHaveCount(1);

  const renameCalls = await readMethodCalls(page, 'thread/name/set');
  expect(renameCalls[0]?.params?.name).toBe('重命名会话');

  const activeCard = page.locator('.thread-rail-item').filter({ hasText: '重命名会话' }).first();
  await activeCard.locator('.thread-rail-archive-btn').click();
  await expect(page.getByTestId('thread-empty-state')).toBeVisible();

  await page.getByTestId('thread-archive-toggle').click();
  const archivedCard = page.locator('.thread-rail-item.archived').filter({ hasText: '重命名会话' }).first();
  await expect(archivedCard).toBeVisible();
  await archivedCard.click();

  await page.getByTestId('recover-agent-button').click();
  await expect.poll(async () => (await readMethodCalls(page, 'thread/recover')).length).toBe(1);

  await archivedCard.locator('.thread-rail-archive-btn').click();
  await page.getByTestId('thread-archive-toggle').click();
  await expect(page.locator('.thread-rail-item').filter({ hasText: '重命名会话' })).toHaveCount(1);

  const backendState = await readBackendState(page);
  expect(backendState.preferences['threadArchives.chat']).toEqual({});
});

test('cmd card stop button interrupts the selected agent and updates status', async ({ page }) => {
  await installMockBackend(page, {
    activeProject: '.',
    threads: [
      {
        id: 'thread-main-agent',
        name: '主Agent',
        alias: '主Agent',
        cwd: '/workspace/project-alpha',
        provider: 'codex',
        state: 'idle',
        interruptible: false,
        statusHeader: '等待指示',
      },
      {
        id: 'thread-cmd-stop-target',
        name: '得到的',
        alias: '得到的',
        cwd: '/workspace/project-alpha',
        provider: 'codex',
        state: 'running',
        interruptible: true,
        statusHeader: '思考中',
      },
    ],
    activeCmdThreadId: 'thread-cmd-stop-target',

    statuses: {
      'thread-main-agent': 'idle',
      'thread-cmd-stop-target': 'running',
    },
    interruptibleByThread: {
      'thread-main-agent': false,
      'thread-cmd-stop-target': true,
    },
    statusHeadersByThread: {
      'thread-main-agent': '等待指示',
      'thread-cmd-stop-target': '思考中',
    },
    agentRuntimeById: {
      'thread-main-agent': {
        cwd: '/workspace/project-alpha',
        provider: 'codex',
      },
      'thread-cmd-stop-target': {
        cwd: '/workspace/project-alpha',
        provider: 'codex',
      },
    },
  });

  await mountCmdHarness(page);

  const card = page.locator('.cmd-thread-card').filter({ hasText: '得到的' }).first();
  await expect(card).toBeVisible();
  await expect(card).toContainText('思考中');

  const stopButton = card.getByRole('button', { name: '停止' });
  await expect(stopButton).toBeEnabled();
  await stopButton.click();

  await expect.poll(async () => (await readMethodCalls(page, 'turn/interrupt')).length).toBe(1);

  const interruptCalls = await readMethodCalls(page, 'turn/interrupt');
  expect(interruptCalls[0]?.params?.threadId).toBe('thread-cmd-stop-target');

  await expect(card).toContainText('已停止');
  await expect(stopButton).toBeDisabled();

  const backendState = await readBackendState(page);
  expect(backendState.statusHeadersByThread['thread-cmd-stop-target']).toBe('已停止');
  expect(backendState.interruptibleByThread['thread-cmd-stop-target']).toBe(false);
});
