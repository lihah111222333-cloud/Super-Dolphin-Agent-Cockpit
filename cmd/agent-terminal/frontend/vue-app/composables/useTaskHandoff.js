import { computed, reactive, ref, watch } from '../../lib/vue.esm-browser.prod.js';
import { callAPI } from '../services/api.js';
import { logInfo, logWarn } from '../services/log.js';
import { truncateSummaryText } from './useSummaryHandoff.js';

function firstNonEmpty(...values) {
  for (const value of values) {
    const text = (value || '').toString().trim();
    if (text) return text;
  }
  return '';
}

function toErrorMessage(error) {
  return (
    (error && typeof error === 'object' && typeof error.message === 'string' ? error.message : '')
    || String(error || '')
  ).toString().trim();
}

function previewTaskHandoff(content, limit = 2400) {
  // 委托到共享工具，保证 useTaskHandoff 和 useForkThread 的截断行为完全一致。
  return truncateSummaryText(content, limit);
}

// 从任意 thread runtime 对象抽 task 描述；runtime 没有 taskId 返 null。
// 抽出为独立 helper 是为了让 activeTask（selected thread）和 continueTaskById（任意 thread）复用同一套字段映射逻辑。
export function buildTaskFromRuntime(runtime, fallbackTitle = '') {
  const safeRuntime = runtime && typeof runtime === 'object' ? runtime : {};
  const taskId = firstNonEmpty(safeRuntime.taskId, safeRuntime.task_id);
  if (!taskId) return null;
  return {
    taskId,
    title: firstNonEmpty(safeRuntime.taskTitle, safeRuntime.task_title, fallbackTitle, '当前任务'),
    handoffFile: firstNonEmpty(safeRuntime.handoffFile, safeRuntime.handoff_file),
    ownerThreadId: firstNonEmpty(safeRuntime.ownerThreadId, safeRuntime.owner_thread_id),
  };
}

function buildContinueTaskConfig(task) {
  return {
    taskId: task.taskId,
    taskTitle: task.title,
    handoffFile: task.handoffFile,
    continueTask: true,
    autoTaskHandoff: true,
  };
}

function buildContinueTaskOptions({ focusMode, task }) {
  return {
    focusMode,
    name: firstNonEmpty(task?.title, task?.taskId, '当前任务'),
    config: buildContinueTaskConfig(task),
  };
}

function buildNewTaskTitle(title) {
  const base = firstNonEmpty(title, '新任务');
  if (base.endsWith(' · 新任务') || base.endsWith('（新任务）')) return base;
  return `${base} · 新任务`;
}

function buildNewTaskSeedInstructions(task, content) {
  const summary = previewTaskHandoff(content);
  if (!summary) return '';
  return [
    '以下是此前任务窗口自动维护的接力摘要，可作为当前新任务的背景参考。',
    '如果后续用户消息提出了新的目标，请以新的用户消息为准。',
    '',
    `来源任务：${firstNonEmpty(task?.title, task?.taskId, '当前任务')}`,
    '',
    '任务接力摘要：',
    summary,
  ].join('\n');
}

// “以此新建任务”后的 agent 自动开场 prompt。与 fork-kickoff 同样提供一次 user message，
// 否则新 thread 只有 baseInstructions 作 system、没人说话 agent 不会主动响应。
// timeline selector 会基于 kickoffByThread 过滤这条，避免用户看到。
const TASK_HANDOFF_KICKOFF_PROMPT = '请基于上文任务接力摘要，简要总结上次进展并提出下一步建议。';

// 返回错误字符串（成功 / 跳过返回空串）。调用方负责写回 kickoffError ref——
// 跟 fork-thread 路径的 maybeSendKickoff 同样接口，为了让 UI 能设 banner/toast。
async function maybeSendTaskKickoff(threadStore, newThreadId, task) {
  const hasSendMessage = typeof threadStore?.sendMessage === 'function';
  if (!hasSendMessage) {
    logWarn('ui', 'taskHandoff.kickoff_no_send_message', {
      next_thread_id: newThreadId,
      task_id: task?.taskId || '',
    });
    return '';
  }
  try {
    await threadStore.sendMessage(newThreadId, TASK_HANDOFF_KICKOFF_PROMPT, [], { kickoff: true });
    logInfo('ui', 'taskHandoff.kickoff_sent', {
      next_thread_id: newThreadId,
      task_id: task?.taskId || '',
    });
    return '';
  } catch (kickoffErr) {
    const msg = toErrorMessage(kickoffErr) || 'kickoff 发送失败';
    // review P2 教训：失败时清 kickoffByThread，避免 timeline selector 过滤这条
    // user message——至少让用户在新 thread 头部看到 prompt 原文，明白 agent 未开场。
    const stateRef = threadStore?.state;
    if (stateRef && stateRef.kickoffByThread && newThreadId) {
      const next = { ...stateRef.kickoffByThread };
      delete next[newThreadId];
      stateRef.kickoffByThread = next;
    }
    logWarn('ui', 'taskHandoff.kickoff_failed', {
      next_thread_id: newThreadId,
      task_id: task?.taskId || '',
      error: msg,
    });
    return msg;
  }
}

