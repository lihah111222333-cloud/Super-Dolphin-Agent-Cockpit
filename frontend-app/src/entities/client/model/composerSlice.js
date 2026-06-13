/*
 * composer slice 管输入区：草稿、附件、模型选择和发送。
 * timeline 合并、线程快照不在这里做。
 */

function createComposerFilePickerActions(runtime, deps) {
  const {
    actionNotice,
    appendUniqueAttachments,
    normalizeAttachment,
    selectFiles,
  } = deps;

  return {
    selectFilesForComposer: async () => {
      try {
        const picked = await selectFiles();
        const attachments = (Array.isArray(picked) ? picked : [])
          .map(normalizeAttachment)
          .filter(Boolean);
        runtime.set((state) => ({
          attachments: appendUniqueAttachments(state.attachments, attachments),
          actionNotice: actionNotice(attachments.length > 0 ? `已添加 ${attachments.length} 个附件` : '未选择附件', attachments.length > 0 ? 'success' : 'info'),
        }));
        return attachments;
      }
      catch (error) {
        runtime.notifyAction(`选择附件失败：${error.message || String(error)}`, 'error');
        runtime.addWarning('error', 'attachments.select.failed', { error: error.message || String(error) });
        return [];
      }
    },

    attachPathsForComposer: (paths) => {
      const attachments = (Array.isArray(paths) ? paths : [])
        .map(normalizeAttachment)
        .filter(Boolean);
      runtime.set((state) => ({
        attachments: appendUniqueAttachments(state.attachments, attachments),
        actionNotice: actionNotice(
          attachments.length > 0 ? `已添加 ${attachments.length} 个附件` : '未找到可添加的附件路径',
          attachments.length > 0 ? 'success' : 'info',
        ),
      }));
      return attachments.length;
    },


  };
}

function createComposerDropActions(runtime, deps) {
  const {
    actionNotice,
    appendUniqueAttachments,
    attachmentKey,
    droppedFilePath,
    fileListOf,
    fileLooksImage,
    imageFileAttachment,
    normalizeFileAttachment,
    normalizeString,
  } = deps;

  return {
    attachDroppedFilesForComposer: async (files) => {
      const list = fileListOf(files);
      if (list.length === 0) return 0;
      const attachments = [];
      const rejected = [];
      for (let index = 0; index < list.length; index += 1) {
        const file = list[index];
        const path = droppedFilePath(file);
        if (path) {
          attachments.push(normalizeFileAttachment(path));
          continue;
        }
        if (fileLooksImage(file)) {
          attachments.push(await imageFileAttachment(file, index, 'dropped-image'));
          continue;
        }
        rejected.push(normalizeString(file?.name) || `file-${index + 1}`);
      }
      runtime.set((state) => ({
        attachments: appendUniqueAttachments(state.attachments, attachments),
        actionNotice: actionNotice(
          attachments.length > 0
            ? `已添加 ${attachments.length} 个附件`
            : '无法添加无路径的非图片文件',
          attachments.length > 0 ? 'success' : 'error',
        ),
      }));
      if (rejected.length > 0) {
        runtime.addWarning('warn', 'attachments.drop.rejected_no_path', { files: rejected });
      }
      return attachments.length;
    },

    attachPastedImagesForComposer: async (files) => {
      const images = fileListOf(files).filter(fileLooksImage);
      if (images.length === 0) return 0;
      const attachments = [];
      for (let index = 0; index < images.length; index += 1) {
        attachments.push(await imageFileAttachment(images[index], index, 'pasted-image'));
      }
      runtime.set((state) => ({
        attachments: appendUniqueAttachments(state.attachments, attachments),
        actionNotice: actionNotice(`已添加 ${attachments.length} 张图片`, 'success'),
      }));
      return attachments.length;
    },

    removeAttachment: (path) => {
      const target = normalizeString(path);
      runtime.set((state) => ({
        attachments: state.attachments.filter((item) => attachmentKey(item) !== target && item.path !== target),
      }));
    },


  };
}

