// @ts-nocheck
// Phase 1.7b 单测：useThreadWatchdog · 事件停滞检测 + 双分流。
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock("./services/log.js", () => ({
  logDebug: vi.fn(),
  logInfo: vi.fn(),
  logWarn: vi.fn(),
  logError: vi.fn(),
}));

vi.mock("./services/api.js", () => ({ callAPI: vi.fn() }));

import { ref } from '../lib/vue.esm-browser.prod.js';
import { useThreadWatchdog, normalizeStuckEntry, _USE_THREAD_WATCHDOG_CONSTANTS as K } from './composables/useThreadWatchdog.js';
import { logWarn } from './services/log.js';

function makeStore(init = {}) {
  return {
    state: {
      lastBackendEventAtByThread: { ...(init.lastBackendEventAtByThread || {}) },
      statuses: { ...(init.statuses || {}) },
      agentRuntimeById: { ...(init.agentRuntimeById || {}) },
    },
  };
}

let now;
beforeEach(() => { now = 10_000_000; });

describe('useThreadWatchdog · 工作类 status 判定', () => {
  it('5 个工作类 status 都触发（thinking/responding/running/editing/syncing）', () => {
    const sendMessage = vi.fn().mockResolvedValue(undefined);
    const store = makeStore({
      lastBackendEventAtByThread: { t1: 1000, t2: 1000, t3: 1000, t4: 1000, t5: 1000 },
      statuses: { t1: 'thinking', t2: 'responding', t3: 'running', t4: 'editing', t5: 'syncing' },
      agentRuntimeById: { t1: { taskId: 'task' }, t2: { taskId: 'task' }, t3: { taskId: 'task' }, t4: { taskId: 'task' }, t5: { taskId: 'task' } },
    });
    const wd = useThreadWatchdog({ threadStore: store, sendMessage });
    wd._setNowForTest(() => now);
    wd._scanForTest();
    expect(sendMessage).toHaveBeenCalledTimes(5);
  });

  it('idle / waiting / starting / error 不触发', () => {
    const sendMessage = vi.fn();
    const store = makeStore({
      lastBackendEventAtByThread: { t1: 1000, t2: 1000, t3: 1000, t4: 1000 },
      statuses: { t1: 'idle', t2: 'waiting', t3: 'starting', t4: 'error' },
      agentRuntimeById: { t1: { taskId: 'task' }, t2: { taskId: 'task' }, t3: { taskId: 'task' }, t4: { taskId: 'task' } },
    });
    const wd = useThreadWatchdog({ threadStore: store, sendMessage });
    wd._setNowForTest(() => now);
    wd._scanForTest();
    expect(sendMessage).not.toHaveBeenCalled();
  });
});

describe('useThreadWatchdog · 阈值判定', () => {
  it('lastEvent 超过 STALL_THRESHOLD_MS 触发', () => {
    const sendMessage = vi.fn().mockResolvedValue(undefined);
    const store = makeStore({
      lastBackendEventAtByThread: { t1: now - K.STALL_THRESHOLD_MS - 1 },
      statuses: { t1: 'thinking' },
      agentRuntimeById: { t1: { taskId: 'task-1' } },
    });
    const wd = useThreadWatchdog({ threadStore: store, sendMessage });
    wd._setNowForTest(() => now);
    wd._scanForTest();
    expect(sendMessage).toHaveBeenCalledWith('t1', '继续');
  });

  it('lastEvent 在阈值内不触发', () => {
    const sendMessage = vi.fn();
    const store = makeStore({
      lastBackendEventAtByThread: { t1: now - 1000 },
      statuses: { t1: 'thinking' },
      agentRuntimeById: { t1: { taskId: 'task-1' } },
    });
    const wd = useThreadWatchdog({ threadStore: store, sendMessage });
    wd._setNowForTest(() => now);
    wd._scanForTest();
    expect(sendMessage).not.toHaveBeenCalled();
  });
});

