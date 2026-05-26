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
import { resolveProjectActionCwd } from './useThreadActions.js';

// Phase 4-fork-kickoff：fork 出新对话后让 agent 自动开场。详见 useForkThread.submit。
// 文案 B：明确指示 agent 基于 system 里塞的摘要总结进展并提建议；
// 简短稳定，便于 timeline selector 用 text 匹配过滤这条 user 消息。
const FORK_KICKOFF_PROMPT = '请基于上文摘要，简要总结上次进展并提出下一步建议。';

// 返回错误字符串（成功 / 跳过返回空字符串）。失败时 useForkThread.submit 会把这个
// 错误暴露在 kickoffError ref 上——sendMessage 内部已经做 isSessionNotAvailableError
// recover+retry 兜底，能走到这层 catch 说明 recover 也失败了，agent 真挂了，需要让
// UI 有机会感知（review M1）。
async function maybeSendKickoff(ctx, sourceThreadId, newThreadId) {
  const hasSendMessage = typeof ctx.threadStore?.sendMessage === 'function';
  // 诊断日志：让生产 [AO] 能看到决策路径全状态。bug 报告「UI 没看到生效」
  // 时第一时间能定位是 wiring 没接通 / sendMessage 未注入。
  logInfo('ui', 'forkThread.kickoff_check', {
    source_thread_id: sourceThreadId,
    new_thread_id: newThreadId,
    has_send_message: hasSendMessage,
  });
  if (!hasSendMessage) {
    logWarn('ui', 'forkThread.kickoff_no_send_message', {
      source_thread_id: sourceThreadId, new_thread_id: newThreadId,
    });
    return '';
  }
  try {
    await ctx.threadStore.sendMessage(newThreadId, FORK_KICKOFF_PROMPT, [], { kickoff: true });
    logInfo('ui', 'forkThread.kickoff_sent', {
      source_thread_id: sourceThreadId, new_thread_id: newThreadId,
    });
    return '';
  } catch (kickoffErr) {
    const msg = toErrorMessage(kickoffErr) || 'kickoff 发送失败';
    // review P2 部分修：失败时清 kickoffByThread，让 timeline selector 不再过滤
    // 这条 user message——agent 没主动开场时用户至少能看到 kickoff prompt 原文
    // 出现在 timeline 头部，比「完全空白」更可定位。
    const stateRef = ctx.threadStore?.state;
    if (stateRef && stateRef.kickoffByThread && newThreadId) {
      const next = { ...stateRef.kickoffByThread };
      delete next[newThreadId];
      stateRef.kickoffByThread = next;
    }
    logWarn('ui', 'forkThread.kickoff_failed', {
      source_thread_id: sourceThreadId, new_thread_id: newThreadId,
      error: msg,
    });
    return msg;
  }
}

function toErrorMessage(error) {
  return ((error && typeof error === 'object' && typeof error.message === 'string' ? error.message : '') || String(error || '')).toString().trim();
}

function buildSourceTitleFromThread(thread) {
  const name = (thread?.name || '').toString().trim();
  const id = (thread?.id || '').toString().trim();
  if (name) return `继承自会话：${name}`;
  if (id) return `继承自会话：${id}`;
  return '继承自前一个对话';
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
  // review M1：kickoff 失败与 fork 主流程错误分开。fork 主流程已成功（thread 已创建），
  // kickoff 失败属于次要错误；混在 error 里会让 UI 误以为 fork 没成功。
  const kickoffError = ref('');

  async function loadSharedFiles(paths) {
    const list = Array.isArray(paths) ? paths.filter(Boolean) : [];
    if (list.length === 0) return [];
    return Promise.all(
      list.map(async (path) => {
        const detail = await callAPI('ui/memory/shared-file/get', { path });
        if (!detail || typeof detail !== 'object') {
          throw new Error(`shared file ${path} returned empty response`);
        }
        return {
          path: (detail.path || path || '').toString(),
          content: (detail.content || '').toString(),
        };
      }),
    );
  }

  function buildSummaryFromCurrentThread() {
    const threadId = (ctx.selectedThreadId.value || '').toString().trim();
    if (!threadId) return '';
    if (typeof ctx.threadStore?.getThreadTimeline !== 'function') return '';
    const timeline = ctx.threadStore.getThreadTimeline(threadId);
    return extractTimelineSummary(timeline);
  }

  function buildSourceTitle() {
    return buildSourceTitleFromThread(ctx.activeThread?.value);
  }

  /**
   * @returns {Promise<string>} 新会话 id；失败返回空串
   */
  async function submit() {
    if (submitting.value) return '';
    submitting.value = true;
    error.value = '';
    kickoffError.value = '';
    const sourceThreadId = (ctx.selectedThreadId.value || '').toString().trim();
    const sourceTitle = buildSourceTitleFromThread(ctx.activeThread?.value);
    const isCurrentSourceThread = () => (ctx.selectedThreadId.value || '').toString().trim() === sourceThreadId;
    try {
      const summary = buildSummaryFromCurrentThread();
      const sharedFiles = await loadSharedFiles(ctx.composer.forkDraft.sharedFilePaths);

      if (!summary && sharedFiles.length === 0) {
        if (isCurrentSourceThread()) {
          error.value = '当前会话没有可用上下文，且未挂载共享文件，无法生成继承摘要。';
        }
        logWarn('ui', 'forkThread.skipped_empty', { source_thread_id: sourceThreadId });
        return '';
      }

      const baseInstructions = buildSeedInstructionsFromSummary(summary, {
        sourceTitle,
        sharedFiles,
      });

      const cwd = resolveProjectActionCwd(ctx.projectStore, ctx.windowCwd);
      const focusMode = ctx.isCmd?.value ? 'cmd' : 'chat';

      logInfo('ui', 'forkThread.start', {
        source_thread_id: sourceThreadId,
        shared_file_count: sharedFiles.length,
        summary_chars: summary.length,
        base_instructions_chars: baseInstructions.length,
      });

      const newThreadId = await ctx.threadStore.startThread(cwd, {
        focusMode,
        name: sourceTitle,
        baseInstructions,
      });

      if (newThreadId) {
        logInfo('ui', 'forkThread.done', {
          source_thread_id: sourceThreadId,
          new_thread_id: newThreadId,
          shared_file_count: sharedFiles.length,
        });
        ctx.composer.closeForkDraft();
        const kickoffMsg = await maybeSendKickoff(ctx, sourceThreadId, newThreadId);
        if (kickoffMsg) kickoffError.value = kickoffMsg;
      }
      return newThreadId || '';
    } catch (err) {
      const msg = toErrorMessage(err) || '新建继承对话失败';
      if (isCurrentSourceThread()) error.value = msg;
      logWarn('ui', 'forkThread.failed', {
        source_thread_id: sourceThreadId,
        error: msg,
      });
      throw err;
    } finally {
      submitting.value = false;
    }
  }

  return {
    submitting,
    error,
    kickoffError,
    submit,
  };
}
