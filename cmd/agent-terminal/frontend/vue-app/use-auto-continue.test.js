// @ts-nocheck
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { reactive, nextTick, ref } from '../lib/vue.esm-browser.prod.js';

// fork 重试链（compact → sleep → healthcheck → fork → record）需多个 microtask。
async function flushAll(rounds = 25) {
  for (let i = 0; i < rounds; i++) {
    await Promise.resolve();
    await nextTick();
  }
}

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
  const ready = ref(true); // R6 fix：测试默认 pref 已 ready，避免所有 case 被跳过
  return {
    useAutoContinuePref: () => p,
    useAutoContinuePrefReady: () => ready,
    loadAutoContinuePref: vi.fn().mockResolvedValue(true),
    saveAutoContinuePref: vi.fn(),
    isValidAutoContinuePref: () => true,
    _resetAutoContinuePrefForTest: () => { p.value = true; ready.value = true; },
    _setAutoContinuePrefForTest: (v) => { p.value = v; },
    _setAutoContinuePrefReadyForTest: (v) => { ready.value = v; },
  };
});

const { logInfo, logWarn, logError } = await import('./services/log.js');
const prefMod = await import('./composables/useAutoContinuePref.js');
const { useAutoContinue } = await import('./composables/useAutoContinue.js');

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
let alertFn;
let sleepFn;
let continueTaskById;

function start(store, overrides = {}) {
  alertFn = overrides.alertFn || vi.fn();
  sleepFn = overrides.sleepFn || vi.fn().mockResolvedValue(undefined);
  continueTaskById = overrides.continueTaskById || vi.fn().mockResolvedValue('new-thread-id');
  const r = useAutoContinue({
    threadStore: store, continueTaskById, alertFn, sleepFn,
  });
  stopFn = r.stop;
  return r;
}

beforeEach(() => {
  vi.mocked(logInfo).mockReset();
  vi.mocked(logWarn).mockReset();
  vi.mocked(logError).mockReset();
  prefMod._resetAutoContinuePrefForTest(); // R3 fix：avoid case-to-case pref leakage
});
afterEach(() => { stopFn(); vi.restoreAllMocks(); });

// ─────────────────────────────────────── signal 埋点（沿用 1.3 行为） ────────────

describe('useAutoContinue · token signals', () => {
  it('emits signal + does NOT touch non-task thread', async () => {
    const store = makeStore({
      tokenUsageByThread: { t1: { usedPercent: 50 } },
      agentRuntimeById: { t1: {} },
    });
    start(store);
    store.state.tokenUsageByThread = { t1: { usedPercent: 99 } };
    await nextTick();
    expect(continueTaskById).not.toHaveBeenCalled();
    expect(store.compactThread).not.toHaveBeenCalled();
  });

  it('does NOT re-fire when already critical', async () => {
    const store = makeStore({
      tokenUsageByThread: { t1: { usedPercent: 96 } },
      agentRuntimeById: { t1: { taskId: 'A' } },
    });
    start(store);
    store.state.tokenUsageByThread = { t1: { usedPercent: 98 } };
    await nextTick();
    expect(continueTaskById).not.toHaveBeenCalled();
  });
});

// ─────────────────────────────────────── token_critical decision ────────────────

