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
import { ActivityPanel } from '../components/ActivityPanel.js';
import { PathChoiceModal } from '../components/PathChoiceModal.js';
import { normalizeStatus } from '../services/status.js';
import { logInfo } from '../services/log.js';
import { observeContainerWidth, disconnectContainerObserver } from '../services/pretext-layout.js';
import { useComposerStore } from '../stores/composer.js';
import { useProviderMode } from '../composables/useProviderMode.js';
import { useAutoScroll } from '../composables/useAutoScroll.js';
import { useResizePanels } from '../composables/useResizePanels.js';
import { useSkillPreview } from '../composables/useSkillPreview.js';
import { useDiffPreview } from '../composables/useDiffPreview.js';
import { useThreadStatus } from '../composables/useThreadStatus.js';
import { useThreadCards } from '../composables/useThreadCards.js';
import { useThreadSelection } from '../composables/useThreadSelection.js';
import { useInlineRename } from '../composables/useInlineRename.js';
import { useThreadActions } from '../composables/useThreadActions.js';
import { useKeyboardShortcuts } from '../composables/useKeyboardShortcuts.js';
import { useFileRefPreview } from '../composables/useFileRefPreview.js';
import { useCopyThreadInfo } from '../composables/useCopyThreadInfo.js';
import { useFileDrop } from '../composables/useFileDrop.js';
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
    logInfo('ui', 'chat.select.same_card.refresh', { thread_id: nextThreadId });
    threadStore.loadMessages(nextThreadId, 300);
    return;
  }
  selectedThreadId.value = nextThreadId;
}

function handlePageTimelineCitation(payload, fileRefPreview, threads, selectedThreadId, toggleComposerSelectedSkill, composer, scheduleScrollToBottom) {
  handleTimelineCitationClick({
    payload,
    fileRefPreview,
    threads,
    selectThread: (threadId) => { selectedThreadId.value = threadId; },
    toggleComposerSelectedSkill,
    composer,
    scheduleScrollToBottom,
    logInfo,
  });
}


/**
 * @typedef {'force' | 'explicit' | 'trigger'} SkillMatchType
 */

/**
 * @typedef {Object} SkillPreviewMatch
 * @property {string} name
 * @property {SkillMatchType} matchedBy
 * @property {string[]} matchedTerms
 */

