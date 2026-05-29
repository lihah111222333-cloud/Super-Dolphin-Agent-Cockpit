import React, { useState, useEffect, useMemo, useRef } from 'react';
import { template as UnifiedChatPageTemplate } from './UnifiedChatPage.template.js';
import { val, useVueSetup } from '../utils/vue-compat.js';
import { ChatToolbar } from '../components/unified-chat/ChatToolbar.jsx';
import { ThreadRailSidePanel } from '../components/unified-chat/ThreadRailSidePanel.jsx';
import { CmdCardGrid } from '../components/unified-chat/CmdCardGrid.jsx';
import { CmdOverviewPanel } from '../components/unified-chat/CmdOverviewPanel.jsx';
import { WorkspaceChatPanel } from '../components/unified-chat/WorkspaceChatPanel.jsx';
import { DiffPanel } from '../components/DiffPanel.jsx';
import { ComposerBar } from '../components/ComposerBar.jsx';
import { ContextUsageBanner } from '../components/ContextUsageBanner.jsx';
import { ComposerForkDraftCard } from '../components/ComposerForkDraftCard.jsx';
import { ActivityPanel } from '../components/ActivityPanel.jsx';
import { PathChoiceModal } from '../components/PathChoiceModal.jsx';

import {
  ref,
  computed,
  watch,
  onBeforeUnmount,
} from '../../lib/vue.esm-browser.prod.js';

import { isThreadErrorStatus, normalizeStatus } from '../services/status.js';
import { logInfo, logWarn } from '../services/log.js';
import { callAPI } from '../services/api.js';
import { observeContainerWidth, disconnectContainerObserver } from '../services/pretext-layout.js';
import { useComposerStore } from '../stores/composer.js';
import { useThreadStore } from '../stores/threads.js';
import { useProjectStore } from '../stores/projects.js';
import { useProviderMode } from '../composables/useProviderMode.js';
import { useAutoScroll } from '../composables/useAutoScroll.js';
import { useResizePanels } from '../composables/useResizePanels.js';
import { useDiffPreview } from '../composables/useDiffPreview.js';
import { useThreadStatus } from '../composables/useThreadStatus.js';
import { useThreadCards } from '../composables/useThreadCards.js';
import { useThreadSelection } from '../composables/useThreadSelection.js';
import { useInlineRename } from '../composables/useInlineRename.js';
import { useThreadActions, resolveProjectActionCwd, resolveProjectViewCwd } from '../composables/useThreadActions.js';
import { useKeyboardShortcuts } from '../composables/useKeyboardShortcuts.js';
import { useFileRefPreview } from '../composables/useFileRefPreview.js';
import { useCopyThreadInfo } from '../composables/useCopyThreadInfo.js';
import { useFileDrop } from '../composables/useFileDrop.js';
import { useForkThread } from '../composables/useForkThread.js';
import { getTokenLevel } from '../utils/format-utils.js';
import { useContextUsageThresholds } from '../composables/useContextUsageThresholds.js';
import { usePageLifecycle } from '../composables/usePageLifecycle.js';
import { createThreadConfigController } from '../composables/useThreadConfigController.js';

import { buildFocusedDiffSelection } from '../utils/diff-utils.js';
import { pinnedPlanCardSpec } from '../utils/plan-utils.js';
import { handleTimelineCitationClick } from '../utils/citation-action-utils.js';
import {
  buildUnifiedChatPageExposed,
  createPathChoiceController,
} from './UnifiedChatPage.helpers.js';