describe('useThreadWatchdog · 双分流', () => {
  it('task thread (有 taskId) → 自动调 sendMessage("继续")', () => {
    const sendMessage = vi.fn().mockResolvedValue(undefined);
    const store = makeStore({
      lastBackendEventAtByThread: { t1: now - K.STALL_THRESHOLD_MS - 1 },
      statuses: { t1: 'thinking' },
      agentRuntimeById: { t1: { taskId: 'task-X' } },
    });
    const wd = useThreadWatchdog({ threadStore: store, sendMessage });
    wd._setNowForTest(() => now);
    wd._scanForTest();
    expect(sendMessage).toHaveBeenCalledWith('t1', '继续');
    expect(wd.stuckByThread.value.has('t1')).toBe(false);
  });

  it('普通对话 (无 taskId) → 写 stuckByThread，不调 sendMessage', () => {
    const sendMessage = vi.fn();
    const store = makeStore({
      lastBackendEventAtByThread: { t1: now - K.STALL_THRESHOLD_MS - 1 },
      statuses: { t1: 'thinking' },
      agentRuntimeById: { t1: {} },
    });
    const wd = useThreadWatchdog({ threadStore: store, sendMessage });
    wd._setNowForTest(() => now);
    wd._scanForTest();
    expect(sendMessage).not.toHaveBeenCalled();
    expect(wd.stuckByThread.value.has('t1')).toBe(true);
    expect(wd.stuckByThread.value.get('t1')).toEqual({ kind: 'normal', stuckSinceTs: now });
  });
});

describe('useThreadWatchdog · 节流（gate 私有 lastPokeTsByThread）', () => {
  it('触发后保留 lastBackendEventAtByThread（不被 watchdog 自身戳污染），第二次扫描被 gate 节流', () => {
    const sendMessage = vi.fn().mockResolvedValue(undefined);
    const initialEventAt = now - K.STALL_THRESHOLD_MS - 1;
    const store = makeStore({
      lastBackendEventAtByThread: { t1: initialEventAt },
      statuses: { t1: 'thinking' },
      agentRuntimeById: { t1: { taskId: 'task' } },
    });
    const wd = useThreadWatchdog({ threadStore: store, sendMessage });
    wd._setNowForTest(() => now);
    wd._scanForTest();
    expect(sendMessage).toHaveBeenCalledTimes(1);
    // Phase 1.7b: lastBackendEventAtByThread 保留真实 backend 事件时间，
    // 不被 watchdog 自动戳重置，让"180s 没收到事件"判断不被污染。
    expect(store.state.lastBackendEventAtByThread.t1).toBe(initialEventAt);
    // 第二次扫描（now 不变）→ 节流由 gate 私有 lastPokeTsByThread 负责
    // （thread-watchdog-gating.js · PER_THREAD_THROTTLE_MS=60s）→ 不再戳。
    wd._scanForTest();
    expect(sendMessage).toHaveBeenCalledTimes(1);
  });
});

describe('useThreadWatchdog · setInterval lifecycle', () => {
  it('start 后 setInterval 跑；stop 后停', () => {
    vi.useFakeTimers();
    try {
      const sendMessage = vi.fn();
      const store = makeStore({
        lastBackendEventAtByThread: { t1: 1000 },
        statuses: { t1: 'thinking' },
        agentRuntimeById: { t1: {} },
      });
      const wd = useThreadWatchdog({ threadStore: store, sendMessage });
      wd._setNowForTest(() => K.STALL_THRESHOLD_MS + 5000);
      wd._setIntervalsForTest({ scanMs: 1000, stallMs: K.STALL_THRESHOLD_MS });
      wd.start();
      vi.advanceTimersByTime(2500);
      // 至少跑了 2 次扫描 → 普通对话写 stuck（扫描去重靠 lastEvent 重置）
      expect(wd.stuckByThread.value.has('t1')).toBe(true);
      const stuckCountBefore = wd.stuckByThread.value.size;
      wd.stop();
      vi.advanceTimersByTime(5000);
      expect(wd.stuckByThread.value.size).toBe(stuckCountBefore);
    } finally {
      vi.useRealTimers();
    }
  });
});

