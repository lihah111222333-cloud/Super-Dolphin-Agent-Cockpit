// @ts-nocheck
// Phase 1.4c 自动续接调度器（接通真实动作链路）
//
// 决策（详见 docs/plans/自动化.md §3）：
//   token_critical → canCompact ? compactThread 优先 + fork 兜底 : 直接 fork
//   status_error   → recoverThread 优先 + fork 兜底
// 兜底 fork 带：retry 1 次（1.5s 延迟）+ retry 前 healthcheck（thread 已自然恢复则跳过）
//
// 闸门：auto-continue-gating.js（per-thread 1 次 + 全局保险丝 1min/20）
// 失败：写入 failedAutoContinueByThread（暴露给 1.4d ContextUsageBanner 渲染重试入口）
// 全局保险丝：logError + window.alert（一次性，相同 reason 不重弹）
// 偏好开关在 useAutoContinuePref（Phase 1.5），调度器不读 prefs。

import { ref, watch } from '../../lib/vue.esm-browser.prod.js';
import { logInfo, logWarn, logError } from '../services/log.js';
import { getTokenLevel } from '../utils/format-utils.js';
import { useContextUsageThresholds } from './useContextUsageThresholds.js';
import { useAutoContinuePref, useAutoContinuePrefReady } from './useAutoContinuePref.js';
import { createAutoContinueGate } from './auto-continue-gating.js';

const FORK_RETRY_DELAY_MS = 1500;
const FUSE_ALERT_MESSAGE = '自动续接保险丝触发：1 分钟内已发起 20 次自动续接，已暂停。请检查后台任务状态后手动处理。';

function getTaskId(runtime) {
  const tid = (runtime && runtime.taskId) || '';
  const s = typeof tid === 'string' ? tid : String(tid);
  return s.trim();
}

function getCapabilities(runtime) {
  const caps = runtime && runtime.capabilities;
  if (!Array.isArray(caps)) return [];
  return caps.map((c) => (c || '').toString().trim().toLowerCase()).filter(Boolean);
}

// R8 fix：原仅取 err.message。如代码/状态可提取也包进去，便于 debug 后台失败。
function classifyError(err) {
  if (!err) return { error_message: '' };
  const obj = (err && typeof err === 'object') ? err : null;
  const msg = (obj && obj.message) ? obj.message : String(err);
  const out = { error_message: msg.toString().slice(0, 200) };
  const code = obj && (obj.code != null ? obj.code : obj.status);
  if (code != null && code !== '') out.error_code = String(code).slice(0, 50);
  return out;
}

// R7 fix：清理孤儿 thread 条目（防 Map 无限增长）。next 是当前 store 中的有效 key 集。
function cleanupOrphanKeys(map, next) {
  if (!next || typeof next !== 'object') return;
  for (const key of map.keys()) {
    if (!Object.prototype.hasOwnProperty.call(next, key)) map.delete(key);
  }
}

function recordFailure(ctx, threadId, info) {
  const next = new Map(ctx.failedRef.value);
  next.set(threadId, { ts: Date.now(), ...info });
  ctx.failedRef.value = next;
}

function clearFailure(ctx, threadId) {
  if (!ctx.failedRef.value.has(threadId)) return;
  const next = new Map(ctx.failedRef.value);
  next.delete(threadId);
  ctx.failedRef.value = next;
}

function getCurrentLevel(ctx, threadId) {
  return getTokenLevel(ctx.threadStore?.state?.tokenUsageByThread?.[threadId], ctx.thresholds.value);
}

function getCurrentStatus(ctx, threadId) {
  return ctx.threadStore?.state?.statuses?.[threadId];
}

function maybeAlertFuse(ctx, reason) {
  if (ctx.fuseAlertedRef.value) return;
  ctx.fuseAlertedRef.value = true;
  logError('ui', 'auto_continue.fuse_blown', { reason });
  try { ctx.alertFn(FUSE_ALERT_MESSAGE); } catch (_e) { /* ignore alert errors */ }
}

