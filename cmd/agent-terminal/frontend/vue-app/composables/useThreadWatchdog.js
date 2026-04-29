// @ts-nocheck
// Phase 1.7b 事件停滞检测（thread watchdog）
//
// 来源：docs/plans/自动化.md §3.6 风险 6（已实证 · 唯一有用户实证的痛点）。
// 独立于 useAutoContinue 链路：监所有 thread（不只 task），检测后端事件源停滞。
//
// 触发条件：
//   status ∈ working类（thinking/responding/running/editing/syncing） &&
//   now - lastEventTsByThread[tid] > STALL_THRESHOLD_MS
//
// 双分流：
//   有 runtime.taskId（task thread）→ 自动调 sendMessage(tid, "继续") + log task_auto
//   无 taskId（普通对话）→ 写 stuckByThread.set(tid, now) 留给 1.7d banner 渲染
//
// 触发后立即 lastEventTsByThread[tid] = now 重置（节流闸：60s 内不会再戳同 thread）。
// 闸门 / 偏好 / 累计兜底由后续 1.7c / 1.7e / 1.7f 加。

import { ref } from '../../lib/vue.esm-browser.prod.js';
import { logInfo } from '../services/log.js';
import { createThreadWatchdogGate } from './thread-watchdog-gating.js';
import { useThreadWatchdogPref } from './useThreadWatchdogPref.js';

const WORKING_STATUS_SET = new Set(['thinking', 'responding', 'running', 'editing', 'syncing']);
const SCAN_INTERVAL_MS = 60_000;
const STALL_THRESHOLD_MS = 180_000;

function isWorkingStatus(status) {
  return WORKING_STATUS_SET.has((status || '').toString().toLowerCase());
}

function safeSendMessage(sendMessage, tid) {
  if (!sendMessage) return;
  try {
    const ret = sendMessage(tid, '继续');
    if (ret && typeof ret.catch === 'function') ret.catch(() => { /* swallow */ });
  } catch (_) { /* never break scan */ }
}

export function useThreadWatchdog(opts = {}) {
  const threadStore = opts.threadStore || null;
  const sendMessage = typeof opts.sendMessage === 'function' ? opts.sendMessage : null;
  const stuckByThread = ref(new Map());

  let timer = null;
  let now = () => Date.now();
  let scanIntervalMs = SCAN_INTERVAL_MS;
  let stallThresholdMs = STALL_THRESHOLD_MS;

  // 1.7c：watchdog 自身的节流闸门（per-thread 60s/1 + 全局 5min/10）
  const gate = createThreadWatchdogGate({
    onFuseRecovered: ({ globalCount, windowMs, windowMax }) => {
      logInfo('ui', 'thread_watchdog.fuse_recovered', {
        global_count: globalCount, window_ms: windowMs, window_max: windowMax,
      });
    },
  });
  // 1.7c：偏好 ref（模块单例，直接读 .value）。允许 opts 注入便于单测。
  const prefRef = opts.prefRef || useThreadWatchdogPref();

  function pokeTaskThread(tid, taskId, elapsed) {
    logInfo('ui', 'thread_watchdog.poke.task_auto', {
      thread_id: tid, task_id: taskId, stall_ms: elapsed,
    });
    safeSendMessage(sendMessage, tid);
  }

  function markStuck(tid, ts, elapsed) {
    logInfo('ui', 'thread_watchdog.stuck', { thread_id: tid, stall_ms: elapsed });
    stuckByThread.value.set(tid, ts);
  }

  function processThread(tid, lastTs, ts) {
    if (!lastTs) return;
    const status = (threadStore.state.statuses || {})[tid];
    if (!isWorkingStatus(status)) return;
    const elapsed = ts - lastTs;
    if (elapsed <= stallThresholdMs) return;
    threadStore.state.lastEventTsByThread[tid] = ts;
    if (!prefRef.value) {
      logInfo('ui', 'thread_watchdog.skipped_by_pref', { thread_id: tid });
      return;
    }
    const gateResult = gate.check({ threadId: tid });
    if (!gateResult.allow) {
      logInfo('ui', 'thread_watchdog.skipped_by_gate', {
        thread_id: tid, reason: gateResult.reason, fuse_blown: Boolean(gateResult.fuseBlown),
      });
      return;
    }
    gate.recordPoke({ threadId: tid });
    const runtime = (threadStore.state.agentRuntimeById || {})[tid] || {};
    const taskId = (runtime.taskId || '').toString().trim();
    if (taskId) pokeTaskThread(tid, taskId, elapsed);
    else markStuck(tid, ts, elapsed);
  }

  function scan() {
    if (!threadStore || !threadStore.state) return;
    const ts = now();
    const lastMap = threadStore.state.lastEventTsByThread || {};
    for (const tid of Object.keys(lastMap)) {
      processThread(tid, Number(lastMap[tid]) || 0, ts);
    }
  }

  function start() {
    if (timer != null) return;
    timer = setInterval(scan, scanIntervalMs);
  }
  function stop() {
    if (timer != null) { clearInterval(timer); timer = null; }
  }

  return {
    stuckByThread,
    start,
    stop,
    _setNowForTest: (fn) => { if (typeof fn === 'function') now = fn; },
    _scanForTest: () => scan(),
    _setIntervalsForTest: ({ scanMs, stallMs } = {}) => {
      if (typeof scanMs === 'number') scanIntervalMs = scanMs;
      if (typeof stallMs === 'number') stallThresholdMs = stallMs;
    },
  };
}

export const _USE_THREAD_WATCHDOG_CONSTANTS = Object.freeze({
  WORKING_STATUS_SET,
  SCAN_INTERVAL_MS,
  STALL_THRESHOLD_MS,
});