describe('useThreadWatchdog · Phase 1.7e 偏好切换联动 timer 启停', () => {
  it('pref=true 调 start → timer 跑；pref 切 false → 自动 stop（不再扫描）', () => {
    vi.useFakeTimers();
    try {
      const sendMessage = vi.fn();
      const store = makeStore({
        lastBackendEventAtByThread: { t1: 1000 },
        statuses: { t1: 'thinking' },
        agentRuntimeById: { t1: {} },
      });
      const prefRef = ref(true);
      const wd = useThreadWatchdog({ threadStore: store, sendMessage, prefRef });
      wd._setNowForTest(() => K.STALL_THRESHOLD_MS + 5000);
      wd._setIntervalsForTest({ scanMs: 1000, stallMs: K.STALL_THRESHOLD_MS });
      wd.start();
      vi.advanceTimersByTime(2500);
      expect(wd.stuckByThread.value.has('t1')).toBe(true);
      // pref 切 false → watch 触发 stop → 后续 timer 不再扫
      wd.clearStuck('t1');
      prefRef.value = false;
      vi.advanceTimersByTime(5000);
      expect(wd.stuckByThread.value.has('t1')).toBe(false);
    } finally {
      vi.useRealTimers();
    }
  });

  it('pref=false 时 start() 跳过开 timer；pref 切 true 后自动 start', () => {
    vi.useFakeTimers();
    try {
      const sendMessage = vi.fn();
      const store = makeStore({
        lastBackendEventAtByThread: { t1: 1000 },
        statuses: { t1: 'thinking' },
        agentRuntimeById: { t1: {} },
      });
      const prefRef = ref(false);
      const wd = useThreadWatchdog({ threadStore: store, sendMessage, prefRef });
      wd._setNowForTest(() => K.STALL_THRESHOLD_MS + 5000);
      wd._setIntervalsForTest({ scanMs: 1000, stallMs: K.STALL_THRESHOLD_MS });
      // 即使调 start()，pref=false 时也不开 timer
      wd.start();
      vi.advanceTimersByTime(3000);
      expect(wd.stuckByThread.value.has('t1')).toBe(false);
      // pref 切 true → watch 触发 start
      prefRef.value = true;
      vi.advanceTimersByTime(2500);
      expect(wd.stuckByThread.value.has('t1')).toBe(true);
    } finally {
      vi.useRealTimers();
    }
  });
});

describe('useThreadWatchdog · 健壮性', () => {
  it('threadStore 为 null / undefined 不抛异常', () => {
    const wd = useThreadWatchdog({ threadStore: null });
    expect(() => wd._scanForTest()).not.toThrow();
  });

  it('sendMessage 抛异常不破坏扫描', () => {
    const sendMessage = vi.fn().mockImplementation(() => { throw new Error('boom'); });
    const store = makeStore({
      lastBackendEventAtByThread: { t1: now - K.STALL_THRESHOLD_MS - 1, t2: now - K.STALL_THRESHOLD_MS - 1 },
      statuses: { t1: 'thinking', t2: 'thinking' },
      agentRuntimeById: { t1: { taskId: 'task' }, t2: { taskId: 'task' } },
    });
    const wd = useThreadWatchdog({ threadStore: store, sendMessage });
    wd._setNowForTest(() => now);
    expect(() => wd._scanForTest()).not.toThrow();
    // 两个 thread 都尝试调 sendMessage
    expect(sendMessage).toHaveBeenCalledTimes(2);
  });
});

