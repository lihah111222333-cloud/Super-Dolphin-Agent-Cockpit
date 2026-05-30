import {
  ref,
  computed,
  watch,
  onBeforeUnmount,
} from '../../lib/vue.esm-browser.prod.js';
import { ChatToolbar } from '../components/unified-chat/ChatToolbar.js';
import { ThreadRailSidePanel } from '../components/unified-chat/ThreadRailSidePanel.js';
import { CmdCardGrid } from '../components/unified-chat/CmdCardGrid.js';
import { CmdOverviewPanel } from '../components/unified-chat/CmdOverviewPanel.js';
import { WorkspaceChatPanel } from '../components/unified-chat/WorkspaceChatPanel.js';
import { DiffPanel } from '../components/DiffPanel.js';
import { ComposerBar } from '../components/ComposerBar.js';
import { ContextUsageBanner } from '../components/ContextUsageBanner.js';
import { ComposerForkDraftCard } from '../components/ComposerForkDraftCard.js';
import { ActivityPanel } from '../components/ActivityPanel.js';
import { PathChoiceModal } from '../components/PathChoiceModal.js';
import { isThreadErrorStatus, normalizeStatus } from '../services/status.js';
import { logInfo, logWarn } from '../services/log.js';
import { callAPI } from '../services/api.js';
import { observeContainerWidth, disconnectContainerObserver } from '../services/pretext-layout.js';
import { useComposerStore } from '../stores/composer.js';
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
import { useMultiAgentLaunch } from '../composables/useMultiAgentLaunch.js';
import { getTokenLevel } from '../utils/format-utils.js';
import { useContextUsageThresholds } from '../composables/useContextUsageThresholds.js';
import { usePageLifecycle } from '../composables/usePageLifecycle.js';
import { createThreadConfigController } from '../composables/useThreadConfigController.js';

import {
  buildFocusedDiffSelection,
} from '../utils/diff-utils.js';
import {
  pinnedPlanCardSpec,
} from '../utils/plan-utils.js';
import { handleTimelineCitationClick } from '../utils/citation-action-utils.js';
import {
  buildUnifiedChatPageExposed,
  createPathChoiceController,
} from './UnifiedChatPage.helpers.js';
import { template } from './UnifiedChatPage.template.js';

function formatMultiAgentLaunchFailure(error) {
  const detail = (error?.message || String(error || '') || '未知错误').toString().trim();
  return `创建子 Agent 失败：${detail}`;
}

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
  return list.some((/** @type {any} */ item) => item?.id === id) ? id : '';
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
    // Phase 2 fork-draft
    forkSubmitting: { value: ctx.forkSubmitting, enumerable: false, configurable: true },
    forkError: { value: ctx.forkError, enumerable: false, configurable: true },
    submitForkThread: { value: ctx.submitForkThread, enumerable: false, configurable: true },
    openForkDraftFromUI: { value: ctx.openForkDraftFromUI, enumerable: false, configurable: true },
    forkSourceThreadName: { value: ctx.forkSourceThreadName, enumerable: false, configurable: true },
    forkAvailableSharedFiles: { value: ctx.forkAvailableSharedFiles, enumerable: false, configurable: true },
  });
}

