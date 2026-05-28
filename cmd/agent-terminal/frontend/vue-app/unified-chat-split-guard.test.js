// @ts-nocheck
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const hooks = vi.hoisted(() => ({ mounted: [], unmounted: [], droppedCleanup: vi.fn() }));
const autoScroll = vi.hoisted(() => ({ schedule: vi.fn() }));
const provider = vi.hoisted(() => ({ useClaude: false, load: vi.fn(async () => { }), toggle: vi.fn(async () => { }) }));
const diffMock = vi.hoisted(() => ({ last: null, timelinePreview: vi.fn(() => []), diffPreview: vi.fn(() => '') }));
const composer = vi.hoisted(() => {
  const state = { text: '', attachments: [] };
  return {
    state,
    clearComposer: vi.fn(() => { state.text = ''; state.attachments = []; }),
    attachByPaths: vi.fn(() => 0),
  };
});

vi.mock('../lib/vue.esm-browser.prod.js', async () => {
  const actual = await vi.importActual('../lib/vue.esm-browser.prod.js');
  return {
    ...actual,
    onMounted: (fn) => hooks.mounted.push(fn),
    onBeforeUnmount: (fn) => hooks.unmounted.push(fn),
  };
});
vi.mock('./stores/composer.js', () => ({ useComposerStore: () => composer }));
vi.mock('./services/api.js', () => ({
  callAPI: vi.fn(async () => ({})),
  copyTextToClipboard: vi.fn(async () => true),
  onFilesDropped: vi.fn(() => hooks.droppedCleanup),
  resolveThreadIdentity: vi.fn(async () => ({})),
}));
vi.mock('./services/log.js', () => ({ logDebug: vi.fn(), logInfo: vi.fn(), logWarn: vi.fn() }));
vi.mock('./composables/useAutoScroll.js', () => ({ useAutoScroll: () => ({ scheduleScrollToBottom: autoScroll.schedule }) }));
vi.mock('./composables/useProviderMode.js', async () => {
  const { ref } = await vi.importActual('../lib/vue.esm-browser.prod.js');
  return { useProviderMode: () => ({ useClaudeProvider: ref(provider.useClaude), loadProviderPreference: provider.load, toggleProviderMode: provider.toggle }) };
});
vi.mock('./composables/useResizePanels.js', async () => {
  const { ref, computed } = await vi.importActual('../lib/vue.esm-browser.prod.js');
  return {
    useResizePanels: () => ({
      dragging: ref(false), threadRailDragging: ref(false), activityPanelDragging: ref(false), splitRatio: ref(60),
      threadRailStyle: computed(() => ({})), chatComposerShellStyle: computed(() => ({})), activityPanelRowStyle: computed(() => ({})),
      onResizeStart: vi.fn(), onThreadRailResizeStart: vi.fn(), onActivityResizeStart: vi.fn(),
    }),
  };
});
vi.mock('./composables/useDiffPreview.js', async () => {
  const { ref, computed } = await vi.importActual('../lib/vue.esm-browser.prod.js');
  return {
    useDiffPreview: (opts) => {
      const focusedDiffPath = ref(''); const focusedDiffLine = ref(0); const pendingFileRefFocus = ref(null);
      const fallbackDiffText = ref(''); const fallbackMediaPreview = ref(null); const fallbackMarkdownPreview = ref(null);
      diffMock.last = { focusedDiffPath, focusedDiffLine, pendingFileRefFocus, fallbackDiffText, fallbackMediaPreview, fallbackMarkdownPreview };
      return {
        focusedDiffPath, focusedDiffLine, pendingFileRefFocus, fallbackDiffText, fallbackMediaPreview, fallbackMarkdownPreview,
        activeMediaPreview: computed(() => fallbackMediaPreview.value), activeMarkdownPreview: computed(() => fallbackMarkdownPreview.value),
        activeDiffText: computed(() => fallbackDiffText.value || (opts.activeThreadDiffText.value || '')),
        activeDiffFocusFile: computed(() => focusedDiffPath.value), activeDiffFocusLine: computed(() => focusedDiffLine.value),
        timelinePreview: diffMock.timelinePreview, diffPreview: diffMock.diffPreview,
      };
    },
  };
});

import { nextTick, reactive, ref } from '../lib/vue.esm-browser.prod.js';
import { callAPI, copyTextToClipboard } from './services/api.js';
import { UnifiedChatPage } from './pages/UnifiedChatPage.js';