function createComposerModelSaveActions(runtime, deps) {
  const {
    actionNotice,
    composerModelConfigTarget,
    normalizeThreadConfig,
    saveGlobalComposerModelConfig,
    saveThreadComposerModelConfig,
    setThreadConfig,
    threadConfigTargetIdForState,
  } = deps;

  return {
    saveComposerModelConfig: async (config = {}) => {
      const cwd = runtime.requireCwd('composer.model.save');
      const state = runtime.get();
      const target = await composerModelConfigTarget(config, state, runtime.get().loadThreadConfig);
      if (target.threadId && !target.threadConfig) {
        runtime.notifyAction('线程配置加载失败，无法保存模型配置', 'error', { threadId: target.threadId });
        return false;
      }
      if (target.threadConfig?.supportsThreadOverride) {
        return saveThreadComposerModelConfig(target, runtime.set, runtime.addWarning);
      }
      return saveGlobalComposerModelConfig(cwd, state, target, runtime.set, runtime.notifyRPCFailure);
    },

    restoreComposerModelInheritance: async (config = {}) => {
      const state = runtime.get();
      const requestedThreadId = Object.prototype.hasOwnProperty.call(config, 'threadId') ||
        Object.prototype.hasOwnProperty.call(config, 'thread_id')
        ? (config.threadId || config.thread_id)
        : state.activeThreadId;
      const threadId = threadConfigTargetIdForState(state, requestedThreadId);
      if (!threadId) return false;
      const existingConfig = state.threadConfigByThread[threadId] || await runtime.get().loadThreadConfig(threadId);
      if (!existingConfig?.supportsThreadOverride) return false;
      try {
        const saved = await setThreadConfig({ threadId, model: '', effort: '' });
        const normalized = normalizeThreadConfig(saved, threadId, existingConfig.provider || state.provider);
        runtime.set((current) => ({
          threadConfigByThread: {
            ...current.threadConfigByThread,
            [threadId]: normalized,
          },
          actionNotice: actionNotice('已恢复继承全局默认', 'success'),
        }));
        return true;
      }
      catch (error) {
        return runtime.notifyRPCFailure('恢复线程模型继承', 'thread.config.restore.failed', error, { threadId });
      }
    },


  };
}

function createComposerModelProviderActions(runtime, deps) {
  const {
    actionNotice,
    defaultProvider,
    normalizeProviderRuntimeConfig,
    providerPreferenceKey,
    requireProviderPreferenceValue,
    setPreference,
  } = deps;

  return {
    saveComposerModelProvider: async (codexModelProvider) => {
      const key = providerPreferenceKey('codex', 'codexModelProvider');
      const value = requireProviderPreferenceValue(codexModelProvider, key, 'composer.modelProvider.save');
      const cwd = runtime.requireCwd('composer.modelProvider.save');
      try {
        await setPreference({ cwd, key, value });
        runtime.set((state) => ({
          providerConfig: normalizeProviderRuntimeConfig({
            ...state.providerConfig,
            codexModelProvider: value,
          }, state.provider || defaultProvider),
          actionNotice: actionNotice('模型渠道已保存', 'success'),
        }));
        return true;
      }
      catch (error) {
        return runtime.notifyRPCFailure('模型渠道保存', 'provider.model_provider.save.failed', error, { provider: 'codex' });
      }
    },


  };
}

function createComposerSendActions(runtime, deps) {
  const {
    createSendDraftRequest,
    createdThreadIdForSendRollback,
    deleteProvisionalThreadAfterSendFailure,
    optimisticSendDraftState,
    promotedDraftThreadState,
    resolveLaunchPreferences,
    rollbackSendDraftState,
    startNewDraftThread,
    startTurnWithStoppedThreadRecovery,
  } = deps;

  return {
    sendDraft: async () => {
      /*
       * 发送新对话时先建 thread，再 start turn。
       * 失败要撤回本地乐观消息，必要时删掉刚建的临时 thread。
       */
      const cwd = runtime.requireCwd('send message');
      const request = createSendDraftRequest(runtime.get(), cwd);
      if (!request) return false;

      runtime.set((state) => optimisticSendDraftState(state, request));

      let threadId = request.previousThreadId;
      try {
        if (!threadId) {
          const started = await startNewDraftThread(request, resolveLaunchPreferences);
          threadId = started.threadId;
          runtime.set((state) => promotedDraftThreadState(state, request, started));
        }

        await startTurnWithStoppedThreadRecovery({
          cwd: request.cwd,
          threadId,
          input: request.input,
          manualSkillSelection: false,
        });
        runtime.clearComposerDraft({ ...runtime.get(), activeThreadId: request.previousActiveThreadId }, request.previousActiveThreadId);
        runtime.clearComposerDraft(runtime.get(), request.provisionalThreadId);
        runtime.clearComposerDraft(runtime.get(), threadId);
        runtime.set({ sending: false });
        return true;
      }
      catch (error) {
        const createdThreadId = createdThreadIdForSendRollback(runtime.get(), request, threadId);
        runtime.set((state) => rollbackSendDraftState(state, request, error));
        await deleteProvisionalThreadAfterSendFailure(createdThreadId, runtime.addWarning);
        runtime.addWarning('error', 'thread.send.failed', { error: error.message });
        throw error;
      }
    },

  };
}

export function createComposerSlice(runtime, deps) {
  return {
    ...createComposerFilePickerActions(runtime, deps.attachment),
    ...createComposerDropActions(runtime, deps.attachment),
    ...createComposerModelSaveActions(runtime, deps.model),
    ...createComposerModelProviderActions(runtime, deps.modelProvider),
    ...createComposerSendActions(runtime, deps.send),
    setDraft: (draft) => runtime.set({ draft }),
  };
}
