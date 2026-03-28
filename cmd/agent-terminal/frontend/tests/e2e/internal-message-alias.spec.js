// @ts-nocheck
import { test, expect } from '@playwright/test';
import { installMockBackend as installSupportMockBackend } from './support/mock-backend.js';
import { prepareVisualSnapshot, expectVisualSnapshot } from './support/visual.js';

const CALL_API_METHOD_ID = 2963398832;
const GET_BUILD_INFO_METHOD_ID = 2341363104;

const RUNTIME_MODULE_SOURCE = `
const listeners = globalThis.__AO_E2E_RUNTIME_LISTENERS__ || (globalThis.__AO_E2E_RUNTIME_LISTENERS__ = new Map());
function listenerSet(name) {
  const key = String(name || '');
  let bucket = listeners.get(key);
  if (!bucket) {
    bucket = new Set();
    listeners.set(key, bucket);
  }
  return bucket;
}
export const Call = {
  async ByID(methodId, ...args) {
    const backend = globalThis.__AO_E2E_BACKEND__;
    if (!backend || typeof backend.byId !== 'function') throw new Error('AO E2E backend is not installed');
    return backend.byId(methodId, ...args);
  },
};
export const Events = {
  On(name, callback) {
    const bucket = listenerSet(name);
    bucket.add(callback);
    return () => bucket.delete(callback);
  },
  Off(name) {
    listeners.delete(String(name || ''));
  },
};
`;
function buildSnapshot({ targetId, targetAlias, sourceId = '', sourceAlias = '', activeThreadId = targetId, item, extraThreads = [] }) {
  const activeProject = '/tmp/go-agent-v2-e2e';
  const threads = [{
    id: targetId,
    name: targetAlias,
    alias: targetAlias,
    state: 'idle',
    provider: 'codex',
    cwd: activeProject,
    timeline: [item],
  }];
  if (sourceId) {
    threads.push({
      id: sourceId,
      name: sourceAlias || sourceId,
      alias: sourceAlias || sourceId,
      state: 'idle',
      provider: 'codex',
      cwd: activeProject,
      timeline: [],
    });
  }
  extraThreads.forEach((thread) => {
    const id = (thread?.id || '').toString().trim();
    if (!id) return;
    threads.push({
      id,
      name: thread.name || thread.alias || id,
      alias: thread.alias || thread.name || id,
      state: thread.state || 'idle',
      provider: thread.provider || 'codex',
      cwd: thread.cwd || activeProject,
      timeline: Array.isArray(thread.timeline) ? thread.timeline : [],
    });
  });
  return {
    projects: [activeProject],
    activeProject,
    threads,
    activeThreadId,

  };
}

async function installMockBackend(page, snapshot) {
  await installSupportMockBackend(page, snapshot);
}


async function openTargetThread(page, threadAlias) {
  const threadCard = page.locator('.thread-rail-item[role="button"]').filter({ hasText: threadAlias }).first();
  await expect(threadCard).toBeVisible();
  await threadCard.click();
}