/**
 * @typedef {Object} SkillPreviewQueuedRequest
 * @property {number} requestSeq
 * @property {string} threadId
 * @property {string} text
 */

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
  },
  /**
   * @param {{
   *  projectStore: any,
   *  threadStore: any,
   *  mode?: string,
   * }} props
   */
  setup(props) {
    const composer = useComposerStore();
    const composerBarRef = ref(null);
    const presenceAnchorRef = ref(null);
    const workspaceRef = ref(null);




    const isCmd = computed(() => props.mode === 'cmd');
    const modeKey = computed(() => (isCmd.value ? 'cmd' : 'chat'));
    const showWorkspace = computed(() => true);
    const skillRevision = computed(() => Number(props.threadStore.state.skillRevision || 0));

    const { useClaudeProvider, loadProviderPreference, toggleProviderMode } = useProviderMode();

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
      props.projectStore?.state?.active || '.',
    ));
    const selectedThreadId = computed({
      get: () => props.threadStore.getCurrentThreadId(modeKey.value) || '',
      set: (/** @type {string} */ value) => {
        if (isCmd.value) {
          props.threadStore.saveActiveCmdThread(value || '');
        } else {
          props.threadStore.saveActiveThread(value || '');
        }
      },
    });

    const pathChoiceController = createPathChoiceController(selectedThreadId);
    const isPreviewDirty = ref(false);
    onBeforeUnmount(() => {
      pathChoiceController.cancelPathChoice();
      disconnectContainerObserver();
    });
    watch(selectedThreadId, () => { isPreviewDirty.value = false; });
    const activeStatus = computed(() => normalizeStatus(props.threadStore.getThreadStatus(selectedThreadId.value)));
    const threadStatus = useThreadStatus(props, selectedThreadId, activeStatus, pathChoiceController.showPathChoiceModal);
    const {
      composerSkillMatches,
      composerEffectiveSelectedSkillNames,
      composerSkillPreviewLoading,
      isComposerSkillSelected,
      toggleComposerSelectedSkill,
      clearComposerSelectedSkills,
      resetSelectedComposerSkills,
      selectAllComposerSuggestedSkills,
      composerSkillMatchClass,
      composerSkillMatchReason,
      resolveComposerSkillSelectionForSend,
    } = useSkillPreview({
      composer,
      selectedThreadId,
      skillRevision,
    });
    const activeThread = computed(() => threads.value.find((/** @type {any} */ item) => item.id === selectedThreadId.value) || null);
    const activeProjectCwd = computed(() => (props.projectStore?.state?.active || '').toString().trim());
    const chatThreadOptions = computed(() => {
      if (isCmd.value) return [];
      return threads.value;
    });
    const showArchivedThreadList = ref(false);

    const activeTimeline = computed(() => props.threadStore.getThreadTimeline(selectedThreadId.value));
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
      threads,
      chatThreadOptions,
      selectedThreadId,

      showArchivedThreadList,
      activeTimeline,
      isCmd,
      layoutMode,
      timelinePreview,
      diffPreview,
      getThreadStatusHeader: threadStatus.getThreadStatusHeader,
      isThreadInterruptible: threadStatus.isThreadInterruptible,
    });

    const copyThreadInfo = useCopyThreadInfo({
      selectedThreadId,
      activeRuntime: threadCards.activeRuntime,
      activeThread,
      activeStatus,

      useClaudeProvider,
      activeProjectCwd,
      threadStore: props.threadStore,
    });

    const threadSelectionArgs = /** @type {any} */ ({
      selectedThreadId,
      threadStore: props.threadStore,
      pendingFileRefFocus,
      focusedDiffPath,
      focusedDiffLine,
      fallbackDiffText,
      fallbackMediaPreview,
      fallbackMarkdownPreview,
      scheduleScrollToBottom,
      resetScrollState,
    });
    useThreadSelection(threadSelectionArgs);

    const selectThread = (threadId) => selectThreadInPage(selectedThreadId, props.threadStore, threadId);
    const inlineRename = useInlineRename(props, threadCards.visibleChatThreadCards, selectThread);

    const threadActions = useThreadActions(props, {
      selectedThreadId,
      modeKey,
      isCmd,
      composer,
      layoutMode,
      cmdCardCols,
      compacting: threadStatus.compacting,
      isThreadInterruptible: threadStatus.isThreadInterruptible,
      beginInlineRename: inlineRename.beginInlineRename,
      scheduleScrollToBottom,
      resolveComposerSkillSelectionForSend,
      resetSelectedComposerSkills,
      showArchivedThreadList,
    });

    const threadConfigController = createThreadConfigController({ threadStore: props.threadStore, threadActions, selectedThreadId, isCmd });

    const keyboardShortcuts = useKeyboardShortcuts({
      selectedThreadId,
      canInterrupt: threadStatus.canInterrupt,
      isStatusTimerModalPaused: threadStatus.isStatusTimerModalPaused,
      stopSelected: threadActions.stopSelected,
    });

    function onPreviewDirtyChange(nextDirty) {
      isPreviewDirty.value = Boolean(nextDirty);
    }

    function confirmAbandonDirtyPreview(meta) {
      if (!isPreviewDirty.value) return true;
      if (typeof window === 'undefined' || typeof window.confirm !== 'function') return true;
      const target = meta?.rawPath ? ` (切换到 ${meta.rawPath})` : '';
      const confirmed = window.confirm(`当前文件有未保存的修改，是否放弃？${target}`);
      if (confirmed) isPreviewDirty.value = false;
      return confirmed;
    }

    const fileRefPreview = useFileRefPreview(props, {
      selectedThreadId,
      activeTimeline,
      activeThreadDiffText,
      focusedDiffPath,
      focusedDiffLine,
      fallbackDiffText,
      fallbackMediaPreview,
      fallbackMarkdownPreview,
      requestPathChoice: pathChoiceController.requestPathChoice,
      confirmAbandonDirtyPreview,
    });
    const onTimelineCitationClick = (payload) => handlePageTimelineCitation(payload, fileRefPreview, threads.value, selectedThreadId, toggleComposerSelectedSkill, composer, scheduleScrollToBottom);

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
      threadCards, threadStatus, threadActions, inlineRename, copyThreadInfo, fileRefPreview,
      threadConfigUi: threadConfigController.threadConfigUi,
      updateThreadConfigModel: threadConfigController.updateThreadConfigModel,
      updateThreadConfigEffort: threadConfigController.updateThreadConfigEffort,
      saveThreadConfigDraft: threadConfigController.saveThreadConfigDraft,
      restoreThreadConfigInherit: threadConfigController.restoreThreadConfigInherit,
      keyboardShortcuts,
      useClaudeProvider, toggleProviderMode,
      activeTimeline, activeDiffText, activeMediaPreview, activeMarkdownPreview,
      activeDiffFocusFile, activeDiffFocusLine, activeStatus,
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
      composerSkillMatches, composerEffectiveSelectedSkillNames, composerSkillPreviewLoading,
      isComposerSkillSelected, toggleComposerSelectedSkill, clearComposerSelectedSkills,
      selectAllComposerSuggestedSkills, composerSkillMatchClass, composerSkillMatchReason,
      onThreadRailResizeStart, onResizeStart, onActivityResizeStart,
      selectThread,
      pinnedPlanCardSpec,
      isAtBottom, scheduleScrollToBottom, scrollToTop, resetScrollState,
    });
    Object.defineProperty(exposed, 'onTimelineCitationClick', { value: onTimelineCitationClick, enumerable: false });
    Object.defineProperty(exposed, 'onPreviewDirtyChange', { value: onPreviewDirtyChange, enumerable: false });
    Object.defineProperty(exposed, 'isPreviewDirty', { value: isPreviewDirty, enumerable: false });
    Object.defineProperty(exposed, 'isStatusTimerModalPaused', { value: threadStatus.isStatusTimerModalPaused, enumerable: false });
    return exposed;
  },
  template,
};
