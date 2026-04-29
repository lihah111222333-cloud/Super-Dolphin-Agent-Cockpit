// @ts-nocheck
// Phase 1 端到端测试（user-journey 风格，串多模块协同验证）
// 不替代真实 GUI 验证，但比单点 unit test 覆盖更多组件交互。
// 模拟场景 5 / 6 / 7 / 8 的完整业务流。

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { reactive, ref } from '../lib/vue.esm-browser.prod.js';

vi.mock('./services/log.js', () => ({
  logInfo: vi.fn(), logWarn: vi.fn(), logDebug: vi.fn(), logError: vi.fn(),
}));
vi.mock('./composables/useContextUsageThresholds.js', () => {
  const r = ref([70, 85, 95]);
  return {
    useContextUsageThresholds: () => r,
    loadContextUsageThresholds: vi.fn().mockResolvedValue([70, 85, 95]),
    saveContextUsageThresholds: vi.fn(),
    isValidThresholds: () => true,
    _resetContextUsageThresholdsForTest: () => { r.value = [70, 85, 95]; },
  };
});
vi.mock('./composables/useAutoContinuePref.js', () => {
  const p = ref(true);
  const ready = ref(true);
  return {
    useAutoContinuePref: () => p,
    useAutoContinuePrefReady: () => ready,
    loadAutoContinuePref: vi.fn().mockResolvedValue(true),
    saveAutoContinuePref: vi.fn().mockResolvedValue(undefined),
    isValidAutoContinuePref: () => true,
    _resetAutoContinuePrefForTest: () => { p.value = true; ready.value = true; },
    _setAutoContinuePrefForTest: (v) => { p.value = v; },
    _setAutoContinuePrefReadyForTest: (v) => { ready.value = v; },
  };
});

vi.mock('./composables/useThreadWatchdogPref.js', () => {
  const r = ref(true);
  return {
    useThreadWatchdogPref: () => r,
    useThreadWatchdogPrefReady: () => ref(true),
    loadThreadWatchdogPref: vi.fn().mockResolvedValue(true),
    saveThreadWatchdogPref: vi.fn().mockResolvedValue(undefined),
    isValidThreadWatchdogPref: () => true,
    _resetThreadWatchdogPrefForTest: () => { r.value = true; },
  };
});

const { logInfo, logWarn, logError } = await import('./services/log.js');
const prefMod = await import('./composables/useAutoContinuePref.js');
const { useAutoContinue } = await import('./composables/useAutoContinue.js');
const { ContextUsageBanner } = await import('./components/ContextUsageBanner.js');
const { AutoContinuePrefCard } = await import('./components/AutoContinuePrefCard.js');

async function flushAll(rounds = 25) {
  for (let i = 0; i < rounds; i++) {
    await Promise.resolve();
    await new Promise((r) => setTimeout(r, 0));
  }
}

function makeStore(initial = {}) {
  return {
    state: reactive({
      tokenUsageByThread: { ...(initial.tokenUsageByThread || {}) },
      statuses: { ...(initial.statuses || {}) },
      agentRuntimeById: { ...(initial.agentRuntimeById || {}) },
    }),
    compactThread: initial.compactThread || vi.fn().mockResolvedValue(undefined),
    recoverThread: initial.recoverThread || vi.fn().mockResolvedValue(undefined),
  };
}

let stopFn = () => {};
beforeEach(() => {
  vi.mocked(logInfo).mockReset();
  vi.mocked(logWarn).mockReset();
  vi.mocked(logError).mockReset();
  vi.mocked(prefMod.saveAutoContinuePref).mockReset().mockResolvedValue(undefined);
  prefMod._resetAutoContinuePrefForTest();
});
afterEach(() => { stopFn(); vi.restoreAllMocks(); });

// ──────────────────── E2E-1: 场景 5 status_error → recover 成功 user journey ────

describe('E2E · 场景 5: status_error → recover 链路', () => {
  it('full journey: idle → error → recover ok → state recovered', async () => {
    const store = makeStore({
      statuses: { t1: 'idle' },
      agentRuntimeById: { t1: { taskId: 'taskA' } },
    });
    const r = useAutoContinue({
      threadStore: store,
      continueTaskById: vi.fn().mockResolvedValue('should-not-be-called'),
      alertFn: vi.fn(),
      sleepFn: vi.fn().mockResolvedValue(undefined),
    });
    stopFn = r.stop;

    // user journey: thread 进入 error
    store.state.statuses = { t1: 'error' };
    await flushAll();

    // 调度器调 recover，状态在后端被 patch 回 idle
    store.state.statuses = { t1: 'idle' };
    await flushAll();

    expect(store.recoverThread).toHaveBeenCalledWith('t1');
    expect(logInfo).toHaveBeenCalledWith('ui', 'auto_continue.recover.done',
      expect.objectContaining({ source_thread_id: 't1', task_id: 'taskA' }));
    expect(r.failedAutoContinueByThread.value.has('t1')).toBe(false);
  });
});

// ──────────────────── E2E-2: 场景 6 fork 失败 → 用户一键重试 → 成功 ────────────