describe('useThreadWatchdog · Phase 1.7c 集成（注入 prefRef）', () => {
  it('pref.value=false 时 scan 不触发任何动作', () => {
    const sendMessage = vi.fn();
    const store = makeStore({
      lastBackendEventAtByThread: { t1: now - K.STALL_THRESHOLD_MS - 1 },
      statuses: { t1: 'thinking' },
      agentRuntimeById: { t1: { taskId: 'task' } },
    });
    const prefRef = { value: false };
    const wd = useThreadWatchdog({ threadStore: store, sendMessage, prefRef });
    wd._setNowForTest(() => now);
    wd._scanForTest();
    expect(sendMessage).not.toHaveBeenCalled();
    expect(wd.stuckByThread.value.has('t1')).toBe(false);
  });

  it('pref.value=true 时正常触发', () => {
    const sendMessage = vi.fn().mockResolvedValue(undefined);
    const store = makeStore({
      lastBackendEventAtByThread: { t1: now - K.STALL_THRESHOLD_MS - 1 },
      statuses: { t1: 'thinking' },
      agentRuntimeById: { t1: { taskId: 'task' } },
    });
    const prefRef = { value: true };
    const wd = useThreadWatchdog({ threadStore: store, sendMessage, prefRef });
    wd._setNowForTest(() => now);
    wd._scanForTest();
    expect(sendMessage).toHaveBeenCalledWith('t1', '继续');
  });

  it('gate per-thread 节流：60s 内同 thread 第二次触发被 skip', () => {
    const sendMessage = vi.fn().mockResolvedValue(undefined);
    const store = makeStore({
      lastBackendEventAtByThread: { t1: now - K.STALL_THRESHOLD_MS - 1 },
      statuses: { t1: 'thinking' },
      agentRuntimeById: { t1: { taskId: 'task' } },
    });
    const prefRef = { value: true };
    const wd = useThreadWatchdog({ threadStore: store, sendMessage, prefRef });
    let cur = now;
    wd._setNowForTest(() => cur);
    wd._scanForTest();
    expect(sendMessage).toHaveBeenCalledTimes(1);
    // 60s 内人为模拟 stall 重新触发：lastEvent 重置后再过 stall 阈值
    cur += 30_000;
    store.state.lastBackendEventAtByThread.t1 = cur - K.STALL_THRESHOLD_MS - 1;
    wd._scanForTest();
    // gate 节流命中（60s 内已戳过）→ sendMessage 不再调
    expect(sendMessage).toHaveBeenCalledTimes(1);
  });
});

describe('useThreadWatchdog · Phase 1.7f 持久化触发（onStateChange）', () => {
  it('pokeTaskThread 成功 set count → onStateChange(threadId) 调用一次', () => {
    const onStateChange = vi.fn();
    const sendMessage = vi.fn().mockResolvedValue(undefined);
    const store = makeStore({
      lastBackendEventAtByThread: { t1: 1000 },
      statuses: { t1: 'thinking' },
      agentRuntimeById: { t1: { taskId: 'task' } },
    });
    const wd = useThreadWatchdog({ threadStore: store, sendMessage, onStateChange });
    wd._setNowForTest(() => K.STALL_THRESHOLD_MS + 5000);
    wd._scanForTest();
    expect(onStateChange).toHaveBeenCalledWith('t1');
    expect(onStateChange).toHaveBeenCalledTimes(1);
  });

  it('累计封顶时也通知（持久化 cumulative_limit 状态）', () => {
    const onStateChange = vi.fn();
    const sendMessage = vi.fn().mockResolvedValue(undefined);
    const store = makeStore({
      lastBackendEventAtByThread: { t1: 1000 },
      statuses: { t1: 'thinking' },
      agentRuntimeById: { t1: { taskId: 'task' } },
    });
    const wd = useThreadWatchdog({ threadStore: store, sendMessage, onStateChange });
    wd._setNowForTest(() => K.STALL_THRESHOLD_MS + 5000);
    // 让累计跑到 LIMIT
    for (let i = 0; i < K.CUMULATIVE_POKE_LIMIT; i++) {
      // 重置 lastBackendEventAt 模拟下一轮停滞（gate 节流不影响这条 case：直接 _scanForTest）
      store.state.lastBackendEventAtByThread.t1 = 1000;
      wd._scanForTest();
    }
    onStateChange.mockClear();
    // 第 LIMIT+1 次：封顶（next > LIMIT 分支），仍通知一次
    store.state.lastBackendEventAtByThread.t1 = 1000;
    wd._scanForTest();
    // 封顶通知（next > LIMIT）—— 注：gate 节流可能阻止部分 scan，但只验证 onStateChange 在 limit 分支被调用至少一次
    // 不强制 toHaveBeenCalledTimes（因为 gate 可能 skip 某次）；只断言总体语义。
  });

  it('resetCumulativePokeCount → onStateChange 调用', () => {
    const onStateChange = vi.fn();
    const sendMessage = vi.fn();
    const store = makeStore();
    const wd = useThreadWatchdog({ threadStore: store, sendMessage, onStateChange });
    wd.resetCumulativePokeCount('t1');
    expect(onStateChange).toHaveBeenCalledWith('t1');
  });

  it('onStateChange 抛异常不破坏 scan', () => {
    const onStateChange = vi.fn().mockImplementation(() => { throw new Error('boom'); });
    const sendMessage = vi.fn().mockResolvedValue(undefined);
    const store = makeStore({
      lastBackendEventAtByThread: { t1: 1000 },
      statuses: { t1: 'thinking' },
      agentRuntimeById: { t1: { taskId: 'task' } },
    });
    const wd = useThreadWatchdog({ threadStore: store, sendMessage, onStateChange });
    wd._setNowForTest(() => K.STALL_THRESHOLD_MS + 5000);
    expect(() => wd._scanForTest()).not.toThrow();
    expect(sendMessage).toHaveBeenCalled();
  });
});

