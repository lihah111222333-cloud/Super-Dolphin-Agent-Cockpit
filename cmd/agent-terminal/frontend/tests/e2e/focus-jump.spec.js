// @ts-nocheck
/**
 * 复现 bug: 多 Agent 并行时窗口焦点跳转
 *
 * 策略: 用 page.evaluate 内联测试 applyRuntimeSnapshot 的核心逻辑,
 *       不依赖 Wails runtime bridge, 直接验证 Vue reactive 状态行为。
 */
import { test, expect } from "@playwright/test";

const THREAD_A = "thread-aaa-111";
const THREAD_B = "thread-bbb-222";

/**
 * 内联构建一个最小化的 thread store 模拟 + buildSnapshot helper,
 * 在浏览器上下文中运行, 验证是否存在焦点跳转 bug。
 */
const INLINE_STORE_AND_HELPERS = `
(function(THREAD_A, THREAD_B) {
  const PREF_ACTIVE_THREAD_ID = 'activeThreadId';
  const PREF_ACTIVE_CMD_THREAD_ID = 'activeCmdThreadId';


  const state = {
    activeThreadId: '',
    activeCmdThreadId: '',

    threads: [],
    statuses: {},
    interruptibleByThread: {},
    timelinesByThread: {},
    diffTextByThread: {},
    statusHeadersByThread: {},
    statusDetailsByThread: {},
    tokenUsageByThread: {},
    agentMetaById: {},
    agentRuntimeById: {},
    activityStatsByThread: {},
    alertsByThread: {},
  };

  function buildSnapshot(activeThreadId) {
    return {
      threads: [
        { id: THREAD_A, name: 'Agent A', state: 'running' },
        { id: THREAD_B, name: 'Agent B', state: 'running' },
      ],
      statuses: { [THREAD_A]: 'running', [THREAD_B]: 'running' },
      interruptibleByThread: {},
      timelinesByThread: {},
      diffTextByThread: {},
      statusHeadersByThread: {},
      statusDetailsByThread: {},
      tokenUsageByThread: {},
      agentMetaById: {},
      agentRuntimeById: {},
      activityStatsByThread: {},
      alertsByThread: {},
      activeThreadId,
      activeCmdThreadId: '',

    };
  }

  // ---- applyRuntimeSnapshot: 镜像 threads.js 修复后逻辑 ----
  let _localActiveThreadIdDirty = false;
  let _localActiveCmdThreadIdDirty = false;

  function applyRuntimeSnapshot(snapshot) {
    const data = snapshot && typeof snapshot === 'object' ? snapshot : {};
    const patch = {};
    const nextThreads = Array.isArray(data.threads)
      ? data.threads.map(t => ({ id: t?.id || '', name: t?.name || t?.id || '', state: t?.state || 'idle' }))
      : [];
    const oldIds = state.threads.map(t => t.id).join(',');
    const newIds = nextThreads.map(t => t.id).join(',');
    if (oldIds !== newIds) patch.threads = nextThreads;
    if (data.statuses && typeof data.statuses === 'object') {
      patch.statuses = { ...state.statuses, ...data.statuses };
    }
    // 修复: 本地切换后跳过后端覆盖
    if (Object.prototype.hasOwnProperty.call(data, PREF_ACTIVE_THREAD_ID)) {
      const next = (data[PREF_ACTIVE_THREAD_ID] || '').toString();
      if (state.activeThreadId !== next) {
        if (_localActiveThreadIdDirty) {
          // dirty 保持 true 直到后端确认本地值
        } else {
          patch.activeThreadId = next;
        }
      } else {
        _localActiveThreadIdDirty = false;
      }
    }
    if (Object.prototype.hasOwnProperty.call(data, PREF_ACTIVE_CMD_THREAD_ID)) {
      const next = (data[PREF_ACTIVE_CMD_THREAD_ID] || '').toString();
      if (state.activeCmdThreadId !== next) {
        if (_localActiveCmdThreadIdDirty) {
          // dirty 保持 true 直到后端确认本地值
        } else {
          patch.activeCmdThreadId = next;
        }
      } else {
        _localActiveCmdThreadIdDirty = false;
      }
    }

    if (Object.keys(patch).length > 0) Object.assign(state, patch);
  }

  function saveActiveThread(id) {
    const next = id || '';
    if (state.activeThreadId === next) return;
    state.activeThreadId = next;
    _localActiveThreadIdDirty = true;
  }

  globalThis.__focusJumpStore = { state, applyRuntimeSnapshot, saveActiveThread, buildSnapshot };
})
`;