describe('E2E · 场景 6: fork 双失败 → user retry → success', () => {
  it('failedMap is shown and cleared after manual retry', async () => {
    const store = makeStore({
      tokenUsageByThread: { t1: { usedPercent: 50 } },
      agentRuntimeById: { t1: { taskId: 'taskA' } },
    });
    let attempt = 0;
    const cont = vi.fn(() => {
      attempt += 1;
      // 自动调度阶段 fork 双失败；user retry 阶段成功
      if (attempt <= 2) return Promise.reject(new Error('rpc dead'));
      return Promise.resolve('user-retry-thread-id');
    });
    const r = useAutoContinue({
      threadStore: store, continueTaskById: cont,
      alertFn: vi.fn(), sleepFn: vi.fn().mockResolvedValue(undefined),
    });
    stopFn = r.stop;

    // step 1: 跨入 critical → 调度器尝试 fork 2 次都失败 → failedMap 记录
    store.state.tokenUsageByThread = { t1: { usedPercent: 99 } };
    await flushAll();
    expect(cont).toHaveBeenCalledTimes(2); // first + retry
    const failed = r.failedAutoContinueByThread.value.get('t1');
    expect(failed).toBeTruthy();
    expect(failed.error_message).toBe('rpc dead');

    // step 2: 模拟用户在 ContextUsageBanner 看到失败行 + 点击 "一键重试"
    // 验 banner 渲染时 failedInfo prop 能正确显示 reason
    const bannerSetup = ContextUsageBanner.setup(
      { ...Object.fromEntries(Object.entries(ContextUsageBanner.props).map(([k, d]) => [k, d.default])),
        failedInfo: failed },
      { emit: vi.fn() },
    );
    expect(bannerSetup.showFailedSection()).toBe(true);
    expect(bannerSetup.failedReasonLabel()).toBe('自动续接失败');

    // step 3: 用户点击 retry → 调 retryAutoContinue
    const newId = await r.retryAutoContinue('t1');
    expect(newId).toBe('user-retry-thread-id');
    expect(r.failedAutoContinueByThread.value.has('t1')).toBe(false);
  });
});

// ──────────────────── E2E-3: 场景 7 偏好开关 完整切换 journey ──────────────────

describe('E2E · 场景 7: pref toggle journey', () => {
  it('off → critical fires nothing → on → critical fires fork', async () => {
    const store = makeStore({
      tokenUsageByThread: { t1: { usedPercent: 50 } },
      agentRuntimeById: { t1: { taskId: 'taskA' } },
    });
    const cont = vi.fn().mockResolvedValue('next-id');
    const r = useAutoContinue({
      threadStore: store, continueTaskById: cont,
      alertFn: vi.fn(), sleepFn: vi.fn().mockResolvedValue(undefined),
    });
    stopFn = r.stop;

    // step 1: 模拟用户在 DagsPage 关闭 pref
    prefMod._setAutoContinuePrefForTest(false);

    // step 2: thread 跨入 critical → 不触发任何动作
    store.state.tokenUsageByThread = { t1: { usedPercent: 99 } };
    await flushAll();
    expect(cont).not.toHaveBeenCalled();
    const signalLogs = vi.mocked(logInfo).mock.calls.filter((args) => args[1] === 'auto_continue.signal');
    expect(signalLogs).toHaveLength(0);

    // step 3: 用户重新打开 pref（模拟 AutoContinuePrefCard.onToggle）
    const cardSetup = AutoContinuePrefCard.setup();
    await cardSetup.onToggle({ target: { checked: true } });
    expect(prefMod.saveAutoContinuePref).toHaveBeenCalledWith(true);
    prefMod._setAutoContinuePrefForTest(true); // 模拟 saveAutoContinuePref 同步更新 ref

    // step 4: thread 跌出再跨入 critical → 触发 fork
    store.state.tokenUsageByThread = { t1: { usedPercent: 70 } };
    await flushAll();
    store.state.tokenUsageByThread = { t1: { usedPercent: 99 } };
    await flushAll();
    expect(cont).toHaveBeenCalledTimes(1);
  });
});

// ──────────────────── E2E-4: 场景 8 保险丝 + 后续 retry 不被挡 ────────────────

describe('E2E · 场景 8: global fuse 触发后用户 retry 仍可工作', () => {
  it('after fuse blown, user retryAutoContinue bypasses gating', async () => {
    const runtimes = {}; const usage = {};
    for (let i = 0; i < 21; i++) {
      runtimes['t' + i] = { taskId: 'task' + i };
      usage['t' + i] = { usedPercent: 50 };
    }
    const store = makeStore({ tokenUsageByThread: usage, agentRuntimeById: runtimes });
    const cont = vi.fn().mockResolvedValue('forked-id');
    const alertFn = vi.fn();
    const r = useAutoContinue({
      threadStore: store, continueTaskById: cont,
      alertFn, sleepFn: vi.fn().mockResolvedValue(undefined),
    });
    stopFn = r.stop;

    // step 1: 21 个 thread 同时跨入 critical → 第 21 个触发保险丝
    const next = {};
    for (let i = 0; i < 21; i++) next['t' + i] = { usedPercent: 99 };
    store.state.tokenUsageByThread = next;
    await flushAll();
    expect(alertFn).toHaveBeenCalledTimes(1);
    expect(logError).toHaveBeenCalledWith('ui', 'auto_continue.fuse_blown', { reason: 'global_fuse_blown' });

    // step 2: 即使 fuse blown，用户主动 retryAutoContinue 仍能工作（绕过 gate）
    const retriedId = await r.retryAutoContinue('t-newcomer');
    expect(retriedId).toBe('forked-id');
    expect(cont).toHaveBeenCalledWith('t-newcomer');

    // step 3: 二次保险丝触发不重弹 alert（一次性）
    const more = {};
    for (let i = 21; i < 25; i++) more['t' + i] = { usedPercent: 99 };
    store.state.tokenUsageByThread = { ...next, ...more };
    await flushAll();
    expect(alertFn).toHaveBeenCalledTimes(1); // 仍只 1 次
  });
});
