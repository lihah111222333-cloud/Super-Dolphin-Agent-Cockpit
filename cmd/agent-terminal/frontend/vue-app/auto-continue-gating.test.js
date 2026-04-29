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