describe('useAutoContinue · token_critical decision', () => {
  it('canCompact + compact success → no fork', async () => {
    const store = makeStore({
      tokenUsageByThread: { t1: { usedPercent: 50 } },
      agentRuntimeById: { t1: { taskId: 'A', capabilities: ['context_compact'] } },
    });
    const { failedAutoContinueByThread } = start(store);
    store.state.tokenUsageByThread = { t1: { usedPercent: 99 } };
    await nextTick();
    await nextTick();
    expect(store.compactThread).toHaveBeenCalledWith('t1');
    expect(continueTaskById).not.toHaveBeenCalled();
    expect(failedAutoContinueByThread.value.has('t1')).toBe(false);
  });

  it('not canCompact → directly fork', async () => {
    const store = makeStore({
      tokenUsageByThread: { t1: { usedPercent: 50 } },
      agentRuntimeById: { t1: { taskId: 'A', capabilities: [] } },
    });
    start(store);
    store.state.tokenUsageByThread = { t1: { usedPercent: 99 } };
    await flushAll();
    expect(store.compactThread).not.toHaveBeenCalled();
    expect(continueTaskById).toHaveBeenCalledWith('t1');
  });

  it('compact fails + still critical → fork tries', async () => {
    const store = makeStore({
      tokenUsageByThread: { t1: { usedPercent: 50 } },
      agentRuntimeById: { t1: { taskId: 'A', capabilities: ['context_compact'] } },
      compactThread: vi.fn().mockRejectedValue(new Error('compact rpc dead')),
    });
    start(store);
    store.state.tokenUsageByThread = { t1: { usedPercent: 99 } };
    await flushAll();
    expect(store.compactThread).toHaveBeenCalled();
    expect(continueTaskById).toHaveBeenCalledWith('t1');
  });

  it('compact fails + thread已自然恢复 → skip fork', async () => {
    let resolveCompact;
    const compactPromise = new Promise((_, reject) => { resolveCompact = reject; });
    const store = makeStore({
      tokenUsageByThread: { t1: { usedPercent: 50 } },
      agentRuntimeById: { t1: { taskId: 'A', capabilities: ['context_compact'] } },
      compactThread: vi.fn(() => compactPromise),
    });
    start(store);
    store.state.tokenUsageByThread = { t1: { usedPercent: 99 } };
    await nextTick();
    // 在 compact 还在 pending 时手动让 thread 跌回 warn
    store.state.tokenUsageByThread = { t1: { usedPercent: 75 } };
    resolveCompact(new Error('boom'));
    await flushAll();
    expect(continueTaskById).not.toHaveBeenCalled();
  });
});

// ─────────────────────────────────────── fork retry & healthcheck ───────────────

describe('useAutoContinue · fork retry / healthcheck', () => {
  it('fork first fails + retry succeeds → no failure recorded', async () => {
    const store = makeStore({
      tokenUsageByThread: { t1: { usedPercent: 50 } },
      agentRuntimeById: { t1: { taskId: 'A' } },
    });
    let calls = 0;
    const cont = vi.fn(() => {
      calls += 1;
      return calls === 1 ? Promise.reject(new Error('flap')) : Promise.resolve('next-id');
    });
    const { failedAutoContinueByThread } = start(store, { continueTaskById: cont });
    store.state.tokenUsageByThread = { t1: { usedPercent: 99 } };
    await flushAll();
    expect(cont).toHaveBeenCalledTimes(2);
    expect(failedAutoContinueByThread.value.has('t1')).toBe(false);
    expect(sleepFn).toHaveBeenCalledWith(1500);
  });

  it('fork first fails + healthcheck recovered before retry → skip retry', async () => {
    const store = makeStore({
      tokenUsageByThread: { t1: { usedPercent: 50 } },
      agentRuntimeById: { t1: { taskId: 'A' } },
    });
    const cont = vi.fn().mockRejectedValueOnce(new Error('first'));
    const sleep = vi.fn(async () => {
      // 在 sleep 期间外部恢复
      store.state.tokenUsageByThread = { t1: { usedPercent: 60 } };
    });
    start(store, { continueTaskById: cont, sleepFn: sleep });
    store.state.tokenUsageByThread = { t1: { usedPercent: 99 } };
    await flushAll();
    expect(cont).toHaveBeenCalledTimes(1); // 只一次，retry 被 healthcheck 跳过
  });

  it('fork double-fail → failedMap recorded with continue_failed', async () => {
    const store = makeStore({
      tokenUsageByThread: { t1: { usedPercent: 50 } },
      agentRuntimeById: { t1: { taskId: 'A' } },
    });
    const cont = vi.fn().mockRejectedValue(new Error('always dead'));
    const { failedAutoContinueByThread } = start(store, { continueTaskById: cont });
    store.state.tokenUsageByThread = { t1: { usedPercent: 99 } };
    await flushAll();
    expect(cont).toHaveBeenCalledTimes(2);
    const failed = failedAutoContinueByThread.value.get('t1');
    expect(failed).toBeTruthy();
    expect(failed.kind).toBe('token_critical');
    expect(failed.reason).toBe('continue_failed');
    expect(failed.error_message).toBe('always dead');
  });
});

