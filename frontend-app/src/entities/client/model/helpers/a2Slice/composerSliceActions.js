function createSelectFilesForComposerAction(runtime, deps) {
  const { actionNotice, appendUniqueAttachments, normalizeAttachment, selectFiles } = deps;
  return async () => {
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
      runtime.notifyAction(error.message || String(error), 'error', { category: 'attachment' });
      runtime.addWarning('error', 'attachments.select.failed', { error: error.message || String(error) });
      return [];
    }
  };
}

function createAttachPathsForComposerAction(runtime, deps) {
  const { actionNotice, appendUniqueAttachments, normalizeAttachment } = deps;
  return (paths) => {
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
  };
}

function createAttachDroppedFilesForComposerAction(runtime, deps) {
  const {
    actionNotice,
    appendUniqueAttachments,
    droppedFilePath,
    fileListOf,
    fileLooksImage,
    imageFileAttachment,
    normalizeFileAttachment,
    normalizeString,
  } = deps;
  return async (files) => {
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
  };
}

function createAttachPastedImagesForComposerAction(runtime, deps) {
  const { actionNotice, appendUniqueAttachments, fileListOf, fileLooksImage, imageFileAttachment } = deps;
  return async (files) => {
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
  };
}

function createRemoveAttachmentAction(runtime, deps) {
  const { attachmentKey, normalizeString } = deps;
  return (path) => {
    const target = normalizeString(path);
    runtime.set((state) => ({
      attachments: state.attachments.filter((item) => attachmentKey(item) !== target && item.path !== target),
    }));
  };
}

function restoredThreadModelConfigState(current, threadId, normalized, actionNotice) {
  return {
    threadConfigByThread: {
      ...current.threadConfigByThread,
      [threadId]: normalized,
    },
    actionNotice: actionNotice('已恢复继承全局默认', 'success'),
  };
}

function savedComposerModelProviderState(state, value, defaultProvider, normalizeProviderRuntimeConfig, actionNotice) {
  return {
    providerConfig: normalizeProviderRuntimeConfig({
      ...state.providerConfig,
      codexModelProvider: value,
    }, state.provider || defaultProvider),
    actionNotice: actionNotice('模型渠道已保存', 'success'),
  };
}

function createSaveComposerModelConfigAction(runtime, deps) {
  const { composerModelConfigTarget, saveGlobalComposerModelConfig, saveThreadComposerModelConfig } = deps;
  return async (config = {}) => {
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
  };
}

function createRestoreComposerModelInheritanceAction(runtime, deps) {
  const { actionNotice, normalizeThreadConfig, setThreadConfig, threadConfigTargetIdForState } = deps;
  return async (config = {}) => {
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
      runtime.set((current) => restoredThreadModelConfigState(current, threadId, normalized, actionNotice));
      return true;
    }
    catch (error) {
      return runtime.notifyRPCFailure('恢复线程模型继承', 'thread.config.restore.failed', error, { threadId });
    }
  };
}

function createSaveComposerModelProviderAction(runtime, deps) {
  const {
    actionNotice,
    defaultProvider,
    normalizeProviderRuntimeConfig,
    providerPreferenceKey,
    requireProviderPreferenceValue,
    setPreference,
  } = deps;
  return async (codexModelProvider) => {
    const key = providerPreferenceKey('codex', 'codexModelProvider');
    const value = requireProviderPreferenceValue(codexModelProvider, key, 'composer.modelProvider.save');
    const cwd = runtime.requireCwd('composer.modelProvider.save');
    try {
      await setPreference({ cwd, key, value });
      runtime.set((state) => savedComposerModelProviderState(
        state,
        value,
        defaultProvider,
        normalizeProviderRuntimeConfig,
        actionNotice,
      ));
      return true;
    }
    catch (error) {
      return runtime.notifyRPCFailure('模型渠道保存', 'provider.model_provider.save.failed', error, { provider: 'codex' });
    }
  };
}

async function promoteNewDraftThread(runtime, request, deps) {
  const started = await deps.startNewDraftThread(request, deps.resolveLaunchPreferences);
  runtime.set((state) => deps.promotedDraftThreadState(state, request, started));
  return started.threadId;
}

