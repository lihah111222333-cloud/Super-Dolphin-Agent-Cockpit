// @ts-nocheck
import { test, expect } from '@playwright/test';

import { installMockBackend, readMethodCalls } from './support/mock-backend.js';
import { prepareVisualSnapshot } from './support/visual.js';

const DIFF_TEXT = [
  'diff --git a/src/main.go b/src/main.go',
  'index 1111111..2222222 100644',
  '--- a/src/main.go',
  '+++ b/src/main.go',
  '@@ -10,2 +10,3 @@',
  ' func main() {',
  '-  println("old")',
  '+  println("new")',
  '+  println("extra")',
  ' }',
].join('\n');

test('timeline interactions cover file refs, approvals, copies, image preview, and activity panel', async ({ page }) => {


  await page.addInitScript(() => {
    const copiedTexts = [];
    globalThis.__AO_E2E_COPIED_TEXTS__ = copiedTexts;
    Object.defineProperty(globalThis.navigator, 'clipboard', {
      configurable: true,
      value: {
        writeText: async (text) => {
          copiedTexts.push((text || '').toString());
        },
      },
    });
  });

  await installMockBackend(page, {
    threads: [
      {
        id: 'thread-timeline-1',
        name: '时间线线程',
        alias: '时间线线程',
        cwd: '/workspace/project-alpha',
        provider: 'claude',
        timeline: [
          {
            id: 'assistant-file-ref',
            kind: 'assistant',
            text: '请查看 `src/main.go:12` 并继续修复。',
            ts: '2026-03-06T10:00:00.000Z',
          },
          {
            id: 'approval-42',
            kind: 'approval',
            command: '允许执行 go test ./... ?',
            requestId: 42,
            ts: '2026-03-06T10:00:01.000Z',
          },
          {
            id: 'file-item-1',
            kind: 'file',
            file: '/tmp/output/report.txt',
            text: '已写入报告文件',
            ts: '2026-03-06T10:00:02.000Z',
          },
          {
            id: 'plan-item-1',
            kind: 'plan',
            text: '1. 收集日志\n2. 修复问题\n3. 回归验证',
            ts: '2026-03-06T10:00:03.000Z',
          },
          {
            id: 'plan-item-2',
            kind: 'plan',
            text: '1. 复核提交\n2. 更新文档',
            ts: '2026-03-06T10:00:03.500Z',
          },
          {
            id: 'assistant-image-1',
            kind: 'assistant',
            text: '这里是图片附件。',
            attachments: [
              {
                kind: 'image',
                name: 'preview.png',
                path: '/tmp/preview.png',
                previewUrl: 'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9sXnkwAAAABJRU5ErkJggg==',
              },
            ],
            ts: '2026-03-06T10:00:04.000Z',
          },
          {
            id: 'command-item-1',
            kind: 'command',
            title: '$ go test ./...',
            command: 'go test ./...',
            output: 'ok\tproject\t0.123s',
            status: 'done',
            exitCode: 0,
            ts: '2026-03-06T10:00:05.000Z',
          },
        ],
        diffText: DIFF_TEXT,
        activityStats: {
          lspCalls: 3,
          commands: 1,
          fileEdits: 2,
          toolCalls: {
            mcp__playwright__click: 2,
            exec_command: 1,
          },
        },
        alerts: [
          {
            id: 'alert-1',
            time: '10:00:05',
            level: 'error',
            message: 'disk almost full',
          },
        ],
      },
    ],
    activeThreadId: 'thread-timeline-1',

    statusHeadersByThread: {
      'thread-timeline-1': '等待指示',
    },
    diffTextByThread: {
      'thread-timeline-1': DIFF_TEXT,
    },
    activityStatsByThread: {
      'thread-timeline-1': {
        lspCalls: 3,
        commands: 1,
        fileEdits: 2,
        toolCalls: {
          mcp__playwright__click: 2,
          exec_command: 1,
        },
      },
    },
    alertsByThread: {
      'thread-timeline-1': [
        {
          id: 'alert-1',
          time: '10:00:05',
          level: 'error',
          message: 'disk almost full',
        },
      ],
    },
  });

  await prepareVisualSnapshot(page);


  await page.goto('/');
  await expect(page.getByTestId('app-shell')).toBeVisible();

  const fileRef = page.locator('.chat-md-file-ref').first();
  await expect(fileRef).toBeVisible();
  await fileRef.click();
  await expect(page.locator('.diff-file-group.is-focused')).toContainText('main.go');

  await page.getByRole('button', { name: '同意' }).click();
  await expect(page.locator('.approval-hint')).toContainText('审批结果已提交');

  await page.locator('.chat-process-copy-btn').first().click();
  await page.locator('.ran-plan-card-json__copy').first().click();
  const copiedTexts = await page.evaluate(() => globalThis.__AO_E2E_COPIED_TEXTS__ || []);
  expect(copiedTexts).toContain('/tmp/output/report.txt');
  expect(copiedTexts.some((item) => (item || '').includes('收集日志'))).toBeTruthy();

  const attachmentCard = page.locator('.chat-attachment-card').first();
  await attachmentCard.click();
  await expect(page.locator('.chat-attachment-lightbox__image')).toBeVisible();
  await page.getByLabel('关闭图片预览').click();
  await attachmentCard.evaluate((node) => node.blur());


  await attachmentCard.hover();
  await page.waitForTimeout(650);
  const hoverPreview = page.locator('.chat-attachment-hover-preview').first();
  await expect(hoverPreview).toBeVisible();
  await page.locator('.chat-messages-vue').dispatchEvent('scroll');
  await page.waitForTimeout(900);
  await expect(page.locator('.chat-attachment-hover-preview')).toHaveCount(0);
  await page.mouse.move(10, 10);
  await page.evaluate(() => document.activeElement?.blur?.());
  await page.locator('.activity-stats').evaluate((node) => node.click());
  await expect(page.locator('.activity-tool-detail')).toContainText('mcp__playwright__click');
  await expect(page.locator('.activity-alerts')).toContainText('go test ./...');
  await expect(page.locator('.activity-alerts')).toContainText('disk almost full');
  const approvalCalls = await readMethodCalls(page, 'approval/respond');
  expect(approvalCalls[0]?.params).toEqual(expect.objectContaining({ requestId: 42, approved: true }));
});



