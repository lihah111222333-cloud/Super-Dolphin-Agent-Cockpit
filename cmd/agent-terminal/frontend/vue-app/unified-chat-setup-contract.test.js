// @ts-nocheck
import { beforeEach, describe, expect, it, vi } from 'vitest';

const composerStoreMock = vi.hoisted(() => ({
  state: {
    text: '',
    attachments: [],
  },
  attachByPaths: vi.fn(() => 0),
  clearComposer: vi.fn(),
}));

vi.mock('../lib/vue.esm-browser.prod.js', async () => {
  const actual = await vi.importActual('../lib/vue.esm-browser.prod.js');
  return {
    ...actual,
    onMounted: () => {},
    onBeforeUnmount: () => {},
  };
});

import { reactive, ref } from '../lib/vue.esm-browser.prod.js';

vi.mock('./stores/composer.js', () => ({
  useComposerStore: () => composerStoreMock,
}));

vi.mock('./services/api.js', () => ({
  callAPI: vi.fn(async () => ({})),
  copyTextToClipboard: vi.fn(async () => true),
  onFilesDropped: vi.fn(() => () => {}),
  resolveThreadIdentity: vi.fn(async () => ({})),
}));

vi.mock('./services/log.js', () => ({
  logDebug: vi.fn(),
  logInfo: vi.fn(),
  logWarn: vi.fn(),
}));

vi.mock('./composables/useAutoScroll.js', () => ({
  useAutoScroll: () => ({
    scheduleScrollToBottom: vi.fn(),
  }),
}));

import { UnifiedChatPage } from './pages/UnifiedChatPage.js';

beforeEach(() => {
  composerStoreMock.state.text = '';
  composerStoreMock.state.attachments = [];
  composerStoreMock.attachByPaths.mockReset();
  composerStoreMock.attachByPaths.mockImplementation(() => 0);
  composerStoreMock.clearComposer.mockReset();
  composerStoreMock.clearComposer.mockImplementation(() => {
    composerStoreMock.state.text = '';
    composerStoreMock.state.attachments = [];
  });

  globalThis.window = {
    ...(globalThis.window || {}),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    setTimeout: vi.fn(() => 1),
    clearTimeout: vi.fn(),
    setInterval: vi.fn(() => 1),
    clearInterval: vi.fn(),
    alert: vi.fn(),
  };
  globalThis.document = {
    ...(globalThis.document || {}),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    querySelector: vi.fn(() => null),
    activeElement: null,
  };
});

function makeProjectStore() {
  return {
    state: reactive({ active: '.', showModal: false, projects: ['.'] }),
    projectOptions: { value: [] },
    setActive: () => {},
  };
}

function makeThreadStore() {
  const currentThreadId = ref('thread-active');
  const statuses = reactive({ 'thread-active': 'idle' });
  const statusHeaders = reactive({ 'thread-active': '等待指示' });
  const timelinesByThread = reactive({ 'thread-active': [] });
  return {
    state: reactive({
      pinnedThreadAtById: {},
      archivedThreadAtById: {},
      agentRuntimeById: {},
      diffTextByThread: {},
      skillRevision: 0,
    }),
    getLayout: () => 'focus',
    setLayout: () => {},
    getCmdCardCols: () => 3,
    setCmdCardCols: () => {},
    getSplitRatio: () => 60,
    setSplitRatio: () => {},
    getThreadRailWidth: () => 232,
    setThreadRailWidth: () => {},
    getCurrentThreadId: () => currentThreadId.value,
    saveActiveThread: (value) => { currentThreadId.value = value || ''; },
    saveActiveCmdThread: (value) => { currentThreadId.value = value || ''; },
    getThreadsByMode: () => [{ id: 'thread-active', name: 'Active' }],
    displayName: (thread) => thread.name,
    getThreadStatus: (threadId) => statuses[threadId] || 'idle',
    getThreadStatusHeader: (threadId) => statusHeaders[threadId] || '等待指示',
    getThreadInterruptible: () => false,
    getThreadPinnedAt: () => 0,
    getThreadArchivedAt: () => 0,
    getThreadTimeline: (threadId) => timelinesByThread[threadId] || [],
    loadMessages: async () => ({}),
    getThreadConfig: vi.fn(async () => ({
      threadId: currentThreadId.value,
      provider: 'codex',
      supportsThreadOverride: true,
      override: { model: '', effort: '' },
      effective: { model: 'gpt-5.4', effort: 'xhigh' },
    })),
    setThreadConfig: vi.fn(async () => ({})),
    stopThread: vi.fn(async () => ({ confirmed: true, settled: true, mode: 'interrupt_confirmed' })),
    startThread: vi.fn(async () => 'thread-active'),
    sendMessage: vi.fn(async () => ({})),

    getThreadDiff: () => '',
    getThreadStatusDetails: () => '',
    getThreadTokenUsage: () => null,
    getThreadCompacting: () => false,
    getThreadCompactResult: () => null,
    getThreadCompactSuccessCount: () => 0,
    getThreadActivityStats: () => ({}),
    getThreadAlerts: () => [],
  };
}