function checkContinueGate(ctx, threadId, taskId, kind) {
  const result = ctx.gate.check({ kind: 'continue', threadId });
  if (!result.allow) {
    if (result.fuseBlown) maybeAlertFuse(ctx, result.reason);
    logWarn('ui', 'auto_continue.gated', {
      source_thread_id: threadId, task_id: taskId, kind, gate_kind: 'continue', reason: result.reason,
    });
    recordFailure(ctx, threadId, {
      kind, last_action: 'continue', reason: 'gated_' + result.reason, error_message: '',
    });
  }
  return result;
}

async function attemptFork(ctx, threadId) {
  try {
    const newId = await ctx.continueTaskById(threadId);
    if (newId) return { ok: true, newId };
    return { ok: false, error: new Error('continueTaskById returned empty id') };
  } catch (err) {
    return { ok: false, error: err };
  }
}

async function forkWithRetry(ctx, threadId, taskId, kind, stillNeedsContinue) {
  const first = await attemptFork(ctx, threadId);
  if (first.ok) {
    logInfo('ui', 'auto_continue.continue.done', {
      source_thread_id: threadId, task_id: taskId, kind, retry: false, next_thread_id: first.newId,
    });
    return first;
  }
  logWarn('ui', 'auto_continue.continue.failed', {
    source_thread_id: threadId, task_id: taskId, kind, retry: false, ...classifyError(first.error),
  });
  await ctx.sleepFn(FORK_RETRY_DELAY_MS);
  if (!stillNeedsContinue()) {
    logInfo('ui', 'auto_continue.skipped_after_recovered', {
      source_thread_id: threadId, task_id: taskId, kind, after: 'fork_retry_wait',
    });
    return { ok: true, recovered: true };
  }
  const second = await attemptFork(ctx, threadId);
  if (second.ok) {
    logInfo('ui', 'auto_continue.continue.done', {
      source_thread_id: threadId, task_id: taskId, kind, retry: true, next_thread_id: second.newId,
    });
    return second;
  }
  logError('ui', 'auto_continue.continue.failed', {
    source_thread_id: threadId, task_id: taskId, kind, retry: true, ...classifyError(second.error),
  });
  return second;
}

async function tryCompact(ctx, threadId, taskId) {
  try {
    await ctx.threadStore.compactThread(threadId);
    clearFailure(ctx, threadId);
    logInfo('ui', 'auto_continue.compact.done', { source_thread_id: threadId, task_id: taskId });
    return { ok: true };
  } catch (err) {
    logWarn('ui', 'auto_continue.compact.failed', {
      source_thread_id: threadId, task_id: taskId, ...classifyError(err),
    });
    return { ok: false, error: err };
  }
}

async function handleTokenCritical(ctx, threadId, taskId) {
  if (ctx.inflight.has(threadId)) return;
  if (getCurrentLevel(ctx, threadId) !== 'critical') return;
  const gateResult = checkContinueGate(ctx, threadId, taskId, 'token_critical');
  if (!gateResult.allow) return;
  ctx.inflight.add(threadId);
  try {
    const runtime = ctx.threadStore?.state?.agentRuntimeById?.[threadId];
    const canCompact = getCapabilities(runtime).includes('context_compact')
      && typeof ctx.threadStore?.compactThread === 'function';
    if (canCompact) {
      const c = await tryCompact(ctx, threadId, taskId);
      if (c.ok) return;
      if (getCurrentLevel(ctx, threadId) !== 'critical') {
        logInfo('ui', 'auto_continue.skipped_after_recovered', {
          source_thread_id: threadId, task_id: taskId, kind: 'token_critical', after: 'compact_failure',
        });
        return;
      }
    }
    // Pre-reservation：fork 发起前先计费（防并发 21 thread 同时跳闸）。
    // R1 fix：自然治愈路径下回滚 per-thread 闸，避免 “未真 fork 但 thread 被锁”。
    ctx.gate.recordContinue({ threadId });
    const fork = await forkWithRetry(ctx, threadId, taskId, 'token_critical',
      () => getCurrentLevel(ctx, threadId) === 'critical');
    if (fork.ok) {
      if (fork.recovered) ctx.gate.releaseThreadContinue({ threadId });
      clearFailure(ctx, threadId);
      return;
    }
    recordFailure(ctx, threadId, {
      kind: 'token_critical', last_action: 'continue',
      reason: canCompact ? 'compact_then_continue_failed' : 'continue_failed',
      ...classifyError(fork.error),
    });
  } finally {
    ctx.inflight.delete(threadId);
  }
}

