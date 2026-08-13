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
      runtime.notifyAction('选择附件失败，请重试。', 'error', { category: 'attachment' });
      runtime.addWarning('error', 'attachments.select.failed', { error: 'action failure; see Health diagnostic ID' });
      throw error;
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
    let cwd;
    try {
      cwd = runtime.requireCwd('composer.model.save');
    }
    catch {
      // 无可用项目 cwd 时保存必然失败；给用户可见反馈而不是静默 reject。
      runtime.notifyAction('请先选择项目，再保存模型配置', 'error', { category: 'model' });
      return false;
    }
    const state = runtime.get();
    const target = await composerModelConfigTarget(config, state, runtime.get().loadThreadConfig);
    if (target.threadId && !target.threadConfig) {
      runtime.notifyAction('线程配置加载失败，无法保存模型配置', 'error', { threadId: target.threadId });
      throw new TypeError('thread config is required for composer model save');
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
      runtime.notifyRPCFailure('恢复线程模型继承', 'thread.config.restore.failed', error, { threadId });
      throw error;
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
      runtime.notifyRPCFailure('模型渠道保存', 'provider.model_provider.save.failed', error, { provider: 'codex' });
      throw error;
    }
  };
}

async function promoteNewDraftThread(runtime, request, deps) {
  const started = await deps.startNewDraftThread(request, deps.resolveLaunchPreferences);
  runtime.set((state) => deps.promotedDraftThreadState(state, request, started));
  if (runtime.pendingTurnStart) {
    runtime.pendingTurnStart.threadId = started.threadId;
    runtime.pendingTurnStart.localTurnId = request.localTurnId;
  }
  return started.threadId;
}

function startDraftTurnPayload(runtime, request, threadId) {
  const params = {
    cwd: request.cwd,
    threadId,
    input: request.input,
    localTurnId: request.localTurnId,
    ...request.capabilityPayload,
  };
  const preparingCancelRequestId = depsNormalizeTurnId(runtime.pendingTurnStart?.interruptRequestId);
  if (preparingCancelRequestId) params.preparingCancelRequestId = preparingCancelRequestId;
  return { params, preparingCancelRequestId };
}

async function startDraftTurn(runtime, deps, request, threadId) {
  const start = startDraftTurnPayload(runtime, request, threadId);
  return {
    result: await deps.startTurnWithStoppedThreadRecovery(start.params),
    preparingCancelRequestId: start.preparingCancelRequestId,
  };
}

function canonicalStartedTurn(observed, localTurnId, threadId) {
  const nextTurn = { id: localTurnId, threadId, status: 'running' };
  if (!observed || typeof observed !== 'object') return nextTurn;
  Object.assign(nextTurn, observed);
  nextTurn.id = localTurnId;
  nextTurn.threadId = threadId;
  if (typeof observed.status !== 'string' || observed.status.trim() === '') nextTurn.status = 'running';
  return nextTurn;
}

function recordCanonicalStartedTurn(runtime, threadId, result) {
  const localTurnId = depsNormalizeTurnId(result?.turn_id);
  if (!localTurnId) throw new Error('turn/start response missing local turn id');
  runtime.set((state) => {
    const observed = state.activeTurnByThread?.[threadId];
    return {
      activeTurnByThread: {
        ...state.activeTurnByThread,
        [threadId]: canonicalStartedTurn(observed, localTurnId, threadId),
      },
    };
  });
  return localTurnId;
}

function exposeRetryableStartInterruptFailure(runtime, threadId, result) {
  if (result?.interrupt_retryable !== true) return;
  if (runtime.pendingTurnStart) runtime.pendingTurnStart.interruptRequested = false;
  runtime.notifyAction('停止请求未送达，任务仍在运行，可再次停止', 'warning', { threadId });
  runtime.addWarning('warn', 'thread.interrupt.delivery_retryable', { threadId, code: result.interrupt_retryable_code || 'REGISTERED_INTERRUPT_DELIVERY_RETRYABLE' });
}

function exposeStartDurabilityDiagnostic(runtime, threadId, result) {
  if (result?.start_diagnostic_code !== 'TURN_DEDUPE_PROVIDER_ID_BIND_FAILED') return;
  if (runtime.pendingTurnStart?.cancelled) runtime.pendingTurnStart.interruptRequested = false;
  runtime.notifyAction('任务已启动，但启动去重状态未持久化', 'warning', { threadId });
  runtime.addWarning('warn', 'thread.start.dedupe_provider_id_bind_failed', { threadId, code: result.start_diagnostic_code });
}

function depsNormalizeTurnId(value) {
  return typeof value === 'string' ? value.trim() : '';
}

function requestWithLocalTurnID(request, deps) {
  const existing = depsNormalizeTurnId(request.localTurnId);
  if (existing) return request;
  const localTurnId = depsNormalizeTurnId(deps.createLocalTurnID());
  if (!localTurnId) throw new Error('turn/start local turn id generator returned an empty value');
  return { ...request, localTurnId };
}

async function retryDraftWithFreshThread(runtime, deps, context, error) {
  const activeRequest = context.activeRequest;
  if (!activeRequest.previousThreadId || !deps.isCodexIdentityAutoResumeError(error)) throw error;
  runtime.set((state) => deps.rollbackSendDraftState(state, activeRequest, error));
  const retryRequest = requestWithLocalTurnID(deps.freshThreadRetryRequest(activeRequest), deps);
  context.activeRequest = retryRequest;
  context.threadId = '';
  runtime.set((state) => deps.optimisticSendDraftState(state, retryRequest));
  context.threadId = await promoteNewDraftThread(runtime, retryRequest, deps);
  const started = await startDraftTurn(runtime, deps, retryRequest, context.threadId);
  context.preparingCancelRequestId = started.preparingCancelRequestId;
  context.turnId = recordCanonicalStartedTurn(runtime, context.threadId, started.result);
  exposeRetryableStartInterruptFailure(runtime, context.threadId, started.result);
  exposeStartDurabilityDiagnostic(runtime, context.threadId, started.result);
  context.started = true;
  return context;
}