// Setup implementation reused for compatibility and React bridge
function UnifiedChatPageSetup(props, setupCtx = {}) {
  const useVueMode = typeof window !== 'undefined' && window.__VUE_SETUP_ACTIVE__;
  const threadStore = useVueMode ? useThreadStore() : (props.threadStore || useThreadStore());
  const projectStore = useVueMode ? useProjectStore() : (props.projectStore || useProjectStore());

  const compatProps = new Proxy(props, {
    get(target, key) {
      if (key === 'threadStore') return threadStore;
      if (key === 'projectStore') return projectStore;
      return target[key];
    }
  });

  const composer = useComposerStore();
  const composerBarRef = ref(null);
  const presenceAnchorRef = ref(null);
  const workspaceRef = ref(null);
  const isCmd = computed(() => compatProps.mode === 'cmd');
  const modeKey = computed(() => (isCmd.value ? 'cmd' : 'chat'));
  const showWorkspace = computed(() => true);
  const providerPreferenceCwd = computed(() => {
    const cwd = (projectStore.state?.active || '').toString().trim();
    if (!cwd || cwd === '.') return '';
    return cwd;
  });
  const {
    useClaudeProvider,
    providerPreferenceReady = ref(true),
    providerPreferenceError = ref(''),
    loadProviderPreference,
    toggleProviderMode,
  } = useProviderMode(providerPreferenceCwd);

  const layoutMode = computed({
    get: () => threadStore.getLayout(modeKey.value),
    set: (value) => threadStore.setLayout(modeKey.value, value),
  });
  const cmdCardCols = computed({
    get: () => (typeof threadStore.getCmdCardCols === 'function'
      ? threadStore.getCmdCardCols()
      : 3),
    set: (value) => {
      if (typeof threadStore.setCmdCardCols === 'function') {
        threadStore.setCmdCardCols(value);
      }
    },
  });
  const {
    dragging,
    threadRailDragging,
    activityPanelDragging,
    splitRatio,
    threadRailStyle,
    chatComposerShellStyle,
    activityPanelRowStyle,
    onResizeStart,
    onThreadRailResizeStart,
    onActivityResizeStart,
  } = useResizePanels({
    isCmd,
    modeKey,
    showWorkspace,
    threadStore: threadStore,
    workspaceRef,
  });

  const threads = computed(() => threadStore.getThreadsByMode(
    modeKey.value,
    resolveProjectViewCwd(projectStore, compatProps.windowCwd),
  ));
  const selectedThreadId = computed({
    get: () => {
      const res = resolveVisibleSelectedThreadId(threadStore, modeKey.value, threads.value);
      return res;
    },
    set: (value) => {
      if (isCmd.value) {
        threadStore.saveActiveCmdThread(value || '');
      } else {
        threadStore.saveActiveThread(value || '');
      }
    },
  });
  watch(
    () => [modeKey.value, selectedThreadId.value],
    () => {
      if (typeof composer.activateDraft === 'function') {
        composer.activateDraft(selectedThreadId.value, modeKey.value);
      }
    },
    { immediate: true, flush: 'sync' },
  );

  const pathChoiceController = createPathChoiceController(selectedThreadId);
  const isPreviewDirty = ref(false);
  
  // Vue setup lifecycle hooks are wrapped inside our useVueSetup hook, so in unit tests
  // (which mock onMounted/onBeforeUnmount in Vitest globally or locally via window shim)
  // this is fully compatible.
  const registerBeforeUnmount = window.__VUE_ON_BEFORE_UNMOUNT__ || onBeforeUnmount;
  registerBeforeUnmount(() => {
    pathChoiceController.cancelPathChoice();
    disconnectContainerObserver();
  });
  
  watch(selectedThreadId, () => { isPreviewDirty.value = false; });

  const sendBlockedNoticesByThread = ref(new Map());
  const sendHoldNoticesByThread = ref(new Map());
  const activeRawStatus = computed(() => (threadStore.getThreadStatus(selectedThreadId.value) || '').toString());
  const activeStatus = computed(() => normalizeStatus(activeRawStatus.value));
  const activeThreadSendBlocked = computed(() => {
    const threadId = (selectedThreadId.value || '').toString().trim();
    const storeBlocked = typeof threadStore.isThreadSendBlocked === 'function'
      ? threadStore.isThreadSendBlocked(threadId)
      : Boolean(threadId && threadStore?.state?.sendBlockedNoticesByThread?.[threadId]);
    const storeHeld = Boolean(threadId && threadStore?.state?.sendHoldNoticesByThread?.[threadId]);
    return isThreadErrorStatus(activeRawStatus.value)
      || Boolean(threadId && (sendBlockedNoticesByThread.value.has(threadId) || sendHoldNoticesByThread.value.has(threadId) || storeBlocked || storeHeld));
  });
  const threadStatus = useThreadStatus(compatProps, selectedThreadId, activeStatus, pathChoiceController.showPathChoiceModal);
  const activeThread = computed(() => threads.value.find((item) => item.id === selectedThreadId.value) || null);
  const activeProjectCwd = computed(() => resolveProjectViewCwd(projectStore, compatProps.windowCwd));
  const chatThreadOptions = computed(() => {
    if (isCmd.value) return [];
    return threads.value;
  });
  const showArchivedThreadList = ref(false);

  const activeTimeline = computed(() => threadStore.getThreadTimeline(selectedThreadId.value));
  const chatEmptyText = computed(() => {
    const name = (activeThread.value?.name || '').toString().trim();
    const items = Array.isArray(activeTimeline.value) ? activeTimeline.value : [];
    if (name === 'AI 设计流程' && items.length === 0) return '我们应该设计点什么？';
    return '暂无消息，先发送一句话试试。';
  });
  const activeThreadDiffText = computed(() => threadStore.getThreadDiff(selectedThreadId.value));

  const {
    focusedDiffPath,
    focusedDiffLine,
    pendingFileRefFocus,
    fallbackDiffText,
    fallbackMediaPreview,
    fallbackMarkdownPreview,
    activeMediaPreview,
    activeMarkdownPreview,
    activeDiffText,
    activeDiffFocusFile,
    activeDiffFocusLine,
    timelinePreview,
    diffPreview,
  } = useDiffPreview({
    activeThreadDiffText,
    threadStore: threadStore,
    buildFocusedDiffSelection,
  });

  const {
    scheduleScrollToBottom,
    scrollToTop,
    resetScrollState,
    saveScrollPosition,
    restoreScrollPosition,
    isAtBottom,
  } = useAutoScroll(workspaceRef);
  observeContainerWidth();

  if (typeof threadStore.setScrollGuard === 'function') {
    threadStore.setScrollGuard(saveScrollPosition, restoreScrollPosition);
  }

  const threadCards = useThreadCards(compatProps, {
    threads, chatThreadOptions, selectedThreadId, showArchivedThreadList, activeTimeline,
    isCmd, layoutMode, timelinePreview, diffPreview,
    getThreadStatusHeader: threadStatus.getThreadStatusHeader,
    isThreadInterruptible: threadStatus.isThreadInterruptible,
  });

  const copyThreadInfo = createPageCopyThreadInfo(selectedThreadId, activeProjectCwd, threadCards, activeThread, activeStatus, useClaudeProvider, compatProps);
  const forkPage = createPageForkThread(compatProps, { composer, selectedThreadId, activeThread, isCmd, emit: typeof setupCtx?.emit === 'function' ? setupCtx.emit : () => {} });
  const tokenLevelByThreadId = createPageTokenLevels(compatProps, threads);

  bindPageThreadSelection(compatProps, {
    selectedThreadId, pendingFileRefFocus, focusedDiffPath, focusedDiffLine, fallbackDiffText,
    fallbackMediaPreview, fallbackMarkdownPreview, scheduleScrollToBottom, resetScrollState,
  });

  const selectThread = (threadId) => selectThreadInPage(selectedThreadId, threadStore, threadId);
  const inlineRename = useInlineRename(compatProps, threadCards.visibleChatThreadCards, selectThread);

  const threadActions = createPageThreadActions(compatProps, {
    selectedThreadId, modeKey, isCmd, composer, layoutMode, cmdCardCols,
    compacting: threadStatus.compacting, isThreadInterruptible: threadStatus.isThreadInterruptible,
    beginInlineRename: inlineRename.beginInlineRename, scheduleScrollToBottom,
    showArchivedThreadList, providerPreferenceReady, providerPreferenceError, sendBlockedNoticesByThread, sendHoldNoticesByThread,
  });

  const threadConfigController = createThreadConfigController({ threadStore: threadStore, threadActions, selectedThreadId, isCmd });

  const keyboardShortcuts = useKeyboardShortcuts({
    selectedThreadId,
    canInterrupt: threadStatus.canInterrupt,
    isStatusTimerModalPaused: threadStatus.isStatusTimerModalPaused,
    stopSelected: threadActions.stopSelected,
  });

  const onPreviewDirtyChange = (nextDirty) => setPreviewDirtyFlag(isPreviewDirty, nextDirty);
  const confirmAbandonDirtyPreview = (meta) => confirmAbandonDirtyPreviewState(isPreviewDirty, meta);
  const fileRefPreview = createPageFileRefPreview(compatProps, {
    selectedThreadId, activeTimeline, activeThreadDiffText, focusedDiffPath, focusedDiffLine,
    fallbackDiffText, fallbackMediaPreview, fallbackMarkdownPreview,
    requestPathChoice: pathChoiceController.requestPathChoice, confirmAbandonDirtyPreview,
  });
  const onTimelineCitationClick = (payload) => handlePageTimelineCitation(payload, fileRefPreview, threads.value, selectedThreadId, composer, scheduleScrollToBottom);

  const { registerFileDrop } = useFileDrop(composer);
  usePageLifecycle({
    keyboardShortcuts,
    registerFileDrop,
    loadProviderPreference,
    copyThreadInfoCleanup: copyThreadInfo.cleanup,
    stopStatusTickTimer: threadStatus.stopStatusTickTimer,
  });

  const exposed = buildUnifiedChatPageExposed({
    composer, isCmd, threads, selectedThreadId, activeThread,
    chatThreadOptions, showArchivedThreadList,
    threadCards, threadStatus, threadActions, inlineRename, copyThreadInfo, fileRefPreview, batchDeleteStaleThreads: threadStore.batchDeleteStaleThreads,
    threadConfigUi: threadConfigController.threadConfigUi,
    updateThreadConfigModel: threadConfigController.updateThreadConfigModel,
    updateThreadConfigEffort: threadConfigController.updateThreadConfigEffort,
    saveThreadConfigDraft: threadConfigController.saveThreadConfigDraft,
    restoreThreadConfigInherit: threadConfigController.restoreThreadConfigInherit,
    keyboardShortcuts,
    useClaudeProvider, providerPreferenceReady, providerPreferenceError, toggleProviderMode,
    activeTimeline, chatEmptyText, activeDiffText, activeMediaPreview, activeMarkdownPreview,
    activeDiffFocusFile, activeDiffFocusLine, activeStatus, activeThreadSendBlocked,
    layoutMode, cmdCardCols, splitRatio, threadRailStyle, showWorkspace,
    chatComposerShellStyle, activityPanelRowStyle, timelinePreview, diffPreview,
    showPathChoiceModal: pathChoiceController.showPathChoiceModal,
    pathChoiceOptions: pathChoiceController.pathChoiceOptions,
    pathChoiceTitle: pathChoiceController.pathChoiceTitle,
    pathChoiceTruncated: pathChoiceController.pathChoiceTruncated,
    confirmPathChoice: pathChoiceController.confirmPathChoice,
    cancelPathChoice: pathChoiceController.cancelPathChoice,
    dragging, threadRailDragging, activityPanelDragging,
    composerBarRef, presenceAnchorRef, workspaceRef,
    onThreadRailResizeStart, onResizeStart, onActivityResizeStart,
    selectThread,
    pinnedPlanCardSpec,
    isAtBottom, scheduleScrollToBottom, scrollToTop, resetScrollState,
  });
  attachPageNonEnumerableState(exposed, {
    forkSubmitting: forkPage.submitting,
    forkError: forkPage.error,
    submitForkThread: forkPage.submit,
    openForkDraftFromUI: forkPage.open,
    forkSourceThreadName: forkPage.sourceThreadName,
    forkAvailableSharedFiles: forkPage.availableSharedFiles,
    tokenLevelByThreadId,
    onTimelineCitationClick,
    onPreviewDirtyChange,
    isPreviewDirty,
    isStatusTimerModalPaused: threadStatus.isStatusTimerModalPaused,
  });
  return exposed;
}