const flush = async () => { await Promise.resolve(); await Promise.resolve(); await nextTick(); };
function displayThreadName(thread) {
  if (!thread) return '';
  return thread.name || thread.id || '';
}

function makeStores(opts = {}) {
  const current = ref(opts.selectedId ?? 'thread-active');
  const threads = reactive(opts.threads ?? [{ id: 'thread-active', name: 'Active' }, { id: 'thread-2', name: 'Second' }]);
  const map = (value) => reactive(value ?? {});
  const status = map(opts.status); const header = map(opts.header); const details = map(opts.details); const interruptible = map(opts.interruptible);
  const timeline = reactive(opts.timeline ?? { 'thread-active': [], 'thread-2': [] }); const diff = map(opts.diff); const token = map(opts.token);
  const activity = map(opts.activity); const alerts = map(opts.alerts); const compacting = map(opts.compacting); const compactResult = map(opts.compactResult); const compactSuccess = map(opts.compactSuccess);
  let layout = opts.layout ?? 'focus'; let cols = opts.cols ?? 3;
  const store = {
    state: reactive({
      pinnedThreadAtById: opts.pinned ?? {},
      archivedThreadAtById: opts.archived ?? {},
      agentRuntimeById: opts.runtime ?? {},
      sendBlockedNoticesByThread: opts.sendBlocked ?? {},
      sendHoldNoticesByThread: opts.sendHold ?? {},
      skillRevision: 0,
      agentMetaById: opts.meta ?? {},
      diffTextByThread: diff,
    }),
    getLayout: vi.fn(() => layout),
    setLayout: vi.fn((_, v) => { layout = v; }),
    getCmdCardCols: vi.fn(() => cols),
    setCmdCardCols: vi.fn((v) => { cols = v; }),
    getSplitRatio: () => 60,
    setSplitRatio() { },
    getThreadRailWidth: () => 232,
    setThreadRailWidth() { },
    getActivityPanelHeight: () => 220,
    setActivityPanelHeight() { },
    getCurrentThreadId: vi.fn(() => current.value),
    saveActiveThread: vi.fn((v) => { current.value = v || ''; }),
    saveActiveCmdThread: vi.fn((v) => { current.value = v || ''; }),
    getThreadsByMode: vi.fn(() => threads),
    displayName: vi.fn(displayThreadName),
    getThreadStatus: vi.fn((id) => status[id] || 'idle'),
    getThreadStatusHeader: vi.fn((id) => header[id] || ''),
    getThreadInterruptible: vi.fn((id) => Boolean(interruptible[id])),
    getThreadTimeline: vi.fn((id) => timeline[id] || []),
    getThreadDiff: vi.fn((id) => diff[id] || ''),
    getThreadStatusDetails: vi.fn((id) => details[id] || ''),
    getThreadTokenUsage: vi.fn((id) => token[id] ?? null),
    getThreadCompacting: vi.fn((id) => Boolean(compacting[id])),
    getThreadCompactResult: vi.fn((id) => compactResult[id] ?? null),
    getThreadCompactSuccessCount: vi.fn((id) => compactSuccess[id] ?? 0),
    getThreadActivityStats: vi.fn((id) => activity[id] ?? {}),
    getThreadAlerts: vi.fn((id) => alerts[id] ?? []),
    startThread: vi.fn(async () => opts.startThreadId ?? 'thread-started'),
    sendMessage: vi.fn(async () => ({})),
    stopThread: vi.fn(async () => ({ confirmed: true, settled: true, mode: 'interrupt_confirmed' })),
    compactThread: vi.fn(async () => ({})),
    forceCompleteThread: vi.fn(async () => ({})),
    recoverThread: vi.fn(async () => ({})),
    loadMessages: vi.fn(async () => ({})),
    renameThread: vi.fn(async (id, name) => { const thread = threads.find((item) => item.id === id); if (thread) thread.name = name; }),
    promptRenameThread: vi.fn(),
    toggleThreadPin: vi.fn((id) => { store.state.pinnedThreadAtById[id] = store.state.pinnedThreadAtById[id] ? 0 : Date.now(); }),
    toggleThreadArchive: vi.fn(async (id) => { store.state.archivedThreadAtById[id] = store.state.archivedThreadAtById[id] ? 0 : Date.now(); }),
  };
  const projectStore = { state: reactive({ active: opts.active ?? '.', showModal: false, projects: opts.projects ?? ['.'] }), projectOptions: { value: [] }, setActive: vi.fn(), quickAdd: vi.fn(), removeProject: vi.fn() };
  return { store, projectStore, current };
}
const createVm = async (opts = {}) => { const { store, projectStore, current } = makeStores(opts); const vm = UnifiedChatPage.setup({ threadStore: store, projectStore, mode: opts.mode ?? 'chat' }); await flush(); return { vm, store, projectStore, current }; };

