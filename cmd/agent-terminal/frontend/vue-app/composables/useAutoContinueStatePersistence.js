// @ts-nocheck
// Phase 1.7f / 1.8a · auto-continue state 持久化协调
//
// 通过 Phase 1.6 ship 的专用 UI RPC 把以下 thread 级 state 写入共享文件
// `_internal/auto-continue/state/<threadId>.json`，让刷新页面 / 多 tab 一致：
//   - manualAbortAt / manualAbortSource（1.8a · 用户主动 stop 抑制位）
//   - watchdogPokeCount（1.7f · watchdog 累计戳次数）
//
// 设计：
//   - 加载：lazy（thread 第一次被关注时拉一次），inflight 去重
//   - 写入：per-thread 5s 节流（合并写一次 upsert）
//   - 删除：当 state 变全空（abort + count 都是空）时调 delete 而非写空文件
//   - schemaVersion=1（与后端 RPC 的硬约束一致）

import { callAPI as defaultCallAPI } from '../services/api.js';
import { logInfo, logWarn } from '../services/log.js';

const SCHEMA_VERSION = 1;
const PATH_PREFIX = '_internal/auto-continue/state/';
const DEFAULT_WRITE_THROTTLE_MS = 5_000;

function buildPath(threadId) {
  return PATH_PREFIX + threadId + '.json';
}

function isNotFoundError(err) {
  const msg = ((err && err.message) || String(err || '')).toLowerCase();
  return msg.includes('not found') || msg.includes('not configured') || msg.includes('no rows');
}