async function tryRecover(ctx, threadId, taskId) {
  const recoverGate = ctx.gate.check({ kind: 'recover', threadId });
  if (!recoverGate.allow) {
    if (recoverGate.fuseBlown) maybeAlertFuse(ctx, recoverGate.reason);
    logWarn('ui', 'auto_continue.gated', {
      source_thread_id: threadId, task_id: taskId, kind: 'status_error',
      gate_kind: 'recover', reason: recoverGate.reason,
    });
    if (recoverGate.fuseBlown) {
      recordFailure(ctx, threadId, {
        kind: 'status_error', last_action: 'recover',
        reason: 'gated_' + recoverGate.reason, error_message: '',
      });
      return { handled: true };
    }
    return { handled: false };
  }
  // R4 fix：pre-reservation——check 通过后立即 record，避免 21 个并发 recover 同时 check时 globalLog 还是空。
  ctx.gate.recordRecover({ threadId });
  try {
    await ctx.threadStore.recoverThread(threadId);
    clearFailure(ctx, threadId);
    logInfo('ui', 'auto_continue.recover.done', { source_thread_id: threadId, task_id: taskId });
    return { handled: true };
  } catch (err) {
    logWarn('ui', 'auto_continue.recover.failed', {
      source_thread_id: threadId, task_id: taskId, ...classifyError(err),
    });
    return { handled: false };
  }
}

async function handleStatusError(ctx, threadId, taskId) {
  if (ctx.inflight.has(threadId)) return;
  if (getCurrentStatus(ctx, threadId) !== 'error') return;
  ctx.inflight.add(threadId);
  try {
    if (typeof ctx.threadStore?.recoverThread === 'function') {
      const r = await tryRecover(ctx, threadId, taskId);
      if (r.handled) return;
      if (getCurrentStatus(ctx, threadId) !== 'error') {
        logInfo('ui', 'auto_continue.skipped_after_recovered', {
          source_thread_id: threadId, task_id: taskId, kind: 'status_error', after: 'recover_failure',
        });
        return;
      }
    }
    const continueGate = checkContinueGate(ctx, threadId, taskId, 'status_error');
    if (!continueGate.allow) return;
    ctx.gate.recordContinue({ threadId });
    const fork = await forkWithRetry(ctx, threadId, taskId, 'status_error',
      () => getCurrentStatus(ctx, threadId) === 'error');
    if (fork.ok) {
      if (fork.recovered) ctx.gate.releaseThreadContinue({ threadId });
      clearFailure(ctx, threadId);
      return;
    }
    recordFailure(ctx, threadId, {
      kind: 'status_error', last_action: 'continue',
      reason: 'recover_then_continue_failed', ...classifyError(fork.error),
    });
  } finally {
    ctx.inflight.delete(threadId);
  }
}

function dispatch(handler, ctx, threadId, taskId) {
  handler(ctx, threadId, taskId).catch((err) => {
    logError('ui', 'auto_continue.handler_uncaught', {
      source_thread_id: threadId, task_id: taskId, ...classifyError(err),
    });
  });
}

function watchTokenLevel(ctx) {
  return watch(
    () => ctx.threadStore?.state?.tokenUsageByThread,
    (next) => {
      if (!next || typeof next !== 'object') return;
      cleanupOrphanKeys(ctx.prevLevelByThread, next); // R7 fix
      const runtimeMap = (ctx.threadStore?.state?.agentRuntimeById) || {};
      for (const threadId of Object.keys(next)) {
        const newLevel = getTokenLevel(next[threadId], ctx.thresholds.value);
        const prev = ctx.prevLevelByThread.get(threadId);
        ctx.prevLevelByThread.set(threadId, newLevel);
        if (prev === newLevel) continue;
        if (newLevel !== 'critical') continue;
        const taskId = getTaskId(runtimeMap[threadId]);
        if (!taskId) continue;
        if (!ctx.prefReadyRef.value) continue; // R6 fix：偏好未 load 完不触发（避免 default 误触发）
        if (!ctx.prefRef.value) continue; // Phase 1.5：偏好关 → 跳过整条链
        logInfo('ui', 'auto_continue.signal', {
          source_thread_id: threadId, task_id: taskId, kind: 'token_critical', level: newLevel,
        });
        dispatch(handleTokenCritical, ctx, threadId, taskId);
      }
    },
    { deep: true },
  );
}