beforeEach(() => {
  hooks.mounted.length = 0; hooks.unmounted.length = 0; hooks.droppedCleanup.mockClear(); diffMock.last = null; diffMock.timelinePreview.mockReset().mockReturnValue([]); diffMock.diffPreview.mockReset().mockReturnValue(''); autoScroll.schedule.mockClear(); provider.useClaude = false; provider.load.mockClear(); provider.toggle.mockClear(); composer.state.text = ''; composer.state.attachments = []; composer.clearComposer.mockClear(); composer.attachByPaths.mockClear(); vi.mocked(callAPI).mockReset().mockResolvedValue({}); vi.mocked(copyTextToClipboard).mockReset().mockResolvedValue(true);
  globalThis.window = { addEventListener: vi.fn(), removeEventListener: vi.fn(), setTimeout: (...args) => setTimeout(...args), clearTimeout: (id) => clearTimeout(id), setInterval: (...args) => setInterval(...args), clearInterval: (id) => clearInterval(id), alert: vi.fn() };
  globalThis.document = { addEventListener: vi.fn(), removeEventListener: vi.fn(), querySelector: vi.fn(() => null), activeElement: null };
});
afterEach(() => { for (const fn of hooks.unmounted.splice(0)) fn(); hooks.mounted.length = 0; vi.useRealTimers(); });

