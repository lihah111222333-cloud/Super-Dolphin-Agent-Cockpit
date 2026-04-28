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

  const activeTask = computed(() => {
    const runtime = activeRuntime.value && typeof activeRuntime.value === 'object' ? activeRuntime.value : {};
    const taskId = firstNonEmpty(runtime.taskId, runtime.task_id);
    if (!taskId) return null;
    return {
      taskId,
      title: firstNonEmpty(runtime.taskTitle, runtime.task_title, activeThread.value?.name, '当前任务'),
      handoffFile: firstNonEmpty(runtime.handoffFile, runtime.handoff_file),
      ownerThreadId: firstNonEmpty(runtime.ownerThreadId, runtime.owner_thread_id),
    };
  });

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

  async function continueTask() {
    const task = activeTask.value;
    if (!task || continueBusy.value) return '';
    continueBusy.value = true;
    try {
      logInfo('ui', 'taskHandoff.continue.start', {
        source_thread_id: selectedThreadId.value || '',
        task_id: task.taskId,
      });
      const id = await threadStore.startThread(
        projectStore?.state?.active || '.',
        buildContinueTaskOptions({
          focusMode: isCmd.value ? 'cmd' : 'chat',
          task,
        }),
      );

      if (id) {
        logInfo('ui', 'taskHandoff.continue.done', {
          source_thread_id: selectedThreadId.value || '',
          next_thread_id: id,
          task_id: task.taskId,
        });
      }
      return id;
    } catch (error) {
      logWarn('ui', 'taskHandoff.continue.failed', {
        source_thread_id: selectedThreadId.value || '',
        task_id: task.taskId,
        error: toErrorMessage(error),
      });
      throw error;
    } finally {
      continueBusy.value = false;
    }
  }

  async function startNewTaskFromHandoff() {
    const task = activeTask.value;
    const content = (state.content || '').toString().trim();
    if (!task || !content || continueBusy.value) return '';
    continueBusy.value = true;
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
      loadTaskHandoff().catch(() => {});
    },
    { immediate: true },
  );

  return {
    activeTask,
    taskHandoffVisible: computed(() => Boolean(activeTask.value)),
    taskHandoffLoading: computed(() => state.loading),
    taskHandoffError: computed(() => state.error),
    taskHandoffContent: computed(() => state.content),
    taskHandoffPreview: computed(() => previewTaskHandoff(state.content)),
    taskHandoffUpdatedAt: computed(() => state.updatedAt),
    taskHandoffUpdatedBy: computed(() => state.updatedBy),
    continueTaskBusy: computed(() => continueBusy.value),
    refreshTaskHandoff: loadTaskHandoff,
    continueCurrentTask: continueTask,
    startNewTaskFromHandoff,
    continueCurrentTaskInNewWindow: continueTaskInNewWindow,
  };
}
