import { ref, watch } from '../../lib/vue.esm-browser.prod.js';

export function buildUnifiedChatPageExposed(ctx) {
  const {
    composer,
    isCmd,
    threads,
    selectedThreadId,
    activeThread,
    chatThreadOptions,
    showArchivedThreadList,
    threadCards,
    threadStatus,
    threadActions,
    inlineRename,
    copyThreadInfo,
    fileRefPreview,
    taskHandoff,
    threadConfigUi,
    updateThreadConfigModel,
    updateThreadConfigEffort,
    saveThreadConfigDraft,
    restoreThreadConfigInherit,
    useClaudeProvider,
    providerPreferenceReady,
    providerPreferenceError,
    toggleProviderMode,
    activeTimeline,
    activeDiffText,
    activeMediaPreview,
    activeMarkdownPreview,
    activeDiffFocusFile,
    activeDiffFocusLine,
    activeStatus,
    activeThreadSendBlocked,
    layoutMode,
    cmdCardCols,
    splitRatio,
    threadRailStyle,
    showWorkspace,
    chatComposerShellStyle,
    activityPanelRowStyle,
    timelinePreview,
    diffPreview,
    showPathChoiceModal,
    pathChoiceOptions,
    pathChoiceTitle,
    pathChoiceTruncated,
    confirmPathChoice,
    cancelPathChoice,
    dragging,
    threadRailDragging,
    activityPanelDragging,
    composerBarRef,
    presenceAnchorRef,
    workspaceRef,
    onThreadRailResizeStart,
    onResizeStart,
    onActivityResizeStart,
    selectThread,
    isAtBottom,
    scheduleScrollToBottom,
    scrollToTop,
    resetScrollState,
  } = ctx;

  return {
    composer,
    isCmd,
    threads,
    selectedThreadId,
    activeThread,
    chatThreadOptions,
    showArchivedThreadList,
    chatActiveThreadCards: threadCards.chatActiveThreadCards,
    chatArchivedThreadCards: threadCards.chatArchivedThreadCards,
    visibleChatThreadCards: threadCards.visibleChatThreadCards,
    activeChatThreadCount: threadCards.activeChatThreadCount,
    archivedChatThreadCount: threadCards.archivedChatThreadCount,
    activeTimeline,
    activeDiffText,
    activeMediaPreview,
    activeMarkdownPreview,
    activeDiffFocusFile,
    activeDiffFocusLine,
    activeStatus,
    activeThreadSendBlocked,
    activeStatusHeader: threadStatus.activeStatusHeader,
    activeStatusDetails: threadStatus.activeStatusDetails,
    activeStatusMeta: threadStatus.activeStatusMeta,
    activeTokenInline: threadStatus.activeTokenInline,
    activeTokenTooltip: threadStatus.activeTokenTooltip,
    activeTokenLevel: threadStatus.activeTokenLevel,
    activeTokenUsage: threadStatus.activeTokenUsage,
    compacting: threadStatus.compacting,
    canCompact: threadStatus.canCompact,
    compactResultText: threadStatus.compactResultText,
    compactResultTone: threadStatus.compactResultTone,
    compactSuccessCount: threadStatus.compactSuccessCount,
    canInterrupt: threadStatus.canInterrupt,
    recoveringSelected: threadActions.recoveringSelected,
    sendFailureNotice: threadActions.sendFailureNotice,
    displayStatusText: threadStatus.displayStatusText,
    noActiveThread: threadCards.noActiveThread,
    copyButtonLabel: copyThreadInfo.copyButtonLabel,
    layoutMode,
    cmdCardCols,
    splitRatio,
    threadRailStyle,
    showOverview: threadCards.showOverview,
    showWorkspace,
    chatComposerShellStyle,
    activityPanelRowStyle,
    activePinnedPlan: threadCards.activePinnedPlan,
    activeTask: taskHandoff.activeTask,
    taskHandoffVisible: taskHandoff.taskHandoffVisible,
    taskHandoffLoading: taskHandoff.taskHandoffLoading,
    taskHandoffError: taskHandoff.taskHandoffError,
    taskHandoffPreview: taskHandoff.taskHandoffPreview,
    taskHandoffUpdatedAt: taskHandoff.taskHandoffUpdatedAt,
    taskHandoffUpdatedBy: taskHandoff.taskHandoffUpdatedBy,
    taskStripExpanded: taskHandoff.taskStripExpanded,
    continueTaskBusy: taskHandoff.continueTaskBusy,
    stats: threadCards.stats,
    recentThreads: threadCards.recentThreads,
    cmdCards: threadCards.cmdCards,
    dragging,
    threadRailDragging,
    activityPanelDragging,
    composerBarRef,
    presenceAnchorRef,
    workspaceRef,
    activeActivityStats: threadStatus.activeActivityStats,
    activeAlerts: threadStatus.activeAlerts,
    activeProcessActivity: threadCards.activeProcessActivity,
    selectThread,
    launchOne: threadActions.launchOne,
    send: threadActions.send,
    refreshTaskHandoff: taskHandoff.refreshTaskHandoff,
    continueCurrentTask: taskHandoff.continueCurrentTask,
    startNewTaskFromHandoff: taskHandoff.startNewTaskFromHandoff,
    // F1：kickoff 失败不走 main error，独立暴露 ref 让 banner/toast 能消费
    // （当前 UI 还没接 banner，先留口子——跟 fork-thread 的 review M1 同款修补）。
    taskHandoffKickoffError: taskHandoff.taskHandoffKickoffError,
    continueCurrentTaskInNewWindow: taskHandoff.continueCurrentTaskInNewWindow,
    toggleTaskStrip: taskHandoff.toggleTaskStrip,
    threadConfigUi,
    updateThreadConfigModel,
    updateThreadConfigEffort,
    saveThreadConfigDraft,
    restoreThreadConfigInherit,
    useClaudeProvider,
    providerPreferenceReady,
    providerPreferenceError,
    toggleProviderMode,
    interruptCurrent: threadActions.interruptCurrent,
    compactCurrent: threadActions.compactCurrent,
    recoverSelected: threadActions.recoverSelected,
    setCmdLayout: threadActions.setCmdLayout,
    setCmdCardCols: threadActions.setCmdCardCols,
    copySelectedThreadId: copyThreadInfo.copySelectedThreadId,
    timelinePreview,
    diffPreview,
    showPathChoiceModal,
    pathChoiceOptions,
    pathChoiceTitle,
    pathChoiceTruncated,
    confirmPathChoice,
    cancelPathChoice,
    onThreadRailResizeStart,
    onResizeStart,
    onActivityResizeStart,
    stopSelected: threadActions.stopSelected,
    renameSelected: threadActions.renameSelected,
    isAtBottom,
    scheduleScrollToBottom,
    scrollToTop,
    resetScrollState,
    loadCardHistory: threadActions.loadCardHistory,
    renameCard: threadActions.renameCard,
    stopCard: threadActions.stopCard,
    toggleThreadPin: threadActions.toggleThreadPin,
    toggleThreadArchive: threadActions.toggleThreadArchive,
    toggleArchivedThreadList: threadActions.toggleArchivedThreadList,
    openNewWindow: threadActions.openNewWindow,
    editingThreadId: inlineRename.editingThreadId,
    editingAlias: inlineRename.editingAlias,
    renamingThreadId: inlineRename.renamingThreadId,
    setRenameInputRef: inlineRename.setRenameInputRef,
    beginInlineRename: inlineRename.beginInlineRename,
    submitInlineRename: inlineRename.submitInlineRename,
    handleInlineRenameEnter: inlineRename.handleInlineRenameEnter,
    cancelInlineRename: inlineRename.cancelInlineRename,
    handleInlineRenameBlur: inlineRename.handleInlineRenameBlur,
    getDisplayName: threadActions.getDisplayName,
    resolveThreadDisplayName: threadActions.resolveThreadDisplayName,
    dismissPinnedPlan: threadCards.dismissPinnedPlan,
    deleteStaleThreads: ctx.batchDeleteStaleThreads,
    pinnedPlanCardSpec: ctx.pinnedPlanCardSpec,
    onTimelineFileRefClick: fileRefPreview.onTimelineFileRefClick,
  };
}

