import { ref } from '../../lib/vue.esm-browser.prod.js';
import { callAPI } from '../services/api.js';
import { logInfo, logWarn } from '../services/log.js';

// Phase 2.2 · 普通对话升级为自动化任务（promote-task）的前端 composable。
//
// 单一职责：包裹 ui/thread/promote-task RPC，暴露 promoting busy ref +
// promoteTaskFromThread(threadId) action。Phase 2.2a 配置面板 toggle 与
// Phase 2.2b banner 卡住升级按钮共用此 composable，避免每个入口都重写
// busy / error / 日志逻辑。
//
// 后端 RPC 已是幂等的（service.PromoteTaskFromThread 在已是 task 时返回
// AlreadyTask=true 不 mutation），因此前端不需要预检 runtime.taskId；但
// busy 防抖仍然有用——避免双击发两条 RPC 让日志吵。
//
// 决策记录：err 不在 composable 内 swallow，向调用方抛出，由调用方决定
// 文案。这是和 useTaskHandoff.continueTaskById 对齐的风格。
export function usePromoteTask({ callAPIFn = callAPI } = {}) {
  const promoting = ref(false);
  const lastError = ref('');

  async function promoteTaskFromThread(threadId) {
    const tid = (threadId || '').toString().trim();
    if (!tid) {
      throw new Error('promote-task: threadId required');
    }
    if (promoting.value) {
      // 双击防抖：当前请求未结束直接返回 null，让调用方知道这次没发出去。
      return null;
    }
    promoting.value = true;
    lastError.value = '';
    logInfo('ui', 'taskHandoff.promote.start', { source_thread_id: tid });
    try {
      const result = await callAPIFn('ui/thread/promote-task', { threadId: tid });
      logInfo('ui', 'taskHandoff.promote.done', {
        source_thread_id: tid,
        task_id: (result && (result.taskId || result.task_id)) || '',
        already_task: Boolean(result && (result.alreadyTask || result.already_task)),
        handoff_shell_warning:
          (result && (result.handoffShellWarning || result.handoff_shell_warning)) || '',
      });
      return result || null;
    } catch (error) {
      const msg =
        (error && typeof error === 'object' && typeof error.message === 'string'
          ? error.message
          : String(error || '')
        ).toString().trim() || 'promote-task RPC failed';
      lastError.value = msg;
      logWarn('ui', 'taskHandoff.promote.failed', {
        source_thread_id: tid,
        error: msg,
      });
      throw error;
    } finally {
      promoting.value = false;
    }
  }

  return { promoting, lastError, promoteTaskFromThread };
}