function makeStableRefCallback(vm, refKey) {
  return (el) => {
    if (vm && vm[refKey]) {
      if (el) {
        if (vm[refKey].value !== el) vm[refKey].value = el;
      } else {
        vm[refKey].value = null;
      }
    }
  };
}

// UnifiedChatPage React component
export function UnifiedChatPage(props) {
  const emit = useMemo(() => (event, ...args) => {
    if (event === 'clear-inherited-chat') props.onClearInheritedChat?.(...args);
  }, [props.onClearInheritedChat]);

  const vm = useVueSetup(UnifiedChatPageSetup, props, emit);

  const isCmd = val(vm.isCmd);
  const selectedThreadId = val(vm.selectedThreadId);

  const setWorkspaceRef = useMemo(() => makeStableRefCallback(vm, 'workspaceRef'), [vm]);
  const setPresenceAnchorRef = useMemo(() => makeStableRefCallback(vm, 'presenceAnchorRef'), [vm]);
  const setComposerBarRef = useMemo(() => makeStableRefCallback(vm, 'composerBarRef'), [vm]);

  return (
    <section className={`page active unified-chat-page ${isCmd ? 'mode-cmd' : 'mode-chat'}`} data-testid="chat-page">
      <ChatToolbar
        isCmd={isCmd}
        activeStatus={val(vm.activeStatus)}
        displayStatusText={val(vm.displayStatusText)}
        activeStatusMeta={val(vm.activeStatusMeta)}
        useClaudeProvider={val(vm.useClaudeProvider)}
        providerPreferenceReady={val(vm.providerPreferenceReady)}
        providerPreferenceError={val(vm.providerPreferenceError)}
        selectedThreadId={selectedThreadId}
        canInterrupt={val(vm.canInterrupt)}
        recoveringSelected={val(vm.recoveringSelected)}
        copyButtonLabel={val(vm.copyButtonLabel)}
        projectOptions={val(props.projectStore.projectOptions)}
        activeProject={val(props.projectStore.state.active)}
        layoutMode={val(vm.layoutMode)}
        cmdCardCols={val(vm.cmdCardCols)}
        windowCwd={props.windowCwd}
        cwdDisplay={props.cwdDisplay}
        onUpdateProject={(e) => props.projectStore.setActive(e)}
        onAddProject={() => props.projectStore.quickAdd()}
        onRemoveProject={(e) => props.projectStore.removeProject(e)}
        onSetCmdLayout={vm.setCmdLayout}
        onSetCmdCardCols={vm.setCmdCardCols}
        onCopyThreadInfo={vm.copySelectedThreadId}
        onStopSelected={vm.stopSelected}
        onToggleProviderMode={vm.toggleProviderMode}
        onLaunchOne={vm.launchOne}
        onRecoverSelected={vm.recoverSelected}
      />
      <div className="unified-main">
        {!isCmd && (
          <ThreadRailSidePanel
            showArchivedThreadList={val(vm.showArchivedThreadList)}
            activeChatThreadCount={val(vm.activeChatThreadCount)}
            archivedChatThreadCount={val(vm.archivedChatThreadCount)}
            visibleChatThreadCards={val(vm.visibleChatThreadCards)}
            threadRailDragging={val(vm.threadRailDragging)}
            threadRailStyle={val(vm.threadRailStyle)}
            editingThreadId={val(vm.editingThreadId)}
            editingAlias={val(vm.editingAlias)}
            renamingThreadId={val(vm.renamingThreadId)}
            setRenameInputRef={vm.setRenameInputRef}
            tokenLevelByThreadId={val(vm.tokenLevelByThreadId)}
            onOpenNewWindow={vm.openNewWindow}
            onToggleArchivedThreadList={vm.toggleArchivedThreadList}
            onSelectThread={vm.selectThread}
            onToggleThreadPin={vm.toggleThreadPin}
            onToggleThreadArchive={vm.toggleThreadArchive}
            onDeleteStaleThreads={vm.deleteStaleThreads}
            onBeginInlineRename={vm.beginInlineRename}
            onSubmitInlineRename={vm.submitInlineRename}
            onHandleInlineRenameEnter={vm.handleInlineRenameEnter}
            onCancelInlineRename={vm.cancelInlineRename}
            onHandleInlineRenameBlur={vm.handleInlineRenameBlur}
            onUpdateEditingAlias={(e) => { vm.editingAlias.value = e; }}
          />
        )}
        {!isCmd && (
          <div
            className={`thread-rail-resizer ${val(vm.threadRailDragging) ? 'dragging' : ''}`}
            role="separator"
            aria-orientation="vertical"
            aria-label="调整会话列表宽度"
            onMouseDown={vm.onThreadRailResizeStart}
          ></div>
        )}
        <div className="unified-center">
          {isCmd && (
            <section className="cmd-card-panel">
              <div className="overview-metrics">
                <div className="metric"><strong>{val(vm.stats).total}</strong><span>子Agent</span></div>
                <div className="metric"><strong>{val(vm.stats).running}</strong><span>执行中</span></div>
                <div className="metric"><strong>{val(vm.stats).thinking}</strong><span>思考/回复</span></div>
                <div className="metric"><strong>{val(vm.stats).editing}</strong><span>改文件</span></div>
                <div className="metric"><strong>{val(vm.stats).error}</strong><span>异常</span></div>
              </div>

              <CmdCardGrid
                cmdCards={val(vm.cmdCards)}
                layoutMode={val(vm.layoutMode)}
                cmdCardCols={val(vm.cmdCardCols)}
                onSelectThread={vm.selectThread}
                onLoadCardHistory={vm.loadCardHistory}
                onRenameCard={vm.renameCard}
                onStopCard={vm.stopCard}
              />
            </section>
          )}

          {val(vm.showOverview) && (
            <CmdOverviewPanel
              stats={val(vm.stats)}
              recentThreads={val(vm.recentThreads)}
              selectedThreadId={selectedThreadId}
              getDisplayName={vm.getDisplayName}
              onSelectThread={vm.selectThread}
            />
          )}

          {val(vm.showWorkspace) && (
            <div className="workspace-area">
              <div ref={setWorkspaceRef} id="agent-workspace" className="chat-workspace with-diff">
                <WorkspaceChatPanel
                  selectedThreadId={selectedThreadId}
                  splitRatio={val(vm.splitRatio)}
                  activePinnedPlan={val(vm.activePinnedPlan)}
                  noActiveThread={val(vm.noActiveThread)}
                  activeTimeline={val(vm.activeTimeline)}
                  activeStatus={val(vm.activeStatus)}
                  displayStatusText={val(vm.displayStatusText)}
                  activeStatusMeta={val(vm.activeStatusMeta)}
                  emptyText={val(vm.chatEmptyText)}
                  resolveThreadDisplayName={vm.resolveThreadDisplayName}
                  presenceTarget={val(vm.presenceAnchorRef)}
                  pinnedPlanCardSpec={vm.pinnedPlanCardSpec}
                  isAtBottom={val(vm.isAtBottom)}
                  onDismissPinnedPlan={vm.dismissPinnedPlan}
                  onFileRefClick={vm.onTimelineFileRefClick}
                  onCitationClick={vm.onTimelineCitationClick}
                  onScrollToBottom={() => vm.scheduleScrollToBottom(true)}
                  onScrollToTop={vm.scrollToTop}
                />
                <div className={`panel-resizer ${val(vm.dragging) ? 'dragging' : ''}`} onMouseDown={vm.onResizeStart}></div>
                <div className="workspace-right-col" style={{ flex: `0 0 ${100 - val(vm.splitRatio)}%` }}>
                  <DiffPanel
                    diffText={val(vm.activeDiffText)}
                    mediaPreview={val(vm.activeMediaPreview)}
                    markdownPreview={val(vm.activeMarkdownPreview)}
                    focusFile={val(vm.activeDiffFocusFile)}
                    focusLine={val(vm.activeDiffFocusLine)}
                    project={val(props.projectStore.state.active)}
                    projects={val(props.projectStore.state.projects)}
                    onFileRefClick={vm.onTimelineFileRefClick}
                    onCitationClick={vm.onTimelineCitationClick}
                    onPreviewDirtyChange={vm.onPreviewDirtyChange}
                  />
                </div>
              </div>

              <div className={`workspace-bottom-row ${isCmd ? 'is-cmd' : ''}`} style={val(vm.activityPanelRowStyle)}>
                <div className={`chat-composer-shell ${!isCmd ? 'for-chat' : ''}`} style={val(vm.chatComposerShellStyle)}>
                  {!isCmd && <div ref={setPresenceAnchorRef} className="chat-status-presence-anchor"></div>}
                  {!isCmd && selectedThreadId && (
                    <ContextUsageBanner
                      level={val(vm.activeTokenLevel)}
                      usedPercent={val(vm.activeTokenUsage)?.usedPercent || 0}
                      usedTokens={val(vm.activeTokenUsage)?.usedTokens || 0}
                      contextWindow={val(vm.activeTokenUsage)?.contextWindowTokens || 0}
                      canCompact={val(vm.canCompact)}
                      compacting={val(vm.compacting)}
                      onCompact={vm.compactCurrent}
                      onFork={() => vm.openForkDraftFromUI('context-banner')}
                    />
                  )}
                  {val(vm.sendFailureNotice) && (
                    <div
                      className="chat-send-failure-notice"
                      data-testid="chat-send-failure-notice"
                      role="alert"
                      aria-live="assertive"
                    >
                      {val(vm.sendFailureNotice)}
                    </div>
                  )}
                  {!isCmd && val(vm.composer.forkDraft)?.active && (
                    <ComposerForkDraftCard
                      forkDraft={val(vm.composer.forkDraft)}
                      submitting={val(vm.forkSubmitting)}
                      error={val(vm.forkError)}
                      sourceThreadName={val(vm.forkSourceThreadName)}
                      contextUsedPercent={val(vm.activeTokenUsage)?.usedPercent || 0}
                      availableSharedFiles={val(vm.forkAvailableSharedFiles)}
                      onClose={() => vm.composer.closeForkDraft()}
                      onSubmit={vm.submitForkThread}
                      onAddSharedFile={(e) => vm.composer.addForkSharedFile(e)}
                      onRemoveSharedFile={(e) => vm.composer.removeForkSharedFile(e)}
                    />
                  )}
                  <ComposerBar
                    ref={setComposerBarRef}
                    isCmd={isCmd}
                    composer={val(vm.composer)}
                    threadId={selectedThreadId}
                    interruptible={val(vm.canInterrupt)}
                    compacting={val(vm.compacting)}
                    canCompact={val(vm.canCompact)}
                    compactResultText={val(vm.compactResultText)}
                    compactResultTone={val(vm.compactResultTone)}
                    compactSuccessCount={val(vm.compactSuccessCount)}
                    tokenInline={val(vm.activeTokenInline)}
                    tokenTooltip={val(vm.activeTokenTooltip)}
                    tokenLevel={val(vm.activeTokenLevel)}
                    disabled={!selectedThreadId && !val(vm.providerPreferenceReady)}
                    sendDisabled={val(vm.activeThreadSendBlocked)}
                    threadConfigProvider={val(vm.threadConfigUi).meta.provider}
                    threadConfigSupportsOverride={val(vm.threadConfigUi).meta.supportsThreadOverride}
                    threadConfigDraftModel={val(vm.threadConfigUi).draft.model}
                    threadConfigDraftEffort={val(vm.threadConfigUi).draft.effort}
                    threadConfigLoading={val(vm.threadConfigUi).loading}
                    threadConfigSaving={val(vm.threadConfigUi).saving}
                    threadConfigNotice={val(vm.threadConfigUi).notice}
                    threadConfigNoticeLevel={val(vm.threadConfigUi).noticeLevel}
                    threadConfigMeta={val(vm.threadConfigUi).meta}
                    routerPreview={val(vm.routerPreview)}
                    onUpdateThreadConfigModel={vm.updateThreadConfigModel}
                    onUpdateThreadConfigEffort={vm.updateThreadConfigEffort}
                    onSaveThreadConfig={vm.saveThreadConfigDraft}
                    onRestoreThreadConfigInherit={vm.restoreThreadConfigInherit}
                    onSend={vm.send}
                    onInterrupt={vm.interruptCurrent}
                    onCompact={vm.compactCurrent}
                    onOpenForkDraft={() => vm.openForkDraftFromUI('composer-bar')}
                  />
                </div>
                {!isCmd && (
                  <div className="workspace-bottom-side">
                    <div className={`workspace-bottom-side-layer ${val(vm.activityPanelDragging) ? 'dragging' : ''}`}>
                      <div className={`activity-panel-resizer ${val(vm.activityPanelDragging) ? 'dragging' : ''}`} onMouseDown={vm.onActivityResizeStart}></div>
                      <ActivityPanel
                        stats={val(vm.activeActivityStats)}
                        alerts={val(vm.activeAlerts)}
                        processEvents={val(vm.activeProcessActivity)}
                      />
                    </div>
                  </div>
                )}
              </div>
            </div>
          )}
        </div>
      </div>
      <PathChoiceModal
        show={val(vm.showPathChoiceModal)}
        options={val(vm.pathChoiceOptions)}
        title={val(vm.pathChoiceTitle)}
        truncated={val(vm.pathChoiceTruncated)}
        onConfirm={vm.confirmPathChoice}
        onCancel={vm.cancelPathChoice}
      />
    </section>
  );
}

