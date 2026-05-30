// @ts-nocheck
import { test, expect } from '@playwright/test';

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

test('current tab chat history renders immediately even while scoped sync is blocked by later state work', async ({ page }) => {
  const sourceId = 'thread-e2e-immediate-source';
  const targetId = 'thread-e2e-immediate-target';
  const targetAssistantText = '目标线程历史应立即显示';

  await page.addInitScript(({ callApiId, buildInfoId, sourceId, targetId, targetAssistantText }) => {
    const clone = (value) => JSON.parse(JSON.stringify(value));
    const largeDiff = `diff --git a/src/huge.js b/src/huge.js\n--- a/src/huge.js\n+++ b/src/huge.js\n@@ -1,1 +1,40000 @@\n${'+line\n'.repeat(40000)}`;
    const gate = { released: false, waiters: [] };
    const state = {
      phase: 'bootstrap',
      threadMessagesCallsAfterSwitch: 0,
      uiStateGetCallsAfterSwitch: 0,
      uiStateDiffCallsAfterSwitch: 0,
      snapshot: {
        threads: [
          { id: sourceId, name: '源线程', state: 'idle' },
          { id: targetId, name: '目标线程', state: 'idle' },
        ],
        statuses: { [sourceId]: 'idle', [targetId]: 'idle' },
        interruptibleByThread: {},
        statusHeadersByThread: {},
        statusDetailsByThread: {},
        timelinesByThread: {
          [sourceId]: [{ id: 'source-assistant-1', kind: 'assistant', text: '源线程已有消息', ts: '2026-03-08T00:00:00Z' }],
          [targetId]: [],
        },
        diffTextByThread: {},
        diffRevisionByThread: { [sourceId]: 0, [targetId]: 1 },
        tokenUsageByThread: {},
        agentMetaById: { [sourceId]: { alias: '源线程' }, [targetId]: { alias: '目标线程' } },
        agentRuntimeById: {},
        activityStatsByThread: {},
        alertsByThread: {},
        activeThreadId: sourceId,
        activeCmdThreadId: '',

      },
    };
    function releaseGate() {
      gate.released = true;
      const waiters = gate.waiters.splice(0);
      waiters.forEach((resolve) => resolve());
    }
    async function waitForGate() {
      if (gate.released) return;
      await new Promise((resolve) => gate.waiters.push(resolve));
    }
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
    globalThis.__AO_E2E_RELEASE_SWITCH_SYNC__ = releaseGate;
    globalThis.__AO_E2E_BACKEND__ = {
      async byId(methodId, ...args) {
        if (methodId === buildInfoId) return { version: 'e2e-test', commit: 'local' };
        if (methodId !== callApiId) return null;
        const [method, params = {}] = args;
        switch (method) {
          case 'ui/projects/get':
            return { projects: [], active: '.' };
          case 'config/read':
            return { cwd: '/tmp/go-agent-v2-e2e' };
          case 'thread/list':
            return { threads: clone(state.snapshot.threads) };
          case 'ui/sidebar/get':
            return clone(state.snapshot);
          case 'ui/state/get': {
            if (state.phase === 'switch' && (params?.threadId || '') === targetId) {
              state.uiStateGetCallsAfterSwitch += 1;
              if (params?.includeDiff) {
                state.uiStateDiffCallsAfterSwitch += 1;
                await waitForGate();
                state.snapshot.diffTextByThread[targetId] = largeDiff;
              }
            }
            return clone(state.snapshot);
          }
          case 'thread/messages': {
            const requestedId = (params?.threadId || state.snapshot.activeThreadId || '').toString().trim();
            if (state.phase === 'switch' && requestedId === targetId) {
              state.threadMessagesCallsAfterSwitch += 1;
            }
            const nextTimeline = Array.isArray(state.snapshot.timelinesByThread[requestedId])
              ? clone(state.snapshot.timelinesByThread[requestedId])
              : [];
            if (requestedId === targetId && nextTimeline.length === 0) {
              nextTimeline.push({ id: 'target-assistant-1', kind: 'assistant', text: targetAssistantText, ts: '2026-03-08T00:01:00Z' });
              state.snapshot.timelinesByThread[requestedId] = nextTimeline;
            }
            return { total: nextTimeline.length, messages: toMessages(requestedId, nextTimeline) };
          }
          case 'ui/preferences/set':
            if ((params?.key || '') === 'activeThreadId') {
              state.snapshot.activeThreadId = (params?.value || '').toString();
            }
            return { ok: true };
          case 'ui/preferences/get':
            return '';
          case 'ui/dashboard/get':
            return {};
          default:
            return {};
        }
      },
    };
  }, { callApiId: CALL_API_METHOD_ID, buildInfoId: GET_BUILD_INFO_METHOD_ID, sourceId, targetId, targetAssistantText });


  await page.route('**/wails/runtime.js', async (route) => {
    await route.fulfill({ status: 200, contentType: 'application/javascript', body: RUNTIME_MODULE_SOURCE });
  });
  await page.goto('/');
  await expect(page.getByTestId('app-shell')).toBeVisible();
  await expect(page.locator('.chat-item.kind-assistant')).toHaveCount(1);

  await page.evaluate(() => {
    globalThis.__AO_E2E_BACKEND_STATE__.phase = 'switch';
  });
  await page.locator('.thread-rail-item[role="button"]').filter({ hasText: '目标线程' }).first().click();

  await expect.poll(async () => page.evaluate(() => globalThis.__AO_E2E_BACKEND_STATE__.threadMessagesCallsAfterSwitch || 0), { timeout: 10000 }).toBe(2);
  await expect(page.locator('.chat-item.kind-assistant .chat-item-body')).toContainText(targetAssistantText);

  await page.evaluate(() => globalThis.__AO_E2E_RELEASE_SWITCH_SYNC__());
  await expect.poll(async () => page.evaluate(() => globalThis.__AO_E2E_BACKEND_STATE__.uiStateDiffCallsAfterSwitch || 0), { timeout: 10000 }).toBe(1);
});