// ─────────────────────────────────────── per-thread gate & fuse ─────────────────

describe('useAutoContinue · gating', () => {
  it('per-thread 1 次闸：second token_critical on same thread is gated', async () => {
    const store = makeStore({
      tokenUsageByThread: { t1: { usedPercent: 50 } },
      agentRuntimeById: { t1: { taskId: 'A' } },
    });
    const { failedAutoContinueByThread } = start(store);
    // 第一次跨入 critical
    store.state.tokenUsageByThread = { t1: { usedPercent: 99 } };
    await flushAll();
    expect(continueTaskById).toHaveBeenCalledTimes(1);
    // 跌回 warn 再跨入 → 应该被 per-thread 闸挡
    store.state.tokenUsageByThread = { t1: { usedPercent: 70 } };
    await nextTick();
    store.state.tokenUsageByThread = { t1: { usedPercent: 99 } };
    await flushAll();
    expect(continueTaskById).toHaveBeenCalledTimes(1); // 没增加
    const failed = failedAutoContinueByThread.value.get('t1');
    expect(failed.reason).toBe('gated_thread_already_continued');
  });

  it('global fuse: 第 21 次触发 alert + logError', async () => {
    const runtimes = {};
    const usage = {};
    for (let i = 0; i < 21; i++) {
      runtimes['t' + i] = { taskId: 'task-' + i };
      usage['t' + i] = { usedPercent: 50 };
    }
    const store = makeStore({ tokenUsageByThread: usage, agentRuntimeById: runtimes });
    const a = vi.fn();
    start(store, { alertFn: a });
    const next = {};
    for (let i = 0; i < 21; i++) next['t' + i] = { usedPercent: 99 };
    store.state.tokenUsageByThread = next;
    await flushAll();
    expect(a).toHaveBeenCalledTimes(1);
    expect(logError).toHaveBeenCalledWith('ui', 'auto_continue.fuse_blown', { reason: 'global_fuse_blown' });
  });

  it('global fuse alert is fired once even if more signals come', async () => {
    const runtimes = {}; const usage = {};
    for (let i = 0; i < 22; i++) {
      runtimes['t' + i] = { taskId: 'task-' + i };
      usage['t' + i] = { usedPercent: 50 };
    }
    const store = makeStore({ tokenUsageByThread: usage, agentRuntimeById: runtimes });
    const a = vi.fn();
    start(store, { alertFn: a });
    const next = {};
    for (let i = 0; i < 22; i++) next['t' + i] = { usedPercent: 99 };
    store.state.tokenUsageByThread = next;
    await flushAll();
    expect(a).toHaveBeenCalledTimes(1);
  });
});

// ─────────────────────────────────────── status_error decision ──────────────────