// Vue setup helper function mounted for compatibility with unit tests
UnifiedChatPage.setup = UnifiedChatPageSetup;

// Static props definitions mimicking Vue component structure for tests
UnifiedChatPage.props = {
  projectStore: { required: true },
  threadStore: { required: true },
  mode: { default: 'chat' },
  windowCwd: { default: '' },
  cwdDisplay: { default: '' },
  inheritedChatPayload: { default: null },
};

UnifiedChatPage.emits = ['clear-inherited-chat'];
UnifiedChatPage.template = UnifiedChatPageTemplate;

// Helper logic extracted from UnifiedChatPage.js
function selectThreadInPage(selectedThreadId, threadStore, threadId) {
  const nextThreadId = (threadId || '').toString().trim();
  const prevThreadId = (selectedThreadId.value || '').toString().trim();
  if (!nextThreadId) {
    logInfo('ui', 'chat.select.skipped.empty', {
      previous_thread_id: prevThreadId,
    });
    return;
  }
  logInfo('ui', 'chat.select.request', {
    previous_thread_id: prevThreadId,
    next_thread_id: nextThreadId,
  });
  if (nextThreadId === prevThreadId) {
    logWarn('ui', 'chat.select.same_card.refresh', { thread_id: nextThreadId, sync_runtime: false });
    threadStore.loadMessages(nextThreadId, 300, { syncRuntime: false });
    return;
  }
  selectedThreadId.value = nextThreadId;
}