test('tool ticker remains visible on hover and preserves collapsed-tool summary', async ({ page }) => {
  await installMockBackend(page, {
    threads: [
      {
        id: 'thread-tool-ticker',
        name: '工具 ticker 验证',
        alias: '工具 ticker 验证',
        cwd: '/workspace/project-alpha',
        provider: 'claude',
        state: 'thinking',
        interruptible: true,
        statusHeader: '思考中',
        timeline: [
          {
            id: 'user-tool-1',
            kind: 'user',
            text: '继续执行诊断。',
            ts: '2026-03-07T12:00:00.000Z',
          },
          {
            id: 'tool-item-1',
            kind: 'tool',
            tool: 'grep',
            preview: 'search timeline-actions.spec.js',
            elapsedMs: 38,
            status: 'done',
            ts: '2026-03-07T12:00:01.000Z',
          },
          {
            id: 'tool-item-2',
            kind: 'tool',
            tool: 'exec_command',
            preview: 'npm run build',
            elapsedMs: 91,
            status: 'done',
            ts: '2026-03-07T12:00:02.000Z',
          },
        ],
      },
    ],
    activeThreadId: 'thread-tool-ticker',

    statuses: {
      'thread-tool-ticker': 'thinking',
    },
    interruptibleByThread: {
      'thread-tool-ticker': true,
    },
    statusHeadersByThread: {
      'thread-tool-ticker': '思考中',
    },
  });

  await page.goto('/');
  await expect(page.getByTestId('app-shell')).toBeVisible();
  await expect(page.getByTestId('chat-page')).toBeVisible();

  const ticker = page.locator('.chat-status-tool-ticker').first();
  const presence = page.locator('.chat-status-presence').first();
  await expect(ticker).toBeVisible();
  await expect(ticker).toContainText('grep');
  await expect(presence).toHaveAttribute('title', /已收起 2 个工具调用/);

  await ticker.hover();
  await page.waitForTimeout(300);
  await expect(ticker).toContainText('grep');

  await presence.hover();
  const popover = page.locator('.chat-thinking-hover-popover').first();
  await expect(popover).toBeVisible();
  await expect(popover).toContainText('工具调用摘要');
});

