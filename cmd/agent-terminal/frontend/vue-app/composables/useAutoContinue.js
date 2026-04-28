// @ts-nocheck
// Phase 1.3 自动续接调度器（仅日志骨架）
// watch 所有 task thread 的 token level + status；跨入 critical / 跨入 error 时
// emit auto_continue.signal 日志事件。Phase 1.4 才接通真实动作（compact / recover / continueTaskById）。

import { watch } from '../../lib/vue.esm-browser.prod.js';
import { logInfo } from '../services/log.js';
import { getTokenLevel } from '../utils/format-utils.js';
import { useContextUsageThresholds } from './useContextUsageThresholds.js';

/**
 * 取 thread 的 taskId（trim 后空字符串视为非 task thread）。
 * @param {object} runtime
 * @returns {string}
 */
function getTaskId(runtime) {
  const tid = (runtime && runtime.taskId) || '';
  const s = typeof tid === 'string' ? tid : String(tid);
  return s.trim();
}

/**
 * 安装 token level / status 跨阈值监听器。Phase 1.3 仅 logInfo，不调动作。
 *
 * @param {object} opts
 * @param {object} opts.threadStore  含 state.tokenUsageByThread / state.statuses / state.agentRuntimeById
 * @returns {{ stop: () => void }}  测试用 stop 清理；UnifiedChatPage 不必调用（页面常驻）
 */
export function useAutoContinue({ threadStore }) {
  const thresholds = useContextUsageThresholds();
  const prevLevelByThread = new Map();
  const prevStatusByThread = new Map();

  // 启动 prime：把当前状态记入 prev，避免应用启动当成"跨入"误报。
  const initialUsage = (threadStore?.state?.tokenUsageByThread) || {};
  for (const tid of Object.keys(initialUsage)) {
    prevLevelByThread.set(tid, getTokenLevel(initialUsage[tid], thresholds.value));
  }
  const initialStatus = (threadStore?.state?.statuses) || {};
  for (const tid of Object.keys(initialStatus)) {
    prevStatusByThread.set(tid, initialStatus[tid]);
  }

  const stopToken = watch(
    () => threadStore?.state?.tokenUsageByThread,
    (next) => {
      if (!next || typeof next !== 'object') return;
      const runtimeMap = (threadStore?.state?.agentRuntimeById) || {};
      for (const threadId of Object.keys(next)) {
        const newLevel = getTokenLevel(next[threadId], thresholds.value);
        const prev = prevLevelByThread.get(threadId);
        prevLevelByThread.set(threadId, newLevel);
        if (prev === newLevel) continue;
        if (newLevel !== 'critical') continue;
        const taskId = getTaskId(runtimeMap[threadId]);
        if (!taskId) continue;
        logInfo('ui', 'auto_continue.signal', {
          source_thread_id: threadId,
          task_id: taskId,
          kind: 'token_critical',
          level: newLevel,
        });
      }
    },
    { deep: true },
  );

  const stopStatus = watch(
    () => threadStore?.state?.statuses,
    (next) => {
      if (!next || typeof next !== 'object') return;
      const runtimeMap = (threadStore?.state?.agentRuntimeById) || {};
      for (const threadId of Object.keys(next)) {
        const status = next[threadId];
        const prev = prevStatusByThread.get(threadId);
        prevStatusByThread.set(threadId, status);
        if (prev === status) continue;
        if (status !== 'error') continue;
        const taskId = getTaskId(runtimeMap[threadId]);
        if (!taskId) continue;
        logInfo('ui', 'auto_continue.signal', {
          source_thread_id: threadId,
          task_id: taskId,
          kind: 'status_error',
          status,
        });
      }
    },
    { deep: true },
  );

  return {
    stop() {
      stopToken();
      stopStatus();
    },
  };
}