describe('useAutoContinue · status_error decision', () => {
  it('recover success → no fork', async () => {
    const store = makeStore({
      statuses: { t1: 'idle' },
      agentRuntimeById: { t1: { taskId: 'A' } },
    });
    const { failedAutoContinueByThread } = start(store);
    store.state.statuses = { t1: 'error' };
    await flushAll();
    expect(store.recoverThread).toHaveBeenCalledWith('t1');
    expect(continueTaskById).not.toHaveBeenCalled();
    expect(failedAutoContinueByThread.value.has('t1')).toBe(false);
  });

  it('recover fails + still error → fork', async () => {
    const store = makeStore({
      statuses: { t1: 'idle' },
      agentRuntimeById: { t1: { taskId: 'A' } },
      recoverThread: vi.fn().mockRejectedValue(new Error('recover dead')),
    });
    start(store);
    store.state.statuses = { t1: 'error' };
    await flushAll();
    expect(store.recoverThread).toHaveBeenCalled();
    expect(continueTaskById).toHaveBeenCalledWith('t1');
  });

  it('recover fails + thread 自然恢复 → skip fork', async () => {
    let rejectRecover;
    const p = new Promise((_, reject) => { rejectRecover = reject; });
    const store = makeStore({
      statuses: { t1: 'idle' },
      agentRuntimeById: { t1: { taskId: 'A' } },
      recoverThread: vi.fn(() => p),
    });
    start(store);
    store.state.statuses = { t1: 'error' };
    await nextTick();
    store.state.statuses = { t1: 'idle' };
    rejectRecover(new Error('boom'));
    await flushAll();
    expect(continueTaskById).not.toHaveBeenCalled();
  });
});

// ─────────────────────────────────────── retryAutoContinue ──────────────────────

describe('useAutoContinue · retryAutoContinue', () => {
  it('clears failedMap when continueTaskById succeeds', async () => {
    const store = makeStore({
      tokenUsageByThread: { t1: { usedPercent: 50 } },
      agentRuntimeById: { t1: { taskId: 'A' } },
    });
    const cont = vi.fn().mockRejectedValueOnce(new Error('a')).mockRejectedValueOnce(new Error('b')).mockResolvedValueOnce('retried-id');
    const { failedAutoContinueByThread, retryAutoContinue } = start(store, { continueTaskById: cont });
    store.state.tokenUsageByThread = { t1: { usedPercent: 99 } };
    await flushAll();
    expect(failedAutoContinueByThread.value.has('t1')).toBe(true);
    const id = await retryAutoContinue('t1');
    expect(id).toBe('retried-id');
    expect(failedAutoContinueByThread.value.has('t1')).toBe(false);
  });

  it('rethrows when continueTaskById throws', async () => {
    const store = makeStore({});
    const cont = vi.fn().mockRejectedValue(new Error('explicit retry fail'));
    const { retryAutoContinue } = start(store, { continueTaskById: cont });
    await expect(retryAutoContinue('t-x')).rejects.toThrow('explicit retry fail');
  });
});

// ─────────────────────────────────────── inflight 并发保护 ──────────────────────

// Phase 1.5·偏好开关
describe('useAutoContinue · pref gating', () => {
  it('does NOT emit signal nor call action when pref=false', async () => {
    prefMod._setAutoContinuePrefForTest(false);
    const store = makeStore({
      tokenUsageByThread: { t1: { usedPercent: 50 } },
      agentRuntimeById: { t1: { taskId: 'A' } },
    });
    start(store);
    store.state.tokenUsageByThread = { t1: { usedPercent: 99 } };
    await flushAll();
    expect(continueTaskById).not.toHaveBeenCalled();
    expect(store.compactThread).not.toHaveBeenCalled();
    // signal 也不应 emit（避免“看似在做事”的日志）
    const signalCalls = vi.mocked(logInfo).mock.calls.filter((args) => args[1] === 'auto_continue.signal');
    expect(signalCalls).toHaveLength(0);
    prefMod._setAutoContinuePrefForTest(true); // 恢复
  });

  it('does NOT trigger on status_error when pref=false', async () => {
    prefMod._setAutoContinuePrefForTest(false);
    const store = makeStore({
      statuses: { t1: 'idle' },
      agentRuntimeById: { t1: { taskId: 'A' } },
    });
    start(store);
    store.state.statuses = { t1: 'error' };
    await flushAll();
    expect(store.recoverThread).not.toHaveBeenCalled();
    expect(continueTaskById).not.toHaveBeenCalled();
    prefMod._setAutoContinuePrefForTest(true);
  });

  it('toggling pref false → true makes next signal trigger normally', async () => {
    prefMod._setAutoContinuePrefForTest(false);
    const store = makeStore({
      tokenUsageByThread: { t1: { usedPercent: 50 } },
      agentRuntimeById: { t1: { taskId: 'A' } },
    });
    start(store);
    // pref off：跨入 critical 不触发
    store.state.tokenUsageByThread = { t1: { usedPercent: 99 } };
    await flushAll();
    expect(continueTaskById).not.toHaveBeenCalled();
    // pref on：跳出再跳入 → 触发
    prefMod._setAutoContinuePrefForTest(true);
    store.state.tokenUsageByThread = { t1: { usedPercent: 70 } };
    await flushAll();
    store.state.tokenUsageByThread = { t1: { usedPercent: 99 } };
    await flushAll();
    expect(continueTaskById).toHaveBeenCalledTimes(1);
  });

  it('retryAutoContinue is NOT gated by pref (用户主动入口不受偏好控制)', async () => {
    prefMod._setAutoContinuePrefForTest(false);
    const store = makeStore({});
    const cont = vi.fn().mockResolvedValue('retried-ok');
    const { retryAutoContinue } = start(store, { continueTaskById: cont });
    const id = await retryAutoContinue('t1');
    expect(id).toBe('retried-ok');
    expect(cont).toHaveBeenCalledWith('t1');
    prefMod._setAutoContinuePrefForTest(true);
  });
});

