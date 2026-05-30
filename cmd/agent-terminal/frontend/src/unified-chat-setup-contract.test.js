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

function makeProjectStore(overrides = {}) {
  return {
    state: reactive({ active: overrides.active ?? '.', showModal: false, projects: overrides.projects ?? ['.'] }),
    projectOptions: { value: [] },
    setActive: () => {},
  };
}

function makeThreadStore(overrides = {}) {
  const currentThreadId = ref(overrides.currentThreadId ?? 'thread-active');
  const statuses = reactive(overrides.statuses ?? { 'thread-active': 'idle' });
  const statusHeaders = reactive(overrides.statusHeaders ?? { 'thread-active': '等待指示' });
  const timelinesByThread = reactive(overrides.timelinesByThread ?? { 'thread-active': [] });
  const visibleThreads = overrides.visibleThreads ?? [{ id: 'thread-active', name: 'Active' }];
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
    getThreadsByMode: overrides.getThreadsByMode ?? (() => visibleThreads),
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
      effective: { model: 'gpt-5.5', effort: 'xhigh' },
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
      'activeChatThreadCount', 'archivedChatThreadCount', 'activeTimeline', 'chatEmptyText', 'activeDiffText', 'activeMediaPreview',
      'activeMarkdownPreview', 'activeDiffFocusFile', 'activeDiffFocusLine', 'activeStatus', 'activeThreadSendBlocked', 'activeStatusHeader',
      'activeStatusDetails', 'activeStatusMeta', 'activeTokenInline', 'activeTokenTooltip', 'activeTokenLevel', 'activeTokenUsage', 'compacting',
      'canCompact', 'compactResultText', 'compactResultTone', 'compactSuccessCount', 'canInterrupt', 'recoveringSelected', 'sendFailureNotice',
      'displayStatusText', 'noActiveThread', 'copyButtonLabel', 'layoutMode', 'cmdCardCols', 'splitRatio',
      'threadRailStyle', 'showOverview', 'showWorkspace', 'chatComposerShellStyle', 'activityPanelRowStyle',
      'activePinnedPlan',
      'stats', 'recentThreads', 'cmdCards', 'dragging', 'threadRailDragging',
      'activityPanelDragging', 'composerBarRef', 'presenceAnchorRef', 'workspaceRef', 'activeActivityStats',
      'activeAlerts', 'activeProcessActivity', 'selectThread', 'launchOne', 'send', 'threadConfigUi',
      'updateThreadConfigModel', 'updateThreadConfigEffort', 'saveThreadConfigDraft', 'restoreThreadConfigInherit', 'useClaudeProvider',
       'providerPreferenceReady', 'providerPreferenceError', 'toggleProviderMode', 'interruptCurrent', 'compactCurrent', 'recoverSelected',
       'setCmdLayout', 'setCmdCardCols', 'copySelectedThreadId', 'timelinePreview', 'diffPreview',
       'showPathChoiceModal', 'pathChoiceOptions', 'pathChoiceTitle', 'pathChoiceTruncated',
       'confirmPathChoice', 'cancelPathChoice',
       'onThreadRailResizeStart', 'onResizeStart', 'onActivityResizeStart', 'stopSelected', 'renameSelected',
       'isAtBottom', 'scheduleScrollToBottom', 'scrollToTop', 'resetScrollState',
       'loadCardHistory', 'renameCard', 'stopCard', 'toggleThreadPin', 'toggleThreadArchive',
       'toggleArchivedThreadList', 'openNewWindow', 'editingThreadId', 'editingAlias', 'renamingThreadId',
       'setRenameInputRef', 'beginInlineRename', 'submitInlineRename', 'handleInlineRenameEnter',
       'cancelInlineRename', 'handleInlineRenameBlur', 'getDisplayName', 'resolveThreadDisplayName',
       'dismissPinnedPlan', 'deleteStaleThreads', 'pinnedPlanCardSpec', 'onTimelineFileRefClick',
     ]);
     expect(vm).not.toHaveProperty('resolvePathChoice');

      // PN integration: non-enumerable properties must also be present
      expect(vm).toHaveProperty('isPreviewDirty');
      expect(vm).toHaveProperty('onPreviewDirtyChange');
      expect(vm).toHaveProperty('isStatusTimerModalPaused');
      expect(vm).toHaveProperty('onTimelineCitationClick');
    });

  // 回归守护：attachPageNonEnumerableState 是个白名单 helper——两边（defineProperties 与调用方 ctx）
  // 不同步会静默丢字段，导致「@click 按钮点了没反应」之类的难定位 bug（Phase 2 中抱过两次）。
  // 任何新增的非枚举字段都必须在下面列表中，测试会验证它们都被赋了值（不是 undefined）。
  it('locks the non-enumerable bag of setup return (preview, fork-draft handles)', () => {
    const vm = UnifiedChatPage.setup({ threadStore: makeThreadStore(), projectStore: makeProjectStore(), mode: 'chat' });
    const expectedNonEnumerableKeys = [
      // preview / status (已有)
      'onTimelineCitationClick',
      'onPreviewDirtyChange',
      'isPreviewDirty',
      'isStatusTimerModalPaused',
      // Phase 2 fork-draft (新增)
      'forkSubmitting',
      'forkError',
      'submitForkThread',
      'openForkDraftFromUI',
      'forkSourceThreadName',
      'forkAvailableSharedFiles',
      'tokenLevelByThreadId',
    ];
    for (const key of expectedNonEnumerableKeys) {
      // 存在 + 不是 undefined——后者抓「接线遗漏」场景（defineProperty 注册了但调用方不传）
      expect(vm, `vm should have non-enumerable key "${key}"`).toHaveProperty(key);
      expect(vm[key], `vm.${key} should not be undefined (white-list mismatch?)`).not.toBeUndefined();
    }
  });

  it('clears the visible selection when the stored active thread is outside the current project', () => {
    const vm = UnifiedChatPage.setup({
      mode: 'chat',
      projectStore: makeProjectStore({ active: '/repo-b' }),
      threadStore: makeThreadStore({
        currentThreadId: 'thread-a',
        visibleThreads: [{ id: 'thread-b', name: 'Repo B' }],
        timelinesByThread: {
          'thread-a': [{ id: 'old-a', kind: 'assistant', text: 'old repo output' }],
          'thread-b': [{ id: 'new-b', kind: 'assistant', text: 'new repo output' }],
        },
      }),
    });

    expect(vm.threads.value.map((thread) => thread.id)).toEqual(['thread-b']);
    expect(vm.selectedThreadId.value).toBe('');
    expect(vm.activeThread.value).toBeNull();
    expect(vm.activeTimeline.value).toEqual([]);
  });

  it('uses the window cwd as the thread list scope when the active project is dot', () => {
    const getThreadsByMode = vi.fn(() => [{ id: 'thread-root', name: 'Root' }]);
    const vm = UnifiedChatPage.setup({
      mode: 'chat',
      windowCwd: '/repo-root',
      projectStore: makeProjectStore({ active: '.' }),
      threadStore: makeThreadStore({ getThreadsByMode }),
    });

    expect(vm.threads.value.map((thread) => thread.id)).toEqual(['thread-root']);
    expect(getThreadsByMode).toHaveBeenCalledWith('chat', '/repo-root');
  });
});