describe('UnifiedChatPage split guard coverage', () => {
  it('locks setup return contract', async () => {
    const { vm } = await createVm();
    const expected = 'composer,isCmd,threads,selectedThreadId,activeThread,chatThreadOptions,showArchivedThreadList,chatActiveThreadCards,chatArchivedThreadCards,visibleChatThreadCards,activeChatThreadCount,archivedChatThreadCount,activeTimeline,chatEmptyText,activeDiffText,activeMediaPreview,activeMarkdownPreview,activeDiffFocusFile,activeDiffFocusLine,activeStatus,activeThreadSendBlocked,activeStatusHeader,activeStatusDetails,activeStatusMeta,activeTokenInline,activeTokenTooltip,activeTokenLevel,activeTokenUsage,compacting,canCompact,compactResultText,compactResultTone,compactSuccessCount,canInterrupt,recoveringSelected,sendFailureNotice,displayStatusText,noActiveThread,copyButtonLabel,layoutMode,cmdCardCols,splitRatio,threadRailStyle,showOverview,showWorkspace,chatComposerShellStyle,activityPanelRowStyle,activePinnedPlan,stats,recentThreads,cmdCards,dragging,threadRailDragging,activityPanelDragging,composerBarRef,presenceAnchorRef,workspaceRef,activeActivityStats,activeAlerts,activeProcessActivity,selectThread,launchOne,send,scheduleScrollToBottom,scrollToTop,resetScrollState,isAtBottom,useClaudeProvider,providerPreferenceReady,providerPreferenceError,toggleProviderMode,interruptCurrent,compactCurrent,recoverSelected,setCmdLayout,setCmdCardCols,copySelectedThreadId,timelinePreview,diffPreview,showPathChoiceModal,pathChoiceOptions,pathChoiceTitle,pathChoiceTruncated,confirmPathChoice,cancelPathChoice,onThreadRailResizeStart,onResizeStart,onActivityResizeStart,stopSelected,renameSelected,loadCardHistory,renameCard,stopCard,toggleThreadPin,toggleThreadArchive,toggleArchivedThreadList,openNewWindow,editingThreadId,editingAlias,renamingThreadId,setRenameInputRef,beginInlineRename,submitInlineRename,handleInlineRenameEnter,cancelInlineRename,handleInlineRenameBlur,getDisplayName,resolveThreadDisplayName,dismissPinnedPlan,deleteStaleThreads,pinnedPlanCardSpec,onTimelineFileRefClick,threadConfigUi,updateThreadConfigModel,updateThreadConfigEffort,saveThreadConfigDraft,restoreThreadConfigInherit'.split(',').sort();
    expect(Object.keys(vm).sort()).toEqual(expected);
    expect(vm).not.toHaveProperty('resolvePathChoice');
  });

  it('covers format, status, cards and plan derived behavior', async () => {
    vi.useFakeTimers();
    const { vm, projectStore } = await createVm({ layout: 'mix', meta: { 'thread-active': { lastActiveAt: '2026-03-09T10:00:00Z' }, 'thread-2': { lastActiveAt: '2026-03-09T09:00:00Z' } }, status: { 'thread-active': 'running', 'thread-2': 'error' }, header: { 'thread-active': '处理中' }, details: { 'thread-active': '执行命令' }, interruptible: { 'thread-active': true }, token: { 'thread-active': { usedTokens: 1530, contextWindowTokens: 4096, usedPercent: 37.3 } }, compactResult: { 'thread-active': { status: 'success', message: '完成压缩' } }, compactSuccess: { 'thread-active': 2 }, activity: { 'thread-active': { commands: 1 } }, alerts: { 'thread-active': ['warn'] }, timeline: { 'thread-active': [{ id: 'thinking-1', kind: 'thinking', done: false, ts: 'bad-ts' }, { id: 'cmd-1', kind: 'command', status: 'failed', command: 'npm test', output: 'x'.repeat(500), exitCode: 2, ts: '2026-03-09T00:00:00Z' }, { id: 'plan-1', kind: 'plan', done: false, text: '先执行\n```json-render\n{"type":"Text","text":"已解析"}\n```' }] } });
    expect(vm.showOverview.value).toBe(true); expect(vm.stats.value).toEqual({ total: 2, running: 1, thinking: 0, editing: 0, error: 1 }); expect(vm.recentThreads.value.map((item) => item.id)).toEqual(['thread-active', 'thread-2']);
    expect(vm.activeProcessActivity.value[0].output.endsWith('...[truncated]')).toBe(true); expect(vm.activeProcessActivity.value[1].time).toBe(''); expect(vm.activeTokenInline.value).toContain('1.5k / 4.1k'); expect(vm.activeTokenTooltip.value).toContain('Context window:');
    await vi.advanceTimersByTimeAsync(65000); expect(vm.activeStatusHeader.value).toBe('处理中'); expect(vm.displayStatusText.value).toBe('处理中'); expect(vm.activeStatusMeta.value).toContain('1m'); expect(vm.activeStatusMeta.value).toContain('Esc 可中断');
    projectStore.state.showModal = true; await flush(); const paused = vm.activeStatusMeta.value; await vi.advanceTimersByTimeAsync(5000); expect(vm.activeStatusMeta.value).toBe(paused);
    expect(vm.compactResultText.value).toBe('完成压缩'); expect(vm.compactResultTone.value).toBe('success'); expect(vm.compactSuccessCount.value).toBe(2); expect(vm.activeActivityStats.value).toEqual({ commands: 1 }); expect(vm.activeAlerts.value).toEqual(['warn']);
    expect(vm.activePinnedPlan.value.key).toBe('id:plan-1'); const card = vm.pinnedPlanCardSpec(vm.activePinnedPlan.value); expect(card.children.some((item) => item.type === 'Markdown')).toBe(true); expect(card.children.some((item) => item.type === 'Text')).toBe(true); vm.dismissPinnedPlan(); expect(vm.activePinnedPlan.value).toBeNull();
  });

  it('surfaces syncing status headers through the existing toolbar header chain', async () => {
    const { vm } = await createVm({
      status: { 'thread-active': 'syncing' },
      header: { 'thread-active': 'Claude 重启中…' },
      details: { 'thread-active': '正在应用新的 Claude 配置' },
    });

    expect(vm.activeStatus.value).toBe('syncing');
    expect(vm.activeStatusHeader.value).toBe('Claude 重启中…');
    expect(vm.displayStatusText.value).toBe('Claude 重启中…');
    expect(vm.activeStatusMeta.value).toContain('正在应用新的 Claude 配置');
  });

  it('marks raw failed thread statuses as composer send-blocked', async () => {
    const { vm } = await createVm({
      status: { 'thread-active': 'failed' },
    });

    expect(vm.activeStatus.value).toBe('error');
    expect(vm.activeThreadSendBlocked.value).toBe(true);
  });

  it('marks an idle thread as send-blocked after provider cwd disappears', async () => {
    const missingCwd = '/Users/ai/.config/superpowers/worktrees/Super-Dolphin/fix-session-error-leak/.tmp-missing-cwd-agent.8vlje0';
    composer.state.text = '111';
    const { vm, store } = await createVm({
      selectedId: 'thread-active',
      active: '/repo',
      status: { 'thread-active': 'idle' },
      runtime: { 'thread-active': { cwd: missingCwd } },
    });
    const sendError = new Error(`{"message":"[-32098] resolve session: thread \\"missing-cwd-repro-20260525\\": resolve session: auto-resume failed: resolve provider project cwd realpath: lstat ${missingCwd}: no such file or directory"}`);
    store.sendMessage.mockImplementationOnce(async () => {
      store.state.sendBlockedNoticesByThread = { 'thread-active': `发送失败：${sendError.message}` };
      throw sendError;
    });

    await expect(vm.send()).rejects.toThrow('resolve provider project cwd realpath');

    expect(vm.activeStatus.value).toBe('idle');
    expect(vm.activeThreadSendBlocked.value).toBe(true);
    expect(vm.sendFailureNotice.value).toContain(missingCwd);

    store.sendMessage.mockClear();
    composer.state.text = '222';
    await vm.send();
    expect(store.sendMessage).not.toHaveBeenCalled();
  });

  it('keeps an idle thread send-blocked from local send failure state', async () => {
    composer.state.text = 'after failure';
    const { vm, store } = await createVm({
      selectedId: 'thread-active',
      status: { 'thread-active': 'idle' },
      sendBlocked: { 'thread-active': '发送失败：backend boom' },
    });

    expect(vm.activeStatus.value).toBe('idle');
    expect(vm.activeThreadSendBlocked.value).toBe(true);

    await vm.send();

    expect(store.sendMessage).not.toHaveBeenCalled();
    expect(vm.sendFailureNotice.value).toContain('backend boom');
    expect(composer.state.text).toBe('after failure');
  });

  it('keeps an idle thread send-held from local sync hold state', async () => {
    composer.state.text = 'after sync failure';
    const { vm, store } = await createVm({
      selectedId: 'thread-active',
      status: { 'thread-active': 'idle' },
      sendHold: { 'thread-active': '发送状态同步失败：local sync failed。请刷新会话状态后继续。' },
    });

    expect(vm.activeStatus.value).toBe('idle');
    expect(vm.activeThreadSendBlocked.value).toBe(true);

    await vm.send();

    expect(store.sendMessage).not.toHaveBeenCalled();
    expect(vm.sendFailureNotice.value).toContain('local sync failed');
    expect(composer.state.text).toBe('after sync failure');
  });

  it('covers public action methods', async () => {
    composer.state.text = 'hello'; composer.state.attachments = [{ name: 'a.txt' }];
    const { vm, store, current } = await createVm({ selectedId: '', active: '/repo', status: { 'thread-started': 'running' }, interruptible: { 'thread-active': true, 'thread-started': true }, runtime: { 'thread-active': { capabilities: ['context_compact'] }, 'thread-started': { capabilities: ['context_compact'] } } });
    await vm.send(); expect(store.startThread).toHaveBeenCalledWith('/repo', expect.objectContaining({ focusMode: 'chat', deferSpawn: true, launchIntentId: expect.stringMatching(/^launch_/) })); expect(store.sendMessage).toHaveBeenCalledWith('thread-started', 'hello', [{ name: 'a.txt' }], expect.objectContaining({ cwd: '/repo' })); expect(composer.clearComposer).toHaveBeenCalled(); expect(autoScroll.schedule).toHaveBeenCalled();
    await vm.launchOne(); expect(current.value).toBe(''); current.value = 'thread-started'; const confirm = vi.fn(); await vm.interruptCurrent({ threadId: 'thread-started', confirm }); expect(confirm).toHaveBeenCalled(); current.value = 'thread-active'; await vm.compactCurrent(); await vm.recoverSelected(); vm.stopSelected();
    expect(store.compactThread).toHaveBeenCalledWith('thread-active'); expect(store.recoverThread).toHaveBeenCalledWith('thread-active'); expect(store.stopThread).toHaveBeenCalledWith('thread-active', { source: 'ui_stop' });
    vm.loadCardHistory('thread-active'); vm.toggleThreadPin('thread-active'); await vm.toggleThreadArchive('thread-active'); vm.toggleArchivedThreadList(); vm.setCmdLayout('mix'); vm.setCmdCardCols(2); vi.mocked(callAPI).mockImplementation(async (method) => method === 'ui/selectProjectDir' ? { path: '/tmp/child' } : {}); await vm.openNewWindow();
    expect(store.loadMessages).toHaveBeenLastCalledWith('thread-active', 300, { syncRuntime: false }); expect(store.toggleThreadPin).toHaveBeenCalledWith('thread-active'); expect(store.toggleThreadArchive).toHaveBeenCalledWith('thread-active'); expect(vm.showArchivedThreadList.value).toBe(true); expect(store.setLayout).toHaveBeenCalledWith('chat', 'mix'); expect(store.setCmdCardCols).toHaveBeenCalledWith(2); expect(callAPI).toHaveBeenCalledWith('ui/openNewWindow', { cwd: '/tmp/child' }); expect(vm.getDisplayName({ id: 'x', name: 'X' })).toBe('X'); expect(vm.resolveThreadDisplayName('system')).toBe('系统');
  });

  it('covers inline rename, file ref, keyboard and copy states', async () => {
    vi.useFakeTimers();
    const diff = ['diff --git a/src/a.js b/src/a.js', '--- a/src/a.js', '+++ b/src/a.js', '@@ -1,2 +1,3 @@', ' line1', '+added', ' line2'].join('\n');
    const { vm, store } = await createVm({ selectedId: 'thread-active', diff: { 'thread-active': diff }, status: { 'thread-active': 'running' }, header: { 'thread-active': '处理中' }, interruptible: { 'thread-active': true }, runtime: { 'thread-active': { cwd: '/repo', providerThreadId: 'p-1', logPath: '/repo/log.log' } } });
    const input = { focus: vi.fn(), select: vi.fn() }; vm.setRenameInputRef('thread-active', input); vm.beginInlineRename('thread-active'); await nextTick(); expect(input.focus).toHaveBeenCalled(); vm.editingAlias.value = 'Renamed'; vm.handleInlineRenameEnter({ preventDefault: vi.fn() }, 'thread-active'); await flush(); expect(store.renameThread).toHaveBeenCalledWith('thread-active', 'Renamed');
    vm.selectThread('thread-active'); vm.renameSelected(); expect(vm.editingThreadId.value).toBe('thread-active'); vm.cancelInlineRename('thread-active'); vm.renameCard('thread-active'); expect(vm.editingThreadId.value).toBe('thread-active'); vm.handleInlineRenameBlur({ relatedTarget: { dataset: { renameSaveButtonFor: 'thread-active' } } }, 'thread-active'); expect(vm.editingThreadId.value).toBe('thread-active'); vm.cancelInlineRename('thread-active'); expect(vm.editingThreadId.value).toBe('');
    await vm.onTimelineFileRefClick({ path: 'src/a.js', line: 7 }); expect(diffMock.last.focusedDiffPath.value).toBe('src/a.js'); vi.mocked(callAPI).mockRejectedValueOnce(new Error('open failed')); await vm.onTimelineFileRefClick({ path: 'app.log', line: 9 }); expect(diffMock.last.focusedDiffPath.value).toBe('app.log');
    await vm.onTimelineFileRefClick({ path: 'src/a.js', line: 7 }); expect(diffMock.last.focusedDiffPath.value).toBe('src/a.js'); vi.mocked(callAPI).mockRejectedValueOnce(new Error('open failed')); await vm.onTimelineFileRefClick({ path: 'app.log', line: 9 }); expect(diffMock.last.focusedDiffPath.value).toBe('app.log');
    hooks.mounted.forEach((fn) => fn()); expect(provider.load).toHaveBeenCalled(); const handler = globalThis.window.addEventListener.mock.calls.find(([type]) => type === 'keydown')[1]; handler({ key: 'Escape', preventDefault: vi.fn(), target: { tagName: 'TEXTAREA', id: 'chatInput', closest: () => null } }); await flush(); expect(store.stopThread).toHaveBeenCalledWith('thread-active', { source: 'ui_stop' }); store.stopThread.mockClear(); handler({ key: 'Escape', preventDefault: vi.fn(), target: { tagName: 'INPUT', closest: () => null } }); expect(store.stopThread).not.toHaveBeenCalled(); for (const fn of hooks.unmounted.splice(0)) fn(); expect(hooks.droppedCleanup).toHaveBeenCalled();
    vi.mocked(copyTextToClipboard).mockResolvedValueOnce(false); await vm.copySelectedThreadId(); expect(vm.copyButtonLabel.value).toBe('复制失败'); await vi.advanceTimersByTimeAsync(1200); expect(vm.copyButtonLabel.value).toBe('复制信息');
    const { vm: cmdVm, store: cmdStore } = await createVm({ mode: 'cmd', selectedId: 'thread-active' }); cmdVm.renameCard('thread-active'); expect(cmdStore.promptRenameThread).toHaveBeenCalledWith('thread-active');
  });
});
