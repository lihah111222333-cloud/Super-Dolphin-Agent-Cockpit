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

// Phase 1.8b：识别 5 类永久错误（不应自动重试，应让用户处理）。
// 命中返回 'permanent_<type>'；未命中返回空字符串。
const PERMANENT_ERROR_PATTERNS = Object.freeze([
  { reason: 'permanent_unauthenticated',     match: /401|unauthoriz|invalid api key|invalid_api_key/i },
  { reason: 'permanent_forbidden',           match: /403|forbidden|permission denied/i },
  { reason: 'permanent_quota_exhausted',     match: /quota_exhausted|insufficient_quota|usage limit|out of credits/i },
  { reason: 'permanent_payment_required',    match: /402|payment_required|subscription expired/i },
  { reason: 'permanent_context_length_exceeded', match: /context_length_exceeded|context length exceeded|maximum context|prompt is too long/i },
  // Phase 1.8d fork 前预检失败：worker flush 超时 / handoff 文件不存在；
  // 后端 ui/task/flush_and_verify 抛错时 message 含这俩关键字。重试解决不了
  // "文件不存在" / "writer 卡住"，标 permanent 跳过 retry。
  { reason: 'permanent_handoff_flush_failed', match: /handoff_flush_failed/i },
  { reason: 'permanent_handoff_missing',      match: /handoff_missing/i },
]);

function classifyPermanentError(err) {
  if (!err) return '';
  const obj = (err && typeof err === 'object') ? err : null;
  const msg = (obj && obj.message) ? obj.message : String(err);
  const code = obj && (obj.code != null ? obj.code : obj.status);
  const haystack = String(msg) + ' ' + String(code != null ? code : '');
  for (const p of PERMANENT_ERROR_PATTERNS) {
    if (p.match.test(haystack)) return p.reason;
  }
  return '';
}

// R8 fix：原仅取 err.message。如代码/状态可提取也包进去，便于 debug 后台失败。
function classifyError(err) {
  if (!err) return { error_message: '' };
  const obj = (err && typeof err === 'object') ? err : null;
  const msg = (obj && obj.message) ? obj.message : String(err);
  const out = { error_message: msg.toString().slice(0, 200) };
  const code = obj && (obj.code != null ? obj.code : obj.status);
  if (code != null && code !== '') out.error_code = String(code).slice(0, 50);
  // Phase 1.8b：永久错误识别
  const permanent = classifyPermanentError(err);
  if (permanent) out.permanent_reason = permanent;
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
  // Phase 1.8a：fork 成功后顺手清抑制位（用户意图已被新 thread 替代）。
  if (ctx.manualAbortByThread) {
    const had = ctx.manualAbortByThread.value.has(threadId);
    ctx.manualAbortByThread.value.delete(threadId);
    if (had) ctx.notifyStateChange(threadId);
  }
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
  const firstClass = classifyError(first.error);
  logWarn('ui', 'auto_continue.continue.failed', {
    source_thread_id: threadId, task_id: taskId, kind, retry: false, ...firstClass,
  });
  // Phase 1.8b：永久错误不重试 —— 直接返回 first（带 permanent_reason）让调用方写 reason。
  if (firstClass.permanent_reason) {
    logInfo('ui', 'auto_continue.permanent_error.skip_retry', {
      source_thread_id: threadId, task_id: taskId, reason: firstClass.permanent_reason,
    });
    return first;
  }
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
  if (getCurrentStatus(ctx, threadId) !== "error") return;
  if (ctx.manualAbortByThread && ctx.manualAbortByThread.value.has(threadId)) {
    logInfo("ui", "auto_continue.skipped_manual_abort", { source_thread_id: threadId, task_id: taskId });
    return;
  }
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
  // Phase 1.8a：用户主动点重试 → 抑制位失效。
  if (ctx.manualAbortByThread) {
    const had = ctx.manualAbortByThread.value.has(threadId);
    ctx.manualAbortByThread.value.delete(threadId);
    if (had) ctx.notifyStateChange(threadId);
  }
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
    gate: createAutoContinueGate({
      onFuseRecovered: ({ globalCount, windowMs, windowMax }) => {
        logInfo('ui', 'auto_continue.fuse_recovered', {
          global_count: globalCount,
          window_ms: windowMs,
          window_max: windowMax,
        });
        // 自愈后允许下次跳闸重新 alert（fuseAlertedRef 一次性 reset）。
        ctx.fuseAlertedRef.value = false;
      },
    }),
    prevLevelByThread: new Map(),
    prevStatusByThread: new Map(),
    inflight: new Set(),
    failedRef: ref(new Map()),
    fuseAlertedRef: ref(false),
    manualAbortByThread: ref(new Map()),
    notifyStateChange: null,
  };
  // Phase 1.8a 持久化：state 变化时通知 useAutoContinueStatePersistence；节流写
  // 共享文件由其负责。callsite 走 ctx 内部 helper，避免 mutate 处忘了通知。
  const onStateChange = typeof opts.onStateChange === 'function' ? opts.onStateChange : null;
  ctx.notifyStateChange = (tid) => {
    if (!onStateChange || !tid) return;
    try { onStateChange(tid); } catch (_) { /* never break interrupt path */ }
  };
  primeState(ctx);
  const stopToken = watchTokenLevel(ctx);
  const stopStatus = watchStatus(ctx);
  return {
    stop() { stopToken(); stopStatus(); },
    failedAutoContinueByThread: ctx.failedRef,
    manualAbortByThread: ctx.manualAbortByThread,
    retryAutoContinue: (threadId) => userRetry(ctx, threadId),
    // Phase 1.8a：让外部（stopThread / startThread / fork）显式标记或清除抑制位。
    // value 形状升级到 { at, source } 以便持久化；旧 .has(threadId) 检查不受影响。
    markManualAbort: (threadId, source) => {
      const id = (threadId || '').toString().trim();
      if (!id) return;
      const src = (source || 'ui_stop').toString();
      ctx.manualAbortByThread.value.set(id, { at: Date.now(), source: src });
      logInfo('ui', 'auto_continue.manual_abort_marked', { thread_id: id, source: src });
      ctx.notifyStateChange(id);
    },
    clearManualAbort: (threadId) => {
      const id = (threadId || '').toString().trim();
      if (!id) return;
      ctx.manualAbortByThread.value.delete(id);
      ctx.notifyStateChange(id);
    },
  };
}