function handlePageTimelineCitation(payload, fileRefPreview, threads, selectedThreadId, composer, scheduleScrollToBottom) {
  handleTimelineCitationClick({
    payload,
    fileRefPreview,
    threads,
    selectThread: (threadId) => { selectedThreadId.value = threadId; },
    composer,
    scheduleScrollToBottom,
    logInfo,
  });
}

function resolveVisibleSelectedThreadId(threadStore, mode, visibleThreads) {
  const id = (threadStore?.getCurrentThreadId?.(mode) || '').toString().trim();
  if (!id) return '';
  const list = Array.isArray(visibleThreads) ? visibleThreads : [];
  return list.some((item) => item?.id === id) ? id : '';
}

function bindPageThreadSelection(props, ctx) {
  useThreadSelection({
    selectedThreadId: ctx.selectedThreadId,
    threadStore: props.threadStore,
    pendingFileRefFocus: ctx.pendingFileRefFocus,
    focusedDiffPath: ctx.focusedDiffPath,
    focusedDiffLine: ctx.focusedDiffLine,
    fallbackDiffText: ctx.fallbackDiffText,
    fallbackMediaPreview: ctx.fallbackMediaPreview,
    fallbackMarkdownPreview: ctx.fallbackMarkdownPreview,
    scheduleScrollToBottom: ctx.scheduleScrollToBottom,
    resetScrollState: ctx.resetScrollState,
  });
}

