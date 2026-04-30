// @ts-nocheck
// Phase 1.7c watchdog 节流闸门（与 auto-continue-gating 同模式 / 闭包私有状态）
// 注：watchdog 是"节流"，不是"per-thread 1 次锁"——失控时反复戳"继续"也只在节流窗口内挡 1 次。
//   1) per-thread 60s/1 次：防同一卡住 thread 在扫描周期内反复触发
//   2) 全局保险丝 5min/10 次：防 watchdog 自身失控（异常的 stall 风暴）

import { logInfo } from '../services/log.js';

const PER_THREAD_THROTTLE_MS = 60_000;
const GLOBAL_WINDOW_MS = 5 * 60 * 1000;
const GLOBAL_WINDOW_MAX = 10;

export function createThreadWatchdogGate(options = {}) {
  const onFuseRecovered = typeof options.onFuseRecovered === 'function' ? options.onFuseRecovered : null;
  const lastPokeTsByThread = new Map();
  const globalLog = [];
  let armed = false;
  let now = () => Date.now();

  function pruneGlobal(currentTs) {
    const cutoff = currentTs - GLOBAL_WINDOW_MS;
    while (globalLog.length > 0 && globalLog[0] < cutoff) globalLog.shift();
    if (armed && globalLog.length < GLOBAL_WINDOW_MAX) {
      armed = false;
      if (onFuseRecovered) {
        try {
          onFuseRecovered({ globalCount: globalLog.length, windowMs: GLOBAL_WINDOW_MS, windowMax: GLOBAL_WINDOW_MAX });
        } catch (_) { /* never break gate */ }
      }
    }
  }

  function checkPerThread(tid, ts) {
    const last = lastPokeTsByThread.get(tid);
    if (typeof last !== 'number') return null;
    if (ts - last < PER_THREAD_THROTTLE_MS) return { allow: false, reason: 'thread_throttled' };
    return null;
  }

  function check(req) {
    const tid = (req && req.threadId) || '';
    const ts = now();
    if (tid) {
      const blocked = checkPerThread(tid, ts);
      if (blocked) return blocked;
    }
    pruneGlobal(ts);
    if (globalLog.length >= GLOBAL_WINDOW_MAX) {
      armed = true;
      return { allow: false, reason: 'global_fuse_blown', fuseBlown: true };
    }
    return { allow: true };
  }

  function recordPoke(req) {
    const tid = (req && req.threadId) || '';
    const ts = now();
    if (tid) lastPokeTsByThread.set(tid, ts);
    globalLog.push(ts);
    logInfo('ui', 'thread_watchdog.poke.recorded', {
      thread_id: tid, global_count: globalLog.length,
    });
  }

  function snapshot() {
    return {
      perThreadCount: lastPokeTsByThread.size,
      globalCount: globalLog.length,
    };
  }

  function _setNowForTest(fn) { if (typeof fn === 'function') now = fn; }

  return { check, recordPoke, snapshot, _setNowForTest };
}

export const _THREAD_WATCHDOG_GATE_CONSTANTS = Object.freeze({
  PER_THREAD_THROTTLE_MS, GLOBAL_WINDOW_MS, GLOBAL_WINDOW_MAX,
});