function createPageTokenLevels(props, threads) {
  // 为侧边栏所有会话计算 token 警报等级 map。仅在 tokenUsageByThread / 阈值变动时重算。
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
  // 卡片打开时拉一次共享文件列表，供内联选择器使用。
  // 初始为 null 表示“未拉过”，拉后是数组（可为空）；这让卡片能区分 加载中 / 空库。
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
    availableSharedFiles.value = null; // 重置为 loading，保证重复打开也有 loading 闪一下
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
  // 跨页 payload：SharedFilesPage 点「用此文件新建对话」 → app.js 设 payload
  watch(
    () => props.inheritedChatPayload,
    (next) => {
      if (!next || typeof next !== 'object') return;
      const path = (next.sharedFilePath || '').toString().trim();
      ctx.composer.openForkDraft({ origin: 'shared-files', sharedFilePath: path });
      if (typeof ctx.emit === 'function') ctx.emit('clear-inherited-chat');
    },
    // SharedFilesPage 会先设置 payload 再把 page 切到 chat；UnifiedChatPage
    // 重新 mount 时 payload 已经存在，所以这里必须 immediate 才能消费跨页意图。
    { immediate: true, flush: 'post' },
  );
  return {
    submitting: forkThread.submitting,
    error: computed(() => availableSharedFilesError.value || forkThread.error.value),
    // review M1 收尾：暴露 kickoffError 给将来的 banner/toast 消费——草稿已关时
    // 错误显示在新 thread 的 UI 上更合理；当前还没接 banner 系统，先留口子。
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

/**
 * @typedef {Object} CodeOpenSnippetLine
 * @property {number} [line]
 * @property {string} [text]
 */

/**
 * @typedef {Object} CodeOpenResult
 * @property {boolean} [ok]
 * @property {string} [relative]
 * @property {string} [filePath]
 * @property {boolean} [image]
 * @property {string} [plugin]
 * @property {string} [mediaType]
 * @property {string} [previewURL]
 * @property {string} [thumbnailURL]
 * @property {number} [sizeBytes]
 * @property {number} [startLine]
 * @property {number} [endLine]
 * @property {number} [totalLines]
 * @property {string} [language]
 * @property {string | CodeOpenSnippetLine[]} [snippet]
 */

/**
 * @typedef {import('../utils/thread-page-types').ProcessActivityItem} ProcessActivityItem
 */



export const UnifiedChatPage = {
  name: 'UnifiedChatPage',
  components: {
    ChatToolbar,
    ThreadRailSidePanel,
    CmdCardGrid,
    CmdOverviewPanel,
    WorkspaceChatPanel,
    DiffPanel,
    ComposerBar,
    ContextUsageBanner,
    ComposerForkDraftCard,
    ActivityPanel,
    PathChoiceModal,
  },
  props: {
    projectStore: { type: Object, required: true },
    threadStore: { type: Object, required: true },
    mode: { type: String, default: 'chat' },
    /** 窗口实际 CWD（绝对路径） */
    windowCwd: { type: String, default: '' },
    /** 完整展示文本 */
    cwdDisplay: { type: String, default: '' },
    /** Phase 2: 跨页面在 SharedFilesPage 点「用此文件新建对话」后传入的 payload，只读 */
    inheritedChatPayload: { type: Object, default: null },
  },
  emits: ['clear-inherited-chat'],
  /**
   * @param {{
   *  projectStore: any,
   *  threadStore: any,
   *  mode?: string,
   * }} props
   */
  setup(props, setupCtx = {}) {
    const composer = useComposerStore();
    const composerBarRef = ref(null);
    const presenceAnchorRef = ref(null);
    const workspaceRef = ref(null);
    const isCmd = computed(() => props.mode === 'cmd');
    const modeKey = computed(() => (isCmd.value ? 'cmd' : 'chat'));
    const showWorkspace = computed(() => true);
    const providerPreferenceCwd = computed(() => {
      const cwd = (props.projectStore?.state?.active || '').toString().trim();
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
      get: () => props.threadStore.getLayout(modeKey.value),
      set: (/** @type {string} */ value) => props.threadStore.setLayout(modeKey.value, value),
    });
    const cmdCardCols = computed({
      get: () => (typeof props.threadStore.getCmdCardCols === 'function'
        ? props.threadStore.getCmdCardCols()
        : 3),
      set: (/** @type {number} */ value) => {
        if (typeof props.threadStore.setCmdCardCols === 'function') {
          props.threadStore.setCmdCardCols(value);
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
      threadStore: props.threadStore,
      workspaceRef,
    });

    const threads = computed(() => props.threadStore.getThreadsByMode(
      modeKey.value,
      resolveProjectViewCwd(props.projectStore, props.windowCwd),
    ));
    const selectedThreadId = computed({
      get: () => resolveVisibleSelectedThreadId(props.threadStore, modeKey.value, threads.value),
      set: (/** @type {string} */ value) => {
        if (isCmd.value) {
          props.threadStore.saveActiveCmdThread(value || '');
        } else {
          props.threadStore.saveActiveThread(value || '');
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
    onBeforeUnmount(() => {
      pathChoiceController.cancelPathChoice();
      disconnectContainerObserver();
    });
    watch(selectedThreadId, () => { isPreviewDirty.value = false; });

    const sendBlockedNoticesByThread = ref(new Map());
    const sendHoldNoticesByThread = ref(new Map());
    const activeRawStatus = computed(() => (props.threadStore.getThreadStatus(selectedThreadId.value) || '').toString());
    const activeStatus = computed(() => normalizeStatus(activeRawStatus.value));
    const activeThreadSendBlocked = computed(() => {
      const threadId = (selectedThreadId.value || '').toString().trim();
      const storeBlocked = typeof props.threadStore.isThreadSendBlocked === 'function'
        ? props.threadStore.isThreadSendBlocked(threadId)
        : Boolean(threadId && props.threadStore?.state?.sendBlockedNoticesByThread?.[threadId]);
      const storeHeld = Boolean(threadId && props.threadStore?.state?.sendHoldNoticesByThread?.[threadId]);
      return isThreadErrorStatus(activeRawStatus.value)
        || Boolean(threadId && (sendBlockedNoticesByThread.value.has(threadId) || sendHoldNoticesByThread.value.has(threadId) || storeBlocked || storeHeld));
    });
    const threadStatus = useThreadStatus(props, selectedThreadId, activeStatus, pathChoiceController.showPathChoiceModal);
    const activeThread = computed(() => threads.value.find((/** @type {any} */ item) => item.id === selectedThreadId.value) || null);
    const activeProjectCwd = computed(() => resolveProjectViewCwd(props.projectStore, props.windowCwd));
    const chatThreadOptions = computed(() => {
      if (isCmd.value) return [];
      return threads.value;
    });
    const showArchivedThreadList = ref(false);

    const activeTimeline = computed(() => props.threadStore.getThreadTimeline(selectedThreadId.value));
    const chatEmptyText = computed(() => {
      const name = (activeThread.value?.name || '').toString().trim();
      const items = Array.isArray(activeTimeline.value) ? activeTimeline.value : [];
      if (name === 'AI 设计流程' && items.length === 0) return '我们应该设计点什么？';
      return '暂无消息，先发送一句话试试。';
    });
    const activeThreadDiffText = computed(() => props.threadStore.getThreadDiff(selectedThreadId.value));

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
      threadStore: props.threadStore,
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

    // 注入 scroll 保护：applyRuntimeSnapshot 前后保存/恢复 scrollTop
    if (typeof props.threadStore.setScrollGuard === 'function') {
      props.threadStore.setScrollGuard(saveScrollPosition, restoreScrollPosition);
    }

    const threadCards = useThreadCards(props, {
      threads, chatThreadOptions, selectedThreadId, showArchivedThreadList, activeTimeline,
      isCmd, layoutMode, timelinePreview, diffPreview,
      getThreadStatusHeader: threadStatus.getThreadStatusHeader,
      isThreadInterruptible: threadStatus.isThreadInterruptible,
    });

    const copyThreadInfo = createPageCopyThreadInfo(selectedThreadId, activeProjectCwd, threadCards, activeThread, activeStatus, useClaudeProvider, props);
    const forkPage = createPageForkThread(props, { composer, selectedThreadId, activeThread, isCmd, emit: typeof setupCtx?.emit === 'function' ? setupCtx.emit : () => {} });
    const tokenLevelByThreadId = createPageTokenLevels(props, threads);

    bindPageThreadSelection(props, {
      selectedThreadId, pendingFileRefFocus, focusedDiffPath, focusedDiffLine, fallbackDiffText,
      fallbackMediaPreview, fallbackMarkdownPreview, scheduleScrollToBottom, resetScrollState,
    });

    const selectThread = (threadId) => selectThreadInPage(selectedThreadId, props.threadStore, threadId);
    const inlineRename = useInlineRename(props, threadCards.visibleChatThreadCards, selectThread);

    const threadActions = createPageThreadActions(props, {
      selectedThreadId, modeKey, isCmd, composer, layoutMode, cmdCardCols,
      compacting: threadStatus.compacting, isThreadInterruptible: threadStatus.isThreadInterruptible,
      beginInlineRename: inlineRename.beginInlineRename, scheduleScrollToBottom,
      showArchivedThreadList, providerPreferenceReady, providerPreferenceError, sendBlockedNoticesByThread, sendHoldNoticesByThread,
    });
    const multiAgentLaunch = useMultiAgentLaunch({
      threadStore: props.threadStore,
      projectStore: props.projectStore,
      selectedThreadId,
      composer,
      resolveCwd: (projectStore) => resolveProjectActionCwd(projectStore, props.windowCwd),
      scheduleScrollToBottom,
    });
    const originalSend = threadActions.send;
    threadActions.send = async () => {
      try {
        if (!isCmd.value && await multiAgentLaunch.maybeLaunchFromComposer()) return;
      } catch (error) {
        threadActions.sendFailureNotice.value = formatMultiAgentLaunchFailure(error);
        throw error;
      }
      return originalSend();
    };

    const threadConfigController = createThreadConfigController({ threadStore: props.threadStore, threadActions, selectedThreadId, isCmd });

    const keyboardShortcuts = useKeyboardShortcuts({
      selectedThreadId,
      canInterrupt: threadStatus.canInterrupt,
      isStatusTimerModalPaused: threadStatus.isStatusTimerModalPaused,
      stopSelected: threadActions.stopSelected,
    });

    const onPreviewDirtyChange = (nextDirty) => setPreviewDirtyFlag(isPreviewDirty, nextDirty);
    const confirmAbandonDirtyPreview = (meta) => confirmAbandonDirtyPreviewState(isPreviewDirty, meta);
    const fileRefPreview = createPageFileRefPreview(props, {
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
      threadCards, threadStatus, threadActions, inlineRename, copyThreadInfo, fileRefPreview, batchDeleteStaleThreads: props.threadStore.batchDeleteStaleThreads,
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
      // Phase 2 fork-draft 靠这里暴露给模板，不进契约表
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
  },
  template,
};