function attachPageNonEnumerableState(exposed, ctx) {
  Object.defineProperties(exposed, {
    onTimelineCitationClick: { value: ctx.onTimelineCitationClick, enumerable: false, configurable: true },
    onPreviewDirtyChange: { value: ctx.onPreviewDirtyChange, enumerable: false, configurable: true },
    isPreviewDirty: { value: ctx.isPreviewDirty, enumerable: false, configurable: true },
    isStatusTimerModalPaused: { value: ctx.isStatusTimerModalPaused, enumerable: false, configurable: true },
    tokenLevelByThreadId: { value: ctx.tokenLevelByThreadId, enumerable: false, configurable: true },
    forkSubmitting: { value: ctx.forkSubmitting, enumerable: false, configurable: true },
    forkError: { value: ctx.forkError, enumerable: false, configurable: true },
    submitForkThread: { value: ctx.submitForkThread, enumerable: false, configurable: true },
    openForkDraftFromUI: { value: ctx.openForkDraftFromUI, enumerable: false, configurable: true },
    forkSourceThreadName: { value: ctx.forkSourceThreadName, enumerable: false, configurable: true },
    forkAvailableSharedFiles: { value: ctx.forkAvailableSharedFiles, enumerable: false, configurable: true },
  });
}

function createPageTokenLevels(props, threads) {
  const thresholds = useContextUsageThresholds();
  return computed(() => {
    const out = {};
    const list = Array.isArray(threads.value) ? threads.value : [];
    if (list.length === 0) return out;
    if (typeof props.threadStore?.getThreadTokenUsage !== 'function') return out;
    for (const thread of list) {
      const id = (thread?.id || '').toString();
      if (!id) continue;
      const level = getTokenLevel(props.threadStore.getThreadTokenUsage(id), thresholds.value);
      if (level !== 'normal') out[id] = level;
    }
    return out;
  });
}

