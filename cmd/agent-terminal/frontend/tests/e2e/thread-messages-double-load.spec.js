// @ts-nocheck
import { test, expect } from '@playwright/test';

const CALL_API_METHOD_ID = 2963398832;
const GET_BUILD_INFO_METHOD_ID = 2341363104;
const THREAD_ID = 'thread-e2e-dup-1';
const ASSISTANT_TEXT = '同一条 assistant 消息不应因为重复 history load 出现两次';

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
    if (!backend || typeof backend.byId !== 'function') {
      throw new Error('AO E2E backend is not installed');
    }
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
    const key = String(name || '');
    listeners.delete(key);
  },
};
`;

test.describe('thread/messages duplicate load regression', () => {
  test('bootstrap + selected-thread watch dedupe the same history load', async ({ page }) => {
    await page.addInitScript(({ callApiId, buildInfoId, threadId, assistantText }) => {
      const clone = (value) => JSON.parse(JSON.stringify(value));
      const state = {
        callLog: [],
        threadMessagesCalls: 0,
        snapshot: {
          threads: [
            { id: threadId, name: 'Duplicate Load Thread', state: 'idle' },
          ],
          statuses: { [threadId]: 'idle' },
          interruptibleByThread: {},
          statusHeadersByThread: {},
          statusDetailsByThread: {},
          timelinesByThread: { [threadId]: [] },
          diffTextByThread: {},
          tokenUsageByThread: {},
          agentMetaById: {},
          agentRuntimeById: {},
          activityStatsByThread: {},
          alertsByThread: {},
          activeThreadId: threadId,
          activeCmdThreadId: '',

        },
      };

      function nowISO(offset = 0) {
        return new Date(Date.UTC(2026, 2, 6, 8, 39, 20 + offset)).toISOString();
      }

      globalThis.__AO_E2E_BACKEND_STATE__ = state;
      globalThis.__AO_E2E_BACKEND__ = {
        async byId(methodId, ...args) {
          if (methodId === buildInfoId) {
            return { version: 'e2e-test', commit: 'local' };
          }
          if (methodId !== callApiId) {
            return null;
          }

          const [method, params = {}] = args;
          state.callLog.push({ method, params: clone(params) });

          switch (method) {
            case 'ui/projects/get':
              return { projects: [], active: '.' };
            case 'config/read':
              return { cwd: '/tmp/go-agent-v2-e2e' };
            case 'thread/list':
              return { threads: clone(state.snapshot.threads) };
            case 'ui/sidebar/get':
              return clone(state.snapshot);
            case 'ui/state/get':
              return clone(state.snapshot);
            case 'thread/messages': {
              state.threadMessagesCalls += 1;
              const nextTimeline = Array.isArray(state.snapshot.timelinesByThread[threadId])
                ? clone(state.snapshot.timelinesByThread[threadId])
                : [];
              nextTimeline.push({
                id: 'assistant-' + state.threadMessagesCalls,
                kind: 'assistant',
                text: assistantText,
                ts: nowISO(state.threadMessagesCalls),
              });
              state.snapshot.timelinesByThread[threadId] = nextTimeline;
              return {
                total: nextTimeline.length,
                messages: nextTimeline.map((item, index) => ({
                  id: index + 1,
                  agentId: threadId,
                  role: 'assistant',
                  eventType: 'agent_message',
                  method: '',
                  content: item.text,
                  createdAt: item.ts,
                })),
              };
            }
            case 'ui/preferences/get':
              return '';
            case 'ui/dashboard/get':
              return {};
            default:
              return {};
          }
        },
      };
    }, {
      callApiId: CALL_API_METHOD_ID,
      buildInfoId: GET_BUILD_INFO_METHOD_ID,
      threadId: THREAD_ID,
      assistantText: ASSISTANT_TEXT,
    });

    await page.route('**/wails/runtime.js', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/javascript',
        body: RUNTIME_MODULE_SOURCE,
      });
    });

    await page.goto('/');
    await expect(page.getByTestId('app-shell')).toBeVisible();

    await expect.poll(async () => {
      return page.evaluate(() => globalThis.__AO_E2E_BACKEND_STATE__?.threadMessagesCalls || 0);
    }, { timeout: 10_000 }).toBe(1);

    await expect.poll(async () => {
      return page.locator('.chat-item.kind-assistant').count();
    }, { timeout: 10_000 }).toBe(1);

    const callCount = await page.evaluate(() => globalThis.__AO_E2E_BACKEND_STATE__?.threadMessagesCalls || 0);
    const bubbleCount = await page.locator('.chat-item.kind-assistant').count();

    // 期望行为（修复后）：只加载一次，页面只出现一条 assistant。
    expect(callCount).toBe(1);
    expect(bubbleCount).toBe(1);
  });
  test('thread switch avoids extra ui/state/get after active thread preference persists', async ({ page }) => {
    const sourceId = 'thread-e2e-switch-source';
    const targetId = 'thread-e2e-switch-target';
    const targetAssistantText = '切换到目标线程后不应触发重复状态同步';

    await page.addInitScript(({ callApiId, buildInfoId, sourceId, targetId, targetAssistantText }) => {
      const clone = (value) => JSON.parse(JSON.stringify(value));
      const state = {
        callLog: [],
        phase: 'bootstrap',
        uiStateGetCalls: 0,
        uiStateGetCallsAfterSwitch: 0,
        threadMessagesCalls: 0,
        threadMessagesCallsAfterSwitch: 0,
        snapshot: {
          threads: [
            { id: sourceId, name: 'Source Switch Thread', state: 'idle' },
            { id: targetId, name: 'Target Switch Thread', state: 'idle' },
          ],
          statuses: { [sourceId]: 'idle', [targetId]: 'idle' },
          interruptibleByThread: {},
          statusHeadersByThread: {},
          statusDetailsByThread: {},
          timelinesByThread: {
            [sourceId]: [{
              id: 'source-assistant-1',
              kind: 'assistant',
              text: '源线程已有首屏历史',
              ts: new Date(Date.UTC(2026, 2, 6, 8, 40, 0)).toISOString(),
            }],
            [targetId]: [],
          },
          diffTextByThread: {},
          tokenUsageByThread: {},
          agentMetaById: {
            [sourceId]: { alias: '源线程' },
            [targetId]: { alias: '目标线程' },
          },
          agentRuntimeById: {},
          activityStatsByThread: {},
          alertsByThread: {},
          activeThreadId: sourceId,
          activeCmdThreadId: '',

        },
      };

      function toMessages(threadId, timeline) {
        return timeline.map((item, index) => ({
          id: index + 1,
          agentId: threadId,
          role: item.kind === 'assistant' ? 'assistant' : 'user',
          eventType: 'agent_message',
          method: '',
          content: item.text,
          createdAt: item.ts,
        }));
      }

      globalThis.__AO_E2E_BACKEND_STATE__ = state;
      globalThis.__AO_E2E_BACKEND__ = {
        async byId(methodId, ...args) {
          if (methodId === buildInfoId) {
            return { version: 'e2e-test', commit: 'local' };
          }
          if (methodId !== callApiId) {
            return null;
          }

          const [method, params = {}] = args;
          state.callLog.push({ method, params: clone(params) });

          switch (method) {
            case 'ui/projects/get':
              return { projects: [], active: '.' };
            case 'config/read':
              return { cwd: '/tmp/go-agent-v2-e2e' };
            case 'thread/list':
              return { threads: clone(state.snapshot.threads) };
            case 'ui/sidebar/get':
              return clone(state.snapshot);
            case 'ui/state/get':
              if (state.phase === 'switch') {
                state.uiStateGetCallsAfterSwitch += 1;
              }
              return clone(state.snapshot);
            case 'thread/messages': {
              state.threadMessagesCalls += 1;
              if (state.phase === 'switch') {
                state.threadMessagesCallsAfterSwitch += 1;
              }
              const requestedId = (params?.threadId || state.snapshot.activeThreadId || '').toString().trim();
              const nextTimeline = Array.isArray(state.snapshot.timelinesByThread[requestedId])
                ? clone(state.snapshot.timelinesByThread[requestedId])
                : [];
              if (requestedId === targetId && nextTimeline.length === 0) {
                nextTimeline.push({
                  id: 'target-assistant-1',
                  kind: 'assistant',
                  text: targetAssistantText,
                  ts: new Date(Date.UTC(2026, 2, 6, 8, 41, 0)).toISOString(),
                });
                state.snapshot.timelinesByThread[requestedId] = nextTimeline;
              }
              return {
                total: nextTimeline.length,
                messages: toMessages(requestedId, nextTimeline),
              };
            }
            case 'ui/preferences/set': {
              const key = (params?.key || '').toString();
              if (key === 'activeThreadId') {
                state.snapshot.activeThreadId = (params?.value || '').toString();
              }
              return { ok: true };
            }
            case 'ui/preferences/get':
              return '';
            case 'ui/dashboard/get':
              return {};
            default:
              return {};
          }
        },
      };
    }, {
      callApiId: CALL_API_METHOD_ID,
      buildInfoId: GET_BUILD_INFO_METHOD_ID,
      sourceId,
      targetId,
      targetAssistantText,
    });

    await page.route('**/wails/runtime.js', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/javascript',
        body: RUNTIME_MODULE_SOURCE,
      });
    });

    await page.goto('/');
    await expect(page.getByTestId('app-shell')).toBeVisible();
    await expect(page.locator('.chat-item.kind-assistant')).toHaveCount(1);

    await page.evaluate(() => {
      if (globalThis.__AO_E2E_BACKEND_STATE__) {
        globalThis.__AO_E2E_BACKEND_STATE__.phase = 'switch';
        globalThis.__AO_E2E_BACKEND_STATE__.uiStateGetCallsAfterSwitch = 0;
        globalThis.__AO_E2E_BACKEND_STATE__.threadMessagesCallsAfterSwitch = 0;
      }
    });

    const targetButton = page.locator('.thread-rail-item[role="button"]').filter({ hasText: '目标线程' }).first();
    await expect(targetButton).toBeVisible();
    await targetButton.click();

    await expect.poll(async () => {
      return page.evaluate(() => globalThis.__AO_E2E_BACKEND_STATE__?.threadMessagesCallsAfterSwitch || 0);
    }, { timeout: 10_000 }).toBe(2);
    await expect.poll(async () => {
      return page.evaluate(() => globalThis.__AO_E2E_BACKEND_STATE__?.uiStateGetCallsAfterSwitch || 0);
    }, { timeout: 10_000 }).toBe(1);

    await expect(page.locator('.chat-item.kind-assistant .chat-item-body')).toContainText(targetAssistantText);
  });
});