function watchStatus(ctx) {
  return watch(
    () => ctx.threadStore?.state?.statuses,
    (next) => {
      if (!next || typeof next !== 'object') return;
      cleanupOrphanKeys(ctx.prevStatusByThread, next); // R7 fix
      const runtimeMap = (ctx.threadStore?.state?.agentRuntimeById) || {};
      for (const threadId of Object.keys(next)) {
        const status = next[threadId];
        const prev = ctx.prevStatusByThread.get(threadId);
        ctx.prevStatusByThread.set(threadId, status);
        if (prev === status) continue;
        if (status !== 'error') continue;
        const taskId = getTaskId(runtimeMap[threadId]);
        if (!taskId) continue;
        if (!ctx.prefReadyRef.value) continue; // R6 fix
        if (!ctx.prefRef.value) continue;
        logInfo('ui', 'auto_continue.signal', {
          source_thread_id: threadId, task_id: taskId, kind: 'status_error', status,
        });
        dispatch(handleStatusError, ctx, threadId, taskId);
      }
    },
    { deep: true },
  );
}

function primeState(ctx) {
  const initialUsage = (ctx.threadStore?.state?.tokenUsageByThread) || {};
  for (const tid of Object.keys(initialUsage)) {
    ctx.prevLevelByThread.set(tid, getTokenLevel(initialUsage[tid], ctx.thresholds.value));
  }
  const initialStatus = (ctx.threadStore?.state?.statuses) || {};
  for (const tid of Object.keys(initialStatus)) {
    ctx.prevStatusByThread.set(tid, initialStatus[tid]);
  }
}

async function userRetry(ctx, threadId) {
  const id = (threadId || '').toString().trim();
  if (!id) return '';
  if (typeof ctx.continueTaskById !== 'function') {
    throw new Error('continueTaskById not injected');
  }
  logInfo('ui', 'auto_continue.user_retry.start', { source_thread_id: id });
  try {
    const newId = await ctx.continueTaskById(id);
    if (newId) {
      clearFailure(ctx, id);
      logInfo('ui', 'auto_continue.user_retry.done', { source_thread_id: id, next_thread_id: newId });
    }
    return newId;
  } catch (err) {
    logWarn('ui', 'auto_continue.user_retry.failed', { source_thread_id: id, ...classifyError(err) });
    throw err;
  }
}

/**
 * @param {object} opts
 * @param {object} opts.threadStore
 * @param {Function} opts.continueTaskById  来自 useTaskHandoff
 * @param {Function} [opts.alertFn]
 * @param {Function} [opts.sleepFn]
 */
export function useAutoContinue(opts) {
  const ctx = {
    threadStore: opts.threadStore,
    continueTaskById: opts.continueTaskById,
    alertFn: opts.alertFn || ((msg) => {
      if (typeof window !== 'undefined' && typeof window.alert === 'function') window.alert(msg);
    }),
    sleepFn: opts.sleepFn || ((ms) => new Promise((r) => setTimeout(r, ms))),
    thresholds: useContextUsageThresholds(),
    prefRef: useAutoContinuePref(),
    prefReadyRef: useAutoContinuePrefReady(),
    gate: createAutoContinueGate(),
    prevLevelByThread: new Map(),
    prevStatusByThread: new Map(),
    inflight: new Set(),
    failedRef: ref(new Map()),
    fuseAlertedRef: ref(false),
  };
  primeState(ctx);
  const stopToken = watchTokenLevel(ctx);
  const stopStatus = watchStatus(ctx);
  return {
    stop() { stopToken(); stopStatus(); },
    failedAutoContinueByThread: ctx.failedRef,
    retryAutoContinue: (threadId) => userRetry(ctx, threadId),
  };
}