export function useAutoContinueStatePersistence(opts = {}) {
  // opts.getStateSnapshot(threadId) -> { manualAbortAt, manualAbortSource, watchdogPokeCount }
  // opts.applyStateSnapshot(threadId, snapshot) -> void
  // opts.callAPIFn / opts.writeThrottleMs / opts.timerImpl 测试注入。
  const callAPIFn = typeof opts.callAPIFn === 'function' ? opts.callAPIFn : defaultCallAPI;
  const getStateSnapshot = typeof opts.getStateSnapshot === 'function' ? opts.getStateSnapshot : null;
  const applyStateSnapshot = typeof opts.applyStateSnapshot === 'function' ? opts.applyStateSnapshot : null;
  const writeThrottleMs = typeof opts.writeThrottleMs === 'number' ? opts.writeThrottleMs : DEFAULT_WRITE_THROTTLE_MS;
  const setTimerFn = (opts.timerImpl && typeof opts.timerImpl.set === 'function') ? opts.timerImpl.set : setTimeout;
  const clearTimerFn = (opts.timerImpl && typeof opts.timerImpl.clear === 'function') ? opts.timerImpl.clear : clearTimeout;

  const pendingTimers = new Map();   // threadId -> timer handle
  const loadedThreads = new Set();   // threadId 已加载（含「文件不存在」标记）
  const inflightLoads = new Map();   // threadId -> Promise

  function parseStateContent(tid, content) {
    if (!content) return null;
    let parsed = null;
    try { parsed = JSON.parse(content); }
    catch (_) {
      logWarn('ui', 'auto_continue.state.load_invalid_json', { thread_id: tid });
      return null;
    }
    if (!parsed || parsed.schemaVersion !== SCHEMA_VERSION || parsed.threadId !== tid) {
      logWarn('ui', 'auto_continue.state.load_schema_mismatch', { thread_id: tid });
      return null;
    }
    return parsed;
  }

  function applySnapshotSafely(tid, parsed) {
    if (!applyStateSnapshot) return;
    try { applyStateSnapshot(tid, parsed); }
    catch (err) {
      logWarn('ui', 'auto_continue.state.apply_failed', {
        thread_id: tid, error: (err && err.message) || String(err),
      });
    }
  }

  async function fetchAndApplyState(tid) {
    const path = buildPath(tid);
    try {
      const detail = await callAPIFn('ui/auto-continue/state/get', { path });
      const content = ((detail && detail.content) || '').toString();
      const parsed = parseStateContent(tid, content);
      loadedThreads.add(tid);
      if (!parsed) return null;
      applySnapshotSafely(tid, parsed);
      logInfo('ui', 'auto_continue.state.loaded', {
        thread_id: tid,
        manual_abort: Boolean(parsed.manualAbortAt),
        watchdog_poke_count: Number(parsed.watchdogPokeCount) || 0,
      });
      return parsed;
    } catch (err) {
      if (isNotFoundError(err)) {
        loadedThreads.add(tid);
        return null;
      }
      logWarn('ui', 'auto_continue.state.load_failed', {
        thread_id: tid, error: (err && err.message) || String(err),
      });
      return null;
    } finally {
      inflightLoads.delete(tid);
    }
  }

  async function loadStateForThread(threadId) {
    const tid = (threadId || '').toString().trim();
    if (!tid) return null;
    if (loadedThreads.has(tid)) return null;
    if (inflightLoads.has(tid)) return inflightLoads.get(tid);
    const promise = fetchAndApplyState(tid);
    inflightLoads.set(tid, promise);
    return promise;
  }

  function flushWrite(threadId) {
    const tid = (threadId || '').toString().trim();
    if (!tid || !getStateSnapshot) return;
    let snapshot = null;
    try { snapshot = getStateSnapshot(tid); }
    catch (err) {
      logWarn('ui', 'auto_continue.state.snapshot_failed', {
        thread_id: tid, error: (err && err.message) || String(err),
      });
      return;
    }
    const manualAbortAt = (snapshot && Number(snapshot.manualAbortAt)) || 0;
    const watchdogPokeCount = (snapshot && Number(snapshot.watchdogPokeCount)) || 0;
    const path = buildPath(tid);
    if (!manualAbortAt && !watchdogPokeCount) {
      // 全空：不写空文件，调 delete 让 _internal/ 整洁。
      Promise.resolve(callAPIFn('ui/auto-continue/state/delete', { path })).catch((err) => {
        if (isNotFoundError(err)) return; // 不存在就不存在
        logWarn('ui', 'auto_continue.state.delete_failed', {
          thread_id: tid, error: (err && err.message) || String(err),
        });
      });
      return;
    }
    const payload = {
      schemaVersion: SCHEMA_VERSION,
      threadId: tid,
      manualAbortAt: manualAbortAt || null,
      manualAbortSource: (snapshot && snapshot.manualAbortSource) || null,
      watchdogPokeCount,
      lastUpdatedAt: Date.now(),
    };
    Promise.resolve(callAPIFn('ui/auto-continue/state/upsert', {
      path,
      threadId: tid,
      content: JSON.stringify(payload),
    })).catch((err) => {
      logWarn('ui', 'auto_continue.state.write_failed', {
        thread_id: tid, error: (err && err.message) || String(err),
      });
    });
  }

  function scheduleWrite(threadId) {
    const tid = (threadId || '').toString().trim();
    if (!tid) return;
    if (pendingTimers.has(tid)) return; // 已有 timer 在排队，本周期内合并
    const handle = setTimerFn(() => {
      pendingTimers.delete(tid);
      flushWrite(tid);
    }, writeThrottleMs);
    pendingTimers.set(tid, handle);
  }

  function cancelPending(threadId) {
    const tid = (threadId || '').toString().trim();
    if (pendingTimers.has(tid)) {
      try { clearTimerFn(pendingTimers.get(tid)); } catch (_) { /* swallow */ }
      pendingTimers.delete(tid);
    }
  }

  function clearStateForThread(threadId) {
    const tid = (threadId || '').toString().trim();
    if (!tid) return;
    cancelPending(tid);
    loadedThreads.delete(tid);
    Promise.resolve(callAPIFn('ui/auto-continue/state/delete', { path: buildPath(tid) })).catch(() => { /* swallow */ });
  }

  function disposeAll() {
    for (const handle of pendingTimers.values()) {
      try { clearTimerFn(handle); } catch (_) { /* swallow */ }
    }
    pendingTimers.clear();
  }

  return {
    loadStateForThread,
    scheduleWrite,
    clearStateForThread,
    disposeAll,
    _flushNowForTest: (tid) => flushWrite(tid),
    _hasPendingForTest: (tid) => pendingTimers.has((tid || '').toString().trim()),
    _isLoadedForTest: (tid) => loadedThreads.has((tid || '').toString().trim()),
  };
}

export const _AUTO_CONTINUE_STATE_PERSISTENCE_CONSTANTS = Object.freeze({
  SCHEMA_VERSION,
  PATH_PREFIX,
  DEFAULT_WRITE_THROTTLE_MS,
});