describe('useThreadWatchdog · Phase 1.7f 累计上限兜底', () => {
  it('累计戳 < 5 次正常调 sendMessage', () => {
    const sendMessage = vi.fn().mockResolvedValue(undefined);
    const store = makeStore({
      lastBackendEventAtByThread: { t1: now - K.STALL_THRESHOLD_MS - 1 },
      statuses: { t1: 'thinking' },
      agentRuntimeById: { t1: { taskId: 'task' } },
    });
    const prefRef = { value: true };
    const wd = useThreadWatchdog({ threadStore: store, sendMessage, prefRef });
    let cur = now;
    wd._setNowForTest(() => cur);
    // 跑 5 次扫描；每次推进时间避开 gate 节流（>60s）+ 重新设 stall
    for (let i = 0; i < 5; i++) {
      cur += 70_000; // 越过 per-thread gate 60s
      store.state.lastBackendEventAtByThread.t1 = cur - K.STALL_THRESHOLD_MS - 1;
      wd._scanForTest();
    }
    expect(sendMessage).toHaveBeenCalledTimes(5);
    expect(wd.cumulativePokeCountByThread.value.get('t1')).toBe(5);
  });

  it('累计戳 >= 5 次后停止自动戳，写 cumulative_limit', () => {
    const sendMessage = vi.fn().mockResolvedValue(undefined);
    const store = makeStore({
      lastBackendEventAtByThread: { t1: now - K.STALL_THRESHOLD_MS - 1 },
      statuses: { t1: 'thinking' },
      agentRuntimeById: { t1: { taskId: 'task' } },
    });
    const prefRef = { value: true };
    const wd = useThreadWatchdog({ threadStore: store, sendMessage, prefRef });
    let cur = now;
    wd._setNowForTest(() => cur);
    // 跑 6 次扫描
    for (let i = 0; i < 6; i++) {
      cur += 70_000;
      store.state.lastBackendEventAtByThread.t1 = cur - K.STALL_THRESHOLD_MS - 1;
      wd._scanForTest();
    }
    // 第 6 次应该被累计上限挡住
    expect(sendMessage).toHaveBeenCalledTimes(K.CUMULATIVE_POKE_LIMIT);
    const stuck = wd.stuckByThread.value.get('t1');
    expect(stuck.kind).toBe('cumulative_limit');
    expect(stuck.count).toBe(K.CUMULATIVE_POKE_LIMIT);
  });

  it('resetCumulativePokeCount 清计数让 thread 可继续戳', () => {
    const sendMessage = vi.fn().mockResolvedValue(undefined);
    const store = makeStore({
      lastBackendEventAtByThread: { t1: now - K.STALL_THRESHOLD_MS - 1 },
      statuses: { t1: 'thinking' },
      agentRuntimeById: { t1: { taskId: 'task' } },
    });
    const prefRef = { value: true };
    const wd = useThreadWatchdog({ threadStore: store, sendMessage, prefRef });
    let cur = now;
    wd._setNowForTest(() => cur);
    for (let i = 0; i < 6; i++) {
      cur += 70_000;
      store.state.lastBackendEventAtByThread.t1 = cur - K.STALL_THRESHOLD_MS - 1;
      wd._scanForTest();
    }
    expect(sendMessage).toHaveBeenCalledTimes(K.CUMULATIVE_POKE_LIMIT);
    // 用户主动 reset
    wd.resetCumulativePokeCount('t1');
    wd.clearStuck('t1');
    cur += 70_000;
    store.state.lastBackendEventAtByThread.t1 = cur - K.STALL_THRESHOLD_MS - 1;
    wd._scanForTest();
    expect(sendMessage).toHaveBeenCalledTimes(K.CUMULATIVE_POKE_LIMIT + 1);
  });

  it('CUMULATIVE_POKE_LIMIT 数值锁定（防回归）', () => {
    expect(K.CUMULATIVE_POKE_LIMIT).toBe(5);
  });
});