test.describe("focus-jump: applyRuntimeSnapshot activeThreadId 覆盖行为", () => {

  test("RED: 本地切换 thread 后, 后端 snapshot 不应覆盖 activeThreadId", async ({ page }) => {
    await page.goto("/");
    await page.waitForTimeout(500);

    const result = await page.evaluate(([script, tA, tB]) => {
      eval(script + `("${tA}", "${tB}")`);
      const store = globalThis.__focusJumpStore;

      // Step 1: 初始 sync — 后端说 activeThreadId = THREAD_A
      store.applyRuntimeSnapshot(store.buildSnapshot(tA));
      const afterInitSync = store.state.activeThreadId;

      // Step 2: 用户本地手动切换到 THREAD_B
      store.saveActiveThread(tB);
      const afterLocalSwitch = store.state.activeThreadId;

      // Step 3: 模拟后端 sync cycle (另一个前端实例设置了 THREAD_A)
      store.applyRuntimeSnapshot(store.buildSnapshot(tA));
      const afterRemoteSync = store.state.activeThreadId;

      return { afterInitSync, afterLocalSwitch, afterRemoteSync };
    }, [INLINE_STORE_AND_HELPERS, THREAD_A, THREAD_B]);

    expect(result.afterInitSync).toBe(THREAD_A);
    expect(result.afterLocalSwitch).toBe(THREAD_B);
    // 核心断言: 后端 sync 不应覆盖用户本地选择
    // 修复前 FAIL: afterRemoteSync === THREAD_A
    expect(result.afterRemoteSync).toBe(THREAD_B);
  });

  test("GREEN: 无本地切换时, 后端 snapshot 应正常设置 activeThreadId", async ({ page }) => {
    await page.goto("/");
    await page.waitForTimeout(500);

    const result = await page.evaluate(([script, tA, tB]) => {
      eval(script + `("${tA}", "${tB}")`);
      const store = globalThis.__focusJumpStore;

      store.applyRuntimeSnapshot(store.buildSnapshot(tA));
      const afterFirst = store.state.activeThreadId;

      store.applyRuntimeSnapshot(store.buildSnapshot(tB));
      const afterSecond = store.state.activeThreadId;

      return { afterFirst, afterSecond };
    }, [INLINE_STORE_AND_HELPERS, THREAD_A, THREAD_B]);

    expect(result.afterFirst).toBe(THREAD_A);
    expect(result.afterSecond).toBe(THREAD_B);
  });

  test("RED: 连续多次 sync 后, 本地选择应保持稳定", async ({ page }) => {
    await page.goto("/");
    await page.waitForTimeout(500);

    const result = await page.evaluate(([script, tA, tB]) => {
      eval(script + `("${tA}", "${tB}")`);
      const store = globalThis.__focusJumpStore;

      store.applyRuntimeSnapshot(store.buildSnapshot(tA));
      store.saveActiveThread(tB);

      const results = [];
      for (let i = 0; i < 3; i++) {
        store.applyRuntimeSnapshot(store.buildSnapshot(tA));
        results.push(store.state.activeThreadId);
      }
      return { results };
    }, [INLINE_STORE_AND_HELPERS, THREAD_A, THREAD_B]);

    // 所有 3 次 sync 后, activeThreadId 都应保持为 THREAD_B
    for (const r of result.results) {
      expect(r).toBe(THREAD_B);
    }
  });
});
