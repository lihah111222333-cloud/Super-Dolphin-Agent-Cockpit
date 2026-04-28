// @ts-nocheck
// Phase 1.4a 自动续接频率限制（精简版 / 闭包状态）
// 仅 2 类闸门 —— 真实失控防御已足够，详见 docs/plans/自动化.md §4 review。
//   1) per-source-thread 1 次：防止同一 thread 反复被自动 fork 死循环
//   2) 全局保险丝 1min/20 次：防止任何级联 / bug 导致的雪崩
// 偏好开关在 useAutoContinuePref（Phase 1.5），不在本模块。
// 全局保险丝触发时仅返回 fuseBlown:true，不调 UI 动作（留给 useAutoContinue 的回调）。

const GLOBAL_WINDOW_MS = 60 * 1000;
const GLOBAL_WINDOW_MAX = 20;

/**
 * 创建自动续接频率闸门。状态闭包私有，多 useAutoContinue 实例需各自创建。
 *
 * @returns {{
 *   check: (req: { kind: 'continue'|'recover', threadId: string }) =>
 *     { allow: boolean, reason?: string, fuseBlown?: boolean },
 *   recordContinue: (req: { threadId: string }) => void,
 *   recordRecover: (req: { threadId: string }) => void,
 *   snapshot: () => object,
 *   _setNowForTest: (fn: () => number) => void,
 * }}
 */
export function createAutoContinueGate() {
  const continuedSourceThreadIds = new Set();   // per-thread 1 次
  const globalLog = [];                          // 全局保险丝时间戳

  let now = () => Date.now();

  function pruneGlobal(currentTs) {
    const cutoff = currentTs - GLOBAL_WINDOW_MS;
    while (globalLog.length > 0 && globalLog[0] < cutoff) globalLog.shift();
  }

  function check(req) {
    const kind = req && req.kind;
    const threadId = (req && req.threadId) || '';
    const ts = now();

    if (kind === 'continue' && continuedSourceThreadIds.has(threadId)) {
      return { allow: false, reason: 'thread_already_continued' };
    }
    pruneGlobal(ts);
    if (globalLog.length >= GLOBAL_WINDOW_MAX) {
      return { allow: false, reason: 'global_fuse_blown', fuseBlown: true };
    }
    return { allow: true };
  }

  function recordContinue(req) {
    const threadId = (req && req.threadId) || '';
    if (threadId) continuedSourceThreadIds.add(threadId);
    globalLog.push(now());
  }

  // Phase 1.4c review fix (R1)：自然治愈路径下回滚 per-thread 闸，
  // 但保留 globalLog 中的记录（pre-reservation 防并发跳闸仍有意义）。
  function releaseThreadContinue(req) {
    const threadId = (req && req.threadId) || '';
    if (threadId) continuedSourceThreadIds.delete(threadId);
  }

  function recordRecover(_req) {
    globalLog.push(now());
  }

  function snapshot() {
    return {
      continuedThreads: continuedSourceThreadIds.size,
      globalCount: globalLog.length,
    };
  }

  function _setNowForTest(fn) {
    if (typeof fn === 'function') now = fn;
  }

  return { check, recordContinue, releaseThreadContinue, recordRecover, snapshot, _setNowForTest };
}

export const _AUTO_CONTINUE_GATE_CONSTANTS = Object.freeze({
  GLOBAL_WINDOW_MS, GLOBAL_WINDOW_MAX,
});