function startDraftTurn(deps, request, threadId) {
  return deps.startTurnWithStoppedThreadRecovery({
    cwd: request.cwd,
    threadId,
    input: request.input,
    manualSkillSelection: false,
  });
}

async function retryDraftWithFreshThread(runtime, deps, context, error) {
  const activeRequest = context.activeRequest;
  if (!activeRequest.previousThreadId || !deps.isCodexIdentityAutoResumeError(error)) throw error;
  runtime.set((state) => deps.rollbackSendDraftState(state, activeRequest, error));
  const retryRequest = deps.freshThreadRetryRequest(activeRequest);
  context.activeRequest = retryRequest;
  context.threadId = '';
  runtime.set((state) => deps.optimisticSendDraftState(state, retryRequest));
  context.threadId = await promoteNewDraftThread(runtime, retryRequest, deps);
  await startDraftTurn(deps, retryRequest, context.threadId);
  return context;
}

async function startDraftTurnWithRecovery(runtime, deps, context) {
  if (!context.threadId) context.threadId = await promoteNewDraftThread(runtime, context.activeRequest, deps);
  try {
    await startDraftTurn(deps, context.activeRequest, context.threadId);
  }
  catch (error) {
    return retryDraftWithFreshThread(runtime, deps, context, error);
  }
  return context;
}

function clearSentDrafts(runtime, activeRequest, threadId) {
  runtime.clearComposerDraft({ ...runtime.get(), activeThreadId: activeRequest.previousActiveThreadId }, activeRequest.previousActiveThreadId);
  runtime.clearComposerDraft(runtime.get(), activeRequest.provisionalThreadId);
  runtime.clearComposerDraft(runtime.get(), threadId);
  runtime.set({ sending: false });
}

async function rollbackFailedSendDraft(runtime, deps, activeRequest, threadId, error) {
  const rollbackState = runtime.get();
  const createdThreadId = deps.createdThreadIdForSendRollback(rollbackState, activeRequest, threadId);
  const shouldCacheFailedDraft = !deps.sendRollbackRestoresVisibleComposer(rollbackState, activeRequest, createdThreadId);
  runtime.set((state) => deps.rollbackSendDraftState(state, activeRequest, error, { createdThreadId }));
  if (shouldCacheFailedDraft) deps.saveFailedSendDraftSnapshot(runtime, activeRequest);
  await deps.deleteProvisionalThreadAfterSendFailure(createdThreadId, runtime.addWarning);
  runtime.addWarning('error', 'thread.send.failed', { error: error.message });
}

function createSendDraftAction(runtime, deps) {
  const { createSendDraftRequest, optimisticSendDraftState } = deps;
  return async () => {
    const cwd = runtime.requireCwd('send message');
    const request = createSendDraftRequest(runtime.get(), cwd);
    if (!request) return false;
    runtime.set((state) => optimisticSendDraftState(state, request));

    const sendContext = { activeRequest: request, threadId: request.previousThreadId };
    try {
      await startDraftTurnWithRecovery(runtime, deps, sendContext);
      clearSentDrafts(runtime, sendContext.activeRequest, sendContext.threadId);
      return true;
    }
    catch (error) {
      await rollbackFailedSendDraft(runtime, deps, sendContext.activeRequest, sendContext.threadId, error);
      throw error;
    }
  };
}

function createComposerActionSet(runtime, deps) {
  return {
    selectFilesForComposer: createSelectFilesForComposerAction(runtime, deps.attachment),
    attachPathsForComposer: createAttachPathsForComposerAction(runtime, deps.attachment),
    attachDroppedFilesForComposer: createAttachDroppedFilesForComposerAction(runtime, deps.attachment),
    attachPastedImagesForComposer: createAttachPastedImagesForComposerAction(runtime, deps.attachment),
    removeAttachment: createRemoveAttachmentAction(runtime, deps.attachment),
    saveComposerModelConfig: createSaveComposerModelConfigAction(runtime, deps.model),
    restoreComposerModelInheritance: createRestoreComposerModelInheritanceAction(runtime, deps.model),
    saveComposerModelProvider: createSaveComposerModelProviderAction(runtime, deps.modelProvider),
    sendDraft: createSendDraftAction(runtime, deps.send),
    setDraft: (draft) => runtime.set({ draft }),
  };
}

export { createComposerActionSet };