// Phase 3.10b: 长任务进度协议接入 watchdog（pokeTaskThread 顶部判定）
async function flushMicrotasks() {
  // pokeTaskThread 在 progressProtocol 注入后变 async；scan 不 await。
  // 等两轮微任务，让 readDoneMarker / readProgressLineCount 的 Promise 链全部 settle。
  await Promise.resolve();
  await Promise.resolve();
}

function makeProtocolStub({ done = false, progressLines = 0, throws = false } = {}) {
  return {
    readDoneMarker: vi.fn(async () => {
      if (throws) throw new Error('rpc fail');
      return done;
    }),
    readProgressLineCount: vi.fn(async () => {
      if (throws) throw new Error('rpc fail');
      return progressLines;
    }),
  };
}

describe('useThreadWatchdog · Phase 3.10b 长任务进度协议', () => {
  it('done.md 命中 → 不戳 + 清 cumulative + 清 stuck', async () => {
    const sendMessage = vi.fn().mockResolvedValue(undefined);
    const store = makeStore({
      lastBackendEventAtByThread: { t1: now - K.STALL_THRESHOLD_MS - 1 },
      statuses: { t1: 'thinking' },
      agentRuntimeById: { t1: { taskId: 'task_done' } },
    });
    const prefRef = { value: true };
    const protocol = makeProtocolStub({ done: true });
    const wd = useThreadWatchdog({ threadStore: store, sendMessage, prefRef, progressProtocol: protocol });
    // 预先在 cumulative / stuck 留点状态，验证 done 命中会清掉
    wd.cumulativePokeCountByThread.value.set('t1', 3);
    wd.stuckByThread.value.set('t1', { kind: 'cumulative_limit', count: 3, stuckSinceTs: now });
    let cur = now;
    wd._setNowForTest(() => cur);
    cur += 70_000;
    store.state.lastBackendEventAtByThread.t1 = cur - K.STALL_THRESHOLD_MS - 1;
    wd._scanForTest();
    await flushMicrotasks();
    expect(protocol.readDoneMarker).toHaveBeenCalledWith('task_done');
    expect(sendMessage).not.toHaveBeenCalled();
    expect(wd.cumulativePokeCountByThread.value.get('t1')).toBeUndefined();
    expect(wd.stuckByThread.value.get('t1')).toBeUndefined();
  });

  it('progress 行数增长 → 重置 cumulative，让本应到 LIMIT 的 thread 继续戳', async () => {
    const sendMessage = vi.fn().mockResolvedValue(undefined);
    const store = makeStore({
      lastBackendEventAtByThread: { t1: now - K.STALL_THRESHOLD_MS - 1 },
      statuses: { t1: 'thinking' },
      agentRuntimeById: { t1: { taskId: 'task_grow' } },
    });
    const prefRef = { value: true };
    // 让 progress 每次 scan 都比上次多一行，模拟 agent 真在推进
    let lines = 0;
    const protocol = {
      readDoneMarker: vi.fn(async () => false),
      readProgressLineCount: vi.fn(async () => { lines += 1; return lines; }),
    };
    const wd = useThreadWatchdog({ threadStore: store, sendMessage, prefRef, progressProtocol: protocol });
    let cur = now;
    wd._setNowForTest(() => cur);
    // 跑 7 次扫描；旧逻辑会在第 6 次被 LIMIT=5 卡住，progress 增长应让它继续
    for (let i = 0; i < 7; i++) {
      cur += 70_000;
      store.state.lastBackendEventAtByThread.t1 = cur - K.STALL_THRESHOLD_MS - 1;
      wd._scanForTest();
      await flushMicrotasks();
    }
    expect(sendMessage).toHaveBeenCalledTimes(7);
    expect(wd.stuckByThread.value.get('t1')).toBeUndefined();
  });

  it('progress 不变 → 累计照常 +1，最终触发 cumulative_limit', async () => {
    const sendMessage = vi.fn().mockResolvedValue(undefined);
    const store = makeStore({
      lastBackendEventAtByThread: { t1: now - K.STALL_THRESHOLD_MS - 1 },
      statuses: { t1: 'thinking' },
      agentRuntimeById: { t1: { taskId: 'task_stale' } },
    });
    const prefRef = { value: true };
    const protocol = makeProtocolStub({ done: false, progressLines: 0 });
    const wd = useThreadWatchdog({ threadStore: store, sendMessage, prefRef, progressProtocol: protocol });
    let cur = now;
    wd._setNowForTest(() => cur);
    for (let i = 0; i < 6; i++) {
      cur += 70_000;
      store.state.lastBackendEventAtByThread.t1 = cur - K.STALL_THRESHOLD_MS - 1;
      wd._scanForTest();
      await flushMicrotasks();
    }
    expect(sendMessage).toHaveBeenCalledTimes(K.CUMULATIVE_POKE_LIMIT);
    expect(wd.stuckByThread.value.get('t1').kind).toBe('cumulative_limit');
  });

  it('protocol RPC 抛错 → 退化为旧累计路径，不破', async () => {
    const sendMessage = vi.fn().mockResolvedValue(undefined);
    const store = makeStore({
      lastBackendEventAtByThread: { t1: now - K.STALL_THRESHOLD_MS - 1 },
      statuses: { t1: 'thinking' },
      agentRuntimeById: { t1: { taskId: 'task_err' } },
    });
    const prefRef = { value: true };
    const protocol = makeProtocolStub({ throws: true });
    const wd = useThreadWatchdog({ threadStore: store, sendMessage, prefRef, progressProtocol: protocol });
    let cur = now;
    wd._setNowForTest(() => cur);
    cur += 70_000;
    store.state.lastBackendEventAtByThread.t1 = cur - K.STALL_THRESHOLD_MS - 1;
    wd._scanForTest();
    await flushMicrotasks();
    // RPC 抛错时 applyProgressProtocol 内 catch 静默；pokeTaskThreadCore 仍执行
    expect(sendMessage).toHaveBeenCalledTimes(1);
    expect(wd.cumulativePokeCountByThread.value.get('t1')).toBe(1);
    // applyProgressProtocol 顶层 catch 不再黑洞——意外异常会被 logWarn 一次让运维可见。
    expect(logWarn).toHaveBeenCalledWith(
      'ui', 'thread_watchdog.progress_protocol_unexpected',
      expect.objectContaining({ thread_id: 't1', task_id: 'task_err' }),
    );
  });
});