export function createPathChoiceController(selectedThreadId) {
  const showPathChoiceModal = ref(false);
  const pathChoiceOptions = ref([]);
  const pathChoiceTitle = ref('选择文件路径');
  const pathChoiceTruncated = ref(false);
  let resolvePathChoice = null;

  function resetPathChoiceState() {
    showPathChoiceModal.value = false;
    pathChoiceOptions.value = [];
    pathChoiceTitle.value = '选择文件路径';
    pathChoiceTruncated.value = false;
  }

  function settlePathChoice(selectedPath = '') {
    const resolve = resolvePathChoice;
    resolvePathChoice = null;
    resetPathChoiceState();
    if (resolve) {
      resolve((selectedPath || '').toString().trim());
    }
  }

  function requestPathChoice(options, meta = {}) {
    const normalizedOptions = Array.isArray(options)
      ? options.map((item) => (item || '').toString().trim()).filter(Boolean)
      : [];
    if (!normalizedOptions.length) return Promise.resolve('');
    settlePathChoice('');
    pathChoiceOptions.value = [...new Set(normalizedOptions)];
    pathChoiceTitle.value = ((meta?.title || '').toString().trim()) || '选择文件路径';
    pathChoiceTruncated.value = Boolean(meta?.truncated);
    showPathChoiceModal.value = true;
    return new Promise((resolve) => {
      resolvePathChoice = resolve;
    });
  }

  function confirmPathChoice(selectedPath) {
    const nextPath = (selectedPath || '').toString().trim();
    if (!nextPath) return;
    settlePathChoice(nextPath);
  }

  function cancelPathChoice() {
    settlePathChoice('');
  }

  watch(
    () => (selectedThreadId.value || '').toString().trim(),
    (next, prev) => {
      if (!showPathChoiceModal.value || next === prev) return;
      cancelPathChoice();
    },
  );

  return {
    showPathChoiceModal,
    pathChoiceOptions,
    pathChoiceTitle,
    pathChoiceTruncated,
    requestPathChoice,
    confirmPathChoice,
    cancelPathChoice,
  };
}
