// @ts-nocheck
// Phase 2: 「新建继承对话」action。
// 给 ComposerForkDraftCard 用。负责：
//   1) 从当前 thread timeline 抽截断式摘要（Phase 2a；后续可换 LLM 摘要）
//   2) 拉取已挂载共享文件的内容
//   3) 构造 baseInstructions 后通过 threadStore.startThread 创建新会话
//   4) 切换到新会话、关闭草稿
import { ref } from '../../lib/vue.esm-browser.prod.js';
import { callAPI } from '../services/api.js';
import { logInfo, logWarn } from '../services/log.js';
import {
  buildSeedInstructionsFromSummary,
  extractTimelineSummary,
} from './useSummaryHandoff.js';

function toErrorMessage(error) {
  return ((error && typeof error === 'object' && typeof error.message === 'string' ? error.message : '') || String(error || '')).toString().trim();
}

/**
 * @param {{
 *   threadStore: any,
 *   projectStore: any,
 *   composer: { forkDraft: { sharedFilePaths: string[], origin: string }, openForkDraft: Function, closeForkDraft: Function },
 *   selectedThreadId: { value: string },
 *   activeThread: { value: any },
 *   isCmd: { value: boolean },
 * }} ctx
 */
export function useForkThread(ctx) {
  const submitting = ref(false);
  const error = ref('');

  async function loadSharedFiles(paths) {
    const list = Array.isArray(paths) ? paths.filter(Boolean) : [];
    if (list.length === 0) return [];
    const settled = await Promise.allSettled(
      list.map((path) => callAPI('ui/memory/shared-file/get', { path })),
    );
    const collected = [];
    settled.forEach((result, idx) => {
      if (result.status === 'fulfilled' && result.value && typeof result.value === 'object') {
        collected.push({
          path: (result.value.path || list[idx] || '').toString(),
          content: (result.value.content || '').toString(),
        });
      } else {
        logWarn('ui', 'forkThread.shared_file.load_failed', {
          path: list[idx],
          error: result.status === 'rejected' ? toErrorMessage(result.reason) : 'empty_response',
        });
      }
    });
    return collected;
  }

  function buildSummaryFromCurrentThread() {
    const threadId = (ctx.selectedThreadId.value || '').toString().trim();
    if (!threadId) return '';
    if (typeof ctx.threadStore?.getThreadTimeline !== 'function') return '';
    const timeline = ctx.threadStore.getThreadTimeline(threadId);
    return extractTimelineSummary(timeline);
  }

  function buildSourceTitle() {
    const thread = ctx.activeThread?.value;
    const name = (thread?.name || '').toString().trim();
    const id = (thread?.id || '').toString().trim();
    if (name) return `继承自会话：${name}`;
    if (id) return `继承自会话：${id}`;
    return '继承自前一个对话';
  }

  /**
   * @returns {Promise<string>} 新会话 id；失败返回空串
   */
  async function submit() {
    if (submitting.value) return '';
    submitting.value = true;
    error.value = '';
    const sourceThreadId = (ctx.selectedThreadId.value || '').toString().trim();
    try {
      const summary = buildSummaryFromCurrentThread();
      const sharedFiles = await loadSharedFiles(ctx.composer.forkDraft.sharedFilePaths);

      if (!summary && sharedFiles.length === 0) {
        error.value = '当前会话没有可用上下文，且未挂载共享文件，无法生成继承摘要。';
        logWarn('ui', 'forkThread.skipped_empty', { source_thread_id: sourceThreadId });
        return '';
      }

      const baseInstructions = buildSeedInstructionsFromSummary(summary, {
        sourceTitle: buildSourceTitle(),
        sharedFiles,
      });

      const cwd = (ctx.projectStore?.state?.active || '.').toString();
      const focusMode = ctx.isCmd?.value ? 'cmd' : 'chat';

      logInfo('ui', 'forkThread.start', {
        source_thread_id: sourceThreadId,
        shared_file_count: sharedFiles.length,
        summary_chars: summary.length,
        base_instructions_chars: baseInstructions.length,
      });

      const newThreadId = await ctx.threadStore.startThread(cwd, {
        focusMode,
        name: buildSourceTitle(),
        baseInstructions,
      });

      if (newThreadId) {
        logInfo('ui', 'forkThread.done', {
          source_thread_id: sourceThreadId,
          new_thread_id: newThreadId,
          shared_file_count: sharedFiles.length,
        });
        ctx.composer.closeForkDraft();
      }
      return newThreadId || '';
    } catch (err) {
      error.value = toErrorMessage(err) || '新建继承对话失败';
      logWarn('ui', 'forkThread.failed', {
        source_thread_id: sourceThreadId,
        error: error.value,
      });
      return '';
    } finally {
      submitting.value = false;
    }
  }

  return {
    submitting,
    error,
    submit,
  };
}
