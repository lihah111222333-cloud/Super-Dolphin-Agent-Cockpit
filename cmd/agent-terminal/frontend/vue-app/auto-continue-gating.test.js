// @ts-nocheck
import { beforeEach, describe, expect, it } from 'vitest';
import {
  createAutoContinueGate,
  _AUTO_CONTINUE_GATE_CONSTANTS as K,
} from './composables/auto-continue-gating.js';

let gate;
let now;
function advance(ms) { now += ms; }

beforeEach(() => {
  gate = createAutoContinueGate();
  now = 1_000_000;
  gate._setNowForTest(() => now);
});

describe('createAutoContinueGate · per-thread continue limit', () => {
  it('allows first continue, blocks second on same source thread', () => {
    expect(gate.check({ kind: 'continue', threadId: 't1' })).toEqual({ allow: true });
    gate.recordContinue({ threadId: 't1' });
    expect(gate.check({ kind: 'continue', threadId: 't1' })).toEqual({
      allow: false, reason: 'thread_already_continued',
    });
  });

  it('per-thread limit does NOT apply to recover kind', () => {
    gate.recordContinue({ threadId: 't1' });
    expect(gate.check({ kind: 'recover', threadId: 't1' }).allow).toBe(true);
  });

  it('per-thread counter is independent across threads', () => {
    gate.recordContinue({ threadId: 't1' });
    expect(gate.check({ kind: 'continue', threadId: 't2' }).allow).toBe(true);
  });
});

describe('createAutoContinueGate · global fuse', () => {
  it('blows after GLOBAL_WINDOW_MAX records in window', () => {
    for (let i = 0; i < K.GLOBAL_WINDOW_MAX; i++) {
      gate.recordContinue({ threadId: `t${i}` });
    }
    expect(gate.check({ kind: 'continue', threadId: 'tx' })).toEqual({
      allow: false, reason: 'global_fuse_blown', fuseBlown: true,
    });
  });

  it('recover records also count toward global fuse', () => {
    for (let i = 0; i < K.GLOBAL_WINDOW_MAX; i++) {
      gate.recordRecover({ threadId: `t${i}` });
    }
    const r = gate.check({ kind: 'continue', threadId: 'tx' });
    expect(r.allow).toBe(false);
    expect(r.fuseBlown).toBe(true);
  });

  it('fuse resets after GLOBAL_WINDOW_MS', () => {
    for (let i = 0; i < K.GLOBAL_WINDOW_MAX; i++) {
      gate.recordContinue({ threadId: `t${i}` });
    }
    advance(K.GLOBAL_WINDOW_MS + 1);
    expect(gate.check({ kind: 'continue', threadId: 'tx' }).allow).toBe(true);
  });
});

describe('createAutoContinueGate · snapshot', () => {
  it('reports current internal counters', () => {
    gate.recordContinue({ threadId: 't1' });
    gate.recordContinue({ threadId: 't2' });
    gate.recordRecover({ threadId: 't3' });
    expect(gate.snapshot()).toEqual({ continuedThreads: 2, globalCount: 3 });
  });
});

describe('createAutoContinueGate · Phase 1.4a-fix 数值锁定（防回归）', () => {
  it('全局闸滑窗常量为 5min/15（原 60s/20，详 §4.3 慢失控数值算式）', () => {
    expect(K.GLOBAL_WINDOW_MS).toBe(5 * 60 * 1000);
    expect(K.GLOBAL_WINDOW_MAX).toBe(15);
  });

  it('族维度常量 5min/5 已为 Phase 4.2 预留', () => {
    expect(K.FAMILY_WINDOW_MS).toBe(5 * 60 * 1000);
    expect(K.FAMILY_WINDOW_MAX).toBe(5);
  });

  it('5min 内累计 15 次记录后第 16 次触发保险丝', () => {
    for (let i = 0; i < 15; i++) gate.recordContinue({ threadId: `t${i}` });
    const r = gate.check({ kind: 'continue', threadId: 'tx' });
    expect(r).toEqual({ allow: false, reason: 'global_fuse_blown', fuseBlown: true });
  });
});

describe('createAutoContinueGate · Phase 1.8e 自愈日志回调', () => {
  it('滑窗自愈时调 onFuseRecovered（单次，不重复）', () => {
    const calls = [];
    const g = createAutoContinueGate({ onFuseRecovered: (info) => calls.push(info) });
    let t = 1_000_000;
    g._setNowForTest(() => t);
    for (let i = 0; i < 15; i++) g.recordContinue({ threadId: `t${i}` });
    // 触发 fuseBlown（设 armed = true）
    expect(g.check({ kind: 'continue', threadId: 'tx' }).fuseBlown).toBe(true);
    expect(calls.length).toBe(0); // 还在窗内，未自愈
    // 跨过滑窗 → 下次 check 触发自愈
    t += K.GLOBAL_WINDOW_MS + 1;
    expect(g.check({ kind: 'continue', threadId: 'ty' }).allow).toBe(true);
    expect(calls.length).toBe(1);
    expect(calls[0].globalCount).toBe(0);
    expect(calls[0].windowMs).toBe(K.GLOBAL_WINDOW_MS);
    expect(calls[0].windowMax).toBe(K.GLOBAL_WINDOW_MAX);
    // 后续 check 不再重复触发
    g.check({ kind: 'continue', threadId: 'tz' });
    expect(calls.length).toBe(1);
  });

  it('回调抛错不会破坏闸门', () => {
    const g = createAutoContinueGate({
      onFuseRecovered: () => { throw new Error('boom'); },
    });
    let t = 2_000_000;
    g._setNowForTest(() => t);
    for (let i = 0; i < 15; i++) g.recordContinue({ threadId: `t${i}` });
    g.check({ kind: 'continue', threadId: 'tx' }); // armed = true
    t += K.GLOBAL_WINDOW_MS + 1;
    // 不应该 throw
    expect(() => g.check({ kind: 'continue', threadId: 'ty' })).not.toThrow();
  });
});