function createPageThreadActions(props, ctx) {
  return useThreadActions(props, {
    selectedThreadId: ctx.selectedThreadId,
    modeKey: ctx.modeKey,
    isCmd: ctx.isCmd,
    composer: ctx.composer,
    layoutMode: ctx.layoutMode,
    cmdCardCols: ctx.cmdCardCols,
    compacting: ctx.compacting,
    isThreadInterruptible: ctx.isThreadInterruptible,
    beginInlineRename: ctx.beginInlineRename,
    scheduleScrollToBottom: ctx.scheduleScrollToBottom,
    showArchivedThreadList: ctx.showArchivedThreadList,
    sendBlockedNoticesByThread: ctx.sendBlockedNoticesByThread,
    sendHoldNoticesByThread: ctx.sendHoldNoticesByThread,
  });
}

function createPageFileRefPreview(props, ctx) {
  return useFileRefPreview(props, {
    selectedThreadId: ctx.selectedThreadId,
    activeTimeline: ctx.activeTimeline,
    activeThreadDiffText: ctx.activeThreadDiffText,
    focusedDiffPath: ctx.focusedDiffPath,
    focusedDiffLine: ctx.focusedDiffLine,
    fallbackDiffText: ctx.fallbackDiffText,
    fallbackMediaPreview: ctx.fallbackMediaPreview,
    fallbackMarkdownPreview: ctx.fallbackMarkdownPreview,
    requestPathChoice: ctx.requestPathChoice,
    confirmAbandonDirtyPreview: ctx.confirmAbandonDirtyPreview,
  });
}

