// @ts-nocheck
import { test, expect } from '@playwright/test';

import {
  CALL_API_METHOD_ID,
  GET_BUILD_INFO_METHOD_ID,
  RUNTIME_MODULE_SOURCE,
} from './support/mock-backend.js';

async function installBlankPage(page, pathname) {
  await page.route(`**${pathname}`, async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'text/html; charset=utf-8',
      body: '<!doctype html><html lang="zh-CN"><body></body></html>',
    });
  });
  await page.goto(pathname);
}

test.describe('memory leak regression', () => {
  test('unsubscribe before delayed runtime registration does not leave dangling listeners', async ({ page }) => {
    await page.route('**/wails/runtime.js', async (route) => {
      await new Promise((resolve) => setTimeout(resolve, 250));
      await route.fulfill({
        status: 200,
        contentType: 'application/javascript',
        body: RUNTIME_MODULE_SOURCE,
      });
    });

    await installBlankPage(page, '/__e2e_runtime_leak_probe__');

    await page.evaluate(async () => {
      const { onBridgeEvent, onFilesDropped, onAppWillQuit } = await import('/vue-app/services/api.js');
      const offBridge = onBridgeEvent(() => {});
      const offFiles = onFilesDropped(() => {});
      const offQuit = onAppWillQuit(() => {});
      offBridge();
      offFiles();
      offQuit();
    });

    await expect.poll(async () => {
      return page.evaluate(() => Boolean(globalThis.__AO_E2E_RUNTIME_LISTENERS__));
    }, { timeout: 5_000 }).toBe(true);

    await expect.poll(async () => {
      return page.evaluate(() => {
        const listeners = globalThis.__AO_E2E_RUNTIME_LISTENERS__;
        return {
          bridge: listeners?.get('bridge-event')?.size || 0,
          files: listeners?.get('files-dropped')?.size || 0,
          quit: listeners?.get('app-will-quit')?.size || 0,
        };
      });
    }, { timeout: 5_000 }).toEqual({
      bridge: 0,
      files: 0,
      quit: 0,
    });
  });

  test('thread payload cache evicts old timeline and diff entries after repeated thread loads', async ({ page }) => {
    const threadIDs = Array.from({ length: 9 }, (_, index) => `thread-leak-${index + 1}`);

    await page.addInitScript(({ callApiId, buildInfoId, threadIDs }) => {
      const clone = (value) => JSON.parse(JSON.stringify(value));
      const makeLargeText = (label) => `${label}:` + 'x'.repeat(16 * 1024);
      const sharedThreads = threadIDs.map((id) => ({
        id,
        name: id,
        state: 'idle',
        provider: 'codex',
      }));

      function buildThreadPayload(threadId, includeDiff) {
        return {
          threads: clone(sharedThreads),
          statuses: Object.fromEntries(threadIDs.map((id) => [id, 'idle'])),
          interruptibleByThread: {},
          timelinesByThread: {
            [threadId]: [{
              id: `assistant-${threadId}`,
              kind: 'assistant',
              text: makeLargeText(`timeline-${threadId}`),
              ts: '2026-03-21T00:00:00.000Z',
            }],
          },
          diffTextByThread: includeDiff ? {
            [threadId]: makeLargeText(`diff-${threadId}`),
          } : {},
          diffRevisionByThread: {
            [threadId]: 1,
          },
          statusHeadersByThread: {
            [threadId]: 'ready',
          },
          statusDetailsByThread: {
            [threadId]: makeLargeText(`status-${threadId}`),
          },
          tokenUsageByThread: {},
          agentMetaById: {
            [threadId]: { alias: threadId },
          },
          agentRuntimeById: {
            [threadId]: { provider: 'codex', providerThreadId: `provider-${threadId}` },
          },
          activityStatsByThread: {
            [threadId]: { count: 1 },
          },
          alertsByThread: {
            [threadId]: { items: [{ severity: 'info', title: threadId }] },
          },
          activeThreadId: threadId,
          activeCmdThreadId: '',

          'threadPins.chat': {},
          'threadArchives.chat': {},
          'viewPrefs.chat': { layout: 'split', splitRatio: 60, threadRailWidth: 232 },
          'viewPrefs.cmd': { layout: 'mix', splitRatio: 60, cardCols: 3 },
        };
      }

      globalThis.__AO_E2E_BACKEND_STATE__ = { callLog: [] };
      globalThis.__AO_E2E_BACKEND__ = {
        async byId(methodId, ...args) {
          if (methodId === buildInfoId) {
            return { version: 'e2e-test', commit: 'local' };
          }
          if (methodId !== callApiId) {
            return null;
          }
          const [method, params = {}] = args;
          globalThis.__AO_E2E_BACKEND_STATE__.callLog.push({ method, params: clone(params) });

          switch (method) {
            case 'ui/preferences/get':
              return '';
            case 'ui/state/get': {
              const threadId = (params?.threadId || threadIDs[0] || '').toString().trim() || threadIDs[0];
              return buildThreadPayload(threadId, params?.includeDiff === true);
            }
            default:
              return {};
          }
        },
      };
    }, {
      callApiId: CALL_API_METHOD_ID,
      buildInfoId: GET_BUILD_INFO_METHOD_ID,
      threadIDs,
    });

    await page.route('**/wails/runtime.js', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/javascript',
        body: RUNTIME_MODULE_SOURCE,
      });
    });

    await installBlankPage(page, '/__e2e_thread_payload_probe__');

    const cacheState = await page.evaluate(async (threadIDs) => {
      const { useThreadStore } = await import('/vue-app/stores/threads.js');
      const store = useThreadStore();
      store.setPreferenceScopeCwd('');

      for (const threadId of threadIDs) {
        store.state.activeThreadId = threadId;
        await store.syncThreadState(threadId);
        await store.syncThreadDiffState(threadId, { force: true });
      }

      return {
        timelineKeys: Object.keys(store.state.timelinesByThread).sort(),
        diffKeys: Object.keys(store.state.diffTextByThread).sort(),
        detailKeys: Object.keys(store.state.statusDetailsByThread).sort(),
        runtimeKeys: Object.keys(store.state.agentRuntimeById).sort(),
      };
    }, threadIDs);

    const expectedRetained = threadIDs.slice(-6);
    expect(cacheState.timelineKeys).toEqual(expectedRetained);
    expect(cacheState.diffKeys).toEqual(expectedRetained);
    expect(cacheState.detailKeys).toEqual(expectedRetained);
    expect(cacheState.runtimeKeys).toEqual(expectedRetained);
  });
});