async function interruptCancelledStartedTurn(runtime, context) {
  const pending = runtime.pendingTurnStart;
  if (!pending?.cancelled) return;
  const pendingRequestId = depsNormalizeTurnId(pending.interruptRequestId);
  if (pending.interruptRequested === true && pendingRequestId && pendingRequestId === context.preparingCancelRequestId) {
    context.cancelled = true;
    return;
  }
  const interruptActiveThread = runtime.get().interruptActiveThread;
  if (typeof interruptActiveThread !== 'function') throw new Error('canonical thread interrupt capability is unavailable');
  const interrupted = await interruptActiveThread({
    activeTurnTarget: { threadId: context.threadId, turnId: context.turnId },
    ...(pendingRequestId ? { requestId: pendingRequestId } : {}),
  });
  if (!interrupted) throw new Error('pending turn cancellation was not accepted');
  context.cancelled = true;
}

async function startDraftTurnWithRecovery(runtime, deps, context) {
  if (!context.threadId) context.threadId = await promoteNewDraftThread(runtime, context.activeRequest, deps);
  try {
    const started = await startDraftTurn(runtime, deps, context.activeRequest, context.threadId);
    context.preparingCancelRequestId = started.preparingCancelRequestId;
    context.turnId = recordCanonicalStartedTurn(runtime, context.threadId, started.result);
    exposeRetryableStartInterruptFailure(runtime, context.threadId, started.result);
    exposeStartDurabilityDiagnostic(runtime, context.threadId, started.result);
    context.started = true;
  }
  catch (error) {
    await retryDraftWithFreshThread(runtime, deps, context, error);
  }
  await interruptCancelledStartedTurn(runtime, context);
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
  const displayMessage = deps.recoveryActionMessageFromRPCError(error) || 'action failure; see Health diagnostic ID';
  runtime.addWarning('error', 'thread.send.failed', { error: displayMessage });
}

function finishSendDraft(runtime, deps, sendContext) {
  if (!sendContext.cancelled) {
    clearSentDrafts(runtime, sendContext.activeRequest, sendContext.threadId);
    return true;
  }
  runtime.set({
    sending: false,
    draft: sendContext.activeRequest.previousDraft,
    attachments: sendContext.activeRequest.previousAttachments,
    composerCapabilities: sendContext.activeRequest.previousComposerCapabilities,
    actionNotice: deps.actionNotice('本轮已取消', 'info'),
  });
  return true;
}

function createSendDraftAction(runtime, deps) {
  const { createSendDraftRequest, optimisticSendDraftState } = deps;
  return async () => {
    let request;
    try {
      const cwd = runtime.requireCwd('send message');
      request = createSendDraftRequest(runtime.get(), cwd);
    }
    catch (error) {
      runtime.notifyAction('无法发送：请重新打开目标会话，确认项目后再重试。', 'error', {
        category: 'thread-scope',
      });
      runtime.addWarning('error', 'thread.send.scope_invalid', {
        error: error instanceof Error ? error.message : 'unknown send scope error',
      });
      throw error;
    }
    if (!request) return false;
    request = requestWithLocalTurnID(request, deps);
    runtime.set((state) => optimisticSendDraftState(state, request));

    const sendContext = { activeRequest: request, threadId: request.previousThreadId };
    runtime.pendingTurnStart = {
      cancelled: false,
      interruptRequested: false,
      interruptRequestId: '',
      localTurnId: request.localTurnId,
      threadId: request.previousThreadId || request.provisionalThreadId,
    };
    try {
      await startDraftTurnWithRecovery(runtime, deps, sendContext);
      return finishSendDraft(runtime, deps, sendContext);
    }
    catch (error) {
      if (sendContext.started) runtime.set({ sending: false });
      else await rollbackFailedSendDraft(runtime, deps, sendContext.activeRequest, sendContext.threadId, error);
      throw error;
    } finally {
      runtime.pendingTurnStart = null;
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
    addComposerCapability: (capability) => runtime.set((state) => ({
      composerCapabilities: deps.capability.addComposerCapability(
        state.composerCapabilities,
        capability,
      ),
    })),
    removeComposerCapability: (key) => runtime.set((state) => ({
      composerCapabilities: deps.capability.removeComposerCapability(
        state.composerCapabilities,
        key,
      ),
    })),
    reconcileComposerCapabilities: (catalog) => runtime.set((state) => ({
      composerCapabilities: deps.capability.reconcileComposerCapabilities(
        state.composerCapabilities,
        catalog,
      ),
    })),
    saveComposerModelConfig: createSaveComposerModelConfigAction(runtime, deps.model),
    restoreComposerModelInheritance: createRestoreComposerModelInheritanceAction(runtime, deps.model),
    saveComposerModelProvider: createSaveComposerModelProviderAction(runtime, deps.modelProvider),
    sendDraft: createSendDraftAction(runtime, deps.send),
    clearComposer: () => runtime.set({
      draft: '',
      attachments: [],
      composerCapabilities: [],
    }),
    setDraft: (draft) => runtime.set({ draft }),
  };
}

export { createComposerActionSet };
