import {
  reactive,
  watch,
} from '../../lib/vue.esm-browser.prod.js';

export function createThreadConfigController({ threadStore, threadActions, selectedThreadId, isCmd }) {
  const threadConfigUi = reactive({
    loading: false,
    saving: false,
    notice: '',
    noticeLevel: 'info',
    error: '',
    draft: { model: '', effort: '' },
    meta: { threadId: '', provider: '', supportsThreadOverride: false, override: { model: '', effort: '' }, effective: { model: '', effort: '' } },
    result: null,
  });
  let threadConfigRequestSeq = 0;
  let noticeTimeoutId = null;

  function normalizeThreadConfigValue(value) {
    return (value || '').toString().trim();
  }

  function anyValue(value) {
    return value;
  }

  function currentRuntimeThreadProvider(threadId = selectedThreadId.value) {
    const id = normalizeThreadConfigValue(threadId);
    return normalizeThreadConfigValue(threadStore.state.agentRuntimeById?.[id]?.provider);
  }

  function currentRuntimeThreadModel(threadId = selectedThreadId.value) {
    const id = normalizeThreadConfigValue(threadId);
    return normalizeThreadConfigValue(threadStore.state.agentRuntimeById?.[id]?.model);
  }

  function buildThreadConfigMeta(raw = {}, threadId = selectedThreadId.value) {
    const source = anyValue(raw);
    const id = normalizeThreadConfigValue(source?.threadId || threadId);
    return {
      threadId: id,
      provider: normalizeThreadConfigValue(source?.provider) || currentRuntimeThreadProvider(id),
      supportsThreadOverride: Boolean(source?.supportsThreadOverride),
      override: {
        model: normalizeThreadConfigValue(source?.override?.model),
        effort: normalizeThreadConfigValue(source?.override?.effort),
      },
      effective: {
        model: normalizeThreadConfigValue(source?.effective?.model) || currentRuntimeThreadModel(id),
        effort: normalizeThreadConfigValue(source?.effective?.effort),
      },
    };
  }

  function resetThreadConfigUi(threadId = '') {
    threadConfigUi.loading = false;
    threadConfigUi.saving = false;
    threadConfigUi.notice = '';
    threadConfigUi.noticeLevel = 'info';
    threadConfigUi.error = '';
    threadConfigUi.draft.model = '';
    threadConfigUi.draft.effort = '';
    threadConfigUi.result = null;
    threadConfigUi.meta = buildThreadConfigMeta({}, threadId);
  }

  function applyThreadConfigResult(result, threadId) {
    const meta = buildThreadConfigMeta(result, threadId);
    threadConfigUi.result = result;
    threadConfigUi.meta = meta;
    threadConfigUi.draft.model = meta.override.model;
    threadConfigUi.draft.effort = meta.override.effort;
    threadConfigUi.error = '';
    return meta;
  }

  function formatThreadConfigError(error) {
    if (error && typeof error === 'object' && error.message) return error.message;
    return String(error || 'unknown error');
  }

  function isBusyThreadConfigError(detail) {
    const normalized = normalizeThreadConfigValue(detail).toLowerCase();
    return normalized.includes('thread') && normalized.includes('busy');
  }

  async function hydrateThreadConfig(threadId, options = {}) {
    const settings = anyValue(options);
    const id = normalizeThreadConfigValue(threadId);
    const requestSeq = ++threadConfigRequestSeq;
    if (!id || isCmd.value) {
      resetThreadConfigUi(id);
      return null;
    }
    if (!settings.preserveNotice) {
      threadConfigUi.notice = '';
      threadConfigUi.noticeLevel = 'info';
    }
    threadConfigUi.error = '';
    threadConfigUi.loading = true;
    threadConfigUi.result = null;
    threadConfigUi.meta = buildThreadConfigMeta({}, id);
    threadConfigUi.draft.model = '';
    threadConfigUi.draft.effort = '';
    try {
      const result = await threadActions.getThreadConfig(id);
      if (requestSeq !== threadConfigRequestSeq || normalizeThreadConfigValue(selectedThreadId.value) !== id || isCmd.value) return null;
      applyThreadConfigResult(result, id);
      return result;
    } catch (error) {
      if (requestSeq !== threadConfigRequestSeq || normalizeThreadConfigValue(selectedThreadId.value) !== id || isCmd.value) return null;
      const detail = formatThreadConfigError(error);
      threadConfigUi.error = detail;
      if (!settings.preserveNotice || !threadConfigUi.notice) {
        threadConfigUi.notice = `线程配置加载失败：${detail}`;
        threadConfigUi.noticeLevel = 'error';
      }
      return null;
    } finally {
      if (requestSeq === threadConfigRequestSeq && normalizeThreadConfigValue(selectedThreadId.value) === id && !isCmd.value) {
        threadConfigUi.loading = false;
      }
    }
  }

  function updateThreadConfigModel(value) {
    threadConfigUi.draft.model = normalizeThreadConfigValue(value);
  }

  function updateThreadConfigEffort(value) {
    threadConfigUi.draft.effort = normalizeThreadConfigValue(value);
  }

  async function persistThreadConfig(config, successNotice) {
    const id = normalizeThreadConfigValue(selectedThreadId.value);
    if (!id || isCmd.value || threadConfigUi.saving) return null;
    threadConfigUi.saving = true;
    threadConfigUi.error = '';
    try {
      const saved = await threadActions.setThreadConfig(id, {
        model: normalizeThreadConfigValue(config?.model),
        effort: normalizeThreadConfigValue(config?.effort),
      });
      if (normalizeThreadConfigValue(selectedThreadId.value) !== id || isCmd.value) return null;
      threadConfigUi.notice = successNotice;
      threadConfigUi.noticeLevel = 'info';

      if (noticeTimeoutId) clearTimeout(noticeTimeoutId);
      if (successNotice) {
        noticeTimeoutId = setTimeout(() => {
          if (threadConfigUi.notice === successNotice) {
            threadConfigUi.notice = '';
          }
        }, 5000);
      }

      if (saved) {
        applyThreadConfigResult(saved, id);
        return saved;
      }
      await hydrateThreadConfig(id, { preserveNotice: true });
      return threadConfigUi.result;
    } catch (error) {
      if (normalizeThreadConfigValue(selectedThreadId.value) !== id || isCmd.value) return null;
      const detail = formatThreadConfigError(error);
      threadConfigUi.error = detail;
      threadConfigUi.notice = isBusyThreadConfigError(detail)
        ? '当前线程正在执行，停止当前任务后再修改该线程的模型 / effort。'
        : `线程配置保存失败：${detail}`;
      threadConfigUi.noticeLevel = isBusyThreadConfigError(detail) ? 'warning' : 'error';
      return null;
    } finally {
      if (normalizeThreadConfigValue(selectedThreadId.value) === id && !isCmd.value) {
        threadConfigUi.saving = false;
      }
    }
  }

  function saveThreadConfigDraft() {
    return persistThreadConfig({
      model: threadConfigUi.draft.model,
      effort: threadConfigUi.draft.effort,
    }, '线程配置已保存，下次发送生效。');
  }

  function restoreThreadConfigInherit() {
    return persistThreadConfig({ model: '', effort: '' }, '已恢复继承全局默认。');
  }

  watch(
    () => [isCmd.value, selectedThreadId.value],
    ([cmdMode, threadId]) => {
      const id = normalizeThreadConfigValue(threadId);
      if (cmdMode || !id) {
        threadConfigRequestSeq += 1;
        resetThreadConfigUi(id);
        return;
      }
      void hydrateThreadConfig(id);
    },
    { immediate: true },
  );

  // Sync runtime model into meta.effective.model when it arrives
  // asynchronously (e.g. after Claude session reports the concrete
  // model). Without this, the summary label shows bare "继承全局"
  // instead of "claude-opus-4-7[1m] (继承全局)".
  watch(
    () => {
      const id = normalizeThreadConfigValue(selectedThreadId.value);
      return id ? normalizeThreadConfigValue(threadStore.state.agentRuntimeById?.[id]?.model) : '';
    },
    (runtimeModel) => {
      if (
        !runtimeModel ||
        isCmd.value ||
        !normalizeThreadConfigValue(selectedThreadId.value)
      ) return;
      // Only backfill when effective.model is empty — don't overwrite
      // an authoritative value returned by GetConfig.
      if (!normalizeThreadConfigValue(threadConfigUi.meta.effective?.model)) {
        threadConfigUi.meta.effective.model = runtimeModel;
      }
    },
  );

  return {
    threadConfigUi,
    updateThreadConfigModel,
    updateThreadConfigEffort,
    saveThreadConfigDraft,
    restoreThreadConfigInherit,
  };
}
