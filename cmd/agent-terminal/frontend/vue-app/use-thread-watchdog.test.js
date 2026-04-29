// @ts-nocheck
// Phase 1.7b 单测：useThreadWatchdog · 事件停滞检测 + 双分流。
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('./services/log.js', () => ({
  logInfo: vi.fn(),
  logWarn: vi.fn(),
  logError: vi.fn(),
}));

import { useThreadWatchdog, _USE_THREAD_WATCHDOG_CONSTANTS as K } from './composables/useThreadWatchdog.js';

function makeStore(init = {}) {
  return {
    state: {
      lastEventTsByThread: { ...(init.lastEventTsByThread || {}) },
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
      lastEventTsByThread: { t1: 1000, t2: 1000, t3: 1000, t4: 1000, t5: 1000 },
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
      lastEventTsByThread: { t1: 1000, t2: 1000, t3: 1000, t4: 1000 },
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
      lastEventTsByThread: { t1: now - K.STALL_THRESHOLD_MS - 1 },
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
      lastEventTsByThread: { t1: now - 1000 },
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
      lastEventTsByThread: { t1: now - K.STALL_THRESHOLD_MS - 1 },
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
      lastEventTsByThread: { t1: now - K.STALL_THRESHOLD_MS - 1 },
      statuses: { t1: 'thinking' },
      agentRuntimeById: { t1: {} },
    });
    const wd = useThreadWatchdog({ threadStore: store, sendMessage });
    wd._setNowForTest(() => now);
    wd._scanForTest();
    expect(sendMessage).not.toHaveBeenCalled();
    expect(wd.stuckByThread.value.has('t1')).toBe(true);
    expect(wd.stuckByThread.value.get('t1')).toBe(now);
  });
});

describe('useThreadWatchdog · 节流（触发后重置 lastEvent）', () => {
  it('触发后立即把 lastEventTsByThread[tid] 重置为 now，下次扫描不再触发', () => {
    const sendMessage = vi.fn().mockResolvedValue(undefined);
    const store = makeStore({
      lastEventTsByThread: { t1: now - K.STALL_THRESHOLD_MS - 1 },
      statuses: { t1: 'thinking' },
      agentRuntimeById: { t1: { taskId: 'task' } },
    });
    const wd = useThreadWatchdog({ threadStore: store, sendMessage });
    wd._setNowForTest(() => now);
    wd._scanForTest();
    expect(sendMessage).toHaveBeenCalledTimes(1);
    expect(store.state.lastEventTsByThread.t1).toBe(now);
    // 第二次扫描（now 不变）→ 不再触发
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
        lastEventTsByThread: { t1: 1000 },
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

describe('useThreadWatchdog · 健壮性', () => {
  it('threadStore 为 null / undefined 不抛异常', () => {
    const wd = useThreadWatchdog({ threadStore: null });
    expect(() => wd._scanForTest()).not.toThrow();
  });

  it('sendMessage 抛异常不破坏扫描', () => {
    const sendMessage = vi.fn().mockImplementation(() => { throw new Error('boom'); });
    const store = makeStore({
      lastEventTsByThread: { t1: now - K.STALL_THRESHOLD_MS - 1, t2: now - K.STALL_THRESHOLD_MS - 1 },
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