async function fetchSharedFilesForFork(props) {
  const cwd = resolveProjectActionCwd(props.projectStore, props.windowCwd);
  const params = { page: 'memory', cwd };
  const res = await callAPI('ui/dashboard/get', params);
  if (!Array.isArray(res?.memory)) throw new Error('fork shared files response memory must be an array');
  return res.memory;
}

function createPageForkThread(props, ctx) {
  const forkThread = useForkThread({
    threadStore: props.threadStore,
    projectStore: props.projectStore,
    composer: ctx.composer,
    selectedThreadId: ctx.selectedThreadId,
    activeThread: ctx.activeThread,
    isCmd: ctx.isCmd,
    windowCwd: props.windowCwd,
  });
  const availableSharedFiles = ref(null);
  const availableSharedFilesError = ref('');
  let availableSharedFilesRequestSeq = 0;
  watch(() => [
    ctx.composer?.forkDraft?.active ? '1' : '0',
    (ctx.selectedThreadId.value || '').toString().trim(),
  ].join('\n'), async () => {
    const active = Boolean(ctx.composer?.forkDraft?.active);
    const sourceThreadId = (ctx.selectedThreadId.value || '').toString().trim();
    const requestSeq = ++availableSharedFilesRequestSeq;
    if (!active) return;
    const isCurrentRequest = () => requestSeq === availableSharedFilesRequestSeq
      && Boolean(ctx.composer?.forkDraft?.active)
      && (ctx.selectedThreadId.value || '').toString().trim() === sourceThreadId;
    availableSharedFilesError.value = '';
    availableSharedFiles.value = null;
    try {
      const files = await fetchSharedFilesForFork(props);
      if (!isCurrentRequest()) return;
      availableSharedFiles.value = files;
    } catch (error) {
      if (!isCurrentRequest()) return;
      availableSharedFilesError.value = (error?.message || String(error) || '加载共享文件失败').toString();
      logWarn('ui', 'forkDraft.shared_files.refresh_failed', { error: availableSharedFilesError.value });
    }
  });
  const forkSourceThreadName = computed(() => {
    const t = ctx.activeThread.value;
    return (t?.name || t?.id || '').toString();
  });
  async function submitForkThread() {
    try {
      const newId = await forkThread.submit();
      if (newId) {
        if (typeof props.threadStore.saveActiveThread === 'function' && !ctx.isCmd.value) {
          props.threadStore.saveActiveThread(newId);
        } else if (typeof props.threadStore.saveActiveCmdThread === 'function' && ctx.isCmd.value) {
          props.threadStore.saveActiveCmdThread(newId);
        }
      }
    } catch (error) {
      throw error;
    }
  }
  function openForkDraftFromUI(origin) {
    if (!ctx.selectedThreadId.value) return;
    ctx.composer.openForkDraft({ origin: origin || 'composer-bar' });
  }
  watch(
    () => props.inheritedChatPayload,
    (next) => {
      if (!next || typeof next !== 'object') return;
      const path = (next.sharedFilePath || '').toString().trim();
      ctx.composer.openForkDraft({ origin: 'shared-files', sharedFilePath: path });
      if (typeof ctx.emit === 'function') ctx.emit('clear-inherited-chat');
    },
    { immediate: true, flush: 'post' },
  );
  return {
    submitting: forkThread.submitting,
    error: computed(() => availableSharedFilesError.value || forkThread.error.value),
    kickoffError: forkThread.kickoffError,
    submit: submitForkThread,
    open: openForkDraftFromUI,
    sourceThreadName: forkSourceThreadName,
    availableSharedFiles,
  };
}

function createPageCopyThreadInfo(selectedThreadId, activeProjectCwd, threadCards, activeThread, activeStatus, useClaudeProvider, props) {
  return useCopyThreadInfo({
    selectedThreadId,
    activeRuntime: threadCards.activeRuntime,
    activeThread,
    activeStatus,
    useClaudeProvider,
    activeProjectCwd,
    threadStore: props.threadStore,
  });
}

function setPreviewDirtyFlag(isPreviewDirty, nextDirty) {
  isPreviewDirty.value = Boolean(nextDirty);
}

function confirmAbandonDirtyPreviewState(isPreviewDirty, meta) {
  if (!isPreviewDirty.value) return true;
  if (typeof window === 'undefined' || typeof window.confirm !== 'function') return true;
  const target = meta?.rawPath ? ` (切换到 ${meta.rawPath})` : '';
  const confirmed = window.confirm(`当前文件有未保存的修改，是否放弃？${target}`);
  if (confirmed) isPreviewDirty.value = false;
  return confirmed;
}