function buildNewTaskOptions({ focusMode, task, content }) {
  const title = buildNewTaskTitle(task?.title);
  return {
    focusMode,
    name: title,
    baseInstructions: buildNewTaskSeedInstructions(task, content),
    config: {
      taskTitle: title,
      autoTaskHandoff: true,
    },
  };
}

function buildNewWindowTaskSnapshot({ cwd, focusMode, task }) {
  const options = buildContinueTaskOptions({ focusMode, task });
  return {
    page: 'chat',
    cwd,
    taskStart: {
      focusMode: options.focusMode,
      name: options.name,
      config: options.config,
    },
  };
}

export function useTaskHandoff({ threadStore, projectStore, selectedThreadId, activeThread, activeRuntime, isCmd }) {

  const state = reactive({
    loading: false,
    error: '',
    content: '',
    updatedBy: '',
    updatedAt: '',
  });
  const continueBusy = ref(false);
  // F1：kickoff 失败不走 main error（fork 主流程已成功、新 thread 已创建切焦点，kickoff
  // 是次要错误）。与 fork-thread 路径对齐独立暴露 ref，让 UI 能挥 banner/toast，
  // 不依赖「看到 prompt 原文」这种间接反馈。
  const kickoffError = ref('');
  // Phase 2.3 任务条默认折叠：只显示 30px chip（标题 + 更新时间），点击才展开
  // 详细接力摘要。切换 thread 或 task 变化时重置为折叠。load 错误
  // 出现时自动展开让用户看到原因。外部（token 满 / watchdog stuck 等）可
  // 调 expandTaskStrip(reason) 主动展开。
  const expanded = ref(false);

  const activeTask = computed(() => buildTaskFromRuntime(activeRuntime.value, activeThread.value?.name));

  async function loadTaskHandoff() {
    const task = activeTask.value;
    const path = firstNonEmpty(task?.handoffFile);
    state.loading = true;
    state.error = '';
    try {
      if (!path) {
        state.content = '';
        state.updatedAt = '';
        state.updatedBy = '';
        return;
      }
      const detail = await callAPI('ui/memory/shared-file/get', { path });
      state.content = (detail?.content || '').toString();
      state.updatedBy = (detail?.updatedBy || '').toString();
      state.updatedAt = (detail?.updatedAt || '').toString();
    } catch (error) {
      state.error = toErrorMessage(error);
      state.content = '';
      state.updatedBy = '';
      state.updatedAt = '';
      logWarn('ui', 'taskHandoff.load.failed', {
        thread_id: selectedThreadId.value || '',
        task_id: activeTask.value?.taskId || '',
        error: state.error,
      });
    } finally {
      state.loading = false;
    }
  }

  // 为任意 source thread 起一个续接 thread。Phase 1.3+ 的自动调度走这条路径，
  // 与 selected thread 动作表现一致：没 taskId 跳过，正在进行中的 fork 不重发。
  async function continueTaskById(sourceThreadId) {
    const threadId = (sourceThreadId || '').toString().trim();
    if (!threadId || continueBusy.value) return '';
    const runtime = threadStore?.state?.agentRuntimeById?.[threadId];
    const task = buildTaskFromRuntime(runtime);
    if (!task) {
      logWarn('ui', 'taskHandoff.continue.skipped', {
        source_thread_id: threadId,
        reason: 'no_task_id',
      });
      return '';
    }
    // Phase 1.4b：焦点判断。source thread 是当前选中的 → 焦点跟随到新 thread（与“以此新建
    // 任务”按钮表现一致）；source 在后台起的续接（自动调度器路径）不抢焦点，避免打断用户
    // 在别处的工作。
    const sameAsSelected = (selectedThreadId.value || '').toString().trim() === threadId;
    continueBusy.value = true;
    try {
      logInfo('ui', 'taskHandoff.continue.start', {
        source_thread_id: threadId,
        task_id: task.taskId,
        focus_followed: sameAsSelected,
      });
      // Phase 1.8d fork 前预检：worker.FlushForThread + EnsureHandoffExists
      // 任一失败抛 error message 含 handoff_flush_failed / handoff_missing
      // 关键字，外层 useAutoContinue.classifyError 识别为 permanent 不重试。
      try {
        await callAPI('ui/task/flush_and_verify', {
          threadId,
          taskId: task.taskId,
        });
      } catch (preflightError) {
        logWarn('ui', 'taskHandoff.continue.preflight_failed', {
          source_thread_id: threadId,
          task_id: task.taskId,
          error: toErrorMessage(preflightError),
        });
        throw preflightError;
      }
      const id = await threadStore.startThread(
        projectStore?.state?.active || '.',
        {
          ...buildContinueTaskOptions({
            focusMode: isCmd.value ? 'cmd' : 'chat',
            task,
          }),
          skipSaveActive: !sameAsSelected,
        },
      );
      if (id) {
        logInfo('ui', 'taskHandoff.continue.done', {
          source_thread_id: threadId,
          next_thread_id: id,
          task_id: task.taskId,
          focus_followed: sameAsSelected,
        });
      }
      return id;
    } catch (error) {
      logWarn('ui', 'taskHandoff.continue.failed', {
        source_thread_id: threadId,
        task_id: task.taskId,
        error: toErrorMessage(error),
      });
      throw error;
    } finally {
      continueBusy.value = false;
    }
  }

  // selected thread 上的按钮路径，委托给 continueTaskById。
  async function continueTask() {
    return continueTaskById(selectedThreadId.value || '');
  }

  async function startNewTaskFromHandoff() {
    const task = activeTask.value;
    const content = (state.content || '').toString().trim();
    if (!task || !content || continueBusy.value) return '';
    continueBusy.value = true;
    kickoffError.value = '';
    try {
      logInfo('ui', 'taskHandoff.new_task.start', {
        source_thread_id: selectedThreadId.value || '',
        task_id: task.taskId,
      });
      const id = await threadStore.startThread(
        projectStore?.state?.active || '.',
        buildNewTaskOptions({
          focusMode: isCmd.value ? 'cmd' : 'chat',
          task,
          content,
        }),
      );
      if (id) {
        logInfo('ui', 'taskHandoff.new_task.done', {
          source_thread_id: selectedThreadId.value || '',
          next_thread_id: id,
          source_task_id: task.taskId,
        });
        // 参照 fork-kickoff：startThread 默认 saveActive 已切焦点到新 thread，但这里必须主动发
        // 一条 user message，否则新 thread 只有 baseInstructions（任务接力摘要）作 system，
        // agent 不会主动说话——用户看到的就是「点了没生成对话」。kickoff 失败
        // 不影响主流程返回 id，maybeSendTaskKickoff 内部自己 catch + logWarn，
        // 返回 error msg 写回 kickoffError ref 供 UI banner 消费。
        const kickoffMsg = await maybeSendTaskKickoff(threadStore, id, task);
        if (kickoffMsg) kickoffError.value = kickoffMsg;
      }
      return id;
    } catch (error) {
      logWarn('ui', 'taskHandoff.new_task.failed', {
        source_thread_id: selectedThreadId.value || '',
        task_id: task.taskId,
        error: toErrorMessage(error),
      });
      throw error;
    } finally {
      continueBusy.value = false;
    }
  }

  async function continueTaskInNewWindow() {
    const task = activeTask.value;
    if (!task || continueBusy.value) return;
    continueBusy.value = true;
    try {
      const cwd = (projectStore?.state?.active || '').toString().trim();
      await callAPI('ui/openNewWindow', {
        cwd,
        snapshot: buildNewWindowTaskSnapshot({
          cwd,
          focusMode: isCmd.value ? 'cmd' : 'chat',
          task,
        }),
      });

      logInfo('ui', 'taskHandoff.continue.new_window.done', {
        source_thread_id: selectedThreadId.value || '',
        task_id: task.taskId,
        cwd,
      });
    } catch (error) {
      logWarn('ui', 'taskHandoff.continue.new_window.failed', {
        source_thread_id: selectedThreadId.value || '',
        task_id: task.taskId,
        error: toErrorMessage(error),
      });
    } finally {
      continueBusy.value = false;
    }
  }

  watch(
    () => firstNonEmpty(activeTask.value?.taskId, activeTask.value?.handoffFile),
    () => {
      // 切 thread / task 时重置为折叠：保证每进入一个任务都是默认紧凑 chip。
      // sync flush 让重置随 activeRuntime 赋值同步发生，在测试里不需 nextTick 就
      // 到位；load 调用本身是异步的不受影响。
      expanded.value = false;
      loadTaskHandoff().catch(() => {});
    },
    { immediate: true, flush: 'sync' },
  );

  // load error 出现时自动展开：错误文案在详细体里，不展开看不到。error
  // 被清空不会反向折起 —— 用户方例在演变间保持展开以看接下来成功的摘要。
  watch(
    () => state.error,
    (err) => {
      if (err) expanded.value = true;
    },
  );

  function expandTaskStrip(_reason) {
    expanded.value = true;
  }
  function collapseTaskStrip() {
    expanded.value = false;
  }
  function toggleTaskStrip() {
    expanded.value = !expanded.value;
  }

  return {
    activeTask,
    taskHandoffVisible: computed(() => Boolean(activeTask.value)),
    taskHandoffLoading: computed(() => state.loading),
    taskHandoffError: computed(() => state.error),
    taskHandoffContent: computed(() => state.content),
    taskHandoffPreview: computed(() => previewTaskHandoff(state.content)),
    taskHandoffUpdatedAt: computed(() => state.updatedAt),
    taskHandoffUpdatedBy: computed(() => state.updatedBy),
    taskStripExpanded: computed(() => expanded.value),
    continueTaskBusy: computed(() => continueBusy.value),
    taskHandoffKickoffError: computed(() => kickoffError.value),
    refreshTaskHandoff: loadTaskHandoff,
    continueCurrentTask: continueTask,
    continueTaskById,
    startNewTaskFromHandoff,
    continueCurrentTaskInNewWindow: continueTaskInNewWindow,
    expandTaskStrip,
    collapseTaskStrip,
    toggleTaskStrip,
  };
}