describe('UnifiedChatPage.setup public contract', () => {
  it('keeps the current public return surface stable', () => {
    const vm = UnifiedChatPage.setup({ threadStore: makeThreadStore(), projectStore: makeProjectStore(), mode: 'chat' });

    expect(Object.keys(vm)).toEqual([
      'composer', 'isCmd', 'threads', 'selectedThreadId', 'activeThread', 'chatThreadOptions',
      'showArchivedThreadList', 'chatActiveThreadCards', 'chatArchivedThreadCards', 'visibleChatThreadCards',
      'activeChatThreadCount', 'archivedChatThreadCount', 'activeTimeline', 'activeDiffText', 'activeMediaPreview',
      'activeMarkdownPreview', 'activeDiffFocusFile', 'activeDiffFocusLine', 'activeStatus', 'activeStatusHeader',
      'activeStatusDetails', 'activeStatusMeta', 'activeTokenInline', 'activeTokenTooltip', 'compacting',
      'canCompact', 'compactResultText', 'compactResultTone', 'compactSuccessCount', 'canInterrupt', 'recoveringSelected',
      'displayStatusText', 'noActiveThread', 'copyButtonLabel', 'layoutMode', 'cmdCardCols', 'splitRatio',
      'threadRailStyle', 'showOverview', 'showWorkspace', 'chatComposerShellStyle', 'activityPanelRowStyle',
      'activePinnedPlan', 'activeTask', 'taskHandoffVisible', 'taskHandoffLoading', 'taskHandoffError',
      'taskHandoffPreview', 'taskHandoffUpdatedAt', 'taskHandoffUpdatedBy', 'continueTaskBusy',
      'stats', 'recentThreads', 'cmdCards', 'composerSkillMatches',
      'composerEffectiveSelectedSkillNames', 'composerSkillPreviewLoading', 'isComposerSkillSelected',
      'toggleComposerSelectedSkill', 'clearComposerSelectedSkills', 'selectAllComposerSuggestedSkills',
      'composerSkillMatchClass', 'composerSkillMatchReason', 'dragging', 'threadRailDragging',
      'activityPanelDragging', 'composerBarRef', 'presenceAnchorRef', 'workspaceRef', 'activeActivityStats',
      'activeAlerts', 'activeProcessActivity', 'selectThread', 'launchOne', 'send', 'refreshTaskHandoff', 'continueCurrentTask', 'startNewTaskFromHandoff', 'continueCurrentTaskInNewWindow', 'threadConfigUi',
      'updateThreadConfigModel', 'updateThreadConfigEffort', 'saveThreadConfigDraft', 'restoreThreadConfigInherit', 'useClaudeProvider',
       'toggleProviderMode', 'interruptCurrent', 'compactCurrent', 'recoverSelected',
       'setCmdLayout', 'setCmdCardCols', 'copySelectedThreadId', 'timelinePreview', 'diffPreview',
       'showPathChoiceModal', 'pathChoiceOptions', 'pathChoiceTitle', 'pathChoiceTruncated',
       'confirmPathChoice', 'cancelPathChoice',
       'onThreadRailResizeStart', 'onResizeStart', 'onActivityResizeStart', 'stopSelected', 'renameSelected',
       'isAtBottom', 'scheduleScrollToBottom', 'scrollToTop', 'resetScrollState',
       'loadCardHistory', 'renameCard', 'stopCard', 'toggleThreadPin', 'toggleThreadArchive',
       'toggleArchivedThreadList', 'openNewWindow', 'editingThreadId', 'editingAlias', 'renamingThreadId',
       'setRenameInputRef', 'beginInlineRename', 'submitInlineRename', 'handleInlineRenameEnter',
       'cancelInlineRename', 'handleInlineRenameBlur', 'getDisplayName', 'resolveThreadDisplayName',
       'dismissPinnedPlan', 'pinnedPlanCardSpec', 'onTimelineFileRefClick', 'routerPreview',
     ]);
     expect(vm).not.toHaveProperty('resolvePathChoice');

     // PN integration: non-enumerable properties must also be present
     expect(vm).toHaveProperty('isPreviewDirty');
     expect(vm).toHaveProperty('onPreviewDirtyChange');
     expect(vm).toHaveProperty('isStatusTimerModalPaused');
     expect(vm).toHaveProperty('onTimelineCitationClick');
   });
});