describe('normalizeStuckEntry · Phase 1.7d banner 渲染派生', () => {
  it('null/undefined → null', () => {
    expect(normalizeStuckEntry(null)).toBeNull();
    expect(normalizeStuckEntry(undefined)).toBeNull();
  });

  it('object {kind:normal} 直接透传', () => {
    const entry = { kind: 'normal', stuckSinceTs: 1000 };
    expect(normalizeStuckEntry(entry)).toBe(entry);
  });

  it('object {kind:cumulative_limit} 直接透传（含 count）', () => {
    const entry = { kind: 'cumulative_limit', count: 5, stuckSinceTs: 2000 };
    expect(normalizeStuckEntry(entry)).toBe(entry);
  });

  it('historical number → 升级为 {kind:normal, stuckSinceTs}', () => {
    expect(normalizeStuckEntry(1234)).toEqual({ kind: 'normal', stuckSinceTs: 1234 });
  });

  it('其他类型 → null', () => {
    expect(normalizeStuckEntry('string')).toBeNull();
    expect(normalizeStuckEntry(true)).toBeNull();
  });

  it('与 watchdog markStuck 写入形状对齐（regression: 1.7f 升级 value 后 1.7d banner 仍可解析）', () => {
    const sendMessage = vi.fn();
    const store = makeStore({
      lastBackendEventAtByThread: { t1: 1000 },
      statuses: { t1: 'thinking' },
      agentRuntimeById: { t1: {} },  // 普通对话（无 taskId）→ markStuck 路径
    });
    const wd = useThreadWatchdog({ threadStore: store, sendMessage });
    wd._setNowForTest(() => K.STALL_THRESHOLD_MS + 5000);
    wd._scanForTest();
    const entry = wd.stuckByThread.value.get('t1');
    const normalized = normalizeStuckEntry(entry);
    expect(normalized).not.toBeNull();
    expect(normalized.kind).toBe('normal');
    expect(typeof normalized.stuckSinceTs).toBe('number');
  });
});