// R6 fix：pref 未 load 完不触发
describe('useAutoContinue · R6 pref-ready gating', () => {
  it('does NOT trigger when prefReady=false (avoids default-true misfire)', async () => {
    prefMod._setAutoContinuePrefReadyForTest(false);
    const store = makeStore({
      tokenUsageByThread: { t1: { usedPercent: 50 } },
      agentRuntimeById: { t1: { taskId: 'A' } },
    });
    start(store);
    store.state.tokenUsageByThread = { t1: { usedPercent: 99 } };
    await flushAll();
    expect(continueTaskById).not.toHaveBeenCalled();
    // ready 之后，跳出再跳入 → 能触发
    prefMod._setAutoContinuePrefReadyForTest(true);
    store.state.tokenUsageByThread = { t1: { usedPercent: 70 } };
    await flushAll();
    store.state.tokenUsageByThread = { t1: { usedPercent: 99 } };
    await flushAll();
    expect(continueTaskById).toHaveBeenCalledTimes(1);
  });
});

// R7 fix：孤儿清理
describe('useAutoContinue · R7 orphan cleanup', () => {
  it('removes prevLevelByThread entries when thread disappears from store', async () => {
    const store = makeStore({
      tokenUsageByThread: { t1: { usedPercent: 50 }, t2: { usedPercent: 50 } },
      agentRuntimeById: { t1: { taskId: 'A' }, t2: { taskId: 'B' } },
    });
    const r = start(store);
    // 移除 t2 后，watch 回调应清理
    store.state.tokenUsageByThread = { t1: { usedPercent: 60 } };
    await flushAll();
    // 看 ctx 内部 Map（只能间接验证：同名 t2 重新进入 critical 应被当“初次跳入”​​触发，
    // 而不是被 “之前 prev 为 normal” 路径获识）
    store.state.tokenUsageByThread = { t1: { usedPercent: 60 }, t2: { usedPercent: 99 } };
    await flushAll();
    expect(continueTaskById).toHaveBeenCalledWith('t2');
  });
});

// R8 fix：classifyError 提取 error.code
describe('useAutoContinue · R8 classifyError code extraction', () => {
  it('records error_code along with error_message', async () => {
    const store = makeStore({
      tokenUsageByThread: { t1: { usedPercent: 50 } },
      agentRuntimeById: { t1: { taskId: 'A' } },
    });
    const err = Object.assign(new Error('rpc shutdown'), { code: 'E_SHUTDOWN' });
    const cont = vi.fn().mockRejectedValue(err);
    const { failedAutoContinueByThread } = start(store, { continueTaskById: cont });
    store.state.tokenUsageByThread = { t1: { usedPercent: 99 } };
    await flushAll();
    const failed = failedAutoContinueByThread.value.get('t1');
    expect(failed.error_message).toBe('rpc shutdown');
    expect(failed.error_code).toBe('E_SHUTDOWN');
  });
});