const LEAKED_PROGRESS_TEXT = [
  'I’m resuming from the current filesystem state and verifying the two exact phase-3 surfaces now: the page helper/export block and the production chat-rail integration block, plus the cleaned integration test file.',
  'I found the helper is currently living at the wrong level in the file.',
  'I’m locating that stray definition around the setup prelude so I can move it back to the top-level utility section and leave only one canonical copy.',
  'I found the file has already evolved to a different phase-3 integration shape: a top-level `buildVisibleChatThreadCards(opts)` helper plus a `visibleChatThreadCardState` computed.',
  'I’m inspecting that live path directly now instead of forcing the earlier helper signature.',
  'The live path is now clearly helper-based and the module export resolves correctly in Node.',
  'I’m rerunning the two exact phase-3 test files from the current state.',
  'Phase-3 tests are green from the current state.',
  'I’m doing a split diagnostics pass so I can confirm the production page file separately from the test-file hints.',
  'I’ve stabilized the phase-3 integration.',
].join('');

test('reasoning-like assistant text stays visible in timeline and does not show hidden popover placeholder', async ({ page }) => {
  await installMockBackend(page, {
    threads: [
      {
        id: 'thread-reasoning-hidden',
        name: '思考泄漏验证',
        alias: '思考泄漏验证',
        cwd: '/workspace/project-alpha',
        provider: 'claude',
        state: 'thinking',
        interruptible: true,
        statusHeader: '思考中',
        timeline: [
          {
            id: 'user-reasoning-1',
            kind: 'user',
            text: '继续修复 markdown 泄漏',
            ts: '2026-03-07T11:00:00.000Z',
          },
          {
            id: 'assistant-reasoning-leak-1',
            kind: 'assistant',
            text: LEAKED_PROGRESS_TEXT,
            ts: '2026-03-07T11:00:01.000Z',
          },
        ],
      },
    ],
    activeThreadId: 'thread-reasoning-hidden',

    statuses: {
      'thread-reasoning-hidden': 'thinking',
    },
    interruptibleByThread: {
      'thread-reasoning-hidden': true,
    },
    statusHeadersByThread: {
      'thread-reasoning-hidden': '思考中',
    },
  });

  await prepareVisualSnapshot(page);

  await page.goto('/');
  await expect(page.getByTestId('app-shell')).toBeVisible();
  await expect(page.getByTestId('chat-page')).toBeVisible();

  await expect(page.locator('.chat-item.role-user')).toContainText('继续修复 markdown 泄漏');
  await expect(page.locator('.chat-item.role-assistant')).toHaveCount(1);
  await expect(page.getByText('I’m resuming from the current filesystem state')).toHaveCount(1);

  const presence = page.locator('.chat-status-presence').first();
  await expect(presence).toBeVisible();
  await presence.hover();

  await expect(page.locator('.chat-thinking-hover-popover')).toHaveCount(0);
});


test('anchored thinking presence stays below chat body', async ({ page }) => {
  await installMockBackend(page, {
    threads: [{
      id: 'thread-presence-layout',
      name: '思考条布局验证',
      alias: '思考条布局验证',
      cwd: '/workspace/project-alpha',
      provider: 'claude',
      state: 'thinking',
      interruptible: true,
      statusHeader: '思考中',
      timeline: [
        { id: 'u1', kind: 'user', text: '继续执行布局修复。', ts: '2026-03-08T10:00:00.000Z' },
        { id: 'a1', kind: 'assistant', text: '正文与思考条需要分层。', ts: '2026-03-08T10:00:01.000Z' },
      ],
    }],
    activeThreadId: 'thread-presence-layout',

    statuses: { 'thread-presence-layout': 'thinking' },
    interruptibleByThread: { 'thread-presence-layout': true },
    statusHeadersByThread: { 'thread-presence-layout': '思考中' },
  });

  await page.goto('/');
  await expect(page.getByTestId('chat-page')).toBeVisible();

  const scroller = page.locator('.chat-messages-vue').first();
  const presence = page.locator('.chat-status-presence').first();
  await expect(scroller).toBeVisible();
  await expect(presence).toBeVisible();

  const [scrollerBox, presenceBox] = await Promise.all([
    scroller.boundingBox(),
    presence.boundingBox(),
  ]);

  expect(scrollerBox).not.toBeNull();
  expect(presenceBox).not.toBeNull();
  expect((scrollerBox?.y || 0) + (scrollerBox?.height || 0)).toBeLessThanOrEqual((presenceBox?.y || 0) - 4);
});