test.describe('internal message alias display', () => {
  test('shows agent -> agent internal sender and receiver aliases in timeline', async ({ page }) => {
    const targetId = 'thread-target-agent';
    const sourceId = 'thread-source-agent';
    const sourceAlias = '主代理备注';
    const targetAlias = '子代理备注';
    await installMockBackend(page, buildSnapshot({
      targetId,
      targetAlias,
      sourceId,
      sourceAlias,
      item: {
        id: 'internal-user-agent-1',
        kind: 'user',
        text: '请帮我继续处理这个任务',
        ts: new Date(Date.UTC(2026, 2, 6, 10, 0, 0)).toISOString(),
        internal: true,
        sourceKind: 'agent',
        fromThreadId: sourceId,
        toThreadId: targetId,
        fromDisplay: sourceAlias,
        toDisplay: targetAlias,
      },
    }));

    await page.goto('/');
    await expect(page.getByTestId('app-shell')).toBeVisible();

    const bubble = page.locator('.chat-item.kind-user.kind-internal.role-internal').first();
    await expect(bubble).toBeVisible();
    await expect(bubble).toHaveClass(/\bkind-internal\b/);
    await expect(bubble).toHaveClass(/\brole-internal\b/);
    await expect(bubble.locator('.chat-item-role')).toHaveText(sourceAlias);
    await expect(bubble.locator('.chat-item-route')).toHaveText(`→ ${targetAlias}`);
    await expect(bubble.locator('.chat-item-body')).toContainText('请帮我继续处理这个任务');
  });

  test('shows system -> agent internal route for approval/report style messages', async ({ page }) => {
    const targetId = 'thread-target-system';
    const targetAlias = '执行代理备注';
    await installMockBackend(page, buildSnapshot({
      targetId,
      targetAlias,
      item: {
        id: 'internal-user-system-1',
        kind: 'user',
        text: 'yes',
        ts: new Date(Date.UTC(2026, 2, 6, 10, 1, 0)).toISOString(),
        internal: true,
        sourceKind: 'approval',
        fromThreadId: 'system',
        toThreadId: targetId,
        fromDisplay: '系统',
        toDisplay: targetAlias,
      },
    }));

    await page.goto('/');
    await expect(page.getByTestId('app-shell')).toBeVisible();

    await expect(page.locator('.chat-item.kind-user .chat-item-role')).toHaveText('系统');
    await expect(page.locator('.chat-item.kind-user .chat-item-route')).toHaveText(`→ ${targetAlias}`);
    await expect(page.locator('.chat-item.kind-user .chat-item-body')).toContainText('yes');
  });

  test('resolves agent -> agent aliases after cross-thread switch instead of stale payload names', async ({ page }) => {
    const targetId = 'thread-target-switch-agent';
    const sourceId = 'thread-source-switch-agent';
    const sourceAlias = '源代理备注';
    const targetAlias = '目标代理备注';
    await installMockBackend(page, buildSnapshot({
      targetId,
      targetAlias,
      sourceId,
      sourceAlias,
      activeThreadId: sourceId,
      item: {
        id: 'internal-user-agent-switch-1',
        kind: 'user',
        text: '跨线程切换后也要显示备注名',
        ts: new Date(Date.UTC(2026, 2, 6, 10, 2, 0)).toISOString(),
        internal: true,
        sourceKind: 'agent',
        fromThreadId: sourceId,
        toThreadId: targetId,
        fromDisplay: '旧发送者名称',
        toDisplay: '旧接收者名称',
      },
    }));

    await page.goto('/');
    await expect(page.getByTestId('app-shell')).toBeVisible();
    await openTargetThread(page, targetAlias);

    await expect(page.locator('.chat-item.kind-user .chat-item-route')).toHaveText(`→ ${targetAlias}`);
    await expect(page.locator('.chat-item.kind-user .chat-item-route')).not.toContainText('旧发送者名称');
    await expect(page.locator('.chat-item.kind-user .chat-item-route')).not.toContainText('旧接收者名称');
    await expect(page.locator('.chat-item.kind-user .chat-item-body')).toContainText('跨线程切换后也要显示备注名');
  });

  test('resolves system -> agent route after cross-thread switch and normalizes system label', async ({ page }) => {
    const targetId = 'thread-target-switch-system';
    const targetAlias = '审批执行代理';
    const otherId = 'thread-other-observer';
    const otherAlias = '旁路线程';
    await installMockBackend(page, buildSnapshot({
      targetId,
      targetAlias,
      sourceId: otherId,
      sourceAlias: otherAlias,
      activeThreadId: otherId,
      item: {
        id: 'internal-user-system-switch-1',
        kind: 'user',
        text: 'auto-report: done',
        ts: new Date(Date.UTC(2026, 2, 6, 10, 3, 0)).toISOString(),
        internal: true,
        sourceKind: 'system',
        fromThreadId: 'system',
        toThreadId: targetId,
        fromDisplay: '过期系统名',
        toDisplay: '过期目标名',
      },
    }));

    await page.goto('/');
    await expect(page.getByTestId('app-shell')).toBeVisible();
    await openTargetThread(page, targetAlias);

    await expect(page.locator('.chat-item.kind-user .chat-item-route')).toHaveText(`→ ${targetAlias}`);
    await expect(page.locator('.chat-item.kind-user .chat-item-route')).not.toContainText('过期系统名');
    await expect(page.locator('.chat-item.kind-user .chat-item-route')).not.toContainText('过期目标名');
    await expect(page.locator('.chat-item.kind-user .chat-item-body')).toContainText('auto-report: done');
  });

  test('matches internal agent report bubble visual snapshot', async ({ page }) => {
    const targetId = 'thread-target-visual';
    const sourceId = 'thread-source-visual';
    const sourceAlias = '视觉源代理';
    const targetAlias = '视觉目标代理';
    await installMockBackend(page, buildSnapshot({ targetId, targetAlias, sourceId, sourceAlias, item: {
      id: 'internal-user-agent-visual-1', kind: 'user', text: '已完成补测，请确认视觉差异。',
      ts: new Date(Date.UTC(2026, 2, 6, 10, 4, 0)).toISOString(), internal: true, sourceKind: 'agent',
      fromThreadId: sourceId, toThreadId: targetId, fromDisplay: sourceAlias, toDisplay: targetAlias,
    } }));

    await prepareVisualSnapshot(page);
    await page.goto('/');
    await expect(page.getByTestId('app-shell')).toBeVisible();

    const bubble = page.locator('.chat-item.kind-user.kind-internal.role-internal').first();
    await expect(bubble).toBeVisible();
    await expectVisualSnapshot(bubble, 'internal-agent-report-bubble.png');
  });
});