// R1 fix：自然治愈路径下 per-thread 闸回滚
describe('useAutoContinue · R1 release on natural recovery', () => {
  it('after recovered=true, same thread can trigger again later', async () => {
    const store = makeStore({
      tokenUsageByThread: { t1: { usedPercent: 50 } },
      agentRuntimeById: { t1: { taskId: 'A' } },
    });
    // 第一次 fork 失败 + sleep 期间恢复 → recovered=true
    let firstAttempt = true;
    const cont = vi.fn(() => {
      if (firstAttempt) { firstAttempt = false; return Promise.reject(new Error('first fail')); }
      return Promise.resolve('next-id');
    });
    const sleep = vi.fn(async () => { store.state.tokenUsageByThread = { t1: { usedPercent: 60 } }; });
    start(store, { continueTaskById: cont, sleepFn: sleep });
    store.state.tokenUsageByThread = { t1: { usedPercent: 99 } };
    await flushAll();
    // recovered，闸门应该 release 了 —— 下次 critical 应能重新触发
    store.state.tokenUsageByThread = { t1: { usedPercent: 70 } };
    await flushAll();
    store.state.tokenUsageByThread = { t1: { usedPercent: 99 } };
    await flushAll();
    expect(cont).toHaveBeenCalledTimes(2); // 首次失败 + recovery 后第二次重新触发
  });
});

// R4 fix：status_error 路径上的全局保险丝
describe('useAutoContinue · R4 global fuse on status_error', () => {
  it('blows fuse + alerts when many threads turn error simultaneously', async () => {
    const runtimes = {}; const statuses = {};
    for (let i = 0; i < 21; i++) {
      runtimes['t' + i] = { taskId: 'task-' + i };
      statuses['t' + i] = 'idle';
    }
    const store = makeStore({ statuses, agentRuntimeById: runtimes });
    const a = vi.fn();
    start(store, { alertFn: a });
    const next = {};
    for (let i = 0; i < 21; i++) next['t' + i] = 'error';
    store.state.statuses = next;
    await flushAll();
    // 第 21 个 thread 的 recover gate 应该 fuseBlown → 触发 alert
    expect(a).toHaveBeenCalledTimes(1);
    expect(logError).toHaveBeenCalledWith('ui', 'auto_continue.fuse_blown', { reason: 'global_fuse_blown' });
  });
});


describe('useAutoContinue · inflight protection', () => {
  it('skips concurrent invocation on same thread', async () => {
    let releaseFirst;
    const cont = vi.fn(() => new Promise((resolve) => { releaseFirst = resolve; }));
    const store = makeStore({
      tokenUsageByThread: { t1: { usedPercent: 50 } },
      agentRuntimeById: { t1: { taskId: 'A' } },
    });
    start(store, { continueTaskById: cont });
    // 第一次 critical
    store.state.tokenUsageByThread = { t1: { usedPercent: 99 } };
    await nextTick();
    // 还在 inflight 时再跨入（先跌出再进）
    store.state.tokenUsageByThread = { t1: { usedPercent: 70 } };
    await nextTick();
    store.state.tokenUsageByThread = { t1: { usedPercent: 99 } };
    await nextTick();
    expect(cont).toHaveBeenCalledTimes(1); // inflight 阻挡
    releaseFirst('done');
  });
});

