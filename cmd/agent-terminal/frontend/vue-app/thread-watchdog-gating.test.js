// @ts-nocheck
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('./services/log.js', () => ({
  logInfo: vi.fn(),
  logWarn: vi.fn(),
  logError: vi.fn(),
}));

import {
  createThreadWatchdogGate,
  _THREAD_WATCHDOG_GATE_CONSTANTS as K,
} from './composables/thread-watchdog-gating.js';

let gate;
let now;
function advance(ms) { now += ms; }

beforeEach(() => {
  gate = createThreadWatchdogGate();
  now = 1_000_000;
  gate._setNowForTest(() => now);
});

describe('createThreadWatchdogGate · per-thread 节流', () => {
  it('60s 内同 thread 第二次被节流', () => {
    expect(gate.check({ threadId: 't1' }).allow).toBe(true);
    gate.recordPoke({ threadId: 't1' });
    expect(gate.check({ threadId: 't1' })).toEqual({ allow: false, reason: 'thread_throttled' });
  });

  it('60s 后允许再次戳同 thread', () => {
    gate.recordPoke({ threadId: 't1' });
    advance(K.PER_THREAD_THROTTLE_MS + 1);
    expect(gate.check({ threadId: 't1' }).allow).toBe(true);
  });

  it('per-thread 不同 thread 互不影响', () => {
    gate.recordPoke({ threadId: 't1' });
    expect(gate.check({ threadId: 't2' }).allow).toBe(true);
  });
});

describe('createThreadWatchdogGate · 全局保险丝 5min/10', () => {
  it('5min 内 10 次后第 11 次撞墙', () => {
    for (let i = 0; i < K.GLOBAL_WINDOW_MAX; i++) {
      gate.recordPoke({ threadId: `t${i}` });
    }
    const r = gate.check({ threadId: 'tx' });
    expect(r).toEqual({ allow: false, reason: 'global_fuse_blown', fuseBlown: true });
  });

  it('5min 后保险丝重置', () => {
    for (let i = 0; i < K.GLOBAL_WINDOW_MAX; i++) {
      gate.recordPoke({ threadId: `t${i}` });
    }
    advance(K.GLOBAL_WINDOW_MS + 1);
    expect(gate.check({ threadId: 'tx' }).allow).toBe(true);
  });
});

describe('createThreadWatchdogGate · 自愈日志回调', () => {
  it('滑窗自愈触发 onFuseRecovered（单次）', () => {
    const calls = [];
    const g = createThreadWatchdogGate({ onFuseRecovered: (info) => calls.push(info) });
    let t = 5_000_000;
    g._setNowForTest(() => t);
    for (let i = 0; i < K.GLOBAL_WINDOW_MAX; i++) g.recordPoke({ threadId: `t${i}` });
    expect(g.check({ threadId: 'tx' }).fuseBlown).toBe(true);
    expect(calls.length).toBe(0);
    t += K.GLOBAL_WINDOW_MS + 1;
    expect(g.check({ threadId: 'ty' }).allow).toBe(true);
    expect(calls.length).toBe(1);
    expect(calls[0].globalCount).toBe(0);
    expect(calls[0].windowMax).toBe(K.GLOBAL_WINDOW_MAX);
  });
});

describe('createThreadWatchdogGate · 数值锁定（防回归）', () => {
  it('PER_THREAD_THROTTLE 60s / 全局 5min/10', () => {
    expect(K.PER_THREAD_THROTTLE_MS).toBe(60_000);
    expect(K.GLOBAL_WINDOW_MS).toBe(5 * 60 * 1000);
    expect(K.GLOBAL_WINDOW_MAX).toBe(10);
  });
});