describe('useAutoContinue · Phase 1.8a manualAbort 抑制位', () => {
  it('markManualAbort 后 status="error" 时不调 recoverThread / continueTaskById', async () => {
    const recoverThread = vi.fn().mockResolvedValue(undefined);
    const store = makeStore({
      statuses: { t1: 'idle' },
      agentRuntimeById: { t1: { taskId: 'T1' } },
      recoverThread,
    });
    const r = start(store);
    r.markManualAbort('t1');
    store.state.statuses = { t1: 'error' };
    await nextTick();
    expect(recoverThread).not.toHaveBeenCalled();
    expect(continueTaskById).not.toHaveBeenCalled();
  });

  it('clearManualAbort 后下次 status=error 恢复正常 F2', async () => {
    const recoverThread = vi.fn().mockResolvedValue(undefined);
    const store = makeStore({
      statuses: { t1: 'idle' },
      agentRuntimeById: { t1: { taskId: 'T1' } },
      recoverThread,
    });
    const r = start(store);
    r.markManualAbort('t1');
    r.clearManualAbort('t1');
    store.state.statuses = { t1: 'error' };
    await nextTick();
    expect(recoverThread).toHaveBeenCalled();
  });

  it('userRetry 自动清抑制位（用户主动 = 已知情同意续接）', async () => {
    const recoverThread = vi.fn().mockResolvedValue(undefined);
    const store = makeStore({
      statuses: { t1: 'idle' },
      agentRuntimeById: { t1: { taskId: 'T1' } },
      recoverThread,
    });
    const r = start(store);
    r.markManualAbort('t1');
    // mock failedRef 让 userRetry 有 entry
    r.failedAutoContinueByThread.value.set('t1', { kind: 'status_error' });
    await r.retryAutoContinue('t1').catch(() => {});
    // 之后 status=error 应触发 F2
    store.state.statuses = { t1: 'error' };
    await nextTick();
    expect(recoverThread).toHaveBeenCalled();
  });

  it('markManualAbort 空字符串 / 空 threadId 是 no-op', () => {
    const store = makeStore({});
    const r = start(store);
    expect(() => r.markManualAbort('')).not.toThrow();
    expect(() => r.markManualAbort(null)).not.toThrow();
    expect(() => r.clearManualAbort('')).not.toThrow();
  });
});

describe("useAutoContinue · Phase 1.8b 永久错误识别", () => {
  it("permanent_unauthenticated 时 fork 不重试（sleepFn 不调）", async () => {
    const store = makeStore({
      statuses: { t1: "thinking" },
      tokenUsageByThread: { t1: { usedPercent: 50 } },
      agentRuntimeById: { t1: { taskId: "T1", capabilities: [] } },
    });
    const sleepSpy = vi.fn().mockResolvedValue(undefined);
    const r = start(store, {
      sleepFn: sleepSpy,
      continueTaskById: vi.fn().mockRejectedValue(new Error("401 invalid_api_key")),
    });
    store.state.tokenUsageByThread = { t1: { usedPercent: 99 } };
    await nextTick();
    await new Promise((resolve) => setTimeout(resolve, 100));
    expect(sleepSpy).not.toHaveBeenCalled();
    const fail = r.failedAutoContinueByThread.value.get("t1");
    expect(fail && fail.permanent_reason).toBe("permanent_unauthenticated");
  });

  it("非永久错误（连接拒绝）走 retry 路径", async () => {
    const store = makeStore({
      statuses: { t1: "thinking" },
      tokenUsageByThread: { t1: { usedPercent: 50 } },
      agentRuntimeById: { t1: { taskId: "T1", capabilities: [] } },
    });
    const sleepSpy = vi.fn().mockResolvedValue(undefined);
    const r = start(store, {
      sleepFn: sleepSpy,
      continueTaskById: vi.fn().mockRejectedValueOnce(new Error("connection refused")).mockResolvedValue("new-id"),
    });
    store.state.tokenUsageByThread = { t1: { usedPercent: 99 } };
    await nextTick();
    await new Promise((resolve) => setTimeout(resolve, 100));
    expect(sleepSpy).toHaveBeenCalled();
  });
});
